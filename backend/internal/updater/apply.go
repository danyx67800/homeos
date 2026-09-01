package updater

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Apply hands the staged release to the privileged helper and returns.
//
// It does not wait, and it cannot: the helper stops this very process,
// swaps the `current` symlink and starts the new one. A process cannot
// supervise its own replacement, so the work runs in a transient systemd unit
// that outlives us. If the new build fails its health check the helper puts the
// symlink back and restarts the old one — which is why the rollback still
// happens even though nothing here is left running to notice.
func (u *Updater) Apply(ctx context.Context, version string) error {
	if version == "" {
		version = u.Status().StagedVersion
	}
	if version == "" {
		return errors.New("no staged release to apply")
	}
	dir := filepath.Join(u.cfg.ReleasesDir, version)
	if _, err := os.Stat(filepath.Join(dir, "bin", "homeos-core")); err != nil {
		return fmt.Errorf("release %s is not staged", version)
	}
	if len(u.cfg.ApplyCommand) == 0 {
		return errors.New("no apply command configured")
	}

	u.set(func(s *Status) {
		s.State, s.Progress, s.Error = StateApplying, 0, ""
		s.Message = "applying " + version + "; the dashboard will reconnect"
	})
	u.log.Warn("applying update", "version", version, "from", u.cfg.Version)

	argv := append(append([]string{}, u.cfg.ApplyCommand...), version)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}

	if err := cmd.Start(); err != nil {
		return u.fail(fmt.Errorf("start the update helper: %w", err))
	}
	// Reaped rather than waited on: the helper detaches into its own unit
	// almost immediately, and blocking here would hold the HTTP request open
	// across our own restart.
	go func() { _ = cmd.Wait() }()
	return nil
}

// Prune keeps the current release, the one before it, and anything staged.
// Everything older goes: release directories are ~25 MB each and an appliance
// that has updated twenty times should not be carrying half a gigabyte of
// history it can never roll back to.
func (u *Updater) Prune(keep int) error {
	if keep < 2 {
		keep = 2
	}
	entries, err := os.ReadDir(u.cfg.ReleasesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	type rel struct {
		name string
		v    Version
	}
	var found []rel
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		v, err := ParseVersion(e.Name())
		if err != nil {
			continue
		}
		found = append(found, rel{e.Name(), v})
	}
	if len(found) <= keep {
		return nil
	}

	// Newest first, then drop the tail.
	for i := 1; i < len(found); i++ {
		for j := i; j > 0 && found[j-1].v.LessThan(found[j].v); j-- {
			found[j-1], found[j] = found[j], found[j-1]
		}
	}
	current := u.resolveCurrent()
	for _, r := range found[keep:] {
		if r.name == current || r.name == u.cfg.Version {
			continue
		}
		path := filepath.Join(u.cfg.ReleasesDir, r.name)
		if err := os.RemoveAll(path); err != nil {
			u.log.Warn("prune release", "version", r.name, "error", err)
			continue
		}
		u.log.Info("pruned old release", "version", r.name)
	}
	return nil
}

// resolveCurrent reads the version the `current` symlink points at.
func (u *Updater) resolveCurrent() string {
	link := filepath.Join(filepath.Dir(u.cfg.ReleasesDir), "current")
	target, err := os.Readlink(link)
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// LoadPublicKey reads the channel signing key.
//
// The build-time key is the default and the common case; a file overrides it so
// an operator can run their own channel without rebuilding. Both are refused
// unless they decode to a real ed25519 key, because a silently-empty key would
// turn signature verification into a no-op.
func LoadPublicKey(builtin, path string) (ed25519.PublicKey, error) {
	raw := strings.TrimSpace(builtin)
	source := "built-in"

	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			raw, source = strings.TrimSpace(string(b)), path
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	}
	if raw == "" {
		return nil, errors.New("no update signing key configured")
	}

	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("update key from %s is not base64: %w", source, err)
	}
	if len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("update key from %s is %d bytes, want %d",
			source, len(key), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(key), nil
}
