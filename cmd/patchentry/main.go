// patchentry is a one-off repair for GM2's 2026-11-13T21:36:04 friendly
// history entry, whose opponent was recorded as GM2 themself and for
// which no mirror entry exists anywhere (the opponent never settled).
// Every other GM2 human friendly that evening was against GM, and the
// entry three minutes later in the same session is GM — so the opponent
// block is set to GM's. Scores stay as recorded. Revert path: the
// pre-fixhistory snapshot in /root/fo3-state-snapshots.
// Usage: patchentry
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

	s, err := game.LoadState(ctx, st, 10)
	if err != nil {
		log.Fatal(err)
	}
	done := false
	for _, coach := range game.Coaches(s) {
		for _, ev := range game.List(coach["multiplay"]) {
			e := game.Map(ev)
			if game.Str(e["date"]) != "2026-11-13T21:36:04.000Z" {
				continue
			}
			opp := game.Map(e["opponent"])
			opp["teamid"] = 243
			opp["coaches"] = []any{map[string]any{
				"coachid": "65f1a2b3c4d5e6f70004c000", "name": "GM"}}
			done = true
			fmt.Println("patched:", e["date"], e["type"])
		}
	}
	if !done {
		log.Fatal("entry not found")
	}
	if err := game.SaveState(ctx, st, 10, s); err != nil {
		log.Fatal(err)
	}
	fmt.Println("saved uid 10")
}
