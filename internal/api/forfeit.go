package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// Quit settlement (user requirement 2026-11-14): a coach who
// quits or kills the client mid-match loses 0-5. Two detectors
// feed settleForfeit:
//
//   - the session relay (POST /internal/relay/forfeit) for human
//     matches already playing over UDP — it sees one engine go
//     totally silent while the peer stays active;
//   - the sweeper (sweeper.go) for stages the relay cannot see:
//     human matches abandoned at the squad/kit prep screens and
//     PvE matches whose owner went offline.
//
// Settlement runs through the REAL matchEnd handler via loopback
// HTTP for both users, so contracts, EP reward, career counters,
// multiplay history and ranked elo/forfeit records are exactly
// what a normal completion produces. matchEnd gates settlement
// on stage != "closed" and the canonical match object is shared,
// so the stage is reopened between the two calls; the second
// call leaves it closed, and any late end call from a thawed
// client then no-ops.
//
// With ghost=true the relay fabricates the quitter's synch
// frames and the winner keeps playing to the final whistle;
// their own /match/<id>/end call is rewritten by the hook in
// matchEnd to the official 5-0, whatever the played score was.

// settleForfeit settles the loser (and, unless ghost, the winner
// too) of a detected quit. Returns "ok", "noop" (match unknown,
// already closed or already settled) or "fail".
func (s *Server) settleForfeit(ctx context.Context, mid string, loserSlot int, ghost bool) string {
	match, found := s.liveMatch(mid)
	if !found || game.Str(match["stage"]) == "closed" {
		return "noop"
	}
	if game.Map(match["_forfeit"]) != nil {
		return "noop"
	}
	if parts := matchParticipantCIDs(match); len(parts) > 2 {
		// Multi match (2v2/3v3): loser_slot is the quitter's
		// SESSION slot (participants enumerate left side
		// first, then right). Settle ONLY the quitter as a
		// 0-5 loss from their own side's perspective — the
		// match plays on for everyone else, and no global
		// _forfeit marker is set (it is a two-party concept:
		// it would block the other participants' own
		// settlements and any second quitter's).
		if loserSlot < 0 || loserSlot >= len(parts) {
			return "noop"
		}
		loser := s.userByCoachID(ctx, parts[loserSlot])
		if loser == nil {
			return "noop"
		}
		leftCount := len(game.List(game.Map(game.Map(match["side"])["left"])["players"]))
		loserTeam := "home"
		if loserSlot >= leftCount {
			loserTeam = "away"
		}
		if !s.callMatchEnd(ctx, mid, loser, endBody(loserTeam, 0, 5, 1)) {
			return "fail"
		}
		return "ok"
	}
	ownerUID, found := s.matchOwner(mid)
	if !found {
		return "noop"
	}
	owner, err := game.UserByID(ctx, s.d.Store, ownerUID)
	if err != nil || owner == nil {
		return "noop"
	}
	opp := s.userByCoachID(ctx, game.Str(game.Map(match["_opp_coach"])["coachid"]))
	if opp == nil {
		return "noop"
	}
	// Room matches: left = host = owner ("home"), right = guest
	// ("away"). loser_slot: 0 = left, 1 = right.
	loser, winner := owner, opp
	loserTeam, winnerTeam := "home", "away"
	if loserSlot != 0 {
		loser, winner = opp, owner
		loserTeam, winnerTeam = "away", "home"
	}
	match["_forfeit"] = map[string]any{
		"loser_slot":    loserSlot,
		"loser_settled": false,
		"ghost":         ghost,
	}
	if !s.callMatchEnd(ctx, mid, loser, endBody(loserTeam, 0, 5, 1)) {
		delete(match, "_forfeit")
		return "fail"
	}
	if live, ok := s.liveMatch(mid); ok {
		live["stage"] = "playing"
		if f := game.Map(live["_forfeit"]); f != nil {
			f["loser_settled"] = true
		}
	}
	if !ghost {
		if !s.callMatchEnd(ctx, mid, winner, endBody(winnerTeam, 5, 0, 0)) {
			return "fail"
		}
		if live, ok := s.liveMatch(mid); ok {
			live["stage"] = "closed"
			// Wake the winner's client if it is sitting on a prep
			// screen: push the closed match + stage like a normal
			// stage transition would.
			if wst, err := game.LoadState(ctx, s.d.Store, winner.ID); err == nil {
				if wcid, _ := myCoachID(wst); wcid != "" {
					game.MessengerPush("/match/"+mid, live, wcid)
					game.MessengerPush("/match/"+mid+"/stage", "closed", wcid)
				}
			}
		}
	}
	return "ok"
}

// relayForfeit is the relay's HTTP entry point into
// settleForfeit (loopback only, shared key in
// /etc/fo3/internal.key).
func (s *Server) relayForfeit(c *gin.Context) {
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
		Mid       string `json:"mid"`
		LoserSlot int    `json:"loser_slot"`
		Ghost     bool   `json:"ghost"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Mid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"result": false})
		return
	}
	switch s.settleForfeit(c.Request.Context(), req.Mid, req.LoserSlot, req.Ghost) {
	case "fail":
		c.JSON(http.StatusBadGateway, gin.H{"result": false})
	case "noop":
		c.JSON(http.StatusOK, gin.H{"result": true, "noop": true})
	default:
		c.JSON(http.StatusOK, gin.H{"result": true})
	}
}

// endBody builds the body a client would have sent to
// /match/<mid>/end from the given side's perspective.
func endBody(myTeam string, myScore, oppScore, forfeits int) map[string]any {
	oppTeam := "away"
	if myTeam == "away" {
		oppTeam = "home"
	}
	body := map[string]any{
		"myTeam": myTeam, "opponentTeam": oppTeam, "forfeits": forfeits,
	}
	body[myTeam] = map[string]any{"score": myScore, "shootout": 0}
	body[oppTeam] = map[string]any{"score": oppScore, "shootout": 0}
	return body
}

// callMatchEnd invokes this server's own /match/<mid>/end over
// loopback HTTP with the given user's legacy token.
func (s *Server) callMatchEnd(ctx context.Context, mid string, u *game.User, body map[string]any) bool {
	addr := os.Getenv("FO3_SHADOW_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return false
	}
	url := "http://" + addr + "/match/" + mid + "/end?token=" +
		"FO3-" + u.Username + "-" + strconv.FormatInt(u.ID, 10)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return false
	}
	hreq.Header.Set("Content-Type", "application/json")
	cli := &http.Client{Timeout: 15 * time.Second}
	resp, err := cli.Do(hreq)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
