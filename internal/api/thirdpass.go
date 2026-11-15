package api

// Third password (player-lock password) endpoints and the lock gate.
//
// Client contract, reverse-engineered from TeamManagementPlayerPool-
// TabPage (closures m28/m35/m36/m39/m42 of its ABC):
//   - The page's lock button first calls POST (lock) or pushDELETE
//     (unlock) on /player/:pid/lock. If the response body carries
//     {result:false, reason:"NEED_ADDITIONAL_AUTH", param:{type:"otp",
//     status, failcount}} it does NOT treat the action as done:
//     status "notset" opens the SET-password popup, any other status
//     opens the ENTER-password popup (or a deny dialog at failcount>=5).
//   - The enter popup posts /3rdpass/check {pass, category:"lock"} and
//     expects {result:true, check:true|false}; on check:true the page
//     re-sends the lock/unlock call, which must then succeed — so a
//     successful check opens a short verified window server-side
//     (Redis lockauth:<uid>, lockAuthWindow).
//   - First set: POST /3rdpass {pass}. Change: PUT /3rdpass
//     {curpass, pass} (the page's cancel path also PUTs curpass==pass,
//     a harmless no-op under this implementation).
//   - A successful lock/unlock ({result:true}) makes the page fire
//     ON_TEAM_CHANGED, refreshing the pool in place.

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// lockAuthWindow is how long a successful /3rdpass/check (or a fresh
// password set/change) authorizes lock/unlock calls. It is served to
// the client as STATICVALUES PlayerLock.duration (minutes) so the
// countdown the client renders matches the enforced window.
const lockAuthWindow = 5 * time.Minute

func lockAuthKey(userID int64) string { return fmt.Sprintf("lockauth:%d", userID) }

func (s *Server) lockAuthFresh(ctx context.Context, userID int64) bool {
	if s.d.Store.Redis == nil {
		return false
	}
	n, err := s.d.Store.Redis.Exists(ctx, lockAuthKey(userID)).Result()
	return err == nil && n > 0
}

func (s *Server) grantLockAuth(ctx context.Context, userID int64) {
	if s.d.Store.Redis != nil {
		s.d.Store.Redis.Set(ctx, lockAuthKey(userID), "1", lockAuthWindow)
	}
}

// needLockAuth builds the NEED_ADDITIONAL_AUTH response in both shapes
// the client might unwrap: flat top level and inside the resource body.
func needLockAuth(pid, status string, fail int) map[string]any {
	param := map[string]any{"type": "otp", "status": status, "failcount": fail}
	inner := map[string]any{"result": false, "reason": "NEED_ADDITIONAL_AUTH", "param": param}
	return map[string]any{"result": false, "reason": "NEED_ADDITIONAL_AUTH", "param": param,
		"resources": []any{map[string]any{"uri": "/player/" + pid + "/lock", "body": inner}}}
}

func (s *Server) thirdPassCheck(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	cand := game.Str(req["pass"])
	if game.HasThirdPass(st) && game.VerifyThirdPass(st, cand) {
		st["_3rdpass_fail"] = 0
		_ = game.SaveState(ctx, s.d.Store, user.ID, st)
		s.grantLockAuth(ctx, user.ID)
		writeJSON(c, map[string]any{"result": true, "check": true})
		return
	}
	cnt := game.Int(st["_3rdpass_fail"]) + 1
	st["_3rdpass_fail"] = cnt
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true, "check": false, "count": cnt, "limit": 5})
}

// thirdPassSet handles POST /3rdpass: {pass} = first set;
// {curpass, pass} = change (the page also reaches change via PUT).
func (s *Server) thirdPassSet(c *gin.Context) {
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	if _, has := req["curpass"]; has {
		s.thirdPassChange(c, req)
		return
	}
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	pass := game.Str(req["pass"])
	if pass == "" {
		writeJSON(c, map[string]any{"result": false, "reason": "EMPTY_PASS"})
		return
	}
	if game.HasThirdPass(st) {
		writeJSON(c, map[string]any{"result": false, "reason": "ALREADY_SET"})
		return
	}
	game.SetThirdPass(st, pass)
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	s.grantLockAuth(c.Request.Context(), user.ID)
	writeJSON(c, map[string]any{"result": true, "check": true})
}

func (s *Server) thirdPassPut(c *gin.Context) {
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	s.thirdPassChange(c, req)
}

func (s *Server) thirdPassChange(c *gin.Context, req map[string]any) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	cur := game.Str(req["curpass"])
	newp := game.Str(req["pass"])
	if newp == "" || !game.HasThirdPass(st) || !game.VerifyThirdPass(st, cur) {
		writeJSON(c, map[string]any{"result": true, "check": false})
		return
	}
	game.SetThirdPass(st, newp)
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	s.grantLockAuth(c.Request.Context(), user.ID)
	writeJSON(c, map[string]any{"result": true, "check": true})
}
