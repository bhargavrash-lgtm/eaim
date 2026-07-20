// Package alerting implements the EAMI alert evaluation engine.
// It runs as a background goroutine, periodically evaluating active alert rules
// against live metric data and dispatching notifications when rules fire.
package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/store"
)

// ConditionConfig is the machine-readable rule definition stored in
// alert_rules.condition_config as JSONB.
type ConditionConfig struct {
	Metric        string  `json:"metric"`
	Condition     string  `json:"condition"`      // only "gt" for MVP
	Threshold     float64 `json:"threshold"`
	WindowMinutes int     `json:"window_minutes"` // 5 | 15 | 60 | 1440
}

// EvalResult describes the outcome of a dry-run rule evaluation.
type EvalResult struct {
	RuleName    string
	Metric      string
	MetricValue float64
	Threshold   float64
	WouldFire   bool
	Message     string
}

// collectorClient calls the collector's /stats endpoint to fetch delivery metrics.
type collectorClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// deadLetterCount calls GET {baseURL}/stats?since=<RFC3339> and returns dead_letter_rows.
func (c *collectorClient) deadLetterCount(ctx context.Context, since time.Time) (int64, error) {
	url := strings.TrimRight(c.baseURL, "/") + "/stats?since=" + since.UTC().Format(time.RFC3339)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("collector /stats: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("collector /stats: HTTP %d: %s", resp.StatusCode, errBody)
	}

	var payload struct {
		DeadLetterRows int64 `json:"dead_letter_rows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, fmt.Errorf("collector /stats: decode: %w", err)
	}
	return payload.DeadLetterRows, nil
}

// Engine runs the alert evaluation loop.
type Engine struct {
	queries   *store.Queries
	collector *collectorClient // nil when collector URL is not configured
}

// NewEngine creates an Engine backed by the given query store.
// collectorURL and collectorAPIKey are optional; if collectorURL is empty the
// failed_delivery_count metric will log a warning and return 0.
func NewEngine(queries *store.Queries, collectorURL, collectorAPIKey string) *Engine {
	e := &Engine{queries: queries}
	if collectorURL != "" {
		e.collector = &collectorClient{
			baseURL: collectorURL,
			apiKey:  collectorAPIKey,
			http:    &http.Client{Timeout: 5 * time.Second},
		}
	}
	return e
}

// Run starts the evaluation loop, ticking every minute.
// Blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	log.Println("alerting: engine started")
	e.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			log.Println("alerting: engine stopped")
			return
		case <-ticker.C:
			e.tick(ctx)
		}
	}
}

func (e *Engine) tick(ctx context.Context) {
	rules, err := e.queries.ListAllActiveAlertRules(ctx)
	if err != nil {
		log.Printf("alerting: list rules: %v", err)
		return
	}
	for _, rule := range rules {
		if err := e.evaluateRule(ctx, rule); err != nil {
			log.Printf("alerting: rule %s (%s): %v", rule.ID, rule.Name, err)
		}
	}
}

func (e *Engine) evaluateRule(ctx context.Context, rule store.AlertRule) error {
	cfg, err := ParseConditionConfig(rule.ConditionConfig)
	if err != nil {
		return fmt.Errorf("parse condition_config: %w", err)
	}

	value, err := e.queryMetricWithCollector(ctx, rule.OrgID, cfg.Metric, cfg.WindowMinutes)
	if err != nil {
		return fmt.Errorf("query metric %s: %w", cfg.Metric, err)
	}

	if !conditionMet(cfg, value) {
		return nil
	}

	// Duplicate suppression: skip if an open alert already exists.
	_, err = e.queries.GetOpenAlertByRuleID(ctx, rule.ID)
	if err == nil {
		return nil // already open — skip
	}
	if err != pgx.ErrNoRows {
		return fmt.Errorf("check open alert: %w", err)
	}

	msg := fmt.Sprintf("%s: %s = %.2f (threshold %.2f, window %dm)",
		rule.Name, cfg.Metric, value, cfg.Threshold, cfg.WindowMinutes)
	alert, err := e.queries.CreateAlert(ctx, rule.OrgID, rule.ID, rule.Severity, msg, nil, value)
	if err != nil {
		return fmt.Errorf("create alert: %w", err)
	}

	e.dispatchNotifications(ctx, rule, *alert, value)
	return nil
}

func (e *Engine) dispatchNotifications(ctx context.Context, rule store.AlertRule, alert store.Alert, metricValue float64) {
	cfg, err := e.queries.GetNotificationConfig(ctx, rule.OrgID)
	if err != nil {
		return // no config or not found — skip
	}
	if cfg.SlackEnabled && cfg.SlackWebhookURL.Valid && cfg.SlackWebhookURL.String != "" {
		msg := BuildSlackMessage(rule, metricValue)
		if err := SendSlack(cfg.SlackWebhookURL.String, msg); err != nil {
			log.Printf("alerting: slack dispatch for rule %s: %v", rule.Name, err)
		} else {
			_ = e.queries.MarkAlertNotified(ctx, alert.ID)
		}
	}
}

// EvaluateRuleDryRun evaluates a rule without creating DB records or sending
// notifications. Used by POST /v1/alerts/rules/{id}/test.
func (e *Engine) EvaluateRuleDryRun(ctx context.Context, rule store.AlertRule) (*EvalResult, error) {
	cfg, err := ParseConditionConfig(rule.ConditionConfig)
	if err != nil {
		return nil, fmt.Errorf("parse condition_config: %w", err)
	}
	value, err := e.queryMetricWithCollector(ctx, rule.OrgID, cfg.Metric, cfg.WindowMinutes)
	if err != nil {
		return nil, fmt.Errorf("query metric: %w", err)
	}
	fires := conditionMet(cfg, value)
	msg := ""
	if fires {
		msg = fmt.Sprintf("%s = %.2f exceeds threshold %.2f (window %dm)",
			cfg.Metric, value, cfg.Threshold, cfg.WindowMinutes)
	}
	return &EvalResult{
		RuleName:    rule.Name,
		Metric:      cfg.Metric,
		MetricValue: value,
		Threshold:   cfg.Threshold,
		WouldFire:   fires,
		Message:     msg,
	}, nil
}

// queryMetricWithCollector dispatches metrics that require the collector HTTP
// API separately from those backed by Postgres.
func (e *Engine) queryMetricWithCollector(ctx context.Context, orgID uuid.UUID, metric string, windowMinutes int) (float64, error) {
	if metric == "failed_delivery_count" {
		if e.collector == nil {
			slog.Warn("failed_delivery_count: collector URL not configured; returning 0",
				"org_id", orgID)
			return 0, nil
		}
		since := time.Now().UTC().Add(-time.Duration(windowMinutes) * time.Minute)
		n, err := e.collector.deadLetterCount(ctx, since)
		return float64(n), err
	}
	return QueryMetric(ctx, e.queries, orgID, metric, windowMinutes)
}

// QueryMetric runs the appropriate SQL query for a metric and window.
// Exported so API test handlers can call it directly.
// Note: failed_delivery_count is NOT handled here — use Engine.queryMetricWithCollector.
func QueryMetric(ctx context.Context, q *store.Queries, orgID uuid.UUID, metric string, windowMinutes int) (float64, error) {
	window := fmt.Sprintf("%d minutes", windowMinutes)
	db := q.DB()
	pgOrgID := pgtype.UUID{Bytes: orgID, Valid: true}

	switch metric {
	case "denied_actions_count":
		const sql = `SELECT COUNT(*) FROM audit_log WHERE org_id=$1 AND decision='denied' AND timestamp > NOW()-$2::interval`
		row := db.QueryRow(ctx, sql, pgOrgID, window)
		var n int64
		return float64(n), row.Scan(&n)
	case "escalated_actions_count":
		const sql = `SELECT COUNT(*) FROM audit_log WHERE org_id=$1 AND decision='escalated' AND timestamp > NOW()-$2::interval`
		row := db.QueryRow(ctx, sql, pgOrgID, window)
		var n int64
		return float64(n), row.Scan(&n)
	case "scope_drift_count":
		// Count audit_log rows where the policy engine escalated (scope drift detected).
		// v1: all escalated decisions are treated as scope drift.
		n, err := q.CountScopeDriftEvents(ctx, orgID, windowMinutes)
		return float64(n), err
	case "new_endpoints_count":
		const sql = `SELECT COUNT(*) FROM endpoints WHERE org_id=$1 AND first_seen > NOW()-$2::interval`
		row := db.QueryRow(ctx, sql, pgOrgID, window)
		var n int64
		return float64(n), row.Scan(&n)
	case "token_spend_usd":
		const sql = `SELECT COALESCE(SUM(cost_usd),0) FROM token_usage WHERE org_id=$1 AND recorded_at > NOW()-$2::interval`
		row := db.QueryRow(ctx, sql, pgOrgID, window)
		var total float64
		return total, row.Scan(&total)
	case "failed_delivery_count":
		// This metric lives in the collector's SQLite dead_letter table, not Postgres.
		// Callers must go through Engine.queryMetricWithCollector instead.
		return 0, fmt.Errorf("failed_delivery_count requires collector HTTP context; use Engine methods")
	default:
		return 0, fmt.Errorf("unsupported metric: %s", metric)
	}
}

// ParseConditionConfig parses the JSONB condition_config field.
func ParseConditionConfig(raw []byte) (ConditionConfig, error) {
	var cfg ConditionConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Metric == "" || cfg.Condition == "" || cfg.WindowMinutes <= 0 {
		return cfg, fmt.Errorf("metric, condition, and window_minutes > 0 are required")
	}
	return cfg, nil
}

// BuildConditionConfig marshals a ConditionConfig to JSONB bytes.
func BuildConditionConfig(cfg ConditionConfig) ([]byte, error) {
	return json.Marshal(cfg)
}

// BuildConditionText returns a human-readable condition string.
func BuildConditionText(cfg ConditionConfig) string {
	return fmt.Sprintf("%s > %.0f in %dm", cfg.Metric, cfg.Threshold, cfg.WindowMinutes)
}

func conditionMet(cfg ConditionConfig, value float64) bool {
	switch cfg.Condition {
	case "gt":
		return value > cfg.Threshold
	}
	return false
}
