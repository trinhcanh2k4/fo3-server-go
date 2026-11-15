package game

// Room + matchmaking, ported from fx's room block
// (_make_room_resource, _build_room_match_and_update and helpers).
// Load-bearing shapes (see the fx comments): the MATCH's bot side
// keeps players == [] (a fake peer entry crashes the engine at
// StartSingleMatch); the ROOM's right side keeps its bot entry for
// display; room matches are born at stage "created" with a
// relayServer block so LobbyManager::ConnectSession can build the
// session object; both sides' room player entries carry the session
// profile fields (a missing string "ip" hard-traps the client).

import (
	"fmt"
	mrand "math/rand"
	"strings"
)

// LegacyCoachID ports hc.coach_id(slot) = "%024x" % (slot + 1).
func LegacyCoachID(slot int) string { return fmt.Sprintf("%024x", slot+1) }

// EuroClubs ports EURO_CLUBS (the validated bot pool).
var EuroClubs = []int{1, 5, 9, 10, 11, 21, 22, 44, 45, 47, 73, 240, 241, 243}

// BotNames ports BOT_ENGLISH_NAMES.
var BotNames = []string{
	"Alex Ferguson", "David Beckham", "Michael Owen", "Steven Gerrard", "Frank Lampard",
	"Wayne Rooney", "Harry Kane", "Marcus Rashford", "John Terry", "Paul Scholes",
	"James Milner", "Jordan Henderson", "Raheem Sterling", "Jack Grealish", "Declan Rice",
	"Phil Foden", "Bukayo Saka", "Mason Mount", "Jude Bellingham", "Trent Alexander",
	"Kyle Walker", "Luke Shaw", "Kieran Trippier", "Harry Maguire", "John Stones",
	"Jordan Pickford", "Aaron Ramsdale", "Nick Pope", "Dominic Calvert", "Callum Wilson",
	"Ollie Watkins", "Ivan Toney", "James Maddison", "Harvey Barnes", "Conor Gallagher",
	"Jarrod Bowen", "Eberechi Eze", "Michael Olise", "Cole Palmer", "Anthony Gordon",
}

// SessionProfileFields ports _session_profile_fields (Issue 2j).
func SessionProfileFields() map[string]any {
	return map[string]any{
		"ip": "127.0.0.1", "privateip": []any{"127.0.0.1"},
		"esports": false, "igr": false, "vip": 0, "vipDetail": 0,
		"qqvip": false, "qqgamevip": false, "qqgamevvip": false,
		"tgp1": false, "tgp2": false, "tgp3": false,
	}
}

// PickBotTeamID ports _pick_bot_team_id: a EURO_CLUBS team whose
// roster really has players and a squad.
func (r *Roster) PickBotTeamID(exclude int) int {
	pool := []int{}
	for _, t := range EuroClubs {
		if t != exclude {
			pool = append(pool, t)
		}
	}
	mrand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	for _, tid := range pool {
		raw := r.RosterTeam(tid)
		if raw == nil {
			continue
		}
		if len(List(raw["players"])) >= 18 && len(List(Map(raw["squad"])["positions"])) >= 11 {
			return tid
		}
	}
	if exclude != 243 {
		return 243
	}
	return 241
}

// BuildBotTeamRaw ports _build_upgraded_bot_team_raw: natural
// grades (+0..+3) and levels; the roster is NOT formation-fixed.
func (r *Roster) BuildBotTeamRaw(oppTid int) map[string]any {
	raw := deepCopyMap(r.RosterTeam(oppTid))
	if raw == nil {
		raw = map[string]any{"players": []any{}, "squad": map[string]any{"positions": []any{}, "kickers": []any{}}}
	}
	gradeChoices := []int{0, 1, 1, 1, 2, 2, 3}
	for _, pv := range List(raw["players"]) {
		if p := Map(pv); p != nil {
			p["grade"] = gradeChoices[mrand.Intn(len(gradeChoices))]
			lvl := 3 + mrand.Intn(13)
			p["levelinfo"] = map[string]any{"level": lvl, "exp": 0, "to": PlayerNeed(lvl)}
		}
	}
	r.repairSquad(raw)
	return raw
}

// SlimTeam ports _slim_team (strip staticdata from players).
func SlimTeam(team map[string]any) map[string]any {
	if team == nil {
		return nil
	}
	t := deepCopyMap(team)
	for _, pv := range List(t["players"]) {
		if p := Map(pv); p != nil {
			delete(p, "staticdata")
		}
	}
	return t
}

// RoomOpts carries _make_room_resource's optional arguments.
type RoomOpts struct {
	Match                map[string]any
	LeftTid, RightTid    int
	LeftReady            bool
	RightPlayersOverride []any
	OppName              string
	OppLevel             int
}

// MakeRoomResource ports _make_room_resource.
func (r *Roster) MakeRoomResource(st State, user *User, roomID int, roomType string, slotsize int, stage string, opts RoomOpts) map[string]any {
	slot := DefaultSlot(st)
	me := r.CoachMe(st, slot, user)
	cid := Str(me["id"])
	cname := Str(me["name"])
	myTid := opts.LeftTid
	if myTid == 0 {
		myTid = Int(me["teamid"])
		if myTid == 0 {
			myTid = 1
		}
	}
	oppTid := opts.RightTid
	if oppTid == 0 {
		oppTid = r.PickBotTeamID(myTid)
	}
	clevel := Int(Map(me["levelinfo"])["level"])
	rkKey := "ranked"
	if strings.Contains(strings.ToLower(roomType), "mgr") || strings.Contains(strings.ToLower(roomType), "manager") {
		rkKey = "ranked_mgr"
	}
	rk := RankedState(st, rkKey)
	myElo := Int(rk["elo"])
	if rk["elo"] == nil {
		myElo = 1000
	}
	myDiv, _ := DivisionForElo(myElo)

	var rawMyTeam map[string]any
	if strings.Contains(roomType, "exhibition") {
		rawMyTeam = r.RosterTeam(myTid)
	} else {
		rawMyTeam = TeamOf(st, slot)
		if rawMyTeam == nil {
			rawMyTeam = r.RosterTeam(myTid)
		}
	}
	myTeam := r.OwnedTeam(rawMyTeam)
	mySummary := r.TeamSummaryOf(myTeam, myTid)
	myTeam["teamid"] = myTid
	myTeam["summary"] = mySummary

	mm := map[string]any{"multiplematch": false, "resources": []any{}}
	if opts.Match != nil {
		mm["resources"] = []any{map[string]any{"uri": "/match/" + Str(opts.Match["id"]), "body": opts.Match}}
	}

	myEntry := map[string]any{
		"id": cid, "_id": cid, "name": cname, "teamid": myTid,
		"level": clevel, "levelinfo": me["levelinfo"],
		"captain": true, "master": true, "onmatch": false,
		"ready": opts.LeftReady,
		"elo": myElo, "division": "DIVISION_" + myDiv,
		"clubid": "", "clubname": "",
		"summary": mySummary,
		"squad":   myTeam["squad"],
		"team":    map[string]any{"teamid": myTid, "summary": mySummary},
	}
	for k, v := range SessionProfileFields() {
		myEntry[k] = v
	}
	if myEntry["levelinfo"] == nil {
		myEntry["levelinfo"] = map[string]any{"level": clevel, "exp": 0, "maxexp": 100}
	}

	var rightPlayers []any
	if opts.RightPlayersOverride != nil {
		rightPlayers = append([]any{}, opts.RightPlayersOverride...)
	} else if stage == "matched" {
		oppID := LegacyCoachID(1)
		oppName := opts.OppName
		if oppName == "" {
			oppName = BotNames[mrand.Intn(len(BotNames))]
		}
		botTeam := r.OwnedTeam(r.BuildBotTeamRaw(oppTid))
		botSummary := r.TeamSummaryOf(botTeam, oppTid)
		botTeam["teamid"] = oppTid
		botTeam["summary"] = botSummary
		botLvl := opts.OppLevel
		if botLvl == 0 {
			botLvl = 15 + mrand.Intn(51)
		}
		entry := map[string]any{
			"id": oppID, "_id": oppID, "name": oppName, "teamid": oppTid,
			"level": botLvl,
			"levelinfo": map[string]any{"level": botLvl, "exp": 0, "maxexp": CoachNeed(botLvl)},
			"captain": true, "master": false, "onmatch": false, "ready": true,
			"elo": myElo, "division": "DIVISION_" + myDiv,
			"clubid": "", "clubname": "",
			"summary": botSummary,
			"squad":   botTeam["squad"],
			"team":    SlimTeam(botTeam),
		}
		for k, v := range SessionProfileFields() {
			entry[k] = v
		}
		rightPlayers = []any{entry}
	} else {
		rightPlayers = []any{}
	}

	rightSide := map[string]any{
		"ready": len(rightPlayers) > 0, "authTeamId": oppTid, "players": rightPlayers,
	}
	if len(rightPlayers) > 0 && Map(rightPlayers[0])["squad"] != nil {
		rightSide["squad"] = Map(rightPlayers[0])["squad"]
		rightSide["team"] = Map(rightPlayers[0])["team"]
	} else {
		empty := r.OwnedTeam(r.BuildBotTeamRaw(oppTid))
		rightSide["squad"] = empty["squad"]
		rightSide["team"] = empty
	}
	rightRole0 := []any{}
	if len(rightPlayers) > 0 {
		rightRole0 = []any{Map(rightPlayers[0])["id"]}
	}
	members := append([]any{myEntry}, rightPlayers...)

	return map[string]any{
		"id": roomID, "roomid": roomID, "uri": "/room/" + itoa(roomID),
		"type": roomType, "stage": stage, "slotsize": slotsize,
		"host": cid, "observermode": false, "shootoutmode": true, "drawmode": false,
		"properties": map[string]any{"publicmode": true},
		"AI":         map[string]any{"index": map[string]any{"nation": 0, "league": 0, "team": 0, "difficulty": 2}},
		"ranked":     map[string]any{"elo": myElo},
		"ladder":     map[string]any{"elo": myElo},
		"roles": map[string]any{
			"left":  map[string]any{"0": []any{cid}, "1": []any{}, "2": []any{}, "3": []any{}},
			"right": map[string]any{"0": rightRole0, "1": []any{}, "2": []any{}, "3": []any{}},
		},
		"side": map[string]any{
			"left": map[string]any{
				"ready": opts.LeftReady, "authTeamId": myTid, "players": []any{myEntry},
				"squad": myTeam["squad"], "team": myTeam,
			},
			"center":  map[string]any{"players": []any{}},
			"right":   rightSide,
			"referee": map[string]any{"players": []any{}},
		},
		"members":     members,
		"matchmaking": mm,
	}
}

// CoachAcrossUsers finds a coach by id across all users (fx's
// _find_coach_across_users): returns its stored team + levelinfo.
func CoachAcrossUsers(users []*User, states map[int64]State, targetCID string) (map[string]any, map[string]any) {
	for _, u := range users {
		st := states[u.ID]
		if st == nil {
			continue
		}
		for _, cv := range List(st["coaches"]) {
			coach := Map(cv)
			if coach == nil || Str(coach["_id"]) != targetCID {
				continue
			}
			team := TeamOf(st, Int(coach["slot"]))
			return team, Map(coach["levelinfo"])
		}
	}
	return nil, nil
}
