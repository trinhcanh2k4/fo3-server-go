package api

// Player lock endpoints (client contract from TeamManagementPlayerPool-
// TabPage): the page's lock button calls pushPOST /player/:pid/lock {}
// to lock and pushDELETE on the same URL to unlock — but the client's
// native HTTP marshal puts BOTH on the wire as an identical POST with
// body "{}" (probe-captured from the real client 2026-11-14, bak-1145:
// no _method, no query, no distinguishing header; a real HTTP DELETE
// never arrives). The endpoint therefore TOGGLES on plain POST: pressing
// the button on a currently locked card releases it, on an unlocked
// card locks it. An explicit {"_method":"DELETE"} body (the convention
// other pages use) and a real DELETE still mean unlock.
// The client renders the lock from the player object's "locked" field —
// a W3C timestamp; the card counts as locked while it is in the future
// (isPlayerLocked in the Recycle/Enhance/Trade pages: parseW3CDTF > now,
// year 9999 = permanent). Neither fx nor the Go port had these routes —
// the unknown-path stub answered {result:true} and nothing persisted.
// The password gate is POST /3rdpass/check {pass, category:"lock"},
// registered in api.go onto the second-password checker.
//
// Player identity is game.IID (never Str(_id) alone — generated cards
// have no _id; see AGENTS.md). A card can appear in several team slots'
// rosters; the lock applies to every copy.

import (
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// playerLockTTL is how long a lock stays in force once set.
const playerLockTTL = 7 * 24 * time.Hour

// playerLockedNow reports whether p's lock is still in force.
func playerLockedNow(p map[string]any) bool {
	ts := game.Str(p["locked"])
	if ts == "" {
		return false
	}
	t, err := time.Parse("2006-01-02T15:04:05.000Z", ts)
	if err != nil {
		return false
	}
	return t.After(time.Now())
}

func (s *Server) playerLock(c *gin.Context) {
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	if game.Str(req["_method"]) == "DELETE" || c.Query("_method") == "DELETE" {
		s.setPlayerLock(c, false, false)
		return
	}
	s.setPlayerLock(c, true, false) // toggle: direction decided inside
}
func (s *Server) playerUnlock(c *gin.Context) { s.setPlayerLock(c, false, false) }

// setPlayerLock locks or unlocks every copy of the card. With
// toggle=true the direction is chosen from the card's current state:
// locked -> unlock, unlocked -> lock (the client's two button actions
// are indistinguishable on the wire, see the header comment).
func (s *Server) setPlayerLock(c *gin.Context, toggle bool, lock bool) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	pid := c.Param("pid")
	// Password gate (see thirdpass.go): without a lock password, or
	// without a fresh verified window, do NOT mutate — answer
	// NEED_ADDITIONAL_AUTH and the client opens the set/enter popup.
	if !game.HasThirdPass(st) {
		writeJSON(c, needLockAuth(pid, "notset", 0))
		return
	}
	if !s.lockAuthFresh(c.Request.Context(), user.ID) {
		writeJSON(c, needLockAuth(pid, "set", game.Int(st["_3rdpass_fail"])))
		return
	}
	wantLock := lock
	expiry := game.W3C(float64(time.Now().Add(playerLockTTL).Unix()))
	n := 0
	var sample map[string]any
	for _, tv := range game.Map(st["teams"]) {
		team := game.Map(tv)
		for _, pv := range game.List(team["players"]) {
			p := game.Map(pv)
			if p == nil || game.IID(p) != pid {
				continue
			}
			if toggle && n == 0 {
				wantLock = !playerLockedNow(p)
			}
			if wantLock {
				p["locked"] = expiry
			} else {
				delete(p, "locked")
			}
			n++
			sample = p
		}
	}
	if n == 0 {
		writeJSON(c, map[string]any{"result": false, "reason": "PLAYER_NOT_FOUND"})
		return
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	// The pool page refreshes in place from the ResourceController's
	// cached /coach/me resource (its ON_TEAM_CHANGED handler rebuilds
	// the list from CoachProxy.team; the controller never refetches
	// over HTTP). The success response must therefore carry the fresh
	// /coach/me resource — the same pattern as the achievement claims —
	// or the lock icon only updates after leaving and re-entering.
	me := s.d.Roster.CoachMe(st, game.DefaultSlot(st), user)
	writeJSON(c, map[string]any{"result": true, "resources": []any{
		map[string]any{"uri": "/player/" + pid + "/lock", "body": sample},
		map[string]any{"resource": "/coach/me", "uri": "/coach/me", "body": me}}})
}
