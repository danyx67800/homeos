package appstore

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ResolveEnv turns the install form's answers into the final environment.
//
// Three rules, in order: an answer wins; a password marked generate gets a
// fresh random value when unanswered; otherwise the manifest default applies.
// A required variable with no value at the end is an error, caught here rather
// than as a confusing container crash loop ten seconds later.
func ResolveEnv(m *Manifest, answers map[string]string) (map[string]string, error) {
	out := make(map[string]string)

	all := append([]EnvVar{}, m.Env...)
	for _, sc := range m.Sidecars {
		all = append(all, sc.Env...)
	}

	for _, e := range all {
		v, answered := answers[e.Key]

		if !answered || v == "" {
			switch {
			case e.Generate:
				secret, err := GenerateSecret(24)
				if err != nil {
					return nil, fmt.Errorf("generate %s: %w", e.Key, err)
				}
				v = secret
			default:
				v = e.Default
			}
		}

		if v == "" && e.Required {
			return nil, fmt.Errorf("%s is required", label(e))
		}
		if err := validateEnvValue(e, v); err != nil {
			return nil, err
		}
		if v != "" {
			out[e.Key] = v
		}
	}
	return out, nil
}

func label(e EnvVar) string {
	if e.Label != "" {
		return e.Label
	}
	return e.Key
}

func validateEnvValue(e EnvVar, v string) error {
	if v == "" {
		return nil
	}
	switch e.Type {
	case "number":
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return fmt.Errorf("%s must be a number", label(e))
		}
	case "bool":
		if _, err := strconv.ParseBool(v); err != nil {
			return fmt.Errorf("%s must be true or false", label(e))
		}
	case "select":
		for _, o := range e.Options {
			if o == v {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of the offered options", label(e))
	}
	return nil
}

// GenerateSecret returns a URL-safe random string. Used for app passwords so
// the default is a strong unique value rather than a documented one that every
// installation shares.
func GenerateSecret(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// InstalledManifest records what was installed and with which values, so an
// update can diff against reality and an uninstall works even after the app has
// left the catalogue.
type InstalledManifest struct {
	Manifest    *Manifest         `json:"manifest"`
	Env         map[string]string `json:"env"`
	InstalledAt time.Time         `json:"installed_at"`
}

func (in *Installer) writeInstalledManifest(appDir string, m *Manifest, env map[string]string) error {
	rec := InstalledManifest{Manifest: m, Env: env, InstalledAt: time.Now()}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode installed manifest: %w", err)
	}
	// 0600: this file holds generated passwords.
	if err := os.WriteFile(filepath.Join(appDir, "installed.json"), raw, 0o600); err != nil {
		return fmt.Errorf("write installed manifest: %w", err)
	}
	return nil
}

// ReadInstalled loads the record written at install time.
func ReadInstalled(appDir string) (*InstalledManifest, error) {
	raw, err := os.ReadFile(filepath.Join(appDir, "installed.json"))
	if err != nil {
		return nil, err
	}
	var rec InstalledManifest
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("parse installed manifest: %w", err)
	}
	return &rec, nil
}
