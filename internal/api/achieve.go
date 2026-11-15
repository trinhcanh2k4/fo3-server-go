package api

// Achievements + weekly quests endpoints (Phase 2):
// GET /achievement/me, POST /achievement/questrewards,
// POST /achievement/:group/reward/:grade, plus the computed statics
// /static/db/BADGES and /static/info/ACHIEVEMENT (served from
// staticFile). Ports of the fo3_ext.py handlers of the same paths.

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func (s *Server) achievementMe(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	payload := s.d.Roster.AchievementPayload(st)
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, payload)
}

func (s *Server) achievementQuestRewards(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	slot := game.DefaultSlot(st)
	coach := game.CoachBySlot(st, slot)
	ach := game.Map(st["achievements"])
	if ach == nil {
		ach = map[string]any{}
		st["achievements"] = ach
	}
	gpClaimed := game.List(ach["quest_gp_claimed"])
	claimedSet := game.IntSet(ach["quest_gp_claimed"])
	awardedEP := 0
	packID := 0
	for _, m := range game.GPMilestones(s.d.GamedataDir) {
		if !claimedSet[m.GP] {
			gpClaimed = append(gpClaimed, m.GP)
			awardedEP = m.EP
			packID = m.Pack
			if coach != nil {
				coach["ep"] = game.Int(coach["ep"]) + awardedEP
				if packID != 0 {
					game.InventoryAdd(coach, packID, 1)
				}
			}
			break
		}
	}
	ach["quest_gp_claimed"] = gpClaimed
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	payload := s.d.Roster.AchievementPayload(st)
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	rewards := []any{map[string]any{"itemid": 1, "type": "ep", "count": maxOr(awardedEP, 1000000), "name": "EP"}}
	if packID != 0 {
		rewards = append(rewards, map[string]any{"itemid": packID, "type": "item", "count": 1, "name": "Thẻ Chỉ Định Top 100"})
	}
	writeJSON(c, map[string]any{
		"result":  true,
		"rewards": rewards,
		"resources": []any{
			map[string]any{"resource": "/achievement/me", "uri": "/achievement/me", "body": payload},
			map[string]any{"resource": "/coach/me", "uri": "/coach/me", "body": s.d.Roster.CoachMe(st, slot, user)},
		},
	})
}

func maxOr(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func (s *Server) achievementClaim(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	t := game.Achieve(s.d.GamedataDir)
	slot := game.DefaultSlot(st)
	coach := game.CoachBySlot(st, slot)
	ach := game.Map(st["achievements"])
	if ach == nil {
		ach = map[string]any{}
		st["achievements"] = ach
	}
	claimedList := game.List(ach["claimed"])
	claimed := game.StrSet(ach["claimed"])
	refuse := func() {
		writeJSON(c, map[string]any{
			"result": false, "reason": "CAN_NOT_CLAIM_ACHIEVEMENT_REWARDS",
			"resources": []any{
				map[string]any{"resource": "/achievement/me", "uri": "/achievement/me", "body": s.d.Roster.AchievementPayload(st)},
				map[string]any{"resource": "/coach/me", "uri": "/coach/me", "body": s.d.Roster.CoachMe(st, slot, user)},
			},
		})
	}
	payload0 := s.d.Roster.AchievementPayload(st)
	complete := map[string]bool{}
	for _, raw := range game.List(payload0["achievement"]) {
		a := game.Map(raw)
		complete[fmt.Sprintf("%v", a["id"])] = a["complete"] == true
	}
	completeOf := func(id string) bool {
		if id == "0" {
			return complete["0"]
		}
		return complete[id]
	}
	groupTarget := func(gid string) string {
		for _, cid := range t.Mixed[gid] {
			if completeOf(cid) && !claimed[cid] {
				return cid
			}
		}
		pid := gid
		if gid == "misc" {
			pid = "0"
		}
		if completeOf(pid) {
			if gid == "misc" {
				if !claimed["misc"] && !claimed["0"] {
					return gid
				}
			} else if !claimed[gid] {
				return gid
			}
		}
		return ""
	}
	aid := c.Param("group")
	target := ""
	switch {
	case aid == "0" || aid == "misc":
		target = groupTarget("misc")
	case t.Mixed[aid] != nil:
		target = groupTarget(aid)
	case isAllDigits(aid) && game.Int(aid) >= 1 && game.Int(aid) <= 33:
		target = groupTarget("ach_" + aid)
	case completeOf(aid) && !claimed[aid]:
		target = aid
	}
	if target == "" {
		for _, g := range t.GroupOrder() {
			if cand := groupTarget(g); cand != "" {
				target = cand
				break
			}
		}
	}
	if target == "" {
		_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
		refuse()
		return
	}
	claimedList = append(claimedList, target)
	claimed[target] = true
	if len(target) > 5 && target[:5] == "misc_" {
		allIn := true
		for _, m := range []string{"misc_1", "misc_2", "misc_3", "misc_4"} {
			if !claimed[m] {
				allIn = false
			}
		}
		if allIn {
			for _, marker := range []string{"misc", "0"} {
				if !claimed[marker] {
					claimedList = append(claimedList, marker)
					claimed[marker] = true
				}
			}
		}
	}
	ach["claimed"] = claimedList
	entries := staticEntries(t, target)
	rewardsList := []any{}
	for _, e := range entries {
		iid, cnt := game.Int(e["itemid"]), game.Int(e["count"])
		if iid == 0 || cnt <= 0 {
			continue
		}
		if coach != nil {
			if iid == 1 {
				coach["ep"] = game.Int(coach["ep"]) + cnt
			} else {
				game.InventoryAdd(coach, iid, cnt)
			}
		}
		def := s.d.Roster.ItemDef(iid)
		idef := map[string]any{}
		for k, v := range def {
			idef[k] = v
		}
		if idef["itemid"] == nil {
			idef = map[string]any{"itemid": iid, "productid": iid, "name": itoa2(iid), "itemicon": iid}
		}
		idef["count"] = cnt
		idef["itemcnt"] = cnt
		if iid == 1 {
			idef["ep"] = cnt
		}
		rewardsList = append(rewardsList, idef)
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	payload := s.d.Roster.AchievementPayload(st)
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{
		"result":  true,
		"rewards": rewardsList,
		"resources": []any{
			map[string]any{"resource": "/achievement/me", "uri": "/achievement/me", "body": payload},
			map[string]any{"resource": "/coach/me", "uri": "/coach/me", "body": s.d.Roster.CoachMe(st, slot, user)},
		},
	})
}

func staticEntries(t *game.AchieveTables, itemID string) []map[string]any {
	if ov, ok := t.RewardOverride(itemID); ok {
		return ov
	}
	if item := t.ByID(itemID); item != nil {
		rewards := game.List(item["rewards"])
		if len(rewards) > 0 {
			out := []map[string]any{}
			for _, e := range game.List(game.Map(rewards[0])["reward"]) {
				out = append(out, game.Map(e))
			}
			return out
		}
	}
	return []map[string]any{{"itemid": 1, "count": 200000}}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
