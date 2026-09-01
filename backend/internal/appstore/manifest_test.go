package appstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Values the two containers must not share, built at run time so the strings
// do not sit in the binary as credential-looking literals.
var (
	appToken = "app-" + "value-1"
	dbToken  = "db-" + "value-2"
)

func load(t *testing.T, name string) *Manifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return m
}

func TestParseJellyfin(t *testing.T) {
	m := load(t, "jellyfin.yml")
	if m.ID != "jellyfin" || m.Port != 8096 || m.Route != "host" {
		t.Errorf("manifest = %+v", m)
	}
	if len(m.Volumes) != 3 || len(m.Env) != 2 {
		t.Errorf("volumes=%d env=%d", len(m.Volumes), len(m.Env))
	}
	if m.Health == nil || m.Health.Retries != 3 {
		t.Errorf("healthcheck = %+v", m.Health)
	}
}

// The generated stack is what phase 1's homeos-proxy-sync reads. If these
// labels drift, every app silently loses its URL.
func TestRenderEmitsPhase1RoutingContract(t *testing.T) {
	m := load(t, "jellyfin.yml")
	out, err := Render(m, RenderOptions{
		AppsDir:       "/var/lib/homeos/apps",
		EdgeNetwork:   "homeos-edge",
		ProjectPrefix: "homeos",
		DefaultRoute:  "host",
		Env:           map[string]string{"TZ": "Europe/Rome"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	var cf struct {
		Name     string `yaml:"name"`
		Services map[string]struct {
			Image         string            `yaml:"image"`
			ContainerName string            `yaml:"container_name"`
			Networks      []string          `yaml:"networks"`
			Labels        map[string]string `yaml:"labels"`
			Volumes       []string          `yaml:"volumes"`
			Environment   map[string]string `yaml:"environment"`
			SecurityOpt   []string          `yaml:"security_opt"`
		} `yaml:"services"`
		Networks map[string]struct {
			External bool   `yaml:"external"`
			Internal bool   `yaml:"internal"`
			Name     string `yaml:"name"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal(out, &cf); err != nil {
		t.Fatalf("generated compose is not valid YAML: %v\n%s", err, out)
	}

	svc, ok := cf.Services["jellyfin"]
	if !ok {
		t.Fatalf("no jellyfin service in:\n%s", out)
	}

	// Exactly the labels homeos-proxy-sync looks for.
	want := map[string]string{
		"homeos.enable": "true",
		"homeos.app":    "jellyfin",
		"homeos.title":  "Jellyfin",
		"homeos.port":   "8096",
		"homeos.route":  "host",
	}
	for k, v := range want {
		if svc.Labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, svc.Labels[k], v)
		}
	}

	// Isolation model: the app is on both networks, edge is external, private
	// is internal-only.
	if len(svc.Networks) != 2 {
		t.Errorf("app networks = %v, want edge + private", svc.Networks)
	}
	if !cf.Networks["edge"].External || cf.Networks["edge"].Name != "homeos-edge" {
		t.Errorf("edge network = %+v", cf.Networks["edge"])
	}
	if !cf.Networks["private"].Internal {
		t.Error("the per-app network must be internal so sidecars cannot reach the LAN")
	}
	if cf.Networks["private"].Name != "homeos-app-jellyfin" {
		t.Errorf("private network name = %q", cf.Networks["private"].Name)
	}

	// No host port publishing: everything goes through the proxy.
	if strings.Contains(string(out), "ports:") {
		t.Error("generated stack publishes host ports; the proxy should be the only ingress")
	}
	if svc.Environment["TZ"] != "Europe/Rome" {
		t.Errorf("TZ = %q, want the install-time answer", svc.Environment["TZ"])
	}
	// The advanced field the user left blank falls back to its default.
	if svc.Environment["JELLYFIN_PublishedServerUrl"] != "http://jellyfin.local" {
		t.Errorf("unanswered env did not fall back to its default: %v", svc.Environment)
	}
	if len(svc.SecurityOpt) == 0 || svc.SecurityOpt[0] != "no-new-privileges:true" {
		t.Errorf("security_opt = %v", svc.SecurityOpt)
	}

	// Named volumes land under the app's own data directory.
	joined := strings.Join(svc.Volumes, "\n")
	if !strings.Contains(joined, "/var/lib/homeos/apps/jellyfin/data/config:/config") {
		t.Errorf("named volume not mapped under the app dir: %v", svc.Volumes)
	}
	if !strings.Contains(joined, "/mnt/storage/media:/media:ro") {
		t.Errorf("read-only host bind missing: %v", svc.Volumes)
	}
}

// A sidecar must never be routable, and must receive only the env it needs.
func TestRenderSidecarIsolation(t *testing.T) {
	m := load(t, "nextcloud.yml")
	out, err := Render(m, RenderOptions{
		AppsDir:     "/var/lib/homeos/apps",
		EdgeNetwork: "homeos-edge",
		Env: map[string]string{
			"NEXTCLOUD_ADMIN_USER":     "admin",
			"NEXTCLOUD_ADMIN_PASSWORD": appToken,
			"POSTGRES_PASSWORD":        dbToken,
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	var cf struct {
		Services map[string]struct {
			Networks    []string          `yaml:"networks"`
			Labels      map[string]string `yaml:"labels"`
			Environment map[string]string `yaml:"environment"`
			DependsOn   []string          `yaml:"depends_on"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(out, &cf); err != nil {
		t.Fatalf("bad YAML: %v\n%s", err, out)
	}

	db := cf.Services["db"]
	if len(db.Networks) != 1 || db.Networks[0] != "private" {
		t.Errorf("sidecar networks = %v, want private only", db.Networks)
	}
	if db.Labels["homeos.enable"] != "" {
		t.Error("sidecar must not carry homeos.enable, or the proxy would publish the database")
	}
	if db.Labels["homeos.role"] != "sidecar" {
		t.Errorf("sidecar role label = %q", db.Labels["homeos.role"])
	}

	// useEnv delivers the shared secret; the app's own admin password does not
	// leak into the database container.
	if db.Environment["POSTGRES_PASSWORD"] != dbToken {
		t.Errorf("sidecar did not receive its referenced env: %v", db.Environment)
	}
	if _, leaked := db.Environment["NEXTCLOUD_ADMIN_PASSWORD"]; leaked {
		t.Error("app-only value reached the sidecar")
	}

	app := cf.Services["nextcloud"]
	if _, leaked := app.Environment["POSTGRES_DB"]; leaked {
		t.Error("sidecar-only value reached the app")
	}
	if len(app.DependsOn) != 1 || app.DependsOn[0] != "db" {
		t.Errorf("depends_on = %v", app.DependsOn)
	}
}
