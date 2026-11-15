package api

// Shop + inventory endpoints (Phase 2, shop domain):
// GET /shop/list, GET /shop/list/GPSHOP, POST /shop/purchase,
// POST /item/:id/use, POST /static/cache/item.
// Ports of the http_config.py handlers of the same paths.

import (
	"math"
	"math/rand"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func (s *Server) shopList(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	writeJSON(c, s.d.Roster.ShopList(st))
}

func (s *Server) gpShopList(c *gin.Context) {
	writeJSON(c, s.d.Roster.GPShopList())
}

func (s *Server) staticCacheItem(c *gin.Context) {
	var req struct {
		ItemIDs []any `json:"itemids"`
	}
	_ = c.ShouldBindJSON(&req)
	t := s.d.Roster.ShopTables()
	out := map[string]any{}
	for _, raw := range req.ItemIDs {
		id := game.Int(raw)
		_, inPacks := t.Packs[id]
		_, inItems := t.Items[id]
		_, inPkgs := t.Packages[id]
		_, inMonthly := t.Monthly[id]
		if id == game.EPItemID || inPacks || inItems || inPkgs || inMonthly || game.IsPiece(id) {
			if d := s.d.Roster.ItemDef(id); d != nil {
				out[itoa2(id)] = d
			}
		}
	}
	writeJSON(c, out)
}

func (s *Server) shopPurchase(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	t := s.d.Roster.ShopTables()
	slot := game.DefaultSlot(st)
	coach := game.CoachBySlot(st, slot)
	if coach == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_EXIST_PRODUCT"})
		return
	}
	shopid := game.Int(req["shopid"])
	// The GP branch applies only to real GP-priced entries. The
	// craft contract/box carry itemids above GPSHOPID and are
	// EP-priced shop items; Python routes them into this branch
	// and rejects them (NOT_EXIST_PRODUCT), which made the whole
	// craft system unbuyable -- fall through to EP for those.
	if shopid >= game.GPSHOPID {
		gitem := shopid - game.GPSHOPID
		gprice, exists := s.d.Roster.EffectiveGPPrice(gitem)
		if !exists {
			goto epPath
		}
		if game.StoredGP(st) < gprice {
			writeJSON(c, map[string]any{"result": false, "reason": "NOT_ENOUGH_GP"})
			return
		}
		q := game.Map(st["quest"])
		if q == nil {
			q = map[string]any{}
		}
		q["gp"] = game.StoredGP(st) - gprice
		st["quest"] = q
		game.InventoryAdd(coach, gitem, 1)
		_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
		res := s.d.Roster.CoachMeResources(st, slot, user)
		money := moneyOf(res)
		writeJSON(c, map[string]any{"result": true, "money": money, "ep": money["ep"],
			"resources": append(res, map[string]any{"uri": "/bag", "body": s.d.Roster.BagList(st)})})
		return
	}
epPath:
	itemid := game.Int(req["productid"])
	if itemid == 0 {
		itemid = game.Int(req["shopid"]) % game.MPShopID % game.STShopID
	}
	_, inPacks := t.Packs[itemid]
	_, inItems := t.Items[itemid]
	_, inPkgs := t.Packages[itemid]
	_, inMonthly := t.Monthly[itemid]
	if s.d.Roster.ShopHidden(itemid) {
		// Recalled from the shop by the admin (see DefaultShopHidden):
		// not listable, not purchasable; still grantable from the
		// admin panel.
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_EXIST_PRODUCT"})
		return
	}
	if !inPacks && !inItems && !inPkgs && !inMonthly {
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_EXIST_PRODUCT"})
		return
	}
	def := s.d.Roster.ItemDef(itemid)
	bunches := game.List(def["bunches"])
	bi := game.Int(req["bunch"])
	if bi < 0 {
		bi = 0
	}
	if bi > len(bunches)-1 {
		bi = len(bunches) - 1
	}
	b := game.Map(bunches[bi])
	price := game.Int(b["price"])
	ep := game.Int(coach["ep"])
	if ep < price {
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_ENOUGH_EP"})
		return
	}
	coach["ep"] = ep - price
	bunch := game.Int(b["bunch"])
	var contents [][]any
	switch {
	case inPkgs:
		contents = t.Packages[itemid].Contents
	case inMonthly:
		contents = t.Monthly[itemid].Now
	default:
		contents = [][]any{{float64(itemid), float64(1)}}
	}
	for _, cn := range contents {
		ci, cn2 := game.Int(cn[0]), game.Int(cn[1])*bunch
		if ci == game.EPItemID {
			coach["ep"] = game.Int(coach["ep"]) + cn2
		} else {
			game.InventoryAdd(coach, ci, cn2)
		}
	}
	if inMonthly {
		md := t.Monthly[itemid]
		monthly := game.Map(coach["monthly"])
		if monthly == nil {
			monthly = map[string]any{}
		}
		key := itoa2(itemid)
		m := game.Map(monthly[key])
		now := float64(time.Now().Unix())
		if m == nil {
			m = map[string]any{"since": now, "expire": now, "rewarded": 0}
		}
		exp := math.Max(float64(game.Int64(m["expire"])), now) + float64(md.Days*86400)
		m["expire"] = int64(exp)
		monthly[key] = m
		coach["monthly"] = monthly
	}
	if steps, chained := chainStepOf(t, itemid); chained {
		chain := game.Map(coach["chain"])
		if chain == nil {
			chain = map[string]any{}
		}
		first, k := steps[0], steps[1]
		key := itoa2(first)
		if game.Int(chain[key]) < k+1 {
			chain[key] = k + 1
		}
		coach["chain"] = chain
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	res := s.d.Roster.CoachMeResources(st, slot, user)
	money := moneyOf(res)
	out := map[string]any{"result": true, "money": money, "ep": money["ep"],
		"resources": append(res, map[string]any{"uri": "/bag", "body": s.d.Roster.BagList(st)})}
	if inMonthly {
		out["resources"] = append(out["resources"].([]any), map[string]any{"uri": "/monthlypack", "body": s.monthlyList(st)})
	}
	writeJSON(c, out)
}

func chainStepOf(t *game.ShopTables, itemid int) ([2]int, bool) {
	for first, steps := range t.Chained {
		for k, id := range steps {
			if id == itemid {
				return [2]int{first, k}, true
			}
		}
	}
	return [2]int{}, false
}

func moneyOf(res []any) map[string]any {
	if len(res) > 0 {
		body := game.Map(game.Map(res[0])["body"])
		if m := game.Map(body["money"]); m != nil {
			return m
		}
	}
	return map[string]any{}
}

// --- POST /item/:id/use ---------------------------------------------------

func (s *Server) itemUse(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	t := s.d.Roster.ShopTables()
	slot := game.DefaultSlot(st)
	coach := game.CoachBySlot(st, slot)
	team := game.TeamOf(st, slot)
	if coach == nil || team == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_ITEM"})
		return
	}
	itemID := c.Param("id")
	var target map[string]any
	for _, raw := range game.List(coach["items"]) {
		it := game.Map(raw)
		if it != nil && game.Str(it["_id"]) == itemID && game.Int(it["remains"]) > 0 {
			target = it
			break
		}
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	if target != nil {
		if _, isUse := t.Items[game.Int(target["itemid"])]; isUse {
			out, changed := s.useItem(st, slot, coach, team, target, req)
			if out["result"] == true && changed {
				_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
			}
			out["resources"] = s.d.Roster.CoachMeResources(st, slot, user)
			writeJSON(c, out)
			return
		}
	}
	if target == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_ITEM"})
		return
	}
	itemid := game.Int(target["itemid"])
	if _, isPack := t.Packs[itemid]; !isPack {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_ITEM"})
		return
	}
	if len(game.List(team["players"])) >= game.MaxPlayers {
		writeJSON(c, map[string]any{"result": false, "reason": "Exceed plyer limit"})
		return
	}
	pool := s.d.Roster.PackPool(itemid)
	if len(pool) == 0 {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_ITEM"})
		return
	}
	spid := drawPool(pool)
	g := game.PackGradeOf(t, itemid)
	p := s.d.Roster.AddPlayer(team, spid, g)
	target["remains"] = game.Int(target["remains"]) - 1
	if game.Int(target["remains"]) == 0 {
		removeCoachItem(coach, target)
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true,
		"player":    map[string]any{"_id": p["_id"], "spid": spid, "grade": g, "price": int(s.d.Roster.CardPrice(spid, g))},
		"resources": s.d.Roster.CoachMeResources(st, slot, user)})
}

func removeCoachItem(coach, target map[string]any) {
	var kept []any
	for _, raw := range game.List(coach["items"]) {
		if game.Map(raw)["_id"] != target["_id"] {
			kept = append(kept, raw)
		}
	}
	coach["items"] = kept
}

func drawPool(pool []game.PoolEntry) int {
	var total float64
	for _, e := range pool {
		total += e.Weight
	}
	x := rand.Float64() * total
	for _, e := range pool {
		x -= e.Weight
		if x <= 0 {
			return e.Spid
		}
	}
	return pool[len(pool)-1].Spid
}

// useItem ports hc.use_item for USE items. Returns (response, mutated).
func (s *Server) useItem(st game.State, slot int, coach, team, it map[string]any, req map[string]any) (map[string]any, bool) {
	t := s.d.Roster.ShopTables()
	itemid := game.Int(it["itemid"])
	raw := t.Items[itemid]
	out := map[string]any{"result": true}
	consume := 1
	switch raw.Type {
	case "LEAGUE_SIMULATION":
		return map[string]any{"result": false, "reason": "ITEM_USE_ERROR"}, false
	case "LOCKERROOM", "ITEM_USE_CRAFT":
		return out, false // permanent pass / craft material: no consume
	case "COACH_EXP_CAPSULE":
		count := game.Int(req["count"])
		if req["count"] == nil {
			count = 1
		}
		if count < 1 || count > game.Int(it["remains"]) {
			return map[string]any{"result": false, "reason": "ITEM_USE_ERROR"}, false
		}
		if game.Int(game.CoachLevelinfo(coach)["level"]) >= game.CoachMaxLevel {
			return map[string]any{"result": false, "reason": "ITEM_USE_ERROR"}, false
		}
		consume = 0
		for consume < count && game.Int(game.CoachLevelinfo(coach)["level"]) < game.CoachMaxLevel {
			game.GainExp(coach, game.Int(raw.Effect["exp"]), true)
			consume++
		}
		out["exp"] = game.Int(raw.Effect["exp"]) * consume
	case "PLAYER_EXP_CAPSULE":
		targetid := game.Str(req["targetid"])
		var player map[string]any
		for _, praw := range game.List(team["players"]) {
			p := game.Map(praw)
			if p != nil && targetid != "" && game.IID(p) == targetid {
				player = p
			}
		}
		if player == nil || game.Int(game.PlayerLevelinfo(player)["level"]) >= game.PlayerMaxLevel {
			return map[string]any{"result": false, "reason": "ITEM_USE_ERROR"}, false
		}
		before, after := game.GainExp(player, game.Int(raw.Effect["exp"]), false)
		out["targetid"] = game.IID(player)
		out["before"] = before
		out["after"] = after
	case "EP_LOTTERY":
		rng := game.List(raw.Effect["ep"])
		lo, hi := game.Int(rng[0]), game.Int(rng[1])
		ep := lo
		if lo != hi {
			ep = int(math.Round(triangular(float64(lo), float64(hi), float64(lo))/10000) * 10000)
			if ep < lo {
				ep = lo
			}
		}
		out["ep"] = ep
		coach["ep"] = game.Int(coach["ep"]) + ep
	case "ITEM_LOTTERY":
		if pr, hasPieces := raw.Effect["pieces"]; hasPieces {
			rng := game.List(pr)
			piece, n, okDraw := s.d.Roster.RandomCraftPiece(game.Int(rng[0]), game.Int(rng[1]))
			if !okDraw {
				return map[string]any{"result": false, "reason": "ITEM_USE_ERROR"}, false
			}
			game.InventoryAdd(coach, piece, n)
			out["item"] = map[string]any{"itemid": piece, "count": n,
				"staticdata": s.d.Roster.PieceDef(piece)}
			break
		}
		prizes := game.List(raw.Effect["prizes"])
		var tot float64
		weights := make([]float64, len(prizes))
		for i, p := range prizes {
			weights[i] = float64(game.Int(game.List(p)[0]))
			tot += weights[i]
		}
		x := rand.Float64() * tot
		var prize any
		for i, p := range prizes {
			x -= weights[i]
			if x <= 0 {
				prize = game.List(p)[1]
				break
			}
		}
		if prize == nil && len(prizes) > 0 {
			prize = game.List(prizes[len(prizes)-1])[1]
		}
		if arr, isArr := prize.([]any); isArr && len(arr) == 2 && game.Str(arr[0]) == "ep" {
			ep := game.Int(arr[1])
			out["ep"] = ep
			coach["ep"] = game.Int(coach["ep"]) + ep
		} else {
			pid := game.Int(prize)
			game.InventoryAdd(coach, pid, 1)
			out["item"] = map[string]any{"itemid": pid, "count": 1, "staticdata": s.d.Roster.ItemDef(pid)}
		}
	case "COACHNAME_CHANGE":
		name := game.Str(req["name"])
		if len([]rune(name)) < 2 || len([]rune(name)) > 12 {
			return map[string]any{"result": false, "reason": "COACH_CREATION_ERROR_COACH_TOO_SHORT"}, false
		}
		coach["name"] = name
	case "TEAM_CHANGE":
		teamid := game.Int(req["teamid"])
		gt := s.d.Roster.GenTeam(teamid, 0, false)
		if len(game.List(game.Map(gt["team"])["players"])) == 0 {
			return map[string]any{"result": false, "reason": "ITEM_USE_ERROR"}, false
		}
		coach["teamid"] = teamid
	default:
		return map[string]any{"result": false, "reason": "ITEM_USE_ERROR"}, false
	}
	it["remains"] = game.Int(it["remains"]) - consume
	if game.Int(it["remains"]) == 0 {
		removeCoachItem(coach, it)
	}
	return out, true
}

// triangular samples Python's random.triangular(low, high, mode).
func triangular(low, high, mode float64) float64 {
	u := rand.Float64()
	if high == low {
		return low
	}
	c := (mode - low) / (high - low)
	if u > c {
		return high - math.Sqrt((1-u)*(high-low)*(high-mode))
	}
	return low + math.Sqrt(u*(high-low)*(mode-low))
}
