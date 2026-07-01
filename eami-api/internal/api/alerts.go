package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/alerting"
	"github.com/eami/api/internal/store"
)

// ── Request / response types ──────────────────────────────────────────────────

type AlertRuleResp struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   *string   `json:"description,omitempty"`
	Metric        string    `json:"metric"`
	Condition     string    `json:"condition"`
	Threshold     float64   `json:"threshold"`
	WindowMinutes int       `json:"window_minutes"`
	Severity      string    `json:"severity"`
	Channels      []string  `json:"channels"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
}

type AlertRuleListResp struct {
	Data []AlertRuleResp `json:"data"`
}

type AlertRuleCreateRequest struct {
	Name          string   `json:"name"`
	Description   *string  `json:"description"`
	Metric        string   `json:"metric"`
	Condition     string   `json:"condition"`
	Threshold     float64  `json:"threshold"`
	WindowMinutes int      `json:"window_minutes"`
	Severity      string   `json:"severity"`
	Channels      []string `json:"channels"`
	Enabled       *bool    `json:"enabled"`
}

type AlertRuleUpdateRequest struct {
	Name          *string  `json:"name"`
	Description   *string  `json:"description"`
	Metric        *string  `json:"metric"`
	Condition     *string  `json:"condition"`
	Threshold     *float64 `json:"threshold"`
	WindowMinutes *int     `json:"window_minutes"`
	Severity      *string  `json:"severity"`
	Channels      []string `json:"channels"`
	Enabled       *bool    `json:"enabled"`
}

type TestAlertRuleResp struct {
	RuleName    string  `json:"rule_name"`
	Metric      string  `json:"metric"`
	MetricValue float64 `json:"metric_value"`
	Threshold   float64 `json:"threshold"`
	WouldFire   bool    `json:"would_fire"`
	Message     string  `json:"message,omitempty"`
}

type AlertResp struct {
	ID             string     `json:"id"`
	RuleID         string     `json:"rule_id"`
	RuleName       string     `json:"rule_name"`
	Severity       string     `json:"severity"`
	Message        string     `json:"message"`
	MetricValue    *float64   `json:"metric_value,omitempty"`
	FiredAt        time.Time  `json:"fired_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	Status         string     `json:"status"`
	AcknowledgedBy *string    `json:"acknowledged_by,omitempty"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
}

type AlertListResp struct {
	Data []AlertResp    `json:"data"`
	Meta PaginationMeta `json:"meta"`
}

type AcknowledgeAlertRequest struct {
	AcknowledgedBy string `json:"acknowledged_by"`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (s *Server) ListAlertRules(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	rules, err := s.queries.ListAlertRules(r.Context(), uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	data := make([]AlertRuleResp, 0, len(rules))
	for _, rule := range rules {
		resp, err := alertRuleToResp(rule)
		if err == nil {
			data = append(data, resp)
		}
	}
	writeJSON(w, http.StatusOK, AlertRuleListResp{Data: data})
}

func (s *Server) CreateAlertRule(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	var req AlertRuleCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if err := validateAlertRuleReq(req.Name, req.Metric, req.Condition, req.WindowMinutes, req.Severity); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	cfg := alerting.ConditionConfig{
		Metric:        req.Metric,
		Condition:     req.Condition,
		Threshold:     req.Threshold,
		WindowMinutes: req.WindowMinutes,
	}
	cfgBytes, _ := alerting.BuildConditionConfig(cfg)
	condText := alerting.BuildConditionText(cfg)

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	channels := req.Channels
	if channels == nil {
		channels = []string{}
	}

	rule, err := s.queries.CreateAlertRule(r.Context(), store.CreateAlertRuleParams{
		OrgID:           uc.OrgID,
		Name:            req.Name,
		Description:     toPgtypeTextStr(req.Description),
		Condition:       condText,
		ConditionConfig: cfgBytes,
		Severity:        req.Severity,
		Channels:        channels,
		Enabled:         enabled,
		CreatedBy:       pgtype.UUID{Bytes: uc.UserID, Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	resp, _ := alertRuleToResp(*rule)
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) UpdateAlertRule(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := uuid.Parse(chi.URLParam(r, "ruleId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid ruleId")
		return
	}
	var req AlertRuleUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	// Fetch current rule to merge condition_config fields.
	current, err := s.queries.GetAlertRule(r.Context(), id, uc.OrgID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "rule not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	// Merge condition config.
	currentCfg, _ := alerting.ParseConditionConfig(current.ConditionConfig)
	if req.Metric != nil {
		currentCfg.Metric = *req.Metric
	}
	if req.Condition != nil {
		currentCfg.Condition = *req.Condition
	}
	if req.Threshold != nil {
		currentCfg.Threshold = *req.Threshold
	}
	if req.WindowMinutes != nil {
		currentCfg.WindowMinutes = *req.WindowMinutes
	}
	cfgBytes, _ := alerting.BuildConditionConfig(currentCfg)
	condText := alerting.BuildConditionText(currentCfg)

	p := store.UpdateAlertRuleParams{
		ID:              id,
		OrgID:           uc.OrgID,
		Condition:       pgtype.Text{String: condText, Valid: true},
		ConditionConfig: cfgBytes,
		Enabled:         req.Enabled,
	}
	if req.Name != nil {
		p.Name = pgtype.Text{String: *req.Name, Valid: true}
	}
	if req.Description != nil {
		p.Description = pgtype.Text{String: *req.Description, Valid: true}
	}
	if req.Severity != nil {
		p.Severity = pgtype.Text{String: *req.Severity, Valid: true}
	}
	if req.Channels != nil {
		p.Channels = req.Channels
	}

	rule, err := s.queries.UpdateAlertRule(r.Context(), p)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "rule not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	resp, _ := alertRuleToResp(*rule)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) DeleteAlertRule(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := uuid.Parse(chi.URLParam(r, "ruleId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid ruleId")
		return
	}
	if err := s.queries.DeleteAlertRule(r.Context(), id, uc.OrgID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) TestAlertRule(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := uuid.Parse(chi.URLParam(r, "ruleId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid ruleId")
		return
	}
	rule, err := s.queries.GetAlertRule(r.Context(), id, uc.OrgID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "rule not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	result, err := s.alertEngine.EvaluateRuleDryRun(r.Context(), *rule)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, TestAlertRuleResp{
		RuleName:    result.RuleName,
		Metric:      result.Metric,
		MetricValue: result.MetricValue,
		Threshold:   result.Threshold,
		WouldFire:   result.WouldFire,
		Message:     result.Message,
	})
}

func (s *Server) ListAlerts(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	q := r.URL.Query()
	page, perPage := pagination(q.Get("page"), q.Get("per_page"), 25, 200)
	status := strPtrFromQuery(q.Get("status"))

	alerts, err := s.queries.ListAlerts(r.Context(), uc.OrgID, status, int32(perPage), int32((page-1)*perPage))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	total, err := s.queries.CountAlerts(r.Context(), uc.OrgID, status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	data := make([]AlertResp, 0, len(alerts))
	for _, a := range alerts {
		data = append(data, alertToResp(a))
	}
	writeJSON(w, http.StatusOK, AlertListResp{
		Data: data,
		Meta: PaginationMeta{Total: total, Page: page, PerPage: perPage},
	})
}

func (s *Server) AcknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := uuid.Parse(chi.URLParam(r, "alertId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid alertId")
		return
	}
	var req AcknowledgeAlertRequest
	_ = decodeJSON(r, &req)
	acknowledgedBy := pgtype.UUID{Bytes: uc.UserID, Valid: true}
	alert, err := s.queries.UpdateAlertStatus(r.Context(), id, uc.OrgID, "acknowledged", acknowledgedBy)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "alert not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, alertToResp(*alert))
}

func (s *Server) ResolveAlert(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := uuid.Parse(chi.URLParam(r, "alertId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid alertId")
		return
	}
	alert, err := s.queries.UpdateAlertStatus(r.Context(), id, uc.OrgID, "resolved", pgtype.UUID{})
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "alert not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, alertToResp(*alert))
}

// ── Converters ────────────────────────────────────────────────────────────────

func alertRuleToResp(rule store.AlertRule) (AlertRuleResp, error) {
	cfg, err := alerting.ParseConditionConfig(rule.ConditionConfig)
	if err != nil {
		return AlertRuleResp{}, err
	}
	resp := AlertRuleResp{
		ID:            rule.ID.String(),
		Name:          rule.Name,
		Metric:        cfg.Metric,
		Condition:     cfg.Condition,
		Threshold:     cfg.Threshold,
		WindowMinutes: cfg.WindowMinutes,
		Severity:      rule.Severity,
		Channels:      rule.Channels,
		Enabled:       rule.Enabled,
		CreatedAt:     rule.CreatedAt,
	}
	if rule.Description.Valid {
		resp.Description = &rule.Description.String
	}
	return resp, nil
}

func alertToResp(a store.Alert) AlertResp {
	resp := AlertResp{
		ID:       a.ID.String(),
		RuleID:   a.RuleID.String(),
		RuleName: a.RuleName,
		Severity: a.Severity,
		Message:  a.Message,
		FiredAt:  a.FiredAt,
		Status:   a.Status,
	}
	if a.MetricValue.Valid {
		v := a.MetricValue.Float64
		resp.MetricValue = &v
	}
	if a.ResolvedAt.Valid {
		resp.ResolvedAt = &a.ResolvedAt.Time
	}
	if a.AcknowledgedBy.Valid {
		s := uuid.UUID(a.AcknowledgedBy.Bytes).String()
		resp.AcknowledgedBy = &s
	}
	if a.AcknowledgedAt.Valid {
		resp.AcknowledgedAt = &a.AcknowledgedAt.Time
	}
	return resp
}

func validateAlertRuleReq(name, metric, condition string, windowMinutes int, severity string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	validMetrics := map[string]bool{
		"denied_actions_count": true, "escalated_actions_count": true,
		"scope_drift_count": true, "new_endpoints_count": true,
		"token_spend_usd": true, "failed_delivery_count": true,
	}
	if !validMetrics[metric] {
		return fmt.Errorf("unknown metric: %s", metric)
	}
	if condition != "gt" {
		return fmt.Errorf("condition must be \"gt\"")
	}
	validWindows := map[int]bool{5: true, 15: true, 60: true, 1440: true}
	if !validWindows[windowMinutes] {
		return fmt.Errorf("window_minutes must be 5, 15, 60, or 1440")
	}
	validSeverities := map[string]bool{"info": true, "warning": true, "high": true, "critical": true}
	if !validSeverities[severity] {
		return fmt.Errorf("severity must be info|warning|high|critical")
	}
	return nil
}

