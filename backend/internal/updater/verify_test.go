package updater

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
)

func signed(t *testing.T, payload []byte) (Artifact, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	return Artifact{
		URL:       "https://example.invalid/r.tar.gz",
		SizeBytes: int64(len(payload)),
		SHA256:    hex.EncodeToString(sum[:]),
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sum[:])),
	}, pub
}

func TestVerifyAcceptsGenuineRelease(t *testing.T) {
	payload := []byte("a release archive")
	art, pub := signed(t, payload)
	sum := sha256.Sum256(payload)
	if err := art.Verify(sum[:], pub); err != nil {
		t.Fatalf("genuine release rejected: %v", err)
	}
}

// The checksum alone only proves the bytes arrived intact. An attacker who
// controls the mirror controls the checksum too, so a tampered archive with a
// matching checksum must still be refused.
func TestVerifyRejectsTamperedArchiveWithMatchingChecksum(t *testing.T) {
	art, pub := signed(t, []byte("a release archive"))

	evil := []byte("a malicious archive")
	evilSum := sha256.Sum256(evil)
	art.SHA256 = hex.EncodeToString(evilSum[:]) // mirror rewrote the checksum too

	err := art.Verify(evilSum[:], pub)
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered release accepted (err = %v)", err)
	}
}

func TestVerifyRejectsWrongSigningKey(t *testing.T) {
	payload := []byte("a release archive")
	art, _ := signed(t, payload)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)

	sum := sha256.Sum256(payload)
	if err := art.Verify(sum[:], otherPub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("release signed by another key accepted (err = %v)", err)
	}
}

func TestVerifyRejectsCorruptDownload(t *testing.T) {
	payload := []byte("a release archive")
	art, pub := signed(t, payload)

	corrupt := sha256.Sum256([]byte("truncated download"))
	if err := art.Verify(corrupt[:], pub); !errors.Is(err, ErrBadChecksum) {
		t.Fatalf("corrupt download accepted (err = %v)", err)
	}
}

// An empty or absent key must never be treated as "verification passed".
func TestVerifyRefusesWithoutAKey(t *testing.T) {
	payload := []byte("a release archive")
	art, _ := signed(t, payload)
	sum := sha256.Sum256(payload)

	for name, key := range map[string]ed25519.PublicKey{
		"nil":   nil,
		"empty": {},
		"short": make([]byte, 16),
	} {
		if err := art.Verify(sum[:], key); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s key was accepted (err = %v)", name, err)
		}
	}
}

func TestVerifyRejectsMalformedFields(t *testing.T) {
	payload := []byte("a release archive")
	sum := sha256.Sum256(payload)
	_, pub := signed(t, payload)

	for name, mutate := range map[string]func(*Artifact){
		"non-hex checksum": func(a *Artifact) { a.SHA256 = "not-hex" },
		"short checksum":   func(a *Artifact) { a.SHA256 = "aabb" },
		"non-base64 sig":   func(a *Artifact) { a.Signature = "!!!not base64!!!" },
		"truncated sig":    func(a *Artifact) { a.Signature = base64.StdEncoding.EncodeToString([]byte("short")) },
		"empty sig":        func(a *Artifact) { a.Signature = "" },
	} {
		art, _ := signed(t, payload)
		mutate(&art)
		if err := art.Verify(sum[:], pub); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestLoadPublicKeyValidation(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	good := base64.StdEncoding.EncodeToString(pub)

	if _, err := LoadPublicKey(good, ""); err != nil {
		t.Errorf("valid built-in key rejected: %v", err)
	}
	for name, in := range map[string]string{
		"empty":      "",
		"not base64": "%%%",
		"wrong size": base64.StdEncoding.EncodeToString([]byte("too short")),
	} {
		if _, err := LoadPublicKey(in, ""); err == nil {
			t.Errorf("%s key was accepted", name)
		}
	}
}
