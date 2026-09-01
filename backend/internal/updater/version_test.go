package updater

import "testing"

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		in            string
		maj, min, pat int
		pre           string
	}{
		{"1.2.3", 1, 2, 3, ""},
		{"v1.2.3", 1, 2, 3, ""},
		{"0.1.0", 0, 1, 0, ""},
		{"1.2.3-rc1", 1, 2, 3, "rc1"},
		{"2.0.0-phase4", 2, 0, 0, "phase4"},
		{"10.20.30", 10, 20, 30, ""},
	} {
		v, err := ParseVersion(tc.in)
		if err != nil {
			t.Errorf("ParseVersion(%q) = %v", tc.in, err)
			continue
		}
		if v.Major != tc.maj || v.Minor != tc.min || v.Patch != tc.pat || v.Pre != tc.pre {
			t.Errorf("ParseVersion(%q) = %+v", tc.in, v)
		}
	}

	for _, bad := range []string{"", "1", "1.2.3.4", "x.y.z", "1.-2.3", "one.two.three"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) should have failed", bad)
		}
	}
}

func TestVersionOrdering(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.1", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.0", 0},
		{"1.9.0", "1.10.0", -1},
		{"1.0.0", "2.0.0", -1},
		// A pre-release sorts before its final release.
		{"1.2.0-rc1", "1.2.0", -1},
		{"1.2.0", "1.2.0-rc1", 1},
		{"1.2.0-rc1", "1.2.0-rc2", -1},
		// A development build is below everything, so an unreleased binary
		// always sees the channel as offering something newer.
		{"dev", "0.0.1", -1},
		{"dev", "dev", 0},
		{"1.0.0", "dev", 1},
	} {
		a, _ := ParseVersion(tc.a)
		b, _ := ParseVersion(tc.b)
		if got := a.Compare(b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
