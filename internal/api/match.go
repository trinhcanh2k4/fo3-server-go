package api

// Match flow endpoints (hc ep_match_* + fx ep_match_end): the
// pre-match steps, the settlement, the practice campaign and the
// in-match chat. Matches that live in the Python state (room/ranked
// flow, not yet ported) are proxied untouched.

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

// matchOwners maps a live match id to the account whose state
// holds it (the room host for friendlies) AND to the canonical
// in-memory match object. Two humans mutate one match (ready
// flags, squads, stage): per-state copies diverged — each client
// set ready on its own copy and the stage machine never saw both
// (smoke 2026-11-13). All handlers therefore share the canonical
// map; state saves serialize it into each party's document.
type matchOwners struct {
	mu   sync.Mutex
	m    map[string]int64
	live map[string]map[string]any
}

func (s *Server) regMatch(mid string, uid int64, match map[string]any) {
	if s.mo == nil {
		s.mo = &matchOwners{m: map[string]int64{}, live: map[string]map[string]any{}}
	}
	s.mo.mu.Lock()
	defer s.mo.mu.Unlock()
	s.mo.m[mid] = uid
	if match != nil {
		s.mo.live[mid] = match
	}
}

func (s *Server) regMatchOwner(mid string, uid int64) {
	s.regMatch(mid, uid, nil)
}

func (s *Server) matchOwner(mid string) (int64, bool) {
	if s.mo == nil {
		return 0, false
	}
	s.mo.mu.Lock()
	defer s.mo.mu.Unlock()
	uid, ok := s.mo.m[mid]
	return uid, ok
}

func (s *Server) liveMatch(mid string) (map[string]any, bool) {
	if s.mo == nil {
		return nil, false
	}
	s.mo.mu.Lock()
	defer s.mo.mu.Unlock()
	m, ok := s.mo.live[mid]
	return m, ok
}

// ownMatch loads the state and returns its match when it is the
// requested one. A match the caller does not hold may belong to
// the room host (friendly between two accounts): it is mirrored
// into the caller's state and served from there. The Phase-4
// fallback proxy is gone, so an unresolved match must answer
// cleanly — calling the nil proxy here panicked and the guest's
// client died on the invalid response (user report 2026-11-13:
// guest client closed with a server-info error after the host
// readied).
func (s *Server) ownMatch(c *gin.Context, mid string) (game.State, *game.User, map[string]any, bool) {
	st, user, ok := s.loadState(c)
	if !ok {
		return nil, nil, nil, false
	}
	// The canonical live object wins over any per-state copy.
	if live, found := s.liveMatch(mid); found {
		st["match"] = live
		return st, user, live, true
	}
	if match := game.Map(st["match"]); match != nil && game.Str(match["id"]) == mid {
		s.regMatch(mid, user.ID, match)
		return st, user, match, true
	}
	ctx := c.Request.Context()
	mirror := func(ownerUID int64) (map[string]any, bool) {
		ost, err := game.LoadState(ctx, s.d.Store, ownerUID)
		if err != nil {
			return nil, false
		}
		m := game.Map(ost["match"])
		if m == nil || game.Str(m["id"]) != mid {
			return nil, false
		}
		s.regMatch(mid, ownerUID, m)
		st["match"] = m
		_ = game.SaveState(ctx, s.d.Store, user.ID, st)
		return m, true
	}
	if ownerUID, found := s.matchOwner(mid); found {
		if m, ok := mirror(ownerUID); ok {
			return st, user, m, true
		}
	} else if users, err := game.AllUsers(ctx, s.d.Store); err == nil {
		// Registry is cold (server restarted mid-match): scan.
		for _, u := range users {
			if u.ID == user.ID {
				continue
			}
			if m, ok := mirror(u.ID); ok {
				s.regMatchOwner(mid, u.ID)
				return st, user, m, true
			}
		}
	}
	if s.proxy != nil {
		s.proxy.ServeHTTP(c.Writer, c.Request)
		return nil, nil, nil, false
	}
	writeJSON(c, map[string]any{"result": false})
	return nil, nil, nil, false
}

func (s *Server) matchGet(c *gin.Context) {
	_, _, match, ok := s.ownMatch(c, c.Param("mid"))
	if !ok {
		return
	}
	writeJSON(c, match)
}

func (s *Server) matchSquad(c *gin.Context) {
	mid, sideName := c.Param("mid"), c.Param("side")
	st, user, match, ok := s.ownMatch(c, mid)
	if !ok {
		return
	}
	stage := game.Str(match["stage"])
	if stage != "created" && stage != "squad" && stage != "setting" {
		writeJSON(c, map[string]any{"result": false})
		return
	}
	side := game.Map(game.Map(match["side"])[sideName])
	if side == nil || len(game.List(side["players"])) == 0 {
		writeJSON(c, map[string]any{"result": false})
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	sq := game.Map(req["squad"])
	team := game.Map(side["team"])
	cards := map[string]map[string]any{}
	for _, pv := range game.List(team["players"]) {
		if p := game.Map(pv); p != nil {
			cards[game.IID(p)] = p
		}
	}
	positions := []any{}
	for _, ev := range game.List(sq["positions"]) {
		entry := game.Map(ev)
		pid := game.Str(entry["id"])
		posF, isNum := entry["pos"].(float64)
		if cards[pid] == nil || !isNum || posF != float64(int(posF)) || int(posF) < 0 || int(posF) > game.PosSub {
			writeJSON(c, map[string]any{"result": false})
			return
		}
		ne := map[string]any{}
		for k, v := range entry {
			ne[k] = v
		}
		ne["id"] = pid
		ne["spid"] = cards[pid]["spid"]
		positions = append(positions, ne)
	}
	starters, distinctPos, distinctIDs, hasGK := 0, map[int]bool{}, map[string]bool{}, false
	for _, pv := range positions {
		pos := game.Map(pv)
		distinctIDs[game.Str(pos["id"])] = true
		if game.Int(pos["pos"]) < game.PosSub {
			starters++
			distinctPos[game.Int(pos["pos"])] = true
			if game.Int(pos["pos"]) == 0 {
				hasGK = true
			}
		}
	}
	if starters != 11 || len(distinctPos) != 11 || len(distinctIDs) != len(positions) ||
		len(positions) > 18 || !hasGK {
		writeJSON(c, map[string]any{"result": false})
		return
	}
	teamSquad := game.Map(team["squad"])
	if teamSquad == nil {
		teamSquad = map[string]any{}
		team["squad"] = teamSquad
	}
	for _, k := range []string{"formation", "workrate"} {
		if v, has := sq[k]; has {
			teamSquad[k] = v
		}
	}
	teamSquad["positions"] = positions
	for _, key := range []string{"captain", "kickers"} {
		if v, has := req[key]; has {
			teamSquad[key] = v
		} else if v, has := sq[key]; has {
			teamSquad[key] = v
		}
	}
	s.d.Roster.RepairSquad(team)
	team["captain"] = teamSquad["captain"]
	team["kickers"] = teamSquad["kickers"]
	if game.List(team["teamcolors"]) == nil {
		team["teamcolors"] = []any{}
	}
	team["summary"] = s.d.Roster.TeamSummaryOf(team, game.Int(team["teamid"]))
	side["team"] = team
	side["squad"] = teamSquad
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/match/" + mid, "body": match}}})
}

func (s *Server) matchGamesettings(c *gin.Context) {
	mid := c.Param("mid")
	st, user, match, ok := s.ownMatch(c, mid)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	// TEMP PROBE (2026-11-14): the owner's real client sent 8
	// gamesettings POSTs in a PvE match that still rendered the
	// defaults; log the exact bodies to see what it sends.
	if probeJSON, err := json.Marshal(req); err == nil {
		log.Printf("gset-probe: mid=%s stage=%s human=%v body=%s gs_tod=%v gs_weather=%v",
			mid, game.Str(match["stage"]), match["_human"], probeJSON,
			game.Map(match["gamesettings"])["tod"], game.Map(match["gamesettings"])["weather"])
	}
	if game.Str(match["stage"]) != "setting" {
		writeJSON(c, map[string]any{"result": false})
		return
	}
	gs := game.Map(match["gamesettings"])
	if gs == nil {
		writeJSON(c, map[string]any{"result": false})
		return
	}
	done := func() {
		_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
		s.pushMatchToOther(c, mid, match, user)
		writeJSON(c, map[string]any{"result": true,
			"resources": []any{map[string]any{"uri": "/match/" + mid, "body": match}}})
	}

	// Kit selection: {side, kit} — per side, as fx had it.
	if _, hasKit := req["kit"]; hasKit {
		sideF, isNum := req["side"].(float64)
		kit := game.Str(req["kit"])
		if !isNum || (sideF != 0 && sideF != 1) || (kit != "home" && kit != "away") {
			writeJSON(c, map[string]any{"result": false})
			return
		}
		kits := game.List(gs["kit"])
		for len(kits) < 2 {
			kits = append(kits, "home")
		}
		kits[int(sideF)] = kit
		gs["kit"] = kits
		done()
		return
	}

	// Environment settings: the setting screens send these as
	// separate single-field PUTs — {tod}, {weather}, {stadium},
	// {ball:{id}}, {halfLength} (MatchController.setTOD /
	// setWeather / setStadium / setBall / setHalfLength). fx and
	// the first Go port only understood the kit body and
	// answered result:false to the rest, so the engine always
	// applied the defaults (daytime, clear) at StartSingleMatch
	// (user report 2026-11-14). In human matches only the HOME
	// side's choices count (owner's rule); in bot matches the
	// single human sets them from either side.
	if match["_human"] == true {
		myCID, _ := myCoachID(st)
		homeSide := "left"
		if h, isBool := gs["home"].(bool); isBool && !h {
			homeSide = "right"
		}
		onHome := false
		for _, pv := range game.List(game.Map(game.Map(match["side"])[homeSide])["players"]) {
			if game.Str(game.Map(pv)["id"]) == myCID && myCID != "" {
				onHome = true
			}
		}
		if !onHome {
			// The away side may not touch the environment
			// (stadium / time of day / weather / ball): those
			// belong to the home side (owner's rule 2026-11-15).
			// Silently accepting the send left the away screen
			// showing values the match would not use; reject it
			// so their client keeps the server's state. Kit picks
			// are handled above and stay per-side.
			writeJSON(c, map[string]any{"result": false})
			return
		}
	}
	applied := false
	// The setting screens send the stepper's selected INDEX
	// tagged with +10000 (client AS3, LeagueCupGameSettingScreen
	// configUI closures: MatchController.setTOD/setWeather/
	// setStadium(event.data + 10000), event.data = selectedIndex).
	// The values are DECODED BEFORE STORING. The client's own
	// display sync proves the stored domain must be the plain
	// index/id: onWeatherChanged switches on data.tod 0..4 and
	// sets the tod stepper to data.tod % 3, and onStadiumChanged
	// locates the stadium with findIndexOf(stadiumList, "id",
	// data) — fed the raw tagged 10000, the tod sync fell
	// through to the skin's default (Đêm) no matter what was
	// picked, and every arrow click re-sent 10000 (owner reports
	// + gset-probe 2026-11-14/15). Storing raw also let a tagged
	// stadium index (10001) reach the engine, which crashed
	// fifazf on match entry (crash dump 2026-11-15 02:27).
	// weather: the index IS the engine value (0/1/2 =
	// dry/rain/snow). tod: the index is NOT the value — the
	// client's tod tables (the onWeatherChanged switch and the
	// todWeatherAvailables keys "1"/"3") use 1 = DAY and
	// 3 = NIGHT, so stepper index 0/1 maps to tod 1/3. Storing
	// the raw index made every NIGHT pick (index 1) render as
	// DAY — the setting stuck on Ngày (owner report 2026-11-15;
	// probe: {"tod":10001} stored 1, displayed DAY). stadium:
	// the index is a position in /stadium/list order and is
	// mapped to that stadium's id, which is what the sync and
	// the engine use.
	env := func(key string) {
		if v, isNum := req[key].(float64); isNum {
			if v >= 10000 && v <= 10009 {
				gs[key] = int(v) - 10000
				applied = true
			} else if v >= 0 && v <= 9 {
				gs[key] = int(v)
				applied = true
			}
		}
	}
	if v, isNum := req["tod"].(float64); isNum {
		idx := -1
		if v >= 10000 && v <= 10009 {
			idx = int(v) - 10000
		} else if v >= 0 && v <= 9 {
			idx = int(v)
		}
		if idx >= 0 {
			if idx == 1 {
				gs["tod"] = 3
			} else {
				gs["tod"] = 1
			}
			applied = true
		}
	}
	env("weather")
	if v, isNum := req["stadium"].(float64); isNum {
		if v >= 10000 {
			if id, okID := s.d.Roster.StadiumIDAt(int(v) - 10000); okID {
				gs["stadium"] = id
				applied = true
			}
		} else if v >= 1 && v <= 2000 {
			gs["stadium"] = int(v)
			applied = true
		}
	}
	if v, isNum := req["halfLength"].(float64); isNum && v >= 10 && v <= 200 {
		gs["halfLength"] = int(v)
		applied = true
	}
	if b := game.Map(req["ball"]); b != nil {
		if id, isNum := b["id"].(float64); isNum && id >= 0 && id <= 100000 {
			// Only balls in the match's availables (the default
			// ball plus the home coach's purchased balls) may be
			// selected, and the availables list itself is
			// preserved — collapsing it to the picked ball would
			// leave the settings stepper with a single option.
			ball := game.Map(gs["ball"])
			if ball == nil {
				ball = map[string]any{"id": 0, "availables": []any{0}}
			}
			allowed := int(id) == 0
			for _, av := range game.List(ball["availables"]) {
				if game.Int(av) == int(id) {
					allowed = true
				}
				if m := game.Map(av); m != nil && game.Int(m["id"]) == int(id) {
					allowed = true
				}
			}
			if allowed {
				ball["id"] = int(id)
				gs["ball"] = ball
				applied = true
			}
		}
	}
	if !applied {
		writeJSON(c, map[string]any{"result": false})
		return
	}
	done()
}

// reqReady mirrors Python's bool(req.get("ready", True)).
func reqReady(req map[string]any) bool {
	v, has := req["ready"]
	if !has {
		return true
	}
	switch b := v.(type) {
	case bool:
		return b
	case nil:
		return false
	case float64:
		return b != 0
	}
	return true
}

func matchSetBothReady(match map[string]any, v bool) {
	side := game.Map(match["side"])
	if side == nil {
		return
	}
	if l := game.Map(side["left"]); l != nil {
		l["ready"] = v
	}
	if r := game.Map(side["right"]); r != nil {
		r["ready"] = v
	}
}

// reqStage mirrors Python's str(req.get("stage") or
// match.get("stage") or "squad").
func reqStage(req, match map[string]any) string {
	cur := game.Str(req["stage"])
	if cur == "" {
		cur = game.Str(match["stage"])
	}
	if cur == "" {
		cur = "squad"
	}
	return cur
}

// playerReadyStep ports fx._player_ready EXACTLY (the elif chain
// matters: branches fire on the request stage OR the match stage,
// each transition resets both sides' ready flags, and the
// "setting" step sets both ready=true as stage becomes "playing").
// The earlier simplified port skipped the ready resets — in room
// matches that left the AI side ready=true from birth, so the
// client never saw the opponent-ready transition it keys the
// match start on, and ranked stalled on the prep screen.
func playerReadyStep(match, req map[string]any) {
	stage := game.Str(match["stage"])
	cur := reqStage(req, match)
	switch {
	case cur == "created" && stage == "created":
		match["stage"] = "squad"
		matchSetBothReady(match, false)
	case cur == "squad" || stage == "squad":
		match["stage"] = "setting"
		matchSetBothReady(match, false)
	case cur == "setting" || stage == "setting":
		matchSetBothReady(match, true)
		match["stage"] = "playing"
		decodeEnvForEngine(match)
	default:
		rs := stage
		if v, has := req["stage"]; has {
			rs = game.Str(v)
		}
		if reqReady(req) && rs == stage {
			switch stage {
			case "created":
				match["stage"] = "squad"
			case "squad":
				match["stage"] = "setting"
			}
		}
	}
}

// sideReadyStep ports fx._side_ready EXACTLY: set my side's flag;
// on ready, force the right (AI) side ready too; at "squad" the
// stage advances to "setting" and BOTH flags reset; at "setting"
// the stage becomes "playing" with the flags as set (both true).
func sideReadyStep(match map[string]any, sideName string, req map[string]any) {
	readyVal := reqReady(req)
	stage := game.Str(match["stage"])
	cur := reqStage(req, match)
	side := game.Map(match["side"])
	if side == nil {
		return
	}
	if sd := game.Map(side[sideName]); sd != nil {
		sd["ready"] = readyVal
	}
	if !readyVal {
		return
	}
	if match["_human"] == true {
		// Human opponent: no forced ready (fx's force assumed the
		// right side is always the AI — with a human guest it made
		// the other player "ready" without their consent, user
		// report 2026-11-13). The stage advances only when BOTH
		// flags are set by their own clients.
		l := game.Map(side["left"])["ready"] == true
		r := game.Map(side["right"])["ready"] == true
		if l && r {
			switch stage {
			case "squad":
				match["stage"] = "setting"
				matchSetBothReady(match, false)
			case "setting":
				match["stage"] = "playing"
				decodeEnvForEngine(match)
			}
		}
		return
	}
	if r := game.Map(side["right"]); r != nil {
		r["ready"] = true
	}
	switch {
	case cur == "squad" || stage == "squad":
		match["stage"] = "setting"
		matchSetBothReady(match, false)
	case cur == "setting" || stage == "setting":
		match["stage"] = "playing"
		decodeEnvForEngine(match)
	}
}

// decodeEnvForEngine is a safety net for matches created before
// the decode-on-store fix in matchGamesettings: any environment
// value still carrying the +10000 wire tag at the setting ->
// playing transition is converted to a plain value the engine
// accepts (tod/weather: subtract the tag; stadium: fall back to
// the default 112 — a tagged stadium index reaching the engine
// crashed fifazf, dump 2026-11-15 02:27; the index->id mapping
// needs the roster, which these fx-parity step functions do not
// carry, and pre-fix in-flight matches are the only ones that
// can still hold a tagged stadium). Fresh matches store decoded
// values already, so this is a no-op for them.
func decodeEnvForEngine(match map[string]any) {
	gs := game.Map(match["gamesettings"])
	if gs == nil {
		return
	}
	if v := game.Int(gs["tod"]); v >= 10000 {
		// Tagged stepper index from a pre-decode-on-store match:
		// map it to the client's tod domain (index 1 = NIGHT = 3,
		// anything else DAY = 1).
		if v-10000 == 1 {
			gs["tod"] = 3
		} else {
			gs["tod"] = 1
		}
	} else if v == 0 {
		// The 0 default only existed for one build (bak-1942);
		// in the client's domain 0 is OVERCAST, which the
		// stadium availables never offer — play DAY instead.
		gs["tod"] = 1
	}
	if v := game.Int(gs["weather"]); v >= 10000 {
		gs["weather"] = v - 10000
	}
	if v := game.Int(gs["stadium"]); v >= 10000 {
		gs["stadium"] = 112
	}
}

// pushMatchUpdate pushes the match (+stage) to the OTHER player
// when a ready step changed the stage, so their client transitions
// together with the caller's (previously only the caller learned
// of the change in the HTTP response; the peer's client sat in the
// prep screen until its own next call).
// matchOtherCoachID resolves the coach id of the OTHER party in
// a human match ("" for bot matches / unknown matches).
func (s *Server) matchOtherCoachID(c *gin.Context, mid string, caller *game.User) string {
	match, found := s.liveMatch(mid)
	if !found || match["_human"] != true {
		return ""
	}
	ownerUID, found := s.matchOwner(mid)
	if !found {
		return ""
	}
	if ownerUID != caller.ID {
		ost, err := game.LoadState(c.Request.Context(), s.d.Store, ownerUID)
		if err != nil {
			return ""
		}
		other, _ := myCoachID(ost)
		return other
	}
	return game.Str(game.Map(match["_opp_coach"])["coachid"])
}

func (s *Server) pushMatchUpdate(c *gin.Context, mid string, match map[string]any, caller *game.User, prevStage string) {
	if game.Str(match["stage"]) == prevStage || match["_human"] != true {
		return
	}
	s.pushMatchToOther(c, mid, match, caller)
}

// pushMatchToOther pushes the canonical match (+ its stage
// subresource) to the OTHER party of a human match. The ready
// handlers call it on EVERY ready click: a ready flag that does
// not yet flip the stage must still reach the peer's prep
// screen, which renders the opponent's ready checkmark from
// this resource — pushing only on stage changes left the peer
// seeing no ready state until both had readied (user report
// 2026-11-14).
// matchParticipantCIDs lists the coach ids in a match's
// playing sides, left first — the session's user order.
func matchParticipantCIDs(match map[string]any) []string {
	side := game.Map(match["side"])
	var out []string
	for _, n := range []string{"left", "right"} {
		for _, pv := range game.List(game.Map(side[n])["players"]) {
			if id := game.Str(game.Map(pv)["id"]); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

func (s *Server) pushMatchToOther(c *gin.Context, mid string, match map[string]any, caller *game.User) {
	if match["_human"] != true {
		return
	}
	ctx := c.Request.Context()
	callerCID := ""
	if cst, err := game.LoadState(ctx, s.d.Store, caller.ID); err == nil {
		callerCID, _ = myCoachID(cst)
	}
	for _, cid := range matchParticipantCIDs(match) {
		if cid == callerCID {
			continue
		}
		game.MessengerPush("/match/"+mid, match, cid)
		game.MessengerPush("/match/"+mid+"/stage", game.Str(match["stage"]), cid)
	}
}

// pushRelayServer delivers the match's relayServer block as its
// own resource at /match/<id>/relayServer. RE (MatchProxy in
// Module.swf): onMatchChangeReceived calls
// fetchMatchRelayServerResource, which reads that URI from the
// resource cache into _relayServerInfo; the relaySessionInfo
// getter then hands it to the native ConnectSession call. If the
// sibling resource was never delivered, relaySessionInfo's
// relayServer is "" and the engine dies with an access violation
// in recvfrom at match load — both clients, every human match
// (user reports 2026-11-13 19:59 + 20:07). Bot matches never
// noticed: their session is local. Human matches only — the bot
// side's players == [] is the discriminator (Bug 2c invariant).
func (s *Server) pushRelayServer(mid string, match map[string]any, coachID string) {
	if coachID == "" || mid == "" {
		return
	}
	right := game.Map(game.Map(match["side"])["right"])
	if len(game.List(right["players"])) == 0 {
		return
	}
	relay := game.Map(match["relayServer"])
	if relay == nil {
		return
	}
	game.MessengerPush("/match/"+mid+"/relayServer", relay, coachID)
}

func (s *Server) matchPlayerReady(c *gin.Context) {
	mid := c.Param("mid")
	st, user, match, ok := s.ownMatch(c, mid)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	prev := game.Str(match["stage"])
	if match["_human"] == true {
		// Human matches: player/ready's only legitimate job is the
		// created->squad step, and it takes BOTH players. fx's elif
		// chain advances on the match-stage disjunct, so with two
		// human callers the second player/ready (plus any retry)
		// jumps created->squad->setting inside a second and the
		// squad prep screen is never shown (user report 2026-11-14,
		// match 602043362: 3 player/ready calls in 1s, squads auto-
		// submitted at defaults). Bot matches never noticed — one
		// human caller only. The squad->setting and setting->playing
		// steps belong to side/ready's mutual-consent branch, so
		// player/ready is a no-op at every later stage here.
		if prev == "created" {
			// The gate is per COACH: the stage advances only
			// once every participant has sent their own
			// player/ready. With one coach per side this is
			// exactly the old both-sides rule; in 2v2/3v3 a
			// side is not dragged forward by its leader alone.
			myCID, _ := myCoachID(st)
			pr := game.Map(match["_pready"])
			if pr == nil {
				pr = map[string]any{}
				match["_pready"] = pr
			}
			pr[myCID] = true
			parts := matchParticipantCIDs(match)
			all := len(parts) > 0
			for _, cid := range parts {
				if pr[cid] != true {
					all = false
				}
			}
			if all {
				match["stage"] = "squad"
				matchSetBothReady(match, false)
				delete(match, "_pready")
			}
		}
	} else {
		playerReadyStep(match, req)
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	s.pushMatchToOther(c, mid, match, user)
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/match/" + mid, "body": match}}})
}

func (s *Server) matchSideReady(c *gin.Context) {
	mid, sideName := c.Param("mid"), c.Param("side")
	st, user, match, ok := s.ownMatch(c, mid)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	sideReadyStep(match, sideName, req)
	_ = game.SaveState(c.Request.Context(), s.d.Store, user.ID, st)
	s.pushMatchToOther(c, mid, match, user)
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"uri": "/match/" + mid, "body": match}}})
}

// matchEnd ports fx's ep_match_end (which shadows hc's): contracts,
// reward, career counters, multiplay records, ranked settlement,
// cup/league pending resolution and the weekly-quest GP hook.
// anyCompetitiveSettled reports whether some party already
// settled this match through a competitive path — the signal
// that a later party's /end (arriving after the first call
// closed the stage) belongs to a match that was really played.
// No-contest marks (Phase B2) do not count: a fabricated prep
// /end must not "unlock" a second fabricated settlement.
func anyCompetitiveSettled(match map[string]any) bool {
	nc := game.Map(match["_nocontest_uids"])
	for uid := range game.Map(match["_settled_uids"]) {
		if nc == nil || nc[uid] != true {
			return true
		}
	}
	return false
}

func (s *Server) matchEnd(c *gin.Context) {
	mid := c.Param("mid")
	st, user, match, ok := s.ownMatch(c, mid)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	// TEMP PROBE (2026-11-14): a PvE league quit/kill was
	// recorded as a 5-0 WIN for the quitter; log the exact /end
	// body of non-human matches to see who the client declares
	// the forfeiter and with what scores.
	if match["_human"] != true {
		if probeJSON, err := json.Marshal(req); err == nil {
			log.Printf("end-probe: mid=%s stage=%s body=%s",
				mid, game.Str(match["stage"]), probeJSON)
		}
	}
	// Forfeit bookkeeping. Two ways a human match becomes a
	// forfeit: the relay/sweeper marks _forfeit when it detects a
	// dead engine, or the quitting client itself posts /end with
	// forfeits>0 (the in-game menu quit — verified 2026-11-14:
	// the quitter's history records 0-5, forfeits=1). In both
	// cases: never settle the quitter twice, and the winner —
	// who keeps playing against the computer locally and posts
	// their own /end at the whistle — is recorded with the
	// official 5-0 whatever the played score was.
	callerSlot := 0
	if ownerUID, found := s.matchOwner(mid); found && user.ID != ownerUID {
		callerSlot = 1
	}
	if game.Map(match["_forfeit"]) == nil && match["_human"] == true &&
		game.Int(req["forfeits"]) > 0 {
		match["_forfeit"] = map[string]any{
			"loser_slot": callerSlot, "loser_settled": false, "ghost": false}
	}
	if f := game.Map(match["_forfeit"]); f != nil {
		if callerSlot == game.Int(f["loser_slot"]) {
			if f["loser_settled"] == true {
				writeJSON(c, map[string]any{"result": true,
					"resources": []any{map[string]any{"uri": "/match/" + mid, "body": match}}})
				return
			}
		} else {
			myTeam := "home"
			if callerSlot != 0 {
				myTeam = "away"
			}
			nb := endBody(myTeam, 5, 0, 0)
			req["home"], req["away"] = nb["home"], nb["away"]
			req["myTeam"], req["opponentTeam"] = nb["myTeam"], nb["opponentTeam"]
			req["forfeits"] = 0
		}
	}
	// --- Phase B2 settlement guards --------------------------------
	// competitive: this settlement may move records and pay
	// rewards only if the match genuinely reached the pitch
	// (stage "playing"), or it arrives through a forfeit path
	// (the quitter's own /end with forfeits>0, or a _forfeit the
	// relay/sweeper marked), or another party already settled
	// this match (per-party settlement: the first /end closes
	// the stage, so a later party's stage reads "closed" even
	// though the match was really played). A bare /end posted
	// during the prep stages — the shape of a fabricated result —
	// settles as a no-contest below: the caller is marked
	// settled and the match closes, but no history, elo, career
	// stats or EP move. The real client's cancel path never
	// settles at all, so no legitimate flow needs prep-stage
	// settlement.
	competitive := game.Str(match["stage"]) == "playing" ||
		game.Int(req["forfeits"]) > 0 || game.Map(match["_forfeit"]) != nil ||
		anyCompetitiveSettled(match)
	// Score sanity: both sides present, scores and shootouts in
	// a plausible range. Anything else is a malformed or forged
	// body and settles nothing at all.
	scoreOK := true
	for _, sd := range []string{"home", "away"} {
		sm := game.Map(req[sd])
		if sm == nil {
			scoreOK = false
			break
		}
		if _, has := sm["score"]; !has {
			scoreOK = false
			break
		}
		if v := game.Int(sm["score"]); v < 0 || v > 30 {
			scoreOK = false
			break
		}
		if v := game.Int(sm["shootout"]); v < 0 || v > 30 {
			scoreOK = false
			break
		}
	}
	if !scoreOK {
		game.Audit(ctx, s.d.Store, "system", "settle.invalid_score", mid,
			map[string]any{"uid": user.ID, "stage": game.Str(match["stage"])})
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_SCORE"})
		return
	}
	if !competitive {
		game.Audit(ctx, s.d.Store, "system", "settle.no_contest", mid,
			map[string]any{"uid": user.ID, "stage": game.Str(match["stage"]),
				"type": game.Str(match["type"])})
		log.Printf("settle: no-contest mid=%s uid=%d stage=%s type=%s",
			mid, user.ID, game.Str(match["stage"]), game.Str(match["type"]))
	}
	lg := game.Map(st["league2"])
	pending := game.Map(lg["pending"])
	out := map[string]any{"result": true}
	rankedFinished := false
	score := func(side string) int { return game.Int(game.Map(req[side])["score"]) }
	// Human matches settle PER PARTY: both clients post /end and
	// the first call closes the stage, so a stage-only gate would
	// silently skip the second party's rewards/history (e.g. the
	// winner finishing locally after a menu quit). PvE keeps the
	// original stage gate.
	settleNow := game.Str(match["stage"]) != "closed"
	if match["_human"] == true {
		settled := game.Map(match["_settled_uids"])
		settleNow = settled == nil || settled[fmt.Sprintf("%d", user.ID)] != true
	}
	if settleNow && !competitive {
		// No-contest: mark only this party settled so the match
		// can close cleanly; nothing else moves.
		if match["_human"] == true {
			su := game.Map(match["_settled_uids"])
			if su == nil {
				su = map[string]any{}
				match["_settled_uids"] = su
			}
			su[fmt.Sprintf("%d", user.ID)] = true
			nc := game.Map(match["_nocontest_uids"])
			if nc == nil {
				nc = map[string]any{}
				match["_nocontest_uids"] = nc
			}
			nc[fmt.Sprintf("%d", user.ID)] = true
		}
	} else if settleNow {
		game.ConsumeMatchContracts(st)
		game.ConsumeStaffContracts(st)
		result := map[string]any{}
		for k, v := range req {
			if k != "_method" {
				result[k] = v
			}
		}
		match["result"] = result
		match["reward"] = s.d.Roster.MatchReward(st, req)
		meName := game.Str(req["myTeam"])
		if game.Str(match["type"]) == "Club_Friendly" {
			// Fold the result into both clubs' records,
			// once per match (the recorder guards itself).
			s.recordClubFriendlyOnce(ctx, match, req, meName)
		}
		if meName == "" {
			meName = "home"
		}
		oppName := game.Str(req["opponentTeam"])
		if oppName == "" {
			oppName = "away"
		}
		car := game.Map(st["career"])
		if car == nil {
			car = map[string]any{}
			st["career"] = car
		}
		car["total_goals"] = game.Int(car["total_goals"]) + score(meName)
		mtype := game.Str(match["type"])
		if mtype == "Ranked_PvP_1v1" {
			car["ranked_goals"] = game.Int(car["ranked_goals"]) + score(meName)
			if score(oppName) == 0 {
				car["ranked_cleansheets"] = game.Int(car["ranked_cleansheets"]) + 1
			}
			if score(meName) > score(oppName) && game.Int(req["forfeits"]) == 0 {
				car["ranked_curr_streak"] = game.Int(car["ranked_curr_streak"]) + 1
				if game.Int(car["ranked_curr_streak"]) > game.Int(car["ranked_best_streak"]) {
					car["ranked_best_streak"] = car["ranked_curr_streak"]
				}
			} else {
				car["ranked_curr_streak"] = 0
			}
		} else if strings.Contains(mtype, "MGR") {
			car["mgr_goals"] = game.Int(car["mgr_goals"]) + score(meName)
		}
		if strings.HasPrefix(mtype, "Ranked") || strings.HasPrefix(mtype, "Friendly") {
			coach := game.CoachBySlot(st, game.DefaultSlot(st))
			// The opponent is the side opposite the caller's. Reading
			// "right" unconditionally recorded the caller as their own
			// opponent whenever they were the guest (right side).
			oppSide := game.Map(game.Map(match["side"])["right"])
			if meName == "away" || meName == "right" {
				oppSide = game.Map(game.Map(match["side"])["left"])
			}
			right := oppSide
			oppCoaches := []any{}
			if oppPlayers := game.List(right["players"]); len(oppPlayers) > 0 {
				op := game.Map(oppPlayers[0])
				cid := game.Str(op["_id"])
				if cid == "" {
					cid = game.Str(op["id"])
				}
				oppCoaches = []any{map[string]any{"coachid": cid, "name": game.Str(op["name"])}}
			} else if oc := game.Map(match["_opp_coach"]); oc != nil {
				oppCoaches = []any{oc}
			}
			oppTid := game.Int(right["teamid"])
			if oppTid == 0 {
				oppTid = game.Int(game.Map(right["team"])["teamid"])
			}
			if oppTid == 0 {
				oppTid = game.Int(right["authTeamId"])
			}
			myScore, oppScore, forfeits := score(meName), score(oppName), 0
			if game.Int(req["forfeits"]) > 0 {
				myScore, oppScore, forfeits = 0, 5, 1
			}
			if coach != nil {
				entry := map[string]any{
					"type": mtype, "date": game.W3C(float64(time.Now().Unix())), "forfeits": forfeits,
					"team": map[string]any{"teamid": game.Int(coach["teamid"]), "score": myScore,
						"shootout": game.Int(game.Map(req[meName])["shootout"]),
						"coaches":  []any{map[string]any{"coachid": game.Str(coach["_id"]), "name": game.Str(coach["name"])}}},
					"opponent": map[string]any{"teamid": oppTid, "score": oppScore,
						"shootout": game.Int(game.Map(req[oppName])["shootout"]),
						"clubname": "", "coaches": oppCoaches},
				}
				hist := append(game.List(coach["multiplay"]), entry)
				if len(hist) > 20 {
					hist = hist[len(hist)-20:]
				}
				coach["multiplay"] = hist
			}
		}
		if match["_ranked"] == true {
			rankedFinished = true
			key := "ranked"
			if strings.Contains(strings.ToLower(mtype), "mgr") {
				key = "ranked_mgr"
			}
			rk := game.RankedState(st, key)
			forfeited := game.Int(req["forfeits"]) > 0
			won := !forfeited && score(meName) > score(oppName)
			drew := !forfeited && score(meName) == score(oppName)
			delta := game.CfgI("elo_loss", -15)
			if won {
				delta = game.CfgI("elo_win", 25)
			} else if drew {
				delta = game.CfgI("elo_draw", 5)
			}
			rk["elo"] = max(0, game.Int(rk["elo"])+delta)
			if game.Int(rk["elo"]) > game.Int(rk["highelo"]) {
				rk["highelo"] = rk["elo"]
			}
			if won {
				rk["wins"] = game.Int(rk["wins"]) + 1
				rk["streak"] = game.Int(rk["streak"]) + 1
			} else if drew {
				rk["draws"] = game.Int(rk["draws"]) + 1
				rk["streak"] = 0
			} else {
				rk["losses"] = game.Int(rk["losses"]) + 1
				rk["streak"] = 0
			}
			resVal := -1
			if won {
				resVal = 1
			} else if drew {
				resVal = 0
			}
			results := append(game.List(rk["results"]), resVal)
			if len(results) > 100 {
				results = results[len(results)-100:]
			}
			rk["results"] = results
			if forfeited {
				rk["forfeits"] = game.Int(rk["forfeits"]) + 1
			}
			isoYear, isoWeek := time.Now().ISOWeek()
			weekKey := fmt.Sprintf("%04d%02d", isoYear, isoWeek)
			ww := game.Map(rk["weekwins"])
			if ww == nil || game.Str(ww["week"]) != weekKey {
				ww = map[string]any{"week": weekKey, "wins": 0}
			}
			if won {
				ww["wins"] = game.Int(ww["wins"]) + 1
			}
			rk["weekwins"] = ww
			hist := game.List(rk["elos"])
			today := time.Now().Format("20060102")
			point := map[string]any{"elo": game.Int(rk["elo"]), "dateymd": today}
			if len(hist) > 0 && game.Str(game.Map(hist[len(hist)-1])["dateymd"]) == today {
				hist[len(hist)-1] = point
			} else {
				hist = append(hist, point)
			}
			if len(hist) > 60 {
				hist = hist[len(hist)-60:]
			}
			rk["elos"] = hist
			if !drew {
				recent := game.RecentServed(rk)
				prevStreak := 0
				if len(recent) > 0 {
					prevStreak = game.Int(game.Map(recent[len(recent)-1])["streak"])
				}
				streak := 0
				if won {
					if prevStreak > 0 {
						streak = prevStreak + 1
					} else {
						streak = 1
					}
				} else {
					if prevStreak < 0 {
						streak = prevStreak - 1
					} else {
						streak = -1
					}
				}
				goals, conceded := score(meName), score(oppName)
				if forfeited {
					goals, conceded = 0, 5
				}
				recent = append(recent, map[string]any{
					"date": game.W3C(float64(time.Now().Unix())), "goals": goals,
					"goalsconceded": conceded, "shootout": 0, "shootoutconceded": 0,
					"streak": streak, "elodiff": delta, "elo": game.Int(rk["elo"]),
					"forfeits": boolToInt(forfeited)})
				if len(recent) > 3 {
					recent = recent[len(recent)-3:]
				}
				rk["recent"] = recent
			}
			money := game.Map(game.Map(match["reward"])["money"])
			reward := game.Map(money["reward"])
			bonus := int(float64(game.CfgI("ranked_bonus_ep", 500000))*game.CfgF("progression_mult", 1) + 0.5)
			reward["total"] = game.Int(reward["total"]) + bonus
			detail := game.Map(reward["detail"])
			detail["extra"] = game.Int(detail["extra"]) + bonus
			money["current"] = game.Int(money["current"]) + bonus
		}
		money0 := game.Map(game.Map(match["reward"])["money"])
		grantEP := int64(game.Int(game.Map(money0["reward"])["total"]))
		if match["_ranked"] == true {
			// Flag implausibly fast ranked settlements for the
			// owner's review (rewards still pay in this pass).
			if ct, perr := time.Parse("2006-01-02T15:04:05.000Z", game.Str(match["created"])); perr == nil {
				if el := time.Since(ct); el < 120*time.Second {
					game.Audit(ctx, s.d.Store, "system", "settle.fast_ranked", mid,
						map[string]any{"uid": user.ID, "elapsed_sec": int(el.Seconds())})
				}
			}
			// Optional daily soft cap on ranked EP (gameplay
			// setting ranked_daily_ep_cap; 0 = unlimited, the
			// default, so behavior is unchanged until the owner
			// sets it). The granted amount replaces the reward
			// total so the client display and the credit agree.
			if capEP := int64(game.CfgI("ranked_daily_ep_cap", 0)); capEP > 0 && grantEP > 0 {
				dayKey := "rankedep:" + fmt.Sprintf("%d", user.ID) + ":" + time.Now().Format("20060102")
				already, _ := s.d.Store.Redis.Get(ctx, dayKey).Int64()
				allowed := grantEP
				if already >= capEP {
					allowed = 0
				} else if already+grantEP > capEP {
					allowed = capEP - already
				}
				if allowed != grantEP {
					game.Audit(ctx, s.d.Store, "system", "settle.ep_capped", mid,
						map[string]any{"uid": user.ID, "wanted": grantEP, "granted": allowed, "cap": capEP})
					grantEP = allowed
					game.Map(money0["reward"])["total"] = int(grantEP)
				}
				if grantEP > 0 {
					_, _ = s.d.Store.Redis.IncrBy(ctx, dayKey, grantEP).Result()
					s.d.Store.Redis.Expire(ctx, dayKey, 48*time.Hour)
				}
			}
		}
		if match["_ranked"] != true && grantEP > 0 {
			// Non-ranked twin of the ranked cap above (gameplay
			// setting match_daily_ep_cap; 0 = unlimited): one shared
			// daily counter across room/league/cup match ends and
			// simulated league rounds, so a bot looping bot-matches
			// hits the same wall as a ranked farmer.
			if granted, capped := game.ClampMatchDailyEP(ctx, s.d.Store, user.ID, grantEP); capped {
				game.Audit(ctx, s.d.Store, "system", "settle.ep_capped", mid,
					map[string]any{"uid": user.ID, "wanted": grantEP, "granted": granted,
						"cap": game.CfgI("match_daily_ep_cap", 0), "kind": "match"})
				grantEP = granted
				game.Map(money0["reward"])["total"] = int(grantEP)
			}
		}
		game.AddEP(st, grantEP)
		if match["_human"] == true {
			su := game.Map(match["_settled_uids"])
			if su == nil {
				su = map[string]any{}
				match["_settled_uids"] = su
			}
			su[fmt.Sprintf("%d", user.ID)] = true
			if f := game.Map(match["_forfeit"]); f != nil &&
				callerSlot == game.Int(f["loser_slot"]) {
				f["loser_settled"] = true
			}
		}
	}
	if cp := game.Map(st["cup_pending"]); competitive && cp != nil && game.Str(cp["id"]) == mid {
		if cups := game.Map(st["cups"]); cups != nil && game.Map(cups[game.Str(cp["cupid"])]) != nil {
			h, a := game.Map(req["home"]), game.Map(req["away"])
			gh, ga := game.Int(h["score"]), game.Int(a["score"])
			sh, sa := game.Int(h["shootout"]), game.Int(a["shootout"])
			if game.Str(h["id"]) == game.Str(cp["away"]) {
				gh, ga, sh, sa = ga, gh, sa, sh
			}
			if game.Int(req["forfeits"]) > 0 {
				// Caller forfeited: official 0-5 against the
				// caller in fixture orientation (see the
				// league block below for the full story).
				if game.Int(cp["home"]) == game.MyTeamID(st) {
					gh, ga, sh, sa = 0, 5, 0, 0
				} else {
					gh, ga, sh, sa = 5, 0, 0, 0
				}
			}
			game.Map(cups[game.Str(cp["cupid"])])["_mine"] = []any{gh, ga, sh, sa}
			delete(st, "cup_pending")
		}
	}
	// TEMP PROBE (2026-11-15, remove after the league-record
	// diagnosis): log the league gate's inputs on every /end.
	log.Printf("endgate-probe: mid=%s uid=%d comp=%v lgNil=%v pendingNil=%v pendID=%q pendRound=%v",
		mid, user.ID, competitive, lg == nil, pending == nil,
		game.Str(pending["id"]), pending["round"])
	if competitive && pending != nil && game.Str(pending["id"]) == mid {
		h, a := game.Map(req["home"]), game.Map(req["away"])
		gh, ga := game.Int(h["score"]), game.Int(a["score"])
		if game.Str(h["id"]) == game.Str(pending["away"]) {
			gh, ga = ga, gh
		}
		if game.Int(req["forfeits"]) > 0 {
			// The caller forfeited (their own /end with
			// forfeits>0, or the sweeper's synthesized body
			// after a process kill). The league table must
			// record the official 0-5 AGAINST the caller.
			// Taking the raw body scores instead recorded
			// the quitter as the 5-0 winner whenever they
			// were the fixture's away team: the sweeper's
			// body carries no side ids, so the orientation
			// swap above never fired (owner report
			// 2026-11-14: two killed league matches were
			// recorded as 5-0 wins, one as a 0-0 draw).
			if game.Int(pending["home"]) == game.MyTeamID(st) {
				gh, ga = 0, 5
			} else {
				gh, ga = 5, 0
			}
		}
		game.RecordLeagueMatchPlayers(st, req, fmt.Sprintf("league:%d", game.Int(pending["round"])))
		out["leagueparam"] = s.d.Roster.LeaguePlayRound(st, game.Int(pending["round"]), &[2]int{gh, ga}, nil)
		delete(lg, "pending")
		out["resources"] = s.d.Roster.L2Resources(st)
	}
	match["stage"] = "closed"
	resList := []any{map[string]any{"uri": "/match/" + mid, "body": match}}
	if prev, isList := out["resources"].([]any); isList {
		resList = append(resList, prev...)
	}
	if rankedFinished {
		resList = append(resList, s.d.Roster.CoachMeResources(st, game.DefaultSlot(st), user)...)
		elos := game.AllElos(ctx, s.d.Store)
		resList = append(resList, map[string]any{"uri": "/ladder/my/info",
			"body": game.LadderStatistics(st, elos)})
	}
	out["resources"] = resList
	game.QuestState(s.d.GamedataDir, st)
	_ = game.SaveState(ctx, s.d.Store, user.ID, st)
	writeJSON(c, out)
}

func (s *Server) matchCampaign(c *gin.Context) {
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	slot := game.DefaultSlot(st)
	me := s.d.Roster.CoachMe(st, slot, user)
	teamid := game.Int(me["teamid"])
	ai := 243
	if teamid == 243 {
		ai = 241
	}
	userObj := map[string]any{"id": me["id"], "name": me["name"], "levelinfo": me["levelinfo"]}
	match := game.NewMatch("League_league",
		s.d.Roster.MatchSide(game.TeamOf(st, slot), teamid, userObj, s.d.Roster.TacticsOf(st)),
		s.d.Roster.MatchSide(s.d.Roster.RosterTeam(ai), ai, nil, nil), nil)
	game.ApplyBallAvailables(match, s.d.Roster.BallAvailables(st))
	writeJSON(c, map[string]any{"result": true,
		"resources": []any{map[string]any{"resource": "/match/" + game.Str(match["id"]),
			"uri": "/match/" + game.Str(match["id"]), "body": match}}})
}

// ---------- in-match chat (fx ep_match_send_message) ----------

var matchMessages = struct {
	sync.Mutex
	byID map[string][]map[string]any
}{byID: map[string][]map[string]any{}}

func (s *Server) matchSendMessage(c *gin.Context) {
	mid := c.Param("mid")
	st, user, ok := s.loadState(c)
	if !ok {
		return
	}
	var req map[string]any
	_ = c.ShouldBindJSON(&req)
	coach := game.CoachBySlot(st, game.DefaultSlot(st))
	name := "HLV"
	if coach != nil && game.Str(coach["name"]) != "" {
		name = game.Str(coach["name"])
	}
	target := game.Str(req["target"])
	if target == "" {
		target = "all"
	}
	coachID, _ := myCoachID(st)
	base := map[string]any{"type": "chat",
		"target": map[bool]string{true: "", false: target}[target == "all"],
		"coachname": name, "body": strings.TrimSpace(game.Str(req["message"]))}
	stored := map[string]any{}
	for k, v := range base {
		stored[k] = v
	}
	stored["myside"] = true
	stored["coachid"] = coachID // internal: per-requester myside in latest
	matchMessages.Lock()
	msgs := append(matchMessages.byID[mid], stored)
	if len(msgs) > 50 {
		msgs = msgs[len(msgs)-50:]
	}
	matchMessages.byID[mid] = msgs
	matchMessages.Unlock()
	voMine := map[string]any{}
	for k, v := range base {
		voMine[k] = v
	}
	voMine["myside"] = true
	game.MessengerPush("/match/"+mid+"/messages/latest", []any{voMine}, coachID)
	// The opponent must receive the line too, rendered from their
	// side (myside=false). Until now only the sender got a push,
	// so prep/in-match chat was invisible to the other coach
	// (user report 2026-11-14).
	if live, found := s.liveMatch(mid); found {
		// Fan out to every other participant, rendered from
		// the RECIPIENT's perspective: teammates see the line
		// as their own side's (myside=true), opponents as the
		// other side's. In 1v1 this is the single opponent
		// push above's exact equivalent.
		sideOf := map[string]string{}
		for _, n := range []string{"left", "right"} {
			for _, pv := range game.List(game.Map(game.Map(live["side"])[n])["players"]) {
				sideOf[game.Str(game.Map(pv)["id"])] = n
			}
		}
		for cid, n := range sideOf {
			if cid == "" || cid == coachID {
				continue
			}
			vo := map[string]any{}
			for k, v := range base {
				vo[k] = v
			}
			vo["myside"] = n == sideOf[coachID]
			game.MessengerPush("/match/"+mid+"/messages/latest", []any{vo}, cid)
		}
	} else if oppCID := s.matchOtherCoachID(c, mid, user); oppCID != "" {
		voOpp := map[string]any{}
		for k, v := range base {
			voOpp[k] = v
		}
		voOpp["myside"] = false
		game.MessengerPush("/match/"+mid+"/messages/latest", []any{voOpp}, oppCID)
	}
	writeJSON(c, map[string]any{"result": true})
}

func (s *Server) matchMessagesLatest(c *gin.Context) {
	mid := c.Param("mid")
	matchMessages.Lock()
	msgs := matchMessages.byID[mid]
	matchMessages.Unlock()
	if len(msgs) == 0 {
		writeJSON(c, []any{})
		return
	}
	last := msgs[len(msgs)-1]
	out := map[string]any{}
	for k, v := range last {
		out[k] = v
	}
	delete(out, "coachid")
	if st, _, ok := s.loadState(c); ok {
		if cid, _ := myCoachID(st); cid != "" {
			out["myside"] = game.Str(last["coachid"]) == cid
		}
	}
	writeJSON(c, []any{out})
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
