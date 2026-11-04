// Package app ties the live game feed, patch data, engine and renderer together.
// It has no Windows dependencies, so it can be tested anywhere.
package app

import (
	"fmt"
	"image"
	"log"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"counterbuild/engine"
	"counterbuild/meta"
	"counterbuild/render"
)

// Frame is one overlay image to show (or nothing).
type Frame struct {
	Img    *image.RGBA
	Show   bool
	InGame bool
}

type App struct {
	Cfg  *Config
	Data *Data
	Live *Live
	Hist *History
	Meta *meta.Provider
	R    *render.Renderer
	Log  *log.Logger

	wake     chan struct{}
	scaleBit atomic.Uint64
	cfgMu    sync.Mutex

	lastErr   string
	lastSave  time.Time
	gameStart time.Time
}

// New builds the app. dir is the data folder; fontDirs are searched for Segoe UI.
func New(cfg *Config, dir string, fontDirs []string, logger *log.Logger) *App {
	a := &App{Cfg: cfg, Log: logger, wake: make(chan struct{}, 1)}
	a.Data = NewData(dir, cfg.DataURL, a.Wake)
	a.Live = NewLive(cfg.LiveURL)
	a.Hist = LoadHistory(dir + "/history.json")
	a.Meta = meta.NewProvider(cfg.MetaURL, dir, Version, a.metaLog, a.Wake)
	a.R = render.NewRenderer(fontDirs...)
	a.SetScale(1)
	return a
}

// SetScale sets the pixel scale (the UI derives it from the game resolution).
func (a *App) SetScale(s float64) { a.scaleBit.Store(math.Float64bits(s)) }

func (a *App) scale() float64 { return math.Float64frombits(a.scaleBit.Load()) }

// Wake asks the loop to redraw soon (icon loaded, settings changed).
func (a *App) Wake() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// WithConfig runs f while holding the config lock (UI thread edits).
func (a *App) WithConfig(f func(c *Config)) {
	a.cfgMu.Lock()
	f(a.Cfg)
	_ = a.Cfg.Save()
	a.cfgMu.Unlock()
	a.Wake()
}

func (a *App) metaLog(format string, args ...any) {
	if a.Log != nil {
		a.Log.Printf(format, args...)
	}
}

func (a *App) logf(format string, args ...any) {
	if a.Log == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	if msg == a.lastErr {
		return
	}
	a.lastErr = msg
	a.Log.Print(msg)
}

// Run polls until stop is closed, calling publish with every frame.
func (a *App) Run(stop <-chan struct{}, publish func(Frame)) {
	go a.Data.Refresh()
	a.cfgMu.Lock()
	poll := time.Duration(a.Cfg.PollMs) * time.Millisecond
	a.cfgMu.Unlock()
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		publish(a.safeStep())
		select {
		case <-stop:
			a.Hist.Save()
			return
		case <-t.C:
		case <-a.wake:
		}
	}
}

func (a *App) safeStep() (f Frame) {
	defer func() {
		if r := recover(); r != nil {
			a.logf("step failed: %v", r)
			f = Frame{}
		}
	}()
	return a.Step()
}

// Step performs one poll and returns what to show.
func (a *App) Step() Frame {
	a.cfgMu.Lock()
	cfg := *a.Cfg
	a.cfgMu.Unlock()
	style := render.Style{Scale: a.scale(), Opacity: cfg.Opacity, Hotkey: cfg.Hotkey}

	b, err := a.Live.Fetch()
	if err != nil {
		if !a.gameStart.IsZero() {
			a.Hist.Save()
			a.gameStart = time.Time{}
		}
		return Frame{}
	}
	g, err := engine.ParseLiveGame(b)
	if err != nil {
		return Frame{InGame: true} // loading screen
	}
	if a.gameStart.IsZero() {
		go a.Data.Refresh() // cheap unless the patch check is due
	}
	cat := a.Data.Catalog()
	if cat == nil {
		go a.Data.Refresh()
		msg := "Downloading item data..."
		if err := a.Data.Err(); err != nil {
			msg = "Can't load item data, retrying"
			a.logf("data: %v", err)
		}
		return Frame{Img: a.R.Status(msg, style), Show: true, InGame: true}
	}
	start := time.Now().Add(-time.Duration(g.GameData.GameTime * float64(time.Second)))
	a.gameStart = start

	champ := ""
	var mb *engine.Meta
	if self := g.Self(); self != nil {
		champ = self.ChampID()
		if cfg.MetaSource != "off" {
			ch := cat.Champion(champ)
			name := champ
			if ch != nil {
				name = ch.Name
			}
			req := meta.Request{ChampID: champ, ChampName: name, Position: rolePosition(self, ch, g.GameData.GameMode),
				Mode: modeName(g.GameData.GameMode), Version: a.Data.Version()}
			mb = a.Meta.Lookup(req, cat)
		}
	}
	opts := engine.Options{MaxItems: cfg.MaxItems, Affinity: a.Hist.Affinity(champ), Core: resolveCore(cat, cfg.Core, champ), Meta: mb}
	res := cat.Recommend(g, opts)
	if !res.Supported {
		if res.Note == "" {
			return Frame{InGame: true}
		}
		return Frame{Img: a.R.Status(res.Note, style), Show: true, InGame: true}
	}
	key := fmt.Sprintf("%s|%d", res.ChampID, start.Unix()/30)
	a.Hist.Observe(key, res.ChampID, res.Completed)
	if time.Since(a.lastSave) > 30*time.Second {
		a.Hist.Save()
		a.lastSave = time.Now()
	}
	return Frame{Img: a.R.Render(res, a.Data, style), Show: true, InGame: true}
}

// rolePosition maps the game's lane assignment to a stats-site role, guessing
// from the champion when the game doesn't say (blind pick, customs).
func rolePosition(self *engine.LivePlayer, ch *engine.Champion, mode string) string {
	if strings.EqualFold(mode, "ARAM") {
		return "none"
	}
	switch strings.ToUpper(strings.TrimSpace(self.Position)) {
	case "TOP":
		return "top"
	case "JUNGLE":
		return "jungle"
	case "MIDDLE", "MID":
		return "mid"
	case "BOTTOM", "BOT", "ADC":
		return "adc"
	case "UTILITY", "SUPPORT":
		return "support"
	}
	if self.HasSmite() {
		return "jungle"
	}
	if ch != nil && len(ch.Tags) > 0 {
		switch ch.Tags[0] {
		case "Marksman":
			return "adc"
		case "Support":
			return "support"
		case "Mage", "Assassin":
			return "mid"
		}
	}
	return "top"
}

func modeName(gameMode string) string {
	switch strings.ToUpper(gameMode) {
	case "ARAM":
		return "aram"
	case "URF", "ARURF":
		return "urf"
	}
	return "ranked"
}

// resolveCore maps configured core item names for this champion to item ids.
func resolveCore(cat *engine.Catalog, core map[string][]string, champ string) []int {
	if len(core) == 0 || champ == "" {
		return nil
	}
	ch := cat.Champion(champ)
	var names []string
	for k, v := range core {
		if strings.EqualFold(k, champ) || (ch != nil && strings.EqualFold(k, ch.Name)) {
			names = v
			break
		}
	}
	var ids []int
	for _, n := range names {
		best := 0
		for id, it := range cat.Items {
			if !strings.EqualFold(it.Name, strings.TrimSpace(n)) || !it.Purchasable || !it.Maps[11] {
				continue
			}
			if best == 0 || id < best {
				best = id
			}
		}
		if best != 0 {
			ids = append(ids, best)
		}
	}
	return ids
}
