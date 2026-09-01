package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/danyx67800/homeos/backend/internal/hub"
	"github.com/danyx67800/homeos/backend/internal/samba"
)

func hubEvent(typ string, data any) hub.Event { return hub.Event{Type: typ, Data: data} }

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 8192,
	// The dashboard is served from the same origin through Caddy, and the
	// listener is loopback-only, so the browser's own origin check plus the
	// session token are the controls here.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsTelemetry streams live samples. WebSocket is the primary transport; /events
// below is the SSE fallback for environments where a proxy mangles upgrades.
func (s *Server) wsTelemetry(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		s.deps.Log.Debug("websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	sub := s.deps.Hub.Subscribe()
	defer sub.Close()

	// A reader goroutine is required even though the client sends nothing:
	// without it, close and pong frames are never processed and a dead
	// connection is never noticed.
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn.SetReadLimit(512)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-done:
			return
		case <-c.Request.Context().Done():
			return
		case ev, ok := <-sub.C:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// sseTelemetry is the Server-Sent Events fallback.
func (s *Server) sseTelemetry(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// Without this, nginx-style proxies buffer the stream and nothing arrives
	// until the connection closes. Caddy honours it too.
	h.Set("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		fail(c, http.StatusInternalServerError, "streaming is not supported by this server")
		return
	}

	sub := s.deps.Hub.Subscribe()
	defer sub.Close()

	// Tell the browser to back off if the connection drops; the default retry
	// is aggressive enough to hammer a restarting daemon.
	fmt.Fprint(c.Writer, "retry: 5000\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case ev, ok := <-sub.C:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", ev.Type, payload)
			flusher.Flush()
		case <-keepalive.C:
			// A comment frame keeps intermediaries from timing the idle
			// connection out.
			fmt.Fprint(c.Writer, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// Share definitions are kept beside the generated Samba config so the API can
// answer "what shares exist" without parsing smb.conf back.
func (s *Server) sharesStatePath() string {
	return filepath.Join(s.deps.Config.Paths.Config, "samba", "shares.json")
}

func (s *Server) loadShares() ([]samba.Share, error) {
	raw, err := os.ReadFile(s.sharesStatePath())
	if os.IsNotExist(err) {
		return []samba.Share{}, nil
	}
	if err != nil {
		return nil, err
	}
	var shares []samba.Share
	if err := json.Unmarshal(raw, &shares); err != nil {
		return nil, fmt.Errorf("parse share state: %w", err)
	}
	return shares, nil
}

func (s *Server) saveShares(shares []samba.Share) error {
	raw, err := json.MarshalIndent(shares, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.sharesStatePath(), raw, 0o644)
}
