package game

// League 2 part 2: sponsors/missions, season leaders, round play,
// match construction, sim events/result, season end. Ports of the
// corresponding hc functions (see league.go for part 1).

import (
	"math"
	"sort"
	"time"
)

// Sponsors maps sponsorid -> mission id (hc.SPONSORS).
var Sponsors = map[string]string{"1": "2", "2": "3", "3": "4"}

// MissionTarget mirrors hc.MISSION_TARGET (LEAGUE2SPONSOR lv1).
var MissionTarget = map[string]int{"2": 3, "3": 3, "4": 2}

// MissionProgress ports hc.mission_progress.
func MissionProgress(st State, mid string) ([]any, bool) {
	lg := Map(st["league2"])
	league := Map(lg["league"])
	me := MyTeamID(st)
	type gm struct{ mine, opp int }
	goals := []gm{}
	mine := []map[string]any{}
	for _, rv := range List(league["matchrecords"]) {
		rec := Map(rv)
		if Int(rec["home"]) == me || Int(rec["away"]) == me {
			mine = append(mine, rec)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool { return Int(mine[i]["round"]) < Int(mine[j]["round"]) })
	for _, m := range mine {
		g := Map(m["goals"])
		if Int(m["home"]) == me {
			goals = append(goals, gm{Int(g["home"]), Int(g["away"])})
		} else {
			goals = append(goals, gm{Int(g["away"]), Int(g["home"])})
		}
	}
	bestRun := func(ok func(gm) bool) int {
		best, run := 0, 0
		for _, g := range goals {
			if ok(g) {
				run++
			} else {
				run = 0
			}
			if run > best {
				best = run
			}
		}
		return best
	}
	target := MissionTarget[mid]
	if target == 0 {
		target = 1
	}
	if mid == "2" {
		stats := Map(league["statistics"])
		rank := Int(Map(stats[itoa(me)])["rank"])
		doneSeason := len(mine) > Int(league["maxround"])
		return []any{rank}, doneSeason && rank > 0 && rank <= target
	}
	var value int
	if mid == "3" {
		value = bestRun(func(g gm) bool { return g.mine > g.opp })
	} else {
		value = bestRun(func(g gm) bool { return g.opp == 0 })
	}
	return []any{value}, value >= target
}

// SeasonLeaders ports hc.season_leaders.
func (r *Roster) SeasonLeaders(st State, me, rank, goals int) map[string]any {
	team := TeamOf(st, DefaultSlot(st))
	owned := r.OwnedTeam(team)
	rating := map[int]int{}
	for _, pv := range List(owned["players"]) {
		p := Map(pv)
		rating[Int(p["spid"])] = r.PlayerOvr(p)
	}
	lines := map[string][2]int{"GK": {0, 1}, "DF": {1, 9}, "MF": {9, 20}, "FW": {20, 28}}
	starters := []map[string]any{}
	if squad := Map(owned["squad"]); squad != nil {
		for _, pv := range List(squad["positions"]) {
			if pos := Map(pv); pos != nil && Int(pos["pos"]) < PosSub {
				starters = append(starters, pos)
			}
		}
	}
	entry := func(spid, ep int, value float64) map[string]any {
		return map[string]any{"spid": spid, "teamid": me, "value": value,
			"reward": map[string]any{"count": ep}}
	}
	out := map[string]any{}
	for line, rg := range lines {
		entries := []any{}
		for _, p := range starters {
			pos := Int(p["pos"])
			if pos >= rg[0] && pos < rg[1] {
				v := 6 + float64(rating[Int(p["spid"])]-80)/25
				if v > 9.9 {
					v = 9.9
				}
				entries = append(entries, entry(Int(p["spid"]), 0, roundHalfEven(v, 1)))
			}
		}
		out[line] = entries
	}
	best := func(line string) int {
		rg := lines[line]
		bestSpid, bestRating := 0, -1
		for _, p := range starters {
			pos := Int(p["pos"])
			if pos >= rg[0] && pos < rg[1] {
				if rv := rating[Int(p["spid"])]; rv > bestRating {
					bestRating, bestSpid = rv, Int(p["spid"])
				}
			}
		}
		return bestSpid
	}
	lg := Map(st["league2"])
	played := 2 * (len(intList(Map(lg["league"])["teams"])) - 1)
	if played < 0 {
		played = 0
	}
	out["ratings"] = []any{entry(best("FW"), 3000, 7.5)}
	out["goals"] = []any{entry(best("FW"), 3000, float64(max(1, goals/2)))}
	out["assists"] = []any{entry(best("MF"), 2000, float64(max(1, goals/3)))}
	out["tackles"] = []any{entry(best("DF"), 2000, float64(played * 3))}
	out["passes"] = []any{entry(best("MF"), 1000, 0.85)}
	out["saves"] = []any{entry(best("GK"), 1000, float64(played * 2))}
	return out
}

// LeaguePlayRound ports hc.league_play_round. mine != nil gives my
// fixture's (home, away) goals; otherwise it is simulated.
func (r *Roster) LeaguePlayRound(st State, rnd int, mine *[2]int, rng *pyRand) map[string]any {
	lg := Map(st["league2"])
	league := Map(lg["league"])
	if league == nil {
		return map[string]any{}
	}
	records := []map[string]any{}
	for _, rv := range List(league["matchrecords"]) {
		if rec := Map(rv); rec != nil {
			records = append(records, rec)
		}
	}
	roundMatches := []any{}
	for _, m := range records {
		if Int(m["round"]) == rnd {
			roundMatches = append(roundMatches, m)
		}
	}
	if len(roundMatches) > 0 {
		return map[string]any{"matchtype": "league", "round": rnd, "leaguematches": roundMatches}
	}
	if rng == nil {
		rng = newPyRand(0)
	}
	me := MyTeamID(st)
	teams := intList(league["teams"])
	played := []map[string]any{}
	for _, fx := range LeagueRound(teams, rnd) {
		if !fx.HomeOK || !fx.AwayOK {
			continue
		}
		var gh, ga int
		if mine != nil && (fx.Home == me || fx.Away == me) {
			gh, ga = mine[0], mine[1]
		} else {
			gh, ga = SimScore(rng, r.TeamStrength(st, fx.Home), r.TeamStrength(st, fx.Away))
		}
		played = append(played, map[string]any{"type": "league", "matchtype": "league",
			"round": rnd, "home": fx.Home, "away": fx.Away,
			"goals": map[string]any{"home": gh, "away": ga}})
	}
	all := append(records, played...)
	league["matchrecords"] = all
	league["statistics"] = LeagueStats(teams, all, Map(league["statistics"]))
	league["nextround"] = rnd + 1
	lg["laststatus"] = "league:" + itoa(rnd)
	out := []any{}
	for _, m := range played {
		out = append(out, m)
	}
	return map[string]any{"matchtype": "league", "round": rnd, "leaguematches": out}
}

// LeagueMatchBody ports hc.league_match_body.
func (r *Roster) LeagueMatchBody(st State, req map[string]any, playtype string, user *User) map[string]any {
	slot := DefaultSlot(st)
	me := r.CoachMe(st, slot, user)
	teamid := Int(me["teamid"])
	opp := Int(req["away"])
	if Int(req["home"]) != teamid {
		opp = Int(req["home"])
	} else {
		opp = Int(req["away"])
	}
	userObj := map[string]any{"id": me["id"], "name": me["name"], "levelinfo": me["levelinfo"]}
	lg := Map(st["league2"])
	mtype := "League_2_league"
	if playtype == "mgr" {
		mtype = "League_2_league_MGR"
	}
	tactics := r.TacticsOf(st)
	match := NewMatch(mtype,
		r.MatchSide(TeamOf(st, slot), teamid, userObj, tactics),
		r.MatchSide(r.RosterTeam(opp), opp, nil, nil), nil)
	ApplyBallAvailables(match, r.BallAvailables(st))
	gs := Map(match["gamesettings"])
	gs["home"] = Int(req["home"]) == teamid
	if gs["home"] == true {
		gs["kit"] = []any{"home", "away"}
	} else {
		gs["kit"] = []any{"away", "home"}
	}
	gs["difficulty"] = 10
	if lg != nil && lg["difficultyselect"] != nil {
		gs["difficulty"] = Int(lg["difficultyselect"])
	}
	matchtype := Str(req["matchtype"])
	if matchtype == "" {
		matchtype = "league"
	}
	match["leagueparam"] = map[string]any{"matchtype": matchtype, "round": Int(req["round"]),
		"home": Int(req["home"]), "away": Int(req["away"]), "leagueid": Int(lg["leagueid"])}
	return match
}

// SimEvents ports hc.sim_events.
func SimEvents(match map[string]any, mine, opp int, rng *pyRand) []any {
	side := Map(match["side"])
	xi := [2][][2]int{}
	keeper := [2]int{}
	for sIdx, sName := range []string{"left", "right"} {
		team := Map(Map(side[sName])["team"])
		squad := Map(team["squad"])
		for _, pv := range List(squad["positions"]) {
			pos := Map(pv)
			if Int(pos["pos"]) < PosSub {
				xi[sIdx] = append(xi[sIdx], [2]int{Int(pos["spid"]), Int(pos["pos"])})
			}
		}
		keeper[sIdx] = xi[sIdx][0][0]
		for _, e := range xi[sIdx] {
			if e[1] == 0 {
				keeper[sIdx] = e[0]
				break
			}
		}
	}
	outfield := func(team int, w [3]int) int {
		items, weights := []int{}, []int{}
		for _, e := range xi[team] {
			if e[1] == 0 {
				continue
			}
			weight := w[2]
			if e[1] <= 8 {
				weight = w[0]
			} else if e[1] <= 19 {
				weight = w[1]
			}
			items = append(items, e[0])
			weights = append(weights, weight)
		}
		return rng.ChoiceWeighted(items, weights)
	}
	events := []map[string]any{}
	add := func(minute, team, player int, ev string) {
		events = append(events, map[string]any{"time": minute, "team": team, "player": player, "event": ev})
	}
	for team, goals := range map[int]int{0: mine, 1: opp} {
		for i := 0; i < goals; i++ {
			minute, scorer := rng.Randint(1, 90), outfield(team, [3]int{1, 3, 6})
			if rng.Random() < 0.6 {
				add(minute, team, outfield(team, [3]int{2, 4, 2}), "ASSIST_EVENT")
			}
			add(minute, team, scorer, "GOAL_EVENT")
		}
	}
	for i, n := 0, rng.Randint(3, 7); i < n; i++ {
		team, minute := rng.Randint(0, 1), rng.Randint(1, 90)
		add(minute, team, outfield(team, [3]int{1, 3, 6}), "SHOT_ON_GOAL_EVENT")
		if rng.Random() < 0.6 {
			add(minute, 1-team, keeper[1-team], "SAVE_EVENT")
		}
	}
	for i, n := 0, rng.Randint(0, 3); i < n; i++ {
		team := rng.Randint(0, 1)
		add(rng.Randint(1, 90), team, outfield(team, [3]int{4, 3, 1}), "YELLOW_CARD_EVENT")
	}
	sort.SliceStable(events, func(i, j int) bool { return Int(events[i]["time"]) < Int(events[j]["time"]) })
	out := []any{}
	for _, e := range events {
		out = append(out, e)
	}
	return out
}

// SimResult ports hc.sim_result.
func SimResult(match map[string]any, events []any, myHome bool, rng *pyRand) map[string]any {
	pair := func(lo, hi int, rate float64) []any {
		n := rng.Randint(lo, hi)
		return []any{int(math.RoundToEven(float64(n) * rate)), n}
	}
	side := Map(match["side"])
	teams := []map[string]any{}
	for idx, sName := range []string{"left", "right"} {
		team := Map(Map(side[sName])["team"])
		squad := Map(team["squad"])
		players := []map[string]any{}
		for _, pv := range List(squad["positions"]) {
			pos := Map(pv)
			if Int(pos["pos"]) >= PosSub {
				continue
			}
			spid := Int(pos["spid"])
			count := func(ev string) int {
				n := 0
				for _, evv := range events {
					e := Map(evv)
					if Int(e["team"]) == idx && Int(e["player"]) == spid && Str(e["event"]) == ev {
						n++
					}
				}
				return n
			}
			goals, assists := count("GOAL_EVENT"), count("ASSIST_EVENT")
			shots, saves, yellow := count("SHOT_ON_GOAL_EVENT"), count("SAVE_EVENT"), count("YELLOW_CARD_EVENT")
			start := List(pos["starting"])
			sx, sy := 0.5, 0.5
			if len(start) >= 4 {
				sx, sy = Float(start[2]), Float(start[3])
			}
			rating := 6.0 + float64(goals) + 0.5*float64(assists) + 0.3*float64(saves) -
				0.4*float64(yellow) + rng.Uniform(-0.4, 0.9)
			if rating > 10 {
				rating = 10
			}
			if rating < 4 {
				rating = 4
			}
			shotAttempt := goals + shots
			if Int(pos["pos"]) != 0 && rng.Randint(0, 1) == 1 {
				shotAttempt++
			}
			var passes, dribbles, tackles []any
			if Int(pos["pos"]) != 0 {
				passes = pair(20, 50, rng.Uniform(0.7, 0.92))
				tackles = pair(0, 5, 0.6)
			} else {
				passes = pair(5, 15, 0.8)
				tackles = []any{0, 0}
			}
			dribbles = pair(0, 6, 0.6)
			run := rng.Uniform(3000, 5000)
			if Int(pos["pos"]) != 0 {
				run = rng.Uniform(8000, 12000)
			}
			players = append(players, map[string]any{
				"id": itoa(spid % 1000000), "spid": spid % 1000000, "dbid": spid % 1000000,
				"role": Int(pos["pos"]), "x": sx, "y": roundHalfEven(1-sy, 4),
				"rating": roundHalfEven(rating, 1),
				"goals": goals, "assists": assists, "saves": saves,
				"shotsOnGoal": goals + shots, "shotAttempt": shotAttempt,
				"passes": passes, "dribbles": dribbles, "tackles": tackles,
				"cards": []any{min(yellow, 1), boolToInt(yellow >= 2)}, "injure": []any{0, 0},
				"runDistanceMeter": roundHalfEven(run, 1), "longTermStamina": 1,
			})
		}
		total := func(key string) int {
			s := 0
			for _, p := range players {
				s += Int(p[key])
			}
			return s
		}
		sums := func(key string) []any {
			a, b := 0, 0
			for _, p := range players {
				l := List(p[key])
				if len(l) >= 2 {
					a += Int(l[0])
					b += Int(l[1])
				}
			}
			return []any{a, b}
		}
		reserved := []any{}
		for _, pv := range List(squad["positions"]) {
			pos := Map(pv)
			if Int(pos["pos"]) == PosSub {
				reserved = append(reserved, map[string]any{
					"id": itoa(Int(pos["spid"]) % 1000000), "rating": 0, "dbid": Int(pos["spid"]) % 1000000})
			}
		}
		ratingSum := 0.0
		runSum := 0.0
		for _, p := range players {
			ratingSum += Float(p["rating"])
			runSum += Float(p["runDistanceMeter"])
		}
		avg := 0.0
		if len(players) > 0 {
			avg = ratingSum / float64(len(players))
		}
		cardsSum := sums("cards")
		// The players list MUST be []any: every consumer reads it
		// back through game.List (the motm pick below, MatchReward's
		// EXP list, RecordLeagueMatchPlayers), which rejects typed
		// []map[string]any slices. Played matches arrive as JSON
		// ([]any) so only sims broke: null motm (#1009 on the stats
		// screen), an empty EXP list and no season player records.
		playersAny := make([]any, len(players))
		for i, p := range players {
			playersAny[i] = p
		}
		teams = append(teams, map[string]any{
			"id": Int(team["teamid"]), "score": total("goals"), "goals": total("goals"),
			"assists": total("assists"), "saves": total("saves"),
			"shotsOnGoal": total("shotsOnGoal"), "shotAttempt": total("shotAttempt"),
			"passes": sums("passes"), "dribbles": sums("dribbles"), "tackles": sums("tackles"),
			"cards": cardsSum, "crossPasses": pair(5, 20, 0.35), "throughPasses": pair(2, 10, 0.4),
			"blocks": pair(0, 5, 0.5), "fouls": Int(cardsSum[0]) + rng.Randint(2, 8),
			"offsides": rng.Randint(0, 3), "clearances": rng.Randint(5, 20), "intercepts": rng.Randint(5, 20),
			"tackles_inbox": 0, "passes_inbox": 0, "intercepts_inbox": 0, "dribble_inbox": 0,
			"rating": roundHalfEven(avg, 2), "runDistanceKm": roundHalfEven(runSum/1000, 1),
			"players": playersAny, "substitutes": []any{}, "users": []any{}, "shootout": 0,
			"injuredPlayers": 0, "reserved": reserved,
		})
	}
	share := rng.Randint(40, 60)
	for i, possession := range []int{share, 100 - share} {
		left := rng.Randint(15, 45)
		center := rng.Randint(20, 100-left-10)
		teams[i]["possession"] = map[string]any{
			"total": possession, "left": left, "center": center, "right": 100 - left - center,
			"df": rng.Randint(10, 30), "mf": rng.Randint(40, 60), "fw": rng.Randint(10, 30)}
	}
	mineName, oppName := "away", "home"
	if myHome {
		mineName, oppName = "home", "away"
	}
	sides := map[string]map[string]any{}
	sides[mineName] = teams[0]
	sides[oppName] = teams[1]
	bestTeam, bestPlayer, bestRating := "", map[string]any(nil), -1.0
	for _, name := range []string{"home", "away"} {
		for _, pv := range List(sides[name]["players"]) {
			p := Map(pv)
			if Float(p["rating"]) > bestRating {
				bestRating, bestTeam, bestPlayer = Float(p["rating"]), name, p
			}
		}
	}
	return map[string]any{
		"myTeam": mineName, "opponentTeam": oppName,
		"home": sides["home"], "away": sides["away"],
		"matchFinishedTime": 5400, "forfeits": 0,
		"motm": map[string]any{"team": bestTeam, "id": bestPlayer["id"], "spid": bestPlayer["spid"],
			"dbid": bestPlayer["dbid"], "rating": bestPlayer["rating"], "name": "XXDISPLAYNAMEXX"},
	}
}

// LeagueStartSeason ports ep_l2_calendar's state write.
func (r *Roster) LeagueStartSeason(st State, difficulty int, sponsor any) {
	teamid := MyTeamID(st)
	lid := Int(Map(r.RosterSummary(teamid)["league"])["id"])
	teams := r.LeagueTeamsList(lid, teamid)
	day := time.Now().Unix() / 86400 * 86400 * 1000
	st["league2"] = map[string]any{
		"teamid": teamid, "leagueid": lid, "difficultyselect": difficulty,
		"sponsor": sponsor, "laststatus": "opening", "openingday": day,
		"sponsoroffice": map[string]any{"season": map[string]any{"status": "started"}},
		"league": map[string]any{"teams": teams, "maxround": 2*(len(teams)-1) - 1,
			"nextround": 0, "matchrecords": []any{},
			"statistics": LeagueStats(teams, nil, nil)},
	}
}

// LeagueEndSeason ports ep_l2_end's computation (sponsorEP comes
// from the LEAGUE2REWARD table, read by the API layer).
func (r *Roster) LeagueEndSeason(st State, sponsorTableEP int) map[string]any {
	lg := Map(st["league2"])
	league := Map(lg["league"])
	if league == nil {
		return nil
	}
	me := MyTeamID(st)
	stats := Map(Map(league["statistics"])[itoa(me)])
	rank := Int(stats["rank"])
	cur := 10
	if lg["difficultyselect"] != nil {
		cur = Int(lg["difficultyselect"])
	}
	leagueSize := r.LeagueTeamCount(Int(lg["leagueid"]), intList(league["teams"]))
	nxt := cur
	if cur >= 20 {
		if rank <= 3 {
			nxt = min(24, cur+1)
		} else if rank > leagueSize-3 {
			nxt = max(20, cur-1)
		}
	} else if rank <= 3 {
		nxt = min(19, cur+1)
	}
	mid := Sponsors[fmt_Sprint(lg["sponsor"])]
	if mid == "" {
		mid = "2"
	}
	progress, done := MissionProgress(st, mid)
	sponsorEP := 0
	if done {
		sponsorEP = sponsorTableEP
	}
	leaders := r.SeasonLeaders(st, me, rank, Int(stats["goals"]))
	leaderEP := 0
	for _, k := range []string{"ratings", "goals", "assists", "tackles", "passes", "saves"} {
		for _, ev := range List(leaders[k]) {
			leaderEP += Int(Map(Map(ev)["reward"])["count"])
		}
	}
	baseEP := (leagueSize - rank + 1) * 2000
	seasonExp := (leagueSize - rank + 1) * 100
	coach := CoachBySlot(st, DefaultSlot(st))
	var cprev, ccur map[string]any
	epNow := int64(0)
	if coach != nil {
		cprev, ccur = GainExp(coach, seasonExp, true)
		epNow = Int64(coach["ep"])
	}
	out := map[string]any{
		"result":    true,
		"promotion": map[string]any{"current": cur, "next": nxt},
		"rewards": map[string]any{
			"coach": map[string]any{"prev": cprev, "current": ccur,
				"reward": map[string]any{"total": seasonExp}},
			"money": map[string]any{"prev": epNow,
				"reward": map[string]any{"total": baseEP + sponsorEP + leaderEP,
					"detail": map[string]any{"season": baseEP, "sponsor": sponsorEP, "leader": leaderEP}}}},
		"sponsorRewards": map[string]any{"sponsorid": fmt_Sprint(lg["sponsor"]), "level": 1,
			"achievement": done, "missions": []any{map[string]any{"mid": mid, "achievement": done, "progress": progress}}},
		"leaderRewards": leaders,
		"seasonRewards": map[string]any{"items": []any{}},
	}
	lg["difficultyselect"] = nxt
	lg["sponsoroffice"] = map[string]any{"season": map[string]any{"status": "finished"}}
	AddEP(st, int64(baseEP+sponsorEP+leaderEP))
	out["resources"] = r.L2Resources(st)
	return out
}

func fmt_Sprint(v any) string {
	if v == nil {
		return ""
	}
	switch n := v.(type) {
	case string:
		return n
	case float64:
		return itoa(int(n))
	default:
		return itoa(Int(v))
	}
}
