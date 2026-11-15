package game

// Single-player cups (PvE): European Champions Cup, South American
// Cup and the World Cup. Ported from hc's cup block: metas, pools,
// group + knock-out fixtures, per-round play, rewards and the
// coach's cup records. Cup state lives at state["cups"][cupid],
// the played-match handoff at state["cup_pending"] (match end) and
// cup["_mine"] (consumed by /cup/<id>/next).

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mrand "math/rand"
	"sort"
	"time"
)

const WorldCupMetaID = 10001
const CupLevels = 5

// CupMeta ports hc.cup_meta.
func CupMeta(metaid int, name, trophy string, quality, matchcount, teams, reward int) map[string]any {
	return map[string]any{
		"metaid": metaid, "_id": fmt.Sprint(metaid), "name": name, "type": "pve",
		"teamselect": "user", "visible": true, "difficulty": 0, "quality": quality,
		"matchcount": matchcount, "teamcount_desc": teams,
		"proviso_desc": "PVECUP_INFO_OPTION_NO", "trophy": trophy, "skin": fmt.Sprint(metaid),
		"bestrewards": []any{[]any{map[string]any{"itemid": 1, "count": reward}}},
		"startat":      "2026-11-01T00:00:00.000Z", "endat": "2030-12-31T00:00:00.000Z",
		"joinable": map[string]any{"fee": 0, "igr": false, "vip": 0, "allteams": true,
			"dayofweek": []any{1, 1, 1, 1, 1, 1, 1}},
	}
}

// CupMetas returns [WorldCup, ECC, SA] as hc's [WORLD_CUP, *CUPS].
func (r *Roster) CupMetas() []map[string]any {
	wc := CupMeta(WorldCupMetaID, "World Cup", "105", 4, 7, 32, 500000)
	wc["teamselect"] = "authentic"
	join := Map(wc["joinable"])
	join["allteams"] = false
	join["leagueids"] = []any{78}
	teamids := []any{}
	for _, tid := range r.nationalOrder {
		teamids = append(teamids, tid)
	}
	join["teamids"] = teamids
	return []map[string]any{
		wc,
		CupMeta(1, "European Champions Cup", "66", 4, 6, 16, 60000),
		CupMeta(2, "South American Cup", "300000", 3, 6, 16, 40000),
	}
}

// CupMetaByID finds a meta (nil when invalid).
func (r *Roster) CupMetaByID(metaid int) map[string]any {
	for _, m := range r.CupMetas() {
		if Int(m["metaid"]) == metaid {
			return m
		}
	}
	return nil
}

// TeamKit ports hc.team_kit (ten-bit kit ids for previews/freeroam).
func TeamKit(teamid int) map[string]any {
	av := map[string]any{}
	for k, t := range map[string]int{"home": 0, "away": 1, "goalie": 2} {
		av[k] = map[string]any{"id": teamid<<10 | t}
	}
	return map[string]any{"availables": av}
}

// RealCoachID ports hc._real_coach_id.
func RealCoachID(st State) string {
	slot := DefaultSlot(st)
	coaches := Coaches(st)
	for _, co := range coaches {
		if Int(co["slot"]) == slot && Str(co["_id"]) != "" {
			return Str(co["_id"])
		}
	}
	for _, co := range coaches {
		if Str(co["_id"]) != "" {
			return Str(co["_id"])
		}
	}
	return CoachIDForUser(0, slot)
}

// HealCupCoachIDs ports hc.heal_cup_coach_ids.
func HealCupCoachIDs(st State, cup map[string]any) {
	real := RealCoachID(st)
	for _, cv := range List(cup["coaches"]) {
		if co := Map(cv); co != nil && Str(co["_id"]) != real {
			co["_id"] = real
		}
	}
	fix := func(t map[string]any) {
		if t != nil && t["coach"] != nil && Str(t["coach"]) != real {
			t["coach"] = real
		}
	}
	for _, tv := range List(cup["teams"]) {
		fix(Map(tv))
	}
	for _, pv := range List(cup["phases"]) {
		for _, gv := range List(Map(pv)["groups"]) {
			for _, tv := range List(Map(gv)["teams"]) {
				fix(Map(tv))
			}
		}
	}
}

// CoachRecord ports hc.coach_record.
func CoachRecord(coach map[string]any, part string) map[string]any {
	rec := Map(coach["record"])
	if rec == nil {
		rec = map[string]any{}
		coach["record"] = rec
	}
	partMap := Map(rec[part])
	if partMap == nil {
		partMap = map[string]any{}
		rec[part] = partMap
	}
	return partMap
}

// RecordMatch ports hc.record_match.
func RecordMatch(st State, part string, gf, ga int) {
	coach := CoachBySlot(st, DefaultSlot(st))
	if coach == nil {
		return
	}
	r := CoachRecord(coach, part)
	for _, k := range []string{"wins", "draws", "losses", "goals", "goalsconceded", "streak", "maxstreak"} {
		if _, ok := r[k]; !ok {
			r[k] = 0
		}
	}
	res := "draws"
	if gf > ga {
		res = "wins"
	} else if gf < ga {
		res = "losses"
	}
	r[res] = Int(r[res]) + 1
	r["goals"] = Int(r["goals"]) + gf
	r["goalsconceded"] = Int(r["goalsconceded"]) + ga
	if res == "wins" {
		r["streak"] = Int(r["streak"]) + 1
	} else {
		r["streak"] = 0
	}
	if Int(r["streak"]) > Int(r["maxstreak"]) {
		r["maxstreak"] = r["streak"]
	}
}

// CupPool ports hc.cup_pool.
func (r *Roster) CupPool(metaid, mine int) []int {
	if metaid == WorldCupMetaID {
		ids := []int{}
		for tid := range r.nationalTeams {
			ids = append(ids, tid)
		}
		sort.Ints(ids)
		out := []int{}
		for _, tid := range ids {
			if tid != mine && len(List(r.RosterTeam(tid)["players"])) > 0 {
				out = append(out, tid)
			}
			if len(out) >= 31 {
				break
			}
		}
		return out
	}
	rows, err := r.db.Query(`SELECT p.teamid FROM players p JOIN permid m ON m.spid = p.spid AND m.licensed = 1
		WHERE p.spid < 1000000 GROUP BY p.teamid HAVING COUNT(*) >= 16
		ORDER BY MAX(p.overallrating) DESC LIMIT 60`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	ids := []int{}
	for rows.Next() {
		var tid int
		if rows.Scan(&tid) == nil && tid != mine {
			ids = append(ids, tid)
		}
	}
	if metaid == 1 {
		if len(ids) > 15 {
			ids = ids[:15]
		}
		return ids
	}
	saRows, err := r.db.Query(`SELECT p.teamid FROM players p JOIN permid m ON m.spid = p.spid AND m.licensed = 1
		WHERE p.spid < 1000000 AND p.leagueid IN (353, 336)
		GROUP BY p.teamid HAVING COUNT(*) >= 16
		ORDER BY MAX(p.overallrating) DESC LIMIT 40`)
	if err != nil {
		return ids[:min(15, len(ids))]
	}
	defer saRows.Close()
	saIDs := []int{}
	inSA := map[int]bool{}
	for saRows.Next() {
		var tid int
		if saRows.Scan(&tid) == nil && tid != mine {
			saIDs = append(saIDs, tid)
			inSA[tid] = true
		}
	}
	out := append(saIDs, []int{}...)
	for _, id := range ids {
		if !inSA[id] {
			out = append(out, id)
		}
	}
	if len(out) > 15 {
		out = out[:15]
	}
	return out
}

func cupTeam(st State, teamid int, userTeamid *int) map[string]any {
	me := MyTeamID(st)
	if userTeamid != nil {
		me = *userTeamid
	}
	coach := any(nil)
	if teamid == me {
		coach = RealCoachID(st)
	}
	return map[string]any{"teamid": teamid, "coach": coach}
}

// NewCup ports hc.new_cup.
func (r *Roster) NewCup(st State, meta map[string]any, difficulty int, selectedTeamid *int) map[string]any {
	isWC := Int(meta["metaid"]) == WorldCupMetaID
	me := MyTeamID(st)
	if isWC && selectedTeamid != nil {
		me = *selectedTeamid
	}
	teams := append([]int{me}, r.CupPool(Int(meta["metaid"]), me)...)
	target := 16
	if isWC {
		target = 32
	}
	if len(teams) > target {
		teams = teams[:target]
	}
	mrand.Shuffle(len(teams), func(i, j int) { teams[i], teams[j] = teams[j], teams[i] })
	groupCount := len(teams) / 4
	groups := []any{}
	for g := 0; g < groupCount; g++ {
		gteams := []any{}
		for _, t := range teams[g*4 : g*4+4] {
			var ut *int
			if isWC {
				ut = &me
			}
			gteams = append(gteams, cupTeam(st, t, ut))
		}
		rounds := []any{}
		for rd := 0; rd < 3; rd++ {
			pairs := []any{}
			for _, fx := range LeagueRound([]int{0, 1, 2, 3}, rd) {
				leg := map[string]any{}
				if fx.HomeOK {
					leg["homeidx"] = fx.Home
				} else {
					leg["homeidx"] = nil
				}
				if fx.AwayOK {
					leg["awayidx"] = fx.Away
				} else {
					leg["awayidx"] = nil
				}
				pairs = append(pairs, map[string]any{"legs": []any{leg}})
			}
			rounds = append(rounds, map[string]any{"shuffle": rd == 0, "pairs": pairs})
		}
		groups = append(groups, map[string]any{"teams": gteams, "rounds": rounds})
	}
	var rb [8]byte
	_, _ = rand.Read(rb[:])
	cupid := fmt.Sprintf("%08x%s", time.Now().Unix(), hex.EncodeToString(rb[:]))
	cup := map[string]any{
		"_id": cupid, "metaid": meta["metaid"], "name": meta["name"], "type": "pve",
		"difficulty": difficulty, "skin": Str(meta["skin"]), "trophy": Str(meta["trophy"]),
		"selectedteamid": me,
		"current":         map[string]any{"phase": 0, "round": 0, "leg": 0},
		"phases":          []any{map[string]any{"system": "round-robin", "name": "Vòng bảng", "maxrounds": 3, "groups": groups}},
		"coaches":         []any{map[string]any{"_id": RealCoachID(st), "state": "playing"}},
	}
	if isWC {
		cup["mode"] = "worldcup"
	} else {
		cup["mode"] = "clubcup"
	}
	teamList := []any{}
	for _, t := range teams {
		var ut *int
		if isWC {
			ut = &me
		}
		teamList = append(teamList, cupTeam(st, t, ut))
	}
	cup["teams"] = teamList
	if isWC {
		wcRoster := r.OwnedTeam(r.RosterTeam(me))
		cup["team"] = wcRoster
		cup["squad"] = wcRoster["squad"]
	}
	CupRefreshStats(cup)
	return cup
}

// CupMyPair ports hc.cup_my_pair.
func CupMyPair(cup map[string]any) (map[string]any, map[string]any) {
	cur := Map(cup["current"])
	phase := Map(List(cup["phases"])[Int(cur["phase"])])
	for _, gv := range List(phase["groups"]) {
		g := Map(gv)
		rounds := List(g["rounds"])
		if Int(cur["round"]) >= len(rounds) {
			continue
		}
		for _, pv := range List(Map(rounds[Int(cur["round"])])["pairs"]) {
			legs := List(Map(pv)["legs"])
			if Int(cur["leg"]) >= len(legs) {
				continue
			}
			leg := Map(legs[Int(cur["leg"])])
			teams := List(g["teams"])
			for _, idxKey := range []string{"homeidx", "awayidx"} {
				if leg[idxKey] != nil {
					i := Int(leg[idxKey])
					if i < len(teams) && Map(teams[i])["coach"] != nil {
						return g, leg
					}
				}
			}
		}
	}
	return nil, nil
}

// groupTable ports hc.group_table: [(idx, points, gd, goals)] order
// applied as a ranking of indices.
func groupTable(g map[string]any) []int {
	teams := List(g["teams"])
	type agg struct{ pts, gd, goals int }
	t := map[int]*agg{}
	for i := range teams {
		t[i] = &agg{}
	}
	for _, rv := range List(g["rounds"]) {
		for _, pv := range List(Map(rv)["pairs"]) {
			leg := Map(List(Map(pv)["legs"])[0])
			score := Map(leg["score"])
			if score == nil {
				continue
			}
			gh, ga := Int(score["home"]), Int(score["away"])
			hi, ai := Int(leg["homeidx"]), Int(leg["awayidx"])
			for _, e := range []struct {
				i, f, a int
			}{{hi, gh, ga}, {ai, ga, gh}} {
				a := t[e.i]
				if e.f > e.a {
					a.pts += 3
				} else if e.f == e.a {
					a.pts += 1
				}
				a.gd += e.f - e.a
				a.goals += e.f
			}
		}
	}
	idx := []int{}
	for i := range teams {
		idx = append(idx, i)
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ta, tb := t[idx[a]], t[idx[b]]
		if ta.pts != tb.pts {
			return ta.pts > tb.pts
		}
		if ta.gd != tb.gd {
			return ta.gd > tb.gd
		}
		return ta.goals > tb.goals
	})
	return idx
}

// CupRefreshStats ports hc.cup_refresh_stats.
func CupRefreshStats(cup map[string]any) {
	wdl := map[int][3]int{}
	goals := map[int][2]int{}
	for _, pv := range List(cup["phases"]) {
		phase := Map(pv)
		for _, gv := range List(phase["groups"]) {
			g := Map(gv)
			for _, rv := range List(g["rounds"]) {
				for _, pairV := range List(Map(rv)["pairs"]) {
					leg := Map(List(Map(pairV)["legs"])[0])
					score := Map(leg["score"])
					if score == nil {
						continue
					}
					teams := List(g["teams"])
					for _, e := range []struct {
						idx, f, a int
					}{{Int(leg["homeidx"]), Int(score["home"]), Int(score["away"])},
						{Int(leg["awayidx"]), Int(score["away"]), Int(score["home"])}} {
						tid := Int(Map(teams[e.idx])["teamid"])
						r := wdl[tid]
						if e.f > e.a {
							r[0]++
						} else if e.f == e.a {
							r[1]++
						} else {
							r[2]++
						}
						wdl[tid] = r
						ga := goals[tid]
						ga[0] += e.f
						ga[1] += e.a
						goals[tid] = ga
					}
				}
			}
		}
	}
	for _, pv := range List(cup["phases"]) {
		phase := Map(pv)
		for _, gv := range List(phase["groups"]) {
			g := Map(gv)
			var order []int
			if Str(phase["system"]) == "round-robin" {
				order = groupTable(g)
			} else {
				for i := range List(g["teams"]) {
					order = append(order, i)
				}
			}
			teams := List(g["teams"])
			for rank, i := range order {
				t := Map(teams[i])
				w := wdl[Int(t["teamid"])]
				gf := goals[Int(t["teamid"])]
				t["win"] = w[0]
				t["draw"] = w[1]
				t["lose"] = w[2]
				t["point"] = 3*w[0] + w[1]
				t["rank"] = rank + 1
				t["goals"] = gf[0]
				t["goalsconceded"] = gf[1]
			}
		}
	}
}

// CupPlayRound ports hc.cup_play_round. mine is the optional played
// result (home, away, shootoutHome, shootoutAway).
func (r *Roster) CupPlayRound(st State, cup map[string]any, mine []int) {
	cur := Map(cup["current"])
	phase := Map(List(cup["phases"])[Int(cur["phase"])])
	ko := Str(phase["system"]) == "knock-out"
	rng := newPyRand(0)
	for _, gv := range List(phase["groups"]) {
		g := Map(gv)
		rounds := List(g["rounds"])
		pairs := List(Map(rounds[Int(cur["round"])])["pairs"])
		for _, pairV := range pairs {
			legs := List(Map(pairV)["legs"])
			leg := Map(legs[Int(cur["leg"])])
			if leg == nil || leg["score"] != nil || leg["homeidx"] == nil {
				continue
			}
			teams := List(g["teams"])
			h := Map(teams[Int(leg["homeidx"])])
			a := Map(teams[Int(leg["awayidx"])])
			played := mine != nil && (h["coach"] != nil || a["coach"] != nil)
			var gh, ga int
			if played {
				gh, ga = mine[0], mine[1]
			} else {
				gh, ga = SimScore(rng, r.TeamStrength(st, Int(h["teamid"])), r.TeamStrength(st, Int(a["teamid"])))
			}
			leg["score"] = map[string]any{"home": gh, "away": ga}
			if h["coach"] != nil || a["coach"] != nil {
				if h["coach"] != nil {
					RecordMatch(st, "cup", gh, ga)
				} else {
					RecordMatch(st, "cup", ga, gh)
				}
			}
			if ko && gh == ga && played && len(mine) > 3 && mine[2] != mine[3] {
				leg["shootout"] = map[string]any{"home": mine[2], "away": mine[3]}
			} else if ko && gh == ga {
				ph := rng.Randint(3, 5)
				pa := ph - 1
				if ph < 5 {
					if rng.r.Intn(2) == 0 {
						pa = ph - 1
					} else {
						pa = ph + 1
					}
				}
				leg["shootout"] = map[string]any{"home": ph, "away": pa}
			}
		}
	}
	meState := Map(List(cup["coaches"])[0])
	if !ko {
		rounds := List(Map(List(phase["groups"])[0])["rounds"])
		if Int(cur["round"])+1 < len(rounds) {
			cur["round"] = Int(cur["round"]) + 1
			return
		}
		groups := List(phase["groups"])
		tables := [][]int{}
		for _, gv := range groups {
			tables = append(tables, groupTable(Map(gv)))
		}
		qual := []any{}
		for x := 0; x < len(groups); x += 2 {
			y := x + 1
			gx, gy := Map(groups[x]), Map(groups[y])
			tx, ty := List(gx["teams"]), List(gy["teams"])
			qual = append(qual, tx[tables[x][0]], ty[tables[y][1]], ty[tables[y][0]], tx[tables[x][1]])
		}
		knockoutRounds := 0
		for n := len(qual); n > 1; n /= 2 {
			knockoutRounds++
		}
		roundsKO := []any{}
		firstPairs := []any{}
		for i := 0; i < len(qual); i += 2 {
			firstPairs = append(firstPairs, map[string]any{"legs": []any{map[string]any{"homeidx": i, "awayidx": i + 1}}})
		}
		roundsKO = append(roundsKO, map[string]any{"shuffle": true, "pairs": firstPairs})
		for rd := 1; rd < knockoutRounds; rd++ {
			pairs := []any{}
			for i := 0; i < len(qual)/(1<<(rd+1)); i++ {
				pairs = append(pairs, map[string]any{"legs": []any{map[string]any{}}})
			}
			roundsKO = append(roundsKO, map[string]any{"pairs": pairs})
		}
		name := "Vòng loại trực tiếp"
		if Str(cup["mode"]) == "worldcup" {
			name = "World Cup knockout"
		}
		cup["phases"] = append(List(cup["phases"]), map[string]any{
			"system": "knock-out", "name": name, "maxrounds": knockoutRounds,
			"groups": []any{map[string]any{"teams": qual, "rounds": roundsKO}}})
		cup["current"] = map[string]any{"phase": Int(cur["phase"]) + 1, "round": 0, "leg": 0}
		anyMine := false
		for _, qv := range qual {
			if Map(qv)["coach"] != nil {
				anyMine = true
			}
		}
		if !anyMine {
			meState["state"] = "eliminated"
		}
		return
	}
	g := Map(List(phase["groups"])[0])
	teams := List(g["teams"])
	rounds := List(g["rounds"])
	winners := []int{}
	for _, pairV := range List(Map(rounds[Int(cur["round"])])["pairs"]) {
		leg := Map(List(Map(pairV)["legs"])[0])
		s := Map(leg["shootout"])
		if s == nil {
			s = Map(leg["score"])
		}
		if Int(s["home"]) > Int(s["away"]) {
			winners = append(winners, Int(leg["homeidx"]))
		} else {
			winners = append(winners, Int(leg["awayidx"]))
		}
	}
	myIdx := -1
	for i, tv := range teams {
		if Map(tv)["coach"] != nil {
			myIdx = i
			break
		}
	}
	if myIdx >= 0 && Str(meState["state"]) == "playing" {
		inWinners := false
		for _, w := range winners {
			if w == myIdx {
				inWinners = true
			}
		}
		playedMine := false
		for _, pairV := range List(Map(rounds[Int(cur["round"])])["pairs"]) {
			leg := Map(List(Map(pairV)["legs"])[0])
			if (leg["homeidx"] != nil && Int(leg["homeidx"]) == myIdx) ||
				(leg["awayidx"] != nil && Int(leg["awayidx"]) == myIdx) {
				playedMine = true
			}
		}
		if !inWinners && playedMine {
			meState["state"] = "eliminated"
		}
	}
	if Int(cur["round"])+1 < len(rounds) {
		nextPairs := List(Map(rounds[Int(cur["round"])+1])["pairs"])
		for k, pairV := range nextPairs {
			legs := List(Map(pairV)["legs"])
			legs[0] = map[string]any{"homeidx": winners[2*k], "awayidx": winners[2*k+1]}
		}
		cur["round"] = Int(cur["round"]) + 1
	} else {
		cup["finished"] = true
		if Str(meState["state"]) == "playing" {
			won := false
			for _, w := range winners {
				if w == myIdx {
					won = true
				}
			}
			if won {
				meState["state"] = "won"
			} else {
				meState["state"] = "eliminated"
			}
		}
	}
}

// CupResources ports hc.cup_resources.
// CupResources serves the cup with its meta's trophy and skin
// injected: cups saved before those fields were stored (legacy
// imports) would otherwise make CupMainScreen draw
// "trophies/tundefined.png" (upstream issue #36).
func (r *Roster) CupResources(cup map[string]any) []any {
	uri := "/cup/" + Str(cup["_id"])
	body := cup
	if Str(cup["trophy"]) == "" || Str(cup["skin"]) == "" {
		if meta := r.CupMetaByID(Int(cup["metaid"])); meta != nil {
			body = map[string]any{}
			for k, v := range cup {
				body[k] = v
			}
			if Str(cup["trophy"]) == "" {
				body["trophy"] = meta["trophy"]
			}
			if Str(cup["skin"]) == "" {
				body["skin"] = meta["skin"]
			}
		}
	}
	return []any{map[string]any{"resource": uri, "uri": uri, "body": body}}
}

// CupRewardEP ports hc.cup_reward_ep.
func (r *Roster) CupRewardEP(cup map[string]any) int {
	state := Str(Map(List(cup["coaches"])[0])["state"])
	meta := r.CupMetaByID(Int(cup["metaid"]))
	if meta == nil {
		meta = r.CupMetaByID(1)
	}
	brList := List(meta["bestrewards"])
	prize := Int(Map(List(brList[len(brList)-1])[0])["count"])
	if state == "won" {
		return prize
	}
	if state != "eliminated" {
		return 0
	}
	phases := List(cup["phases"])
	var ko map[string]any
	if len(phases) > 1 {
		ko = Map(List(Map(phases[1])["groups"])[0])
	}
	mine := -1
	if ko != nil {
		for i, tv := range List(ko["teams"]) {
			if Map(tv)["coach"] != nil {
				mine = i
				break
			}
		}
	}
	if mine < 0 {
		return prize / 10
	}
	played := 0
	for _, rv := range List(ko["rounds"]) {
		for _, pairV := range List(Map(rv)["pairs"]) {
			leg := Map(List(Map(pairV)["legs"])[0])
			if ((leg["homeidx"] != nil && Int(leg["homeidx"]) == mine) ||
				(leg["awayidx"] != nil && Int(leg["awayidx"]) == mine)) && leg["score"] != nil {
				played++
			}
		}
	}
	shares := []int{20, 35, 50}
	if len(List(ko["rounds"])) == 4 {
		shares = []int{20, 35, 50, 75}
	}
	idx := played - 1
	if idx < 0 {
		idx = 0
	}
	if idx > len(shares)-1 {
		idx = len(shares) - 1
	}
	return prize * shares[idx] / 100
}

// CupByMetaID ports hc._cup_bymetaid.
func CupByMetaID(st State, r map[string]any) map[string]any {
	bm := Map(r["bymetaid"])
	if bm == nil {
		bm = map[string]any{}
		r["bymetaid"] = bm
	}
	ongoing := map[int]int{}
	for _, cv := range MapSlice(st["cups"]) {
		if cv["finished"] != true {
			ongoing[Int(cv["metaid"])]++
		}
	}
	for m, n := range ongoing {
		e := Map(bm[fmt.Sprint(m)])
		if e == nil {
			e = map[string]any{"joincount": 0, "wins": 0, "challenges": 0}
			bm[fmt.Sprint(m)] = e
		}
		if Int(e["challenges"]) < n {
			e["challenges"] = n
		}
		if Int(e["joincount"]) < n {
			e["joincount"] = n
		}
	}
	return bm
}

// MapSlice reads a map-of-maps state value (e.g. state["cups"]).
func MapSlice(v any) []map[string]any {
	m := Map(v)
	out := []map[string]any{}
	for _, e := range m {
		if em := Map(e); em != nil {
			out = append(out, em)
		}
	}
	return out
}

// CupsOf returns the state's cup map (created when absent).
func CupsOf(st State) map[string]any {
	cups := Map(st["cups"])
	if cups == nil {
		cups = map[string]any{}
		st["cups"] = cups
	}
	return cups
}

// IsNationalTeam reports whether teamid is a national team.
func (r *Roster) IsNationalTeam(teamid int) bool {
	_, ok := r.nationalTeams[teamid]
	return ok
}

// NationalTeamOfNation ports _CLUB_NATIONS[nid]["teamid"].
func (r *Roster) NationalTeamOfNation(nationid int) int {
	if n := r.clubNations[nationid]; n != nil {
		return Int(n["teamid"])
	}
	return 0
}
