// Package collector is the HTTP client that POSTs assembled reports
// to the eami-collector receiver.
package collector

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/eami/agent/internal/config"
)

// Config holds sender connection settings, populated from eami-agent.yaml.
type Config struct {
	URL            string `yaml:"url"`
	APIKey         string `yaml:"api_key"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

// Sender wraps an http.Client and posts gzip-compressed reports.
type Sender struct {
	cfg    Config
	client *http.Client
}

// New creates a Sender.
func New(cfg Config) *Sender {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Sender{cfg: cfg, client: &http.Client{Timeout: timeout}}
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
