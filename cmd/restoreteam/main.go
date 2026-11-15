// restoreteam regenerates one user's team (seed-style, from the
// coach's club) after team data loss, preserving the rest of the
// state (EP optionally reset to a given value; staff carried over).
// Usage: restoreteam <uid> <ep-or--1>
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"

	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/store"
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	uid, _ := strconv.ParseInt(os.Args[1], 10, 64)
	epArg, _ := strconv.ParseInt(os.Args[2], 10, 64)
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	cipher, err := store.NewCipher(cfg.MasterKey)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DBDSN, cfg.RedisAddr, cfg.RedisPass, cipher)
	if err != nil {
		log.Fatal(err)
	}
	defer st.DB.Close()
	roster, err := game.OpenRoster(envOr("FO3_GAMEDATA_DIR", "/opt/fo3-go/gamedata"),
		envOr("FO3_RESPONSES_DIR", "/opt/fo3-go/responses"))
	if err != nil {
		log.Fatal(err)
	}
	state, err := game.LoadState(ctx, st, uid)
	if err != nil {
		log.Fatal(err)
	}
	slot := game.DefaultSlot(state)
	coach := game.CoachBySlot(state, slot)
	teamid := game.Int(coach["teamid"])
	old := game.TeamOf(state, slot)
	gt := roster.GenTeam(teamid, 0, false)
	team := game.Map(gt["team"])
	team["teamid"] = teamid
	if old != nil {
		for _, k := range []string{"staffs", "staffroles", "staffroleidx", "staffsmc", "maxstaff"} {
			if old[k] != nil {
				team[k] = old[k]
			}
		}
	}
	teams := game.Map(state["teams"])
	if teams == nil {
		teams = map[string]any{}
		state["teams"] = teams
	}
	teams[fmt.Sprint(slot)] = team
	if epArg >= 0 {
		coach["ep"] = epArg
	}
	if err := game.SaveState(ctx, st, uid, state); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("uid %d: team %d regenerated (%d players), ep=%v\n",
		uid, teamid, len(game.List(team["players"])), coach["ep"])
}
