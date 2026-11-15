package game

import (
	"fmt"
	"math"
	"sort"
)

// gen_teams.py port: build a club's starter team from fifadb.
// 4-4-2 formation slots: (position index, x, y).
var genFormation = [][3]float64{
	{0, 0.5, 0.02},
	{3, 0.85, 0.22}, {4, 0.62, 0.16}, {6, 0.38, 0.16}, {7, 0.15, 0.22},
	{12, 0.85, 0.5}, {13, 0.62, 0.46}, {15, 0.38, 0.46}, {16, 0.15, 0.5},
	{24, 0.62, 0.8}, {26, 0.38, 0.8},
}

var genFits = map[int]map[int]bool{
	0: {0: true}, 3: {3: true, 2: true}, 4: {4: true, 5: true, 1: true},
	6: {6: true, 5: true, 1: true}, 7: {7: true, 8: true},
	12: {12: true, 23: true}, 13: {13: true, 14: true, 10: true, 9: true, 11: true, 18: true},
	15: {15: true, 14: true, 10: true, 11: true, 9: true, 18: true},
	16: {16: true, 27: true},
	24: {24: true, 25: true, 21: true, 20: true, 22: true, 23: true},
	26: {26: true, 25: true, 21: true, 22: true, 20: true, 27: true},
}

var genNumbers = map[int]int{0: 1, 3: 2, 4: 4, 6: 5, 7: 3, 12: 7, 13: 8, 15: 6, 16: 11, 24: 9, 26: 10}

var genTeamInfo = map[int][2]string{
	241: {"FC Barcelona", "Real Madrid"}, 243: {"Real Madrid", "FC Barcelona"},
	1: {"Arsenal", "Tottenham Hotspur"}, 9: {"Liverpool", "Everton"},
	45: {"Juventus", "Inter"}, 22: {"Borussia Dortmund", "FC Schalke 04"},
}

var genNationNames = map[int]string{45: "Spanyol", 14: "Inggris", 27: "Italia", 21: "Jerman"}

const (
	genCurrentSeason = 88
	genSelectable    = 18
	genFitBonus      = 4
)

func teamGrade(avg float64) float64 {
	g := float64(int((avg-59)/1.75+0.5)) / 2 // python round() ~ half-up here
	if g < 0.5 {
		g = 0.5
	}
	if g > 5 {
		g = 5
	}
	return g
}

// GenTeam builds {teamid, summary, team} for a club (or, with
// byNationality, a national team). Cached per teamid.
func (r *Roster) GenTeam(teamid int, nationality int, byNationality bool) map[string]any {
	key := teamid
	if byNationality {
		key = -teamid
	}
	r.mu.Lock()
	if t, ok := r.teamCache[key]; ok {
		r.mu.Unlock()
		return t
	}
	r.mu.Unlock()

	field := "p.teamid"
	value := teamid
	if byNationality {
		field = "p.nationality"
		value = nationality
	}
	cols := "p.spid, p.playerid, p.teamid, p.leagueid, p.nationality, p.preferredposition1, p.overallrating"
	for _, c := range statCols {
		cols += ", p." + c
	}
	q := fmt.Sprintf(`SELECT %s FROM players p JOIN permid m ON m.spid = p.spid AND m.licensed = 1
		WHERE %s = ? ORDER BY (p.spid / 1000000 != ?), (p.spid < 1000000), p.spid`, cols, field)
	rows, err := r.db.Query(q, value, genCurrentSeason)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ordered []*PlayerRow
	for rows.Next() {
		vals := make([]any, 7+len(statCols))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil
		}
		pr := &PlayerRow{Stats: map[string]int{}}
		asInt := func(i int) int {
			switch v := vals[i].(type) {
			case int64:
				return int(v)
			case float64:
				return int(v)
			}
			return 0
		}
		pr.Spid, pr.PlayerID, pr.TeamID, pr.LeagueID, pr.Nationality, pr.PrefPos1, pr.Overall =
			asInt(0), asInt(1), asInt(2), asInt(3), asInt(4), asInt(5), asInt(6)
		for i, c := range statCols {
			pr.Stats[c] = asInt(7 + i)
		}
		ordered = append(ordered, pr)
	}
	if err := rows.Err(); err != nil {
		return nil
	}
	// best card per playerid: current-season first (query order), then
	// fill up to SELECTABLE with older cards (gen_teams semantics).
	best := map[int]*PlayerRow{}
	var bestOrder []*PlayerRow
	for _, row := range ordered {
		if row.Spid/1000000 == genCurrentSeason || len(best) < genSelectable {
			if _, ok := best[row.PlayerID]; !ok {
				best[row.PlayerID] = row
				bestOrder = append(bestOrder, row)
			}
		}
	}
	sort.SliceStable(bestOrder, func(i, j int) bool { return bestOrder[i].Overall > bestOrder[j].Overall })
	if len(bestOrder) > 30 {
		bestOrder = bestOrder[:30]
	}
	if len(bestOrder) < 11 {
		return nil
	}
	rows30 := bestOrder

	type cand struct{ score, i, pos int }
	var cands []cand
	for i, row := range rows30 {
		for _, f := range genFormation {
			pos := int(f[0])
			if (pos == 0) != (row.PrefPos1 == 0) {
				continue
			}
			score := ratingAt(row, pos)
			if genFits[pos][row.PrefPos1] {
				score += genFitBonus
			}
			cands = append(cands, cand{score, i, pos})
		}
	}
	sort.Slice(cands, func(a, b int) bool {
		if cands[a].score != cands[b].score {
			return cands[a].score > cands[b].score
		}
		if cands[a].i != cands[b].i {
			return cands[a].i > cands[b].i
		}
		return cands[a].pos > cands[b].pos
	})
	pick := map[int]int{} // pos -> row index
	usedIdx := map[int]bool{}
	for _, c := range cands {
		if _, taken := pick[c.pos]; taken || usedIdx[c.i] {
			continue
		}
		pick[c.pos] = c.i
		usedIdx[c.i] = true
	}
	for _, f := range genFormation {
		pos := int(f[0])
		if _, ok := pick[pos]; ok {
			continue
		}
		bestI, bestV := -1, -1
		for i, row := range rows30 {
			if usedIdx[i] {
				continue
			}
			if v := ratingAt(row, pos); v > bestV {
				bestV, bestI = v, i
			}
		}
		if bestI >= 0 {
			pick[pos] = bestI
			usedIdx[bestI] = true
		}
	}

	var positions []any
	pickedSet := map[int]bool{}
	for _, f := range genFormation {
		pos := int(f[0])
		row := rows30[pick[pos]]
		pickedSet[pick[pos]] = true
		positions = append(positions, map[string]any{
			"id": fmt.Sprint(row.Spid), "spid": row.Spid, "pos": pos,
			"starting": []any{1, 1, f[1], f[2]},
		})
	}
	for i, row := range rows30 {
		if pickedSet[i] {
			continue
		}
		positions = append(positions, map[string]any{
			"id": fmt.Sprint(row.Spid), "spid": row.Spid, "pos": posSub})
	}
	numbers := map[int]int{}
	for _, p := range positions {
		pm := Map(p)
		if n, ok := genNumbers[Int(pm["pos"])]; ok {
			numbers[Int(pm["spid"])] = n
		}
	}
	nextNum := 12
	var players []any
	for _, row := range rows30 {
		jersey := numbers[row.Spid]
		if jersey == 0 {
			jersey = nextNum
			nextNum++
		}
		players = append(players, map[string]any{
			"spid": row.Spid, "expandme": "player", "price": row.Overall,
			"jersey": jersey, "jerseyname": r.jerseyName(row.PlayerID),
		})
	}
	var nonGK []int
	for _, p := range positions {
		pm := Map(p)
		if Int(pm["pos"]) != 0 {
			nonGK = append(nonGK, Int(pm["spid"]))
		}
	}
	kickers := []any{}
	if len(nonGK) >= 5 {
		for _, s := range nonGK[len(nonGK)-5:] {
			kickers = append(kickers, fmt.Sprint(s))
		}
	}
	squad := map[string]any{
		"positions": positions, "captain": fmt.Sprint(rows30[0].Spid),
		"kickers": kickers, "formation": 1,
	}
	formations := []any{map[string]any{
		"formationid": 1, "formationname": "4-4-2",
		"positions": func() []any {
			out := []any{}
			for _, f := range genFormation {
				out = append(out, int(f[0]))
			}
			return out
		}(),
		"offsets": func() []any {
			out := []any{}
			for _, f := range genFormation {
				out = append(out, map[string]any{"x": f[1], "y": f[2]})
			}
			return out
		}(),
	}}
	league := modeOf(func(i int) int { return rows30[i].LeagueID }, len(rows30))
	nation := modeOf(func(i int) int { return rows30[i].Nationality }, len(rows30))
	name, rival := "", ""
	if info, ok := genTeamInfo[teamid]; ok {
		name, rival = info[0], info[1]
	}
	ovrSum, ovrCount := 0, 0
	lineOvrs := map[string][]int{}
	lines := map[string][2]int{"DF": {1, 9}, "MF": {9, 20}, "FW": {20, 28}}
	for _, p := range positions {
		pm := Map(p)
		o := r.CardOvr(Int(pm["spid"]))
		ovrSum += o
		ovrCount++
		for k, rg := range lines {
			if pos := Int(pm["pos"]); pos >= rg[0] && pos < rg[1] {
				lineOvrs[k] = append(lineOvrs[k], o)
			}
		}
	}
	line := map[string]any{}
	for k := range lines {
		v := lineOvrs[k]
		if len(v) == 0 {
			line[k] = 0
			continue
		}
		s := 0
			for _, n := range v {
			s += n
		}
		// Python round() is half-even; int(x+0.5) drifted +1 on
		// exact .5 means (Australia MF 61 vs 60).
		line[k] = int(math.RoundToEven(float64(s) / float64(len(v))))
	}
	summary := map[string]any{
		"teamid": teamid, "id": teamid, "numofselectableplayers": genSelectable,
		"name": name, "league": map[string]any{"id": league},
		"nation": map[string]any{"id": nation, "name": genNationNames[nation]},
		"rival":  map[string]any{"name": rival},
		"ovr":    line,
		"teamgrade": teamGrade(float64(ovrSum) / float64(max(1, ovrCount))),
	}
	team := map[string]any{
		"teamid": teamid, "squad": squad, "players": players, "formations": formations,
	}
	out := map[string]any{"teamid": teamid, "summary": summary, "team": team}
	r.mu.Lock()
	r.teamCache[key] = out
	r.mu.Unlock()
	return out
}

func modeOf(get func(int) int, n int) int {
	counts := map[int]int{}
	bestV, bestC := 0, -1
	for i := 0; i < n; i++ {
		v := get(i)
		counts[v]++
		if counts[v] > bestC || (counts[v] == bestC && v < bestV) {
			bestV, bestC = v, counts[v]
		}
	}
	return bestV
}

func (r *Roster) jerseyName(playerid int) string {
	var j string
	_ = r.db.QueryRow("SELECT jersey FROM namesind WHERE playerid = ?", playerid).Scan(&j)
	return j
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
