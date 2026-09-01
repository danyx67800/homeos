// Package updater implements HomeOS over-the-air updates.
//
// The shape of an update is deliberate: program code lives in versioned release
// directories and `current` is a symlink. Applying an update is therefore a
// symlink rename — atomic on POSIX — and rolling back is pointing it at the
// previous release. Nothing is ever unpacked over a running installation, so a
// download that dies halfway leaves a half-written staging directory rather
// than a half-written appliance.
//
// User data is never touched. /etc/homeos is merged, /var/lib/homeos and
// /mnt/storage are outside the update's reach entirely.
package updater

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ManifestVersion is the channel format this build understands.
const ManifestVersion = 1

var (
	ErrBadSignature   = errors.New("release signature does not verify")
	ErrBadChecksum    = errors.New("release checksum does not match")
	ErrNoArtifact     = errors.New("no artifact for this architecture")
	ErrTooOld         = errors.New("installed version is too old to update directly")
	ErrManifestFormat = errors.New("malformed release manifest")
)

// Channel is the document served at the update URL. It lists every release the
// channel offers; the client picks the newest it can apply.
type Channel struct {
	ManifestVersion int       `json:"manifest_version"`
	Channel         string    `json:"channel"` // stable | beta
	UpdatedAt       time.Time `json:"updated_at"`
	Releases        []Release `json:"releases"`
}

type Release struct {
	Version    string    `json:"version"`
	ReleasedAt time.Time `json:"released_at"`
	Notes      string    `json:"notes"`
	// MinVersion refuses a direct jump from a build too old to have the
	// migration steps this release assumes. The operator is told to step
	// through an intermediate release rather than silently getting a broken box.
	MinVersion string              `json:"min_version,omitempty"`
	Artifacts  map[string]Artifact `json:"artifacts"` // keyed by GOARCH
}

type Artifact struct {
	URL       string `json:"url"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	// Signature is the detached ed25519 signature over the SHA-256 digest
	// bytes, base64 (standard encoding). Signing the digest rather than the
	// archive means verification does not need the whole file in memory.
	Signature string `json:"signature"`
}

// ArtifactFor returns the artifact for a GOARCH, if the release ships one.
func (r Release) ArtifactFor(arch string) (Artifact, error) {
	a, ok := r.Artifacts[arch]
	if !ok || a.URL == "" {
		return Artifact{}, fmt.Errorf("%w: %s has no %s build", ErrNoArtifact, r.Version, arch)
	}
	return a, nil
}

// Verify checks the digest against the artifact's declared checksum and the
// signature against the channel's public key.
//
// Both, always. The checksum alone proves the bytes arrived intact, which a
// corrupted mirror would fail — but an attacker who controls the mirror
// controls the checksum too. Only the signature proves provenance.
func (a Artifact) Verify(digest []byte, pub ed25519.PublicKey) error {
	want, err := hex.DecodeString(strings.TrimSpace(a.SHA256))
	if err != nil || len(want) != 32 {
		return fmt.Errorf("%w: sha256 field is not a 32-byte hex digest", ErrManifestFormat)
	}
	if len(digest) != len(want) {
		return ErrBadChecksum
	}
	var diff byte
	for i := range want {
		diff |= want[i] ^ digest[i]
	}
	if diff != 0 {
		return fmt.Errorf("%w: got %s, want %s", ErrBadChecksum, hex.EncodeToString(digest), a.SHA256)
	}

	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: no usable public key configured", ErrBadSignature)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(a.Signature))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature is not %d base64 bytes", ErrBadSignature, ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, digest, sig) {
		return ErrBadSignature
	}
	return nil
}

// ParseChannel decodes and sanity-checks a channel document.
func ParseChannel(raw []byte) (*Channel, error) {
	var c Channel
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManifestFormat, err)
	}
	if c.ManifestVersion == 0 {
		return nil, fmt.Errorf("%w: manifest_version is required", ErrManifestFormat)
	}
	if c.ManifestVersion > ManifestVersion {
		return nil, fmt.Errorf("%w: channel format %d is newer than this build understands (%d)",
			ErrManifestFormat, c.ManifestVersion, ManifestVersion)
	}
	for i, r := range c.Releases {
		if _, err := ParseVersion(r.Version); err != nil {
			return nil, fmt.Errorf("%w: release %d has version %q: %v",
				ErrManifestFormat, i, r.Version, err)
		}
	}
	return &c, nil
}
