package api

import (
	"context"
	"log"
	"sync"
	"time"

	"fo3server/internal/game"
)

// Forfeit sweeper: the quit→0-5-loss mechanism for the stages
// the UDP relay cannot see (user requirement 2026-11-14 — the
// mechanism must also cover quitting at the squad-prep and
// kit-prep screens, and quitting a PvE match).
//
// Every 15s it scans the live match registry (human matches) and
// every user's current-match slot (PvE matches, which may never
// enter the registry). A party counts as gone only when the
// messenger-authoritative presence says offline on TWO
// consecutive scans with the match stage unchanged — a single
// hiccup or a brief alt-tab never settles anything. Human
// matches already "playing" are skipped: the relay owns those
// (10s silence detection + ghost). Settlement reuses
// settleForfeit / callMatchEnd, i.e. the real matchEnd path.

type sweepObs struct {
	stage string
	off   map[int64]int // uid -> consecutive offline scans
}

var sweep = struct {
	sync.Mutex
	once sync.Once
	obs  map[string]*sweepObs
}{obs: map[string]*sweepObs{}}

func (s *Server) liveMatchIDs() []string {
	if s.mo == nil {
		return nil
	}
	s.mo.mu.Lock()
	defer s.mo.mu.Unlock()
	ids := make([]string, 0, len(s.mo.live))
	for mid := range s.mo.live {
		ids = append(ids, mid)
	}
	return ids
}

func (s *Server) startForfeitSweeper() {
	sweep.once.Do(func() {
		go func() {
			for range time.Tick(15 * time.Second) {
				s.sweepOnce()
			}
		}()
	})
}

var lastMarketSweep time.Time

// sweepMarketExpiry flips live listings older than 24h to
// "expired" (the DB row plus the seller's offer entry). The player
// stays escrowed: the seller can re-list the offer
// (POST /transfer/offer/from/:offerid) or cancel it to take the
// player back. Throttled to once a minute inside the 15s tick.
func (s *Server) sweepMarketExpiry(ctx context.Context) {
	if time.Since(lastMarketSweep) < time.Minute {
		return
	}
	lastMarketSweep = time.Now()
	game.EnsureMarketTables(ctx, s.d.Store)
	cutoff := time.Now().Unix() - 86400
	rows, err := s.d.Store.DB.QueryContext(ctx,
		`SELECT id, seller_uid FROM market_listings
		 WHERE status = 'waiting' AND created_at < ? LIMIT 200`, cutoff)
	if err != nil {
		return
	}
	type exp struct {
		id  string
		uid int64
	}
	var exps []exp
	for rows.Next() {
		var e exp
		if err := rows.Scan(&e.id, &e.uid); err == nil {
			exps = append(exps, e)
		}
	}
	rows.Close()
	for _, e := range exps {
		if _, err := s.d.Store.DB.ExecContext(ctx,
			`UPDATE market_listings SET status = 'expired' WHERE id = ? AND status = 'waiting'`, e.id); err != nil {
			continue
		}
		sst, err := game.LoadState(ctx, s.d.Store, e.uid)
		if err != nil {
			continue
		}
		changed := false
		for _, ov := range game.List(sst["offers"]) {
			if o := game.Map(ov); o != nil && game.Str(o["_id"]) == e.id &&
				game.Str(o["status"]) == "waiting" {
				o["status"] = "expired"
				changed = true
			}
		}
		if changed {
			_ = game.SaveState(ctx, s.d.Store, e.uid, sst)
		}
	}
}

func (s *Server) coachOnline(ctx context.Context, uid int64) (bool, *game.User) {
	u, err := game.UserByID(ctx, s.d.Store, uid)
	if err != nil || u == nil {
		return false, nil
	}
	st, err := game.LoadState(ctx, s.d.Store, uid)
	if err != nil {
		return false, u
	}
	cid, _ := myCoachID(st)
	if cid == "" {
		return false, u
	}
	return game.IsCoachOnline(ctx, s.d.Store, cid, u.Username), u
}

// observe records one presence sample for (mid, uid) and returns
// the consecutive-offline count. The stage must match the
// previous observation or the counters reset.
func observe(mid, stage string, uid int64, online bool) int {
	sweep.Lock()
	defer sweep.Unlock()
	o, ok := sweep.obs[mid]
	if !ok || o.stage != stage {
		o = &sweepObs{stage: stage, off: map[int64]int{}}
		sweep.obs[mid] = o
	}
	if online {
		delete(o.off, uid)
		return 0
	}
	o.off[uid]++
	return o.off[uid]
}

func forgetObs(mid string) {
	sweep.Lock()
	delete(sweep.obs, mid)
	sweep.Unlock()
}

var prepStages = map[string]bool{"created": true, "squad": true, "setting": true}

func (s *Server) sweepOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	s.sweepMarketExpiry(ctx)
	seen := map[string]bool{}

	// --- human matches: the live registry is authoritative ---
	for _, mid := range s.liveMatchIDs() {
		match, found := s.liveMatch(mid)
		if !found {
			continue
		}
		stage := game.Str(match["stage"])
		if stage == "closed" || game.Map(match["_forfeit"]) != nil {
			forgetObs(mid)
			continue
		}
		if match["_human"] != true {
			continue // PvE handled by the state scan below
		}
		if len(matchParticipantCIDs(match)) > 2 {
			// Multi matches (2v2/3v3): this sweeper settles a
			// whole match 0-5 between two parties — with 4-6
			// parties that would punish everyone still
			// playing. Per-quitter settlement in multi is the
			// relay's job (in-play silence detection); a
			// prep-stage abandonment in a multi match stays
			// unsettled in this foundation.
			continue
		}
		seen[mid] = true
		if !prepStages[stage] {
			continue // playing: the relay owns quit detection
		}
		ownerUID, found := s.matchOwner(mid)
		if !found {
			continue
		}
		opp := s.userByCoachID(ctx, game.Str(game.Map(match["_opp_coach"])["coachid"]))
		if opp == nil {
			continue
		}
		ownerOnline, _ := s.coachOnline(ctx, ownerUID)
		oppOnline, _ := s.coachOnline(ctx, opp.ID)
		ownerOff := observe(mid, stage, ownerUID, ownerOnline)
		oppOff := observe(mid, stage, opp.ID, oppOnline)
		switch {
		case ownerOff >= 2 && oppOnline:
			log.Printf("fo3-sweeper: human match %s abandoned at %s by owner (uid %d); settling 0-5 forfeit", mid, stage, ownerUID)
			s.settleForfeit(ctx, mid, 0, false)
			forgetObs(mid)
		case oppOff >= 2 && ownerOnline:
			log.Printf("fo3-sweeper: human match %s abandoned at %s by guest (uid %d); settling 0-5 forfeit", mid, stage, opp.ID)
			s.settleForfeit(ctx, mid, 1, false)
			forgetObs(mid)
		}
	}

	// --- PvE matches: scan each user's current-match slot ---
	users, err := game.AllUsers(ctx, s.d.Store)
	if err != nil {
		return
	}
	for _, u := range users {
		st, err := game.LoadState(ctx, s.d.Store, u.ID)
		if err != nil {
			continue
		}
		m := game.Map(st["match"])
		if m == nil {
			continue
		}
		mid := game.Str(m["id"])
		stage := game.Str(m["stage"])
		if mid == "" || stage == "closed" || game.Map(m["_forfeit"]) != nil {
			forgetObs(mid)
			continue
		}
		if m["_human"] == true {
			continue // registry path above owns human matches
		}
		if stage != "playing" && !prepStages[stage] {
			continue
		}
		seen[mid] = true
		if _, found := s.liveMatch(mid); !found {
			s.regMatch(mid, u.ID, m)
		}
		online, _ := s.coachOnline(ctx, u.ID)
		if off := observe(mid, stage, u.ID, online); off >= 2 {
			team := ownerTeamOf(m, st)
			log.Printf("fo3-sweeper: PvE match %s abandoned at %s by uid %d; settling 0-5 forfeit loss", mid, stage, u.ID)
			if s.callMatchEnd(ctx, mid, u, endBody(team, 0, 5, 1)) {
				if live, found := s.liveMatch(mid); found {
					live["stage"] = "closed"
				}
			}
			forgetObs(mid)
		}
	}

	// Drop observations for matches that vanished entirely.
	sweep.Lock()
	for mid := range sweep.obs {
		if !seen[mid] {
			if _, found := s.liveMatch(mid); !found {
				delete(sweep.obs, mid)
			}
		}
	}
	sweep.Unlock()
}

// ownerTeamOf reports which side ("home"/"away") the match
// owner's coach sits on: a human side's players[0] is the user
// object carrying the coach id (game.MatchSide).
func ownerTeamOf(match map[string]any, st game.State) string {
	cid, _ := myCoachID(st)
	if cid == "" {
		return "home"
	}
	sides := game.Map(match["side"])
	for _, kv := range []struct{ key, team string }{{"left", "home"}, {"right", "away"}} {
		players := game.List(game.Map(sides[kv.key])["players"])
		if len(players) > 0 && game.Str(game.Map(players[0])["id"]) == cid {
			return kv.team
		}
	}
	return "home"
}
