package api

// Admin grant section: player/team search + add-coach for a
// designated user (the user asked for search instead of raw IDs,
// in its own section).

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// GET /admin/api/search/players?q=
func (s *Server) adminSearchPlayers(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if len([]rune(q)) < 2 {
		writeJSON(c, map[string]any{"result": true, "players": []any{}})
		return
	}
	writeJSON(c, map[string]any{"result": true,
		"players": s.d.Roster.PlayerSearch(q, 30)})
}

// GET /admin/api/search/teams?q=
func (s *Server) adminSearchTeams(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	writeJSON(c, map[string]any{"result": true,
		"teams": s.d.Roster.TeamSearch(q, 30)})
}

// GET /admin/api/search/staff?q= — HLV huấn luyện (staff).
func (s *Server) adminSearchStaff(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	writeJSON(c, map[string]any{"result": true,
		"staff": s.d.Roster.StaffSearch(q, 40)})
}

// POST /admin/api/users/:uid/coaches {name, teamid} — add a coach
// (next free slot 0/1) to an existing user, mirroring the game's
// own coachCreate construction.
func (s *Server) adminCoachAdd(c *gin.Context) {
	ctx := c.Request.Context()
	uid, ok := uidParam(c)
	if !ok {
		writeJSON(c, map[string]any{"result": false, "reason": "BAD_UID"})
		return
	}
	user, err := game.UserByID(ctx, s.d.Store, uid)
	if err != nil || user == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NO_USER"})
		return
	}
	st, err := game.LoadState(ctx, s.d.Store, uid)
	if err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NO_STATE"})
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	name := strings.TrimSpace(game.Str(req["name"]))
	teamid := game.Int(req["teamid"])
	slot := -1
	for _, cand := range []int{0, 1} {
		if !slotBusy(st, cand) {
			slot = cand
			break
		}
	}
	if slot < 0 {
		writeJSON(c, map[string]any{"result": false, "reason": "FULL_SLOTS"})
		return
	}
	if reason := game.CoachNameValid(st, name); reason != "" {
		writeJSON(c, map[string]any{"result": false, "reason": reason})
		return
	}
	team := s.d.Roster.NewManagerTeam(teamid, nil)
	if len(game.List(team["players"])) < 18 {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_TEAM"})
		return
	}
	teams := game.Map(st["teams"])
	if teams == nil {
		teams = map[string]any{}
	}
	teams[strconv.Itoa(slot)] = team
	st["teams"] = teams
	coach := map[string]any{
		"_id": game.CoachIDForUser(uid, slot), "slot": slot,
		"name": name, "state": "active", "teamid": teamid,
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
	if err := game.SaveState(ctx, s.d.Store, uid, st); err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "SAVE_FAILED"})
		return
	}
	s.audit(c, "coach.add", user.Username, map[string]any{"slot": slot, "name": name, "teamid": teamid})
	writeJSON(c, map[string]any{"result": true, "slot": slot})
}
