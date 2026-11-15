package api

// POST /launcher/login + /launcher/status, ported natively from
// fx._handle_user_auth / ep_launcher_status. These must NOT ride the
// fallback proxy: Python resolves the client IP from the socket, so a
// proxied login looks like 127.0.0.1 and Python answers
// real_ip=127.0.0.1 -- the launcher then points the game at the
// player's own machine and the client dies with COMMON_ERROR_CURL
// (observed in the first shadow boot test, 2026-11-13).

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"fo3server/internal/game"
)

func (s *Server) launcherLogin(c *gin.Context) {
	if on, msg := game.MaintenanceOn(c.Request.Context(), s.d.Store); on {
		writeJSON(c, map[string]any{"result": false, "reason": "MAINTENANCE", "message": msg})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Action   string `json:"action"`
	}
	_ = c.ShouldBindJSON(&req)
	username := strings.ToLower(strings.TrimSpace(req.Username))
	password := req.Password
	if username == "" || !validUsername(username) {
		writeJSON(c, map[string]any{"result": false, "message": "Tên tài khoản phải từ 3-24 ký tự (chữ thường, số, dấu gạch dưới)."})
		return
	}
	if len(password) < 3 {
		writeJSON(c, map[string]any{"result": false, "message": "Mật khẩu phải có ít nhất 3 ký tự."})
		return
	}
	ctx := c.Request.Context()
	ip := clientIP(c)
	if s.loginBlocked(ctx, "lg:", ip, username) {
		writeJSON(c, map[string]any{"result": false, "message": "Đăng nhập bị tạm khóa do sai mật khẩu nhiều lần từ địa chỉ này. Thử lại sau 15 phút."})
		return
	}
	u, err := game.UserByUsername(ctx, s.d.Store, username)
	if err != nil || u == nil {
		s.noteLoginFailure(ctx, "lg:", ip, username)
		writeJSON(c, map[string]any{"result": false, "message": "Tài khoản không tồn tại! Vui lòng đăng ký trước."})
		return
	}
	if ok := game.VerifyPassword(ctx, s.d.Store, u, password); !ok {
		s.noteLoginFailure(ctx, "lg:", ip, username)
		writeJSON(c, map[string]any{"result": false, "message": "Mật khẩu không chính xác!"})
		return
	}
	s.noteLoginSuccess(ctx, "lg:", ip, username)
	token, err := game.IssueToken(ctx, s.d.Store, u, ip)
	if err != nil {
		writeJSON(c, map[string]any{"result": false, "message": "Lỗi máy chủ."})
		return
	}
	game.NoteActive(ctx, s.d.Store, u)
	if path := os.Getenv("FO3_SQLITE_PATH"); path != "" {
		if err := game.DualWriteSQLiteToken(path, u.ID, token, ip); err != nil {
			log.Printf("launcher: sqlite dual-write: %v", err)
		}
	} else if err := game.DualWriteSQLiteToken("/opt/fo3/server/gamedata/fo3_users.db", u.ID, token, ip); err != nil {
		log.Printf("launcher: sqlite dual-write: %v", err)
	}
	realIP := s.d.PublicIP
	if ip == "127.0.0.1" || ip == "::1" || ip == "localhost" {
		realIP = "127.0.0.1"
	}
	writeJSON(c, map[string]any{
		"result": true,
		"message": "Đăng nhập thành công (" + username + ")! Đang khởi động FIFA Online 3...",
		"token": token, "uid": u.ID, "username": username,
		"real_ip": realIP, "game_port": 28000, "noti_port": 28001,
	})
}

func (s *Server) launcherStatus(c *gin.Context) {
	n := 0
	row := s.d.Store.DB.QueryRowContext(c.Request.Context(), "SELECT COUNT(*) FROM users")
	_ = row.Scan(&n)
	c.JSON(http.StatusOK, map[string]any{"result": true, "online": true, "accounts": n, "server_ip": s.d.PublicIP})
}

