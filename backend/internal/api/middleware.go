package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const ctxUser = "homeos.user"

func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		lvl := s.deps.Log.Info
		status := c.Writer.Status()
		switch {
		case status >= 500:
			lvl = s.deps.Log.Error
		case status >= 400:
			lvl = s.deps.Log.Warn
		}
		// Health and telemetry poll constantly; logging them at info would bury
		// everything else.
		if status < 400 && isChatty(c.Request.URL.Path) {
			return
		}
		lvl("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"duration", time.Since(start).Round(time.Millisecond).String(),
		)
	}
}

func isChatty(path string) bool {
	return strings.HasSuffix(path, "/health") ||
		strings.HasPrefix(path, "/api/v1/system/metrics") ||
		strings.HasPrefix(path, "/ws/") ||
		strings.HasPrefix(path, "/events")
}

// securityHeaders complements what Caddy adds. Duplicating a couple of headers
// is harmless and means a direct-to-loopback request (a debugging curl, a
// future unix socket) is not served bare.
func (s *Server) securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		c.Next()
	}
}

// bearer extracts the session token from the Authorization header.
func bearer(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if v, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Before the first-run wizard has been completed there is nothing to
		// protect and no way to obtain a token, so the API stays closed rather
		// than open: the setup endpoints are the only way in.
		user, ok := s.deps.Auth.Validate(bearer(c))
		if !ok {
			fail(c, http.StatusUnauthorized, "authentication required")
			return
		}
		c.Set(ctxUser, user)
		c.Next()
	}
}

// requireAuthStream also accepts ?token=, because neither EventSource nor the
// WebSocket constructor lets a browser set an Authorization header.
//
// The token is therefore in a URL. That is acceptable only because the URL
// never leaves the loopback interface: Caddy proxies to 127.0.0.1 and the
// access log it writes is on the same trusted box. It would not be acceptable
// over the public internet.
func (s *Server) requireAuthStream() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearer(c)
		if token == "" {
			token = c.Query("token")
		}
		user, ok := s.deps.Auth.Validate(token)
		if !ok {
			fail(c, http.StatusUnauthorized, "authentication required")
			return
		}
		c.Set(ctxUser, user)
		c.Next()
	}
}

// fail writes a consistent error body and stops the chain. The dashboard shows
// `error` verbatim, so the text is written for a person, not a log parser.
func fail(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}
