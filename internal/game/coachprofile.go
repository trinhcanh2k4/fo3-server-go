package game

// Cross-coach profile helpers, ported from fx (_calc_team_value,
// _slot_of_coach, _recorded_coach_identity).

import (
	"context"

	"fo3server/internal/store"
)

// SlotOfCoach is fx._slot_of_coach.
func SlotOfCoach(s State, cid string) int {
	for _, coach := range Coaches(s) {
		if Str(coach["_id"]) == cid {
			return Int(coach["slot"])
		}
	}
	return DefaultSlot(s)
}

// RecordedIdentity is fx._recorded_coach_identity: (name, teamid) of a
// non-account coach id found in MY OWN multiplay records.
func RecordedIdentity(myState State, cid string) (string, int) {
	slot := DefaultSlot(myState)
	coach := CoachBySlot(myState, slot)
	if coach == nil {
		return "", 0
	}
	for _, raw := range List(coach["multiplay"]) {
		e := Map(raw)
		for _, sideKey := range []string{"team", "opponent"} {
			side := Map(e[sideKey])
			for _, craw := range List(side["coaches"]) {
				co := Map(craw)
				if Str(co["coachid"]) == cid {
					name := Str(co["name"])
					if name == "" {
						name = "HLV"
					}
					return name, Int(side["teamid"])
				}
			}
		}
	}
	return "", 0
}

// ClubMemberIDs returns the coach ids of a club's members.
func ClubMemberIDs(ctx context.Context, st *store.Store, clubID string) (map[string]bool, error) {
	rows, err := st.DB.QueryContext(ctx,
		"SELECT coach_id FROM club_members WHERE clubid = ?", clubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out, rows.Err()
}
