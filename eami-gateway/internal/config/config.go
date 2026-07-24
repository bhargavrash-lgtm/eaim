// Package config loads and validates the gateway YAML configuration.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level gateway configuration.
type Config struct {
	ListenAddr  string         `yaml:"listen_addr"`
	PostgresDSN string         `yaml:"postgres_dsn"`
	Token       TokenConfig    `yaml:"token"`
	Policy      PolicyConfig   `yaml:"policy"`
	Proxy       ProxyConfig    `yaml:"proxy"`
	Approval    ApprovalConfig `yaml:"approval"`
	API         APIConfig      `yaml:"api"`
	Log         LogConfig      `yaml:"log"`
}

// TokenConfig controls AI token issuance (ADR-006).
type TokenConfig struct {
	DefaultTTLSeconds int    `yaml:"default_ttl_seconds"`
	KeypairPath       string `yaml:"keypair_path"`
}

// PolicyConfig points to the policy rules file and optional semantic LLM.
type PolicyConfig struct {
	RulesPath           string `yaml:"rules_path"`
	SemanticLLMEndpoint string `yaml:"semantic_llm_endpoint"`
	SemanticLLMAPIKey   string `yaml:"semantic_llm_api_key"`
}

// ProxyConfig defines the downstream MCP proxy target.
type ProxyConfig struct {
	DownstreamSSEAddr string `yaml:"downstream_sse_addr"`
}

// ApprovalConfig controls escalation notification (ADR-011).
type ApprovalConfig struct {
	SlackWebhookURL string `yaml:"slack_webhook_url"`
	ExpirySeconds   int    `yaml:"expiry_seconds"`
	UIBaseURL       string `yaml:"ui_base_url"`
}

// APIConfig contains settings for calling back into the eami-api service.
// The gateway uses this to write token usage records (FinOps) and to
// resolve org-level settings without a direct DB query.
type APIConfig struct {
	// BaseURL is the internal base URL of the eami-api service.
	// Example: "http://eami-api:8081"
	BaseURL string `yaml:"base_url"`

	// ServiceKey is the shared secret sent as X-Service-Key on internal API calls.
	ServiceKey string `yaml:"service_key"`

	// EpisodeReadServiceKey is the shared secret required (as X-Service-Key)
	// on inbound calls to the gateway's episode read endpoint
	// (GET /v1/gateway/episodes*, see internal/episode/http.go). Deliberately
	// separate from ServiceKey above: that key is sent outbound by the
	// gateway to eami-api; this one is validated inbound, from eami-api's
	// memory proxy. Keeping them distinct means a leak of one does not also
	// grant the other capability.
	EpisodeReadServiceKey string `yaml:"episode_read_service_key"`
}

// LogConfig controls logging behaviour.
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Load reads the YAML file at path and overlays environment variables.
// Missing file is tolerated when env vars supply the required settings.
func Load(path string) (*Config, error) {
	var cfg Config

	f, err := os.Open(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("config: open %s: %w", path, err)
	}
	if err == nil {
		defer f.Close()
		dec := yaml.NewDecoder(f)
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil {
			return nil, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}

	// Environment variable overrides (docker-compose / Kubernetes style).
	if v := os.Getenv("GATEWAY_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	} else if p := os.Getenv("GATEWAY_LISTEN_API_PORT"); p != "" {
		cfg.ListenAddr = "0.0.0.0:" + p
	}

	// Build postgres DSN from individual env vars when present.
	host := os.Getenv("GATEWAY_DB_HOST")
	dbName := os.Getenv("GATEWAY_DB_NAME")
	user := os.Getenv("GATEWAY_DB_USER")
	pass := os.Getenv("GATEWAY_DB_PASSWORD")
	if host != "" || dbName != "" || user != "" || pass != "" {
		if host == "" {
			host = "localhost"
		}
		if dbName == "" {
			dbName = "eami"
		}
		if user == "" {
			user = "eami_app"
		}
		cfg.PostgresDSN = fmt.Sprintf(
			"postgres://%s:%s@%s:5432/%s?sslmode=disable",
			user, pass, host, dbName,
		)
	}

	if v := os.Getenv("GATEWAY_JWT_KEY_PATH"); v != "" {
		cfg.Token.KeypairPath = v
	}
	if v := os.Getenv("GATEWAY_APPROVAL_SLACK_WEBHOOK"); v != "" {
		cfg.Approval.SlackWebhookURL = v
	}
	if v := os.Getenv("GATEWAY_UI_BASE_URL"); v != "" {
		cfg.Approval.UIBaseURL = v
	}
	if v := os.Getenv("GATEWAY_API_BASE_URL"); v != "" {
		cfg.API.BaseURL = v
	}
	if v := os.Getenv("GATEWAY_API_SERVICE_KEY"); v != "" {
		cfg.API.ServiceKey = v
	}
	if v := os.Getenv("GATEWAY_EPISODE_READ_SERVICE_KEY"); v != "" {
		cfg.API.EpisodeReadServiceKey = v
	}

	// Policy rules file: default to empty (allows gateway to start without rules)
	if cfg.Policy.RulesPath == "" {
		cfg.Policy.RulesPath = "/etc/eami-gateway/rules.yaml"
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// knownPlaceholderSecrets lists literal values that must never be accepted
// as a real secret, even if a caller explicitly sets them -- these are the
// values that historically shipped as working defaults in this repo
// (eami-api.yaml, eami-gateway.yaml, docker-compose.yml), so treating them
// as "configured" would silently reproduce the same bypass.
var knownPlaceholderSecrets = map[string]bool{
	"":            true,
	"changeme":    true,
	"devpassword": true,
}

func isPlaceholderSecret(v string) bool {
	return knownPlaceholderSecrets[strings.ToLower(strings.TrimSpace(v))]
}

// dsnPassword extracts the password segment from a "scheme://user:password@
// host/..." style DSN (the only shape this file ever builds or reads). ok is
// false if dsn doesn't have that shape at all, which validate() treats as
// unconfigured rather than trying to guess.
func dsnPassword(dsn string) (pw string, ok bool) {
	at := strings.LastIndex(dsn, "@")
	if at < 0 {
		return "", false
	}
	userinfo := dsn[:at]
	colon := strings.LastIndex(userinfo, ":")
	if colon < 0 || !strings.Contains(userinfo[:colon], "://") {
		return "", false
	}
	return userinfo[colon+1:], true
}

// dsnHasPlaceholderPassword reports whether dsn's password segment is empty,
// a known placeholder, or the DSN doesn't parse -- checked through
// isPlaceholderSecret (not a raw substring match) so the same trimming/
// case-folding applies here as to every other secret, e.g. a CRLF-corrupted
// .env value (GATEWAY_DB_PASSWORD="changeme\r") is still caught.
func dsnHasPlaceholderPassword(dsn string) bool {
	pw, ok := dsnPassword(dsn)
	if !ok {
		return true
	}
	return isPlaceholderSecret(pw)
}

// validate applies defaults and checks required fields.
func validate(cfg *Config) error {
	// Defaults
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "0.0.0.0:8443"
	}
	if cfg.Token.DefaultTTLSeconds == 0 {
		cfg.Token.DefaultTTLSeconds = 900
	}
	if cfg.Token.KeypairPath == "" {
		cfg.Token.KeypairPath = "/var/lib/eami-gateway/gateway.key"
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "json"
	}
	if cfg.Approval.ExpirySeconds == 0 {
		cfg.Approval.ExpirySeconds = 600 // 10 minutes per spec (ADR-011)
	}
	if cfg.API.BaseURL == "" {
		cfg.API.BaseURL = "http://eami-api:8081"
	}

	// Required fields
	if cfg.PostgresDSN == "" {
		return fmt.Errorf("config: postgres_dsn is required")
	}
	if dsnHasPlaceholderPassword(cfg.PostgresDSN) {
		return fmt.Errorf("config: postgres_dsn password (POSTGRES_PASSWORD/GATEWAY_DB_PASSWORD) must be set to a real secret, not empty or a known placeholder — see .env.example (generate: openssl rand -base64 24)")
	}
	if cfg.Policy.RulesPath == "" {
		return fmt.Errorf("config: policy.rules_path is required")
	}
	if isPlaceholderSecret(cfg.API.ServiceKey) {
		return fmt.Errorf("config: api.service_key (GATEWAY_API_SERVICE_KEY) must be set to a real secret, not empty or a known placeholder — see .env.example (generate: openssl rand -hex 32)")
	}
	if cfg.API.EpisodeReadServiceKey == "" {
		return fmt.Errorf("config: api.episode_read_service_key is required (gates full episode content access — see GATEWAY_EPISODE_READ_SERVICE_KEY)")
	}

	// Bounds check
	if cfg.Token.DefaultTTLSeconds < 60 || cfg.Token.DefaultTTLSeconds > 14400 {
		return fmt.Errorf("config: token.default_ttl_seconds must be between 60 and 14400, got %d", cfg.Token.DefaultTTLSeconds)
	}

	return nil
}
