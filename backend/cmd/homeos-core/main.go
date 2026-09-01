// Command homeos-core is the HomeOS backend daemon.
//
// It is installed at /usr/lib/homeos/bin/homeos-core and supervised by the
// systemd unit phase 1 wrote. Type=notify, so the readiness ping below is what
// makes `systemctl start` block until the API is actually accepting requests.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"

	"github.com/danyx67800/homeos/backend/internal/api"
	"github.com/danyx67800/homeos/backend/internal/appstore"
	"github.com/danyx67800/homeos/backend/internal/auth"
	"github.com/danyx67800/homeos/backend/internal/config"
	"github.com/danyx67800/homeos/backend/internal/dockerx"
	"github.com/danyx67800/homeos/backend/internal/hub"
	"github.com/danyx67800/homeos/backend/internal/samba"
	"github.com/danyx67800/homeos/backend/internal/storage"
	"github.com/danyx67800/homeos/backend/internal/telemetry"
)

// Both are stamped at build time by the release pipeline:
//
//	-ldflags "-X main.Version=1.2.3 -X main.UpdatePublicKey=<base64>"
//
// An unsigned development build simply has no key, and the updater refuses to
// verify anything rather than treating "no key" as "verification passed".
var (
	Version         = "dev"
	UpdatePublicKey = ""
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "homeos-core: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		cfgPath     = flag.String("config", config.DefaultPath, "path to config.yaml")
		showVersion = flag.Bool("version", false, "print the version and exit")
		checkOnly   = flag.Bool("check", false, "validate configuration and environment, then exit")
	)
	flag.Parse()

	// `homeos-core serve` is what the unit file invokes; accept it so the unit
	// reads naturally, but do not require it.
	if flag.NArg() > 0 && flag.Arg(0) != "serve" {
		return fmt.Errorf("unknown command %q (only \"serve\" is accepted)", flag.Arg(0))
	}

	if *showVersion {
		fmt.Printf("homeos-core %s (%s/%s, %s)\n", Version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return nil
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	log := newLogger()
	log.Info("starting", "version", Version, "config", *cfgPath, "arch", runtime.GOARCH)

	if *checkOnly {
		return check(cfg, log)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- Wiring ------------------------------------------------------------
	events := hub.New(32)

	collector := telemetry.NewCollector(
		cfg.Telemetry.SampleInterval(),
		time.Duration(cfg.Telemetry.HistoryRetentionMinutes)*time.Minute,
		log,
	)

	// A missing Docker is a degraded state, not a fatal one: telemetry,
	// storage and shares still work, and Docker may just be slow to start. The
	// health endpoint reports it, and a background loop reconnects.
	docker, err := dockerx.New(cfg.Docker.Socket, cfg.Docker.EdgeNetwork, log)
	if err != nil {
		log.Error("cannot create the Docker client", "socket", cfg.Docker.Socket, "error", err)
		docker = dockerx.Unavailable(err, log)
	} else if err := docker.Ping(ctx); err != nil {
		log.Warn("Docker is not reachable yet", "socket", cfg.Docker.Socket, "error", err)
	}
	defer docker.Close()
	go reconnectDocker(ctx, docker, cfg, log)

	store := storage.NewManager(
		cfg.Storage.MountRoot,
		cfg.Storage.DefaultFilesystem,
		time.Duration(cfg.Storage.SMARTPollIntervalMinutes)*time.Minute,
		log,
	)

	sudo := storage.ExecRunner{Sudo: true, Timeout: 30 * time.Second}
	shares := samba.NewManager(
		cfg.Samba.ManagedConfig,
		cfg.Samba.ShareGroup,
		[]string{cfg.Storage.MountRoot, cfg.Paths.Data},
		log,
		sudo.Run,
	)

	catalog := appstore.NewCatalog(cfg.Paths.Store, cfg.AppStore.Repository,
		cfg.AppStore.Branch, runtime.GOARCH, log)
	if err := catalog.Load(); err != nil {
		log.Warn("no local catalogue yet; it will be fetched shortly", "error", err)
	}

	renderOpt := appstore.RenderOptions{
		AppsDir:       cfg.Paths.Apps,
		EdgeNetwork:   cfg.Docker.EdgeNetwork,
		ProjectPrefix: cfg.Docker.ComposeProjectPrefix,
		DefaultRoute:  cfg.Proxy.DefaultRouteMode,
	}
	installer := appstore.NewInstaller(catalog, renderOpt, log,
		func(dir, project string) appstore.ComposeRunner {
			return dockerx.NewCompose(dir, project)
		},
		// Install progress rides the same stream as telemetry, so the dashboard
		// needs one connection rather than a second polling loop.
		func(j appstore.Job) { events.Publish(hub.Event{Type: "install", Data: j}) },
	)

	authSvc, err := auth.New(cfg.SecretsDir(), cfg.API.SessionTTL())
	if err != nil {
		return fmt.Errorf("initialise authentication: %w", err)
	}
	if authSvc.NeedsSetup() {
		log.Warn("no admin account yet - the dashboard will show the first-run wizard")
	}

	up := buildUpdater(cfg, log, events)

	srv := api.NewServer(api.Deps{
		Config: cfg, Log: log, Hub: events, Collector: collector,
		Docker: docker, Storage: store, Samba: shares,
		Catalog: catalog, Installer: installer, Auth: authSvc,
		Updater: up,
		Version: Version, StartedAt: time.Now(),
	})

	// --- Background workers -------------------------------------------------
	go collector.Run(ctx, func(s telemetry.Snapshot) {
		events.Publish(hub.Event{Type: "metrics", Data: s})
	})
	go pollDisks(ctx, store, events, log)
	go sweepSMART(ctx, store, cfg, log)
	go refreshCatalog(ctx, catalog, cfg, log)
	go purgeSessions(ctx, authSvc)
	if up != nil {
		go autoUpdate(ctx, up, cfg, log)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	// Tell systemd we are up only once the listener is live, so dependent units
	// do not start against a socket that is not accepting yet.
	waitForListener(cfg.API.Addr())
	if sent, err := daemon.SdNotify(false, daemon.SdNotifyReady); err == nil && sent {
		log.Info("notified systemd: ready")
	}
	go watchdog(ctx, log)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	_, _ = daemon.SdNotify(false, daemon.SdNotifyStopping)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("stopped")
	return nil
}
