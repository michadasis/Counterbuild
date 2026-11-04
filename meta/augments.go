package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Augment is one ARAM Mayhem augment's stats on a champion, from OP.GG.
type Augment struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Tier  int     `json:"tier"`
	Score float64 `json:"score"` // OP.GG performance score, higher is better
	Pop   float64 `json:"pop"`   // how often it is picked, 0..1
}

type augCache struct {
	Augs    []Augment `json:"augs"`
	Fetched time.Time `json:"fetched"`
}

const augTool = "lol_list_aram_augments"

var augFields = []string{"data.augments[].{id,name,tier,performance,popular}"}

func (p *Provider) augPath(version string, champKey int) string {
	return filepath.Join(p.dir, "opgg", version, fmt.Sprintf("augments_%d.json", champKey))
}

// Augments returns the cached augment stats for a champion (by its numeric
// Data Dragon key), or nil until they have loaded. Like Lookup it never blocks:
// a missing or day-old cache is refreshed in the background.
func (p *Provider) Augments(champKey int, version string) []Augment {
	if champKey <= 0 || version == "" {
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
		go p.fetchAugments(champKey, version, k)
	}
	if c == nil {
		return nil
	}
	return c.Augs
}

func (p *Provider) fetchAugments(champKey int, version, k string) {
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
	augs, err := p.queryAugments(ctx, champKey)
	if err == nil && len(augs) == 0 {
		err = errors.New("no augments in the response (saved to meta/opgg_augments_last_response.txt)")
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
	p.logf("OP.GG augments: champion %d: %d augments", champKey, len(augs))
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
