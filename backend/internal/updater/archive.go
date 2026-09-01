package updater

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// maxEntry bounds a single file inside the archive, independently of the
// archive's own compressed size. Without it a small tarball can decompress into
// something that fills the disk.
const maxEntry = 256 << 20

// extractTarGz unpacks an archive into dest.
//
// Every entry is checked for path traversal: a tar can contain "../../etc/
// sudoers.d/homeos", and an extractor that simply joins the name onto the
// destination will happily write it. Symlinks are refused for the same reason —
// a link to /etc followed by a write through it is the same escape in two steps.
func extractTarGz(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}

		target, err := safeJoin(root, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if hdr.Size > maxEntry {
				return fmt.Errorf("archive entry %q is %d bytes, over the limit", hdr.Name, hdr.Size)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			// Only the owner-execute bit is honoured from the archive; the
			// rest of the mode is ours, so a release cannot ship a
			// world-writable or setuid file.
			mode := os.FileMode(0o644)
			if hdr.FileInfo().Mode().Perm()&0o100 != 0 {
				mode = 0o755
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			n, err := io.Copy(out, io.LimitReader(tr, maxEntry+1))
			out.Close()
			if err != nil {
				return fmt.Errorf("write %s: %w", hdr.Name, err)
			}
			if n > maxEntry {
				return fmt.Errorf("archive entry %q exceeds the size limit", hdr.Name)
			}
		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("archive contains a link (%q); releases must be plain files", hdr.Name)
		default:
			// Devices, FIFOs and sockets have no business in a release.
			return fmt.Errorf("archive entry %q has unsupported type %d", hdr.Name, hdr.Typeflag)
		}
	}
}

// safeJoin resolves a tar entry name against root, refusing anything that is
// not a plain relative path underneath it.
//
// The ".." segments are rejected outright rather than cleaned away. Cleaning
// would turn "../../etc/sudoers.d/homeos" into "<root>/etc/sudoers.d/homeos",
// which does not escape — but it silently writes a file somewhere nobody asked
// for. A genuine release never contains such a name, so the honest response is
// to refuse the archive rather than quietly rewrite it.
func safeJoin(root, name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, `\`) {
		return "", fmt.Errorf("archive entry %q is not a relative path", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return "", fmt.Errorf("archive entry %q contains a parent-directory segment", name)
		}
	}

	target := filepath.Join(root, filepath.Clean("/"+name))
	// Belt and braces: even with the segment check above, confirm the result
	// really is under root before anything is written through it.
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q escapes the destination directory", name)
	}
	return target, nil
}

// progressReader reports how far through a download it is.
type progressReader struct {
	r      io.Reader
	total  int64
	read   int64
	report func(int)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.total > 0 && p.report != nil {
		pct := int(p.read * 100 / p.total)
		if pct > 100 {
			pct = 100
		}
		p.report(pct)
	}
	return n, err
}

// throttle rate-limits a callback, always letting the final 100% through so the
// bar never stops just short.
func throttle(d time.Duration, fn func(int)) func(int) {
	var mu sync.Mutex
	var last time.Time
	return func(pct int) {
		mu.Lock()
		defer mu.Unlock()
		if pct < 100 && time.Since(last) < d {
			return
		}
		last = time.Now()
		fn(pct)
	}
}
