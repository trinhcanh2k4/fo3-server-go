package api

// Cup endpoints, ported from hc's ep_cup_* handlers plus fx's
// record push after cup new/leave.

import (
	"sort"
	"time"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func (s *Server) cupList(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ev := game.Map(st["events"])
	// Cup season boundaries are admin settings (settings doc
	// "seasons"); they intentionally no longer share the ladder's
	// end date.
	seasonEnd := game.SeasonDate("cup", "end")
	endISO := seasonEnd + "T23:59:59.000Z"
	startISO := game.SeasonDate("cup", "start") + "T00:00:00.000Z"
	active := map[string]bool{}
	if ev["active_cups"] != nil {
		for _, v := range game.List(ev["active_cups"]) {
			active[game.Str(v)] = true
		}
	} else {
		active = map[string]bool{"1": true, "2": true, "10001": true}
	}
	wcpve := strings.Contains(c.Request.URL.RawQuery, "type=wcpve")
	filtered := []any{}
	for _, m := range s.d.Roster.CupMetas() {
		mm := map[string]any{}
		for k, v := range m {
			mm[k] = v
		}
		mm["endat"] = endISO
		mm["startat"] = startISO
		if active[game.Str(m["metaid"])] {
			filtered = append(filtered, mm)
		}
	}
	if len(filtered) == 0 {
		for _, m := range s.d.Roster.CupMetas() {
			mm := map[string]any{}
			for k, v := range m {
				mm[k] = v
			}
			mm["endat"] = endISO
		mm["startat"] = startISO
			if !wcpve || game.Int(m["metaid"]) == game.WorldCupMetaID {
				filtered = append(filtered, mm)
			}
		}
	}
	if wcpve {
		out := []any{}
		for _, f := range filtered {
			if game.Int(game.Map(f)["metaid"]) == game.WorldCupMetaID {
				out = append(out, f)
			}
		}
		writeJSON(c, out)
		return
	}
	writeJSON(c, filtered)
}

func (s *Server) worldcup(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	var ongoing map[string]any
	for _, cup := range game.MapSlice(st["cups"]) {
		if game.Str(cup["mode"]) == "worldcup" && cup["finished"] != true {
			playing := false
			for _, cv := range game.List(cup["coaches"]) {
				if game.Str(game.Map(cv)["state"]) == "playing" {
					playing = true
				}
			}
			if playing {
				ongoing = cup
				break
			}
		}
	}
	pve := map[string]any{"ongoing": ongoing != nil}
	if ongoing != nil {
		pve["cupid"] = ongoing["_id"]
	}
	writeJSON(c, map[string]any{"_id": game.RealCoachID(st), "pve": pve,
		"pvp": map[string]any{"state": "idle", "coach": nil},
		"live": map[string]any{}, "livematch": nil})
}

func (s *Server) worldcupAuthenticTeam(c *gin.Context) {
	_, _, ok := s.loadState(c)
	if !ok {
		return
	}
	teamid, _ := strconv.Atoi(c.Param("teamid"))
	res := s.d.Roster.RosterRaw(teamid)
	rawTeam := game.Map(res["team"])
	if res == nil || rawTeam == nil || !s.d.Roster.IsNationalTeam(teamid) {
		writeJSON(c, map[string]any{"result": false, "reason": "select a valid national team"})
		return
	}
	team := s.d.Roster.OwnedTeam(rawTeam)
	captain := game.Str(team["captain"])
	if captain == "" {
		captain = game.Str(game.Map(rawTeam["squad"])["captain"])
	}
	team["teamId"] = teamid
	team["teamid"] = teamid
	team["captain"] = captain
	summary := game.Map(res["summary"])
	gradeF := game.Float(summary["teamgrade"])
	if summary["teamgrade"] == nil {
		gradeF = game.Float(summary["grade"])
	}
	if summary["teamgrade"] == nil && summary["grade"] == nil {
		gradeF = 3
	}
	grade := int(gradeF + 0.5)
	if grade < 0 {
		grade = 0
	}
	if grade > 5 {
		grade = 5
	}
	writeJSON(c, map[string]any{"team": team, "kit": game.TeamKit(teamid),
		"extra": map[string]any{"captain": captain, "fifarank": 0,
			"preliminary_result": "", "worldcup_results": "", "qualification": "",
			"coach": "", "grade": grade}})
}

func (s *Server) authenticTeam(c *gin.Context) {
	teamid, _ := strconv.Atoi(c.Param("teamid"))
	res := s.d.Roster.RosterRaw(teamid)
	out := map[string]any{}
	for k, v := range res {
		out[k] = v
	}
	// hc.ep_authentic_team merges club_summary_response OVER the
	// roster doc: full summary (ovr/teamgrade/numofselectable-
	// players), stadium and kit at the top level. The thin
	// RosterSummary left the WC creation carousel without the
	// kit/captain data it dresses the 3D players with.
	for k, v := range s.d.Roster.ClubSummaryResponse(teamid) {
		out[k] = v
	}
	out["team"] = s.d.Roster.OwnedTeam(game.Map(res["team"]))
	out["teamid"] = teamid
	writeJSON(c, out)
}

// pushCupRecord mirrors fx._push_cup_record (record BODY only).
func (s *Server) pushCupRecord(st game.State) {
	coach := game.CoachBySlot(st, game.DefaultSlot(st))
	if coach == nil {
		return
	}
	body := s.cupRecordBody(st)
	coachID, _ := myCoachID(st)
	game.MessengerPush("/cup/record", body, coachID)
}

// pushCoachMe pushes the refreshed /coach/me body after a cup join.
// Client mechanism (Module.swf, read 2026-11-13): CupProxy.cupPlayList
// is _coachCupInfo.cupPlayList, updated ONLY by CoachEvent
// ON_CUPS_CHANGED, which CoachController (a ResourceInvalidator)
// dispatches when the cached /coach/me resource's "cups" path changes.
// /cup/leave carries a /coach/me resource in its response, so the hub
// drops the tile instantly; /cup/new must NOT carry one — CupProxy.
// onCupCreateReceived iterates the create response's resources and
// throws #1010 on the foreign /coach/me resource (telemetry 92,
// 2026-11-13). A Messenger push updates the same cached resource
// without polluting the create response.
func (s *Server) pushCoachMe(st game.State, user *game.User) {
	if game.CoachBySlot(st, game.DefaultSlot(st)) == nil {
		return
	}
	body := s.d.Roster.CoachMe(st, game.DefaultSlot(st), user)
	coachID, _ := myCoachID(st)
	game.MessengerPush("/coach/me", body, coachID)
}

func (s *Server) cupRecordBody(st game.State) map[string]any {
	coach := game.CoachBySlot(st, game.DefaultSlot(st))
	r := game.CoachRecord(coach, "cup")
	bm := game.CupByMetaID(st, r)
	type entry struct {
		metaid int
		d      map[string]any
	}
	entries := []entry{}
	for m, dv := range bm {
		d := game.Map(dv)
		if game.Int(d["challenges"]) > 0 {
			mid, _ := strconv.Atoi(m)
			entries = append(entries, entry{mid, d})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].metaid < entries[j].metaid })
	records := []any{}
	for _, e := range entries {
		records = append(records, map[string]any{"metaid": e.metaid,
			"joincount": game.Int(e.d["joincount"]),
			"records": []any{map[string]any{"wins": game.Int(e.d["wins"]),
				"challenges": game.Int(e.d["challenges"])}}})
	}
	body := map[string]any{"_id": game.Str(coach["_id"]), "recordsbymetaid": records}
	for _, k := range []string{"totalchallenges", "totalwins", "totalprize", "abandonments"} {
		body[k] = game.Int(r[k])
	}
	if r["best"] != nil {
		body["highrecords"] = r["best"]
	}
	return body
}

func (s *Server) cupRecord(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	body := s.cupRecordBody(st)
	resources := []any{map[string]any{"resource": "/cup/record", "uri": "/cup/record", "body": body}}
	resources = append(resources, s.d.Roster.CoachMeResources(st, game.DefaultSlot(st), user)...)
	cups := game.Map(st["cups"])
	ids := []string{}
	for id := range cups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		cup := game.Map(cups[id])
		game.HealCupCoachIDs(st, cup)
		resources = append(resources, s.d.Roster.CupResources(cup)...)
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true, "resources": resources})
}

func (s *Server) cupNew(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	metaid := game.Int(req["metaid"])
	meta := s.d.Roster.CupMetaByID(metaid)
	if meta == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "invalid metaid"})
		return
	}
	var selected *int
	if metaid == game.WorldCupMetaID {
		ev := game.Map(st["events"])
		configured := game.Int(ev["worldcup_teamid"])
		sel := game.Int(req["teamid"])
		if s.d.Roster.IsNationalTeam(configured) {
			sel = configured
		} else if !s.d.Roster.IsNationalTeam(sel) {
			nid := game.Int(game.Map(s.d.Roster.RosterSummary(sel)["nation"])["id"])
			ntid := s.d.Roster.NationalTeamOfNation(nid)
			if s.d.Roster.IsNationalTeam(ntid) {
				sel = ntid
			} else {
				sel = 1362
			}
		}
		selected = &sel
	}
	for _, cup := range game.MapSlice(st["cups"]) {
		if game.Int(cup["metaid"]) == metaid && cup["finished"] != true {
			game.HealCupCoachIDs(st, cup)
			_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
			s.pushCupRecord(st)
			s.pushCoachMe(st, user)
			writeJSON(c, map[string]any{"result": true, "resources": s.d.Roster.CupResources(cup)})
			return
		}
	}
	cup := s.d.Roster.NewCup(st, meta, game.Int(req["difficulty"]), selected)
	coach := game.CoachBySlot(st, game.DefaultSlot(st))
	rec := game.CoachRecord(coach, "cup")
	bm := game.CupByMetaID(st, rec)
	e := game.Map(bm[strconv.Itoa(metaid)])
	if e == nil {
		e = map[string]any{"joincount": 0, "wins": 0, "challenges": 0}
		bm[strconv.Itoa(metaid)] = e
	}
	e["challenges"] = game.Int(e["challenges"]) + 1
	e["joincount"] = game.Int(e["joincount"]) + 1
	game.CupsOf(st)[game.Str(cup["_id"])] = cup
	rec["totalchallenges"] = game.Int(rec["totalchallenges"]) + 1
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	s.pushCupRecord(st)
	s.pushCoachMe(st, user)
	writeJSON(c, map[string]any{"result": true, "resources": s.d.Roster.CupResources(cup)})
}

// cupStep loads a cup or answers "not exist cupid" (cup_step).
func (s *Server) cupStep(c *gin.Context) (game.State, *game.User, map[string]any, bool) {
	st, user, ok := s.loadState(c)
	if !ok {
		return nil, nil, nil, false
	}
	cup := game.Map(game.Map(st["cups"])[c.Param("cupid")])
	if cup == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "not exist cupid"})
		return nil, nil, nil, false
	}
	game.HealCupCoachIDs(st, cup)
	return st, user, cup, true
}

func (s *Server) cupGet(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	cup := game.Map(game.Map(st["cups"])[c.Param("cupid")])
	if cup == nil {
		writeJSON(c, map[string]any{})
		return
	}
	writeJSON(c, cup)
}

func (s *Server) cupResume(c *gin.Context) {
	st, user, cup, ok := s.cupStep(c)
	if !ok {
		return
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true, "resources": s.d.Roster.CupResources(cup)})
}

func (s *Server) cupNext(c *gin.Context) {
	st, user, cup, ok := s.cupStep(c)
	if !ok {
		return
	}
	if cup["finished"] != true {
		var mine []int
		if m := game.List(cup["_mine"]); len(m) >= 2 {
			mine = []int{game.Int(m[0]), game.Int(m[1])}
			if len(m) >= 4 {
				mine = append(mine, game.Int(m[2]), game.Int(m[3]))
			}
		}
		delete(cup, "_mine")
		s.d.Roster.CupPlayRound(st, cup, mine)
		game.CupRefreshStats(cup)
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true, "resources": s.d.Roster.CupResources(cup)})
}

func (s *Server) cupLeave(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	cupid := c.Param("cupid")
	cups := game.CupsOf(st)
	cup := game.Map(cups[cupid])
	ep := 0
	if cup != nil {
		ep = s.d.Roster.CupRewardEP(cup)
	}
	game.AddEP(st, int64(ep))
	if cup != nil {
		delete(cups, cupid)
		coach := game.CoachBySlot(st, game.DefaultSlot(st))
		r := game.CoachRecord(coach, "cup")
		state := game.Str(game.Map(game.List(cup["coaches"])[0])["state"])
		if state == "playing" {
			r["abandonments"] = game.Int(r["abandonments"]) + 1
		}
		bm := game.CupByMetaID(st, r)
		e := game.Map(bm[strconv.Itoa(game.Int(cup["metaid"]))])
		if e == nil {
			e = map[string]any{"joincount": 0, "wins": 0, "challenges": 0}
			bm[strconv.Itoa(game.Int(cup["metaid"]))] = e
		}
		if state == "won" {
			e["wins"] = game.Int(e["wins"]) + 1
			r["totalwins"] = game.Int(r["totalwins"]) + 1
			wins := game.List(r["cupwins"])
			for len(wins) < game.CupLevels {
				wins = append(wins, 0)
			}
			wins = wins[:game.CupLevels]
			diff := game.Int(cup["difficulty"])
			if diff < 0 {
				diff = 0
			}
			if diff > game.CupLevels-1 {
				diff = game.CupLevels - 1
			}
			wins[diff] = game.Int(wins[diff]) + 1
			r["cupwins"] = wins
		}
		r["totalprize"] = game.Int(r["totalprize"]) + ep
		best := game.Map(r["best"])
		if ep > game.Int(best["prize"]) {
			r["best"] = map[string]any{"prize": ep, "metaid": cup["metaid"],
				"date": game.W3C(float64(time.Now().Unix()))}
		}
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	rewards := []any{}
	if ep > 0 {
		rewards = []any{map[string]any{"itemid": 1, "count": ep}}
	}
	out := map[string]any{"result": true, "rewards": rewards}
	if cup != nil {
		out["resources"] = s.d.Roster.CoachMeResources(st, game.DefaultSlot(st), user)
	}
	s.pushCupRecord(st)
	writeJSON(c, out)
}

func (s *Server) cupMatch(c *gin.Context) {
	st, user, cup, ok := s.cupStep(c)
	if !ok {
		return
	}
	g, leg := game.CupMyPair(cup)
	if leg == nil || leg["score"] != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "already played match"})
		return
	}
	slot := game.DefaultSlot(st)
	me := s.d.Roster.CoachMe(st, slot, user)
	worldcup := game.Str(cup["mode"]) == "worldcup"
	teamid := game.Int(me["teamid"])
	if worldcup {
		teamid = game.Int(cup["selectedteamid"])
		if teamid == 0 {
			teamid = game.Int(me["teamid"])
		}
	}
	teams := game.List(g["teams"])
	home := game.Int(game.Map(teams[game.Int(leg["homeidx"])])["teamid"])
	away := game.Int(game.Map(teams[game.Int(leg["awayidx"])])["teamid"])
	opp := away
	if home == teamid {
		opp = away
	} else {
		opp = home
	}
	userObj := map[string]any{"id": me["id"], "name": me["name"], "levelinfo": me["levelinfo"]}
	var myTeam map[string]any
	if worldcup {
		myTeam = s.d.Roster.RosterTeam(teamid)
	} else {
		myTeam = game.TeamOf(st, slot)
	}
	mtype := "Cup_PvE"
	if worldcup {
		mtype = "WorldCup_PvE"
	}
	match := game.NewMatch(mtype,
		s.d.Roster.MatchSide(myTeam, teamid, userObj, s.d.Roster.TacticsOf(st)),
		s.d.Roster.MatchSide(s.d.Roster.RosterTeam(opp), opp, nil, nil), nil)
	game.ApplyBallAvailables(match, s.d.Roster.BallAvailables(st))
	gs := game.Map(match["gamesettings"])
	gs["home"] = home == teamid
	if home == teamid {
		gs["kit"] = []any{"home", "away"}
	} else {
		gs["kit"] = []any{"away", "home"}
	}
	gs["difficulty"] = game.Int(cup["difficulty"])
	cur := game.Map(cup["current"])
	phase := game.Map(game.List(cup["phases"])[game.Int(cur["phase"])])
	if game.Str(phase["system"]) == "knock-out" {
		gs["drawMode"] = 2
	}
	st["match"] = match
	st["cup_pending"] = map[string]any{"id": match["id"], "cupid": c.Param("cupid"),
		"home": home, "away": away}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"resource": "/match/" + game.Str(match["id"]),
			"uri": "/match/" + game.Str(match["id"]), "body": match}}})
}
