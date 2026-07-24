package config

import (
	"os"
	"path/filepath"
	"testing"
)

func validConfig() *Config {
	return &Config{
		PostgresDSN: "postgres://eami_app:S3cur3Pass@postgres:5432/eami?sslmode=disable",
		Policy: PolicyConfig{
			RulesPath: "/etc/eami-gateway/rules.yaml",
		},
		API: APIConfig{
			ServiceKey:            "a-real-generated-service-key",
			EpisodeReadServiceKey: "a-real-generated-episode-key",
		},
	}
}

func TestValidate_RejectsPlaceholderAPIServiceKey(t *testing.T) {
	cases := []string{"", "changeme", "CHANGEME", " changeme ", "devpassword"}
	for _, v := range cases {
		cfg := validConfig()
		cfg.API.ServiceKey = v
		if err := validate(cfg); err == nil {
			t.Errorf("validate() with API.ServiceKey=%q: expected error, got nil", v)
		}
	}
}

func TestValidate_AcceptsRealAPIServiceKey(t *testing.T) {
	cfg := validConfig()
	if err := validate(cfg); err != nil {
		t.Errorf("validate() with a real-looking API.ServiceKey: unexpected error: %v", err)
	}
}

func TestValidate_RejectsEmptyEpisodeReadServiceKey(t *testing.T) {
	cfg := validConfig()
	cfg.API.EpisodeReadServiceKey = ""
	if err := validate(cfg); err == nil {
		t.Error("validate() with empty API.EpisodeReadServiceKey: expected error, got nil")
	}
}

func TestValidate_RejectsEmptyPostgresDSN(t *testing.T) {
	cfg := validConfig()
	cfg.PostgresDSN = ""
	if err := validate(cfg); err == nil {
		t.Error("validate() with empty PostgresDSN: expected error, got nil")
	}
}

func TestValidate_RejectsPlaceholderPostgresDSNPassword(t *testing.T) {
	cases := []string{
		"postgres://eami_app:changeme@postgres:5432/eami?sslmode=disable",
		"postgres://eami_app:devpassword@postgres:5432/eami?sslmode=disable",
		"postgres://eami_app:@postgres:5432/eami?sslmode=disable", // empty password
		"postgres://eami_app:CHANGEME@postgres:5432/eami?sslmode=disable",
		"postgres://eami_app: changeme@postgres:5432/eami?sslmode=disable",   // leading space
		"postgres://eami_app:changeme\r@postgres:5432/eami?sslmode=disable", // CRLF-corrupted .env value
		"not-a-valid-dsn-at-all",                                            // unparseable -> treated as unconfigured
	}
	for _, dsn := range cases {
		cfg := validConfig()
		cfg.PostgresDSN = dsn
		if err := validate(cfg); err == nil {
			t.Errorf("validate() with PostgresDSN=%q: expected error, got nil", dsn)
		}
	}
}

func TestValidate_AcceptsRealPostgresDSNPassword(t *testing.T) {
	cases := []string{
		"postgres://eami_app:S3cur3Pass@postgres:5432/eami?sslmode=disable",
		"postgres://eami_app:aGVsbG93b3JsZA==@postgres:5432/eami?sslmode=disable",
	}
	for _, dsn := range cases {
		cfg := validConfig()
		cfg.PostgresDSN = dsn
		if err := validate(cfg); err != nil {
			t.Errorf("validate() with PostgresDSN=%q: unexpected error: %v", dsn, err)
		}
	}
}

func TestValidate_AppliesDefaults(t *testing.T) {
	cfg := validConfig()
	if err := validate(cfg); err != nil {
		t.Fatalf("validate() unexpected error: %v", err)
	}
	if cfg.ListenAddr != "0.0.0.0:8443" {
		t.Errorf("ListenAddr default not applied, got %q", cfg.ListenAddr)
	}
	if cfg.Token.DefaultTTLSeconds != 900 {
		t.Errorf("Token.DefaultTTLSeconds default not applied, got %d", cfg.Token.DefaultTTLSeconds)
	}
}

// clearSecretEnv unsets every env var Load() reads, so each test starts from
// a clean slate regardless of what's set in the host shell.
func clearSecretEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"GATEWAY_LISTEN_ADDR", "GATEWAY_LISTEN_API_PORT", "GATEWAY_DB_HOST", "GATEWAY_DB_NAME",
		"GATEWAY_DB_USER", "GATEWAY_DB_PASSWORD", "GATEWAY_JWT_KEY_PATH",
		"GATEWAY_APPROVAL_SLACK_WEBHOOK", "GATEWAY_UI_BASE_URL", "GATEWAY_API_BASE_URL",
		"GATEWAY_API_SERVICE_KEY", "GATEWAY_EPISODE_READ_SERVICE_KEY",
	} {
		os.Unsetenv(k)
	}
}

// nonexistentConfigPath returns a path Load() will treat as "no config file"
// (os.IsNotExist), so these tests exercise the env-var-only startup path --
// the real attack surface, since docker-compose configures every service via
// env vars, not by editing the shipped YAML.
func nonexistentConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "does-not-exist.yaml")
}

// TestLoad_RejectsUnsetAPIServiceKey is the integration-level regression
// test for this task's gateway-side fix: validate() previously never checked
// API.ServiceKey (GATEWAY_API_SERVICE_KEY) at all. This fails if that
// wiring, not just validate()'s internal logic, regresses.
func TestLoad_RejectsUnsetAPIServiceKey(t *testing.T) {
	clearSecretEnv(t)
	t.Setenv("GATEWAY_DB_HOST", "postgres")
	t.Setenv("GATEWAY_DB_PASSWORD", "S3cur3Pass")
	t.Setenv("GATEWAY_EPISODE_READ_SERVICE_KEY", "a-real-generated-episode-key")
	// GATEWAY_API_SERVICE_KEY deliberately left unset.

	if _, err := Load(nonexistentConfigPath(t)); err == nil {
		t.Fatal("Load() with GATEWAY_API_SERVICE_KEY unset: expected error, got nil")
	}
}

func TestLoad_RejectsPlaceholderDBPassword(t *testing.T) {
	clearSecretEnv(t)
	t.Setenv("GATEWAY_API_SERVICE_KEY", "a-real-generated-service-key")
	t.Setenv("GATEWAY_EPISODE_READ_SERVICE_KEY", "a-real-generated-episode-key")
	t.Setenv("GATEWAY_DB_HOST", "postgres")
	t.Setenv("GATEWAY_DB_PASSWORD", "devpassword")

	if _, err := Load(nonexistentConfigPath(t)); err == nil {
		t.Fatal("Load() with GATEWAY_DB_PASSWORD=devpassword: expected error, got nil")
	}
}

func TestLoad_AcceptsRealSecrets(t *testing.T) {
	clearSecretEnv(t)
	t.Setenv("GATEWAY_API_SERVICE_KEY", "a-real-generated-service-key")
	t.Setenv("GATEWAY_EPISODE_READ_SERVICE_KEY", "a-real-generated-episode-key")
	t.Setenv("GATEWAY_DB_HOST", "postgres")
	t.Setenv("GATEWAY_DB_USER", "eami_app")
	t.Setenv("GATEWAY_DB_PASSWORD", "S3cur3Pass")

	cfg, err := Load(nonexistentConfigPath(t))
	if err != nil {
		t.Fatalf("Load() with real-looking secrets: unexpected error: %v", err)
	}
	if cfg.API.ServiceKey != "a-real-generated-service-key" {
		t.Errorf("API.ServiceKey = %q, want the configured value", cfg.API.ServiceKey)
	}
}
