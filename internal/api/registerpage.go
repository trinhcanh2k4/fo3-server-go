package api

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

// The browser registration page, ported verbatim from fo3_ext.py's
// WEB_REGISTER_HTML (audit group B). Its form posts to
// /api/register and /launcher/login, both served by this binary.

//go:embed register.html
var registerHTML []byte

func (s *Server) registerPage(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", registerHTML)
}

//go:embed signin.html
var signinHTML []byte

func (s *Server) signinPage(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", signinHTML)
}
