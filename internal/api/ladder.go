package api

// Ladder read-side endpoints (Phase 2): my info, division table,
// reward info + reward board, season infos, leaderboard, friends.
// Ports of the fo3_ext.py handlers of the same paths.

import (
	"fmt"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func (s *Server) ladderMyInfo(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	stats := game.LadderStatistics(st, game.AllElos(c.Request.Context(), s.d.Store))
	stats["result"] = true
	writeJSON(c, stats)
}

func (s *Server) ladderDivisionInfo(c *gin.Context) {
	writeJSON(c, game.LadderDivisionsJSON())
}

func (s *Server) ladderRewardInfo(c *gin.Context) {
	divs := []any{}
	for i, d := range game.LadderDivisions {
		ep := (16 - i) * 100000
		if ep < 50000 {
			ep = 50000
		}
		divs = append(divs, map[string]any{
			"name": "DIVISION_" + d.Name,
			"items": []any{
				map[string]any{"itemid": 1, "count": ep},
				map[string]any{"itemid": 8334, "count": 1},
			},
		})
	}
	wins := []any{
		map[string]any{"from": 10, "to": 10, "items": []any{map[string]any{"itemid": 1, "count": 50000}}},
		map[string]any{"from": 7, "to": 9, "items": []any{map[string]any{"itemid": 1, "count": 30000}}},
		map[string]any{"from": 4, "to": 6, "items": []any{map[string]any{"itemid": 1, "count": 15000}}},
		map[string]any{"from": 1, "to": 3, "items": []any{map[string]any{"itemid": 1, "count": 5000}}},
	}
	writeJSON(c, map[string]any{"divs": divs, "wins": wins})
}

func (s *Server) ladderReward(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	slot := game.DefaultSlot(st)
	rk := game.RankedState(st, "ranked")
	elo := game.Int(rk["elo"])
	if rk["elo"] == nil {
		elo = game.LadderStartElo
	}
	divName, divNum := game.DivisionForElo(elo)
	wins, draws, losses := game.Int(rk["wins"]), game.Int(rk["draws"]), game.Int(rk["losses"])
	games := wins + draws + losses
	winrate := 0
	if games > 0 {
		winrate = int(float64(wins*100)/float64(games) + 0.5)
	}
	stats := game.LadderStatistics(st, game.AllElos(c.Request.Context(), s.d.Store))
	out := map[string]any{
		"result": true, "season": 202611,
		"rewards": []any{}, "items": []any{}, "ep": 0,
		"elo": elo, "points": elo, "highelo": game.Int(rk["highelo"]),
		"division":     map[string]any{"name": "DIVISION_" + divName, "rank": divNum},
		"divisionname": "DIVISION_" + divName,
		"wins": wins, "draws": draws, "losses": losses, "games": games,
		"winrate":        winrate,
		"ladder_pvp_1v1": stats["ladder_pvp_1v1"],
		"resources":      s.d.Roster.CoachMeResources(st, slot, user),
	}
	if len(game.Coaches(st)) == 0 {
		out["resources"] = []any{}
	}
	writeJSON(c, out)
}

// ladderSeasonInfo serves both season-info shapes (fx
// ep_ladder_season_info and ep_club_ranked_season_info differ in the
// "times" rows).
func (s *Server) ladderSeasonInfo(club bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		_, _, ok := s.loadState(c)
		if !ok {
			return
		}
		// Ladder season boundaries are admin settings (settings
		// doc "seasons"), independent of the cup season.
		endStr := game.SeasonDate("ladder", "end")
		startStr := game.SeasonDate("ladder", "start")
		remain := int64(86400 * 30)
		if t, err := time.Parse("2006-01-02", endStr); err == nil {
			remain = int64(time.Until(t).Seconds())
			if remain < 0 {
				remain = 0
			}
		}
		endISO := endStr + "T23:59:59Z"
		startISO := startStr + "T00:00:00Z"
		endDate := ""
		for _, ch := range endStr {
			if ch != '-' {
				endDate += string(ch)
			}
		}
		if len(endDate) != 8 {
			endDate = "20261231"
		}
		startDate := ""
		for _, ch := range startStr {
			if ch != '-' {
				startDate += string(ch)
			}
		}
		if len(startDate) != 8 {
			startDate = "20261101"
		}
		var times []any
		if club {
			for i := 0; i < 7; i++ {
				times = append(times, []any{map[string]any{"starttime": "0000", "endtime": "2359"}})
			}
		} else {
			times = []any{[]any{"00:00", "23:59"}}
		}
		season := map[string]any{
			"seasonid": 202611, "sid": 202611, "prevseasonid": 202610,
			"startedat": startISO, "rewardedat": endISO,
			"start": startISO, "end": endISO, "next": endISO,
			"startdate": startDate, "starttime": "000000",
			"enddate": endDate, "endtime": "235959",
			"remaintime": remain, "times": times,
		}
		writeJSON(c, map[string]any{
			"result": true, "sid": 202611, "prevsid": 202610,
			"start": startISO, "end": endISO, "next": endISO,
			"startdate": startDate, "starttime": "000000",
			"enddate": endDate, "endtime": "235959",
			"remaintime": remain, "season": season,
		})
	}
}

func (s *Server) ladderLeaderboard(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	users, _ := game.AllUsers(ctx, s.d.Store)
	type row struct {
		m map[string]any
	}
	rows := []map[string]any{}
	for _, u := range users {
		ust, err := game.LoadState(ctx, s.d.Store, u.ID)
		var rk map[string]any
		if err == nil && ust != nil {
			rk = game.RankedState(ust, "ranked")
		} else {
			rk = game.FreshRankedState()
		}
		elo := game.Int(rk["elo"])
		if rk["elo"] == nil {
			elo = game.LadderStartElo
		}
		divName, divNum := game.DivisionForElo(elo)
		name := u.CoachName
		if name == "" {
			name = u.Username
		}
		rows = append(rows, map[string]any{
			"rank": 0, "ranking": 0,
			"id":   fmt.Sprintf("%024x", u.ID),
			"name": name, "teamid": 1, "elo": elo,
			"division": map[string]any{"name": "DIVISION_" + divName, "rank": divNum},
			"wins":     game.Int(rk["wins"]), "draws": game.Int(rk["draws"]),
			"losses": game.Int(rk["losses"]), "forfeits": 0,
		})
	}
	if len(rows) == 0 {
		slot := game.DefaultSlot(st)
		coach := game.CoachBySlot(st, slot)
		rk := game.RankedState(st, "ranked")
		elo := game.Int(rk["elo"])
		if rk["elo"] == nil {
			elo = game.LadderStartElo
		}
		divName, divNum := game.DivisionForElo(elo)
		name, teamid, cid := "Manager", 1, game.CoachIDForUser(1, 0)
		if coach != nil {
			name = game.Str(coach["name"])
			teamid = game.Int(coach["teamid"])
			if teamid == 0 {
				teamid = 1
			}
			cid = game.Str(coach["_id"])
		}
		rows = append(rows, map[string]any{
			"rank": 1, "ranking": 1, "id": cid, "name": name, "teamid": teamid, "elo": elo,
			"division": map[string]any{"name": "DIVISION_" + divName, "rank": divNum},
			"wins":     game.Int(rk["wins"]), "draws": game.Int(rk["draws"]),
			"losses": game.Int(rk["losses"]), "forfeits": 0,
		})
	}
	bots := []struct {
		name string
		team int
		elo  int
		w, d, l int
	}{
		{"HLV Huyền Thoại", 10, 2150, 42, 5, 3},
		{"Siêu Sao sân cỏ", 11, 1850, 28, 4, 8},
		{"Chiến Thuật Gia", 22, 1620, 19, 6, 9},
		{"Vua Phá Lưới FO3", 5, 1480, 14, 3, 10},
		{"Tân Binh Xếp Hạng", 1, 1320, 8, 2, 7},
	}
	for _, b := range bots {
		divName, divNum := game.DivisionForElo(b.elo)
		rows = append(rows, map[string]any{
			"rank": 0, "ranking": 0, "id": game.NewIID(),
			"name": b.name, "teamid": b.team, "elo": b.elo,
			"division": map[string]any{"name": "DIVISION_" + divName, "rank": divNum},
			"wins":     b.w, "draws": b.d, "losses": b.l, "forfeits": 0,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ei, ej := game.Int(rows[i]["elo"]), game.Int(rows[j]["elo"])
		if ei != ej {
			return ei > ej
		}
		return game.Int(rows[i]["wins"]) > game.Int(rows[j]["wins"])
	})
	out := []any{}
	for i, r := range rows {
		r["rank"] = i + 1
		r["ranking"] = i + 1
		out = append(out, r)
	}
	writeJSON(c, map[string]any{
		"result": true, "list": out, "rankers": out, "myrank": 1,
		"Ladder_PvP_1v1": out, "Ladder_PvP_2v2": out, "Ladder_PvP_3v3": out,
		"Ladder_RolePlay_PvP_3v3": out, "Ladder_MGR_PvP_1v1": out,
		"ladder_pvp_1v1": out, "ladder_pvp_2v2": out, "ladder_pvp_3v3": out,
		"ladder_roleplay_pvp_3v3": out, "ladder_mgr_pvp_1v1": out,
	})
}

func (s *Server) ladderFriendsInfo(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	slot := game.DefaultSlot(st)
	myCoach := game.CoachBySlot(st, slot)
	myCID := ""
	if myCoach != nil {
		myCID = game.Str(myCoach["_id"])
	}
	friendIDs, _ := game.AcceptedFriendCoachIDs(ctx, s.d.Store, myCID)
	users, _ := game.AllUsers(ctx, s.d.Store)
	rows := []map[string]any{}
	for _, u := range users {
		if u.ID == user.ID {
			continue
		}
		ust, err := game.LoadState(ctx, s.d.Store, u.ID)
		if err != nil || ust == nil {
			continue
		}
		uslot := game.DefaultSlot(ust)
		ucoach := game.CoachBySlot(ust, uslot)
		cid := game.CoachIDForUser(u.ID, 0)
		teamid := 1
		if ucoach != nil {
			if v := game.Str(ucoach["_id"]); v != "" {
				cid = v
			}
			teamid = game.Int(ucoach["teamid"])
			if teamid == 0 {
				teamid = 1
			}
		}
		if !friendIDs[cid] {
			continue
		}
		rk := game.RankedState(ust, "ranked")
		rkm := game.RankedState(ust, "ranked_mgr")
		eloArr := []int{game.LadderStartElo, game.LadderStartElo, game.LadderStartElo, game.LadderStartElo, game.LadderStartElo, game.LadderStartElo}
		winsArr := make([]int, 6)
		lossArr := make([]int, 6)
		forfArr := make([]int, 6)
		eloArr[0] = eloOr(rk)
		eloArr[3] = eloOr(rkm)
		winsArr[0], winsArr[3] = game.Int(rk["wins"]), game.Int(rkm["wins"])
		lossArr[0], lossArr[3] = game.Int(rk["losses"]), game.Int(rkm["losses"])
		forfArr[0], forfArr[3] = game.Int(rk["forfeits"]), game.Int(rkm["forfeits"])
		ovr := map[string]any{"FW": 0, "MF": 0, "DF": 0}
		if uteam := game.TeamOf(ust, uslot); uteam != nil {
			sum := s.d.Roster.TeamSummaryOf(uteam, teamid)
			if o := game.Map(sum["ovr"]); o != nil {
				ovr = map[string]any{"FW": game.Int(o["FW"]), "MF": game.Int(o["MF"]), "DF": game.Int(o["DF"])}
			}
		}
		name := u.CoachName
		if name == "" {
			name = u.Username
		}
		rows = append(rows, map[string]any{
			"_id": cid, "name": name, "teamid": teamid, "ovr": ovr,
			"elo": eloArr, "wins": winsArr, "losses": lossArr, "forfeits": forfArr,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i]["elo"].([]int)[0] > rows[j]["elo"].([]int)[0]
	})
	out := []any{}
	for _, r := range rows {
		out = append(out, r)
	}
	writeJSON(c, out)
}

func eloOr(rk map[string]any) int {
	if rk["elo"] == nil {
		return game.LadderStartElo
	}
	return game.Int(rk["elo"])
}
