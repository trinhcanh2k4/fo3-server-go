// mreset runs the live-market daily reset once, on demand
// (ops/testing tool; the server also runs it daily via
// game.MarketResetTick). Usage: mreset
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/store"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

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
	roster, err := game.OpenRoster(envOr("FO3_GAMEDATA_DIR", "/opt/fo3-go/gamedata"),
		envOr("FO3_RESPONSES_DIR", "/opt/fo3-go/responses"))
	if err != nil {
		log.Fatal(err)
	}
	roster.SetMarketFactors(game.MarketFactors(ctx, st))
	moved := game.RunMarketReset(ctx, st, roster)
	fmt.Println("moved:", moved)
}
