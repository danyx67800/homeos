package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/danyx67800/homeos/backend/internal/samba"
	"github.com/danyx67800/homeos/backend/internal/storage"
)

func (s *Server) listDisks(c *gin.Context) {
	devs, err := s.deps.Storage.List(c.Request.Context())
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"disks": devs, "mount_root": s.deps.Config.Storage.MountRoot})
}

func (s *Server) diskHealth(c *gin.Context) {
	// The device arrives as a bare name (sda, nvme0n1) because a slash in a
	// path parameter would split the route.
	dev := "/dev/" + strings.TrimPrefix(c.Param("device"), "/dev/")
	h, err := s.deps.Storage.Health(c.Request.Context(), dev, c.Query("force") == "true")
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"device": dev, "health": h, "degraded": h.Degraded()})
}

func (s *Server) formatDisk(c *gin.Context) {
	var req storage.FormatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	part, err := s.deps.Storage.Format(c.Request.Context(), req)
	if err != nil {
		writeStorageError(c, err)
		return
	}
	s.deps.Log.Warn("disk formatted via API",
		"device", req.Device, "user", c.GetString(ctxUser))
	c.JSON(http.StatusOK, gin.H{"status": "formatted", "partition": part})
}

func (s *Server) mountDisk(c *gin.Context) {
	var req storage.MountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	target, err := s.deps.Storage.Mount(c.Request.Context(), req)
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "mounted", "mountpoint": target})
}

func (s *Server) unmountDisk(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "name is required")
		return
	}
	if err := s.deps.Storage.Unmount(c.Request.Context(), req.Name); err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "unmounted"})
}

// storageEvent is the endpoint phase 1's udev helper posts to. It only nudges
// a rescan; the payload is advisory, because the authoritative view is lsblk.
func (s *Server) storageEvent(c *gin.Context) {
	var ev struct {
		Action string `json:"action"`
		Device string `json:"device"`
	}
	_ = c.ShouldBindJSON(&ev)
	s.deps.Log.Info("storage hotplug", "action", ev.Action, "device", ev.Device)

	if devs, err := s.deps.Storage.List(c.Request.Context()); err == nil {
		s.deps.Hub.Publish(hubEvent("disks", devs))
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "noted"})
}

// A bad device path is the caller's fault, not the server's, so it is a 400.
func writeStorageError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, storage.ErrToolMissing):
		// The subsystem is unavailable on this system, not broken. 503 lets the
		// dashboard say so instead of showing a server-error page.
		fail(c, http.StatusServiceUnavailable,
			"storage tooling is not available on this system: "+err.Error())
	case errors.Is(err, storage.ErrInvalidDevice),
		errors.Is(err, storage.ErrInvalidMount),
		errors.Is(err, storage.ErrInvalidLabel):
		fail(c, http.StatusBadRequest, err.Error())
	default:
		fail(c, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) listShares(c *gin.Context) {
	shares, err := s.loadShares()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"shares": shares})
}

// putShares replaces the whole share set. A whole-set PUT rather than
// per-share POST/DELETE because the generated file is written atomically: the
// API shape matches what actually happens on disk.
func (s *Server) putShares(c *gin.Context) {
	var req struct {
		Shares []samba.Share `json:"shares"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.deps.Samba.Apply(c.Request.Context(), req.Shares); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.saveShares(req.Shares); err != nil {
		s.deps.Log.Warn("shares applied but state not persisted", "error", err)
	}
	c.JSON(http.StatusOK, gin.H{"status": "applied", "count": len(req.Shares)})
}
