package meta

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"counterbuild/engine"
)

// extracted is item popularity pulled out of a stats response.
type extracted struct {
	Pop     map[int]float64
	WR      map[int]float64
	Slot    map[int]float64 // typical build slot (1 = first completed item)
	Entries int
	How     string
}

type entry struct {
	ids   []int
	pick  float64 // pick rate 0..1, or -1
	play  float64 // games, or -1
	wr    float64 // win rate 0..1, or -1
	group string
	order int
}

// Rune groups reuse numbers that collide with item ids (Conqueror is 8010, so is Bloodletter's Curse).
var skipGroup = []string{"rune", "perk", "keystone", "shard", "stat_mod", "statmod"}

func skipped(group string) bool {
	g := strings.ToLower(group)
	for _, s := range skipGroup {
		if strings.Contains(g, s) {
			return true
		}
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(x), "%"), 64)
		return f, err == nil
	}
	return 0, false
}

func rate(v float64) float64 {
	if v > 1 {
		v /= 100
	}
	return v
}

type walker struct {
	cat     *engine.Catalog
	entries []*entry
	orders  map[string]int
}

func (w *walker) itemIDs(arr []any) []int {
	var ids []int
	for _, x := range arr {
		f, ok := toFloat(x)
		if !ok || f != math.Trunc(f) {
			return nil
		}
		id := int(f)
		if id < 1000 || w.cat.Items[id] == nil {
			return nil
		}
		ids = append(ids, id)
	}
	return ids
}

func idKey(k string) bool {
	k = strings.ToLower(k)
	return strings.Contains(k, "id") || strings.Contains(k, "item") || k == "build" || k == "core" || k == "path"
}

func (w *walker) walk(v any, group string) {
	switch x := v.(type) {
	case map[string]any:
		if !skipped(group) {
			var ids []int
			for k, val := range x {
				switch t := val.(type) {
				case []any:
					if idKey(k) {
						ids = append(ids, w.itemIDs(t)...)
					}
				default:
					kl := strings.ToLower(k)
					if kl == "id" || kl == "item_id" || kl == "itemid" {
						if f, ok := toFloat(t); ok && w.cat.Items[int(f)] != nil && f >= 1000 {
							ids = append(ids, int(f))
						}
					}
				}
			}
			if len(ids) > 0 {
				e := &entry{ids: ids, pick: -1, play: -1, wr: -1, group: group, order: w.orders[group]}
				w.orders[group]++
				var wins float64 = -1
				for k, val := range x {
					f, ok := toFloat(val)
					if !ok {
						continue
					}
					kl := strings.ToLower(k)
					switch {
					case strings.Contains(kl, "pick") && (strings.Contains(kl, "rate") || kl == "pick" || kl == "pickrate"):
						e.pick = rate(f)
					case kl == "pr":
						e.pick = rate(f)
					case kl == "play" || kl == "plays" || kl == "games" || kl == "count" || kl == "total" || kl == "matches" || kl == "play_count" || kl == "num_games":
						e.play = f
					case strings.Contains(kl, "win") && strings.Contains(kl, "rate"), kl == "wr":
						e.wr = rate(f)
					case kl == "win" || kl == "wins" || kl == "win_count":
						wins = f
					}
				}
				if e.wr < 0 && wins >= 0 && e.play > 0 {
					e.wr = wins / e.play
				}
				w.entries = append(w.entries, e)
			}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			w.walk(x[k], strings.TrimPrefix(group+"."+k, "."))
		}
	case []any:
		for _, val := range x {
			w.walk(val, group)
		}
	}
}

// buyable maps an item OP.GG lists to the one you buy for it (Diadem of
// Songs -> Whispering Circlet), or leaves it as it is.
func buyable(cat *engine.Catalog, id int) int {
	if b := cat.Buyable(id); b != 0 {
		return b
	}
	return id
}

// relevant items are the ones the engine scores: completed items and upgraded boots.
func relevant(cat *engine.Catalog, id int) bool {
	it := cat.Items[id]
	if it == nil || !it.Purchasable {
		return false
	}
	if it.Boots {
		return id != 1001
	}
	return it.Cost >= 2000
}

// groupWeight says how "core" a section of the response is, from its name.
func groupWeight(group string) float64 {
	g := strings.ToLower(group)
	switch {
	case strings.Contains(g, "start"):
		return 0.3
	case strings.Contains(g, "last") || strings.Contains(g, "late") || strings.Contains(g, "situational") ||
		strings.Contains(g, "fourth") || strings.Contains(g, "fifth") || strings.Contains(g, "sixth") || strings.Contains(g, "option"):
		return 0.65
	}
	return 1
}

// finish turns build entries into per-item popularity. Each section of the
// response is normalized on its own (its most common item = 1), then weighted
// by how core that section is; an item keeps its best section score.
func finish(cat *engine.Catalog, entries []*entry, how string) *extracted {
	pop := map[int]float64{}
	wrSum, wrW := map[int]float64{}, map[int]float64{}
	byGroup := map[string][]*entry{}
	for _, e := range entries {
		byGroup[e.group] = append(byGroup[e.group], e)
	}
	for group, es := range byGroup {
		hasPick, maxPlay := false, 0.0
		for _, e := range es {
			if e.pick >= 0 {
				hasPick = true
			}
			if e.play > maxPlay {
				maxPlay = e.play
			}
		}
		sum := map[int]float64{}
		for _, e := range es {
			var w float64
			switch {
			case hasPick:
				w = math.Max(e.pick, 0)
			case maxPlay > 0:
				w = math.Max(e.play, 0) / maxPlay
			default:
				w = 1 / (1 + 0.5*float64(e.order))
			}
			for _, id := range e.ids {
				id = buyable(cat, id)
				if !relevant(cat, id) {
					continue
				}
				sum[id] += w
				if e.wr >= 0 && w > 0 {
					wrSum[id] += e.wr * w
					wrW[id] += w
				}
			}
		}
		top := 0.0
		for _, v := range sum {
			if v > top {
				top = v
			}
		}
		if top <= 0 {
			continue
		}
		gw := groupWeight(group)
		for id, v := range sum {
			if p := v / top * gw; p > pop[id] {
				pop[id] = p
			}
		}
	}
	out := &extracted{Pop: map[int]float64{}, WR: map[int]float64{}, Entries: len(entries), How: how}
	for id, p := range pop {
		if p < 0.05 {
			continue
		}
		out.Pop[id] = p
		if wrW[id] > 0 {
			out.WR[id] = wrSum[id] / wrW[id]
		}
	}
	return out
}

var reIDArray = regexp.MustCompile(`\[\s*(\d{4,6}(?:\s*,\s*\d{4,6})*)\s*\]`)

// extractText handles responses that are not plain JSON.
func extractText(cat *engine.Catalog, text string) *extracted {
	// JSON embedded in prose or a code block.
	if i, j := strings.Index(text, "{"), strings.LastIndex(text, "}"); i >= 0 && j > i {
		if ex := extractJSON(cat, []byte(text[i:j+1])); ex != nil && len(ex.Pop) > 0 {
			ex.How = "embedded json"
			return ex
		}
	}
	// Bracketed id lists such as [3031, 3046, 3036].
	var entries []*entry
	for n, m := range reIDArray.FindAllStringSubmatchIndex(text, -1) {
		ctx := strings.ToLower(text[maxInt(0, m[0]-60):m[0]])
		if skipped(ctx) {
			continue
		}
		var ids []int
		for _, p := range strings.Split(text[m[2]:m[3]], ",") {
			id, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || cat.Items[id] == nil {
				ids = nil
				break
			}
			ids = append(ids, id)
		}
		if len(ids) > 0 {
			entries = append(entries, &entry{ids: ids, pick: -1, play: -1, wr: -1, group: "text", order: n})
		}
	}
	if ex := finish(cat, entries, "text ids"); len(ex.Pop) > 0 {
		return ex
	}
	// Item names, earlier mention = more prominent.
	lower := strings.ToLower(text)
	type hit struct {
		id  int
		pos int
	}
	var hits []hit
	for id, it := range cat.Items {
		if id >= 10000 || !relevant(cat, id) || len(it.Name) < 4 {
			continue
		}
		if p := strings.Index(lower, strings.ToLower(it.Name)); p >= 0 {
			hits = append(hits, hit{id, p})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	entries = entries[:0]
	for n, h := range hits {
		entries = append(entries, &entry{ids: []int{h.id}, pick: -1, play: -1, wr: -1, group: "names", order: n})
	}
	return finish(cat, entries, "item names")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func extractJSON(cat *engine.Catalog, b []byte) *extracted {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil
	}
	return extractTree(cat, v, "json")
}

// extractTree reads item popularity and build slots from a decoded answer.
func extractTree(cat *engine.Catalog, v any, how string) *extracted {
	w := &walker{cat: cat, orders: map[string]int{}}
	w.walk(v, "")
	ex := finish(cat, w.entries, how)
	ex.Slot = slotsFrom(cat, v)
	return ex
}

// asList returns v as a list of build entries (one entry or a list of them).
func asList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case map[string]any:
		return []any{x}
	}
	return nil
}

func entryIDs(e any) []int {
	m, _ := e.(map[string]any)
	var ids []int
	for _, k := range []string{"ids", "item_ids", "items"} {
		arr, _ := m[k].([]any)
		for _, x := range arr {
			if f, ok := toFloat(x); ok {
				ids = append(ids, int(f))
			}
		}
		if len(ids) > 0 {
			break
		}
	}
	return ids
}

func entryPick(e any) float64 {
	m, _ := e.(map[string]any)
	for _, k := range []string{"pick_rate", "pickrate", "pick"} {
		if f, ok := toFloat(m[k]); ok {
			return rate(f)
		}
	}
	return -1
}

// slotsFrom works out where each item is usually built, from the core build
// order (item 1, 2, 3) and the 4th/5th/6th item lists, weighted by how often
// each option is picked. Only completed items count; boots don't take a slot.
func slotsFrom(cat *engine.Catalog, v any) map[int]float64 {
	root, _ := v.(map[string]any)
	data, _ := root["data"].(map[string]any)
	if data == nil {
		return nil
	}
	sum, wt := map[int]float64{}, map[int]float64{}
	add := func(id int, slot, w float64) {
		sum[id] += slot * w
		wt[id] += w
	}
	// Items that can't be bought can't be suggested, so they don't hold a
	// slot either; transformations count as the item they grow out of.
	completed := func(id int) bool {
		it := cat.Items[id]
		return it != nil && it.Purchasable && !it.Boots && it.Cost >= 2000
	}
	for _, e := range asList(data["core_items"]) {
		// The core build order is the strongest signal, so it outweighs the
		// later lists an item also shows up in.
		w := 2 * max(entryPick(e), 0.3)
		pos := 0
		seen := map[int]bool{}
		for _, id := range entryIDs(e) {
			if id = buyable(cat, id); completed(id) && !seen[id] {
				seen[id] = true
				pos++
				add(id, float64(pos), w)
			}
		}
	}
	for slot, key := range map[float64]string{4: "fourth_items", 5: "fifth_items", 6: "sixth_items"} {
		for _, e := range asList(data[key]) {
			w := entryPick(e)
			if w <= 0 {
				w = 0.05
			}
			for _, id := range entryIDs(e) {
				if id = buyable(cat, id); completed(id) {
					add(id, slot, w)
				}
			}
		}
	}
	if len(sum) == 0 {
		return nil
	}
	out := make(map[int]float64, len(sum))
	for id, s := range sum {
		out[id] = s / wt[id]
	}
	return out
}

// toolResult is the MCP tools/call result shape.
type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

// extractResult pulls item popularity out of a tools/call result. It returns
// the result text too (for error messages and debugging).
func extractResult(cat *engine.Catalog, raw json.RawMessage) (*extracted, string, bool) {
	var tr toolResult
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, string(raw), false
	}
	var texts []string
	for _, c := range tr.Content {
		if c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	text := strings.Join(texts, "\n")
	if tr.IsError {
		return nil, text, true
	}
	if len(tr.StructuredContent) > 0 && string(tr.StructuredContent) != "null" {
		if ex := extractJSON(cat, tr.StructuredContent); ex != nil && len(ex.Pop) > 0 {
			return ex, text, false
		}
	}
	if ex := extractJSON(cat, []byte(strings.TrimSpace(text))); ex != nil && len(ex.Pop) > 0 {
		return ex, text, false
	}
	if tree, ok := parseRepr(text); ok {
		if ex := extractTree(cat, tree, "opgg text"); len(ex.Pop) > 0 {
			return ex, text, false
		}
	}
	return extractText(cat, text), text, false
}
