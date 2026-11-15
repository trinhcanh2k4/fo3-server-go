package api

// Admin API part 2: shop overrides, settings, ladder, league,
// cups, clubs, friends, presence/notice, ops, telemetry, audit.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// --- shop -------------------------------------------------------

func (s *Server) adminShop(c *gin.Context) {
	ctx := c.Request.Context()
	t := s.d.Roster.ShopTables()
	overrides := game.ShopPriceOverrides(ctx, s.d.Store)
	gpOverrides := game.GPPriceOverrides(ctx, s.d.Store)
	kind := c.Param("kind")
	out := []any{}
	add := func(itemid int, category string) {
		def := s.d.Roster.ItemDef(itemid)
		price := int64(0)
		if b := game.List(def["bunches"]); len(b) > 0 {
			price = int64(game.Int(game.Map(b[0])["price"]))
		}
		entry := map[string]any{"itemid": itemid, "name": def["name"], "category": category,
			"price": price, "hidden": s.d.Roster.ShopHidden(itemid)}
		if category == "PLAYERPACK" {
			entry["season_label"] = s.d.Roster.PackSeasonLabel(itemid)
		}
		if ov, ok := overrides[itemid]; ok {
			entry["override"] = ov
		}
		out = append(out, entry)
	}
	switch kind {
	case "packs":
		for id := range t.Packs {
			add(id, "PLAYERPACK")
		}
	case "items":
		for id := range t.Items {
			add(id, "USE")
		}
	case "packages":
		for id := range t.Packages {
			add(id, "PACKAGE")
		}
	case "gp":
		for id, price := range t.GPPrices {
			entry := map[string]any{"itemid": id, "price": price, "category": "GPSHOP"}
			if def := s.d.Roster.ItemDef(id); def != nil {
				entry["name"] = def["name"]
			}
			if ov, ok := gpOverrides[id]; ok {
				entry["override"] = ov
			}
			out = append(out, entry)
		}
	default:
		writeJSON(c, map[string]any{"result": false, "reason": "UNKNOWN_KIND"})
		return
	}
	writeJSON(c, map[string]any{"result": true, "entries": out})
}

func (s *Server) adminShopPrice(c *gin.Context) {
	ctx := c.Request.Context()
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	itemid := game.Int(req["itemid"])
	setting := "shop_price_overrides"
	if c.Param("kind") == "gp" {
		setting = "gp_price_overrides"
	}
	current := map[string]int64{}
	if v, ok := game.SettingGet(ctx, s.d.Store, setting); ok {
		_ = json.Unmarshal([]byte(v), &current)
	}
	key := strconv.Itoa(itemid)
	if req["price"] == nil {
		delete(current, key)
	} else {
		current[key] = game.Int64(req["price"])
	}
	b, _ := json.Marshal(current)
	_ = game.SettingPut(ctx, s.d.Store, setting, string(b))
	s.audit(c, "shop.price_"+c.Param("kind"), key, map[string]any{"price": req["price"]})
	writeJSON(c, map[string]any{"result": true})
}

// adminShopHide toggles an item's recalled-from-shop state. The
// effective list starts from the current set (settings doc or
// the built-in default) and is written back as a settings doc.
func (s *Server) adminShopHide(c *gin.Context) {
	ctx := c.Request.Context()
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	itemid := game.Int(req["itemid"])
	hide, _ := req["hidden"].(bool)
	ids := game.ShopHiddenIDs(ctx, s.d.Store)
	out := []int{}
	found := false
	for _, id := range ids {
		if id == itemid {
			found = true
			if hide {
				out = append(out, id)
			}
			continue
		}
		out = append(out, id)
	}
	if hide && !found {
		out = append(out, itemid)
	}
	b, _ := json.Marshal(map[string]any{"itemids": out})
	_ = game.SettingPut(ctx, s.d.Store, "shop_hidden", string(b))
	s.d.Roster.SetShopHidden(out)
	s.audit(c, "shop.hide", strconv.Itoa(itemid), map[string]any{"hidden": hide})
	writeJSON(c, map[string]any{"result": true})
}

// --- settings ---------------------------------------------------

func (s *Server) adminSettings(c *gin.Context) {
	ctx := c.Request.Context()
	if c.Request.Method == "GET" {
		all, err := game.SettingAll(ctx, s.d.Store)
		if err != nil {
			writeJSON(c, map[string]any{"result": false, "reason": "DB_ERROR"})
			return
		}
		writeJSON(c, map[string]any{"result": true, "settings": all})
		return
	}
	name := c.Param("name")
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	b, _ := json.Marshal(req["value"])
	_ = game.SettingPut(ctx, s.d.Store, name, string(b))
	s.audit(c, "settings.set", name, req["value"])
	writeJSON(c, map[string]any{"result": true})
}

// --- ladder / league / cups -------------------------------------

func (s *Server) adminLadder(c *gin.Context) {
	ctx := c.Request.Context()
	users, _ := game.AllUsers(ctx, s.d.Store)
	out := []any{}
	for _, u := range users {
		st, _ := game.LoadState(ctx, s.d.Store, u.ID)
		if st == nil {
			continue
		}
		rk := game.RankedState(st, "ranked")
		coach := game.CoachBySlot(st, game.DefaultSlot(st))
		name := ""
		if coach != nil {
			name = game.Str(coach["name"])
		}
		out = append(out, map[string]any{"uid": u.ID, "username": u.Username, "coach": name,
			"elo": game.Int(rk["elo"]), "wins": game.Int(rk["wins"]),
			"draws": game.Int(rk["draws"]), "losses": game.Int(rk["losses"])})
	}
	writeJSON(c, map[string]any{"result": true, "rows": out})
}

func (s *Server) adminLadderReset(c *gin.Context) {
	ctx := c.Request.Context()
	users, _ := game.AllUsers(ctx, s.d.Store)
	for _, u := range users {
		st, err := game.LoadState(ctx, s.d.Store, u.ID)
		if err != nil {
			continue
		}
		st["ranked"] = game.FreshRankedState()
		st["ranked_mgr"] = game.FreshRankedState()
		_ = game.SaveState(ctx, s.d.Store, u.ID, st)
		_, _ = s.d.Store.DB.ExecContext(ctx, "UPDATE users SET elo = 1000 WHERE id = ?", u.ID)
	}
	s.audit(c, "ladder.reset_season", "all", nil)
	writeJSON(c, map[string]any{"result": true})
}

func (s *Server) adminLeague(c *gin.Context) {
	st, user, _, ok := s.loadUserState(c)
	if !ok {
		return
	}
	if c.Request.Method == "POST" {
		delete(st, "league2")
		delete(st, "league2_archives")
		_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
		s.audit(c, "league.reset", strconv.FormatInt(user.ID, 10), nil)
		writeJSON(c, map[string]any{"result": true})
		return
	}
	l2 := game.Map(st["league2"])
	writeJSON(c, map[string]any{"result": true, "league2": map[string]any{
		"leagueid": l2["leagueid"], "laststatus": l2["laststatus"],
		"nextround": l2["nextround"], "difficulty": l2["difficulty"],
		"exists": st["league2"] != nil,
	}})
}

func (s *Server) adminCups(c *gin.Context) {
	st, user, _, ok := s.loadUserState(c)
	if !ok {
		return
	}
	if c.Request.Method == "POST" {
		var req map[string]any
		_ = c.ShouldBindJSON(&req)
		cupid := game.Str(req["cupid"])
		cups := game.Map(st["cups"])
		if cups != nil {
			delete(cups, cupid)
		}
		_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
		s.audit(c, "cup.force_leave", strconv.FormatInt(user.ID, 10), map[string]any{"cupid": cupid})
		writeJSON(c, map[string]any{"result": true})
		return
	}
	out := []any{}
	for id, cv := range game.Map(st["cups"]) {
		if cup := game.Map(cv); cup != nil {
			out = append(out, map[string]any{"cupid": id, "metaid": cup["metaid"],
				"state": cup["state"], "phase": cup["phase"]})
		}
	}
	writeJSON(c, map[string]any{"result": true, "cups": out})
}

// --- clubs / friends ----------------------------------------------

func (s *Server) adminClubs(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := s.d.Store.DB.QueryContext(ctx,
		"SELECT clubid, name, master_id, (SELECT COUNT(*) FROM club_members m WHERE m.clubid = clubs.clubid) FROM clubs ORDER BY name")
	if err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "DB_ERROR"})
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var clubid, name, master string
		var members int
		if err := rows.Scan(&clubid, &name, &master, &members); err == nil {
			out = append(out, map[string]any{"clubid": clubid, "name": name,
				"master_id": master, "members": members})
		}
	}
	writeJSON(c, map[string]any{"result": true, "clubs": out})
}

func (s *Server) adminClubDissolve(c *gin.Context) {
	ctx := c.Request.Context()
	clubid := c.Param("clubid")
	_, _ = s.d.Store.DB.ExecContext(ctx, "DELETE FROM club_posts WHERE clubid = ?", clubid)
	_, _ = s.d.Store.DB.ExecContext(ctx, "DELETE FROM club_requests WHERE clubid = ?", clubid)
	_, _ = s.d.Store.DB.ExecContext(ctx, "DELETE FROM club_members WHERE clubid = ?", clubid)
	_, _ = s.d.Store.DB.ExecContext(ctx, "DELETE FROM clubs WHERE clubid = ?", clubid)
	s.audit(c, "club.dissolve", clubid, nil)
	writeJSON(c, map[string]any{"result": true})
}

func (s *Server) adminFriends(c *gin.Context) {
	st, user, slot, ok := s.loadUserState(c)
	if !ok {
		return
	}
	_ = slot
	coachID, _ := myCoachID(st)
	rows, _ := game.FriendRows(c.Request.Context(), s.d.Store, coachID)
	out := []any{}
	for _, r := range rows {
		out = append(out, map[string]any{"friend_coach_id": r.FriendID,
			"status": r.Status, "group": r.GroupName})
	}
	_ = user
	writeJSON(c, map[string]any{"result": true, "friends": out})
}

// --- presence / notice / ops --------------------------------------

func (s *Server) adminOnline(c *gin.Context) {
	ctx := c.Request.Context()
	keys, _ := s.d.Store.Redis.Keys(ctx, "msgonline:*").Result()
	online := []string{}
	hb := int64(0)
	for _, k := range keys {
		if k == "msgonline:heartbeat" {
			v, _ := s.d.Store.Redis.Get(ctx, k).Int64()
			hb = v
			continue
		}
		online = append(online, strings.TrimPrefix(k, "msgonline:"))
	}
	names := map[string]string{}
	if len(online) > 0 {
		if idx := game.CoachIndex(ctx, s.d.Store, s.d.Roster); idx != nil {
			for _, cid := range online {
				if m := idx[cid]; m != nil {
					names[cid] = m.Name
				}
			}
		}
	}
	writeJSON(c, map[string]any{"result": true, "online": online, "names": names,
		"heartbeat_age_sec": time.Now().Unix() - hb})
}

func (s *Server) adminNotice(c *gin.Context) {
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	text := game.Str(req["text"])
	if text == "" {
		writeJSON(c, map[string]any{"result": false, "reason": "EMPTY_TEXT"})
		return
	}
	vo := map[string]any{"type": "chat", "coachname": "Hệ thống", "body": text, "message": text}
	coachid := game.Str(req["coachid"])
	game.MessengerPush("/chat/my/channel/messages/latest", vo, coachid)
	s.audit(c, "messenger.notice", coachid, map[string]any{"text": text})
	writeJSON(c, map[string]any{"result": true})
}

func (s *Server) adminOps(c *gin.Context) {
	ctx := c.Request.Context()
	ops := map[string]any{"time": time.Now().Unix()}
	ops["mariadb"] = s.d.Store.DB.PingContext(ctx) == nil
	ops["redis"] = s.d.Store.Redis.Ping(ctx).Err() == nil
	var users int
	_ = s.d.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&users)
	ops["users"] = users
	keys, _ := s.d.Store.Redis.Keys(ctx, "msgonline:*").Result()
	online := 0
	for _, k := range keys {
		if k != "msgonline:heartbeat" {
			online++
		}
	}
	ops["online"] = online
	writeJSON(c, map[string]any{"result": true, "ops": ops})
}

func (s *Server) adminTelemetry(c *gin.Context) {
	ctx := c.Request.Context()
	hours, _ := strconv.Atoi(c.DefaultQuery("hours", "24"))
	if hours <= 0 || hours > 24*30 {
		hours = 24
	}
	rows, err := s.d.Store.DB.QueryContext(ctx,
		`SELECT id, IFNULL(user_id,0), IFNULL(uri,''), IFNULL(category,''), IFNULL(message,''), created_at
		 FROM telemetry_errors WHERE created_at > NOW() - INTERVAL ? HOUR ORDER BY id DESC LIMIT 200`, hours)
	if err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "DB_ERROR"})
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, uid int64
		var uri, cat, msg string
		var at time.Time
		if err := rows.Scan(&id, &uid, &uri, &cat, &msg, &at); err == nil {
			out = append(out, map[string]any{"id": id, "user_id": uid, "uri": uri,
				"category": cat, "message": msg, "at": at.Unix()})
		}
	}
	writeJSON(c, map[string]any{"result": true, "errors": out})
}

func (s *Server) adminAudit(c *gin.Context) {
	ctx := c.Request.Context()
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.d.Store.DB.QueryContext(ctx,
		"SELECT id, admin, action, IFNULL(target,''), IFNULL(detail,''), created_at FROM admin_audit ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "DB_ERROR"})
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id int64
		var admin, action, target, detail string
		var at time.Time
		if err := rows.Scan(&id, &admin, &action, &target, &detail, &at); err == nil {
			out = append(out, map[string]any{"id": id, "admin": admin, "action": action,
				"target": target, "detail": detail, "at": at.Unix()})
		}
	}
	writeJSON(c, map[string]any{"result": true, "audit": out})
}

var _ = context.Background
