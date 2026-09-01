package updater

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Download fetches a release, verifies it, and unpacks it into its own
// directory under releases/.
//
// Nothing live is touched. The result is a staged release that Apply can swap
// to, or that can simply be deleted if the operator changes their mind.
func (u *Updater) Download(ctx context.Context, rel *Release) (string, error) {
	art, err := rel.ArtifactFor(u.cfg.Arch)
	if err != nil {
		return "", u.fail(err)
	}
	if art.SizeBytes > maxArtifact {
		return "", u.fail(fmt.Errorf("release archive declares %d bytes, over the %d limit",
			art.SizeBytes, maxArtifact))
	}

	u.set(func(s *Status) {
		s.State, s.Progress, s.Error = StateDownloading, 0, ""
		s.Message = "downloading " + rel.Version
	})

	if err := os.MkdirAll(u.cfg.ReleasesDir, 0o755); err != nil {
		return "", u.fail(fmt.Errorf("create releases directory: %w", err))
	}

	// The archive lands beside the release directories so the rename that
	// publishes it stays on one filesystem.
	tmp, err := os.CreateTemp(u.cfg.ReleasesDir, ".download-*.tar.gz")
	if err != nil {
		return "", u.fail(fmt.Errorf("stage download: %w", err))
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	digest, err := u.fetch(ctx, art, tmp)
	tmp.Close()
	if err != nil {
		return "", u.fail(err)
	}

	u.set(func(s *Status) { s.State, s.Progress, s.Message = StateVerifying, 100, "verifying signature" })
	if err := art.Verify(digest, u.cfg.PublicKey); err != nil {
		return "", u.fail(fmt.Errorf("release %s: %w", rel.Version, err))
	}

	target := filepath.Join(u.cfg.ReleasesDir, rel.Version)
	// A previous half-extracted attempt must not be merged into.
	staging := target + ".staging"
	os.RemoveAll(staging)
	if err := extractTarGz(tmpName, staging); err != nil {
		os.RemoveAll(staging)
		return "", u.fail(fmt.Errorf("unpack release: %w", err))
	}

	if err := u.sanityCheck(ctx, staging, rel.Version); err != nil {
		os.RemoveAll(staging)
		return "", u.fail(err)
	}

	os.RemoveAll(target)
	if err := publish(staging, target); err != nil {
		os.RemoveAll(staging)
		return "", u.fail(fmt.Errorf("publish staged release: %w", err))
	}

	u.set(func(s *Status) {
		s.State, s.Progress, s.StagedVersion = StateStaged, 100, rel.Version
		s.Message = rel.Version + " is staged and ready to apply"
	})
	u.log.Info("release staged", "version", rel.Version, "path", target)
	return target, nil
}

// fetch streams the body to w while hashing it, and returns the digest.
func (u *Updater) fetch(ctx context.Context, art Artifact, w io.Writer) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, art.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "homeos-core/"+u.cfg.Version)

	res, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download release: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %s", res.Status)
	}

	total := art.SizeBytes
	if total <= 0 {
		total = res.ContentLength
	}

	h := sha256.New()
	pr := &progressReader{
		r:     io.LimitReader(res.Body, maxArtifact+1),
		total: total,
		// Throttled: a 25 MB download over a 32 KB buffer would otherwise
		// publish several hundred events a second to every open dashboard.
		report: throttle(500*time.Millisecond, func(pct int) {
			u.set(func(s *Status) { s.Progress = pct })
		}),
	}

	n, err := io.Copy(io.MultiWriter(w, h), pr)
	if err != nil {
		return nil, fmt.Errorf("download release: %w", err)
	}
	if n > maxArtifact {
		return nil, fmt.Errorf("release archive exceeds the %d byte limit", maxArtifact)
	}
	if art.SizeBytes > 0 && n != art.SizeBytes {
		return nil, fmt.Errorf("downloaded %d bytes, manifest declared %d", n, art.SizeBytes)
	}
	return h.Sum(nil), nil
}

// sanityCheck refuses a release whose binary will not run on this machine.
// Finding that out now costs a few milliseconds; finding it out after the
// symlink swap costs a restart loop and a rollback.
func (u *Updater) sanityCheck(ctx context.Context, dir, version string) error {
	bin := filepath.Join(dir, "bin", "homeos-core")
	if runtime.GOOS == "windows" {
		// Only so the release path can be exercised on a developer machine;
		// an appliance ships the suffix-less name.
		if _, err := os.Stat(bin + ".exe"); err == nil {
			bin += ".exe"
		}
	}
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("release is missing bin/homeos-core")
	}
	if _, err := os.Stat(filepath.Join(dir, "web", "index.html")); err != nil {
		return fmt.Errorf("release is missing the dashboard (web/index.html)")
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		return fmt.Errorf("make the new binary executable: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the new binary does not run on this machine: %w", err)
	}
	if !strings.Contains(string(out), version) {
		return fmt.Errorf("release %s contains a binary reporting %q",
			version, strings.TrimSpace(string(out)))
	}
	return nil
}

// publish renames the staged directory into place, retrying briefly.
//
// The sanity check has just executed the binary inside the staging directory,
// and something may still hold a handle to it — an on-access virus scanner, an
// exec that has not fully reaped, an indexer. Failing the whole download over a
// transient rename would throw away a verified 25 MB archive for no reason.
func publish(staging, target string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = os.Rename(staging, target); err == nil {
			return nil
		}
		time.Sleep(time.Duration(200*(attempt+1)) * time.Millisecond)
	}
	return err
}
