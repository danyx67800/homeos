package samba

import (
	"strings"
	"testing"
)

var allowedRoots = []string{"/mnt/storage", "/var/lib/homeos/data"}

func TestRenderProducesUsableShare(t *testing.T) {
	out := string(Render([]Share{{
		Name:       "media",
		Path:       "/mnt/storage/media",
		Comment:    "Films and series",
		Browseable: true,
		RecycleBin: true,
		ValidUsers: []string{"marco", "anna"},
	}}, "homeos-share"))

	for _, want := range []string{
		"[media]",
		"path = /mnt/storage/media",
		"comment = Films and series",
		"browseable = yes",
		"read only = no",
		"guest ok = no",
		"valid users = anna marco", // sorted, so the file is stable
		"force group = homeos-share",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// A writable share must not hand out world-writable files, and a read-only one
// has no business carrying create masks at all.
func TestRenderOmitsWriteMasksOnReadOnlyShares(t *testing.T) {
	ro := string(Render([]Share{{
		Name: "archive", Path: "/mnt/storage/archive", ReadOnly: true,
		ValidUsers: []string{"marco"},
	}}, "homeos-share"))

	if strings.Contains(ro, "force group") || strings.Contains(ro, "create mask") {
		t.Errorf("read-only share carries write settings:\n%s", ro)
	}
	if !strings.Contains(ro, "read only = yes") {
		t.Errorf("read only flag missing:\n%s", ro)
	}
}

// Public means unauthenticated. It has to be explicit, never inferred from an
// empty user list, or somebody publishes their files by forgetting a field.
func TestPublicMustBeExplicit(t *testing.T) {
	private := Share{Name: "docs", Path: "/mnt/storage/docs"}
	if err := private.Validate(allowedRoots); err == nil {
		t.Error("a non-public share with no users should be rejected, not silently opened")
	}

	public := Share{Name: "docs", Path: "/mnt/storage/docs", Public: true}
	if err := public.Validate(allowedRoots); err != nil {
		t.Errorf("explicit public share rejected: %v", err)
	}
	out := string(Render([]Share{public}, "homeos-share"))
	if !strings.Contains(out, "guest ok = yes") {
		t.Errorf("public share not rendered as guest-accessible:\n%s", out)
	}
}

// A share name reaches smb.conf as a section header. A name containing a
// bracket or newline would let a caller inject arbitrary Samba directives.
func TestValidateRejectsInjectionAndEscape(t *testing.T) {
	for _, tc := range []struct {
		name  string
		share Share
	}{
		{"bracket in name", Share{Name: "a]" + "\n[global", Path: "/mnt/storage/x", Public: true}},
		{"newline in name", Share{Name: "a\nb", Path: "/mnt/storage/x", Public: true}},
		{"space in name", Share{Name: "my share", Path: "/mnt/storage/x", Public: true}},
		{"empty name", Share{Name: "", Path: "/mnt/storage/x", Public: true}},
		{"newline in comment", Share{Name: "ok", Path: "/mnt/storage/x", Public: true,
			Comment: "hi\n   guest ok = yes"}},
		{"path outside roots", Share{Name: "etc", Path: "/etc", Public: true}},
		{"traversal in path", Share{Name: "up", Path: "/mnt/storage/../../etc", Public: true}},
		{"relative path", Share{Name: "rel", Path: "storage/x", Public: true}},
		{"bad user name", Share{Name: "ok", Path: "/mnt/storage/x",
			ValidUsers: []string{"root; rm"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.share.Validate(allowedRoots); err == nil {
				t.Errorf("accepted a share that should be refused: %+v", tc.share)
			}
		})
	}
}

func TestValidateAcceptsBothRoots(t *testing.T) {
	for _, p := range []string{
		"/mnt/storage", "/mnt/storage/media", "/var/lib/homeos/data/photos",
	} {
		s := Share{Name: "ok", Path: p, Public: true}
		if err := s.Validate(allowedRoots); err != nil {
			t.Errorf("ValidateShare(%q) = %v", p, err)
		}
	}
}

// Two runs over the same set must produce identical bytes, so the file stays
// out of diffs and backups for no reason.
func TestRenderIsStable(t *testing.T) {
	a := Render([]Share{
		{Name: "zeta", Path: "/mnt/storage/z", Public: true},
		{Name: "alpha", Path: "/mnt/storage/a", Public: true},
	}, "homeos-share")
	b := Render([]Share{
		{Name: "alpha", Path: "/mnt/storage/a", Public: true},
		{Name: "zeta", Path: "/mnt/storage/z", Public: true},
	}, "homeos-share")

	if string(a) != string(b) {
		t.Error("share order changes the generated file")
	}
	if strings.Index(string(a), "[alpha]") > strings.Index(string(a), "[zeta]") {
		t.Error("shares are not sorted by name")
	}
}

// Time Machine needs the fruit VFS stack; without it macOS refuses the target.
func TestRenderTimeMachine(t *testing.T) {
	out := string(Render([]Share{{
		Name: "backup", Path: "/mnt/storage/backup", TimeMachine: true,
		ValidUsers: []string{"marco"},
	}}, "homeos-share"))

	for _, want := range []string{"fruit:time machine = yes", "vfs objects = catia fruit streams_xattr"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
