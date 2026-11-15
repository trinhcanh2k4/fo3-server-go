package game

// Runtime settings wired from the admin "gameplay" and
// "feature_flags" settings documents (Phase 3 of the admin
// matrix: constants the owner can change without a code deploy).
// The server refreshes these caches from MariaDB every minute;
// every consumer falls back to the historical constant when a key
// is absent, so an empty settings table == today's behavior.

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"fo3server/internal/store"
)

var gameplayNums atomic.Value  // map[string]float64
var featureFlags atomic.Value  // map[string]bool

// CfgF reads a numeric gameplay setting with a default.
func CfgF(key string, def float64) float64 {
	if v := gameplayNums.Load(); v != nil {
		if n, ok := v.(map[string]float64)[key]; ok {
			return n
		}
	}
	return def
}

// CfgI is CfgF for integer settings.
func CfgI(key string, def int) int { return int(CfgF(key, float64(def))) }

// Flag reads a boolean feature flag with a default.
func Flag(key string, def bool) bool {
	if v := featureFlags.Load(); v != nil {
		if b, ok := v.(map[string]bool)[key]; ok {
			return b
		}
	}
	return def
}

// RefreshRuntimeSettings reloads both caches from the settings
// table (called at startup and every minute by the server).
func RefreshRuntimeSettings(ctx context.Context, st *store.Store) {
	if v, ok := SettingGet(ctx, st, "gameplay"); ok {
		raw := map[string]float64{}
		if json.Unmarshal([]byte(v), &raw) == nil {
			gameplayNums.Store(raw)
		}
	}
	if v, ok := SettingGet(ctx, st, "feature_flags"); ok {
		raw := map[string]bool{}
		if json.Unmarshal([]byte(v), &raw) == nil {
			featureFlags.Store(raw)
		}
	}
	if v, ok := SettingGet(ctx, st, "seasons"); ok {
		raw := map[string]string{}
		if json.Unmarshal([]byte(v), &raw) == nil {
			seasonDates.Store(raw)
		}
	}
}

// GameplayDefaults documents every wired key (the admin UI seeds
// its editor from this list).
var GameplayDefaults = map[string]float64{
	"elo_win":           25,
	"elo_draw":          5,
	"elo_loss":          -15,
	"ranked_bonus_ep":   500000,
	"reward_win_ep":     50000,
	"reward_draw_ep":    25000,
	"reward_loss_ep":    12500,
	"progression_mult":  1,
	"sale_fee_pct":      10,
	"igr_refund":        0.2,
	"vip_refund":        0.3,
	"contract_fee_rate": 0.13,
	"club_fee":          10000,
	"club_expand_fee":   5000,
}

var seasonDates atomic.Value // map[string]string

// SeasonDate returns an admin-configured season boundary
// ("YYYY-MM-DD") for kind "ladder" or "cup", which is "start" or
// "end". Falls back to the historical defaults when unset.
func SeasonDate(kind, which string) string {
	def := map[string]string{
		"ladder_start": "2026-11-01", "ladder_end": "2026-12-31",
		"cup_start": "2026-11-01", "cup_end": "2026-12-31",
	}[kind+"_"+which]
	if v := seasonDates.Load(); v != nil {
		if s, ok := v.(map[string]string)[kind+"_"+which]; ok && s != "" {
			return s
		}
	}
	return def
}

// Entitlements: PC (IGR) and VIP are per-USER grants with an
// expiry, stored on the state doc as {vip_until, pc_until} in
// milliseconds. Nobody has either by default (the owner revoked
// the historical always-on VIP/PC on 2026-11-13); only the admin
// panel grants them, for a set duration.
func EntitlementUntil(st State, kind string) int64 {
	e := Map(st["entitlements"])
	if e == nil {
		return 0
	}
	return Int64(e[kind+"_until"])
}

func VIPActive(st State) bool {
	return EntitlementUntil(st, "vip") > timeNowMs()
}

func PCActive(st State) bool {
	return EntitlementUntil(st, "pc") > timeNowMs()
}

func timeNowMs() int64 {
	return time.Now().UnixMilli()
}
