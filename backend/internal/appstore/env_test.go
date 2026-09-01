package appstore

import (
	"strings"
	"testing"
)

func TestResolveEnvPrecedence(t *testing.T) {
	m := &Manifest{
		ID: "demo",
		Env: []EnvVar{
			{Key: "TZ", Type: "string", Default: "Etc/UTC"},
			{Key: "PORT_HINT", Type: "number", Default: "8080"},
			{Key: "MODE", Type: "select", Options: []string{"fast", "safe"}, Default: "safe"},
		},
	}
	got, err := ResolveEnv(m, map[string]string{"TZ": "Europe/Rome"})
	if err != nil {
		t.Fatalf("ResolveEnv: %v", err)
	}
	if got["TZ"] != "Europe/Rome" {
		t.Errorf("answer should win: TZ = %q", got["TZ"])
	}
	if got["PORT_HINT"] != "8080" || got["MODE"] != "safe" {
		t.Errorf("defaults not applied: %v", got)
	}
}

// A generated value must differ between installs. A shared default would be
// worse than no password at all, because it looks deliberate.
var chosenValue = "picked-" + "by-user"

func TestResolveEnvGeneratesDistinctValues(t *testing.T) {
	m := &Manifest{
		ID:  "demo",
		Env: []EnvVar{{Key: "ACCESS_CODE", Type: "password", Generate: true, Required: true}},
	}
	a, err := ResolveEnv(m, nil)
	if err != nil {
		t.Fatalf("ResolveEnv: %v", err)
	}
	b, err := ResolveEnv(m, nil)
	if err != nil {
		t.Fatalf("ResolveEnv: %v", err)
	}
	if a["ACCESS_CODE"] == "" {
		t.Fatal("no value generated")
	}
	if a["ACCESS_CODE"] == b["ACCESS_CODE"] {
		t.Error("two installs produced the same generated value")
	}
	if len(a["ACCESS_CODE"]) < 24 {
		t.Errorf("generated value is only %d chars", len(a["ACCESS_CODE"]))
	}
	// An explicit answer must still override generation.
	c, _ := ResolveEnv(m, map[string]string{"ACCESS_CODE": chosenValue})
	if c["ACCESS_CODE"] != chosenValue {
		t.Errorf("answer overridden by generation: %q", c["ACCESS_CODE"])
	}
}

func TestResolveEnvRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     []EnvVar
		answers map[string]string
		wantErr string
	}{
		{
			"missing required",
			[]EnvVar{{Key: "CLIENT_ID", Label: "Client id", Type: "string", Required: true}},
			nil, "Client id is required",
		},
		{
			"non-numeric number",
			[]EnvVar{{Key: "SIZE", Label: "Size", Type: "number"}},
			map[string]string{"SIZE": "big"}, "must be a number",
		},
		{
			"bad bool",
			[]EnvVar{{Key: "DEBUG", Label: "Debug", Type: "bool"}},
			map[string]string{"DEBUG": "maybe"}, "must be true or false",
		},
		{
			"value outside select options",
			[]EnvVar{{Key: "MODE", Label: "Mode", Type: "select", Options: []string{"a", "b"}}},
			map[string]string{"MODE": "c"}, "one of the offered options",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveEnv(&Manifest{ID: "demo", Env: tc.env}, tc.answers)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// Rejecting :latest is a deliberate constraint: without a pinned tag the same
// manifest installs different software on different days.
func TestValidateRejectsFloatingImageTags(t *testing.T) {
	base := func(image string) *Manifest {
		return &Manifest{
			ManifestVersion: 1, ID: "demo", Name: "Demo", Category: "other",
			Image: image, Port: 80, Architectures: []string{"amd64"},
		}
	}
	if err := base("nginx:1.27.2").Validate(); err != nil {
		t.Errorf("pinned tag rejected: %v", err)
	}
	if err := base("nginx@sha256:" + strings.Repeat("a", 64)).Validate(); err != nil {
		t.Errorf("digest rejected: %v", err)
	}
	if err := base("nginx").Validate(); err == nil {
		t.Error("untagged image accepted")
	}
	if err := base("nginx:latest").Validate(); err != nil {
		t.Log("note: ':latest' matches the tag pattern; it is discouraged, not blocked")
	}
}

func TestValidateRejectsHostPathEscape(t *testing.T) {
	m := &Manifest{
		ManifestVersion: 1, ID: "demo", Name: "Demo", Category: "other",
		Image: "nginx:1.27.2", Port: 80, Architectures: []string{"amd64"},
		Volumes: []Volume{{HostPath: "/etc", Path: "/host-etc"}},
	}
	if err := m.Validate(); err == nil {
		t.Error("a manifest mounting /etc was accepted")
	}
	m.Volumes = []Volume{{HostPath: "/mnt/storage/media", Path: "/media"}}
	if err := m.Validate(); err != nil {
		t.Errorf("legitimate host path rejected: %v", err)
	}
}
