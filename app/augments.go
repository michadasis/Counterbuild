package app

import (
	"sort"
	"strings"
	"unicode"

	"counterbuild/engine"
	"counterbuild/meta"
)

// OCRWord is one word read from a screen capture. X and W are its left edge
// and width as fractions of the capture width, so it works at any resolution.
type OCRWord struct {
	Text string
	X, W float64
}

// OCRLine is one line of recognized text.
type OCRLine []OCRWord

// ScreenReader captures the part of the screen where augment cards appear and
// returns the text on it. It returns nil, nil when there is nothing to read
// (for example the game window is not in front).
type ScreenReader func() ([]OCRLine, error)

// augmentMode reports whether the augment cards should be read in this game.
// ARAM Mayhem is played on Howling Abyss (map 12) and, unlike classic ARAM,
// does not report "ARAM" as its game mode.
func augmentMode(setting string, gameMode string, mapNumber int) bool {
	if setting == "off" || mapNumber != 12 {
		return false
	}
	return setting == "always" || !strings.EqualFold(gameMode, "ARAM")
}

// normWords lowercases s and splits it into letter/digit words.
func normWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func similarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	n := max(len(ra), len(rb))
	if n == 0 {
		return 0
	}
	return 1 - float64(levenshtein(ra, rb))/float64(n)
}

// maxWordGap splits a line into separate titles (fraction of capture width).
const maxWordGap = 0.03

type cardMatch struct {
	aug meta.Augment
	x   float64
	sim float64
}

// matchCards finds the augment names among the recognized lines.
//
// Lines are first cut wherever words are far apart, since OCR can join the
// titles of cards side by side into one line. A name only matches a segment
// about its own length, so names inside description sentences don't count.
// Longer names are matched first and claim their words, so "Stats!" can't
// also match inside "Stats on Stats on Stats!". OCR mistakes of a letter or
// two are tolerated, except in short names, which must be read exactly.
func matchCards(lines []OCRLine, augs []meta.Augment) []cardMatch {
	type nameRef struct {
		aug   meta.Augment
		words []string
		flat  string
	}
	var names []nameRef
	for _, a := range augs {
		w := normWords(a.Name)
		if len(w) == 0 {
			continue
		}
		names = append(names, nameRef{a, w, strings.Join(w, " ")})
	}
	sort.SliceStable(names, func(i, j int) bool { return len(names[i].flat) > len(names[j].flat) })

	type normLine struct {
		words []string
		xs    []float64
		used  []bool
	}
	var nls []normLine
	for _, l := range lines {
		var nl normLine
		for i, w := range l {
			if i > 0 && w.X-(l[i-1].X+l[i-1].W) > maxWordGap && len(nl.words) > 0 {
				nl.used = make([]bool, len(nl.words))
				nls = append(nls, nl)
				nl = normLine{}
			}
			for _, part := range normWords(w.Text) {
				nl.words = append(nl.words, part)
				nl.xs = append(nl.xs, w.X)
			}
		}
		if len(nl.words) > 0 {
			nl.used = make([]bool, len(nl.words))
			nls = append(nls, nl)
		}
	}

	var out []cardMatch
	for _, n := range names {
		need := 0.84
		if len(n.flat) <= 6 {
			need = 1 // short names must be read exactly
		}
		best, bl, bs, bk := 0.0, -1, 0, 0
		for li, nl := range nls {
			if len(nl.words) > len(n.words)+1 {
				continue // a sentence, not a title
			}
			if need == 1 && len(nl.words) != len(n.words) {
				continue
			}
			for k := max(1, len(n.words)-1); k <= len(n.words)+1; k++ {
			window:
				for s := 0; s+k <= len(nl.words); s++ {
					for i := s; i < s+k; i++ {
						if nl.used[i] {
							continue window
						}
					}
					if sim := similarity(strings.Join(nl.words[s:s+k], " "), n.flat); sim > best {
						best, bl, bs, bk = sim, li, s, k
					}
				}
			}
		}
		if bl < 0 || best < need {
			continue
		}
		for i := bs; i < bs+bk; i++ {
			nls[bl].used[i] = true
		}
		out = append(out, cardMatch{aug: n.aug, x: nls[bl].xs[bs], sim: best})
	}
	return out
}

// RateCards finds the augment cards in the recognized text and rates them, or
// returns nil when the text doesn't show an augment choice.
func RateCards(lines []OCRLine, augs []meta.Augment) []engine.AugmentCard {
	return offerFrom(matchCards(lines, augs))
}

// offerFrom turns matches into the cards on screen, or nil when the text
// doesn't look like an augment choice (fewer than two names spread across the
// screen).
func offerFrom(found []cardMatch) []engine.AugmentCard {
	if len(found) < 2 {
		return nil
	}
	if len(found) > 3 {
		sort.Slice(found, func(i, j int) bool { return found[i].sim > found[j].sim })
		found = found[:3]
	}
	sort.Slice(found, func(i, j int) bool { return found[i].x < found[j].x })
	if found[len(found)-1].x-found[0].x < 0.12 {
		return nil
	}
	cards := make([]engine.AugmentCard, len(found))
	hi, lo, rated := 0.0, 0.0, 0
	for i, f := range found {
		c := engine.AugmentCard{Name: f.aug.Name, Score: f.aug.Score}
		if f.aug.Score <= 0 {
			c.NoData, c.Verdict = true, "?"
		} else {
			c.FewGames = f.aug.Pop < 0.01
			if rated == 0 {
				hi, lo = c.Score, c.Score
			}
			hi, lo = max(hi, c.Score), min(lo, c.Score)
			rated++
		}
		cards[i] = c
	}
	for i := range cards {
		switch {
		case cards[i].NoData:
		case rated < 2:
			cards[i].Verdict = "OK"
		case hi-lo < 1:
			cards[i].Verdict = "Even"
		case cards[i].Score == hi:
			cards[i].Verdict = "Take"
		case cards[i].Score == lo && hi-lo >= 3:
			cards[i].Verdict = "Skip"
		default:
			cards[i].Verdict = "OK"
		}
	}
	return cards
}
