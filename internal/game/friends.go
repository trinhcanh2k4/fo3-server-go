package game

// Friends/contacts domain, ported from fx section (ep_contact_*,
// _build_contact_list_for_coach, _friend_pair, notify_messenger_push).
// Pairs live in the MariaDB friends table; groups live in the user
// state (contact_groups), exactly as fx keeps them.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"fo3server/internal/store"
)

type FriendRow struct {
	FriendID   string
	FriendName string
	GroupName  string
	Status     string
	CreatedAt  string
}

func FriendRows(ctx context.Context, st *store.Store, coachID string) ([]FriendRow, error) {
	rows, err := st.DB.QueryContext(ctx,
		"SELECT friend_coach_id, friend_name, group_name, status, created_at FROM friends WHERE coach_id = ? ORDER BY created_at, friend_coach_id", coachID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FriendRow
	for rows.Next() {
		var r FriendRow
		var name, group, created *string
		if err := rows.Scan(&r.FriendID, &name, &group, &r.Status, &created); err != nil {
			return nil, err
		}
		if name != nil {
			r.FriendName = *name
		}
		if group != nil {
			r.GroupName = *group
		}
		if created != nil {
			r.CreatedAt = *created
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func ContactGroups(s State) []string {
	var out []string
	for _, g := range List(s["contact_groups"]) {
		out = append(out, Str(g))
	}
	return out
}

// ContactList builds fx._build_contact_list_for_coach for the owner of
// coachID. idx is the cross-account coach index (may be nil).
func ContactList(ctx context.Context, st *store.Store, idx map[string]*ClubMember,
	ownerState State, coachID string) map[string]any {
	groups := ContactGroups(ownerState)
	groupSet := map[string]bool{}
	for _, g := range groups {
		groupSet[g] = true
	}
	friends := []any{}
	requested := []any{}
	requesting := []any{}
	rows, _ := FriendRows(ctx, st, coachID)
	for _, r := range rows {
		status := r.Status
		if status == "" {
			status = "accepted"
		}
		switch status {
		case "accepted":
			level, elo, online := 1, 1000, false
			if idx != nil {
				if info := idx[r.FriendID]; info != nil {
					if info.Level > 0 {
						level = info.Level
					}
					if info.Elo > 0 {
						elo = info.Elo
					}
					online = IsCoachOnline(ctx, st, r.FriendID, info.Username)
				}
			}
			divName, _ := DivisionForElo(elo)
			entry := map[string]any{
				"_id":          r.FriendID,
				"coachname":    r.FriendName,
				"status":       map[bool]string{true: "online", false: "offline"}[online],
				"level":        level,
				"currdivision": "DIVISION_" + divName,
			}
			if r.GroupName != "" && groupSet[r.GroupName] {
				entry["group"] = r.GroupName
			}
			friends = append(friends, entry)
		case "requested", "requesting":
			date := r.CreatedAt
			if date == "" {
				date = "2026-11-06T00:00:00.000Z"
			}
			entry := map[string]any{"_id": r.FriendID, "coachname": r.FriendName, "date": date}
			if status == "requested" {
				requested = append(requested, entry)
			} else {
				requesting = append(requesting, entry)
			}
		}
	}
	groupAny := make([]any, len(groups))
	for i, g := range groups {
		groupAny[i] = g
	}
	return map[string]any{
		"groups":     groupAny,
		"friends":    friends,
		"newfriends": []any{},
		"requested":  requested,
		"requesting": requesting,
	}
}

// PresenceList is fx._build_presence_for_coach.
func PresenceList(ctx context.Context, st *store.Store, idx map[string]*ClubMember,
	ownerState State, coachID string) []any {
	cl := ContactList(ctx, st, idx, ownerState, coachID)
	out := []any{}
	for _, raw := range List(cl["friends"]) {
		f := Map(raw)
		teamid, level := 10, 1
		if idx != nil {
			if info := idx[Str(f["_id"])]; info != nil {
				teamid = info.TeamID
				if info.Level > 0 {
					level = info.Level
				}
			}
		}
		online := Str(f["status"]) == "online"
		status := "offline"
		if online {
			status = "online"
		}
		out = append(out, map[string]any{
			"_id": f["_id"], "id": f["_id"], "coachname": f["coachname"], "name": f["coachname"],
			"presence": status, "status": status, "online": online,
			"teamid": teamid, "level": level,
		})
	}
	return out
}

// (FriendPair — a port of fx._friend_pair that returned the FIRST pair
// row involving a coach — was removed 2026-11-15: with directional
// rows and several pairs per coach, "first row" is some older friend,
// so accept/decline/cancel acted on the wrong pair. Handlers now name
// both coaches of the pair explicitly.)

func FriendStatus(ctx context.Context, st *store.Store, coachID, friendID string) string {
	var s string
	err := st.DB.QueryRowContext(ctx,
		"SELECT status FROM friends WHERE coach_id = ? AND friend_coach_id = ?", coachID, friendID).Scan(&s)
	if err != nil {
		return ""
	}
	return s
}

func FriendUpsert(ctx context.Context, st *store.Store, coachID, friendID, friendName, status, createdAt string) {
	_, _ = st.DB.ExecContext(ctx,
		`INSERT INTO friends (coach_id, friend_coach_id, friend_name, group_name, status, created_at)
		 VALUES (?, ?, ?, NULL, ?, ?)
		 ON DUPLICATE KEY UPDATE friend_name = VALUES(friend_name), status = VALUES(status), created_at = VALUES(created_at)`,
		coachID, friendID, friendName, status, createdAt)
}

func FriendSetPairStatus(ctx context.Context, st *store.Store, a, b, status string) {
	_, _ = st.DB.ExecContext(ctx,
		"UPDATE friends SET status = ? WHERE (coach_id = ? AND friend_coach_id = ?) OR (coach_id = ? AND friend_coach_id = ?)",
		status, a, b, b, a)
}

func FriendDeletePair(ctx context.Context, st *store.Store, a, b string) {
	_, _ = st.DB.ExecContext(ctx,
		"DELETE FROM friends WHERE (coach_id = ? AND friend_coach_id = ?) OR (coach_id = ? AND friend_coach_id = ?)",
		a, b, b, a)
}

func FriendSetGroup(ctx context.Context, st *store.Store, coachID, friendID string, group *string) {
	_, _ = st.DB.ExecContext(ctx,
		"UPDATE friends SET group_name = ? WHERE coach_id = ? AND friend_coach_id = ?",
		group, coachID, friendID)
}

// VirtualCoachID is fx's deterministic id for a name with no account.
func VirtualCoachID(name string) string {
	sum := md5.Sum([]byte(strings.ToLower(name)))
	return "65f1a2b3" + hex.EncodeToString(sum[:])[:16]
}

// NowISO formats like fx's %Y-%m-%dT%H:%M:%S.000Z (UTC).
func NowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000") + "Z"
}

// MessengerPush is fx.notify_messenger_push: one JSON line to the
// messenger IPC on 127.0.0.1:28003, targeted at one coach when set.
// Fire-and-forget; failures are silent by design.
func MessengerPush(uri string, body any, coachID string) {
	payload := map[string]any{"uri": uri, "body": body}
	if coachID != "" {
		payload["coachid"] = coachID
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:28003", 350*time.Millisecond)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(350 * time.Millisecond))
	_, _ = fmt.Fprintf(conn, "%s\n", raw)
}
