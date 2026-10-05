// Package config loads agent configuration from YAML, with Windows registry
// fallback for collector.url and collector.api_key (ADR-014), extended by
// B-072 to also cover collector.ca_cert_path.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level agent configuration.
type Config struct {
	Agent     AgentConfig     `yaml:"agent"`
	Collector CollectorConfig `yaml:"collector"`
	Detection DetectionConfig `yaml:"detection"`

	// FileLoaded reports whether a config file was actually read (B-293):
	// it is how a report tells "local" config apart from built-in
	// "defaults". Not a YAML key.
	FileLoaded bool `yaml:"-"`
}

// AllScanners is every scanner name payload.Build gates on. A config that
// doesn't list enabled_scanners at all gets this full list explicitly at
// load time (B-293, decision D-a): an explicit list is the only meaning
// there is, so an empty list means "no scanners", never "all". Keep in sync
// with eami-api's store.AllScanners; new names ship server-side first,
// agent second (CLAUDE.md release rule), or an agent-side addition runs
// nowhere until the server lists it.
var AllScanners = []string{
	"ai_apps", "models", "mcp_servers", "cloud_clients", "network_activity", "browser",
	"ai_processes", "gpu", "python_envs", "nodejs_ai",
}

// AgentConfig controls agent identity and scan cadence.
type AgentConfig struct {
	ID           string `yaml:"id"`
	IntervalSecs int    `yaml:"interval_secs"`
	LogLevel     string `yaml:"log_level"`
}

// CollectorConfig controls the upstream HTTP receiver.
type CollectorConfig struct {
	URL            string `yaml:"url"`
	APIKey         string `yaml:"api_key"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
	// CACertPath is the path to a PEM file containing the CA the collector's
	// TLS certificate was issued by (B-072). Only needed when that CA isn't
	// already in the OS trust store -- the appliance's default self-signed
	// cert (eami-proxy's Caddy, B-071's tls internal) is the case this
	// exists for. Empty means "use the OS default trust store," identical
	// to this field not existing at all -- a customer who installs a
	// real, publicly-trusted certificate on eami-proxy needs nothing here.
	CACertPath string `yaml:"ca_cert_path"`
}

// DetectionConfig controls scanner behaviour.
type DetectionConfig struct {
	ModelFileScanPaths []string `yaml:"model_file_scan_paths"`
	MinModelSizeMB     int64    `yaml:"model_file_size_mb"`
	// EnabledScanners is the exact set of scanner names that run. Leaving
	// enabled_scanners out of the YAML gives every scanner (AllScanners,
	// filled in by Load); an explicit empty list runs none (B-293). Remote
	// config replaces it per scan cycle without an agent restart.
	EnabledScanners []string `yaml:"enabled_scanners"`
}

// IsEnabled reports whether the named scanner is in EnabledScanners. Plain
// membership: an empty list enables nothing.
func (d *DetectionConfig) IsEnabled(name string) bool {
	for _, s := range d.EnabledScanners {
		if s == name {
			return true
		}
	}
	return false
}

// RegistryReader abstracts Windows registry access so tests can inject a mock
// and Linux/macOS CI can use a no-op without build tags in test files.
type RegistryReader interface {
	// ReadString reads a string value from the named registry key/value.
	// Returns ("", nil) when the key or value is absent — not an error.
	ReadString(key, value string) (string, error)
}

// NoopRegistryReader is an exported no-op RegistryReader for use in tests
// and on non-Windows platforms.
type NoopRegistryReader struct{}

func (NoopRegistryReader) ReadString(_, _ string) (string, error) { return "", nil }

func (c *Config) defaults() {
	if c.Agent.IntervalSecs == 0 {
		c.Agent.IntervalSecs = 300
	}
	if c.Agent.LogLevel == "" {
		c.Agent.LogLevel = "info"
	}
	if c.Collector.TimeoutSeconds == 0 {
		c.Collector.TimeoutSeconds = 30
	}
	if c.Detection.MinModelSizeMB == 0 {
		c.Detection.MinModelSizeMB = 100
	}
	// Absent (nil) only: an explicit `enabled_scanners: []` stays empty.
	if c.Detection.EnabledScanners == nil {
		c.Detection.EnabledScanners = append([]string(nil), AllScanners...)
	}
}

// Load reads the YAML file at path, applies defaults, then applies the
// platform-default registry fallback. It is a thin wrapper around LoadWithRegistry.
func Load(path string) (*Config, error) {
	return LoadWithRegistry(path, defaultRegistryReader())
}

// LoadWithRegistry reads the YAML config and fills empty collector fields
// from the provided RegistryReader (ADR-014).
func LoadWithRegistry(path string, reg RegistryReader) (*Config, error) {
	f, err := os.Open(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("config: open %s: %w", path, err)
	}

	cfg := &Config{}
	if err == nil {
		defer f.Close()
		dec := yaml.NewDecoder(f)
		dec.KnownFields(true)
		if decErr := dec.Decode(cfg); decErr != nil {
			return nil, fmt.Errorf("config: decode %s: %w", path, decErr)
		}
		cfg.FileLoaded = true
	}

	cfg.defaults()

	if err2 := applyRegistry(cfg, reg); err2 != nil {
		_, _ = fmt.Fprintf(os.Stderr, "config: registry fallback: %v\n", err2)
	}
	return cfg, nil
}

func applyRegistry(cfg *Config, reg RegistryReader) error {
	if cfg.Collector.URL == "" {
		val, err := reg.ReadString(`SOFTWARE\EAMI\Agent`, "CollectorURL")
		if err != nil {
			return fmt.Errorf("read CollectorURL: %w", err)
		}
		cfg.Collector.URL = val
	}
	if cfg.Collector.APIKey == "" {
		val, err := reg.ReadString(`SOFTWARE\EAMI\Agent`, "CollectorAPIKey")
		if err != nil {
			return fmt.Errorf("read CollectorAPIKey: %w", err)
		}
		cfg.Collector.APIKey = val
	}
	if cfg.Collector.CACertPath == "" {
		val, err := reg.ReadString(`SOFTWARE\EAMI\Agent`, "CollectorCACertPath")
		if err != nil {
			return fmt.Errorf("read CollectorCACertPath: %w", err)
		}
		cfg.Collector.CACertPath = val
	}
	return nil
}
