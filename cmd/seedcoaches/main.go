// seedcoaches builds a FRESH core state for every imported user who
// has none: coach identity (name, team, id, EP, level) is carried over
// from the Python-era backup, but the team is a clean gen_teams squad
// and all records/statistics start at zero -- the agreed data reset
// without losing account identity.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"

	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/store"
)

type oldCoach struct {
	ID        string         `json:"_id"`
	Slot      int            `json:"slot"`
	Name      string         `json:"name"`
	TeamID    int            `json:"teamid"`
	EP        int            `json:"ep"`
	LevelInfo map[string]any `json:"levelinfo"`
	Badge     int            `json:"currentbadge"`
}

type oldState struct {
	Coaches        []oldCoach `json:"coaches"`
	DefaultCoachID string     `json:"defaultcoachid"`
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: seedcoaches <fo3_users.db>")
	}
	src, err := sql.Open("sqlite", "file:"+os.Args[1]+"?mode=ro")
	if err != nil {
		log.Fatal(err)
	}
	defer src.Close()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cipher, err := store.NewCipher(cfg.MasterKey)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DBDSN, cfg.RedisAddr, cfg.RedisPass, cipher)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.DB.Close()
	roster, err := game.OpenRoster(
		envOr("FO3_GAMEDATA_DIR", "/opt/fo3-go/gamedata"),
		envOr("FO3_RESPONSES_DIR", "/opt/fo3-go/responses"))
	if err != nil {
		log.Fatalf("roster: %v", err)
	}

	rows, err := src.Query("SELECT id, username, state_json FROM users ORDER BY id")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var uid int64
		var username, stateJSON string
		if err := rows.Scan(&uid, &username, &stateJSON); err != nil {
			log.Fatal(err)
		}
		var doc string
		err := st.DB.QueryRowContext(ctx,
			"SELECT doc FROM user_state WHERE user_id = ? AND domain = 'core'", uid).Scan(&doc)
		if err == nil && doc != "" {
			fmt.Printf("%s: state exists, skipped\n", username)
			continue
		}
		var old oldState
		_ = json.Unmarshal([]byte(stateJSON), &old)
		state := game.DefaultEmptyState()
		var coaches []any
		teams := map[string]any{}
		for _, oc := range old.Coaches {
			gt := roster.GenTeam(oc.TeamID, 0, false)
			if gt == nil {
				log.Printf("%s: gen team %d failed, skipped coach", username, oc.TeamID)
				continue
			}
			team := game.Map(gt["team"])
			team["teamid"] = oc.TeamID
			teams[fmt.Sprint(oc.Slot)] = team
			coach := map[string]any{
				"_id": oc.ID, "slot": oc.Slot, "name": oc.Name, "teamid": oc.TeamID,
				"state": "active", "ep": oc.EP, "createdat": "2026-11-01T00:00:00.000Z",
				"currentbadge": oc.Badge, "items": []any{}, "multiplay": []any{},
				"record": map[string]any{}, "profile": map[string]any{
					"description": "Xin chào! Chào mừng đến với FIFA Online 3.", "picture": 0},
			}
			if oc.LevelInfo != nil {
				coach["levelinfo"] = oc.LevelInfo
			}
			if coach["_id"] == "" {
				coach["_id"] = game.CoachIDForUser(uid, oc.Slot)
			}
			if game.Int(coach["currentbadge"]) == 0 {
				coach["currentbadge"] = 1001
			}
			coaches = append(coaches, coach)
		}
		if coaches == nil {
			coaches = []any{}
		}
		var userElo int
		_ = st.DB.QueryRowContext(ctx, "SELECT elo FROM users WHERE id = ?", uid).Scan(&userElo)
		if userElo > 0 {
			rk := game.FreshRankedState()
			rk["elo"] = userElo
			rk["highelo"] = userElo
			state["ranked"] = rk
		}
		state["coaches"] = coaches
		state["teams"] = teams
		if old.DefaultCoachID != "" {
			state["defaultcoachid"] = old.DefaultCoachID
		} else if len(coaches) > 0 {
			state["defaultcoachid"] = game.Map(coaches[0])["_id"]
		}
		if err := game.SaveState(ctx, st, uid, state); err != nil {
			log.Fatalf("save %s: %v", username, err)
		}
		fmt.Printf("%s: seeded %d coach(es)\n", username, len(coaches))
	}
	fmt.Println("done")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
