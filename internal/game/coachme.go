package game

import (
	"fmt"
	"time"
)

const (
	StartEP    = 1000000000
	VIPDetail  = 2
	MaxPlayers = 200
	FirstTeamMax = 36
	ContractMax  = 40
)

// VIP fields follow the admin-granted entitlement (see
// settings.go): no grant -> no VIP badge, no expiry, empty status.
func vipDetailOf(st State) int {
	if VIPActive(st) {
		return VIPDetail
	}
	return 0
}

func vipStatusOf(st State) map[string]any {
	if VIPActive(st) {
		return map[string]any{"VIP1": EntitlementUntil(st, "vip")}
	}
	return map[string]any{}
}

// W3C formats a unix timestamp the way hc.w3c does.
func W3C(ts float64) string {
	t := time.Unix(int64(ts), int64((ts-float64(int64(ts)))*1e9)).UTC()
	return t.Format("2006-01-02T15:04:05.000Z")
}

// WeekEndTs is hc._week_end_ts: next Monday 00:00 (UTC+7).
func WeekEndTs() float64 {
	loc := time.FixedZone("ICT", 7*3600)
	now := time.Now().In(loc)
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	wd := (int(d.Weekday()) + 6) % 7 // Monday = 0
	days := (7 - wd) % 7
	if days == 0 {
		days = 7
	}
	return float64(d.AddDate(0, 0, days).Unix())
}

// GPBalance is fx/hc _gp_balance: this week's GP (0 after the reset).
func GPBalance(s State) int {
	q := Map(s["quest"])
	if q == nil {
		return 0
	}
	if Int64(q["week"]) == int64(WeekEndTs()) {
		return Int(q["gp"])
	}
	return 0
}

func playerNeed(level int) int { return 100 * level }
func coachNeed(level int) int  { return 500 + 100*(level-1) }

// MakeLevelinfo is hc.make_levelinfo.
func MakeLevelinfo(info any, need func(int) int, top int) map[string]any {
	m := Map(info)
	level := 1
	if m != nil {
		level = Int(m["level"])
	}
	if level < 1 {
		level = 1
	}
	if level > top {
		level = top
	}
	exp := 0
	if level < top && m != nil {
		exp = Int(m["exp"])
	}
	return map[string]any{"level": level, "exp": exp, "to": need(level)}
}

// PlayerLevelinfo / CoachLevelinfo (hc).
func PlayerLevelinfo(p map[string]any) map[string]any {
	return MakeLevelinfo(p["levelinfo"], playerNeed, 23)
}
func CoachLevelinfo(c map[string]any) map[string]any {
	return MakeLevelinfo(c["levelinfo"], coachNeed, 100)
}

// PlayerLife is hc._player_life.
func PlayerLife(p map[string]any) int {
	v, ok := p["life"]
	if !ok || v == nil {
		return ContractMax
	}
	n := Int(v)
	if n < 0 {
		n = 0
	}
	if n > ContractMax {
		n = ContractMax
	}
	return n
}

// IID is hc.iid: the player instance id.
func IID(p map[string]any) string {
	if id := Str(p["_id"]); id != "" {
		return id
	}
	return fmt.Sprint(Int(p["spid"]))
}

// DefaultCoachStatistics is hc.default_coach_statistics (verbatim shape,
// including the upstream "noramlbest" typo the client tolerates).
func DefaultCoachStatistics() map[string]any {
	zero5 := func() []any { return []any{0, 0, 0, 0, 0} }
	friendRow := func() map[string]any {
		return map[string]any{"wins": zero5(), "draws": zero5(), "losses": zero5(),
			"total": zero5(), "rate": zero5(), "winningstreak": map[string]any{"max": []any{0}, "curr": 0}}
	}
	simRow := func() map[string]any {
		return map[string]any{"wins": 0, "draws": 0, "losses": 0, "total": 0, "rate": 0}
	}
	return map[string]any{
		"club": map[string]any{
			"ranked":   map[string]any{"total": 0, "wins": 0, "losses": 0, "forfeitwins": 0},
			"friendly": map[string]any{"total": 0, "wins": 0, "losses": 0, "forfeitwins": 0},
		},
		"league": map[string]any{
			"wins": 0, "draws": 0, "losses": 0, "goals": 0, "goalsconceded": 0,
			"winningstreak": map[string]any{"max": 0, "curr": 0},
			"leaguewins":    []any{0, 0, 0, 0},
			"normalwins":    0, "noramlbest": 0, "normalbest": 0,
			"ultimatebest": 0, "ultimatewins": 0,
		},
		"friendly": map[string]any{
			"custom": friendRow(), "quick": friendRow(), "vscom": friendRow(), "roleplay": friendRow(),
		},
		"simulation": map[string]any{
			"custom": simRow(), "quick": simRow(), "vscom": simRow(),
		},
		"ladder": map[string]any{
			"elo": 1000, "highelo": 1000,
			"division":      map[string]any{"name": "DIVISION_AMATEUR_C", "rank": 17},
			"rank":          0, "ranking": 0,
			"wins": 0, "draws": 0, "losses": 0, "forfeits": 0, "winningstreak": 0,
			"seasons": []any{},
			"highrecords": map[string]any{
				"count": 0, "elo": []any{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000},
				"rank": []any{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
				"division": func() []any {
					out := []any{}
					for i := 0; i < 10; i++ {
						out = append(out, map[string]any{"name": "DIVISION_AMATEUR_C", "rank": 17})
					}
					return out
				}(),
				"winningstreak": []any{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
				"wins":          []any{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
				"losses":        []any{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
				"date":          "2026-11-01T00:00:00.000Z",
			},
		},
		"winstreak": []any{map[string]any{"win": 0, "lose": 0}, map[string]any{"win": 0, "lose": 0}, map[string]any{"win": 0, "lose": 0}},
		"cup": map[string]any{
			"total": 0, "wins": 0, "draws": 0, "losses": 0, "abandonments": 0,
			"cupwins": []any{0, 0, 0, 0, 0}, "totalwins": 0, "wincups": 0, "totalprize": 0,
		},
		"boxingday": map[string]any{
			"wins": 0, "losses": 0, "draws": 0, "total": 0,
			"consecutive_wins": 0, "max_consecutive_wins": 0,
			"pvp": map[string]any{"maxstreak": 0, "currstreak": 0, "wins": 0, "draws": 0, "losses": 0, "bestrank": 0},
			"pve": map[string]any{"maxstreak": 0, "currstreak": 0, "wins": 0, "draws": 0, "losses": 0, "bestrank": 0},
		},
	}
}

// LadderDivisions is fx.LADDER_DIVISIONS (best first).
var LadderDivisions = []struct {
	Name   string
	Rating int
}{
	{"LEGENDARY_S", 1750}, {"LEGENDARY_A", 1700}, {"LEGENDARY_B", 1650}, {"LEGENDARY_C", 1600},
	{"WORLDCLASS_A", 1550}, {"WORLDCLASS_B", 1500}, {"WORLDCLASS_C", 1450},
	{"PROFESSIONAL_A", 1400}, {"PROFESSIONAL_B", 1350}, {"PROFESSIONAL_C", 1300},
	{"SEMIPRO_A", 1250}, {"SEMIPRO_B", 1200}, {"SEMIPRO_C", 1150},
	{"AMATEUR_A", 1100}, {"AMATEUR_B", 1050}, {"AMATEUR_C", 1000},
}

// DivisionForElo is fx._division_for_elo: (name, rank) with rank 1 = best.
// Note: Python returns rank 16 for AMATEUR_C in the fallback while the
// table position is 16; the statistics default uses rank 17 (the
// client-facing value from the ladder info file) -- callers that serve
// statistics use DivisionObject below.
func DivisionForElo(elo int) (string, int) {
	for i, d := range LadderDivisions {
		if elo >= d.Rating {
			return d.Name, i + 1
		}
	}
	return "AMATEUR_C", 16
}

// repairSquad is hc.repair_squad.
func (r *Roster) repairSquad(team map[string]any) {
	sq := Map(team["squad"])
	if sq == nil {
		return
	}
	players := List(team["players"])
	cards := map[string]map[string]any{}
	bySpid := map[string][]map[string]any{}
	for _, pv := range players {
		p := Map(pv)
		cards[IID(p)] = p
		k := fmt.Sprint(Int(p["spid"]))
		bySpid[k] = append(bySpid[k], p)
	}
	used := map[string]bool{}
	slots := map[int]bool{}
	var positions []map[string]any
	for _, ev := range List(sq["positions"]) {
		e := Map(ev)
		pid := Str(e["id"])
		if pid == "" {
			pid = fmt.Sprint(Int(e["spid"]))
		}
		pos := posSub
		if v, ok := e["pos"]; ok {
			pos = Int(v)
		}
		if pos < 0 || pos > posSub {
			continue
		}
		card := cards[pid]
		if card == nil || used[pid] {
			spid := Str(e["spid"])
			if spid == "" {
				spid = pid
			}
			var cands []map[string]any
			for _, p := range bySpid[spid] {
				if !used[IID(p)] {
					cands = append(cands, p)
				}
			}
			if len(cands) == 0 {
				continue
			}
			card = cands[0]
			for _, p := range cands[1:] {
				if GradeOf(p) > GradeOf(card) || (GradeOf(p) == GradeOf(card) && r.PlayerOvr(p) > r.PlayerOvr(card)) {
					card = p
				}
			}
			pid = IID(card)
		}
		if pos < posSub && (slots[pos] || len(slots) >= 11) {
			continue
		}
		ne := map[string]any{}
		for k, v := range e {
			ne[k] = v
		}
		ne["id"] = pid
		ne["spid"] = Int(card["spid"])
		positions = append(positions, ne)
		used[pid] = true
		if pos < posSub {
			slots[pos] = true
		}
	}
	var starters []map[string]any
	startingIDs := map[string]bool{}
	for _, p := range positions {
		if Int(p["pos"]) < posSub {
			starters = append(starters, p)
			startingIDs[Str(p["id"])] = true
		}
	}
	for _, f := range genFormation {
		if len(starters) >= 11 {
			break
		}
		pos := int(f[0])
		if slots[pos] {
			continue
		}
		var chosen map[string]any
		for _, p := range players {
			pm := Map(p)
			if startingIDs[IID(pm)] || pm["team2"] == true {
				continue
			}
			if chosen == nil || r.PlayerOvr(pm) > r.PlayerOvr(chosen) {
				chosen = pm
			}
		}
		if chosen == nil {
			break
		}
		pid := IID(chosen)
		starters = append(starters, map[string]any{
			"id": pid, "spid": Int(chosen["spid"]), "pos": pos,
			"starting": []any{1, 1, f[1], f[2]},
		})
		slots[pos] = true
		startingIDs[pid] = true
	}
	var bench []map[string]any
	for _, p := range positions {
		if Int(p["pos"]) == posSub && !startingIDs[Str(p["id"])] && len(bench) < 7 {
			bench = append(bench, p)
		}
	}
	selected := map[string]bool{}
	for id := range startingIDs {
		selected[id] = true
	}
	for _, b := range bench {
		selected[Str(b["id"])] = true
	}
	sortedPlayers := append([]map[string]any{}, func() []map[string]any {
		out := []map[string]any{}
		for _, pv := range players {
			out = append(out, Map(pv))
		}
		return out
	}()...)
	for i := 0; i < len(sortedPlayers); i++ {
		for j := i + 1; j < len(sortedPlayers); j++ {
			if r.PlayerOvr(sortedPlayers[j]) > r.PlayerOvr(sortedPlayers[i]) {
				sortedPlayers[i], sortedPlayers[j] = sortedPlayers[j], sortedPlayers[i]
			}
		}
	}
	for _, p := range sortedPlayers {
		if len(bench) >= 7 {
			break
		}
		if !selected[IID(p)] && p["team2"] != true {
			bench = append(bench, map[string]any{"id": IID(p), "spid": Int(p["spid"]), "pos": posSub})
			selected[IID(p)] = true
		}
	}
	finalPos := []any{}
	for _, p := range starters {
		finalPos = append(finalPos, p)
	}
	for _, p := range bench {
		finalPos = append(finalPos, p)
	}
	sq["positions"] = finalPos
	// fmt_Sprint, not Str: stored squads (static files, older
	// saves) can carry the captain as a NUMBER — Str() would read
	// "" and the captain got silently replaced (Real Madrid file
	// team: CR7 88020801 -> first starter).
	captain := fmt_Sprint(sq["captain"])
	if !startingIDs[captain] {
		captain = ""
		for _, p := range starters {
			if Int(p["pos"]) != 0 {
				captain = Str(p["id"])
				break
			}
		}
		sq["captain"] = captain
	} else {
		sq["captain"] = captain
	}
	var fallback []string
	for _, p := range starters {
		if Int(p["pos"]) != 0 {
			fallback = append(fallback, Str(p["id"]))
		}
	}
	kickers := List(sq["kickers"])
	newKickers := []any{}
	for i := 0; i < 5; i++ {
		if i < len(kickers) && startingIDs[fmt.Sprint(kickers[i])] {
			newKickers = append(newKickers, fmt.Sprint(kickers[i]))
		} else if len(fallback) > 0 {
			newKickers = append(newKickers, fallback[i%len(fallback)])
		} else {
			newKickers = append(newKickers, "")
		}
	}
	sq["kickers"] = newKickers
}

// squadPresets is hc.squad_presets.
func (r *Roster) squadPresets(team map[string]any) {
	stored := Map(team["teamsquadlist"])
	var entries []any
	if stored != nil {
		entries = List(stored["preset"])
	}
	active := Int(team["presetidx"])
	if active < 0 || active >= 3 {
		active = 0
	}
	presets := []any{}
	for i := 0; i < 3; i++ {
		var cand map[string]any
		if i < len(entries) && Map(entries[i]) != nil {
			cand = deepCopyMap(Map(entries[i]))
		} else {
			cand = deepCopyMap(Map(team["squad"]))
		}
		temp := map[string]any{"players": team["players"], "squad": cand}
		r.repairSquad(temp)
		presets = append(presets, temp["squad"])
	}
	presets[active] = deepCopyMap(Map(team["squad"]))
	team["presetidx"] = active
	team["teamsquadlist"] = map[string]any{"preset": presets}
}

func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopy(v)
	}
	return out
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopy(e)
		}
		return out
	}
	return v
}

// OwnedTeam is hc.owned_team (teamcolors computed by ActiveTeamColors;
// the in-match boost application pass is not ported).
func (r *Roster) OwnedTeam(raw map[string]any) map[string]any {
	team := deepCopyMap(raw)
	if team == nil {
		team = map[string]any{"squad": map[string]any{"positions": []any{}, "kickers": []any{}}, "players": []any{}}
	}
	if Map(team["squad"]) == nil {
		team["squad"] = map[string]any{"positions": []any{}, "kickers": []any{}}
	}
	if team["players"] == nil {
		team["players"] = []any{}
	}
	r.repairSquad(team)
	var newPlayers []any
	for _, pv := range List(team["players"]) {
		p := Map(pv)
		np := map[string]any{}
		for k, v := range p {
			if k != "team2" {
				np[k] = v
			}
		}
		if p["team2"] != true {
			np["state"] = "first"
		}
		np["grade"] = GradeOf(p)
		np["life"] = PlayerLife(p)
		np["levelinfo"] = PlayerLevelinfo(p)
		np["_id"] = IID(p)
		np["id"] = IID(p)
		np["price"] = r.CardPrice(Int(p["spid"]), GradeOf(p))
		if _, ok := np["expandme"]; !ok {
			np["expandme"] = "player"
		}
		newPlayers = append(newPlayers, np)
	}
	team["players"] = newPlayers
	sq := Map(team["squad"])
	var newPos []any
	hasSub := false
	for _, ev := range List(sq["positions"]) {
		e := Map(ev)
		ne := map[string]any{}
		for k, v := range e {
			ne[k] = v
		}
		id := Str(e["id"])
		if id == "" {
			id = fmt.Sprint(Int(e["spid"]))
		}
		ne["id"] = id
		if Int(e["pos"]) == posSub {
			hasSub = true
		}
		newPos = append(newPos, ne)
	}
	if !hasSub {
		used := map[string]bool{}
		for _, ev := range newPos {
			used[Str(Map(ev)["id"])] = true
		}
		var bench []map[string]any
		for _, pv := range newPlayers {
			p := Map(pv)
			if !used[Str(p["id"])] {
				bench = append(bench, p)
			}
		}
		for i := 0; i < len(bench); i++ {
			for j := i + 1; j < len(bench); j++ {
				if r.PlayerOvr(bench[j]) > r.PlayerOvr(bench[i]) {
					bench[i], bench[j] = bench[j], bench[i]
				}
			}
		}
		for i := 0; i < len(bench) && i < 7; i++ {
			newPos = append(newPos, map[string]any{"spid": Int(bench[i]["spid"]), "pos": posSub, "id": Str(bench[i]["id"])})
		}
	}
	sq["positions"] = newPos
	if sq["captain"] != nil {
		sq["captain"] = fmt.Sprint(sq["captain"])
	} else {
		sq["captain"] = ""
	}
	var kickers []any
	for _, k := range List(sq["kickers"]) {
		kickers = append(kickers, fmt.Sprint(k))
	}
	sq["kickers"] = kickers
	team["squad"] = sq
	if team["staffs"] == nil {
		team["staffs"] = []any{}
	}
	// Staff contract display cap (owner directive 2026-11-15:
	// 100 -> 40 matches). OwnedTeam serves a deep copy, so this
	// clamps what the client shows immediately; the stored value
	// converges via NormalizeStaffTeam / ConsumeStaffContracts,
	// which clamp before use, so display and mechanics agree.
	for _, sv := range List(team["staffs"]) {
		if s := Map(sv); s != nil {
			if _, present := s["life"]; present && Int(s["life"]) > RecontractStaffMaxLife {
				s["life"] = RecontractStaffMaxLife
			}
		}
	}
	if List(team["staffroles"]) == nil {
		roles := []any{}
		for i := 1; i <= 7; i++ {
			roles = append(roles, map[string]any{"roleidx": 1, "slotidx": i, "open": true, "staffid": nil})
		}
		team["staffroles"] = roles
	}
	if team["staffroleidx"] == nil {
		team["staffroleidx"] = 1
	}
	if team["maxstaff"] == nil {
		team["maxstaff"] = 30
	}
	if team["staffsmc"] == nil {
		team["staffsmc"] = 0
	}
	team["captain"] = sq["captain"]
	team["kickers"] = sq["kickers"]
	r.squadPresets(team)
	if team["formations"] == nil {
		team["formations"] = []any{}
	}
	team["teamcolors"] = r.ActiveTeamColors(team)
	return team
}

// teamSummary is hc.team_summary (+ the coach_me summary extras).
// TeamSummaryOf is the exported teamSummary (cross-package callers).
func (r *Roster) TeamSummaryOf(team map[string]any, teamid int) map[string]any {
	return r.teamSummary(team, teamid)
}

func (r *Roster) teamSummary(team map[string]any, teamid int) map[string]any {
	sum := r.RosterSummary(teamid)
	ovr := map[string]int{}
	players := map[string]map[string]any{}
	for _, pv := range List(team["players"]) {
		p := Map(pv)
		ovr[IID(p)] = r.PlayerOvr(p)
		players[IID(p)] = p
	}
	lines := map[string][2]int{"DF": {1, 9}, "MF": {9, 20}, "FW": {20, 28}}
	ovrOut := map[string]any{}
	for k, rg := range lines {
		var vals []int
		for _, ev := range List(Map(team["squad"])["positions"]) {
			e := Map(ev)
			if pos := Int(e["pos"]); pos >= rg[0] && pos < rg[1] {
				id := Str(e["id"])
				if id == "" {
					id = fmt.Sprint(Int(e["spid"]))
				}
				vals = append(vals, ovr[id])
			}
		}
		if len(vals) == 0 {
			ovrOut[k] = 0
		} else {
			s := 0
			for _, v := range vals {
				s += v
			}
			ovrOut[k] = int(float64(s)/float64(len(vals)) + 0.5)
		}
	}
	out := map[string]any{}
	for k, v := range sum {
		out[k] = v
	}
	out["id"] = teamid
	out["ovr"] = ovrOut
	return out
}

// CoachItems is hc.coach_items: one served entry per owned stack.
// Items the catalogue does not know are hidden; an owned Locker Room
// ticket is presented in the ticket shape. Every entry carries its
// staticdata -- without it the inventory (Kho) renders nothing, which
// is why bought packs "vanished" in the first Go test session.
func (r *Roster) CoachItems(s State, slot int) []any {
	coach := CoachBySlot(s, slot)
	if coach == nil {
		return []any{}
	}
	t := r.ShopTables()
	out := []any{}
	for _, iv := range List(coach["items"]) {
		it := Map(iv)
		if it == nil {
			continue
		}
		itemid := Int(it["itemid"])
		if itemid == LockerItemID {
			now := time.Now().UnixMilli()
			out = append(out, map[string]any{
				"_id": it["_id"], "id": it["_id"], "itemid": 801,
				"createat": now - 86400000, "expireat": now + 3650*86400000,
				"remains": it["remains"], "count": it["remains"],
				"expireable": true, "expired": false,
				"staticdata": map[string]any{
					"itemid": 801, "name": "Vé Phòng Thay Đồ",
					"category": "LOCKERROOM", "type": "LOCKERROOM", "itemicon": 801},
			})
			continue
		}
		_, inPacks := t.Packs[itemid]
		_, inItems := t.Items[itemid]
		if inPacks || inItems || IsPiece(itemid) {
			out = append(out, map[string]any{
				"_id": it["_id"], "id": it["_id"], "itemid": it["itemid"],
				"remains": it["remains"], "count": it["remains"],
				"expireable": true, "expired": false,
				"staticdata": r.ItemDef(itemid),
			})
		}
	}
	return out
}

var scouts = []struct {
	key, sid string
	quest    map[string]any
}{
	{"default_A01", "STR_SCOUTER_KR_DEFAULT_A01", nil},
	{"default_B01", "STR_SCOUTER_KR_DEFAULT_B01", map[string]any{"require_value": 100000}},
	{"igr", "STR_SCOUTER_KR_IGR_A01", map[string]any{"require_grade": 2, "require_grade_count": 2}},
	{"vip", "STR_SCOUTER_KR_VIP_A01", map[string]any{"require_season": "88", "require_season_count": 2}},
}

// CoachScouters is hc.coach_scouters (cooldowns are all zero here).
func CoachScouters(s State, slot int) []any {
	out := []any{}
	for _, sc := range scouts {
		e := map[string]any{
			"key": sc.key, "scoutid": sc.key,
			"scoutername": sc.sid + "_NAME", "scouterdesc": sc.sid + "_DESC",
			"lock": false,
		}
		if sc.quest != nil {
			e["quest"] = sc.quest
		}
		out = append(out, e)
	}
	return out
}

// CoachMe is hc.coach_me + the fx statistics treatment (ladder block
// rebuilt from the user's elo; winningstreak.max normalized to arrays).
func (r *Roster) CoachMe(s State, slot int, user *User) map[string]any {
	coach := CoachBySlot(s, slot)
	if coach == nil {
		coaches := Coaches(s)
		if len(coaches) > 0 {
			coach = coaches[0]
		} else {
			coach = map[string]any{"slot": slot, "name": "Manager", "teamid": 241, "currentbadge": 1001, "hiderank": false}
		}
	}
	teamid := Int(coach["teamid"])
	if teamid == 0 {
		teamid = 241
	}
	rawTeam := TeamOf(s, slot)
	if rawTeam == nil {
		rawTeam = map[string]any{"squad": map[string]any{"positions": []any{}, "kickers": []any{}}, "players": []any{}}
	}
	team := r.OwnedTeam(rawTeam)
	team["formations"] = r.TeamFormations(teamid)
	sq := Map(team["squad"])
	if sq["formation"] == nil {
		if forms := List(team["formations"]); len(forms) > 0 {
			sq["formation"] = Map(forms[0])["formationid"]
		}
	}
	stats := Map(coach["statistics"])
	if stats == nil {
		stats = DefaultCoachStatistics()
	} else {
		stats = deepMerge(DefaultCoachStatistics(), stats)
	}
	// friendly winningstreak.max must be an array (fx normalization).
	if fr := Map(stats["friendly"]); fr != nil {
		for _, kv := range fr {
			if row := Map(kv); row != nil {
				if ws := Map(row["winningstreak"]); ws != nil {
					if _, isList := ws["max"].([]any); !isList {
						ws["max"] = []any{Int(ws["max"])}
					}
				}
			}
		}
	}
	// winstreak entries need both win and lose: the Data tab renders
	// win+lose per difficulty and a missing lose shows as NaN.
	for _, ev := range List(stats["winstreak"]) {
		if e := Map(ev); e != nil {
			if _, ok := e["win"]; !ok {
				e["win"] = 0
			}
			if _, ok := e["lose"]; !ok {
				e["lose"] = 0
			}
		}
	}
	// Cup statistics from the coach's cup record (hc.coach_me).
	rec := Map(coach["record"])
	cupRec := map[string]any{}
	if rec != nil {
		cupRec = Map(rec["cup"])
	}
	if cupRec == nil {
		cupRec = map[string]any{}
	}
	cupwins := List(cupRec["cupwins"])
	for len(cupwins) < 5 {
		cupwins = append(cupwins, 0)
	}
	cupStats := map[string]any{
		"wins": Int(cupRec["wins"]), "draws": Int(cupRec["draws"]), "losses": Int(cupRec["losses"]),
		"abandonments": Int(cupRec["abandonments"]), "cupwins": cupwins[:5],
		"totalwins": Int(cupRec["totalwins"]), "wincups": Int(cupRec["totalwins"]),
		"totalprize": Int(cupRec["totalprize"]),
		"total":      Int(cupRec["wins"]) + Int(cupRec["draws"]) + Int(cupRec["losses"]),
	}
	if b := cupRec["best"]; b != nil {
		cupStats["highrecords"] = b
	}
	stats["cup"] = cupStats
	// Ladder block: elo from the account, division from the fx table.
	if user != nil {
		lad := Map(stats["ladder"])
		if lad == nil {
			lad = map[string]any{}
			stats["ladder"] = lad
		}
		name, _ := DivisionForElo(user.Elo)
		lad["elo"] = user.Elo
		if Int(lad["highelo"]) < user.Elo {
			lad["highelo"] = user.Elo
		}
		lad["division"] = map[string]any{"name": name, "rank": 17}
		if name == "AMATEUR_C" {
			lad["division"] = map[string]any{"name": name, "rank": 17}
		}
		if hr := Map(lad["highrecords"]); hr != nil {
			divs := []any{}
			for i := 0; i < 10; i++ {
				divs = append(divs, map[string]any{"name": name, "rank": 17})
			}
			hr["division"] = divs
		}
	}
	createdAt := Str(coach["createdat"])
	if createdAt == "" {
		createdAt = "2026-11-01T00:00:00.000Z"
	}
	playTime := Int(coach["playtime"])
	if playTime == 0 {
		playTime = 3600
	}
	career := Map(coach["career"])
	if career == nil {
		career = map[string]any{
			"clubs": 1, "maxplayers": len(List(team["players"])),
			"buyplayers": 0, "sellplayers": 0, "maxbuy": nil, "maxsell": nil,
			"appat": createdAt,
		}
	}
	teamVal := 0.0
	for _, pv := range List(team["players"]) {
		p := Map(pv)
		g := GradeOf(p)
		teamVal += r.CardPrice(Int(p["spid"]), g)
	}
	profile := Map(coach["profile"])
	if profile == nil {
		profile = map[string]any{"picture": 0}
	}
	ep := StartEP
	if v, ok := coach["ep"]; ok {
		ep = Int(v)
	}
	badge := Int(coach["currentbadge"])
	if badge < 1001 {
		badge = 1001
	}
	stadiumID, stadium := r.StadiumFor(teamid, coach)
	stadiumName, yearBuilt, capacity := "Sân vận động", 1990, 50000
	if stadium != nil {
		if n := Str(stadium["name"]); n != "" {
			stadiumName = n
		}
		if y := Int(stadium["yearbuilt"]); y != 0 {
			yearBuilt = y
		}
		if c := Int(stadium["capacity"]); c != 0 {
			capacity = c
		}
	}
	summary := r.teamSummary(team, teamid)
	summary["stadium"] = map[string]any{"id": stadiumID}
	summary["appat"] = createdAt
	// fx._wrapped_coach_me: the summary carries the COACH's name.
	if n := Str(coach["name"]); n != "" {
		summary["name"] = n
	} else {
		summary["name"] = "HLV"
	}
	// hc coach_me: "cups" -> CupProxy.cupPlayList ({metaid, cupid} of
	// the stored cups) — the front screen resumes them after relogin.
	// state["cups"] is a MAP keyed by cupid; List() on it is always
	// empty (the sim-result trap), so iterate the map.
	var cups []any
	for _, c := range MapSlice(s["cups"]) {
		cups = append(cups, map[string]any{"metaid": c["metaid"], "cupid": c["_id"]})
	}
	if cups == nil {
		cups = []any{}
	}
	leagueID := 0
	if lg := Map(r.RosterSummary(teamid)["league"]); lg != nil {
		leagueID = Int(lg["id"])
	}
	coachID := Str(coach["_id"])
	if coachID == "" {
		coachID = "1"
	}
	return map[string]any{
		"id": coachID, "name": Str(coach["name"]), "slot": slot, "teamid": teamid,
		"team": team, "hiderank": false,
		"profile": profile,
		"money": map[string]any{"ep": ep, "cash": 0, "mp": 0, "useable_mp": 0,
			"mpdata": map[string]any{"point": 0, "until": "2030-01-01T00:00:00.000Z"}},
		"igr": PCActive(s), "vipDetail": vipDetailOf(s), "vipExpireAt": EntitlementUntil(s, "vip"),
		"vipStatus": vipStatusOf(s),
		"sp":    map[string]any{"point": 0},
		"gpdata": map[string]any{"point": GPBalance(s), "until": W3C(WeekEndTs())},
		"badges": []any{}, "currentbadge": badge,
		"levelinfo": CoachLevelinfo(coach), "statistics": stats,
		"createdat": createdAt, "playtime": playTime, "career": career,
		"teamvalue": map[string]any{"val": int64(teamVal), "rank": 1, "updown": 0},
		"league":    map[string]any{"leagueid": leagueID},
		"club":      map[string]any{}, "uimode": 0,
		"limits": map[string]any{"cups": 3, "players": MaxPlayers, "baseplayers": MaxPlayers, "firstTeam": FirstTeamMax},
		"transferfavorites": []any{}, "currdivision": "",
		"playlimit": map[string]any{},
		"kit": map[string]any{"availables": map[string]any{
			"home":   map[string]any{"id": teamid<<10 | 0},
			"away":   map[string]any{"id": teamid<<10 | 1},
			"goalie": map[string]any{"id": teamid<<10 | 2},
		}},
		"summary":  summary,
		"stadium":  map[string]any{"id": stadiumID, "name": stadiumName, "yearbuilt": yearBuilt, "capacity": capacity},
		"items":    r.CoachItems(s, slot),
		"matchstats": []any{},
		"multiplay": func() any {
			if mp := coach["multiplay"]; mp != nil {
				return mp
			}
			return []any{}
		}(),
		"scouters": CoachScouters(s, slot),
		"cups":      cups,
		"formations":    team["formations"],
		"teamsquadlist": team["teamsquadlist"],
	}
}

// CoachMeResources wraps CoachMe in the single-resource envelope body
// (hc.coach_me_resources: exactly one /coach/me resource).
func (r *Roster) CoachMeResources(s State, slot int, user *User) []any {
	return []any{map[string]any{"uri": "/coach/me", "body": r.CoachMe(s, slot, user)}}
}

// deepMerge fills missing keys of dst from defaults (fx._deep_merge_stats).
func deepMerge(defaults, dst map[string]any) map[string]any {
	for k, v := range defaults {
		if cur, ok := dst[k]; !ok || cur == nil {
			dst[k] = deepCopy(v)
		} else if vd, ok := v.(map[string]any); ok {
			if cd, ok := cur.(map[string]any); ok {
				deepMerge(vd, cd)
			}
		}
	}
	return dst
}

// RepairSquad exposes repairSquad to the API layer (squad saves).
func (r *Roster) RepairSquad(team map[string]any) { r.repairSquad(team) }

// SquadPresets exposes squadPresets to the API layer (squad saves).
func (r *Roster) SquadPresets(team map[string]any) { r.squadPresets(team) }

// PosSub is hc.POS_SUB: pitch positions are 0..27, 28 is SUB.
const PosSub = 28
