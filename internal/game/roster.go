package game

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

// Rating weights (hc.ROLE_WEIGHTS): the client's per-role attribute
// weights; a card's shown rating is floor(sum(stat*weight)/100) at its
// preferred position's role.
var roleWeights = map[string]map[string]int{
	"GK": {"gkdiving": 21, "gkhandling": 21, "gkkicking": 5, "gkpositioning": 21, "gkreflexes": 21, "reactions": 11},
	"SW": {"aggression": 8, "ballcontrol": 5, "headingaccuracy": 10, "jumping": 4, "marking": 15, "reactions": 5, "shortpassing": 5, "slidingtackle": 15, "standingtackle": 15, "strength": 10, "interceptions": 8},
	"WB": {"acceleration": 4, "ballcontrol": 8, "crossing": 12, "dribbling": 4, "marking": 7, "reactions": 8, "shortpassing": 10, "slidingtackle": 11, "sprintspeed": 6, "stamina": 10, "standingtackle": 8, "interceptions": 12},
	"FB": {"acceleration": 5, "ballcontrol": 7, "crossing": 9, "headingaccuracy": 4, "marking": 8, "reactions": 8, "shortpassing": 7, "slidingtackle": 14, "sprintspeed": 7, "stamina": 8, "standingtackle": 11, "interceptions": 12},
	"CB": {"aggression": 7, "ballcontrol": 4, "headingaccuracy": 10, "jumping": 3, "marking": 14, "reactions": 5, "shortpassing": 5, "slidingtackle": 17, "sprintspeed": 2, "standingtackle": 10, "strength": 10, "interceptions": 13},
	"DM": {"aggression": 5, "ballcontrol": 10, "longpassing": 10, "marking": 9, "reactions": 7, "shortpassing": 14, "slidingtackle": 5, "stamina": 6, "standingtackle": 12, "strength": 4, "interceptions": 14, "vision": 4},
	"WM": {"acceleration": 7, "ballcontrol": 13, "crossing": 10, "dribbling": 15, "finishing": 6, "longpassing": 5, "positioning": 8, "reactions": 7, "shortpassing": 11, "sprintspeed": 6, "stamina": 5, "vision": 7},
	"CM": {"ballcontrol": 14, "dribbling": 7, "finishing": 2, "longpassing": 13, "longshots": 4, "positioning": 6, "reactions": 8, "shortpassing": 17, "stamina": 6, "standingtackle": 5, "interceptions": 5, "vision": 13},
	"AM": {"acceleration": 4, "agility": 3, "ballcontrol": 15, "dribbling": 13, "finishing": 7, "longpassing": 4, "longshots": 5, "positioning": 9, "reactions": 7, "shortpassing": 16, "sprintspeed": 3, "vision": 14},
	"CF": {"acceleration": 5, "ballcontrol": 15, "dribbling": 14, "finishing": 11, "headingaccuracy": 2, "longshots": 4, "positioning": 13, "reactions": 9, "shortpassing": 9, "shotpower": 5, "sprintspeed": 5, "vision": 8},
	"W":  {"acceleration": 7, "agility": 3, "ballcontrol": 14, "crossing": 9, "dribbling": 16, "finishing": 10, "longshots": 4, "positioning": 9, "reactions": 7, "shortpassing": 9, "sprintspeed": 6, "vision": 6},
	"ST": {"acceleration": 4, "ballcontrol": 10, "dribbling": 7, "finishing": 18, "headingaccuracy": 10, "longshots": 3, "positioning": 13, "reactions": 8, "shortpassing": 5, "shotpower": 10, "sprintspeed": 5, "strength": 5, "volleys": 2},
}

// posRole is hc.POS_ROLE (Enums.PLAYERPOSITION index -> role).
var posRole = strings.Fields("GK SW WB FB CB CB CB FB WB DM DM DM WM CM CM CM WM AM AM AM CF CF CF W ST ST ST W")

const posSub = 28 // Enums.PLAYERPOSITION SUB

var gradeBoost = []int{0, 5, 6, 7, 8, 10, 12, 14, 17, 20, 24}
var gradePrice = []float64{0, 1, 2.65, 7.1, 19.6, 53, 162, 420, 1090, 2680, 6290}

// PlayerRow is one fifadb players row (the fields the server uses).
type PlayerRow struct {
	Spid, PlayerID, TeamID, LeagueID, Nationality, PrefPos1, Overall int
	Stats                                                            map[string]int
}

var statCols = func() []string {
	seen := map[string]bool{}
	var cols []string
	for _, w := range roleWeights {
		for k := range w {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	sort.Strings(cols)
	return cols
}()

// ratingAt computes the client's rating of a row at a pitch position.
func ratingAt(r *PlayerRow, pos int) int {
	role := posRole[pos]
	sum := 0
	for k, w := range roleWeights[role] {
		sum += r.Stats[k] * w
	}
	return sum / 100
}

// Rating is the shown rating (at the preferred position).
func (r *PlayerRow) Rating() int { return ratingAt(r, r.PrefPos1) }

// Roster serves fifadb data (read-only) plus the formations table.
type Roster struct {
	db           *sql.DB
	responsesDir string
	gamedataDir  string

	controlDefaults map[string]any
	cameraDefaults  map[string]any

	mu        sync.Mutex
	ovrCache  map[int]int
	pidCache  map[int]int
	teamCache map[int]map[string]any
	formations map[string]any
	stadiums  map[int]map[string]any
	stadiumOf map[int]int

	staffCatalog map[int]*StaffInfo
	craftCache   []CraftCard

	// teamcolor statics (tcLoad, teamcolor.go)
	tcOnce    sync.Once
	tcCards   map[int]tcCardInfo
	tcHist    map[int]map[int]bool
	tcCatSets map[string]map[int]bool
	shopOv, gpOv atomic.Value // map[int]int64 admin price overrides
	shopHidden   atomic.Value // map[int]bool items recalled from the EP shop
	marketFactors atomic.Value // map[int]float64 spid -> demand factor (market reset)
	staffOrder   []int
	assetsDir    string

	leagueTactics  []map[string]any
	leagueTeamsTab map[int][]int
	nationalTeams  map[int]int
	nationalOrder  []int

	clubTeams   map[int]map[string]any
	clubLeagues map[int]map[string]any
	clubNations map[int]map[string]any
	clubLinks   map[int]int
	rowNation   map[int]int

	worldValues map[int]int
	legendPeak  map[int]int
	legendFO4   map[int]int

	faPrices     map[int]int
	ratingTable  map[int]float64
	ratingOnce   sync.Once
}

// OpenRoster opens fifadb.sqlite read-only and loads the JSON statics.
func OpenRoster(gamedataDir, responsesDir string) (*Roster, error) {
	dbPath := filepath.Join(gamedataDir, "fifadb.sqlite")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("fifadb: %w", err)
	}
	r := &Roster{db: db, responsesDir: responsesDir, gamedataDir: gamedataDir,
		ovrCache: map[int]int{}, pidCache: map[int]int{}, teamCache: map[int]map[string]any{},
		stadiums: map[int]map[string]any{}, stadiumOf: map[int]int{},
		clubTeams: map[int]map[string]any{}, clubLeagues: map[int]map[string]any{},
		clubNations: map[int]map[string]any{}, clubLinks: map[int]int{}, rowNation: map[int]int{},
		worldValues: map[int]int{}, legendPeak: map[int]int{}, legendFO4: map[int]int{},
		faPrices: map[int]int{}}
	if b, err := os.ReadFile(filepath.Join(gamedataDir, "options_tables.json")); err == nil {
		var ot struct {
			Control map[string]any `json:"control_defaults"`
			Camera  map[string]any `json:"camera_defaults"`
		}
		if json.Unmarshal(b, &ot) == nil {
			r.controlDefaults, r.cameraDefaults = ot.Control, ot.Camera
		}
	}
	if b, err := os.ReadFile(filepath.Join(gamedataDir, "staff_catalog.json")); err == nil {
		var doc struct {
			Staff map[string]struct {
				Name        string     `json:"name"`
				Position    string     `json:"position"`
				Material    bool       `json:"material"`
				BoostGroups [][]string `json:"boost_groups"`
			} `json:"staff"`
		}
		if json.Unmarshal(b, &doc) == nil {
			r.staffCatalog = map[int]*StaffInfo{}
			for k, v := range doc.Staff {
				id, _ := strconv.Atoi(k)
				r.staffCatalog[id] = &StaffInfo{Name: v.Name, Position: v.Position,
					Material: v.Material, BoostGroups: v.BoostGroups}
			}
			r.staffOrder = orderedKeys(b, "staff")
		}
	}
	if b, err := os.ReadFile(filepath.Join(gamedataDir, "league_tables.json")); err == nil {
		var lt struct {
			Tactics       []map[string]any `json:"tactics"`
			LeagueTeams   map[string][]int `json:"league_teams"`
			NationalTeams map[string]int   `json:"national_teams"`
		}
		if json.Unmarshal(b, &lt) == nil {
			r.leagueTactics = lt.Tactics
			r.leagueTeamsTab = map[int][]int{}
			for k, v := range lt.LeagueTeams {
				id, _ := strconv.Atoi(k)
				r.leagueTeamsTab[id] = v
			}
			r.nationalTeams = map[int]int{}
			for k, v := range lt.NationalTeams {
				id, _ := strconv.Atoi(k)
				r.nationalTeams[id] = v
			}
			r.nationalOrder = orderedKeys(b, "national_teams")
		}
	}
	if b, err := os.ReadFile(filepath.Join(gamedataDir, "fa_prices.json")); err == nil {
		var doc map[string]int
		if json.Unmarshal(b, &doc) == nil {
			for k, v := range doc {
				r.faPrices[mustAtoi(k)] = v
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(gamedataDir, "world_market_values.json")); err == nil {
		var doc struct {
			PlayerValues     map[string]int `json:"player_values"`
			LegendPeakValues map[string]int `json:"legend_peak_values"`
		}
		if json.Unmarshal(b, &doc) == nil {
			for k, v := range doc.PlayerValues {
				r.worldValues[mustAtoi(k)] = v
			}
			for k, v := range doc.LegendPeakValues {
				r.legendPeak[mustAtoi(k)] = v
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(gamedataDir, "legend_fo4_prices.json")); err == nil {
		var doc struct {
			Players map[string]struct {
				Price     int    `json:"price"`
				SeasonCode string `json:"season_code"`
			} `json:"players"`
		}
		if json.Unmarshal(b, &doc) == nil {
			for k, v := range doc.Players {
				if v.Price > 0 && v.SeasonCode == "100" {
					r.legendFO4[mustAtoi(k)] = v.Price
				}
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(gamedataDir, "formations.json")); err == nil {
		_ = json.Unmarshal(b, &r.formations)
	}
	// Club catalog (stadiums + team->stadium links), same file Python loads.
	for _, name := range []string{"club_catalog.json", "clubs.json"} {
		if b, err := os.ReadFile(filepath.Join(gamedataDir, name)); err == nil {
			var cat struct {
				Stadiums []map[string]any `json:"stadiums"`
				Links    []map[string]any `json:"teamstadiumlinks"`
				Teams    []map[string]any `json:"teams"`
				Leagues  []map[string]any `json:"leagues"`
				Nations  []map[string]any `json:"nations"`
				LLinks   []map[string]any `json:"leagueteamlinks"`
				RowLinks []map[string]any `json:"rowteamnationlinks"`
			}
			if json.Unmarshal(b, &cat) == nil {
				for _, s := range cat.Stadiums {
					r.stadiums[Int(s["stadiumid"])] = s
				}
				for _, l := range cat.Links {
					r.stadiumOf[Int(l["teamid"])] = Int(l["stadiumid"])
				}
				for _, t := range cat.Teams {
					r.clubTeams[Int(t["teamid"])] = t
				}
				for _, l := range cat.Leagues {
					r.clubLeagues[Int(l["leagueid"])] = l
				}
				for _, n := range cat.Nations {
					r.clubNations[Int(n["nationid"])] = n
				}
				for _, l := range cat.LLinks {
					r.clubLinks[Int(l["teamid"])] = Int(l["leagueid"])
				}
				for _, l := range cat.RowLinks {
					r.rowNation[Int(l["teamid"])] = Int(l["nationid"])
				}
			}
			break
		}
	}
	return r, nil
}


// orderedKeys returns the key order of one object member of a JSON
// document (Python dict order survives in the extractor's output and
// a few client payloads mirror it, e.g. the World Cup teamids list).
func orderedKeys(doc []byte, member string) []int {
	dec := json.NewDecoder(strings.NewReader(string(doc)))
	var out []int
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case json.Delim:
			if t == '{' || t == '[' {
				depth++
			} else {
				depth--
			}
		case string:
			if depth == 1 && t == member {
				inner, err := dec.Token()
				if err != nil {
					return out
				}
				if d, ok := inner.(json.Delim); ok && d == '{' {
					d2 := 1
					for d2 > 0 {
						tk, err := dec.Token()
						if err != nil {
							return out
						}
						switch tt := tk.(type) {
						case json.Delim:
							if tt == '{' || tt == '[' {
								d2++
							} else {
								d2--
							}
						case string:
							if d2 == 1 {
								if id, err := strconv.Atoi(tt); err == nil {
									out = append(out, id)
								}
							}
						}
					}
				}
				return out
			}
		}
	}
	return out
}

func (r *Roster) queryPlayers(where string, args ...any) ([]*PlayerRow, error) {
	cols := "p.spid, p.playerid, p.teamid, p.leagueid, p.nationality, p.preferredposition1, p.overallrating"
	for _, c := range statCols {
		cols += ", p." + c
	}
	rows, err := r.db.Query("SELECT "+cols+" FROM players p "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PlayerRow
	for rows.Next() {
		vals := make([]any, 7+len(statCols))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
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
		out = append(out, pr)
	}
	return out, rows.Err()
}

// CardOvr is hc.card_ovr: the rating the client shows for a card.
func (r *Roster) CardOvr(spid int) int {
	r.mu.Lock()
	if v, ok := r.ovrCache[spid]; ok {
		r.mu.Unlock()
		return v
	}
	r.mu.Unlock()
	rows, err := r.queryPlayers("WHERE p.spid = ?", spid)
	v := 0
	if err == nil && len(rows) > 0 {
		v = rows[0].Rating()
		r.mu.Lock()
		r.ovrCache[spid] = v
		r.pidCache[spid] = rows[0].PlayerID
		r.mu.Unlock()
	}
	return v
}

// PlayerOvr is hc.player_ovr: card rating + grade boost.
func (r *Roster) PlayerOvr(p map[string]any) int {
	g := GradeOf(p)
	return r.CardOvr(Int(p["spid"])) + gradeBoost[g]
}

// GradeOf is hc.grade_of (+1..+10).
func GradeOf(p map[string]any) int {
	g := Int(p["grade"])
	if g < 1 {
		g = 1
	}
	if g > 10 {
		g = 10
	}
	return g
}

// sig3 is hc.sig3 (3 significant figures).
func sig3(x float64) float64 {
	// hc.sig3: round(int(x), 2 - len(str(int(x)))) -- Python's round()
	// is half-to-even, so RoundToEven is required for exact parity
	// (e.g. sig3(2850000) = 2800000, not 2900000).
	i := int64(x)
	if i >= 1000 {
		digits := len(fmt.Sprint(i))
		nd := 2 - digits
		pow := math.Pow(10, float64(-nd))
		return math.RoundToEven(float64(i)/pow) * pow
	}
	return x
}

func priceFormula(ovr int) float64 {
	v := math.Exp(2.53 + 0.1113*float64(ovr))
	if v < 1000 {
		v = 1000
	}
	return sig3(v)
}

// PlayerID returns the canonical playerid of a spid (0 if unknown).
func (r *Roster) PlayerID(spid int) int {
	r.mu.Lock()
	if v, ok := r.pidCache[spid]; ok {
		r.mu.Unlock()
		return v
	}
	r.mu.Unlock()
	r.CardOvr(spid) // populates the cache
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pidCache[spid]
}

// OnMarket ports hc.on_market: tradeable = listed on fifaaddict
// (fa_prices.json, present on the production server); without the
// file it falls back to spid >= 1000000.
func (r *Roster) OnMarket(spid int) bool {
	if len(r.faPrices) > 0 {
		_, ok := r.faPrices[spid]
		return ok
	}
	return spid >= 1000000
}

// ratingPrices ports hc.rating_prices: median fifaaddict price per
// client rating (ratings with 5+ priced cards), smoothed into a
// monotonic table rising at least 5% per rating point.
func (r *Roster) ratingPrices() map[int]float64 {
	r.ratingOnce.Do(func() {
		by := map[int][]float64{}
		for spid, price := range r.faPrices {
			if price == 0 {
				continue
			}
			if o := r.CardOvr(spid); o != 0 {
				by[o] = append(by[o], float64(price))
			}
		}
		med := map[int]float64{}
		lo, hi := 0, 0
		for o, vals := range by {
			if len(vals) < 5 {
				continue
			}
			s := append([]float64{}, vals...)
			sort.Float64s(s)
			n := len(s)
			if n%2 == 1 {
				med[o] = s[n/2]
			} else {
				med[o] = (s[n/2-1] + s[n/2]) / 2
			}
			if lo == 0 || o < lo {
				lo = o
			}
			if o > hi {
				hi = o
			}
		}
		table := map[int]float64{}
		top := 0.0
		if len(med) > 0 {
			for o := lo; o <= hi; o++ {
				if top*1.05 > med[o] {
					top = top * 1.05
				} else {
					top = med[o]
				}
				table[o] = top
			}
		}
		r.ratingTable = table
	})
	return r.ratingTable
}

// statPrice ports hc.stat_price.
func (r *Roster) statPrice(ovr int) float64 {
	t := r.ratingPrices()
	if len(t) < 6 {
		return priceFormula(ovr)
	}
	lo, hi := 0, 0
	for o := range t {
		if lo == 0 || o < lo {
			lo = o
		}
		if o > hi {
			hi = o
		}
	}
	if ovr > hi {
		return t[hi] * math.Pow(t[hi]/t[hi-5], float64(ovr-hi)/5)
	}
	if ovr < lo {
		ovr = lo
	}
	return t[ovr]
}

// CardPrice is hc.card_price with all production data files loaded:
// fifaaddict prices (on-market + rating medians), world-market values
// and the Legend tables.
func (r *Roster) CardPrice(spid, grade int) float64 {
	if grade < 0 {
		grade = 0
	}
	if grade > 10 {
		grade = 10
	}
	return sig3(r.cardBase(spid) * r.MarketFactor(spid) * gradePrice[grade])
}

// cardBase is the unadjusted reference base price of a card,
// before the demand factor and the grade multiplier.
func (r *Roster) cardBase(spid int) float64 {
	pid := r.PlayerID(spid)
	if spid/1000000 == 93 { // World Legend
		market := r.legendFO4[pid]
		peak := r.legendPeak[pid]
		b := float64(max(market, peak)) * 10
		if b < 100_000_000 {
			b = 100_000_000
		}
		return b
	} else if v, ok := r.worldValues[pid]; ok && v > 0 {
		return float64(v)
	}
	return r.statPrice(r.CardOvr(spid))
}

// MarketFactor returns the demand factor the daily market reset
// assigned to a spid (1 when it has never moved). The factor is
// part of the reference price, so the transfer market, the trade
// exchanges and enhancement all price from the same stabilized
// base (owner directive 2026-11-15).
func (r *Roster) MarketFactor(spid int) float64 {
	if v := r.marketFactors.Load(); v != nil {
		if f, ok := v.(map[int]float64)[spid]; ok && f > 0 {
			return f
		}
	}
	return 1
}

// SetMarketFactors installs the spid -> factor map (refreshed by
// the server from the market_price_adj table).
func (r *Roster) SetMarketFactors(m map[int]float64) { r.marketFactors.Store(m) }

// CardBaseRef is the current reference base price (cardBase x
// demand factor); the market reset's ratio math works against it.
func (r *Roster) CardBaseRef(spid int) float64 {
	return r.cardBase(spid) * r.MarketFactor(spid)
}

// SetAssetsDir points portrait existence checks at the assets tree.
func (r *Roster) SetAssetsDir(dir string) { r.assetsDir = dir }

// TeamFormations is hc.team_formations: the club's own formation first
// (else 4-4-2(1)), then the common ones.
func (r *Roster) TeamFormations(teamid int) []any {
	if r.formations == nil {
		return []any{}
	}
	common := List(r.formations["common"])
	teams := Map(r.formations["teams"])
	var own any
	if teams != nil {
		own = teams[fmt.Sprint(teamid)]
	}
	if own == nil {
		for _, e := range common {
			if Str(Map(e)["formationname"]) == "4-4-2(1)" {
				own = e
				break
			}
		}
	}
	out := []any{}
	if own != nil {
		out = append(out, own)
	}
	ownID := Int(Map(own)["formationid"])
	for _, e := range common {
		if own == nil || Int(Map(e)["formationid"]) != ownID {
			out = append(out, e)
		}
	}
	return out
}

// RosterSummary is hc.roster_summary: {name, league, nation, rival}.
// RosterSummary ports hc.roster_summary -> club_summary_response:
// the static file's summary enriched from the club catalog (real
// league/nation names and rival from fifadb's team tables).
func (r *Roster) RosterSummary(teamid int) map[string]any {
	base := map[string]any{}
	if b, err := readFileCached(filepath.Join(r.responsesDir,
		fmt.Sprintf("static_info_authentic_team_all_%d.json", teamid))); err == nil {
		var doc map[string]any
		if json.Unmarshal(b, &doc) == nil {
			if s := Map(doc["summary"]); s != nil {
				base = s
			}
		}
	}
	if len(base) == 0 {
		if t := r.GenTeam(teamid, 0, false); t != nil {
			base = Map(t["summary"])
		}
	}
	info := r.clubTeams[teamid]
	leagueid, hasLink := r.clubLinks[teamid]
	if !hasLink {
		leagueid = Int(Map(base["league"])["id"])
	}
	league := r.clubLeagues[leagueid]
	nationid := Int(league["countryid"])
	if leagueid == 78 {
		for nid, n := range r.clubNations {
			if Int(n["teamid"]) == teamid {
				nationid = nid
				break
			}
		}
	} else if leagueid == 76 {
		if nid, ok := r.rowNation[teamid]; ok {
			nationid = nid
		}
	}
	name := Str(info["teamname"])
	if name == "" {
		name = Str(base["name"])
	}
	rivalName := ""
	if info != nil {
		if rt := r.clubTeams[Int(info["rivalteam"])]; rt != nil {
			rivalName = Str(rt["teamname"])
		}
	}
	return map[string]any{
		"name": name,
		"league": map[string]any{
			"id": leagueid, "name": Str(league["leaguename"]), "nationid": nationid,
		},
		"nation": map[string]any{"id": nationid, "name": Str(r.clubNations[nationid]["nationname"])},
		"rival":  map[string]any{"name": rivalName},
	}
}

// ClubSummaryResponse ports hc.club_summary_response exactly:
// {**summary, "summary": summary, "stadium": {...}, "kit": TeamKit}
// where summary is the roster() base summary (static file or
// GenTeam, national override included) updated with id/teamid and
// the catalog name/league/nation/rival. The thin RosterSummary
// drops base fields (ovr, teamgrade, numofselectableplayers) that
// the World Cup creation carousel needs — that was why the WC
// squad screen rendered no players.
func (r *Roster) ClubSummaryResponse(teamid int) map[string]any {
	summary := map[string]any{}
	if raw := r.RosterRaw(teamid); raw != nil {
		for k, v := range Map(raw["summary"]) {
			summary[k] = v
		}
	}
	info := r.clubTeams[teamid]
	leagueid, hasLink := r.clubLinks[teamid]
	if !hasLink {
		leagueid = Int(Map(summary["league"])["id"])
	}
	league := r.clubLeagues[leagueid]
	nationid := Int(league["countryid"])
	if leagueid == 78 {
		for nid, n := range r.clubNations {
			if Int(n["teamid"]) == teamid {
				nationid = nid
				break
			}
		}
	} else if leagueid == 76 {
		if nid, ok := r.rowNation[teamid]; ok {
			nationid = nid
		}
	}
	name := Str(info["teamname"])
	if name == "" {
		name = Str(summary["name"])
	}
	rivalName := ""
	if info != nil {
		if rt := r.clubTeams[Int(info["rivalteam"])]; rt != nil {
			rivalName = Str(rt["teamname"])
		}
	}
	summary["id"] = teamid
	summary["teamid"] = teamid
	summary["name"] = name
	summary["league"] = map[string]any{
		"id": leagueid, "name": Str(league["leaguename"]), "nationid": nationid}
	summary["nation"] = map[string]any{
		"id": nationid, "name": Str(r.clubNations[nationid]["nationname"])}
	summary["rival"] = map[string]any{"name": rivalName}
	sid := r.stadiumOf[teamid]
	stad := r.stadiums[sid]
	sname := Str(stad["name"])
	if sname == "" {
		sname = "Sân vận động Quốc gia Mỹ Đình"
	}
	yearbuilt := 2008
	if stad != nil && stad["yearbuilt"] != nil {
		yearbuilt = Int(stad["yearbuilt"])
	}
	capacity := 68000
	if stad != nil && stad["capacity"] != nil {
		capacity = Int(stad["capacity"])
	}
	out := map[string]any{}
	for k, v := range summary {
		out[k] = v
	}
	out["summary"] = summary
	out["stadium"] = map[string]any{
		"id": sid, "name": sname, "yearbuilt": yearbuilt, "capacity": capacity}
	out["kit"] = TeamKit(teamid)
	return out
}

// StadiumDoc serves GET /stadium/<id> (and /static/stadium/<id>)
// exactly like hc's ep_static_stadium_dynamic: the id is a stadium
// id, falling back to the team->stadium link when it is a team id.
// The Team info tab reads yearbuilt/capacity from this document —
// Go previously only had the hardcoded /stadium/1, so every other
// stadium rendered "undefined / undefined" (user report
// 2026-11-13, GM's Bernabéu id 2).
func (r *Roster) StadiumDoc(id int) map[string]any {
	s := r.stadiums[id]
	if s == nil {
		if sid := r.stadiumOf[id]; sid != 0 {
			s = r.stadiums[sid]
		}
	}
	name := Str(s["name"])
	if name == "" {
		name = "Sân vận động"
	}
	if strings.Contains(name, "Bernab") {
		name = "Santiago Bernabéu"
	}
	yb := 0
	for _, k := range []string{"yearbuilt", "buildyear", "year"} {
		if yb = Int(s[k]); yb != 0 {
			break
		}
	}
	if yb == 0 {
		yb = 1990
	}
	capacity := Int(s["capacity"])
	if capacity == 0 {
		capacity = 50000
	}
	return map[string]any{
		"id": id, "stadiumid": id, "name": name, "stadiumname": name,
		// The stadium stepper on the match setting screens uses
		// the stadium list docs themselves as its data provider
		// items and renders item.label (the tod/weather steppers
		// get explicit {label} objects built in configUI). Without
		// a label field the stadium row renders "[object Object]"
		// (owner report 2026-11-15).
		"label":     name,
		"yearbuilt": yb, "buildyear": yb, "year": yb, "capacity": capacity,
		// The match setting screens build their time/weather
		// steppers from this map (MatchProxy.getTODByStadiumId /
		// getWeatherByStadiumId / getTODWeatherByStadiumId read
		// stadium.todWeatherAvailables: tod id -> [weather ids]).
		// fx never served the field, so the steppers had no
		// stadium-backed option list at all (owner reports
		// 2026-11-14/15: arrows dead, tod label stuck). Tod ids
		// follow the client's own name table (1 = DAY, 3 =
		// NIGHT); weather ids 0 = DRY, 1 = RAINY, 2 = SNOWY —
		// exactly the owner's six accepted combinations.
		"todWeatherAvailables": map[string]any{
			"1": []any{0, 1, 2},
			"3": []any{0, 1, 2},
		},
	}
}

// StadiumList serves GET /stadium/list: the full catalog as an
// array of stadium docs (the client's MatchProxy keeps a stadium
// list it searches by id for the setting screens).
func (r *Roster) StadiumList() []any {
	ids := make([]int, 0, len(r.stadiums))
	for id := range r.stadiums {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.StadiumDoc(id))
	}
	return out
}

// StadiumIDAt resolves a position in StadiumList() order (the
// order the setting screens received) to a stadium id. The
// stadium stepper sends its selected INDEX tagged with +10000
// (MatchController.setStadium(index + 10000)), so incoming
// stadium picks must be mapped through the same list.
func (r *Roster) StadiumIDAt(index int) (int, bool) {
	ids := make([]int, 0, len(r.stadiums))
	for id := range r.stadiums {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	if index < 0 || index >= len(ids) {
		return 0, false
	}
	return ids[index], true
}

// StadiumFor returns (id, stadium doc) for a team (hc _CLUB_STADIUM_LINKS).
func (r *Roster) StadiumFor(teamid int, coach map[string]any) (int, map[string]any) {
	id := Int(coach["stadiumid"])
	if id == 0 {
		id = r.stadiumOf[teamid]
	}
	if id == 0 {
		id = 1
	}
	return id, r.stadiums[id]
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// PlayerSearch finds cards by player name (namesvie full/short,
// falling back to namesind) for the admin grant section. Each
// result is one card (spid) with its display name, season code,
// overall rating and grade-1 price.
func (r *Roster) PlayerSearch(q string, limit int) []map[string]any {
	like := "%" + q + "%"
	ids := []int{}
	rows, err := r.db.Query("SELECT DISTINCT playerid FROM namesvie WHERE full LIKE ? OR short LIKE ? LIMIT 120", like, like)
	if err == nil {
		for rows.Next() {
			var pid int
			if rows.Scan(&pid) == nil {
				ids = append(ids, pid)
			}
		}
		rows.Close()
	}
	if len(ids) == 0 {
		rows, err := r.db.Query("SELECT DISTINCT playerid FROM namesind WHERE short LIKE ? LIMIT 120", like)
		if err == nil {
			for rows.Next() {
				var pid int
				if rows.Scan(&pid) == nil {
					ids = append(ids, pid)
				}
			}
			rows.Close()
		}
	}
	out := []map[string]any{}
	if len(ids) == 0 {
		return out
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	prows, err := r.queryPlayers("WHERE p.playerid IN ("+strings.Join(ph, ",")+")", args...)
	if err != nil {
		return out
	}
	for _, row := range prows {
		if row.Spid < 1000000 {
			continue // base template row, not a real card
		}
		name, _ := r.PlayerDisplayName(row.Spid)
		out = append(out, map[string]any{
			"spid": row.Spid, "playerid": row.PlayerID, "name": name,
			"season":      row.Spid / 1000000,
			"season_name": SeasonLabel(row.Spid / 1000000),
			"ovr":         row.Rating(),
			"price":       int64(r.CardPrice(row.Spid, 1)),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i]["ovr"] != out[j]["ovr"] {
			return out[i]["ovr"].(int) > out[j]["ovr"].(int)
		}
		return out[i]["spid"].(int) > out[j]["spid"].(int)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// SeasonLabel maps a season code (spid / 1000000) to the label the
// client shows for that season (from the /static/roster seasons
// list, plus the special pools only present in the shop tables:
// 32 Midas, 53 TOTS '14, 91 World Best, 93 World Legend). Used by
// the admin so seasons read clearly instead of raw codes
// (user request 2026-11-13).
var seasonLabels = map[int]string{
	0: "Gốc", 6: "06", 7: "07", 8: "08", 9: "09", 10: "10", 11: "11",
	14: "14", 15: "15", 16: "16", 22: "CP", 23: "LP", 24: "CC",
	32: "Midas", 35: "16EC", 40: "NHD", 47: "10WC", 53: "TOTS '14",
	63: "06U", 65: "06WC", 67: "08E", 69: "10U", 77: "14WC",
	82: "CSL 82", 85: "CSL 85", 87: "CSL 87", 88: "17",
	91: "World Best", 93: "World Legend",
}

func SeasonLabel(code int) string {
	if s, ok := seasonLabels[code]; ok {
		return s
	}
	return fmt.Sprintf("Mùa %d", code)
}

// TeamSearch finds clubs by name for the admin grant section
// (empty query lists the first teams alphabetically).
func (r *Roster) TeamSearch(q string, limit int) []map[string]any {
	q = strings.ToLower(strings.TrimSpace(q))
	out := []map[string]any{}
	for id, t := range r.clubTeams {
		name := Str(t["teamname"])
		if name == "" {
			continue
		}
		if q == "" || strings.Contains(strings.ToLower(name), q) {
			out = append(out, map[string]any{"teamid": id, "teamname": name})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["teamname"].(string) < out[j]["teamname"].(string)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
