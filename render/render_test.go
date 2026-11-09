package render

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"counterbuild/engine"
)

type dirIcons struct{ dir string }

func (d dirIcons) load(p string) image.Image {
	f, err := os.Open(filepath.Join(d.dir, p))
	if err != nil {
		return nil
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil
	}
	return img
}
func (d dirIcons) Item(id int) image.Image        { return d.load("item/" + strconv.Itoa(id) + ".png") }
func (d dirIcons) Champion(id string) image.Image { return d.load("champion/" + id + ".png") }

func sampleResult() *engine.Result {
	return &engine.Result{
		Supported: true, ChampID: "Ahri", Champion: "Ahri", Archetype: "Mage", Level: 11, Gold: 1400, GameTime: 1020,
		EnemyPhys: 0.88,
		Enemies: []engine.EnemyView{
			{ChampID: "Zed", Name: "Zed", Threat: .44, Phys: 1, Fed: true, K: 8, D: 1, A: 2},
			{ChampID: "Talon", Name: "Talon", Threat: .18, Phys: 1, K: 4, D: 2, A: 3},
			{ChampID: "Jinx", Name: "Jinx", Threat: .16, Phys: 1, K: 2, D: 3, A: 1},
			{ChampID: "Malphite", Name: "Malphite", Threat: .16, Phys: .2, K: 1, D: 2, A: 4},
			{ChampID: "Leona", Name: "Leona", Threat: .06, Phys: .1, K: 0, D: 4, A: 7},
		},
		Picks: []engine.Pick{
			{ID: 3157, Name: "Zhonya's Hourglass", Cost: 3250, Remaining: 3250, Reasons: []string{"stasis vs Zed, Talon", "vs 88% physical dmg"}},
			{ID: 3118, Name: "Malignance", Cost: 2700, Remaining: 2700, Reasons: []string{"core Mage stats"}},
			{ID: 4645, Name: "Shadowflame", Cost: 3200, Remaining: 3200, Reasons: []string{"pen: avg 41 MR"}},
		},
		Boots:  &engine.Pick{ID: 3047, Name: "Plated Steelcaps", Cost: 1200, Remaining: 900, Reasons: []string{"vs auto-attacks: Jinx, Zed"}},
		BuyNow: &engine.BuyAdvice{ID: 1058, Name: "Needlessly Large Rod", Cost: 1200, Target: "Zhonya's Hourglass", Label: "Buy now"},
	}
}

// TestPreview writes a PNG preview when PREVIEW_OUT is set.
func TestPreview(t *testing.T) {
	out := os.Getenv("PREVIEW_OUT")
	if out == "" {
		t.Skip("set PREVIEW_OUT to write a preview")
	}
	r := NewRenderer()
	res := sampleResult()
	res2 := sampleResult()
	res2.Counter = &engine.BuyAdvice{ID: 3123, Name: "Executioner's Calling", Cost: 800, Short: 150, Target: "Mortal Reminder", Label: "Anti-heal"}
	res2.BuyNow = &engine.BuyAdvice{ID: 1058, Name: "Needlessly Large Rod", Cost: 1200, Short: 300, Target: "Zhonya's Hourglass", Label: "Next"}
	res2.Augments = []engine.AugmentCard{
		{Name: "Tank Engine", Tier: 0, Rank: 2, Verdict: "Take"},
		{Name: "Titan's Resolve", Tier: 1, Rank: 13, Verdict: "Skip"},
		{Name: "Upgrade Zhonya's", Tier: 5, Rank: 138, Verdict: "Reroll"},
	}
	ic := dirIcons{os.Getenv("PREVIEW_ICONS")}
	a := r.Render(res, ic, Style{Scale: 1, Opacity: 1})
	b := r.Render(res2, nil, Style{Scale: 1.25, Opacity: 0.92})
	st := r.Status("Waiting for a game...", Style{Scale: 1})
	W := a.Bounds().Dx() + b.Bounds().Dx() + 60
	H := b.Bounds().Dy() + 120
	bg := image.NewRGBA(image.Rect(0, 0, W, H))
	// A busy, game-like backdrop to judge legibility.
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			bg.Set(x, y, color.RGBA{uint8(40 + (x*3+y)%60), uint8(70 + (x+y*2)%50), uint8(40 + (x*y)%40), 255})
		}
	}
	draw.Draw(bg, a.Bounds().Add(image.Pt(20, 20)), a, image.Point{}, draw.Over)
	draw.Draw(bg, b.Bounds().Add(image.Pt(40+a.Bounds().Dx(), 20)), b, image.Point{}, draw.Over)
	draw.Draw(bg, st.Bounds().Add(image.Pt(20, 40+a.Bounds().Dy())), st, image.Point{}, draw.Over)
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, bg); err != nil {
		t.Fatal(err)
	}
}

func TestRenderScales(t *testing.T) {
	r := NewRenderer()
	res := sampleResult()
	res.Counter = &engine.BuyAdvice{ID: 3123, Name: "Executioner's Calling", Cost: 800, Target: "Mortal Reminder", Label: "Anti-heal"}
	for s := 0.6; s <= 3.0; s += 0.07 {
		img := r.Render(res, nil, Style{Scale: s, Opacity: 0.9})
		if img.Bounds().Dx() < 100 || img.Bounds().Dy() < 100 {
			t.Fatalf("scale %.2f: bad size %v", s, img.Bounds())
		}
		_ = r.Status("Waiting", Style{Scale: s})
		_ = r.AppIcon(int(16 * s))
	}
}
