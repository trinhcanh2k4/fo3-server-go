package api

// B1 (public-launch security plan): relay JOIN authentication.
// When the session relay sees the first JOIN of a (matchid, slot)
// it asks this endpoint — loopback only, shared internal key, the
// same pattern as /internal/relay/forfeit — whether the JOIN's
// source IP is the address the match recorded for that slot's
// coach (fixMatchUserIPs wrote each participant's server-observed
// IP into the session user object at build time). The client is
// not modified: its JOIN already carries matchid + slot, and the
// relay observes the source IP itself. A JOIN is only valid
// within joinAuthWindow of the match's creation.

import (
	"crypto/subtle"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

const joinAuthWindow = 10 * time.Minute

// matchParticipantIPs lists the expected source IPs in session
// slot order (left side's players first, then right's) — the
// same order as matchParticipantCIDs.
func matchParticipantIPs(match map[string]any) []string {
	side := game.Map(match["side"])
	var out []string
	for _, n := range []string{"left", "right"} {
		for _, pv := range game.List(game.Map(side[n])["players"]) {
			out = append(out, game.Str(game.Map(pv)["ip"]))
		}
	}
	return out
}

func (s *Server) relayJoinAuth(c *gin.Context) {
	deny := func(reason string) {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		_ = reason
	}
	ip := net.ParseIP(c.ClientIP())
	if ip == nil || !ip.IsLoopback() {
		c.JSON(http.StatusForbidden, gin.H{"ok": false})
		return
	}
	keyBytes, err := os.ReadFile("/etc/fo3/internal.key")
	if err != nil || subtle.ConstantTimeCompare(
		[]byte(strings.TrimSpace(string(keyBytes))),
		[]byte(c.GetHeader("X-Internal-Key"))) != 1 {
		c.JSON(http.StatusForbidden, gin.H{"ok": false})
		return
	}
	var req struct {
		Mid  string `json:"mid"`
		Slot int    `json:"slot"`
		IP   string `json:"ip"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Mid == "" || req.IP == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false})
		return
	}
	fail := func(reason string) {
		log.Printf("joinauth: deny mid=%s slot=%d ip=%s (%s)", req.Mid, req.Slot, req.IP, reason)
		deny(reason)
	}
	match, found := s.liveMatch(req.Mid)
	if !found {
		fail("unknown match")
		return
	}
	if match["_human"] != true {
		fail("not a human match")
		return
	}
	created, perr := time.Parse("2006-01-02T15:04:05.000Z", game.Str(match["created"]))
	if perr != nil || time.Since(created) > joinAuthWindow {
		fail("outside join window")
		return
	}
	ips := matchParticipantIPs(match)
	if req.Slot < 0 || req.Slot >= len(ips) {
		fail("slot out of range")
		return
	}
	if ips[req.Slot] == "" || ips[req.Slot] != req.IP {
		fail("ip mismatch")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
