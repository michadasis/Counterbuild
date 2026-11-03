package engine

import (
	"math"
	"sort"
	"strings"
)

// Rune keystone ids used as play-style hints.
const (
	ksConqueror   = 8010
	ksLethalTempo = 8008
	ksPressAttack = 8005
	ksFleet       = 8021
	ksElectrocute = 8112
	ksDarkHarvest = 8128
	ksHailBlades  = 9923
	ksFirstStrike = 8369
	ksComet       = 8229
	ksGrasp       = 8437
)

// EnemyProfile is what we know about one enemy right now.
type EnemyProfile struct {
	ChampID  string
	Name     string
	Level    int
	K, D, A  int
	ItemGold int
	Items    Stats
	Armor    float64
	MR       float64
	HP       float64
	Phys     float64
	Heal     float64
	Shield   float64
	CC       float64
	Burst    float64
	Auto     float64
	Tank     float64
	Crit     float64
	Suppress bool
	Support  bool

	HasAntiHeal bool
	HasAntiCrit bool
	HasAntiAS   bool

	Threat float64 // normalized, sums to 1 across enemies
	Fed    bool
}

// Context is the threat-weighted summary of the enemy team.
type Context struct {
	Enemies    []*EnemyProfile
	Phys       float64 // share of enemy damage that is physical
	Heal       float64
	Shield     float64
	CritT      float64
	AutoPhys   float64
	Burst      float64
	BurstMagic float64
	CC         float64
	Suppress   float64
	Tank       float64

	EnemyAntiHeal float64
	EnemyAntiCrit float64
	EnemyAntiAS   float64
	AllyAntiHeal  bool

	GameMinutes float64
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// supportItems returns the ids of the support quest item family (World Atlas line).
func (c *Catalog) isSupportItem(id int) bool {
	const atlas = 3865
	if id == atlas {
		return true
	}
	it := c.Items[id]
	if it == nil {
		return false
	}
	for _, f := range it.From {
		if f == atlas || c.isSupportItem(f) {
			return true
		}
	}
	return false
}

func (c *Catalog) ownsFeature(items []LiveItem, f Feature) bool {
	for _, li := range items {
		if it := c.Items[li.ItemID]; it != nil && it.Feat&f != 0 {
			return true
		}
	}
	return false
}

func itemHealBonus(it *Item) float64 {
	t := it.Text
	for _, k := range []string{"heal for", "restores health", "restore health", "heals you", "and heal", "restore 50% base health"} {
		if strings.Contains(t, k) {
			return 0.07
		}
	}
	return 0
}

func itemShieldBonus(it *Item) float64 {
	t := it.Text
	if it.Feat&FLifeline != 0 || strings.Contains(t, "grant nearby allies a") || strings.Contains(t, "shield that") || strings.Contains(t, "gain a shield") || strings.Contains(t, "convert excess healing") {
		return 0.08
	}
	return 0
}

func (c *Catalog) profileEnemy(p *LivePlayer) *EnemyProfile {
	ch := c.Champion(p.ChampID())
	e := &EnemyProfile{ChampID: p.ChampID(), Name: p.ChampionName, Level: p.Level,
		K: p.Scores.Kills, D: p.Scores.Deaths, A: p.Scores.Assists}
	if e.Level < 1 {
		e.Level = 1
	}
	var tr Trait
	if ch != nil {
		tr = ch.Trait
		e.Name = ch.Name
	} else {
		tr = tagDefaults(nil)
		tr.Phys = 0.6
	}
	heal, shield := tr.Heal, tr.Shield
	var extraHeal, extraShield float64
	for _, li := range p.Items {
		it := c.Items[li.ItemID]
		if it == nil || it.Consumable {
			continue
		}
		n := li.Count
		if n < 1 {
			n = 1
		}
		for i := 0; i < n; i++ {
			e.Items.Add(it.Stats)
		}
		e.ItemGold += it.Cost * n
		if it.Feat&FAntiHeal != 0 {
			e.HasAntiHeal = true
		}
		if it.Feat&FAntiCrit != 0 {
			e.HasAntiCrit = true
		}
		if it.Feat&FAntiAttackSpeed != 0 {
			e.HasAntiAS = true
		}
		if c.isSupportItem(li.ItemID) {
			e.Support = true
		}
		extraHeal += itemHealBonus(it)
		extraShield += itemShieldBonus(it)
	}
	pos := strings.ToUpper(p.Position)
	if pos == "UTILITY" || pos == "SUPPORT" {
		e.Support = true
	}

	if ch != nil {
		b := ch.Base
		e.Armor = growth(b["armor"], b["armorperlevel"], e.Level)
		e.MR = growth(b["spellblock"], b["spellblockperlevel"], e.Level)
		e.HP = growth(b["hp"], b["hpperlevel"], e.Level)
	} else {
		e.Armor, e.MR, e.HP = 30+4.5*float64(e.Level-1), 30+1.6*float64(e.Level-1), 600+100*float64(e.Level-1)
	}
	e.Armor += e.Items[StArmor]
	e.MR += e.Items[StMR]
	e.HP += e.Items[StHP]

	it := e.Items
	adG := it[StAD]*35 + it[StLethality]*30 + it[StArmorPen]*42 + it[StCrit]*40
	apG := it[StAP]*20 + it[StMagicPen]*40 + it[StMagicPenPct]*46
	defG := it[StHP]*2.67 + it[StArmor]*20 + it[StMR]*20
	e.Phys = tr.Phys
	if off := adG + apG; off > 0 {
		w := math.Min(0.75, off/4000)
		e.Phys = (1-w)*tr.Phys + w*(adG/off)
	}

	ks := p.Runes.Keystone.ID
	switch ks {
	case ksConqueror:
		heal += 0.15
	case ksGrasp:
		heal += 0.1
	case ksFleet:
		heal += 0.08
	}
	heal += math.Min(0.3, (it[StLifeSteal]+it[StOmnivamp])/40) + math.Min(0.25, extraHeal)
	shield += math.Min(0.3, extraShield) + it[StHealShield]/100
	e.Heal = clamp(heal, 0, 1.2)
	e.Shield = clamp(shield, 0, 1.2)
	e.CC = tr.CC

	offShare := 0.5
	if adG+apG+defG > 0 {
		offShare = (adG + apG) / (adG + apG + defG)
	}
	e.Burst = tr.Burst * (0.75 + 0.5*offShare)
	if ks == ksElectrocute || ks == ksDarkHarvest || ks == ksHailBlades || ks == ksFirstStrike || ks == ksComet {
		e.Burst += 0.1
	}
	e.Burst = clamp(e.Burst, 0, 1.2)
	e.Auto = tr.Auto + it[StAS]/200 + it[StCrit]/200
	if ks == ksLethalTempo || ks == ksPressAttack {
		e.Auto += 0.15
	}
	e.Auto = clamp(e.Auto, 0, 1.2)
	e.Crit = clamp(it[StCrit]/100, 0, 1)
	defShare := 0.0
	if adG+apG+defG > 0 {
		defShare = defG / (adG + apG + defG)
	}
	e.Tank = clamp(0.55*tr.Tank+0.6*defShare+it[StHP]/4000, 0, 1.2)
	e.Suppress = tr.Suppress
	return e
}

// BuildContext profiles every enemy and summarizes the team.
func (c *Catalog) BuildContext(g *LiveGame, self *LivePlayer) *Context {
	ctx := &Context{GameMinutes: g.GameData.GameTime / 60}
	myTeam := self.Team
	var lvlSum float64
	for i := range g.AllPlayers {
		p := &g.AllPlayers[i]
		if p == self {
			continue
		}
		if p.Team == myTeam && myTeam != "" {
			if c.ownsFeature(p.Items, FAntiHeal) {
				ctx.AllyAntiHeal = true
			}
			continue
		}
		e := c.profileEnemy(p)
		ctx.Enemies = append(ctx.Enemies, e)
		lvlSum += float64(e.Level)
	}
	n := len(ctx.Enemies)
	if n == 0 {
		ctx.Phys = 0.5
		return ctx
	}
	avgLvl := lvlSum / float64(n)
	var goldSum float64
	for _, e := range ctx.Enemies {
		goldSum += float64(e.ItemGold)
	}
	avgGold := goldSum / float64(n)

	raw := make([]float64, n)
	var rawSum float64
	for i, e := range ctx.Enemies {
		kda := 1 + 0.07*clamp(float64(e.K-e.D), -5, 10) + 0.015*float64(e.A)
		kda = clamp(kda, 0.6, 2.0)
		lvl := 1 + 0.05*(float64(e.Level)-avgLvl)
		r := (400 + float64(e.ItemGold)) * kda * clamp(lvl, 0.7, 1.4)
		if e.Support {
			r *= 0.55
		}
		raw[i] = r
		rawSum += r
		e.Fed = (e.K >= 4 && e.K >= e.D+3) || (e.ItemGold >= 2500 && float64(e.ItemGold) >= 1.4*avgGold)
	}
	var dmgW, physW float64
	for i, e := range ctx.Enemies {
		e.Threat = raw[i] / rawSum
		t := e.Threat
		dw := t * (1.15 - 0.5*math.Min(1, e.Tank))
		dmgW += dw
		physW += dw * e.Phys
		w := 0.18 + 0.6*t
		ctx.Heal += e.Heal * w
		ctx.Shield += e.Shield * w
		ctx.CritT += t * e.Crit * e.Auto
		ctx.AutoPhys += t * e.Auto * e.Phys
		ctx.Burst += t * e.Burst
		ctx.BurstMagic += t * e.Burst * (1 - e.Phys)
		ctx.CC += e.CC / float64(n)
		ctx.Tank += e.Tank * (0.12 + 0.4*t)
		if e.Suppress {
			ctx.Suppress = math.Max(ctx.Suppress, clamp(0.5+2*t, 0, 1))
		}
		if e.HasAntiHeal {
			ctx.EnemyAntiHeal += t
		}
		if e.HasAntiCrit {
			ctx.EnemyAntiCrit += t
		}
		if e.HasAntiAS {
			ctx.EnemyAntiAS += t
		}
	}
	if dmgW > 0 {
		ctx.Phys = physW / dmgW
	}
	sort.SliceStable(ctx.Enemies, func(i, j int) bool { return ctx.Enemies[i].Threat > ctx.Enemies[j].Threat })
	return ctx
}

// topBy returns up to n enemy names with the highest value of f (above min).
func (ctx *Context) topBy(n int, min float64, f func(*EnemyProfile) float64) []string {
	type kv struct {
		name string
		v    float64
	}
	var all []kv
	for _, e := range ctx.Enemies {
		if v := f(e); v >= min {
			all = append(all, kv{e.Name, v})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].v > all[j].v })
	var out []string
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].name)
	}
	return out
}
