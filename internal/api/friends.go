package api

// Contact/friends endpoints, ported from fx ep_contact_*. The list
// goes at the mutation responses' TOP LEVEL (not only in resources):
// the Messenger window repaints from the response itself.

import (
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func (s *Server) myCoachID(st game.State) string {
	slot := game.DefaultSlot(st)
	if coach := game.CoachBySlot(st, slot); coach != nil {
		return game.Str(coach["_id"])
	}
	return ""
}

// listForCoach builds the contact list owned by coachID (their own
// groups, their own rows). Unknown/virtual coaches get an empty list.
func (s *Server) listForCoach(c *gin.Context, coachID string) map[string]any {
	ctx := c.Request.Context()
	idx := game.CoachIndex(ctx, s.d.Store, s.d.Roster)
	ownerState := game.State{}
	if info := idx[coachID]; info != nil && info.Username != "" {
		if u, err := game.UserByUsername(ctx, s.d.Store, info.Username); err == nil && u != nil {
			if os, err := game.LoadState(ctx, s.d.Store, u.ID); err == nil && os != nil {
				ownerState = os
			}
		}
	}
	return game.ContactList(ctx, s.d.Store, idx, ownerState, coachID)
}

func (s *Server) pushContactList(c *gin.Context, coachID string) {
	game.MessengerPush("/contact/list", s.listForCoach(c, coachID), coachID)
}

func (s *Server) contactList(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	idx := game.CoachIndex(ctx, s.d.Store, s.d.Roster)
	writeJSON(c, game.ContactList(ctx, s.d.Store, idx, st, s.myCoachID(st)))
}

func (s *Server) presence(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	idx := game.CoachIndex(ctx, s.d.Store, s.d.Roster)
	writeJSON(c, game.PresenceList(ctx, s.d.Store, idx, st, s.myCoachID(st)))
}

func (s *Server) contactRequesting(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	fname := strings.TrimSpace(game.Str(req["friendname"]))
	if fname == "" {
		writeJSON(c, map[string]any{"result": false, "reason": "not found"})
		return
	}
	myCID := s.myCoachID(st)
	myName := ""
	if coach := game.CoachBySlot(st, game.DefaultSlot(st)); coach != nil {
		myName = game.Str(coach["name"])
	}
	idx := game.CoachIndex(ctx, s.d.Store, s.d.Roster)
	targetCID, targetName := "", fname
	for cid, info := range idx {
		if info != nil && info.Name == fname {
			targetCID, targetName = cid, info.Name
			break
		}
	}
	if targetCID == "" {
		targetCID = game.VirtualCoachID(fname)
	}
	if targetCID == myCID {
		writeJSON(c, map[string]any{"result": false, "reason": "cannot add self"})
		return
	}
	cur := game.FriendStatus(ctx, s.d.Store, myCID, targetCID)
	switch {
	case cur == "requested":
		// They had already asked me: this send accepts the pair.
		game.FriendSetPairStatus(ctx, s.d.Store, myCID, targetCID, "accepted")
	case cur != "accepted" && cur != "requesting":
		now := game.NowISO()
		game.FriendUpsert(ctx, s.d.Store, myCID, targetCID, targetName, "requesting", now)
		game.FriendUpsert(ctx, s.d.Store, targetCID, myCID, myName, "requested", now)
	}
	cl := game.ContactList(ctx, s.d.Store, idx, st, myCID)
	s.pushContactList(c, myCID)
	s.pushContactList(c, targetCID)
	out := map[string]any{"result": true, "friend": map[string]any{"_id": targetCID, "name": targetName},
		"resources": []any{map[string]any{"uri": "/contact/list", "body": cl}}}
	for k, v := range cl {
		out[k] = v
	}
	writeJSON(c, out)
}

func (s *Server) contactReply(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	cid := c.Param("cid")
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	reply := game.Str(req["reply"])
	if reply == "" {
		reply = "accept"
	}
	myCID := s.myCoachID(st)
	// The pair is between me (the replier) and :cid (the requester) —
	// both are known exactly. (The old code looked the pair up by the
	// requester alone via FriendPair, which returned the requester's
	// FIRST friend row — so once the requester had any earlier friend,
	// accept/decline silently hit that older pair instead.)
	if reply == "accept" {
		game.FriendSetPairStatus(ctx, s.d.Store, myCID, cid, "accepted")
	} else {
		game.FriendDeletePair(ctx, s.d.Store, myCID, cid)
	}
	owner := myCID
	cl := s.listForCoach(c, owner)
	s.pushContactList(c, owner)
	s.pushContactList(c, cid)
	out := map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/contact/list", "body": cl}}}
	for k, v := range cl {
		out[k] = v
	}
	writeJSON(c, out)
}

func (s *Server) contactCancelRequesting(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	cid := c.Param("cid")
	myCID := s.myCoachID(st)
	// Cancel MY outgoing request to :cid — the pair is (me, :cid);
	// see contactReply for why the pair must not be looked up by
	// the other coach alone.
	game.FriendDeletePair(ctx, s.d.Store, myCID, cid)
	idx := game.CoachIndex(ctx, s.d.Store, s.d.Roster)
	cl := game.ContactList(ctx, s.d.Store, idx, st, myCID)
	s.pushContactList(c, myCID)
	s.pushContactList(c, cid)
	out := map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/contact/list", "body": cl}}}
	for k, v := range cl {
		out[k] = v
	}
	writeJSON(c, out)
}

func (s *Server) contactFriendAction(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	cid := c.Param("cid")
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	myCID := s.myCoachID(st)
	if game.Str(req["_method"]) == "DELETE" || c.Request.Method == "DELETE" {
		game.FriendDeletePair(ctx, s.d.Store, myCID, cid)
	} else {
		var group *string
		if v, present := req["groupname"]; present && v != nil {
			g := game.Str(v)
			group = &g
		}
		game.FriendSetGroup(ctx, s.d.Store, myCID, cid, group)
	}
	idx := game.CoachIndex(ctx, s.d.Store, s.d.Roster)
	cl := game.ContactList(ctx, s.d.Store, idx, st, myCID)
	s.pushContactList(c, myCID)
	s.pushContactList(c, cid)
	out := map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/contact/list", "body": cl}}}
	for k, v := range cl {
		out[k] = v
	}
	writeJSON(c, out)
}

func (s *Server) contactNewFriends(c *gin.Context) {
	writeJSON(c, map[string]any{"result": true})
}

func (s *Server) contactGroup(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	gname := strings.TrimSpace(game.Str(req["groupname"]))
	if gname != "" {
		groups := game.ContactGroups(st)
		found := false
		for _, g := range groups {
			if g == gname {
				found = true
			}
		}
		if !found {
			all := game.List(st["contact_groups"])
			st["contact_groups"] = append(all, gname)
			_ = game.SaveState(ctx, s.d.Store, user.ID, st)
		}
	}
	myCID := s.myCoachID(st)
	cl := s.listForCoach(c, myCID)
	s.pushContactList(c, myCID)
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/contact/list", "body": cl}}})
}

func (s *Server) contactGroupDelete(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	gname, _ := url.PathUnescape(c.Param("gname"))
	groups := game.ContactGroups(st)
	out := []any{}
	for _, g := range groups {
		if g != gname {
			out = append(out, g)
		}
	}
	if len(out) != len(groups) {
		st["contact_groups"] = out
		_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	}
	myCID := s.myCoachID(st)
	cl := s.listForCoach(c, myCID)
	s.pushContactList(c, myCID)
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/contact/list", "body": cl}}})
}
