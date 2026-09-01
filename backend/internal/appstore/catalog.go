package appstore

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Catalog is the local checkout of the app repository.
//
// Layout expected in the repository:
//
//	apps/<id>/homeos-app.yml
//	apps/<id>/icon.svg
//	apps/<id>/screenshots/*.png
//
// A git checkout rather than a packaged index: it makes the catalogue
// forkable, diffable and reviewable, and updating it is a pull rather than a
// bespoke protocol.
type Catalog struct {
	dir    string
	repo   string
	branch string
	log    *slog.Logger

	mu       sync.RWMutex
	apps     map[string]*Manifest
	errs     map[string]string // id -> why it was rejected
	syncedAt time.Time
	arch     string
}

func NewCatalog(dir, repo, branch, arch string, log *slog.Logger) *Catalog {
	if branch == "" {
		branch = "main"
	}
	return &Catalog{
		dir: dir, repo: repo, branch: branch, arch: arch, log: log,
		apps: map[string]*Manifest{}, errs: map[string]string{},
	}
}

// Sync clones or fast-forwards the catalogue, then reloads it. A network
// failure is not fatal: the existing checkout stays usable, which matters on a
// box whose internet is down but whose apps still need managing.
func (c *Catalog) Sync(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	gitDir := filepath.Join(c.dir, ".git")
	var err error
	if _, statErr := os.Stat(gitDir); statErr == nil {
		err = c.git(ctx, c.dir, "fetch", "--depth", "1", "origin", c.branch)
		if err == nil {
			err = c.git(ctx, c.dir, "reset", "--hard", "origin/"+c.branch)
		}
	} else {
		if mkErr := os.MkdirAll(filepath.Dir(c.dir), 0o755); mkErr != nil {
			return fmt.Errorf("create catalogue directory: %w", mkErr)
		}
		os.RemoveAll(c.dir)
		err = c.git(ctx, "", "clone", "--depth", "1", "--branch", c.branch, c.repo, c.dir)
	}

	if err != nil {
		if loadErr := c.Load(); loadErr == nil {
			c.log.Warn("catalogue sync failed; serving the existing checkout", "error", err)
			return nil
		}
		return fmt.Errorf("sync catalogue: %w", err)
	}

	c.mu.Lock()
	c.syncedAt = time.Now()
	c.mu.Unlock()
	return c.Load()
}

func (c *Catalog) git(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	// Never let git prompt for credentials: a private repo URL would otherwise
	// hang the sync until the timeout.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "GCM_INTERACTIVE=never")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		return fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return nil
}

// Load parses every manifest in the checkout. A single broken manifest is
// recorded and skipped rather than failing the whole catalogue, so one bad
// contribution cannot take the store offline.
func (c *Catalog) Load() error {
	appsDir := filepath.Join(c.dir, "apps")
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return fmt.Errorf("read catalogue at %s: %w", appsDir, err)
	}

	apps := map[string]*Manifest{}
	errs := map[string]string{}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(appsDir, e.Name(), "homeos-app.yml")
		raw, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				errs[e.Name()] = err.Error()
			}
			continue
		}
		m, err := Parse(raw)
		if err != nil {
			errs[e.Name()] = err.Error()
			c.log.Warn("skipping invalid manifest", "app", e.Name(), "error", err)
			continue
		}
		if m.ID != e.Name() {
			errs[e.Name()] = fmt.Sprintf("id %q does not match its directory", m.ID)
			continue
		}
		apps[m.ID] = m
	}

	c.mu.Lock()
	c.apps, c.errs = apps, errs
	c.mu.Unlock()

	c.log.Info("catalogue loaded", "apps", len(apps), "rejected", len(errs))
	return nil
}

// List returns apps runnable on this machine. An app whose images are not
// published for this architecture is hidden rather than offered and then
// failing at pull time.
func (c *Catalog) List(category string) []*Manifest {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]*Manifest, 0, len(c.apps))
	for _, m := range c.apps {
		if !m.SupportsArch(c.arch) {
			continue
		}
		if category != "" && m.Category != category {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (c *Catalog) Get(id string) (*Manifest, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.apps[id]
	return m, ok
}

// Rejected exposes manifests that failed to parse, so a catalogue author can
// see why their app is missing instead of guessing.
func (c *Catalog) Rejected() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.errs))
	for k, v := range c.errs {
		out[k] = v
	}
	return out
}

func (c *Catalog) SyncedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.syncedAt
}

// IconPath resolves an app's icon inside the checkout, refusing anything that
// escapes the app's own directory.
func (c *Catalog) IconPath(id string) (string, error) {
	m, ok := c.Get(id)
	if !ok {
		return "", fmt.Errorf("unknown app %q", id)
	}
	if m.Icon == "" {
		return "", fmt.Errorf("app %q has no icon", id)
	}
	base := filepath.Join(c.dir, "apps", id)
	full := filepath.Join(base, filepath.Clean("/"+m.Icon))
	rel, err := filepath.Rel(base, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("icon path %q escapes the app directory", m.Icon)
	}
	if _, err := os.Stat(full); err != nil {
		return "", fmt.Errorf("icon not found: %w", err)
	}
	return full, nil
}

func (m *Manifest) SupportsArch(arch string) bool {
	if arch == "" {
		return true
	}
	for _, a := range m.Architectures {
		if a == arch {
			return true
		}
	}
	return false
}
