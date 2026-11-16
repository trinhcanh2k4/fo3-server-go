package api

// Room endpoints + the matchmaking timer, ported from fx's room
// handlers. Rooms live in state["active_room"] (room id is always
// 1 in this single-room design, as in fx). Completion runs 2s
// after the client enters "searching" and pushes the matched room
// + match over the Messenger; a bridge push 1.5s later advances
// the match created -> squad (Issue 2h).

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

type roomTimers struct {
	mu sync.Mutex
	m  map[string]*time.Timer
}

func (s *Server) timers() *roomTimers {
	if s.rt == nil {
		s.rt = &roomTimers{m: map[string]*time.Timer{}}
	}
	return s.rt
}

// timerKey scopes a matchmaking timer to one account: rid is
// always 1 in this design, so keying timers by rid alone let two
// accounts queueing at once cancel each other's bot pairing (user
// report 2026-11-13: queued both accounts for 1v1, one sat at
// "searching" forever).
func timerKey(rid int, uid int64) string {
	return strconv.Itoa(rid) + ":" + strconv.FormatInt(uid, 10)
}

// roomRegistry tracks live rooms BY HOST (plus who sits in each
// room's guest seat). fx kept rooms in one process-global slot
// (ACTIVE_ROOMS, rid is always 1): with two users in room screens
// at once, the slot's last writer wins, an invite operates on the
// OTHER user's room, and the inviter's client — handed back a room
// it is not a member of — exits to the mode-select screen (user
// report 2026-11-13, corrected: the INVITER was kicked). Keying
// rooms by host keeps fx's shared-room visibility (an invitee
// resolves to the host's room) without the cross-user hijack.
type roomRegistry struct {
	mu    sync.Mutex
	hosts map[string]map[string]any // host coachID -> canonical room
	seat  map[string]string         // guest coachID -> host coachID
}

func (s *Server) registry() *roomRegistry {
	if s.gr == nil {
		s.gr = &roomRegistry{hosts: map[string]map[string]any{}, seat: map[string]string{}}
	}
	return s.gr
}

func (s *Server) regHostRoom(hostCID string) map[string]any {
	g := s.registry()
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hosts[hostCID]
}

func (s *Server) regSetHost(hostCID string, room map[string]any) {
	g := s.registry()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hosts[hostCID] = room
}

// regDropHost removes a host's room and returns its guests' coach
// ids (their seats are cleared).
func (s *Server) regDropHost(hostCID string) []string {
	g := s.registry()
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.hosts, hostCID)
	var guests []string
	for guest, host := range g.seat {
		if host == hostCID {
			guests = append(guests, guest)
			delete(g.seat, guest)
		}
	}
	return guests
}

func (s *Server) regSeat(guestCID string) (string, bool) {
	g := s.registry()
	g.mu.Lock()
	defer g.mu.Unlock()
	host, ok := g.seat[guestCID]
	return host, ok
}

func (s *Server) regSetSeat(guestCID, hostCID string) {
	g := s.registry()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seat[guestCID] = hostCID
}

func (s *Server) regClearSeat(guestCID string) {
	g := s.registry()
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.seat, guestCID)
}

// regSnapshot copies the host->room map (rooms themselves are
// shared, as everywhere else in this registry).
func (s *Server) regSnapshot() map[string]map[string]any {
	g := s.registry()
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]map[string]any, len(g.hosts))
	for k, v := range g.hosts {
		out[k] = v
	}
	return out
}

func (s *Server) cancelRoomTimer(rid int, uid int64) {
	t := s.timers()
	t.mu.Lock()
	defer t.mu.Unlock()
	key := timerKey(rid, uid)
	if tm, ok := t.m[key]; ok {
		tm.Stop()
		delete(t.m, key)
	}
}

func roomIDOf(c *gin.Context) int {
	rid, err := strconv.Atoi(c.Param("rid"))
	if err != nil || rid <= 0 {
		return 1
	}
	return rid
}

// adoptRoom registers a room loaded from a user's persisted state:
// the room's own host field decides the role — I host it, or I sit
// in its guest seat.
func (s *Server) adoptRoom(st game.State, room map[string]any) {
	myCID, _ := myCoachID(st)
	host := game.Str(room["host"])
	switch {
	case host == "" || host == myCID:
		s.regSetHost(myCID, room)
		if guest := roomGuestCID(room); guest != "" {
			s.regSetSeat(guest, myCID)
		}
	default:
		s.regSetHost(host, room)
		s.regSetSeat(myCID, host)
	}
}

// roomGuestCID returns the coach id seated in the room's right
// side, or "".
func roomGuestCID(room map[string]any) string {
	right := game.Map(game.Map(room["side"])["right"])
	players := game.List(right["players"])
	if len(players) == 0 {
		return ""
	}
	return game.Str(game.Map(players[0])["id"])
}

// roomCapacity is how many coaches sit on each playing side
// (1 for 1v1 rooms, 2 for 2v2, 3 for 3v3).
func roomCapacity(room map[string]any) int {
	if n := game.Int(room["slotsize"]); n > 1 {
		return n
	}
	// The ladder screens may encode the mode only in the
	// room type; the seat count follows it.
	t := game.Str(room["type"])
	if strings.Contains(t, "3v3") {
		return 3
	}
	if strings.Contains(t, "2v2") {
		return 2
	}
	return 1
}

// roomEntrySide finds which side list holds a coach's entry
// ("", -1 when not seated).
func roomEntrySide(room map[string]any, cid string) (string, int) {
	side := game.Map(room["side"])
	for _, n := range []string{"left", "right", "center"} {
		for i, pl := range game.List(game.Map(side[n])["players"]) {
			if game.Str(game.Map(pl)["id"]) == cid {
				return n, i
			}
		}
	}
	return "", -1
}

// roomCoachIDs lists every seated coach id, left side first.
func roomCoachIDs(room map[string]any) []string {
	side := game.Map(room["side"])
	var out []string
	for _, n := range []string{"left", "right"} {
		for _, pl := range game.List(game.Map(side[n])["players"]) {
			if id := game.Str(game.Map(pl)["id"]); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

// roomFullSeats reports whether both playing sides are at
// capacity (only then may a multi room start its match).
func roomFullSeats(room map[string]any) bool {
	cap := roomCapacity(room)
	side := game.Map(room["side"])
	return len(game.List(game.Map(side["left"])["players"])) >= cap &&
		len(game.List(game.Map(side["right"])["players"])) >= cap
}

// removeRoomEntry drops one coach's entry from whichever
// side holds it.
func removeRoomEntry(room map[string]any, cid string) bool {
	side := game.Map(room["side"])
	for _, n := range []string{"left", "right", "center"} {
		b := game.Map(side[n])
		players := game.List(b["players"])
		for i, pl := range players {
			if game.Str(game.Map(pl)["id"]) == cid {
				rest := append([]any{}, players[:i]...)
				b["players"] = append(rest, players[i+1:]...)
				return true
			}
		}
	}
	return false
}

// refreshSideLeader re-points a side block's leader fields
// (authTeamId, squad) at its current first entry after the
// membership changed.
func refreshSideLeader(b map[string]any) {
	players := game.List(b["players"])
	if len(players) == 0 {
		b["ready"] = false
		delete(b, "squad")
		return
	}
	lead := game.Map(players[0])
	b["authTeamId"] = game.Int(lead["teamid"])
	if lead["squad"] != nil {
		b["squad"] = lead["squad"]
	} else {
		delete(b, "squad")
	}
}

// roomInvitedHas reports whether a coach holds a live
// invitation marker on the room (single "invited" field or
// the multi-invite list used by multi-seat rooms).
func roomInvitedHas(room map[string]any, cid string) bool {
	if game.Str(room["invited"]) == cid {
		return true
	}
	for _, v := range game.List(room["invited_list"]) {
		if game.Str(v) == cid {
			return true
		}
	}
	return false
}

// roomInvitedAdd records an invitation marker for a coach.
func roomInvitedAdd(room map[string]any, cid string) {
	had := roomInvitedHas(room, cid)
	room["invited"] = cid
	if had {
		return
	}
	room["invited_list"] = append(game.List(room["invited_list"]), cid)
}

// roomInvitedRemove drops a coach's invitation markers.
func roomInvitedRemove(room map[string]any, cid string) {
	if game.Str(room["invited"]) == cid {
		delete(room, "invited")
	}
	list := game.List(room["invited_list"])
	out := list[:0]
	for _, v := range list {
		if game.Str(v) != cid {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		delete(room, "invited_list")
	} else {
		room["invited_list"] = out
	}
}

// loadRoom resolves the room the caller is part of. A guest seat
// wins over their own hosted room: the invitation is the most
// recent room event, the invitee's client displays the host's room
// after the push, and its follow-up reads/actions must land there
// (host-first resolution sent the invitee's GET/ready to their own
// stale room — smoke 2026-11-13).
func (s *Server) loadRoom(st game.State, user *game.User, rid int, defType string) map[string]any {
	myCID, _ := myCoachID(st)
	if hostCID, ok := s.regSeat(myCID); ok {
		if room := s.regHostRoom(hostCID); room != nil {
			return room
		}
		s.regClearSeat(myCID)
	}
	if room := s.regHostRoom(myCID); room != nil {
		return room
	}
	if room := game.Map(st["active_room"]); room != nil {
		s.adoptRoom(st, room)
		return room
	}
	roomType := defType
	if roomType == "" {
		roomType = "ladder_pvp_1v1"
	}
	room := s.d.Roster.MakeRoomResource(st, user, rid, roomType, 1, "room", game.RoomOpts{})
	s.regSetHost(myCID, room)
	return room
}

// loadOwnRoom resolves only the caller's OWN room (never a host's
// room they are guest in): matchmaking queues and invites act on
// the caller's own room.
func (s *Server) loadOwnRoom(st game.State, user *game.User, rid int, defType string) map[string]any {
	myCID, _ := myCoachID(st)
	if room := s.regHostRoom(myCID); room != nil {
		return room
	}
	if room := game.Map(st["active_room"]); room != nil {
		if host := game.Str(room["host"]); host == "" || host == myCID {
			s.adoptRoom(st, room)
			return room
		}
	}
	roomType := defType
	if roomType == "" {
		roomType = "ladder_pvp_1v1"
	}
	room := s.d.Roster.MakeRoomResource(st, user, rid, roomType, 1, "room", game.RoomOpts{})
	s.regSetHost(myCID, room)
	return room
}

// roomFamily buckets room types for create-time decisions: ladder
// (ranked queue) vs everything else (friendly/exhibition rooms).
func roomFamily(roomType string) string {
	if strings.Contains(roomType, "ladder") {
		return "ladder"
	}
	return "friendly"
}

// unseatGuest removes a guest from their host's room (right side
// emptied, invitation cleared), persists the host's room and
// refreshes the host's client.
// syncRoomMembers rebuilds the room's flat member list and
// the per-side role head from the side player lists.
func syncRoomMembers(room map[string]any) {
	side := game.Map(room["side"])
	left := game.List(game.Map(side["left"])["players"])
	right := game.List(game.Map(side["right"])["players"])
	members := append([]any{}, left...)
	members = append(members, right...)
	room["members"] = members
	if roles := game.Map(room["roles"]); roles != nil {
		for name, players := range map[string][]any{"left": left, "right": right} {
			if r := game.Map(roles[name]); r != nil {
				if len(players) > 0 {
					r["0"] = []any{game.Str(game.Map(players[0])["id"])}
				} else {
					r["0"] = []any{}
				}
			}
		}
	}
}

func (s *Server) regRoom(room map[string]any) {
	if host := game.Str(room["host"]); host != "" {
		s.regSetHost(host, room)
	}
}

// saveRoomForHost persists the room into the host user's
// own state (their active_room), so the host's reads resolve
// to the same room body the registry holds.
func (s *Server) saveRoomForHost(ctx context.Context, hostCID string, room map[string]any) {
	hu := s.userByCoachID(ctx, hostCID)
	if hu == nil {
		return
	}
	hst, err := game.LoadState(ctx, s.d.Store, hu.ID)
	if err != nil {
		return
	}
	hst["active_room"] = room
	_ = game.SaveState(ctx, s.d.Store, hu.ID, hst)
}

// userByCoachID finds the account that owns a coach id.
func (s *Server) userByCoachID(ctx context.Context, cid string) *game.User {
	if cid == "" {
		return nil
	}
	users, err := game.AllUsers(ctx, s.d.Store)
	if err != nil {
		return nil
	}
	for _, u := range users {
		st, err := game.LoadState(ctx, s.d.Store, u.ID)
		if err != nil {
			continue
		}
		for _, cv := range game.List(st["coaches"]) {
			if game.Str(game.Map(cv)["_id"]) == cid {
				return u
			}
		}
	}
	return nil
}

// coachRoomEntry builds the room member entry for a coach
// owned by another account — the same shape the host's own
// entry has in MakeRoomResource (the bot entry on the right
// side is the display precedent), with the session profile
// fields the client engine requires.
func (s *Server) coachRoomEntry(ctx context.Context, cid string) map[string]any {
	u := s.userByCoachID(ctx, cid)
	if u == nil {
		return nil
	}
	st, err := game.LoadState(ctx, s.d.Store, u.ID)
	if err != nil {
		return nil
	}
	slot := -1
	for _, cv := range game.List(st["coaches"]) {
		coach := game.Map(cv)
		if coach != nil && game.Str(coach["_id"]) == cid {
			slot = game.Int(coach["slot"])
			break
		}
	}
	if slot < 0 {
		return nil
	}
	me := s.d.Roster.CoachMe(st, slot, u)
	tid := game.Int(me["teamid"])
	if tid == 0 {
		tid = 1
	}
	clevel := game.Int(game.Map(me["levelinfo"])["level"])
	rk := game.RankedState(st, "ranked")
	elo := game.Int(rk["elo"])
	if rk["elo"] == nil {
		elo = 1000
	}
	div, _ := game.DivisionForElo(elo)
	rawTeam := game.TeamOf(st, slot)
	if rawTeam == nil {
		rawTeam = s.d.Roster.RosterTeam(tid)
	}
	team := s.d.Roster.OwnedTeam(rawTeam)
	summary := s.d.Roster.TeamSummaryOf(team, tid)
	entry := map[string]any{
		"id": cid, "_id": cid, "name": game.Str(me["name"]), "teamid": tid,
		"level": clevel, "levelinfo": me["levelinfo"],
		"captain": true, "master": false, "onmatch": false, "ready": false,
		"elo": elo, "division": "DIVISION_" + div,
		"clubid": "", "clubname": "",
		"summary": summary,
		"squad":   team["squad"],
		// The entry's team is the same identity stub the
		// host's own entry carries (MakeRoomResource):
		// teamid + summary. Embedding the full roster here
		// added ~13 KB per guest TWICE (side list + members)
		// and pushed room resources past the Messenger's
		// 65,000-byte frame limit, so room updates were
		// silently dropped and the peer's room screen went
		// stale (user report 2026-11-14). Nothing reads the
		// entry's roster: match building takes the side
		// leader's team from their state, entryUserObj does
		// not touch entry["team"].
		"team": map[string]any{"teamid": tid, "summary": summary},
	}
	for k, v := range game.SessionProfileFields() {
		entry[k] = v
	}
	if entry["levelinfo"] == nil {
		entry["levelinfo"] = map[string]any{"level": clevel, "exp": 0, "maxexp": 100}
	}
	// Club identity on the entry: club rooms read it to
	// place the coach on their club's side (RoomProxy
	// compares entry clubids against the sides' clubs), and
	// the fields exist on every entry already — they were
	// just always empty.
	if club, _ := game.ClubOfCoach(ctx, s.d.Store, cid); club != nil {
		entry["clubid"] = club.ID
		entry["clubname"] = club.Name
	}
	return entry
}

func (s *Server) unseatGuest(ctx context.Context, guestCID string) {
	hostCID, ok := s.regSeat(guestCID)
	if !ok {
		return
	}
	s.regClearSeat(guestCID)
	room := s.regHostRoom(hostCID)
	if room == nil {
		return
	}
	s.applyUnseat(ctx, hostCID, room, guestCID)
}

// applyUnseat removes ONE guest's entry from the host's room
// (multi-seat rooms keep their other members), refreshes the
// affected side's leader fields, persists the room for the
// host and pushes it, so the host's room screen drops the
// departed coach right away.
func (s *Server) applyUnseat(ctx context.Context, hostCID string, room map[string]any, guestCID string) {
	sideName, _ := roomEntrySide(room, guestCID)
	removeRoomEntry(room, guestCID)
	roomInvitedRemove(room, guestCID)
	if sideName == "left" || sideName == "right" {
		refreshSideLeader(game.Map(game.Map(room["side"])[sideName]))
	}
	syncRoomMembers(room)
	s.saveRoomForHost(ctx, hostCID, room)
	uri := "/room/" + strconv.Itoa(game.Int(room["id"]))
	game.MessengerPush(uri, pushBody(uri, room), hostCID)
}

// promoteGuest hands a lobby room to a remaining member when
// the host leaves and returns the successor's coach id (""
// when the room has no successor). The successor is the
// first remaining member, left side first; a successor from
// the right moves to the left when the left side is empty.
// The updated room is pushed to every remaining member.
func (s *Server) promoteGuest(ctx context.Context, room map[string]any, leaverCID string) string {
	removeRoomEntry(room, leaverCID)
	roomInvitedRemove(room, leaverCID)
	side := game.Map(room["side"])
	left := game.Map(side["left"])
	right := game.Map(side["right"])
	successor := ""
	if players := game.List(left["players"]); len(players) > 0 {
		successor = game.Str(game.Map(players[0])["id"])
	} else if players := game.List(right["players"]); len(players) > 0 {
		successor = game.Str(game.Map(players[0])["id"])
		// Move the successor's block to the left side.
		for _, k := range []string{"players", "authTeamId", "squad", "team"} {
			if v, ok := right[k]; ok {
				left[k] = v
			} else {
				delete(left, k)
			}
		}
		right["players"] = []any{}
		right["ready"] = false
		delete(right, "squad")
		delete(right, "team")
	}
	if successor == "" {
		return ""
	}
	refreshSideLeader(left)
	refreshSideLeader(right)
	room["host"] = successor
	syncRoomMembers(room)
	s.regSetHost(successor, room)
	s.saveRoomForHost(ctx, successor, room)
	uri := "/room/" + strconv.Itoa(game.Int(room["id"]))
	for _, cid := range roomCoachIDs(room) {
		game.MessengerPush(uri, pushBody(uri, room), cid)
	}
	return successor
}

// persistRoom saves the room into the caller's state and the
// registry; when the caller is a guest in someone else's room
// it also persists the room for the host and pushes it to
// them, so both clients render the same room.
func (s *Server) persistRoom(ctx context.Context, st game.State, user *game.User, room map[string]any) {
	st["active_room"] = room
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	s.regRoom(room)
	myCID, _ := myCoachID(st)
	if hostCID, seated := s.regSeat(myCID); seated && hostCID != myCID {
		s.saveRoomForHost(ctx, hostCID, room)
		uri := "/room/" + strconv.Itoa(game.Int(room["id"]))
		game.MessengerPush(uri, pushBody(uri, room), hostCID)
	}
}

// dissolveRoom tears down the room the caller is in (hosted
// or seated): seated guests get a null-room push and their
// persisted mirrors are cleared, so the dead room cannot
// resurrect on their next read.
func (s *Server) dissolveRoom(ctx context.Context, st game.State, user *game.User) {
	myCID, _ := myCoachID(st)
	rid := 1
	if room := game.Map(st["active_room"]); room != nil && game.Int(room["id"]) > 0 {
		rid = game.Int(room["id"])
	}
	if hostCID, seated := s.regSeat(myCID); seated && hostCID != myCID {
		s.regClearSeat(myCID)
		if hr := s.regHostRoom(hostCID); hr != nil {
			s.applyUnseat(ctx, hostCID, hr, myCID)
		}
		delete(st, "active_room")
		_ = game.SaveState(ctx, s.d.Store, user.ID, st)
		return
	}
	for _, guest := range s.regDropHost(myCID) {
		game.MessengerPush("/room/"+strconv.Itoa(rid), nil, guest)
		if gu := s.userByCoachID(ctx, guest); gu != nil {
			if gst, err := game.LoadState(ctx, s.d.Store, gu.ID); err == nil {
				if gm := game.Map(gst["active_room"]); gm != nil &&
					game.Str(gm["host"]) == myCID {
					delete(gst, "active_room")
					_ = game.SaveState(ctx, s.d.Store, gu.ID, gst)
				}
			}
		}
	}
	delete(st, "active_room")
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
}

// callerRoom resolves the room the caller is in (hosted or
// seated) without creating one: registry seat, registry host,
// then the persisted mirror.
func (s *Server) callerRoom(st game.State) map[string]any {
	myCID, _ := myCoachID(st)
	if hostCID, ok := s.regSeat(myCID); ok {
		if room := s.regHostRoom(hostCID); room != nil {
			return room
		}
	}
	if room := s.regHostRoom(myCID); room != nil {
		return room
	}
	return game.Map(st["active_room"])
}

// roomMatchStage reports the stage of the match a matched
// room produced ("" when unknown).
func (s *Server) roomMatchStage(st game.State, room map[string]any) string {
	mid := ""
	for _, rv := range game.List(game.Map(room["matchmaking"])["resources"]) {
		if uri := game.Str(game.Map(rv)["uri"]); strings.HasPrefix(uri, "/match/") {
			mid = strings.TrimPrefix(uri, "/match/")
		}
	}
	if mid == "" {
		if m := game.Map(st["match"]); m != nil {
			mid = game.Str(m["id"])
		}
	}
	if mid == "" {
		return ""
	}
	if lm, ok := s.liveMatch(mid); ok {
		return game.Str(lm["stage"])
	}
	if m := game.Map(st["match"]); m != nil && game.Str(m["id"]) == mid {
		return game.Str(m["stage"])
	}
	return ""
}

// expireDeadMatchRoom (bak-2205) dissolves the caller's room
// when its stage is "matched" and the match it produced has
// already reached "playing"/"closed" — a leftover shell of a
// finished match (ghost room after a crash).
func (s *Server) expireDeadMatchRoom(ctx context.Context, st game.State, user *game.User) {
	room := s.callerRoom(st)
	if room == nil || game.Str(room["stage"]) != "matched" {
		return
	}
	if stage := s.roomMatchStage(st, room); stage == "playing" || stage == "closed" {
		s.dissolveRoom(ctx, st, user)
	}
}

// dissolveMatchedRoom (bak-2235) dissolves the caller's own
// matched room regardless of the match's stage — creating a
// room or inviting is a fresh start and must not trip over
// the shell of the previous match.
func (s *Server) dissolveMatchedRoom(ctx context.Context, st game.State, user *game.User) {
	room := s.callerRoom(st)
	if room == nil || game.Str(room["stage"]) != "matched" {
		return
	}
	s.dissolveRoom(ctx, st, user)
}

// roomCreate handles POST /room and POST /exhibition/room:
// a fresh room of the route's type. The body may carry an
// explicit type / slotsize (the 2v2/3v3 ladder screens do).
func (s *Server) roomCreate(defType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		st, user, ok := s.loadState(c)
		if !ok {
			return
		}
		if contractBlock(c, st) {
			return
		}
		ctx := c.Request.Context()
		s.cancelRoomTimer(1, user.ID)
		s.dissolveMatchedRoom(ctx, st, user)
		var req map[string]any
		_ = c.ShouldBindJSON(&req)
		roomType := defType
		if t := game.Str(req["type"]); t != "" {
			roomType = t
		}
		slotsize := 1
		if n := game.Int(req["slotsize"]); n > 1 {
			slotsize = n
		}
		myCID, _ := myCoachID(st)
		if hostCID, seated := s.regSeat(myCID); seated && hostCID != myCID {
			s.regClearSeat(myCID)
			if hr := s.regHostRoom(hostCID); hr != nil {
				s.applyUnseat(ctx, hostCID, hr, myCID)
			}
		}
		s.regDropHost(myCID)
		room := s.d.Roster.MakeRoomResource(st, user, 1, roomType, slotsize, "room", game.RoomOpts{})
		if isClubRoomType(roomType) {
			// A club room's sides carry their club's block
			// ({clubid, clublv, clubname} + side-level clubid)
			// — the client decides which side is whose club
			// from these (RoomProxy.clubRoomSide).
			s.decorateRoomSideClub(ctx, room, "left")
		}
		s.regSetHost(myCID, room)
		s.persistRoom(ctx, st, user, room)
		writeJSON(c, map[string]any{"result": true,
			"resources": []any{map[string]any{"uri": "/room/1", "body": room}}})
	}
}

// roomGet handles GET /room/:rid: the caller's current room
// (a host's room when seated), with dead matched-room shells
// expired first.
func (s *Server) roomGet(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	rid := roomIDOf(c)
	ctx := c.Request.Context()
	s.expireDeadMatchRoom(ctx, st, user)
	room := s.loadRoom(st, user, rid, "ladder_pvp_1v1")
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/room/" + strconv.Itoa(rid), "body": room}}})
}

func (s *Server) roomLeave(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	rid := roomIDOf(c)
	s.cancelRoomTimer(rid, user.ID)
	ctx := c.Request.Context()
	myCID, _ := myCoachID(st)
	hostCID, seated := s.regSeat(myCID)
	if !seated {
		// The seat map is in-memory: after a server restart a
		// seated guest has no entry, and the room mirror in
		// their own state is the only record of whose room they
		// sit in. Resolve the host from it — otherwise the
		// leave silently does nothing and the host keeps
		// seeing the departed coach in the room (user report
		// 2026-11-14).
		if mirror := game.Map(st["active_room"]); mirror != nil {
			if h := game.Str(mirror["host"]); h != "" && h != myCID {
				hostCID, seated = h, true
			}
		}
	}
	if seated && hostCID != myCID {
		// Guest leaving the host's room: free the seat and
		// refresh the host (the host's client is updated
		// inside applyUnseat).
		s.regClearSeat(myCID)
		room := s.regHostRoom(hostCID)
		if room == nil {
			if hu := s.userByCoachID(ctx, hostCID); hu != nil {
				if hst, err := game.LoadState(ctx, s.d.Store, hu.ID); err == nil {
					if hr := game.Map(hst["active_room"]); hr != nil &&
						roomGuestCID(hr) == myCID {
						room = hr
						s.regSetHost(hostCID, room)
					}
				}
			}
		}
		if room != nil {
			s.applyUnseat(ctx, hostCID, room, myCID)
		}
	} else {
		// Host (or solo) leaving dissolves the room; seated guests
		// get a null room push so their clients exit it too, and a
		// still-pending invitation dies with the room.
		room := s.regHostRoom(myCID)
		stage := ""
		if room != nil {
			stage = game.Str(room["stage"])
			invited := map[string]bool{}
			if inv := game.Str(room["invited"]); inv != "" {
				invited[inv] = true
			}
			for _, v := range game.List(room["invited_list"]) {
				if cid := game.Str(v); cid != "" {
					invited[cid] = true
				}
			}
			for inv := range invited {
				s.clearPendingInvite(ctx, inv)
				s.pushNotes(ctx, inv) // the letter dies with the room
			}
		}
		guests := s.regDropHost(myCID)
		promoted := ""
		if room != nil && stage == "room" && len(guests) > 0 {
			// Host transfer: a remaining member takes over
			// the room instead of it dissolving (user logic
			// 2026-11-14: the remaining coach becomes chủ
			// phòng, so they can invite someone else or
			// play the CPU from the same room).
			promoted = s.promoteGuest(ctx, room, myCID)
		}
		for _, guest := range guests {
			if guest == promoted {
				continue
			}
			game.MessengerPush("/room/"+strconv.Itoa(rid), nil, guest)
			if stage != "matched" {
				// The guest's persisted room mirror would
				// resurrect the dissolved room on their next
				// room read (adoptRoom re-registers it) —
				// clear it so the leave is final (user
				// report 2026-11-14).
				if gu := s.userByCoachID(ctx, guest); gu != nil {
					if gst, err := game.LoadState(ctx, s.d.Store, gu.ID); err == nil {
						if gm := game.Map(gst["active_room"]); gm != nil &&
							game.Str(gm["host"]) == myCID {
							delete(gst, "active_room")
							_ = game.SaveState(ctx, s.d.Store, gu.ID, gst)
						}
					}
				}
			}
		}
	}
	prev := game.Map(st["active_room"])
	delete(st, "active_room")
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	roomType := "ladder_pvp_1v1"
	slotsize := 1
	if prev != nil {
		if t := game.Str(prev["type"]); t != "" {
			roomType = t
		}
		if n := game.Int(prev["slotsize"]); n > 0 {
			slotsize = n
		}
	}
	_ = roomType
	_ = slotsize
	_ = user
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/room/" + strconv.Itoa(rid), "body": nil}}})
}

func (s *Server) roomMatchmaking(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	rid := roomIDOf(c)
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	room := s.loadOwnRoom(st, user, rid, "")
	s.cancelRoomTimer(rid, user.ID)
	rawQuery := c.Request.URL.RawQuery
	isDelete := game.Str(req["_method"]) == "DELETE" || req["cancel"] != nil ||
		strings.Contains(rawQuery, "_method=DELETE") || strings.Contains(rawQuery, "cancel")
	if isDelete {
		room["stage"] = "room"
		delete(room, "queue_since")
		room["matchmaking"] = map[string]any{"multiplematch": false, "resources": []any{}}
		st["active_room"] = room
		s.regRoom(room)
		_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
		writeJSON(c, map[string]any{"result": true,
			"resources": []any{map[string]any{"uri": "/room/" + strconv.Itoa(rid), "body": room}}})
		return
	}
	// Joining the queue is a match entry: an out-of-contract squad
	// may not queue (a cancel above always goes through).
	if contractBlock(c, st) {
		return
	}
	room["stage"] = "searching"
	room["queue_since"] = time.Now().Unix()
	room["matchmaking"] = map[string]any{"multiplematch": false, "resources": []any{}}
	st["active_room"] = room
	s.regRoom(room)
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	uid := user.ID
	coachID, _ := myCoachID(st)
	// Human-vs-human queue pairing: another coach may already
	// be searching the same queue — pair with them right away
	// instead of both waiting out the bot window (user report
	// 2026-11-14: two accounts queued and never met).
	if updated := s.tryPairQueue(c.Request.Context(), coachID); updated != nil {
		writeJSON(c, map[string]any{"result": true, "stage": "matched",
			"matchmaking": updated["matchmaking"],
			"resources":   []any{map[string]any{"uri": "/room/" + strconv.Itoa(rid), "body": updated}}})
		return
	}
	t := s.timers()
	t.mu.Lock()
	// Owner decision 2026-11-13: a real player's PvP queue waits
	// the full search window before the bot is paired (was 2s).
	// Tunable via the gameplay setting bot_match_delay_sec.
	delay := game.CfgI("bot_match_delay_sec", 30)
	if delay < 1 {
		delay = 1
	}
	t.m[timerKey(rid, uid)] = time.AfterFunc(time.Duration(delay)*time.Second, func() { s.completeMatchmaking(rid, uid, coachID) })
	t.mu.Unlock()
	writeJSON(c, map[string]any{"result": true, "stage": "searching",
		"matchmaking": map[string]any{"multiplematch": false, "resources": []any{}},
		"resources":   []any{map[string]any{"uri": "/room/" + strconv.Itoa(rid), "body": room}}})
}

// pushSlim mirrors fx.notify_messenger_push's >55KB slimming
// (strip staticdata from teams under side/left/right).
func pushBody(uri string, body any) any {
	b, err := json.Marshal(map[string]any{"uri": uri, "body": body})
	if err != nil || len(b) <= 55000 {
		return body
	}
	m, isMap := body.(map[string]any)
	if !isMap {
		return body
	}
	cp := map[string]any{}
	for k, v := range m {
		cp[k] = v
	}
	for _, key := range []string{"side", "left", "right"} {
		sideMap := game.Map(cp[key])
		if sideMap == nil {
			continue
		}
		sideCopy := map[string]any{}
		for k, v := range sideMap {
			sideCopy[k] = v
		}
		for subK, subV := range sideMap {
			if sub := game.Map(subV); sub != nil && sub["team"] != nil {
				subCopy := map[string]any{}
				for k, v := range sub {
					subCopy[k] = v
				}
				subCopy["team"] = game.SlimTeam(game.Map(sub["team"]))
				sideCopy[subK] = subCopy
			}
		}
		cp[key] = sideCopy
	}
	// Entries seated before the coachRoomEntry stub fix carry
	// a full roster in their "team"; stub those in the push
	// copy too (teamid + summary, as slimRoomPlayer does), in
	// the side lists and in "members". Without this, a room
	// with a legacy fat entry still exceeds the Messenger's
	// 65,000-byte frame limit and the push is dropped.
	stubEntry := func(ev any) any {
		e := game.Map(ev)
		if e == nil {
			return ev
		}
		t := game.Map(e["team"])
		if t == nil || t["players"] == nil {
			return ev
		}
		ec := map[string]any{}
		for k, v := range e {
			ec[k] = v
		}
		ec["team"] = map[string]any{"teamid": t["teamid"], "summary": t["summary"]}
		return ec
	}
	if sideMap := game.Map(cp["side"]); sideMap != nil {
		sc := map[string]any{}
		for k, v := range sideMap {
			sc[k] = v
		}
		for _, n := range []string{"left", "right", "center"} {
			blk := game.Map(sideMap[n])
			if blk == nil {
				continue
			}
			bc := map[string]any{}
			for k, v := range blk {
				bc[k] = v
			}
			if pls := game.List(blk["players"]); pls != nil {
				out := make([]any, len(pls))
				for i, ev := range pls {
					out[i] = stubEntry(ev)
				}
				bc["players"] = out
			}
			sc[n] = bc
		}
		cp["side"] = sc
	}
	if ms := game.List(cp["members"]); ms != nil {
		out := make([]any, len(ms))
		for i, ev := range ms {
			out[i] = stubEntry(ev)
		}
		cp["members"] = out
	}
	return cp
}

func (s *Server) completeMatchmaking(rid int, uid int64, coachID string) {
	ctx := context.Background()
	user, err := game.UserByID(ctx, s.d.Store, uid)
	if err != nil || user == nil {
		return
	}
	st, err := game.LoadState(ctx, s.d.Store, uid)
	if err != nil {
		return
	}
	queuerCID, _ := myCoachID(st)
	room := s.regHostRoom(queuerCID)
	if room == nil {
		room = game.Map(st["active_room"])
	}
	if room == nil || game.Str(room["stage"]) != "searching" {
		return
	}
	// A human may have queued during the window: pair with
	// them instead of the bot (the pairing cancels this
	// timer's counterpart and completes both queues).
	if s.tryPairQueue(ctx, coachID) != nil {
		return
	}
	if roomCapacity(room) > 1 {
		// Multi-seat queues never fall back to a bot match:
		// a 2v2/3v3 match needs every seat filled by a human
		// (an empty coach seat has no bot stand-in — the bot
		// is a whole team, not a coach). Re-arm the search
		// window and keep waiting for the assembly.
		delay := game.CfgI("bot_match_delay_sec", 30)
		if delay < 1 {
			delay = 1
		}
		t := s.timers()
		t.mu.Lock()
		t.m[timerKey(rid, uid)] = time.AfterFunc(time.Duration(delay)*time.Second, func() {
			s.completeMatchmaking(rid, uid, coachID)
		})
		t.mu.Unlock()
		return
	}
	match, updated := s.d.Roster.BuildRoomMatch(st, user, room, s.friendLookup(ctx))
	if match == nil {
		return
	}
	s.regMatch(game.Str(match["id"]), uid, match)
	matchRes := map[string]any{"resource": "/match/" + game.Str(match["id"]),
		"uri": "/match/" + game.Str(match["id"]), "body": match}
	updated["matchmaking"] = map[string]any{"multiplematch": false, "resources": []any{matchRes}}
	updated["stage"] = "matched"
	st["active_room"] = updated
	s.regRoom(updated)
	_ = game.SaveState(ctx, s.d.Store, uid, st)

	s.pushQueueCompletion(updated, match, rid, coachID)
	s.redeliverQueueCompletion(updated, match, rid, []string{coachID})
	mid := game.Str(match["id"])
	time.AfterFunc(1500*time.Millisecond, func() { s.bridgeRoomMatch(rid, uid, coachID, mid) })
}


// queuePairMu serializes queue pairing: two searchers whose
// handlers fire at the same moment must not pair twice.
var queuePairMu sync.Mutex

// tryPairQueue pairs the caller's searching room with the
// longest-waiting other searching room of the same type, if
// any, and completes both queues with a human match. Returns
// the surviving (host) room when paired, nil otherwise.
func (s *Server) tryPairQueue(ctx context.Context, selfCID string) map[string]any {
	queuePairMu.Lock()
	defer queuePairMu.Unlock()
	self := s.regHostRoom(selfCID)
	if self == nil || game.Str(self["stage"]) != "searching" {
		return nil
	}
	if roomCapacity(self) > 1 {
		return s.tryAssembleMultiQueue(ctx, self)
	}
	if roomGuestCID(self) != "" {
		return nil
	}
	var candCID string
	var cand map[string]any
	var candSince int64
	for hostCID, room := range s.regSnapshot() {
		if hostCID == selfCID || room == nil {
			continue
		}
		if game.Str(room["stage"]) != "searching" ||
			roomFamily(game.Str(room["type"])) != roomFamily(game.Str(self["type"])) ||
			roomGuestCID(room) != "" {
			continue
		}
		since := int64(game.Int(room["queue_since"]))
		if cand == nil || since < candSince {
			candCID, cand, candSince = hostCID, room, since
		}
	}
	if cand == nil {
		return nil
	}
	// The longer-waiting room hosts the match (its queue has
	// been running longer; the other coach becomes the guest).
	hostCID, guestCID := selfCID, candCID
	if int64(game.Int(self["queue_since"])) > candSince {
		hostCID, guestCID = candCID, selfCID
	}
	return s.pairQueueRooms(ctx, hostCID, guestCID)
}

// tryAssembleMultiQueue fills a multi-seat queue: when the
// searching multi rooms of the same family and capacity
// together hold enough coaches for a full match (2x capacity),
// the longest-waiting room anchors and the other rooms'
// members move into it — parties stay together when they fit
// a side, otherwise they split fill-left-first. Members who
// do not fit stay queued in their own rooms. The match is
// built from the anchor host's state like a room build, and
// every participant is mirrored, pushed and bridged.
func (s *Server) tryAssembleMultiQueue(ctx context.Context, self map[string]any) map[string]any {
	capN := roomCapacity(self)
	need := 2 * capN
	type candRoom struct {
		hostCID string
		room    map[string]any
		since   int64
	}
	var cands []candRoom
	total := 0
	for hostCID, room := range s.regSnapshot() {
		if room == nil || game.Str(room["stage"]) != "searching" ||
			roomCapacity(room) != capN ||
			roomFamily(game.Str(room["type"])) != roomFamily(game.Str(self["type"])) {
			continue
		}
		cands = append(cands, candRoom{hostCID, room, int64(game.Int(room["queue_since"]))})
		total += len(roomCoachIDs(room))
	}
	if total < need {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].since < cands[j].since })
	anchor := cands[0]
	anchorRoom := anchor.room
	anchorSide := game.Map(anchorRoom["side"])
	free := func(name string) int {
		return capN - len(game.List(game.Map(anchorSide[name])["players"]))
	}
	rid := game.Int(anchorRoom["id"])
	if rid == 0 {
		rid = 1
	}
	// Move members group by group until the anchor is full.
	for _, src := range cands[1:] {
		if free("left") == 0 && free("right") == 0 {
			break
		}
		srcSide := game.Map(src.room["side"])
		var group []any
		for _, n := range []string{"left", "right"} {
			group = append(group, game.List(game.Map(srcSide[n])["players"])...)
		}
		var moved []any
		for _, ev := range group {
			if free("left") == 0 && free("right") == 0 {
				break
			}
			entry := game.Map(ev)
			cid := game.Str(entry["id"])
			if cid == "" {
				continue
			}
			to := ""
			switch {
			case len(group)-len(moved) <= free("left"):
				to = "left"
			case len(group)-len(moved) <= free("right"):
				to = "right"
			case free("left") > 0:
				to = "left"
			default:
				to = "right"
			}
			dst := game.Map(anchorSide[to])
			dst["players"] = append(game.List(dst["players"]), entry)
			removeRoomEntry(src.room, cid)
			moved = append(moved, ev)
			if u := s.userByCoachID(ctx, cid); u != nil {
				s.cancelRoomTimer(rid, u.ID)
			}
			if cid == src.hostCID {
				s.regDropHost(cid)
			} else {
				s.regClearSeat(cid)
			}
			s.regSetSeat(cid, anchor.hostCID)
		}
		if len(moved) > 0 {
			syncRoomMembers(src.room)
			if len(roomCoachIDs(src.room)) > 0 {
				s.saveRoomForHost(ctx, src.hostCID, src.room)
			}
		}
	}
	if !roomFullSeats(anchorRoom) {
		return nil
	}
	anchorUser := s.userByCoachID(ctx, anchor.hostCID)
	if anchorUser == nil {
		return nil
	}
	anchorSt, err := game.LoadState(ctx, s.d.Store, anchorUser.ID)
	if err != nil {
		return nil
	}
	leftEntries := game.List(game.Map(anchorSide["left"])["players"])
	syncRoomMembers(anchorRoom)
	for _, cid := range roomCoachIDs(anchorRoom) {
		if u := s.userByCoachID(ctx, cid); u != nil {
			s.cancelRoomTimer(rid, u.ID)
		}
	}
	match, updated := s.d.Roster.BuildRoomMatch(anchorSt, anchorUser, anchorRoom, s.friendLookup(ctx))
	if match == nil {
		return nil
	}
	// BuildRoomMatch's fresh room keeps only the host on the
	// left; restore the teammates so the matched room lists
	// every participant.
	if updLeft := game.Map(game.Map(updated["side"])["left"]); updLeft != nil &&
		len(game.List(updLeft["players"])) < len(leftEntries) {
		players := game.List(updLeft["players"])
		players = append(players, leftEntries[1:]...)
		updLeft["players"] = players
	}
	syncRoomMembers(updated)
	matchSetBothReady(match, false)
	s.fixMatchUserIPs(ctx, match)
	mid := game.Str(match["id"])
	matchRes := map[string]any{"resource": "/match/" + mid,
		"uri": "/match/" + mid, "body": match}
	updated["matchmaking"] = map[string]any{"multiplematch": false, "resources": []any{matchRes}}
	updated["stage"] = "matched"
	delete(updated, "queue_since")
	s.regRoom(updated)
	s.regMatch(mid, anchorUser.ID, match)
	participants := roomCoachIDs(updated)
	for _, cid := range participants {
		pu := s.userByCoachID(ctx, cid)
		if pu == nil {
			continue
		}
		if pu.ID == anchorUser.ID {
			anchorSt["active_room"] = updated
			anchorSt["match"] = match
			_ = game.SaveState(ctx, s.d.Store, pu.ID, anchorSt)
			continue
		}
		if pst, err := game.LoadState(ctx, s.d.Store, pu.ID); err == nil {
			pst["active_room"] = updated
			pst["match"] = match
			_ = game.SaveState(ctx, s.d.Store, pu.ID, pst)
		}
	}
	for _, cid := range participants {
		s.pushQueueCompletion(updated, match, rid, cid)
	}
	s.redeliverQueueCompletion(updated, match, rid, participants)
	for _, cid := range participants {
		if pu := s.userByCoachID(ctx, cid); pu != nil {
			uid := pu.ID
			time.AfterFunc(1500*time.Millisecond, func() {
				s.bridgeRoomMatch(rid, uid, cid, mid)
			})
		}
	}
	return updated
}

// pairQueueRooms seats guestCID in hostCID's searching room
// and builds the human match exactly like a room-flow build,
// completing both players' queues with the matchmaking push
// sequence and the stage bridge.
func (s *Server) pairQueueRooms(ctx context.Context, hostCID, guestCID string) map[string]any {
	hostRoom := s.regHostRoom(hostCID)
	guestRoom := s.regHostRoom(guestCID)
	if hostRoom == nil || guestRoom == nil ||
		game.Str(hostRoom["stage"]) != "searching" ||
		game.Str(guestRoom["stage"]) != "searching" {
		return nil
	}
	hostUser := s.userByCoachID(ctx, hostCID)
	guestUser := s.userByCoachID(ctx, guestCID)
	if hostUser == nil || guestUser == nil {
		return nil
	}
	hostSt, err := game.LoadState(ctx, s.d.Store, hostUser.ID)
	if err != nil {
		return nil
	}
	guestSt, err := game.LoadState(ctx, s.d.Store, guestUser.ID)
	if err != nil {
		return nil
	}
	entry := s.coachRoomEntry(ctx, guestCID)
	if entry == nil {
		return nil
	}
	rid := game.Int(hostRoom["id"])
	if rid == 0 {
		rid = 1
	}
	s.cancelRoomTimer(rid, hostUser.ID)
	s.cancelRoomTimer(rid, guestUser.ID)
	right := game.Map(game.Map(hostRoom["side"])["right"])
	right["players"] = []any{entry}
	right["ready"] = true
	right["authTeamId"] = game.Int(entry["teamid"])
	if entry["squad"] != nil {
		right["squad"] = entry["squad"]
	}
	if left := game.Map(game.Map(hostRoom["side"])["left"]); left != nil {
		left["ready"] = true
	}
	delete(hostRoom, "invited")
	syncRoomMembers(hostRoom)
	s.regSetSeat(guestCID, hostCID)
	s.regDropHost(guestCID)
	match, updated := s.d.Roster.BuildRoomMatch(hostSt, hostUser, hostRoom, s.friendLookup(ctx))
	if match == nil {
		// Roll the seating back; both coaches stay queued.
		right["players"] = []any{}
		right["ready"] = false
		syncRoomMembers(hostRoom)
		s.regClearSeat(guestCID)
		s.regSetHost(guestCID, guestRoom)
		return nil
	}
	if roomGuestCID(updated) != "" {
		// Human-vs-human adjustments, mirroring roomSideReady:
		// clean ready slate for the prep screens and each
		// side's session address set to its real peer IP.
		matchSetBothReady(match, false)
		setIP := func(sideName string, uid int64) {
			ip := game.UserLastIP(ctx, s.d.Store, uid)
			if ip == "" {
				return
			}
			sd := game.Map(game.Map(match["side"])[sideName])
			if players := game.List(sd["players"]); len(players) > 0 {
				p := game.Map(players[0])
				p["ip"] = ip
				p["privateip"] = []any{ip}
			}
		}
		setIP("left", hostUser.ID)
		setIP("right", guestUser.ID)
	}
	mid := game.Str(match["id"])
	matchRes := map[string]any{"resource": "/match/" + mid,
		"uri": "/match/" + mid, "body": match}
	updated["matchmaking"] = map[string]any{"multiplematch": false, "resources": []any{matchRes}}
	updated["stage"] = "matched"
	delete(updated, "queue_since")
	s.regRoom(updated)
	s.regMatch(mid, hostUser.ID, match)
	hostSt["active_room"] = updated
	hostSt["match"] = match
	_ = game.SaveState(ctx, s.d.Store, hostUser.ID, hostSt)
	guestSt["active_room"] = updated
	guestSt["match"] = match
	_ = game.SaveState(ctx, s.d.Store, guestUser.ID, guestSt)
	for _, rcpt := range []string{hostCID, guestCID} {
		s.pushQueueCompletion(updated, match, rid, rcpt)
	}
	s.redeliverQueueCompletion(updated, match, rid, []string{hostCID, guestCID})
	time.AfterFunc(1500*time.Millisecond, func() { s.bridgeRoomMatch(rid, hostUser.ID, hostCID, mid) })
	time.AfterFunc(1500*time.Millisecond, func() { s.bridgeRoomMatch(rid, guestUser.ID, guestCID, mid) })
	return updated
}

// pushQueueCompletion sends the matchmaking completion
// sequence (room matchmaking + stage, slim room, match +
// stage) to one coach.
func (s *Server) pushQueueCompletion(updated, match map[string]any, rid int, coachID string) {
	mid := game.Str(match["id"])
	push := func(uri string, body any) {
		game.MessengerPush(uri, pushBody(uri, body), coachID)
	}
	push("/room/"+strconv.Itoa(rid)+"/matchmaking", updated["matchmaking"])
	push("/room/"+strconv.Itoa(rid)+"/stage", "matched")
	push("/room/"+strconv.Itoa(rid), slimRoomForPush(updated, match, rid))
	push("/match/"+mid, match)
	push("/match/"+mid+"/stage", match["stage"])
}

// redeliverQueueCompletion re-sends the completion sequence
// every few seconds until the match leaves "created". The
// pushes are fire-and-forget: a coach whose Messenger socket
// was down at completion time (e.g. right after a server
// restart) otherwise sits at "searching" forever with the
// bot timer already cancelled (user report 2026-11-14: the
// ranked queue paired server-side but both clients missed
// every push and never saw each other).
func (s *Server) redeliverQueueCompletion(updated, match map[string]any, rid int, recipients []string) {
	mid := game.Str(match["id"])
	go func() {
		for i := 0; i < 12; i++ {
			time.Sleep(5 * time.Second)
			live, found := s.liveMatch(mid)
			if !found || game.Str(live["stage"]) != "created" {
				return
			}
			for _, rcpt := range recipients {
				s.pushQueueCompletion(updated, match, rid, rcpt)
			}
		}
	}()
}

// roomMoveMySide implements the room screen's side buttons:
// PUT /room/<id>/my/side {"side": "left"|"right"|"center"}
// (RoomController.moveMySide). The buttons step LEFT -> CENTER
// -> RIGHT: left/right are the playing sides (moving between
// them swaps the two side blocks wholesale), "center" is the
// referee/spectator seat — moving there takes only the caller's
// own entry out of his playing side, and leaving it joins the
// target side (or swaps with its lone occupant in a 1v1 room,
// so nobody is ever dropped from the room). Center occupants
// are not match participants: roomCoachIDs/roomFullSeats count
// the playing sides only. Affected sides must ready up again.
func (s *Server) roomMoveMySide(c *gin.Context, st game.State, user *game.User, room map[string]any, target string) {
	ctx := c.Request.Context()
	rid := roomIDOf(c)
	myCID, _ := myCoachID(st)
	if target == "left" || target == "right" || target == "center" {
		side := game.Map(room["side"])
		cur := ""
		var myEntry any
		for _, n := range []string{"left", "right", "center"} {
			for _, pl := range game.List(game.Map(side[n])["players"]) {
				if game.Str(game.Map(pl)["id"]) == myCID {
					cur, myEntry = n, pl
				}
			}
		}
		if cur != "" && target != cur {
			removeMine := func() {
				b := game.Map(side[cur])
				rest := []any{}
				for _, pl := range game.List(b["players"]) {
					if game.Str(game.Map(pl)["id"]) != myCID {
						rest = append(rest, pl)
					}
				}
				b["players"] = rest
			}
			switch {
			case target == "center":
				// Into the referee/spectator seat: only my
				// entry moves; my playing side keeps its
				// remaining players under a refreshed
				// leader and must ready up again.
				removeMine()
				cb := game.Map(side["center"])
				cb["players"] = append(game.List(cb["players"]), myEntry)
				src := game.Map(side[cur])
				refreshSideLeader(src)
				src["ready"] = false
			case cur == "center":
				dst := game.Map(side[target])
				occ := game.List(dst["players"])
				switch {
				case len(occ) < roomCapacity(room):
					removeMine()
					dst["players"] = append(game.List(dst["players"]), myEntry)
					refreshSideLeader(dst)
					dst["ready"] = false
				case roomCapacity(room) == 1 && len(occ) == 1:
					// Full 1v1 side: swap seats with the
					// occupant — he takes the center seat,
					// I take the side. Nobody is dropped.
					other := occ[0]
					removeMine()
					dst["players"] = []any{myEntry}
					cb := game.Map(side["center"])
					cb["players"] = append(game.List(cb["players"]), other)
					refreshSideLeader(dst)
					dst["ready"] = false
				}
				// A full multi-seat side: stay in center.
			default:
				// left <-> right: exchange the two side
				// blocks wholesale (players, team
				// choice, squad, team). Both sides
				// must ready up again.
				dst := game.Map(side[target])
				curB := game.Map(side[cur])
				for _, k := range []string{"players", "authTeamId", "squad", "team"} {
					cv, cok := curB[k]
					tv, tok := dst[k]
					if tok {
						curB[k] = tv
					} else {
						delete(curB, k)
					}
					if cok {
						dst[k] = cv
					} else {
						delete(dst, k)
					}
				}
				curB["ready"] = false
				dst["ready"] = false
			}
			syncRoomMembers(room)
			// syncRoomMembers rebuilds the member list from
			// the playing sides only; keep spectators listed.
			if cb := game.Map(side["center"]); cb != nil {
				if ms := game.List(room["members"]); ms != nil {
					room["members"] = append(ms, game.List(cb["players"])...)
				}
			}
		}
	}
	s.persistRoom(ctx, st, user, room)
	// Push the new seating to every other seated coach (playing
	// sides and spectators alike), not just one guest.
	uri := "/room/" + strconv.Itoa(rid)
	side := game.Map(room["side"])
	pushed := map[string]bool{}
	for _, n := range []string{"left", "right", "center"} {
		for _, pl := range game.List(game.Map(side[n])["players"]) {
			if cid := game.Str(game.Map(pl)["id"]); cid != "" && cid != myCID && !pushed[cid] {
				pushed[cid] = true
				game.MessengerPush(uri, pushBody(uri, room), cid)
			}
		}
	}
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/room/" + strconv.Itoa(rid), "body": room}}})
}

func slimRoomPlayer(pl any) any {
	p := game.Map(pl)
	if p == nil {
		return pl
	}
	out := map[string]any{}
	for k, v := range p {
		out[k] = v
	}
	if t := game.Map(p["team"]); t != nil && len(game.List(t["players"])) > 0 {
		out["team"] = map[string]any{"teamid": t["teamid"], "summary": t["summary"]}
	}
	return out
}

func slimRoomSide(side map[string]any) map[string]any {
	out := map[string]any{"ready": side["ready"] == true,
		"authTeamId": game.Int(side["authTeamId"]), "players": []any{}}
	players := []any{}
	for _, pl := range game.List(side["players"]) {
		players = append(players, slimRoomPlayer(pl))
	}
	out["players"] = players
	if side["squad"] != nil {
		out["squad"] = side["squad"]
	}
	if t := game.Map(side["team"]); t != nil {
		out["team"] = game.SlimTeam(t)
	}
	return out
}

// slimRoomForPush ports the slim room built in
// _complete_matchmaking_async for the Messenger push.
func slimRoomForPush(updated map[string]any, match map[string]any, rid int) map[string]any {
	elo := game.Int(game.Map(updated["ranked"])["elo"])
	if elo == 0 {
		elo = game.Int(game.Map(updated["ladder"])["elo"])
	}
	if elo == 0 {
		elo = 1000
	}
	side := game.Map(updated["side"])
	members := []any{}
	for _, pl := range game.List(updated["members"]) {
		members = append(members, slimRoomPlayer(pl))
	}
	roomType := game.Str(updated["type"])
	if roomType == "" {
		roomType = "ranked"
	}
	return map[string]any{
		"id": rid, "roomid": rid, "uri": "/room/" + strconv.Itoa(rid),
		"type": roomType, "stage": "matched",
		"slotsize": game.Int(updated["slotsize"]), "host": updated["host"],
		"observermode": false, "shootoutmode": true, "drawmode": false,
		"properties": updated["properties"],
		"AI":         updated["AI"],
		"ranked":     map[string]any{"elo": elo}, "ladder": map[string]any{"elo": elo},
		"roles": updated["roles"],
		"side": map[string]any{
			"left":    slimRoomSide(game.Map(side["left"])),
			"center":  map[string]any{"players": []any{}},
			"right":   slimRoomSide(game.Map(side["right"])),
			"referee": map[string]any{"players": []any{}},
		},
		"members": members,
		"matchmaking": map[string]any{"multiplematch": false,
			"resources": []any{map[string]any{"resource": "/match/" + game.Str(match["id"]),
				"uri": "/match/" + game.Str(match["id"])}}},
	}
}

func (s *Server) bridgeRoomMatch(rid int, uid int64, coachID, mid string) {
	ctx := context.Background()
	st, err := game.LoadState(ctx, s.d.Store, uid)
	if err != nil {
		return
	}
	room := game.Map(st["active_room"])
	if room == nil || game.Str(room["stage"]) != "matched" {
		return
	}
	match := game.Map(st["match"])
	if live, found := s.liveMatch(mid); found {
		match = live
	}
	if match == nil || game.Str(match["id"]) != mid || game.Str(match["stage"]) != "created" {
		return
	}
	// fx's bridge runs the _player_ready step itself: stage ->
	// "squad" AND both sides' ready flags cleared. Only advancing
	// the stage left the AI side ready=true from birth — the
	// client then never observed an opponent-ready transition and
	// ranked stalled on the prep screen (user report 2026-11-13).
	playerReadyStep(match, map[string]any{})
	st["match"] = match
	_ = game.SaveState(ctx, s.d.Store, uid, st)
	s.pushRelayServer(mid, match, coachID)
	game.MessengerPush("/match/"+mid, match, coachID)
	game.MessengerPush("/match/"+mid+"/stage", "squad", coachID)
}

// friendLookup resolves an invited coach id to its stored team
// across all users (fx _find_coach_across_users).
// fixMatchUserIPs sets every human participant's session
// address (ip + privateip) to their account's server-observed
// address — the per-user version of the two setIP calls the
// 1v1 build makes for the side leaders.
func (s *Server) fixMatchUserIPs(ctx context.Context, match map[string]any) {
	side := game.Map(match["side"])
	for _, n := range []string{"left", "right"} {
		for _, pv := range game.List(game.Map(side[n])["players"]) {
			p := game.Map(pv)
			cid := game.Str(p["id"])
			if cid == "" {
				continue
			}
			u := s.userByCoachID(ctx, cid)
			if u == nil {
				continue
			}
			if ip := game.UserLastIP(ctx, s.d.Store, u.ID); ip != "" {
				p["ip"] = ip
				p["privateip"] = []any{ip}
			}
		}
	}
}

func (s *Server) friendLookup(ctx context.Context) func(string) (map[string]any, map[string]any) {
	return func(oppID string) (map[string]any, map[string]any) {
		users, _ := game.AllUsers(ctx, s.d.Store)
		return s.d.Roster.FindCoachTeam(users, func(u *game.User) game.State {
			ust, err := game.LoadState(ctx, s.d.Store, u.ID)
			if err != nil {
				return nil
			}
			return ust
		}, oppID)
	}
}

// buildAndAnnounceRoomMatch builds the match for a room whose
// sides are ready, mirrors room+match into every participant's
// state and pushes the completion sequence, then writes the
// response. Extracted from roomSideReady so the club-match
// start endpoint drives the identical completion.
func (s *Server) buildAndAnnounceRoomMatch(c *gin.Context, st game.State, user *game.User, room map[string]any, rid int) {
	ctx := c.Request.Context()
	// Build from the HOST's state no matter who clicked ready:
	// the match's left side is the host (fx passed the clicker's
	// state, which would build guest-vs-guest when the guest
	// clicks first).
	buildSt, buildUser := st, user
	actorCID, _ := myCoachID(st)
	hostCID := game.Str(room["host"])
	guestClicked := actorCID != "" && hostCID != "" && actorCID != hostCID
	if guestClicked {
		if hu := s.userByCoachID(ctx, hostCID); hu != nil {
			if hst, err := game.LoadState(ctx, s.d.Store, hu.ID); err == nil {
				buildSt, buildUser = hst, hu
			}
		}
	}
	match, updated := s.d.Roster.BuildRoomMatch(buildSt, buildUser, room, s.friendLookup(ctx))
	if match == nil {
		writeJSON(c, map[string]any{"result": false})
		return
	}
	if guest := roomGuestCID(updated); guest != "" {
		// Human-vs-human: the match prep starts with a clean
		// consent slate (the room's ready flags made both
		// players show "ready" in prep before either pressed
		// it — user report 2026-11-13), and the sides carry
		// each player's public IP for the peer connection.
		// The addresses are each account's server-observed
		// address — the user tests on TWO machines on two
		// networks (corrected 2026-11-13 20:10), so these are
		// the real peer endpoints, not stale values.
		matchSetBothReady(match, false)
		setIP := func(sideName, ip string) {
			if ip == "" {
				return
			}
			sd := game.Map(game.Map(match["side"])[sideName])
			if players := game.List(sd["players"]); len(players) > 0 {
				p := game.Map(players[0])
				p["ip"] = ip
				// privateip must name the SAME peer: the fx
				// placeholder ["127.0.0.1"] tells the engine
				// the opponent is on the local machine, so a
				// remote peer's session machine handshakes
				// with itself and dies in recvfrom at match
				// load (crash rounds 2026-11-13 19:59-20:17,
				// all with loopback privateip). A bot never
				// noticed — its session is local anyway.
				p["privateip"] = []any{ip}
			}
		}
		setIP("left", game.UserLastIP(ctx, s.d.Store, buildUser.ID))
		if gu := s.userByCoachID(ctx, guest); gu != nil {
			setIP("right", game.UserLastIP(ctx, s.d.Store, gu.ID))
		}
		// Multi-seat rooms: the additional participants
		// get their own addresses the same way.
		s.fixMatchUserIPs(ctx, match)
	}
	s.regRoom(updated)
	s.regMatch(game.Str(match["id"]), buildUser.ID, match)
	_ = game.SaveState(ctx, s.d.Store, buildUser.ID, buildSt)
	// Mirror the room+match into EVERY participant's state
	// (the builder's own state already carries them from
	// BuildRoomMatch) so all of their /match/* calls
	// resolve natively, and push the completion sequence
	// to every participant plus the clicker. For a 1v1
	// room this is exactly the old host+guest behavior.
	recipients := []string{}
	seenRcpt := map[string]bool{}
	addRcpt := func(cid string) {
		if cid != "" && !seenRcpt[cid] {
			seenRcpt[cid] = true
			recipients = append(recipients, cid)
		}
	}
	builderCID, _ := myCoachID(buildSt)
	for _, cid := range roomCoachIDs(updated) {
		addRcpt(cid)
		if cid == builderCID {
			continue
		}
		if pu := s.userByCoachID(ctx, cid); pu != nil {
			if pst, err := game.LoadState(ctx, s.d.Store, pu.ID); err == nil {
				pst["active_room"] = updated
				pst["match"] = match
				_ = game.SaveState(ctx, s.d.Store, pu.ID, pst)
			}
		}
	}
	if actorCID != "" {
		// The clicker otherwise gets the transition only
		// via the HTTP response; his room screen can miss
		// the navigation that a push dispatches. This bit
		// the guest clicker (GM, round 2026-11-13 21:38)
		// and the host clicker alike (GM2, rounds 21:56
		// and 22:10: the clicker's client called
		// /player/ready but its squad screen never
		// activated while the pushed peer proceeded).
		// Push the same sequence to him as well — the
		// resources are identical and idempotent.
		recipients = append(recipients, actorCID)
	}
	for _, rcpt := range recipients {
		// Mirror the matchmaking completion's push sequence
		// (the full updated room is ~91KB with the match
		// embedded and would be dropped by the push size cap;
		// the slim room carries only the match reference).
		muri := "/match/" + game.Str(match["id"])
		game.MessengerPush("/matchmaking", map[string]any{"stage": "matched"}, rcpt)
		game.MessengerPush(muri+"/stage", pushBody(muri+"/stage", match["stage"]), rcpt)
		ruri := "/room/" + strconv.Itoa(rid)
		slim := slimRoomForPush(updated, match, rid)
		game.MessengerPush(ruri, pushBody(ruri, slim), rcpt)
		game.MessengerPush(muri, pushBody(muri, match), rcpt)
	}
	// No +1.5s stage bridge here (removed 2026-11-13 ~22:00):
	// both clients call /match/<id>/player/ready themselves
	// on entering the match, which drives created -> squad
	// through the normal handler (proven in every round
	// since 21:08). The bridge's extra full-match pushes to
	// both parties landed mid-initialization of the squad
	// screen and are the prime suspect for its flaky
	// half-initialized state (the #1009 chat-area failure
	// and the double squad posts). fx's room completion
	// pushes the sequence exactly once.
	resources := []any{
		map[string]any{"uri": "/room/" + strconv.Itoa(rid), "body": updated},
		map[string]any{"uri": "/match/" + game.Str(match["id"]), "body": match},
	}
	if relay := game.Map(match["relayServer"]); relay != nil &&
		len(game.List(game.Map(game.Map(match["side"])["right"])["players"])) > 0 {
		resources = append(resources, map[string]any{
			"uri": "/match/" + game.Str(match["id"]) + "/relayServer", "body": relay})
	}
	writeJSON(c, map[string]any{"result": true, "id": match["id"], "match": match,
		"room":      updated,
		"resources": resources})
	return
}

func (s *Server) roomSideReady(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	rid := roomIDOf(c)
	side := c.Param("side")
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	room := s.loadRoom(st, user, rid, "pvp_exhibition")
	ready := req["ready"] != false
	ctx := c.Request.Context()
	if ready && len(roomCoachIDs(room)) > 1 {
		// A human guest is seated: starting needs BOTH sides'
		// consent. Record this side's ready in the room and only
		// build the match once the other side has readied too
		// (building on the first click made the other player
		// "auto-ready" without their consent — user report
		// 2026-11-13).
		if sd := game.Map(game.Map(room["side"])[side]); sd != nil {
			sd["ready"] = true
		}
		leftReady := game.Map(game.Map(room["side"])["left"])["ready"] == true
		rightReady := game.Map(game.Map(room["side"])["right"])["ready"] == true
		s.persistRoom(ctx, st, user, room)
		// Multi-seat rooms additionally need every seat filled
		// before the build (a half-empty 2v2 room must not
		// start); 1v1 rooms keep the plain both-ready rule.
		// Club friendly rooms are exempt: the client creates
		// them with slotsize 5, but the extra seats are
		// observer seats — the contest itself is one captain
		// per club, so full seats can never be reached and
		// the build was dead (user report 2026-11-15: both
		// sides ready, nothing happened).
		if !(leftReady && rightReady) ||
			(roomCapacity(room) > 1 && !isClubFriendlyRoom(room) && !roomFullSeats(room)) {
			writeJSON(c, map[string]any{"result": true,
				"resources": []any{map[string]any{"uri": "/room/" + strconv.Itoa(rid), "body": room}}})
			return
		}
	}
	if ready {
		s.buildAndAnnounceRoomMatch(c, st, user, room, rid)
		return
	}
	room["stage"] = "room"
	if sd := game.Map(game.Map(room["side"])[side]); sd != nil {
		sd["ready"] = false
	}
	s.persistRoom(ctx, st, user, room)
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/room/" + strconv.Itoa(rid), "body": room}}})
}

func (s *Server) roomInvite(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	rid := roomIDOf(c)
	target := c.Param("cid")
	myCID, _ := myCoachID(st)
	// Inviting is a fresh start: drop any leftover matched room
	// first so the invitation always targets a live lobby.
	s.dissolveMatchedRoom(ctx, st, user)
	room := s.loadOwnRoom(st, user, rid, "pvp_sim")
	// In a 1v1 room, a previous guest (accepted earlier) is
	// unseated when the host invites someone else. Multi-seat
	// rooms keep their members — the host fills the room by
	// inviting several coaches.
	if roomCapacity(room) <= 1 {
		if prev := roomGuestCID(room); prev != "" && prev != target {
			s.unseatGuest(ctx, prev)
			room = s.loadOwnRoom(st, user, rid, "pvp_sim")
		}
	}
	roomInvitedAdd(room, target)
	st["active_room"] = room
	s.regRoom(room)
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	// The invitation stays PENDING until the invitee answers the
	// in-game letter (invite.go): seating happens in roomJoin, not
	// here — seating on invite alone dragged the invitee into the
	// room without their consent.
	me := s.d.Roster.CoachMe(st, game.DefaultSlot(st), user)
	rec := map[string]any{
		"host": myCID, "host_name": game.Str(me["name"]),
		"host_teamid": game.Int(me["teamid"]),
		"host_level":  game.Int(game.Map(me["levelinfo"])["level"]),
		"room_type":   game.Str(room["type"]),
		"created":     time.Now().Unix(),
	}
	if raw, err := json.Marshal(rec); err == nil {
		s.d.Store.Redis.Set(ctx, "roominvite:"+target, raw, inviteTTL)
	}
	// Deliver the invitation as an in-game letter: the invitee's
	// notification feed now carries the match/invite note.
	s.pushNotes(ctx, target)
	uri := "/room/" + strconv.Itoa(rid)
	game.MessengerPush(uri, pushBody(uri, room), myCID)
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/room/" + strconv.Itoa(rid), "body": room}}})
}

func (s *Server) roomUpdate(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	rid := roomIDOf(c)
	action := c.Param("action")
	if rest := c.Param("rest"); rest != "" {
		action = action + "/" + strings.TrimPrefix(rest, "/")
	}
	// The invite letter's Đồng ý lands here: POST (the client's
	// ResourceController sends POST despite the REST-y shape —
	// proven by the production journal 2026-11-13 12:53:46) or PUT
	// /room/<hostCID>/join — dispatch before the generic flow.
	if strings.TrimSuffix(action, "/") == "join" &&
		(c.Request.Method == "POST" || c.Request.Method == "PUT") {
		s.roomJoin(c, st, user)
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	room := s.loadRoom(st, user, rid, "")
	switch {
	case action == "my/side" && game.Str(req["side"]) != "":
		s.roomMoveMySide(c, st, user, room, game.Str(req["side"]))
		return
	case action == "type" && game.Str(req["type"]) != "":
		room["type"] = game.Str(req["type"])
	case action == "slotsize" && game.Int(req["slotsize"]) > 0:
		room["slotsize"] = game.Int(req["slotsize"])
	case action == "AI" && game.Map(req["index"]) != nil:
		room["AI"] = map[string]any{"index": req["index"]}
		// The AI steppers ARE the bot pick in the generic room
		// flow (the client posts the index on every stepper
		// move; production journal 2026-11-14 19:49: six posts
		// before ready). fx stored the index and never used it,
		// so the match rolled a random bot club instead of the
		// picked one (owner report 2026-11-15). Resolve it
		// against the SORT* lists and put the club on the right
		// side, where BuildRoomMatch reads it (AI_picked gate).
		room["AI_picked"] = true
		if tid := s.d.Roster.AITeamID(game.Map(req["index"])); tid != 0 {
			s.applyRoomSidePick(room, "right", tid)
		}
	case action == "back":
		room["stage"] = "room"
		// Returning from a match must not leave the host flagged
		// ready: in a bot room the client offers no unready path,
		// so the carried-over ready flag traps the player in the
		// room — leave stays blocked until the game is restarted
		// (owner report 2026-11-15; his client never sent a leave
		// after /back). Clear both sides' ready flags, and for a
		// bot room also drop the matched bot entry so the right
		// side is the empty seat the room was created with.
		for _, sideName := range []string{"left", "right"} {
			sd := game.Map(game.Map(room["side"])[sideName])
			if sd == nil {
				continue
			}
			sd["ready"] = false
			for _, pv := range game.List(sd["players"]) {
				if p := game.Map(pv); p != nil {
					p["ready"] = false
				}
			}
		}
		if game.Map(st["match"])["_human"] != true {
			if sd := game.Map(game.Map(room["side"])["right"]); sd != nil {
				sd["players"] = []any{}
			}
		}
		room["matchmaking"] = map[string]any{"multiplematch": false, "resources": []any{}}
		delete(room, "queue_since")
	case action == "messages" && game.Str(req["message"]) != "":
		coach := game.CoachBySlot(st, game.DefaultSlot(st))
		name := "Manager"
		if coach != nil && game.Str(coach["name"]) != "" {
			name = game.Str(coach["name"])
		}
		msg := game.Str(req["message"])
		if len(msg) > 200 {
			msg = msg[:200]
		}
		vo := map[string]any{"type": "chat", "coachname": name, "message": msg}
		coachID, _ := myCoachID(st)
		muri := "/room/" + strconv.Itoa(rid) + "/messages/latest"
		game.MessengerPush(muri, vo, coachID)
		// Room chat reaches the other occupant too (fx pushed only
		// to the sender, which was all a solo room needed).
		if host := game.Str(room["host"]); host != "" && host != coachID {
			game.MessengerPush(muri, vo, host)
		} else if g := roomGuestCID(room); g != "" && g != coachID {
			game.MessengerPush(muri, vo, g)
		}
		writeJSON(c, map[string]any{"result": true,
			"resources": []any{map[string]any{"uri": "/room/" + strconv.Itoa(rid) + "/messages/latest", "body": vo}}})
		return
	}
	s.persistRoom(c.Request.Context(), st, user, room)
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/room/" + strconv.Itoa(rid), "body": room}}})
}

// applyRoomSidePick records a team pick on one room side and
// rebuilds that side's displayed team/squad from the picked
// club (the same construction MakeRoomResource uses). Updating
// authTeamId alone left the waiting room showing the club
// rolled at creation time (owner report 2026-11-15).
func (s *Server) applyRoomSidePick(room map[string]any, side string, tid int) {
	sd := game.Map(game.Map(room["side"])[side])
	if sd == nil {
		return
	}
	sd["authTeamId"] = tid
	for _, pv := range game.List(sd["players"]) {
		if p := game.Map(pv); p != nil {
			p["teamid"] = tid
		}
	}
	var raw map[string]any
	if side == "right" {
		raw = s.d.Roster.BuildBotTeamRaw(tid)
	} else {
		raw = s.d.Roster.RosterTeam(tid)
	}
	if raw == nil {
		return
	}
	team := s.d.Roster.OwnedTeam(raw)
	team["teamid"] = tid
	summary := s.d.Roster.TeamSummaryOf(team, tid)
	team["summary"] = summary
	sd["team"] = team
	sd["squad"] = team["squad"]
	for _, pv := range game.List(sd["players"]) {
		p := game.Map(pv)
		if p == nil {
			continue
		}
		p["summary"] = summary
		p["squad"] = team["squad"]
		if side == "left" {
			p["team"] = map[string]any{"teamid": tid, "summary": summary}
		} else {
			p["team"] = game.SlimTeam(team)
		}
	}
}

func (s *Server) exhibitionTeamChange(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	rid := roomIDOf(c)
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	room := s.loadRoom(st, user, rid, "pvp_exhibition")
	side := game.Str(req["side"])
	if side == "" {
		side = "left"
	}
	tid := game.Int(req["teamid"])
	if tid == 0 {
		tid = 1
	}
	// TEMP PROBE (2026-11-15): learn which side label the real
	// client sends its exhibition team picks with.
	log.Printf("exhib-team-probe: uid=%d side=%q teamid=%d", user.ID, side, tid)
	s.applyRoomSidePick(room, side, tid)
	s.persistRoom(c.Request.Context(), st, user, room)
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/room/" + strconv.Itoa(rid), "body": room}}})
}
