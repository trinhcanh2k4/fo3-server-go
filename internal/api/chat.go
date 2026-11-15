package api

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// World chat + coach profile + badge use, ported from fo3_ext.py's
// chat subsystem and http_config.py's ep_coach_profile (batch
// 2026-11-13: audit groups A/B).

func (s *Server) chatChannelBody(st game.State, user *game.User) (game.State, map[string]any) {
	info := game.ChatCoachSummary(st)
	num := game.Int(st["chat_channel"])
	if num == 0 {
		num = 1
	}
	ch := game.ChatEnsureInChannel(num, info)
	st["chat_channel"] = ch.ID
	return st, map[string]any{
		"result": true, "channelid": ch.ID, "created": false, "reason": "",
		"id": ch.ID, "size": ch.Size, "members": ch.Members,
	}
}

// Any /chat/channel — join (or _method=DELETE leave).
func (s *Server) chatChannel(c *gin.Context) {
	body, _ := io.ReadAll(c.Request.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	if game.Str(req["_method"]) == "DELETE" {
		writeJSON(c, map[string]any{"result": true, "channelid": 0, "reason": ""})
		return
	}
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	_, resp := s.chatChannelBody(st, user)
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, resp)
}

// Any /chat/channel/:id — switch channel.
func (s *Server) chatChannelJoin(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	info := game.ChatCoachSummary(st)
	ch := game.ChatEnsureInChannel(mustAtoiParam(c.Param("id")), info)
	st["chat_channel"] = ch.ID
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{
		"result": true, "channelid": ch.ID, "created": false, "reason": "",
		"id": ch.ID, "size": ch.Size, "members": ch.Members,
	})
}

// POST /chat/my/channel/message — send to the current world channel.
func (s *Server) chatSendMessage(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	_ = user
	info := game.ChatCoachSummary(st)
	num := game.Int(st["chat_channel"])
	if num == 0 {
		num = 1
	}
	ch := game.ChatChannelGet(num)
	body, _ := io.ReadAll(c.Request.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	text := strings.TrimSpace(game.Str(req["message"]))
	hint := game.Map(req["hint"])
	if hint == nil {
		hint = map[string]any{"linkinfo": []any{}}
	}
	if text == "" {
		writeJSON(c, map[string]any{"result": true})
		return
	}
	if len([]rune(text)) > 200 {
		text = string([]rune(text)[:200])
	}
	vo := map[string]any{
		"type":      "chat",
		"coachid":   info["id"],
		"coachname": info["name"],
		"message":   text,
		"hint":      hint,
		"time":      int(time.Now().Unix()),
	}
	game.ChatAppendMessage(ch, vo)
	game.MessengerPush("/chat/my/channel/messages/latest", vo, "")
	writeJSON(c, map[string]any{"result": true})
}

// GET /chat/my/channel/messages/latest — newest channel message VO.
func (s *Server) chatChannelLatest(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	info := game.ChatCoachSummary(st)
	num := game.Int(st["chat_channel"])
	if num == 0 {
		num = 1
	}
	ch := game.ChatEnsureInChannel(num, info)
	writeJSON(c, game.ChatLatest(ch))
}

// GET /chat/my/messages/latest — whisper latest (fx constant).
func (s *Server) chatWhisperLatest(c *gin.Context) {
	writeJSON(c, map[string]any{
		"type":      "whisper",
		"coachid":   "",
		"coachname": "",
		"message":   "",
		"hint":      map[string]any{"linkinfo": []any{}},
		"time":      int(time.Now().Unix()),
	})
}

// Any /chat/coach/:cid/channel/detail — fx returns MY coach summary
// under the requested id (parity quirk).
func (s *Server) chatCoachDetail(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	info := game.ChatCoachSummary(st)
	cid := c.Param("cid")
	writeJSON(c, map[string]any{
		"result":    true,
		"id":        cid,
		"_id":       cid,
		"name":      info["name"],
		"coachname": info["name"],
		"teamid":    info["teamid"],
		"level":     info["level"],
		"clubname":  "",
	})
}

// Any /coach/me/profile — read/update the coach's profile blurb.
func (s *Server) coachMeProfile(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	slot := game.DefaultSlot(st)
	coach := game.CoachBySlot(st, slot)
	if coach == nil {
		writeJSON(c, map[string]any{"result": false})
		return
	}
	body, _ := io.ReadAll(c.Request.Body)
	if len(body) > 0 {
		var req map[string]any
		if json.Unmarshal(body, &req) == nil {
			prof := game.Map(coach["profile"])
			if prof == nil {
				prof = map[string]any{}
				coach["profile"] = prof
			}
			if v, has := req["description"]; has {
				prof["description"] = v
			}
			if v, has := req["picture"]; has {
				prof["picture"] = v
			}
			_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
		}
	}
	prof := game.Map(coach["profile"])
	if prof == nil {
		prof = map[string]any{
			"description": "Xin chào! Chào mừng đến với FIFA Online 3.",
			"picture":     0,
		}
	}
	writeJSON(c, prof)
}

// POST /badge/:id/use — set the displayed badge (fx ep_badge_use).
func (s *Server) badgeUse(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	slot := game.DefaultSlot(st)
	coach := game.CoachBySlot(st, slot)
	if coach == nil {
		writeJSON(c, map[string]any{"result": true})
		return
	}
	val, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		val = 1001
	}
	if val < 1001 {
		val = 1001
	}
	coach["currentbadge"] = val
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{
		"result":    true,
		"resources": s.d.Roster.CoachMeResources(st, slot, user),
	})
}
