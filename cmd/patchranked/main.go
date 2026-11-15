// patchranked aligns each existing core state's ranked elo/highelo
// with the users.elo column (the seed carried elo only into the
// column; fx reads the ranked record from the state). One-off repair
// for the Phase 1 seed, 2026-11-13.
package main

import (
	"context"
	"fmt"
	"log"

	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/store"
)

func main() {
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
	users, err := game.AllUsers(ctx, st)
	if err != nil {
		log.Fatal(err)
	}
	for _, u := range users {
		s, err := game.LoadState(ctx, st, u.ID)
		if err != nil || s == nil {
			fmt.Printf("%s: no state (%v)\n", u.Username, err)
			continue
		}
		rk := game.RankedState(s, "ranked")
		rk["elo"] = u.Elo
		rk["highelo"] = u.Elo
		if err := game.SaveState(ctx, st, u.ID, s); err != nil {
			log.Fatalf("%s: save: %v", u.Username, err)
		}
		fmt.Printf("%s: ranked elo -> %d\n", u.Username, u.Elo)
	}
}
