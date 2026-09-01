// Package config loads /etc/homeos/config.yaml, the file install.sh writes in
// phase 1. The struct mirrors that file key for key; if you add a field here,
// add it to the installer template too or operators will never see it.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultPath = "/etc/homeos/config.yaml"

type Config struct {
	Version int `yaml:"version"`

	System    System    `yaml:"system"`
	API       API       `yaml:"api"`
	Paths     Paths     `yaml:"paths"`
	Docker    Docker    `yaml:"docker"`
	Proxy     Proxy     `yaml:"proxy"`
	Storage   Storage   `yaml:"storage"`
	Samba     Samba     `yaml:"samba"`
	AppStore  AppStore  `yaml:"appstore"`
	Telemetry Telemetry `yaml:"telemetry"`
	Update    Update    `yaml:"update"`
}

type System struct {
	Hostname string `yaml:"hostname"`
	Domain   string `yaml:"domain"`
	Timezone string `yaml:"timezone"`
}

type API struct {
	Listen          string `yaml:"listen"`
	Port            int    `yaml:"port"`
	SessionTTLHours int    `yaml:"session_ttl_hours"`
}

func (a API) Addr() string { return fmt.Sprintf("%s:%d", a.Listen, a.Port) }

func (a API) SessionTTL() time.Duration {
	if a.SessionTTLHours <= 0 {
		return 168 * time.Hour
	}
	return time.Duration(a.SessionTTLHours) * time.Hour
}

type Paths struct {
	Config      string `yaml:"config"`
	Apps        string `yaml:"apps"`
	Data        string `yaml:"data"`
	Store       string `yaml:"store"`
	Database    string `yaml:"database"`
	Backups     string `yaml:"backups"`
	Logs        string `yaml:"logs"`
	StorageRoot string `yaml:"storage_root"`
	WebRoot     string `yaml:"web_root"`
}

type Docker struct {
	Socket               string `yaml:"socket"`
	EdgeNetwork          string `yaml:"edge_network"`
	AppSubnetPool        string `yaml:"app_subnet_pool"`
	AppSubnetSize        int    `yaml:"app_subnet_size"`
	ComposeProjectPrefix string `yaml:"compose_project_prefix"`
}

type Proxy struct {
	Engine             string `yaml:"engine"`
	AdminEndpoint      string `yaml:"admin_endpoint"`
	RoutesDir          string `yaml:"routes_dir"`
	DefaultRouteMode   string `yaml:"default_route_mode"`
	PublishMDNSAliases bool   `yaml:"publish_mdns_aliases"`
}

type Storage struct {
	MountRoot                string `yaml:"mount_root"`
	DefaultFilesystem        string `yaml:"default_filesystem"`
	SMARTPollIntervalMinutes int    `yaml:"smart_poll_interval_minutes"`
}

type Samba struct {
	Workgroup     string `yaml:"workgroup"`
	ManagedConfig string `yaml:"managed_config"`
	ShareGroup    string `yaml:"share_group"`
}

type AppStore struct {
	Repository           string `yaml:"repository"`
	Branch               string `yaml:"branch"`
	RefreshIntervalHours int    `yaml:"refresh_interval_hours"`
}

type Telemetry struct {
	SampleIntervalSeconds   int `yaml:"sample_interval_seconds"`
	HistoryRetentionMinutes int `yaml:"history_retention_minutes"`
}

type Update struct {
	ChannelURL string `yaml:"channel_url"`
	// AutoCheck only polls and downloads. Applying is a separate decision
	// because it restarts the appliance, and a NAS mid-transfer is a bad
	// moment to be surprised by that.
	AutoCheck          bool   `yaml:"auto_check"`
	CheckIntervalHours int    `yaml:"check_interval_hours"`
	AutoApply          bool   `yaml:"auto_apply"`
	ReleasesDir        string `yaml:"releases_dir"`
	PublicKeyFile      string `yaml:"public_key_file"`
	KeepReleases       int    `yaml:"keep_releases"`
}

func (u Update) CheckInterval() time.Duration {
	if u.CheckIntervalHours <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(u.CheckIntervalHours) * time.Hour
}

func (t Telemetry) SampleInterval() time.Duration {
	if t.SampleIntervalSeconds <= 0 {
		return 2 * time.Second
	}
	return time.Duration(t.SampleIntervalSeconds) * time.Second
}

// Load reads the config file and fills in defaults for anything absent. A
// missing file is not an error: the defaults describe exactly the layout
// install.sh creates, so the daemon still starts on a hand-built system.
func Load(path string) (*Config, error) {
	c := Default()

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func Default() *Config {
	c := &Config{Version: 1}
	c.applyDefaults()
	return c
}

// applyDefaults only fills zero values, so it is safe to call after unmarshal.
func (c *Config) applyDefaults() {
	set := func(dst *string, v string) {
		if strings.TrimSpace(*dst) == "" {
			*dst = v
		}
	}

	set(&c.System.Hostname, "homenas")
	set(&c.System.Domain, "local")
	set(&c.System.Timezone, "UTC")

	set(&c.API.Listen, "127.0.0.1")
	if c.API.Port == 0 {
		c.API.Port = 8790
	}
	if c.API.SessionTTLHours == 0 {
		c.API.SessionTTLHours = 168
	}

	set(&c.Paths.Config, "/etc/homeos")
	set(&c.Paths.Apps, "/var/lib/homeos/apps")
	set(&c.Paths.Data, "/var/lib/homeos/data")
	set(&c.Paths.Store, "/var/lib/homeos/store")
	set(&c.Paths.Database, "/var/lib/homeos/db/homeos.db")
	set(&c.Paths.Backups, "/var/lib/homeos/backups")
	set(&c.Paths.Logs, "/var/log/homeos")
	set(&c.Paths.StorageRoot, "/mnt/storage")
	set(&c.Paths.WebRoot, "/opt/homeos/web")

	set(&c.Docker.Socket, "/var/run/docker.sock")
	set(&c.Docker.EdgeNetwork, "homeos-edge")
	set(&c.Docker.AppSubnetPool, "10.21.0.0/16")
	set(&c.Docker.ComposeProjectPrefix, "homeos")
	if c.Docker.AppSubnetSize == 0 {
		c.Docker.AppSubnetSize = 24
	}

	set(&c.Proxy.Engine, "caddy")
	set(&c.Proxy.AdminEndpoint, "http://127.0.0.1:2019")
	set(&c.Proxy.RoutesDir, "/etc/homeos/proxy/routes.d")
	set(&c.Proxy.DefaultRouteMode, "host")

	set(&c.Storage.MountRoot, "/mnt/storage")
	set(&c.Storage.DefaultFilesystem, "ext4")
	if c.Storage.SMARTPollIntervalMinutes == 0 {
		c.Storage.SMARTPollIntervalMinutes = 30
	}

	set(&c.Samba.Workgroup, "WORKGROUP")
	set(&c.Samba.ManagedConfig, "/etc/homeos/samba/shares.conf")
	set(&c.Samba.ShareGroup, "homeos-share")

	set(&c.AppStore.Repository, "https://github.com/danyx67800/homeos-appstore.git")
	set(&c.AppStore.Branch, "main")
	if c.AppStore.RefreshIntervalHours == 0 {
		c.AppStore.RefreshIntervalHours = 12
	}

	// ChannelURL is deliberately not defaulted: an empty value is how updates
	// are turned off, and filling it in here would make that impossible.
	// install.sh writes the shipped default into config.yaml instead.
	set(&c.Update.ReleasesDir, "/usr/lib/homeos/releases")
	set(&c.Update.PublicKeyFile, "/etc/homeos/update.pub")
	if c.Update.CheckIntervalHours == 0 {
		c.Update.CheckIntervalHours = 24
	}
	if c.Update.KeepReleases == 0 {
		c.Update.KeepReleases = 3
	}

	if c.Telemetry.SampleIntervalSeconds == 0 {
		c.Telemetry.SampleIntervalSeconds = 2
	}
	if c.Telemetry.HistoryRetentionMinutes == 0 {
		c.Telemetry.HistoryRetentionMinutes = 60
	}
}

func (c *Config) Validate() error {
	if c.API.Port < 1 || c.API.Port > 65535 {
		return fmt.Errorf("api.port %d is out of range", c.API.Port)
	}
	switch c.Proxy.DefaultRouteMode {
	case "host", "path", "port":
	default:
		return fmt.Errorf("proxy.default_route_mode %q must be host, path or port",
			c.Proxy.DefaultRouteMode)
	}
	switch c.Storage.DefaultFilesystem {
	case "ext4", "btrfs", "xfs":
	default:
		return fmt.Errorf("storage.default_filesystem %q must be ext4, btrfs or xfs",
			c.Storage.DefaultFilesystem)
	}
	// filepath.IsAbs follows host rules, so "/var/lib/homeos" reads as relative
	// on a Windows developer machine. HomeOS paths are always POSIX.
	for _, p := range []string{c.Paths.Apps, c.Paths.Data, c.Paths.Database} {
		if !strings.HasPrefix(p, "/") {
			return fmt.Errorf("path %q must be absolute", p)
		}
	}
	return nil
}

// UpdatesDir holds the pending-version file and the result the privileged
// update helper writes back after this process has been restarted.
func (c *Config) UpdatesDir() string {
	return filepath.Join(filepath.Dir(c.Paths.Apps), "updates")
}

// SecretsDir holds the API token and per-app generated credentials.
func (c *Config) SecretsDir() string { return filepath.Join(c.Paths.Config, "secrets") }

// FQDN is the name the appliance answers to on the LAN.
func (c *Config) FQDN() string { return c.System.Hostname + "." + c.System.Domain }
