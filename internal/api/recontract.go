package api

// Contract renewal endpoints (players + staff), ported from hc.

import (
	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func recontractCap(kind string) int {
	if kind == "player" {
		return game.RecontractPlayerMaxLife
	}
	return game.RecontractStaffMaxLife
}

func recontractCollection(st game.State, kind string) []map[string]any {
	team := game.TeamOf(st, game.DefaultSlot(st))
	if team == nil {
		return nil
	}
	key := "players"
	if kind == "staff" {
		key = "staffs"
	}
	var out []map[string]any
	for _, raw := range game.List(team[key]) {
		if m := game.Map(raw); m != nil {
			out = append(out, m)
		}
	}
	return out
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func (s *Server) recontractPreview(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		st, _, ok := s.loadState(c)
		if !ok {
			return
		}
		var req map[string]any
		_ = c.ShouldBindJSON(&req)
		key := "players"
		if kind == "staff" {
			key = "staffs"
		}
		var requested []string
		for _, raw := range game.List(req[key]) {
			requested = append(requested, game.Str(raw))
		}
		requested = dedupeStrings(requested)
		collection := recontractCollection(st, kind)
		byID := map[string]map[string]any{}
		for _, item := range collection {
			byID[game.RecontractKey(item)] = item
		}
		cap := recontractCap(kind)
		selected := []any{}
		rows := []any{}
		total := 0
		for _, ident := range requested {
			item := byID[ident]
			if item == nil {
				continue
			}
			life := game.PlayerLife(item)
			if kind == "staff" {
				life = game.StaffLife(item, cap)
			}
			if life >= cap {
				continue
			}
			fee := s.d.Roster.RecontractFeeFor(item, kind, cap)
			row := map[string]any{}
			for k, v := range item {
				row[k] = v
			}
			row["id"] = game.RecontractKey(item)
			row["contractfee"] = fee
			selected = append(selected, ident)
			rows = append(rows, row)
			total += fee
		}
		coach := game.CoachBySlot(st, game.DefaultSlot(st))
		ep := int64(0)
		if coach != nil {
			ep = game.Int64(coach["ep"])
		}
		ci := game.RecontractCostInfo(st, total)
		final := game.Int(ci["final"])
		writeJSON(c, map[string]any{
			"result": true, key: rows, "selected": selected,
			"MaximumRemainingMatch": cap, "StaffMaximumRemainingMatch": cap,
			"accountInfo": map[string]any{"ep": ep, "after": ep - int64(final),
				"balance": ep - int64(final)},
			"costInfo": ci,
			"total":    total, "boost": ci["boost"],
			"discount": game.Int(ci["discount"]), "final": final,
			"type": "single",
		})
	}
}

func (s *Server) recontractApply(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		st, user, ok := s.loadState(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		var req map[string]any
		_ = c.ShouldBindJSON(&req)
		key := "players"
		if kind == "staff" {
			key = "staffs"
		}
		var ids []string
		for _, raw := range game.List(req[key]) {
			ids = append(ids, game.Str(raw))
		}
		if len(ids) == 0 {
			writeJSON(c, map[string]any{"result": false, "reason": "INVALID_RECONTRACT_REQUEST"})
			return
		}
		ids = dedupeStrings(ids)
		collection := recontractCollection(st, kind)
		byID := map[string]map[string]any{}
		for _, item := range collection {
			byID[game.RecontractKey(item)] = item
		}
		for _, ident := range ids {
			if byID[ident] == nil {
				writeJSON(c, map[string]any{"result": false, "reason": "INVALID_RECONTRACT_TARGET"})
				return
			}
		}
		cap := recontractCap(kind)
		sum := 0
		targets := []any{}
		for _, ident := range ids {
			item := byID[ident]
			sum += s.d.Roster.RecontractFeeFor(item, kind, cap)
			targets = append(targets, item)
		}
		total := sum - game.RecontractDiscount(st, sum)
		coach := game.CoachBySlot(st, game.DefaultSlot(st))
		var balance int64
		if coach != nil {
			balance = game.Int64(coach["ep"])
		}
		if balance < int64(total) {
			writeJSON(c, map[string]any{"result": false,
				"reason": "PLAYER_RECONTRACT_ERROR_POPUP_INFO_NOTENOUGHTMONEY",
				"ep": balance, "required": total, "lack": int64(total) - balance})
			return
		}
		for _, raw := range targets {
			item := raw.(map[string]any)
			item["life"] = cap
			if kind == "staff" {
				item["contractfee"] = 0
			}
		}
		if coach != nil {
			coach["ep"] = balance - int64(total)
		}
		_ = game.SaveState(ctx, s.d.Store, user.ID, st)
		slot := game.DefaultSlot(st)
		writeJSON(c, map[string]any{"result": true, "ep": balance - int64(total), key: targets,
			"resources": s.d.Roster.CoachMeResources(st, slot, user)})
	}
}

func (s *Server) baseContractFee(c *gin.Context) {
	writeJSON(c, game.BaseContractFeeTable())
}
