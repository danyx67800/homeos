package appstore

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

var (
	appIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)
	// Mixed case is allowed on purpose: real images use keys such as
	// JELLYFIN_PublishedServerUrl, and Docker itself only requires a name that
	// is non-empty and contains no "=".
	envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	// A tag or digest is required. ":latest" is not reproducible: the same
	// manifest would install different software on different days, and an
	// update would be indistinguishable from a rollback.
	imageRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*(:[a-zA-Z0-9._-]+|@sha256:[a-f0-9]{64})$`)
)

// Categories the dashboard groups the store by.
var Categories = map[string]bool{
	"media": true, "productivity": true, "networking": true, "developer": true,
	"automation": true, "storage": true, "security": true, "games": true,
	"monitoring": true, "communication": true, "other": true,
}

// AllowedHostPathRoots limits where a manifest may bind-mount from. Without
// this, any catalogue entry could mount / and read the whole machine.
var AllowedHostPathRoots = []string{"/mnt/storage", "/var/lib/homeos/data"}

func (m *Manifest) Validate() error {
	if m.ManifestVersion == 0 {
		return fmt.Errorf("manifestVersion is required")
	}
	if m.ManifestVersion > ManifestVersion {
		return fmt.Errorf("manifestVersion %d is newer than this build understands (%d)",
			m.ManifestVersion, ManifestVersion)
	}
	if !appIDRE.MatchString(m.ID) {
		return fmt.Errorf("id %q must be 2-32 chars of lowercase letters, digits or dashes", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if !Categories[m.Category] {
		return fmt.Errorf("category %q is not a known category", m.Category)
	}
	if !imageRE.MatchString(m.Image) {
		return fmt.Errorf("image %q must include an explicit tag or digest", m.Image)
	}
	if m.Port < 1 || m.Port > 65535 {
		return fmt.Errorf("port %d is out of range", m.Port)
	}
	switch m.Route {
	case "", "host", "path", "port":
	default:
		return fmt.Errorf("route %q must be host, path or port", m.Route)
	}
	if len(m.Architectures) == 0 {
		return fmt.Errorf("architectures must list at least one of amd64, arm64")
	}
	for _, a := range m.Architectures {
		if a != "amd64" && a != "arm64" {
			return fmt.Errorf("unsupported architecture %q", a)
		}
	}

	// Env keys share one namespace across the app and its sidecars: the install
	// form asks for each key once, so the same key in two services would be
	// ambiguous to the person filling it in.
	seenEnv := map[string]bool{}
	for _, e := range append(append([]EnvVar{}, m.Env...), sidecarEnv(m)...) {
		if !envKeyRE.MatchString(e.Key) {
			return fmt.Errorf("env key %q must start with a letter or underscore and contain only letters, digits and underscores", e.Key)
		}
		if seenEnv[e.Key] {
			return fmt.Errorf("duplicate env key %q", e.Key)
		}
		seenEnv[e.Key] = true

		switch e.Type {
		case "", "string", "password", "number", "bool", "select":
		default:
			return fmt.Errorf("env %s: unknown type %q", e.Key, e.Type)
		}
		if e.Type == "select" && len(e.Options) == 0 {
			return fmt.Errorf("env %s: type select needs options", e.Key)
		}
		if e.Generate && e.Type != "password" {
			return fmt.Errorf("env %s: generate is only meaningful for a password", e.Key)
		}
	}

	// Mount paths are per-container, so the app and a sidecar may both mount
	// /data. Checking them in one namespace would reject valid manifests.
	if err := validateVolumeSet(m.Volumes, m.ID); err != nil {
		return err
	}
	for name, sc := range m.Sidecars {
		if err := validateVolumeSet(sc.Volumes, name); err != nil {
			return err
		}
	}

	for _, d := range m.Dependencies {
		if !appIDRE.MatchString(d) {
			return fmt.Errorf("dependency %q is not a valid app id", d)
		}
		if d == m.ID {
			return fmt.Errorf("app %q depends on itself", m.ID)
		}
	}

	for name, sc := range m.Sidecars {
		if !appIDRE.MatchString(name) {
			return fmt.Errorf("sidecar name %q must be lowercase letters, digits or dashes", name)
		}
		if name == m.ID {
			return fmt.Errorf("sidecar %q collides with the app's own service name", name)
		}
		if !imageRE.MatchString(sc.Image) {
			return fmt.Errorf("sidecar %s: image %q must include an explicit tag or digest",
				name, sc.Image)
		}
		for _, k := range sc.UseEnv {
			if !declaresEnvKey(m.Env, k) {
				return fmt.Errorf("sidecar %s: useEnv references %q, which the app does not declare",
					name, k)
			}
		}
	}

	// Device passthrough (a GPU for transcoding, a Zigbee stick) is legitimate
	// but hands the container real hardware, so the paths are constrained.
	for _, d := range m.Devices {
		if !strings.HasPrefix(d, "/dev/") || path.Clean(d) != d || strings.Contains(d, "..") {
			return fmt.Errorf("device %q must be a normalised path under /dev", d)
		}
	}
	return nil
}

func (v Volume) validate() error {
	if v.Path == "" || !strings.HasPrefix(v.Path, "/") || path.Clean(v.Path) != v.Path {
		return fmt.Errorf("volume mount path %q must be absolute and normalised", v.Path)
	}
	if (v.Name == "") == (v.HostPath == "") {
		return fmt.Errorf("volume at %q needs exactly one of name or hostPath", v.Path)
	}
	if v.Name != "" && !appIDRE.MatchString(v.Name) {
		return fmt.Errorf("volume name %q must be lowercase letters, digits or dashes", v.Name)
	}
	if v.HostPath != "" {
		if path.Clean(v.HostPath) != v.HostPath {
			return fmt.Errorf("hostPath %q is not normalised", v.HostPath)
		}
		ok := false
		for _, root := range AllowedHostPathRoots {
			if v.HostPath == root || strings.HasPrefix(v.HostPath, root+"/") {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("hostPath %q must be under %s",
				v.HostPath, strings.Join(AllowedHostPathRoots, " or "))
		}
	}
	return nil
}

func sidecarEnv(m *Manifest) []EnvVar {
	var out []EnvVar
	for _, sc := range m.Sidecars {
		out = append(out, sc.Env...)
	}
	return out
}

func validateVolumeSet(vols []Volume, owner string) error {
	seen := map[string]bool{}
	for _, v := range vols {
		if err := v.validate(); err != nil {
			return fmt.Errorf("%s: %w", owner, err)
		}
		if seen[v.Path] {
			return fmt.Errorf("%s: duplicate container mount path %q", owner, v.Path)
		}
		seen[v.Path] = true
	}
	return nil
}

func declaresEnvKey(env []EnvVar, key string) bool {
	for _, e := range env {
		if e.Key == key {
			return true
		}
	}
	return false
}
