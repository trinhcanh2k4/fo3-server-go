package game

// Coach creation / deletion lifecycle (hc ep_create_coach,
// ep_new_team, ep_coach_reserve, ep_coach_postponed,
// purge_deleted_coaches). Slots are 0 and 1.

import (
	"strings"
	"time"
)

// CoachNameValid ports hc.coach_name_valid: "" when the name is
// usable, else the client reason ("invalid name" / "busy").
func CoachNameValid(st State, name string) string {
	trimmed := strings.TrimSpace(name)
	if len([]rune(trimmed)) < 2 || len([]rune(trimmed)) > 12 {
		return "invalid name"
	}
	for _, r := range trimmed {
		if r < 32 {
			return "invalid name"
		}
	}
	for _, c := range Coaches(st) {
		if strings.EqualFold(Str(c["name"]), trimmed) {
			return "busy"
		}
	}
	return ""
}

// NewManagerTeam ports hc.new_manager_team: a generated team for
// teamid, keeping the chosen spids first and filling the squad to
// 18 from the generated pool.
func (r *Roster) NewManagerTeam(teamid int, chosen []any) map[string]any {
	gt := r.GenTeam(teamid, 0, false)
	team := deepCopyMap(Map(gt["team"]))
	if len(chosen) > 0 {
		want := map[int]bool{}
		for _, cv := range chosen {
			if cm := Map(cv); cm != nil {
				want[Int(cm["spid"])] = true
			} else if n := Int(cv); n != 0 {
				want[n] = true
			}
		}
		players := List(team["players"])
		picked := map[int]bool{}
		selected := []any{}
		for i, pv := range players {
			if p := Map(pv); p != nil && want[Int(p["spid"])] {
				selected = append(selected, pv)
				picked[i] = true
			}
		}
		for i, pv := range players {
			if len(selected) >= 18 {
				break
			}
			if !picked[i] {
				selected = append(selected, pv)
			}
		}
		team["players"] = selected
	}
	return team
}

// PurgeDeletedCoaches ports hc.purge_deleted_coaches: postponed
// coaches whose restore window has passed are dropped with their
// team (and the account-wide contexts when the default goes).
func PurgeDeletedCoaches(st State) bool {
	now := float64(time.Now().Unix())
	kept := []any{}
	changed := false
	for _, cv := range List(st["coaches"]) {
		c := Map(cv)
		if c != nil && Str(c["state"]) == "postpone" {
			if del, ok := c["deleteat"].(float64); ok && del <= now {
				if teams := Map(st["teams"]); teams != nil {
					delete(teams, itoa(Int(c["slot"])))
				}
				if Str(st["defaultcoachid"]) == Str(c["_id"]) {
					st["defaultcoachid"] = ""
					for _, k := range []string{"league2", "cups", "offers", "match", "trade"} {
						delete(st, k)
					}
				}
				changed = true
				continue
			}
		}
		kept = append(kept, cv)
	}
	if changed {
		st["coaches"] = kept
	}
	return changed
}
