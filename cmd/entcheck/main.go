// entcheck prints the PC/VIP entitlement fields of every user's
// state (uids 1..30) so an operator can audit who actually holds a
// grant. Read-only.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

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
	now := time.Now().UnixMilli()
	for uid := int64(1); uid <= 30; uid++ {
		s, err := game.LoadState(ctx, st, uid)
		if err != nil || s == nil {
			continue
		}
		ent := game.Map(s["entitlements"])
		if ent == nil {
			fmt.Printf("uid %d: entitlements=nil (no grants)\n", uid)
			continue
		}
		fmt.Printf("uid %d: vip_until=%d (active=%v) pc_until=%d (active=%v)\n",
			uid, game.Int64(ent["vip_until"]), game.Int64(ent["vip_until"]) > now,
			game.Int64(ent["pc_until"]), game.Int64(ent["pc_until"]) > now)
	}
}
