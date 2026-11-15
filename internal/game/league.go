package game

// League 2 (single-player league), ported from hc's l2 block:
// season state at state["league2"], fixtures by the circle method,
// Poisson sim scores, sponsor missions, season-end rewards.

import (
	"math"
	mrand "math/rand"
	"sort"
	"time"
)

// rng helpers mirroring Python's random.Random surface used here.
type pyRand struct{ r *mrand.Rand }

func newPyRand(seed int64) *pyRand {
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	return &pyRand{r: mrand.New(mrand.NewSource(seed))}
}

func (p *pyRand) Random() float64          { return p.r.Float64() }
func (p *pyRand) Randint(a, b int) int     { return a + p.r.Intn(b-a+1) }
func (p *pyRand) Uniform(a, b float64) float64 {
	return a + p.r.Float64()*(b-a)
}
func (p *pyRand) ChoiceWeighted(items []int, weights []int) int {
	total := 0
	for _, w := range weights {
		total += w
	}
	n := p.r.Intn(total)
	for i, w := range weights {
		if n < w {
			return items[i]
		}
		n -= w
	}
	return items[len(items)-1]
}

func poisson(p *pyRand, lam float64) int {
	lim, k, prod := math.Exp(-lam), 0, 1.0
	for {
		prod *= p.Random()
		if prod <= lim {
			return k
		}
		k++
	}
}

// MyTeamID is hc.my_teamid.
func MyTeamID(st State) int {
	if coach := CoachBySlot(st, DefaultSlot(st)); coach != nil {
		return Int(coach["teamid"])
	}
	return 0
}

// LeagueTeamsList ports hc.league_teams.
func (r *Roster) LeagueTeamsList(leagueid, mine int) []int {
	choices := r.leagueTeamsTab[leagueid]
	out := []int{mine}
	for _, tid := range choices {
		if tid != mine {
			out = append(out, tid)
		}
	}
	return out
}

// LeagueTeamCount ports hc.league_team_count (LEAGUE_SIZE fallback 0
// is unreachable in practice: catalog covers every league).
func (r *Roster) LeagueTeamCount(leagueid int, teams []int) int {
	if len(teams) > 0 {
		return len(teams)
	}
	if choices := r.leagueTeamsTab[leagueid]; len(choices) > 0 {
		return len(choices)
	}
	return 0
}

// LeagueRound ports hc.league_round (circle method). A nil side is a
// bye; pairs carry (home, away, ok).
type Fixture struct {
	Home, Away   int
	HomeOK, AwayOK bool
}

func LeagueRound(teams []int, rnd int) []Fixture {
	n := len(teams)
	size := n + n%2
	per := size - 1
	if per <= 0 {
		return nil
	}
	flip := (rnd/per)%2 == 1
	r := rnd % per
	rot := func(i int) (int, bool) {
		k := 0
		if i != 0 {
			k = (i-1+r)%per + 1
		}
		if k < n {
			return teams[k], true
		}
		return 0, false
	}
	pairs := []Fixture{}
	for k := 0; k < size/2; k++ {
		a, aok := rot(k)
		b, bok := rot(size - 1 - k)
		f := flip
		if k == 0 && r%4 > 1 {
			f = !flip
		}
		if f {
			pairs = append(pairs, Fixture{Home: b, Away: a, HomeOK: bok, AwayOK: aok})
		} else {
			pairs = append(pairs, Fixture{Home: a, Away: b, HomeOK: aok, AwayOK: bok})
		}
	}
	return pairs
}

// TeamStrength ports hc.team_strength.
func (r *Roster) TeamStrength(st State, teamid int) float64 {
	var raw map[string]any
	if teamid == MyTeamID(st) {
		raw = TeamOf(st, DefaultSlot(st))
	}
	if raw == nil {
		raw = r.RosterTeam(teamid)
	}
	ovrs := []int{}
	for _, pv := range List(raw["players"]) {
		if p := Map(pv); p != nil {
			ovrs = append(ovrs, r.PlayerOvr(p))
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ovrs)))
	if len(ovrs) > 11 {
		ovrs = ovrs[:11]
	}
	if len(ovrs) == 0 {
		return 60
	}
	sum := 0
	for _, v := range ovrs {
		sum += v
	}
	return float64(sum) / float64(len(ovrs))
}

// SimScore ports hc.sim_score.
func SimScore(p *pyRand, home, away float64) (int, int) {
	d := (home + 3 - away) / 10
	return poisson(p, math.Max(0.3, 1.4+0.6*d)), poisson(p, math.Max(0.2, 1.1-0.6*d))
}

// LeagueStats ports hc.league_stats.
func LeagueStats(teams []int, records []map[string]any, prev map[string]any) map[string]any {
	s := map[string]map[string]any{}
	for _, t := range teams {
		s[itoa(t)] = map[string]any{"wins": 0, "draws": 0, "losses": 0, "goals": 0,
			"goalsconceded": 0, "awaygoals": 0, "point": 0}
	}
	for _, m := range records {
		h, a := s[itoa(Int(m["home"]))], s[itoa(Int(m["away"]))]
		if h == nil || a == nil {
			continue
		}
		goals := Map(m["goals"])
		gh, ga := Int(goals["home"]), Int(goals["away"])
		h["goals"] = Int(h["goals"]) + gh
		h["goalsconceded"] = Int(h["goalsconceded"]) + ga
		a["goals"] = Int(a["goals"]) + ga
		a["goalsconceded"] = Int(a["goalsconceded"]) + gh
		a["awaygoals"] = Int(a["awaygoals"]) + ga
		if gh != ga {
			win, lose := h, a
			if gh < ga {
				win, lose = a, h
			}
			win["wins"] = Int(win["wins"]) + 1
			win["point"] = Int(win["point"]) + 3
			lose["losses"] = Int(lose["losses"]) + 1
		} else {
			h["draws"] = Int(h["draws"]) + 1
			a["draws"] = Int(a["draws"]) + 1
			h["point"] = Int(h["point"]) + 1
			a["point"] = Int(a["point"]) + 1
		}
	}
	order := append([]int{}, teams...)
	sort.SliceStable(order, func(i, j int) bool {
		si, sj := s[itoa(order[i])], s[itoa(order[j])]
		ki := []int{Int(si["point"]), Int(si["goals"]) - Int(si["goalsconceded"]), Int(si["goals"]), Int(si["awaygoals"])}
		kj := []int{Int(sj["point"]), Int(sj["goals"]) - Int(sj["goalsconceded"]), Int(sj["goals"]), Int(sj["awaygoals"])}
		for x := range ki {
			if ki[x] != kj[x] {
				return ki[x] > kj[x]
			}
		}
		return false
	})
	for i, t := range order {
		s[itoa(t)]["rank"] = i + 1
	}
	out := map[string]any{}
	for t, v := range s {
		p := Map(prev[t])
		if p == nil {
			p = v
		}
		v["before"] = map[string]any{"rank": Int(p["rank"]), "point": Int(p["point"])}
		out[t] = v
	}
	return out
}

// ---------- /league2/me ----------

// L2Me ports hc.l2_me.
func (r *Roster) L2Me(st State) map[string]any {
	teamid := MyTeamID(st)
	lg := Map(st["league2"])
	leagueid := 0
	if lg != nil {
		leagueid = Int(lg["leagueid"])
	}
	if leagueid == 0 {
		leagueid = Int(Map(r.RosterSummary(teamid)["league"])["id"])
	}
	me := map[string]any{
		"teamid": teamid, "leagueid": leagueid,
		"difficultybest": 24, "difficultyselect": 10,
		"laststatus": "", "cupinfo": map[string]any{"entered": false},
		"leader": map[string]any{"league": map[string]any{}, "cup": map[string]any{}},
		"sponsoroffice": map[string]any{"season": map[string]any{"status": "none"}},
	}
	if lg != nil {
		if v, ok := lg["difficultyselect"]; ok {
			me["difficultyselect"] = v
		}
		if v, ok := lg["laststatus"]; ok {
			me["laststatus"] = v
		}
		if v, ok := lg["sponsoroffice"]; ok {
			me["sponsoroffice"] = v
		}
		if Map(lg["league"]) != nil {
			me["openingday"] = lg["openingday"]
			me["league"] = lg["league"]
		}
	}
	return me
}

// L2Resources ports hc.l2_resources.
func (r *Roster) L2Resources(st State) []any {
	return []any{map[string]any{"uri": "/league2/me", "body": r.L2Me(st)}}
}

// RepairLeagueContext ports hc.repair_league_context.
func (r *Roster) RepairLeagueContext(st State) bool {
	lg := Map(st["league2"])
	if lg == nil {
		return false
	}
	league := Map(lg["league"])
	if league == nil {
		return false
	}
	teamid := MyTeamID(st)
	teams := intList(league["teams"])
	owner := lg["teamid"]
	ownerID := Int(owner)
	if owner == nil && len(teams) > 0 {
		ownerID = teams[0]
	}
	leagueid := Int(Map(r.RosterSummary(teamid)["league"])["id"])
	contains := false
	for _, t := range teams {
		if t == teamid {
			contains = true
		}
	}
	if ownerID == teamid && Int(lg["leagueid"]) == leagueid && contains {
		return false
	}
	archives := List(st["league2_archives"])
	archives = append(archives, map[string]any{"slot": DefaultSlot(st),
		"reason": "team_changed", "archivedat": time.Now().UnixMilli(), "season": lg})
	st["league2_archives"] = archives
	st["league2"] = map[string]any{"difficultyselect": firstNonZero(Int(lg["difficultyselect"]), 10)}
	return true
}

func intList(v any) []int {
	out := []int{}
	for _, e := range List(v) {
		out = append(out, Int(e))
	}
	return out
}

// ---------- player records (league Data tab) ----------

// LeagueRecordRows ports hc._league_record_rows.
func LeagueRecordRows(st State) []any {
	lg := Map(st["league2"])
	records := Map(lg["playerrecords"])
	team := TeamOf(st, DefaultSlot(st))
	roleByID := map[string]int{}
	spidByHex := map[string]int{}
	if team != nil {
		if squad := Map(team["squad"]); squad != nil {
			for i, pv := range List(squad["positions"]) {
				if id := Str(Map(pv)["id"]); id != "" {
					roleByID[id] = i
				}
			}
		}
		for _, pv := range List(team["players"]) {
			if p := Map(pv); p != nil && Str(p["_id"]) != "" {
				spidByHex[Str(p["_id"])] = Int(p["spid"])
			}
		}
	}
	rows := []any{}
	for pid, raw := range records {
		value := Map(raw)
		matches := Int(value["matches"])
		if matches <= 0 {
			continue
		}
		spid := spidByHex[pid]
		if isAllDigits(pid) {
			spid = atoiSafe(pid)
		}
		// "passes" is the pass-success RATE as a 0..1 fraction,
		// NOT a count: LeagueCupFrontScreen.joinPlayerData computes
		// pass = int(record.passes * 100) and renders it with "%"
		// (proven from the client bytecode). Serving the success
		// count (133) rendered "13300%". "passtries" (attempts) is
		// not read by that page but kept for parity.
		succ, att := Int(value["passes_success"]), Int(value["passes_attempted"])
		rate := 0.0
		if att > 0 {
			rate = roundHalfEven(float64(succ)/float64(att), 4)
		}
		rows = append(rows, map[string]any{
			"id": pid, "spid": spid, "role": roleByID[pid], "matches": matches,
			"ratings": roundHalfEven(Float(value["ratings"])*float64(matches), 2),
			"goals": Int(value["goals"]), "assists": Int(value["assists"]),
			"passes": rate, "passtries": att,
			"tackles": Int(value["tackles"]), "saves": Int(value["saves"]),
		})
	}
	return rows
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// Float reads a float64 from a state value.
func Float(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func roundHalfEven(x float64, places int) float64 {
	m := math.Pow(10, float64(places))
	return math.RoundToEven(x*m) / m
}

// RecordLeagueMatchPlayers ports hc.record_league_match_players.
func RecordLeagueMatchPlayers(st State, result map[string]any, matchKey string) {
	lg := Map(st["league2"])
	if lg == nil || Map(lg["league"]) == nil || result == nil {
		return
	}
	seen := List(lg["playerrecord_matchkeys"])
	for _, k := range seen {
		if Str(k) == matchKey {
			return
		}
	}
	team := TeamOf(st, DefaultSlot(st))
	if team == nil {
		return
	}
	players := []map[string]any{}
	for _, pv := range List(team["players"]) {
		if p := Map(pv); p != nil {
			players = append(players, p)
		}
	}
	squad := Map(team["squad"])
	registeredIDs := map[string]bool{}
	registeredSpids := map[int]bool{}
	for _, pv := range List(squad["positions"]) {
		if id := Str(Map(pv)["id"]); id != "" {
			registeredIDs[id] = true
		}
		registeredSpids[Int(Map(pv)["spid"])%1000000] = true
	}
	var active []map[string]any
	for _, p := range players {
		if registeredIDs[IID(p)] || registeredSpids[Int(p["spid"])%1000000] {
			active = append(active, p)
		}
	}
	if len(active) == 0 {
		for _, p := range players {
			if p["team2"] != true {
				active = append(active, p)
			}
		}
	}
	bySpid := map[int][]map[string]any{}
	for _, p := range active {
		k := Int(p["spid"]) % 1000000
		bySpid[k] = append(bySpid[k], p)
	}
	mine := MyTeamID(st)
	var side map[string]any
	if mt := Str(result["myTeam"]); mt == "home" || mt == "away" {
		side = Map(result[mt])
	}
	if side == nil {
		for _, name := range []string{"home", "away"} {
			cand := Map(result[name])
			if cand != nil && Str(cand["id"]) == itoa(mine) {
				side = cand
				break
			}
		}
	}
	if side == nil {
		match := Map(st["match"])
		gs := Map(match["gamesettings"])
		home := true
		if v, ok := gs["home"]; ok && v == false {
			home = false
		}
		if home {
			side = Map(result["home"])
		} else {
			side = Map(result["away"])
		}
	}
	if side == nil {
		return
	}
	statPlayers := List(side["players"])
	if len(statPlayers) == 0 {
		return
	}
	records := Map(lg["playerrecords"])
	if records == nil {
		records = map[string]any{}
		lg["playerrecords"] = records
	}
	for _, sv := range statPlayers {
		stat := Map(sv)
		if stat == nil {
			continue
		}
		spid := firstNonZero(Int(stat["spid"]), Int(stat["dbid"]), parseIntLoose(stat["id"])) % 1000000
		cands := bySpid[spid]
		if len(cands) == 0 {
			continue
		}
		player := cands[0]
		bySpid[spid] = cands[1:]
		key := IID(player)
		row := Map(records[key])
		if row == nil {
			row = map[string]any{"matches": 0, "ratings": 0.0, "goals": 0, "assists": 0,
				"passes_success": 0, "passes_attempted": 0, "tackles": 0, "saves": 0}
			records[key] = row
		}
		old := Int(row["matches"])
		rating := Float(stat["rating"])
		row["ratings"] = (Float(row["ratings"])*float64(old) + rating) / float64(old+1)
		row["matches"] = old + 1
		for _, f := range []string{"goals", "assists", "saves"} {
			row[f] = Int(row[f]) + Int(stat[f])
		}
		switch t := stat["tackles"].(type) {
		case []any:
			if len(t) > 0 {
				row["tackles"] = Int(row["tackles"]) + Int(t[0])
			}
		case float64, int, int64:
			row["tackles"] = Int(row["tackles"]) + Int(t)
		}
		switch pv := stat["passes"].(type) {
		case []any:
			if len(pv) >= 2 {
				row["passes_success"] = Int(row["passes_success"]) + Int(pv[0])
				row["passes_attempted"] = Int(row["passes_attempted"]) + Int(pv[1])
			}
		case float64, int, int64:
			row["passes_success"] = Int(row["passes_success"]) + Int(pv)
			row["passes_attempted"] = Int(row["passes_attempted"]) + Int(pv)
		}
	}
	lg["playerrecord_matchkeys"] = append(seen, matchKey)
}

// SeedLatestLeaguePlayerStats ports hc.seed_latest_league_player_stats.
func SeedLatestLeaguePlayerStats(st State) bool {
	lg := Map(st["league2"])
	match := Map(st["match"])
	if lg == nil || match == nil {
		return false
	}
	param := Map(match["leagueparam"])
	if Map(lg["league"]) == nil || len(Map(lg["playerrecords"])) > 0 ||
		len(List(lg["playerrecord_matchkeys"])) > 0 ||
		Str(match["stage"]) != "closed" ||
		len(Str(match["type"])) < 9 || Str(match["type"])[:9] != "League_2_" ||
		Str(param["matchtype"]) != "league" {
		return false
	}
	lastRound, ok := param["round"].(float64)
	if !ok && param["round"] == nil {
		return false
	}
	me := MyTeamID(st)
	found := false
	for _, rv := range List(Map(lg["league"])["matchrecords"]) {
		rec := Map(rv)
		if Int(rec["round"]) == int(lastRound) && (Int(rec["home"]) == me || Int(rec["away"]) == me) {
			found = true
		}
	}
	if !found {
		return false
	}
	result := Map(match["result"])
	if len(List(Map(result["home"])["players"])) == 0 && len(List(Map(result["away"])["players"])) == 0 {
		return false
	}
	RecordLeagueMatchPlayers(st, result, "league:"+itoa(int(lastRound)))
	return len(Map(lg["playerrecords"])) > 0
}

// PyRand is the exported handle for the sim RNG stream.
type PyRand = pyRand

// NewPyRand builds a sim RNG (seed 0 = time-seeded).
func NewPyRand(seed int64) *pyRand { return newPyRand(seed) }

// MatchSeed ports int(match["id"][:8], 16) — the sim event seed.
func MatchSeed(match map[string]any) int64 {
	id := Str(match["id"])
	if len(id) > 8 {
		id = id[:8]
	}
	var v int64
	for _, c := range id {
		v *= 16
		switch {
		case c >= '0' && c <= '9':
			v += int64(c - '0')
		case c >= 'a' && c <= 'f':
			v += int64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v += int64(c-'A') + 10
		}
	}
	return v
}
