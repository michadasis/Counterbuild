package meta

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestParseRepr(t *testing.T) {
	v, ok := parseRepr("class Top: data\nclass Data: name,items,empty,flag\nclass It: ids,rate\n\nTop(Data(\"A \\\"b\\\", (c)\",[It([1,2],0.5),It([3],-1e-2)],[],null))")
	if !ok {
		t.Fatal("not parsed")
	}
	data := v.(map[string]any)["data"].(map[string]any)
	if data["name"] != `A "b", (c)` || data["flag"] != nil || len(data["empty"].([]any)) != 0 {
		t.Fatalf("got %#v", data)
	}
	items := data["items"].([]any)
	if len(items) != 2 || items[1].(map[string]any)["rate"] != -0.01 {
		t.Fatalf("got %#v", items)
	}
	for _, bad := range []string{"", "no classes here", "class A: x\n\nA(1,", "class A: x\n\nA(1 2)"} {
		if _, ok := parseRepr(bad); ok {
			t.Fatalf("%q should not parse", bad)
		}
	}
}

// TestOPGGTextBuildOrder reads OP.GG's real ARAM answer for Soraka, where
// Moonstone Renewer is the first item, Warmog's comes in the core build and
// Redemption is a 4th/5th item.
func TestOPGGTextBuildOrder(t *testing.T) {
	text, err := os.ReadFile("testdata/opgg_soraka_aram.txt")
	if err != nil {
		t.Fatal(err)
	}
	raw := mustJSON(t, textResult(string(text)))
	ex, _, isErr := extractResult(testCatalog(t), raw)
	if isErr || ex == nil || ex.How != "opgg text" {
		t.Fatalf("not read as OP.GG text: %+v", ex)
	}
	near := func(id int, want float64) {
		t.Helper()
		if got, ok := ex.Slot[id]; !ok || math.Abs(got-want) > 0.05 {
			t.Fatalf("slot of %d = %.2f (%v), want %.2f; all %v", id, got, ok, want, ex.Slot)
		}
	}
	near(6617, 1)    // Moonstone Renewer: first core item
	near(2526, 2)    // Whispering Circlet, listed as Diadem of Songs (what it turns into)
	near(3083, 3.19) // Warmog's: 3rd core item, sometimes 4th
	near(3065, 4.35) // Spirit Visage: 4th or 5th
	near(3107, 4.66) // Redemption: 4th or 5th
	near(3504, 5)    // Ardent Censer
	for _, id := range []int{3070, 2530} {
		if _, ok := ex.Slot[id]; ok {
			t.Fatalf("%d holds no build slot (Tear is a component, Diadem of Songs is bought as Whispering Circlet)", id)
		}
	}
	if ex.Pop[6617] < 0.99 || ex.Pop[3107] >= ex.Pop[3083] {
		t.Fatalf("unexpected popularity: %v", ex.Pop)
	}
	if wr := ex.WR[6617]; wr < 0.5 || wr > 0.55 {
		t.Fatalf("Moonstone win rate %.3f, want about 0.52 (and win rates at all: %v)", wr, ex.WR)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
