package api

// Coach creation lifecycle endpoints (hc ep_new_team,
// ep_create_coach, ep_coach_reserve, ep_coach_postponed). These
// were never ported in Phase 2, so the calls fell through to the
// Python fallback and wrote into the legacy dataset — the client
// then failed PUT /coach/<slot>/default on Go with
// INVALID_COACH (split-brain, same family as the sim ticket).

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func slotParam(c *gin.Context) (int, bool) {
	slot, err := strconv.Atoi(c.Param("slot"))
	if err != nil || (slot != 0 && slot != 1) {
		return 0, false
	}
	return slot, true
}

func slotBusy(st game.State, slot int) bool {
	for _, coach := range game.Coaches(st) {
		if game.Int(coach["slot"]) == slot {
			return true
		}
	}
	return false
}

// POST/PUT /coach/:slot/new/step/3 — build the pending team.
func (s *Server) coachNewTeam(c *gin.Context) {
	st, u, ok := s.loadState(c)
	if !ok {
		return
	}
	slot, valid := slotParam(c)
	if !valid || slotBusy(st, slot) {
		writeJSON(c, map[string]any{"result": false, "reason": "busy"})
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	teamid := game.Int(req["id"])
	team := s.d.Roster.NewManagerTeam(teamid, game.List(req["spids"]))
	if len(game.List(team["players"])) < 18 {
		writeJSON(c, map[string]any{"result": false, "reason": "invalid team"})
		return
	}
	pending := game.Map(st["pendingteams"])
	if pending == nil {
		pending = map[string]any{}
	}
	pending[strconv.Itoa(slot)] = map[string]any{"teamid": teamid, "team": team}
	st["pendingteams"] = pending
	_ = game.SaveState(c.Request.Context(), s.d.Store, u.ID, st)
	writeJSON(c, map[string]any{"result": true, "team": s.d.Roster.OwnedTeam(team)})
}

// POST/PUT /coach/:slot — create the coach.
func (s *Server) coachCreate(c *gin.Context) {
	st, u, ok := s.loadState(c)
	if !ok {
		return
	}
	slot, valid := slotParam(c)
	if !valid || slotBusy(st, slot) {
		writeJSON(c, map[string]any{"result": false, "reason": "busy"})
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	name := game.Str(req["coachName"])
	if reason := game.CoachNameValid(st, name); reason != "" {
		writeJSON(c, map[string]any{"result": false, "reason": reason})
		return
	}
	teamid := game.Int(req["id"])
	var team map[string]any
	if pending := game.Map(game.Map(st["pendingteams"])[strconv.Itoa(slot)]); pending != nil &&
		game.Int(pending["teamid"]) == teamid {
		team = game.Map(pending["team"])
	}
	if team == nil {
		team = s.d.Roster.NewManagerTeam(teamid, nil)
	}
	if len(game.List(team["players"])) < 18 {
		writeJSON(c, map[string]any{"result": false, "reason": "invalid team"})
		return
	}
	teams := game.Map(st["teams"])
	if teams == nil {
		teams = map[string]any{}
	}
	teams[strconv.Itoa(slot)] = team
	st["teams"] = teams
	if pending := game.Map(st["pendingteams"]); pending != nil {
		delete(pending, strconv.Itoa(slot))
	}
	coach := map[string]any{
		"_id": game.CoachIDForUser(u.ID, slot), "slot": slot,
		"name": strings.TrimSpace(name), "state": "active", "teamid": teamid,
		"teamvalue": map[string]any{"val": 0},
		"levelinfo": map[string]any{"level": 1, "exp": 0, "maxexp": game.CoachNeed(1)},
		"summary":   map[string]any{},
		"createdat": game.W3C(float64(time.Now().Unix())),
		"ep":        game.StartEP, "items": []any{}, "currentbadge": 1001,
		"record": map[string]any{}, "multiplay": []any{},
		"profile": map[string]any{
			"description": "Xin chào! Chào mừng đến với FIFA Online 3.", "picture": 0},
	}
	st["coaches"] = append(game.List(st["coaches"]), coach)
	_ = game.SaveState(c.Request.Context(), s.d.Store, u.ID, st)
	writeJSON(c, map[string]any{"result": true})
}

// POST /coach/new/reserve — name availability probe.
func (s *Server) coachReserve(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	reason := game.CoachNameValid(st, game.Str(req["name"]))
	if len(game.Coaches(st)) >= 2 {
		reason = "busy"
	}
	if reason != "" {
		writeJSON(c, map[string]any{"result": false, "reason": reason})
		return
	}
	writeJSON(c, map[string]any{"result": true})
}

// PUT/DELETE /coach/:key/postponed — delete / restore a coach.
func (s *Server) coachPostponed(c *gin.Context) {
	st, u, ok := s.loadState(c)
	if !ok {
		return
	}
	game.PurgeDeletedCoaches(st)
	key := c.Param("slot")
	var target map[string]any
	for _, coach := range game.Coaches(st) {
		if strconv.Itoa(game.Int(coach["slot"])) == key || game.Str(coach["_id"]) == key {
			target = coach
			break
		}
	}
	if target == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "already removed"})
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	if game.Str(req["_method"]) == "DELETE" {
		target["state"] = "active"
		delete(target, "deleteat")
	} else {
		target["state"] = "postpone"
		target["deleteat"] = float64(time.Now().Unix()) + 300
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, u.ID, st)
	writeJSON(c, map[string]any{"result": true})
}
