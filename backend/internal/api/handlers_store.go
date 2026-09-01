package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) listStore(c *gin.Context) {
	apps := s.deps.Catalog.List(c.Query("category"))
	installed := s.installedApps()

	type storeView struct {
		ID         string   `json:"id"`
		Name       string   `json:"name"`
		Tagline    string   `json:"tagline"`
		Category   string   `json:"category"`
		Version    string   `json:"version"`
		Icon       string   `json:"icon"`
		Developer  string   `json:"developer,omitempty"`
		Deprecated bool     `json:"deprecated,omitempty"`
		Installed  bool     `json:"installed"`
		NeedsInput bool     `json:"needs_input"`
		Deps       []string `json:"dependencies,omitempty"`
	}

	out := make([]storeView, 0, len(apps))
	for _, m := range apps {
		_, isInstalled := installed[m.ID]
		needsInput := false
		for _, e := range m.Env {
			// A field the user must fill in, as opposed to one with a default
			// or one HomeOS generates.
			if e.Required && e.Default == "" && !e.Generate {
				needsInput = true
				break
			}
		}
		out = append(out, storeView{
			ID: m.ID, Name: m.Name, Tagline: m.Tagline, Category: m.Category,
			Version: m.Version, Icon: m.Icon, Developer: m.Developer,
			Deprecated: m.Deprecated, Installed: isInstalled,
			NeedsInput: needsInput, Deps: m.Dependencies,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"apps":      out,
		"synced_at": s.deps.Catalog.SyncedAt(),
		"rejected":  s.deps.Catalog.Rejected(),
	})
}

func (s *Server) storeApp(c *gin.Context) {
	m, ok := s.deps.Catalog.Get(c.Param("id"))
	if !ok {
		fail(c, http.StatusNotFound, "no such app in the catalogue")
		return
	}
	_, installed := s.installedApps()[m.ID]
	c.JSON(http.StatusOK, gin.H{"app": m, "installed": installed})
}

func (s *Server) storeIcon(c *gin.Context) {
	path, err := s.deps.Catalog.IconPath(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, err.Error())
		return
	}
	// Icons are immutable per catalogue revision, so they can be cached hard.
	c.Header("Cache-Control", "public, max-age=86400")
	c.File(path)
}

// installApp starts the install in the background and returns immediately. The
// dashboard follows progress on the telemetry stream, which is why a long
// install does not need a long-lived HTTP request.
func (s *Server) installApp(c *gin.Context) {
	id := c.Param("id")
	if _, ok := s.deps.Catalog.Get(id); !ok {
		fail(c, http.StatusNotFound, "no such app in the catalogue")
		return
	}

	var req struct {
		Env map[string]string `json:"env"`
	}
	_ = c.ShouldBindJSON(&req) // an app with no configurable fields sends nothing

	if job, ok := s.deps.Installer.Job(id); ok && job.Finished == nil {
		fail(c, http.StatusConflict, "an operation on this app is already running")
		return
	}

	go func() {
		// Detached from the request context on purpose: the browser closing
		// the connection must not abort a pull that is already underway.
		ctx, cancel := context.WithTimeout(context.Background(), 30*60*1e9)
		defer cancel()
		_ = s.deps.Installer.Install(ctx, id, req.Env)
	}()

	c.JSON(http.StatusAccepted, gin.H{"status": "installing", "app_id": id})
}

func (s *Server) uninstallApp(c *gin.Context) {
	id := c.Param("id")
	purge := c.Query("purge") == "true"

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*60*1e9)
		defer cancel()
		_ = s.deps.Installer.Uninstall(ctx, id, purge)
	}()

	c.JSON(http.StatusAccepted, gin.H{"status": "removing", "app_id": id, "purge": purge})
}

func (s *Server) refreshStore(c *gin.Context) {
	if err := s.deps.Catalog.Sync(c.Request.Context()); err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":   "refreshed",
		"apps":     len(s.deps.Catalog.List("")),
		"rejected": s.deps.Catalog.Rejected(),
	})
}

func (s *Server) installJobs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"jobs": s.deps.Installer.Jobs()})
}
