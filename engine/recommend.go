package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Options tune a recommendation.
type Options struct {
	MaxItems int
	// Affinity maps item id to 0..1: how often the player builds it on this champion.
	Affinity map[int]float64
	// Core lists item ids the player always wants on this champion (from config).
	Core []int
	// Meta is community build data for this champion and role (may be nil).
	Meta *Meta
}

// Meta is what players most often build on a champion, from a stats site.
type Meta struct {
	Pop    map[int]float64 // item id -> 0..1 popularity (1 = most built)
	WR     map[int]float64 // item id -> win rate 0..1, when known
	Slot   map[int]float64 // item id -> typical build slot (1 = first completed item), when known
	Source string          // e.g. "OP.GG"
}

// Pick is one recommended item.
type Pick struct {
	ID        int
	Name      string
	Cost      int
	Remaining int
	Value     float64
	Score     float64
	Reasons   []string
}

// BuyAdvice is what to purchase right now.
type BuyAdvice struct {
	ID       int
	Name     string
	Cost     int
	Complete bool
	Target   string
	Short    int
	Label    string
	Note     string
}

// EnemyView is a compact enemy summary for display.
type EnemyView struct {
	ChampID string
	Name    string
	Threat  float64
	Phys    float64
	Fed     bool
	K, D, A int
	Level   int
}

// Result is everything the overlay shows.
type Result struct {
	Supported  bool
	Note       string
	ChampID    string
	Champion   string
	Archetype  string
	Level      int
	Gold       int
	GameTime   float64
	EnemyPhys  float64
	Enemies    []EnemyView
	Picks      []Pick
	Boots      *Pick
	BuyNow     *BuyAdvice
	Counter    *BuyAdvice
	Completed  []int         // completed items the player owns
	MetaSource string        // set when community build data was used
	Augments   []AugmentCard // augment cards on screen, left to right (ARAM Mayhem)
}

// AugmentCard is one augment offered on screen, rated against the others.
type AugmentCard struct {
	Name    string
	Tier    int    // stats-site tier: 0 = S, 1 = A, 2 = B, 3 and up lower
	Rank    int    // place in the stats site's list for the champion, 1 = best; 0 if unknown
	Top     bool   // tier unknown but somewhere in S to B (the data feed leaves those out)
	NoData  bool   // the stats site has no data for it on this champion
	Verdict string // "Take", "Skip", "Reroll", "Even", or "?" when there are no stats
}

type reason struct {
	label string
	gold  float64
}

type scorer struct {
	c        *Catalog
	ctx      *Context
	arch     Archetype
	U        Stats
	C        Stats
	physMine float64
	physOff  float64
	magOff   float64
	myCrit   float64
	myAP     float64
	myLeth   float64
	myArPct  float64
	myMPen   float64
	myMPct   float64
	ints     map[Feature]float64
	affinity map[int]float64
	core     map[int]bool
	meta     *Meta
	nextSlot int // which completed item the player is buying next (1 = first)
	early    bool
	ownAH    bool
}

// styleSynergy boosts items whose passive fits how an archetype plays.
var styleSynergy = map[Archetype]map[string]float64{
	ArchADCaster:  {"spellblade": 1.35, "manaflow": 1.3, "transcendence": 1.15, "dragonforce": 1.15},
	ArchADBruiser: {"spellblade": 1.08, "dragonforce": 1.05},
	ArchADOnHit:   {"spellblade": 1.05},
}

var flatCounter = map[Feature]float64{
	FAntiHeal: 700, FAntiShield: 500, FAntiCrit: 600, FAntiAttackSpeed: 350, FAntiAuto: 350,
	FStasis: 600, FSpellShield: 450, FLifeline: 400, FCleanse: 700, FTankBust: 450,
	FArmorShred: 300, FMRShred: 300, FMagicDR: 400, FRevive: 350, FTeamUtility: 500, FAllyCleanse: 250,
	FOnHit: 450,
}

func penMult(r, pct, flat float64) float64 {
	eff := r*(1-pct) - flat
	if eff < 0 {
		eff = 0
	}
	return 100 / (100 + eff)
}

func (s *scorer) penGain(phys bool, addPct, addFlat float64) float64 {
	var before, after, wsum float64
	for _, e := range s.ctx.Enemies {
		w := e.Threat
		if s.arch == ArchADAssassin || s.arch == ArchAPAssassin {
			w *= 1.25 - math.Min(1, e.Tank)
		}
		if w <= 0 {
			continue
		}
		wsum += w
		if phys {
			before += w * penMult(e.Armor, s.myArPct, s.myLeth)
			after += w * penMult(e.Armor, 1-(1-s.myArPct)*(1-addPct), s.myLeth+addFlat)
		} else {
			before += w * penMult(e.MR, s.myMPct, s.myMPen)
			after += w * penMult(e.MR, 1-(1-s.myMPct)*(1-addPct), s.myMPen+addFlat)
		}
	}
	if before <= 0 || wsum <= 0 {
		return 0
	}
	return after/before - 1
}

func (s *scorer) avgResist(phys bool) float64 {
	var sum, w float64
	for _, e := range s.ctx.Enemies {
		r := e.MR
		if phys {
			r = e.Armor
		}
		sum += e.Threat * r
		w += e.Threat
	}
	if w == 0 {
		return 0
	}
	return sum / w
}

func names(n []string) string {
	if len(n) == 0 {
		return ""
	}
	return strings.Join(n, ", ")
}

func (s *scorer) featureLabel(f Feature) string {
	ctx := s.ctx
	switch f {
	case FAntiHeal:
		if l := names(ctx.topBy(2, 0.35, func(e *EnemyProfile) float64 { return e.Heal })); l != "" {
			return "anti-heal: " + l
		}
		return "anti-heal"
	case FAntiShield:
		if l := names(ctx.topBy(2, 0.3, func(e *EnemyProfile) float64 { return e.Shield })); l != "" {
			return "anti-shield: " + l
		}
		return "anti-shield"
	case FAntiCrit:
		if l := names(ctx.topBy(2, 0.15, func(e *EnemyProfile) float64 { return e.Crit * e.Auto })); l != "" {
			return "vs crit: " + l
		}
		return "vs crit"
	case FAntiAuto, FAntiAttackSpeed:
		if l := names(ctx.topBy(2, 0.45, func(e *EnemyProfile) float64 { return e.Auto * e.Phys * (0.5 + e.Threat*2.5) })); l != "" {
			return "vs auto-attacks: " + l
		}
		return "vs auto-attacks"
	case FStasis:
		if l := names(ctx.topBy(2, 0.3, func(e *EnemyProfile) float64 { return e.Burst * (0.5 + e.Threat*2.5) })); l != "" {
			return "stasis vs " + l
		}
		return "stasis vs burst"
	case FSpellShield:
		if l := names(ctx.topBy(2, 0.3, func(e *EnemyProfile) float64 { return math.Max(e.Burst*(1-e.Phys), e.CC*0.8) * (0.5 + e.Threat*2.5) })); l != "" {
			return "spell shield vs " + l
		}
		return "spell shield"
	case FLifeline:
		return "lifeline vs burst"
	case FCleanse:
		if l := names(ctx.topBy(2, 0.5, func(e *EnemyProfile) float64 {
			if e.Suppress {
				return 1
			}
			return 0
		})); l != "" {
			return "cleanse vs " + l
		}
		return "cleanse vs CC"
	case FTankBust, FArmorShred, FMRShred:
		if l := names(ctx.topBy(2, 0.6, func(e *EnemyProfile) float64 { return e.Tank })); l != "" {
			return "shreds " + l
		}
		return "vs tanks"
	case FMagicDR:
		return fmt.Sprintf("vs %d%% magic dmg", int(math.Round((1-ctx.Phys)*100)))
	case FRevive:
		return "late-game insurance"
	case FTeamUtility, FAllyCleanse:
		return "team utility"
	case FOnHit:
		return "on-hit synergy"
	}
	return ""
}

func (s *scorer) offense() float64 {
	switch {
	case s.arch.physical():
		return s.physOff
	case s.arch.magical():
		return s.magOff
	}
	return 0.4 * (s.physOff*s.physMine + s.magOff*(1-s.physMine))
}

// labelMin is the intensity a counter must reach before it is named as a reason.
func (s *scorer) labelOK(f Feature) bool {
	in := s.ints[f]
	switch f {
	case FTankBust, FArmorShred, FMRShred:
		return in >= 0.6
	case FRevive:
		return s.ctx.GameMinutes >= 25
	case FTeamUtility, FAllyCleanse:
		return s.arch.support()
	case FOnHit:
		return s.arch == ArchADOnHit || s.arch == ArchAPOnHit
	}
	return in >= 0.4
}

func (s *scorer) score(it *Item) (float64, []reason) {
	var value float64
	var reasons []reason
	U, C := s.U, s.C
	st := it.Stats
	st[StCrit] += it.Ex.CritBonus

	// Stat value (penetration is handled with resistance math below).
	var nominal, fitNum float64
	var armorExtra, mrExtra float64
	for k := Stat(0); k < numStats; k++ {
		amt := st[k]
		if amt <= 0 {
			continue
		}
		g := amt * statGold[k]
		nominal += g
		fitNum += g * U[k]
		if isPen(k) {
			continue
		}
		switch k {
		case StCrit:
			eff := math.Min(amt, math.Max(0, 100-s.myCrit))
			value += eff * statGold[k] * U[k] * C[k]
		case StCritDmg:
			cr := math.Min(100, s.myCrit+st[StCrit]) / 100
			value += amt * statGold[k] * U[k] * C[k] * cr * 2.5
		default:
			v := g * U[k] * C[k]
			value += v
			switch k {
			case StArmor:
				armorExtra = v - g*U[k]
			case StMR:
				mrExtra = v - g*U[k]
			}
		}
	}
	ex := it.Ex
	if ex.UltAH > 0 || ex.BasicAH > 0 {
		g := (ex.UltAH*0.35 + ex.BasicAH*0.8) * statGold[StAH]
		nominal += g
		fitNum += g * U[StAH]
		value += g * U[StAH]
	}
	fit := 0.3
	if nominal > 0 {
		fit = fitNum / nominal
	}
	if armorExtra > 120 && s.ctx.Phys >= 0.55 {
		reasons = append(reasons, reason{fmt.Sprintf("vs %d%% physical dmg", int(math.Round(s.ctx.Phys*100))), armorExtra})
	}
	if mrExtra > 120 && s.ctx.Phys <= 0.45 {
		reasons = append(reasons, reason{fmt.Sprintf("vs %d%% magic dmg", int(math.Round((1-s.ctx.Phys)*100))), mrExtra})
	}
	if st[StTenacity] > 0 && s.ctx.CC >= 0.5 {
		reasons = append(reasons, reason{"tenacity vs heavy CC", st[StTenacity] * 15 * C[StTenacity]})
	}

	// Penetration, valued by how much more damage it adds against these enemies.
	if st[StLethality] > 0 || st[StArmorPen] > 0 {
		u := math.Max(U[StLethality]*sign(st[StLethality]), U[StArmorPen]*sign(st[StArmorPen]))
		if u > 0 {
			gain := s.penGain(true, st[StArmorPen]/100, st[StLethality])
			pv := gain * s.physOff * s.physMine * u
			value += pv
			if pv > 200 {
				reasons = append(reasons, reason{fmt.Sprintf("pen: avg %d armor", int(math.Round(s.avgResist(true)))), pv})
			}
		}
	}
	if st[StMagicPen] > 0 || st[StMagicPenPct] > 0 {
		u := math.Max(U[StMagicPen]*sign(st[StMagicPen]), U[StMagicPenPct]*sign(st[StMagicPenPct]))
		if u > 0 {
			gain := s.penGain(false, st[StMagicPenPct]/100, st[StMagicPen])
			pv := gain * s.magOff * (1 - s.physMine) * u
			value += pv
			if pv > 200 {
				reasons = append(reasons, reason{fmt.Sprintf("pen: avg %d MR", int(math.Round(s.avgResist(false)))), pv})
			}
		}
	}

	// Passive effects that carry a number.
	if ex.APAmp > 0 {
		v := (s.myAP + st[StAP]) * ex.APAmp * statGold[StAP] * U[StAP]
		value += v
		if v > 400 {
			reasons = append(reasons, reason{fmt.Sprintf("+%d%% total AP", int(ex.APAmp*100)), v})
		}
	}
	if ex.DmgAmp > 0 {
		value += ex.DmgAmp * ex.DmgAmpK * s.offense() * fit
	}
	if ex.TankAmp > 0 {
		value += ex.TankAmp * 0.6 * s.offense() * fit * s.ints[FTankBust]
	}
	if ex.ArmorShred > 0 && s.arch.physical() {
		value += s.penGain(true, ex.ArmorShred*0.6, 0) * s.physOff * s.physMine * fit
	}
	if ex.MRShred > 0 && s.arch.magical() {
		value += s.penGain(false, ex.MRShred*0.6, 0) * s.magOff * (1 - s.physMine) * fit
	}
	if ex.MagicAmp > 0 {
		v := ex.MagicAmp * s.magOff * (1 - s.physMine) * 0.8
		if s.arch == ArchTank || s.arch == ArchTankSupport {
			v += ex.MagicAmp * 1500
		}
		value += v * fit
	}

	// Passive: the part of the price not explained by stats, scaled by how well
	// the item's stats suit this player and by how relevant its effect is now.
	passive := math.Max(float64(it.Cost)*0.15, float64(it.Cost)-nominal)
	if ex.APAmp > 0 || ex.DmgAmp > 0 || ex.TankAmp > 0 || ex.ArmorShred > 0 || ex.MRShred > 0 {
		passive *= 0.5
	}
	pmul := 1.0
	best := -1.0
	for f, flat := range flatCounter {
		if it.Feat&f == 0 {
			continue
		}
		in := s.ints[f]
		if in > best {
			best = in
		}
		cv := flat * in * fit
		value += cv
		if s.labelOK(f) {
			if l := s.featureLabel(f); l != "" {
				reasons = append(reasons, reason{l, cv + passive*fit*1.6*in*0.5})
			}
		}
	}
	if best >= 0 {
		pmul = 0.4 + 1.6*math.Min(best, 1.2)
	}
	value += passive * fit * pmul

	// Situational defensive items only earn their slot when the threat is real.
	const situational = FLifeline | FSpellShield | FStasis | FRevive | FMagicDR | FCleanse | FAntiCrit | FAntiAuto | FAntiAttackSpeed
	if it.Feat&situational != 0 {
		in := 0.0
		for f := range flatCounter {
			if it.Feat&f != 0 && f&situational != 0 && s.ints[f] > in {
				in = s.ints[f]
			}
		}
		value *= 0.85 + 0.15*math.Min(1, in/0.7)
	}

	if syn := styleSynergy[s.arch]; syn != nil {
		m := 1.0
		for _, p := range it.Passives {
			if v := syn[p]; v > m {
				if p == "manaflow" && s.U[StMana] == 0 {
					continue
				}
				m = v
			}
		}
		value *= m
	}
	if s.early && (ex.APAmp > 0 || st[StCritDmg] > 0) {
		value *= 0.85
	}
	if s.core[it.ID] {
		value *= 1.6
		reasons = append(reasons, reason{"your core build", value})
	}
	if m := s.meta; m != nil {
		boost, label := s.metaBoost(it.ID)
		value *= 1 + boost
		if label != "" && boost > 0 {
			if wr, ok := m.WR[it.ID]; ok && wr > 0 {
				label += fmt.Sprintf(" (%d%% WR)", int(math.Round(wr*100)))
			}
			reasons = append(reasons, reason{label, boost * value * 0.9})
		}
	}
	if a := s.affinity[it.ID]; a > 0 {
		value *= 1 + 0.25*a
		reasons = append(reasons, reason{"your usual build", 0.25 * a * value})
	}
	return value, reasons
}

// metaBoost is how much community build data lifts (or lowers) an item now.
//
// With build slots, it follows the build order: the options players build as
// their next item get the full boost, the item after that half of it, and
// items usually built two or more slots later are held back a little, so a
// 5th item isn't suggested second. Strong counters can still win, since they
// score on what the enemy team does. Without slots, popular items get a
// smaller, flat boost.
func (s *scorer) metaBoost(id int) (float64, string) {
	m := s.meta
	p := m.Pop[id]
	slot, hasSlot := m.Slot[id]
	if len(m.Slot) == 0 || !hasSlot {
		if p <= 0 {
			return 0, ""
		}
		w := 0.4
		if len(m.Slot) > 0 {
			w = 0.2 // popular, but not part of the build order (boots, starters)
		}
		label := "popular on " + m.Source
		if p >= 0.6 {
			label = m.Source + " core"
		}
		return w * p, label
	}
	boost := 0.8 * s.slotFit(id)
	if d := slot - float64(s.nextSlot); d > 1.5 {
		boost = -0.25 * math.Min(1, (d-1.5)/1.5)
	}
	n := int(math.Round(slot))
	label := fmt.Sprintf("%s item %d", m.Source, n)
	if slot < 3.5 {
		label = m.Source + " core"
	}
	return boost, label
}

// scored is a candidate item with its value, its score (value per gold) and
// why it scored that way.
type scored struct {
	it      *Item
	value   float64
	score   float64
	reasons []reason
}

// slotFit is 1 for items players build as their next item (or are overdue
// on), 0..1 for the item after that, and 0 for later or unknown items.
func (s *scorer) slotFit(id int) float64 {
	if s.meta == nil {
		return 0
	}
	slot, ok := s.meta.Slot[id]
	if !ok {
		return 0
	}
	switch d := slot - float64(s.nextSlot); {
	case d <= 0.5:
		return 1
	case d <= 1.5:
		return 1.5 - d
	}
	return 0
}

// metaFloor lifts the items players build next to near the top of the list.
// The engine's own scoring rates items by stats and the enemy team, which can
// badly undervalue a champion-specific choice (Warmog's on Soraka), so a
// multiplier alone isn't enough. The floor is 80-110% of the best score, by
// popularity and fit, so a strong counter can still come first.
func (s *scorer) metaFloor(all []scored) {
	if s.meta == nil || len(s.meta.Slot) == 0 {
		return
	}
	top := 0.0
	for _, sc := range all {
		top = math.Max(top, sc.score)
	}
	for i := range all {
		f := s.slotFit(all[i].it.ID)
		if f <= 0 {
			continue
		}
		p := s.meta.Pop[all[i].it.ID]
		if p <= 0 {
			p = 0.5
		}
		if floor := top * (0.8 + 0.3*f*p); all[i].score < floor {
			all[i].score = floor
		}
	}
}

// pinCore puts the items the player pinned in "core" above everything else
// until they own them, as the settings promise. Among themselves they keep
// their order.
func (s *scorer) pinCore(all []scored) {
	top := 0.0
	for _, sc := range all {
		if !s.core[sc.it.ID] {
			top = math.Max(top, sc.score)
		}
	}
	for i := range all {
		if s.core[all[i].it.ID] {
			all[i].score += top
		}
	}
}

// offMeta reports whether a pick is outside the community builds for this
// champion, so the overlay can say so.
func (s *scorer) offMeta(id int) bool {
	m := s.meta
	if m == nil || len(m.Slot) == 0 || s.core[id] {
		return false // items pinned in "core" are the player's own choice
	}
	_, hasSlot := m.Slot[id]
	return !hasSlot && m.Pop[id] == 0
}

func sign(v float64) float64 {
	if v > 0 {
		return 1
	}
	return 0
}

func (c *Catalog) completedFor(mapID int, strictIDs bool) []*Item {
	var out []*Item
	for _, it := range c.Items {
		if !it.Purchasable || it.Restricted || it.Consumable || it.Boots || it.Cost < 2000 {
			continue
		}
		if !it.Maps[mapID] {
			continue
		}
		if strictIDs && it.ID >= 10000 {
			continue
		}
		if c.isSupportItem(it.ID) {
			continue
		}
		upgradable := false
		for _, n := range it.Into {
			if nx := c.Items[n]; nx != nil && nx.Purchasable && nx.Maps[mapID] {
				upgradable = true
				break
			}
		}
		if upgradable {
			continue
		}
		out = append(out, it)
	}
	return out
}

func (c *Catalog) bootsFor(mapID int) []*Item {
	var out []*Item
	for _, it := range c.Items {
		if !it.Boots || !it.Purchasable || it.Restricted || it.ID >= 10000 || !it.Maps[mapID] || it.ID == 1001 {
			continue
		}
		tier2 := false
		for _, f := range it.From {
			if f == 1001 {
				tier2 = true
			}
		}
		if tier2 {
			out = append(out, it)
		}
	}
	return out
}

// Recommend computes the overlay content for the current game state.
func (c *Catalog) Recommend(g *LiveGame, opt Options) *Result {
	res := &Result{GameTime: g.GameData.GameTime}
	if opt.MaxItems <= 0 {
		opt.MaxItems = 3
	}
	switch strings.ToUpper(g.GameData.GameMode) {
	case "CHERRY", "TFT", "STRAWBERRY":
		res.Note = "This game mode isn't supported"
		return res
	}
	self := g.Self()
	if self == nil {
		res.Note = "Waiting for player data"
		return res
	}
	mapID := g.GameData.MapNumber
	if mapID == 0 {
		mapID = 11
	}
	res.Supported = true
	res.ChampID = self.ChampID()
	ch := c.Champion(res.ChampID)
	res.Champion = self.ChampionName
	if ch != nil {
		res.Champion = ch.Name
	}
	ap := &g.ActivePlayer
	res.Level = ap.Level
	if res.Level < 1 {
		res.Level = self.Level
	}
	if res.Level < 1 {
		res.Level = 1
	}
	res.Gold = int(ap.CurrentGold)

	// What the player owns.
	var owned []int
	var myStats Stats
	ownedGroups := map[string][]int{} // unique group -> owned items that have it
	ownedSet := map[int]bool{}
	support := strings.EqualFold(self.Position, "UTILITY")
	if pos := strings.ToUpper(strings.TrimSpace(self.Position)); (pos == "" || pos == "NONE") && ch != nil && len(ch.Tags) > 0 && ch.Tags[0] == "Support" {
		support = true // support champions without a lane assignment (blind pick, customs)
	}
	used := 0
	hasBoots := false
	myArKeep, myMKeep := 1.0, 1.0
	var myLeth, myMPen float64
	for _, li := range self.Items {
		if li.Slot >= 0 && li.Slot <= 5 {
			used++
		}
		it := c.Items[li.ItemID]
		if it == nil || it.Consumable {
			continue
		}
		n := li.Count
		if n < 1 {
			n = 1
		}
		for i := 0; i < n; i++ {
			owned = append(owned, it.ID)
			myStats.Add(it.Stats)
			myLeth += it.Stats[StLethality]
			myMPen += it.Stats[StMagicPen]
			myArKeep *= 1 - it.Stats[StArmorPen]/100
			myMKeep *= 1 - it.Stats[StMagicPenPct]/100
		}
		ownedSet[it.ID] = true
		if base, ok := c.Transforms[it.ID]; ok {
			ownedSet[base] = true // owning Diadem of Songs means Whispering Circlet is done
		}
		for _, gname := range uniqueGroups(it) {
			ownedGroups[gname] = append(ownedGroups[gname], it.ID)
		}
		if it.Boots && it.ID != 1001 {
			hasBoots = true
		}
		if c.isSupportItem(it.ID) {
			support = true
		}
		if it.Cost >= 2000 && !it.Boots {
			res.Completed = append(res.Completed, it.ID)
		}
	}
	freeSlots := 6 - used

	arch, physMine := pickArchetype(ch, myStats, ap, res.Level, support)
	res.Archetype = arch.String()
	ctx := c.BuildContext(g, self)
	res.EnemyPhys = ctx.Phys
	for _, e := range ctx.Enemies {
		res.Enemies = append(res.Enemies, EnemyView{ChampID: e.ChampID, Name: e.Name, Threat: e.Threat, Phys: e.Phys,
			Fed: e.Fed, K: e.K, D: e.D, A: e.A, Level: e.Level})
	}

	s := &scorer{c: c, ctx: ctx, arch: arch, U: archWeights[arch], physMine: physMine, affinity: opt.Affinity,
		myLeth: myLeth, myMPen: myMPen, myArPct: 1 - myArKeep, myMPct: 1 - myMKeep,
		core: map[int]bool{}, early: len(res.Completed) == 0, nextSlot: len(res.Completed) + 1}
	if opt.Meta != nil && len(opt.Meta.Pop) > 0 {
		s.meta = opt.Meta
		res.MetaSource = opt.Meta.Source
	}
	for _, id := range opt.Core {
		s.core[id] = true
	}
	if ch == nil || ch.Partype != "Mana" {
		s.U[StMana] = 0
		s.U[StManaRegen] = 0
	}
	// Context multipliers.
	for i := range s.C {
		s.C[i] = 1
	}
	s.C[StArmor] = 0.35 + 1.3*ctx.Phys
	s.C[StMR] = 0.35 + 1.3*(1-ctx.Phys)
	s.C[StTenacity] = 0.4 + 1.2*ctx.CC
	s.C[StSlowResist] = 0.6 + 0.6*ctx.CC
	for _, st := range []Stat{StLifeSteal, StOmnivamp, StHealthRegen, StHealShield} {
		s.C[st] = 1 - 0.35*ctx.EnemyAntiHeal
	}
	s.C[StCrit] = 1 - 0.2*ctx.EnemyAntiCrit
	s.C[StCritDmg] = s.C[StCrit]
	s.C[StAS] = 1 - 0.12*ctx.EnemyAntiAS
	cs := ap.ChampionStats
	if arch.defensive() && cs.MaxHealth > 0 {
		mA := clamp(math.Sqrt(0.1335*cs.MaxHealth/(100+cs.Armor)), 0.7, 1.5)
		mM := clamp(math.Sqrt(0.1335*cs.MaxHealth/(100+cs.MagicResist)), 0.7, 1.5)
		s.C[StArmor] *= mA
		s.C[StMR] *= mM
		s.C[StHP] *= clamp(1/math.Sqrt(mA*mM), 0.75, 1.35)
	}
	if arch.squishy() {
		k := 0.85 + 0.5*ctx.Burst
		s.C[StHP] *= k
		s.C[StArmor] *= k
		s.C[StMR] *= k
	}
	// Offensive budget used to value penetration.
	lvl := float64(res.Level)
	crit := cs.CritChance
	if crit <= 1 {
		crit *= 100
	}
	s.myCrit = math.Max(crit, myStats[StCrit])
	s.physOff = math.Max(cs.AttackDamage, 50) * 35
	if arch == ArchMarksman {
		s.physOff += s.myCrit*40*0.8 + math.Max(0, cs.AttackSpeed-0.65)*100*25*0.6
	} else {
		s.physOff += 250 + 45*lvl
	}
	s.magOff = (cs.AbilityPower + 40 + 12*lvl) * 20
	s.myAP = cs.AbilityPower

	// Situational intensities.
	s.ownAH = c.ownsFeature(self.Items, FAntiHeal)
	iHeal := ctx.Heal
	if s.ownAH {
		iHeal = 0
	} else if ctx.AllyAntiHeal {
		iHeal *= 0.5
	}
	s.ints = map[Feature]float64{
		FAntiHeal:        iHeal,
		FAntiShield:      ctx.Shield,
		FAntiCrit:        clamp(ctx.CritT*2.2, 0, 1.2),
		FAntiAttackSpeed: clamp(ctx.AutoPhys*1.4, 0, 1.2),
		FAntiAuto:        clamp(ctx.AutoPhys*1.6, 0, 1.2),
		FStasis:          clamp(ctx.Burst*1.5, 0, 1.2),
		FSpellShield:     clamp(math.Max(ctx.BurstMagic*1.8, ctx.CC*0.8), 0, 1.2),
		FLifeline:        clamp(ctx.Burst*1.3, 0, 1.2),
		FCleanse:         clamp(math.Max(ctx.Suppress, (ctx.CC-0.35)*1.2), 0, 1.2),
		FTankBust:        clamp(ctx.Tank*1.3, 0, 1.2),
		FArmorShred:      clamp(ctx.Tank*1.3, 0, 1.2),
		FMRShred:         clamp(ctx.Tank*1.3, 0, 1.2),
		FMagicDR:         clamp((1-ctx.Phys)*1.2, 0, 1.2),
		FRevive:          clamp(0.2+ctx.GameMinutes/35, 0.2, 1.0),
		FTeamUtility:     0.15,
		FAllyCleanse:     0.1,
		FOnHit:           0,
	}
	switch arch {
	case ArchADOnHit, ArchAPOnHit:
		s.ints[FOnHit] = 1
	case ArchMarksman:
		s.ints[FOnHit] = 0.25
	}
	if arch.support() {
		s.ints[FTeamUtility] = 1
		s.ints[FAllyCleanse] = clamp(ctx.CC, 0.3, 1)
	}

	// Score completed items.
	cands := c.completedFor(mapID, true)
	if len(cands) < 30 {
		cands = c.completedFor(mapID, false)
	}
	var all []scored
	for _, it := range cands {
		if ownedSet[it.ID] {
			continue
		}
		clash := false
		for _, gname := range uniqueGroups(it) {
			for _, o := range ownedGroups[gname] {
				// A component the item is built from gets used up, so
				// owning Tear doesn't block Manamune or Whispering Circlet.
				if !c.buildsFrom(it, o) {
					clash = true
				}
			}
		}
		if clash {
			continue
		}
		v, rs := s.score(it)
		all = append(all, scored{it, v, v / float64(it.Cost), rs})
	}
	s.metaFloor(all)
	s.pinCore(all)
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score == all[j].score {
			return all[i].it.ID < all[j].it.ID
		}
		return all[i].score > all[j].score
	})
	pickedGroups := map[string]bool{}
	for _, sc := range all {
		if len(res.Picks) >= opt.MaxItems {
			break
		}
		clash := false
		for _, gname := range uniqueGroups(sc.it) {
			if pickedGroups[gname] {
				clash = true
			}
		}
		if clash {
			continue
		}
		for _, gname := range uniqueGroups(sc.it) {
			pickedGroups[gname] = true
		}
		reasons := topReasons(sc.reasons, arch)
		if s.offMeta(sc.it.ID) && len(reasons) > 0 {
			reasons[0] = "off-meta: " + reasons[0]
		}
		res.Picks = append(res.Picks, Pick{ID: sc.it.ID, Name: sc.it.Name, Cost: sc.it.Cost,
			Remaining: c.RemainingCost(sc.it.ID, owned), Value: sc.value, Score: sc.score,
			Reasons: reasons})
	}

	// Boots.
	if !hasBoots && res.ChampID != "Cassiopeia" && (ctx.GameMinutes >= 5 || res.Level >= 5 || len(res.Completed) > 0) {
		var best *Item
		var bestScore float64
		var bestR []reason
		for _, it := range c.bootsFor(mapID) {
			v, rs := s.score(it)
			sc := v / float64(it.Cost)
			if best == nil || sc > bestScore {
				best, bestScore, bestR = it, sc, rs
			}
		}
		if best != nil {
			res.Boots = &Pick{ID: best.ID, Name: best.Name, Cost: best.Cost, Remaining: c.RemainingCost(best.ID, owned),
				Score: bestScore, Reasons: topReasons(bestR, arch)}
		}
	}

	if len(res.Picks) > 0 {
		res.BuyNow = c.planBuy(c.Items[res.Picks[0].ID], owned, res.Gold, freeSlots)
	}
	res.Counter = s.urgentCounter(res, owned, freeSlots)
	return res
}

func topReasons(rs []reason, arch Archetype) []string {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].gold > rs[j].gold })
	var out []string
	seen := map[string]bool{}
	for _, r := range rs {
		if r.gold < 60 || seen[r.label] {
			continue
		}
		seen[r.label] = true
		out = append(out, r.label)
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		out = append(out, "core "+arch.String()+" stats")
	}
	return out
}

type part struct {
	id, cost, consumed int
}

// missingParts lists every node of target's recipe tree that is not owned yet,
// with the gold each would cost right now (owned sub-components are used).
func (c *Catalog) missingParts(target int, owned []int) []part {
	inv := map[int]int{}
	for _, o := range owned {
		inv[o]++
	}
	count := func(m map[int]int) int {
		n := 0
		for _, v := range m {
			n += v
		}
		return n
	}
	var parts []part
	var walk func(id int)
	walk = func(id int) {
		if inv[id] > 0 {
			inv[id]--
			return
		}
		cp := make(map[int]int, len(inv))
		for k, v := range inv {
			cp[k] = v
		}
		before := count(cp)
		cost := c.remainingCost(id, cp)
		parts = append(parts, part{id, cost, before - count(cp)})
		if it := c.Items[id]; it != nil {
			for _, f := range it.From {
				walk(f)
			}
		}
	}
	walk(target)
	return parts
}

// planBuy picks the best purchase toward target with the gold available now.
func (c *Catalog) planBuy(target *Item, owned []int, gold, freeSlots int) *BuyAdvice {
	if target == nil {
		return nil
	}
	parts := c.missingParts(target.ID, owned)
	if len(parts) == 0 {
		return nil
	}
	whole := parts[0]
	if whole.cost <= gold && freeSlots-1+whole.consumed >= 0 {
		return &BuyAdvice{ID: target.ID, Name: target.Name, Cost: whole.cost, Complete: true, Target: target.Name, Label: "Buy now"}
	}
	var best, cheapest *part
	slotBlocked := false
	for i := 1; i < len(parts); i++ {
		p := &parts[i]
		it := c.Items[p.id]
		if it == nil || !it.Purchasable || p.cost <= 0 {
			continue
		}
		if freeSlots-1+p.consumed < 0 {
			slotBlocked = true
			continue
		}
		if cheapest == nil || p.cost < cheapest.cost {
			cheapest = p
		}
		if p.cost <= gold && (best == nil || p.cost > best.cost) {
			best = p
		}
	}
	name := func(id int) string { return c.Items[id].Name }
	if best != nil {
		return &BuyAdvice{ID: best.id, Name: name(best.id), Cost: best.cost, Target: target.Name, Label: "Buy now"}
	}
	if cheapest != nil {
		return &BuyAdvice{ID: cheapest.id, Name: name(cheapest.id), Cost: cheapest.cost, Target: target.Name,
			Short: cheapest.cost - gold, Label: "Next"}
	}
	if slotBlocked || freeSlots <= 0 {
		return &BuyAdvice{ID: target.ID, Name: target.Name, Cost: whole.cost, Target: target.Name, Label: "Next",
			Note: "inventory full"}
	}
	return &BuyAdvice{ID: target.ID, Name: target.Name, Cost: whole.cost, Target: target.Name,
		Short: whole.cost - gold, Label: "Next"}
}

// urgentCounter suggests a cheap component that counters something now.
func (s *scorer) urgentCounter(res *Result, owned []int, freeSlots int) *BuyAdvice {
	if s.arch.support() {
		return nil
	}
	// Skip only when the top pick already is the counter; otherwise "buy now"
	// would point at a different item while the counter is urgent.
	pickHas := func(f Feature) bool {
		if len(res.Picks) == 0 {
			return false
		}
		it := s.c.Items[res.Picks[0].ID]
		return it != nil && it.Feat&f != 0
	}
	type want struct {
		f     Feature
		min   float64
		label string
	}
	wants := []want{{FAntiHeal, 0.55, "Anti-heal"}, {FCleanse, 0.75, "Cleanse"}}
	for _, w := range wants {
		if s.ints[w.f] < w.min || pickHas(w.f) || s.c.ownsFeature(liveItems(owned), w.f) {
			continue
		}
		if w.f == FCleanse && !(s.arch == ArchMarksman || s.arch == ArchADAssassin || s.arch == ArchAPAssassin || s.arch == ArchAPMage) {
			continue
		}
		// Best completed item with this effect for this player, then its cheapest counter component.
		var best *Item
		bestFit := 0.0
		for _, it := range s.c.Items {
			if it.Feat&w.f == 0 || !it.Purchasable || it.Restricted || it.ID >= 10000 || it.Cost < 2000 {
				continue
			}
			var nom, fn float64
			for st := Stat(0); st < numStats; st++ {
				g := it.Stats[st] * statGold[st]
				nom += g
				fn += g * s.U[st]
			}
			if nom == 0 {
				continue
			}
			if f := fn / nom; f > bestFit {
				best, bestFit = it, f
			}
		}
		if best == nil || bestFit < 0.45 {
			continue
		}
		var comp *Item
		for _, id := range s.c.componentTree(best.ID) {
			it := s.c.Items[id]
			if it != nil && it.Feat&w.f != 0 && it.Purchasable && (comp == nil || it.Cost < comp.Cost) {
				comp = it
			}
		}
		if comp == nil {
			comp = best
		}
		cost := s.c.RemainingCost(comp.ID, owned)
		adv := &BuyAdvice{ID: comp.ID, Name: comp.Name, Cost: cost, Target: best.Name, Label: w.label}
		if cost > res.Gold {
			adv.Short = cost - res.Gold
		}
		if freeSlots <= 0 {
			adv.Note = "inventory full"
		}
		return adv
	}
	return nil
}

func liveItems(ids []int) []LiveItem {
	out := make([]LiveItem, len(ids))
	for i, id := range ids {
		out[i] = LiveItem{ItemID: id, Count: 1}
	}
	return out
}
