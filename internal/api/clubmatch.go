package api

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// Club-vs-club matches ("đá clan"). The client has two systems:
// Club Friendly (this file) and Club Ranked (a season/WP ladder,
// not implemented). The friendly flow, from the client's AS3:
//
//   - A club member creates a normal room with type
//     "pvp_club_friendly" from the club hub (the button is gated
//     by the friendlymatch/clubmatch feature). That room is the
//     club's WAITING room; clubmates join it through the normal
//     room machinery.
//   - The room master browses other clubs' waiting rooms
//     (POST /room/club), opens one (POST /room/club/<id>/info)
//     and sends a merge request (POST .../request/<id> {memo}).
//   - The other master accepts (POST .../accept/<id>): the
//     requester's club moves into the accepter's room on the
//     opposite side, the requester's room dissolves, and both
//     clubs share one room. Reject/cancel mirror it.
//   - Once merged, members ready up through the standard side
//     ready endpoint and the master starts the match with
//     POST /room/club/<id>/, which drives the same completion
//     as a both-sides-ready room (buildAndAnnounceRoomMatch).
//   - The match itself is an ordinary human match of type
//     Club_Friendly; at match end the result is folded into
//     both clubs' record data (recordClubFriendlyOnce).
//
// Room ids in these endpoints are the room's host coach id —
// the registry key — exactly as the client received them in
// the waiting-room list.

func isClubRoomType(t string) bool {
	l := strings.ToLower(t)
	return strings.Contains(l, "club_friendly") || strings.Contains(l, "club_ranked")
}

func isClubFriendlyRoom(room map[string]any) bool {
	return room != nil && strings.Contains(strings.ToLower(game.Str(room["type"])), "club_friendly")
}

// clubBlock builds the {clubid, clublv, clubname} object the
// client copies around room sides (RoomProxy.clubinfoCopy).
// clublv mirrors ClubBody, whose level is ClubLevel(0) == 1
// until club activity points exist.
func clubBlock(club *game.Club) map[string]any {
	if club == nil {
		return nil
	}
	// records.wins is REQUIRED: the matchmaking effect reads
	// data.records.wins unconditionally for club matches
	// (MatchmakingEffect.Left/RightClubFriendlyInfoChanged,
	// AS3 line 460/508) — without it both clients threw
	// #1010 when the match was announced (telemetry
	// 781/782, user report 2026-11-15).
	wins := 0
	if club.Doc.Records != nil {
		wins = game.Int(club.Doc.Records["friendly_wins"])
	}
	return map[string]any{"clubid": club.ID, "clublv": 1, "clubname": club.Name,
		"records": map[string]any{"wins": wins}}
}

// decorateRoomSideClub fills the side's player entries with
// their club identity (from club_members) and stamps the
// side's club block + clubid from its first player. Called at
// club-room creation (left side) and after a merge (right).
func (s *Server) decorateRoomSideClub(ctx context.Context, room map[string]any, sideName string) {
	sd := game.Map(game.Map(room["side"])[sideName])
	if sd == nil {
		return
	}
	players := game.List(sd["players"])
	for _, pv := range players {
		e := game.Map(pv)
		if e == nil || game.Str(e["clubid"]) != "" {
			continue
		}
		if club, _ := game.ClubOfCoach(ctx, s.d.Store, game.Str(e["id"])); club != nil {
			e["clubid"] = club.ID
			e["clubname"] = club.Name
		}
	}
	if len(players) == 0 {
		return
	}
	first := game.Map(players[0])
	cid := game.Str(first["clubid"])
	if cid == "" {
		return
	}
	if club, _ := game.ClubGet(ctx, s.d.Store, cid); club != nil {
		sd["club"] = clubBlock(club)
		sd["clubid"] = club.ID
	}
}

func roomSideClubID(room map[string]any, sideName string) string {
	sd := game.Map(game.Map(room["side"])[sideName])
	if sd == nil {
		return ""
	}
	if id := game.Str(sd["clubid"]); id != "" {
		return id
	}
	return game.Str(game.Map(sd["club"])["clubid"])
}

func roomSidePlayers(room map[string]any, sideName string) []map[string]any {
	out := []map[string]any{}
	for _, pv := range game.List(game.Map(game.Map(room["side"])[sideName])["players"]) {
		if e := game.Map(pv); e != nil {
			out = append(out, e)
		}
	}
	return out
}

// clubRoomMerged reports whether the room holds two different
// clubs on its two sides — the state a merge produces and the
// start endpoint requires.
func clubRoomMerged(room map[string]any) bool {
	l, r := roomSideClubID(room, "left"), roomSideClubID(room, "right")
	return l != "" && r != "" && l != r &&
		len(roomSidePlayers(room, "left")) > 0 && len(roomSidePlayers(room, "right")) > 0
}

// regAllRooms snapshots the room registry (host coach id ->
// room) for the waiting-room list.
func (s *Server) regAllRooms() map[string]map[string]any {
	g := s.registry()
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]map[string]any, len(g.hosts))
	for k, v := range g.hosts {
		out[k] = v
	}
	return out
}

// clubWaitingRooms lists the club-friendly rooms still waiting
// for an opponent club (stage "room", not yet merged).
func (s *Server) clubWaitingRooms() map[string]map[string]any {
	out := map[string]map[string]any{}
	for hostCID, room := range s.regAllRooms() {
		if !isClubFriendlyRoom(room) || game.Str(room["stage"]) != "room" {
			continue
		}
		if roomSideClubID(room, "right") != "" {
			continue // already merged
		}
		out[hostCID] = room
	}
	return out
}

// clubRoomListEntry is one waiting-room row for POST /room/club
// and for the merge-request push: identity of the host club,
// the room's member list and its memo.
func (s *Server) clubRoomListEntry(ctx context.Context, hostCID string, room map[string]any) map[string]any {
	clubID := roomSideClubID(room, "left")
	entry := map[string]any{
		"roomid": hostCID, "id": hostCID,
		"clubid": clubID, "slotsize": game.Int(room["slotsize"]),
		"__message": game.Str(room["club_memo"]),
		"users":     roomSidePlayers(room, "left"),
	}
	if club, _ := game.ClubGet(ctx, s.d.Store, clubID); club != nil {
		entry["clubname"] = club.Name
		entry["clublv"] = 1
		entry["info"] = clubBlock(club)
	}
	return entry
}

// clubRoomList serves POST /room/club {keyword, searchtype}:
// the waiting-room browser. The client reads the rooms array
// out of the response's resources.
func (s *Server) clubRoomList(c *gin.Context) {
	if _, _, ok := s.loadState(c); !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	keyword := strings.ToLower(game.Str(req["keyword"]))
	ctx := c.Request.Context()
	rooms := []any{}
	for hostCID, room := range s.clubWaitingRooms() {
		e := s.clubRoomListEntry(ctx, hostCID, room)
		if keyword != "" && !strings.Contains(strings.ToLower(game.Str(e["clubname"])), keyword) {
			continue
		}
		rooms = append(rooms, e)
	}
	writeJSON(c, map[string]any{"result": true, "resources": []any{
		map[string]any{"uri": "/room/club", "body": map[string]any{"rooms": rooms}},
	}})
}

// clubRoomCount serves POST /room/club/count.
func (s *Server) clubRoomCount(c *gin.Context) {
	if _, _, ok := s.loadState(c); !ok {
		return
	}
	writeJSON(c, map[string]any{"result": true, "resources": []any{
		map[string]any{"uri": "/room/club/count", "body": map[string]any{"count": len(s.clubWaitingRooms())}},
	}})
}

// clubRoomInfo serves POST /room/club/<roomid>/info: the VS
// popup data — the room resource plus the info block and memo
// the popup reads (room.info.clubid, room.__message, members).
func (s *Server) clubRoomInfo(c *gin.Context) {
	if _, _, ok := s.loadState(c); !ok {
		return
	}
	hostCID := c.Param("a")
	room := s.regHostRoom(hostCID)
	if room == nil || !isClubFriendlyRoom(room) {
		writeJSON(c, map[string]any{"result": false})
		return
	}
	ctx := c.Request.Context()
	body := map[string]any{}
	for k, v := range room {
		body[k] = v
	}
	body["__message"] = game.Str(room["club_memo"])
	if club, _ := game.ClubGet(ctx, s.d.Store, roomSideClubID(room, "left")); club != nil {
		body["info"] = clubBlock(club)
	}
	writeJSON(c, map[string]any{"result": true, "resources": []any{
		map[string]any{"uri": "/room/club/" + hostCID + "/info", "body": body},
	}})
}

// clubRoomPair resolves the two rooms of a merge action and
// checks the caller hosts the first one. The second id is a
// host coach id (it comes from our own room list), but the
// FIRST id is whatever the client's RoomProxy holds — the
// constant fx-style "1", never the registry key (journal
// 2026-11-15: POST /room/club/1 answered NOT_ROOM_MASTER on
// both clients). So "my room" is resolved from the caller:
// the room they host, if any.
func (s *Server) clubRoomPair(c *gin.Context) (mine, theirs map[string]any, myCID, a, b string, ok bool) {
	st, _, sok := s.loadState(c)
	if !sok {
		return nil, nil, "", "", "", false
	}
	myCID, _ = myCoachID(st)
	a, b = c.Param("a"), c.Param("b")
	if a != myCID {
		if s.regHostRoom(myCID) != nil {
			a = myCID
		} else {
			writeJSON(c, map[string]any{"result": false, "reason": "NOT_ROOM_MASTER"})
			return nil, nil, "", "", "", false
		}
	}
	mine = s.regHostRoom(a)
	theirs = s.regHostRoom(b)
	if mine == nil || theirs == nil || !isClubFriendlyRoom(mine) || !isClubFriendlyRoom(theirs) {
		writeJSON(c, map[string]any{"result": false, "reason": "ROOM_NOT_FOUND"})
		return nil, nil, "", "", "", false
	}
	return mine, theirs, myCID, a, b, true
}

func pushToRoomMembers(uri string, body any, room map[string]any) {
	for _, cid := range roomCoachIDs(room) {
		game.MessengerPush(uri, body, cid)
	}
}

// clubRoomRequest serves POST /room/club/<mine>/request/<theirs>
// {memo}: ask another club's waiting room to merge into a match.
func (s *Server) clubRoomRequest(c *gin.Context) {
	mine, theirs, _, a, b, ok := s.clubRoomPair(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	memo := game.Str(req["memo"])
	ctx := c.Request.Context()
	myClub, theirClub := roomSideClubID(mine, "left"), roomSideClubID(theirs, "left")
	if myClub == "" || theirClub == "" || myClub == theirClub {
		writeJSON(c, map[string]any{"result": false, "reason": "SAME_CLUB"})
		return
	}
	if game.Map(theirs["club_request"]) != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "ALREADY_REQUESTED"})
		return
	}
	theirs["club_request"] = map[string]any{
		"from": a, "clubid": myClub, "memo": memo,
		"at": time.Now().Unix(),
	}
	mine["club_request_out"] = b
	entry := s.clubRoomListEntry(ctx, a, mine)
	entry["__message"] = memo
	pushToRoomMembers("/room/club/request/", entry, theirs)
	writeJSON(c, map[string]any{"result": true, "room": mine})
}

// clubRoomAccept serves POST /room/club/<mine>/accept/<theirs>:
// the caller's room (which received the request) survives; the
// requester's club moves onto its right side and the
// requester's room dissolves.
func (s *Server) clubRoomAccept(c *gin.Context) {
	mine, theirs, _, a, b, ok := s.clubRoomPair(c)
	if !ok {
		return
	}
	// The pending record sits on the room that RECEIVED the
	// request. Accept is called by the receiver, so the
	// caller's room must hold a request from b's room.
	pendMine := game.Map(mine["club_request"])
	if pendMine == nil || game.Str(pendMine["from"]) != b {
		writeJSON(c, map[string]any{"result": false, "reason": "NO_REQUEST"})
		return
	}
	if !s.mergeClubRooms(c.Request.Context(), a, mine, b, theirs) {
		writeJSON(c, map[string]any{"result": false, "reason": "ROOM_SHAPE"})
		return
	}
	writeJSON(c, map[string]any{"result": true, "resources": []any{
		map[string]any{"uri": "/room/1", "body": mine},
	}})
}

// mergeClubRooms folds room b's club (its left side) into
// room a's right side; room a survives, room b dissolves,
// and every member of the merged room is pushed the new
// room. Shared by the accept endpoint (b asked, a
// accepted) and by the start endpoint's random match (a
// pressed the main button in an unmerged waiting room and
// b was waiting too).
func (s *Server) mergeClubRooms(ctx context.Context, a string, mine map[string]any, b string, theirs map[string]any) bool {
	theirLeft := game.Map(game.Map(theirs["side"])["left"])
	myRight := game.Map(game.Map(mine["side"])["right"])
	if theirLeft == nil || myRight == nil {
		return false
	}
	moved := roomSidePlayers(theirs, "left")
	myRight["players"] = game.List(theirLeft["players"])
	myRight["squad"] = theirLeft["squad"]
	myRight["team"] = theirLeft["team"]
	myRight["authTeamId"] = theirLeft["authTeamId"]
	myRight["ready"] = false
	if roles := game.Map(mine["roles"]); roles != nil {
		theirRoles := game.Map(game.Map(theirs["roles"])["left"])
		roles["right"] = map[string]any{
			"0": game.List(theirRoles["0"]), "1": []any{}, "2": []any{}, "3": []any{},
		}
	}
	s.decorateRoomSideClub(ctx, mine, "right")
	delete(mine, "club_request")
	delete(theirs, "club_request_out")
	// The requester's members now sit in the surviving room.
	for _, e := range moved {
		s.regSetSeat(game.Str(e["id"]), a)
	}
	s.regDropHost(b)
	// Mirror the merged room into every moved member's state
	// and persist the host's copy.
	if hu := s.userByCoachID(ctx, a); hu != nil {
		if hst, err := game.LoadState(ctx, s.d.Store, hu.ID); err == nil {
			s.persistRoom(ctx, hst, hu, mine)
		}
	}
	for _, e := range moved {
		cid := game.Str(e["id"])
		if cid == a {
			continue
		}
		if pu := s.userByCoachID(ctx, cid); pu != nil {
			if pst, err := game.LoadState(ctx, s.d.Store, pu.ID); err == nil {
				pst["active_room"] = mine
				_ = game.SaveState(ctx, s.d.Store, pu.ID, pst)
			}
		}
	}
	pushToRoomMembers("/room/club/merge", mine, mine)
	for _, cid := range roomCoachIDs(mine) {
		game.MessengerPush("/room/1", pushBody("/room/1", mine), cid)
	}
	return true
}

// clubRoomReject serves POST /room/club/<mine>/reject/<theirs>.
func (s *Server) clubRoomReject(c *gin.Context) {
	mine, theirs, _, a, b, ok := s.clubRoomPair(c)
	if !ok {
		return
	}
	if pend := game.Map(mine["club_request"]); pend != nil && game.Str(pend["from"]) == b {
		delete(mine, "club_request")
	}
	if game.Str(theirs["club_request_out"]) == a {
		delete(theirs, "club_request_out")
	}
	pushToRoomMembers("/room/club/reject/", map[string]any{"roomid": a}, theirs)
	writeJSON(c, map[string]any{"result": true})
}

// clubRoomCancel serves POST /room/club/<mine>/cancel/<theirs>:
// withdraw a merge request the caller sent earlier.
func (s *Server) clubRoomCancel(c *gin.Context) {
	mine, theirs, _, a, b, ok := s.clubRoomPair(c)
	if !ok {
		return
	}
	if pend := game.Map(theirs["club_request"]); pend != nil && game.Str(pend["from"]) == a {
		delete(theirs, "club_request")
	}
	if game.Str(mine["club_request_out"]) == b {
		delete(mine, "club_request_out")
	}
	pushToRoomMembers("/room/club/cancel/", map[string]any{"roomid": a}, theirs)
	writeJSON(c, map[string]any{"result": true})
}

// clubRoomStart serves POST /room/club/<roomid>/ (and without
// the trailing slash): the room master starts the match once
// the room is merged. Both sides are marked ready and the
// standard room completion builds and announces the match.
func (s *Server) clubRoomStart(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	myCID, _ := myCoachID(st)
	a := c.Param("a")
	if a != myCID {
		// Same fx-style constant "1" as in clubRoomPair:
		// resolve the caller's own hosted room instead.
		if s.regHostRoom(myCID) != nil {
			a = myCID
		} else {
			writeJSON(c, map[string]any{"result": false, "reason": "NOT_ROOM_MASTER"})
			return
		}
	}
	room := s.regHostRoom(a)
	if room == nil || !isClubFriendlyRoom(room) {
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_MERGED"})
		return
	}
	ctx := c.Request.Context()
	if !clubRoomMerged(room) {
		// The waiting room's main button posts here even
		// before any merge (RoomController.
		// createClubMatchRoom, fired by the screen's
		// matchmaking button): it is the random-match
		// entry, not an error. Pair with another club's
		// waiting room when one exists. The match itself
		// then starts through the ready flow (or a second
		// start press), so nobody is dragged into a match
		// without readying first.
		myClub := roomSideClubID(room, "left")
		var other string
		var otherRoom map[string]any
		for hostCID, wr := range s.clubWaitingRooms() {
			if hostCID == a || roomSideClubID(wr, "left") == myClub {
				continue
			}
			other, otherRoom = hostCID, wr
			break
		}
		if other == "" {
			writeJSON(c, map[string]any{"result": false, "reason": "NO_WAITING_ROOM"})
			return
		}
		if !s.mergeClubRooms(ctx, a, room, other, otherRoom) {
			writeJSON(c, map[string]any{"result": false, "reason": "ROOM_SHAPE"})
			return
		}
		writeJSON(c, map[string]any{"result": true, "resources": []any{
			map[string]any{"uri": "/room/1", "body": room},
		}})
		return
	}
	for _, sideName := range []string{"left", "right"} {
		sd := game.Map(game.Map(room["side"])[sideName])
		if sd == nil {
			continue
		}
		sd["ready"] = true
		for _, e := range roomSidePlayers(room, sideName) {
			e["ready"] = true
		}
	}
	s.persistRoom(ctx, st, user, room)
	s.buildAndAnnounceRoomMatch(c, st, user, room, 1)
}

// recordClubFriendlyOnce folds a finished Club_Friendly match
// into both clubs' record data, exactly once per match (Redis
// SetNX guard — both parties post /end with their own state
// copies). Scores arrive in match orientation (home = left).
func (s *Server) recordClubFriendlyOnce(ctx context.Context, match map[string]any, req map[string]any, meName string) {
	mid := game.Str(match["id"])
	if mid == "" {
		return
	}
	guard, err := s.d.Store.Redis.SetNX(ctx, "clubrec:"+mid, "1", 24*time.Hour).Result()
	if err != nil || !guard {
		return
	}
	if meName == "" {
		meName = "home" // matchEnd's own default for the caller
	}
	score := func(side string) int { return game.Int(game.Map(req[side])["score"]) }
	homeScore, awayScore := score("home"), score("away")
	forfeitSide := ""
	if game.Int(req["forfeits"]) > 0 {
		// The caller forfeits: their side loses 0-5.
		if meName == "home" || meName == "left" {
			homeScore, awayScore, forfeitSide = 0, 5, "left"
		} else {
			homeScore, awayScore, forfeitSide = 5, 0, "right"
		}
	}
	leftClub := game.Str(game.Map(game.Map(game.Map(match["side"])["left"])["club"])["clubid"])
	rightClub := game.Str(game.Map(game.Map(game.Map(match["side"])["right"])["club"])["clubid"])
	if leftClub == "" || rightClub == "" || leftClub == rightClub {
		return
	}
	// The shared fixture row, in match orientation (home =
	// left club), identical in both clubs' records.
	fixture := map[string]any{
		"date": game.W3C(float64(time.Now().Unix())), "type": "friendly",
		"homeclubid": leftClub, "awayclubid": rightClub,
		"homescore": homeScore, "awayscore": awayScore,
	}
	if lc, _ := game.ClubGet(ctx, s.d.Store, leftClub); lc != nil {
		fixture["homeclubname"] = lc.Name
	}
	if rc, _ := game.ClubGet(ctx, s.d.Store, rightClub); rc != nil {
		fixture["awayclubname"] = rc.Name
	}
	leftPlayers := sidePlayerIDs(match, "left")
	rightPlayers := sidePlayerIDs(match, "right")
	s.recordClubFriendlyFor(ctx, leftClub, rightClub, homeScore, awayScore,
		leftPlayers, forfeitSide == "right", fixture)
	s.recordClubFriendlyFor(ctx, rightClub, leftClub, awayScore, homeScore,
		rightPlayers, forfeitSide == "left", fixture)
}

func sidePlayerIDs(match map[string]any, sideName string) []string {
	out := []string{}
	for _, pv := range game.List(game.Map(game.Map(match["side"])[sideName])["players"]) {
		e := game.Map(pv)
		id := game.Str(e["_id"])
		if id == "" {
			id = game.Str(e["id"])
		}
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

// recordClubFriendlyFor applies one club's view of a finished
// friendly: totals, the opponent row, the shared recent-fixture
// entry and the monthly per-member win counts. oppForfeited
// marks a win handed over by the opponent's forfeit.
func (s *Server) recordClubFriendlyFor(ctx context.Context, clubID, oppID string,
	myScore, oppScore int, myPlayers []string, oppForfeited bool,
	fixture map[string]any) {
	club, err := game.ClubGet(ctx, s.d.Store, clubID)
	if err != nil || club == nil {
		return
	}
	opp, _ := game.ClubGet(ctx, s.d.Store, oppID)
	oppName := ""
	if opp != nil {
		oppName = opp.Name
	}
	rec := club.Doc.Records
	if rec == nil {
		rec = map[string]any{}
	}
	month := clubRecordMonth()
	if game.Int(rec["month"]) != month {
		// New month: member contributions reset; totals and
		// opponent/recent history carry over.
		rec["month"] = month
		rec["members"] = map[string]any{}
	}
	won := myScore > oppScore
	draw := myScore == oppScore
	rec["friendly_total"] = game.Int(rec["friendly_total"]) + 1
	rec["total"] = game.Int(rec["total"]) + 1
	if won {
		rec["friendly_wins"] = game.Int(rec["friendly_wins"]) + 1
		rec["wins"] = game.Int(rec["wins"]) + 1
		rec["totalwins"] = game.Int(rec["totalwins"]) + 1
		if oppForfeited {
			rec["friendly_forfeitwins"] = game.Int(rec["friendly_forfeitwins"]) + 1
			rec["forfeitwins"] = game.Int(rec["forfeitwins"]) + 1
			rec["totalforfeitwins"] = game.Int(rec["totalforfeitwins"]) + 1
		}
	}
	// Opponent row.
	opps := game.List(rec["opponents"])
	var row map[string]any
	for _, ov := range opps {
		if game.Str(game.Map(ov)["clubid"]) == oppID {
			row = game.Map(ov)
			break
		}
	}
	if row == nil {
		row = map[string]any{"clubid": oppID, "name": oppName,
			"wins": 0, "draws": 0, "losses": 0, "forfeitwins": 0, "total": 0, "wp": 0}
		opps = append(opps, row)
	}
	row["total"] = game.Int(row["total"]) + 1
	switch {
	case won:
		row["wins"] = game.Int(row["wins"]) + 1
		if oppForfeited {
			row["forfeitwins"] = game.Int(row["forfeitwins"]) + 1
		}
	case draw:
		row["draws"] = game.Int(row["draws"]) + 1
	default:
		row["losses"] = game.Int(row["losses"]) + 1
	}
	rec["opponents"] = opps
	// Recent fixture (shared row, match orientation).
	recent := game.List(rec["recent"])
	recent = append([]any{fixture}, recent...)
	if len(recent) > 20 {
		recent = recent[:20]
	}
	rec["recent"] = recent
	// Member contributions (monthly).
	members := game.Map(rec["members"])
	if members == nil {
		members = map[string]any{}
		rec["members"] = members
	}
	for _, cid := range myPlayers {
		ms := game.Map(members[cid])
		if ms == nil {
			ms = map[string]any{"friendly_wins": 0, "friendly_forfeitwins": 0,
				"ranked_wins": 0, "ranked_losses": 0}
			members[cid] = ms
		}
		if won {
			ms["friendly_wins"] = game.Int(ms["friendly_wins"]) + 1
			if oppForfeited {
				ms["friendly_forfeitwins"] = game.Int(ms["friendly_forfeitwins"]) + 1
			}
		}
	}
	club.Doc.Records = rec
	_ = game.ClubSaveDoc(ctx, s.d.Store, club)
}
