package game

// Match core, ported from hc: tactics, roster() AI teams, match_side,
// new_match, match_reward (+ post-match cards), contract consumption
// and the duplicate-spid uniquifier. Match state lives at
// state["match"], exactly as in Python.

import (
	"encoding/json"
	"fmt"
	"math"
	mrand "math/rand"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const TacticSlots = 6
const HalfLength = 30

// ---------- tactics ----------

// DefaultTactic is hc.default_tactic (extracted presets).
func (r *Roster) DefaultTactic(i int) map[string]any {
	if i >= 0 && i < len(r.leagueTactics) {
		return deepCopyMap(r.leagueTactics[i])
	}
	return map[string]any{"name": fmt.Sprintf("Strategy %d", i+1),
		"bus": map[string]any{}, "cc": map[string]any{}, "def": map[string]any{}}
}

// TacticsOf ports hc._tactics: the saved per-slot dict + the 6-list.
func (r *Roster) TacticsOf(st State) []map[string]any {
	slot := itoa(DefaultSlot(st))
	tac := Map(st["tactics"])
	if tac == nil {
		tac = map[string]any{}
		st["tactics"] = tac
	}
	saved := Map(tac[slot])
	if saved == nil {
		saved = map[string]any{}
		tac[slot] = saved
	}
	out := []map[string]any{}
	for i := 0; i < TacticSlots; i++ {
		t := deepCopyMap(Map(saved[itoa(i)]))
		if t == nil {
			t = map[string]any{}
		}
		def := r.DefaultTactic(i)
		for k, v := range def {
			if _, ok := t[k]; !ok {
				t[k] = v
			}
		}
		name := strings.TrimSpace(Str(t["name"]))
		if name == "" || name == fmt.Sprintf("Strategy %d", i+1) || name == fmt.Sprintf("Tactic %d", i+1) {
			t["name"] = def["name"]
			if Map(t["bus"]) == nil {
				for k, v := range def {
					t[k] = v
				}
			}
		}
		out = append(out, t)
	}
	return out
}

// TacticsSaved returns the raw per-slot saved-tactics dict (the
// first return value of hc._tactics), creating the containers.
func (r *Roster) TacticsSaved(st State) map[string]any {
	slot := itoa(DefaultSlot(st))
	tac := Map(st["tactics"])
	if tac == nil {
		tac = map[string]any{}
		st["tactics"] = tac
	}
	saved := Map(tac[slot])
	if saved == nil {
		saved = map[string]any{}
		tac[slot] = saved
	}
	return saved
}

// MatchTacticPayload ports hc.match_tactic_payload.
func (r *Roster) MatchTacticPayload(tactics []map[string]any) []any {
	indexed := map[int]map[string]any{}
	for _, t := range tactics {
		if t == nil {
			continue
		}
		idx := Int(t["id"])
		if _, has := t["id"]; has && idx >= 0 && idx < TacticSlots {
			if _, dup := indexed[idx]; !dup {
				indexed[idx] = t
			}
		}
	}
	out := []any{}
	for i := 0; i < TacticSlots; i++ {
		base := r.DefaultTactic(i)
		if saved := indexed[i]; saved != nil {
			for k, v := range saved {
				if (k == "bus" || k == "cc" || k == "def") && Map(v) != nil && Map(base[k]) != nil {
					merged := Map(base[k])
					for kk, vv := range Map(v) {
						merged[kk] = vv
					}
					base[k] = merged
				} else {
					base[k] = v
				}
			}
		}
		if strings.TrimSpace(Str(base["name"])) == "" {
			base["name"] = fmt.Sprintf("Strategy %d", i+1)
		}
		base["id"] = i
		out = append(out, base)
	}
	return out
}

// ---------- roster() AI teams ----------

// EnsurePlayerStaticdata ports hc._ensure_player_staticdata over a
// {summary, team} result (or a bare team).
func EnsurePlayerStaticdata(res map[string]any) map[string]any {
	if res == nil {
		return res
	}
	team := res
	if t := Map(res["team"]); t != nil {
		team = t
	}
	for _, pv := range List(team["players"]) {
		p := Map(pv)
		if p == nil {
			continue
		}
		p["id"] = itoa(firstNonZero(Int(p["spid"]), parseIntLoose(p["id"])))
		p["spid"] = firstNonZero(Int(p["spid"]), parseIntLoose(p["id"]))
		if _, ok := p["expandme"]; !ok {
			p["expandme"] = "player"
		}
		p["applyliveboost"] = 0
		if sd := Map(p["staticdata"]); sd != nil {
			if _, ok := sd["boosts"]; !ok {
				sd["boosts"] = map[string]any{"ovr": 0}
			}
			if _, ok := sd["stats"]; !ok {
				stats := make([]any, 40)
				for i := range stats {
					stats[i] = 75
				}
				sd["stats"] = stats
			}
			if _, ok := sd["statusAttribute"]; !ok {
				sd["statusAttribute"] = map[string]any{}
			}
			if _, ok := sd["preferredposovr"]; !ok {
				sd["preferredposovr"] = firstNonZero(Int(p["overallrating"]), Int(p["ovr"]), 75)
			}
			if _, ok := sd["names"]; !ok {
				name := Str(p["name"])
				if name == "" {
					name = "Cầu thủ"
				}
				sd["names"] = map[string]any{"short": name, "full": name}
			}
			sd["applyliveboost"] = 0
		}
	}
	return res
}

func parseIntLoose(v any) int {
	switch n := v.(type) {
	case string:
		var out int
		fmt.Sscanf(n, "%d", &out)
		return out
	default:
		return Int(v)
	}
}

func firstNonZero(vals ...int) int {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

// RosterRaw ports hc.roster(): {summary, team} of a club — the
// authentic-team response file when present, else GenTeam.
func (r *Roster) RosterRaw(teamid int) map[string]any {
	if nat, isNational := r.nationalTeams[teamid]; isNational {
		res := r.GenTeam(teamid, nat, true)
		if res != nil {
			if sum := Map(res["summary"]); sum != nil {
				if info := r.clubTeams[teamid]; info != nil {
					sum["name"] = info["teamname"]
				}
				sum["league"] = map[string]any{"id": 78}
				if nation := r.clubNations[nat]; nation != nil {
					sum["nation"] = map[string]any{"id": nation["nationid"], "name": nation["nationname"]}
				}
				sum["rival"] = map[string]any{"name": ""}
			}
			return EnsurePlayerStaticdata(res)
		}
	}
	if b, err := readFileCached(filepath.Join(r.responsesDir,
		fmt.Sprintf("static_info_authentic_team_all_%d.json", teamid))); err == nil {
		var res map[string]any
		if json.Unmarshal(b, &res) == nil {
			return res
		}
	}
	res := r.GenTeam(teamid, 0, false)
	if res == nil {
		res = map[string]any{"summary": map[string]any{},
			"team": map[string]any{"players": []any{},
				"squad": map[string]any{"positions": []any{}, "kickers": []any{}}}}
	}
	return EnsurePlayerStaticdata(res)
}

// RosterTeam ports hc.roster_team.
func (r *Roster) RosterTeam(teamid int) map[string]any {
	res := r.RosterRaw(teamid)
	if t := Map(res["team"]); t != nil {
		return t
	}
	return map[string]any{"players": []any{}, "squad": map[string]any{"positions": []any{}, "kickers": []any{}}}
}

// ---------- match construction ----------

func matchKit(teamid int) map[string]any {
	av := map[string]any{}
	for k, t := range map[string]int{"home": 0, "away": 1, "goalie": 2} {
		av[k] = map[string]any{"id": teamid<<5 | t}
	}
	return map[string]any{"availables": av}
}

// MatchUniqueDuplicateSpids ports hc._match_unique_duplicate_spids:
// repeated copies of one card get a different season's spid inside
// the transient match payload only.
func (r *Roster) MatchUniqueDuplicateSpids(team map[string]any) {
	players := []map[string]any{}
	for _, pv := range List(team["players"]) {
		if p := Map(pv); p != nil {
			players = append(players, p)
		}
	}
	spids := []int{}
	seen := map[int]bool{}
	dup := map[int]bool{}
	for _, p := range players {
		s := Int(p["spid"])
		spids = append(spids, s)
		if seen[s] {
			dup[s] = true
		}
		seen[s] = true
	}
	if len(dup) == 0 {
		return
	}
	pidOf := map[int]int{}
	idSet := map[int]bool{}
	for s := range dup {
		pid := r.PlayerID(s)
		pidOf[s] = pid
		if pid != 0 {
			idSet[pid] = true
		}
	}
	if len(idSet) == 0 {
		return
	}
	ids := []int{}
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	variants := map[int][][2]int{}
	for off := 0; off < len(ids); off += 500 {
		chunk := ids[off:min(off+500, len(ids))]
		rows, err := r.queryPlayers(fmt.Sprintf("playerid IN (%s)", qmarks(len(chunk))), intsToAny(chunk)...)
		if err != nil {
			return
		}
		for _, row := range rows {
			variants[row.PlayerID] = append(variants[row.PlayerID], [2]int{row.Spid, row.Overall})
		}
	}
	used := map[int]bool{}
	for _, s := range spids {
		used[s] = true
	}
	replacement := map[string]int{}
	for original := range dup {
		playerid := pidOf[original]
		if playerid == 0 {
			continue
		}
		var group []map[string]any
		for _, p := range players {
			if Int(p["spid"]) == original {
				group = append(group, p)
			}
		}
		target := r.CardOvr(original)
		for _, p := range group[1:] {
			type cand struct{ spid, rating int }
			var cands []cand
			for _, v := range variants[playerid] {
				if !used[v[0]] && v[0] != original {
					cands = append(cands, cand{v[0], v[1]})
				}
			}
			sort.Slice(cands, func(i, j int) bool {
				di, dj := math.Abs(float64(cands[i].rating-target)), math.Abs(float64(cands[j].rating-target))
				if di != dj {
					return di < dj
				}
				return cands[i].spid < cands[j].spid
			})
			if len(cands) == 0 {
				continue
			}
			rep := cands[0].spid
			used[rep] = true
			oldID := Str(p["id"])
			if oldID == "" {
				oldID = Str(p["_id"])
			}
			if oldID == "" {
				oldID = itoa(original)
			}
			replacement[oldID] = rep
			p["spid"] = rep
		}
	}
	if len(replacement) > 0 {
		squad := Map(team["squad"])
		for _, pv := range List(squad["positions"]) {
			pos := Map(pv)
			if rep, ok := replacement[Str(pos["id"])]; ok {
				pos["spid"] = rep
			}
		}
	}
}

func qmarks(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func intsToAny(in []int) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// MatchSide ports hc.match_side.
func (r *Roster) MatchSide(raw map[string]any, teamid int, user map[string]any, tactics []map[string]any) map[string]any {
	kit := matchKit(teamid)
	team := r.OwnedTeam(raw)
	r.MatchUniqueDuplicateSpids(team)
	team["teamid"] = teamid
	team["summary"] = r.TeamSummaryOf(team, teamid)
	team["kit"] = kit
	if user != nil {
		if List(team["teamcolors"]) == nil {
			team["teamcolors"] = []any{}
		}
		team["formations"] = r.TeamFormations(teamid)
	}
	// Bot sides keep the teamcolors OwnedTeam computed: fx sets
	// active_teamcolors on bot teams too (fx:1928, fx:2338), and the
	// client applies the boosts in-match from this block. Zeroing
	// them (the old Go behavior) silently stripped bot team colors.
	var players []any
	if user != nil {
		players = []any{user}
	} else {
		players = []any{}
	}
	return map[string]any{
		"team": team, "squad": team["squad"], "players": players,
		"tactics": r.MatchTacticPayload(tactics), "ready": user == nil,
		"kit": kit, "club": map[string]any{"clubname": ""},
	}
}

// NewMatch ports hc.new_match.
func NewMatch(mtype string, left, right map[string]any, extra map[string]any) map[string]any {
	// The match id must be NUMERIC: the client's
	// relaySessionInfo.matchid is parseInt(resourceId) and the
	// relay packet header carries it as u32 — a 24-char hex id
	// collapses to its leading decimal digits (or NaN), so two
	// clients could rendezvous on different match ids (upstream
	// uses str(int(time.time()) % 1000000000) for the same
	// reason). 10 digits: seconds (8) + two entropy digits.
	// UnixMilli % 1e9: at most 9 digits, always < 2^31 (the
	// header field is u32 and native code may read it as a
	// signed int; a 10-digit id overflows both and crashed
	// the client at the room->match transition, bak-2215).
	mid := fmt.Sprintf("%d", time.Now().UnixMilli()%1000000000)
	match := map[string]any{
		"id": mid, "type": mtype,
		"created": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"stage":   "created",
		"side":    map[string]any{"left": left, "right": right},
		"elo": map[string]any{
			"left":  map[string]any{"rating": 1000},
			"right": map[string]any{"rating": 1000}},
		"result": nil, "reward": nil, "boostweekid": 0,
		"gamesettings": map[string]any{
			"home": true, "simulated": false, "canHaveReferee": false,
			"stadiumchange": false, "timechange": false, "weatherchange": false,
			"matchtimechange": false, "cardstrictness": 0, "foulstrictness": 0,
			"kit": []any{"home", "away"},
			"ball": map[string]any{"id": 0, "availables": []any{0}},
			// tod/weather are stored DECODED: the screens send
			// stepper index + 10000 on the wire and
			// matchGamesettings decodes before storing, because
			// the client's display sync (tod switch, tod % 3,
			// stadium find-by-id) only works on plain values.
			// weather's domain is the index itself (0 = dry);
			// tod's domain is 1 = DAY / 3 = NIGHT (the client's
			// tod tables), so the default is 1, not 0.
			"stadium": 112, "tod": 1, "weather": 0, "difficulty": 0,
			"halfLength": HalfLength,
			"gameSpeed": 1, "crowd": 100, "pitchWear": 1, "refereeIdSelector": 845,
			"subRefereeIds": []any{0, 0}, "disableMomentum": false, "bookingsOn": true,
			"offsidesOn": true, "injuriesOn": true, "foulsOn": true, "fatigueOn": true,
			"subOnBench": 5, "availableSubs": 3, "drawMode": 0, "gameType": mtype,
			"shootoutmode": false,
			"prevScore":    map[string]any{"home": 0, "away": 0},
		},
	}
	for k, v := range extra {
		match[k] = v
	}
	return match
}

// BallItemBase: a match ball with id N is sold in the shop as item
// BallItemBase+N (type "BALL", effect {"ball": N}); owning the item
// unlocks the ball in the match settings screen.
const BallItemBase = 960000

// BallAvailables returns the gamesettings.ball.availables list for a
// coach: raw ball ids — the default ball 0 plus every ball whose shop
// item the coach owns. Entries must be plain numbers: the client's
// MatchProxy maps each entry to {label: BALL_NAME_TYPE<id>, id} for
// the settings screen's ball stepper.
func (r *Roster) BallAvailables(st State) []any {
	av := []any{0}
	coach := CoachBySlot(st, DefaultSlot(st))
	if coach == nil {
		return av
	}
	t := Tables(r.gamedataDir)
	seen := map[int]bool{0: true}
	for _, raw := range List(coach["items"]) {
		it := Map(raw)
		if it == nil || Int(it["remains"]) <= 0 {
			continue
		}
		def, ok := t.Items[Int(it["itemid"])]
		if !ok {
			continue
		}
		if bid := Int(def.Effect["ball"]); bid > 0 && !seen[bid] {
			seen[bid] = true
			av = append(av, bid)
		}
	}
	return av
}

// ApplyBallAvailables stamps a coach's ball availables onto a match.
func ApplyBallAvailables(match map[string]any, av []any) {
	gs := Map(match["gamesettings"])
	if gs == nil {
		return
	}
	ball := Map(gs["ball"])
	if ball == nil {
		ball = map[string]any{"id": 0}
		gs["ball"] = ball
	}
	ball["availables"] = av
}

// ExpWin/ExpDraw/ExpLoss are hc's (coach, player) exp per result.
var (
	ExpWin  = [2]int{300, 150}
	ExpDraw = [2]int{200, 100}
	ExpLoss = [2]int{100, 60}
)

// AddEP ports hc.add_ep.
func AddEP(st State, n int64) {
	if coach := CoachBySlot(st, DefaultSlot(st)); coach != nil {
		coach["ep"] = Int64(coach["ep"]) + n
	}
}

// ---------- rewards & contracts ----------

var matchCardPool = []struct {
	weight int
	prize  any // int = EP amount marker via epPrize, else itemid int
}{
	{30, 25000}, {25, 75000}, {15, 125000}, {8, 250000},
	{10, -28001}, {7, -45013}, {5, -82214180}, // negative = itemid
}

// MatchCardsReward ports hc._match_cards_reward.
func (r *Roster) MatchCardsReward(st State) map[string]any {
	total := 0
	for _, e := range matchCardPool {
		total += e.weight
	}
	draw := func() any {
		n := mrand.Intn(total)
		for _, e := range matchCardPool {
			if n < e.weight {
				return e.prize
			}
			n -= e.weight
		}
		return matchCardPool[0].prize
	}
	cards := []any{draw(), draw(), draw()}
	coach := CoachBySlot(st, DefaultSlot(st))
	if epAmt, isEP := cards[0].(int); isEP && epAmt > 0 {
		AddEP(st, int64(epAmt))
	} else if itemid, isItem := cards[0].(int); isItem && itemid < 0 && coach != nil {
		InventoryAdd(coach, -itemid, 1)
	}
	availables := []any{}
	for _, c := range cards {
		if amt, isEP := c.(int); isEP && amt > 0 {
			availables = append(availables, map[string]any{
				"itemid": EPItemID, "count": amt, "staticdata": EPItemDef})
		} else if itemid, isItem := c.(int); isItem {
			availables = append(availables, map[string]any{
				"itemid": -itemid, "count": 1, "staticdata": r.ItemDef(-itemid)})
		}
	}
	pick := map[string]any{"normal": []any{0}}
	return map[string]any{"extra": map[string]any{"selected": pick, "availables": availables},
		"selected": pick}
}

// MatchReward ports hc.match_reward (the x25 EP scale included).
func (r *Roster) MatchReward(st State, stats map[string]any) map[string]any {
	meName := Str(stats["myTeam"])
	if meName == "" {
		meName = "home"
	}
	oppName := Str(stats["opponentTeam"])
	if oppName == "" {
		oppName = "away"
	}
	score := func(side string) int {
		return Int(Map(stats[side])["score"])
	}
	won, drew := score(meName) > score(oppName), score(meName) == score(oppName)
	ep := CfgI("reward_loss_ep", 12500)
	cexp, pexp := ExpLoss[0], ExpLoss[1]
	if won {
		ep, cexp, pexp = CfgI("reward_win_ep", 50000), ExpWin[0], ExpWin[1]
	} else if drew {
		ep, cexp, pexp = CfgI("reward_draw_ep", 25000), ExpDraw[0], ExpDraw[1]
	}
	// progression_mult is the owner's progression-speed dial
	// (set to 2 on 2026-11-14): it scales match EP, coach EXP
	// and player EXP together; the ranked bonus in matchEnd
	// applies the same factor.
	if mult := CfgF("progression_mult", 1); mult != 1 {
		ep = int(float64(ep)*mult + 0.5)
		cexp = int(float64(cexp)*mult + 0.5)
		pexp = int(float64(pexp)*mult + 0.5)
	}
	reward := func(n int) map[string]any {
		return map[string]any{"total": n, "detail": map[string]any{"base": n, "extra": 0}}
	}
	coach := CoachBySlot(st, DefaultSlot(st))
	epBefore := int64(0)
	if coach != nil {
		epBefore = Int64(coach["ep"])
	}
	var prev, cur map[string]any
	if coach != nil {
		prev, cur = GainExp(coach, cexp, true)
	}
	team := TeamOf(st, DefaultSlot(st))
	if team == nil {
		team = map[string]any{"players": []any{}}
	}
	team["staffsmc"] = Int(team["staffsmc"]) + 1
	done := map[string]bool{}
	players := []any{}
	inSquad := map[string]bool{}
	if squad := Map(team["squad"]); squad != nil {
		for _, pv := range List(squad["positions"]) {
			inSquad[Str(Map(pv)["id"])] = true
		}
	}
	for _, sv := range List(Map(stats[meName])["players"]) {
		s := Map(sv)
		if s == nil {
			continue
		}
		pid := Int(s["spid"]) % 1000000
		var mine []map[string]any
		for _, pv := range List(team["players"]) {
			p := Map(pv)
			if p != nil && Int(p["spid"])%1000000 == pid && !done[IID(p)] {
				mine = append(mine, p)
			}
		}
		var p map[string]any
		for _, cand := range mine {
			if inSquad[IID(cand)] {
				p = cand
				break
			}
		}
		if p == nil && len(mine) > 0 {
			p = mine[0]
		}
		if p == nil {
			continue
		}
		n := pexp + 20*Int(s["goals"]) + 10*Int(s["assists"])
		pprev, pcur := GainExp(p, n, false)
		done[IID(p)] = true
		players = append(players, map[string]any{"id": IID(p), "spid": Int(p["spid"]),
			"prev": pprev, "current": pcur, "reward": reward(n)})
	}
	out := map[string]any{"matchRewardBackType": 0}
	for k, v := range r.MatchCardsReward(st) {
		out[k] = v
	}
	out["coach"] = map[string]any{"reward": reward(cexp), "prev": prev, "current": cur}
	out["money"] = map[string]any{"reward": reward(ep), "prev": epBefore, "current": epBefore + int64(ep)}
	out["players"] = players
	out["missions"] = []any{}
	out["exptankinfo"] = map[string]any{}
	return out
}

// ConsumeMatchContracts ports hc.consume_match_contracts.
func ConsumeMatchContracts(st State) int {
	team := TeamOf(st, DefaultSlot(st))
	if team == nil {
		return 0
	}
	registered := map[string]bool{}
	if squad := Map(team["squad"]); squad != nil {
		for _, pv := range List(squad["positions"]) {
			if id := Str(Map(pv)["id"]); id != "" {
				registered[id] = true
			}
		}
	}
	players := []map[string]any{}
	for _, pv := range List(team["players"]) {
		if p := Map(pv); p != nil {
			players = append(players, p)
		}
	}
	if len(registered) == 0 {
		for _, p := range players {
			if p["team2"] != true {
				registered[IID(p)] = true
			}
		}
	}
	consumed := 0
	for _, p := range players {
		if !registered[IID(p)] {
			continue
		}
		if remaining := PlayerLife(p); remaining > 0 {
			p["life"] = remaining - 1
			consumed++
		}
	}
	return consumed
}

// ConsumeStaffContracts burns one contract match from every
// APPOINTED staff member (the ones in staffroles), mirroring
// ConsumeMatchContracts for players (owner directive 2026-11-15:
// staff contracts run 40 matches and tick down on every match
// played, whatever the mode). Legacy staff above the cap are
// clamped to the cap first; a staff with no life key is at full
// contract (StaffLife semantics). Unappointed reserves do not
// burn, just like benched players.
func ConsumeStaffContracts(st State) int {
	team := TeamOf(st, DefaultSlot(st))
	if team == nil {
		return 0
	}
	appointed := map[string]bool{}
	for _, rv := range List(team["staffroles"]) {
		if id := Str(Map(rv)["staffid"]); id != "" {
			appointed[id] = true
		}
	}
	if len(appointed) == 0 {
		return 0
	}
	consumed := 0
	for _, sv := range List(team["staffs"]) {
		s := Map(sv)
		if s == nil || !appointed[Str(s["_id"])] {
			continue
		}
		life := StaffLife(s, RecontractStaffMaxLife)
		if life > RecontractStaffMaxLife {
			life = RecontractStaffMaxLife
		}
		if life > 0 {
			s["life"] = life - 1
			consumed++
		}
	}
	return consumed
}

// InSquadPlayers is a small helper for other domains.
func InSquad(team map[string]any, p map[string]any) bool {
	squad := Map(team["squad"])
	for _, pv := range List(squad["positions"]) {
		if Str(Map(pv)["id"]) == IID(p) {
			return true
		}
	}
	return false
}
