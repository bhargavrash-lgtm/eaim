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
			TokenRevokeServiceKey: "a-real-generated-revoke-key",
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

func TestValidate_RejectsPlaceholderTokenRevokeServiceKey(t *testing.T) {
	cases := []string{"", "changeme", "CHANGEME", " changeme ", "devpassword"}
	for _, v := range cases {
		cfg := validConfig()
		cfg.API.TokenRevokeServiceKey = v
		if err := validate(cfg); err == nil {
			t.Errorf("validate() with API.TokenRevokeServiceKey=%q: expected error, got nil", v)
		}
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

// TestValidate_AppliesTokenIssueRateLimitDefaults (B-119/B-120) proves the
// new config-driven thresholds default to byte-for-byte what issue_http.go's
// original hardcoded constants enforced (B-118: 20 requests/60s per agent),
// plus the new pre-auth concurrency default (B-120: 10 concurrent) -- an
// operator who sets no env var must see unchanged per-agent behavior.
func TestValidate_AppliesTokenIssueRateLimitDefaults(t *testing.T) {
	cfg := validConfig()
	if err := validate(cfg); err != nil {
		t.Fatalf("validate() unexpected error: %v", err)
	}
	if cfg.RateLimit.TokenIssuePerAgent != 20 {
		t.Errorf("RateLimit.TokenIssuePerAgent default = %d, want 20 (B-118's original hardcoded value)", cfg.RateLimit.TokenIssuePerAgent)
	}
	if cfg.RateLimit.TokenIssuePerAgentWindowSeconds != 60 {
		t.Errorf("RateLimit.TokenIssuePerAgentWindowSeconds default = %d, want 60", cfg.RateLimit.TokenIssuePerAgentWindowSeconds)
	}
	if cfg.RateLimit.TokenIssuePreAuthMaxConcurrent != 10 {
		t.Errorf("RateLimit.TokenIssuePreAuthMaxConcurrent default = %d, want 10", cfg.RateLimit.TokenIssuePreAuthMaxConcurrent)
	}
}

// TestValidate_OverriddenTokenIssueRateLimits_NotOverwrittenByDefaults proves
// AC1's real contract at the validate() layer: a non-zero caller-supplied
// value (what Load() would have already set from an env var) survives
// validate() unchanged, rather than being silently replaced by the default.
func TestValidate_OverriddenTokenIssueRateLimits_NotOverwrittenByDefaults(t *testing.T) {
	cfg := validConfig()
	cfg.RateLimit.TokenIssuePerAgent = 5
	cfg.RateLimit.TokenIssuePerAgentWindowSeconds = 30
	cfg.RateLimit.TokenIssuePreAuthMaxConcurrent = 25
	if err := validate(cfg); err != nil {
		t.Fatalf("validate() unexpected error: %v", err)
	}
	if cfg.RateLimit.TokenIssuePerAgent != 5 {
		t.Errorf("RateLimit.TokenIssuePerAgent = %d, want the overridden 5 (not the default)", cfg.RateLimit.TokenIssuePerAgent)
	}
	if cfg.RateLimit.TokenIssuePerAgentWindowSeconds != 30 {
		t.Errorf("RateLimit.TokenIssuePerAgentWindowSeconds = %d, want the overridden 30", cfg.RateLimit.TokenIssuePerAgentWindowSeconds)
	}
	if cfg.RateLimit.TokenIssuePreAuthMaxConcurrent != 25 {
		t.Errorf("RateLimit.TokenIssuePreAuthMaxConcurrent = %d, want the overridden 25", cfg.RateLimit.TokenIssuePreAuthMaxConcurrent)
	}
}

// TestValidate_RejectsNegativeTokenIssueRateLimits proves the same
// "0 means default, negative is a real misconfiguration" guard
// WorkflowRunPerAgent already has, extended to all 3 new fields.
func TestValidate_RejectsNegativeTokenIssueRateLimits(t *testing.T) {
	cases := []struct {
		name  string
		apply func(*Config)
	}{
		{"TokenIssuePerAgent", func(c *Config) { c.RateLimit.TokenIssuePerAgent = -1 }},
		{"TokenIssuePerAgentWindowSeconds", func(c *Config) { c.RateLimit.TokenIssuePerAgentWindowSeconds = -1 }},
		{"TokenIssuePreAuthMaxConcurrent", func(c *Config) { c.RateLimit.TokenIssuePreAuthMaxConcurrent = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.apply(cfg)
			if err := validate(cfg); err == nil {
				t.Fatalf("validate() with negative %s: expected an error, got nil", tc.name)
			}
		})
	}
}

// TestLoad_TokenIssueRateLimitEnvVars_OverrideDefaults (B-119/B-120) is
// AC1's real, end-to-end proof: setting the documented env vars changes
// Load()'s real returned config, not just validate()'s internal defaulting.
func TestLoad_TokenIssueRateLimitEnvVars_OverrideDefaults(t *testing.T) {
	clearSecretEnv(t)
	t.Setenv("GATEWAY_API_SERVICE_KEY", "a-real-generated-service-key")
	t.Setenv("GATEWAY_EPISODE_READ_SERVICE_KEY", "a-real-generated-episode-key")
	t.Setenv("GATEWAY_TOKEN_REVOKE_SERVICE_KEY", "a-real-generated-revoke-key")
	t.Setenv("GATEWAY_DB_HOST", "postgres")
	t.Setenv("GATEWAY_DB_USER", "eami_app")
	t.Setenv("GATEWAY_DB_PASSWORD", "S3cur3Pass")
	t.Setenv("TOKEN_ISSUE_RATE_LIMIT_PER_AGENT", "3")
	t.Setenv("TOKEN_ISSUE_RATE_LIMIT_WINDOW_SECONDS", "15")
	t.Setenv("TOKEN_ISSUE_PREAUTH_MAX_CONCURRENT", "9")

	cfg, err := Load(nonexistentConfigPath(t))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.RateLimit.TokenIssuePerAgent != 3 {
		t.Errorf("RateLimit.TokenIssuePerAgent = %d, want 3 (from env)", cfg.RateLimit.TokenIssuePerAgent)
	}
	if cfg.RateLimit.TokenIssuePerAgentWindowSeconds != 15 {
		t.Errorf("RateLimit.TokenIssuePerAgentWindowSeconds = %d, want 15 (from env)", cfg.RateLimit.TokenIssuePerAgentWindowSeconds)
	}
	if cfg.RateLimit.TokenIssuePreAuthMaxConcurrent != 9 {
		t.Errorf("RateLimit.TokenIssuePreAuthMaxConcurrent = %d, want 9 (from env)", cfg.RateLimit.TokenIssuePreAuthMaxConcurrent)
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
		"GATEWAY_API_SERVICE_KEY", "GATEWAY_EPISODE_READ_SERVICE_KEY", "GATEWAY_TOKEN_REVOKE_SERVICE_KEY",
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
	t.Setenv("GATEWAY_TOKEN_REVOKE_SERVICE_KEY", "a-real-generated-revoke-key")
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
	t.Setenv("GATEWAY_TOKEN_REVOKE_SERVICE_KEY", "a-real-generated-revoke-key")
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
