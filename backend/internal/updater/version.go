package updater

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a semantic version with an optional pre-release suffix.
//
// A small parser rather than a dependency: HomeOS needs exactly two operations
// — parse and compare — and the comparison rule that matters here (a
// pre-release sorts before its release) is four lines.
type Version struct {
	Major, Minor, Patch int
	Pre                 string // "rc1", "phase4"; empty for a final release
	Raw                 string
}

// ParseVersion accepts "1.2.3", "v1.2.3", "1.2.3-rc1" and "dev".
func ParseVersion(s string) (Version, error) {
	raw := strings.TrimSpace(s)
	v := Version{Raw: raw}
	if raw == "" {
		return v, fmt.Errorf("empty version")
	}

	// A development build sorts below everything, so an unreleased binary
	// always sees the channel as offering something newer.
	if raw == "dev" {
		v.Pre = "dev"
		return v, nil
	}

	body := strings.TrimPrefix(raw, "v")
	if i := strings.IndexAny(body, "-+"); i >= 0 {
		v.Pre = body[i+1:]
		body = body[:i]
	}

	parts := strings.Split(body, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return v, fmt.Errorf("want MAJOR.MINOR[.PATCH], got %q", s)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, fmt.Errorf("%q is not a version number", p)
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, nil
}

// Compare returns -1, 0 or 1.
func (v Version) Compare(o Version) int {
	// "dev" is below every real version, including below another dev build,
	// which is what makes an unreleased binary always updatable.
	if v.Pre == "dev" && o.Pre == "dev" {
		return 0
	}
	if v.Pre == "dev" {
		return -1
	}
	if o.Pre == "dev" {
		return 1
	}

	for _, pair := range [][2]int{
		{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch},
	} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}

	// Same numbers: a pre-release sorts before its final release, so
	// 1.2.0-rc1 < 1.2.0. Two different pre-releases fall back to string order,
	// which is right for rc1 < rc2 and arbitrary but stable otherwise.
	switch {
	case v.Pre == "" && o.Pre == "":
		return 0
	case v.Pre == "":
		return 1
	case o.Pre == "":
		return -1
	case v.Pre < o.Pre:
		return -1
	case v.Pre > o.Pre:
		return 1
	}
	return 0
}

func (v Version) LessThan(o Version) bool { return v.Compare(o) < 0 }
func (v Version) String() string          { return v.Raw }
