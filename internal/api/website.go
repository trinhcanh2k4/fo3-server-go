package api

// Public website at the domain root (landing page + the two
// JSON feeds its widgets use), added 2026-11-14 at the owner's
// request: the same functions as the 3sea.online homepage
// (server status, live 1v1 rankings, sign-up/download steps),
// with our own page and wording. No events, no Discord.

import (
	_ "embed"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

//go:embed landing.html
var landingHTML []byte

func (s *Server) landingPage(c *gin.Context) {
	// The admin subdomain's root is the admin panel, not the
	// public landing page.
	if requestHost(c) == adminHost && s.d.AdminDir != "" {
		index := filepath.Join(s.d.AdminDir, "index.html")
		if _, err := os.Stat(index); err == nil {
			c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
			c.File(index)
			return
		}
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", landingHTML)
}

// webStatus reports how many coaches are in game right now
// (Messenger presence keys), for the landing page status pill.
func (s *Server) webStatus(c *gin.Context) {
	ctx := c.Request.Context()
	online := 0
	if keys, err := s.d.Store.Redis.Keys(ctx, "msgonline:*").Result(); err == nil {
		for _, k := range keys {
			if k != "msgonline:heartbeat" {
				online++
			}
		}
	}
	writeJSON(c, map[string]any{"online": online})
}

var (
	rankCacheMu   sync.Mutex
	rankCacheRows []map[string]any
	rankCacheAt   time.Time
)

// divisionVN renders a ladder division in the owner's terms:
// AMATEUR_* is "Nghiệp Dư", SEMIPRO_* "Bán Chuyên Nghiệp",
// PROFESSIONAL_* "Chuyên Nghiệp", WORLDCLASS_* "Thế Giới",
// LEGENDARY_* "Huyền Thoại", keeping the C/B/A (and top S)
// grade letter.
func divisionVN(name string) string {
	tiers := map[string]string{
		"AMATEUR":      "Nghiệp Dư",
		"SEMIPRO":      "Bán Chuyên Nghiệp",
		"PROFESSIONAL": "Chuyên Nghiệp",
		"WORLDCLASS":   "Thế Giới",
		"LEGENDARY":    "Huyền Thoại",
	}
	if i := strings.LastIndex(name, "_"); i > 0 {
		if vn, ok := tiers[name[:i]]; ok {
			return vn + " " + name[i+1:]
		}
	}
	return strings.ReplaceAll(name, "_", " ")
}

// webRankings serves the public 1v1 ladder (real accounts
// only: no seeded bot rows like the in-game board, and no
// musetest* tooling accounts), cached for a minute.
func (s *Server) webRankings(c *gin.Context) {
	rankCacheMu.Lock()
	fresh := time.Since(rankCacheAt) < time.Minute && rankCacheRows != nil
	rows := rankCacheRows
	rankCacheMu.Unlock()
	if !fresh {
		ctx := c.Request.Context()
		users, _ := game.AllUsers(ctx, s.d.Store)
		rows = []map[string]any{}
		for _, u := range users {
			if strings.HasPrefix(u.Username, "musetest") {
				continue
			}
			ust, err := game.LoadState(ctx, s.d.Store, u.ID)
			if err != nil || ust == nil {
				continue
			}
			rk := game.RankedState(ust, "ranked")
			elo := game.Int(rk["elo"])
			if rk["elo"] == nil {
				elo = game.LadderStartElo
			}
			name := u.CoachName
			if name == "" {
				name = u.Username
			}
			divName, _ := game.DivisionForElo(elo)
			rows = append(rows, map[string]any{
				"name":     name,
				"division": divisionVN(divName),
				"elo":      elo,
				"wins":     game.Int(rk["wins"]),
				"draws":    game.Int(rk["draws"]),
				"losses":   game.Int(rk["losses"]),
			})
		}
		sort.SliceStable(rows, func(i, j int) bool {
			ei, ej := game.Int(rows[i]["elo"]), game.Int(rows[j]["elo"])
			if ei != ej {
				return ei > ej
			}
			return game.Int(rows[i]["wins"]) > game.Int(rows[j]["wins"])
		})
		if len(rows) > 50 {
			rows = rows[:50]
		}
		for i, r := range rows {
			r["rank"] = i + 1
		}
		rankCacheMu.Lock()
		rankCacheRows, rankCacheAt = rows, time.Now()
		rankCacheMu.Unlock()
	}
	writeJSON(c, map[string]any{"rows": rows})
}
