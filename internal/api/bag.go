package api

// Bag endpoints (Item saya > Items): list + claim one/all.

import (
	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func (s *Server) bagReply(c *gin.Context, st game.State, user *game.User) {
	slot := game.DefaultSlot(st)
	res := s.d.Roster.CoachMeResources(st, slot, user)
	res = append(res, map[string]any{"uri": "/bag", "body": s.d.Roster.BagList(st)})
	writeJSON(c, map[string]any{"result": true, "overdueitems": []any{}, "resources": res})
}

func (s *Server) bagList(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	writeJSON(c, s.d.Roster.BagList(st))
}

func (s *Server) bagUnpack(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	bid := c.Param("id")
	coach := game.CoachBySlot(st, game.DefaultSlot(st))
	var entry map[string]any
	if coach != nil {
		for _, raw := range game.List(coach["bag"]) {
			if e := game.Map(raw); e != nil && game.Str(e["_id"]) == bid {
				entry = e
			}
		}
	}
	if entry == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "ITEM_USE_ERROR"})
		return
	}
	team := game.TeamOf(st, game.DefaultSlot(st))
	players := 0
	if team != nil {
		players = len(game.List(team["players"]))
	}
	if players+len(game.List(entry["players"])) > game.MaxPlayers {
		writeJSON(c, map[string]any{"result": false, "reason": "TRANSFERMARKET_FULLSLOT_DESC"})
		return
	}
	s.d.Roster.BagApply(st, entry)
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	s.bagReply(c, st, user)
}

func (s *Server) bagUnpackAll(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	ids := map[string]bool{}
	for _, raw := range game.List(req["reqUnpackItems"]) {
		ids[game.Str(raw)] = true
	}
	coach := game.CoachBySlot(st, game.DefaultSlot(st))
	if coach != nil {
		// Snapshot the entries first: BagApply mutates the bag.
		var entries []map[string]any
		for _, raw := range game.List(coach["bag"]) {
			if e := game.Map(raw); e != nil && ids[game.Str(e["_id"])] {
				entries = append(entries, e)
			}
		}
		team := game.TeamOf(st, game.DefaultSlot(st))
		for _, e := range entries {
			players := 0
			if team != nil {
				players = len(game.List(team["players"]))
			}
			if players+len(game.List(e["players"])) <= game.MaxPlayers {
				s.d.Roster.BagApply(st, e)
			}
		}
	}
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	s.bagReply(c, st, user)
}
