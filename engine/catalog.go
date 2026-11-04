package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Data Dragon item.json / champion.json shapes (only the fields we use).

type ddItemFile struct {
	Version string            `json:"version"`
	Data    map[string]ddItem `json:"data"`
}

type ddItem struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	From        []string `json:"from"`
	Into        []string `json:"into"`
	Gold        struct {
		Base        int  `json:"base"`
		Total       int  `json:"total"`
		Purchasable bool `json:"purchasable"`
	} `json:"gold"`
	Tags             []string        `json:"tags"`
	Maps             map[string]bool `json:"maps"`
	InStore          *bool           `json:"inStore"`
	HideFromAll      bool            `json:"hideFromAll"`
	Consumed         bool            `json:"consumed"`
	RequiredChampion string          `json:"requiredChampion"`
	RequiredAlly     string          `json:"requiredAlly"`
}

type ddChampFile struct {
	Version string             `json:"version"`
	Data    map[string]ddChamp `json:"data"`
}

type ddChamp struct {
	ID      string   `json:"id"`
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Tags    []string `json:"tags"`
	Partype string   `json:"partype"`
	Info    struct {
		Attack float64 `json:"attack"`
		Magic  float64 `json:"magic"`
	} `json:"info"`
	Stats map[string]float64 `json:"stats"`
}

// Item is a parsed, engine-friendly item.
type Item struct {
	ID       int
	Name     string
	Cost     int // total cost
	Combine  int // gold.base, the recipe cost
	From     []int
	Into     []int
	Tags     map[string]bool
	Maps     map[int]bool
	Stats    Stats
	Passives []string // lowercased passive/active names
	Text     string   // lowercased description without markup
	Feat     Feature

	Purchasable bool
	Boots       bool
	Consumable  bool
	Restricted  bool // champion/ally specific or hidden

	Ex Extras
}

// Extras are passive effects with a number we can value directly.
type Extras struct {
	APAmp      float64 // Rabadon's: % total AP
	DmgAmp     float64 // generic % damage increase
	DmgAmpK    float64 // how much of DmgAmp applies in practice
	TankAmp    float64 // bonus damage vs high bonus health (Giant Slayer)
	CritBonus  float64 // crit chance granted by passive (Yun Tal)
	UltAH      float64
	BasicAH    float64
	ArmorShred float64 // stacked % armor reduction
	MRShred    float64 // stacked % MR reduction
	MagicAmp   float64 // enemies take % more magic damage
}

func (e Extras) any() bool { return e != (Extras{}) }

var (
	reAPAmp    = regexp.MustCompile(`increases your total ability power by (\d+)%`)
	reAmpUpTo  = regexp.MustCompile(`deal \d+% bonus damage, up to (\d+)%`)
	reAmpRange = regexp.MustCompile(`deal up to (\d+)% increased damage`)
	reGiant    = regexp.MustCompile(`deal up to (\d+)% bonus damage against champions based on their bonus health`)
	reLowHPAmp = regexp.MustCompile(`dealing (\d+)% increased damage`)
	reStackAmp = regexp.MustCompile(`damage by (\d+)% for \d+ seconds\. \(stacks (\d+) times\)`)
	reArmorStk = regexp.MustCompile(`reduces the target's armor by ([\d.]+)% for \d+ seconds\. \(stacks (\d+) times\)`)
	reMRStk    = regexp.MustCompile(`reduces their magic resist by ([\d.]+)% for \d+ seconds\. \(stacks (\d+) times\)`)
	reMagicAmp = regexp.MustCompile(`take (\d+)% more magic damage`)
	reCritPerm = regexp.MustCompile(`critical strike chance permanently up to (\d+)%`)
	reUltAH    = regexp.MustCompile(`gain (\d+) ultimate ability haste`)
	reBasicAH  = regexp.MustCompile(`gain (\d+) basic ability haste`)
)

func num(re *regexp.Regexp, s string, group int) float64 {
	m := re.FindStringSubmatch(s)
	if m == nil || len(m) <= group {
		return 0
	}
	v, _ := strconv.ParseFloat(m[group], 64)
	return v
}

func parseExtras(t string) Extras {
	var e Extras
	e.APAmp = num(reAPAmp, t, 1) / 100
	if v := num(reAmpUpTo, t, 1); v > 0 {
		e.DmgAmp, e.DmgAmpK = v/100, 0.7
	} else if v := num(reAmpRange, t, 1); v > 0 {
		e.DmgAmp, e.DmgAmpK = v/100, 0.6
	} else if m := reStackAmp.FindStringSubmatch(t); m != nil {
		a, _ := strconv.ParseFloat(m[1], 64)
		n, _ := strconv.ParseFloat(m[2], 64)
		e.DmgAmp, e.DmgAmpK = a*n/100, 0.55
	} else if v := num(reLowHPAmp, t, 1); v > 0 {
		e.DmgAmp, e.DmgAmpK = v/100, 0.3
	}
	e.TankAmp = num(reGiant, t, 1) / 100
	if m := reArmorStk.FindStringSubmatch(t); m != nil {
		a, _ := strconv.ParseFloat(m[1], 64)
		n, _ := strconv.ParseFloat(m[2], 64)
		e.ArmorShred = a * n / 100
	}
	if m := reMRStk.FindStringSubmatch(t); m != nil {
		a, _ := strconv.ParseFloat(m[1], 64)
		n, _ := strconv.ParseFloat(m[2], 64)
		e.MRShred = a * n / 100
	}
	e.MagicAmp = num(reMagicAmp, t, 1) / 100
	e.CritBonus = num(reCritPerm, t, 1)
	e.UltAH = num(reUltAH, t, 1)
	e.BasicAH = num(reBasicAH, t, 1)
	return e
}

// Champion is a parsed champion with base stats and traits.
type Champion struct {
	ID      string // Data Dragon id, e.g. "MonkeyKing"
	Key     int
	Name    string
	Tags    []string
	Partype string
	Melee   bool
	Base    map[string]float64
	Trait   Trait
}

// Catalog holds the static data for one patch.
type Catalog struct {
	Version   string
	Items     map[int]*Item
	Champions map[string]*Champion // by Data Dragon id
	byLower   map[string]*Champion // lowercased id and name

	// Transforms maps items that can't be bought but grow out of one
	// (Muramana, Seraph's Embrace, Diadem of Songs) to the item you buy.
	Transforms map[int]int
}

var (
	reStatsBlock = regexp.MustCompile(`(?s)<stats>(.*?)</stats>`)
	reStatLine   = regexp.MustCompile(`<attention>\s*([0-9.]+)(%?)\s*</attention>\s*([^<]+)`)
	rePassive    = regexp.MustCompile(`<(?:passive|active)>([^<]+)</(?:passive|active)>`)
	reTag        = regexp.MustCompile(`<[^>]+>`)
	reSpaces     = regexp.MustCompile(`\s+`)
)

// LoadCatalog parses Data Dragon item.json and champion.json bytes.
func LoadCatalog(itemJSON, champJSON []byte) (*Catalog, error) {
	var itf ddItemFile
	if err := json.Unmarshal(itemJSON, &itf); err != nil {
		return nil, fmt.Errorf("item.json: %w", err)
	}
	var chf ddChampFile
	if err := json.Unmarshal(champJSON, &chf); err != nil {
		return nil, fmt.Errorf("champion.json: %w", err)
	}
	c := &Catalog{
		Version:   itf.Version,
		Items:     make(map[int]*Item, len(itf.Data)),
		Champions: make(map[string]*Champion, len(chf.Data)),
		byLower:   make(map[string]*Champion, len(chf.Data)*2),
	}
	for k, d := range itf.Data {
		id, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		c.Items[id] = parseItem(id, d)
	}
	for _, it := range c.Items {
		it.Feat = detectFeatures(it)
	}
	c.Transforms = findTransforms(c.Items)
	for _, d := range chf.Data {
		key, _ := strconv.Atoi(d.Key)
		ch := &Champion{
			ID:      d.ID,
			Key:     key,
			Name:    d.Name,
			Tags:    d.Tags,
			Partype: d.Partype,
			Base:    d.Stats,
			Melee:   d.Stats["attackrange"] <= 325,
		}
		ch.Trait = traitFor(d)
		c.Champions[d.ID] = ch
		c.byLower[strings.ToLower(d.ID)] = ch
		c.byLower[strings.ToLower(d.Name)] = ch
	}
	if len(c.Items) == 0 || len(c.Champions) == 0 {
		return nil, fmt.Errorf("empty catalog")
	}
	return c, nil
}

func parseItem(id int, d ddItem) *Item {
	it := &Item{
		ID:          id,
		Name:        d.Name,
		Cost:        d.Gold.Total,
		Combine:     d.Gold.Base,
		Tags:        map[string]bool{},
		Maps:        map[int]bool{},
		Purchasable: d.Gold.Purchasable && (d.InStore == nil || *d.InStore) && !d.HideFromAll,
		Restricted:  d.RequiredChampion != "" || d.RequiredAlly != "",
		Consumable:  d.Consumed,
	}
	for _, f := range d.From {
		if n, err := strconv.Atoi(f); err == nil {
			it.From = append(it.From, n)
		}
	}
	for _, f := range d.Into {
		if n, err := strconv.Atoi(f); err == nil {
			it.Into = append(it.Into, n)
		}
	}
	for _, t := range d.Tags {
		it.Tags[t] = true
	}
	for m, ok := range d.Maps {
		if n, err := strconv.Atoi(m); err == nil {
			it.Maps[n] = ok
		}
	}
	it.Boots = it.Tags["Boots"]
	if it.Tags["Consumable"] || it.Tags["Trinket"] {
		it.Consumable = true
	}
	desc := d.Description
	if m := reStatsBlock.FindStringSubmatch(desc); m != nil {
		for _, sm := range reStatLine.FindAllStringSubmatch(m[1], -1) {
			v, err := strconv.ParseFloat(sm[1], 64)
			if err != nil {
				continue
			}
			pct := sm[2] == "%"
			name := strings.ToLower(strings.TrimSpace(sm[3]))
			st, ok := statFromName(name, pct)
			if !ok {
				continue
			}
			it.Stats[st] += v
		}
	}
	for _, pm := range rePassive.FindAllStringSubmatch(desc, -1) {
		p := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(pm[1]), ":")))
		if p != "" {
			it.Passives = append(it.Passives, p)
		}
	}
	text := reTag.ReplaceAllString(strings.ReplaceAll(desc, "<br>", " "), " ")
	it.Text = strings.ToLower(reSpaces.ReplaceAllString(text, " "))
	// Collapse " ." left behind by removed markup so phrase patterns match.
	it.Text = strings.ReplaceAll(it.Text, " .", ".")
	it.Ex = parseExtras(it.Text)
	return it
}

// Champion looks up a champion by Data Dragon id or English name (case-insensitive).
func (c *Catalog) Champion(idOrName string) *Champion {
	if ch, ok := c.Champions[idOrName]; ok {
		return ch
	}
	return c.byLower[strings.ToLower(idOrName)]
}

// findTransforms reads "transforms into X" from shop item descriptions.
func findTransforms(items map[int]*Item) map[int]int {
	out := map[int]int{}
	for _, a := range items {
		if !a.Purchasable || !strings.Contains(a.Text, "transform") {
			continue
		}
		for _, b := range items {
			if b.Purchasable || b.ID == a.ID || len(b.Name) < 4 {
				continue
			}
			// Data Dragon repeats items for other modes under long ids
			// (323003 is Arena's Archangel's Staff); prefer the standard one.
			if prev, ok := out[b.ID]; strings.Contains(a.Text, "into "+strings.ToLower(b.Name)) && (!ok || a.ID < prev) {
				out[b.ID] = a.ID
			}
		}
	}
	return out
}

// Buyable returns the shop item that gives id: id itself if it can be
// bought, the item it grows out of if it is a transformation, else 0.
func (c *Catalog) Buyable(id int) int {
	if it := c.Items[id]; it != nil && it.Purchasable {
		return id
	}
	return c.Transforms[id]
}

// buildsFrom reports whether id is somewhere in it's recipe.
func (c *Catalog) buildsFrom(it *Item, id int) bool {
	for _, f := range it.From {
		if f == id {
			return true
		}
		if sub := c.Items[f]; sub != nil && c.buildsFrom(sub, id) {
			return true
		}
	}
	return false
}

// Item returns the item or nil.
func (c *Catalog) Item(id int) *Item { return c.Items[id] }

// remainingCost returns the gold still needed to build item id given an inventory
// multiset (which is consumed as components are used).
func (c *Catalog) remainingCost(id int, inv map[int]int) int {
	it := c.Items[id]
	if it == nil {
		return 0
	}
	if inv[id] > 0 {
		inv[id]--
		return 0
	}
	cost := it.Combine
	for _, f := range it.From {
		cost += c.remainingCost(f, inv)
	}
	return cost
}

// RemainingCost is the gold needed to complete id with the given owned items.
func (c *Catalog) RemainingCost(id int, owned []int) int {
	inv := map[int]int{}
	for _, o := range owned {
		inv[o]++
	}
	return c.remainingCost(id, inv)
}

// componentTree lists every component id (recursively) of id, deduplicated.
func (c *Catalog) componentTree(id int) []int {
	seen := map[int]bool{}
	var walk func(int)
	walk = func(x int) {
		it := c.Items[x]
		if it == nil {
			return
		}
		for _, f := range it.From {
			if !seen[f] {
				seen[f] = true
				walk(f)
			}
		}
	}
	walk(id)
	out := make([]int, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
