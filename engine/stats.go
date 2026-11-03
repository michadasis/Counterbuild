package engine

// Stat identifies an item stat parsed from Data Dragon descriptions.
type Stat int

const (
	StAD Stat = iota
	StAP
	StHP
	StArmor
	StMR
	StAS      // attack speed, percent points
	StCrit    // crit chance, percent points
	StCritDmg // crit damage, percent points
	StAH
	StLethality
	StArmorPen // percent armor penetration
	StMagicPen // flat magic penetration
	StMagicPenPct
	StLifeSteal
	StOmnivamp
	StMS    // flat move speed
	StMSPct // percent move speed
	StMana
	StManaRegen   // percent base mana regen
	StHealthRegen // percent base health regen
	StHealShield
	StTenacity
	StSlowResist
	numStats
)

// Stats is a dense stat vector.
type Stats [numStats]float64

func (s *Stats) Add(o Stats) {
	for i := range s {
		s[i] += o[i]
	}
}

// statGold is the approximate shop value of one unit of each stat, derived
// from basic components (Long Sword, Amplifying Tome, Ruby Crystal, ...).
var statGold = Stats{
	StAD:          35,
	StAP:          20,
	StHP:          2.67,
	StArmor:       20,
	StMR:          20,
	StAS:          25,
	StCrit:        40,
	StCritDmg:     20,
	StAH:          40,
	StLethality:   30,
	StArmorPen:    42,
	StMagicPen:    40,
	StMagicPenPct: 46,
	StLifeSteal:   54,
	StOmnivamp:    55,
	StMS:          12,
	StMSPct:       65,
	StMana:        1,
	StManaRegen:   4,
	StHealthRegen: 3,
	StHealShield:  50,
	StTenacity:    15,
	StSlowResist:  10,
}

var statByName = map[string]Stat{
	"attack damage":          StAD,
	"ability power":          StAP,
	"health":                 StHP,
	"armor":                  StArmor,
	"magic resist":           StMR,
	"attack speed":           StAS,
	"critical strike chance": StCrit,
	"critical strike damage": StCritDmg,
	"ability haste":          StAH,
	"lethality":              StLethality,
	"armor penetration":      StArmorPen,
	"magic penetration":      StMagicPen,
	"life steal":             StLifeSteal,
	"omnivamp":               StOmnivamp,
	"move speed":             StMS,
	"mana":                   StMana,
	"base mana regen":        StManaRegen,
	"base health regen":      StHealthRegen,
	"heal and shield power":  StHealShield,
	"tenacity":               StTenacity,
	"slow resist":            StSlowResist,
}

func statFromName(name string, pct bool) (Stat, bool) {
	st, ok := statByName[name]
	if !ok {
		return 0, false
	}
	switch st {
	case StMagicPen:
		if pct {
			return StMagicPenPct, true
		}
	case StMS:
		if pct {
			return StMSPct, true
		}
	}
	return st, true
}

// isPen reports whether a stat is evaluated with resistance math rather than a flat weight.
func isPen(s Stat) bool {
	return s == StLethality || s == StArmorPen || s == StMagicPen || s == StMagicPenPct
}

// growth applies League's per-level stat growth formula.
func growth(base, perLevel float64, level int) float64 {
	n := float64(level - 1)
	if n < 0 {
		n = 0
	}
	return base + perLevel*n*(0.7025+0.0175*n)
}
