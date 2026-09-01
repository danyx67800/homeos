package api

import (
	"context"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) health(c *gin.Context) {
	dockerOK := s.deps.Docker.Ping(c.Request.Context()) == nil
	status := http.StatusOK
	if !dockerOK {
		// Degraded, not down: telemetry and storage still work without Docker,
		// so 503 would be misleading to an uptime monitor.
		status = http.StatusOK
	}
	c.JSON(status, gin.H{
		"status":  "ok",
		"version": s.deps.Version,
		"uptime":  time.Since(s.deps.StartedAt).Round(time.Second).String(),
		"docker":  dockerOK,
		"setup":   !s.deps.Auth.NeedsSetup(),
	})
}

func (s *Server) setupStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"needs_setup": s.deps.Auth.NeedsSetup(),
		"hostname":    s.deps.Config.System.Hostname,
	})
}

func (s *Server) setup(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "username and password are required")
		return
	}
	if err := s.deps.Auth.Setup(req.Username, req.Password); err != nil {
		fail(c, http.StatusConflict, err.Error())
		return
	}
	s.deps.Log.Info("admin account created", "username", req.Username)
	c.JSON(http.StatusCreated, gin.H{"status": "created"})
}

func (s *Server) login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "username and password are required")
		return
	}
	token, exp, err := s.deps.Auth.Login(req.Username, req.Password)
	if err != nil {
		// One message for every failure mode: distinguishing "no such user"
		// from "wrong password" tells an attacker which half to keep trying.
		fail(c, http.StatusUnauthorized, "incorrect username or password")
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "expires_at": exp})
}

func (s *Server) logout(c *gin.Context) {
	s.deps.Auth.Logout(bearer(c))
	c.JSON(http.StatusOK, gin.H{"status": "logged out"})
}

func (s *Server) me(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"username": c.GetString(ctxUser)})
}

func (s *Server) changePassword(c *gin.Context) {
	var req struct {
		Current string `json:"current" binding:"required"`
		Next    string `json:"next"    binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "current and next are required")
		return
	}
	if err := s.deps.Auth.ChangePassword(c.GetString(ctxUser), req.Current, req.Next); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "changed, all sessions ended"})
}

func (s *Server) systemInfo(c *gin.Context) {
	dockerVersion, _ := s.deps.Docker.Version(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{
		"hostname":       s.deps.Config.System.Hostname,
		"fqdn":           s.deps.Config.FQDN(),
		"timezone":       s.deps.Config.System.Timezone,
		"version":        s.deps.Version,
		"architecture":   runtime.GOARCH,
		"docker_version": dockerVersion,
		"started_at":     s.deps.StartedAt,
		"route_mode":     s.deps.Config.Proxy.DefaultRouteMode,
	})
}

func (s *Server) metrics(c *gin.Context) {
	c.JSON(http.StatusOK, s.deps.Collector.Latest())
}

func (s *Server) metricsHistory(c *gin.Context) {
	n, _ := strconv.Atoi(c.DefaultQuery("samples", "120"))
	c.JSON(http.StatusOK, gin.H{"samples": s.deps.Collector.History(n)})
}

// power reboots or shuts down. Delayed by a minute so the response reaches the
// dashboard and the browser can show "going down" rather than a dead socket.
func (s *Server) power(c *gin.Context) {
	action := c.Param("action")
	var args []string
	switch action {
	case "reboot":
		args = []string{"-r", "+0"}
	case "shutdown", "poweroff":
		args = []string{"-h", "+0"}
	default:
		fail(c, http.StatusBadRequest, "action must be reboot or shutdown")
		return
	}

	s.deps.Log.Warn("power action requested", "action", action, "user", c.GetString(ctxUser))
	c.JSON(http.StatusAccepted, gin.H{"status": action + " scheduled"})

	go func() {
		time.Sleep(2 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n", "/usr/sbin/shutdown"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			s.deps.Log.Error("power action failed", "action", action, "error", err, "output", string(out))
		}
	}()
}
