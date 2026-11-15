package api

import (
	"crypto/subtle"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// relaySetStage is an internal probe/control endpoint (loopback
// + shared key, like relayForfeit): it sets the canonical match's
// stage and pushes the match + stage resources to both parties,
// exactly like a normal stage transition. Built to test how the
// client reacts to stage "canceled" mid-match (the AS3
// processMatchStageResource path: connection-fail system
// message, exit(), native onMatchCanceled) when a peer has
// vanished — the designed "opponent gone" lever.
func (s *Server) relaySetStage(c *gin.Context) {
	ip := net.ParseIP(c.ClientIP())
	if ip == nil || !ip.IsLoopback() {
		c.JSON(http.StatusForbidden, gin.H{"result": false})
		return
	}
	keyBytes, err := os.ReadFile("/etc/fo3/internal.key")
	if err != nil || subtle.ConstantTimeCompare(
		[]byte(strings.TrimSpace(string(keyBytes))),
		[]byte(c.GetHeader("X-Internal-Key"))) != 1 {
		c.JSON(http.StatusForbidden, gin.H{"result": false})
		return
	}
	var req struct {
		Mid   string `json:"mid"`
		Stage string `json:"stage"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Mid == "" || req.Stage == "" {
		c.JSON(http.StatusBadRequest, gin.H{"result": false})
		return
	}
	ctx := c.Request.Context()
	match, found := s.liveMatch(req.Mid)
	if !found {
		c.JSON(http.StatusOK, gin.H{"result": true, "noop": true})
		return
	}
	match["stage"] = req.Stage
	pushTo := []string{}
	if ownerUID, found := s.matchOwner(req.Mid); found {
		if ost, err := game.LoadState(ctx, s.d.Store, ownerUID); err == nil {
			if cid, _ := myCoachID(ost); cid != "" {
				pushTo = append(pushTo, cid)
			}
		}
	}
	if cid := game.Str(game.Map(match["_opp_coach"])["coachid"]); cid != "" {
		pushTo = append(pushTo, cid)
	}
	for _, cid := range pushTo {
		game.MessengerPush("/match/"+req.Mid, match, cid)
		game.MessengerPush("/match/"+req.Mid+"/stage", req.Stage, cid)
	}
	c.JSON(http.StatusOK, gin.H{"result": true, "pushed": len(pushTo)})
}
