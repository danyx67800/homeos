package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/danyx67800/homeos/backend/internal/appstore"
	"github.com/danyx67800/homeos/backend/internal/dockerx"
)

func (s *Server) listContainers(c *gin.Context) {
	all := c.Query("all") == "true"
	list, err := s.deps.Docker.List(c.Request.Context(), !all)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"containers": list})
}

func (s *Server) containerAction(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		ctx := c.Request.Context()
		timeout := 30 * time.Second

		var err error
		switch action {
		case "start":
			err = s.deps.Docker.Start(ctx, id)
		case "stop":
			err = s.deps.Docker.Stop(ctx, id, timeout)
		case "restart":
			err = s.deps.Docker.Restart(ctx, id, timeout)
		}
		if err != nil {
			writeDockerError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": action + "ed"})
	}
}

func (s *Server) removeContainer(c *gin.Context) {
	force := c.Query("force") == "true"
	if err := s.deps.Docker.Remove(c.Request.Context(), c.Param("id"), force); err != nil {
		writeDockerError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "removed"})
}

func (s *Server) containerLogs(c *gin.Context) {
	tail, _ := strconv.Atoi(c.DefaultQuery("tail", "200"))
	logs, err := s.deps.Docker.Logs(c.Request.Context(), c.Param("id"), tail)
	if err != nil {
		writeDockerError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

// writeDockerError maps the wrapper's sentinels onto status codes. "Not
// managed" is 403 rather than 404 on purpose: the container exists, and saying
// so is useful, but HomeOS will not touch something it did not create.
func writeDockerError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, dockerx.ErrNotFound):
		fail(c, http.StatusNotFound, err.Error())
	case errors.Is(err, dockerx.ErrNotManaged):
		fail(c, http.StatusForbidden, err.Error())
	default:
		fail(c, http.StatusBadGateway, err.Error())
	}
}

// listApps joins the catalogue with what is actually installed, which is what
// the launcher grid renders.
func (s *Server) listApps(c *gin.Context) {
	containers, err := s.deps.Docker.List(c.Request.Context(), true)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}

	byApp := map[string]dockerx.Container{}
	for _, ct := range containers {
		// A sidecar is part of an app, not an app; showing postgres in the
		// launcher would be noise.
		if ct.Role == "sidecar" || ct.App == "" {
			continue
		}
		byApp[ct.App] = ct
	}

	type appView struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		Icon      string    `json:"icon,omitempty"`
		Category  string    `json:"category,omitempty"`
		Version   string    `json:"version,omitempty"`
		State     string    `json:"state"`
		Health    string    `json:"health,omitempty"`
		URL       string    `json:"url,omitempty"`
		Container string    `json:"container_id,omitempty"`
		Installed time.Time `json:"installed_at,omitempty"`
	}

	out := make([]appView, 0, len(byApp))
	for id, ct := range byApp {
		v := appView{
			ID: id, Name: ct.Title, State: ct.State, Health: ct.Health,
			Container: ct.ID, Installed: ct.Created,
			Category: ct.Labels["homeos.category"],
			Version:  ct.Labels["homeos.version"],
			Icon:     ct.Labels[dockerx.LabelIcon],
		}
		if v.Name == "" {
			v.Name = id
		}
		v.URL = s.appURL(id, ct.Labels[dockerx.LabelRoute], ct.Labels[dockerx.LabelPort])
		out = append(out, v)
	}
	c.JSON(http.StatusOK, gin.H{"apps": out})
}

// appURL mirrors the three route modes homeos-proxy-sync implements. Kept in
// step with scripts/homeos-proxy-sync; if one changes, so must the other.
func (s *Server) appURL(id, route, port string) string {
	host := s.deps.Config.FQDN()
	if route == "" {
		route = s.deps.Config.Proxy.DefaultRouteMode
	}
	switch route {
	case "host":
		return "http://" + id + "." + s.deps.Config.System.Domain + "/"
	case "path":
		return "http://" + host + "/app/" + id + "/"
	case "port":
		if port != "" {
			return "http://" + host + ":" + port + "/"
		}
	}
	return ""
}

// installedApps lists app directories that still carry a compose file.
func (s *Server) installedApps() map[string]*appstore.InstalledManifest {
	out := map[string]*appstore.InstalledManifest{}
	entries, err := os.ReadDir(s.deps.Config.Paths.Apps)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.deps.Config.Paths.Apps, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err != nil {
			continue
		}
		if rec, err := appstore.ReadInstalled(dir); err == nil {
			out[e.Name()] = rec
		} else {
			out[e.Name()] = nil
		}
	}
	return out
}
