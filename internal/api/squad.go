package api

// Squad management endpoints, ported from hc.ep_coach_squad and
// hc.ep_team_role. Until this port, squad saves fell through the
// fallback proxy into the PYTHON state, so a swap made on the Go
// server never persisted into the Go squad.

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func squadHasDuplicates(r interface {
	PlayerID(int) int
}, positions []map[string]any) bool {
	seen := map[int]bool{}
	for _, p := range positions {
		id := r.PlayerID(game.Int(p["spid"]))
		if seen[id] {
			return true
		}
		seen[id] = true
	}
	return len(positions) >= 2 && len(seen) != len(positions)
}

// contractBlock refuses the request while a registered-squad
// player is out of contract (life 0) — owner directive 2026-11-16:
// an out-of-contract player may not take the field.
func contractBlock(c *gin.Context, st game.State) bool {
	if exp := game.ExpiredSquadPlayers(st); len(exp) > 0 {
		writeJSON(c, map[string]any{"result": false, "reason": "PLAYER_CONTRACT_EXPIRED"})
		return true
	}
	return false
}

func (s *Server) coachSquad(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	ctx := c.Request.Context()
	slot := game.DefaultSlot(st)
	team := game.TeamOf(st, slot)
	sq := game.Map(req["squad"])
	spidOf := map[string]int{}
	lifeOf := map[string]int{}
	if team != nil {
		for _, raw := range game.List(team["players"]) {
			if p := game.Map(raw); p != nil {
				spidOf[game.IID(p)] = game.Int(p["spid"])
				lifeOf[game.IID(p)] = game.PlayerLife(p)
			}
		}
	}
	positions := []map[string]any{}
	if sq != nil {
		for _, raw := range game.List(sq["positions"]) {
			p := game.Map(raw)
			if p == nil {
				continue
			}
			id := game.Str(p["id"])
			spid, known := spidOf[id]
			if !known || game.Int(p["pos"]) > game.PosSub {
				continue
			}
			entry := map[string]any{"id": id, "spid": spid, "pos": game.Int(p["pos"])}
			if p["starting"] != nil {
				entry["starting"] = p["starting"]
			}
			positions = append(positions, entry)
		}
	}
	// Out-of-contract players cannot be registered (owner directive
	// 2026-11-16) — the save is refused until they are renewed or
	// swapped out of the lineup.
	for _, p := range positions {
		if life, known := lifeOf[game.Str(p["id"])]; known && life == 0 {
			writeJSON(c, map[string]any{"result": false, "reason": "PLAYER_CONTRACT_EXPIRED"})
			return
		}
	}
	// Production setting: duplicate players (same canonical identity,
	// any season) are not allowed in one lineup.
	if squadHasDuplicates(s.d.Roster, positions) {
		writeJSON(c, map[string]any{"result": false, "reason": "DUPLICATE_PLAYERS_NOT_ALLOWED"})
		return
	}
	starters := 0
	ids := map[string]bool{}
	posSet := map[int]bool{}
	for _, p := range positions {
		ids[game.Str(p["id"])] = true
		if game.Int(p["pos"]) < game.PosSub {
			starters++
			posSet[game.Int(p["pos"])] = true
		}
	}
	if team != nil && starters == 11 && len(ids) == len(positions) && len(posSet) == 11 {
		s.d.Roster.SquadPresets(team)
		selected := game.Int(team["presetidx"])
		if raw, present := req["presetidx"]; present {
			f, isNum := raw.(float64)
			if !isNum || f != float64(int(f)) || int(f) < 0 || int(f) >= 3 {
				writeJSON(c, map[string]any{"result": false, "reason": "INVALID_SQUAD"})
				return
			}
			selected = int(f)
		}
		team["presetidx"] = selected
		squad := game.Map(team["squad"])
		if squad == nil {
			squad = map[string]any{}
			team["squad"] = squad
		}
		posAny := make([]any, len(positions))
		for i, p := range positions {
			posAny[i] = p
		}
		squad["positions"] = posAny
		if sq != nil {
			for _, k := range []string{"formation", "workrate"} {
				if v, present := sq[k]; present {
					squad[k] = v
				}
			}
		}
		if capID := game.Str(req["captain"]); capID != "" {
			if _, known := spidOf[capID]; known {
				squad["captain"] = capID
			}
		}
		if kickers := game.List(req["kickers"]); len(kickers) > 0 {
			allKnown := true
			for _, k := range kickers {
				if ks := game.Str(k); ks != "" {
					if _, known := spidOf[ks]; !known {
						allKnown = false
					}
				}
			}
			if allKnown {
				out := []any{}
				for _, k := range kickers {
					if ks := game.Str(k); ks != "" {
						out = append(out, ks)
					}
				}
				squad["kickers"] = out
			}
		}
		s.d.Roster.SquadPresets(team)
		_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	}
	writeJSON(c, map[string]any{"result": true,
		"resources": s.d.Roster.CoachMeResources(st, slot, user)})
}

func (s *Server) teamRole(c *gin.Context, role string) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	if role != "captain" {
		if n, err := strconv.Atoi(role); err != nil || n < 0 || n > 4 {
			writeJSON(c, map[string]any{"result": false, "reason": "INVALID_SQUAD"})
			return
		}
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	rawID, present := req["id"]
	if !present {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_SQUAD"})
		return
	}
	var playerID string
	switch v := rawID.(type) {
	case string:
		playerID = v
	case float64:
		playerID = strconv.FormatInt(int64(v), 10)
	default:
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_SQUAD"})
		return
	}
	slot := game.DefaultSlot(st)
	team := game.TeamOf(st, slot)
	if team == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_SQUAD"})
		return
	}
	s.d.Roster.RepairSquad(team)
	squad := game.Map(team["squad"])
	starters := map[string]bool{}
	for _, raw := range game.List(squad["positions"]) {
		if p := game.Map(raw); p != nil && game.Int(p["pos"]) < game.PosSub {
			starters[game.Str(p["id"])] = true
		}
	}
	if !starters[playerID] {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_SQUAD"})
		return
	}
	if role == "captain" {
		squad["captain"] = playerID
	} else {
		idx, _ := strconv.Atoi(role)
		kickers := game.List(squad["kickers"])
		for len(kickers) <= idx {
			kickers = append(kickers, "")
		}
		kickers[idx] = playerID
		squad["kickers"] = kickers
	}
	team["captain"] = squad["captain"]
	team["kickers"] = squad["kickers"]
	s.d.Roster.SquadPresets(team)
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true,
		"resources": s.d.Roster.CoachMeResources(st, slot, user)})
}

func (s *Server) teamCaptain(c *gin.Context) { s.teamRole(c, "captain") }

func (s *Server) teamKickers(c *gin.Context) { s.teamRole(c, c.Param("idx")) }
