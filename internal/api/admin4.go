package api

// Admin round 2: squad view, daily-reward table editor,
// enhancement-rate editor, gift-bag grant — the functions the
// Python admin had that Phase 3 missed, plus player names.

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// GET /admin/api/squad/:uid/:slot — read-only formation view with
// player names resolved from fifadb.
func (s *Server) adminSquad(c *gin.Context) {
	st, _, slot, ok := s.loadUserState(c)
	if !ok {
		return
	}
	team := game.TeamOf(st, slot)
	if team == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NO_TEAM"})
		return
	}
	byIID := map[string]map[string]any{}
	for _, pv := range game.List(team["players"]) {
		if p := game.Map(pv); p != nil {
			byIID[game.IID(p)] = p
		}
	}
	squad := game.Map(team["squad"])
	inSquad := map[string]bool{}
	positions := []any{}
	for _, posv := range game.List(squad["positions"]) {
		pos := game.Map(posv)
		if pos == nil {
			continue
		}
		iid := game.Str(pos["id"])
		inSquad[iid] = true
		p := byIID[iid]
		entry := map[string]any{"pos": game.Int(pos["pos"]), "iid": iid,
			"spid": game.Int(pos["spid"])}
		if p != nil {
			full, short := s.d.Roster.PlayerDisplayName(game.Int(p["spid"]))
			entry["name"] = full
			entry["short"] = short
			entry["season_name"] = game.SeasonLabel(game.Int(p["spid"]) / 1000000)
			entry["grade"] = game.GradeOf(p)
			entry["life"] = game.PlayerLife(p)
		}
		positions = append(positions, entry)
	}
	reserves := []any{}
	for iid, p := range byIID {
		if inSquad[iid] {
			continue
		}
		full, short := s.d.Roster.PlayerDisplayName(game.Int(p["spid"]))
		reserves = append(reserves, map[string]any{"iid": iid,
			"spid": game.Int(p["spid"]), "name": full, "short": short,
			"season_name": game.SeasonLabel(game.Int(p["spid"]) / 1000000),
			"grade":       game.GradeOf(p), "life": game.PlayerLife(p),
			"team2": p["team2"] == true})
	}
	writeJSON(c, map[string]any{"result": true,
		"teamid":    game.Int(team["teamid"]),
		"formation": squad["formation"],
		"captain":   squad["captain"],
		"positions": positions, "reserves": reserves})
}

// GET /admin/api/config/daily — the effective daily-login table.
func (s *Server) adminDailyGet(c *gin.Context) {
	path := filepath.Join(s.d.GamedataDir, "daily_rewards.json")
	_, err := os.Stat(path)
	writeJSON(c, map[string]any{"result": true,
		"table": s.d.Roster.DailyTable(), "custom": err == nil})
}

// PUT /admin/api/config/daily {table: [{ep, pack, count}]}.
func (s *Server) adminDailyPut(c *gin.Context) {
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	raw, _ := json.Marshal(req["table"])
	var table []game.DailyReward
	if json.Unmarshal(raw, &table) != nil || len(table) == 0 || len(table) > 31 {
		writeJSON(c, map[string]any{"result": false, "reason": "BAD_TABLE"})
		return
	}
	path := filepath.Join(s.d.GamedataDir, "daily_rewards.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "WRITE_FAILED"})
		return
	}
	s.audit(c, "config.daily", "", map[string]any{"days": len(table)})
	writeJSON(c, map[string]any{"result": true, "table": table})
}

// GET /admin/api/config/enhance — enhancement rate override state.
func (s *Server) adminEnhanceGet(c *gin.Context) {
	path := filepath.Join(s.d.GamedataDir, "enhancement_settings.json")
	pct := 0
	if b, err := os.ReadFile(path); err == nil {
		var doc map[string]any
		if json.Unmarshal(b, &doc) == nil {
			pct = game.Int(doc["success_percent"])
		}
	}
	writeJSON(c, map[string]any{"result": true, "success_percent": pct,
		"default_rates": game.EnhanceRates})
}

// PUT /admin/api/config/enhance {success_percent: 0..100}
// (0 clears the override, back to the per-grade table).
func (s *Server) adminEnhancePut(c *gin.Context) {
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	pct := game.Int(req["success_percent"])
	path := filepath.Join(s.d.GamedataDir, "enhancement_settings.json")
	if pct == 0 {
		_ = os.Remove(path)
	} else {
		if pct < 1 || pct > 100 {
			writeJSON(c, map[string]any{"result": false, "reason": "BAD_PERCENT"})
			return
		}
		raw, _ := json.Marshal(map[string]any{"success_percent": pct})
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			writeJSON(c, map[string]any{"result": false, "reason": "WRITE_FAILED"})
			return
		}
	}
	s.audit(c, "config.enhance", "", map[string]any{"success_percent": pct})
	writeJSON(c, map[string]any{"result": true, "success_percent": pct})
}

// POST /admin/api/inventory/:uid/:slot/bag {title, ep, pack, count}
// — grant a gift bag entry (the Python admin's "gói quà").
func (s *Server) adminBagGrant(c *gin.Context) {
	st, user, slot, ok := s.loadUserState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	coach := game.CoachBySlot(st, slot)
	if coach == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NO_COACH"})
		return
	}
	rw := game.DailyReward{EP: game.Int(req["ep"]), Pack: game.Int(req["pack"]),
		Count: game.Int(req["count"])}
	items := []any{}
	if rw.EP > 0 {
		items = append(items, map[string]any{"itemid": game.EPItemID, "count": rw.EP})
	}
	if rw.Pack > 0 {
		n := rw.Count
		if n <= 0 {
			n = 1
		}
		items = append(items, map[string]any{"itemid": rw.Pack, "count": n})
	}
	if len(items) == 0 {
		writeJSON(c, map[string]any{"result": false, "reason": "EMPTY_GIFT"})
		return
	}
	title := game.Str(req["title"])
	if title == "" {
		title = "Quà từ quản trị viên"
	}
	entry := game.BagAdd(coach, title, items, game.Str(req["message"]))
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	s.audit(c, "inventory.bag_grant", user.Username, map[string]any{"items": items})
	writeJSON(c, map[string]any{"result": true, "bag": entry})
}
