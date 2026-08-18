// Package collector is the HTTP client that POSTs assembled reports
// to the eami-collector receiver.
package collector

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/eami/agent/internal/config"
)

// Config holds sender connection settings, populated from eami-agent.yaml.
type Config struct {
	URL            string `yaml:"url"`
	APIKey         string `yaml:"api_key"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
	// CACertPath, when set, is trusted as an ADDITIONAL root CA for
	// connections to URL (B-072) -- see config.CollectorConfig.CACertPath's
	// doc comment for the full reasoning (the appliance's default
	// self-signed cert isn't in the OS trust store). Empty means "use the
	// OS default trust store only," identical to New()'s pre-B-072
	// behavior -- a customer-installed, publicly-trusted certificate needs
	// nothing here.
	CACertPath string `yaml:"ca_cert_path"`
}

// Sender wraps an http.Client and posts gzip-compressed reports.
type Sender struct {
	cfg    Config
	client *http.Client
}

// New creates a Sender. Returns an error only when CACertPath is set but
// unreadable/unparseable -- a misconfigured trust anchor must fail loudly
// at startup, not silently fall back to a client that will then fail
// every single request with an opaque TLS error.
func New(cfg Config) (*Sender, error) {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{Timeout: timeout}

	if cfg.CACertPath != "" {
		pemBytes, err := os.ReadFile(cfg.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("sender: read ca_cert_path %q: %w", cfg.CACertPath, err)
		}
		// AppendCertsFromPEM's own return value (ok bool, no error detail)
		// is the only signal available -- a bad/empty PEM file must still
		// fail loudly here rather than silently produce a pool with zero
		// certs in it (which would make every connection fail TLS
		// verification with a generic "certificate signed by unknown
		// authority" error, much harder to diagnose than this explicit one).
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			// SystemCertPool can return (nil, err) on platforms without a
			// usable system pool (documented Go behavior on some
			// Windows/other configurations) -- start from an empty pool
			// rather than fail startup entirely; the appliance's own CA is
			// still added below either way.
			pool = x509.NewCertPool()
		}
		if ok := pool.AppendCertsFromPEM(pemBytes); !ok {
			return nil, fmt.Errorf("sender: ca_cert_path %q contains no valid PEM certificate", cfg.CACertPath)
		}
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		}
	}

	return &Sender{cfg: cfg, client: client}, nil
}

// Send marshals report to JSON, gzip-compresses it, and POSTs to the collector.
// Returns nil on HTTP 202, an error otherwise.
func (s *Sender) Send(ctx context.Context, report any) error {
	if s.cfg.URL == "" {
		return nil // no collector configured — stdout-only mode
	}

	raw, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("sender: marshal: %w", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		return fmt.Errorf("sender: gzip write: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("sender: gzip close: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.URL+"/v1/ingest", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("X-API-Key", s.cfg.APIKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sender: post: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("sender: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// AgentConfigUpdate is the remote config payload returned by the collector
// config proxy (GET /v1/agent-config/{agent_id}).
type AgentConfigUpdate struct {
	ScanIntervalSeconds int      `json:"scan_interval_seconds"`
	ModelScanPaths      []string `json:"model_scan_paths"`
	MaxReportSizeBytes  int      `json:"max_report_size_bytes"`
	EnabledScanners     []string `json:"enabled_scanners"`
}

// FetchConfig polls the collector config proxy and applies any non-zero fields
// to dst. A 404 (agent not yet registered) is silently ignored. All other
// errors are returned but never fatal — callers should log and continue.
func (s *Sender) FetchConfig(ctx context.Context, agentID string, dst *config.Config) error {
	if s.cfg.URL == "" || agentID == "" {
		return nil
	}

	reqURL := s.cfg.URL + "/v1/agent-config/" + agentID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("fetchconfig: build request: %w", err)
	}
	req.Header.Set("X-API-Key", s.cfg.APIKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetchconfig: get: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil // agent not yet registered — not an error
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetchconfig: status %d", resp.StatusCode)
	}

	var update AgentConfigUpdate
	if err := json.NewDecoder(resp.Body).Decode(&update); err != nil {
		return fmt.Errorf("fetchconfig: decode: %w", err)
	}

	// Apply non-zero fields only — zero values mean "no change".
	if update.ScanIntervalSeconds > 0 {
		dst.Agent.IntervalSecs = update.ScanIntervalSeconds
	}
	if len(update.ModelScanPaths) > 0 {
		dst.Detection.ModelFileScanPaths = update.ModelScanPaths
	}
	if len(update.EnabledScanners) > 0 {
		dst.Detection.EnabledScanners = update.EnabledScanners
	}
	return nil
}
