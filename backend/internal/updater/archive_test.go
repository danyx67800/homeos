package updater

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type entry struct {
	name string
	typ  byte
	body string
	mode int64
	link string
}

func buildArchive(t *testing.T, entries []entry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{
			Name: e.name, Typeflag: e.typ, Mode: mode,
			Size: int64(len(e.body)), Linkname: e.link,
		}
		if e.typ == tar.TypeDir {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	tw.Close()
	gz.Close()
	return path
}

func TestExtractNormalRelease(t *testing.T) {
	archive := buildArchive(t, []entry{
		{name: "bin/", typ: tar.TypeDir},
		{name: "bin/homeos-core", typ: tar.TypeReg, body: "#!/bin/true\n", mode: 0o755},
		{name: "web/index.html", typ: tar.TypeReg, body: "<!doctype html>"},
		{name: "web/assets/app.js", typ: tar.TypeReg, body: "console.log(1)"},
	})
	dest := filepath.Join(t.TempDir(), "out")
	if err := extractTarGz(archive, dest); err != nil {
		t.Fatalf("extract: %v", err)
	}

	for _, p := range []string{"bin/homeos-core", "web/index.html", "web/assets/app.js"} {
		if _, err := os.Stat(filepath.Join(dest, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	// The execute bit is carried across; without it the swapped-in binary
	// cannot start and the update rolls back for no good reason. Windows has
	// no POSIX permission bits, so the assertion only means anything on the
	// platform HomeOS actually runs on.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dest, "bin/homeos-core"))
		if err == nil && info.Mode().Perm()&0o100 == 0 {
			t.Errorf("binary is not executable: %v", info.Mode())
		}
	}
}

// A release archive is downloaded from the network. Every one of these would
// let a hostile or compromised channel write outside the release directory.
func TestExtractRefusesEscapes(t *testing.T) {
	for name, e := range map[string]entry{
		"parent traversal":   {name: "../../etc/sudoers.d/homeos", typ: tar.TypeReg, body: "x"},
		"absolute path":      {name: "/etc/shadow", typ: tar.TypeReg, body: "x"},
		"traversal mid-path": {name: "bin/../../../tmp/pwn", typ: tar.TypeReg, body: "x"},
		"backslash path":     {name: `..\..\windows\system32`, typ: tar.TypeReg, body: "x"},
		"empty name":         {name: "", typ: tar.TypeReg, body: "x"},
		"symlink":            {name: "bin/evil", typ: tar.TypeSymlink, link: "/etc/passwd"},
		"hardlink":           {name: "bin/evil", typ: tar.TypeLink, link: "/etc/passwd"},
		"device node":        {name: "dev/sda", typ: tar.TypeChar},
		"fifo":               {name: "p", typ: tar.TypeFifo},
	} {
		t.Run(name, func(t *testing.T) {
			archive := buildArchive(t, []entry{e})
			dest := filepath.Join(t.TempDir(), "out")
			if err := extractTarGz(archive, dest); err == nil {
				t.Errorf("archive containing %q was extracted", e.name)
			}
		})
	}
}

// Verified separately from the refusal above: an escape must not leave a file
// behind before it is noticed.
func TestExtractLeavesNothingOutside(t *testing.T) {
	base := t.TempDir()
	dest := filepath.Join(base, "release")
	victim := filepath.Join(base, "victim")

	archive := buildArchive(t, []entry{
		{name: "bin/homeos-core", typ: tar.TypeReg, body: "ok", mode: 0o755},
		{name: "../victim", typ: tar.TypeReg, body: "owned"},
	})
	if err := extractTarGz(archive, dest); err == nil {
		t.Fatal("traversal entry was accepted")
	}
	if _, err := os.Stat(victim); err == nil {
		t.Error("a file was written outside the destination directory")
	}
}

func TestExtractRejectsNonGzip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.tar.gz")
	os.WriteFile(path, []byte("this is not a gzip stream"), 0o644)
	if err := extractTarGz(path, filepath.Join(t.TempDir(), "out")); err == nil {
		t.Error("a non-gzip file was accepted")
	}
}

func TestSafeJoin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	for _, good := range []string{"bin/homeos-core", "web/index.html", "./a/b"} {
		if _, err := safeJoin(root, good); err != nil {
			t.Errorf("safeJoin(%q) = %v", good, err)
		}
	}
	for _, bad := range []string{"", "/abs", "../up", "a/../../up", `back\slash`} {
		if p, err := safeJoin(root, bad); err == nil {
			t.Errorf("safeJoin(%q) = %q, want an error", bad, p)
		}
	}
	// A resolved path must always stay under the root.
	got, err := safeJoin(root, "web/assets/app.js")
	if err != nil || !strings.HasPrefix(got, root) {
		t.Errorf("safeJoin escaped the root: %q (%v)", got, err)
	}
}
