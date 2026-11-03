package engine

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestSweepAllChampions(t *testing.T) {
	c := testCatalog(t)
	var ids []string
	for id := range c.Champions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	enemies := []pl{{champ: "Garen", team: "CHAOS", level: 9, items: []int{3071}}, {champ: "LeeSin", team: "CHAOS", level: 9, items: []int{6692}},
		{champ: "Orianna", team: "CHAOS", level: 9, items: []int{6655}}, {champ: "Caitlyn", team: "CHAOS", level: 9, items: []int{3031}},
		{champ: "Thresh", team: "CHAOS", level: 8, pos: "UTILITY", items: []int{3190}}}
	var b strings.Builder
	counts := map[string]int{}
	for _, id := range ids {
		g := withStats(c, mkGame(pl{champ: id, team: "ORDER", level: 9}, 1300, 14, enemies...))
		r := c.Recommend(g, Options{})
		if len(r.Picks) != 3 {
			t.Fatalf("%s: %d picks", id, len(r.Picks))
		}
		counts[r.Archetype]++
		fmt.Fprintf(&b, "%-13s %-12s %s | %s | %s\n", id, r.Archetype, r.Picks[0].Name, r.Picks[1].Name, r.Picks[2].Name)
	}
	t.Log("\n" + b.String())
	t.Log(counts)
}
