// Package appstore parses app manifests and turns them into runnable Compose
// stacks.
//
// The manifest is the contract between a catalogue author and HomeOS. It is
// deliberately declarative: it says what the app is and what it needs, never
// how to install it. Everything operational -- networks, labels, volume paths,
// generated secrets -- is derived here, so an app cannot opt out of the
// isolation model by writing its own Compose file.
package appstore

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ManifestVersion is the schema version this build understands. A manifest
// declaring a higher version is rejected rather than half-parsed.
const ManifestVersion = 1

type Manifest struct {
	ManifestVersion int    `yaml:"manifestVersion" json:"manifest_version"`
	ID              string `yaml:"id"              json:"id"`
	Name            string `yaml:"name"            json:"name"`
	Tagline         string `yaml:"tagline"         json:"tagline"`
	Description     string `yaml:"description"     json:"description"`
	Category        string `yaml:"category"        json:"category"`
	Version         string `yaml:"version"         json:"version"`
	Icon            string `yaml:"icon"            json:"icon"`
	Website         string `yaml:"website"         json:"website,omitempty"`
	Developer       string `yaml:"developer"       json:"developer,omitempty"`
	Source          string `yaml:"source"          json:"source,omitempty"`
	License         string `yaml:"license"         json:"license,omitempty"`

	// Architectures the images are published for. An app absent here is hidden
	// from the store on that machine rather than failing at pull time.
	Architectures []string `yaml:"architectures" json:"architectures"`

	// Deprecated marks an app that still runs but is no longer recommended.
	Deprecated bool   `yaml:"deprecated" json:"deprecated,omitempty"`
	Notice     string `yaml:"notice"     json:"notice,omitempty"`

	Image     string       `yaml:"image"     json:"image"`
	Port      int          `yaml:"port"      json:"port"`
	Route     string       `yaml:"route"     json:"route,omitempty"` // host | path | port
	Path      string       `yaml:"path"      json:"path,omitempty"`  // entry path within the app
	Command   []string     `yaml:"command"   json:"command,omitempty"`
	Env       []EnvVar     `yaml:"env"       json:"env,omitempty"`
	Volumes   []Volume     `yaml:"volumes"   json:"volumes,omitempty"`
	Devices   []string     `yaml:"devices"   json:"devices,omitempty"`
	Resources Resources    `yaml:"resources" json:"resources,omitempty"`
	Health    *Healthcheck `yaml:"healthcheck" json:"healthcheck,omitempty"`

	// Sidecars run beside the app on its private network only. They are never
	// reachable from the edge network, so a database cannot be proxied by
	// accident.
	Sidecars map[string]Sidecar `yaml:"sidecars" json:"sidecars,omitempty"`

	// Dependencies are other app IDs that must already be installed.
	Dependencies []string `yaml:"dependencies" json:"dependencies,omitempty"`
}

// EnvVar is a value the user is asked for at install time, or one generated for
// them. Type drives the form control the dashboard renders.
type EnvVar struct {
	Key         string   `yaml:"key"         json:"key"`
	Label       string   `yaml:"label"       json:"label"`
	Description string   `yaml:"description" json:"description,omitempty"`
	Type        string   `yaml:"type"        json:"type"` // string | password | number | bool | select
	Default     string   `yaml:"default"     json:"default,omitempty"`
	Options     []string `yaml:"options"     json:"options,omitempty"`
	Required    bool     `yaml:"required"    json:"required,omitempty"`
	// Generate fills the value with a random secret when the user leaves it
	// blank, so an app with a database password has a strong one by default
	// instead of a documented one everybody reuses.
	Generate bool `yaml:"generate" json:"generate,omitempty"`
	// Advanced hides the field behind a disclosure in the install form.
	Advanced bool `yaml:"advanced" json:"advanced,omitempty"`
}

type Volume struct {
	// Name creates persistent storage under the app's data directory.
	Name string `yaml:"name" json:"name,omitempty"`
	// HostPath binds an existing host directory instead. Restricted to the
	// storage root and the shared data directory; see Validate.
	HostPath string `yaml:"hostPath" json:"host_path,omitempty"`
	Path     string `yaml:"path"     json:"path"`
	ReadOnly bool   `yaml:"readOnly" json:"read_only,omitempty"`
}

type Resources struct {
	Memory string `yaml:"memory" json:"memory,omitempty"` // 512m, 2g
	CPUs   string `yaml:"cpus"   json:"cpus,omitempty"`   // "1.5"
}

type Healthcheck struct {
	Test     []string `yaml:"test"     json:"test"`
	Interval string   `yaml:"interval" json:"interval,omitempty"`
	Timeout  string   `yaml:"timeout"  json:"timeout,omitempty"`
	Retries  int      `yaml:"retries"  json:"retries,omitempty"`
}

type Sidecar struct {
	Image string `yaml:"image" json:"image"`
	// UseEnv names app-level env keys this sidecar also receives. Env keys are
	// unique across the whole stack (the install form asks for each once), so a
	// database password declared for the app cannot be re-declared here; it is
	// referenced instead.
	UseEnv    []string  `yaml:"useEnv"    json:"use_env,omitempty"`
	Env       []EnvVar  `yaml:"env"       json:"env,omitempty"`
	Volumes   []Volume  `yaml:"volumes"   json:"volumes,omitempty"`
	Command   []string  `yaml:"command"   json:"command,omitempty"`
	Resources Resources `yaml:"resources" json:"resources,omitempty"`
}

// Parse reads a manifest and validates it. A manifest that does not validate is
// never returned partially: a broken entry in the catalogue must not become a
// half-configured app.
func Parse(raw []byte) (*Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true) // typo in a key is an error, not a silent default
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}
