package api

import (
	"encoding/json"
	"io"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
	"fo3server/internal/store"
)

// Locker Room per-player customization, ported from http_config.py
// ep_locker_jersey / ep_locker_itemset (audit group B). Validation
// runs against the same locker catalogue the static endpoint
// serves (responses/static_db_LOCKERROOMITEM.json).

type lockerRow struct {
	Category string `json:"category"`
	Option   string `json:"option"`
	Color    []int  `json:"color"`
}

func (s *Server) lockerCatalog() map[string]lockerRow {
	b, _ := store.ReadFileCached(filepath.Join(s.d.ResponsesDir, "static_db_LOCKERROOMITEM.json"))
	if len(b) == 0 {
		return nil
	}
	var raw map[string]lockerRow
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	return raw
}

func (s *Server) lockerPlayer(c *gin.Context) (game.State, *game.User, map[string]any, int, bool) {
	st, user, ok := s.loadState(c)
	if !ok {
		return nil, nil, nil, 0, false
	}
	slot := game.DefaultSlot(st)
	team := game.TeamOf(st, slot)
	pid := c.Param("pid")
	if team != nil {
		for _, raw := range game.List(team["players"]) {
			if p := game.Map(raw); p != nil && game.IID(p) == pid {
				return st, user, p, slot, true
			}
		}
	}
	writeJSON(c, map[string]any{"result": false, "reason": "INVALID_PLAYER"})
	return nil, nil, nil, 0, false
}

// Any /lockerroom/:pid/jersey — read or change the shirt number
// (swapping with the current holder, hc semantics).
func (s *Server) lockerJersey(c *gin.Context) {
	st, user, player, slot, ok := s.lockerPlayer(c)
	if !ok {
		return
	}
	body, _ := io.ReadAll(c.Request.Body)
	if len(body) == 0 {
		writeJSON(c, map[string]any{"jersey": game.Int(player["jersey"])})
		return
	}
	var req map[string]any
	if json.Unmarshal(body, &req) != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_JERSEY"})
		return
	}
	desiredF, isNum := req["desired"].(float64)
	if !isNum || desiredF != float64(int(desiredF)) || int(desiredF) < 1 || int(desiredF) > 99 {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_JERSEY"})
		return
	}
	desired := int(desiredF)
	team := game.TeamOf(st, slot)
	var players []map[string]any
	var holder map[string]any
	for _, raw := range game.List(team["players"]) {
		if p := game.Map(raw); p != nil {
			players = append(players, p)
			if p != nil && game.IID(p) != game.IID(player) && game.Int(p["jersey"]) == desired {
				holder = p
			}
		}
	}
	if giver, has := req["giverplayerid"]; has && giver != nil {
		if holder == nil || game.IID(holder) != game.Str(giver) && game.IID(holder) != jsonNumStr(giver) {
			writeJSON(c, map[string]any{"result": false, "reason": "INVALID_GIVER"})
			return
		}
	}
	if holder != nil {
		old := game.Int(player["jersey"])
		used := map[int]bool{}
		for _, p := range players {
			if game.IID(p) != game.IID(player) && game.IID(p) != game.IID(holder) {
				used[game.Int(p["jersey"])] = true
			}
		}
		if old < 1 || old > 99 || used[old] {
			old = 0
			for n := 1; n <= 99; n++ {
				if n != desired && !used[n] {
					old = n
					break
				}
			}
		}
		if old == 0 {
			writeJSON(c, map[string]any{"result": false, "reason": "NO_FREE_JERSEY"})
			return
		}
		holder["jersey"] = old
	}
	player["jersey"] = desired
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{
		"result": true, "jersey": desired,
		"resources": s.d.Roster.CoachMeResources(st, slot, user),
	})
}

func jsonNumStr(v any) string {
	if f, ok := v.(float64); ok {
		return json.Number(jsonRaw(f)).String()
	}
	return game.Str(v)
}

func jsonRaw(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// Any /lockerroom/:pid/itemset — read or save the outfit set.
func (s *Server) lockerItemset(c *gin.Context) {
	st, user, player, slot, ok := s.lockerPlayer(c)
	if !ok {
		return
	}
	body, _ := io.ReadAll(c.Request.Body)
	if len(body) == 0 {
		lr := game.Map(player["lr"])
		if lr == nil {
			lr = map[string]any{}
		}
		writeJSON(c, lr)
		return
	}
	var req map[string]any
	if json.Unmarshal(body, &req) != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_ITEMSET"})
		return
	}
	selected := game.Map(req["itemset"])
	if selected == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_ITEMSET"})
		return
	}
	catalog := s.lockerCatalog()
	normalized := map[string]any{}
	groups := map[string]bool{}
	for key, rawVal := range selected {
		row, exists := catalog[key]
		val := game.Map(rawVal)
		if !exists || val == nil || game.Str(val["id"]) != key && jsonNumStr(val["id"]) != key {
			writeJSON(c, map[string]any{"result": false, "reason": "INVALID_ITEMSET"})
			return
		}
		grp := row.Category
		if row.Category == "GLOVE" {
			grp += "|" + row.Option
		}
		if groups[grp] {
			writeJSON(c, map[string]any{"result": false, "reason": "INVALID_ITEMSET"})
			return
		}
		groups[grp] = true
		item := map[string]any{"id": key}
		if posRaw, has := val["pos"]; has {
			pos := game.Str(posRaw)
			if pos != "" && pos != "L" && pos != "R" && pos != "LR" {
				writeJSON(c, map[string]any{"result": false, "reason": "INVALID_ITEMSET"})
				return
			}
			item["pos"] = pos
		}
		if clrRaw, has := val["clr"]; has {
			clrStr := jsonNumStr(clrRaw)
			if clrStr != "-1" {
				found := false
				for _, cn := range row.Color {
					if jsonRaw(float64(cn)) == clrStr {
						found = true
						break
					}
				}
				if !found {
					writeJSON(c, map[string]any{"result": false, "reason": "INVALID_ITEMSET"})
					return
				}
				item["clr"] = game.Int(clrRaw)
			}
		}
		normalized[key] = item
	}
	player["lr"] = normalized
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	resources := s.d.Roster.CoachMeResources(st, slot, user)
	resources = append(resources, map[string]any{
		"uri": "/lockerroom/" + c.Param("pid") + "/itemset", "body": normalized,
	})
	writeJSON(c, map[string]any{"result": true, "resources": resources})
}
