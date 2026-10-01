// Package config loads and saves the user's settings, including the
// whitelist of paths and cleanup rules that must never be touched.
//
// Settings live in %APPDATA%\oow\config.json (roaming, so they follow the
// user); history and logs live in %LOCALAPPDATA%\oow. OOW_CONFIG_DIR and
// OOW_DATA_DIR override both locations.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// CurrentVersion is the config schema version written by this build.
const CurrentVersion = 1

// Config is the persisted user configuration.
type Config struct {
	Version   int       `json:"version"`
	Whitelist Whitelist `json:"whitelist"`
	UI        UI        `json:"ui"`
	Purge     Purge     `json:"purge"`
}

// Whitelist lists what must never be cleaned.
type Whitelist struct {
	// Paths are absolute paths; their contents are protected too.
	Paths []string `json:"paths"`
	// Rules are cleanup rule IDs or dotted prefixes ("browser.chrome").
	Rules []string `json:"rules"`
}

// UI holds display preferences.
type UI struct {
	// Color is "auto" (default), "always" or "never".
	Color string `json:"color,omitempty"`
}

// Purge holds settings for project artifact cleanup.
type Purge struct {
	// Paths are directories scanned for projects. Empty means defaults.
	Paths []string `json:"paths,omitempty"`
}

// Default returns the configuration used when no file exists.
func Default() *Config {
	return &Config{Version: CurrentVersion, UI: UI{Color: "auto"}}
}

// Dirs are the tool's own directories.
type Dirs struct {
	Config string // settings
	Data   string // history, logs, caches
}

// ConfigFile is the path of config.json.
func (d Dirs) ConfigFile() string { return filepath.Join(d.Config, "config.json") }

// LogDir is where log files are written.
func (d Dirs) LogDir() string { return filepath.Join(d.Data, "logs") }

// DefaultDirs resolves the tool's directories, honouring overrides.
func DefaultDirs() (Dirs, error) {
	var d Dirs
	if v := os.Getenv("OOW_CONFIG_DIR"); v != "" {
		d.Config = v
	} else {
		base, err := os.UserConfigDir() // %APPDATA%
		if err != nil {
			return d, err
		}
		d.Config = filepath.Join(base, buildinfo.AppID)
	}
	if v := os.Getenv("OOW_DATA_DIR"); v != "" {
		d.Data = v
	} else {
		base, err := os.UserCacheDir() // %LOCALAPPDATA%
		if err != nil {
			return d, err
		}
		d.Data = filepath.Join(base, buildinfo.AppID)
	}
	return d, nil
}

// Load reads the configuration file. A missing file yields the defaults; a
// malformed file is an error, never silently replaced, so the user's
// whitelist is not lost.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	c := Default()
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%v); fix or delete it", path, err)
	}
	if c.Version > CurrentVersion {
		return nil, fmt.Errorf("%s was written by a newer version (schema %d); update %s", path, c.Version, buildinfo.Name)
	}
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	return c, c.Validate()
}

// Validate checks values that could otherwise silently weaken protection.
func (c *Config) Validate() error {
	for _, p := range c.Whitelist.Paths {
		if _, err := safety.Normalize(p); err != nil {
			return fmt.Errorf("whitelist path %q: %v", p, err)
		}
	}
	switch c.UI.Color {
	case "", "auto", "always", "never":
	default:
		return fmt.Errorf("ui.color must be auto, always or never (got %q)", c.UI.Color)
	}
	return nil
}

// Save writes the configuration atomically.
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// IsPathEntry reports whether a whitelist argument names a path rather than a
// rule ID.
func IsPathEntry(entry string) bool {
	return strings.ContainsAny(entry, `\/:`)
}

// AddPath adds an absolute path to the whitelist. It returns false if the
// path (or a parent of it) is already protected.
func (c *Config) AddPath(p string) (bool, error) {
	n, err := safety.Normalize(p)
	if err != nil {
		return false, err
	}
	for _, existing := range c.Whitelist.Paths {
		if e, err := safety.Normalize(existing); err == nil && safety.IsWithin(n, e) {
			return false, nil
		}
	}
	c.Whitelist.Paths = append(c.Whitelist.Paths, n)
	sort.Strings(c.Whitelist.Paths)
	return true, nil
}

// AddRule adds a rule ID or prefix. It returns false if already present.
func (c *Config) AddRule(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, existing := range c.Whitelist.Rules {
		if existing == id {
			return false
		}
	}
	c.Whitelist.Rules = append(c.Whitelist.Rules, id)
	sort.Strings(c.Whitelist.Rules)
	return true
}

// Remove deletes a whitelist entry (path or rule). It returns false if the
// entry was not present.
func (c *Config) Remove(entry string) bool {
	if IsPathEntry(entry) {
		n, err := safety.Normalize(entry)
		if err != nil {
			return false
		}
		for i, existing := range c.Whitelist.Paths {
			if e, err := safety.Normalize(existing); err == nil && safety.Key(e) == safety.Key(n) {
				c.Whitelist.Paths = append(c.Whitelist.Paths[:i], c.Whitelist.Paths[i+1:]...)
				return true
			}
		}
		return false
	}
	id := strings.ToLower(strings.TrimSpace(entry))
	for i, existing := range c.Whitelist.Rules {
		if existing == id {
			c.Whitelist.Rules = append(c.Whitelist.Rules[:i], c.Whitelist.Rules[i+1:]...)
			return true
		}
	}
	return false
}
