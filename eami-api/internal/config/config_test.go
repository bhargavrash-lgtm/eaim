package config

import (
	"os"
	"path/filepath"
	"testing"
)

func validConfig() *Config {
	return &Config{
		ServiceKey: "a-real-generated-service-key",
		Database: DatabaseConfig{
			DSN: "postgres://eami_app:S3cur3Pass@postgres:5432/eami?sslmode=disable",
		},
		// B-070: validate() now also rejects a non-positive rate-limit
		// value -- a bare zero-value RateLimitConfig (what this struct
		// literal would otherwise leave in place) would fail every one of
		// this file's existing "accepts a valid config" cases. Real Load()
		// always populates this via defaults()/DefaultRateLimitConfig()
		// before validate() ever runs, so this fixture matches that.
		RateLimit: DefaultRateLimitConfig(),
	}
}

func TestValidate_RejectsPlaceholderServiceKey(t *testing.T) {
	cases := []string{"", "changeme", "CHANGEME", " changeme ", "devpassword"}
	for _, v := range cases {
		cfg := validConfig()
		cfg.ServiceKey = v
		if err := validate(cfg); err == nil {
			t.Errorf("validate() with ServiceKey=%q: expected error, got nil", v)
		}
	}
}

func TestValidate_AcceptsRealServiceKey(t *testing.T) {
	cfg := validConfig()
	if err := validate(cfg); err != nil {
		t.Errorf("validate() with a real-looking ServiceKey: unexpected error: %v", err)
	}
}

func TestValidate_RejectsEmptyDSN(t *testing.T) {
	cfg := validConfig()
	cfg.Database.DSN = ""
	if err := validate(cfg); err == nil {
		t.Error("validate() with empty Database.DSN: expected error, got nil")
	}
}

func TestValidate_RejectsPlaceholderDSNPassword(t *testing.T) {
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
		cfg.Database.DSN = dsn
		if err := validate(cfg); err == nil {
			t.Errorf("validate() with DSN=%q: expected error, got nil", dsn)
		}
	}
}

func TestValidate_AcceptsRealDSNPassword(t *testing.T) {
	cases := []string{
		"postgres://eami_app:S3cur3Pass@postgres:5432/eami?sslmode=disable",
		"postgres://eami_app:aGVsbG93b3JsZA==@postgres:5432/eami?sslmode=disable",
	}
	for _, dsn := range cases {
		cfg := validConfig()
		cfg.Database.DSN = dsn
		if err := validate(cfg); err != nil {
			t.Errorf("validate() with DSN=%q: unexpected error: %v", dsn, err)
		}
	}
}

// TestValidate_RejectsNonPositiveRateLimitValues (B-070) is the regression
// test for a real panic a security review found: rateLimiter.Allow indexes
// kept[0] once len(kept) >= limit, which is true on the very first request
// whenever limit <= 0 -- so a misconfigured (e.g. typo'd "0") rate-limit env
// var wouldn't just "disable" limiting, it would crash every request to the
// guarded route. validate() must reject 0 and negative values for all six
// fields before that code path is ever reachable.
func TestValidate_RejectsNonPositiveRateLimitValues(t *testing.T) {
	fields := []func(cfg *Config, v int){
		func(cfg *Config, v int) { cfg.RateLimit.LoginPerIP = v },
		func(cfg *Config, v int) { cfg.RateLimit.LoginPerIPWindowSeconds = v },
		func(cfg *Config, v int) { cfg.RateLimit.LoginPerAccount = v },
		func(cfg *Config, v int) { cfg.RateLimit.LoginPerAccountWindowSeconds = v },
		func(cfg *Config, v int) { cfg.RateLimit.Setup = v },
		func(cfg *Config, v int) { cfg.RateLimit.SetupWindowSeconds = v },
	}
	for i, setField := range fields {
		for _, bad := range []int{0, -1, -100} {
			cfg := validConfig()
			setField(cfg, bad)
			if err := validate(cfg); err == nil {
				t.Errorf("field %d, value %d: expected validate() to reject a non-positive rate-limit value, got nil error", i, bad)
			}
		}
	}
}

func TestValidate_AcceptsPositiveRateLimitValues(t *testing.T) {
	cfg := validConfig()
	cfg.RateLimit = RateLimitConfig{
		LoginPerIP: 1, LoginPerIPWindowSeconds: 1,
		LoginPerAccount: 1, LoginPerAccountWindowSeconds: 1,
		Setup: 1, SetupWindowSeconds: 1,
	}
	if err := validate(cfg); err != nil {
		t.Errorf("validate() with all-positive rate-limit values: unexpected error: %v", err)
	}
}

// TestLoad_RejectsZeroRateLimitEnvVar proves the rejection is reachable via
// the real env-var startup path, not just validate()'s internal logic --
// same integration-level standard as TestLoad_RejectsUnsetServiceKey above.
func TestLoad_RejectsZeroRateLimitEnvVar(t *testing.T) {
	clearSecretEnv(t)
	t.Setenv("API_SERVICE_KEY", "a-real-generated-service-key")
	t.Setenv("API_DB_HOST", "postgres")
	t.Setenv("API_DB_PASSWORD", "S3cur3Pass")
	t.Setenv("LOGIN_RATE_LIMIT_PER_ACCOUNT", "0")

	if _, err := Load(nonexistentConfigPath(t)); err == nil {
		t.Fatal("Load() with LOGIN_RATE_LIMIT_PER_ACCOUNT=0: expected error, got nil")
	}
}

// clearSecretEnv unsets every env var Load() reads, so each test starts from
// a clean slate regardless of what's set in the host shell.
func clearSecretEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"API_LISTEN_PORT", "API_DB_HOST", "API_DB_NAME", "API_DB_USER", "API_DB_PASSWORD",
		"API_SERVICE_KEY", "COLLECTOR_URL", "COLLECTOR_API_KEY", "API_GATEWAY_URL",
		"GATEWAY_EPISODE_READ_SERVICE_KEY", "TOOL_CREDENTIALS_ENCRYPTION_KEY",
		"LOGIN_RATE_LIMIT_PER_IP", "LOGIN_RATE_LIMIT_PER_IP_WINDOW_SECONDS",
		"LOGIN_RATE_LIMIT_PER_ACCOUNT", "LOGIN_RATE_LIMIT_PER_ACCOUNT_WINDOW_SECONDS",
		"SETUP_RATE_LIMIT", "SETUP_RATE_LIMIT_WINDOW_SECONDS",
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

// TestLoad_RejectsUnsetServiceKey is the integration-level regression test
// for the actual bug this task fixes: Load() previously had no validate()
// call at all, so an unset API_SERVICE_KEY silently kept defaults()'s
// hardcoded "changeme" and requireServiceKey accepted it at runtime. This
// test fails if that wiring (not just validate()'s internal logic) regresses.
func TestLoad_RejectsUnsetServiceKey(t *testing.T) {
	clearSecretEnv(t)
	t.Setenv("API_DB_HOST", "postgres")
	t.Setenv("API_DB_PASSWORD", "S3cur3Pass")
	// API_SERVICE_KEY deliberately left unset.

	if _, err := Load(nonexistentConfigPath(t)); err == nil {
		t.Fatal("Load() with API_SERVICE_KEY unset: expected error, got nil")
	}
}

func TestLoad_RejectsPlaceholderDBPassword(t *testing.T) {
	clearSecretEnv(t)
	t.Setenv("API_SERVICE_KEY", "a-real-generated-service-key")
	t.Setenv("API_DB_HOST", "postgres")
	t.Setenv("API_DB_PASSWORD", "changeme")

	if _, err := Load(nonexistentConfigPath(t)); err == nil {
		t.Fatal("Load() with API_DB_PASSWORD=changeme: expected error, got nil")
	}
}

func TestLoad_AcceptsRealSecrets(t *testing.T) {
	clearSecretEnv(t)
	t.Setenv("API_SERVICE_KEY", "a-real-generated-service-key")
	t.Setenv("API_DB_HOST", "postgres")
	t.Setenv("API_DB_USER", "eami_app")
	t.Setenv("API_DB_PASSWORD", "S3cur3Pass")

	cfg, err := Load(nonexistentConfigPath(t))
	if err != nil {
		t.Fatalf("Load() with real-looking secrets: unexpected error: %v", err)
	}
	if cfg.ServiceKey != "a-real-generated-service-key" {
		t.Errorf("ServiceKey = %q, want the configured value", cfg.ServiceKey)
	}
}
