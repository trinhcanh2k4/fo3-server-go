package api

// Craft endpoints (Kho > Trao doi vat pham) + the Special Bonus
// shop list, ported from hc's craft block and ep_stshop.

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func (s *Server) mixinRecipes(c *gin.Context) {
	writeJSON(c, s.d.Roster.CraftRecipes())
}

func (s *Server) mixinDo(c *gin.Context) {
	rid := c.Param("rid")
	if rid == "recipes" {
		s.mixinRecipes(c)
		return
	}
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	slot := game.DefaultSlot(st)
	coach := game.CoachBySlot(st, slot)
	team := game.TeamOf(st, slot)
	recipe := game.Map(s.d.Roster.CraftRecipes()[rid])
	if recipe == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "CRAFTSYSTEM_ERROR_03"})
		return
	}
	need := map[int]int{}
	for _, mv := range game.List(recipe["materials"]) {
		if m := game.Map(mv); m != nil {
			need[game.Int(m["itemid"])] = game.Int(m["count"])
		}
	}
	have := map[int]map[string]any{}
	for _, iv := range game.List(coach["items"]) {
		if it := game.Map(iv); it != nil {
			if _, wanted := need[game.Int(it["itemid"])]; wanted {
				have[game.Int(it["itemid"])] = it
			}
		}
	}
	fee := int64(game.Int(game.Map(recipe["money"])["ep"]))
	if recipe["category"] == "item" {
		for itemid, n := range need {
			if have[itemid] == nil || game.Int(have[itemid]["remains"]) < n {
				writeJSON(c, map[string]any{"result": false, "reason": "CRAFTSYSTEM_ERROR_02"})
				return
			}
		}
		if int64(game.Int(coach["ep"])) < fee {
			writeJSON(c, map[string]any{"result": false, "reason": "CRAFTSYSTEM_ERROR_05"})
			return
		}
		consumeItems(coach, need, have)
		if fee > 0 {
			game.AddEP(st, -fee)
		}
		res := game.Map(game.Map(recipe["result"])["normal"])
		count := game.Int(res["count"])
		if count == 0 {
			count = 1
		}
		game.InventoryAdd(coach, game.Int(res["id"]), count)
		_ = game.SaveState(ctx, s.d.Store, user.ID, st)
		writeJSON(c, map[string]any{"result": true,
			"results":   []any{map[string]any{"itemid": game.Int(res["id"]), "count": count}},
			"resources": s.d.Roster.CoachMeResources(st, slot, user)})
		return
	}
	if team != nil && len(game.List(team["players"])) >= game.MaxPlayers {
		writeJSON(c, map[string]any{"result": false, "reason": "CRAFTSYSTEM_ERROR_01"})
		return
	}
	for itemid, n := range need {
		if have[itemid] == nil || game.Int(have[itemid]["remains"]) < n {
			writeJSON(c, map[string]any{"result": false, "reason": "CRAFTSYSTEM_ERROR_02"})
			return
		}
	}
	if int64(game.Int(coach["ep"])) < fee {
		writeJSON(c, map[string]any{"result": false, "reason": "CRAFTSYSTEM_ERROR_05"})
		return
	}
	consumeItems(coach, need, have)
	game.AddEP(st, -fee)
	card := game.Map(game.Map(recipe["result"])["normal"])
	if team != nil {
		s.d.Roster.AddPlayer(team, game.Int(card["id"]), game.Int(card["grade"]))
	}
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true,
		"results":   []any{map[string]any{"spid": game.Int(card["id"]), "grade": game.Int(card["grade"])}},
		"resources": s.d.Roster.CoachMeResources(st, slot, user)})
}

func consumeItems(coach map[string]any, need map[int]int, have map[int]map[string]any) {
	kept := []any{}
	removedIDs := map[string]bool{}
	for itemid, n := range need {
		it := have[itemid]
		it["remains"] = game.Int(it["remains"]) - n
		if game.Int(it["remains"]) <= 0 {
			removedIDs[game.Str(it["_id"])] = true
		}
	}
	for _, iv := range game.List(coach["items"]) {
		if it := game.Map(iv); it != nil && removedIDs[game.Str(it["_id"])] {
			continue
		}
		kept = append(kept, iv)
	}
	coach["items"] = kept
}

func (s *Server) stShopList(c *gin.Context) {
	out := []any{}
	for _, itemid := range game.SpecialShop {
		entry := s.d.Roster.ShopEntry(itemid, s.d.Roster.ProductCategory(itemid), game.STSHOPID+itemid)
		entry["stid"] = game.STSHOPID + itemid
		out = append(out, entry)
	}
	_ = strconv.Itoa
	writeJSON(c, out)
}
