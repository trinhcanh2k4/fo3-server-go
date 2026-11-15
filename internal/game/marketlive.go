package game

// Live player-to-player transfer market (owner directive
// 2026-11-15): the NPC market is gone — no system-generated offers,
// no instant system buy-back. Coaches list real players, other
// coaches buy them, and a daily reset re-derives each card's
// reference-price demand factor from the previous day's REAL sale
// prices (median, clamped to +/-10% per reset, wash trades between
// accounts sharing an IP excluded), so hot cards keep rising a
// step at a time while speculation spikes are flattened. The
// factor feeds CardPrice, so the market, the trade exchanges and
// enhancement all share the same stabilized reference.
//
// Anti-sniping (owner directive 2026-11-15, evening): a fixed
// 00:00 repricing is a speculator's appointment, so HOT cards
// (>= 10 sales in the window, or demand strong enough to hit the
// +10% clamp) do not change price at the reset itself — their new
// factor activates at a random moment 0-12h later (apply_at on
// market_price_adj; MarketFactors only serves due rows). The
// window always closes before the next reset, so steps never
// stack up unapplied.

import (
	"context"
	"log"
	mrand "math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"fo3server/internal/store"
)

// LiveAgg is one (spid, grade) live-market aggregate: the cheapest
// waiting listing's price and how many listings exist.
type LiveAgg struct {
	Price int64
	Count int
}

// LiveAggregates reads every waiting listing grouped by spid+grade.
func LiveAggregates(ctx context.Context, st *store.Store) (map[int]map[int]LiveAgg, error) {
	out := map[int]map[int]LiveAgg{}
	rows, err := st.DB.QueryContext(ctx,
		`SELECT spid, grade, MIN(price), COUNT(*) FROM market_listings
		 WHERE status = 'waiting' GROUP BY spid, grade`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var spid, grade, count int
		var price int64
		if err := rows.Scan(&spid, &grade, &price, &count); err != nil {
			continue
		}
		if out[spid] == nil {
			out[spid] = map[int]LiveAgg{}
		}
		out[spid][grade] = LiveAgg{Price: price, Count: count}
	}
	return out, nil
}

// LiveTop is TransferTop against live listings only: the 50
// highest-rated spids that actually have a listing, each shown
// with its cheapest listing across grades.
func (r *Roster) LiveTop(position string, agg map[int]map[int]LiveAgg) []any {
	rows, err := r.queryPlayers("")
	if err != nil {
		return []any{}
	}
	rng, hasLine := marketLines[strings.ToUpper(position)]
	type cand struct{ spid, rating int }
	cands := []cand{}
	for _, row := range rows {
		if _, ok := agg[row.Spid]; !ok {
			continue
		}
		if hasLine && (row.PrefPos1 < rng[0] || row.PrefPos1 >= rng[1]) {
			continue
		}
		cands = append(cands, cand{row.Spid, row.Rating()})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].rating != cands[j].rating {
			return cands[i].rating > cands[j].rating
		}
		return cands[i].spid < cands[j].spid
	})
	out := []any{}
	for i := 0; i < len(cands) && i < 50; i++ {
		byGrade := agg[cands[i].spid]
		var best *LiveAgg
		bestGrade, total := 0, 0
		for g, a := range byGrade {
			total += a.Count
			if best == nil || a.Price < best.Price {
				aa := a
				best = &aa
				bestGrade = g
			}
		}
		if best == nil {
			continue
		}
		out = append(out, map[string]any{"spid": cands[i].spid, "expandme": "player",
			"grade": bestGrade, "price": best.Price, "count": total})
	}
	return out
}

var marketTablesOnce sync.Once

// EnsureMarketTables creates the live-market tables on first use.
func EnsureMarketTables(ctx context.Context, st *store.Store) {
	marketTablesOnce.Do(func() {
		stmts := []string{
			`CREATE TABLE IF NOT EXISTS market_listings (
				id VARCHAR(32) PRIMARY KEY,
				seller_uid BIGINT NOT NULL,
				spid INT NOT NULL,
				grade INT NOT NULL,
				price BIGINT NOT NULL,
				player MEDIUMTEXT NOT NULL,
				status VARCHAR(16) NOT NULL DEFAULT 'waiting',
				buyer_uid BIGINT NULL,
				created_at BIGINT NOT NULL,
				sold_at BIGINT NULL,
				INDEX idx_spid_status (spid, grade, status, price),
				INDEX idx_seller (seller_uid, status),
				INDEX idx_status_time (status, created_at))`,
			`CREATE TABLE IF NOT EXISTS market_price_adj (
				spid INT PRIMARY KEY,
				factor DOUBLE NOT NULL DEFAULT 1,
				apply_at BIGINT NULL,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP)`,
			`ALTER TABLE market_price_adj ADD COLUMN IF NOT EXISTS apply_at BIGINT NULL`,
			`CREATE TABLE IF NOT EXISTS market_sales (
				id BIGINT AUTO_INCREMENT PRIMARY KEY,
				spid INT NOT NULL,
				grade INT NOT NULL,
				price BIGINT NOT NULL,
				seller_uid BIGINT NOT NULL,
				buyer_uid BIGINT NOT NULL,
				seller_ip VARCHAR(64) NOT NULL DEFAULT '',
				buyer_ip VARCHAR(64) NOT NULL DEFAULT '',
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				INDEX idx_spid_time (spid, created_at))`,
		}
		for _, q := range stmts {
			if _, err := st.DB.ExecContext(ctx, q); err != nil {
				log.Printf("market: create table failed: %v", err)
			}
		}
	})
}

// MarketFactors loads the spid -> demand factor map. Rows with a
// future apply_at (hot cards staggered after the reset) are not
// live yet and stay invisible here until their moment comes.
func MarketFactors(ctx context.Context, st *store.Store) map[int]float64 {
	out := map[int]float64{}
	rows, err := st.DB.QueryContext(ctx,
		"SELECT spid, factor FROM market_price_adj WHERE apply_at IS NULL OR apply_at <= ?",
		time.Now().Unix())
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var spid int
		var f float64
		if err := rows.Scan(&spid, &f); err == nil && f > 0 {
			out[spid] = f
		}
	}
	return out
}

// ict is Vietnam time (fixed UTC+7, no DST) for market day edges.
var ict = time.FixedZone("ICT", 7*3600)

// RunMarketReset recomputes demand factors from the last 24h of
// real sales. Returns how many spids moved.
func RunMarketReset(ctx context.Context, st *store.Store, r *Roster) int {
	EnsureMarketTables(ctx, st)
	rows, err := st.DB.QueryContext(ctx,
		`SELECT spid, grade, price, seller_uid, buyer_uid, seller_ip, buyer_ip
		 FROM market_sales WHERE created_at >= NOW() - INTERVAL 24 HOUR`)
	if err != nil {
		log.Printf("market reset: sales query failed: %v", err)
		return 0
	}
	defer rows.Close()
	bySpid := map[int][]float64{}
	for rows.Next() {
		var spid, grade int
		var price, sellerUID, buyerUID int64
		var sellerIP, buyerIP string
		if err := rows.Scan(&spid, &grade, &price, &sellerUID, &buyerUID, &sellerIP, &buyerIP); err != nil {
			continue
		}
		// Wash trades never move prices: same account, or two
		// accounts trading from one IP.
		if sellerUID == buyerUID || (sellerIP != "" && sellerIP == buyerIP) {
			continue
		}
		if grade < 0 || grade >= len(gradePrice) || gradePrice[grade] <= 0 {
			continue
		}
		bySpid[spid] = append(bySpid[spid], float64(price)/gradePrice[grade])
	}
	moved, staggered := 0, 0
	resetAt := time.Now().Unix()
	for spid, bases := range bySpid {
		if len(bases) < 3 {
			continue // too thin to trust: one or two sales are noise
		}
		sort.Float64s(bases)
		median := bases[len(bases)/2]
		if len(bases)%2 == 0 {
			median = (bases[len(bases)/2-1] + bases[len(bases)/2]) / 2
		}
		ref := r.CardBaseRef(spid)
		if ref <= 0 {
			continue
		}
		rawRatio := median / ref
		ratio := rawRatio
		if ratio > 1.10 {
			ratio = 1.10
		}
		if ratio < 0.90 {
			ratio = 0.90
		}
		newFactor := r.MarketFactor(spid) * ratio
		if newFactor > 5 {
			newFactor = 5
		}
		if newFactor < 0.2 {
			newFactor = 0.2
		}
		newFactor = float64(int(newFactor*10000+0.5)) / 10000
		if newFactor == r.MarketFactor(spid) {
			continue
		}
		// Hot cards (heavy volume, or demand pinned at the +10%
		// clamp) activate at a random moment within 12h instead
		// of at the reset itself, so nobody can camp the reset
		// hour for the cards that matter.
		var applyAt any
		if len(bases) >= 10 || rawRatio >= 1.10 {
			applyAt = resetAt + mrand.Int63n(12*3600)
			staggered++
		}
		if _, err := st.DB.ExecContext(ctx,
			`INSERT INTO market_price_adj (spid, factor, apply_at) VALUES (?, ?, ?)
			 ON DUPLICATE KEY UPDATE factor = VALUES(factor), apply_at = VALUES(apply_at)`,
			spid, newFactor, applyAt); err != nil {
			log.Printf("market reset: factor update failed for %d: %v", spid, err)
			continue
		}
		moved++
	}
	if moved > 0 {
		r.SetMarketFactors(MarketFactors(ctx, st))
	}
	Audit(ctx, st, "system", "market.reset", "",
		map[string]any{"moved": moved, "staggered": staggered, "spids_with_sales": len(bySpid)})
	log.Printf("market reset: %d spid factors moved (%d staggered hot, %d spids had sales)",
		moved, staggered, len(bySpid))
	return moved
}

// MarketResetTick runs the daily reset once per ICT day, at/after
// the gameplay setting market_reset_hour (default 0 = midnight).
// Called every minute by the server; the Redis marker makes it
// fire exactly once even across restarts.
func MarketResetTick(ctx context.Context, st *store.Store, r *Roster) {
	now := time.Now().In(ict)
	if now.Hour() < CfgI("market_reset_hour", 0) {
		return
	}
	today := now.Format("2006-01-02")
	last, _ := st.Redis.Get(ctx, "market:lastreset").Result()
	if last == today {
		return
	}
	// Claim the day first so a slow reset never double-fires
	// (SetNX: only one process wins, even a scratch instance).
	claimed, err := st.Redis.SetNX(ctx, "market:lastreset", today, 72*time.Hour).Result()
	if err != nil || !claimed {
		return
	}
	RunMarketReset(ctx, st, r)
}
