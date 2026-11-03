package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// History remembers which completed items you build on each champion, so the
// overlay can lean toward your usual build over time.
type History struct {
	mu       sync.Mutex
	path     string
	Champs   map[string]map[int]int `json:"champs"`
	LastGame struct {
		Key  string `json:"key"`
		Seen []int  `json:"seen"`
	} `json:"lastGame"`
	dirty bool
}

func LoadHistory(path string) *History {
	h := &History{path: path, Champs: map[string]map[int]int{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, h)
	}
	if h.Champs == nil {
		h.Champs = map[string]map[int]int{}
	}
	return h
}

// Observe records newly completed items for this game. gameKey identifies the match.
func (h *History) Observe(gameKey, champ string, completed []int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.LastGame.Key != gameKey {
		h.LastGame.Key = gameKey
		h.LastGame.Seen = nil
		h.dirty = true
	}
	seen := map[int]bool{}
	for _, id := range h.LastGame.Seen {
		seen[id] = true
	}
	for _, id := range completed {
		if seen[id] {
			continue
		}
		seen[id] = true
		h.LastGame.Seen = append(h.LastGame.Seen, id)
		m := h.Champs[champ]
		if m == nil {
			m = map[int]int{}
			h.Champs[champ] = m
		}
		m[id]++
		h.dirty = true
	}
}

// Affinity returns 0..0.8 per item: higher for items you build often on champ.
func (h *History) Affinity(champ string) map[int]float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[int]float64{}
	for id, n := range h.Champs[champ] {
		a := float64(n) / float64(n+3)
		if a > 0.8 {
			a = 0.8
		}
		out[id] = a
	}
	return out
}

// Save writes the file if anything changed.
func (h *History) Save() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.dirty || h.path == "" {
		return
	}
	b, err := json.Marshal(h)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(h.path), 0o755)
	if os.WriteFile(h.path+".tmp", b, 0o644) == nil && os.Rename(h.path+".tmp", h.path) == nil {
		h.dirty = false
	}
}
