package game

// Transfer market + player trade + enhancement, ported from hc's
// market block: NPC-priced offers (fifaaddict card_price), selling
// with a 10% fee and IGR/VIP fee rebates, two-card enhancement, and
// the trade / trade3 (Scout) exchanges.

import (
	"encoding/json"
	mrand "math/rand"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// MarketOffer ports hc.market_offer.
func (r *Roster) MarketOffer(spid, grade int) map[string]any {
	return map[string]any{"spid": spid, "expandme": "player", "grade": grade,
		"price": int64(r.CardPrice(spid, grade)), "count": 1}
}

var marketLines = map[string][2]int{"GK": {0, 1}, "DF": {1, 9}, "MF": {9, 20}, "FW": {20, 28}}

// TransferTop ports ep_transfer_top: the 50 priciest tradeable
// cards (optionally one position line), best client rating first.
func (r *Roster) TransferTop(position string) []any {
	rows, err := r.queryPlayers("")
	if err != nil {
		return []any{}
	}
	rng, hasLine := marketLines[strings.ToUpper(position)]
	type cand struct {
		spid, rating int
	}
	cands := []cand{}
	for _, row := range rows {
		if hasLine && (row.PrefPos1 < rng[0] || row.PrefPos1 >= rng[1]) {
			continue
		}
		if r.OnMarket(row.Spid) {
			cands = append(cands, cand{row.Spid, row.Rating()})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].rating != cands[j].rating {
			return cands[i].rating > cands[j].rating
		}
		return cands[i].spid < cands[j].spid
	})
	out := []any{}
	for i := 0; i < len(cands) && i < 50; i++ {
		out = append(out, r.MarketOffer(cands[i].spid, 1))
	}
	return out
}

// IsStarter / InSquad port hc.is_starter / in_squad.
func IsStarter(team, p map[string]any) bool {
	pid := IID(p)
	for _, sv := range List(Map(team["squad"])["positions"]) {
		if s := Map(sv); s != nil && squadSlotID(s) == pid && Int(s["pos"]) < PosSub {
			return true
		}
	}
	return false
}

func squadSlotID(s map[string]any) string {
	if id := Str(s["id"]); id != "" {
		return id
	}
	if s["id"] != nil {
		return fmt_Sprint(s["id"])
	}
	return fmt_Sprint(s["spid"])
}

// RemovePlayer ports hc.remove_player.
func (r *Roster) RemovePlayer(team, p map[string]any) {
	players := []any{}
	for _, pv := range List(team["players"]) {
		if Map(pv) != nil && IID(Map(pv)) != IID(p) {
			players = append(players, pv)
		}
	}
	team["players"] = players
	sq := Map(team["squad"])
	pid := IID(p)
	positions := []any{}
	for _, sv := range List(sq["positions"]) {
		if s := Map(sv); s != nil && squadSlotID(s) != pid {
			positions = append(positions, sv)
		}
	}
	sq["positions"] = positions
	kickers := []any{}
	for _, kv := range List(sq["kickers"]) {
		if Str(kv) != pid && fmt_Sprint(kv) != pid {
			kickers = append(kickers, kv)
		}
	}
	sq["kickers"] = kickers
	r.repairSquad(team)
	if Str(sq["captain"]) == pid {
		sq["captain"] = ""
		for _, sv := range List(sq["positions"]) {
			if s := Map(sv); s != nil && Int(s["pos"]) != 0 {
				sq["captain"] = squadSlotID(s)
				break
			}
		}
	}
}

// --- Selling ---------------------------------------------------------------

const SaleFee = 0.10

// BoostFor ports NO_BOOST for one user: the IGR (PC) 20% and
// VIP 30% fee rebates apply only while the matching admin-granted
// entitlement is active (both were always-on before 2026-11-13).
func BoostFor(st State) map[string]any {
	igr, vip := 0.0, 0.0
	if st != nil && PCActive(st) {
		igr = CfgF("igr_refund", 0.2)
	}
	if st != nil && VIPActive(st) {
		vip = CfgF("vip_refund", 0.3)
	}
	return map[string]any{"igr": igr, "vip": vip, "event": 0, "item": 0, "discountlimit": 0}
}

// SaleNet ports hc.sale_net (fee and IGR/VIP rebates are admin
// settings: sale_fee_pct is applied when the offer is created;
// the rebates resolve here at collect time, gated on the user's
// PC/VIP entitlements).
func SaleNet(st State, o map[string]any) int {
	fee := Int(Map(o["completefee"])["base"])
	net := Int(o["price"]) - fee
	if st != nil && PCActive(st) {
		net += int(CfgF("igr_refund", 0.2) * float64(fee))
	}
	if st != nil && VIPActive(st) {
		net += int(CfgF("vip_refund", 0.3) * float64(fee))
	}
	return net
}

// SaleFeeNow resolves the current sale fee rate.
func SaleFeeNow() float64 { return CfgF("sale_fee_pct", 10) / 100 }

// OfferList ports hc.offer_list (state["offers"]).
func OfferList(st State) map[string]any {
	offers := st["offers"]
	if _, ok := offers.([]any); !ok {
		offers = []any{}
	}
	return map[string]any{"offerexpireoffset": 86400000, "boost": BoostFor(st),
		"offers": offers, "soldedoffers": []any{}}
}

// --- Enhancement -----------------------------------------------------------

// EnhanceRates ports ENHANCE_RATE (+g -> +(g+1) base chance).
var EnhanceRates = []float64{1.0, 1.0, 0.81, 0.64, 0.50, 0.26, 0.15, 0.07, 0.04, 0.02, 0}

// EnhanceRate ports enhance_rate incl. the enhancement_settings
// override (gamedata/enhancement_settings.json success_percent).
func (r *Roster) EnhanceRate(target, material map[string]any) float64 {
	if b, err := readFileCached(filepath.Join(r.gamedataDir, "enhancement_settings.json")); err == nil {
		var doc map[string]any
		if json.Unmarshal(b, &doc) == nil {
			if v, ok := doc["success_percent"].(float64); ok && v == float64(int(v)) && v >= 1 && v <= 100 {
				return v / 100.0
			}
		}
	}
	return DefaultEnhanceRate(target, material)
}

// DefaultEnhanceRate ports default_enhance_rate.
func DefaultEnhanceRate(target, material map[string]any) float64 {
	tg, mg := GradeOf(target), GradeOf(material)
	bonus := 1.1
	if mg <= 1 {
		bonus = 1.0
	} else if float64(mg) >= float64(tg)*2.0/3.0 {
		bonus = 1.5
	} else if float64(mg) >= float64(tg)/3.0 {
		bonus = 1.25
	}
	rate := EnhanceRates[tg] * bonus
	if rate > 1.0 {
		rate = 1.0
	}
	return rate
}

// EnhanceCost ports enhance_cost (2% of value, min 1,000, sig3).
func (r *Roster) EnhanceCost(target map[string]any) int {
	c := int(r.CardPrice(Int(target["spid"]), GradeOf(target))) / 50
	if c < 1000 {
		c = 1000
	}
	return int(sig3(float64(c)))
}

// FailedEnhanceGrade ports failed_enhance_grade.
func FailedEnhanceGrade(target, material map[string]any) int {
	before, mg := GradeOf(target), GradeOf(material)
	if before <= 1 {
		return 0
	}
	hi := before
	if mg < hi {
		hi = mg
	}
	if hi < 1 {
		hi = 1
	}
	return 1 + mrand.Intn(hi)
}

// EnhancePair ports enhance_pair.
func EnhancePair(team map[string]any, a, b string) (map[string]any, map[string]any) {
	var pa, pb map[string]any
	for _, pv := range List(team["players"]) {
		if p := Map(pv); p != nil {
			if IID(p) == a {
				pa = p
			}
			if IID(p) == b {
				pb = p
			}
		}
	}
	if pa == nil || pb == nil || Str(pa["_id"]) == Str(pb["_id"]) ||
		Int(pa["spid"]) != Int(pb["spid"]) || GradeOf(pa) >= 10 {
		return nil, nil
	}
	return pa, pb
}

// MergeInto ports merge_into: the consumed card's squad slot (when
// better than the kept card's) passes to the kept card.
func (r *Roster) MergeInto(team, keep, gone map[string]any) {
	sq := Map(team["squad"])
	k, g := IID(keep), IID(gone)
	slots := map[string]map[string]any{}
	var slotList []map[string]any
	for _, sv := range List(sq["positions"]) {
		if s := Map(sv); s != nil {
			slots[squadSlotID(s)] = s
			slotList = append(slotList, s)
		}
	}
	gs, hasG := slots[g]
	ks, hasK := slots[k]
	if hasG && (!hasK || (Int(gs["pos"]) < PosSub && Int(ks["pos"]) >= PosSub)) {
		if hasK {
			positions := []any{}
			for _, s := range slotList {
				if squadSlotID(s) != k {
					positions = append(positions, s)
				}
			}
			sq["positions"] = positions
		}
		gs["id"] = k
		if Str(sq["captain"]) == g {
			sq["captain"] = k
		}
		kickers := []any{}
		for _, kv := range List(sq["kickers"]) {
			if fmt_Sprint(kv) == g {
				kickers = append(kickers, k)
			} else {
				kickers = append(kickers, kv)
			}
		}
		sq["kickers"] = kickers
	}
	r.RemovePlayer(team, gone)
}

// --- Trade v1 ---------------------------------------------------------------

// StaticValues loads responses/static_db_STATICVALUES.json (the
// Python code re-reads it per call).
func (r *Roster) StaticValues() map[string]any {
	b, err := readFileCached(filepath.Join(r.responsesDir, "static_db_STATICVALUES.json"))
	if err != nil {
		return map[string]any{}
	}
	var doc map[string]any
	if json.Unmarshal(b, &doc) != nil {
		return map[string]any{}
	}
	return doc
}

var (
	tradePoolOnce sync.Once
	tradePoolCache map[int][][2]int
)

// TradePool ports trade_pool: {client rating: [(spid, teamid)]} of
// every Market card (cached).
func (r *Roster) TradePool() map[int][][2]int {
	tradePoolOnce.Do(func() {
		tradePoolCache = map[int][][2]int{}
		rows, err := r.queryPlayers("")
		if err != nil {
			return
		}
		for _, row := range rows {
			if r.OnMarket(row.Spid) {
				rt := row.Rating()
				tradePoolCache[rt] = append(tradePoolCache[rt], [2]int{row.Spid, row.TeamID})
			}
		}
	})
	return tradePoolCache
}

// TradeGrades ports trade_grades.
func TradeGrades(gsum int) map[string]float64 {
	out := map[string]float64{}
	maxG := 1 + gsum/8
	if maxG > 10 {
		maxG = 10
	}
	for g := 1; g <= maxG; g++ {
		w := 1.0
		for i := 1; i < g; i++ {
			w *= 0.5
		}
		out[strconv.Itoa(g)] = w
	}
	return out
}

// TradeProps ports trade_props (rng = math/rand global, Python used
// a fresh random.Random()).
func (r *Roster) TradeProps(st State, give []map[string]any) []any {
	sv := r.StaticValues()
	lim := Map(sv["playertrade_limit"])
	n := len(give)
	avg := 0.0
	total := 0.0
	gsum := 0
	for _, p := range give {
		avg += float64(r.CardOvr(Int(p["spid"])))
		total += r.CardPrice(Int(p["spid"]), GradeOf(p))
		gsum += GradeOf(p)
	}
	avg /= float64(n)
	bonus := Map(sv["playertrade_cardovrbonus"])[strconv.Itoa(n)]
	lo := int(avg + Float(bonus))
	if lo < Int(lim["CardMinValue"]) {
		lo = Int(lim["CardMinValue"])
	}
	if lo > Int(lim["CardMaxValue"]) {
		lo = Int(lim["CardMaxValue"])
	}
	weights := TradeGrades(gsum)
	pool := r.TradePool()
	normal := Int(Map(sv["playertrade_normal"])[strconv.Itoa(n)])
	boosts := []any{}
	for i := 0; i < normal; i++ {
		boosts = append(boosts, nil)
	}
	if PCActive(st) {
		boosts = append(boosts, "igr")
	}
	if VIPActive(st) {
		boosts = append(boosts, "vip")
	}
	props := []any{}
	for i, boost := range boosts {
		ovr := lo
		hiOvr := lo + 8
		if hiOvr > Int(lim["CardMaxValue"]) {
			hiOvr = Int(lim["CardMaxValue"])
		}
		for ovr < hiOvr && mrand.Float64() < 0.55 {
			ovr++
		}
		best := 0
		bestDiff := -1
		for rt := range pool {
			diff := rt - ovr
			if diff < 0 {
				diff = -diff
			}
			if bestDiff < 0 || diff < bestDiff || (diff == bestDiff && rt > best) {
				best, bestDiff = rt, diff
			}
		}
		cards := pool[best]
		pick := cards[mrand.Intn(len(cards))]
		if i == 1 && boost == nil {
			ep := total * (0.9 + mrand.Float64()*0.25)
			if ep > total {
				// Price stabilization (2026-11-15): the trade EP
				// payout is capped at the cards' reference value —
				// an exchange must not mint EP above the stabilized
				// market band's upper edge.
				ep = total
			}
			props = append(props, map[string]any{"teamid": pick[1],
				"ep": int(sig3(ep))})
			continue
		}
		g := weightedGrade(weights)
		offer := map[string]any{"spid": pick[0], "grade": g, "teamid": pick[1],
			"price": int64(r.CardPrice(pick[0], g))}
		if i == 2 && boost == nil {
			offer["ep"] = int(sig3(total * (0.05 + mrand.Float64()*0.15)))
		}
		if boost != nil {
			offer["boost"] = boost
		}
		props = append(props, offer)
	}
	return props
}

func weightedGrade(weights map[string]float64) int {
	keys := []int{}
	for k := range weights {
		g, _ := strconv.Atoi(k)
		keys = append(keys, g)
	}
	sort.Ints(keys)
	total := 0.0
	for _, g := range keys {
		total += weights[strconv.Itoa(g)]
	}
	x := mrand.Float64() * total
	for _, g := range keys {
		x -= weights[strconv.Itoa(g)]
		if x <= 0 {
			return g
		}
	}
	return keys[len(keys)-1]
}

// --- Trade3 (Scout) ---------------------------------------------------------

// Trade3Static ports TRADE3_STATIC (unlimited caps by user request).
func Trade3Static() map[string]any {
	noLimit := int64(9000000000000000)
	normal := map[string]any{}
	for n := 2; n <= 10; n++ {
		v := 1
		if n >= 4 {
			v++
		}
		if n >= 7 {
			v++
		}
		normal[strconv.Itoa(n)] = v
	}
	return map[string]any{
		"playertrade3_limit":        map[string]any{"max_trade": noLimit, "max": noLimit},
		"vvip_playertrade3_limit":   map[string]any{"max_trade": noLimit, "max": noLimit},
		"playertrade3_normal":       normal,
		"playertrade3_effect_price": []any{10000000, 3000000, 1000000},
	}
}

// ScoutDef is one SCOUTS row.
type ScoutDef struct {
	Key   string
	Range [2]float64
	Quest map[string]any
}

// Scouts ports the SCOUTS table (cooldowns all zero).
var Scouts = []ScoutDef{
	{Key: "default_A01", Range: [2]float64{0.8, 1.1}},
	{Key: "default_B01", Range: [2]float64{0.9, 1.3}, Quest: map[string]any{"require_value": 100000}},
	{Key: "igr", Range: [2]float64{0.9, 1.4}, Quest: map[string]any{"require_grade": 2, "require_grade_count": 2}},
	{Key: "vip", Range: [2]float64{1.0, 1.5}, Quest: map[string]any{"require_season": "88", "require_season_count": 2}},
}

// QuestMet ports quest_met.
func (r *Roster) QuestMet(quest map[string]any, give []map[string]any) bool {
	if quest == nil {
		return false
	}
	total := 0.0
	for _, p := range give {
		total += r.CardPrice(Int(p["spid"]), GradeOf(p))
	}
	if total < Float(quest["require_value"]) {
		return false
	}
	if quest["require_grade"] != nil {
		cnt := 0
		for _, p := range give {
			if GradeOf(p) >= Int(quest["require_grade"]) {
				cnt++
			}
		}
		if cnt < Int(quest["require_grade_count"]) {
			return false
		}
	}
	seasons := map[int]bool{}
	for _, part := range strings.Split(Str(quest["require_season"]), ",") {
		if part != "" {
			if v, err := strconv.Atoi(part); err == nil {
				seasons[v] = true
			}
		}
	}
	if len(seasons) == 0 {
		return true
	}
	cnt := 0
	for _, p := range give {
		if seasons[Int(p["spid"])/1000000] {
			cnt++
		}
	}
	return cnt >= Int(quest["require_season_count"])
}

type trade3Card struct {
	price, spid, teamid, grade int
}

var (
	trade3PoolOnce  sync.Once
	trade3PoolCache []trade3Card
)

// Trade3Pool ports trade3_pool: every Market card at grades 1..10,
// cheapest first (cached).
func (r *Roster) Trade3Pool() []trade3Card {
	trade3PoolOnce.Do(func() {
		pool := r.TradePool()
		for _, cards := range pool {
			for _, st := range cards {
				for g := 1; g <= 10; g++ {
					trade3PoolCache = append(trade3PoolCache, trade3Card{
						price: int(r.CardPrice(st[0], g)), spid: st[0], teamid: st[1], grade: g})
				}
			}
		}
		sort.Slice(trade3PoolCache, func(i, j int) bool {
			if trade3PoolCache[i].price != trade3PoolCache[j].price {
				return trade3PoolCache[i].price < trade3PoolCache[j].price
			}
			if trade3PoolCache[i].spid != trade3PoolCache[j].spid {
				return trade3PoolCache[i].spid < trade3PoolCache[j].spid
			}
			return trade3PoolCache[i].grade < trade3PoolCache[j].grade
		})
	})
	return trade3PoolCache
}

// Trade3Props ports trade3_props.
func (r *Roster) Trade3Props(st State, give []map[string]any, scout ScoutDef) []any {
	lo, hi := scout.Range[0], scout.Range[1]
	if r.QuestMet(scout.Quest, give) {
		lo += 0.2
		hi += 0.2
	}
	total := 0.0
	for _, p := range give {
		total += r.CardPrice(Int(p["spid"]), GradeOf(p))
	}
	pool := r.Trade3Pool()
	normal := Int(Trade3Static()["playertrade3_normal"].(map[string]any)[strconv.Itoa(len(give))])
	boosts := []any{}
	for i := 0; i < normal; i++ {
		boosts = append(boosts, nil)
	}
	if PCActive(st) {
		boosts = append(boosts, "igr")
	}
	if VIPActive(st) {
		boosts = append(boosts, "vip")
	}
	props := []any{}
	for i, boost := range boosts {
		target := total * (lo + mrand.Float64()*(hi-lo))
		k := sort.Search(len(pool), func(j int) bool { return float64(pool[j].price) >= target })
		from := k - 10
		if from < 0 {
			from = 0
		}
		to := k + 10
		if to > len(pool) {
			to = len(pool)
		}
		pick := pool[from+mrand.Intn(to-from)]
		offer := map[string]any{"spid": pick.spid, "grade": pick.grade, "teamid": pick.teamid,
			"price": pick.price}
		if i == normal-1 && normal > 1 {
			ep := int(total * (0.05 + mrand.Float64()*0.10))
			if ep < 1000 {
				ep = 1000
			}
			offer["ep"] = int(sig3(float64(ep)))
		}
		if boost != nil {
			offer["boost"] = boost
		}
		props = append(props, offer)
	}
	return props
}

// GradeBoostAt returns GRADE_BOOST[g] (0 outside 0..10).
func GradeBoostAt(g int) int {
	if g < 0 || g >= len(gradeBoost) {
		return 0
	}
	return gradeBoost[g]
}
