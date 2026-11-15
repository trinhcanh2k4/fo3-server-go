// lifeset sets the contract life of the first player of a user's
// default team (test helper for the recontract domain).
// Usage: lifeset <uid> <life>
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
	if err := st.Migrate(ctx); err != nil {
		log.Fatal(err)
	}
	uid, _ := strconv.ParseInt(os.Args[1], 10, 64)
	life, _ := strconv.Atoi(os.Args[2])
	gs, err := game.LoadState(ctx, st, uid)
	if err != nil || gs == nil {
		log.Fatalf("state: %v", err)
	}
	team := game.TeamOf(gs, game.DefaultSlot(gs))
	players := game.List(team["players"])
	p := game.Map(players[0])
	p["life"] = life
	fmt.Printf("player spid=%v key=%s life -> %d\n", p["spid"], game.RecontractKey(p), life)
	if err := game.SaveState(ctx, st, uid, gs); err != nil {
		log.Fatal(err)
	}
}
