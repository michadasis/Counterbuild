package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupData(t *testing.T, dir string) {
	t.Helper()
	v := filepath.Join(dir, "ddragon", "16.21.1")
	if err := os.MkdirAll(v, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"item.json", "champion.json"} {
		b, err := os.ReadFile(filepath.Join("..", "engine", "testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(v, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func liveJSON() []byte {
	p := func(champ, team, pos string, items ...int) map[string]any {
		var its []map[string]any
		for i, id := range items {
			its = append(its, map[string]any{"itemID": id, "count": 1, "slot": i})
		}
		return map[string]any{"championName": champ, "rawChampionName": "game_character_displayname_" + champ,
			"level": 9, "position": pos, "team": team, "riotId": champ + "#T", "summonerName": champ + "#T", "items": its}
	}
	g := map[string]any{
		"activePlayer": map[string]any{"riotId": "Jinx#T", "level": 9, "currentGold": 950.0,
			"championStats": map[string]any{"attackDamage": 120.0, "attackSpeed": 0.9, "maxHealth": 1500.0, "armor": 60.0, "magicResist": 40.0}},
		"allPlayers": []any{
			p("Jinx", "ORDER", "BOTTOM", 3032, 3006),
			p("Soraka", "CHAOS", "UTILITY", 3869, 6617),
			p("Aatrox", "CHAOS", "TOP", 6692),
			p("Vladimir", "CHAOS", "MIDDLE", 4633),
			p("Zed", "CHAOS", "JUNGLE", 3142),
			p("Caitlyn", "CHAOS", "BOTTOM", 3031),
		},
		"gameData": map[string]any{"gameMode": "CLASSIC", "gameTime": 900.0, "mapNumber": 11},
	}
	b, _ := json.Marshal(g)
	return b
}

func TestStepEndToEnd(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(liveJSON())
	}))
	defer srv.Close()
	dir := t.TempDir()
	setupData(t, dir)
	cfg, err := LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.LiveURL = srv.URL
	cfg.DataURL = "http://127.0.0.1:1" // unreachable: must fall back to the cache
	cfg.MetaSource = "off"             // tests never contact OP.GG
	a := New(cfg, dir, nil, nil)
	a.Data.Refresh()
	if a.Data.Catalog() == nil {
		t.Fatalf("catalog not loaded from cache: %v", a.Data.Err())
	}
	f := a.Step()
	if !f.Show || f.Img == nil || !f.InGame {
		t.Fatalf("expected a frame, got %+v", f)
	}
	if f.Img.Bounds().Dx() < 200 || f.Img.Bounds().Dy() < 200 {
		t.Fatalf("frame too small: %v", f.Img.Bounds())
	}
	a.Hist.Save()
	if _, err := os.Stat(filepath.Join(dir, "history.json")); err != nil {
		t.Fatalf("history not saved: %v", err)
	}
}

func TestNoGame(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := LoadConfig(filepath.Join(dir, "config.json"))
	cfg.LiveURL = "https://127.0.0.1:1/liveclientdata/allgamedata"
	cfg.MetaSource = "off"
	a := New(cfg, dir, nil, nil)
	if f := a.Step(); f.Show || f.InGame {
		t.Fatalf("expected empty frame without a game, got %+v", f)
	}
}

func TestConfigBadJSONIsKept(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	bad := []byte(`{"hotkey": "F8",`)
	os.WriteFile(p, bad, 0o644)
	c, err := LoadConfig(p)
	if err == nil || c.Hotkey != DefaultConfig().Hotkey {
		t.Fatalf("expected error and defaults, got %v %+v", err, c)
	}
	if b, _ := os.ReadFile(p); string(b) != string(bad) {
		t.Fatalf("broken config was overwritten")
	}
}

func TestHistoryAffinity(t *testing.T) {
	h := LoadHistory(filepath.Join(t.TempDir(), "h.json"))
	for g := 0; g < 3; g++ {
		key := "Jinx|" + string(rune('a'+g))
		h.Observe(key, "Jinx", []int{3031})
		h.Observe(key, "Jinx", []int{3031, 3036}) // same game: 3031 not double counted
	}
	a := h.Affinity("Jinx")
	if a[3031] != 0.5 || a[3036] != 0.5 {
		t.Fatalf("unexpected affinity %v", a)
	}
}

func TestModeNameSkipsUnsupportedModes(t *testing.T) {
	for _, m := range []string{"CHERRY", "TFT", "STRAWBERRY"} {
		if got := modeName(m, 30); got != "" {
			t.Fatalf("modeName(%q) = %q, want no OP.GG lookup", m, got)
		}
	}
	for m, want := range map[string]string{"CLASSIC": "ranked", "ARAM": "aram", "ARURF": "urf"} {
		if got := modeName(m, 11); got != want {
			t.Fatalf("modeName(%q) = %q, want %q", m, got, want)
		}
	}
}

func TestHistoryKey(t *testing.T) {
	for _, c := range []struct{ mode, want string }{
		{"ranked", "Soraka"}, {"", "Soraka"}, {"aram", "Soraka@aram"}, {"urf", "Soraka@urf"},
	} {
		if got := historyKey("Soraka", c.mode); got != c.want {
			t.Fatalf("historyKey(Soraka, %q) = %q, want %q", c.mode, got, c.want)
		}
	}
	h := LoadHistory(filepath.Join(t.TempDir(), "history.json"))
	h.Observe("g1", historyKey("Soraka", "aram"), []int{3083})
	if len(h.Affinity(historyKey("Soraka", "ranked"))) != 0 {
		t.Fatalf("an ARAM build must not count on the Rift")
	}
}

func TestScanInterval(t *testing.T) {
	a := &App{}
	if a.scanInterval() != scanIdle {
		t.Fatalf("without cards on screen, read every %v", scanIdle)
	}
	a.offerAt = time.Now()
	if a.scanInterval() != scanOffer {
		t.Fatalf("while cards are showing, read every %v", scanOffer)
	}
}
