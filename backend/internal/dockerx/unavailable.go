package dockerx

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Unavailable returns a Client that fails every call with the reason Docker
// could not be reached.
//
// It exists because the daemon must survive a missing Docker: telemetry,
// storage and Samba are all still useful and manageable without it, and Docker
// may simply be slow to start. Refusing to boot would turn a recoverable
// condition into an appliance that shows nothing at all.
func Unavailable(reason error, log *slog.Logger) *Client {
	return &Client{unavailable: reason, log: log}
}

// Available reports whether container operations can be attempted.
func (c *Client) Available() bool { return c.unavailable == nil && c.api != nil }

func (c *Client) unavailableErr() error {
	return fmt.Errorf("Docker is not available: %w", c.unavailable)
}

// Reconnect retries the connection, so a daemon that started before Docker can
// recover without being restarted.
func (c *Client) Reconnect(ctx context.Context, socket, edge string) bool {
	if c.Available() {
		return true
	}
	fresh, err := New(socket, edge, c.log)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := fresh.Ping(ctx); err != nil {
		fresh.Close()
		return false
	}
	c.api, c.unavailable = fresh.api, nil
	c.log.Info("reconnected to Docker")
	return true
}
