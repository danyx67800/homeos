package appstore

import (
	"bytes"
	"fmt"
	"path"
	"sort"

	"gopkg.in/yaml.v3"
)

// Compose types cover only what HomeOS emits. Marshalling a struct rather than
// templating a string means the output cannot be malformed by a hostile value
// in a manifest -- yaml.Marshal quotes and escapes for us.
type composeFile struct {
	Name     string                    `yaml:"name"`
	Services map[string]composeService `yaml:"services"`
	Networks map[string]composeNetwork `yaml:"networks"`
}

type composeService struct {
	Image         string            `yaml:"image"`
	ContainerName string            `yaml:"container_name"`
	Restart       string            `yaml:"restart"`
	Networks      []string          `yaml:"networks"`
	Environment   map[string]string `yaml:"environment,omitempty"`
	Volumes       []string          `yaml:"volumes,omitempty"`
	Devices       []string          `yaml:"devices,omitempty"`
	Command       []string          `yaml:"command,omitempty"`
	Labels        map[string]string `yaml:"labels,omitempty"`
	DependsOn     []string          `yaml:"depends_on,omitempty"`
	Healthcheck   *composeHealth    `yaml:"healthcheck,omitempty"`
	MemLimit      string            `yaml:"mem_limit,omitempty"`
	CPUs          string            `yaml:"cpus,omitempty"`
	SecurityOpt   []string          `yaml:"security_opt,omitempty"`
}

type composeHealth struct {
	Test     []string `yaml:"test"`
	Interval string   `yaml:"interval,omitempty"`
	Timeout  string   `yaml:"timeout,omitempty"`
	Retries  int      `yaml:"retries,omitempty"`
}

type composeNetwork struct {
	External bool   `yaml:"external,omitempty"`
	Driver   string `yaml:"driver,omitempty"`
	Internal bool   `yaml:"internal,omitempty"`
	Name     string `yaml:"name,omitempty"`
}

// RenderOptions carries the host-side facts a manifest deliberately does not
// know: where data lives, what the edge network is called, how routing defaults.
type RenderOptions struct {
	AppsDir       string            // /var/lib/homeos/apps
	EdgeNetwork   string            // homeos-edge
	ProjectPrefix string            // homeos
	DefaultRoute  string            // host | path | port
	Env           map[string]string // answers from the install form, already resolved
}

// Render turns a manifest plus install-time answers into a Compose file.
//
// The isolation model is enforced here rather than in the manifest: the app
// joins both the shared edge network (so the proxy can reach it) and a private
// per-app network, while sidecars join only the private one. A catalogue author
// cannot expose a database by writing the wrong thing, because they never get
// to write the network list at all.
func Render(m *Manifest, opt RenderOptions) ([]byte, error) {
	if opt.AppsDir == "" || opt.EdgeNetwork == "" {
		return nil, fmt.Errorf("render options need AppsDir and EdgeNetwork")
	}
	prefix := opt.ProjectPrefix
	if prefix == "" {
		prefix = "homeos"
	}
	route := m.Route
	if route == "" {
		route = opt.DefaultRoute
	}
	if route == "" {
		route = "host"
	}

	privateNet := fmt.Sprintf("%s-app-%s", prefix, m.ID)
	appDir := path.Join(opt.AppsDir, m.ID)
	containerName := fmt.Sprintf("%s-%s", prefix, m.ID)

	app := composeService{
		Image:         m.Image,
		ContainerName: containerName,
		Restart:       "unless-stopped",
		Networks:      []string{"edge", "private"},
		Environment:   pickEnv(m.Env, opt.Env),
		Volumes:       renderVolumes(m.Volumes, appDir),
		Devices:       m.Devices,
		Command:       m.Command,
		MemLimit:      m.Resources.Memory,
		CPUs:          m.Resources.CPUs,
		// Containers should not be able to gain privileges through setuid
		// binaries in their own image.
		SecurityOpt: []string{"no-new-privileges:true"},
		Labels:      routingLabels(m, route),
	}
	if m.Health != nil {
		app.Healthcheck = &composeHealth{
			Test:     m.Health.Test,
			Interval: m.Health.Interval,
			Timeout:  m.Health.Timeout,
			Retries:  m.Health.Retries,
		}
	}

	services := map[string]composeService{m.ID: app}

	names := make([]string, 0, len(m.Sidecars))
	for n := range m.Sidecars {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sc := m.Sidecars[n]
		services[n] = composeService{
			Image:         sc.Image,
			ContainerName: fmt.Sprintf("%s-%s-%s", prefix, m.ID, n),
			Restart:       "unless-stopped",
			// Private only: a sidecar is never routable.
			Networks:    []string{"private"},
			Environment: mergeEnv(pickEnv(sc.Env, opt.Env), referencedEnv(sc.UseEnv, opt.Env)),
			Volumes:     renderVolumes(sc.Volumes, appDir),
			Command:     sc.Command,
			MemLimit:    sc.Resources.Memory,
			CPUs:        sc.Resources.CPUs,
			SecurityOpt: []string{"no-new-privileges:true"},
			Labels: map[string]string{
				"homeos.managed": "true",
				"homeos.app":     m.ID,
				"homeos.role":    "sidecar",
			},
		}
		app.DependsOn = append(app.DependsOn, n)
	}
	if len(app.DependsOn) > 0 {
		sort.Strings(app.DependsOn)
		services[m.ID] = app
	}

	cf := composeFile{
		Name:     fmt.Sprintf("%s-%s", prefix, m.ID),
		Services: services,
		Networks: map[string]composeNetwork{
			"edge":    {External: true, Name: opt.EdgeNetwork},
			"private": {Driver: "bridge", Internal: true, Name: privateNet},
		},
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cf); err != nil {
		return nil, fmt.Errorf("render compose file: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}

	header := fmt.Sprintf(
		"# Generated by homeos-core from the %s manifest (v%s).\n"+
			"# Do not edit: reinstalling or updating the app overwrites this file.\n"+
			"# App data lives in %s and is never touched by an update.\n",
		m.ID, m.Version, appDir)
	return append([]byte(header), buf.Bytes()...), nil
}

// routingLabels are the contract phase 1's homeos-proxy-sync consumes. Changing
// a key here without changing the sync script silently unpublishes every app.
func routingLabels(m *Manifest, route string) map[string]string {
	l := map[string]string{
		"homeos.managed":  "true",
		"homeos.enable":   "true",
		"homeos.app":      m.ID,
		"homeos.title":    m.Name,
		"homeos.port":     fmt.Sprintf("%d", m.Port),
		"homeos.route":    route,
		"homeos.category": m.Category,
		"homeos.version":  m.Version,
	}
	if m.Icon != "" {
		l["homeos.icon"] = m.Icon
	}
	if m.Path != "" && m.Path != "/" {
		l["homeos.path"] = m.Path
	}
	return l
}

// renderVolumes maps named volumes into the app's own data directory, so all of
// an app's state is under one path that backup and uninstall can reason about.
func renderVolumes(vols []Volume, appDir string) []string {
	out := make([]string, 0, len(vols))
	for _, v := range vols {
		src := v.HostPath
		if src == "" {
			src = path.Join(appDir, "data", v.Name)
		}
		spec := src + ":" + v.Path
		if v.ReadOnly {
			spec += ":ro"
		}
		out = append(out, spec)
	}
	sort.Strings(out)
	return out
}

// pickEnv returns only the keys this service declares, so a sidecar's database
// password is not also handed to the app container.
func pickEnv(declared []EnvVar, resolved map[string]string) map[string]string {
	if len(declared) == 0 {
		return nil
	}
	out := make(map[string]string, len(declared))
	for _, e := range declared {
		if v, ok := resolved[e.Key]; ok {
			out[e.Key] = v
		} else if e.Default != "" {
			out[e.Key] = e.Default
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// referencedEnv resolves a sidecar's useEnv list against the install answers.
func referencedEnv(keys []string, resolved map[string]string) map[string]string {
	if len(keys) == 0 {
		return nil
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := resolved[k]; ok {
			out[k] = v
		}
	}
	return out
}

func mergeEnv(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
