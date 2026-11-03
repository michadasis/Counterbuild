package engine

import (
	"encoding/json"
	"errors"
	"strings"
)

// Live Client Data API (/liveclientdata/allgamedata) shapes.

type LiveGame struct {
	ActivePlayer LivePlayerSelf `json:"activePlayer"`
	AllPlayers   []LivePlayer   `json:"allPlayers"`
	GameData     struct {
		GameMode  string  `json:"gameMode"`
		GameTime  float64 `json:"gameTime"`
		MapNumber int     `json:"mapNumber"`
	} `json:"gameData"`
}

type LivePlayerSelf struct {
	ChampionStats struct {
		AbilityHaste float64 `json:"abilityHaste"`
		AbilityPower float64 `json:"abilityPower"`
		Armor        float64 `json:"armor"`
		AttackDamage float64 `json:"attackDamage"`
		AttackSpeed  float64 `json:"attackSpeed"`
		CritChance   float64 `json:"critChance"`
		MagicResist  float64 `json:"magicResist"`
		MaxHealth    float64 `json:"maxHealth"`
		ResourceType string  `json:"resourceType"`
	} `json:"championStats"`
	CurrentGold    float64 `json:"currentGold"`
	Level          int     `json:"level"`
	RiotID         string  `json:"riotId"`
	RiotIDGameName string  `json:"riotIdGameName"`
	SummonerName   string  `json:"summonerName"`
}

type LiveItem struct {
	ItemID     int    `json:"itemID"`
	Count      int    `json:"count"`
	Slot       int    `json:"slot"`
	Price      int    `json:"price"`
	Consumable bool   `json:"consumable"`
	Name       string `json:"displayName"`
}

type liveSpell struct {
	DisplayName    string `json:"displayName"`
	RawDisplayName string `json:"rawDisplayName"`
}

type LivePlayer struct {
	ChampionName    string     `json:"championName"`
	RawChampionName string     `json:"rawChampionName"`
	IsBot           bool       `json:"isBot"`
	IsDead          bool       `json:"isDead"`
	Items           []LiveItem `json:"items"`
	Level           int        `json:"level"`
	Position        string     `json:"position"`
	RiotID          string     `json:"riotId"`
	RiotIDGameName  string     `json:"riotIdGameName"`
	SummonerName    string     `json:"summonerName"`
	Team            string     `json:"team"`
	Runes           struct {
		Keystone struct {
			ID int `json:"id"`
		} `json:"keystone"`
	} `json:"runes"`
	Scores struct {
		Kills      int     `json:"kills"`
		Deaths     int     `json:"deaths"`
		Assists    int     `json:"assists"`
		CreepScore int     `json:"creepScore"`
		WardScore  float64 `json:"wardScore"`
	} `json:"scores"`
	SummonerSpells struct {
		One liveSpell `json:"summonerSpellOne"`
		Two liveSpell `json:"summonerSpellTwo"`
	} `json:"summonerSpells"`
}

// ChampID returns the Data Dragon id, independent of client language.
func (p *LivePlayer) ChampID() string {
	const pre = "game_character_displayname_"
	if strings.HasPrefix(p.RawChampionName, pre) {
		return p.RawChampionName[len(pre):]
	}
	return p.ChampionName
}

func (p *LivePlayer) HasSmite() bool {
	for _, s := range []liveSpell{p.SummonerSpells.One, p.SummonerSpells.Two} {
		if strings.Contains(strings.ToLower(s.RawDisplayName), "smite") || strings.Contains(strings.ToLower(s.DisplayName), "smite") {
			return true
		}
	}
	return false
}

// ParseLiveGame decodes allgamedata JSON.
func ParseLiveGame(b []byte) (*LiveGame, error) {
	var g LiveGame
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, err
	}
	if len(g.AllPlayers) == 0 {
		return nil, errors.New("no players yet")
	}
	return &g, nil
}

// Self finds the active player's entry in allPlayers.
func (g *LiveGame) Self() *LivePlayer {
	ap := g.ActivePlayer
	keys := []string{ap.RiotID, ap.RiotIDGameName, ap.SummonerName}
	for _, k := range keys {
		if k == "" {
			continue
		}
		for i := range g.AllPlayers {
			p := &g.AllPlayers[i]
			if p.RiotID == k || p.SummonerName == k || (p.RiotIDGameName != "" && p.RiotIDGameName == k) {
				return p
			}
		}
	}
	// Fall back to a name prefix match ("Name#TAG" vs "Name").
	for _, k := range keys {
		if k == "" {
			continue
		}
		base := strings.SplitN(k, "#", 2)[0]
		for i := range g.AllPlayers {
			p := &g.AllPlayers[i]
			if strings.SplitN(p.RiotID, "#", 2)[0] == base || strings.SplitN(p.SummonerName, "#", 2)[0] == base {
				return p
			}
		}
	}
	if len(g.AllPlayers) == 1 {
		return &g.AllPlayers[0]
	}
	return nil
}
