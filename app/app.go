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

	reader   ScreenReader
	augMu    sync.Mutex
	augBusy  bool
	scanAt   time.Time
	lines    []OCRLine
	linesNew bool
	scanErr  string
	offer    []engine.AugmentCard
	offerAt  time.Time
}

// SetScreenReader enables reading augment cards (Windows only).
func (a *App) SetScreenReader(r ScreenReader) {
	a.augMu.Lock()
	a.reader = r
	a.augMu.Unlock()
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
	mode := modeName(g.GameData.GameMode, g.GameData.MapNumber)
	var mb *engine.Meta
	var ch *engine.Champion
	if self := g.Self(); self != nil {
		champ = self.ChampID()
		ch = cat.Champion(champ)
		if cfg.MetaSource != "off" && mode != "" {
			name := champ
			if ch != nil {
				name = ch.Name
			}
			req := meta.Request{ChampID: champ, ChampName: name, Position: rolePosition(self, ch, mode),
				Mode: mode, Version: a.Data.Version()}
			mb = a.Meta.Lookup(req, cat)
		}
	}
	opts := engine.Options{MaxItems: cfg.MaxItems, Affinity: a.Hist.Affinity(historyKey(champ, mode)), Core: resolveCore(cat, cfg.Core, champ), Meta: mb}
	res := cat.Recommend(g, opts)
	if !res.Supported {
		if res.Note == "" {
			return Frame{InGame: true}
		}
		return Frame{Img: a.R.Status(res.Note, style), Show: true, InGame: true}
	}
	if ch != nil && cfg.MetaSource != "off" && augmentMode(cfg.Augments, g.GameData.GameMode, g.GameData.MapNumber) {
		res.Augments = a.augmentOffer(meta.MergeAugments(a.Meta.Augments(ch.Key, a.Data.Version()), a.Meta.AugmentNames()))
	}
	key := fmt.Sprintf("%s|%d", res.ChampID, start.Unix()/30)
	a.Hist.Observe(key, historyKey(res.ChampID, mode), res.Completed)
	if time.Since(a.lastSave) > 30*time.Second {
		a.Hist.Save()
		a.lastSave = time.Now()
	}
	return Frame{Img: a.R.Render(res, a.Data, style), Show: true, InGame: true}
}

// historyKey keeps what you build in each kind of game apart: Summoner's
// Rift builds stay under the champion's name, ARAM (and Mayhem) and URF ones
// go under "Soraka@aram" and "Soraka@urf", so Warmog's on ARAM Soraka doesn't
// become "your usual build" on the Rift.
func historyKey(champ, mode string) string {
	if mode == "" || mode == "ranked" {
		return champ
	}
	return champ + "@" + mode
}

// rolePosition maps the game's lane assignment to a stats-site role, guessing
// from the champion when the game doesn't say (blind pick, customs).
func rolePosition(self *engine.LivePlayer, ch *engine.Champion, mode string) string {
	if mode == "aram" {
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

// modeName maps the game mode to a stats-site mode, or "" for modes the engine
// doesn't support (Arena, Swarm, TFT), so no OP.GG lookup is made for them.
// Everything on Howling Abyss (map 12), ARAM Mayhem included, uses ARAM data.
func modeName(gameMode string, mapNumber int) string {
	switch strings.ToUpper(gameMode) {
	case "CHERRY", "TFT", "STRAWBERRY":
		return ""
	case "ARAM":
		return "aram"
	case "URF", "ARURF":
		return "urf"
	}
	if mapNumber == 12 {
		return "aram"
	}
	return "ranked"
}

const (
	scanEvery = time.Second             // how often to read the screen in Mayhem
	offerHold = 2500 * time.Millisecond // keep showing an offer this long after the last read
)

// augmentOffer starts a screen read when one is due and returns the augment
// cards currently on screen, rated with this champion's stats.
func (a *App) augmentOffer(augs []meta.Augment) []engine.AugmentCard {
	a.augMu.Lock()
	defer a.augMu.Unlock()
	if a.reader == nil || len(augs) == 0 {
		return nil
	}
	if a.linesNew {
		a.linesNew = false
		if cards := RateCards(a.lines, augs); cards != nil {
			a.offer, a.offerAt = cards, time.Now()
		}
	}
	if !a.augBusy && time.Since(a.scanAt) >= scanEvery {
		a.augBusy, a.scanAt = true, time.Now()
		go a.scan()
	}
	if time.Since(a.offerAt) < offerHold {
		return a.offer
	}
	return nil
}

func (a *App) scan() {
	var lines []OCRLine
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("crashed: %v", r)
			}
		}()
		lines, err = a.reader()
	}()
	a.augMu.Lock()
	a.augBusy = false
	logErr := ""
	if err != nil {
		if msg := err.Error(); msg != a.scanErr {
			a.scanErr, logErr = msg, msg
		}
	} else {
		a.lines, a.linesNew, a.scanErr = lines, true, ""
	}
	a.augMu.Unlock()
	if logErr != "" {
		a.metaLog("augment cards: %s", logErr)
	}
	if len(lines) > 0 {
		a.Wake()
	}
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
