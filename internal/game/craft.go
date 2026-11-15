package game

// Craft (Kho > Trao doi vat pham), ported from hc's craft block:
// player recipes from gamedata/craft.json (default: the 30 best
// Legend-season cards on the market), 8 fixed item-exchange
// recipes, craft pieces (itemid = CraftPiece + playerid) and the
// Special Bonus shop table. Pieces ride the normal item stack
// shape; their staticdata is built here.

import (
	"encoding/json"
	mrand "math/rand"
	"path/filepath"
	"sort"
)

// STSHOPID ports STSHOPID; SpecialShop ports SPECIAL.
const STSHOPID = 910_000_000

var SpecialShop = []int{8335, 8194, 50841, 50881}

var craftLegendSeasons = []int{93, 30, 27, 71, 51, 61, 89, 48, 55, 49}

type craftConfig struct {
	Players []int   `json:"players"`
	Count   int     `json:"count"`
	Pieces  int     `json:"pieces"`
	Fee     float64 `json:"fee"`
	Grade   int     `json:"grade"`
}

// CraftCfg ports craft_cfg (re-read per call site that needs it).
func (r *Roster) CraftCfg() craftConfig {
	cfg := craftConfig{Players: []int{}, Count: 30, Pieces: 10, Fee: 0.05, Grade: 1}
	if b, err := readFileCached(filepath.Join(r.gamedataDir, "craft.json")); err == nil {
		var raw map[string]json.RawMessage
		if json.Unmarshal(b, &raw) == nil {
			if v, ok := raw["players"]; ok {
				_ = json.Unmarshal(v, &cfg.Players)
			}
			if v, ok := raw["count"]; ok {
				_ = json.Unmarshal(v, &cfg.Count)
			}
			if v, ok := raw["pieces"]; ok {
				_ = json.Unmarshal(v, &cfg.Pieces)
			}
			if v, ok := raw["fee"]; ok {
				_ = json.Unmarshal(v, &cfg.Fee)
			}
			if v, ok := raw["grade"]; ok {
				_ = json.Unmarshal(v, &cfg.Grade)
			}
		}
	}
	return cfg
}

// CraftCard is one recipe player: (spid, playerid, short name).
type CraftCard struct {
	Spid, PID int
	Name      string
}

// CraftCards ports craft_cards (+ _craft_cards): explicit players
// from the config, else the best Legend-season cards on the
// market, deduped by player, best first.
func (r *Roster) CraftCards() []CraftCard {
	if r.craftCache != nil {
		return r.craftCache
	}
	cfg := r.CraftCfg()
	out := []CraftCard{}
	if len(cfg.Players) > 0 {
		for _, spid := range cfg.Players {
			rows, _ := r.queryPlayers("WHERE p.spid = ?", spid)
			if len(rows) == 0 {
				continue
			}
			row := rows[0]
			out = append(out, CraftCard{Spid: row.Spid, PID: row.PlayerID, Name: r.playerShortName(row.PlayerID)})
		}
		r.craftCache = out
		return out
	}
	seasonList := ""
	for i, s := range craftLegendSeasons {
		if i > 0 {
			seasonList += ","
		}
		seasonList += itoa(s)
	}
	rows, _ := r.queryPlayers("WHERE (p.spid / 1000000) IN ("+seasonList+")")
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := rows[i].Rating(), rows[j].Rating()
		if ri != rj {
			return ri > rj
		}
		return rows[i].Spid < rows[j].Spid
	})
	seen := map[int]bool{}
	for _, row := range rows {
		if !r.OnMarket(row.Spid) || seen[row.PlayerID] {
			continue
		}
		seen[row.PlayerID] = true
		out = append(out, CraftCard{Spid: row.Spid, PID: row.PlayerID, Name: r.playerShortName(row.PlayerID)})
		if len(out) >= cfg.Count {
			break
		}
	}
	r.craftCache = out
	return out
}

func (r *Roster) playerShortName(pid int) string {
	var name string
	if r.db != nil {
		_ = r.db.QueryRow("SELECT short FROM namesind WHERE playerid = ?", pid).Scan(&name)
	}
	if name == "" {
		name = itoa(pid)
	}
	return name
}

// PlayerDisplayName resolves a spid to (full, short) display names
// from fifadb (namesvie, falling back to namesind / the spid).
func (r *Roster) PlayerDisplayName(spid int) (string, string) {
	var pid int
	if err := r.db.QueryRow("SELECT playerid FROM players WHERE spid = ?", spid).Scan(&pid); err != nil || pid == 0 {
		return itoa(spid), itoa(spid)
	}
	var full, short string
	if err := r.db.QueryRow("SELECT full, short FROM namesvie WHERE playerid = ?", pid).Scan(&full, &short); err == nil && full != "" {
		return full, short
	}
	short = r.playerShortName(pid)
	if short == "" {
		short = itoa(spid)
	}
	return short, short
}

// PieceDef ports piece_def.
func (r *Roster) PieceDef(itemid int) map[string]any {
	pid := itemid - CraftPiece
	spid, name := 0, ""
	for _, c := range r.CraftCards() {
		if c.PID == pid {
			spid, name = c.Spid, c.Name
			break
		}
	}
	if spid == 0 {
		rows, _ := r.queryPlayers("WHERE p.playerid = ? AND p.spid >= 1000000", pid)
		best := -1
		for _, row := range rows {
			if rt := row.Rating(); rt > best {
				best, spid = rt, row.Spid
			}
		}
		if spid == 0 {
			spid = pid
		}
		name = r.playerShortName(pid)
	}
	cfg := r.CraftCfg()
	box := Tables(r.gamedataDir).Items[CraftBox]
	return map[string]any{
		"itemid": itemid, "productid": itemid,
		"name": "Mảnh cầu thủ " + name, "category": "USE",
		"type": "ITEM_PLAYER_CRAFT", "unit": "COUNT", "itemicon": box.Icon,
		"stackable": true, "spid": spid, "enhance": cfg.Grade,
		"description": "Thu thập " + itoa(cfg.Pieces) + " mảnh để ký hợp đồng với " + name + " (Kho > Trao đổi vật phẩm).",
	}
}

// ItemExchangeRecipes ports ITEM_EXCHANGE_RECIPES.
var ItemExchangeRecipes = map[int]map[string]any{
	980000001: exchangeRecipe(980000001, 910001, 3, 910002),
	980000002: exchangeRecipe(980000002, 910002, 3, 910006),
	980000003: exchangeRecipe(980000003, 910005, 2, 910003),
	980000004: exchangeRecipe(980000004, 911001, 3, 911002),
	980000005: exchangeRecipe(980000005, 911002, 3, 911003),
	980000006: exchangeRecipe(980000006, 50019, 2, 50020),
	980000007: exchangeRecipe(980000007, 50020, 2, 50021),
	980000008: exchangeRecipe(980000008, 501, 3, 50840),
}

func exchangeRecipe(rid, mat, count, result int) map[string]any {
	return map[string]any{
		"recipeid": rid, "category": "item", "group": "USE",
		"materials": []any{map[string]any{"itemid": mat, "count": count}},
		"result":    map[string]any{"normal": map[string]any{"id": result, "count": 1}},
		"money":     map[string]any{"ep": 0},
	}
}

// CraftRecipes ports craft_recipes: {recipeid: recipe} for both
// the player tab (category "player") and the item tab.
func (r *Roster) CraftRecipes() map[string]any {
	cfg := r.CraftCfg()
	out := map[string]any{}
	for _, c := range r.CraftCards() {
		price := r.CardPrice(c.Spid, cfg.Grade)
		out[itoa(c.PID)] = map[string]any{
			"recipeid": c.PID, "category": "player",
			"materials": []any{
				map[string]any{"itemid": CraftPiece + c.PID, "count": cfg.Pieces},
				map[string]any{"itemid": CraftContract, "count": 1},
			},
			"result": map[string]any{"normal": map[string]any{"id": c.Spid, "grade": cfg.Grade, "price": int64(price)}},
			"money":  map[string]any{"ep": int64(sig3(price * cfg.Fee))},
		}
	}
	for rid, rec := range ItemExchangeRecipes {
		out[itoa(rid)] = rec
	}
	return out
}

// ProductCategory ports product_category.
func (r *Roster) ProductCategory(itemid int) string {
	if _, ok := Tables(r.gamedataDir).Packs[itemid]; ok {
		return "PLAYERPACK"
	}
	if _, ok := Tables(r.gamedataDir).Items[itemid]; ok {
		return "USE"
	}
	return "PACKAGE"
}

// RandomCraftPiece ports the craft box draw: pieces of one
// recipe's player, count in [lo, hi]. Returns (pieceItemID, n, ok).
func (r *Roster) RandomCraftPiece(lo, hi int) (int, int, bool) {
	cards := r.CraftCards()
	if len(cards) == 0 {
		return 0, 0, false
	}
	c := cards[mrand.Intn(len(cards))]
	n := lo
	if hi > lo {
		n = lo + mrand.Intn(hi-lo+1)
	}
	return CraftPiece + c.PID, n, true
}
