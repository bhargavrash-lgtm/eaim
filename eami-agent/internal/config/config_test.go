package config

import (
	"os"
	"path/filepath"
	"testing"
)

// mockRegistry implements RegistryReader for testing.
type mockRegistry struct {
	values map[string]string // "key/value" → result
}

func (m *mockRegistry) ReadString(key, value string) (string, error) {
	return m.values[key+"/"+value], nil
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := LoadWithRegistry("/nonexistent/path.yaml", NoopRegistryReader{})
	if err != nil {
		t.Fatalf("LoadWithRegistry: %v", err)
	}
	if cfg.Agent.IntervalSecs != 300 {
		t.Errorf("interval_secs default: got %d", cfg.Agent.IntervalSecs)
	}
	if cfg.Agent.LogLevel != "info" {
		t.Errorf("log_level default: got %q", cfg.Agent.LogLevel)
	}
	if cfg.Collector.TimeoutSeconds != 30 {
		t.Errorf("timeout_seconds default: got %d", cfg.Collector.TimeoutSeconds)
	}
	if cfg.Detection.MinModelSizeMB != 100 {
		t.Errorf("min_model_size_mb default: got %d", cfg.Detection.MinModelSizeMB)
	}
}

func TestLoad_YAMLOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "agent.yaml")
	content := `
agent:
  interval_secs: 60
  log_level: debug
collector:
  url: "http://my-collector:9000"
  timeout_seconds: 15
detection:
  model_file_size_mb: 50
`
	if err := os.WriteFile(cfgFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadWithRegistry(cfgFile, NoopRegistryReader{})
	if err != nil {
		t.Fatalf("LoadWithRegistry: %v", err)
	}
	if cfg.Agent.IntervalSecs != 60 {
		t.Errorf("interval_secs: got %d", cfg.Agent.IntervalSecs)
	}
	if cfg.Agent.LogLevel != "debug" {
		t.Errorf("log_level: got %q", cfg.Agent.LogLevel)
	}
	if cfg.Collector.URL != "http://my-collector:9000" {
		t.Errorf("url: got %q", cfg.Collector.URL)
	}
	if cfg.Collector.TimeoutSeconds != 15 {
		t.Errorf("timeout_seconds: got %d", cfg.Collector.TimeoutSeconds)
	}
	if cfg.Detection.MinModelSizeMB != 50 {
		t.Errorf("min_model_size_mb: got %d", cfg.Detection.MinModelSizeMB)
	}
}

func TestLoad_RegistryFallback_URLEmpty(t *testing.T) {
	reg := &mockRegistry{
		values: map[string]string{
			`SOFTWARE\EAMI\Agent/CollectorURL`:    "http://registry-collector:8888",
			`SOFTWARE\EAMI\Agent/CollectorAPIKey`: "reg-api-key",
		},
	}

	cfg, err := LoadWithRegistry("/nonexistent/path.yaml", reg)
	if err != nil {
		t.Fatalf("LoadWithRegistry: %v", err)
	}
	if cfg.Collector.URL != "http://registry-collector:8888" {
		t.Errorf("URL from registry: got %q", cfg.Collector.URL)
	}
	if cfg.Collector.APIKey != "reg-api-key" {
		t.Errorf("APIKey from registry: got %q", cfg.Collector.APIKey)
	}
}

func TestLoad_RegistryFallback_DoesNotOverrideYAML(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "agent.yaml")
	content := "collector:\n  url: \"http://yaml-collector:7777\"\n"
	if err := os.WriteFile(cfgFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := &mockRegistry{
		values: map[string]string{
			`SOFTWARE\EAMI\Agent/CollectorURL`: "http://registry-collector:8888",
		},
	}

	cfg, err := LoadWithRegistry(cfgFile, reg)
	if err != nil {
		t.Fatalf("LoadWithRegistry: %v", err)
	}
	// YAML value should win; registry fallback only applies when field is empty.
	if cfg.Collector.URL != "http://yaml-collector:7777" {
		t.Errorf("YAML should take precedence over registry; got %q", cfg.Collector.URL)
	}
}

func TestNoopRegistryReader(t *testing.T) {
	val, err := NoopRegistryReader{}.ReadString("any\\key", "anyValue")
	if err != nil {
		t.Errorf("NoopRegistryReader.ReadString error: %v", err)
	}
	if val != "" {
		t.Errorf("NoopRegistryReader.ReadString: want empty, got %q", val)
	}
}
