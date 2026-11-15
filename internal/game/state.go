// Package game holds the FO3 domain logic ported from the Python
// server (http_config.py + fo3_ext.py). The per-user game state keeps
// the Python shape (one JSON document: coaches, teams, ...) and is
// stored AES-256-GCM-encrypted in MariaDB (user_state domain "core"),
// so handlers port almost line by line.
package game

import (
	"context"
	"fmt"

	"fo3server/internal/store"
)

// State is one user's whole game state (the Python state_json).
type State = map[string]any

// LoadState reads the user's core state document (empty state if none).
func LoadState(ctx context.Context, st *store.Store, userID int64) (State, error) {
	s := State{}
	found, err := st.LoadState(ctx, userID, "core", &s)
	if err != nil {
		return nil, err
	}
	if !found {
		return DefaultEmptyState(), nil
	}
	return s, nil
}

// SaveState persists the user's core state document (encrypted).
// Every successful save also feeds the EP ledger (see epledger.go).
func SaveState(ctx context.Context, st *store.Store, userID int64, s State) error {
	err := st.SaveState(ctx, userID, "core", s)
	if err == nil {
		RecordEPDelta(ctx, st, userID, s)
	}
	return err
}

// DefaultEmptyState is fx._default_empty_state().
func DefaultEmptyState() State {
	return State{"coaches": []any{}, "defaultcoachid": "", "teams": map[string]any{}}
}

// CoachIDForUser is fx._coach_id_for_user: per-user unique coach ids.
func CoachIDForUser(uid int64, slot int) string {
	if uid <= 1 {
		return fmt.Sprintf("65f1a2b3c4d5e6f7a8b9c%03x", slot)
	}
	return fmt.Sprintf("65f1a2b3c4d5e6f7%04xc%03x", uid, slot)
}

// Coaches returns the state's coach list.
func Coaches(s State) []map[string]any {
	raw, _ := s["coaches"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, c := range raw {
		if m, ok := c.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// DefaultSlot is hc._default_slot: the slot of the default coach.
func DefaultSlot(s State) int {
	coaches := Coaches(s)
	defID, _ := s["defaultcoachid"].(string)
	for _, c := range coaches {
		if c["_id"] == defID {
			return Int(c["slot"])
		}
	}
	if len(coaches) > 0 {
		return Int(coaches[0]["slot"])
	}
	return 0
}

// CoachBySlot returns the coach map for a slot (nil if absent).
func CoachBySlot(s State, slot int) map[string]any {
	for _, c := range Coaches(s) {
		if Int(c["slot"]) == slot {
			return c
		}
	}
	return nil
}

// TeamOf returns the stored team doc for a slot (raw, pre-owned_team).
func TeamOf(s State, slot int) map[string]any {
	teams, _ := s["teams"].(map[string]any)
	if teams == nil {
		return nil
	}
	t, _ := teams[fmt.Sprint(slot)].(map[string]any)
	return t
}

// --- small typed accessors over the loose JSON state -----------------

func Int(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case string:
		var i int
		_, _ = fmt.Sscanf(n, "%d", &i)
		return i
	}
	return 0
}

func Int64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

func Str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func Map(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func List(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}
