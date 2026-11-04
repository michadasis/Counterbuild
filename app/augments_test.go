package app

import (
	"fmt"
	"testing"

	"counterbuild/meta"
)

var testAugs = []meta.Augment{
	{ID: 1, Name: "Stats on Stats on Stats!", Tier: 4, Score: 81.4, Pop: 0.15},
	{ID: 2, Name: "Stats!", Tier: 4, Score: 76.7, Pop: 0.13},
	{ID: 3, Name: "King Me", Tier: 4, Score: 58.6, Pop: 0.10},
	{ID: 4, Name: "Hand of Baron", Tier: 4, Score: 87.0, Pop: 0.10},
	{ID: 5, Name: "Tooth Fairy", Tier: 5, Score: 88.3, Pop: 0.005},
	{ID: 6, Name: "", Tier: 5, Score: 99, Pop: 0},
}

func words(x float64, s ...string) OCRLine {
	var l OCRLine
	for i, w := range s {
		l = append(l, OCRWord{Text: w, X: x + float64(i)*0.02, W: 0.015})
	}
	return l
}

func TestMatchCardsWithOCRMistakes(t *testing.T) {
	lines := []OCRLine{
		words(0.05, "Hand", "of", "Baran"), // one wrong letter
		words(0.40, "King", "Me"),
		words(0.75, "Stats", "on", "Stats", "on", "Stats!"),
		words(0.40, "Gain", "gold", "and", "stats"), // description text
	}
	cards := offerFrom(matchCards(lines, testAugs))
	if len(cards) != 3 {
		t.Fatalf("expected 3 cards, got %+v", cards)
	}
	want := []struct{ name, verdict string }{{"Hand of Baron", "Take"}, {"King Me", "Skip"}, {"Stats on Stats on Stats!", "OK"}}
	for i, w := range want {
		if cards[i].Name != w.name || cards[i].Verdict != w.verdict {
			t.Fatalf("card %d: got %+v, want %v", i, cards[i], w)
		}
	}
}

func TestMergedLineAndShortNames(t *testing.T) {
	// OCR may join titles on the same row into one line.
	merged := OCRLine{{Text: "Stats!", X: 0.1, W: 0.03}, {Text: "Tooth", X: 0.45, W: 0.03}, {Text: "Fairy", X: 0.485, W: 0.03},
		{Text: "King", X: 0.8, W: 0.025}, {Text: "Me", X: 0.83, W: 0.015}}
	cards := offerFrom(matchCards([]OCRLine{merged}, testAugs))
	if len(cards) != 3 || cards[0].Name != "Stats!" || cards[1].Name != "Tooth Fairy" || !cards[1].FewGames {
		t.Fatalf("got %+v", cards)
	}
	// A short name read wrong must not match, nor one inside a sentence.
	if got := matchCards([]OCRLine{words(0.1, "Stal5!"), words(0.5, "Gain", "bonus", "stats", "each", "level")}, testAugs); len(got) != 0 {
		t.Fatalf("short names need an exact, standalone read, got %+v", got)
	}
}

func TestNoOfferFromStrayText(t *testing.T) {
	if cards := offerFrom(matchCards([]OCRLine{words(0.3, "King", "Me")}, testAugs)); cards != nil {
		t.Fatalf("one name is not an offer: %+v", cards)
	}
	// Two names in the same spot (a tooltip) are not cards side by side.
	if cards := offerFrom(matchCards([]OCRLine{words(0.3, "King", "Me"), words(0.31, "Hand", "of", "Baron")}, testAugs)); cards != nil {
		t.Fatalf("names in one place are not an offer: %+v", cards)
	}
}

func TestAugmentMode(t *testing.T) {
	cases := []struct {
		setting, mode string
		mapN          int
		want          bool
	}{
		{"auto", "KIWI", 12, true},
		{"auto", "ARAM", 12, false},
		{"always", "ARAM", 12, true},
		{"off", "KIWI", 12, false},
		{"auto", "CLASSIC", 11, false},
	}
	for _, c := range cases {
		if got := augmentMode(c.setting, c.mode, c.mapN); got != c.want {
			t.Fatalf("augmentMode(%q, %q, %d) = %v", c.setting, c.mode, c.mapN, got)
		}
	}
}

func TestCardsWithoutStats(t *testing.T) {
	augs := append([]meta.Augment{{ID: 7, Name: "First-Aid Kit"}, {ID: 8, Name: "ADAPt"}}, testAugs...)
	cards := RateCards([]OCRLine{words(0.1, "King", "Me"), words(0.4, "First-Aid", "Kit"), words(0.7, "Hand", "of", "Baron")}, augs)
	if len(cards) != 3 || !cards[1].NoData || cards[1].Verdict != "?" || cards[0].Verdict != "Skip" || cards[2].Verdict != "Take" {
		t.Fatalf("got %+v", cards)
	}
	// Cards without stats are still recognized, so the choice is shown.
	cards = RateCards([]OCRLine{words(0.1, "First-Aid", "Kit"), words(0.7, "ADAPt")}, augs)
	if len(cards) != 2 || !cards[0].NoData || !cards[1].NoData {
		t.Fatalf("got %+v", cards)
	}
}

func TestTierBeatsScore(t *testing.T) {
	augs := []meta.Augment{
		{ID: 1, Name: "First-Aid Kit", Tier: 1}, // missing from OP.GG's list: its top tiers
		{ID: 2, Name: "Overloaded", Tier: 3, Score: 70},
		{ID: 3, Name: "Tooth Fairy", Tier: 5, Score: 88.3, Pop: 0.005},
		{ID: 4, Name: "ADAPt", Tier: 1},
		{ID: 5, Name: "Upgrade Zhonya's", Tier: 4, Score: 82.5, Pop: 0.15},
	}
	verdicts := func(lines ...OCRLine) string {
		var v []string
		for _, c := range RateCards(lines, augs) {
			v = append(v, c.Name+"="+c.Verdict)
		}
		return fmt.Sprint(v)
	}
	// A higher score in a worse tier doesn't win.
	if got := verdicts(words(0.1, "First-Aid", "Kit"), words(0.4, "Overloaded"), words(0.7, "Tooth", "Fairy")); got != "[First-Aid Kit=Take Overloaded=OK Tooth Fairy=Skip]" {
		t.Fatalf("got %v", got)
	}
	// The real offer from the Soraka game: two top-tier cards and a tier 4 one.
	if got := verdicts(words(0.1, "Upgrade", "Zhonya's"), words(0.4, "First-Aid", "Kit"), words(0.7, "ADAPt")); got != "[Upgrade Zhonya's=Skip First-Aid Kit=Even ADAPt=Even]" {
		t.Fatalf("got %v", got)
	}
}
