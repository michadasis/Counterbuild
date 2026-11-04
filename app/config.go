package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config is stored as config.json next to the cache and can be edited by hand.
type Config struct {
	Hotkey            string              `json:"hotkey"`
	Position          string              `json:"position"`
	OffsetX           int                 `json:"offsetX"`
	OffsetY           int                 `json:"offsetY"`
	Scale             float64             `json:"scale"`
	Opacity           float64             `json:"opacity"`
	MaxItems          int                 `json:"maxItems"`
	PollMs            int                 `json:"pollMs"`
	HideWhenUnfocused bool                `json:"hideWhenUnfocused"`
	Core              map[string][]string `json:"core"`
	LiveURL           string              `json:"liveUrl"`
	DataURL           string              `json:"dataUrl"`
	MetaSource        string              `json:"metaSource"`
	MetaURL           string              `json:"metaUrl"`
	Augments          string              `json:"augments"`
	Welcomed          bool                `json:"welcomed"`

	path string
	raw  []byte
}

// Positions the tray menu offers.
var Positions = []string{"right", "top-right", "left", "top-left", "bottom-left"}

func DefaultConfig() *Config {
	return &Config{
		Hotkey:            "Ctrl+Shift+B",
		Position:          "right",
		Scale:             1,
		Opacity:           0.94,
		MaxItems:          3,
		PollMs:            1000,
		HideWhenUnfocused: true,
		Core:              map[string][]string{},
		LiveURL:           "https://127.0.0.1:2999/liveclientdata/allgamedata",
		DataURL:           "https://ddragon.leagueoflegends.com",
		MetaSource:        "opgg",
		MetaURL:           "https://mcp-api.op.gg/mcp",
		Augments:          "auto",
	}
}

// LoadConfig reads path, filling defaults for anything missing, and writes the
// file back so new options appear for the user to edit. If the file is not
// valid JSON it is left untouched and defaults are returned with the error.
func LoadConfig(path string) (*Config, error) {
	c := DefaultConfig()
	c.path = path
	if b, err := os.ReadFile(path); err == nil {
		c.raw = b
		if err := json.Unmarshal(b, c); err != nil {
			d := DefaultConfig()
			d.path, d.raw = path, b
			return d, err
		}
	}
	c.normalize()
	_ = c.Save()
	return c, nil
}

// FileChanged reports whether config.json was edited outside the app.
func (c *Config) FileChanged() bool {
	if c.path == "" {
		return false
	}
	b, err := os.ReadFile(c.path)
	return err == nil && !bytes.Equal(b, c.raw)
}

func (c *Config) normalize() {
	d := DefaultConfig()
	if c.Hotkey == "" {
		c.Hotkey = d.Hotkey
	}
	ok := false
	for _, p := range Positions {
		if strings.EqualFold(p, c.Position) {
			c.Position, ok = p, true
		}
	}
	if !ok {
		c.Position = d.Position
	}
	if c.Scale < 0.5 || c.Scale > 3 {
		c.Scale = d.Scale
	}
	if c.Opacity < 0.3 || c.Opacity > 1 {
		c.Opacity = d.Opacity
	}
	if c.MaxItems < 1 || c.MaxItems > 5 {
		c.MaxItems = d.MaxItems
	}
	if c.PollMs < 250 || c.PollMs > 10000 {
		c.PollMs = d.PollMs
	}
	if c.Core == nil {
		c.Core = map[string][]string{}
	}
	if c.LiveURL == "" {
		c.LiveURL = d.LiveURL
	}
	if c.DataURL == "" {
		c.DataURL = d.DataURL
	}
	c.MetaSource = strings.ToLower(strings.TrimSpace(c.MetaSource))
	if c.MetaSource != "off" {
		c.MetaSource = "opgg"
	}
	if c.MetaURL == "" {
		c.MetaURL = d.MetaURL
	}
	c.Augments = strings.ToLower(strings.TrimSpace(c.Augments))
	if c.Augments != "off" && c.Augments != "always" {
		c.Augments = "auto"
	}
}

// Save writes the config atomically.
func (c *Config) Save() error {
	if c.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if bytes.Equal(b, c.raw) {
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(c.path), 0o755)
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return err
	}
	c.raw = b
	return nil
}

func (c *Config) Path() string { return c.path }
