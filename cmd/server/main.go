// FO3 Go server: game API (Gin) + messenger + admin/debug APIs.
//
// Phase 0 scope: configuration, encrypted store, health endpoint.
// The game routes are ported domain by domain (see the plan document);
// until a domain lands, the fallback proxy forwards it to the Python
// server so the game stays playable (the "cua lui" requirement).
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/api"
	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/messenger"
	"fo3server/internal/store"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cipher, err := store.NewCipher(cfg.MasterKey)
	if err != nil {
		log.Fatalf("cipher: %v", err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DBDSN, cfg.RedisAddr, cfg.RedisPass, cipher)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.DB.Close()
	defer st.Redis.Close()
	if err := st.Migrate(ctx); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Println("store ready: mariadb + redis connected, schema applied")

	// Go Messenger (port of messenger.py): the client's TCP
	// notification channel + the loopback push IPC + Redis
	// presence. Bind failures only disable that listener.
	ipcAddr := envOr("FO3_MSG_IPC_ADDR", "127.0.0.1:28003")
	messenger.New(st.Redis).ListenAndServe(cfg.MessengerAddr, ipcAddr)

	gamedataDir := envOr("FO3_GAMEDATA_DIR", "/opt/fo3-go/gamedata")
	responsesDir := envOr("FO3_RESPONSES_DIR", "/opt/fo3-go/responses")
	assetsDir := envOr("FO3_ASSETS_DIR", "/opt/fo3-go/assets")
	roster, err := game.OpenRoster(gamedataDir, responsesDir)
	if err != nil {
		log.Fatalf("roster: %v", err)
	}
	roster.SetAssetsDir(assetsDir)
	log.Println("roster ready: fifadb + formations loaded")

	// Admin price overrides: load now, refresh every minute.
	refreshOverrides := func() {
		roster.SetShopOverrides(game.ShopPriceOverrides(ctx, st))
		roster.SetGPOverrides(game.GPPriceOverrides(ctx, st))
		roster.SetShopHidden(game.ShopHiddenIDs(ctx, st))
		roster.SetMarketFactors(game.MarketFactors(ctx, st))
		game.RefreshRuntimeSettings(ctx, st)
	}
	refreshOverrides()
	go func() {
		for range time.Tick(time.Minute) {
			refreshOverrides()
		}
	}()
	// Live-market daily reset (demand factors from real sales).
	go func() {
		for range time.Tick(time.Minute) {
			game.MarketResetTick(ctx, st, roster)
		}
	}()
	go roster.WarmPacks()

	r := api.New(api.Deps{
		Store: st, Cfg: cfg, Roster: roster,
		ResponsesDir: responsesDir,
		AdminDir: envOr("FO3_ADMIN_DIR", "/opt/fo3-go/web-admin-dist"), AssetsDir: assetsDir, GamedataDir: gamedataDir,
		PublicIP:   envOr("FO3_PUBLIC_IP", "203.0.113.10"),
		PublicHost: envOr("FO3_PUBLIC_HOST", "fo3.example.com"),
	})
	r.GET("/healthz", func(c *gin.Context) {
		hctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		dbOK := st.DB.PingContext(hctx) == nil
		redisOK := st.Redis.Ping(hctx).Err() == nil
		c.JSON(http.StatusOK, gin.H{
			"ok": dbOK && redisOK, "mariadb": dbOK, "redis": redisOK,
			"service": "fo3-go", "phase": 1,
		})
	})

	addr := cfg.ShadowAddr // pre-cutover: shadow port; production port after cutover
	srv := &http.Server{
		Addr: addr, Handler: r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		log.Printf("fo3-go listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	log.Println("fo3-go stopped")
}
