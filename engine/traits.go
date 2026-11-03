package engine

import (
	"strconv"
	"strings"
)

// Trait describes how a champion typically plays, on 0..1 scales.
type Trait struct {
	Phys      float64 // share of damage that is physical
	Heal      float64 // self / team healing
	Shield    float64 // shields on self / allies
	CC        float64 // crowd control
	Burst     float64 // one-combo kill pressure
	Auto      float64 // reliance on basic attacks
	Tank      float64 // natural durability
	Suppress  bool    // ultimate worth a cleanse item (suppression or death mark)
	Enchanter bool    // builds heal/shield support items when supporting
	Style     byte    // build style override: 'K' crit, 'O' on-hit, 'X' AD caster, 'L' lethality
}

// traitTable holds hand-tuned traits. Omitted fields fall back to tag defaults.
// Keys: p=physical share, h=heal, s=shield, c=cc, b=burst, a=auto, t=tank, Q=cleanse-worthy ult, E=enchanter,
// K/O/X/L = build style (crit, on-hit, AD caster, lethality) when it differs from the class default.
var traitTable = map[string]string{
	"Aatrox": "p.95 h1 c.45 b.35 a.3 t.55", "Ahri": "p.05 c.55 b.65 a.1", "Akali": "p.25 b.85 c.1 a.2",
	"Akshan": "p.95 b.5 a.7 K", "Alistar": "p.1 c1 h.4 t.9", "Ambessa": "p1 b.65 h.4 c.4 a.45 t.45",
	"Amumu": "p.1 c.95 t.85", "Anivia": "p.05 c.65 b.35", "Annie": "p.05 c.75 b.65 s.3",
	"Aphelios": "p.95 a.9 h.3 b.35", "Ashe": "p1 a.95 c.65", "AurelionSol": "p.05 b.4 c.35",
	"Aurora": "p.1 b.6 c.45", "Azir": "p.1 a.55 c.5 b.4", "Bard": "p.1 c.6 h.3 E",
	"Belveth": "p1 a.9 h.5 t.4 c.3 O", "Blitzcrank": "p.15 c.75 s.3 t.8", "Brand": "p.05 b.5 c.35",
	"Braum": "p.2 c.8 s.2 t.85", "Briar": "p.95 h.9 a.6 b.5 c.3 t.4", "Caitlyn": "p1 a.85 b.3 c.35",
	"Camille": "p.85 b.5 a.45 s.3 c.5", "Cassiopeia": "p.05 c.5 b.35 h.1", "Chogath": "p.25 c.6 t.9 b.4 h.2",
	"Corki": "p.35 a.5 b.45 X", "Darius": "p.95 h.3 c.35 a.35 t.55 b.45", "Diana": "p.1 b.6 s.3 c.5 t.3",
	"DrMundo": "p.5 h.9 t.9 c.2 a.4", "Draven": "p1 a.95 b.45", "Ekko": "p.1 b.6 h.35 s.2 c.4",
	"Elise": "p.1 b.5 c.45 h.2", "Evelynn": "p.1 b.85 c.3", "Ezreal": "p.7 a.55 b.35 X",
	"Fiddlesticks": "p.05 h.6 c.65 b.5", "Fiora": "p1 h.6 a.65 b.4 t.35", "Fizz": "p.1 b.85 c.3",
	"Galio": "p.1 c.8 t.75 s.2", "Gangplank": "p.85 b.55 a.4 h.3 X", "Garen": "p.9 h.3 b.4 a.35 t.55 c.3",
	"Gnar": "p.9 a.55 c.65 t.55", "Gragas": "p.15 c.6 b.45 h.3 t.5", "Graves": "p1 a.65 b.5",
	"Gwen": "p.1 h.5 a.45 b.4 t.35", "Hecarim": "p.95 h.3 c.55 b.4 t.5", "Heimerdinger": "p.1 c.45 b.35",
	"Hwei": "p.05 c.6 b.5", "Illaoi": "p.95 h.7 t.6 c.25 a.3", "Irelia": "p.95 h.4 a.6 b.45 c.4 t.35 O",
	"Ivern": "p.1 s.6 c.5 h.1 E", "Janna": "p.15 s.9 c.6 h.2 E", "JarvanIV": "p.95 c.65 b.45 s.2 t.55",
	"Jax": "p.7 a.7 t.5 c.4 b.35", "Jayce": "p.95 b.6 a.45 L", "Jhin": "p1 a.6 b.45 c.3",
	"Jinx": "p1 a.95 b.3 c.3", "KSante": "p.85 t.9 c.65 s.3 b.3", "Kaisa": "p.6 a.7 b.5 s.2 O",
	"Kalista": "p1 a.95 c.4 O", "Karma": "p.1 s.8 c.4 b.35 E", "Karthus": "p.05 b.5",
	"Kassadin": "p.1 b.75", "Katarina": "p.35 b.85", "Kayle": "p.3 a.85 b.3 h.2",
	"Kayn": "p.9 b.6 h.5 t.4", "Kennen": "p.2 c.7 b.5", "Khazix": "p1 b.9",
	"Kindred": "p1 a.7 h.3 K", "Kled": "p1 c.4 a.45 t.5 b.4", "KogMaw": "p.55 a.95 O",
	"Leblanc": "p.05 b.9 c.4", "LeeSin": "p1 b.55 c.45 h.3 s.2 t.4", "Leona": "p.1 c1 t.9",
	"Lillia": "p.05 c.5 h.3 b.35", "Lissandra": "p.05 c.8 b.45", "Lucian": "p1 a.7 b.45 h.1",
	"Lulu": "p.1 s1 c.6 E", "Lux": "p.05 s.4 c.6 b.5", "Malphite": "p.2 c.8 t.85 b.45 s.4",
	"Malzahar": "p.05 c.7 Q", "Maokai": "p.1 c.8 t.9 h.6", "MasterYi": "p.95 a.95 h.4 b.5 O",
	"Mel": "p.05 b.5 c.4 s.2", "Milio": "p.1 s.7 h.6 c.3 E", "MissFortune": "p1 a.7 b.45 c.2",
	"MonkeyKing": "p1 b.5 c.5 t.45 a.4 h.2", "Mordekaiser": "p.1 s.6 t.55 h.2 a.3", "Morgana": "p.05 c.75 s.4",
	"Naafiri": "p1 b.75 h.3 a.35", "Nami": "p.1 h.6 c.6 E", "Nasus": "p.9 h.6 a.6 t.55 c.3",
	"Nautilus": "p.1 c1 s.3 t.9", "Neeko": "p.05 c.7 b.5", "Nidalee": "p.15 b.6 h.5 c.1",
	"Nilah": "p1 a.8 h.4 s.3 b.45 K", "Nocturne": "p1 b.6 a.5 c.3 s.2", "Nunu": "p.1 c.6 t.8 h.3",
	"Olaf": "p.95 h.6 a.5 t.5", "Orianna": "p.05 s.5 c.6 b.45", "Ornn": "p.5 c.8 t.95",
	"Pantheon": "p1 b.6 c.4 L", "Poppy": "p.95 c.7 t.7 s.2", "Pyke": "p1 b.65 h.3 c.55",
	"Qiyana": "p1 b.8 c.5", "Quinn": "p1 a.7 b.5 L", "Rakan": "p.1 h.3 s.4 c.8 E",
	"Rammus": "p.2 c.7 t.95 a.3", "RekSai": "p1 b.5 c.45 t.45", "Rell": "p.1 c.9 t.9",
	"Renata": "p.1 s.3 h.3 c.6 E", "Renekton": "p.95 h.5 b.5 c.4 t.55", "Rengar": "p1 b.9 h.3 a.4",
	"Riven": "p.95 b.5 s.3 c.5 h.2 t.35", "Rumble": "p.05 s.4 b.45 c.3", "Ryze": "p.05 c.4 s.2 b.4",
	"Samira": "p1 a.7 b.55 h.4", "Sejuani": "p.1 c.9 t.9 s.3", "Senna": "p1 a.6 h.3 s.3 c.3 L",
	"Seraphine": "p.05 s.4 h.4 c.6 b.35 E", "Sett": "p.95 s.6 c.5 t.6 a.4 b.4", "Shaco": "p.7 b.6 c.3 L",
	"Shen": "p.4 s.6 t.85 c.55", "Shyvana": "p.55 a.6 h.3 t.5", "Singed": "p.05 t.7 c.5",
	"Sion": "p.9 t.85 c.7 s.3 h.2", "Sivir": "p1 a.8 s.2", "Skarner": "p.8 t.85 c.8 s.4 Q",
	"Smolder": "p.85 a.4 b.35 X", "Sona": "p.1 h.5 s.2 c.5 E", "Soraka": "p.1 h1 c.3 E",
	"Swain": "p.05 h.8 c.6 t.4", "Sylas": "p.1 h.8 b.5 c.45", "Syndra": "p.05 b.7 c.6",
	"TahmKench": "p.2 h.4 s.4 c.6 t.85", "Taliyah": "p.05 c.55 b.5", "Talon": "p1 b.9",
	"Taric": "p.2 h.5 s.6 c.6 t.7 E", "Teemo": "p.25 a.55", "Thresh": "p.2 s.4 c.8 t.75",
	"Tristana": "p1 a.9 b.45", "Trundle": "p.95 h.6 a.55 t.55 c.3", "Tryndamere": "p1 a.9 h.6 b.4 K",
	"TwistedFate": "p.2 c.5 b.45 a.3", "Twitch": "p.9 a.9 b.4", "Udyr": "p.5 h.4 s.3 a.6 t.55 c.4",
	"Urgot": "p1 s.3 t.55 c.5 a.4 Q", "Varus": "p.9 a.55 b.4 c.4 O", "Vayne": "p1 a.95 b.3 h.1 O",
	"Veigar": "p.05 c.7 b.7", "Velkoz": "p.05 b.5 c.45", "Vex": "p.05 b.65 c.55 s.3",
	"Vi": "p1 c.65 b.5 s.3 t.45", "Viego": "p1 h.5 a.55 b.45", "Viktor": "p.05 s.2 c.45 b.55",
	"Vladimir": "p.05 h1 b.5 t.3", "Volibear": "p.5 h.6 c.5 t.6 a.4", "Warwick": "p.65 h.9 c.55 a.5 t.5 Q",
	"Xayah": "p1 a.85 c.4", "Xerath": "p.05 b.45 c.45", "XinZhao": "p1 h.3 a.6 c.5 t.45",
	"Yasuo": "p1 s.4 a.6 b.45 c.4 K", "Yone": "p.7 s.3 a.55 b.55 c.45 K", "Yorick": "p1 h.3 t.5 a.4 c.3",
	"Yunara": "p1 a.9", "Yuumi": "p.1 h.8 s.4 c.3 E", "Zaahen": "p.95",
	"Zac": "p.1 h.6 c.8 t.9", "Zed": "p1 b.9 Q", "Zeri": "p.9 a.8 s.2",
	"Ziggs": "p.05 b.4 c.3", "Zilean": "p.05 c.5 E", "Zoe": "p.05 b.6 c.45",
	"Zyra": "p.05 c.6 b.45",
}

func tagDefaults(tags []string) Trait {
	t := Trait{Heal: 0.1, Shield: 0.05}
	primary := ""
	if len(tags) > 0 {
		primary = tags[0]
	}
	switch primary {
	case "Tank":
		t.CC, t.Burst, t.Auto, t.Tank = 0.7, 0.2, 0.25, 0.9
	case "Support":
		t.CC, t.Burst, t.Auto, t.Tank = 0.6, 0.15, 0.1, 0.3
	case "Mage":
		t.CC, t.Burst, t.Auto, t.Tank = 0.5, 0.45, 0.1, 0.1
	case "Fighter":
		t.CC, t.Burst, t.Auto, t.Tank = 0.35, 0.3, 0.45, 0.55
	case "Assassin":
		t.CC, t.Burst, t.Auto, t.Tank = 0.3, 0.8, 0.35, 0.15
	case "Marksman":
		t.CC, t.Burst, t.Auto, t.Tank = 0.25, 0.3, 0.9, 0.1
	default:
		t.CC, t.Burst, t.Auto, t.Tank = 0.4, 0.4, 0.4, 0.3
	}
	return t
}

func traitFor(d ddChamp) Trait {
	t := tagDefaults(d.Tags)
	// Physical share fallback from Data Dragon's attack/magic ratings.
	a, m := d.Info.Attack, d.Info.Magic
	if a+m > 0 {
		t.Phys = a * a / (a*a + m*m)
	} else {
		t.Phys = 0.5
	}
	if len(d.Tags) > 0 && (d.Tags[0] == "Mage" || d.Tags[0] == "Support") && t.Phys > 0.4 {
		t.Phys = 0.3
	}
	spec, ok := traitTable[d.ID]
	if !ok {
		return t
	}
	for _, f := range strings.Fields(spec) {
		switch f[0] {
		case 'Q':
			t.Suppress = true
			continue
		case 'E':
			t.Enchanter = true
			continue
		case 'K', 'O', 'X', 'L':
			t.Style = f[0]
			continue
		}
		v, err := strconv.ParseFloat(f[1:], 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case 'p':
			t.Phys = v
		case 'h':
			t.Heal = v
		case 's':
			t.Shield = v
		case 'c':
			t.CC = v
		case 'b':
			t.Burst = v
		case 'a':
			t.Auto = v
		case 't':
			t.Tank = v
		}
	}
	return t
}
