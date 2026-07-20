// Package forwarder reads buffered agent reports from SQLite and delivers them
// in batches to the SaaS API endpoint.
//
// Delivery semantics:
//   HTTP 202  → accepted; rows deleted from buffer.
//   HTTP 4xx  → bad data; rows moved to dead_letter, not retried.
//   HTTP 5xx / net error → transient; rows stay in buffer, attempts incremented.
//   attempts > maxAttempts → moved to dead_letter with reason "max_attempts".
package forwarder

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Config holds forwarder tunables from eami-collector.yaml.
type Config struct {
	SAASURL         string `yaml:"saas_url"`
	APIKey          string `yaml:"api_key"`
	// ServiceKey is the X-Service-Key sent to eami-api when forwarding batches.
	// If empty, falls back to APIKey (for simple single-key deployments).
	ServiceKey      string `yaml:"service_key"`
	BatchSize       int    `yaml:"batch_size"`
	IntervalSeconds int    `yaml:"interval_seconds"`
	TimeoutSeconds  int    `yaml:"timeout_seconds"`
}

func (c *Config) defaults() {
	if c.BatchSize == 0 {
		c.BatchSize = 100
	}
	if c.IntervalSeconds == 0 {
		c.IntervalSeconds = 10
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 30
	}
}

const (
	maxAttempts   = 10
	batchEndpoint = "/v1/ingest/batch"
	apiKeyHeader  = "X-Service-Key"
)

// Forwarder polls the SQLite buffer and forwards batches to the SaaS API.
type Forwarder struct {
	cfg    Config
	db     *sql.DB
	client *http.Client
	log    *slog.Logger
}

// New creates a Forwarder. db must have report_buffer and dead_letter tables.
func New(cfg Config, db *sql.DB, log *slog.Logger) *Forwarder {
	cfg.defaults()
	if log == nil {
		log = slog.Default()
	}
	return &Forwarder{
		cfg: cfg,
		db:  db,
		client: &http.Client{
			Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
		},
		log: log,
	}
}

// Run blocks until ctx is cancelled, ticking every IntervalSeconds.
func (f *Forwarder) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Duration(f.cfg.IntervalSeconds) * time.Second)
	defer ticker.Stop()
	f.log.Info("forwarder started",
		"saas_url", f.cfg.SAASURL,
		"batch_size", f.cfg.BatchSize,
		"interval_s", f.cfg.IntervalSeconds)
	for {
		select {
		case <-ctx.Done():
			f.log.Info("forwarder shutting down")
			return ctx.Err()
		case <-ticker.C:
			if err := f.tick(ctx); err != nil {
				f.log.Error("forwarder tick failed", "err", err)
			}
		}
	}
}

func (f *Forwarder) tick(ctx context.Context) error {
	rows, err := f.fetchBatch(ctx, f.cfg.BatchSize)
	if err != nil {
		return fmt.Errorf("fetch batch: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	f.log.Debug("forwarding batch", "count", len(rows))

	status, err := f.sendBatch(ctx, rows)
	if err != nil {
		f.log.Warn("send failed (transient)", "err", err, "rows", len(rows))
		return f.markFailed(ctx, rows)
	}
	switch {
	case status == http.StatusAccepted:
		f.log.Info("batch accepted", "count", len(rows))
		return f.markDelivered(ctx, rowIDs(rows))
	case status >= 400 && status < 500:
		reason := fmt.Sprintf("http_4xx:%d", status)
		f.log.Error("batch rejected (bad data); moving to dead-letter", "status", status)
		return f.moveToDeadLetter(ctx, rows, reason)
	default:
		f.log.Warn("SaaS returned transient error", "status", status)
		return f.markFailed(ctx, rows)
	}
}

// BufferRow is a row from the report_buffer table.
type BufferRow struct {
	ID         string
	ReceivedAt time.Time
	AgentID    string
	Hostname   string
	ReportJSON []byte // gzip-compressed
	Attempts   int
}

func (f *Forwarder) fetchBatch(ctx context.Context, n int) ([]BufferRow, error) {
	rows, err := f.db.QueryContext(ctx,
		`SELECT id, received_at, agent_id, hostname, report_json, attempts
		 FROM report_buffer ORDER BY received_at ASC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BufferRow
	for rows.Next() {
		var r BufferRow
		var ts string
		if err := rows.Scan(&r.ID, &ts, &r.AgentID, &r.Hostname, &r.ReportJSON, &r.Attempts); err != nil {
			return nil, err
		}
		r.ReceivedAt, _ = time.Parse(time.RFC3339, ts)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (f *Forwarder) markDelivered(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := f.db.ExecContext(ctx,
		`DELETE FROM report_buffer WHERE id IN (`+placeholders(len(ids))+`)`,
		stringsToAny(ids)...)
	return err
}

func (f *Forwarder) markFailed(ctx context.Context, rows []BufferRow) error {
	now := time.Now().UTC().Format(time.RFC3339)
	var retry []string
	var dl []BufferRow
	for _, r := range rows {
		if r.Attempts+1 > maxAttempts {
			dl = append(dl, r)
		} else {
			retry = append(retry, r.ID)
		}
	}
	if len(retry) > 0 {
		args := append([]any{now}, stringsToAny(retry)...)
		if _, err := f.db.ExecContext(ctx,
			`UPDATE report_buffer SET attempts=attempts+1, last_attempt=? WHERE id IN (`+placeholders(len(retry))+`)`,
			args...); err != nil {
			return fmt.Errorf("mark failed: %w", err)
		}
	}
	if len(dl) > 0 {
		f.log.Warn("max attempts exceeded; moving to dead-letter", "count", len(dl))
		if err := f.moveToDeadLetter(ctx, dl, "max_attempts"); err != nil {
			return err
		}
	}
	return nil
}

func (f *Forwarder) moveToDeadLetter(ctx context.Context, rows []BufferRow, reason string) error {
	if len(rows) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO dead_letter (id,received_at,agent_id,hostname,report_json,attempts,failed_at,reason) VALUES (?,?,?,?,?,?,?,?)`,
			r.ID, r.ReceivedAt.UTC().Format(time.RFC3339), r.AgentID, r.Hostname,
			r.ReportJSON, r.Attempts+1, now, reason,
		); err != nil {
			return fmt.Errorf("insert dead_letter %s: %w", r.ID, err)
		}
	}
	ids := rowIDs(rows)
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM report_buffer WHERE id IN (`+placeholders(len(ids))+`)`,
		stringsToAny(ids)...); err != nil {
		return err
	}
	return tx.Commit()
}

// --- HTTP ---

type BatchReport struct {
	ID         string          `json:"id"`
	AgentID    string          `json:"agent_id"`
	Hostname   string          `json:"hostname"`
	ReceivedAt time.Time       `json:"received_at"`
	Report     json.RawMessage `json:"report"`
}

type batchPayload struct {
	Reports []BatchReport `json:"reports"`
}

func (f *Forwarder) sendBatch(ctx context.Context, rows []BufferRow) (int, error) {
	payload, err := f.buildPayload(rows)
	if err != nil {
		return 0, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	url := strings.TrimRight(f.cfg.SAASURL, "/") + batchEndpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	serviceKey := f.cfg.ServiceKey
	if serviceKey == "" {
		serviceKey = f.cfg.APIKey
	}
	req.Header.Set(apiKeyHeader, serviceKey)
	resp, err := f.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func (f *Forwarder) buildPayload(rows []BufferRow) (*batchPayload, error) {
	reports := make([]BatchReport, 0, len(rows))
	for _, r := range rows {
		raw, err := gunzip(r.ReportJSON)
		if err != nil {
			f.log.Warn("gunzip failed; skipping row", "id", r.ID, "err", err)
			continue
		}
		reports = append(reports, BatchReport{
			ID: r.ID, AgentID: r.AgentID, Hostname: r.Hostname,
			ReceivedAt: r.ReceivedAt, Report: json.RawMessage(raw),
		})
	}
	return &batchPayload{Reports: reports}, nil
}

func gunzip(data []byte) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	return io.ReadAll(gr)
}

func rowIDs(rows []BufferRow) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}

func placeholders(n int) string {
	if n == 0 {
		return ""
	}
	sb := strings.Builder{}
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte('?')
	}
	return sb.String()
}

func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
