package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"counterbuild/engine"
)

// Data downloads and caches Data Dragon files (item/champion data and icons).
type Data struct {
	dir    string
	base   string
	client *http.Client
	onIcon func()

	mu        sync.RWMutex
	cat       *engine.Catalog
	version   string
	lastCheck time.Time
	loading   bool
	lastErr   error
	icons     map[string]image.Image
	pending   map[string]bool
	failed    map[string]time.Time
}

func NewData(dir, base string, onIcon func()) *Data {
	return &Data{
		dir:     filepath.Join(dir, "ddragon"),
		base:    strings.TrimRight(base, "/"),
		client:  &http.Client{Timeout: 20 * time.Second},
		onIcon:  onIcon,
		icons:   map[string]image.Image{},
		pending: map[string]bool{},
		failed:  map[string]time.Time{},
	}
}

func (d *Data) Catalog() *engine.Catalog {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cat
}

// Version is the patch of the loaded data ("" before the first load).
func (d *Data) Version() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.version
}

func (d *Data) Err() error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.lastErr
}

func (d *Data) get(url string) ([]byte, error) {
	resp, err := d.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

func (d *Data) cachedVersions() []string {
	ents, _ := os.ReadDir(d.dir)
	var vs []string
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(d.dir, e.Name(), "item.json")); err == nil {
			vs = append(vs, e.Name())
		}
	}
	sort.Slice(vs, func(i, j int) bool { return versionLess(vs[i], vs[j]) })
	return vs
}

// Refresh makes sure the newest patch data is loaded. It is safe to call often:
// the network is only checked every few hours.
func (d *Data) Refresh() {
	d.mu.Lock()
	if d.loading || (d.cat != nil && time.Since(d.lastCheck) < 3*time.Hour) {
		d.mu.Unlock()
		return
	}
	d.loading = true
	d.mu.Unlock()
	err := d.refresh()
	d.mu.Lock()
	d.loading = false
	d.lastCheck = time.Now()
	d.lastErr = err
	d.mu.Unlock()
}

func (d *Data) refresh() error {
	latest := ""
	if b, err := d.get(d.base + "/api/versions.json"); err == nil {
		var vs []string
		if json.Unmarshal(b, &vs) == nil && len(vs) > 0 {
			latest = vs[0]
		}
	}
	cached := d.cachedVersions()
	if latest == "" {
		if len(cached) == 0 {
			return errors.New("can't reach Data Dragon and no cached data")
		}
		latest = cached[len(cached)-1]
	}
	d.mu.RLock()
	same := d.cat != nil && d.version == latest
	d.mu.RUnlock()
	if same {
		return nil
	}
	vdir := filepath.Join(d.dir, latest)
	read := func(name string) ([]byte, error) {
		p := filepath.Join(vdir, name)
		if b, err := os.ReadFile(p); err == nil {
			return b, nil
		}
		b, err := d.get(fmt.Sprintf("%s/cdn/%s/data/en_US/%s", d.base, latest, name))
		if err != nil {
			return nil, err
		}
		_ = os.MkdirAll(vdir, 0o755)
		_ = os.WriteFile(p+".tmp", b, 0o644)
		_ = os.Rename(p+".tmp", p)
		return b, nil
	}
	items, err := read("item.json")
	if err != nil {
		return d.fallback(cached, err)
	}
	champs, err := read("champion.json")
	if err != nil {
		return d.fallback(cached, err)
	}
	cat, err := engine.LoadCatalog(items, champs)
	if err != nil {
		os.RemoveAll(vdir)
		return d.fallback(cached, err)
	}
	d.mu.Lock()
	d.cat, d.version = cat, latest
	d.icons = map[string]image.Image{}
	d.mu.Unlock()
	d.prune(latest)
	return nil
}

func (d *Data) fallback(cached []string, cause error) error {
	if d.Catalog() != nil {
		return nil
	}
	for i := len(cached) - 1; i >= 0; i-- {
		v := cached[i]
		items, e1 := os.ReadFile(filepath.Join(d.dir, v, "item.json"))
		champs, e2 := os.ReadFile(filepath.Join(d.dir, v, "champion.json"))
		if e1 != nil || e2 != nil {
			continue
		}
		if cat, err := engine.LoadCatalog(items, champs); err == nil {
			d.mu.Lock()
			d.cat, d.version = cat, v
			d.mu.Unlock()
			return nil
		}
	}
	return cause
}

// prune keeps the two newest patch folders.
func (d *Data) prune(keep string) {
	vs := d.cachedVersions()
	for i := 0; i < len(vs)-2; i++ {
		if vs[i] != keep {
			os.RemoveAll(filepath.Join(d.dir, vs[i]))
		}
	}
}

// Item implements render.Icons.
func (d *Data) Item(id int) image.Image { return d.icon("item", strconv.Itoa(id)) }

// Champion implements render.Icons.
func (d *Data) Champion(id string) image.Image { return d.icon("champion", id) }

func (d *Data) icon(kind, name string) image.Image {
	key := kind + "/" + name
	d.mu.RLock()
	img, ok := d.icons[key]
	pend := d.pending[key]
	failedAt, failed := d.failed[key]
	ver := d.version
	d.mu.RUnlock()
	if ok {
		return img
	}
	if pend || ver == "" || (failed && time.Since(failedAt) < 2*time.Minute) {
		return nil
	}
	d.mu.Lock()
	d.pending[key] = true
	d.mu.Unlock()
	go d.loadIcon(ver, kind, name, key)
	return nil
}

func (d *Data) loadIcon(ver, kind, name, key string) {
	p := filepath.Join(d.dir, ver, "img", kind, name+".png")
	b, err := os.ReadFile(p)
	if err != nil {
		b, err = d.get(fmt.Sprintf("%s/cdn/%s/img/%s/%s.png", d.base, ver, kind, name))
		if err == nil {
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, b, 0o644)
		}
	}
	var img image.Image
	if err == nil {
		img, err = png.Decode(bytes.NewReader(b))
	}
	d.mu.Lock()
	delete(d.pending, key)
	if err != nil {
		d.failed[key] = time.Now()
	} else if d.version == ver {
		d.icons[key] = img
	}
	d.mu.Unlock()
	if err == nil && d.onIcon != nil {
		d.onIcon()
	}
}
