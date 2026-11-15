package game

// _build_room_match_and_update, ported from fx: builds the room's
// match (real PvP type, relayServer, born at "created") and the
// "matched" room. The bot side of the MATCH keeps players == []
// (Bug 2c, closed by the user's decision — do not reopen).

import (
	mrand "math/rand"
	"os"
	"strings"
)

// relayAddress is the public relay endpoint announced to clients,
// "<FO3_PUBLIC_HOST>:28000". Set FO3_PUBLIC_HOST to your server's
// public domain or IP (see README, "Server address configuration").
func relayAddress() string {
	h := os.Getenv("FO3_PUBLIC_HOST")
	if h == "" {
		h = "fo3.example.com"
	}
	return h + ":28000"
}

// FindCoachTeam locates a coach's stored team across every user
// (the invited-friend path of the room match builder).
func (r *Roster) FindCoachTeam(users []*User, load func(*User) State, targetCID string) (map[string]any, map[string]any) {
	for _, u := range users {
		st := load(u)
		if st == nil {
			continue
		}
		for _, cv := range List(st["coaches"]) {
			coach := Map(cv)
			if coach == nil || Str(coach["_id"]) != targetCID {
				continue
			}
			return TeamOf(st, Int(coach["slot"])), Map(coach["levelinfo"])
		}
	}
	return nil, nil
}

// BuildRoomMatch ports _build_room_match_and_update. It mutates st
// (sets st["match"] and st["active_room"]) and returns both objects.
func (r *Roster) BuildRoomMatch(st State, user *User, room map[string]any, friendTeam func(oppID string) (map[string]any, map[string]any)) (map[string]any, map[string]any) {
	coach := CoachBySlot(st, DefaultSlot(st))
	if coach == nil {
		return nil, nil
	}
	roomType := Str(room["type"])
	if roomType == "" {
		roomType = "ladder_pvp_1v1"
	}
	leftInfo := Map(Map(room["side"])["left"])
	rightInfo := Map(Map(room["side"])["right"])
	// If the room's sides were swapped (the builder/host sits
	// on the right after a my/side move), build from the
	// host's perspective: the match build always puts the
	// builder on the left and reads the opponent from the
	// right entry.
	builderOnRight, builderOnLeft := false, false
	for _, pv := range List(rightInfo["players"]) {
		if Str(Map(pv)["id"]) == Str(coach["_id"]) {
			builderOnRight = true
		}
	}
	for _, pv := range List(leftInfo["players"]) {
		if Str(Map(pv)["id"]) == Str(coach["_id"]) {
			builderOnLeft = true
		}
	}
	if builderOnRight && !builderOnLeft {
		leftInfo, rightInfo = rightInfo, leftInfo
	}
	myTid := Int(leftInfo["authTeamId"])
	if myTid == 0 {
		myTid = Int(coach["teamid"])
		if myTid == 0 {
			myTid = 1
		}
	}
	existingRight := List(rightInfo["players"])
	var invited map[string]any
	if len(existingRight) > 0 {
		invited = Map(existingRight[0])
	}
	var oppTid int
	var oppName, oppID string
	if invited != nil {
		oppTid = Int(invited["teamid"])
		if oppTid == 0 {
			oppTid = Int(rightInfo["authTeamId"])
		}
		if oppTid == 0 {
			oppTid = EuroClubs[0]
		}
		oppName = Str(invited["name"])
		if oppName == "" {
			oppName = BotNames[mrand.Intn(len(BotNames))]
		}
		oppID = Str(invited["id"])
		if oppID == "" {
			oppID = NewIID()
		}
	} else {
		// The right side's authTeamId is the club the room
		// DISPLAYS, and in two flows it is the owner's explicit
		// choice: exhibition rooms (picked via
		// /exhibition/room/<id>/team) and any room whose bot was
		// picked through the AI steppers (POST /room/<id>/AI,
		// flagged AI_picked by the room update handler).
		// Re-rolling a fresh club here made the match use a
		// different club than the one shown in the waiting room
		// (owner reports 2026-11-15). Plain ladder rooms keep
		// the fresh roll: their right authTeamId is not a
		// user choice.
		oppTid = 0
		if strings.Contains(strings.ToLower(roomType), "exhibition") || room["AI_picked"] == true {
			if t := Int(rightInfo["authTeamId"]); t != 0 && r.RosterTeam(t) != nil {
				oppTid = t
			}
		}
		if oppTid == 0 {
			oppTid = r.PickBotTeamID(myTid)
		}
		oppName = BotNames[mrand.Intn(len(BotNames))]
		oppID = NewIID()
	}
	slot := DefaultSlot(st)
	me := r.CoachMe(st, slot, user)
	lower := strings.ToLower(roomType)
	isMgr := strings.Contains(lower, "mgr") || strings.Contains(lower, "manager")
	rkKey := "ranked"
	if isMgr {
		rkKey = "ranked_mgr"
	}
	rk := RankedState(st, rkKey)
	myElo := Int(rk["elo"])
	if rk["elo"] == nil {
		myElo = 1000
	}
	divSuffix, _ := DivisionForElo(myElo)
	myDiv := "DIVISION_" + divSuffix

	userObj := map[string]any{
		"id": me["id"], "_id": me["id"], "name": me["name"], "levelinfo": me["levelinfo"],
		"currdivision": myDiv, "prevdivision": myDiv, "clubid": "",
		"currentbadge": Int(me["currentbadge"]), "elo": myElo,
	}
	if userObj["currentbadge"] == 0 {
		userObj["currentbadge"] = 1001
	}
	// "referee" included: the client's custom-match screen creates
	// its room with type "pvp_referee" (the standard 1v1 friendly
	// room — referee is a role slot in it). fx's exhibition set
	// missed it, so such rooms fell through to League_2_league and
	// died exactly like Issue 2i: a non-PvP type makes the client
	// skip LobbyManager::ConnectSession at stage "created", the
	// session-info object stays null, and StartSingleMatch crashes
	// at fifazf.exe+0x5D34EA when the match loads (every human
	// room test 2026-11-13, dumps from both machines).
	isExhibition := strings.Contains(lower, "exhibition") || strings.Contains(lower, "custom") ||
		strings.Contains(lower, "sim") || strings.Contains(lower, "random") ||
		strings.Contains(lower, "referee")
	oppUser := map[string]any{
		"id": oppID, "_id": oppID, "name": oppName,
		"levelinfo":    map[string]any{"level": 20 + mrand.Intn(31), "exp": 0, "to": 10000},
		"currdivision": myDiv, "prevdivision": myDiv, "clubid": "",
		"currentbadge": 1001, "elo": myElo,
	}
	for k, v := range SessionProfileFields() {
		userObj[k] = v
		oppUser[k] = v
	}
	isRanked := strings.Contains(lower, "ladder") || strings.Contains(lower, "ranked")
	mtype := "League_2_league"
	if isMgr {
		if isRanked {
			mtype = "Ranked_MGR_PvP_1v1"
		} else {
			mtype = "League_2_league_MGR"
		}
	} else if strings.Contains(lower, "club_friendly") {
		// Club-vs-club friendly (waiting-room merge flow):
		// the client's match type is Club_Friendly (MATCH_RULES
		// key, settings screen special-case). Without this
		// branch the room fell through to League_2_league —
		// the Issue 2i blackscreen class.
		mtype = "Club_Friendly"
	} else if strings.Contains(lower, "club_ranked") {
		mtype = "Club_Ranked"
	} else if isRanked {
		mtype = "Ranked_PvP_1v1"
	} else if isExhibition {
		mtype = "Friendly_PvP_1v1"
	}

	var leftTeamRaw map[string]any
	if strings.Contains(lower, "exhibition") {
		leftTeamRaw = r.RosterTeam(myTid)
	} else {
		leftTeamRaw = TeamOf(st, slot)
		if leftTeamRaw == nil {
			leftTeamRaw = r.RosterTeam(myTid)
		}
	}
	var rightTeamRaw map[string]any
	isBot := true
	if invited != nil && friendTeam != nil {
		if ft, fl := friendTeam(oppID); ft != nil {
			rightTeamRaw = deepCopyMap(ft)
			if fl != nil {
				oppUser["levelinfo"] = fl
			}
			isBot = false
		}
	}
	if rightTeamRaw == nil {
		rightTeamRaw = r.BuildBotTeamRaw(oppTid)
		isBot = true
	}
	var rightSide map[string]any
	if isBot {
		rightSide = r.MatchSide(rightTeamRaw, oppTid, nil, nil)
	} else {
		rightSide = r.MatchSide(rightTeamRaw, oppTid, oppUser, nil)
		if len(List(Map(rightSide["team"])["teamcolors"])) == 0 {
			Map(rightSide["team"])["teamcolors"] = ActiveTeamcolors(Map(rightSide["team"]))
		}
	}
	match := NewMatch(mtype,
		r.MatchSide(leftTeamRaw, myTid, userObj, r.TacticsOf(st)),
		rightSide, nil)
	ApplyBallAvailables(match, r.BallAvailables(st))
	if !isBot {
		// Human session: MatchController.connectToTheSession
		// builds FOUR user arrays (left/right/observer/referee)
		// from dot-paths side.observer.players and
		// side.referee.players. With those paths ABSENT the
		// native session builder receives null arrays, and at
		// match start its consumer hits the deliberate trap
		// `mov dword ptr [eax], 0x4cb2f` with eax=0 (crash dump
		// 2026-11-13 21:30:41, fifazf+0x5D5898, both engines died
		// at start after a clean session exchange). Present-but-
		// empty sides mirror fx's room sides, which always carry
		// center/referee entries. Bot matches keep the old shape:
		// their local session never needed them.
		sd := Map(match["side"])
		sd["observer"] = map[string]any{"players": []any{}, "ready": true}
		sd["referee"] = map[string]any{"players": []any{}, "ready": true}
	}
	if isMgr {
		Map(match["gamesettings"])["simulated"] = true
	}
	if !isBot {
		// Multi-seat rooms (2v2/3v3): every additional seated
		// coach joins their side's session user list — the
		// side's TEAM stays the side leader's (the client
		// derives each coach's control role from their index
		// in this list). The leaders are already in place
		// (userObj left, oppUser right); the extras come from
		// their room entries, which carry their real level,
		// elo and division.
		sd := Map(match["side"])
		appendEntries := func(sideName string, entries []any) {
			if len(entries) <= 1 {
				return
			}
			players := List(Map(sd[sideName])["players"])
			for _, ev := range entries[1:] {
				if uo := entryUserObj(Map(ev)); uo != nil {
					players = append(players, uo)
				}
			}
			Map(sd[sideName])["players"] = players
		}
		appendEntries("left", List(leftInfo["players"]))
		appendEntries("right", List(rightInfo["players"]))
	}
	match["stage"] = "created"
	match["_ranked"] = !isExhibition
	if strings.Contains(lower, "club_friendly") {
		// Club friendlies never touch personal elo or the
		// ranked EP bonus; their stake is the club record.
		match["_ranked"] = false
	}
	match["_opp_coach"] = map[string]any{"coachid": oppID, "name": oppName}
	if !isBot {
		// Only a HUMAN opponent gates the ready steps on mutual
		// consent (sideReadyStep) and receives stage pushes
		// (pushMatchUpdate). Bot matches also carry _opp_coach
		// (match-history identity), which used to trip that gate:
		// the AI side never readies itself, so squad prep waited
		// out the 60s client timeout and the kit screen hung
		// forever (user report 2026-11-14).
		match["_human"] = true
	}
	match["elo"] = map[string]any{
		"left":  map[string]any{"rating": myElo},
		"right": map[string]any{"rating": myElo},
	}
	rid := Int(room["id"])
	if rid == 0 {
		rid = 1
	}
	match["roomid"] = rid
	// Relay stays OFF (fx parity): the engine never dialed the
	// relay in any test round, with the flag either way.
	match["relayServer"] = map[string]any{"address": relayAddress(), "forceRelay": true}
	match["leagueparam"] = map[string]any{
		"matchtype": "league", "round": 1, "home": myTid, "away": oppTid, "leagueid": 1,
	}
	st["match"] = match

	oppLevel := Int(Map(oppUser["levelinfo"])["level"])
	opts := RoomOpts{Match: match, LeftTid: myTid, RightTid: oppTid, LeftReady: true,
		OppName: oppName, OppLevel: oppLevel}
	if len(existingRight) > 0 {
		opts.RightPlayersOverride = existingRight
	}
	updated := r.MakeRoomResource(st, user, rid, roomType, Int(room["slotsize"]), "matched", opts)
	st["active_room"] = updated
	if strings.Contains(lower, "club_friendly") || strings.Contains(lower, "club_ranked") {
		// Carry the room sides' club blocks into the match
		// (the client reads side.club {clubid, clublv,
		// clubname} in club matches, and the server records
		// the result against these club ids at match end)
		// and into the rebuilt room: MakeRoomResource mints
		// the host entry fresh with empty club fields, so
		// restore them from the original room.
		for _, sideName := range []string{"left", "right"} {
			rsd := Map(Map(room["side"])[sideName])
			clubBlk := Map(rsd["club"])
			if clubBlk == nil {
				continue
			}
			if msd := Map(Map(match["side"])[sideName]); msd != nil {
				msd["club"] = clubBlk
			}
			if usd := Map(Map(updated["side"])[sideName]); usd != nil {
				usd["club"] = clubBlk
				usd["clubid"] = clubBlk["clubid"]
				rp := List(rsd["players"])
				up := List(usd["players"])
				if len(rp) > 0 && len(up) > 0 {
					Map(up[0])["clubid"] = Map(rp[0])["clubid"]
					Map(up[0])["clubname"] = Map(rp[0])["clubname"]
				}
			}
		}
	}
	return match, updated
}

// entryUserObj builds a match session user object for an
// additional seated coach from their room entry (the entry
// already carries their real level/levelinfo/elo/division
// from their own account).
func entryUserObj(e map[string]any) map[string]any {
	if e == nil {
		return nil
	}
	div := Str(e["division"])
	if div == "" {
		div = "DIVISION_AMATEUR_C"
	}
	elo := Int(e["elo"])
	if elo == 0 {
		elo = 1000
	}
	uo := map[string]any{
		"id": e["id"], "_id": e["id"], "name": e["name"],
		"levelinfo":    e["levelinfo"],
		"currdivision": div, "prevdivision": div, "clubid": "",
		"currentbadge": 1001, "elo": elo,
	}
	if uo["levelinfo"] == nil {
		uo["levelinfo"] = map[string]any{"level": Int(e["level"]), "exp": 0, "to": 10000}
	}
	for k, v := range SessionProfileFields() {
		uo[k] = v
	}
	return uo
}

// ActiveTeamcolors ports hc.active_teamcolors for a friend side
// (the user's own club colors by team color id).
func ActiveTeamcolors(team map[string]any) []any {
	// The Go roster serves teamcolors empty everywhere else (the
	// client tolerates it); keep the same here for consistency.
	if team == nil {
		return []any{}
	}
	return []any{}
}
