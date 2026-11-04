// Package render draws the overlay into an RGBA image (premultiplied alpha),
// independent of the windowing system.
package render

import (
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"

	"counterbuild/engine"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed fonts/Go-Regular.ttf
var goRegular []byte

//go:embed fonts/Go-Bold.ttf
var goBold []byte

// Icons supplies item and champion pictures; nil means "not loaded yet".
type Icons interface {
	Item(id int) image.Image
	Champion(id string) image.Image
}

// Style controls size and look.
type Style struct {
	Scale   float64
	Opacity float64
	Hotkey  string
}

var (
	cBG     = color.NRGBA{4, 12, 22, 226}
	cBorder = color.NRGBA{200, 170, 110, 130}
	cGold   = color.NRGBA{200, 170, 110, 255}
	cLight  = color.NRGBA{240, 230, 210, 255}
	cMuted  = color.NRGBA{165, 160, 145, 255}
	cDim    = color.NRGBA{105, 104, 98, 255}
	cPhys   = color.NRGBA{232, 108, 71, 255}
	cMagic  = color.NRGBA{91, 163, 232, 255}
	cTeal   = color.NRGBA{10, 200, 185, 255}
	cWarn   = color.NRGBA{236, 142, 64, 255}
	cRowHi  = color.NRGBA{200, 170, 110, 22}
	cSlot   = color.NRGBA{30, 35, 40, 255}
)

type faceKey struct {
	weight int
	px     int
}

// Renderer caches fonts and faces.
type Renderer struct {
	fonts map[int]*opentype.Font // 0 regular, 1 semibold, 2 bold
	faces map[faceKey]font.Face
}

const (
	wRegular = iota
	wSemi
	wBold
)

// NewRenderer loads Segoe UI when available (Windows) and falls back to the embedded Go fonts.
func NewRenderer(fontDirs ...string) *Renderer {
	r := &Renderer{fonts: map[int]*opentype.Font{}, faces: map[faceKey]font.Face{}}
	try := func(w int, names ...string) {
		for _, d := range fontDirs {
			for _, n := range names {
				b, err := os.ReadFile(filepath.Join(d, n))
				if err != nil {
					continue
				}
				if f, err := opentype.Parse(b); err == nil {
					r.fonts[w] = f
					return
				}
			}
		}
	}
	try(wRegular, "segoeui.ttf", "SegoeUI.ttf", "arial.ttf", "DejaVuSans.ttf")
	try(wSemi, "seguisb.ttf", "segoeuib.ttf", "arialbd.ttf", "DejaVuSans-Bold.ttf")
	try(wBold, "segoeuib.ttf", "seguisb.ttf", "arialbd.ttf", "DejaVuSans-Bold.ttf")
	if r.fonts[wRegular] == nil {
		r.fonts[wRegular], _ = opentype.Parse(goRegular)
	}
	if r.fonts[wSemi] == nil {
		r.fonts[wSemi], _ = opentype.Parse(goBold)
	}
	if r.fonts[wBold] == nil {
		r.fonts[wBold] = r.fonts[wSemi]
	}
	return r
}

func (r *Renderer) face(w int, px float64) font.Face {
	k := faceKey{w, int(math.Round(px * 4))}
	if f, ok := r.faces[k]; ok {
		return f
	}
	f, err := opentype.NewFace(r.fonts[w], &opentype.FaceOptions{Size: float64(k.px) / 4, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil
	}
	r.faces[k] = f
	return f
}

// canvas wraps an image with scale-aware drawing helpers.
type canvas struct {
	img     *image.RGBA
	s       float64
	opacity float64
	r       *Renderer
}

func (c *canvas) px(v float64) int { return int(math.Round(v * c.s)) }

func (c *canvas) col(n color.NRGBA) color.NRGBA {
	n.A = uint8(float64(n.A) * c.opacity)
	return n
}

// blend paints color n with coverage a (0..1) over pixel x,y.
func (c *canvas) blend(x, y int, n color.NRGBA, a float64) {
	if a <= 0 || !image.Pt(x, y).In(c.img.Rect) {
		return
	}
	if a > 1 {
		a = 1
	}
	sa := float64(n.A) / 255 * a
	i := c.img.PixOffset(x, y)
	p := c.img.Pix[i : i+4 : i+4]
	inv := 1 - sa
	p[0] = uint8(float64(n.R)*sa + float64(p[0])*inv + 0.5)
	p[1] = uint8(float64(n.G)*sa + float64(p[1])*inv + 0.5)
	p[2] = uint8(float64(n.B)*sa + float64(p[2])*inv + 0.5)
	p[3] = uint8(255*sa + float64(p[3])*inv + 0.5)
}

func fmax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func fmin(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// rrectSDF is the signed distance from (px,py) to a rounded rectangle.
func rrectSDF(px, py, x0, y0, x1, y1, rad float64) float64 {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	hx, hy := (x1-x0)/2-rad, (y1-y0)/2-rad
	dx := px - cx
	if dx < 0 {
		dx = -dx
	}
	dy := py - cy
	if dy < 0 {
		dy = -dy
	}
	dx -= hx
	dy -= hy
	ox, oy := fmax(dx, 0), fmax(dy, 0)
	return math.Sqrt(ox*ox+oy*oy) + fmin(fmax(dx, dy), 0) - rad
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// fillRRect fills a rounded rectangle given in unscaled units. Interior pixels
// take a fast path; only the anti-aliased edge band evaluates the distance field.
func (c *canvas) fillRRect(x, y, w, h, rad float64, n color.NRGBA) {
	n = c.col(n)
	x0, y0, x1, y1 := x*c.s, y*c.s, (x+w)*c.s, (y+h)*c.s
	rr := rad * c.s
	edgeY := rr + 1.5
	b := c.img.Rect
	ys, ye := maxInt(int(math.Floor(y0)), b.Min.Y), minInt(int(math.Ceil(y1)), b.Max.Y)
	xs, xe := maxInt(int(math.Floor(x0)), b.Min.X), minInt(int(math.Ceil(x1)), b.Max.X)
	sa := float64(n.A) / 255
	pr, pg, pb, inv := float64(n.R)*sa, float64(n.G)*sa, float64(n.B)*sa, 1-sa
	for py := ys; py < ye; py++ {
		fy := float64(py) + 0.5
		nearY := fy < y0+edgeY || fy > y1-edgeY
		for px := xs; px < xe; px++ {
			fx := float64(px) + 0.5
			if !nearY && fx > x0+1.5 && fx < x1-1.5 {
				i := c.img.PixOffset(px, py)
				p := c.img.Pix[i : i+4 : i+4]
				p[0] = uint8(pr + float64(p[0])*inv + 0.5)
				p[1] = uint8(pg + float64(p[1])*inv + 0.5)
				p[2] = uint8(pb + float64(p[2])*inv + 0.5)
				p[3] = uint8(255*sa + float64(p[3])*inv + 0.5)
				continue
			}
			c.blend(px, py, n, clamp01(0.5-rrectSDF(fx, fy, x0, y0, x1, y1, rr)))
		}
	}
}

// strokeRRect draws a rounded rectangle outline of width bw (unscaled),
// visiting only the pixels in the border band.
func (c *canvas) strokeRRect(x, y, w, h, rad, bw float64, n color.NRGBA) {
	n = c.col(n)
	x0, y0, x1, y1 := x*c.s, y*c.s, (x+w)*c.s, (y+h)*c.s
	rr, bb := rad*c.s, fmax(1, bw*c.s)
	band := bb + 1.5
	edgeY := rr + band
	b := c.img.Rect
	ys, ye := maxInt(int(math.Floor(y0)), b.Min.Y), minInt(int(math.Ceil(y1)), b.Max.Y)
	xs, xe := maxInt(int(math.Floor(x0)), b.Min.X), minInt(int(math.Ceil(x1)), b.Max.X)
	for py := ys; py < ye; py++ {
		fy := float64(py) + 0.5
		nearY := fy < y0+edgeY || fy > y1-edgeY
		for px := xs; px < xe; px++ {
			fx := float64(px) + 0.5
			if !nearY && fx > x0+band && fx < x1-band {
				if skip := int(x1-band) - 1; skip > px {
					px = skip
				}
				continue
			}
			d := rrectSDF(fx, fy, x0, y0, x1, y1, rr)
			c.blend(px, py, n, clamp01(0.5-d)-clamp01(0.5-(d+bb)))
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (c *canvas) fillCircle(cx, cy, rad float64, n color.NRGBA) {
	c.fillRRect(cx-rad, cy-rad, rad*2, rad*2, rad, n)
}

// text draws s with its baseline at (x, y) in unscaled units; returns the advance (unscaled).
func (c *canvas) text(s string, x, y float64, w int, size float64, n color.NRGBA, tracking float64) float64 {
	f := c.r.face(w, size*c.s)
	if f == nil {
		return 0
	}
	d := &font.Drawer{Dst: c.img, Src: image.NewUniform(c.col(n)), Face: f}
	if tracking == 0 {
		d.Dot = fixed.Point26_6{X: fixed.I(int(math.Round(x * c.s))), Y: fixed.I(int(math.Round(y * c.s)))}
		d.DrawString(s)
		return float64(d.MeasureString(s).Round()) / c.s
	}
	dx := x * c.s
	for _, ch := range s {
		d.Dot = fixed.Point26_6{X: fixed.Int26_6(dx * 64), Y: fixed.I(int(math.Round(y * c.s)))}
		d.DrawString(string(ch))
		adv, _ := f.GlyphAdvance(ch)
		dx += float64(adv)/64 + tracking*c.s
	}
	return dx/c.s - x
}

func (c *canvas) measure(s string, w int, size float64) float64 {
	f := c.r.face(w, size*c.s)
	if f == nil {
		return 0
	}
	return float64(font.MeasureString(f, s).Round()) / c.s
}

// fit shortens s with an ellipsis so it fits maxW (unscaled).
func (c *canvas) fit(s string, w int, size, maxW float64) string {
	if c.measure(s, w, size) <= maxW {
		return s
	}
	r := []rune(s)
	for len(r) > 1 {
		r = r[:len(r)-1]
		t := strings.TrimRight(string(r), " ,.·") + "…"
		if c.measure(t, w, size) <= maxW {
			return t
		}
	}
	return "…"
}

// icon draws src scaled into a square at (x,y) size sz, with a border.
func (c *canvas) icon(src image.Image, x, y, sz float64, border color.NRGBA, bw float64, label string) {
	c.fillRRect(x, y, sz, sz, 3, cSlot)
	if src != nil {
		dr := image.Rect(c.px(x), c.px(y), c.px(x+sz), c.px(y+sz))
		tmp := image.NewRGBA(dr)
		xdraw.CatmullRom.Scale(tmp, dr, src, src.Bounds(), xdraw.Src, nil)
		// Apply opacity and rounded corners while copying.
		x0, y0, x1, y1 := float64(dr.Min.X), float64(dr.Min.Y), float64(dr.Max.X), float64(dr.Max.Y)
		for py := dr.Min.Y; py < dr.Max.Y; py++ {
			for px := dr.Min.X; px < dr.Max.X; px++ {
				cov := clamp01(0.5 - rrectSDF(float64(px)+0.5, float64(py)+0.5, x0, y0, x1, y1, 3*c.s))
				o := tmp.RGBAAt(px, py)
				if o.A == 0 {
					continue
				}
				n := color.NRGBA{o.R, o.G, o.B, o.A}
				if o.A != 255 {
					n = color.NRGBAModel.Convert(o).(color.NRGBA)
				}
				c.blend(px, py, c.col(n), cov)
			}
		}
	} else if label != "" {
		r := []rune(label)
		if len(r) > 2 {
			r = r[:2]
		}
		t := strings.ToUpper(string(r))
		tw := c.measure(t, wBold, sz*0.36)
		c.text(t, x+(sz-tw)/2, y+sz*0.62, wBold, sz*0.36, cMuted, 0)
	}
	if bw > 0 {
		c.strokeRRect(x, y, sz, sz, 3, bw, border)
	}
}

func gold(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		s = fmt.Sprintf("%d", -n)
	}
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	if n < 0 {
		return "-" + b.String()
	}
	return b.String()
}

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	t = clamp01(t)
	return color.NRGBA{
		uint8(float64(a.R)*(1-t) + float64(b.R)*t),
		uint8(float64(a.G)*(1-t) + float64(b.G)*t),
		uint8(float64(a.B)*(1-t) + float64(b.B)*t),
		uint8(float64(a.A)*(1-t) + float64(b.A)*t),
	}
}

const panelW = 304.0

func (r *Renderer) newCanvas(h float64, st Style) *canvas {
	s := st.Scale
	if s <= 0 {
		s = 1
	}
	op := st.Opacity
	if op <= 0 || op > 1 {
		op = 1
	}
	img := image.NewRGBA(image.Rect(0, 0, int(math.Ceil(panelW*s)), int(math.Ceil(h*s))))
	return &canvas{img: img, s: s, opacity: op, r: r}
}

// Status renders a small one-line panel (loading, errors).
func (r *Renderer) Status(msg string, st Style) *image.RGBA {
	c := r.newCanvas(46, st)
	c.fillRRect(0.5, 0.5, panelW-1, 45, 8, cBG)
	c.strokeRRect(0.5, 0.5, panelW-1, 45, 8, 1, cBorder)
	c.text("COUNTERBUILD", 12, 18, wBold, 10, cGold, 1.2)
	c.text(c.fit(msg, wRegular, 12, panelW-24), 12, 36, wRegular, 12, cMuted, 0)
	return c.img
}

// Render draws the full overlay for a recommendation.
func (r *Renderer) Render(res *engine.Result, ic Icons, st Style) *image.RGBA {
	const pad = 12.0
	inner := panelW - 2*pad

	// Height pass.
	h := pad + 16 + 18 + 26 + 50 + 12 + 16
	h += float64(len(res.Picks)) * 50
	if len(res.Augments) > 0 {
		h += 12 + 16 + float64(len(res.Augments))*24 + 6
	}
	if res.Boots != nil {
		h += 34
	}
	if res.BuyNow != nil {
		h += 46
	}
	if res.Counter != nil {
		h += 46
	}
	h += pad - 4
	c := r.newCanvas(h, st)
	c.fillRRect(0.5, 0.5, panelW-1, h-1, 9, cBG)
	c.strokeRRect(0.5, 0.5, panelW-1, h-1, 9, 1, cBorder)

	y := pad
	// Header: brand + gold.
	c.text("COUNTERBUILD", pad, y+10, wBold, 10, cGold, 1.4)
	gs := gold(res.Gold) + " g"
	gw := c.measure(gs, wSemi, 12)
	c.text(gs, panelW-pad-gw, y+11, wSemi, 12, cLight, 0)
	c.fillCircle(panelW-pad-gw-8, y+7, 4, cGold)
	y += 16
	sub := fmt.Sprintf("%s  ·  %s  ·  Lv %d", res.Champion, res.Archetype, res.Level)
	c.text(c.fit(sub, wRegular, 12, inner), pad, y+12, wRegular, 12, cMuted, 0)
	y += 18

	// Enemy damage split.
	ad := int(math.Round(res.EnemyPhys * 100))
	c.text(fmt.Sprintf("AD %d%%", ad), pad, y+9, wSemi, 10, cPhys, 0)
	apS := fmt.Sprintf("AP %d%%", 100-ad)
	c.text(apS, panelW-pad-c.measure(apS, wSemi, 10), y+9, wSemi, 10, cMagic, 0)
	lbl := "ENEMY DAMAGE"
	c.text(lbl, pad+inner/2-c.measure(lbl, wBold, 9)/2-3, y+9, wBold, 9, cDim, 0.6)
	by := y + 14
	c.fillRRect(pad, by, inner, 5, 2.5, cMagic)
	if res.EnemyPhys > 0.01 {
		c.fillRRect(pad, by, math.Max(5, inner*res.EnemyPhys), 5, 2.5, cPhys)
	}
	y += 26

	// Enemy strip.
	n := len(res.Enemies)
	if n > 0 {
		sz := 30.0
		gap := (inner - float64(n)*sz) / math.Max(1, float64(n-1))
		if n == 1 {
			gap = 0
		}
		for i, e := range res.Enemies {
			x := pad + float64(i)*(sz+gap)
			bc := mix(cPhys, cMagic, 1-e.Phys)
			bw := 1.5
			if e.Fed {
				bc, bw = cGold, 2
			}
			var img image.Image
			if ic != nil {
				img = ic.Champion(e.ChampID)
			}
			c.icon(img, x, y, sz, bc, bw, e.Name)
			if e.Fed {
				c.fillCircle(x+sz-2, y+2, 4.5, cGold)
				c.text("!", x+sz-3.6, y+5.6, wBold, 8, color.NRGBA{20, 16, 8, 255}, 0)
			}
			kda := fmt.Sprintf("%d/%d/%d", e.K, e.D, e.A)
			kw := c.measure(kda, wRegular, 10)
			kc := cMuted
			if e.Fed {
				kc = cGold
			}
			c.text(kda, x+sz/2-kw/2, y+sz+12, wRegular, 10, kc, 0)
		}
	}
	y += 50

	// Augment cards on screen (ARAM Mayhem), shown first while the choice is open.
	if len(res.Augments) > 0 {
		c.fillRRect(pad, y, inner, 1, 0, color.NRGBA{200, 170, 110, 55})
		y += 12
		c.text("AUGMENT PICK", pad, y+9, wBold, 9, cDim, 1.2)
		src := "OP.GG tier"
		c.text(src, panelW-pad-c.measure(src, wRegular, 9.5), y+9, wRegular, 9.5, cDim, 0)
		y += 16
		for _, a := range res.Augments {
			vc := cMuted
			switch a.Verdict {
			case "Take":
				vc = cTeal
				c.fillRRect(pad-6, y-2, inner+12, 22, 5, cRowHi)
			case "Skip":
				vc = cWarn
			}
			c.text(strings.ToUpper(a.Verdict), pad, y+13, wBold, 9.5, vc, 0.8)
			var score string
			switch {
			case a.NoData:
				score = "no data"
			case a.Tier <= 2:
				score = "top tier"
			case a.FewGames:
				score = fmt.Sprintf("tier %d · few games", a.Tier)
			default:
				score = fmt.Sprintf("tier %d · %.1f", a.Tier, a.Score)
			}
			sw := c.measure(score, wSemi, 11)
			c.text(score, panelW-pad-sw, y+13, wSemi, 11, cMuted, 0)
			tx := pad + 42
			c.text(c.fit(a.Name, wSemi, 12.5, panelW-pad-sw-8-tx), tx, y+13, wSemi, 12.5, cLight, 0)
			y += 24
		}
		y += 6
	}

	// Divider + section label.
	c.fillRRect(pad, y, inner, 1, 0, color.NRGBA{200, 170, 110, 55})
	y += 12
	c.text("BUILD NEXT", pad, y+9, wBold, 9, cDim, 1.2)
	if res.MetaSource != "" {
		src := "with " + res.MetaSource + " stats"
		c.text(src, panelW-pad-c.measure(src, wRegular, 9.5), y+9, wRegular, 9.5, cDim, 0)
	}
	y += 16

	for i, p := range res.Picks {
		rowH := 46.0
		if i == 0 {
			c.fillRRect(pad-6, y-3, inner+12, rowH+2, 6, cRowHi)
			c.fillRRect(pad-6, y+6, 2, rowH-16, 1, cGold)
		}
		var img image.Image
		if ic != nil {
			img = ic.Item(p.ID)
		}
		c.icon(img, pad, y, 38, cGold, 1, p.Name)
		tx := pad + 38 + 10
		cost := gold(p.Remaining) + " g"
		cw := c.measure(cost, wSemi, 12)
		costCol := cGold
		if p.Remaining <= res.Gold {
			costCol = cTeal
		}
		c.text(cost, panelW-pad-cw, y+15, wSemi, 12, costCol, 0)
		nameW := panelW - pad - cw - 8 - tx
		c.text(c.fit(p.Name, wSemi, 13.5, nameW), tx, y+15, wSemi, 13.5, cLight, 0)
		why := strings.Join(p.Reasons, "  ·  ")
		c.text(c.fit(why, wRegular, 11, panelW-pad-tx), tx, y+33, wRegular, 11, cMuted, 0)
		y += rowH + 4
	}

	if b := res.Boots; b != nil {
		var img image.Image
		if ic != nil {
			img = ic.Item(b.ID)
		}
		c.icon(img, pad, y, 26, cGold, 1, b.Name)
		tx := pad + 26 + 10
		cost := gold(b.Remaining) + " g"
		cw := c.measure(cost, wSemi, 11)
		c.text(cost, panelW-pad-cw, y+11, wSemi, 11, cGold, 0)
		lead := "Boots  "
		lw := c.text(lead, tx, y+11, wBold, 11, cDim, 0)
		c.text(c.fit(b.Name, wSemi, 12, panelW-pad-cw-8-tx-lw), tx+lw, y+11, wSemi, 12, cLight, 0)
		if len(b.Reasons) > 0 {
			c.text(c.fit(b.Reasons[0], wRegular, 10.5, panelW-pad-tx), tx, y+24, wRegular, 10.5, cMuted, 0)
		}
		y += 34
	}

	box := func(a *engine.BuyAdvice, accent color.NRGBA, title string) {
		c.fillRRect(pad-2, y, inner+4, 40, 6, color.NRGBA{accent.R, accent.G, accent.B, 26})
		c.strokeRRect(pad-2, y, inner+4, 40, 6, 1, color.NRGBA{accent.R, accent.G, accent.B, 110})
		var img image.Image
		if ic != nil {
			img = ic.Item(a.ID)
		}
		c.icon(img, pad+5, y+7, 26, accent, 1, a.Name)
		tx := pad + 5 + 26 + 9
		c.text(title, tx, y+16, wBold, 9, accent, 1.1)
		var right string
		rc := cLight
		switch {
		case a.Note != "":
			right, rc = a.Note, cWarn
		case a.Short > 0:
			right, rc = "need "+gold(a.Short)+" g", cWarn
		default:
			right = gold(a.Cost) + " g"
		}
		rw := c.measure(right, wSemi, 11)
		c.text(right, panelW-pad-4-rw, y+16, wSemi, 11, rc, 0)
		name := a.Name
		if !a.Complete && a.Target != "" && a.Target != a.Name {
			name += "  →  " + a.Target
		}
		c.text(c.fit(name, wSemi, 12, panelW-pad-4-tx), tx, y+32, wSemi, 12, cLight, 0)
		y += 46
	}
	if a := res.BuyNow; a != nil {
		title, acc := "BUY NOW", cTeal
		if a.Short > 0 || a.Note != "" {
			title, acc = "NEXT COMPONENT", cGold
		}
		if a.Complete {
			title = "COMPLETE NOW"
		}
		box(a, acc, title)
	}
	if a := res.Counter; a != nil {
		box(a, cWarn, strings.ToUpper(a.Label)+" NOW")
	}
	return c.img
}

// AppIcon draws the tray / window icon at the given pixel size.
func (r *Renderer) AppIcon(px int) *image.RGBA {
	s := float64(px) / 32
	c := &canvas{img: image.NewRGBA(image.Rect(0, 0, px, px)), s: s, opacity: 1, r: r}
	c.fillRRect(1, 1, 30, 30, 7, color.NRGBA{4, 12, 22, 255})
	c.strokeRRect(1, 1, 30, 30, 7, 2, cGold)
	// Stylized up-arrow: "build up / counter".
	c.fillRRect(14, 10, 4, 14, 1, cGold)
	for i := 0.0; i < 7; i++ {
		c.fillRRect(16-i-1, 8+i, 2*i+2, 1.2, 0, cGold)
	}
	return c.img
}
