// Package config loads agent configuration from YAML, with Windows registry
// fallback for collector.url and collector.api_key (ADR-014).
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
}

// DetectionConfig controls scanner behaviour.
type DetectionConfig struct {
	ModelFileScanPaths []string `yaml:"model_file_scan_paths"`
	MinModelSizeMB     int64    `yaml:"model_file_size_mb"`
	// EnabledScanners is the set of scanner names that should run.
	// An empty list means all scanners are enabled (default behaviour).
	// Updated at runtime by remote config pull without agent restart.
	EnabledScanners []string `yaml:"enabled_scanners"`
}

// IsEnabled reports whether the named scanner should run.
// An empty EnabledScanners list means every scanner is enabled.
func (d *DetectionConfig) IsEnabled(name string) bool {
	if len(d.EnabledScanners) == 0 {
		return true
	}
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
	return nil
}
