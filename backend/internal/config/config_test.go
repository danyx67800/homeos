package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The installer template and this struct must not drift apart. This test loads
// a capture of exactly what install.sh renders. Regenerate it after changing
// fs::write_config in scripts/lib/filesystem.sh:
//
//	sudo ./install.sh --dry-run ... && cp /etc/homeos/config.yaml \
//	    backend/internal/config/testdata/installer-config.yaml
//
// the exact file install.sh renders and asserts on values only that file
// supplies, so a renamed key here fails loudly instead of silently defaulting.
func TestLoadInstallerConfig(t *testing.T) {
	path := filepath.Join("testdata", "installer-config.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("installer fixture not present")
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"hostname", c.System.Hostname, "homenas"},
		{"timezone", c.System.Timezone, "Europe/Rome"},
		{"api.listen", c.API.Listen, "127.0.0.1"},
		{"api.port", c.API.Port, 8790},
		{"paths.apps", c.Paths.Apps, "/var/lib/homeos/apps"},
		{"paths.database", c.Paths.Database, "/var/lib/homeos/db/homeos.db"},
		{"docker.edge_network", c.Docker.EdgeNetwork, "homeos-edge"},
		{"docker.app_subnet_size", c.Docker.AppSubnetSize, 24},
		{"proxy.routes_dir", c.Proxy.RoutesDir, "/etc/homeos/proxy/routes.d"},
		{"proxy.default_route_mode", c.Proxy.DefaultRouteMode, "host"},
		{"proxy.publish_mdns_aliases", c.Proxy.PublishMDNSAliases, true},
		{"storage.mount_root", c.Storage.MountRoot, "/mnt/storage"},
		{"samba.managed_config", c.Samba.ManagedConfig, "/etc/homeos/samba/shares.conf"},
		{"samba.share_group", c.Samba.ShareGroup, "homeos-share"},
		{"appstore.branch", c.AppStore.Branch, "main"},
		{"telemetry.sample_interval", c.Telemetry.SampleIntervalSeconds, 2},
		{"update.channel_url", c.Update.ChannelURL,
			"https://github.com/danyx67800/homeos/releases/latest/download/stable.json"},
		{"update.auto_check", c.Update.AutoCheck, true},
		// Unattended reboots stay opt-in.
		{"update.auto_apply", c.Update.AutoApply, false},
		{"update.keep_releases", c.Update.KeepReleases, 3},
	}
	for _, ch := range checks {
		if ch.got != ch.want {
			t.Errorf("%s = %v, want %v", ch.name, ch.got, ch.want)
		}
	}
	if c.FQDN() != "homenas.local" {
		t.Errorf("FQDN = %q", c.FQDN())
	}
}

func TestMissingFileYieldsDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load of missing file should succeed: %v", err)
	}
	if c.API.Addr() != "127.0.0.1:8790" {
		t.Errorf("Addr = %q", c.API.Addr())
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"bad route mode", func(c *Config) { c.Proxy.DefaultRouteMode = "sideways" }},
		{"bad filesystem", func(c *Config) { c.Storage.DefaultFilesystem = "ntfs" }},
		{"bad port", func(c *Config) { c.API.Port = 70000 }},
		{"relative path", func(c *Config) { c.Paths.Apps = "relative/apps" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mutate(c)
			if err := c.Validate(); err == nil {
				t.Error("expected validation error, got nil")
			}
		})
	}
}

// The containerised stack ships its own config. It is parsed by the same code
// as an appliance's, so a key renamed on one side and not the other shows up
// here rather than as a container that starts with silent defaults.
func TestLoadDockerConfig(t *testing.T) {
	c, err := Load(filepath.Join("testdata", "docker-config.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.API.Listen != "0.0.0.0" {
		t.Errorf("listen = %q; the container needs a routable bind", c.API.Listen)
	}
	if c.Proxy.DefaultRouteMode != "port" {
		t.Errorf("route mode = %q; mDNS host routing is meaningless in a container",
			c.Proxy.DefaultRouteMode)
	}
	if c.Proxy.PublishMDNSAliases {
		t.Error("mDNS alias publishing should be off in a container")
	}
	if c.Update.ChannelURL != "" || c.Update.AutoCheck {
		t.Error("over-the-air updates should be off in a container: there is no " +
			"symlink to swap and no systemd unit to restart")
	}
}
