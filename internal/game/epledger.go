package game

// EP ledger + non-ranked daily match-EP cap (economy guardrails,
// 2026-11-15). The ledger is an audit trail for abuse hunting and
// dispute tracing, not double-entry accounting: on every core-state
// save, the default coach's EP is compared with the last balance
// recorded in Redis (epbal:<uid>) and the difference is appended to
// the ep_ledger table with the request path as its source. Because
// the hook sits in SaveState, it catches every EP mutation no
// matter which handler produced it (matches, shop, market, quests,
// staff, admin grants...). A missing baseline (first save after
// deploy, or a lost Redis key) is adopted silently so the ledger
// never invents a delta.

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"fo3server/internal/store"
)

type epSourceKey struct{}

// WithEPSource tags a request context with the label the EP ledger
// records for state saves made under it ("METHOD /path").
func WithEPSource(ctx context.Context, src string) context.Context {
	return context.WithValue(ctx, epSourceKey{}, src)
}

func epSource(ctx context.Context) string {
	if v, ok := ctx.Value(epSourceKey{}).(string); ok && v != "" {
		return v
	}
	return "internal"
}

var epLedgerOnce sync.Once

func ensureEPLedger(ctx context.Context, st *store.Store) {
	epLedgerOnce.Do(func() {
		_, err := st.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS ep_ledger (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			user_id BIGINT NOT NULL,
			delta BIGINT NOT NULL,
			balance BIGINT NOT NULL,
			source VARCHAR(128) NOT NULL DEFAULT '',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_user_time (user_id, created_at),
			INDEX idx_time (created_at))`)
		if err != nil {
			log.Printf("ep-ledger: create table failed: %v", err)
		}
	})
}

// RecordEPDelta is called by SaveState after a successful save.
func RecordEPDelta(ctx context.Context, st *store.Store, userID int64, s State) {
	coach := CoachBySlot(s, DefaultSlot(s))
	if coach == nil {
		return
	}
	newBal := Int64(coach["ep"])
	key := fmt.Sprintf("epbal:%d", userID)
	oldStr, err := st.Redis.Get(ctx, key).Result()
	if err != nil {
		_ = st.Redis.Set(ctx, key, newBal, 0).Err()
		return
	}
	var old int64
	if _, serr := fmt.Sscanf(oldStr, "%d", &old); serr != nil || old == newBal {
		if serr == nil {
			return
		}
		_ = st.Redis.Set(ctx, key, newBal, 0).Err()
		return
	}
	ensureEPLedger(ctx, st)
	src := epSource(ctx)
	if len(src) > 128 {
		src = src[:128]
	}
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO ep_ledger (user_id, delta, balance, source) VALUES (?, ?, ?, ?)",
		userID, newBal-old, newBal, src); err != nil {
		log.Printf("ep-ledger: insert failed for user %d: %v", userID, err)
	}
	_ = st.Redis.Set(ctx, key, newBal, 0).Err()
}

// ClampMatchDailyEP applies the gameplay setting match_daily_ep_cap
// (0 = off) to a non-ranked match EP grant. All non-ranked faucets
// (room/league/cup match ends, simulated league rounds) share the
// Redis daily counter matchep:<uid>:<yyyymmdd>, mirroring the
// ranked cap's rankedep: counter. Returns the amount that may be
// granted and whether the cap cut it. The caller credits exactly
// the returned amount.
func ClampMatchDailyEP(ctx context.Context, st *store.Store, uid int64, want int64) (int64, bool) {
	capEP := int64(CfgI("match_daily_ep_cap", 0))
	if capEP <= 0 || want <= 0 {
		return want, false
	}
	dayKey := "matchep:" + fmt.Sprintf("%d", uid) + ":" + time.Now().Format("20060102")
	already, _ := st.Redis.Get(ctx, dayKey).Int64()
	allowed := want
	if already >= capEP {
		allowed = 0
	} else if already+want > capEP {
		allowed = capEP - already
	}
	if allowed > 0 {
		_, _ = st.Redis.IncrBy(ctx, dayKey, allowed).Result()
		st.Redis.Expire(ctx, dayKey, 48*time.Hour)
	}
	return allowed, allowed != want
}
