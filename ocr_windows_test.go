//go:build windows

package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"testing"

	"counterbuild/app"
	"counterbuild/meta"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// TestWindowsOCR draws three augment titles the way the cards show them (light
// text on a dark background, spread across the screen) and reads them back
// with the Windows OCR engine.
func TestWindowsOCR(t *testing.T) {
	eng, err := startOCR()
	if err != nil {
		t.Skipf("Windows OCR not available: %v", err)
	}
	b, err := os.ReadFile("render/fonts/Go-Bold.ttf")
	if err != nil {
		t.Fatal(err)
	}
	f, err := opentype.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 22, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		t.Fatal(err)
	}
	w, h := 1400, 300
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{18, 20, 30, 255}}, image.Point{}, draw.Src)
	titles := []string{"Hand of Baron", "King Me", "Stats on Stats on Stats!"}
	for i, s := range titles {
		d := &font.Drawer{Dst: img, Src: &image.Uniform{color.RGBA{240, 225, 190, 255}}, Face: face,
			Dot: fixed.P(80+i*450, 140)}
		d.DrawString(s)
	}
	// RGBA -> BGRA
	pix := make([]byte, len(img.Pix))
	for i := 0; i < len(pix); i += 4 {
		pix[i], pix[i+1], pix[i+2], pix[i+3] = img.Pix[i+2], img.Pix[i+1], img.Pix[i], 255
	}
	lines, err := eng.read(pix, w, h)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range lines {
		for _, wd := range l {
			got = append(got, wd.text)
			if wd.x < 0 || int(wd.x) > w {
				t.Fatalf("word %q has bad x %v", wd.text, wd.x)
			}
		}
	}
	t.Logf("OCR words: %q", got)
	joined := ""
	for _, s := range got {
		joined += s + " "
	}
	for _, want := range []string{"Hand", "Baron", "King", "Stats"} {
		found := false
		for _, s := range got {
			if s == want || s == want+"!" {
				found = true
			}
		}
		if !found {
			t.Fatalf("OCR did not read %q in %q", want, joined)
		}
	}
	// Second call reuses the engine.
	if _, err := eng.read(pix, w, h); err != nil {
		t.Fatalf("second read: %v", err)
	}
}

// TestRealMayhemCards reads the titles of a real ARAM Mayhem augment choice
// (cropped from a 1920x1080 game screenshot) and rates them. OP.GG's list for
// that champion had only Upgrade Zhonya's (tier 4); the other two are in its
// top tiers, which the list leaves out.
func TestRealMayhemCards(t *testing.T) {
	eng, err := startOCR()
	if err != nil {
		t.Skipf("Windows OCR not available: %v", err)
	}
	f, err := os.Open("testdata/mayhem_cards.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	// Put the strip back at its place in a 1920-wide capture of the middle of the screen.
	w, h := 1460, 200
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{10, 20, 25, 255}}, image.Point{}, draw.Src)
	draw.Draw(img, src.Bounds().Add(image.Pt(430-230, 70)), src, image.Point{}, draw.Src)
	pix := make([]byte, len(img.Pix))
	for i := 0; i < len(pix); i += 4 {
		pix[i], pix[i+1], pix[i+2], pix[i+3] = img.Pix[i+2], img.Pix[i+1], img.Pix[i], 255
	}
	raw, err := eng.read(pix, w, h)
	if err != nil {
		t.Fatal(err)
	}
	names := map[int]string{1318: "Upgrade Zhonya's", 1037: "First-Aid Kit", 1205: "ADAPt", 2138: "Adaptive Ward", 1404: "Stats!"}
	stats := []meta.Augment{{ID: 1318, Name: "Upgrade Zhonya's", Tier: 4, Score: 82.46, Pop: 0.15}, {ID: 1404, Name: "Stats!", Tier: 4, Score: 76, Pop: 0.1}}
	cards := app.RateCards(toLines(raw, w), meta.MergeAugments(stats, names))
	t.Logf("%+v", cards)
	if len(cards) != 3 || cards[0].Name != "Upgrade Zhonya's" || cards[1].Name != "First-Aid Kit" || cards[2].Name != "ADAPt" {
		t.Fatalf("expected the three cards left to right, got %+v", cards)
	}
	// First-Aid Kit and ADAPt aren't in OP.GG's tier 3-5 list: they are in its top tiers.
	if cards[0].Verdict != "Skip" || cards[1].Verdict != "Even" || cards[2].Verdict != "Even" || cards[1].Tier != 1 {
		t.Fatalf("expected Skip, then two top-tier cards, got %+v", cards)
	}
}
