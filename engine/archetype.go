package engine

import "math"

// Archetype is the build family the engine scores items for.
type Archetype int

const (
	ArchMarksman Archetype = iota
	ArchADAssassin
	ArchADBruiser
	ArchTank
	ArchAPMage
	ArchAPAssassin
	ArchAPBruiser
	ArchAPOnHit
	ArchEnchanter
	ArchTankSupport
	ArchADOnHit
	ArchADCaster
)

var archNames = map[Archetype]string{
	ArchMarksman: "Marksman", ArchADAssassin: "AD Assassin", ArchADBruiser: "AD Bruiser",
	ArchTank: "Tank", ArchAPMage: "Mage", ArchAPAssassin: "AP Assassin", ArchAPBruiser: "AP Bruiser",
	ArchAPOnHit: "AP On-hit", ArchEnchanter: "Enchanter", ArchTankSupport: "Tank Support",
	ArchADOnHit: "On-hit", ArchADCaster: "AD Caster",
}

func (a Archetype) String() string { return archNames[a] }

func (a Archetype) squishy() bool {
	switch a {
	case ArchMarksman, ArchADAssassin, ArchAPMage, ArchAPAssassin, ArchAPOnHit, ArchEnchanter, ArchADOnHit, ArchADCaster:
		return true
	}
	return false
}

func (a Archetype) defensive() bool {
	return a == ArchTank || a == ArchTankSupport || a == ArchADBruiser || a == ArchAPBruiser
}

func (a Archetype) physical() bool {
	return a == ArchMarksman || a == ArchADAssassin || a == ArchADBruiser || a == ArchADOnHit || a == ArchADCaster
}

func (a Archetype) magical() bool {
	return a == ArchAPMage || a == ArchAPAssassin || a == ArchAPBruiser || a == ArchAPOnHit
}

func (a Archetype) support() bool { return a == ArchEnchanter || a == ArchTankSupport }

func weights(pairs map[Stat]float64) Stats {
	var s Stats
	for k, v := range pairs {
		s[k] = v
	}
	return s
}

// usefulness of each stat for each archetype (0..1).
var archWeights = map[Archetype]Stats{
	ArchMarksman: weights(map[Stat]float64{StAD: 1, StHP: .25, StArmor: .3, StMR: .3, StAS: .95, StCrit: 1, StCritDmg: 1,
		StAH: .35, StLethality: .5, StArmorPen: 1, StLifeSteal: .7, StOmnivamp: .6, StMS: .5, StMSPct: .6, StMana: .15,
		StManaRegen: .1, StHealthRegen: .1, StTenacity: .7, StSlowResist: .3}),
	ArchADAssassin: weights(map[Stat]float64{StAD: 1, StHP: .3, StArmor: .3, StMR: .3, StAS: .2, StCrit: .2, StCritDmg: .2,
		StAH: .75, StLethality: 1, StArmorPen: .6, StLifeSteal: .3, StOmnivamp: .45, StMS: .45, StMSPct: .6, StMana: .1,
		StTenacity: .6, StSlowResist: .3}),
	ArchADBruiser: weights(map[Stat]float64{StAD: .85, StHP: .85, StArmor: .6, StMR: .6, StAS: .45, StCrit: .1, StAH: .85,
		StLethality: .5, StArmorPen: .8, StLifeSteal: .45, StOmnivamp: .65, StMS: .4, StMSPct: .5, StMana: .1,
		StHealthRegen: .35, StTenacity: .8, StSlowResist: .4}),
	ArchTank: weights(map[Stat]float64{StAD: .1, StAP: .15, StHP: 1, StArmor: .9, StMR: .9, StAS: .1, StAH: .85, StLifeSteal: .1,
		StOmnivamp: .2, StMS: .4, StMSPct: .6, StMana: .15, StHealthRegen: .5, StHealShield: .15, StTenacity: .8, StSlowResist: .5}),
	ArchAPMage: weights(map[Stat]float64{StAP: 1, StHP: .3, StArmor: .3, StMR: .3, StAS: .05, StAH: .8, StMagicPen: 1,
		StMagicPenPct: 1, StOmnivamp: .3, StMS: .35, StMSPct: .5, StMana: .45, StManaRegen: .25, StHealShield: .1,
		StTenacity: .5, StSlowResist: .3}),
	ArchAPAssassin: weights(map[Stat]float64{StAP: 1, StHP: .3, StArmor: .25, StMR: .25, StAH: .55, StMagicPen: 1,
		StMagicPenPct: .9, StOmnivamp: .35, StMS: .5, StMSPct: .65, StMana: .25, StTenacity: .5, StSlowResist: .3}),
	ArchAPBruiser: weights(map[Stat]float64{StAP: .95, StHP: .8, StArmor: .55, StMR: .55, StAS: .35, StAH: .7, StMagicPen: .8,
		StMagicPenPct: .8, StOmnivamp: .6, StLifeSteal: .1, StMS: .4, StMSPct: .5, StMana: .15, StHealthRegen: .3,
		StTenacity: .75, StSlowResist: .4}),
	ArchAPOnHit: weights(map[Stat]float64{StAD: .2, StAP: .9, StHP: .35, StArmor: .35, StMR: .35, StAS: .95, StCrit: .15,
		StAH: .35, StMagicPen: .8, StMagicPenPct: .8, StOmnivamp: .4, StLifeSteal: .2, StMS: .4, StMSPct: .5, StTenacity: .6}),
	ArchEnchanter: weights(map[Stat]float64{StAP: .45, StHP: .35, StArmor: .35, StMR: .35, StAH: .95, StMS: .5, StMSPct: .7,
		StMana: .35, StManaRegen: .8, StHealthRegen: .2, StHealShield: 1, StTenacity: .5, StSlowResist: .3}),
	ArchTankSupport: weights(map[Stat]float64{StAP: .1, StHP: .95, StArmor: .85, StMR: .85, StAH: .95, StMS: .5, StMSPct: .7,
		StMana: .15, StManaRegen: .45, StHealthRegen: .4, StHealShield: .35, StTenacity: .7, StSlowResist: .4}),
	ArchADOnHit: weights(map[Stat]float64{StAD: .85, StAP: .2, StHP: .4, StArmor: .35, StMR: .35, StAS: 1, StCrit: .4, StCritDmg: .4,
		StAH: .3, StLethality: .3, StArmorPen: .8, StLifeSteal: .7, StOmnivamp: .5, StMS: .45, StMSPct: .55, StMana: .1,
		StTenacity: .7, StSlowResist: .3}),
	ArchADCaster: weights(map[Stat]float64{StAD: 1, StAP: .15, StHP: .35, StArmor: .3, StMR: .3, StAS: .5, StCrit: .2, StCritDmg: .2,
		StAH: 1, StLethality: .5, StArmorPen: .9, StLifeSteal: .35, StOmnivamp: .45, StMS: .4, StMSPct: .5, StMana: .6,
		StTenacity: .6, StSlowResist: .3}),
}

func hasTag(tags []string, t string) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}

// pickArchetype decides how to score items for the player, using the champion's
// class, its usual damage type, and what the player has actually bought so far.
func pickArchetype(ch *Champion, myItems Stats, live *LivePlayerSelf, level int, support bool) (Archetype, float64) {
	tags := []string{"Fighter"}
	phys := 0.6
	var tr Trait
	if ch != nil {
		tags = ch.Tags
		tr = ch.Trait
		phys = tr.Phys
	}
	it := myItems
	adG := it[StAD]*35 + it[StLethality]*30 + it[StArmorPen]*42 + it[StCrit]*40
	apG := it[StAP]*20 + it[StMagicPen]*40 + it[StMagicPenPct]*46
	defG := it[StHP]*2.67 + it[StArmor]*20 + it[StMR]*20
	if off := adG + apG; off > 0 {
		w := math.Min(0.8, off/3000)
		phys = (1-w)*phys + w*(adG/off)
	}
	apLean := adG+apG > 1500 && apG > adG
	if live != nil && ch != nil {
		baseAD := growth(ch.Base["attackdamage"], ch.Base["attackdamageperlevel"], level)
		bonusAD := live.ChampionStats.AttackDamage - baseAD
		ap := live.ChampionStats.AbilityPower
		if ap > 60 && ap*20 > 1.6*bonusAD*35 {
			phys = math.Min(phys, 0.35)
			apLean = true
		} else if bonusAD > 30 && bonusAD*35 > 1.6*ap*20 {
			phys = math.Max(phys, 0.65)
		}
	}
	tankish := defG > 2500 && defG > 1.8*(adG+apG)
	primary := tags[0]
	melee := ch == nil || ch.Melee
	var a Archetype
	switch {
	case support:
		switch {
		case tr.Enchanter:
			a = ArchEnchanter
		case hasTag(tags, "Tank") || tankish:
			a = ArchTankSupport
		case phys >= 0.5 && hasTag(tags, "Marksman"):
			a = ArchMarksman
		case phys >= 0.5:
			a = ArchADAssassin
		default:
			a = ArchAPMage
		}
	case primary == "Marksman":
		if phys >= 0.5 {
			a = ArchMarksman
		} else {
			a = ArchAPOnHit
		}
	case primary == "Assassin":
		if phys >= 0.5 {
			a = ArchADAssassin
		} else {
			a = ArchAPAssassin
		}
	case primary == "Mage":
		switch {
		case phys >= 0.5:
			a = ArchADBruiser
		case melee:
			a = ArchAPBruiser
		case hasTag(tags, "Assassin"):
			a = ArchAPAssassin
		default:
			a = ArchAPMage
		}
	case primary == "Tank":
		a = ArchTank
	case primary == "Support":
		switch {
		case phys >= 0.5:
			a = ArchADAssassin
		case tr.Enchanter:
			a = ArchEnchanter
		default:
			a = ArchAPMage
		}
	default: // Fighter
		if phys >= 0.5 {
			a = ArchADBruiser
		} else {
			a = ArchAPBruiser
		}
	}
	// Champions whose usual build differs from their class (unless they are
	// clearly building AP this game).
	if !support && !tankish && !apLean {
		switch tr.Style {
		case 'K':
			a = ArchMarksman
		case 'O':
			a = ArchADOnHit
		case 'X':
			a = ArchADCaster
		case 'L':
			a = ArchADAssassin
		}
	}
	if tankish && (a == ArchADBruiser || a == ArchAPBruiser) {
		a = ArchTank
	}
	return a, phys
}
