package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Augment is one ARAM Mayhem augment's stats on a champion, from OP.GG.
type Augment struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Known bool    `json:"known"` // OP.GG has data on it for this champion
	Tier  int     `json:"tier"`  // OP.GG tier when Known: 0 = S, 1 = A, 2 = B, 3 to 5 lower
	Rank  int     `json:"rank"`  // place in OP.GG's list for the champion, 1 = best; 0 if unknown
	Score float64 `json:"score"` // OP.GG performance score, higher is better
	Pop   float64 `json:"pop"`   // how often it is picked, 0..1
	Top   bool    `json:"-"`     // guessed to be S to B: missing from OP.GG's data feed, which leaves those out
}

type augCache struct {
	Augs    []Augment `json:"augs"`
	Fetched time.Time `json:"fetched"`
}

// DefaultNamesURL is CommunityDragon's copy of the game's augment list. It
// names every augment, including the ones OP.GG has no stats or no name for.
const DefaultNamesURL = "https://raw.communitydragon.org/latest/plugins/rcp-be-lol-game-data/global/default/v1/cherry-augments.json"

type namesCache struct {
	Names   map[int]string `json:"names"`
	Fetched time.Time      `json:"fetched"`
}

func (p *Provider) namesPath() string { return filepath.Join(p.dir, "augment_names.json") }

// AugmentNames returns the names of all ARAM Mayhem augments by id (nil until
// loaded), refreshing them in the background once a day.
func (p *Provider) AugmentNames() map[int]string {
	p.mu.Lock()
	c := p.names
	if c == nil {
		if b, err := os.ReadFile(p.namesPath()); err == nil {
			var d namesCache
			if json.Unmarshal(b, &d) == nil && len(d.Names) > 0 {
				c = &d
				p.names = c
			}
		}
	}
	fresh := c != nil && time.Since(c.Fetched) < freshFor
	start := !fresh && !p.namesBusy && time.Now().After(p.retryAt["names"])
	if start {
		p.namesBusy = true
	}
	p.mu.Unlock()
	if start {
		go p.fetchNames()
	}
	if c == nil {
		return nil
	}
	return c.Names
}

func (p *Provider) fetchNames() {
	defer func() {
		if rec := recover(); rec != nil {
			p.logf("augment names: fetch crashed: %v", rec)
		}
		p.mu.Lock()
		p.namesBusy = false
		p.mu.Unlock()
	}()
	names, err := p.downloadNames()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.retryAt["names"] = time.Now().Add(retryAfter)
		p.logf("augment names: %v", err)
		return
	}
	c := &namesCache{Names: names, Fetched: time.Now()}
	p.names = c
	_ = os.MkdirAll(p.dir, 0o755)
	if b, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(p.namesPath()+".tmp", b, 0o644)
		_ = os.Rename(p.namesPath()+".tmp", p.namesPath())
	}
	p.logf("augment names: %d ARAM Mayhem augments", len(names))
	if p.onReady != nil {
		go p.onReady()
	}
}

func (p *Provider) downloadNames() (map[int]string, error) {
	req, err := http.NewRequest(http.MethodGet, p.NamesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", p.agent)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, p.NamesURL)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	return parseAugmentNames(b)
}

// parseAugmentNames reads cherry-augments.json. Arena augments have ids below
// 1000; ARAM Mayhem ones are 1000 and up, the same ids OP.GG uses.
func parseAugmentNames(b []byte) (map[int]string, error) {
	var list []struct {
		ID   int    `json:"id"`
		Name string `json:"nameTRA"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	names := map[int]string{}
	for _, a := range list {
		if a.ID >= 1000 && strings.TrimSpace(a.Name) != "" {
			names[a.ID] = strings.TrimSpace(a.Name)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("no ARAM Mayhem augments in the list")
	}
	return names, nil
}

// MergeAugments combines the full name list with OP.GG's stats: every augment
// gets a name (the game's own, which is what the cards show).
//
// Stats from OP.GG's augment page list every tier, so an augment missing from
// them is unknown. Stats from OP.GG's data feed (the fallback) only have tiers
// 3 to 5 ("only tier 3 or higher") while the website lists the champion's S, A
// and B augments (tiers 0 to 2) above them, so an augment missing from the
// feed is marked Top: somewhere in S to B.
func MergeAugments(stats []Augment, names map[int]string) []Augment {
	ranked := false
	for _, a := range stats {
		if a.Rank > 0 {
			ranked = true
			break
		}
	}
	out := make([]Augment, 0, len(names)+len(stats))
	seen := map[int]bool{}
	for _, a := range stats {
		if n := names[a.ID]; n != "" {
			a.Name = n
		}
		if a.Name == "" {
			continue
		}
		seen[a.ID] = true
		out = append(out, a)
	}
	for id, n := range names {
		if !seen[id] {
			out = append(out, Augment{ID: id, Name: n, Top: len(stats) > 0 && !ranked})
		}
	}
	return out
}

const augTool = "lol_list_aram_augments"

var augFields = []string{"data.augments[].{id,name,tier,performance,popular}"}

// DefaultAugmentPageURL is OP.GG's ARAM Mayhem augment page for a champion;
// %s is the champion's Data Dragon id in lowercase ("monkeyking"). Its
// robots.txt allows it; it is read once per champion per day.
const DefaultAugmentPageURL = "https://op.gg/lol/modes/aram-mayhem/%s/augments"

// reAugRow is one augment in the data embedded in OP.GG's augment page:
// {"id":3049,"tier":0,"performance":85.85,"popular":5.12,...}, with the
// quotes escaped inside the page's script data.
var reAugRow = regexp.MustCompile(`\\?"id\\?":(\d+),\\?"tier\\?":(\d+),\\?"performance\\?":([\d.]+),\\?"popular\\?":([\d.]+)`)

// parseAugmentPage reads the champion's augments from OP.GG's augment page in
// the order the page lists them: by tier (S first), then by popularity.
func parseAugmentPage(b []byte) []Augment {
	var out []Augment
	seen := map[int]bool{}
	for _, m := range reAugRow.FindAllSubmatch(b, -1) {
		id, _ := strconv.Atoi(string(m[1]))
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		tier, _ := strconv.Atoi(string(m[2]))
		perf, _ := strconv.ParseFloat(string(m[3]), 64)
		pop, _ := strconv.ParseFloat(string(m[4]), 64)
		out = append(out, Augment{ID: id, Known: true, Tier: tier, Rank: len(out) + 1, Score: perf, Pop: pop / 100})
	}
	return out
}

func (p *Provider) augmentPage(ctx context.Context, champID string) ([]Augment, error) {
	url := fmt.Sprintf(p.AugmentPageURL, strings.ToLower(champID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", p.agent)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	augs := parseAugmentPage(b)
	if len(augs) < 30 {
		return nil, fmt.Errorf("no augment list on %s (found %d)", url, len(augs))
	}
	return augs, nil
}

func (p *Provider) augPath(version string, champKey int) string {
	return filepath.Join(p.dir, "opgg", version, fmt.Sprintf("augment_tiers_%d.json", champKey))
}

// Augments returns the cached augment stats for a champion (by its numeric
// Data Dragon key and its id), or nil until they have loaded. Like Lookup it
// never blocks: a missing or day-old cache is refreshed in the background.
func (p *Provider) Augments(champKey int, champID, version string) []Augment {
	if champKey <= 0 || champID == "" || version == "" {
		return nil
	}
	k := fmt.Sprintf("%s/augments/%d", version, champKey)
	p.mu.Lock()
	c := p.augMem[k]
	if c == nil {
		if b, err := os.ReadFile(p.augPath(version, champKey)); err == nil {
			var d augCache
			if json.Unmarshal(b, &d) == nil {
				c = &d
				p.augMem[k] = c
			}
		}
	}
	fresh := c != nil && time.Since(c.Fetched) < freshFor
	start := !fresh && !p.augBusy && time.Now().After(p.retryAt[k])
	if start {
		p.augBusy = true
	}
	p.mu.Unlock()
	if start {
		go p.fetchAugments(champKey, champID, version, k)
	}
	if c == nil {
		return nil
	}
	return c.Augs
}

func (p *Provider) fetchAugments(champKey int, champID, version, k string) {
	defer func() {
		if rec := recover(); rec != nil {
			p.logf("OP.GG augments: fetch crashed: %v", rec)
		}
		p.mu.Lock()
		p.augBusy = false
		p.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	source := "augment page"
	augs, err := p.augmentPage(ctx, champID)
	if err != nil {
		// The page has every tier in OP.GG's order; the data feed is the
		// fallback, without the S, A and B augments.
		p.logf("OP.GG augments: %s: %v; using the data feed", champID, err)
		source = "data feed"
		augs, err = p.queryAugments(ctx, champKey)
		if err == nil && len(augs) == 0 {
			err = errors.New("no augments in the response (saved to meta/opgg_augments_last_response.txt)")
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.retryAt[k] = time.Now().Add(retryAfter)
		p.logf("OP.GG augments: champion %d failed: %v", champKey, err)
		return
	}
	c := &augCache{Augs: augs, Fetched: time.Now()}
	p.augMem[k] = c
	path := p.augPath(version, champKey)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if b, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(path+".tmp", b, 0o644)
		_ = os.Rename(path+".tmp", path)
	}
	p.logf("OP.GG augments: %s: %d augments from the %s", champID, len(augs), source)
	if p.onReady != nil {
		go p.onReady()
	}
}

func (p *Provider) queryAugments(ctx context.Context, champKey int) ([]Augment, error) {
	c, err := p.open(ctx)
	defer c.close(ctx)
	if err != nil {
		return nil, err
	}
	args := map[string]any{"champion_id": champKey, "lang": "en_US", "desired_output_fields": augFields}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": augTool, "arguments": args})
	if err != nil {
		return nil, err
	}
	p.saveRaw("opgg_augments_last_response.txt", augTool, args, raw)
	return parseAugmentResult(raw)
}

// parseAugmentResult reads a tools/call result as JSON when it is JSON, else as
// the compact text form OP.GG returns:
//
//	class Augment: id,name,tier,performance,popular
//	LolListAramAugments(Data([Augment(2123,"Overloaded",3,69.87,0.43),...]))
func parseAugmentResult(raw json.RawMessage) ([]Augment, error) {
	var tr toolResult
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, err
	}
	var texts []string
	for _, c := range tr.Content {
		if c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	text := strings.Join(texts, "\n")
	if tr.IsError {
		return nil, errors.New(truncate(strings.TrimSpace(text), 400))
	}
	var augs []Augment
	if len(tr.StructuredContent) > 0 && string(tr.StructuredContent) != "null" {
		augs = parseAugmentJSON(tr.StructuredContent)
	}
	if len(augs) == 0 {
		augs = parseAugmentJSON([]byte(strings.TrimSpace(text)))
	}
	if len(augs) == 0 {
		augs = parseAugmentText(text)
	}
	for i := range augs {
		augs[i].Known = true
	}
	return normalizePop(augs), nil
}

func parseAugmentJSON(b []byte) []Augment {
	var v struct {
		Data struct {
			Augments []struct {
				ID          json.Number `json:"id"`
				Name        *string     `json:"name"`
				Tier        json.Number `json:"tier"`
				Performance json.Number `json:"performance"`
				Popular     json.Number `json:"popular"`
			} `json:"augments"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	var out []Augment
	for _, a := range v.Data.Augments {
		id, _ := a.ID.Int64()
		if id <= 0 {
			continue
		}
		au := Augment{ID: int(id)}
		if a.Name != nil {
			au.Name = *a.Name
		}
		t, _ := a.Tier.Int64()
		au.Tier = int(t)
		au.Score, _ = a.Performance.Float64()
		au.Pop, _ = a.Popular.Float64()
		out = append(out, au)
	}
	return out
}

func parseAugmentText(text string) []Augment {
	order := []string{"id", "name", "tier", "performance", "popular"}
	if i := strings.Index(text, "class Augment:"); i >= 0 {
		line := text[i+len("class Augment:"):]
		if j := strings.IndexByte(line, '\n'); j >= 0 {
			line = line[:j]
		}
		var f []string
		for _, s := range strings.Split(line, ",") {
			f = append(f, strings.TrimSpace(s))
		}
		if len(f) > 1 {
			order = f
		}
	}
	var out []Augment
	rest := text
	for {
		i := strings.Index(rest, "Augment(")
		if i < 0 {
			break
		}
		rest = rest[i+len("Augment("):]
		args, n, ok := splitArgs(rest)
		if !ok {
			continue
		}
		rest = rest[n:]
		var a Augment
		for k, name := range order {
			if k >= len(args) || args[k] == nil {
				continue
			}
			v := *args[k]
			switch name {
			case "id":
				a.ID, _ = strconv.Atoi(v)
			case "name":
				a.Name = v
			case "tier":
				a.Tier, _ = strconv.Atoi(v)
			case "performance":
				a.Score, _ = strconv.ParseFloat(v, 64)
			case "popular":
				a.Pop, _ = strconv.ParseFloat(v, 64)
			}
		}
		if a.ID > 0 {
			out = append(out, a)
		}
	}
	return out
}

// splitArgs reads comma separated values up to the closing parenthesis. Quoted
// values may contain commas and parentheses; null becomes a nil entry. It
// returns the values and how many bytes it consumed.
func splitArgs(s string) ([]*string, int, bool) {
	var out []*string
	i := 0
	for {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i >= len(s) {
			return nil, i, false
		}
		var v string
		if s[i] == '"' {
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				return nil, j, false
			}
			q := s[i : j+1]
			if u, err := strconv.Unquote(q); err == nil {
				v = u
			} else {
				v = q[1 : len(q)-1]
			}
			out = append(out, &v)
			i = j + 1
		} else {
			j := i
			for j < len(s) && s[j] != ',' && s[j] != ')' {
				j++
			}
			v = strings.TrimSpace(s[i:j])
			if v == "null" || v == "None" || v == "" {
				out = append(out, nil)
			} else {
				out = append(out, &v)
			}
			i = j
		}
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i >= len(s) {
			return nil, i, false
		}
		if s[i] == ')' {
			return out, i + 1, true
		}
		if s[i] != ',' {
			return nil, i, false
		}
		i++
	}
}

// normalizePop turns percentages into 0..1 if the source used 0..100.
func normalizePop(augs []Augment) []Augment {
	for _, a := range augs {
		if a.Pop > 1 {
			for i := range augs {
				augs[i].Pop /= 100
			}
			break
		}
	}
	return augs
}
