// Package config loads EAMI API server configuration from a YAML file.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level EAMI API configuration.
type Config struct {
	Server     ServerConfig    `yaml:"server"`
	Database   DatabaseConfig  `yaml:"database"`
	Auth       AuthConfig      `yaml:"auth"`
	Log        LogConfig       `yaml:"log"`
	ServiceKey string          `yaml:"service_key"`
	Collector  CollectorConfig `yaml:"collector"`
	Gateway    GatewayConfig   `yaml:"gateway"`

	// ToolCredentialsEncryptionKey is a hex-encoded 32-byte (AES-256) key
	// used to encrypt gateway_tools.credentials_encrypted before it is
	// written -- see internal/toolcreds. Empty by default: CreateTool fails
	// closed (returns an error, stores nothing) for any request that
	// includes credentials until this is set. Generate: openssl rand -hex 32
	ToolCredentialsEncryptionKey string `yaml:"tool_credentials_encryption_key"`
}

// CollectorConfig tells the API server how to reach the on-prem collector for
// metrics that live in its SQLite buffer (e.g. failed_delivery_count).
type CollectorConfig struct {
	// URL is the base URL of the collector HTTP API, e.g. "http://collector:9091".
	// Leave empty to disable collector-backed alert metrics.
	URL string `yaml:"url"`
	// APIKey is the X-API-Key used to authenticate against the collector's /stats endpoint.
	APIKey string `yaml:"api_key"`
}

// GatewayConfig tells the API server how to reach eami-gateway's episode read
// endpoint (B-002 Brief 2 -- proxies full episode content per ADR-019, never
// stored/served directly by eami-api). Both fields are optional at startup:
// if either is empty, the gateway proxy routes fail cleanly per-request
// (502) rather than eami-api refusing to start.
type GatewayConfig struct {
	// URL is the base URL of eami-gateway, e.g. "http://eami-gateway:8080".
	URL string `yaml:"url"`
	// EpisodeReadServiceKey is sent as X-Service-Key on calls to eami-gateway's
	// episode read endpoint. Must match eami-gateway's own
	// GATEWAY_EPISODE_READ_SERVICE_KEY (eami-gateway/internal/config) -- same
	// env var name is used on both sides so one .env value configures both.
	EpisodeReadServiceKey string `yaml:"episode_read_service_key"`
}

type ServerConfig struct {
	Port                int `yaml:"port"`
	ReadTimeoutSeconds  int `yaml:"read_timeout_seconds"`
	WriteTimeoutSeconds int `yaml:"write_timeout_seconds"`
	IdleTimeoutSeconds  int `yaml:"idle_timeout_seconds"`
}

type DatabaseConfig struct {
	DSN          string `yaml:"dsn"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

type AuthConfig struct {
	RSAPrivateKeyPath      string `yaml:"rsa_private_key_path"`
	AccessTokenTTLSeconds  int    `yaml:"access_token_ttl_seconds"`
	RefreshTokenTTLSeconds int    `yaml:"refresh_token_ttl_seconds"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Load reads configuration from path, then overlays environment variables.
// Missing file is tolerated when env vars supply the required settings.
func Load(path string) (*Config, error) {
	cfg := defaults()

	f, err := os.Open(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("config: open %q: %w", path, err)
	}
	if err == nil {
		defer f.Close()
		if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
			return nil, fmt.Errorf("config: decode %q: %w", path, err)
		}
	}

	// Environment variable overrides (docker-compose / Kubernetes style).
	if v := os.Getenv("API_LISTEN_PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil {
			cfg.Server.Port = port
		}
	}

	// Build DSN from individual DB env vars when present.
	host := os.Getenv("API_DB_HOST")
	dbName := os.Getenv("API_DB_NAME")
	user := os.Getenv("API_DB_USER")
	pass := os.Getenv("API_DB_PASSWORD")
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
		cfg.Database.DSN = fmt.Sprintf(
			"postgres://%s:%s@%s:5432/%s?sslmode=disable",
			user, pass, host, dbName,
		)
	}

	// Service key override.
	if v := os.Getenv("API_SERVICE_KEY"); v != "" {
		cfg.ServiceKey = v
	}

	// JWT signing key path override (B-026) -- mirrors eami-gateway's
	// GATEWAY_JWT_KEY_PATH. Left empty, auth.NewService generates an
	// ephemeral in-memory key (dev mode); set here, the key persists
	// across restarts via auth.loadOrGenerateKey.
	if v := os.Getenv("API_JWT_KEY_PATH"); v != "" {
		cfg.Auth.RSAPrivateKeyPath = v
	}

	// Collector config overrides.
	if v := os.Getenv("COLLECTOR_URL"); v != "" {
		cfg.Collector.URL = v
	}
	if v := os.Getenv("COLLECTOR_API_KEY"); v != "" {
		cfg.Collector.APIKey = v
	}

	// Gateway config overrides (B-002 Brief 2).
	if v := os.Getenv("API_GATEWAY_URL"); v != "" {
		cfg.Gateway.URL = v
	}
	if v := os.Getenv("GATEWAY_EPISODE_READ_SERVICE_KEY"); v != "" {
		cfg.Gateway.EpisodeReadServiceKey = v
	}

	// Tool credentials encryption key override.
	if v := os.Getenv("TOOL_CREDENTIALS_ENCRYPTION_KEY"); v != "" {
		cfg.ToolCredentialsEncryptionKey = v
	}

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
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
// .env value (API_DB_PASSWORD="changeme\r") is still caught.
func dsnHasPlaceholderPassword(dsn string) bool {
	pw, ok := dsnPassword(dsn)
	if !ok {
		return true
	}
	return isPlaceholderSecret(pw)
}

// validate rejects config that would let the server start with a known-bad
// or missing secret. A missing required secret must fail startup loudly and
// actionably, never fall through to a guessable default.
func validate(cfg *Config) error {
	if isPlaceholderSecret(cfg.ServiceKey) {
		return fmt.Errorf("config: service_key (API_SERVICE_KEY) must be set to a real secret, not empty or a known placeholder — see .env.example (generate: openssl rand -hex 32)")
	}
	if cfg.Database.DSN == "" {
		return fmt.Errorf("config: database.dsn is required — set API_DB_HOST/API_DB_USER/API_DB_PASSWORD or database.dsn in config, see .env.example")
	}
	if dsnHasPlaceholderPassword(cfg.Database.DSN) {
		return fmt.Errorf("config: database DSN password (POSTGRES_PASSWORD/API_DB_PASSWORD) must be set to a real secret, not empty or a known placeholder — see .env.example (generate: openssl rand -base64 24)")
	}
	return nil
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Port:                8080,
			ReadTimeoutSeconds:  30,
			WriteTimeoutSeconds: 60,
			IdleTimeoutSeconds:  120,
		},
		Database: DatabaseConfig{
			// No default DSN: a real one must come from database.dsn in the
			// YAML config or the API_DB_* env vars -- validate() rejects an
			// empty or placeholder-password DSN rather than silently using one.
			MaxOpenConns: 25,
			MaxIdleConns: 5,
		},
		Auth: AuthConfig{
			AccessTokenTTLSeconds:  3600,
			RefreshTokenTTLSeconds: 2592000,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		// No default ServiceKey: a real one must come from service_key in the
		// YAML config or API_SERVICE_KEY -- validate() rejects empty/placeholder.
		Gateway: GatewayConfig{
			URL: "http://eami-gateway:8080",
		},
	}
}
