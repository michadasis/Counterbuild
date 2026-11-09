package app

import (
	"fmt"
	"testing"

	"counterbuild/engine"
	"counterbuild/meta"
)

// testAugs are page data: OP.GG's tier (0 = S, 1 = A, 2 = B) and place in its list.
var testAugs = []meta.Augment{
	{ID: 1, Name: "Stats on Stats on Stats!", Known: true, Tier: 1, Rank: 20},
	{ID: 2, Name: "Stats!", Known: true, Tier: 2, Rank: 45},
	{ID: 3, Name: "King Me", Known: true, Tier: 4, Rank: 120},
	{ID: 4, Name: "Hand of Baron", Known: true, Tier: 0, Rank: 3},
	{ID: 5, Name: "Tooth Fairy", Known: true, Tier: 5, Rank: 170},
	{ID: 6, Name: "", Known: true, Tier: 5, Rank: 180},
}

func words(x float64, s ...string) OCRLine {
	var l OCRLine
	for i, w := range s {
		l = append(l, OCRWord{Text: w, X: x + float64(i)*0.02, W: 0.015})
	}
	return l
}

func verdicts(cards []engine.AugmentCard) string {
	var v []string
	for _, c := range cards {
		v = append(v, c.Name+"="+c.Verdict)
	}
	return fmt.Sprint(v)
}

func TestMatchCardsWithOCRMistakes(t *testing.T) {
	lines := []OCRLine{
		words(0.05, "Hand", "of", "Baran"), // one wrong letter
		words(0.40, "King", "Me"),
		words(0.75, "Stats", "on", "Stats", "on", "Stats!"),
		words(0.40, "Gain", "gold", "and", "stats"), // description text
	}
	cards := offerFrom(matchCards(lines, testAugs))
	if got := verdicts(cards); got != "[Hand of Baron=Take King Me=Reroll Stats on Stats on Stats!=Skip]" {
		t.Fatalf("got %v", got)
	}
}

func TestMergedLineAndShortNames(t *testing.T) {
	// OCR may join titles on the same row into one line.
	merged := OCRLine{{Text: "Stats!", X: 0.1, W: 0.03}, {Text: "Tooth", X: 0.45, W: 0.03}, {Text: "Fairy", X: 0.485, W: 0.03},
		{Text: "King", X: 0.8, W: 0.025}, {Text: "Me", X: 0.83, W: 0.015}}
	cards := offerFrom(matchCards([]OCRLine{merged}, testAugs))
	if got := verdicts(cards); got != "[Stats!=Reroll Tooth Fairy=Reroll King Me=Reroll]" {
		t.Fatalf("got %v", got)
	}
	// A short name read wrong must not match, nor one inside a sentence.
	if got := matchCards([]OCRLine{words(0.1, "Stal5!"), words(0.5, "Gain", "bonus", "stats", "each", "level")}, testAugs); len(got) != 0 {
		t.Fatalf("short names need an exact, standalone read, got %+v", got)
	}
}

// TestTightlySpacedTitles: the card titles are tightly spaced, so OCR can run
// words together ("TankEngine") or break one up.
func TestTightlySpacedTitles(t *testing.T) {
	augs := []meta.Augment{
		{ID: 1, Name: "Tank Engine", Known: true, Tier: 0, Rank: 2},
		{ID: 2, Name: "Tank It Or Leave It", Known: true, Tier: 5, Rank: 178},
		{ID: 3, Name: "Shrink Engine", Known: true, Tier: 4, Rank: 74},
		{ID: 4, Name: "Titan's Resolve", Known: true, Tier: 1, Rank: 13},
	}
	lines := []OCRLine{words(0.1, "TankEngine"), words(0.4, "TankItOr", "LeaveIt"), words(0.7, "Titan's", "Reso", "lve")}
	if got := verdicts(RateCards(lines, augs)); got != "[Tank Engine=Take Tank It Or Leave It=Reroll Titan's Resolve=Skip]" {
		t.Fatalf("got %v", got)
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
	if got := verdicts(cards); got != "[King Me=Reroll First-Aid Kit=? Hand of Baron=Take]" || !cards[1].NoData {
		t.Fatalf("got %v", got)
	}
	// Cards without stats are still recognized, so the choice is shown.
	cards = RateCards([]OCRLine{words(0.1, "First-Aid", "Kit"), words(0.7, "ADAPt")}, augs)
	if len(cards) != 2 || !cards[0].NoData || !cards[1].NoData {
		t.Fatalf("got %+v", cards)
	}
}

// TestRerollAndListOrder: B tier and below say Reroll; of the S and A cards,
// the one highest on OP.GG's list says Take.
func TestRerollAndListOrder(t *testing.T) {
	augs := []meta.Augment{
		{ID: 1, Name: "Armored Impact", Known: true, Tier: 0, Rank: 1},
		{ID: 2, Name: "Goliath", Known: true, Tier: 0, Rank: 6},
		{ID: 3, Name: "Titan's Resolve", Known: true, Tier: 1, Rank: 13},
		{ID: 4, Name: "Multishot", Known: true, Tier: 2, Rank: 46},
		{ID: 5, Name: "Overloaded", Known: true, Tier: 3, Rank: 92},
		{ID: 6, Name: "Upgrade Zhonya's", Known: true, Tier: 5, Rank: 138},
		{ID: 7, Name: "Escape Plan", Known: true, Tier: 1, Rank: 11},
		{ID: 8, Name: "Lonely Warrior", Known: true, Tier: 1, Rank: 26},
		{ID: 9, Name: "Leg Day", Known: true, Tier: 1, Rank: 42},
	}
	cases := []struct {
		names []string
		want  string
	}{
		{[]string{"Titan's Resolve", "Goliath", "Multishot"}, "[Titan's Resolve=Skip Goliath=Take Multishot=Reroll]"},
		{[]string{"Goliath", "Armored Impact", "Titan's Resolve"}, "[Goliath=Skip Armored Impact=Take Titan's Resolve=Skip]"},
		{[]string{"Multishot", "Overloaded", "Upgrade Zhonya's"}, "[Multishot=Reroll Overloaded=Reroll Upgrade Zhonya's=Reroll]"},
		{[]string{"Overloaded", "Titan's Resolve", "Upgrade Zhonya's"}, "[Overloaded=Reroll Titan's Resolve=Take Upgrade Zhonya's=Reroll]"},
		// Three A cards: the bottom, the middle and the top of the A list.
		{[]string{"Leg Day", "Lonely Warrior", "Escape Plan"}, "[Leg Day=Skip Lonely Warrior=Skip Escape Plan=Take]"},
	}
	for _, c := range cases {
		var lines []OCRLine
		for i, n := range c.names {
			lines = append(lines, words(0.1+0.3*float64(i), n))
		}
		if got := verdicts(RateCards(lines, augs)); got != c.want {
			t.Fatalf("got %v, want %v", got, c.want)
		}
	}

	// With only OP.GG's data feed (no list): it has tiers 3 to 5, and
	// augments missing from it are somewhere in S to B. The real offer from a
	// Soraka game:
	feed := meta.MergeAugments([]meta.Augment{{ID: 1318, Name: "Upgrade Zhonya's", Known: true, Tier: 4}},
		map[int]string{1318: "Upgrade Zhonya's", 1037: "First-Aid Kit", 1205: "ADAPt"})
	got := verdicts(RateCards([]OCRLine{words(0.1, "Upgrade", "Zhonya's"), words(0.4, "First-Aid", "Kit"), words(0.7, "ADAPt")}, feed))
	if got != "[Upgrade Zhonya's=Reroll First-Aid Kit=Even ADAPt=Even]" {
		t.Fatalf("got %v", got)
	}
}

func TestDescriptionWordsAreNotCards(t *testing.T) {
	augs := []meta.Augment{
		{ID: 1, Name: "Upgrade Zhonya's", Known: true, Tier: 4},
		{ID: 2, Name: "First-Aid Kit", Top: true},
		{ID: 3, Name: "ADAPt", Top: true},
		{ID: 4, Name: "POWER", Known: true, Tier: 3},
	}
	at := func(x, y float64, s ...string) OCRLine {
		l := words(x, s...)
		for i := range l {
			l[i].Y, l[i].H = y, 0.08
		}
		return l
	}
	// ADAPt was misread, and a description line was split so "Power" stands alone.
	lines := []OCRLine{
		at(0.10, 0.40, "Upgrade", "Zhonya's"), at(0.42, 0.40, "First-Aid", "Kit"), at(0.75, 0.41, "ADAP1"),
		at(0.62, 0.62, "Power"),
	}
	cards := RateCards(lines, augs)
	if len(cards) != 2 || cards[0].Name != "Upgrade Zhonya's" || cards[1].Name != "First-Aid Kit" {
		t.Fatalf("the description word must not become a card: %+v", cards)
	}
}
