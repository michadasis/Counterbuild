package meta

import (
	"net/http/httptest"
	"testing"
	"time"
)

// A trimmed copy of a real lol_list_aram_augments answer for Ezreal.
const augText = `class LolListAramAugments: data
class Data: augments
class Augment: id,name,tier,performance,popular

LolListAramAugments(Data([Augment(2123,"Overloaded",3,69.87,0.43),Augment(1402,"Stats on Stats on Stats!",4,81.42,0.15),Augment(1404,"Stats!",4,76.66,0.13),Augment(1337,"King Me",4,58.64,0.1),Augment(1250,null,5,74.73,0),Augment(9001,"Odd \"Name\", (with) commas",5,70,0.02)]))`

func TestParseAugmentText(t *testing.T) {
	augs := parseAugmentText(augText)
	if len(augs) != 6 {
		t.Fatalf("got %d augments: %+v", len(augs), augs)
	}
	if a := augs[1]; a.ID != 1402 || a.Name != "Stats on Stats on Stats!" || a.Tier != 4 || a.Score != 81.42 || a.Pop != 0.15 {
		t.Fatalf("bad parse: %+v", a)
	}
	if augs[4].Name != "" || augs[4].ID != 1250 {
		t.Fatalf("null name should be empty: %+v", augs[4])
	}
	if augs[5].Name != `Odd "Name", (with) commas` {
		t.Fatalf("quoted name: %q", augs[5].Name)
	}
}

func TestParseAugmentJSONAndPercent(t *testing.T) {
	raw := `{"content":[{"type":"text","text":"{\"data\":{\"augments\":[{\"id\":1,\"name\":\"A\",\"tier\":3,\"performance\":70.5,\"popular\":43},{\"id\":2,\"name\":null,\"tier\":4,\"performance\":60,\"popular\":5}]}}"}]}`
	augs, err := parseAugmentResult([]byte(raw))
	if err != nil || len(augs) != 2 {
		t.Fatalf("got %+v, %v", augs, err)
	}
	if augs[0].Pop != 0.43 || augs[1].Pop != 0.05 || augs[0].Score != 70.5 {
		t.Fatalf("percentages not normalized: %+v", augs)
	}
}

func TestFetchAugments(t *testing.T) {
	f := &fakeServer{t: t, schema: schema(false, ""),
		handle: func(args map[string]any) map[string]any { return textResult(augText) }}
	srv := httptest.NewServer(f)
	defer srv.Close()
	dir := t.TempDir()
	ready := make(chan struct{}, 1)
	p := NewProvider(srv.URL, dir, "test", nil, func() { ready <- struct{}{} })
	if p.Augments(81, "16.21.1") != nil {
		t.Fatalf("expected nothing before the first fetch")
	}
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatalf("fetch did not finish")
	}
	augs := p.Augments(81, "16.21.1")
	if len(augs) != 6 {
		t.Fatalf("got %+v", augs)
	}
	call := f.calls[0]
	if call["champion_id"] != float64(81) || call["lang"] != "en_US" {
		t.Fatalf("bad arguments: %v", call)
	}
	// Cached on disk for the next run.
	p2 := NewProvider("http://127.0.0.1:1", dir, "test", nil, nil)
	if got := p2.Augments(81, "16.21.1"); len(got) != 6 {
		t.Fatalf("disk cache not used: %+v", got)
	}
}
