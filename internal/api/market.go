package api

// Transfer market / enhance / trade endpoints, ported from hc's
// market block (ep_transfer_*, ep_enhance*, ep_trade*, ep_trade3_*).

import (
	"context"
	"encoding/json"
	"log"
	mrand "math/rand"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// --- Live player-to-player market (2026-11-15) --------------------
// The NPC market is gone: listings are real players escrowed from
// real squads, purchases fulfill the cheapest live listing, and
// prices move only through the daily reset (see game/marketlive.go).

func (s *Server) transferTop(c *gin.Context) {
	ctx := c.Request.Context()
	game.EnsureMarketTables(ctx, s.d.Store)
	agg, _ := game.LiveAggregates(ctx, s.d.Store)
	writeJSON(c, s.d.Roster.LiveTop(c.Param("position"), agg))
}

func (s *Server) transferSearch(c *gin.Context) {
	ctx := c.Request.Context()
	game.EnsureMarketTables(ctx, s.d.Store)
	spid, _ := strconv.Atoi(c.Param("spid"))
	agg, _ := game.LiveAggregates(ctx, s.d.Store)
	out := []any{}
	if byGrade := agg[spid]; byGrade != nil {
		grades := []int{}
		for g := range byGrade {
			grades = append(grades, g)
		}
		sortInts(grades)
		for _, g := range grades {
			a := byGrade[g]
			out = append(out, map[string]any{"spid": spid, "expandme": "player",
				"grade": g, "price": a.Price, "count": a.Count})
		}
	}
	writeJSON(c, out)
}

func sortInts(v []int) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// cloneListingPlayer prepares an escrowed player snapshot for its
// new owner: fresh identity, no second-team flag carried over.
func cloneListingPlayer(playerJSON string) map[string]any {
	var snap map[string]any
	if err := json.Unmarshal([]byte(playerJSON), &snap); err != nil || snap == nil {
		return nil
	}
	snap["_id"] = game.NewIID()
	delete(snap, "team2")
	delete(snap, "locked")
	return snap
}

func (s *Server) transferPurchase(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	game.EnsureMarketTables(ctx, s.d.Store)
	spid, _ := strconv.Atoi(c.Param("spid"))
	grade, _ := strconv.Atoi(c.Param("grade"))
	agreed, _ := strconv.ParseInt(c.Param("price"), 10, 64)
	slot := game.DefaultSlot(st)
	team := game.TeamOf(st, slot)
	if len(game.List(team["players"])) >= game.MaxPlayers {
		writeJSON(c, map[string]any{"result": false, "reason": "Exceed plyer limit"})
		return
	}
	tx, err := s.d.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_EXIST_OFFER"})
		return
	}
	var lid, playerJSON string
	var sellerUID, price int64
	err = tx.QueryRowContext(ctx,
		`SELECT id, seller_uid, price, player FROM market_listings
		 WHERE spid = ? AND grade = ? AND status = 'waiting' AND seller_uid <> ?
		 ORDER BY price ASC, created_at ASC LIMIT 1 FOR UPDATE`,
		spid, grade, user.ID).Scan(&lid, &sellerUID, &price, &playerJSON)
	if err != nil {
		_ = tx.Rollback()
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_EXIST_OFFER"})
		return
	}
	if agreed > 0 && price > agreed {
		// The cheapest listing moved past the price the buyer
		// confirmed; make them look again instead of overcharging.
		_ = tx.Rollback()
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_EXIST_OFFER"})
		return
	}
	me := s.d.Roster.CoachMe(st, slot, user)
	if game.Int64(game.Map(me["money"])["ep"]) < price {
		_ = tx.Rollback()
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_ENOUGH_EP"})
		return
	}
	snap := cloneListingPlayer(playerJSON)
	if snap == nil {
		_ = tx.Rollback()
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_EXIST_OFFER"})
		return
	}
	nowUnix := time.Now().Unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE market_listings SET status = 'sold', buyer_uid = ?, sold_at = ? WHERE id = ?`,
		user.ID, nowUnix, lid); err != nil {
		_ = tx.Rollback()
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_EXIST_OFFER"})
		return
	}
	var sellerIP string
	_ = tx.QueryRowContext(ctx, "SELECT last_ip FROM users WHERE id = ?", sellerUID).Scan(&sellerIP)
	_, _ = tx.ExecContext(ctx,
		`INSERT INTO market_sales (spid, grade, price, seller_uid, buyer_uid, seller_ip, buyer_ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		spid, grade, price, sellerUID, user.ID, sellerIP, clientIP(c))
	if err := tx.Commit(); err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_EXIST_OFFER"})
		return
	}
	game.AddEP(st, -price)
	team["players"] = append(game.List(team["players"]), snap)
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	s.flipSellerOfferSold(ctx, sellerUID, lid)
	writeJSON(c, map[string]any{"result": true,
		"resources": s.d.Roster.CoachMeResources(st, slot, user)})
}

// flipSellerOfferSold marks the seller's own offer entry sold so
// the existing collect flow pays them (price minus fee), and pushes
// the refreshed offer list + notifications to a live seller client.
func (s *Server) flipSellerOfferSold(ctx context.Context, sellerUID int64, listingID string) {
	sst, err := game.LoadState(ctx, s.d.Store, sellerUID)
	if err != nil {
		log.Printf("market: seller state load failed (uid %d): %v", sellerUID, err)
		return
	}
	changed := false
	for _, ov := range game.List(sst["offers"]) {
		if o := game.Map(ov); o != nil && game.Str(o["_id"]) == listingID &&
			game.Str(o["status"]) == "waiting" {
			o["status"] = "sold"
			o["soldat"] = game.W3C(float64(time.Now().Unix()))
			changed = true
		}
	}
	if !changed {
		return
	}
	_ = game.SaveState(ctx, s.d.Store, sellerUID, sst)
	if cid, _ := myCoachID(sst); cid != "" {
		game.MessengerPush("/transfer/my/offer/list", game.OfferList(sst), cid)
		s.pushNotes(ctx, cid)
	}
}

func (s *Server) transferPrice(c *gin.Context) {
	spid, _ := strconv.Atoi(c.Param("spid"))
	grade, _ := strconv.Atoi(c.Param("grade"))
	p := int64(s.d.Roster.CardPrice(spid, grade))
	writeJSON(c, map[string]any{"lower": p * 8 / 10, "upper": p, "default": p,
		"lowest": p, "registrationFee": 0})
}

func (s *Server) offerResources(st game.State, user *game.User, slot int) []any {
	res := []any{map[string]any{"uri": "/transfer/my/offer/list", "body": game.OfferList(st)}}
	return append(res, s.d.Roster.CoachMeResources(st, slot, user)...)
}

func (s *Server) transferOffer(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	slot := game.DefaultSlot(st)
	team := game.TeamOf(st, slot)
	pid := game.Str(req["playerobjectid"])
	if req["playerobjectid"] != nil && pid == "" {
		pid = anyToIDStr(req["playerobjectid"])
	}
	var p map[string]any
	for _, pv := range game.List(team["players"]) {
		if cand := game.Map(pv); cand != nil && game.IID(cand) == pid {
			p = cand
			break
		}
	}
	price := game.Int(req["price"])
	if p != nil && playerLockedNow(p) {
		writeJSON(c, map[string]any{"result": false, "reason": "PLAYER_LOCKED"})
		return
	}
	if p == nil || game.IsStarter(team, p) {
		writeJSON(c, map[string]any{"result": false, "reason": "TRANSFERMARKET_SELECT_SELLABLE_PLAYER"})
		return
	}
	// Stabilized band: a listing must sit inside [80%, 100%] of the
	// reference price (the same band /transfer/price advertises).
	// Nobody can dump for wash trades or corner above the reference.
	ref := s.d.Roster.CardPrice(game.Int(p["spid"]), game.GradeOf(p))
	if price <= 0 || float64(price) > ref || float64(price) < ref*0.8 {
		writeJSON(c, map[string]any{"result": false, "reason": "TRANSFERMARKET_LOWERLIMIT"})
		return
	}
	ctx := c.Request.Context()
	game.EnsureMarketTables(ctx, s.d.Store)
	snapJSON, _ := json.Marshal(p)
	lid := game.NewIID()
	s.d.Roster.RemovePlayer(team, p)
	now := game.W3C(float64(time.Now().Unix()))
	offers := append(game.List(st["offers"]), map[string]any{
		"_id": lid, "status": "waiting", "issuedat": now,
		"spid": p["spid"], "expandme": "player", "grade": game.GradeOf(p), "price": price,
		"playerobjectid": pid, "playerobj": p,
		"completefee": map[string]any{"base": int(float64(price) * game.SaleFeeNow())}})
	st["offers"] = offers
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	if _, err := s.d.Store.DB.ExecContext(ctx,
		`INSERT INTO market_listings (id, seller_uid, spid, grade, price, player, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'waiting', ?)`,
		lid, user.ID, game.Int(p["spid"]), game.GradeOf(p), price, string(snapJSON), time.Now().Unix()); err != nil {
		// The offer entry survives in the seller's list and can be
		// cancelled to recover the player; log loudly.
		log.Printf("market: listing insert failed (offer %s, uid %d): %v", lid, user.ID, err)
	}
	writeJSON(c, map[string]any{"result": true, "resources": s.offerResources(st, user, slot)})
}

// findOffer locates one of the caller's offer entries by id.
func findOffer(st game.State, offerid string) map[string]any {
	for _, ov := range game.List(st["offers"]) {
		if o := game.Map(ov); o != nil && game.Str(o["_id"]) == offerid {
			return o
		}
	}
	return nil
}

// dropOffer removes one offer entry from the caller's state.
func dropOffer(st game.State, offerid string) {
	kept := []any{}
	for _, ov := range game.List(st["offers"]) {
		if o := game.Map(ov); o == nil || game.Str(o["_id"]) != offerid {
			kept = append(kept, ov)
		}
	}
	st["offers"] = kept
}

// offerCancel handles DELETE /transfer/my/offer/:status/:offerid:
// cancelling a waiting or expired listing returns the escrowed
// player to the seller's squad.
func (s *Server) offerCancel(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	game.EnsureMarketTables(ctx, s.d.Store)
	offerid := c.Param("offerid")
	o := findOffer(st, offerid)
	if o == nil || (game.Str(o["status"]) != "waiting" && game.Str(o["status"]) != "expired") {
		writeJSON(c, map[string]any{"result": false, "reason": "OFFER_NOT_EXIST_ERROR"})
		return
	}
	snap := game.Map(o["playerobj"])
	if snap == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "OFFER_NOT_EXIST_ERROR"})
		return
	}
	slot := game.DefaultSlot(st)
	team := game.TeamOf(st, slot)
	ret := cloneListingPlayer(mustJSON(snap))
	if ret == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "OFFER_NOT_EXIST_ERROR"})
		return
	}
	// Keep the player's original identity on a cancel return: it
	// never changed hands.
	if idv := snap["_id"]; idv != nil {
		ret["_id"] = idv
	}
	if snap["team2"] == true {
		ret["team2"] = true
	}
	team["players"] = append(game.List(team["players"]), ret)
	dropOffer(st, offerid)
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	_, _ = s.d.Store.DB.ExecContext(ctx,
		`UPDATE market_listings SET status = 'cancelled' WHERE id = ? AND seller_uid = ? AND status IN ('waiting', 'expired')`,
		offerid, user.ID)
	writeJSON(c, map[string]any{"result": true, "resources": s.offerResources(st, user, slot)})
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// offerRecreate handles POST /transfer/offer/from/:offerid: an
// expired listing's escrowed player goes back on sale at a new
// price inside the current reference band.
func (s *Server) offerRecreate(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	ctx := c.Request.Context()
	game.EnsureMarketTables(ctx, s.d.Store)
	offerid := c.Param("offerid")
	o := findOffer(st, offerid)
	if o == nil || game.Str(o["status"]) != "expired" {
		writeJSON(c, map[string]any{"result": false, "reason": "OFFER_NOT_EXIST_ERROR"})
		return
	}
	snap := game.Map(o["playerobj"])
	price := game.Int(req["price"])
	if snap == nil || price <= 0 {
		writeJSON(c, map[string]any{"result": false, "reason": "OFFER_NOT_EXIST_ERROR"})
		return
	}
	ref := s.d.Roster.CardPrice(game.Int(o["spid"]), game.Int(o["grade"]))
	if float64(price) > ref || float64(price) < ref*0.8 {
		writeJSON(c, map[string]any{"result": false, "reason": "TRANSFERMARKET_LOWERLIMIT"})
		return
	}
	res, err := s.d.Store.DB.ExecContext(ctx,
		`UPDATE market_listings SET status = 'waiting', price = ?, created_at = ?, sold_at = NULL, buyer_uid = NULL
		 WHERE id = ? AND seller_uid = ? AND status = 'expired'`,
		price, time.Now().Unix(), offerid, user.ID)
	if err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "OFFER_NOT_EXIST_ERROR"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSON(c, map[string]any{"result": false, "reason": "OFFER_NOT_EXIST_ERROR"})
		return
	}
	o["status"] = "waiting"
	o["price"] = price
	o["issuedat"] = game.W3C(float64(time.Now().Unix()))
	o["completefee"] = map[string]any{"base": int(float64(price) * game.SaleFeeNow())}
	slot := game.DefaultSlot(st)
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true, "resources": s.offerResources(st, user, slot)})
}

func (s *Server) offerList(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	writeJSON(c, game.OfferList(st))
}

func (s *Server) offerCollect(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	offerid := c.Param("offerid")
	done := []map[string]any{}
	kept := []any{}
	for _, ov := range game.List(st["offers"]) {
		o := game.Map(ov)
		if o != nil && game.Str(o["status"]) == "sold" && (offerid == "all" || game.Str(o["_id"]) == offerid) {
			done = append(done, o)
		} else {
			kept = append(kept, ov)
		}
	}
	if len(done) == 0 {
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_EXIST_OFFER"})
		return
	}
	ep := 0
	for _, o := range done {
		ep += game.SaleNet(st, o)
	}
	game.AddEP(st, int64(ep))
	st["offers"] = kept
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	slot := game.DefaultSlot(st)
	writeJSON(c, map[string]any{"result": true, "ep": ep,
		"resources": s.offerResources(st, user, slot)})
}

func anyToIDStr(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case string:
		return n
	}
	return ""
}

// --- Enhancement ---------------------------------------------------------

func (s *Server) enhancePreview(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	team := game.TeamOf(st, game.DefaultSlot(st))
	pa, pb := game.EnhancePair(team, c.Param("pid"), c.Param("bid"))
	if pa == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_PARAM"})
		return
	}
	_ = pb
	g := game.GradeOf(pa) + 1
	levelinfo := pa["levelinfo"]
	if levelinfo == nil {
		levelinfo = map[string]any{"level": 1, "exp": 0}
	}
	writeJSON(c, map[string]any{"result": true, "grade": g, "levelinfo": levelinfo,
		"price": int64(s.d.Roster.CardPrice(game.Int(pa["spid"]), g)),
		"enhancepay": s.d.Roster.EnhanceCost(pa),
		"probability": int(s.d.Roster.EnhanceRate(pa, pb)*100 + 0.5)})
}

func (s *Server) enhanceDo(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	slot := game.DefaultSlot(st)
	team := game.TeamOf(st, slot)
	pa, pb := game.EnhancePair(team, c.Param("pid"), c.Param("bid"))
	if pa == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_PARAM"})
		return
	}
	cost := s.d.Roster.EnhanceCost(pa)
	me := s.d.Roster.CoachMe(st, slot, user)
	if game.Int64(game.Map(me["money"])["ep"]) < int64(cost) {
		writeJSON(c, map[string]any{"result": false, "reason": "Invalid target ep price"})
		return
	}
	game.AddEP(st, -int64(cost))
	before := game.GradeOf(pa)
	success := mrand.Float64() < s.d.Roster.EnhanceRate(pa, pb)
	after := before + 1
	if !success {
		after = game.FailedEnhanceGrade(pa, pb)
	}
	s.d.Roster.MergeInto(team, pa, pb)
	pa["grade"] = after
	if success {
		career := game.Map(st["career"])
		if career == nil {
			career = map[string]any{}
			st["career"] = career
		}
		ups := game.Map(career["upgrades"])
		if ups == nil {
			ups = map[string]any{}
			career["upgrades"] = ups
		}
		key := strconv.Itoa(after)
		ups[key] = game.Int(ups[key]) + 1
	}
	// Grade restore (enhance 2.5 popup): a failed attempt leaves a
	// pending restore the player can buy back with EP via
	// PUT /player/enhance2.5/restore; a success clears any older
	// pending one. The popup reads _result.usepoint.{currency,price},
	// _result.grantes and _result.resultgrade unguarded — missing
	// keys throw #1010 (telemetry 886).
	var restorePrice int64
	if success {
		delete(st, "enhance_restore")
	} else {
		restorePrice = int64(s.d.Roster.CardPrice(game.Int(pa["spid"]), before)) / 10
		if restorePrice < 10000 {
			restorePrice = 10000
		}
		st["enhance_restore"] = map[string]any{"pid": game.IID(pa),
			"spid": game.Int(pa["spid"]), "before": before, "after": after,
			"price": restorePrice}
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	out := map[string]any{"result": true, "success": success, "grade": after,
		"rgrade": after, "resultgrade": after,
		"rprice": int64(s.d.Roster.CardPrice(game.Int(pa["spid"]), after)),
		"attribPlus": game.GradeBoostAt(after) - game.GradeBoostAt(before),
		"resources": s.d.Roster.CoachMeResources(st, slot, user)}
	if !success {
		out["usepoint"] = map[string]any{"currency": "EP", "price": restorePrice}
		out["grantes"] = 0
	}
	writeJSON(c, out)
}

// enhanceRestore buys back the grade lost in the last failed enhance
// (PUT /player/enhance2.5/restore, empty body). The pending record
// comes from enhanceDo; it is consumed here, replaced by the next
// enhance, or dropped by enhanceRestoreCancel when the popup closes
// without restoring.
func (s *Server) enhanceRestore(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	pend := game.Map(st["enhance_restore"])
	if pend == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NO_PENDING_RESTORE"})
		return
	}
	slot := game.DefaultSlot(st)
	team := game.TeamOf(st, slot)
	var p map[string]any
	for _, pv := range game.List(team["players"]) {
		if q := game.Map(pv); q != nil && game.IID(q) == game.Str(pend["pid"]) {
			p = q
		}
	}
	if p == nil || game.GradeOf(p) != game.Int(pend["after"]) {
		delete(st, "enhance_restore")
		_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
		writeJSON(c, map[string]any{"result": false, "reason": "RESTORE_EXPIRED"})
		return
	}
	price := int64(game.Int(pend["price"]))
	me := s.d.Roster.CoachMe(st, slot, user)
	if game.Int64(game.Map(me["money"])["ep"]) < price {
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_ENOUGH_EP"})
		return
	}
	game.AddEP(st, -price)
	before := game.Int(pend["before"])
	p["grade"] = before
	delete(st, "enhance_restore")
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true, "success": true,
		"grade": before, "rgrade": before, "resultgrade": before,
		"rprice": int64(s.d.Roster.CardPrice(game.Int(p["spid"]), before)),
		"grantes": 0,
		"resources": s.d.Roster.CoachMeResources(st, slot, user)})
}

// enhanceRestoreCancel drops the pending restore (the popup calls it
// on close when the failed card was not restored).
func (s *Server) enhanceRestoreCancel(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	if st["enhance_restore"] != nil {
		delete(st, "enhance_restore")
		_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	}
	writeJSON(c, map[string]any{"result": true})
}

// --- Trade v1 + shared contract ------------------------------------------

func tradePlayers(team map[string]any, ids []string) []map[string]any {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	out := []map[string]any{}
	for _, pv := range game.List(team["players"]) {
		if p := game.Map(pv); p != nil && set[game.IID(p)] {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) tradeProposal(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	team := game.TeamOf(st, game.DefaultSlot(st))
	ids := []string{}
	for _, v := range game.List(req["players"]) {
		ids = append(ids, anyToIDStr(v))
	}
	give := tradePlayers(team, ids)
	for _, p := range give {
		if playerLockedNow(p) {
			writeJSON(c, map[string]any{"result": false, "reason": "PLAYER_LOCKED"})
			return
		}
	}
	valid := len(give) >= 2 && len(give) <= 10 && len(give) == len(ids)
	if valid {
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				valid = false
			}
			seen[id] = true
		}
		for _, p := range give {
			if game.InSquad(team, p) {
				valid = false
			}
		}
	}
	if !valid {
		writeJSON(c, map[string]any{"result": false, "reason": "invalid playerid"})
		return
	}
	props := s.d.Roster.TradeProps(st, give)
	idList := []any{}
	for _, id := range ids {
		idList = append(idList, id)
	}
	st["trade"] = map[string]any{"players": idList, "props": props}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	_ = user
	writeJSON(c, map[string]any{"result": true, "props": props})
}

func (s *Server) tradeContract(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	slot := game.DefaultSlot(st)
	team := game.TeamOf(st, slot)
	tr := game.Map(st["trade"])
	props := game.List(tr["props"])
	ids := []string{}
	for _, v := range game.List(tr["players"]) {
		ids = append(ids, anyToIDStr(v))
	}
	sel := -1
	if req["select"] != nil {
		sel = game.Int(req["select"])
	} else if req["selectnum"] != nil {
		sel = game.Int(req["selectnum"])
	}
	give := tradePlayers(team, ids)
	if sel < 0 || sel >= len(props) || len(give) != len(ids) {
		writeJSON(c, map[string]any{"result": false, "reason": "invalid playerid"})
		return
	}
	for _, p := range give {
		if playerLockedNow(p) {
			writeJSON(c, map[string]any{"result": false, "reason": "PLAYER_LOCKED"})
			return
		}
	}
	for _, p := range give {
		s.d.Roster.RemovePlayer(team, p)
	}
	offer := game.Map(props[sel])
	if offer["spid"] != nil {
		s.d.Roster.AddPlayer(team, game.Int(offer["spid"]), game.Int(offer["grade"]))
	}
	// Anti-mint backstop (option A, 2026-11-16): trade EP shares
	// the daily match-EP cap bucket instead of bypassing it.
	if epAmt := int64(game.Int(offer["ep"])); epAmt > 0 {
		ctx := c.Request.Context()
		if granted, capped := game.ClampMatchDailyEP(ctx, s.d.Store, user.ID, epAmt); capped {
			game.Audit(ctx, s.d.Store, "system", "trade.ep_capped", "",
				map[string]any{"uid": user.ID, "wanted": epAmt, "granted": granted,
					"cap": game.CfgI("match_daily_ep_cap", 0), "kind": "trade"})
			epAmt = granted
		}
		game.AddEP(st, epAmt)
	}
	delete(st, "trade")
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true,
		"resources": s.d.Roster.CoachMeResources(st, slot, user)})
}

// --- Trade3 (Scout) --------------------------------------------------------

func (s *Server) trade3Proposal(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	slot := game.DefaultSlot(st)
	team := game.TeamOf(st, slot)
	var scout *game.ScoutDef
	for i := range game.Scouts {
		if game.Scouts[i].Key == game.Str(req["id"]) {
			scout = &game.Scouts[i]
			break
		}
	}
	// PC/VIP scout gate (owner, 2026-11-16): the igr scout needs an
	// active PC entitlement, the vip scout an active VIP one. The
	// client greys them out via CoachScouters' lock flag; this check
	// is the enforcement behind it.
	if scout != nil && ((scout.Key == "igr" && !game.PCActive(st)) ||
		(scout.Key == "vip" && !game.VIPActive(st))) {
		writeJSON(c, map[string]any{"result": false, "reason": "SCOUT_LOCKED"})
		return
	}
	sent := map[string]map[string]any{}
	for _, pv := range game.List(req["playerids"]) {
		if p := game.Map(pv); p != nil {
			sent[anyToIDStr(p["id"])] = p
		}
	}
	ids := []string{}
	for id := range sent {
		ids = append(ids, id)
	}
	give := tradePlayers(team, ids)
	for _, p := range give {
		if playerLockedNow(p) {
			writeJSON(c, map[string]any{"result": false, "reason": "PLAYER_LOCKED"})
			return
		}
	}
	valid := scout != nil && len(give) >= 2 && len(give) <= 10 && len(give) == len(sent)
	if valid {
		for _, p := range give {
			if game.InSquad(team, p) || p["team2"] != true {
				valid = false
			}
		}
	}
	if !valid {
		writeJSON(c, map[string]any{"result": false, "reason": "invalid playerid"})
		return
	}
	for _, p := range give {
		if game.Int(sent[game.IID(p)]["price"]) != int(s.d.Roster.CardPrice(game.Int(p["spid"]), game.GradeOf(p))) {
			writeJSON(c, map[string]any{"result": false, "reason": "TRADE3_NOTICE_PLAYERPRICE03"})
			return
		}
	}
	props := s.d.Roster.Trade3Props(st, give, *scout)
	idList := []any{}
	for _, p := range give {
		idList = append(idList, game.IID(p))
	}
	st["trade"] = map[string]any{"players": idList, "props": props}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true, "props": props,
		"resources": s.d.Roster.CoachMeResources(st, slot, user)})
}

func (s *Server) trade3Info(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	writeJSON(c, map[string]any{"result": true,
		"resources": s.d.Roster.CoachMeResources(st, game.DefaultSlot(st), user)})
}

func (s *Server) trade3Static(c *gin.Context) {
	writeJSON(c, game.Trade3Static())
}

func (s *Server) playerMove(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	slot := game.DefaultSlot(st)
	team := game.TeamOf(st, slot)
	pid := c.Param("pid")
	where := c.Param("where")
	var p map[string]any
	for _, pv := range game.List(team["players"]) {
		if cand := game.Map(pv); cand != nil && game.IID(cand) == pid {
			p = cand
			break
		}
	}
	if p == nil || (where == "secondteam" && game.InSquad(team, p)) {
		writeJSON(c, map[string]any{"result": false, "reason": "TRANSFERMARKET_SELL_ERROR_FIRSTTEAM"})
		return
	}
	if where == "firstteam" && p["team2"] == true && game.FirstTeamCount(team) >= game.FirstTeamMax {
		writeJSON(c, map[string]any{"result": false, "reason": "FIRSTTEAM_REGISTRATION_FAIL_POPUP_TITLE"})
		return
	}
	if where == "secondteam" {
		p["team2"] = true
	} else {
		delete(p, "team2")
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true,
		"resources": s.d.Roster.CoachMeResources(st, slot, user)})
}
