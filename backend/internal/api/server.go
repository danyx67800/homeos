// Package api exposes the REST and streaming surface the dashboard talks to.
//
// Everything binds to loopback: phase 1's Caddy configuration is the only thing
// that reaches the LAN, and it owns TLS, access logging and the RFC1918 guard.
// Binding wider here would quietly bypass all of that.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/danyx67800/homeos/backend/internal/appstore"
	"github.com/danyx67800/homeos/backend/internal/auth"
	"github.com/danyx67800/homeos/backend/internal/config"
	"github.com/danyx67800/homeos/backend/internal/dockerx"
	"github.com/danyx67800/homeos/backend/internal/hub"
	"github.com/danyx67800/homeos/backend/internal/samba"
	"github.com/danyx67800/homeos/backend/internal/storage"
	"github.com/danyx67800/homeos/backend/internal/telemetry"
	"github.com/danyx67800/homeos/backend/internal/updater"
)

type Deps struct {
	Config    *config.Config
	Log       *slog.Logger
	Hub       *hub.Hub
	Collector *telemetry.Collector
	Docker    *dockerx.Client
	Storage   *storage.Manager
	Samba     *samba.Manager
	Catalog   *appstore.Catalog
	Installer *appstore.Installer
	Auth      *auth.Service
	// Updater is nil when no update channel is configured; every update
	// handler answers 503 in that case rather than pretending to work.
	Updater   *updater.Updater
	Version   string
	StartedAt time.Time
}

type Server struct {
	deps Deps
	http *http.Server
	eng  *gin.Engine
}

func NewServer(d Deps) *Server {
	gin.SetMode(gin.ReleaseMode)
	eng := gin.New()

	s := &Server{deps: d, eng: eng}

	eng.Use(gin.Recovery())
	eng.Use(s.requestLogger())
	eng.Use(s.securityHeaders())

	s.routes()

	s.http = &http.Server{
		Addr:    d.Config.API.Addr(),
		Handler: eng,
		// Generous write timeout would still kill a websocket, so streaming
		// routes are served on a separate handler path that opts out; see
		// telemetry.go.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s
}

func (s *Server) Start() error {
	s.deps.Log.Info("api listening", "addr", s.http.Addr)
	if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

func (s *Server) routes() {
	// Unauthenticated: liveness, and the first-run wizard.
	s.eng.GET("/api/v1/health", s.health)
	s.eng.GET("/api/v1/setup/status", s.setupStatus)
	s.eng.POST("/api/v1/setup", s.setup)
	s.eng.POST("/api/v1/auth/login", s.login)

	v1 := s.eng.Group("/api/v1", s.requireAuth())
	{
		v1.POST("/auth/logout", s.logout)
		v1.GET("/auth/me", s.me)
		v1.POST("/auth/password", s.changePassword)

		v1.GET("/system/info", s.systemInfo)
		v1.GET("/system/metrics", s.metrics)
		v1.GET("/system/metrics/history", s.metricsHistory)
		v1.POST("/system/power/:action", s.power)

		v1.GET("/containers", s.listContainers)
		v1.POST("/containers/:id/start", s.containerAction("start"))
		v1.POST("/containers/:id/stop", s.containerAction("stop"))
		v1.POST("/containers/:id/restart", s.containerAction("restart"))
		v1.DELETE("/containers/:id", s.removeContainer)
		v1.GET("/containers/:id/logs", s.containerLogs)

		v1.GET("/apps", s.listApps)
		v1.GET("/store", s.listStore)
		v1.GET("/store/:id", s.storeApp)
		v1.GET("/store/:id/icon", s.storeIcon)
		v1.POST("/store/:id/install", s.installApp)
		v1.DELETE("/store/:id", s.uninstallApp)
		v1.POST("/store/refresh", s.refreshStore)
		v1.GET("/store/jobs", s.installJobs)

		v1.GET("/storage/disks", s.listDisks)
		v1.GET("/storage/disks/:device/health", s.diskHealth)
		v1.POST("/storage/format", s.formatDisk)
		v1.POST("/storage/mount", s.mountDisk)
		v1.POST("/storage/unmount", s.unmountDisk)
		v1.POST("/storage/events", s.storageEvent)

		v1.GET("/system/update", s.updateStatus)
		v1.POST("/system/update/check", s.updateCheck)
		v1.POST("/system/update/download", s.updateDownload)
		v1.POST("/system/update/apply", s.updateApply)

		v1.GET("/shares", s.listShares)
		v1.PUT("/shares", s.putShares)
	}

	// Streaming. Registered outside the group so the auth middleware can accept
	// a token in the query string: browsers cannot set headers on an
	// EventSource or a WebSocket handshake.
	s.eng.GET("/ws/telemetry", s.requireAuthStream(), s.wsTelemetry)
	s.eng.GET("/events", s.requireAuthStream(), s.sseTelemetry)

	// Everything else is the dashboard. Registered last, so no API route can
	// be shadowed by a file on disk.
	s.eng.NoRoute(s.serveDashboard())
}
