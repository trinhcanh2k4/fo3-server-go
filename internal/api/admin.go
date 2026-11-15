package api

// Admin API (Phase 3): separate admin accounts (Argon2id, Redis
// session tokens), audit-logged mutations over users, coaches,
// players and inventory. React is only a client of these routes.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
	"fo3server/internal/store"
)

func adminTokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "admintok:" + hex.EncodeToString(sum[:])
}

// The admin panel can live on its own subdomain (convention:
// fo3-adm.<your-domain>), gated by HTTP basic auth at the Nginx
// vhost; the game domain then no longer serves the panel or its
// API at all. Requests arriving under the admin host are
// therefore trusted without the panel's own token login (no
// second password), requests under the game host get a 404, and
// direct (loopback) requests keep the token flow. The Go port
// itself is firewalled, so a forged Host header can only arrive
// through one of the two Nginx vhosts, both of which set Host
// from their own server_name. Set FO3_ADMIN_HOST / FO3_GAME_HOST
// to your own hosts (see README, "Server address configuration").
var adminHost = envOrDefault("FO3_ADMIN_HOST", "fo3-adm.example.com")
var gameHost = envOrDefault("FO3_GAME_HOST", "fo3.example.com")

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func requestHost(c *gin.Context) string {
	h := c.Request.Host
	if i := strings.Index(h, ":"); i >= 0 {
		h = h[:i]
	}
	return strings.ToLower(h)
}

func (s *Server) adminLogin(c *gin.Context) {
	if requestHost(c) == gameHost {
		c.AbortWithStatusJSON(404, map[string]any{"result": false})
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	username := game.Str(req["username"])
	password := game.Str(req["password"])
	ctx := c.Request.Context()
	ip := clientIP(c)
	if s.loginBlocked(ctx, "ad:", ip, username) {
		writeJSON(c, map[string]any{"result": false, "reason": "ADMIN_LOCKED"})
		return
	}
	var id int64
	var hash, role string
	err := s.d.Store.DB.QueryRowContext(ctx,
		"SELECT id, pass_hash, role FROM admins WHERE username = ?", username).Scan(&id, &hash, &role)
	if err != nil || !store.CheckPassword(password, hash) {
		s.noteLoginFailure(ctx, "ad:", ip, username)
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_ADMIN_LOGIN"})
		return
	}
	s.noteLoginSuccess(ctx, "ad:", ip, username)
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	token := hex.EncodeToString(buf)
	s.d.Store.Redis.Set(ctx, adminTokenKey(token), username+"|"+role, 12*time.Hour)
	writeJSON(c, map[string]any{"result": true, "token": token, "username": username, "role": role})
}

func (s *Server) adminAuth(c *gin.Context) {
	switch requestHost(c) {
	case adminHost:
		// Behind the subdomain's Nginx basic auth: no second login.
		c.Set("admin_user", "aapanel")
		c.Set("admin_role", "admin")
		c.Next()
		return
	case gameHost:
		c.AbortWithStatusJSON(404, map[string]any{"result": false})
		return
	}
	token := c.GetHeader("X-Admin-Token")
	if token == "" && strings.HasPrefix(c.GetHeader("Authorization"), "Bearer ") {
		token = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	}
	ctx := c.Request.Context()
	val, err := s.d.Store.Redis.Get(ctx, adminTokenKey(token)).Result()
	if token == "" || err != nil {
		c.AbortWithStatusJSON(401, map[string]any{"result": false, "reason": "ADMIN_UNAUTHORIZED"})
		return
	}
	parts := strings.SplitN(val, "|", 2)
	c.Set("admin_user", parts[0])
	role := ""
	if len(parts) > 1 {
		role = parts[1]
	}
	c.Set("admin_role", role)
	if role == "viewer" && c.Request.Method != "GET" {
		c.AbortWithStatusJSON(403, map[string]any{"result": false, "reason": "ADMIN_READ_ONLY"})
		return
	}
	c.Next()
}

func adminName(c *gin.Context) string {
	if v, ok := c.Get("admin_user"); ok {
		return v.(string)
	}
	return ""
}

func (s *Server) audit(c *gin.Context, action, target string, detail any) {
	game.Audit(c.Request.Context(), s.d.Store, adminName(c), action, target, detail)
}

func uidParam(c *gin.Context) (int64, bool) {
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	return uid, err == nil
}

func (s *Server) adminUsers(c *gin.Context) {
	ctx := c.Request.Context()
	users, err := game.AllUsers(ctx, s.d.Store)
	if err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "DB_ERROR"})
		return
	}
	q := strings.ToLower(c.Query("q"))
	out := []any{}
	for _, u := range users {
		if q != "" && !strings.Contains(strings.ToLower(u.Username), q) {
			continue
		}
		st, _ := game.LoadState(ctx, s.d.Store, u.ID)
		coaches := []any{}
		if st != nil {
			for _, cv := range game.List(st["coaches"]) {
				if coach := game.Map(cv); coach != nil {
					coaches = append(coaches, s.d.Roster.CoachSummary(st, game.Int(coach["slot"]), u))
				}
			}
		}
		var vipUntil, pcUntil int64
		if st != nil {
			vipUntil = game.EntitlementUntil(st, "vip")
			pcUntil = game.EntitlementUntil(st, "pc")
		}
		out = append(out, map[string]any{
			"id": u.ID, "username": u.Username, "disabled": u.Disabled,
			"elo": u.Elo, "coaches": coaches,
			"vip_until": vipUntil, "pc_until": pcUntil,
		})
	}
	writeJSON(c, map[string]any{"result": true, "users": out})
}

func (s *Server) adminUserCreate(c *gin.Context) {
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	username := game.Str(req["username"])
	password := game.Str(req["password"])
	coachName := game.Str(req["coach_name"])
	teamid := game.Int(req["teamid"])
	if username == "" || password == "" || coachName == "" || teamid == 0 {
		writeJSON(c, map[string]any{"result": false, "reason": "MISSING_FIELDS"})
		return
	}
	ctx := c.Request.Context()
	user, err := game.CreateUser(ctx, s.d.Store, username, password)
	if err == nil {
		_, _ = s.d.Store.DB.ExecContext(ctx, "UPDATE users SET coach_name = ? WHERE id = ?", coachName, user.ID)
	}
	if err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "CREATE_FAILED"})
		return
	}
	st := s.d.Roster.SeedNewCoach(user.ID, coachName, teamid)
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	s.audit(c, "user.create", username, map[string]any{"teamid": teamid})
	writeJSON(c, map[string]any{"result": true, "id": user.ID})
}

func (s *Server) adminUserAction(c *gin.Context) {
	uid, ok := uidParam(c)
	if !ok {
		writeJSON(c, map[string]any{"result": false, "reason": "BAD_UID"})
		return
	}
	ctx := c.Request.Context()
	action := c.Param("action")
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	switch action {
	case "disable", "enable":
		disabled := action == "disable"
		_, err := s.d.Store.DB.ExecContext(ctx, "UPDATE users SET disabled = ? WHERE id = ?", disabled, uid)
		if err != nil {
			writeJSON(c, map[string]any{"result": false, "reason": "DB_ERROR"})
			return
		}
		if disabled {
			_, _ = s.d.Store.DB.ExecContext(ctx, "UPDATE users SET token_hash = NULL WHERE id = ?", uid)
		}
		s.audit(c, "user."+action, strconv.FormatInt(uid, 10), nil)
	case "password":
		pw := game.Str(req["password"])
		if len(pw) < 4 {
			writeJSON(c, map[string]any{"result": false, "reason": "PASSWORD_TOO_SHORT"})
			return
		}
		hash, err := store.HashPassword(pw)
		if err != nil {
			writeJSON(c, map[string]any{"result": false, "reason": "HASH_ERROR"})
			return
		}
		_, _ = s.d.Store.DB.ExecContext(ctx,
			"UPDATE users SET pass_hash = ?, legacy_hash = NULL, token_hash = NULL WHERE id = ?", hash, uid)
		s.audit(c, "user.password", strconv.FormatInt(uid, 10), nil)
	case "secondpass-reset":
		_, _ = s.d.Store.DB.ExecContext(ctx, "UPDATE users SET pass2_hash = NULL WHERE id = ?", uid)
		if st, err := game.LoadState(ctx, s.d.Store, uid); err == nil {
			delete(st, "second_pass_hash")
			delete(st, "_2ndpass_fail")
			for _, cv := range game.List(st["coaches"]) {
				if coach := game.Map(cv); coach != nil {
					delete(coach, "second_pass")
				}
			}
			_ = game.SaveState(ctx, s.d.Store, uid, st)
		}
		s.audit(c, "user.secondpass_reset", strconv.FormatInt(uid, 10), nil)
	case "entitlement":
		// Grant/revoke PC (IGR) and VIP for a set duration:
		// {vip_days, pc_days}; a present key with days <= 0
		// revokes that right, an absent key leaves it unchanged.
		st, err := game.LoadState(ctx, s.d.Store, uid)
		if err != nil || st == nil {
			writeJSON(c, map[string]any{"result": false, "reason": "NO_STATE"})
			return
		}
		ent := game.Map(st["entitlements"])
		if ent == nil {
			ent = map[string]any{}
		}
		old := map[string]any{"vip_until": game.Int64(ent["vip_until"]), "pc_until": game.Int64(ent["pc_until"])}
		now := time.Now().UnixMilli()
		for _, kind := range []string{"vip", "pc"} {
			if dv, present := req[kind+"_days"]; present {
				days := game.Int(dv)
				if days <= 0 {
					ent[kind+"_until"] = int64(0)
				} else {
					ent[kind+"_until"] = now + int64(days)*86400000
				}
			}
		}
		st["entitlements"] = ent
		if err := game.SaveState(ctx, s.d.Store, uid, st); err != nil {
			writeJSON(c, map[string]any{"result": false, "reason": "SAVE_FAILED"})
			return
		}
		s.audit(c, "user.entitlement", strconv.FormatInt(uid, 10),
			map[string]any{"old": old, "new": map[string]any{
				"vip_until": game.Int64(ent["vip_until"]), "pc_until": game.Int64(ent["pc_until"])}})
	case "delete":
		_, _ = s.d.Store.DB.ExecContext(ctx, "DELETE FROM user_state WHERE user_id = ?", uid)
		_, _ = s.d.Store.DB.ExecContext(ctx, "DELETE FROM friends WHERE coach_id IN (SELECT coach_id FROM coaches WHERE user_id = ?) OR friend_coach_id IN (SELECT coach_id FROM coaches WHERE user_id = ?)", uid, uid)
		_, _ = s.d.Store.DB.ExecContext(ctx, "DELETE FROM coaches WHERE user_id = ?", uid)
		_, _ = s.d.Store.DB.ExecContext(ctx, "DELETE FROM users WHERE id = ?", uid)
		s.audit(c, "user.delete", strconv.FormatInt(uid, 10), nil)
	default:
		writeJSON(c, map[string]any{"result": false, "reason": "UNKNOWN_ACTION"})
		return
	}
	writeJSON(c, map[string]any{"result": true})
}

// --- coaches ----------------------------------------------------

func (s *Server) loadUserState(c *gin.Context) (game.State, *game.User, int, bool) {
	uid, ok := uidParam(c)
	if !ok {
		writeJSON(c, map[string]any{"result": false, "reason": "BAD_UID"})
		return nil, nil, 0, false
	}
	ctx := c.Request.Context()
	user, err := game.UserByID(ctx, s.d.Store, uid)
	if err != nil || user == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NO_USER"})
		return nil, nil, 0, false
	}
	st, err := game.LoadState(ctx, s.d.Store, uid)
	if err != nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NO_STATE"})
		return nil, nil, 0, false
	}
	slot, _ := strconv.Atoi(c.Param("slot"))
	return st, user, slot, true
}

func (s *Server) adminCoachSet(c *gin.Context) {
	st, user, slot, ok := s.loadUserState(c)
	if !ok {
		return
	}
	coach := game.CoachBySlot(st, slot)
	if coach == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NO_COACH"})
		return
	}
	ctx := c.Request.Context()
	field := c.Param("field")
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	old := map[string]any{}
	switch field {
	case "ep":
		old["ep"] = coach["ep"]
		coach["ep"] = game.Int64(req["value"])
	case "gp":
		old["gp"] = game.StoredGP(st)
		q := game.Map(st["quest"])
		if q == nil {
			q = map[string]any{}
		}
		q["gp"] = game.Int(req["value"])
		st["quest"] = q
	case "elo":
		key := "ranked"
		if game.Str(req["mode"]) == "mgr" {
			key = "ranked_mgr"
		}
		rk := game.RankedState(st, key)
		old["elo"] = rk["elo"]
		rk["elo"] = game.Int(req["value"])
		if game.Int(rk["highelo"]) < game.Int(req["value"]) {
			rk["highelo"] = game.Int(req["value"])
		}
		st[key] = rk
		if key == "ranked" {
			_, _ = s.d.Store.DB.ExecContext(ctx, "UPDATE users SET elo = ? WHERE id = ?", game.Int(req["value"]), user.ID)
		}
	case "level":
		li := game.Map(coach["levelinfo"])
		if li == nil {
			li = map[string]any{}
		}
		old["level"] = li["level"]
		lvl := game.Int(req["value"])
		if lvl < 1 {
			lvl = 1
		}
		if lvl > 100 {
			lvl = 100
		}
		li["level"] = lvl
		li["exp"] = 0
		li["maxexp"] = game.CoachNeed(lvl)
		coach["levelinfo"] = li
	case "name":
		old["name"] = coach["name"]
		coach["name"] = game.Str(req["value"])
	case "badge":
		old["badge"] = coach["currentbadge"]
		coach["currentbadge"] = game.Int(req["value"])
	default:
		writeJSON(c, map[string]any{"result": false, "reason": "UNKNOWN_FIELD"})
		return
	}
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	s.audit(c, "coach.set_"+field, strconv.FormatInt(user.ID, 10)+"/"+strconv.Itoa(slot),
		map[string]any{"old": old, "new": req["value"]})
	writeJSON(c, map[string]any{"result": true,
		"coach": s.d.Roster.CoachSummary(st, slot, user)})
}

func (s *Server) adminCoachReset(c *gin.Context) {
	st, user, slot, ok := s.loadUserState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	module := game.Str(req["module"])
	switch module {
	case "quests":
		delete(st, "quest")
	case "achievements":
		delete(st, "achievements")
	case "league":
		delete(st, "league2")
		delete(st, "league2_archives")
	case "cups":
		delete(st, "cups")
		delete(st, "cup_pending")
	case "ranked":
		st["ranked"] = game.FreshRankedState()
		st["ranked_mgr"] = game.FreshRankedState()
		_, _ = s.d.Store.DB.ExecContext(c.Request.Context(), "UPDATE users SET elo = 1000 WHERE id = ?", user.ID)
	case "progress":
		for _, k := range []string{"quest", "achievements", "league2", "league2_archives",
			"cups", "cup_pending", "daily", "bag"} {
			delete(st, k)
		}
		st["ranked"] = game.FreshRankedState()
		st["ranked_mgr"] = game.FreshRankedState()
	default:
		writeJSON(c, map[string]any{"result": false, "reason": "UNKNOWN_MODULE"})
		return
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	s.audit(c, "coach.reset_"+module, strconv.FormatInt(user.ID, 10), nil)
	writeJSON(c, map[string]any{"result": true})
	_ = slot
}

// --- players & inventory ----------------------------------------

func (s *Server) adminPlayers(c *gin.Context) {
	st, user, slot, ok := s.loadUserState(c)
	if !ok {
		return
	}
	team := game.TeamOf(st, slot)
	out := []any{}
	if team != nil {
		for _, pv := range game.List(team["players"]) {
			if p := game.Map(pv); p != nil {
				out = append(out, map[string]any{
					"iid": game.IID(p), "spid": game.Int(p["spid"]),
					"season":      game.Int(p["spid"]) / 1000000,
					"season_name": game.SeasonLabel(game.Int(p["spid"]) / 1000000),
					"name":        func() string { n, _ := s.d.Roster.PlayerDisplayName(game.Int(p["spid"])); return n }(),
					"grade": game.GradeOf(p), "life": game.PlayerLife(p),
					"team2": p["team2"] == true, "price": int64(s.d.Roster.CardPrice(game.Int(p["spid"]), game.GradeOf(p))),
					"jersey": game.Int(p["jerseynumber"]),
				})
			}
		}
	}
	_ = user
	writeJSON(c, map[string]any{"result": true, "players": out})
}

func (s *Server) adminPlayerOp(c *gin.Context) {
	st, user, slot, ok := s.loadUserState(c)
	if !ok {
		return
	}
	team := game.TeamOf(st, slot)
	if team == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NO_TEAM"})
		return
	}
	ctx := c.Request.Context()
	op := c.Param("op")
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	switch op {
	case "grant":
		spid, grade := game.Int(req["spid"]), game.Int(req["grade"])
		if spid == 0 {
			writeJSON(c, map[string]any{"result": false, "reason": "BAD_SPID"})
			return
		}
		if grade < 1 {
			grade = 1
		}
		s.d.Roster.AddPlayer(team, spid, grade)
		s.audit(c, "player.grant", strconv.FormatInt(user.ID, 10), map[string]any{"spid": spid, "grade": grade})
	case "remove", "life", "grade":
		iid := game.Str(req["iid"])
		var target map[string]any
		for _, pv := range game.List(team["players"]) {
			if p := game.Map(pv); p != nil && game.IID(p) == iid {
				target = p
				break
			}
		}
		if target == nil {
			writeJSON(c, map[string]any{"result": false, "reason": "NO_PLAYER"})
			return
		}
		switch op {
		case "remove":
			s.d.Roster.RemovePlayer(team, target)
		case "life":
			life := game.Int(req["value"])
			if life < 0 {
				life = 0
			}
			if life > 40 {
				life = 40
			}
			target["life"] = life
		case "grade":
			g := game.Int(req["value"])
			if g < 1 {
				g = 1
			}
			if g > 10 {
				g = 10
			}
			target["grade"] = g
		}
		s.audit(c, "player."+op, strconv.FormatInt(user.ID, 10), map[string]any{"iid": iid, "value": req["value"]})
	default:
		writeJSON(c, map[string]any{"result": false, "reason": "UNKNOWN_OP"})
		return
	}
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true})
}

func (s *Server) adminInventory(c *gin.Context) {
	st, user, slot, ok := s.loadUserState(c)
	if !ok {
		return
	}
	coach := game.CoachBySlot(st, slot)
	if coach == nil {
		writeJSON(c, map[string]any{"result": false, "reason": "NO_COACH"})
		return
	}
	if c.Request.Method == "GET" {
		writeJSON(c, map[string]any{"result": true, "items": s.d.Roster.CoachItems(st, slot)})
		return
	}
	ctx := c.Request.Context()
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	itemid, count := game.Int(req["itemid"]), game.Int(req["count"])
	if count <= 0 {
		count = 1
	}
	switch c.Param("op") {
	case "grant":
		game.InventoryAdd(coach, itemid, count)
	case "remove":
		kept := []any{}
		for _, iv := range game.List(coach["items"]) {
			it := game.Map(iv)
			if it != nil && game.Int(it["itemid"]) == itemid {
				it["remains"] = game.Int(it["remains"]) - count
				if game.Int(it["remains"]) <= 0 {
					continue
				}
			}
			kept = append(kept, iv)
		}
		coach["items"] = kept
	default:
		writeJSON(c, map[string]any{"result": false, "reason": "UNKNOWN_OP"})
		return
	}
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	s.audit(c, "inventory."+c.Param("op"), strconv.FormatInt(user.ID, 10),
		map[string]any{"itemid": itemid, "count": count})
	writeJSON(c, map[string]any{"result": true})
	_ = slot
}
