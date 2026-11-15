package game

import (
	"sort"
)

// Team colors: port of hc's tc_* subsystem (http_config.py:1132-1459).
// The server picks the colors the starting XI meets: team.teamcolors
// [{teamcolorid, boostindex, teamid}] in coach/me and the user's match
// side; /static/info/teamcolor serves the full catalog + the owned
// card lists per color (the squad screen's Team Color tab).

const (
	tcClub   = -1
	tcNation = -2
	tcCard   = -3
	tcSeason = 50000
)

type tcBoost struct {
	Type   string
	Amount int
}

// tcTier is one boost group: counts in [Lo, Hi] earn Boosts.
type tcTier struct {
	Lo, Hi int
	Boosts []tcBoost
}

func boostsOf(pairs ...any) []tcBoost {
	out := []tcBoost{}
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, tcBoost{Type: pairs[i].(string), Amount: pairs[i+1].(int)})
	}
	return out
}

func ovrTier(lo, hi, amt int) tcTier {
	return tcTier{lo, hi, []tcBoost{{"ovr", amt}}}
}

// club colors: colorid -> (db teamid, name, boosts) — Python dict order.
var tcClubOrder = []int{5, 1, 7, 2, 11, 6, 12, 13, 3, 4, 43, 14, 9, 15, 8}
var tcClubColors = map[int]struct {
	TeamID int
	Name   string
	Boosts []tcBoost
}{
	5:  {11, "Manchester United", boostsOf("crossing", 2, "standingtackle", 3)},
	1:  {243, "Real Madrid", boostsOf("dribbling", 2, "shortpassing", 1)},
	7:  {5, "Chelsea", boostsOf("strength", 2, "standingtackle", 1)},
	2:  {241, "Barcelona", boostsOf("shortpassing", 2, "vision", 1)},
	11: {21, "Bayern Munich", boostsOf("longshots", 2, "ballcontrol", 1)},
	6:  {1, "Arsenal", boostsOf("sprintspeed", 2, "positioning", 1)},
	12: {47, "AC Milan", boostsOf("marking", 2, "interceptions", 1)},
	13: {45, "Juventus", boostsOf("stamina", 2, "aggression", 1)},
	3:  {9, "Liverpool", boostsOf("reactions", 2, "sprintspeed", 1)},
	4:  {10, "Manchester City", boostsOf("longpassing", 2, "ballcontrol", 1)},
	43: {240, "Atletico Madrid", boostsOf("aggression", 2, "stamina", 1)},
	14: {44, "Inter Milan", boostsOf("reactions", 2, "balance", 1)},
	9:  {22, "Dortmund", boostsOf("acceleration", 2, "reactions", 1)},
	15: {73, "Paris Saint-Germain", boostsOf("ballcontrol", 2, "strength", 1)},
	8:  {18, "Tottenham Hotspur", boostsOf("acceleration", 2, "dribbling", 1)},
}

// nation colors: nationality -> (colorid, name, boosts@6-10, boosts@11).
var tcNationOrder = []int{21, 45, 54, 52, 27, 18, 14, 7, 34, 38, 60, 192}
var tcNationColors = map[int]struct {
	ColorID int
	Name    string
	B6      []tcBoost
	B11     []tcBoost
}{
	21:  {102, "Germany", boostsOf("positioning", 2, "gkreflexes", 3), boostsOf("positioning", 3, "gkreflexes", 4)},
	45:  {101, "Spain", boostsOf("shortpassing", 2, "ballcontrol", 3), boostsOf("shortpassing", 3, "ballcontrol", 4)},
	54:  {103, "Brazil", boostsOf("dribbling", 2, "agility", 3), boostsOf("dribbling", 3, "agility", 4)},
	52:  {105, "Argentina", boostsOf("balance", 2, "strength", 1), boostsOf("balance", 2, "strength", 1)},
	27:  {30001, "Italy", boostsOf("standingtackle", 2, "aggression", 3), boostsOf("standingtackle", 3, "aggression", 4)},
	18:  {113, "France", boostsOf("stamina", 2, "strength", 3), boostsOf("stamina", 3, "strength", 4)},
	14:  {109, "England", boostsOf("crossing", 2, "headingaccuracy", 3), boostsOf("crossing", 3, "headingaccuracy", 4)},
	7:   {110, "Belgium", boostsOf("ballcontrol", 2, "balance", 2, "gkreflexes", 3), boostsOf("ballcontrol", 2, "balance", 2, "gkreflexes", 3)},
	34:  {112, "Netherlands", boostsOf("sprintspeed", 2, "shotpower", 3), boostsOf("sprintspeed", 3, "shotpower", 4)},
	38:  {104, "Portugal", boostsOf("dribbling", 2, "reactions", 3), boostsOf("dribbling", 3, "reactions", 4)},
	60:  {106, "Uruguay", boostsOf("aggression", 2, "acceleration", 3), boostsOf("aggression", 3, "acceleration", 4)},
	192: {30007, "Vietnam", boostsOf(), boostsOf("stamina", 3)},
}

var tcNationOvrBonuses = map[int][2]int{192: {1, 2}}

// card-category colors (ordered: best, premium, special).
var tcCategoryOrder = []string{"best", "premium", "special"}
var tcCategoryColors = map[string]struct {
	ColorID int
	Name    string
	Desc    string
	Tiers   []tcTier
}{
	"best":    {41, "TEAMCOLOR_ATTRIBUTES_TEAM41", "Top 100 cầu thủ các mùa thường", []tcTier{ovrTier(6, 11, 1)}},
	"premium": {30002, "TEAMCOLOR_ATTRIBUTES_TEAM40002", "Top 100 mùa LP, CP và CC", []tcTier{ovrTier(6, 11, 2)}},
	"special": {30003, "SPECIAL PLAYER", "Thẻ WL, WB/XI, UL và VNL", []tcTier{ovrTier(3, 5, 2), ovrTier(6, 10, 3), ovrTier(11, 11, 5)}},
}

var tcPremiumSeasons = map[int]bool{
	22: true, 23: true, 24: true, 31: true, 33: true, 40: true, 49: true,
	55: true, 95: true, 27: true, 28: true, 30: true, 48: true, 51: true,
	61: true, 71: true, 89: true, 91: true, 93: true,
}
var tcSpecialSeasons = map[int]bool{
	27: true, 28: true, 30: true, 48: true, 51: true, 61: true, 71: true,
	89: true, 91: true, 93: true,
}

// enhancement-grade colors (ordered: copper, silver, gold).
var tcGradeOrder = []string{"copper", "silver", "gold"}
var tcGradeColors = map[string]struct {
	ColorID int
	Name    string
	Desc    string
	Tiers   []tcTier
}{
	"copper": {24, "TEAMCOLOR_ATTRIBUTES_EVOLVE24", "Thẻ +2 đến +4", []tcTier{ovrTier(6, 11, 1)}},
	"silver": {10011, "TEAMCOLOR_ATTRIBUTES_EVOLVE10011", "Thẻ +5 đến +7", []tcTier{ovrTier(6, 10, 1), ovrTier(11, 11, 2)}},
	"gold":   {810, "TEAMCOLOR_ATTRIBUTES_EVOLVE810", "Thẻ +8 đến +10", []tcTier{ovrTier(6, 10, 2), ovrTier(11, 11, 3)}},
}
var tcGradeRanges = map[string][2]int{"copper": {2, 4}, "silver": {5, 7}, "gold": {8, 10}}

// duo/trio feature colors (Python dict order).
var tcMiniOrder = []string{
	"msn", "bbc", "milan_trio", "torres_gerrard", "rooney_ronaldo",
	"etoo_ronaldinho", "drogba_lampard", "milan_dutch_trio",
	"arsenal_invincibles", "barcelona_tikitaka", "manutd_wall",
	"juventus_bbc", "chelsea_spine", "bayern_robbery",
	"ronaldo_rooney_tevez", "galacticos",
}
var tcMiniColors = map[string]struct {
	ColorID   int
	Name      string
	Desc      string
	PlayerIDs map[int]bool
	Tiers     []tcTier
}{
	"msn": {30008, "Bộ ba MSN", "Messi, Suarez và Neymar", idSet(158023, 176580, 190871),
		[]tcTier{{3, 3, boostsOf("ovr", 2, "ballcontrol", 2, "dribbling", 1)}}},
	"bbc": {30009, "Bộ ba BBC", "Bale, Benzema và Cristiano Ronaldo", idSet(173731, 165153, 20801),
		[]tcTier{{3, 3, boostsOf("ovr", 2, "sprintspeed", 2, "finishing", 1)}}},
	"milan_trio": {30010, "Mũi đinh ba Milan", "Kaka, Shevchenko và Crespo", idSet(138449, 13128, 7512),
		[]tcTier{{3, 3, boostsOf("ovr", 1, "positioning", 2, "acceleration", 1)}}},
	"torres_gerrard": {30011, "Cặp đôi Hoàn hảo", "Torres và Gerrard", idSet(49369, 13743),
		[]tcTier{{2, 2, boostsOf("ovr", 1, "positioning", 2, "longpassing", 2)}}},
	"rooney_ronaldo": {30012, "Song sát Quỷ đỏ", "Rooney và Cristiano Ronaldo", idSet(54050, 20801),
		[]tcTier{{2, 2, boostsOf("ovr", 2, "sprintspeed", 2, "shotpower", 1)}}},
	"etoo_ronaldinho": {30013, "Vũ điệu và Tốc độ", "Ronaldinho và Eto", idSet(9676, 28130),
		[]tcTier{{2, 2, boostsOf("ovr", 1, "acceleration", 2, "dribbling", 2)}}},
	"drogba_lampard": {30014, "Trục dọc Stamford Bridge", "Drogba và Lampard", idSet(31432, 5471),
		[]tcTier{{2, 2, boostsOf("ovr", 1, "strength", 2, "longshots", 2)}}},
	"milan_dutch_trio": {30015, "Bộ ba Hà Lan bay", "Gullit, van Basten và Rijkaard", idSet(214100, 192181, 214098),
		[]tcTier{{3, 3, boostsOf("ovr", 2, "strength", 2, "finishing", 2)}}},
	"arsenal_invincibles": {30016, "Trục dọc bất bại", "Henry, Vieira và Campbell", idSet(1625, 1419, 388),
		[]tcTier{{3, 3, boostsOf("ovr", 1, "sprintspeed", 2, "aggression", 2)}}},
	"barcelona_tikitaka": {30017, "Trục giữa Tiki-Taka", "Xavi, Iniesta và Busquets", idSet(10535, 41, 189511),
		[]tcTier{{3, 3, boostsOf("ovr", 1, "shortpassing", 2, "ballcontrol", 2)}}},
	"manutd_wall": {30018, "Bức tường Old Trafford", "Ferdinand, Vidic và van der Sar", idSet(7289, 140601, 51539),
		[]tcTier{{3, 3, boostsOf("ovr", 1, "standingtackle", 2, "slidingtackle", 2, "gkdiving", 2, "gkreflexes", 2)}}},
	"juventus_bbc": {30020, "Hàng thủ BBC Juventus", "Buffon, Bonucci, Barzagli và Chiellini", idSet(1179, 184344, 137186, 138956),
		[]tcTier{{4, 4, boostsOf("ovr", 1, "marking", 2, "standingtackle", 2, "gkdiving", 2)}}},
	"chelsea_spine": {30021, "Xương sống The Blues", "Cech, Terry, Lampard và Drogba", idSet(48717, 13732, 5471, 31432),
		[]tcTier{{4, 4, boostsOf("ovr", 2, "headingaccuracy", 2, "shotpower", 2)}}},
	"bayern_robbery": {30022, "Đôi cánh Robbery", "Robben và Ribery", idSet(9014, 156616),
		[]tcTier{{2, 2, boostsOf("ovr", 1, "acceleration", 2, "dribbling", 2, "curve", 2)}}},
	// "inter_treble" (30023) was REMOVED 2026-11-15 at the user's word:
	// it needs all of {Milito, Sneijder, Zanetti, Cesar} but Sneijder
	// (18507) and Cesar (9600) have no cards in fifadb at all, so the
	// color could never activate.
	"ronaldo_rooney_tevez": {30024, "Mũi đinh ba R-R-T", "Cristiano Ronaldo, Wayne Rooney và Carlos Tevez", idSet(20801, 54050, 143001),
		[]tcTier{{3, 3, boostsOf("ovr", 2, "sprintspeed", 2, "shotpower", 2)}}},
	"galacticos": {30019, "Dải ngân hà Galacticos", "Ronaldo, Beckham, Figo, Zidane, Carlos và Raul", idSet(20801, 250, 1088, 1397, 1040, 240),
		[]tcTier{{3, 6, boostsOf("ovr", 2, "dribbling", 2, "shortpassing", 2)}}},
}

func idSet(ids ...int) map[int]bool {
	out := map[int]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

var tcSeasonGroups = []tcTier{ovrTier(5, 6, 1), ovrTier(7, 8, 2), ovrTier(9, 11, 3)}

// tcSeasonGroupOverrides replaces the shared season tiers for one
// season (keyed by its first season code). WL (93) is the only entry:
// the client applies at most ONE base-bucket color per player — the
// strongest — and every WL card also belongs to SPECIAL PLAYER, whose
// top tier (+5) permanently shadows Season WL's own +3, so an
// un-merged Season WL boost could never land on a WL player. At the
// user's decision (2026-11-15) the WL top tier carries the merged
// value 8 = SPECIAL 5 + season 3; the client then picks Season WL as
// the best base color and the player ends up with the expected total.
var tcSeasonGroupOverrides = map[int][]tcTier{
	93: {ovrTier(5, 6, 1), ovrTier(7, 8, 2), ovrTier(9, 11, 8)},
}

func tcSeasonTiers(s tcSeasonDef) []tcTier {
	if g, ok := tcSeasonGroupOverrides[s.Codes[0]]; ok {
		return g
	}
	return tcSeasonGroups
}

func tcSeasonTiersByColor(colorid int) []tcTier {
	for _, s := range tcSeasonsList {
		if tcSeason+s.Codes[0] == colorid {
			return tcSeasonTiers(s)
		}
	}
	return tcSeasonGroups
}

type tcSeasonDef struct {
	Label string
	Codes []int
}

// FA seasons — hc FA_SEASONS preceded by the synthetic "07".
var tcSeasonsList = []tcSeasonDef{
	{"07", []int{7}},
	{"06", []int{6}}, {"06U", []int{63}}, {"06WC", []int{65}}, {"08", []int{8}},
	{"08E", []int{67}}, {"09", []int{9}}, {"10", []int{10}}, {"10U", []int{69}},
	{"10WC", []int{47}}, {"11", []int{11}}, {"14", []int{14}}, {"14WC", []int{77}},
	{"14T", []int{53}}, {"15", []int{15}}, {"16", []int{16}}, {"17", []int{88}},
	{"16EC", []int{35}}, {"16TH", []int{45}}, {"WB", []int{91}}, {"WL", []int{93}},
	{"EL", []int{27}}, {"LP", []int{23}},
	{"CP", []int{22}}, {"CC", []int{24}}, {"17TH", []int{44}}, {"TCO92", []int{49}},
	{"MUA", []int{55}}, {"VL", []int{61}},
	{"CL", []int{89}}, {"KL02", []int{95}}, {"CEL", []int{31, 33}}, {"U23", []int{29}},
	{"KXI", []int{97}}, {"MUL", []int{48}}, {"VS", []int{73}},
	// UB (28), UL (30), TL (51), ML (71) season colors were REMOVED
	// 2026-11-15 at the user's word: those seasons hold only 5/5/3/3
	// cards in fifadb, so the lowest tier (5 same-season starters) is
	// unreachable or needs literally every card in the database. The
	// seasons stay in tcSpecialSeasons/tcPremiumSeasons, so their
	// cards still count toward SPECIAL PLAYER / premium.
	// Added 2026-11-15 (user: "fix như mùa WL"): seasons that hold
	// cards in fifadb and have a name in the client's own season
	// catalog (responses/static_roster.json) but had no season color.
	// Codes 20/21/32/37/38/39/41/42/43/57/81/83 also hold cards but
	// the client catalog does not name them, so they stay colorless.
	{"Europe Legend", []int{26}}, {"NHD", []int{40}},
	{"CSL 82", []int{82}}, {"CSL 85", []int{85}}, {"CSL 87", []int{87}},
}

func tcSeasonOf(code int) int {
	for _, s := range tcSeasonsList {
		for _, c := range s.Codes {
			if c == code {
				return tcSeason + s.Codes[0]
			}
		}
	}
	return 0
}

// --- fifadb-backed statics (tc_static + category card sets) ---

type tcCardInfo struct {
	PlayerID    int
	TeamID      int
	Nationality int
}

func (r *Roster) tcLoad() {
	r.tcOnce.Do(func() {
		r.tcCards = map[int]tcCardInfo{}
		r.tcHist = map[int]map[int]bool{}
		r.tcCatSets = map[string]map[int]bool{"best": {}, "premium": {}, "special": {}}
		if rows, err := r.db.Query("SELECT spid, playerid, teamid, nationality FROM players"); err == nil {
			for rows.Next() {
				var spid, pid, tid, nat int
				if rows.Scan(&spid, &pid, &tid, &nat) == nil {
					r.tcCards[spid] = tcCardInfo{PlayerID: pid, TeamID: tid, Nationality: nat}
					// Club-history derivation (2026-11-15): the
					// playerhistory table covers only ~1,040 players,
					// which left the history half of the club colors
					// inert. A player's club in each season printing
					// IS a club he played for, so union every real
					// printing teamid into his history set (the
					// playerhistory rows below add the rest — e.g.
					// clubs from before the card era). Pseudo teams
					// of the special printings (999997, 111xxx,
					// 113xxx) are not real clubs and are skipped;
					// they could never match a club color anyway.
					if tid > 0 && tid < 100000 {
						if r.tcHist[pid] == nil {
							r.tcHist[pid] = map[int]bool{}
						}
						r.tcHist[pid][tid] = true
					}
				}
			}
			rows.Close()
		}
		if rows, err := r.db.Query("SELECT playerid, teamid FROM playerhistory"); err == nil {
			for rows.Next() {
				var pid, tid int
				if rows.Scan(&pid, &tid) == nil {
					if r.tcHist[pid] == nil {
						r.tcHist[pid] = map[int]bool{}
					}
					r.tcHist[pid][tid] = true
				}
			}
			rows.Close()
		}
		// tc_player_category_card_sets: top-100 distinct players per
		// season by rating (special seasons take all their cards).
		type cardRow struct{ spid, pid, rating int }
		bySeason := map[int][]cardRow{}
		if rows, err := r.db.Query("SELECT spid, playerid, overallrating FROM players WHERE spid >= 1000000"); err == nil {
			for rows.Next() {
				var spid, pid, rating int
				if rows.Scan(&spid, &pid, &rating) == nil {
					s := spid / 1000000
					bySeason[s] = append(bySeason[s], cardRow{spid, pid, rating})
				}
			}
			rows.Close()
		}
		for season, cards := range bySeason {
			if tcSpecialSeasons[season] {
				for _, c := range cards {
					r.tcCatSets["special"][c.spid] = true
				}
				continue
			}
			cat := "best"
			if tcPremiumSeasons[season] {
				cat = "premium"
			}
			sort.Slice(cards, func(i, j int) bool {
				if cards[i].rating != cards[j].rating {
					return cards[i].rating > cards[j].rating
				}
				return cards[i].spid < cards[j].spid
			})
			seen := map[int]bool{}
			for _, c := range cards {
				if seen[c.pid] {
					continue
				}
				seen[c.pid] = true
				if len(seen) > 100 {
					break
				}
				r.tcCatSets[cat][c.spid] = true
			}
		}
	})
}

// tcGradeCardSets: owned spids grouped by grade band; a spid owned at
// conflicting grades is omitted (hc tc_grade_card_sets).
func tcGradeCardSets(players []map[string]any) map[string]map[int]bool {
	bySpid := map[int]map[string]bool{}
	for _, p := range players {
		g := GradeOf(p)
		for _, key := range tcGradeOrder {
			rng := tcGradeRanges[key]
			if g >= rng[0] && g <= rng[1] {
				spid := Int(p["spid"])
				if bySpid[spid] == nil {
					bySpid[spid] = map[string]bool{}
				}
				bySpid[spid][key] = true
			}
		}
	}
	out := map[string]map[int]bool{"copper": {}, "silver": {}, "gold": {}}
	for spid, cats := range bySpid {
		if len(cats) == 1 {
			for k := range cats {
				out[k][spid] = true
			}
		}
	}
	return out
}

// ActiveTeamColors ports hc.active_teamcolors.
func (r *Roster) ActiveTeamColors(team map[string]any) []any {
	r.tcLoad()
	players := []map[string]any{}
	for _, pv := range List(team["players"]) {
		if p := Map(pv); p != nil {
			players = append(players, p)
		}
	}
	gradeSets := tcGradeCardSets(players)
	instances := map[string]map[string]any{}
	for _, p := range players {
		instances[IID(p)] = p
	}
	type key struct{ kind, id int }
	counts := map[key]int{}
	seen := map[int]bool{}
	sq := Map(team["squad"])
	for _, pv := range List(sq["positions"]) {
		pos := Map(pv)
		if pos == nil || Int(pos["pos"]) >= PosSub {
			continue
		}
		spid := Int(pos["spid"])
		c, ok := r.tcCards[spid]
		if !ok || seen[c.PlayerID] {
			continue
		}
		seen[c.PlayerID] = true
		var keys []key
		clubIDs := map[int]bool{c.TeamID: true}
		for tid := range r.tcHist[c.PlayerID] {
			clubIDs[tid] = true
		}
		for tid := range clubIDs {
			keys = append(keys, key{tcClub, tid})
		}
		keys = append(keys, key{tcNation, c.Nationality})
		grade := 0
		inst := instances[fmt_Sprint(pos["id"])]
		if inst == nil {
			inst = instances[itoa(spid)]
		}
		if inst != nil {
			grade = GradeOf(inst)
		}
		for _, k := range tcGradeOrder {
			rng := tcGradeRanges[k]
			if grade >= rng[0] && grade <= rng[1] && gradeSets[k][spid] {
				keys = append(keys, key{tcCard, tcGradeColors[k].ColorID})
			}
		}
		for _, k := range tcCategoryOrder {
			if r.tcCatSets[k][spid] {
				keys = append(keys, key{tcCard, tcCategoryColors[k].ColorID})
			}
		}
		for _, mk := range tcMiniOrder {
			if tcMiniColors[mk].PlayerIDs[c.PlayerID] {
				keys = append(keys, key{tcCard, tcMiniColors[mk].ColorID})
			}
		}
		if sid := tcSeasonOf(spid / 1000000); sid != 0 {
			keys = append(keys, key{tcSeason, sid})
		}
		for _, k := range keys {
			counts[k]++
		}
	}
	clubByTeam := map[int]int{}
	for cid, cc := range tcClubColors {
		clubByTeam[cc.TeamID] = cid
	}
	cardTiers := map[int][]tcTier{}
	for _, k := range tcCategoryOrder {
		cardTiers[tcCategoryColors[k].ColorID] = tcCategoryColors[k].Tiers
	}
	for _, k := range tcGradeOrder {
		cardTiers[tcGradeColors[k].ColorID] = tcGradeColors[k].Tiers
	}
	for _, mk := range tcMiniOrder {
		cardTiers[tcMiniColors[mk].ColorID] = tcMiniColors[mk].Tiers
	}
	sortedKeys := make([]key, 0, len(counts))
	for k := range counts {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Slice(sortedKeys, func(i, j int) bool {
		if sortedKeys[i].kind != sortedKeys[j].kind {
			return sortedKeys[i].kind < sortedKeys[j].kind
		}
		return sortedKeys[i].id < sortedKeys[j].id
	})
	out := []any{}
	for _, k := range sortedKeys {
		n := counts[k]
		switch k.kind {
		case tcClub:
			if cid, ok := clubByTeam[k.id]; ok && n >= 6 {
				bi := 0
				if n >= 11 {
					bi = 1
				}
				out = append(out, map[string]any{"teamcolorid": cid, "boostindex": bi, "teamid": k.id})
			}
		case tcNation:
			if nc, ok := tcNationColors[k.id]; ok && n >= 6 {
				bi := 0
				if n >= 11 {
					bi = 1
				}
				out = append(out, map[string]any{"teamcolorid": nc.ColorID, "boostindex": bi, "teamid": k.id})
			}
		case tcSeason:
			for i, g := range tcSeasonTiersByColor(k.id) {
				if n >= g.Lo && n <= g.Hi {
					out = append(out, map[string]any{"teamcolorid": k.id, "boostindex": i, "teamid": itoa(k.id)})
					break
				}
			}
		case tcCard:
			if tiers, ok := cardTiers[k.id]; ok {
				for i, t := range tiers {
					if n >= t.Lo && n <= t.Hi {
						out = append(out, map[string]any{"teamcolorid": k.id, "boostindex": i, "teamid": itoa(k.id)})
						break
					}
				}
			}
		}
	}
	return out
}

// TeamColorPlayerBoosts ports hc.teamcolor_player_boosts: the
// client-equivalent selection of Team Color boosts per starting
// card and assigned position. Python defines this as the reference
// for what the client itself selects in-match from the teamcolors
// block of the match side (hc never calls it server-side); it is
// ported for parity and verification. Returns one entry per
// starting position: {spid, pos, boosts: [{type, amount}]} where
// boosts merges the best-scoring group of each bucket (base /
// grade / feature).
func (r *Roster) TeamColorPlayerBoosts(team map[string]any) []map[string]any {
	r.tcLoad()
	active := r.ActiveTeamColors(team)
	if len(active) == 0 {
		return nil
	}
	type cdef struct {
		kind   string
		groups [][]tcBoost
	}
	defs := map[int]cdef{}
	for _, raw := range r.TeamColorsCatalog() {
		e := Map(raw)
		if e == nil {
			continue
		}
		var groups [][]tcBoost
		for _, g := range List(e["boostgroups"]) {
			gm := Map(g)
			var bs []tcBoost
			for _, b := range List(gm["boosts"]) {
				bm := Map(b)
				bs = append(bs, tcBoost{Str(bm["type"]), Int(bm["amount"])})
			}
			groups = append(groups, bs)
		}
		defs[Int(e["teamcolorid"])] = cdef{Str(e["type"]), groups}
	}
	players := []map[string]any{}
	for _, pv := range List(team["players"]) {
		if p := Map(pv); p != nil {
			players = append(players, p)
		}
	}
	gradeSets := tcGradeCardSets(players)
	instances := map[string]map[string]any{}
	for _, p := range players {
		instances[IID(p)] = p
	}
	miniByColor := map[int]map[int]bool{}
	for _, mk := range tcMiniOrder {
		mc := tcMiniColors[mk]
		miniByColor[mc.ColorID] = mc.PlayerIDs
	}
	catByColor := map[int]map[int]bool{}
	for _, k := range tcCategoryOrder {
		catByColor[tcCategoryColors[k].ColorID] = r.tcCatSets[k]
	}
	gradeColorOf := map[string]int{}
	for _, k := range tcGradeOrder {
		gradeColorOf[k] = tcGradeColors[k].ColorID
	}
	out := []map[string]any{}
	sq := Map(team["squad"])
	for _, pv := range List(sq["positions"]) {
		position := Map(pv)
		if position == nil {
			continue
		}
		pos := Int(position["pos"])
		if pos < 0 || pos >= PosSub {
			continue
		}
		spid := Int(position["spid"])
		card, ok := r.tcCards[spid]
		if !ok {
			continue
		}
		inst := instances[fmt_Sprint(position["id"])]
		if inst == nil {
			inst = instances[itoa(spid)]
		}
		grade := 0
		if inst != nil {
			grade = GradeOf(inst)
		}
		gradeCat := ""
		for _, k := range tcGradeOrder {
			rng := tcGradeRanges[k]
			if grade >= rng[0] && grade <= rng[1] {
				gradeCat = k
				break
			}
		}
		best := map[string][]tcBoost{}
		bestScore := map[string]int{}
		bucketOrder := []string{}
		for _, rawItem := range active {
			item := Map(rawItem)
			cid := Int(item["teamcolorid"])
			def, ok := defs[cid]
			if !ok {
				continue
			}
			matches := false
			bucket := "base"
			switch def.kind {
			case "club":
				tid := Int(item["teamid"])
				matches = tid == card.TeamID || r.tcHist[card.PlayerID][tid]
			case "nation":
				matches = Int(item["teamid"]) == card.Nationality
			case "grade":
				bucket = "grade"
				matches = gradeCat != "" && cid == gradeColorOf[gradeCat] && gradeSets[gradeCat][spid]
			case "special":
				if mini, isMini := miniByColor[cid]; isMini {
					bucket = "feature"
					matches = mini[card.PlayerID]
				} else {
					matches = catByColor[cid][spid]
				}
			case "feature":
				bucket = "feature"
				matches = miniByColor[cid][card.PlayerID]
			}
			if !matches {
				continue
			}
			bi := Int(item["boostindex"])
			if bi < 0 || bi >= len(def.groups) {
				continue
			}
			boosts := def.groups[bi]
			score := 0
			for _, b := range boosts {
				w := b.Amount
				if b.Type == "ovr" {
					w *= 33
				}
				score += w
			}
			if _, has := best[bucket]; !has {
				bucketOrder = append(bucketOrder, bucket)
				best[bucket] = boosts
				bestScore[bucket] = score
			} else if score > bestScore[bucket] {
				best[bucket] = boosts
				bestScore[bucket] = score
			}
		}
		merged := []any{}
		for _, bucket := range bucketOrder {
			for _, b := range best[bucket] {
				merged = append(merged, map[string]any{"type": b.Type, "amount": b.Amount})
			}
		}
		out = append(out, map[string]any{"spid": spid, "pos": pos, "boosts": merged})
	}
	return out
}

func boostJSON(bs []tcBoost) []any {
	out := []any{}
	for _, b := range bs {
		out = append(out, map[string]any{"type": b.Type, "amount": b.Amount})
	}
	return out
}

func groupsJSON(tiers []tcTier) []any {
	out := []any{}
	for _, t := range tiers {
		out = append(out, map[string]any{"count_min": t.Lo, "count_max": t.Hi, "boosts": boostJSON(t.Boosts)})
	}
	return out
}

// TeamColorsCatalog ports hc.tc_colors().
func (r *Roster) TeamColorsCatalog() []any {
	out := []any{}
	for _, cid := range tcClubOrder {
		cc := tcClubColors[cid]
		groups := []any{
			map[string]any{"count_min": 6, "count_max": 10, "boosts": boostJSON(cc.Boosts)},
			map[string]any{"count_min": 11, "count_max": 11, "boosts": boostJSON(append([]tcBoost{{"ovr", 1}}, cc.Boosts...))},
		}
		out = append(out, map[string]any{"teamcolorid": cid, "type": "club", "teamid": cc.TeamID,
			"name": cc.Name, "description": "Câu lạc bộ " + cc.Name, "boostgroups": groups})
	}
	for _, nat := range tcNationOrder {
		nc := tcNationColors[nat]
		ovr, ok := tcNationOvrBonuses[nat]
		if !ok {
			ovr = [2]int{0, 1} // hc default (0, 1)
		}
		g1 := []tcBoost{}
		if ovr[0] != 0 {
			g1 = append(g1, tcBoost{"ovr", ovr[0]})
		}
		g1 = append(g1, nc.B6...)
		g2 := append([]tcBoost{{"ovr", ovr[1]}}, nc.B11...)
		groups := []any{
			map[string]any{"count_min": 6, "count_max": 10, "boosts": boostJSON(g1)},
			map[string]any{"count_min": 11, "count_max": 11, "boosts": boostJSON(g2)},
		}
		out = append(out, map[string]any{"teamcolorid": nc.ColorID, "type": "nation", "teamid": nat,
			"name": nc.Name, "description": "Đội tuyển " + nc.Name, "boostgroups": groups})
	}
	for _, k := range tcCategoryOrder {
		cc := tcCategoryColors[k]
		out = append(out, map[string]any{"teamcolorid": cc.ColorID, "type": "special", "teamid": itoa(cc.ColorID),
			"name": cc.Name, "description": cc.Desc, "boostgroups": groupsJSON(cc.Tiers)})
	}
	for _, k := range tcGradeOrder {
		gc := tcGradeColors[k]
		out = append(out, map[string]any{"teamcolorid": gc.ColorID, "type": "grade", "teamid": itoa(gc.ColorID),
			"name": gc.Name, "description": gc.Desc, "grade": tcGradeRanges[k][0], "boostgroups": groupsJSON(gc.Tiers)})
	}
	for _, mk := range tcMiniOrder {
		mc := tcMiniColors[mk]
		out = append(out, map[string]any{"teamcolorid": mc.ColorID, "type": "feature", "displaytype": "special",
			"teamid": itoa(mc.ColorID), "name": mc.Name, "description": mc.Desc, "boostgroups": groupsJSON(mc.Tiers)})
	}
	for _, s := range tcSeasonsList {
		cid := tcSeason + s.Codes[0]
		// The "season" field is part of the client's data model:
		// ClassifyTeamColorsOfPlayer reads def.season (defaulting
		// to 255 = "no season") to key the generic team/nation maps
		// and availableTeamColor's club/nation branches gate on it.
		// The "special"-type application path itself ignores it and
		// matches purely via teamColorSpecials[teamid] (verified
		// 2026-11-15 by reading the client's TeamColorController).
		out = append(out, map[string]any{"teamcolorid": cid, "type": "special", "teamid": itoa(cid),
			"name": "Season " + s.Label, "description": "Cầu thủ mùa giải " + s.Label,
			"season": s.Codes[0], "boostgroups": groupsJSON(tcSeasonTiers(s))})
	}
	return out
}

// TeamColorSpecials ports the specials half of hc.ep_teamcolor.
func (r *Roster) TeamColorSpecials(team map[string]any) map[string]any {
	r.tcLoad()
	players := []map[string]any{}
	for _, pv := range List(team["players"]) {
		if p := Map(pv); p != nil {
			players = append(players, p)
		}
	}
	owned := map[int]bool{}
	for _, p := range players {
		owned[Int(p["spid"])] = true
	}
	specials := map[string]any{}
	for _, c := range r.TeamColorsCatalog() {
		cm := Map(c)
		t := Str(cm["type"])
		if t == "special" || t == "feature" {
			specials[fmt_Sprint(cm["teamid"])] = []any{}
		}
		if Int(cm["teamcolorid"]) >= tcSeason {
			k := itoa(Int(cm["teamcolorid"]))
			if specials[k] == nil {
				specials[k] = []any{}
			}
		}
	}
	ownedSorted := make([]int, 0, len(owned))
	for spid := range owned {
		ownedSorted = append(ownedSorted, spid)
	}
	sort.Ints(ownedSorted)
	for _, spid := range ownedSorted {
		if sid := tcSeasonOf(spid / 1000000); sid != 0 {
			k := itoa(sid)
			specials[k] = append(List(specials[k]), spid)
		}
	}
	for k, spids := range tcGradeCardSets(players) {
		arr := []int{}
		for spid := range spids {
			arr = append(arr, spid)
		}
		sort.Ints(arr)
		out := []any{}
		for _, spid := range arr {
			out = append(out, spid)
		}
		specials[itoa(tcGradeColors[k].ColorID)] = out
	}
	for _, k := range tcCategoryOrder {
		arr := []int{}
		for spid := range r.tcCatSets[k] {
			if owned[spid] {
				arr = append(arr, spid)
			}
		}
		sort.Ints(arr)
		out := []any{}
		for _, spid := range arr {
			out = append(out, spid)
		}
		specials[itoa(tcCategoryColors[k].ColorID)] = out
	}
	for _, mk := range tcMiniOrder {
		mc := tcMiniColors[mk]
		out := []any{}
		for _, spid := range ownedSorted {
			if card, ok := r.tcCards[spid]; ok && mc.PlayerIDs[card.PlayerID] {
				out = append(out, spid)
			}
		}
		specials[itoa(mc.ColorID)] = out
	}
	return specials
}
