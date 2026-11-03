package engine

import (
	"regexp"
	"strings"
)

// Feature is a bitmask of item effects that matter for counter-building.
type Feature uint32

const (
	FAntiHeal Feature = 1 << iota
	FAntiShield
	FAntiCrit
	FAntiAttackSpeed
	FAntiAuto
	FStasis
	FRevive
	FSpellShield
	FLifeline
	FCleanse
	FTankBust
	FArmorShred
	FMRShred
	FMagicDR
	FTeamUtility
	FAllyCleanse
	FOnHit
	FSlow
)

var (
	reTankBust = regexp.MustCompile(`max health (magic|physical|true) damage|enemy's current health|current health as bonus|based on their bonus health|giant slayer`)
	reArmorRed = regexp.MustCompile(`reduc\w* (the target's |their )?armor`)
	reMRRed    = regexp.MustCompile(`reduc\w* (the target's |their )?magic resist|take \d+% more magic damage`)
	reAlly     = regexp.MustCompile(`\b(ally|allies|allied)\b`)
	reAntiCrit = regexp.MustCompile(`(less|reduced) damage (taken )?from critical strikes`)
)

func (it *Item) hasPassive(name string) bool {
	for _, p := range it.Passives {
		if p == name {
			return true
		}
	}
	return false
}

func detectFeatures(it *Item) Feature {
	var f Feature
	t := it.Text
	if strings.Contains(t, "wounds") {
		f |= FAntiHeal
	}
	if it.hasPassive("shield reaver") || strings.Contains(t, "reduces shields") || strings.Contains(t, "reduce shields") {
		f |= FAntiShield
	}
	if reAntiCrit.MatchString(t) {
		f |= FAntiCrit
	}
	if strings.Contains(t, "attack speed of nearby") || strings.Contains(t, "reduce the attack speed") {
		f |= FAntiAttackSpeed
	}
	if strings.Contains(t, "struck by an attack") || strings.Contains(t, "damage from attacks") || f&(FAntiAttackSpeed|FAntiCrit) != 0 {
		f |= FAntiAuto
	}
	if it.hasPassive("time stop") {
		f |= FStasis
	}
	if it.hasPassive("rebirth") {
		f |= FRevive
	}
	if it.hasPassive("annul") || strings.Contains(t, "spell shield") {
		f |= FSpellShield
	}
	if it.hasPassive("lifeline") {
		f |= FLifeline
	}
	if it.hasPassive("quicksilver") {
		f |= FCleanse
	}
	if it.hasPassive("purify") {
		f |= FAllyCleanse
	}
	if reTankBust.MatchString(t) {
		f |= FTankBust
	}
	if reArmorRed.MatchString(t) {
		f |= FArmorShred
	}
	if reMRRed.MatchString(t) {
		f |= FMRShred
	}
	if it.hasPassive("magebane") || it.hasPassive("steadfast") || (it.hasPassive("lifeline") && strings.Contains(t, "magic damage shield")) {
		f |= FMagicDR
	}
	if reAlly.MatchString(t) {
		f |= FTeamUtility
	}
	if strings.Contains(t, "on-hit") || it.Tags["OnHit"] {
		f |= FOnHit
	}
	if it.Tags["Slow"] || strings.Contains(t, "slow enemies") {
		f |= FSlow
	}
	return f
}

// uniqueGroups returns the "limited to one" groups an item belongs to.
func uniqueGroups(it *Item) []string {
	var g []string
	for _, p := range it.Passives {
		switch p {
		case "lifeline", "spellblade", "cleave", "immolate", "annul", "quicksilver", "time stop", "rebirth":
			g = append(g, p)
		case "manaflow", "awe":
			g = append(g, "manaflow")
		}
	}
	if it.Stats[StArmorPen] > 0 {
		g = append(g, "last whisper")
	}
	if it.Stats[StMagicPenPct] > 0 && !it.Boots {
		g = append(g, "blight")
	}
	if it.Boots {
		g = append(g, "boots")
	}
	return g
}
