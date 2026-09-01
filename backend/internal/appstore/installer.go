package appstore

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// InstallState is what the dashboard's progress bar renders.
type InstallState string

const (
	StateQueued    InstallState = "queued"
	StateResolving InstallState = "resolving"
	StatePulling   InstallState = "pulling"
	StateStarting  InstallState = "starting"
	StateInstalled InstallState = "installed"
	StateFailed    InstallState = "failed"
	StateRemoving  InstallState = "removing"
	StateRemoved   InstallState = "removed"
)

type Job struct {
	AppID    string       `json:"app_id"`
	State    InstallState `json:"state"`
	Progress int          `json:"progress"` // 0-100, coarse but monotonic
	Message  string       `json:"message,omitempty"`
	Error    string       `json:"error,omitempty"`
	Log      []string     `json:"log,omitempty"`
	Started  time.Time    `json:"started"`
	Finished *time.Time   `json:"finished,omitempty"`
}

// ComposeRunner is the subset of dockerx.Compose the installer needs, as an
// interface so installation can be tested without a Docker daemon.
type ComposeRunner interface {
	Config(ctx context.Context) error
	Up(ctx context.Context, onLine func(string)) error
	Down(ctx context.Context, removeVolumes bool, onLine func(string)) error
}

type Installer struct {
	catalog    *Catalog
	appsDir    string
	renderOpt  RenderOptions
	log        *slog.Logger
	newCompose func(dir, project string) ComposeRunner
	onUpdate   func(Job)

	mu   sync.RWMutex
	jobs map[string]*Job
}

func NewInstaller(cat *Catalog, opt RenderOptions, log *slog.Logger,
	newCompose func(dir, project string) ComposeRunner, onUpdate func(Job)) *Installer {
	return &Installer{
		catalog: cat, appsDir: opt.AppsDir, renderOpt: opt, log: log,
		newCompose: newCompose, onUpdate: onUpdate,
		jobs: map[string]*Job{},
	}
}

func (in *Installer) Job(appID string) (Job, bool) {
	in.mu.RLock()
	defer in.mu.RUnlock()
	j, ok := in.jobs[appID]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

func (in *Installer) Jobs() []Job {
	in.mu.RLock()
	defer in.mu.RUnlock()
	out := make([]Job, 0, len(in.jobs))
	for _, j := range in.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

func (in *Installer) update(appID string, mutate func(*Job)) {
	in.mu.Lock()
	j, ok := in.jobs[appID]
	if !ok {
		j = &Job{AppID: appID, Started: time.Now()}
		in.jobs[appID] = j
	}
	mutate(j)
	snapshot := *j
	in.mu.Unlock()

	if in.onUpdate != nil {
		in.onUpdate(snapshot)
	}
}

// Install writes the stack and brings it up. It runs synchronously; the API
// layer calls it in a goroutine and the caller follows progress over the
// telemetry stream.
func (in *Installer) Install(ctx context.Context, appID string, answers map[string]string) error {
	m, ok := in.catalog.Get(appID)
	if !ok {
		return fmt.Errorf("unknown app %q", appID)
	}
	if !m.SupportsArch(in.renderOpt.Env["HOMEOS_ARCH"]) && in.renderOpt.Env["HOMEOS_ARCH"] != "" {
		return fmt.Errorf("%s is not published for this architecture", m.Name)
	}

	in.update(appID, func(j *Job) {
		*j = Job{AppID: appID, State: StateQueued, Started: time.Now()}
	})

	resolved, err := ResolveEnv(m, answers)
	if err != nil {
		in.fail(appID, err)
		return err
	}
	in.update(appID, func(j *Job) {
		j.State, j.Progress, j.Message = StateResolving, 10, "preparing stack"
	})

	appDir := filepath.Join(in.appsDir, appID)
	if err := os.MkdirAll(filepath.Join(appDir, "data"), 0o750); err != nil {
		in.fail(appID, fmt.Errorf("create app directory: %w", err))
		return err
	}

	opt := in.renderOpt
	opt.Env = resolved
	composeYAML, err := Render(m, opt)
	if err != nil {
		in.fail(appID, err)
		return err
	}
	if err := os.WriteFile(filepath.Join(appDir, "docker-compose.yml"), composeYAML, 0o640); err != nil {
		in.fail(appID, fmt.Errorf("write compose file: %w", err))
		return err
	}
	// The manifest is kept beside the stack so an update can diff against what
	// was actually installed, and an uninstall knows what it is removing even
	// if the app has since left the catalogue.
	if err := in.writeInstalledManifest(appDir, m, resolved); err != nil {
		in.fail(appID, err)
		return err
	}

	project := fmt.Sprintf("%s-%s", in.renderOpt.ProjectPrefix, appID)
	comp := in.newCompose(appDir, project)

	if err := comp.Config(ctx); err != nil {
		in.fail(appID, fmt.Errorf("generated stack is invalid: %w", err))
		return err
	}

	in.update(appID, func(j *Job) {
		j.State, j.Progress, j.Message = StatePulling, 25, "downloading images"
	})

	if err := comp.Up(ctx, func(line string) {
		in.update(appID, func(j *Job) {
			j.Log = appendCapped(j.Log, line, 200)
			if j.Progress < 90 {
				j.Progress++
			}
			if strings.Contains(line, "Creating") || strings.Contains(line, "Starting") {
				j.State, j.Message = StateStarting, "starting containers"
			}
		})
	}); err != nil {
		in.fail(appID, err)
		return err
	}

	now := time.Now()
	in.update(appID, func(j *Job) {
		j.State, j.Progress, j.Message = StateInstalled, 100, "installed"
		j.Finished = &now
	})
	in.log.Info("app installed", "app", appID, "version", m.Version)
	return nil
}

// Uninstall stops the stack. Data under the app directory is kept unless
// purge is set, because "I want this app gone" and "I want my photos gone" are
// different intentions.
func (in *Installer) Uninstall(ctx context.Context, appID string, purge bool) error {
	appDir := filepath.Join(in.appsDir, appID)
	if _, err := os.Stat(filepath.Join(appDir, "docker-compose.yml")); err != nil {
		return fmt.Errorf("%s is not installed", appID)
	}

	in.update(appID, func(j *Job) {
		j.State, j.Progress, j.Message = StateRemoving, 10, "stopping containers"
	})

	project := fmt.Sprintf("%s-%s", in.renderOpt.ProjectPrefix, appID)
	comp := in.newCompose(appDir, project)
	if err := comp.Down(ctx, purge, func(line string) {
		in.update(appID, func(j *Job) { j.Log = appendCapped(j.Log, line, 200) })
	}); err != nil {
		in.fail(appID, err)
		return err
	}

	if purge {
		if err := os.RemoveAll(appDir); err != nil {
			in.fail(appID, fmt.Errorf("remove app data: %w", err))
			return err
		}
		in.log.Warn("app purged including data", "app", appID)
	} else {
		// Keep the data, drop the stack definition so the app no longer shows
		// as installed.
		os.Remove(filepath.Join(appDir, "docker-compose.yml"))
		in.log.Info("app uninstalled, data kept", "app", appID, "data", appDir)
	}

	now := time.Now()
	in.update(appID, func(j *Job) {
		j.State, j.Progress, j.Message = StateRemoved, 100, "removed"
		j.Finished = &now
	})
	return nil
}

func (in *Installer) fail(appID string, err error) {
	now := time.Now()
	in.update(appID, func(j *Job) {
		j.State, j.Error, j.Finished = StateFailed, err.Error(), &now
	})
	in.log.Error("app operation failed", "app", appID, "error", err)
}

func appendCapped(s []string, line string, max int) []string {
	s = append(s, line)
	if len(s) > max {
		s = s[len(s)-max:]
	}
	return s
}
