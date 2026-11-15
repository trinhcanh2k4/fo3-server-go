package api

// Second-password endpoints, ported from fx ep_2ndpass_*.

import (
	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func (s *Server) secondPassInfo(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	writeJSON(c, map[string]any{"result": true, "use": true,
		"securitySetting": game.HasSecondPass(st)})
}

func (s *Server) secondPassCheck(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	cand := game.Str(req["pass"])
	if game.VerifySecondPass(st, cand) {
		st["_2ndpass_fail"] = 0
		_ = game.SaveState(ctx, s.d.Store, user.ID, st)
		writeJSON(c, map[string]any{"result": true, "check": true})
		return
	}
	cnt := game.Int(st["_2ndpass_fail"]) + 1
	st["_2ndpass_fail"] = cnt
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true, "check": false, "count": cnt, "limit": 5})
}

func (s *Server) secondPassSet(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	game.SetSecondPass(st, game.Str(req["pass"]))
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true, "check": true})
}

func (s *Server) secondPassReset(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	game.ClearSecondPass(st)
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true})
}
