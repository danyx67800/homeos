package main

import (
	"context"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"runtime"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"

	"github.com/danyx67800/homeos/backend/internal/auth"
	"github.com/danyx67800/homeos/backend/internal/config"
	"github.com/danyx67800/homeos/backend/internal/dockerx"
	"github.com/danyx67800/homeos/backend/internal/hub"
	"github.com/danyx67800/homeos/backend/internal/storage"
	"github.com/danyx67800/homeos/backend/internal/updater"
)

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	switch os.Getenv("HOMEOS_LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	// Text, not JSON: the destination is the journal, which people read with
	// journalctl. Timestamps are dropped because journald already stamps every
	// line and two of them per record is just noise.
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
}

// check validates the environment without starting anything, so an operator can
// run `homeos-core -check` after editing config.yaml.
func check(cfg *config.Config, log *slog.Logger) error {
	log.Info("configuration parsed", "hostname", cfg.System.Hostname, "api", cfg.API.Addr())
	for name, path := range map[string]string{
		"apps":    cfg.Paths.Apps,
		"data":    cfg.Paths.Data,
		"store":   cfg.Paths.Store,
		"config":  cfg.Paths.Config,
		"storage": cfg.Storage.MountRoot,
	} {
		if _, err := os.Stat(path); err != nil {
			log.Warn("path missing", "name", name, "path", path)
		} else {
			log.Info("path ok", "name", name, "path", path)
		}
	}
	if _, err := os.Stat(cfg.Docker.Socket); err != nil {
		log.Warn("docker socket not found", "socket", cfg.Docker.Socket)
	}
	return nil
}

// waitForListener blocks until the API socket accepts, so the systemd readiness
// notification is not sent before the server can actually answer.
func waitForListener(addr string) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// watchdog answers systemd's WatchdogSec. If the process wedges, systemd
// restarts it rather than leaving a dashboard that hangs forever.
func watchdog(ctx context.Context, log *slog.Logger) {
	interval, err := daemon.SdWatchdogEnabled(false)
	if err != nil || interval == 0 {
		return
	}
	// Ping at half the interval, the conventional margin.
	tick := time.NewTicker(interval / 2)
	defer tick.Stop()
	log.Debug("watchdog active", "interval", interval.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_, _ = daemon.SdNotify(false, daemon.SdNotifyWatchdog)
		}
	}
}

// pollDisks publishes the disk topology on a slow cadence. Separate from the
// metrics loop because lsblk is far more expensive than reading /proc, and the
// set of disks changes rarely.
func pollDisks(ctx context.Context, store *storage.Manager, events *hub.Hub, log *slog.Logger) {
	publish := func() {
		devs, err := store.List(ctx)
		if err != nil {
			log.Debug("disk poll failed", "error", err)
			return
		}
		events.Publish(hub.Event{Type: "disks", Data: devs})
	}
	publish()

	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			publish()
		}
	}
}

// sweepSMART re-reads drive health on the configured interval. The first sweep
// is delayed: at boot every other subsystem is competing for I/O, and spinning
// up all the disks then is the worst possible moment.
func sweepSMART(ctx context.Context, store *storage.Manager, cfg *config.Config, log *slog.Logger) {
	interval := time.Duration(cfg.Storage.SMARTPollIntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}

	for {
		store.RefreshAllHealth(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// refreshCatalog keeps the app store in step with its git repository.
func refreshCatalog(ctx context.Context, cat catalogSyncer, cfg *config.Config, log *slog.Logger) {
	interval := time.Duration(cfg.AppStore.RefreshIntervalHours) * time.Hour
	if interval <= 0 {
		interval = 12 * time.Hour
	}
	// A short initial delay lets the network come up on a slow boot.
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}

	for {
		if err := cat.Sync(ctx); err != nil {
			log.Warn("app catalogue sync failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

type catalogSyncer interface {
	Sync(ctx context.Context) error
}

// purgeSessions bounds the session map on a daemon that runs for months.
func purgeSessions(ctx context.Context, a *auth.Service) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			a.PurgeExpired()
		}
	}
}

// reconnectDocker retries a failed Docker connection so an appliance that
// started before dockerd does not need a manual restart.
func reconnectDocker(ctx context.Context, dc *dockerx.Client, cfg *config.Config, log *slog.Logger) {
	if dc.Available() {
		return
	}
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if dc.Reconnect(ctx, cfg.Docker.Socket, cfg.Docker.EdgeNetwork) {
				return
			}
			log.Debug("Docker still unreachable")
		}
	}
}

// buildUpdater returns nil when updates are not usable on this appliance,
// which the API reports as 503 rather than failing mysteriously later.
func buildUpdater(cfg *config.Config, log *slog.Logger, events *hub.Hub) *updater.Updater {
	if cfg.Update.ChannelURL == "" {
		log.Info("no update channel configured; over-the-air updates are off")
		return nil
	}
	pub, err := updater.LoadPublicKey(UpdatePublicKey, cfg.Update.PublicKeyFile)
	if err != nil {
		// Refusing to run the updater at all is the safe failure: an updater
		// without a key could only ever install unverified code.
		log.Warn("over-the-air updates are disabled", "reason", err)
		return nil
	}

	return updater.New(updater.Config{
		ChannelURL:  cfg.Update.ChannelURL,
		Arch:        runtime.GOARCH,
		Version:     Version,
		ReleasesDir: cfg.Update.ReleasesDir,
		PublicKey:   pub,
		// The helper is started by name, so the version to apply travels
		// through a file. That is also why this needs no sudoers rule beyond
		// the `systemctl start homeos-*` phase 1 already granted.
		ApplyCommand: []string{
			"sudo", "-n", "/usr/bin/systemctl", "start", "homeos-update-apply.service",
		},
	}, log, func(st updater.Status) {
		events.Publish(hub.Event{Type: "update", Data: st})
	})
}

// autoUpdate polls the channel and, when configured to, downloads in the
// background. Applying stays a deliberate act unless auto_apply is set: it
// restarts the appliance, and a NAS in the middle of a transfer is a bad moment
// to be surprised by that.
func autoUpdate(ctx context.Context, up *updater.Updater, cfg *config.Config, log *slog.Logger) {
	if !cfg.Update.AutoCheck {
		return
	}
	// Let the network settle, and stagger boxes that all boot after a power cut.
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(60+rand.Intn(120)) * time.Second):
	}

	for {
		rel, err := up.Check(ctx)
		switch {
		case err != nil:
			log.Warn("update check failed", "error", err)
		case rel == nil:
			log.Debug("no update available")
		default:
			log.Info("update available", "version", rel.Version)
			if path, err := up.Download(ctx, rel); err != nil {
				log.Warn("update download failed", "version", rel.Version, "error", err)
			} else {
				log.Info("update staged", "version", rel.Version, "path", path)
				if err := up.Prune(cfg.Update.KeepReleases); err != nil {
					log.Warn("prune old releases", "error", err)
				}
				if cfg.Update.AutoApply {
					if err := up.Apply(ctx, rel.Version); err != nil {
						log.Error("automatic apply failed", "error", err)
					}
					return // this process is about to be replaced
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(cfg.Update.CheckInterval()):
		}
	}
}
