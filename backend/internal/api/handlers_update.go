package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) updateStatus(c *gin.Context) {
	if s.deps.Updater == nil {
		fail(c, http.StatusServiceUnavailable, "updates are not configured on this appliance")
		return
	}
	st := s.deps.Updater.Status()
	c.JSON(http.StatusOK, gin.H{
		"status": st,
		// The outcome of the last apply is written by the privileged helper
		// after this process has been restarted, so it cannot be held in
		// memory — it is read back from disk.
		"last_apply": s.lastApplyResult(),
	})
}

func (s *Server) updateCheck(c *gin.Context) {
	if s.deps.Updater == nil {
		fail(c, http.StatusServiceUnavailable, "updates are not configured on this appliance")
		return
	}
	rel, err := s.deps.Updater.Check(c.Request.Context())
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	if rel == nil {
		c.JSON(http.StatusOK, gin.H{"up_to_date": true, "status": s.deps.Updater.Status()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"up_to_date": false, "release": rel, "status": s.deps.Updater.Status()})
}

// updateDownload stages a release in the background. Nothing live is touched,
// so this is safe to start and forget; applying is a separate call.
func (s *Server) updateDownload(c *gin.Context) {
	if s.deps.Updater == nil {
		fail(c, http.StatusServiceUnavailable, "updates are not configured on this appliance")
		return
	}
	st := s.deps.Updater.Status()
	if st.Available == nil {
		fail(c, http.StatusConflict, "no update is available; check first")
		return
	}
	rel := *st.Available

	go func() {
		// Detached from the request: a slow link must not have the download
		// cancelled by a browser tab closing.
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		if _, err := s.deps.Updater.Download(ctx, &rel); err != nil {
			s.deps.Log.Error("update download failed", "version", rel.Version, "error", err)
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{"status": "downloading", "version": rel.Version})
}

// updateApply hands the staged release to the privileged helper. The response
// is the last thing this process sends: it is about to be replaced.
func (s *Server) updateApply(c *gin.Context) {
	if s.deps.Updater == nil {
		fail(c, http.StatusServiceUnavailable, "updates are not configured on this appliance")
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	_ = c.ShouldBindJSON(&req)

	s.deps.Log.Warn("update requested via API", "user", c.GetString(ctxUser), "version", req.Version)
	if err := s.deps.Updater.Apply(c.Request.Context(), req.Version); err != nil {
		fail(c, http.StatusConflict, err.Error())
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"status": "applying",
		"note":   "the service is restarting; the dashboard will reconnect on its own",
	})
}

// lastApplyResult reads what the helper recorded about the previous apply,
// including a rollback the daemon was not alive to observe.
func (s *Server) lastApplyResult() any {
	raw, err := os.ReadFile(filepath.Join(s.deps.Config.UpdatesDir(), "last-result.json"))
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := jsonUnmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
