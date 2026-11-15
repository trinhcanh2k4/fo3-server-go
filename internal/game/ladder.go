package game

// Ladder (ranked) read-side, ported from fo3_ext.py section 3:
// _coach_ladder_statistics, _ladder_mode_record, _legacy_mode_record,
// _recent_served, division table + season/reward/leaderboard data.

import (
	"fmt"
	"time"
)

const LadderStartElo = 1000

// FreshRankedState ports fx._fresh_ranked_state.
func FreshRankedState() map[string]any {
	return map[string]any{
		"elo": LadderStartElo, "highelo": LadderStartElo,
		"wins": 0, "losses": 0, "draws": 0, "streak": 0,
		"season": 202611, "reward_checked": true,
		"results": []any{}, "forfeits": 0, "weekwins": map[string]any{},
		"recent": []any{}, "elos": []any{},
	}
}

// RankedState returns st[key] ("ranked" / "ranked_mgr"), creating the
// fresh record when absent (fx._get_ranked_state setdefault).
func RankedState(st State, key string) map[string]any {
	if rk := Map(st[key]); rk != nil {
		return rk
	}
	rk := FreshRankedState()
	st[key] = rk
	return rk
}

func rankWinrate(wins, total int) int {
	if total <= 0 {
		return 0
	}
	return int(float64(wins*100)/float64(total) + 0.5)
}

// LadderModeRecord ports fx._ladder_mode_record.
func LadderModeRecord(rk map[string]any, rank int) map[string]any {
	elo := Int(rk["elo"])
	if rk["elo"] == nil {
		elo = LadderStartElo
	}
	highelo := maxInt(elo, Int(rk["highelo"]))
	divName, divNum := DivisionForElo(elo)
	wins, draws, losses := Int(rk["wins"]), Int(rk["draws"]), Int(rk["losses"])
	total := wins + draws + losses
	firstWins := []any{}
	for _, r := range List(rk["results"]) {
		if len(firstWins) >= 5 {
			break
		}
		firstWins = append(firstWins, Int(r) == 1)
	}
	return map[string]any{
		"elo": elo, "highelo": highelo,
		"division":      map[string]any{"name": "DIVISION_" + divName, "rank": divNum},
		"rank":          rank,
		"ranking":       rank,
		"wins":          wins, "draws": draws, "losses": losses,
		"forfeits":      Int(rk["forfeits"]),
		"winningstreak": Int(rk["streak"]),
		"games":         total, "mingames": 5,
		"winrate": rankWinrate(wins, total),
		"firstround": map[string]any{
			"ongoing": total < 5, "wins": firstWins,
			"beforeelo": LadderStartElo, "afterelo": elo,
		},
	}
}

// LegacyModeRecord ports fx._legacy_mode_record.
func LegacyModeRecord(rk map[string]any) map[string]any {
	elo := Int(rk["elo"])
	if rk["elo"] == nil {
		elo = LadderStartElo
	}
	highelo := maxInt(elo, Int(rk["highelo"]))
	divName, divNum := DivisionForElo(elo)
	bestName, bestNum := DivisionForElo(highelo)
	wins, draws, losses := Int(rk["wins"]), Int(rk["draws"]), Int(rk["losses"])
	total := wins + draws + losses
	return map[string]any{
		"elo": elo, "highelo": highelo,
		"division": "DIVISION_" + divName, "divisionnum": divNum,
		"bestdivision": "DIVISION_" + bestName, "bestdivisionnum": bestNum,
		"wins": wins, "draws": draws, "losses": losses,
		"forfeits": Int(rk["forfeits"]),
		"win":     wins, "draw": draws, "lose": losses,
		"games": total, "mingames": 5,
		"winrate": rankWinrate(wins, total),
		"ranking": 1, "rank": 1, "rankpercent": 1,
		"winningstreak": Int(rk["streak"]),
		"points":        wins*3 + draws,
	}
}

// RecentServed ports fx._recent_served.
func RecentServed(rk map[string]any) []any {
	raw := List(rk["recent"])
	if len(raw) > 0 {
		if first := Map(raw[0]); first != nil {
			if _, hasGoals := first["goals"]; hasGoals {
				if len(raw) > 3 {
					raw = raw[len(raw)-3:]
				}
				out := []any{}
				for _, e := range raw {
					out = append(out, e)
				}
				return out
			}
		}
	}
	byElo := map[int]string{}
	for _, hraw := range List(rk["elos"]) {
		h := Map(hraw)
		d := Str(h["dateymd"])
		if len(d) == 8 {
			elo := Int(h["elo"])
			if _, ok := byElo[elo]; !ok {
				byElo[elo] = fmt.Sprintf("%s-%s-%sT00:00:00.000Z", d[:4], d[4:6], d[6:8])
			}
		}
	}
	out := []any{}
	streak := 0
	// legacy storage is newest first: iterate reversed
	for i := len(raw) - 1; i >= 0; i-- {
		e := Map(raw[i])
		if e == nil || e["draw"] == true {
			continue
		}
		won := e["win"] == true
		if won {
			if streak > 0 {
				streak++
			} else {
				streak = 1
			}
		} else {
			if streak < 0 {
				streak--
			} else {
				streak = -1
			}
		}
		sc := List(e["score"])
		var gf, ga int
		if len(sc) >= 2 {
			gf, ga = Int(sc[0]), Int(sc[1])
		}
		date := byElo[Int(e["elo"])]
		if date == "" {
			date = "2026-11-01T00:00:00.000Z"
		}
		forfeits := 0
		if e["forfeit"] == true {
			forfeits = 1
		}
		out = append(out, map[string]any{
			"date": date, "goals": gf, "goalsconceded": ga,
			"shootout": 0, "shootoutconceded": 0, "streak": streak,
			"elodiff": Int(e["delta"]), "elo": Int(e["elo"]), "forfeits": forfeits,
		})
	}
	if len(out) > 3 {
		out = out[len(out)-3:]
	}
	return out
}

func eloOfRk(rk map[string]any) int {
	if rk["elo"] == nil {
		return LadderStartElo
	}
	return Int(rk["elo"])
}

func highOfRk(rk map[string]any, elo int) int {
	if rk["highelo"] == nil {
		return elo
	}
	return maxInt(elo, Int(rk["highelo"]))
}

// LadderStatistics ports fx._coach_ladder_statistics. allElos carries
// every account's (1v1 elo, manager elo) for the board positions.
func LadderStatistics(st State, allElos [][2]int) map[string]any {
	rk := RankedState(st, "ranked")
	rk3 := RankedState(st, "ranked_mgr")
	elo, highelo := eloOfRk(rk), highOfRk(rk, eloOfRk(rk))
	divName, _ := DivisionForElo(elo)
	bestName, bestNum := DivisionForElo(highelo)
	wins, draws, losses := Int(rk["wins"]), Int(rk["draws"]), Int(rk["losses"])
	total := wins + draws + losses
	streak := Int(rk["streak"])
	bestDivObj := map[string]any{"name": "DIVISION_" + bestName, "rank": bestNum}
	elo3, highelo3 := eloOfRk(rk3), highOfRk(rk3, eloOfRk(rk3))
	best3Name, best3Num := DivisionForElo(highelo3)
	best3Obj := map[string]any{"name": "DIVISION_" + best3Name, "rank": best3Num}
	wins3, draws3, losses3 := Int(rk3["wins"]), Int(rk3["draws"]), Int(rk3["losses"])
	total3 := wins3 + draws3 + losses3
	streak3 := Int(rk3["streak"])
	_, startRank := DivisionForElo(LadderStartElo)
	startDivObj := map[string]any{"name": "DIVISION_AMATEUR_C", "rank": startRank}

	rank0, rank3 := 0, 0
	if total > 0 {
		rank0 = 1
		for _, e := range allElos {
			if e[0] > elo {
				rank0++
			}
		}
	}
	if total3 > 0 {
		rank3 = 1
		for _, e := range allElos {
			if e[1] > elo3 {
				rank3++
			}
		}
	}
	ranks := []int{rank0, 0, 0, rank3, 0, 0}
	elosI := []int{elo, LadderStartElo, LadderStartElo, elo3, LadderStartElo, LadderStartElo}
	highsI := []int{highelo, LadderStartElo, LadderStartElo, highelo3, LadderStartElo, LadderStartElo}

	eloHistory := func(src map[string]any, defElo int) []any {
		out := []any{}
		for _, hraw := range List(src["elos"]) {
			h := Map(hraw)
			e := defElo
			if h["elo"] != nil {
				e = Int(h["elo"])
			}
			out = append(out, map[string]any{"elo": e, "dateymd": Str(h["dateymd"])})
		}
		return out
	}
	divArr := []any{}
	highArr := []any{}
	for i := 0; i < 6; i++ {
		n, _ := DivisionForElo(elosI[i])
		divArr = append(divArr, map[string]any{"name": "DIVISION_" + n, "rank": ranks[i]})
		highArr = append(highArr, map[string]any{"elo": highsI[i], "rank": ranks[i]})
	}
	seasonEntry := map[string]any{
		"sid": "202611",
		"elos": []any{eloHistory(rk, elo), []any{}, []any{}, eloHistory(rk3, elo3), []any{}, []any{}},
		"wins":     []int{wins, 0, 0, wins3, 0, 0},
		"losses":   []int{losses, 0, 0, losses3, 0, 0},
		"forfeits": []int{Int(rk["forfeits"]), 0, 0, Int(rk3["forfeits"]), 0, 0},
		"swins":    []int{0, 0, 0, 0, 0, 0},
		"slosses":  []int{0, 0, 0, 0, 0, 0},
		"rank":     ranks,
		"division": divArr,
		"highrecord": highArr,
		"recentmatches": []any{RecentServed(rk), []any{}, []any{}, RecentServed(rk3), []any{}, []any{}},
	}
	zeroDivs := []any{}
	zeroHigh := []any{}
	emptyRecent := []any{}
	for i := 0; i < 6; i++ {
		zeroDivs = append(zeroDivs, map[string]any{"name": "DIVISION_AMATEUR_C", "rank": 0})
		zeroHigh = append(zeroHigh, map[string]any{"elo": LadderStartElo, "rank": 0})
		emptyRecent = append(emptyRecent, []any{})
	}
	prevEntry := map[string]any{
		"sid": "202610", "rewarded": true,
		"elos": emptyRecent, "wins": []int{0, 0, 0, 0, 0, 0}, "losses": []int{0, 0, 0, 0, 0, 0},
		"forfeits": []int{0, 0, 0, 0, 0, 0}, "swins": []int{0, 0, 0, 0, 0, 0}, "slosses": []int{0, 0, 0, 0, 0, 0},
		"rank": []int{0, 0, 0, 0, 0, 0}, "division": zeroDivs, "highrecord": zeroHigh,
		"recentmatches": emptyRecent,
	}
	modeRecord := LegacyModeRecord(rk)
	mgrRecord := LegacyModeRecord(rk3)
	ladderMode := LadderModeRecord(rk, rank0)
	mgrMode := LadderModeRecord(rk3, rank3)
	freshMode := LadderModeRecord(FreshRankedState(), 0)
	endDate := SeasonDate("ladder", "end")
	startDate := SeasonDate("ladder", "start")
	endISO := endDate + "T23:59:59Z"
	out := map[string]any{
		"sid": "202611", "prevsid": 202610, "season": 202611, "lastseason": 202610,
		"prevseason": 202610, "year": 2026, "month": 11,
		"title": "Season 2026", "title2": "Season 2026",
		"start": startDate + "T00:00:00Z", "next": endISO, "end": endISO,
		"rewardchecked": true, "hasreward": false,
		"elo": elo, "highelo": highelo,
		"division": "DIVISION_" + divName, "bestdivision": "DIVISION_" + bestName,
		"ladder_pvp_1v1": ladderMode, "ladder_pvp_2v2": freshMode,
		"ladder_pvp_3v3": freshMode, "ladder_roleplay_pvp_3v3": freshMode,
		"ladder_mgr_pvp_1v1": mgrMode, "ladder_mgr_versus": mgrMode,
		"seasons": []any{seasonEntry, prevEntry},
		"highrecords": map[string]any{
			"count": total + total3, "elo": highsI,
			"division": []any{bestDivObj, startDivObj, startDivObj, best3Obj, startDivObj, startDivObj},
			"rank":     []int{0, 0, 0, 0, 0, 0},
			"winningstreak": []int{streak, 0, 0, streak3, 0, 0},
			"wins":   []int{wins, 0, 0, wins3, 0, 0},
			"losses": []int{losses, 0, 0, losses3, 0, 0},
			"date":   startDate + "T00:00:00.000Z",
		},
		"1on1": modeRecord, "2on2": modeRecord, "3on3": modeRecord,
		"4on4": modeRecord, "5on5": modeRecord, "mgr": mgrRecord, "custom": modeRecord,
	}
	if total+total3 > 0 {
		y, w := time.Now().ISOWeek()
		wkNow := fmt.Sprintf("%d%02d", y, w)
		weekWins := 0
		for _, src := range []map[string]any{rk, rk3} {
			ww := Map(src["weekwins"])
			if Str(ww["week"]) == wkNow {
				weekWins += Int(ww["wins"])
			}
		}
		hightype := "Ranked_PvP_1v1"
		if highelo3 > highelo {
			hightype = "Ranked_MGR_PvP_1v1"
		}
		out["ladder_total"] = map[string]any{
			"midrewardable": false,
			"highelo":       maxInt(highelo, highelo3),
			"hightype":      hightype,
			"playedgames":   total + total3,
			"totalwins":     weekWins,
		}
	}
	return out
}

// LadderDivisionsJSON is ep_ladder_division_info's body.
func LadderDivisionsJSON() []any {
	out := []any{}
	for _, d := range LadderDivisions {
		out = append(out, map[string]any{"name": d.Name, "rating": d.Rating})
	}
	return out
}
