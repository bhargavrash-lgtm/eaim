package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/store"
)


// validRiskTiers is the exhaustive set of allowed risk_tier values.
var validRiskTiers = map[string]bool{
	"low": true, "medium": true, "high": true, "critical": true,
}

func isValidRiskTier(s string) bool { return validRiskTiers[s] }
// ListAgents handles GET /v1/gateway/agents
func (s *Server) ListAgents(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	q := r.URL.Query()
	var status, riskTier *string
	if v := q.Get("status"); v != "" {
		status = &v
	}
	if v := q.Get("risk_tier"); v != "" {
		riskTier = &v
	}

	// Production: use queries for filter pushdown; test: use storeIface.
	if s.queries != nil {
		agents, err := s.queries.ListAgents(r.Context(), uc.OrgID, status, riskTier)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		resp := make([]AgentResp, 0, len(agents))
		for _, a := range agents {
			resp = append(resp, agentToResp(a))
		}
		writeJSON(w, http.StatusOK, AgentListResponse{Data: resp})
		return
	}
	agents, err := s.storeIface.ListAgents(r.Context(), uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	resp := make([]AgentResp, 0, len(agents))
	for _, a := range agents {
		resp = append(resp, storeAgentToResp(a))
	}
	writeJSON(w, http.StatusOK, AgentListResponse{Data: resp})
}

// GetAgent handles GET /v1/gateway/agents/{agentId}
func (s *Server) GetAgent(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "agentId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid agentId")
		return
	}
	if s.queries != nil {
		a, err := s.queries.GetAgent(r.Context(), id, uc.OrgID)
		if err != nil {
			if err == pgx.ErrNoRows {
				writeError(w, http.StatusNotFound, "not_found", "agent not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, agentToResp(*a))
		return
	}
	sa, err := s.storeIface.GetAgent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	if sa.OrgID != uc.OrgID {
		writeError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	writeJSON(w, http.StatusOK, storeAgentToResp(sa))
}

// CreateAgent handles POST /v1/gateway/agents
func (s *Server) CreateAgent(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	var req AgentCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Name == "" || req.Model == "" || req.Owner == "" || req.Scope == "" || req.RiskTier == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name, model, owner, scope, risk_tier are required")
		return
	}
	if !isValidRiskTier(req.RiskTier) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid risk_tier, must be one of: low, medium, high, critical")
		return
	}
	ttl := int32(900)
	if req.TokenTTLSeconds != nil {
		ttl = *req.TokenTTLSeconds
	}
	if s.queries != nil {
		a, err := s.queries.CreateAgent(r.Context(), store.CreateAgentParams{
			OrgID:           uc.OrgID,
			Name:            req.Name,
			Model:           req.Model,
			Owner:           req.Owner,
			Scope:           req.Scope,
			RiskTier:        req.RiskTier,
			TokenTTLSeconds: ttl,
			CreatedBy:       pgtype.UUID{Bytes: uc.UserID, Valid: true},
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		// Seed default config row (no-op if trigger already created it).
		_ = s.queries.SeedAgentConfig(r.Context(), a.ID)
		writeJSON(w, http.StatusCreated, agentToResp(*a))
		return
	}
	sa, err := s.storeIface.CreateAgent(r.Context(), CreateAgentParams{
		OrgID:           uc.OrgID,
		Name:            req.Name,
		Model:           req.Model,
		Owner:           req.Owner,
		Scope:           req.Scope,
		RiskTier:        req.RiskTier,
		TokenTTLSeconds: ttl,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, storeAgentToResp(sa))
}

// UpdateAgent handles PATCH /v1/gateway/agents/{agentId}
func (s *Server) UpdateAgent(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "agentId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid agentId")
		return
	}
	var req AgentUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	p := store.UpdateAgentParams{
		ID:    id,
		OrgID: uc.OrgID,
	}
	if req.Scope != nil {
		p.Scope = pgtype.Text{String: *req.Scope, Valid: true}
	}
	if req.RiskTier != nil {
		if !isValidRiskTier(*req.RiskTier) {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid risk_tier, must be one of: low, medium, high, critical")
			return
		}
		p.RiskTier = pgtype.Text{String: *req.RiskTier, Valid: true}
	}
	if req.Status != nil {
		p.Status = pgtype.Text{String: *req.Status, Valid: true}
	}
	if req.TokenTTLSeconds != nil {
		p.TokenTTLSeconds = pgtype.Int4{Int32: int32(*req.TokenTTLSeconds), Valid: true}
	}

	a, err := s.queries.UpdateAgent(r.Context(), p)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agentToResp(*a))
}

// DeleteAgent handles DELETE /v1/gateway/agents/{agentId}
func (s *Server) DeleteAgent(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "agentId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid agentId")
		return
	}
	if s.queries != nil {
		if err := s.queries.DeleteAgent(r.Context(), id, uc.OrgID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Test path: validate existence + org before deleting.
	existing, err := s.storeIface.GetAgent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	if existing.OrgID != uc.OrgID {
		writeError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	if err := s.storeIface.DeleteAgent(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── converters ────────────────────────────────────────────────────────────────

func agentToResp(a store.GatewayAgent) AgentResp {
	resp := AgentResp{
		ID:              a.ID.String(),
		OrgID:           a.OrgID.String(),
		Name:            a.Name,
		Model:           a.Model,
		Owner:           a.Owner,
		Scope:           a.Scope,
		RiskTier:        a.RiskTier,
		Status:          a.Status,
		TokenTTLSeconds: a.TokenTTLSeconds,
		CreatedAt:       a.CreatedAt,
		UpdatedAt:       a.UpdatedAt,
	}
	if a.LastSeen.Valid {
		resp.LastSeen = &a.LastSeen.Time
	}
	return resp
}

// ── shared helpers ────────────────────────────────────────────────────────────

func parseUUIDParam(r *http.Request, param string) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, param))
}

// storeAgentToResp converts a StoreAgent (api mirror type) to AgentResp.
func storeAgentToResp(a StoreAgent) AgentResp {
	return AgentResp{
		ID:              a.ID.String(),
		OrgID:           a.OrgID.String(),
		Name:            a.Name,
		Model:           a.Model,
		Owner:           a.Owner,
		Scope:           a.Scope,
		RiskTier:        a.RiskTier,
		Status:          a.Status,
		TokenTTLSeconds: a.TokenTTLSeconds,
		CreatedAt:       a.CreatedAt,
		UpdatedAt:       a.UpdatedAt,
	}
}

// ── Agent config handlers ──────────────────────────────────────────────────────

// AgentConfigResp is the JSON representation of agent_configs.
type AgentConfigResp struct {
	AgentID             string   `json:"agent_id"`
	ScanIntervalSeconds int32    `json:"scan_interval_seconds"`
	ModelScanPaths      []string `json:"model_scan_paths"`
	MaxReportSizeBytes  int32    `json:"max_report_size_bytes"`
	EnabledScanners     []string `json:"enabled_scanners"`
	UpdatedAt           string   `json:"updated_at"`
}

// AgentConfigUpdateRequest is the PUT body for config updates.
type AgentConfigUpdateRequest struct {
	ScanIntervalSeconds *int32   `json:"scan_interval_seconds"`
	ModelScanPaths      []string `json:"model_scan_paths"`
	MaxReportSizeBytes  *int32   `json:"max_report_size_bytes"`
	EnabledScanners     []string `json:"enabled_scanners"`
}

// GetAgentConfig handles GET /v1/gateway/agents/{agentId}/config
// Auth: viewer+ JWT or X-Service-Key (handled by route middleware).
func (s *Server) GetAgentConfig(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	agentID, err := parseUUIDParam(r, "agentId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid agentId")
		return
	}
	// Verify the agent belongs to this org.
	if s.queries != nil {
		if _, err := s.queries.GetAgent(r.Context(), agentID, uc.OrgID); err != nil {
			writeError(w, http.StatusNotFound, "not_found", "agent not found")
			return
		}
		cfg, err := s.queries.GetAgentConfig(r.Context(), agentID)
		if err != nil {
			// No config row yet — return defaults.
			d := store.AgentConfigDefaults
			d.AgentID = agentID
			writeJSON(w, http.StatusOK, agentConfigToResp(d))
			return
		}
		writeJSON(w, http.StatusOK, agentConfigToResp(*cfg))
		return
	}
	// Test path: storeIface org check.
	sa, err := s.storeIface.GetAgent(r.Context(), agentID)
	if err != nil || sa.OrgID != uc.OrgID {
		writeError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	d := store.AgentConfigDefaults
	d.AgentID = agentID
	writeJSON(w, http.StatusOK, agentConfigToResp(d))
}

// UpdateAgentConfig handles PUT /v1/gateway/agents/{agentId}/config
// Auth: operator+ JWT.
func (s *Server) UpdateAgentConfig(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	agentID, err := parseUUIDParam(r, "agentId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid agentId")
		return
	}
	var req AgentConfigUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	// Validate fields.
	if req.ScanIntervalSeconds != nil && (*req.ScanIntervalSeconds < 60 || *req.ScanIntervalSeconds > 86400) {
		writeError(w, http.StatusBadRequest, "bad_request", "scan_interval_seconds must be 60–86400")
		return
	}
	if req.MaxReportSizeBytes != nil {
		mb := *req.MaxReportSizeBytes
		if mb < 1048576 || mb > 52428800 {
			writeError(w, http.StatusBadRequest, "bad_request", "max_report_size_bytes must be 1MB–50MB")
			return
		}
	}
	if len(req.ModelScanPaths) == 0 && req.ModelScanPaths != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "model_scan_paths must have at least 1 entry")
		return
	}
	if len(req.EnabledScanners) == 0 && req.EnabledScanners != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "enabled_scanners must have at least 1 entry")
		return
	}

	if s.queries != nil {
		// Fetch current config to merge partial updates.
		existing, err := s.queries.GetAgentConfig(r.Context(), agentID)
		if err != nil {
			// Verify agent ownership; seed defaults on miss.
			if _, err2 := s.queries.GetAgent(r.Context(), agentID, uc.OrgID); err2 != nil {
				writeError(w, http.StatusNotFound, "not_found", "agent not found")
				return
			}
			_ = s.queries.SeedAgentConfig(r.Context(), agentID)
			d := store.AgentConfigDefaults
			existing = &d
			existing.AgentID = agentID
		}
		p := store.UpsertAgentConfigParams{
			AgentID:             agentID,
			ScanIntervalSeconds: existing.ScanIntervalSeconds,
			ModelScanPaths:      existing.ModelScanPaths,
			MaxReportSizeBytes:  existing.MaxReportSizeBytes,
			EnabledScanners:     existing.EnabledScanners,
		}
		if req.ScanIntervalSeconds != nil {
			p.ScanIntervalSeconds = *req.ScanIntervalSeconds
		}
		if req.ModelScanPaths != nil {
			p.ModelScanPaths = req.ModelScanPaths
		}
		if req.MaxReportSizeBytes != nil {
			p.MaxReportSizeBytes = *req.MaxReportSizeBytes
		}
		if req.EnabledScanners != nil {
			p.EnabledScanners = req.EnabledScanners
		}
		cfg, err := s.queries.UpsertAgentConfig(r.Context(), p)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, agentConfigToResp(*cfg))
		return
	}
	// Test path: return merged defaults.
	sa, err := s.storeIface.GetAgent(r.Context(), agentID)
	if err != nil || sa.OrgID != uc.OrgID {
		writeError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	d := store.AgentConfigDefaults
	d.AgentID = agentID
	if req.ScanIntervalSeconds != nil {
		d.ScanIntervalSeconds = *req.ScanIntervalSeconds
	}
	if req.ModelScanPaths != nil {
		d.ModelScanPaths = req.ModelScanPaths
	}
	if req.MaxReportSizeBytes != nil {
		d.MaxReportSizeBytes = *req.MaxReportSizeBytes
	}
	if req.EnabledScanners != nil {
		d.EnabledScanners = req.EnabledScanners
	}
	writeJSON(w, http.StatusOK, agentConfigToResp(d))
}

func agentConfigToResp(c store.AgentConfig) AgentConfigResp {
	return AgentConfigResp{
		AgentID:             c.AgentID.String(),
		ScanIntervalSeconds: c.ScanIntervalSeconds,
		ModelScanPaths:      c.ModelScanPaths,
		MaxReportSizeBytes:  c.MaxReportSizeBytes,
		EnabledScanners:     c.EnabledScanners,
		UpdatedAt:           c.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}
