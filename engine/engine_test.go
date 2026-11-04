package engine

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

var (
	catOnce sync.Once
	catVal  *Catalog
	catErr  error
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	catOnce.Do(func() {
		items, err := os.ReadFile("testdata/item.json")
		if err != nil {
			catErr = err
			return
		}
		champs, err := os.ReadFile("testdata/champion.json")
		if err != nil {
			catErr = err
			return
		}
		catVal, catErr = LoadCatalog(items, champs)
	})
	if catErr != nil {
		t.Fatal(catErr)
	}
	return catVal
}

type pl struct {
	champ    string
	team     string
	level    int
	k, d, a  int
	pos      string
	keystone int
	items    []int
	smite    bool
}

func mkGame(me pl, gold float64, minutes float64, others ...pl) *LiveGame {
	g := &LiveGame{}
	g.GameData.GameMode = "CLASSIC"
	g.GameData.MapNumber = 11
	g.GameData.GameTime = minutes * 60
	add := func(p pl, name string) {
		lp := LivePlayer{ChampionName: p.champ, RawChampionName: "game_character_displayname_" + p.champ,
			Level: p.level, Position: p.pos, Team: p.team, RiotID: name, SummonerName: name}
		lp.Scores.Kills, lp.Scores.Deaths, lp.Scores.Assists = p.k, p.d, p.a
		lp.Runes.Keystone.ID = p.keystone
		for i, id := range p.items {
			lp.Items = append(lp.Items, LiveItem{ItemID: id, Count: 1, Slot: i})
		}
		if p.smite {
			lp.SummonerSpells.One.RawDisplayName = "GeneratedTip_SummonerSpell_SummonerSmite_DisplayName"
		}
		g.AllPlayers = append(g.AllPlayers, lp)
	}
	add(me, "Me#EUNE")
	for i, o := range others {
		add(o, fmt.Sprintf("P%d#EUW", i))
	}
	g.ActivePlayer.RiotID = "Me#EUNE"
	g.ActivePlayer.Level = me.level
	g.ActivePlayer.CurrentGold = gold
	return g
}

// withStats fills activePlayer champion stats from base stats + items, like the game would.
func withStats(c *Catalog, g *LiveGame) *LiveGame {
	self := g.Self()
	ch := c.Champion(self.ChampID())
	var st Stats
	for _, li := range self.Items {
		if it := c.Items[li.ItemID]; it != nil {
			st.Add(it.Stats)
		}
	}
	b := ch.Base
	lv := self.Level
	cs := &g.ActivePlayer.ChampionStats
	cs.AttackDamage = growth(b["attackdamage"], b["attackdamageperlevel"], lv) + st[StAD]
	cs.AbilityPower = st[StAP]
	cs.Armor = growth(b["armor"], b["armorperlevel"], lv) + st[StArmor]
	cs.MagicResist = growth(b["spellblock"], b["spellblockperlevel"], lv) + st[StMR]
	cs.MaxHealth = growth(b["hp"], b["hpperlevel"], lv) + st[StHP]
	cs.CritChance = st[StCrit] / 100
	cs.AttackSpeed = b["attackspeed"] * (1 + st[StAS]/100)
	return g
}

func dump(t *testing.T, title string, r *Result) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "== %s: %s (%s) lvl %d gold %d, enemy phys %.0f%%\n", title, r.Champion, r.Archetype, r.Level, r.Gold, r.EnemyPhys*100)
	for _, e := range r.Enemies {
		fmt.Fprintf(&b, "   enemy %-12s threat %.2f phys %.2f fed=%v\n", e.Name, e.Threat, e.Phys, e.Fed)
	}
	for _, p := range r.Picks {
		fmt.Fprintf(&b, "   PICK %-26s %5d (rem %5d) score %.3f  %v\n", p.Name, p.Cost, p.Remaining, p.Score, p.Reasons)
	}
	if r.Boots != nil {
		fmt.Fprintf(&b, "   BOOTS %s %v\n", r.Boots.Name, r.Boots.Reasons)
	}
	if r.BuyNow != nil {
		fmt.Fprintf(&b, "   %s: %s (%dg) toward %s complete=%v short=%d %s\n", r.BuyNow.Label, r.BuyNow.Name, r.BuyNow.Cost, r.BuyNow.Target, r.BuyNow.Complete, r.BuyNow.Short, r.BuyNow.Note)
	}
	if r.Counter != nil {
		fmt.Fprintf(&b, "   %s: %s (%dg) for %s short=%d\n", r.Counter.Label, r.Counter.Name, r.Counter.Cost, r.Counter.Target, r.Counter.Short)
	}
	t.Log(b.String())
}

func pickNames(r *Result) []string {
	var n []string
	for _, p := range r.Picks {
		n = append(n, p.Name)
	}
	return n
}

func hasPick(r *Result, name string) bool {
	for _, p := range r.Picks {
		if p.Name == name {
			return true
		}
	}
	return false
}

func TestItemParsing(t *testing.T) {
	c := testCatalog(t)
	ie := c.Items[3031]
	if ie.Stats[StAD] != 75 || ie.Stats[StCrit] != 25 || ie.Stats[StCritDmg] != 30 {
		t.Fatalf("IE stats wrong: %+v", ie.Stats)
	}
	if v := c.Items[3135]; v.Stats[StMagicPenPct] != 40 || v.Stats[StAP] != 95 {
		t.Fatalf("Void Staff stats wrong")
	}
	if c.Items[3020].Stats[StMagicPen] != 12 {
		t.Fatalf("Sorc flat pen wrong")
	}
	checks := map[int]Feature{
		3033: FAntiHeal, 3075: FAntiHeal | FAntiAuto, 3165: FAntiHeal, 6609: FAntiHeal, 3123: FAntiHeal, 3916: FAntiHeal, 3076: FAntiHeal,
		6695: FAntiShield, 3143: FAntiCrit, 3110: FAntiAttackSpeed, 3157: FStasis, 3102: FSpellShield, 3814: FSpellShield,
		3053: FLifeline, 3156: FLifeline | FMagicDR, 6673: FLifeline, 3139: FCleanse, 3140: FCleanse, 3026: FRevive,
		6653: FTankBust, 3153: FTankBust, 3036: FTankBust, 3071: FArmorShred, 8010: FMRShred, 8020: FMRShred,
		4401: FMagicDR, 2504: FMagicDR, 3190: FTeamUtility, 3107: FTeamUtility, 3222: FAllyCleanse, 3047: FAntiAuto,
	}
	for id, f := range checks {
		it := c.Items[id]
		if it.Feat&f != f {
			t.Errorf("%s (%d) missing features %b, has %b (passives %v)", it.Name, id, f, it.Feat, it.Passives)
		}
	}
	// No false anti-heal on common items.
	for _, id := range []int{3031, 3089, 3157, 3084, 6672} {
		if c.Items[id].Feat&FAntiHeal != 0 {
			t.Errorf("%s wrongly anti-heal", c.Items[id].Name)
		}
	}
	if c.Items[3084].Feat&FTankBust != 0 {
		t.Errorf("Heartsteel should not count as tank-busting")
	}
}

func TestRemainingCost(t *testing.T) {
	c := testCatalog(t)
	// Mortal Reminder with Executioner's Calling owned.
	full := c.RemainingCost(3033, nil)
	part := c.RemainingCost(3033, []int{3123})
	if full != c.Items[3033].Cost || part != full-c.Items[3123].Cost {
		t.Fatalf("remaining cost wrong: full %d part %d", full, part)
	}
}

func TestHealingTeamGetsAntiHeal(t *testing.T) {
	c := testCatalog(t)
	g := withStats(c, mkGame(pl{champ: "Jinx", team: "ORDER", level: 11, pos: "BOTTOM", items: []int{3032, 3006, 1018}}, 900, 18,
		pl{champ: "Soraka", team: "CHAOS", level: 10, pos: "UTILITY", items: []int{3869, 6617, 3158}},
		pl{champ: "Aatrox", team: "CHAOS", level: 12, k: 5, d: 2, keystone: ksConqueror, items: []int{6692, 3047, 6333}},
		pl{champ: "Vladimir", team: "CHAOS", level: 11, k: 3, d: 3, items: []int{4633, 3020}},
		pl{champ: "Briar", team: "CHAOS", level: 11, k: 2, d: 4, items: []int{3071, 3111}},
		pl{champ: "Ezreal", team: "CHAOS", level: 10, k: 1, d: 3, items: []int{3078, 3158}},
	))
	r := c.Recommend(g, Options{})
	dump(t, "healers", r)
	if !hasPick(r, "Mortal Reminder") && (r.Counter == nil || r.Counter.Name != "Executioner's Calling") {
		t.Fatalf("expected anti-heal (Mortal Reminder or Executioner's), got %v counter %+v", pickNames(r), r.Counter)
	}
}

func TestBurstMageWantsZhonya(t *testing.T) {
	c := testCatalog(t)
	g := withStats(c, mkGame(pl{champ: "Ahri", team: "ORDER", level: 11, pos: "MIDDLE", items: []int{2503, 3020}}, 1400, 17,
		pl{champ: "Zed", team: "CHAOS", level: 13, k: 8, d: 1, pos: "MIDDLE", keystone: ksElectrocute, items: []int{3142, 6696, 3047}},
		pl{champ: "Talon", team: "CHAOS", level: 11, k: 4, d: 2, pos: "JUNGLE", items: []int{6698, 3158}},
		pl{champ: "Malphite", team: "CHAOS", level: 10, pos: "TOP", items: []int{3068, 3047}},
		pl{champ: "Leona", team: "CHAOS", level: 9, pos: "UTILITY", items: []int{3871, 3190}},
		pl{champ: "Jinx", team: "CHAOS", level: 10, pos: "BOTTOM", items: []int{3032, 3006}},
	))
	r := c.Recommend(g, Options{})
	dump(t, "burst", r)
	if !hasPick(r, "Zhonya's Hourglass") {
		t.Fatalf("expected Zhonya's, got %v", pickNames(r))
	}
}

func TestTankVsCritAutoAttackers(t *testing.T) {
	c := testCatalog(t)
	g := withStats(c, mkGame(pl{champ: "Malphite", team: "ORDER", level: 12, pos: "TOP", items: []int{3068}}, 1200, 20,
		pl{champ: "Yasuo", team: "CHAOS", level: 13, k: 6, d: 2, items: []int{3046, 3031, 3006}},
		pl{champ: "Jinx", team: "CHAOS", level: 12, k: 5, d: 1, keystone: ksLethalTempo, items: []int{3032, 3031, 3006}},
		pl{champ: "Tryndamere", team: "CHAOS", level: 12, k: 3, d: 3, items: []int{3153, 3031}},
		pl{champ: "MasterYi", team: "CHAOS", level: 12, k: 4, d: 3, items: []int{3153, 3124, 3006}, smite: true},
		pl{champ: "Nami", team: "CHAOS", level: 10, pos: "UTILITY", items: []int{3870, 6620}},
	))
	r := c.Recommend(g, Options{})
	dump(t, "tank vs crit", r)
	n := strings.Join(pickNames(r), ",")
	if !strings.Contains(n, "Randuin") && !strings.Contains(n, "Frozen Heart") && !strings.Contains(n, "Thornmail") {
		t.Fatalf("expected anti-auto armor items, got %v", n)
	}
	if r.Boots == nil || r.Boots.Name != "Plated Steelcaps" {
		t.Fatalf("expected Plated Steelcaps, got %+v", r.Boots)
	}
}

func TestMagePenTankyVsSquishy(t *testing.T) {
	c := testCatalog(t)
	tanky := withStats(c, mkGame(pl{champ: "Syndra", team: "ORDER", level: 14, pos: "MIDDLE", items: []int{6655, 3020, 4645}}, 1000, 26,
		pl{champ: "Galio", team: "CHAOS", level: 14, items: []int{4401, 3111, 3065}},
		pl{champ: "Ornn", team: "CHAOS", level: 15, items: []int{6665, 3068, 3111}},
		pl{champ: "Sejuani", team: "CHAOS", level: 13, items: []int{6664, 2504, 3047}, smite: true},
		pl{champ: "Braum", team: "CHAOS", level: 12, pos: "UTILITY", items: []int{3190, 3109, 3111}},
		pl{champ: "Jinx", team: "CHAOS", level: 14, items: []int{3032, 3031, 3006}},
	))
	r := c.Recommend(tanky, Options{})
	dump(t, "mage vs tanks", r)
	if !hasPick(r, "Void Staff") && !hasPick(r, "Cryptbloom") {
		t.Fatalf("expected percent magic pen vs MR stackers, got %v", pickNames(r))
	}
	squishy := withStats(c, mkGame(pl{champ: "Syndra", team: "ORDER", level: 14, pos: "MIDDLE", items: []int{6655, 3020, 4645}}, 1000, 26,
		pl{champ: "Lux", team: "CHAOS", level: 14, items: []int{6655, 3020}},
		pl{champ: "Zed", team: "CHAOS", level: 15, items: []int{3142, 6696, 3047}},
		pl{champ: "Ahri", team: "CHAOS", level: 13, items: []int{6655, 3089}},
		pl{champ: "Yuumi", team: "CHAOS", level: 12, pos: "UTILITY", items: []int{6617, 3876}},
		pl{champ: "Jinx", team: "CHAOS", level: 14, items: []int{3032, 3031, 3006}},
	))
	r2 := c.Recommend(squishy, Options{})
	dump(t, "mage vs squishies", r2)
	vTank, vSq := scoreOf(r, "Void Staff"), scoreOf(r2, "Void Staff")
	if vTank > 0 && vSq > 0 && vTank <= vSq {
		t.Fatalf("Void Staff should score higher vs tanks (%.3f) than squishies (%.3f)", vTank, vSq)
	}
}

func scoreOf(r *Result, name string) float64 {
	for _, p := range r.Picks {
		if p.Name == name {
			return p.Score
		}
	}
	return 0
}

func TestAssassinVsShields(t *testing.T) {
	c := testCatalog(t)
	g := withStats(c, mkGame(pl{champ: "Zed", team: "ORDER", level: 10, pos: "MIDDLE", items: []int{3142, 3158}}, 1200, 16,
		pl{champ: "Lulu", team: "CHAOS", level: 9, pos: "UTILITY", items: []int{3869, 6620}},
		pl{champ: "Janna", team: "CHAOS", level: 9, items: []int{6617}},
		pl{champ: "Karma", team: "CHAOS", level: 10, items: []int{6655}},
		pl{champ: "Jinx", team: "CHAOS", level: 10, items: []int{3032, 3006}},
		pl{champ: "Sett", team: "CHAOS", level: 11, items: []int{3071, 3047}},
	))
	r := c.Recommend(g, Options{})
	dump(t, "assassin vs shields", r)
	if !hasPick(r, "Serpent's Fang") {
		t.Fatalf("expected Serpent's Fang, got %v", pickNames(r))
	}
}

func TestEnchanterSupport(t *testing.T) {
	c := testCatalog(t)
	g := withStats(c, mkGame(pl{champ: "Lulu", team: "ORDER", level: 9, pos: "UTILITY", items: []int{3869, 3158}}, 900, 15,
		pl{champ: "Zed", team: "CHAOS", level: 10, items: []int{3142}},
		pl{champ: "Leona", team: "CHAOS", level: 9, pos: "UTILITY", items: []int{3871}},
		pl{champ: "Jinx", team: "CHAOS", level: 10, items: []int{3032}},
		pl{champ: "Malphite", team: "CHAOS", level: 10, items: []int{3068}},
		pl{champ: "Lillia", team: "CHAOS", level: 10, items: []int{6653}},
	))
	r := c.Recommend(g, Options{})
	dump(t, "enchanter", r)
	if r.Archetype != "Enchanter" {
		t.Fatalf("expected Enchanter, got %s", r.Archetype)
	}
	for _, p := range r.Picks {
		it := c.Items[p.ID]
		if it.Stats[StHealShield] == 0 && it.Stats[StManaRegen] == 0 && it.Feat&FTeamUtility == 0 {
			t.Errorf("non-support item %s for enchanter", p.Name)
		}
	}
}

func TestADCvsSuppression(t *testing.T) {
	c := testCatalog(t)
	g := withStats(c, mkGame(pl{champ: "Jinx", team: "ORDER", level: 13, pos: "BOTTOM", items: []int{3032, 3031, 3006}}, 1500, 24,
		pl{champ: "Malzahar", team: "CHAOS", level: 14, k: 6, d: 2, items: []int{6655, 3020, 3157}},
		pl{champ: "Skarner", team: "CHAOS", level: 13, k: 3, d: 2, items: []int{3068, 3111}, smite: true},
		pl{champ: "Warwick", team: "CHAOS", level: 13, k: 4, d: 3, items: []int{3153, 3047}},
		pl{champ: "Leona", team: "CHAOS", level: 11, pos: "UTILITY", items: []int{3871, 3190}},
		pl{champ: "Caitlyn", team: "CHAOS", level: 13, items: []int{3031, 3094, 3006}},
	))
	r := c.Recommend(g, Options{})
	dump(t, "adc vs suppress", r)
	if !hasPick(r, "Mercurial Scimitar") && (r.Counter == nil || r.Counter.Label != "Cleanse") {
		t.Fatalf("expected a cleanse option, got %v counter %+v", pickNames(r), r.Counter)
	}
}

func TestBruiserVsAP(t *testing.T) {
	c := testCatalog(t)
	g := withStats(c, mkGame(pl{champ: "Darius", team: "ORDER", level: 13, pos: "TOP", items: []int{3071, 3047}}, 1100, 22,
		pl{champ: "Syndra", team: "CHAOS", level: 13, k: 5, d: 2, items: []int{6655, 3020, 4645}},
		pl{champ: "Lux", team: "CHAOS", level: 12, items: []int{6655, 3020}},
		pl{champ: "Brand", team: "CHAOS", level: 12, items: []int{6653, 3020}},
		pl{champ: "Elise", team: "CHAOS", level: 12, items: []int{6655}, smite: true},
		pl{champ: "Teemo", team: "CHAOS", level: 13, items: []int{3115, 6653}},
	))
	r := c.Recommend(g, Options{})
	dump(t, "bruiser vs AP", r)
	if r.EnemyPhys > 0.4 {
		t.Fatalf("enemy phys share too high: %.2f", r.EnemyPhys)
	}
	mr := 0
	for _, p := range r.Picks {
		if c.Items[p.ID].Stats[StMR] > 0 {
			mr++
		}
	}
	if mr == 0 {
		t.Fatalf("expected MR items, got %v", pickNames(r))
	}
}

func TestFirstItemsAreSane(t *testing.T) {
	c := testCatalog(t)
	champs := []string{"Jinx", "Zed", "Ahri", "Darius", "Malphite", "Lulu", "Kayle", "Leona", "Ezreal", "Mordekaiser", "Kaisa", "Yunara", "Locke"}
	enemies := []pl{{champ: "Garen", team: "CHAOS", level: 1}, {champ: "LeeSin", team: "CHAOS", level: 1}, {champ: "Orianna", team: "CHAOS", level: 1},
		{champ: "Caitlyn", team: "CHAOS", level: 1}, {champ: "Thresh", team: "CHAOS", level: 1, pos: "UTILITY"}}
	for _, ch := range champs {
		pos := ""
		if ch == "Lulu" || ch == "Leona" {
			pos = "UTILITY"
		}
		g := withStats(c, mkGame(pl{champ: ch, team: "ORDER", level: 1, pos: pos}, 500, 0.5, enemies...))
		r := c.Recommend(g, Options{})
		dump(t, "first "+ch, r)
		if len(r.Picks) != 3 {
			t.Fatalf("%s: expected 3 picks", ch)
		}
	}
}

func TestPlanBuyDuplicateComponent(t *testing.T) {
	c := testCatalog(t)
	rab := c.Items[3089]
	// Own one Needlessly Large Rod; with 1300 gold the second rod is the right buy.
	adv := c.planBuy(rab, []int{1058}, 1300, 4)
	if adv == nil || adv.ID != 1058 || adv.Cost != 1200 || adv.Complete {
		t.Fatalf("expected second Needlessly Large Rod, got %+v", adv)
	}
	// Enough gold to finish.
	adv = c.planBuy(rab, []int{1058}, 2400, 4)
	if adv == nil || !adv.Complete || adv.Cost != c.RemainingCost(3089, []int{1058}) {
		t.Fatalf("expected completion, got %+v", adv)
	}
	// Not enough for anything.
	adv = c.planBuy(rab, nil, 100, 4)
	if adv == nil || adv.Short <= 0 {
		t.Fatalf("expected shortfall, got %+v", adv)
	}
	// Full inventory, nothing to combine.
	adv = c.planBuy(rab, []int{1001, 1036, 1036, 1036, 1036, 1036}, 5000, 0)
	if adv == nil || adv.Note == "" {
		t.Fatalf("expected inventory full note, got %+v", adv)
	}
}

func TestMetaBuildGuidesCore(t *testing.T) {
	c := testCatalog(t)
	enemies := []pl{{champ: "Garen", team: "CHAOS", level: 6}, {champ: "LeeSin", team: "CHAOS", level: 6},
		{champ: "Orianna", team: "CHAOS", level: 6}, {champ: "Caitlyn", team: "CHAOS", level: 6}, {champ: "Thresh", team: "CHAOS", level: 5, pos: "UTILITY"}}
	g := withStats(c, mkGame(pl{champ: "Ezreal", team: "ORDER", level: 6, pos: "BOTTOM"}, 1100, 8, enemies...))
	plain := c.Recommend(g, Options{})
	m := &Meta{Pop: map[int]float64{3078: 1, 3004: 0.95, 3158: 0.9}, WR: map[int]float64{3078: 0.52}, Source: "OP.GG"}
	r := c.Recommend(g, Options{Meta: m})
	dump(t, "ezreal plain", plain)
	dump(t, "ezreal + meta", r)
	if !hasPick(r, "Trinity Force") || !hasPick(r, "Manamune") {
		t.Fatalf("meta core should be picked, got %v", pickNames(r))
	}
	if r.MetaSource != "OP.GG" {
		t.Fatalf("meta source not reported")
	}
	found := false
	for _, p := range r.Picks {
		for _, why := range p.Reasons {
			if strings.Contains(why, "OP.GG core") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected an OP.GG reason")
	}
}

func TestCountersStillBeatMeta(t *testing.T) {
	c := testCatalog(t)
	g := withStats(c, mkGame(pl{champ: "Jinx", team: "ORDER", level: 11, pos: "BOTTOM", items: []int{3032, 3006, 1018}}, 900, 18,
		pl{champ: "Soraka", team: "CHAOS", level: 10, pos: "UTILITY", items: []int{3869, 6617, 3158}},
		pl{champ: "Aatrox", team: "CHAOS", level: 12, k: 5, d: 2, keystone: ksConqueror, items: []int{6692, 3047, 6333}},
		pl{champ: "Vladimir", team: "CHAOS", level: 11, k: 3, d: 3, items: []int{4633, 3020}},
		pl{champ: "Briar", team: "CHAOS", level: 11, k: 2, d: 4, items: []int{3071, 3111}},
		pl{champ: "Ezreal", team: "CHAOS", level: 10, k: 1, d: 3, items: []int{3078, 3158}},
	))
	m := &Meta{Pop: map[int]float64{3031: 1, 3046: 0.8, 3036: 0.7, 3072: 0.5}, Source: "OP.GG"}
	r := c.Recommend(g, Options{Meta: m})
	dump(t, "healers + meta", r)
	if !hasPick(r, "Mortal Reminder") && (r.Counter == nil || r.Counter.Name != "Executioner's Calling") {
		t.Fatalf("anti-heal lost to meta: %v counter %+v", pickNames(r), r.Counter)
	}
}

// sorakaARAM is what OP.GG's real ARAM answer for Soraka turns into (see
// meta/testdata/opgg_soraka_aram.txt): Moonstone, then Whispering Circlet
// (listed as Diadem of Songs, which it turns into), then Warmog's; Spirit
// Visage and Redemption as 4th/5th items.
var sorakaARAM = &Meta{
	Pop:    map[int]float64{2526: 1, 3065: 0.65, 3083: 1, 3107: 0.65, 3158: 1, 3504: 0.38, 6617: 1},
	WR:     map[int]float64{2526: 0.525, 3065: 0.485, 3083: 0.504, 3107: 0.479, 3158: 0.51, 3504: 0.488, 6617: 0.517},
	Slot:   map[int]float64{2526: 2, 3065: 4.35, 3083: 3.19, 3107: 4.66, 3504: 5, 6617: 1},
	Source: "OP.GG",
}

func sorakaGame(c *Catalog, items []int) *LiveGame {
	g := withStats(c, mkGame(pl{champ: "Soraka", team: "ORDER", level: 9, items: items}, 2400, 9,
		pl{champ: "KSante", team: "CHAOS", level: 9, k: 2, d: 2, items: []int{3068}},
		pl{champ: "Volibear", team: "CHAOS", level: 9, k: 3, d: 2, items: []int{3078}},
		pl{champ: "Ahri", team: "CHAOS", level: 9, k: 2, d: 3, items: []int{6655}},
		pl{champ: "Jinx", team: "CHAOS", level: 9, k: 4, d: 2, items: []int{3031}},
		pl{champ: "Lux", team: "CHAOS", level: 9, k: 1, d: 3, items: []int{6655}},
	))
	g.GameData.GameMode = "KIWI"
	g.GameData.MapNumber = 12
	return g
}

func TestMetaFollowsBuildOrder(t *testing.T) {
	c := testCatalog(t)
	// Moonstone done: the next OP.GG item is Whispering Circlet, then
	// Warmog's; Redemption is a 4th/5th item.
	r := c.Recommend(sorakaGame(c, []int{6617, 3158}), Options{Meta: sorakaARAM})
	dump(t, "soraka after moonstone", r)
	if len(r.Picks) == 0 || r.Picks[0].Name != "Whispering Circlet" {
		t.Fatalf("expected Whispering Circlet next, got %v", pickNames(r))
	}
	if r.Picks[0].Reasons[0] != "OP.GG core (53% WR)" {
		t.Fatalf("expected the OP.GG reason first, got %v", r.Picks[0].Reasons)
	}
	// Circlet done (as Diadem of Songs): Warmog's next.
	r = c.Recommend(sorakaGame(c, []int{6617, 2530, 3158}), Options{Meta: sorakaARAM})
	dump(t, "soraka after diadem", r)
	if r.Picks[0].Name != "Warmog's Armor" {
		t.Fatalf("expected Warmog's after Diadem of Songs, got %v", pickNames(r))
	}
	for _, p := range r.Picks[:2] {
		if p.Name == "Redemption" {
			t.Fatalf("Redemption suggested too early: %v", pickNames(r))
		}
	}
	// The core build done: now the 4th/5th item options come up.
	r = c.Recommend(sorakaGame(c, []int{6617, 2530, 3083, 3158}), Options{Meta: sorakaARAM})
	dump(t, "soraka 4th item", r)
	if !hasPick(r, "Redemption") || !hasPick(r, "Spirit Visage") {
		t.Fatalf("expected Redemption and Spirit Visage as 4th item options, got %v", pickNames(r))
	}
}

func TestOffMetaPickIsLabelled(t *testing.T) {
	c := testCatalog(t)
	m := &Meta{Pop: map[int]float64{3089: 1}, Slot: map[int]float64{3089: 4}, Source: "OP.GG"}
	r := c.Recommend(sorakaGame(c, []int{6617, 3158}), Options{Meta: m})
	dump(t, "soraka off-meta", r)
	found := false
	for _, p := range r.Picks {
		if p.Name != "Rabadon's Deathcap" && strings.HasPrefix(p.Reasons[0], "off-meta: ") {
			found = true
		}
		if p.Name == "Rabadon's Deathcap" && strings.HasPrefix(p.Reasons[0], "off-meta") {
			t.Fatalf("an OP.GG item is not off-meta: %v", p.Reasons)
		}
	}
	if !found {
		t.Fatalf("expected picks outside the OP.GG build to say off-meta")
	}
}

func TestCountersStillBeatBuildOrder(t *testing.T) {
	c := testCatalog(t)
	g := withStats(c, mkGame(pl{champ: "Jinx", team: "ORDER", level: 11, pos: "BOTTOM", items: []int{3032, 3006, 1018}}, 900, 18,
		pl{champ: "Soraka", team: "CHAOS", level: 10, pos: "UTILITY", items: []int{3869, 6617, 3158}},
		pl{champ: "Aatrox", team: "CHAOS", level: 12, k: 5, d: 2, keystone: ksConqueror, items: []int{6692, 3047, 6333}},
		pl{champ: "Vladimir", team: "CHAOS", level: 11, k: 3, d: 3, items: []int{4633, 3020}},
		pl{champ: "Briar", team: "CHAOS", level: 11, k: 2, d: 4, items: []int{3071, 3111}},
		pl{champ: "Ezreal", team: "CHAOS", level: 10, k: 1, d: 3, items: []int{3078, 3158}},
	))
	// The usual 2nd and 3rd items have no anti-heal.
	m := &Meta{Pop: map[int]float64{3032: 1, 3031: 1, 3046: 0.8, 3036: 0.6}, Slot: map[int]float64{3032: 1, 3031: 2, 3046: 3, 3036: 4}, Source: "OP.GG"}
	r := c.Recommend(g, Options{Meta: m})
	dump(t, "healers + build order", r)
	if r.Picks[0].Name != "Infinity Edge" {
		t.Fatalf("expected the usual 2nd item first, got %v", pickNames(r))
	}
	if !hasPick(r, "Mortal Reminder") && (r.Counter == nil || r.Counter.Name != "Executioner's Calling") {
		t.Fatalf("anti-heal lost to the build order: %v counter %+v", pickNames(r), r.Counter)
	}
}

func TestTransforms(t *testing.T) {
	c := testCatalog(t)
	for transformed, base := range map[int]int{2530: 2526, 3040: 3003, 3042: 3004, 3121: 3119} {
		if got := c.Buyable(transformed); got != base {
			t.Fatalf("Buyable(%d %s) = %d, want %d", transformed, c.Items[transformed].Name, got, base)
		}
	}
	if c.Buyable(3089) != 3089 || c.Buyable(3070) != 3070 {
		t.Fatalf("shop items buy as themselves")
	}
	// Owning Diadem of Songs means Whispering Circlet is done.
	r := c.Recommend(sorakaGame(c, []int{6617, 2530, 3158}), Options{Meta: sorakaARAM, MaxItems: 40})
	if hasPick(r, "Whispering Circlet") {
		t.Fatalf("Whispering Circlet suggested while owning Diadem of Songs")
	}
}

func TestComponentDoesNotBlockItsItem(t *testing.T) {
	c := testCatalog(t)
	// Tear of the Goddess has the unique Manaflow passive, like the items built from it.
	r := c.Recommend(sorakaGame(c, []int{6617, 3158, 3070}), Options{Meta: sorakaARAM})
	dump(t, "soraka with tear", r)
	if r.Picks[0].Name != "Whispering Circlet" {
		t.Fatalf("owning Tear must not block Whispering Circlet, got %v", pickNames(r))
	}
	// But a finished Manaflow item still blocks another one.
	r = c.Recommend(sorakaGame(c, []int{6617, 3158, 3004}), Options{Meta: sorakaARAM, MaxItems: 40})
	if hasPick(r, "Whispering Circlet") {
		t.Fatalf("Manamune and Whispering Circlet share Manaflow, got %v", pickNames(r))
	}
}
