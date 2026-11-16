package api

// Room invitations as in-game letters (owner design 2026-11-13,
// replacing the short-lived /invite web page). RE over the client's
// SystemMessageUtil (Module.swf) proved this build carries the
// original game's invitation protocol end to end:
//
//   - A notification {category:"match", type:"invite"} in the
//     /inbox/my/notifications feed renders as the letter
//     "Thư mời đấu: <details.coachname>"; clicking it opens the
//     "Lời mời" dialog (Đồng ý / Từ chối).
//   - Đồng ý (after a GET /presence/me gate whose response needs a
//     "status" field) issues PUT /room/<details.roomid>/join/ with
//     body {version}. Success consumes the response resources (the
//     room resource lands the guest in the room); failure answers
//     {result:false, reason} and the client maps the reason string
//     to a popup — "room expired" shows the waiting-room-lost
//     ("hết hạn") dialog.
//   - Từ chối issues POST /room/<details.roomid>/invite/rejection/
//     from/<details.coachid>; a {category:"match", type:"reject"}
//     note tells the inviter ("<coachname> đã từ chối...").
//
// details.roomid is the host's coach id — the room registry is
// keyed by host, so the join/rejection handlers resolve the room
// straight from the path. The Redis pending record tracks the
// invitation server-side (who invited whom, TTL).

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// inviteTTL bounds how long an invitation letter stays valid.
const inviteTTL = 1800 * time.Second

func (s *Server) pendingInvite(ctx context.Context, guestCID string) map[string]any {
	raw, err := s.d.Store.Redis.Get(ctx, "roominvite:"+guestCID).Result()
	if err != nil || raw == "" {
		return nil
	}
	var rec map[string]any
	if json.Unmarshal([]byte(raw), &rec) != nil {
		return nil
	}
	return rec
}

func (s *Server) clearPendingInvite(ctx context.Context, guestCID string) {
	s.d.Store.Redis.Del(ctx, "roominvite:"+guestCID)
}

// hostRoomForInvite loads the host's live room (registry first,
// then the host's persisted state).
func (s *Server) hostRoomForInvite(ctx context.Context, hostCID string) map[string]any {
	if room := s.regHostRoom(hostCID); room != nil {
		return room
	}
	hu := s.userByCoachID(ctx, hostCID)
	if hu == nil {
		return nil
	}
	hst, err := game.LoadState(ctx, s.d.Store, hu.ID)
	if err != nil {
		return nil
	}
	room := game.Map(hst["active_room"])
	if room != nil {
		s.adoptRoom(hst, room)
	}
	return room
}

// inviteNote builds the match/invite letter for a pending record.
func inviteNote(rec map[string]any, guestCID string) map[string]any {
	host := game.Str(rec["host"])
	return map[string]any{
		"_id":      "invite-" + host + "-" + guestCID,
		"category": "match", "type": "invite",
		"date": game.W3C(float64(game.Int(rec["created"]))),
		"details": map[string]any{
			"title":     "SYSMESSAGE_GAME_INVITE",
			"coachname": rec["host_name"],
			"coachid":   host,
			"roomid":    host,
		},
	}
}

// userNotes computes a user's current system notifications: the
// transfer-market sold letter (fx parity) plus the room-invite
// letter while the invitation and its room are both alive.
func (s *Server) userNotes(ctx context.Context, st game.State, myCID string) []any {
	notes := []any{}
	for _, ov := range game.List(st["offers"]) {
		if game.Str(game.Map(ov)["status"]) == "sold" {
			notes = append(notes, map[string]any{
				"_id": newIID(), "category": "transfermarket", "type": "list",
				"date":    game.W3C(float64(time.Now().Unix())),
				"details": map[string]any{"title": "TRANSFERMARKET_LOGIN_ALARM"},
			})
			break
		}
	}
	if rec := s.pendingInvite(ctx, myCID); rec != nil {
		host := game.Str(rec["host"])
		if room := s.hostRoomForInvite(ctx, host); room != nil &&
			roomInvitedHas(room, myCID) {
			notes = append(notes, inviteNote(rec, myCID))
		}
	}
	return notes
}

// notesForCID loads a coach's state and computes their notes.
func (s *Server) notesForCID(ctx context.Context, cid string) []any {
	u := s.userByCoachID(ctx, cid)
	if u == nil {
		return []any{}
	}
	st, err := game.LoadState(ctx, s.d.Store, u.ID)
	if err != nil {
		return []any{}
	}
	return s.userNotes(ctx, st, cid)
}

// pushNotes re-feeds a coach's notification list over Messenger so
// a letter appears (or disappears) without a relog.
func (s *Server) pushNotes(ctx context.Context, cid string) {
	game.MessengerPush("/inbox/my/notifications", s.notesForCID(ctx, cid), cid)
}

// GET /inbox/my/notifications — the caller's letters, bare array
// (same body the onready resource carries).
func (s *Server) inboxNotifications(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	myCID, _ := myCoachID(st)
	writeJSON(c, s.userNotes(c.Request.Context(), st, myCID))
}

// GET /presence/me — the invite dialog's conditionFunction gates on
// this response's "status" field; serve the caller's own presence
// in the same VO shape fx builds for friends.
func (s *Server) presenceMe(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	myCID, myName := myCoachID(st)
	me := s.d.Roster.CoachMe(st, game.DefaultSlot(st), user)
	writeJSON(c, map[string]any{
		"_id": myCID, "id": myCID,
		"coachname": myName, "name": myName,
		"presence": "online", "status": "online", "online": true,
		"teamid": game.Int(me["teamid"]),
		"level":  game.Int(game.Map(me["levelinfo"])["level"]),
		"_uid":   user.ID,
	})
}

// roomJoin seats the caller in the host's room — the Đồng ý half
// of the invite letter: PUT /room/<hostCID>/join/ {version}. It is
// dispatched from roomUpdate (the client's URI carries the room id
// in the :rid segment, which collides with the :action wildcard).
func (s *Server) roomJoin(c *gin.Context, st game.State, user *game.User) {
	ctx := c.Request.Context()
	hostCID := c.Param("rid")
	myCID, _ := myCoachID(st)
	fail := func(reason string) {
		writeJSON(c, map[string]any{"result": false, "reason": reason})
	}
	if exp := game.ExpiredSquadPlayers(st); len(exp) > 0 {
		fail("PLAYER_CONTRACT_EXPIRED")
		return
	}
	if hostCID == "" || hostCID == myCID {
		fail("invalid roomid")
		return
	}
	// Already seated in that room: hand the room back. The seat
	// alone is not proof — after a crashed/abandoned match the
	// seat map can point at a room whose body no longer holds
	// the guest (or that already produced a match); then fall
	// through and seat them properly.
	if seatHost, seated := s.regSeat(myCID); seated && seatHost == hostCID {
		sideName, _ := roomEntrySide(s.regHostRoom(hostCID), myCID)
		if room := s.regHostRoom(hostCID); room != nil &&
			game.Str(room["stage"]) == "room" && sideName != "" {
			writeJSON(c, map[string]any{"result": true,
				"resources": []any{map[string]any{"uri": "/room/1", "body": room}}})
			return
		}
		s.regClearSeat(myCID)
	}
	room := s.hostRoomForInvite(ctx, hostCID)
	rec := s.pendingInvite(ctx, myCID)
	if room == nil || rec == nil || game.Str(rec["host"]) != hostCID ||
		!roomInvitedHas(room, myCID) {
		// The letter outlived its room: the client shows the
		// waiting-room-lost ("hết hạn") popup for this reason.
		fail("room expired")
		return
	}
	if roomFullSeats(room) {
		fail("full")
		return
	}
	entry := s.coachRoomEntry(ctx, myCID)
	if entry == nil {
		fail("invalid roomid")
		return
	}
	// Seat into the first side with space, opponents (right)
	// first — the 1v1 behavior; a full right side overflows
	// into the host's side as a teammate. Members rearrange
	// afterwards with the side buttons.
	side := game.Map(room["side"])
	target := game.Map(side["right"])
	if len(game.List(target["players"])) >= roomCapacity(room) {
		target = game.Map(side["left"])
	}
	wasEmpty := len(game.List(target["players"])) == 0
	target["players"] = append(game.List(target["players"]), entry)
	target["ready"] = false
	if wasEmpty {
		target["authTeamId"] = game.Int(entry["teamid"])
		if entry["squad"] != nil {
			target["squad"] = entry["squad"]
		}
	}
	roomInvitedRemove(room, myCID)
	syncRoomMembers(room)
	s.regSetSeat(myCID, hostCID)
	s.regRoom(room)
	s.saveRoomForHost(ctx, hostCID, room)
	s.clearPendingInvite(ctx, myCID)
	// Mirror the room into the guest's state so their room reads
	// and creates resolve to the host's room natively.
	st["active_room"] = room
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	uri := "/room/1"
	push := pushBody(uri, room)
	game.MessengerPush(uri, push, hostCID)
	game.MessengerPush(uri, push, myCID)
	s.pushNotes(ctx, myCID) // the letter is consumed
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/room/1", "body": room}}})
}

// roomInviteReject is the Từ chối half of the letter:
// POST /room/<hostCID>/invite/rejection/from/<coachid>. The host's
// room loses the invited marker and the host receives a
// match/reject letter naming the declining coach.
func (s *Server) roomInviteReject(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	hostCID := c.Param("rid")
	myCID, myName := myCoachID(st)
	if rec := s.pendingInvite(ctx, myCID); rec != nil &&
		game.Str(rec["host"]) == hostCID {
		s.clearPendingInvite(ctx, myCID)
	}
	if room := s.hostRoomForInvite(ctx, hostCID); room != nil &&
		roomInvitedHas(room, myCID) {
		roomInvitedRemove(room, myCID)
		s.saveRoomForHost(ctx, hostCID, room)
		game.MessengerPush("/room/1", pushBody("/room/1", room), hostCID)
	}
	notes := s.notesForCID(ctx, hostCID)
	notes = append(notes, map[string]any{
		"_id":      "reject-" + myCID + "-" + strconv.FormatInt(time.Now().Unix(), 10),
		"category": "match", "type": "reject",
		"date": game.W3C(float64(time.Now().Unix())),
		"details": map[string]any{
			"coachname": myName, "coachid": myCID, "roomid": hostCID,
		},
	})
	game.MessengerPush("/inbox/my/notifications", notes, hostCID)
	writeJSON(c, map[string]any{"result": true})
}
