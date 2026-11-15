package api

// Team tactics endpoints (Quản lý đội > Chiến thuật), ported from
// hc ep_tactic_all / ep_tactic (http_config.py:2160-2178). These
// paths were never routed in Go — they fell through to the Python
// fallback, so after the Phase 4 cutover /game/my/tactic/all got
// the {} stub and the tactics tab hung on "loading..." with all
// sliders at 50 (user report 2026-11-13).

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// GET /game/my/tactic/all -> BARE array of the 6 tactics.
func (s *Server) tacticAll(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	list := s.d.Roster.TacticsOf(st)
	out := make([]any, 0, len(list))
	for _, t := range list {
		out = append(out, t)
	}
	writeJSON(c, out)
}

// /game/my/tactic/:i — save when the body carries "bus"; reset
// otherwise (DELETE, a _method=DELETE body, or the Tạo lại call) —
// exactly hc ep_tactic's three branches.
func (s *Server) tacticOne(c *gin.Context) {
	i, err := strconv.Atoi(c.Param("i"))
	if err != nil || i < 0 || i >= game.TacticSlots {
		s.fallback(c)
		return
	}
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	if req == nil {
		req = map[string]any{}
	}
	saved := s.d.Roster.TacticsSaved(st)
	key := strconv.Itoa(i)
	reset := func() {
		delete(saved, key)
		_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
		writeJSON(c, map[string]any{"result": true, "resources": []any{
			map[string]any{"uri": "/game/my/tactic/" + key,
				"body": s.d.Roster.DefaultTactic(i)}}})
	}
	if c.Request.Method == "DELETE" || game.Str(req["_method"]) == "DELETE" {
		reset()
		return
	}
	if _, has := req["bus"]; has {
		cp := map[string]any{}
		for k, v := range req {
			if k != "_method" {
				cp[k] = v
			}
		}
		cp["id"] = i
		saved[key] = cp
		_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
		writeJSON(c, map[string]any{"result": true})
		return
	}
	reset()
}
