// mockgame serves a fake Live Client Data API on https://127.0.0.1:2999 so the
// overlay can be tested without running League of Legends.
//
//	go run ./tools/mockgame            (then start Counterbuild)
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"flag"
	"log"
	"math/big"
	"net"
	"net/http"
	"sync"
	"time"
)

type item struct {
	ItemID int    `json:"itemID"`
	Count  int    `json:"count"`
	Slot   int    `json:"slot"`
	Name   string `json:"displayName"`
}

type player struct {
	ChampionName    string `json:"championName"`
	RawChampionName string `json:"rawChampionName"`
	Level           int    `json:"level"`
	Position        string `json:"position"`
	RiotID          string `json:"riotId"`
	SummonerName    string `json:"summonerName"`
	Team            string `json:"team"`
	Items           []item `json:"items"`
	Scores          struct {
		Kills   int `json:"kills"`
		Deaths  int `json:"deaths"`
		Assists int `json:"assists"`
	} `json:"scores"`
	Runes struct {
		Keystone struct {
			ID int `json:"id"`
		} `json:"keystone"`
	} `json:"runes"`
	buys []int
}

func mk(champ, team, pos string, keystone int, items []int, buys []int) *player {
	p := &player{ChampionName: champ, RawChampionName: "game_character_displayname_" + champ, Level: 1,
		Position: pos, RiotID: champ + "#MOCK", SummonerName: champ + "#MOCK", Team: team, buys: buys}
	p.Runes.Keystone.ID = keystone
	for _, id := range items {
		p.Items = append(p.Items, item{ItemID: id, Count: 1, Slot: len(p.Items)})
	}
	return p
}

func main() {
	addr := flag.String("addr", "127.0.0.1:2999", "listen address")
	speed := flag.Float64("speed", 20, "game seconds per real second")
	flag.Parse()

	var mu sync.Mutex
	start := time.Now()
	me := mk("Ahri", "ORDER", "MIDDLE", 8112, []int{1056, 2003}, []int{3020, 1058, 3157, 3118})
	players := []*player{
		me,
		mk("Darius", "ORDER", "TOP", 8010, []int{1054}, nil),
		mk("LeeSin", "ORDER", "JUNGLE", 8010, nil, nil),
		mk("Jinx", "ORDER", "BOTTOM", 8008, []int{1055}, nil),
		mk("Thresh", "ORDER", "UTILITY", 8439, []int{3865}, nil),
		mk("Zed", "CHAOS", "MIDDLE", 8112, []int{1055}, []int{3142, 3047, 6696, 3814}),
		mk("Aatrox", "CHAOS", "TOP", 8010, []int{1055}, []int{6692, 3047, 6333, 3053}),
		mk("Vladimir", "CHAOS", "JUNGLE", 8010, nil, []int{4633, 3020, 3089, 3157}),
		mk("Caitlyn", "CHAOS", "BOTTOM", 8008, []int{1055}, []int{3508, 3006, 3031, 3036}),
		mk("Soraka", "CHAOS", "UTILITY", 8214, []int{3865}, []int{3869, 6617, 3158, 3107}),
	}

	http.HandleFunc("/liveclientdata/allgamedata", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gt := time.Since(start).Seconds() * *speed
		minute := gt / 60
		for i, p := range players {
			p.Level = 1 + int(minute/1.6)
			if p.Level > 18 {
				p.Level = 18
			}
			bought := int(minute / 6)
			if bought > len(p.buys) {
				bought = len(p.buys)
			}
			var items []item
			for j := 0; j < bought; j++ {
				items = append(items, item{ItemID: p.buys[j], Count: 1, Slot: j})
			}
			if bought == 0 {
				for j, it := range p.Items {
					if it.ItemID < 3000 || it.ItemID == 3865 {
						items = append(items, item{ItemID: it.ItemID, Count: 1, Slot: j})
					}
				}
			}
			p.Items = items
			if i == 5 { // Zed snowballs
				p.Scores.Kills = int(minute / 2.5)
				p.Scores.Deaths = int(minute / 12)
			}
		}
		out := map[string]any{
			"activePlayer": map[string]any{
				"riotId": me.RiotID, "summonerName": me.SummonerName, "level": me.Level,
				"currentGold": float64(int(gt*2.1) % 1600),
				"championStats": map[string]any{
					"abilityPower": 18 + 35*minute/6, "attackDamage": 53.0, "armor": 21 + 4.2*float64(me.Level-1),
					"magicResist": 30.0, "maxHealth": 590 + 104*float64(me.Level-1), "critChance": 0.0, "attackSpeed": 0.668,
					"resourceType": "MANA",
				},
			},
			"allPlayers": players,
			"gameData":   map[string]any{"gameMode": "CLASSIC", "gameTime": gt, "mapNumber": 11},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	cert, err := selfSigned()
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Addr: *addr, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
	log.Printf("mock Live Client API on https://%s/liveclientdata/allgamedata (x%.0f speed)", *addr, *speed)
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(srv.ServeTLS(ln, "", ""))
}

func selfSigned() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
