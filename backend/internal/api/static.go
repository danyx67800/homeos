package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

/*
serveDashboard serves the built SPA from the configured web root.

In production Caddy already does this, and it stays the front door: it owns
TLS, the RFC1918 guard, access logging and the per-app routes. This is a
fallback, and it earns its place twice — it makes `homeos-core` self-sufficient
when Caddy is stopped or misconfigured (exactly when you are on SSH trying to
find out why), and it means the dashboard can be exercised against one process
in development.

Registered with NoRoute so every real API route still wins.
*/
func (s *Server) serveDashboard() gin.HandlerFunc {
	root := s.deps.Config.Paths.WebRoot
	index := filepath.Join(root, "index.html")

	return func(c *gin.Context) {
		p := c.Request.URL.Path

		// Anything under /api that reached NoRoute is a genuine 404, not a
		// client-side route. Answering with index.html there would turn a typo
		// into a confusing "unexpected token <" in the browser console.
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/ws/") || p == "/events" {
			fail(c, http.StatusNotFound, "no such endpoint")
			return
		}

		if _, err := os.Stat(index); err != nil {
			fail(c, http.StatusNotFound,
				"the dashboard is not installed; build it with `make -C web build` "+
					"and copy web/dist to "+root)
			return
		}

		// filepath.Join cleans the path, and the Rel check refuses anything
		// that still escapes the web root — a request for
		// /../../etc/homeos/secrets/api.token must not be served.
		target := filepath.Join(root, filepath.Clean("/"+p))
		if rel, err := filepath.Rel(root, target); err != nil || strings.HasPrefix(rel, "..") {
			fail(c, http.StatusForbidden, "path outside the web root")
			return
		}

		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			// Hashed asset filenames are immutable, so they can be cached hard;
			// index.html must not be, or an update never reaches the browser.
			if strings.HasPrefix(p, "/assets/") {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				c.Header("Cache-Control", "no-cache")
			}
			c.File(target)
			return
		}

		// Unknown path: hand back the SPA shell so client-side routing survives
		// a hard refresh or a deep link.
		c.Header("Cache-Control", "no-cache")
		c.File(index)
	}
}
