package meta

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"counterbuild/engine"
)

var (
	catOnce sync.Once
	catVal  *engine.Catalog
)

func testCatalog(t *testing.T) *engine.Catalog {
	t.Helper()
	catOnce.Do(func() {
		items, _ := os.ReadFile("../engine/testdata/item.json")
		champs, _ := os.ReadFile("../engine/testdata/champion.json")
		catVal, _ = engine.LoadCatalog(items, champs)
	})
	if catVal == nil {
		t.Fatal("no catalog")
	}
	return catVal
}

// fakeServer imitates an MCP server over Streamable HTTP.
type fakeServer struct {
	t       *testing.T
	sse     bool
	schema  map[string]any
	handle  func(args map[string]any) (result map[string]any)
	mu      sync.Mutex
	calls   []map[string]any
	headers []http.Header
	deleted bool
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.headers = append(f.headers, r.Header.Clone())
	f.mu.Unlock()
	if r.Method == http.MethodDelete {
		f.mu.Lock()
		f.deleted = true
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}
	b, _ := io.ReadAll(r.Body)
	var req struct {
		ID     any            `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if req.ID == nil { // notification
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result any
	switch req.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "sess-123")
		result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "fake", "version": "1"}}
	case "tools/list":
		result = map[string]any{"tools": []any{
			map[string]any{"name": "lol_list_champions", "description": "list", "inputSchema": map[string]any{"type": "object"}},
			map[string]any{"name": "lol_get_champion_analysis", "description": "Champion analysis with builds.", "inputSchema": f.schema},
		}}
	case "tools/call":
		args, _ := req.Params["arguments"].(map[string]any)
		f.mu.Lock()
		f.calls = append(f.calls, args)
		f.mu.Unlock()
		result = f.handle(args)
	default:
		result = map[string]any{}
	}
	resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	if f.sse {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", resp)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

func schema(withFields bool, fieldDesc string) map[string]any {
	props := map[string]any{
		"champion":  map[string]any{"type": "string", "enum": []any{"AHRI", "JINX", "MONKEY_KING", "EZREAL"}},
		"position":  map[string]any{"type": "string", "enum": []any{"TOP", "JUNGLE", "MID", "ADC", "SUPPORT"}},
		"game_mode": map[string]any{"type": "string", "enum": []any{"RANKED", "ARAM"}},
		"lang":      map[string]any{"type": "string", "enum": []any{"ko_KR", "en_US"}},
	}
	req := []any{"champion", "position", "game_mode", "lang"}
	if withFields {
		props["desired_output_fields"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": fieldDesc}
		req = append(req, "desired_output_fields")
	}
	return map[string]any{"type": "object", "properties": props, "required": req}
}

func textResult(s string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": s}}}
}

const buildJSON = `{"data":{"summary":{"win_rate":0.51},
 "runes":[{"primary_rune_ids":[8010,8009,9104,8014],"pick_rate":0.6}],
 "core_items":[{"ids":[6655,3020,3089],"play":5200,"win":2800,"pick_rate":0.34},
               {"ids":[3118,3020,3089],"play":2100,"win":1100,"pick_rate":0.14}],
 "boots":[{"ids":[3020],"play":9000,"win":4700,"pick_rate":0.78}],
 "last_items":[{"ids":[3157],"play":3000,"win":1650},{"ids":[3135],"play":2500,"win":1300}]}}`

func runFetch(t *testing.T, f *fakeServer, r Request) (*Provider, *engine.Meta) {
	t.Helper()
	srv := httptest.NewServer(f)
	defer srv.Close()
	dir := t.TempDir()
	ready := make(chan struct{}, 1)
	var logs []string
	var lmu sync.Mutex
	p := NewProvider(srv.URL, dir, "test", func(format string, a ...any) {
		lmu.Lock()
		logs = append(logs, fmt.Sprintf(format, a...))
		lmu.Unlock()
	}, func() { ready <- struct{}{} })
	cat := testCatalog(t)
	if m := p.Lookup(r, cat); m != nil {
		t.Fatalf("expected nothing before the first fetch")
	}
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		lmu.Lock()
		defer lmu.Unlock()
		t.Fatalf("fetch did not finish; logs: %v", logs)
	}
	m := p.Lookup(r, cat)
	if m == nil {
		t.Fatalf("no meta after fetch; logs: %v", logs)
	}
	return p, m
}

func TestFetchJSONBuildsOverSSE(t *testing.T) {
	f := &fakeServer{t: t, sse: true, schema: schema(true, "Fields: data.core_items[].{ids,play,win,pick_rate}, data.boots[].{ids,play,win}, data.runes[]"),
		handle: func(args map[string]any) map[string]any { return textResult(buildJSON) }}
	p, m := runFetch(t, f, Request{ChampID: "Ahri", ChampName: "Ahri", Position: "mid", Mode: "ranked", Version: "16.21.1"})

	// Rabadon's is in both core builds, Luden's in the most picked one, Sorc in all.
	if m.Pop[3089] < 0.99 || m.Pop[3020] < 0.99 || m.Pop[6655] < 0.6 {
		t.Fatalf("core items not on top: %v", m.Pop)
	}
	if m.Pop[3157] <= 0 || m.Pop[3157] >= m.Pop[6655] {
		t.Fatalf("late items should count, but less than core: %v", m.Pop)
	}
	if _, bad := m.Pop[8010]; bad {
		t.Fatalf("rune id 8010 counted as Bloodletter's Curse")
	}
	if wr := m.WR[6655]; wr < 0.53 || wr > 0.54 {
		t.Fatalf("unexpected Luden's win rate %.3f", wr)
	}
	call := f.calls[0]
	if call["champion"] != "AHRI" || call["position"] != "MID" || call["game_mode"] != "RANKED" || call["lang"] != "en_US" {
		t.Fatalf("bad arguments: %v", call)
	}
	fields, _ := call["desired_output_fields"].([]any)
	if len(fields) == 0 || !strings.Contains(fmt.Sprint(fields), "core_items") || strings.Contains(fmt.Sprint(fields), "runes") {
		t.Fatalf("expected build fields from the schema hints, got %v", fields)
	}
	sawSession, sawProto := false, false
	for _, h := range f.headers[1:] {
		if h.Get("Mcp-Session-Id") == "sess-123" {
			sawSession = true
		}
		if h.Get("MCP-Protocol-Version") == "2025-06-18" {
			sawProto = true
		}
	}
	if !sawSession || !sawProto {
		t.Fatalf("session or protocol header not sent after initialize")
	}
	// Cached on disk for the next run.
	p2 := NewProvider("http://127.0.0.1:1", p.dir[:len(p.dir)-len("/meta")], "test", nil, nil)
	if m2 := p2.Lookup(Request{ChampID: "Ahri", ChampName: "Ahri", Position: "mid", Mode: "ranked", Version: "16.21.1"}, testCatalog(t)); m2 == nil || m2.Pop[3089] < 0.99 {
		t.Fatalf("disk cache not used")
	}
}

func TestRetriesWithFieldsFromError(t *testing.T) {
	f := &fakeServer{t: t, schema: schema(true, "Fields to return.")}
	f.handle = func(args map[string]any) map[string]any {
		fields := fmt.Sprint(args["desired_output_fields"])
		if !strings.Contains(fields, "core_items") {
			return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text",
				"text": "Invalid field 'data'. Available fields include data.core_items[].{ids,pick_rate,win_rate}, data.boots[].{ids,pick_rate}"}}}
		}
		return textResult(`{"data":{"core_items":[{"ids":[3078,3004],"pick_rate":42.1,"win_rate":52.3}],"boots":[{"ids":[3158],"pick_rate":61}]}}`)
	}
	_, m := runFetch(t, f, Request{ChampID: "Ezreal", ChampName: "Ezreal", Position: "adc", Mode: "ranked", Version: "16.21.1"})
	if m.Pop[3078] < 0.99 || m.Pop[3004] < 0.99 || m.Pop[3158] <= 0 {
		t.Fatalf("unexpected popularity %v", m.Pop)
	}
	if wr := m.WR[3078]; wr < 0.52 || wr > 0.53 {
		t.Fatalf("percent win rate not normalized: %v", wr)
	}
	if len(f.calls) != 2 {
		t.Fatalf("expected one retry, got %d calls", len(f.calls))
	}
}

func TestTextNamesFallback(t *testing.T) {
	f := &fakeServer{t: t, schema: schema(false, ""),
		handle: func(args map[string]any) map[string]any {
			return textResult("Jinx ADC build: Yun Tal Wildarrows, then Infinity Edge, Berserker's Greaves, later Lord Dominik's Regards.")
		}}
	_, m := runFetch(t, f, Request{ChampID: "Jinx", ChampName: "Jinx", Position: "adc", Mode: "ranked", Version: "16.21.1"})
	if m.Pop[3032] < 0.99 || m.Pop[3031] <= 0 || m.Pop[3036] <= 0 || m.Pop[3006] <= 0 {
		t.Fatalf("names not picked up: %v", m.Pop)
	}
	if m.Pop[3032] <= m.Pop[3036] {
		t.Fatalf("earlier mentions should rank higher")
	}
	if _, ok := f.calls[0]["desired_output_fields"]; ok {
		t.Fatalf("sent field selection to a tool without it")
	}
}

func TestEnumMatching(t *testing.T) {
	if v := matchEnum([]string{"AHRI", "MONKEY_KING", "NUNU_WILLUMP"}, "MonkeyKing", "Wukong"); v != "MONKEY_KING" {
		t.Fatalf("got %q", v)
	}
	if v := matchEnum([]string{"AHRI", "WUKONG"}, "MonkeyKing", "Wukong"); v != "WUKONG" {
		t.Fatalf("got %q", v)
	}
	if v := matchEnum([]string{"NUNU_WILLUMP"}, "Nunu", "Nunu & Willump"); v != "NUNU_WILLUMP" {
		t.Fatalf("got %q", v)
	}
	if v := matchEnum([]string{"top", "jungle", "middle", "bottom", "utility"}, positionAliases["adc"]...); v != "bottom" {
		t.Fatalf("got %q", v)
	}
	if v := matchEnum([]string{"RANKED_SOLO_5x5", "ARAM"}, modeAliases["ranked"]...); v != "RANKED_SOLO_5x5" {
		t.Fatalf("got %q", v)
	}
	if v := matchEnum([]string{"VI", "VIEGO"}, "Vi"); v != "VI" {
		t.Fatalf("got %q", v)
	}
}

func TestFailureIsQuietAndRetriedLater(t *testing.T) {
	dir := t.TempDir()
	done := make(chan struct{}, 1)
	var logs []string
	p := NewProvider("http://127.0.0.1:1/mcp", dir, "test", func(f string, a ...any) {
		logs = append(logs, fmt.Sprintf(f, a...))
		done <- struct{}{}
	}, nil)
	r := Request{ChampID: "Ahri", ChampName: "Ahri", Position: "mid", Mode: "ranked", Version: "16.21.1"}
	if p.Lookup(r, testCatalog(t)) != nil {
		t.Fatal("unexpected data")
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("no failure logged")
	}
	time.Sleep(50 * time.Millisecond)
	p.mu.Lock()
	next := p.retryAt[r.Version+"/"+r.key()]
	inflight := p.inflight
	p.mu.Unlock()
	if inflight || time.Until(next) < 10*time.Minute {
		t.Fatalf("expected a delayed retry, inflight=%v next=%v", inflight, next)
	}
	if _, err := os.Stat(filepath.Join(dir, "meta", "opgg", "16.21.1", r.key()+".json")); err == nil {
		t.Fatal("failure should not write a cache file")
	}
}
