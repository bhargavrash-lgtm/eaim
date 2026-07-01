package api

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"github.com/eami/api/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ── Org settings ──────────────────────────────────────────────────────────────

type OrgSettingsResp struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Slug            string    `json:"slug"`
	Plan            string    `json:"plan"`
	Timezone        string    `json:"timezone"`
	DefaultRiskTier string    `json:"default_risk_tier"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type UpdateOrgSettingsRequest struct {
	Name            *string `json:"name"`
	Timezone        *string `json:"timezone"`
	DefaultRiskTier *string `json:"default_risk_tier"`
}

func (s *Server) GetOrgSettings(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	org, err := s.queries.GetOrgSettings(r.Context(), uc.OrgID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "org not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, orgSettingsToResp(*org))
}

func (s *Server) UpdateOrgSettings(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	var req UpdateOrgSettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.DefaultRiskTier != nil {
		switch *req.DefaultRiskTier {
		case "low", "medium", "high":
		default:
			writeError(w, http.StatusBadRequest, "bad_request", "default_risk_tier must be low|medium|high")
			return
		}
	}
	org, err := s.queries.UpdateOrgSettings(r.Context(), store.UpdateOrgSettingsParams{
		OrgID:           uc.OrgID,
		Name:            toPgtypeTextStr(req.Name),
		Timezone:        toPgtypeTextStr(req.Timezone),
		DefaultRiskTier: toPgtypeTextStr(req.DefaultRiskTier),
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "org not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, orgSettingsToResp(*org))
}

func orgSettingsToResp(o store.OrgSettings) OrgSettingsResp {
	return OrgSettingsResp{
		ID:              o.ID.String(),
		Name:            o.Name,
		Slug:            o.Slug,
		Plan:            o.Plan,
		Timezone:        o.Timezone,
		DefaultRiskTier: o.DefaultRiskTier,
		UpdatedAt:       o.UpdatedAt,
	}
}

// ── Notification config ───────────────────────────────────────────────────────

type NotificationConfigResp struct {
	SlackEnabled    bool      `json:"slack_enabled"`
	SlackWebhookURL *string   `json:"slack_webhook_url,omitempty"` // masked
	EmailEnabled    bool      `json:"email_enabled"`
	EmailSMTPHost   *string   `json:"email_smtp_host,omitempty"`
	EmailSMTPPort   int32     `json:"email_smtp_port"`
	EmailFrom       *string   `json:"email_from,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type UpdateNotificationConfigRequest struct {
	SlackEnabled    *bool   `json:"slack_enabled"`
	SlackWebhookURL *string `json:"slack_webhook_url"`
	EmailEnabled    *bool   `json:"email_enabled"`
	EmailSMTPHost   *string `json:"email_smtp_host"`
	EmailSMTPPort   *int32  `json:"email_smtp_port"`
	EmailFrom       *string `json:"email_from"`
}

type TestNotificationRequest struct {
	Channel   string `json:"channel"`
	Recipient string `json:"recipient"`
}

type TestNotificationResp struct {
	Sent   bool   `json:"sent"`
	Reason string `json:"reason,omitempty"`
}

func (s *Server) GetNotificationConfig(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	cfg, err := s.queries.GetNotificationConfig(r.Context(), uc.OrgID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeJSON(w, http.StatusOK, NotificationConfigResp{EmailSMTPPort: 587})
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, notificationConfigToResp(*cfg))
}

func (s *Server) UpdateNotificationConfig(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)

	current, err := s.queries.GetNotificationConfig(r.Context(), uc.OrgID)
	if err != nil && err != pgx.ErrNoRows {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	var req UpdateNotificationConfigRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	p := store.UpsertNotificationConfigParams{OrgID: uc.OrgID, EmailSMTPPort: 587}
	if current != nil {
		p.SlackEnabled = current.SlackEnabled
		p.SlackWebhookURL = current.SlackWebhookURL
		p.EmailEnabled = current.EmailEnabled
		p.EmailSMTPHost = current.EmailSMTPHost
		p.EmailSMTPPort = current.EmailSMTPPort
		p.EmailFrom = current.EmailFrom
	}
	if req.SlackEnabled != nil {
		p.SlackEnabled = *req.SlackEnabled
	}
	if req.SlackWebhookURL != nil {
		p.SlackWebhookURL = pgtype.Text{String: *req.SlackWebhookURL, Valid: true}
	}
	if req.EmailEnabled != nil {
		p.EmailEnabled = *req.EmailEnabled
	}
	if req.EmailSMTPHost != nil {
		p.EmailSMTPHost = pgtype.Text{String: *req.EmailSMTPHost, Valid: true}
	}
	if req.EmailSMTPPort != nil {
		p.EmailSMTPPort = *req.EmailSMTPPort
	}
	if req.EmailFrom != nil {
		p.EmailFrom = pgtype.Text{String: *req.EmailFrom, Valid: true}
	}

	cfg, err := s.queries.UpsertNotificationConfig(r.Context(), p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, notificationConfigToResp(*cfg))
}

func (s *Server) TestNotificationChannel(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	var req TestNotificationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	cfg, err := s.queries.GetNotificationConfig(r.Context(), uc.OrgID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeJSON(w, http.StatusOK, TestNotificationResp{Sent: false, Reason: "not_configured"})
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	switch req.Channel {
	case "slack":
		if !cfg.SlackEnabled || !cfg.SlackWebhookURL.Valid || cfg.SlackWebhookURL.String == "" {
			writeJSON(w, http.StatusOK, TestNotificationResp{Sent: false, Reason: "slack_not_configured"})
			return
		}
		payload := `{"text":"EAMI test notification \u2014 your Slack integration is working."}`
		resp, err := http.Post(cfg.SlackWebhookURL.String, "application/json", bytes.NewBufferString(payload))
		if err != nil {
			writeJSON(w, http.StatusOK, TestNotificationResp{Sent: false, Reason: err.Error()})
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			writeJSON(w, http.StatusOK, TestNotificationResp{Sent: false, Reason: "slack_webhook_non_200"})
			return
		}
		writeJSON(w, http.StatusOK, TestNotificationResp{Sent: true})
	case "email":
		writeJSON(w, http.StatusOK, TestNotificationResp{Sent: false, Reason: "smtp_not_configured"})
	default:
		writeError(w, http.StatusBadRequest, "bad_request", `channel must be "slack" or "email"`)
	}
}

// notificationConfigToResp masks the webhook URL (last 6 chars only).
func notificationConfigToResp(cfg store.NotificationConfig) NotificationConfigResp {
	resp := NotificationConfigResp{
		SlackEnabled:  cfg.SlackEnabled,
		EmailEnabled:  cfg.EmailEnabled,
		EmailSMTPPort: cfg.EmailSMTPPort,
		UpdatedAt:     cfg.UpdatedAt,
	}
	if cfg.SlackWebhookURL.Valid && cfg.SlackWebhookURL.String != "" {
		masked := maskSecret(cfg.SlackWebhookURL.String)
		resp.SlackWebhookURL = &masked
	}
	if cfg.EmailSMTPHost.Valid {
		resp.EmailSMTPHost = &cfg.EmailSMTPHost.String
	}
	if cfg.EmailFrom.Valid {
		resp.EmailFrom = &cfg.EmailFrom.String
	}
	return resp
}

// maskSecret returns the string with all but the last 6 characters replaced by '*'.
func maskSecret(s string) string {
	if len(s) <= 6 {
		return strings.Repeat("*", len(s))
	}
	return strings.Repeat("*", len(s)-6) + s[len(s)-6:]
}
