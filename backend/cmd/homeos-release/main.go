// Command homeos-release builds the update channel: it generates signing keys,
// signs release archives, and assembles the channel manifest clients poll.
//
// It is a build-host tool, never installed on an appliance. The private key
// never leaves the machine that runs it.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/danyx67800/homeos/backend/internal/updater"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	case "channel":
		err = channel(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "homeos-release: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`homeos-release — build the HomeOS update channel

  keygen  -out DIR              generate an ed25519 signing key pair
  sign    -key FILE ARCHIVE...  print the signature of each archive
  channel -key FILE -version V -base-url URL ARCHIVE...
                                write a channel manifest to stdout
  verify  -pub KEY -manifest F  re-verify a manifest against local archives

The private key never leaves the build host. The public half is compiled into
homeos-core with -ldflags "-X main.UpdatePublicKey=<base64>".
`)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", ".", "directory to write the key pair into")
	fs.Parse(args)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}

	privPath := filepath.Join(*out, "homeos-update.key")
	pubPath := filepath.Join(*out, "homeos-update.pub")

	// 0600, and refuse to overwrite: silently replacing a signing key would
	// orphan every appliance already trusting the old one.
	if _, err := os.Stat(privPath); err == nil {
		return fmt.Errorf("%s already exists; move it aside deliberately", privPath)
	}
	if err := os.WriteFile(privPath, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(pubPath, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644); err != nil {
		return err
	}

	fmt.Printf("private key  %s  (keep this secret, back it up)\n", privPath)
	fmt.Printf("public key   %s\n", pubPath)
	fmt.Printf("\nCompile it into the daemon with:\n")
	fmt.Printf("  -ldflags \"-X main.UpdatePublicKey=%s\"\n", base64.StdEncoding.EncodeToString(pub))
	return nil
}

func loadPrivate(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read signing key: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("signing key is not base64: %w", err)
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("signing key is %d bytes, want %d", len(key), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(key), nil
}

func digestOf(path string) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return nil, 0, err
	}
	return h.Sum(nil), n, nil
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	keyPath := fs.String("key", "", "path to the private signing key")
	fs.Parse(args)
	if *keyPath == "" || fs.NArg() == 0 {
		return fmt.Errorf("usage: homeos-release sign -key FILE ARCHIVE...")
	}
	priv, err := loadPrivate(*keyPath)
	if err != nil {
		return err
	}
	for _, a := range fs.Args() {
		d, size, err := digestOf(a)
		if err != nil {
			return err
		}
		fmt.Printf("%s\n  size      %d\n  sha256    %s\n  signature %s\n",
			filepath.Base(a), size, hex.EncodeToString(d),
			base64.StdEncoding.EncodeToString(ed25519.Sign(priv, d)))
	}
	return nil
}

// channel emits the manifest an appliance polls. Archives are named
// homeos-<version>-linux-<arch>.tar.gz; the architecture is taken from the
// filename so the caller cannot mislabel one.
func channel(args []string) error {
	fs := flag.NewFlagSet("channel", flag.ExitOnError)
	keyPath := fs.String("key", "", "path to the private signing key")
	version := fs.String("version", "", "release version, e.g. 1.1.0")
	baseURL := fs.String("base-url", "", "where the archives will be served from")
	name := fs.String("channel", "stable", "channel name")
	notes := fs.String("notes", "", "release notes (or @file to read them)")
	minVer := fs.String("min-version", "", "oldest version that may update directly to this one")
	merge := fs.String("merge", "", "existing manifest to add this release to")
	fs.Parse(args)

	if *keyPath == "" || *version == "" || *baseURL == "" || fs.NArg() == 0 {
		return fmt.Errorf("usage: homeos-release channel -key FILE -version V -base-url URL ARCHIVE...")
	}
	if _, err := updater.ParseVersion(*version); err != nil {
		return fmt.Errorf("version %q: %w", *version, err)
	}
	priv, err := loadPrivate(*keyPath)
	if err != nil {
		return err
	}

	body := *notes
	if strings.HasPrefix(body, "@") {
		raw, err := os.ReadFile(strings.TrimPrefix(body, "@"))
		if err != nil {
			return fmt.Errorf("read release notes: %w", err)
		}
		body = strings.TrimSpace(string(raw))
	}

	rel := updater.Release{
		Version:    *version,
		ReleasedAt: time.Now().UTC().Truncate(time.Second),
		Notes:      body,
		MinVersion: *minVer,
		Artifacts:  map[string]updater.Artifact{},
	}

	for _, path := range fs.Args() {
		base := filepath.Base(path)
		arch := archFromName(base)
		if arch == "" {
			return fmt.Errorf("cannot tell the architecture from %q; "+
				"name archives homeos-<version>-linux-<arch>.tar.gz", base)
		}
		if _, dup := rel.Artifacts[arch]; dup {
			return fmt.Errorf("two archives claim architecture %s", arch)
		}
		d, size, err := digestOf(path)
		if err != nil {
			return err
		}
		rel.Artifacts[arch] = updater.Artifact{
			URL:       strings.TrimSuffix(*baseURL, "/") + "/" + base,
			SizeBytes: size,
			SHA256:    hex.EncodeToString(d),
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, d)),
		}
	}

	ch := updater.Channel{
		ManifestVersion: updater.ManifestVersion,
		Channel:         *name,
		UpdatedAt:       time.Now().UTC().Truncate(time.Second),
	}
	if *merge != "" {
		raw, err := os.ReadFile(*merge)
		if err != nil {
			return fmt.Errorf("read the manifest being merged into: %w", err)
		}
		prev, err := updater.ParseChannel(raw)
		if err != nil {
			return err
		}
		for _, r := range prev.Releases {
			if r.Version != rel.Version { // a re-release replaces its predecessor
				ch.Releases = append(ch.Releases, r)
			}
		}
	}
	ch.Releases = append(ch.Releases, rel)

	// Newest first: a human reading the file sees the current release at the
	// top, and a truncated fetch still yields the one that matters.
	sort.Slice(ch.Releases, func(i, j int) bool {
		a, _ := updater.ParseVersion(ch.Releases[i].Version)
		b, _ := updater.ParseVersion(ch.Releases[j].Version)
		return b.LessThan(a)
	})

	out, err := json.MarshalIndent(ch, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

func archFromName(base string) string {
	switch {
	case strings.Contains(base, "linux-amd64"), strings.Contains(base, "-x86_64"):
		return "amd64"
	case strings.Contains(base, "linux-arm64"), strings.Contains(base, "-aarch64"):
		return "arm64"
	}
	return ""
}

// verify re-checks a manifest against archives on disk, so a release can be
// validated before it is published rather than after an appliance rejects it.
func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pubPath := fs.String("pub", "", "path to the public key")
	manifest := fs.String("manifest", "", "path to the channel manifest")
	dir := fs.String("dir", ".", "directory holding the archives")
	fs.Parse(args)
	if *pubPath == "" || *manifest == "" {
		return fmt.Errorf("usage: homeos-release verify -pub KEY -manifest FILE [-dir DIR]")
	}

	pubRaw, err := os.ReadFile(*pubPath)
	if err != nil {
		return err
	}
	pub, err := updater.LoadPublicKey(strings.TrimSpace(string(pubRaw)), "")
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*manifest)
	if err != nil {
		return err
	}
	ch, err := updater.ParseChannel(raw)
	if err != nil {
		return err
	}

	problems := 0
	for _, rel := range ch.Releases {
		for arch, art := range rel.Artifacts {
			path := filepath.Join(*dir, filepath.Base(art.URL))
			d, size, err := digestOf(path)
			if err != nil {
				fmt.Printf("  MISSING  %s %s (%v)\n", rel.Version, arch, err)
				problems++
				continue
			}
			if size != art.SizeBytes {
				fmt.Printf("  SIZE     %s %s: %d on disk, %d in manifest\n",
					rel.Version, arch, size, art.SizeBytes)
				problems++
				continue
			}
			if err := art.Verify(d, pub); err != nil {
				fmt.Printf("  BAD      %s %s: %v\n", rel.Version, arch, err)
				problems++
				continue
			}
			fmt.Printf("  ok       %s %s (%d bytes)\n", rel.Version, arch, size)
		}
	}
	if problems > 0 {
		return fmt.Errorf("%d artifact(s) failed verification", problems)
	}
	return nil
}
