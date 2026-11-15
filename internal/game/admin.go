package game

// Helpers for the Admin API: the settings table, admin seeding of
// a fresh coach, and price overrides wired into the shop.

import (
	"context"
	"encoding/json"
	"fmt"

	"fo3server/internal/store"
)

// SettingGet reads one settings row (JSON text) by name.
func SettingGet(ctx context.Context, st *store.Store, name string) (string, bool) {
	var v string
	err := st.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE name = ?", name).Scan(&v)
	return v, err == nil
}

// SettingPut writes one settings row.
func SettingPut(ctx context.Context, st *store.Store, name, value string) error {
	_, err := st.DB.ExecContext(ctx,
		"INSERT INTO settings (name, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value)",
		name, value)
	return err
}

// SettingAll lists every settings row.
func SettingAll(ctx context.Context, st *store.Store) (map[string]string, error) {
	rows, err := st.DB.QueryContext(ctx, "SELECT name, value FROM settings ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var n, v string
		if err := rows.Scan(&n, &v); err == nil {
			out[n] = v
		}
	}
	return out, rows.Err()
}

// Audit writes one admin_audit row.
func Audit(ctx context.Context, st *store.Store, admin, action, target string, detail any) {
	b, _ := json.Marshal(detail)
	_, _ = st.DB.ExecContext(ctx,
		"INSERT INTO admin_audit (admin, action, target, detail) VALUES (?, ?, ?, ?)",
		admin, action, target, string(b))
}

// MaintenanceOn reports the admin maintenance switch.
func MaintenanceOn(ctx context.Context, st *store.Store) (bool, string) {
	v, ok := SettingGet(ctx, st, "maintenance")
	if !ok {
		return false, ""
	}
	var m map[string]any
	if json.Unmarshal([]byte(v), &m) != nil {
		return false, ""
	}
	msg := Str(m["message"])
	if msg == "" {
		msg = "Server đang bảo trì, vui lòng quay lại sau."
	}
	return m["on"] == true, msg
}

// priceOverrides reads a {itemid: price} JSON map setting.
func priceOverrides(ctx context.Context, st *store.Store, name string) map[int]int64 {
	out := map[int]int64{}
	v, ok := SettingGet(ctx, st, name)
	if !ok {
		return out
	}
	raw := map[string]int64{}
	if json.Unmarshal([]byte(v), &raw) == nil {
		for k, val := range raw {
			var id int
			if _, err := fmt.Sscan(k, &id); err == nil {
				out[id] = val
			}
		}
	}
	return out
}

// ShopPriceOverrides / GPPriceOverrides expose the admin overrides.
func ShopPriceOverrides(ctx context.Context, st *store.Store) map[int]int64 {
	return priceOverrides(ctx, st, "shop_price_overrides")
}

func GPPriceOverrides(ctx context.Context, st *store.Store) map[int]int64 {
	return priceOverrides(ctx, st, "gp_price_overrides")
}

// ShopHiddenIDs returns the admin's recalled-from-shop item ids
// (settings doc "shop_hidden" {"itemids": [...]}), or the
// built-in DefaultShopHidden list when no doc exists.
func ShopHiddenIDs(ctx context.Context, st *store.Store) []int {
	if v, ok := SettingGet(ctx, st, "shop_hidden"); ok {
		var doc struct {
			ItemIDs []int `json:"itemids"`
		}
		if json.Unmarshal([]byte(v), &doc) == nil && doc.ItemIDs != nil {
			return doc.ItemIDs
		}
	}
	return DefaultShopHidden
}

// SeedNewCoach builds a fresh state with one generated team for a
// newly created account (the seedcoaches recipe).
func (r *Roster) SeedNewCoach(uid int64, coachName string, teamid int) State {
	st := DefaultEmptyState()
	gt := r.GenTeam(teamid, 0, false)
	team := Map(gt["team"])
	team["teamid"] = teamid
	coach := map[string]any{
		"_id": CoachIDForUser(uid, 0), "slot": 0, "name": coachName, "teamid": teamid,
		"state": "active", "ep": StartEP, "createdat": "2026-11-01T00:00:00.000Z",
		"currentbadge": 1001, "items": []any{}, "multiplay": []any{},
		"record": map[string]any{}, "profile": map[string]any{
			"description": "Xin chào! Chào mừng đến với FIFA Online 3.", "picture": 0},
		"levelinfo": map[string]any{"level": 1, "exp": 0, "maxexp": CoachNeed(1)},
	}
	st["coaches"] = []any{coach}
	st["teams"] = map[string]any{"0": team}
	return st
}

// CoachSummary is the admin list/detail view of one coach.
func (r *Roster) CoachSummary(st State, slot int, user *User) map[string]any {
	coach := CoachBySlot(st, slot)
	if coach == nil {
		return nil
	}
	me := r.CoachMe(st, slot, user)
	rk := RankedState(st, "ranked")
	rkm := RankedState(st, "ranked_mgr")
	return map[string]any{
		"slot": slot, "id": Str(coach["_id"]), "name": Str(coach["name"]),
		"teamid": Int(coach["teamid"]),
		"teamname": Str(r.clubTeams[Int(coach["teamid"])]["teamname"]),
		"level":    Int(Map(coach["levelinfo"])["level"]),
		"ep": Int64(coach["ep"]), "gp": StoredGP(st),
		"elo": Int(rk["elo"]), "elo_mgr": Int(rkm["elo"]),
		"wins": Int(rk["wins"]), "draws": Int(rk["draws"]), "losses": Int(rk["losses"]),
		"badge": Int(coach["currentbadge"]),
		"teamvalue":    Int64(Map(me["teamvalue"])["val"]),
		"players":      len(List(Map(me["team"])["players"])),
		"items":        len(List(coach["items"])),
		"clubid":       Str(coach["clubid"]),
	}
}
