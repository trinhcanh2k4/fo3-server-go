package api

// PUT /account/option, ported from hc.ep_account_option: saves the
// client's option profiles (*Option dicts) into the user state.

import (
	"strings"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func (s *Server) accountOption(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	options := game.Map(st["option"])
	if options == nil {
		options = map[string]any{}
	}
	for k, v := range req {
		if strings.HasSuffix(k, "Option") {
			if m := game.Map(v); m != nil {
				options[k] = m
			}
		}
	}
	options["controlOption"] = s.d.Roster.ControlOptions(game.Map(options["controlOption"]))
	options["cameraOption"] = s.d.Roster.CameraOptions(game.Map(options["cameraOption"]))
	st["option"] = options
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true, "resources": []any{
		map[string]any{"resource": "/account/option", "uri": "/account/option", "body": options},
	}})
}
