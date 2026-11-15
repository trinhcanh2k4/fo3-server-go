// bagdel removes bag entries with a given title from a user's
// default coach. Usage: bagdel <uid> <title>
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/store"
)

func main() {
	uid, _ := strconv.ParseInt(os.Args[1], 10, 64)
	title := os.Args[2]
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	cipher, err := store.NewCipher(cfg.MasterKey)
	if err != nil {
		panic(err)
	}
	st, err := store.Open(context.Background(), cfg.DBDSN, cfg.RedisAddr, cfg.RedisPass, cipher)
	if err != nil {
		panic(err)
	}
	state, err := game.LoadState(context.Background(), st, uid)
	if err != nil {
		panic(err)
	}
	coach := game.CoachBySlot(state, game.DefaultSlot(state))
	kept := []any{}
	removed := 0
	for _, bv := range game.List(coach["bag"]) {
		if game.Str(game.Map(bv)["title"]) == title {
			removed++
			continue
		}
		kept = append(kept, bv)
	}
	coach["bag"] = kept
	if err := game.SaveState(context.Background(), st, uid, state); err != nil {
		panic(err)
	}
	fmt.Printf("removed %d bag entries titled %q\n", removed, title)
}
