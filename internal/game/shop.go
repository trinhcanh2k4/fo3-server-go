package game

// Shop / inventory domain, ported from http_config.py (shop_entry,
// item_def, pack_pool, pack_price, purchase helpers, bag). The data
// tables (PACKS, ITEMS, PACKAGES, MONTHLY, CHAINED, GP prices) are
// extracted verbatim from the Python module into gamedata/
// shop_tables.json -- one source of truth, no hand transcription.

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type PackDef struct {
	Name string         `json:"name"`
	Pool map[string]any `json:"pool"`
}

type ItemDefRaw struct {
	Name   string         `json:"name"`
	Icon   any            `json:"icon"`
	Type   string         `json:"type"`
	Effect map[string]any `json:"effect"`
	Desc   string         `json:"desc"`
}

type PackageDef struct {
	Name     string  `json:"name"`
	Icon     any     `json:"icon"`
	Contents [][]any `json:"contents"`
	Desc     string  `json:"desc"`
}

type MonthlyDef struct {
	Name  string  `json:"name"`
	Icon  any     `json:"icon"`
	Days  int     `json:"days"`
	Now   [][]any `json:"now"`
	Daily [][]any `json:"daily"`
	Desc  string  `json:"desc"`
}

type ShopTables struct {
	Packs    map[int]PackDef
	Items    map[int]ItemDefRaw
	Packages map[int]PackageDef
	Monthly  map[int]MonthlyDef
	Chained  map[int][]int
	GPPrices map[int]int
	MPShop   []int
	ShopNew  []int
	ShopBest []int
	EPOrder  []int
	GPOrder  []int
}

const (
	EPItemID     = 1
	GPSHOPID     = 920_000_000
	MPShopID     = 900_000_000
	STShopID     = 910_000_000
	LockerItemID = 801
	CraftContract = 970_000_000
	CraftBox      = 970_000_001
	CraftPiece    = 971_000_000
	CraftBoxEP    = 5_000_000
)

// ShopTables returns the loaded shop tables for this roster's data dir.
func (r *Roster) ShopTables() *ShopTables { return Tables(r.gamedataDir) }

var shopTables *ShopTables
var shopOnce sync.Once

func Tables(gamedataDir string) *ShopTables {
	shopOnce.Do(func() {
		t := &ShopTables{Packs: map[int]PackDef{}, Items: map[int]ItemDefRaw{},
			Packages: map[int]PackageDef{}, Monthly: map[int]MonthlyDef{},
			Chained: map[int][]int{}, GPPrices: map[int]int{}}
		b, err := os.ReadFile(filepath.Join(gamedataDir, "shop_tables.json"))
		if err == nil {
			var raw struct {
				Packs    map[string]PackDef    `json:"packs"`
				Items    map[string]ItemDefRaw `json:"items"`
				Packages map[string]PackageDef `json:"packages"`
				Monthly  map[string]MonthlyDef `json:"monthly"`
				Chained  map[string][]int      `json:"chained"`
				GPPrices map[string]int        `json:"gp_prices"`
				MPShop   []int                 `json:"mp_shop"`
				ShopNew  []int                 `json:"shop_new"`
				ShopBest []int                 `json:"shop_best"`
				EPOrder  []int                 `json:"ep_list_order"`
				GPOrder  []int                 `json:"gp_list_order"`
			}
			if json.Unmarshal(b, &raw) == nil {
				for k, v := range raw.Packs {
					t.Packs[mustAtoi(k)] = v
				}
				for k, v := range raw.Items {
					t.Items[mustAtoi(k)] = v
				}
				for k, v := range raw.Packages {
					t.Packages[mustAtoi(k)] = v
				}
				for k, v := range raw.Monthly {
					t.Monthly[mustAtoi(k)] = v
				}
				for k, v := range raw.Chained {
					t.Chained[mustAtoi(k)] = v
				}
				for k, v := range raw.GPPrices {
					t.GPPrices[mustAtoi(k)] = v
				}
				t.MPShop, t.ShopNew, t.ShopBest = raw.MPShop, raw.ShopNew, raw.ShopBest
			t.EPOrder, t.GPOrder = raw.EPOrder, raw.GPOrder
			}
		}
		shopTables = t
	})
	return shopTables
}

var EPItemDef = map[string]any{"itemid": 1, "productid": 1, "name": "EP", "category": "EP",
	"type": "EP", "unit": "EP", "itemicon": 1}

var packBunches = []int{1, 10}
var positionsRange = map[string][2]int{"GK": {0, 0}, "DF": {1, 8}, "MF": {9, 19}, "FW": {20, 27}}

func ShopPrice(value float64, n int) int {
	f := 1.0
	if n > 1 {
		f = 0.95
	}
	return int(sig3(value * float64(n) * f))
}

func IsPiece(itemid int) bool { return itemid > CraftPiece && itemid < CraftPiece+1_000_000 }

// posovrSQL builds hc's POSOVR CASE expression from ROLE_WEIGHTS.
// All operands are integers, so SQLite's /100 is integer division,
// exactly as in the Python server.
func posovrSQL() string {
	var whens []string
	for pos := 0; pos <= 27; pos++ {
		w := roleWeights[posRole[pos]]
		stats := make([]string, 0, len(w))
		for col := range w {
			stats = append(stats, col)
		}
		sort.Strings(stats)
		terms := make([]string, 0, len(w))
		for _, col := range stats {
			terms = append(terms, itoa(w[col])+" * p."+col)
		}
		whens = append(whens, "WHEN "+itoa(pos)+" THEN ("+strings.Join(terms, " + ")+") / 100")
	}
	return "(CASE p.preferredposition1 " + strings.Join(whens, " ") + " END)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// --- pack pool / price --------------------------------------------------

type PoolEntry struct {
	Spid   int
	Weight float64
}

// PackSeasonLabel describes which season(s) a pack's pool draws
// from, using the same season extraction as PackPool (groups ->
// seasons -> season). "" for non-pack items; "Tất cả mùa" when the
// pool has no season restriction. For admin/shop display and pack
// descriptions (user request 2026-11-13).
func (r *Roster) PackSeasonLabel(itemid int) string {
	t := Tables(r.gamedataDir)
	pd, ok := t.Packs[itemid]
	if !ok {
		return ""
	}
	f := pd.Pool
	var seasons []int
	seen := map[int]bool{}
	add := func(s int) {
		if !seen[s] {
			seen[s] = true
			seasons = append(seasons, s)
		}
	}
	if groups := List(f["groups"]); len(groups) > 0 {
		for _, g := range groups {
			if gl := List(g); len(gl) > 0 && gl[0] != nil {
				add(Int(gl[0]))
			}
		}
	} else if ss := List(f["seasons"]); len(ss) > 0 {
		for _, s := range ss {
			add(Int(s))
		}
	} else if f["season"] != nil {
		add(Int(f["season"]))
	}
	if len(seasons) == 0 {
		return "Tất cả mùa"
	}
	labels := make([]string, 0, len(seasons))
	for _, s := range seasons {
		labels = append(labels, SeasonLabel(s))
	}
	return strings.Join(labels, ", ")
}

var poolCache sync.Map // itemid -> []PoolEntry

func (r *Roster) PackPool(itemid int) []PoolEntry {
	if v, ok := poolCache.Load(itemid); ok {
		return v.([]PoolEntry)
	}
	t := Tables(r.gamedataDir)
	pd, ok := t.Packs[itemid]
	if !ok {
		return nil
	}
	f := pd.Pool
	lo, hi := 0, 27
	if pr, ok := positionsRange[Str(f["pos"])]; ok {
		lo, hi = pr[0], pr[1]
	}
	groups := List(f["groups"])
	var seasons []int
	seenSeason := map[int]bool{}
	if len(groups) > 0 {
		for _, g := range groups {
			gl := List(g)
			if len(gl) > 0 && gl[0] != nil {
				s := Int(gl[0])
				if !seenSeason[s] {
					seenSeason[s] = true
					seasons = append(seasons, s)
				}
			}
		}
	} else if ss := List(f["seasons"]); len(ss) > 0 {
		for _, s := range ss {
			seasons = append(seasons, Int(s))
		}
	} else if f["season"] != nil {
		seasons = []int{Int(f["season"])}
	}
	ovrMin := Int(f["ovr"]) - gradeBoost[1]
	var league any
	if f["league"] != nil {
		league = Int(f["league"])
	}
	query := func(skipLicensed bool) [][2]any {
		q := "SELECT p.spid, " + posovrSQL() + " FROM players p LEFT JOIN permid m ON m.spid = p.spid " +
			"WHERE (" + posovrSQL() + ") >= ? AND p.preferredposition1 BETWEEN ? AND ? " +
			"AND (? IS NULL OR p.leagueid = ?) AND (? OR m.licensed = 1)"
		rows, err := r.db.Query(q, ovrMin, lo, hi, league, league, boolToInt(skipLicensed))
		if err != nil {
			return nil
		}
		defer rows.Close()
		var out [][2]any
		for rows.Next() {
			var spid int
			var ovr float64
			if rows.Scan(&spid, &ovr) == nil {
				out = append(out, [2]any{spid, ovr})
			}
		}
		return out
	}
	onMarket := func(spid int) bool { return r.OnMarket(spid) }
	seasonOK := func(spid int) bool {
		if len(seasons) == 0 {
			return true
		}
		for _, s := range seasons {
			if spid/1000000 == s {
				return true
			}
		}
		return false
	}
	// Python sorts floats; ovr here is the weighted sum/100 (not floored)
	// -- POSOVR in SQL is (sum)/100 without FLOOR as well.
	selectRows := func(raw [][2]any, requireMarket bool) [][2]any {
		var out [][2]any
		for _, rw := range raw {
			s := rw[0].(int)
			if requireMarket && !onMarket(s) {
				continue
			}
			if !seasonOK(s) {
				continue
			}
			out = append(out, rw)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i][1].(float64) > out[j][1].(float64) })
		return out
	}
	all := f["all"] != nil && f["all"] != false
	raw := query(all)
	rows := selectRows(raw, true)
	if len(rows) == 0 {
		rows = selectRows(raw, false)
	}
	if len(rows) == 0 && !all {
		rows = selectRows(query(true), false)
	}
	selected := map[int]int{}
	if len(groups) > 0 {
		ovrBySpid := map[int]float64{}
		for _, rw := range rows {
			ovrBySpid[rw[0].(int)] = rw[1].(float64)
		}
		var newRows [][2]any
		var order []int
		for _, g := range groups {
			gl := List(g)
			if len(gl) < 3 {
				continue
			}
			season, top, count := gl[0], Int(gl[1]), Int(gl[2])
			n := 0
			for _, rw := range rows {
				if season != nil && rw[0].(int)/1000000 != Int(season) {
					continue
				}
				if n >= top {
					break
				}
				spid := rw[0].(int)
				if _, seen := selected[spid]; !seen {
					order = append(order, spid)
				}
				selected[spid] += count
				n++
			}
		}
		for _, spid := range order {
			newRows = append(newRows, [2]any{spid, ovrBySpid[spid]})
		}
		rows = newRows
	} else if f["top"] != nil {
		if top := Int(f["top"]); top > 0 && len(rows) > top {
			rows = rows[:top]
		}
	}
	if len(rows) == 0 {
		poolCache.Store(itemid, []PoolEntry{})
		return []PoolEntry{}
	}
	prices := make([]float64, len(rows))
	for i, rw := range rows {
		// Python's pack_pool calls card_price(spid) whose grade defaults
		// to 1 -- grade 0 would price every card at GRADE_PRICE[0] = 0
		// and the weight formula max(p, floor)^-0.75 would divide by 0.
		prices[i] = r.CardPrice(rw[0].(int), 1)
	}
	sortedPrices := append([]float64{}, prices...)
	sort.Float64s(sortedPrices)
	floor := sortedPrices[len(sortedPrices)/2] / 10
	out := make([]PoolEntry, 0, len(rows))
	for i, rw := range rows {
		p := prices[i]
		if p < floor {
			p = floor
		}
		w := math.Pow(p, -0.75)
		if len(groups) > 0 {
			w *= float64(selected[rw[0].(int)])
		}
		out = append(out, PoolEntry{Spid: rw[0].(int), Weight: w})
	}
	poolCache.Store(itemid, out)
	return out
}

func packGrade(t *ShopTables, itemid int) int {
	if pd, ok := t.Packs[itemid]; ok {
		if g := Int(pd.Pool["grade"]); g != 0 {
			return g
		}
	}
	return 1
}

var packPriceCache sync.Map

func (r *Roster) PackPrice(itemid int) int {
	if v, ok := packPriceCache.Load(itemid); ok {
		return v.(int)
	}
	pool := r.PackPool(itemid)
	price := 1000
	if len(pool) > 0 {
		t := Tables(r.gamedataDir)
		g := packGrade(t, itemid)
		var num, den float64
		for _, e := range pool {
			num += r.CardPrice(e.Spid, g) * e.Weight
			den += e.Weight
		}
		if den > 0 {
			price = int(sig3(num / den))
		}
	}
	packPriceCache.Store(itemid, price)
	return price
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- item staticdata ----------------------------------------------------

// SetShopOverrides / SetGPOverrides install the admin price
// overrides (refreshed by the server from the settings table).
func (r *Roster) SetShopOverrides(m map[int]int64) { r.shopOv.Store(m) }
func (r *Roster) SetGPOverrides(m map[int]int64)   { r.gpOv.Store(m) }

// DefaultShopHidden lists the items recalled from the EP shop
// (owner decision 2026-11-13): EP lottery packs/cards, player
// EXP capsules and coach EXP capsules. The Top 200/300 player
// packs stay listed. A "shop_hidden" settings doc
// ({"itemids": [...]}) replaces this default wholesale; the
// items remain visible in the admin panel and grantable there.
var DefaultShopHidden = []int{
	501, 908, 50019, 50020, 50021, 50028, 50029, 50840, 50841,
	119082304, 117121308, 117121309, 117121310, 117121311,
	8223734, 8223735, 8223736, 81000001,
	910001, 910002, 910003, 910004, 910005, 910006, 912001,
	911001, 911002, 911003, 912002,
}

var defaultShopHiddenSet = func() map[int]bool {
	m := map[int]bool{}
	for _, id := range DefaultShopHidden {
		m[id] = true
	}
	return m
}()

// SetShopHidden installs the admin's recalled-items set.
func (r *Roster) SetShopHidden(ids []int) {
	m := map[int]bool{}
	for _, id := range ids {
		m[id] = true
	}
	r.shopHidden.Store(m)
}

// ShopHidden reports whether an item is recalled from the shop.
func (r *Roster) ShopHidden(itemid int) bool {
	if v := r.shopHidden.Load(); v != nil {
		return v.(map[int]bool)[itemid]
	}
	return defaultShopHiddenSet[itemid]
}

func (r *Roster) shopOverride(itemid int) (int64, bool) {
	if v := r.shopOv.Load(); v != nil {
		ov, ok := v.(map[int]int64)[itemid]
		return ov, ok
	}
	return 0, false
}

// EffectiveGPPrice resolves a GP price with the admin override.
func (r *Roster) EffectiveGPPrice(itemid int) (int, bool) {
	if v := r.gpOv.Load(); v != nil {
		if ov, ok := v.(map[int]int64)[itemid]; ok {
			return int(ov), true
		}
	}
	p, ok := Tables(r.gamedataDir).GPPrices[itemid]
	return p, ok
}

// ItemDef builds an item's staticdata, applying the admin EP
// price override (if any) to its bunches.
func (r *Roster) ItemDef(itemid int) map[string]any {
	def := r.itemDefRaw(itemid)
	if def != nil {
		if ov, ok := r.shopOverride(itemid); ok {
			bunches := []any{}
			for _, bv := range List(def["bunches"]) {
				if b := Map(bv); b != nil {
					n := Int(b["bunch"])
					if n <= 0 {
						n = 1
					}
					bunches = append(bunches, map[string]any{
						"bunch": n, "price": int64(ShopPrice(float64(ov), n))})
				}
			}
			if len(bunches) > 0 {
				def["bunches"] = bunches
			}
		}
	}
	return def
}

func (r *Roster) itemDefRaw(itemid int) map[string]any {
	t := Tables(r.gamedataDir)
	if itemid == EPItemID {
		return EPItemDef
	}
	if raw, ok := t.Items[itemid]; ok {
		return r.useItemDef(itemid, raw)
	}
	if IsPiece(itemid) {
		return r.PieceDef(itemid)
	}
	if _, ok := t.Packages[itemid]; ok {
		return r.packageDef(itemid)
	}
	if _, ok := t.Monthly[itemid]; ok {
		return r.packageDef(itemid)
	}
	pd, ok := t.Packs[itemid]
	if !ok {
		return nil
	}
	f := pd.Pool
	typ := "CUSTOM"
	if f["league"] != nil {
		typ = "LEAGUE"
	} else if f["pos"] != nil {
		typ = "POSITION"
	}
	bunches := []any{}
	for _, n := range packBunches {
		bunches = append(bunches, map[string]any{"bunch": n, "price": ShopPrice(float64(r.PackPrice(itemid)), n)})
	}
	year := 2017
	if f["season"] != nil {
		year = Int(f["season"])
	}
	out := map[string]any{"itemid": itemid, "productid": itemid, "name": pd.Name, "category": "PLAYERPACK",
		"type": typ, "unit": "COUNT", "itemicon": iconOr(f["icon"], itemid), "currency": "ep",
		"bunches": bunches, "draw": map[string]any{"year": year}}
	if f["icon"] != nil {
		out["bundleicon"] = f["icon"]
	}
	return out
}

func iconOr(v any, def int) any {
	if v == nil {
		return def
	}
	return v
}

func (r *Roster) useItemDef(itemid int, raw ItemDefRaw) map[string]any {
	out := map[string]any{"itemid": itemid, "productid": itemid, "name": raw.Name, "category": "USE",
		"type": raw.Type, "unit": "COUNT", "itemicon": raw.Icon, "bundleicon": raw.Icon,
		"currency": "ep", "stackable": true, "description": raw.Desc}
	if raw.Type == "PLAYER_EXP_CAPSULE" {
		out["capsuleexp"] = Int(raw.Effect["exp"])
		out["capsuleseason"] = nil
		out["capsulelv"] = nil
	}
	if ep, ok := raw.Effect["ep"]; ok {
		rng := List(ep)
		if len(rng) == 2 {
			out["count"] = map[string]any{"min": Int(rng[0]), "max": Int(rng[1])}
		}
	}
	if raw.Type == "ITEM_LOTTERY" {
		// TeamManagementRandomCardScreen reads staticdata.availables
		// unguarded (setCandidateCard takes its length) — a missing
		// key leaves it null and throws #1009 when the lottery box
		// screen opens (telemetry 862/893/898). The flip flow itself
		// runs through the open buttons + /item/<id>/use, so an empty
		// candidate list is safe.
		out["availables"] = []any{}
	}
	bunches := []any{}
	for _, n := range packBunches {
		bunches = append(bunches, map[string]any{"bunch": n, "price": ShopPrice(float64(r.UseItemPrice(itemid)), n)})
	}
	out["bunches"] = bunches
	return out
}

func (r *Roster) packageDef(itemid int) map[string]any {
	t := Tables(r.gamedataDir)
	var name string
	var icon any
	var contents [][]any
	var desc string
	extra := map[string]any{}
	if md, ok := t.Monthly[itemid]; ok {
		name, icon, contents, desc = md.Name, md.Icon, md.Now, md.Desc
		extra["daily_period"] = md.Days
		extra["monthly_platform"] = "all"
		daily := []any{}
		for _, cn := range md.Daily {
			daily = append(daily, map[string]any{"itemid": Int(cn[0]), "count": Int(cn[1]), "staticdata": r.ItemDef(Int(cn[0]))})
		}
		extra["dailyitems"] = daily
	} else if pd, ok := t.Packages[itemid]; ok {
		name, icon, contents, desc = pd.Name, pd.Icon, pd.Contents, pd.Desc
	}
	items := []any{}
	for _, cn := range contents {
		items = append(items, map[string]any{"itemid": Int(cn[0]), "count": Int(cn[1]), "staticdata": r.ItemDef(Int(cn[0]))})
	}
	out := map[string]any{"itemid": itemid, "productid": itemid, "name": name, "category": "PACKAGE",
		"type": "ITEM", "draw": map[string]any{"year": 2017}, "unit": "COUNT", "itemicon": icon,
		"bundleicon": icon, "currency": "ep", "description": desc,
		"bunches": []any{map[string]any{"bunch": 1, "price": ShopPrice(float64(r.PackageValue(itemid)), 1)}},
		"items": items}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func (r *Roster) UseItemPrice(itemid int) int {
	t := Tables(r.gamedataDir)
	raw, ok := t.Items[itemid]
	if !ok {
		return 500_000
	}
	eff := raw.Effect
	if itemid == LockerItemID || itemid == CraftContract {
		return 1_000_000
	}
	if p := Int(eff["price"]); p > 0 {
		return p
	}
	if _, ok := eff["pieces"]; ok {
		return CraftBoxEP
	}
	if ep, ok := eff["ep"]; ok {
		rng := List(ep)
		lo, hi := float64(Int(rng[0])), float64(Int(rng[1]))
		exp := lo
		if lo != hi {
			exp = (2*lo + hi) / 3
		}
		return int(sig3(exp * 0.95))
	}
	if prizes, ok := eff["prizes"]; ok {
		var tot, val float64
		for _, p := range List(prizes) {
			pl := List(p)
			if len(pl) < 2 {
				continue
			}
			w := float64(Int(pl[0]))
			tot += w
			if arr, isArr := pl[1].([]any); isArr { // ("ep", amount)
				val += w * float64(Int(arr[1]))
			} else {
				val += w * float64(r.PackPrice(Int(pl[1])))
			}
		}
		if tot > 0 {
			return int(sig3(val / tot * 0.5))
		}
		return 0
	}
	if exp, ok := eff["exp"]; ok {
		return int(sig3(float64(Int(exp)) * 20))
	}
	switch raw.Type {
	case "COACHNAME_CHANGE":
		return 1_000_000
	case "TEAM_CHANGE":
		return 2_000_000
	case "LEAGUE_SIMULATION":
		return 100_000
	}
	return 500_000
}

func (r *Roster) PackageValue(itemid int) int {
	t := Tables(r.gamedataDir)
	val := func(i, n int) float64 {
		if i == EPItemID {
			return float64(n)
		}
		if _, ok := t.Packs[i]; ok {
			return float64(r.PackPrice(i) * n)
		}
		if _, ok := t.Items[i]; ok {
			return float64(r.UseItemPrice(i) * n)
		}
		return 0
	}
	if md, ok := t.Monthly[itemid]; ok {
		sum := 0.0
		for _, cn := range md.Now {
			sum += val(Int(cn[0]), Int(cn[1]))
		}
		daily := 0.0
		for _, cn := range md.Daily {
			daily += val(Int(cn[0]), Int(cn[1]))
		}
		return int(sum) + int(daily)*md.Days/2
	}
	if pd, ok := t.Packages[itemid]; ok {
		sum := 0.0
		for _, cn := range pd.Contents {
			sum += val(Int(cn[0]), Int(cn[1]))
		}
		return int(sum)
	}
	return 0
}

// --- shop list entries ----------------------------------------------------

func (r *Roster) ShopEntry(itemid int, category string, shopid int) map[string]any {
	t := Tables(r.gamedataDir)
	d := r.ItemDef(itemid)
	out := map[string]any{}
	if shopid == 0 && d != nil {
		for _, lst := range []struct {
			key string
			ids []int
		}{{"new", t.ShopNew}, {"best", t.ShopBest}} {
			for i, id := range lst.ids {
				if id == itemid {
					out[lst.key] = i + 1
				}
			}
		}
	}
	var items []any
	if d != nil {
		if category == "PACKAGE" {
			items = List(d["items"])
		} else {
			items = []any{map[string]any{"itemid": itemid, "count": 1, "staticdata": d}}
		}
	}
	sid := shopid
	if sid == 0 {
		sid = itemid
	}
	sd := map[string]any{}
	for k, v := range d {
		sd[k] = v
	}
	sd["bundleicon"] = iconAny(d, itemid)
	sd["items"] = items
	out["_id"] = itoa(sid)
	out["shopid"] = sid
	out["shopcategory"] = category
	out["forceenable"] = true
	out["visible"] = true
	out["salestart"] = "2014-01-01T00:00:00.000Z"
	out["saleend"] = "2035-01-01T00:00:00.000Z"
	out["product"] = map[string]any{"itemid": itemid, "staticdata": sd}
	return out
}

func iconAny(d map[string]any, itemid int) any {
	if d != nil {
		if b, ok := d["bundleicon"]; ok && b != nil {
			return b
		}
	}
	return itemid
}

func (r *Roster) ShopList(st State) []any {
	t := Tables(r.gamedataDir)
	chainSteps := map[int]bool{}
	for _, steps := range t.Chained {
		for _, s := range steps {
			chainSteps[s] = true
		}
	}
	coach := CoachBySlot(st, DefaultSlot(st))
	var out []any
	ids := make([]int, 0, len(t.Packs))
	for id := range t.Packs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if r.ShopHidden(id) {
			continue
		}
		pd := t.Packs[id]
		top, hasTop := pd.Pool["top"]
		nm := strings.ToLower(pd.Name)
		if (hasTop && (Int(top) == 100 || Int(top) == 200 || Int(top) == 300)) ||
			strings.Contains(nm, "top 100") || strings.Contains(nm, "top 200") || strings.Contains(nm, "top 300") {
			out = append(out, r.ShopEntry(id, "PLAYERPACK", 0))
		}
	}
	itemIDs := make([]int, 0, len(t.Items))
	for id := range t.Items {
		itemIDs = append(itemIDs, id)
	}
	sort.Ints(itemIDs)
	for _, id := range itemIDs {
		if r.ShopHidden(id) {
			continue
		}
		out = append(out, r.ShopEntry(id, "USE", 0))
	}
	pkgIDs := make([]int, 0, len(t.Packages))
	for id := range t.Packages {
		pkgIDs = append(pkgIDs, id)
	}
	sort.Ints(pkgIDs)
	for _, id := range pkgIDs {
		if !chainSteps[id] && !r.ShopHidden(id) {
			out = append(out, r.ShopEntry(id, "PACKAGE", 0))
		}
	}
	for _, id := range t.MPShop {
		if !r.ShopHidden(id) {
			out = append(out, r.ShopEntry(id, "MILEAGE", MPShopID+id))
		}
	}
	monIDs := make([]int, 0, len(t.Monthly))
	for id := range t.Monthly {
		monIDs = append(monIDs, id)
	}
	sort.Ints(monIDs)
	for _, id := range monIDs {
		if r.ShopHidden(id) {
			continue
		}
		out = append(out, r.ShopEntry(id, "MONTHLYPACK", 0))
	}
	firsts := make([]int, 0, len(t.Chained))
	for id := range t.Chained {
		firsts = append(firsts, id)
	}
	sort.Ints(firsts)
	for _, first := range firsts {
		out = append(out, r.chainedEntry(coach, first))
	}
	sortByOrder(out, t.EPOrder)
	return out
}

func (r *Roster) chainedEntry(coach map[string]any, first int) map[string]any {
	t := Tables(r.gamedataDir)
	steps := t.Chained[first]
	done := 0
	if coach != nil {
		done = Int(Map(coach["chain"])[itoa(first)])
	}
	parts := []map[string]any{}
	for k, id := range steps {
		e := r.ShopEntry(id, "CHAINED", 0)
		remained := 1
		if k < done {
			remained = 0
		}
		e["remainedperaccount"] = remained
		e["buy"] = k < done
		parts = append(parts, e)
	}
	out := parts[0]
	step := done
	if step > len(steps)-1 {
		step = len(steps) - 1
	}
	out["step"] = step
	rest := []any{}
	for _, p := range parts[1:] {
		rest = append(rest, p)
	}
	out["chained"] = rest
	return out
}


// sortByOrder stable-sorts shop entries by their itemid's position in
// the authored order list (extracted from the Python server's own
// responses). The client consumes list position semantically (quest
// reward boxes, shop sections), so Go's original itemid sort was a
// visible behavior difference, not a cosmetic one.
func sortByOrder(out []any, order []int) {
	if len(order) == 0 {
		return
	}
	idx := make(map[int]int, len(order))
	for i, id := range order {
		if _, seen := idx[id]; !seen {
			idx[id] = i
		}
	}
	key := func(e any) int {
		m := Map(e)
		if m == nil {
			return len(order)
		}
		if i, ok := idx[Int(Map(m["product"])["itemid"])]; ok {
			return i
		}
		return len(order)
	}
	sort.SliceStable(out, func(a, b int) bool { return key(out[a]) < key(out[b]) })
}

// GPShopList serves the GP shop: ONLY the GP-priced quest entries
// plus the consumable range. Python also appended the whole pack
// catalogue at EP prices; those entries are unbuyable here (the GP
// purchase path only knows GP_SHOP_PRICES) and the quest window's
// reward boxes read this list -- EP-priced pack copies are exactly
// what made it render Top-200 packs at millions of "GP" (one price
// overflowing int32). Packs are sold in the EP shop instead.
func (r *Roster) GPShopList() []any {
	t := Tables(r.gamedataDir)
	var out []any
	gpIDs := make([]int, 0, len(t.GPPrices))
	for id := range t.GPPrices {
		gpIDs = append(gpIDs, id)
	}
	sort.Ints(gpIDs)
	for _, id := range gpIDs {
		out = append(out, r.gpShopEntry(id))
	}
	// ONLY the GP-priced reward boxes belong to this list (upstream
	// gp_shop_list). Copying the EP-priced USE items in here made the
	// Quest window render those (its boxes are picked from this list)
	// with EP prices under a GP coin icon.
	sortByOrder(out, t.GPOrder)
	return out
}

func (r *Roster) gpShopEntry(itemid int) map[string]any {
	e := r.ShopEntry(itemid, "GPSHOP", GPSHOPID+itemid)
	prod := Map(e["product"])
	sd := Map(prod["staticdata"])
	gp, _ := r.EffectiveGPPrice(itemid)
	sd["bunches"] = []any{map[string]any{"bunch": 1, "price": gp}}
	icon := sd["bundleicon"]
	if icon == nil {
		icon = sd["itemicon"]
	}
	sd["bundleicon"] = "quest_" + iconStr(icon, itemid)
	return e
}

func iconStr(v any, itemid int) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return itoa(int(x))
	case int:
		return itoa(x)
	}
	return itoa(itemid)
}

func ProductCategory(t *ShopTables, itemid int) string {
	if _, ok := t.Packs[itemid]; ok {
		return "PLAYERPACK"
	}
	if _, ok := t.Items[itemid]; ok {
		return "USE"
	}
	return "PACKAGE"
}

// --- inventory / bag / players ---------------------------------------------

func randRead(b []byte) {
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte(time.Now().UnixNano() >> (i % 8 * 8))
		}
	}
}

func nowUnix() int64 { return time.Now().Unix() }

func NewIID() string {
	b := make([]byte, 12)
	randRead(b)
	const hexd = "0123456789abcdef"
	out := make([]byte, 24)
	for i, x := range b {
		out[2*i] = hexd[x>>4]
		out[2*i+1] = hexd[x&15]
	}
	return string(out)
}

func InventoryAdd(coach map[string]any, itemid, count int) map[string]any {
	inv := List(coach["items"])
	for _, raw := range inv {
		it := Map(raw)
		if it != nil && Int(it["itemid"]) == itemid {
			it["remains"] = Int(it["remains"]) + count
			coach["items"] = inv
			return it
		}
	}
	it := map[string]any{"_id": NewIID(), "itemid": itemid, "remains": count}
	coach["items"] = append(inv, it)
	return it
}

func FirstTeamCount(team map[string]any) int {
	n := 0
	for _, raw := range List(team["players"]) {
		p := Map(raw)
		if p != nil && p["team2"] != true {
			n++
		}
	}
	return n
}

func (r *Roster) AddPlayer(team map[string]any, spid, grade int) map[string]any {
	pid := r.PlayerID(spid)
	name := ""
	if pid > 0 {
		name = r.jerseyName(pid)
	}
	used := map[int]bool{}
	for _, raw := range List(team["players"]) {
		if p := Map(raw); p != nil {
			used[Int(p["jersey"])] = true
		}
	}
	jersey := 99
	for n := 12; n < 100; n++ {
		if !used[n] {
			jersey = n
			break
		}
	}
	p := map[string]any{"_id": NewIID(), "spid": spid, "expandme": "player", "grade": grade,
		"jersey": jersey, "jerseyname": name}
	if FirstTeamCount(team) >= FirstTeamMax {
		p["team2"] = true
	}
	team["players"] = append(List(team["players"]), p)
	return p
}

// --- EXP ----------------------------------------------------------------

const (
	PlayerMaxLevel = 23
	CoachMaxLevel  = 100
)

func PlayerNeed(level int) int { return 100 * level }
func CoachNeed(level int) int  { return 500 + 100*(level-1) }

// ExpPlayerTable is hc's EXPPLAYER static (GET /static/db/EXPPLAYER):
// {level: {requiredplayerexp}} for levels 1..max+1. The client's
// PlayerListUtil takes its level table from this document and
// derives max level as rows-1 (getMaxLevel) — serving {} here made
// getMaxLevel() -1 and the EXP capsule screen's level filter
// reject every player (empty list, user report 2026-11-13).
func ExpPlayerTable() map[string]any {
	out := map[string]any{}
	for lv := 1; lv <= PlayerMaxLevel+1; lv++ {
		out[fmt.Sprintf("%d", lv)] = map[string]any{"requiredplayerexp": PlayerNeed(lv)}
	}
	return out
}

// ExpCoachTable is hc's EXPCOACH static (GET /static/db/EXPCOACH):
// {level: {totalexp}} with cumulative coach EXP requirements.
func ExpCoachTable() map[string]any {
	out := map[string]any{}
	total := 0
	for lv := 1; lv <= CoachMaxLevel; lv++ {
		total += CoachNeed(lv)
		out[fmt.Sprintf("%d", lv)] = map[string]any{"totalexp": total}
	}
	return out
}

// GainExp ports hc.gain_exp: mutate owner's levelinfo; returns the
// (prev, current) served levelinfo pair.
func GainExp(owner map[string]any, n int, coach bool) (map[string]any, map[string]any) {
	need, top := PlayerNeed, PlayerMaxLevel
	if coach {
		need, top = CoachNeed, CoachMaxLevel
	}
	prev := MakeLevelinfo(owner["levelinfo"], need, top)
	level, exp := Int(prev["level"]), Int(prev["exp"])+n
	for level < top && exp >= need(level) {
		exp -= need(level)
		level++
	}
	if level >= top {
		exp = 0
	}
	owner["levelinfo"] = map[string]any{"level": level, "exp": exp}
	return prev, MakeLevelinfo(owner["levelinfo"], need, top)
}

// StoredGP is the quest GP balance as stored (the weekly rollover and
// quest crediting live with the quest domain port).
func StoredGP(st State) int {
	return Int(Map(st["quest"])["gp"])
}

func BagAdd(coach map[string]any, title string, items []any, msg string) map[string]any {
	entry := map[string]any{"_id": NewIID(), "title": title, "msg": msg,
		"items": items, "players": []any{}, "createdat": nowUnix()}
	coach["bag"] = append(List(coach["bag"]), entry)
	return entry
}

func (r *Roster) BagList(st State) []any {
	coach := CoachBySlot(st, DefaultSlot(st))
	if coach == nil {
		return []any{}
	}
	t := Tables(r.gamedataDir)
	out := []any{}
	for _, raw := range List(coach["bag"]) {
		e := Map(raw)
		if e == nil {
			continue
		}
		icon := any(EPItemID)
		for _, iraw := range List(e["items"]) {
			im := Map(iraw)
			if im != nil && Int(im["itemid"]) != EPItemID {
				iid := Int(im["itemid"])
				if pd, ok := t.Packs[iid]; ok && pd.Pool["icon"] != nil {
					icon = pd.Pool["icon"]
				} else {
					icon = iid
				}
				break
			}
		}
		msg := Str(e["msg"])
		if msg == "" {
			msg = "Phần thưởng từ hệ thống máy chủ."
		}
		typ := Str(e["type"])
		if typ == "" {
			typ = "event"
		}
		out = append(out, map[string]any{"_id": e["_id"], "id": e["_id"], "type": typ,
			"title": e["title"], "msg": msg, "date": W3C(float64(Int64(e["createdat"]))),
			"items": List(e["items"]), "players": List(e["players"]), "bundleicon": icon})
	}
	return out
}


// PackGradeOf is hc.pack_grade: the pack's guaranteed enhancement.
func PackGradeOf(t *ShopTables, itemid int) int { return packGrade(t, itemid) }

// WarmPacks precomputes every pack pool and price (hc.warm_packs).
func (r *Roster) WarmPacks() {
	t := Tables(r.gamedataDir)
	for id := range t.Packs {
		r.PackPrice(id)
	}
}
