package api

// Authentication guards (public-launch hardening, Phase A):
//
//   - observeAuthFallback logs and counts every request that was
//     resolved by the unauthenticated IP / last-active fallbacks,
//     so the tightening in Phase A2b is driven by evidence of what
//     the real client still depends on.
//   - Login failure counters in Redis: five failures for one
//     (IP, account) pair inside 15 minutes lock that account FOR
//     THAT IP ONLY — the real owner signing in from elsewhere is
//     never locked out by an attacker's guesses — and 30 failures
//     from one IP across all accounts pause all logins from that
//     source for the window. A successful login clears the pair
//     counter. Registrations are rate-limited per IP.

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

const (
	loginWindow   = 15 * time.Minute
	loginPairMax  = 5
	loginIPMax    = 30
	registerIPMax = 5 // per hour
)

func (s *Server) observeAuthFallback(c *gin.Context, u *game.User, via, ip string) {
	ctx := c.Request.Context()
	path := c.Request.URL.Path
	log.Printf("auth-fallback: via=%s user=%s ip=%s %s %s", via, u.Username, ip, c.Request.Method, path)
	key := "fallbackstat:" + via + ":" + path
	if n, err := s.d.Store.Redis.Incr(ctx, key).Result(); err == nil && n == 1 {
		s.d.Store.Redis.Expire(ctx, key, 30*24*time.Hour)
	}
}

func guardPairKey(prefix, kind, ip, name string) string {
	return prefix + kind + ":" + ip + ":" + name
}

// loginBlocked reports whether this (IP, account) pair or the IP
// as a whole is currently paused. prefix separates the game login
// ("lg:") from the admin login ("ad:").
func (s *Server) loginBlocked(ctx context.Context, prefix, ip, username string) bool {
	r := s.d.Store.Redis
	if n, _ := r.Exists(ctx, guardPairKey(prefix, "lock", ip, username)).Result(); n > 0 {
		return true
	}
	if n, _ := r.Exists(ctx, prefix+"lockip:"+ip).Result(); n > 0 {
		return true
	}
	return false
}

func (s *Server) noteLoginFailure(ctx context.Context, prefix, ip, username string) {
	r := s.d.Store.Redis
	pk := guardPairKey(prefix, "fail", ip, username)
	if n, err := r.Incr(ctx, pk).Result(); err == nil {
		if n == 1 {
			r.Expire(ctx, pk, loginWindow)
		}
		if n >= loginPairMax {
			r.Set(ctx, guardPairKey(prefix, "lock", ip, username), "1", loginWindow)
			log.Printf("auth-guard: %s locked for ip %s after %d failures (prefix %s)", username, ip, n, prefix)
		}
	}
	ik := prefix + "failip:" + ip
	if n, err := r.Incr(ctx, ik).Result(); err == nil {
		if n == 1 {
			r.Expire(ctx, ik, loginWindow)
		}
		if n >= loginIPMax {
			r.Set(ctx, prefix+"lockip:"+ip, "1", loginWindow)
			log.Printf("auth-guard: all logins paused for ip %s after %d failures (prefix %s)", ip, n, prefix)
		}
	}
}

func (s *Server) noteLoginSuccess(ctx context.Context, prefix, ip, username string) {
	r := s.d.Store.Redis
	r.Del(ctx, guardPairKey(prefix, "fail", ip, username),
		guardPairKey(prefix, "lock", ip, username))
}

func (s *Server) registerBlocked(ctx context.Context, ip string) bool {
	n, _ := s.d.Store.Redis.Get(ctx, "regcount:"+ip).Int64()
	return n >= registerIPMax
}

func (s *Server) noteRegister(ctx context.Context, ip string) {
	k := "regcount:" + ip
	if n, err := s.d.Store.Redis.Incr(ctx, k).Result(); err == nil && n == 1 {
		s.d.Store.Redis.Expire(ctx, k, time.Hour)
	}
}

// registrationGate reads the "security" settings document fresh
// (login/register are low-frequency, so no cache) and decides
// whether a registration with the presented invite code may
// proceed. An absent or unreadable document means the historical
// open registration.
func (s *Server) registrationGate(ctx context.Context, inviteCode string) (bool, string) {
	raw, ok := game.SettingGet(ctx, s.d.Store, "security")
	if !ok || raw == "" {
		return true, ""
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return true, ""
	}
	switch game.Str(doc["registration_mode"]) {
	case "closed":
		return false, "Đăng ký tài khoản đang tạm đóng."
	case "invite":
		code := strings.TrimSpace(inviteCode)
		for _, p := range strings.Split(game.Str(doc["invite_codes"]), ",") {
			if p = strings.TrimSpace(p); p != "" && p == code && code != "" {
				return true, ""
			}
		}
		return false, "Mã mời không hợp lệ."
	}
	return true, ""
}
