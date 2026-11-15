package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
	"fo3server/internal/store"
)

// Boot-chain endpoints that are not domain logic: service discovery,
// sign wait, stub lists the client fetches while booting. Ported from
// the Python handlers of the same paths.

// signWait serves responses/sign_wait.json (POST /sign/wait).
func (s *Server) signWait(c *gin.Context) {
	if hit := s.fromFile("/sign/wait"); hit != nil {
		c.Data(http.StatusOK, "application/json", hit)
		return
	}
	writeJSON(c, map[string]any{"result": true})
}

// messengerService ports hc.ep_info_my_services_messenger_2: the
// address the client dials for the messenger (Bug 4's endpoint).
// Under fx the Python handler's request-host global never updates, so
// it effectively always answers REDIR_HOST (the configured public IP)
// even for a domain Host header.
func (s *Server) messengerService(c *gin.Context) {
	host := s.d.PublicIP
	writeJSON(c, map[string]any{
		"result": true, "address": host, "port": 28002,
		"SERVER": map[string]any{"address": host, "port": 28002},
	})
}

// specificTarget ports hc.ep_specifictarget (Special Bonus popup off).
func (s *Server) specificTarget(c *gin.Context) {
	writeJSON(c, map[string]any{"issell": false, "targets": []any{}})
}

// monthlyPack ports the GET half of hc.ep_monthlypack (the POST reward
// flow joins with the Phase 2 shop domain).
func (s *Server) monthlyPack(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	writeJSON(c, s.monthlyList(st))
}

// monthlyList is hc.monthly_list: my active monthly packages.
func (s *Server) monthlyList(st game.State) []any {
	slot := game.DefaultSlot(st)
	coach := game.CoachBySlot(st, slot)
	out := []any{}
	if coach != nil {
		monthly := game.Map(coach["monthly"])
		now := float64(time.Now().Unix())
		for k, v := range monthly {
			m := game.Map(v)
			if m == nil || game.Int64(m["expire"]) <= int64(now) {
				continue
			}
			productid := game.Int(k)
			def := s.d.Roster.ItemDef(productid)
			out = append(out, map[string]any{
				"_id": pad24(k), "productid": productid,
				"product":     map[string]any{"itemid": productid, "staticdata": def},
				"purchasedat": game.W3C(float64(game.Int64(m["since"]))),
				"expireat":    game.W3C(float64(game.Int64(m["expire"]))),
				"rewardedat":  game.W3C(float64(game.Int64(m["rewarded"]))),
			})
		}
	}
	return out
}

func pad24(k string) string {
	for len(k) < 24 {
		k = "0" + k
	}
	return k
}

// inboxMessages: Python serves a bare [] for GET /inbox/my/messages.
func (s *Server) inboxMessages(c *gin.Context) {
	writeJSON(c, []any{})
}

// chatBlacklist: Python serves {result, list: []}.
func (s *Server) chatBlacklist(c *gin.Context) {
	writeJSON(c, map[string]any{"result": true, "list": []any{}})
}


// staticInfoRequest ports fx.ep_static_info_request.
func (s *Server) staticInfoRequest(c *gin.Context) {
	week := int64(game.WeekEndTs())
	writeJSON(c, map[string]any{
		"expire":  []any{game.W3C(float64(week)), game.W3C(float64(week + 7*86400))},
		"current": weekQuests(s.d.GamedataDir, week),
		"next":    weekQuests(s.d.GamedataDir, week+7*86400),
	})
}

// weekQuests serves the week's quest list via game.WeekQuests, which
// reproduces Python's random.Random("quests/<ts>").sample exactly
// (see game/pyrandom.go, verified draw-for-draw).
func weekQuests(dir string, weekEnd int64) []map[string]any {
	out := []map[string]any{}
	for _, q := range game.WeekQuests(dir, weekEnd) {
		out = append(out, map[string]any{
			"name":   q.Name,
			"todo":   q.Target,
			"reward": map[string]any{"count": q.GP},
		})
	}
	return out
}

// transferBoostFactor ports hc's /transfer/my/boostfactor route,
// which serves the constant NO_BOOST object (http_config.py:3841).
// Missed in the Phase 2 port — the path fell through to Python
// (67 B there) and got the {} stub after the Phase 4 cutover;
// found by the fallthrough sweep of 2026-11-13.
func (s *Server) transferBoostFactor(c *gin.Context) {
	writeJSON(c, map[string]any{"igr": 0.2, "vip": 0.3, "event": 0,
		"item": 0, "discountlimit": 0})
}

// clubRankedDivisionInfo serves responses/club_ranked_division_info.json
// (bare array; fx.ep_club_ranked_division_info).
func (s *Server) clubRankedDivisionInfo(c *gin.Context) {
	data, err := store.ReadFileCached(filepath.Join(s.d.ResponsesDir, "club_ranked_division_info.json"))
	if err != nil {
		writeJSON(c, game.LadderDivisionsJSON())
		return
	}
	var v any
	if json.Unmarshal(data, &v) != nil {
		writeJSON(c, game.LadderDivisionsJSON())
		return
	}
	writeJSON(c, v)
}
