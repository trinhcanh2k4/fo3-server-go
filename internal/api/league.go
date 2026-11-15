package api

// League 2 endpoints, ported from hc's ep_l2_* handlers.

import (
	"encoding/json"
	"log"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
	"fo3server/internal/store"
)

func (s *Server) leagueMe(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	changed := s.d.Roster.RepairLeagueContext(st)
	if !changed {
		changed = game.SeedLatestLeaguePlayerStats(st)
	}
	if changed {
		_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	}
	writeJSON(c, s.d.Roster.L2Me(st))
}

func (s *Server) leagueReward(c *gin.Context) {
	out := []any{}
	for i := 0; i < 15; i++ {
		out = append(out, map[string]any{"ep": 20000 * (i + 1), "items": []any{}})
	}
	writeJSON(c, out)
}

func (s *Server) leagueSponsor(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	teamid := game.MyTeamID(st)
	leagueid := intOf(game.Map(s.d.Roster.RosterSummary(teamid)["league"])["id"])
	count := s.d.Roster.LeagueTeamCount(leagueid, nil)
	sponsors := []any{}
	for _, sid := range []string{"1", "2", "3"} {
		sponsors = append(sponsors, map[string]any{"_id": sid, "sponsorid": sid,
			"missions": []any{map[string]any{"mid": game.Sponsors[sid]}}})
	}
	writeJSON(c, map[string]any{"result": true, "level": 1,
		"maxround": 2*(count-1) - 1, "sponsors": sponsors})
}

func (s *Server) leagueSponsorAchievement(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	lg := game.Map(st["league2"])
	mid := game.Sponsors[game.Str(lg["sponsor"])]
	if mid == "" {
		mid = "2"
	}
	progress, done := game.MissionProgress(st, mid)
	writeJSON(c, map[string]any{"result": true, "level": 1,
		"missions": []any{map[string]any{"mid": mid, "achievement": done, "progress": progress}}})
}

func (s *Server) leagueHistoryPlayers(c *gin.Context) {
	kind := c.Param("kind")
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	if kind != "league" {
		writeJSON(c, []any{})
		return
	}
	if game.SeedLatestLeaguePlayerStats(st) {
		_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	}
	writeJSON(c, game.LeagueRecordRows(st))
}

// leagueBack ports hc's routing of /league2/back to ep_telemetry:
// a fire-and-forget ack when leaving the league screen.
func (s *Server) leagueBack(c *gin.Context) {
	writeJSON(c, map[string]any{"result": true})
}

func (s *Server) leagueLeader(c *gin.Context) {
	kind := c.Param("kind")
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	if kind == "cup" {
		writeJSON(c, map[string]any{"FW": []any{}, "MF": []any{}, "DF": []any{}, "GK": []any{},
			"ratings": []any{}, "goals": []any{}, "assists": []any{}, "tackles": []any{},
			"passes": []any{}, "saves": []any{}})
		return
	}
	if game.SeedLatestLeaguePlayerStats(st) {
		_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	}
	lg := game.Map(st["league2"])
	league := game.Map(lg["league"])
	stats := game.Map(league["statistics"])
	me := game.MyTeamID(st)
	myStats := game.Map(stats[strconv.Itoa(me)])
	writeJSON(c, s.d.Roster.SeasonLeaders(st, me, intOf(myStats["rank"]), intOf(myStats["goals"])))
}

func (s *Server) leagueCalendar(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	difficulty := intOf(req["difficulty"])
	if difficulty == 0 {
		difficulty = 10
	}
	s.d.Roster.LeagueStartSeason(st, difficulty, req["sponsor"])
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true, "resources": s.d.Roster.L2Resources(st)})
}

func (s *Server) leagueSleep(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	param := map[string]any{}
	if lg := game.Map(st["league2"]); lg != nil && game.Map(lg["league"]) != nil {
		param = s.d.Roster.LeaguePlayRound(st, intOf(req["round"]), nil, nil)
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true, "leagueparam": param,
		"resources": s.d.Roster.L2Resources(st)})
}

func (s *Server) leagueAbandon(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	lg := game.Map(st["league2"])
	difficulty := 10
	if lg != nil && lg["difficultyselect"] != nil {
		difficulty = intOf(lg["difficultyselect"])
	}
	st["league2"] = map[string]any{"difficultyselect": difficulty}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true, "resources": s.d.Roster.L2Resources(st)})
}

// sponsorRewardEP reads responses/static_db_LEAGUE2REWARD.json:
// sponsor[str(int(mid)-1)][str(difficulty)].count.
func (s *Server) sponsorRewardEP(mid string, difficulty int) int {
	b, err := store.ReadFileCached(filepath.Join(s.d.ResponsesDir, "static_db_LEAGUE2REWARD.json"))
	if err != nil {
		return 0
	}
	var doc map[string]any
	if json.Unmarshal(b, &doc) != nil {
		return 0
	}
	midNum, _ := strconv.Atoi(mid)
	byMid := game.Map(game.Map(doc["sponsor"])[strconv.Itoa(midNum-1)])
	return intOf(game.Map(byMid[strconv.Itoa(difficulty)])["count"])
}

func (s *Server) leagueEnd(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	lg := game.Map(st["league2"])
	if game.Map(lg["league"]) == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "LEAGUE_FAIL"})
		return
	}
	mid := game.Sponsors[game.Str(lg["sponsor"])]
	if mid == "" {
		mid = "2"
	}
	cur := 10
	if lg["difficultyselect"] != nil {
		cur = intOf(lg["difficultyselect"])
	}
	out := s.d.Roster.LeagueEndSeason(st, s.sponsorRewardEP(mid, cur))
	if out == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "LEAGUE_FAIL"})
		return
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, out)
}

func (s *Server) leagueMatch(sim bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		st, user, ok := s.loadState(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		var req map[string]any
		_ = c.ShouldBindJSON(&req)
		lg := game.Map(st["league2"])
		if lg == nil || game.Map(lg["league"]) == nil {
			writeJSON(c, map[string]any{"result": false, "reason": "LEAGUE_FAIL"})
			return
		}
		coach := game.CoachBySlot(st, game.DefaultSlot(st))
		var ticket map[string]any
		ticketIdx := -1
		if sim && coach != nil {
			for i, iv := range game.List(coach["items"]) {
				if it := game.Map(iv); it != nil && game.Int(it["itemid"]) == 2001 && game.Int(it["remains"]) > 0 {
					ticket, ticketIdx = it, i
					break
				}
			}
			if ticket == nil {
				writeJSON(c, map[string]any{"result": false, "reason": "ITEM_USE_ERROR"})
				return
			}
		}
		match := s.d.Roster.LeagueMatchBody(st, req, game.Str(req["playtype"]), user)
		st["match"] = match
		lg["pending"] = map[string]any{"id": match["id"], "round": intOf(req["round"]),
			"home": intOf(req["home"]), "away": intOf(req["away"])}
		out := map[string]any{"result": true}
		var events []any
		if sim {
			param := s.d.Roster.LeaguePlayRound(st, intOf(req["round"]), nil, nil)
			me := game.MyTeamID(st)
			var mine map[string]any
			for _, mv := range game.List(param["leaguematches"]) {
				if m := game.Map(mv); m != nil && (game.Int(m["home"]) == me || game.Int(m["away"]) == me) {
					mine = m
					break
				}
			}
			match["stage"] = "closed"
			match["simulated"] = true
			game.Map(match["gamesettings"])["simulated"] = true
			events = []any{}
			if mine != nil {
				isHome := intOf(req["home"]) == me
				gh := game.Int(game.Map(mine["goals"])["home"])
				ga := game.Int(game.Map(mine["goals"])["away"])
				mineGoals, oppGoals := ga, gh
				if isHome {
					mineGoals, oppGoals = gh, ga
				}
				rng := game.NewPyRand(game.MatchSeed(match))
				events = game.SimEvents(match, mineGoals, oppGoals, rng)
				game.Map(match["gamesettings"])["prevScore"] = map[string]any{"home": gh, "away": ga}
				match["result"] = game.SimResult(match, events, isHome, rng)
				game.RecordLeagueMatchPlayers(st, game.Map(match["result"]), "league:"+strconv.Itoa(intOf(req["round"])))
				match["reward"] = s.d.Roster.MatchReward(st, game.Map(match["result"]))
				money := game.Map(game.Map(match["reward"])["money"])
				epAmt := int64(intOf(game.Map(money["reward"])["total"]))
				if granted, capped := game.ClampMatchDailyEP(ctx, s.d.Store, user.ID, epAmt); capped {
					game.Audit(ctx, s.d.Store, "system", "settle.ep_capped", game.Str(match["id"]),
						map[string]any{"uid": user.ID, "wanted": epAmt, "granted": granted,
							"cap": game.CfgI("match_daily_ep_cap", 0), "kind": "league_sim"})
					epAmt = granted
					game.Map(money["reward"])["total"] = int(epAmt)
				}
				game.AddEP(st, epAmt)
			}
			out["leagueparam"] = param
			out["eventlist"] = events
			if mine != nil {
				out["score"] = mine["goals"]
			} else {
				out["score"] = nil
			}
			ticket["remains"] = game.Int(ticket["remains"]) - 1
			if game.Int(ticket["remains"]) == 0 && coach != nil && ticketIdx >= 0 {
				items := game.List(coach["items"])
				coach["items"] = append(items[:ticketIdx], items[ticketIdx+1:]...)
			}
			game.ConsumeMatchContracts(st)
			game.ConsumeStaffContracts(st)
		}
		_ = game.SaveState(ctx, s.d.Store, user.ID, st)
		// TEMP PROBE (2026-11-15, remove after the league-record
		// diagnosis): the owner's real league settlement recorded
		// nothing while every synthetic battery passes — capture
		// exactly what a real /league2/match stored.
		log.Printf("l2m-probe: uid=%d mid=%v pending=%v reqround=%v reqhome=%v reqaway=%v",
			user.ID, match["id"], lg["pending"], req["round"], req["home"], req["away"])
		resources := []any{map[string]any{"uri": "/match/" + game.Str(match["id"]), "body": match}}
		if sim {
			resources = append(resources, s.d.Roster.L2Resources(st)...)
			resources = append(resources, s.d.Roster.CoachMeResources(st, game.DefaultSlot(st), user)...)
		}
		out["resources"] = resources
		writeJSON(c, out)
	}
}

func intOf(v any) int { return game.Int(v) }
