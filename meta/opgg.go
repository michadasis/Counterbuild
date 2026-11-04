package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"counterbuild/engine"
)

// Request identifies the build data to fetch.
type Request struct {
	ChampID   string // Data Dragon id, e.g. "MonkeyKing"
	ChampName string // English name, e.g. "Wukong"
	Position  string // top, jungle, mid, adc, support
	Mode      string // ranked, aram, urf
	Version   string // patch; part of the cache key
}

func (r Request) key() string {
	return fmt.Sprintf("%s_%s_%s", r.ChampID, r.Position, r.Mode)
}

// cached is what we keep per champion, role and mode.
type cached struct {
	Pop     map[int]float64 `json:"pop"`
	WR      map[int]float64 `json:"wr"`
	How     string          `json:"how"`
	Fetched time.Time       `json:"fetched"`
}

// Provider fetches "most built" data from OP.GG's public MCP endpoint, caches it
// on disk for a day, and never blocks the overlay: Lookup returns what it has
// and fetches in the background.
type Provider struct {
	url     string
	dir     string
	http    *http.Client
	logf    func(string, ...any)
	onReady func()
	agent   string
	version string

	mu       sync.Mutex
	mem      map[string]*cached
	inflight bool
	retryAt  map[string]time.Time
	tool     *toolSpec

	augMem  map[string]*augCache
	augBusy bool
}

const (
	freshFor   = 24 * time.Hour
	retryAfter = 15 * time.Minute
	emptyRetry = 6 * time.Hour
)

// NewProvider creates a provider. dir is the app data folder.
func NewProvider(url, dir, appVersion string, logf func(string, ...any), onReady func()) *Provider {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Provider{
		url:     url,
		dir:     filepath.Join(dir, "meta"),
		http:    &http.Client{Timeout: 15 * time.Second},
		logf:    logf,
		onReady: onReady,
		agent:   "Counterbuild/" + appVersion + " (personal League overlay)",
		version: appVersion,
		mem:     map[string]*cached{},
		retryAt: map[string]time.Time{},
		augMem:  map[string]*augCache{},
	}
}

func (p *Provider) cachePath(r Request) string {
	return filepath.Join(p.dir, "opgg", r.Version, r.key()+".json")
}

func (p *Provider) loadDisk(r Request) *cached {
	b, err := os.ReadFile(p.cachePath(r))
	if err != nil {
		return nil
	}
	var c cached
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	return &c
}

func (p *Provider) saveDisk(r Request, c *cached) {
	path := p.cachePath(r)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if b, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(path+".tmp", b, 0o644)
		_ = os.Rename(path+".tmp", path)
	}
}

// Lookup returns cached build data for r (nil if none yet) and starts a
// background refresh when the cache is missing or older than a day.
func (p *Provider) Lookup(r Request, cat *engine.Catalog) *engine.Meta {
	if r.ChampID == "" || r.Version == "" || cat == nil {
		return nil
	}
	k := r.Version + "/" + r.key()
	p.mu.Lock()
	c := p.mem[k]
	if c == nil {
		if c = p.loadDisk(r); c != nil {
			p.mem[k] = c
		}
	}
	fresh := c != nil && time.Since(c.Fetched) < freshFor
	start := !fresh && !p.inflight && time.Now().After(p.retryAt[k])
	if start {
		p.inflight = true
	}
	p.mu.Unlock()
	if start {
		go p.fetch(r, cat, k)
	}
	if c == nil || len(c.Pop) == 0 {
		return nil
	}
	return &engine.Meta{Pop: c.Pop, WR: c.WR, Source: "OP.GG"}
}

func (p *Provider) fetch(r Request, cat *engine.Catalog, k string) {
	defer func() {
		if rec := recover(); rec != nil {
			p.logf("OP.GG: fetch crashed: %v", rec)
		}
		p.mu.Lock()
		p.inflight = false
		p.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	ex, err := p.query(ctx, r, cat)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.retryAt[k] = time.Now().Add(retryAfter)
		p.logf("OP.GG: %s %s %s failed: %v", r.ChampID, r.Position, r.Mode, err)
		return
	}
	c := &cached{Pop: ex.Pop, WR: ex.WR, How: ex.How, Fetched: time.Now()}
	if len(c.Pop) == 0 {
		p.retryAt[k] = time.Now().Add(emptyRetry)
		p.logf("OP.GG: %s %s %s: response had no item data I could read (saved to meta/opgg_last_response.txt)", r.ChampID, r.Position, r.Mode)
		return
	}
	p.mem[k] = c
	p.saveDisk(r, c)
	p.logf("OP.GG: %s %s %s: %d items from %d entries (%s)", r.ChampID, r.Position, r.Mode, len(c.Pop), ex.Entries, ex.How)
	if p.onReady != nil {
		go p.onReady()
	}
}

// toolSpec is what we learned about the champion analysis tool.
type toolSpec struct {
	name     string
	props    map[string]propSpec
	required map[string]bool
	fields   string   // name of the field-selection parameter, if any
	hints    []string // field paths mentioned in the schema that look build related
}

type propSpec struct {
	enum []string
	typ  string
}

var rePath = regexp.MustCompile(`(?i)\b[a-z_][a-z0-9_]*(?:\[\])?(?:\.(?:[a-z_][a-z0-9_]*(?:\[\])?|\{[a-z0-9_,\s]+\}))+`)

// buildPaths finds field paths in free text that look like build data.
func buildPaths(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range rePath.FindAllString(text, -1) {
		l := strings.ToLower(m)
		if !(strings.Contains(l, "item") || strings.Contains(l, "boot") || strings.Contains(l, "build")) {
			continue
		}
		if strings.Contains(l, "rune") || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
		if len(out) >= 12 {
			break
		}
	}
	return out
}

func (p *Provider) findTool(ctx context.Context, c *mcpClient) (*toolSpec, error) {
	p.mu.Lock()
	t := p.tool
	p.mu.Unlock()
	if t != nil {
		return t, nil
	}
	type tool struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	var all []tool
	cursor := ""
	var rawPages []json.RawMessage
	for page := 0; page < 5; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		rawPages = append(rawPages, raw)
		var res struct {
			Tools      []tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	_ = os.MkdirAll(p.dir, 0o755)
	if b, err := json.MarshalIndent(rawPages, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(p.dir, "opgg_tools.json"), b, 0o644)
	}
	var pick *tool
	best := 0
	for i := range all {
		n := strings.ToLower(all[i].Name)
		s := 0
		if strings.Contains(n, "champion") && strings.Contains(n, "analysis") {
			s = 2
			if strings.Contains(n, "lol") {
				s = 3
			}
		}
		if s > best {
			best, pick = s, &all[i]
		}
	}
	if pick == nil {
		var names []string
		for _, t := range all {
			names = append(names, t.Name)
		}
		return nil, fmt.Errorf("no champion analysis tool among %d tools (%s)", len(all), truncate(strings.Join(names, ", "), 300))
	}
	var schema struct {
		Properties map[string]struct {
			Type        any    `json:"type"`
			Enum        []any  `json:"enum"`
			Description string `json:"description"`
			Items       struct {
				Enum []any `json:"enum"`
			} `json:"items"`
			Examples []any `json:"examples"`
			Default  any   `json:"default"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	_ = json.Unmarshal(pick.InputSchema, &schema)
	t = &toolSpec{name: pick.Name, props: map[string]propSpec{}, required: map[string]bool{}}
	for _, r := range schema.Required {
		t.required[r] = true
	}
	var hintText strings.Builder
	hintText.WriteString(pick.Description)
	for name, ps := range schema.Properties {
		sp := propSpec{typ: fmt.Sprint(ps.Type)}
		for _, e := range ps.Enum {
			sp.enum = append(sp.enum, fmt.Sprint(e))
		}
		t.props[name] = sp
		ln := strings.ToLower(name)
		if strings.Contains(ln, "field") || strings.Contains(ln, "output") {
			t.fields = name
			hintText.WriteString(" " + ps.Description)
			for _, e := range ps.Items.Enum {
				hintText.WriteString(" " + fmt.Sprint(e))
			}
			for _, e := range ps.Examples {
				if b, err := json.Marshal(e); err == nil {
					hintText.Write(b)
				}
			}
		}
	}
	t.hints = buildPaths(hintText.String())
	p.mu.Lock()
	p.tool = t
	p.mu.Unlock()
	p.logf("OP.GG: using tool %s (params: %s; field hints: %d)", t.name, strings.Join(keys(t.props), ", "), len(t.hints))
	return t, nil
}

func keys(m map[string]propSpec) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matchEnum returns the enum value matching one of the candidates, or "".
func matchEnum(enum []string, cands ...string) string {
	for _, c := range cands {
		nc := norm(c)
		if nc == "" {
			continue
		}
		for _, e := range enum {
			if norm(e) == nc {
				return e
			}
		}
	}
	for _, c := range cands {
		nc := norm(c)
		if len(nc) < 3 {
			continue
		}
		for _, e := range enum {
			ne := norm(e)
			if strings.HasPrefix(ne, nc) || strings.HasPrefix(nc, ne) && len(ne) >= 3 {
				return e
			}
		}
	}
	return ""
}

var positionAliases = map[string][]string{
	"top":     {"top"},
	"jungle":  {"jungle", "jg", "jungler"},
	"mid":     {"mid", "middle"},
	"adc":     {"adc", "bottom", "bot", "adcarry", "carry"},
	"support": {"support", "utility", "sup", "supp"},
	"none":    {"none", "all", "any", "aram"},
}

var modeAliases = map[string][]string{
	"ranked": {"ranked", "rank", "solo", "rankedsolo", "solorank", "classic", "normal"},
	"aram":   {"aram"},
	"urf":    {"urf", "arurf"},
}

// args fills the tool's parameters for this request.
func (t *toolSpec) args(r Request) map[string]any {
	a := map[string]any{}
	for name, ps := range t.props {
		if name == t.fields {
			continue
		}
		ln := strings.ToLower(name)
		var v string
		switch {
		case strings.Contains(ln, "champion"):
			v = matchEnum(ps.enum, r.ChampID, r.ChampName)
			if v == "" && len(ps.enum) == 0 {
				v = strings.ToUpper(norm(r.ChampID))
			}
		case strings.Contains(ln, "position") || strings.Contains(ln, "lane") || strings.Contains(ln, "role"):
			v = matchEnum(ps.enum, positionAliases[r.Position]...)
			if v == "" && len(ps.enum) == 0 {
				v = strings.ToUpper(r.Position)
			}
		case strings.Contains(ln, "mode") || strings.Contains(ln, "queue"):
			v = matchEnum(ps.enum, modeAliases[r.Mode]...)
			if v == "" && len(ps.enum) == 0 {
				v = strings.ToUpper(r.Mode)
			}
		case strings.Contains(ln, "lang") || strings.Contains(ln, "locale"):
			v = matchEnum(ps.enum, "en_US", "en")
			if v == "" && len(ps.enum) == 0 {
				v = "en_US"
			}
		case strings.Contains(ln, "tier") || strings.Contains(ln, "rank"):
			v = matchEnum(ps.enum, "emerald_plus", "platinum_plus", "all")
		case strings.Contains(ln, "region"):
			v = matchEnum(ps.enum, "global", "all", "world")
		}
		if v == "" && t.required[name] && len(ps.enum) > 0 {
			v = ps.enum[0]
		}
		if v != "" {
			a[name] = v
		}
	}
	return a
}

// open starts an MCP session. The client is never nil, so callers can always
// defer close.
func (p *Provider) open(ctx context.Context) (*mcpClient, error) {
	c := &mcpClient{url: p.url, http: p.http, agent: p.agent}
	raw, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "Counterbuild", "version": p.version},
	})
	if err != nil {
		return c, err
	}
	var ir struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(raw, &ir)
	c.proto = ir.ProtocolVersion
	c.notify(ctx, "notifications/initialized")
	return c, nil
}

// query runs one MCP session: initialize, find the tool, call it until we
// get item data or run out of field selections to try.
func (p *Provider) query(ctx context.Context, r Request, cat *engine.Catalog) (*extracted, error) {
	c, err := p.open(ctx)
	defer c.close(ctx)
	if err != nil {
		return nil, err
	}
	t, err := p.findTool(ctx, c)
	if err != nil {
		return nil, err
	}
	base := t.args(r)

	var tries [][]string
	if t.fields == "" {
		tries = [][]string{nil}
	} else {
		if len(t.hints) > 0 {
			tries = append(tries, t.hints)
		}
		tries = append(tries, []string{"data"})
	}
	var lastErr error
	tried := map[string]bool{}
	for i := 0; i < len(tries) && i < 4; i++ {
		f := tries[i]
		sig := strings.Join(f, "|")
		if tried[sig] {
			continue
		}
		tried[sig] = true
		args := map[string]any{}
		for k, v := range base {
			args[k] = v
		}
		if f != nil {
			args[t.fields] = f
		}
		raw, err := c.call(ctx, "tools/call", map[string]any{"name": t.name, "arguments": args})
		var text string
		if err != nil {
			lastErr = err
			text = err.Error()
		} else {
			p.saveRaw("opgg_last_response.txt", t.name, args, raw)
			ex, txt, isErr := extractResult(cat, raw)
			if !isErr && ex != nil && len(ex.Pop) > 0 {
				return ex, nil
			}
			text = txt
			if isErr {
				lastErr = errors.New(truncate(strings.TrimSpace(txt), 400))
			} else {
				lastErr = nil
			}
		}
		// Error messages often list the valid fields; try those next.
		if t.fields != "" {
			if hints := buildPaths(text); len(hints) > 0 {
				tries = append(tries, hints)
			}
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return &extracted{Pop: map[int]float64{}, WR: map[int]float64{}}, nil
}

func (p *Provider) saveRaw(file, tool string, args map[string]any, raw json.RawMessage) {
	_ = os.MkdirAll(p.dir, 0o755)
	a, _ := json.Marshal(args)
	body := fmt.Sprintf("tool: %s\narguments: %s\n\n%s\n", tool, a, raw)
	_ = os.WriteFile(filepath.Join(p.dir, file), []byte(body), 0o644)
}
