package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/eami/api/internal/store"
)

// ── Agent endpoint list ───────────────────────────────────────────────────────

// agentEndpointListItem is the per-row shape returned by GET /v1/endpoints.
type agentEndpointListItem struct {
	ID             string  `json:"id"`
	OrgID          string  `json:"org_id"`
	AgentID        string  `json:"agent_id"`
	Hostname       string  `json:"hostname"`
	OS             string  `json:"os"`
	AgentVersion   string  `json:"agent_version"`
	LastSeen       string  `json:"last_seen"`
	FirstSeen      string  `json:"first_seen"`
	RiskScore      float64 `json:"risk_score"`
	AIAppCount     int64   `json:"ai_app_count"`
	LocalModelCount int64  `json:"local_model_count"`
	MCPServerCount int64   `json:"mcp_server_count"`
	GPUCount       int64   `json:"gpu_count"`
	// GatewayAgentID/GatewayAgentName (B-164/B-165): nil unless an admin has
	// explicitly linked this endpoint to a real gateway_agents row via
	// PATCH /v1/endpoints/{endpointId}/link-agent -- there is no automatic
	// derivation.
	GatewayAgentID   *string `json:"gateway_agent_id,omitempty"`
	GatewayAgentName *string `json:"gateway_agent_name,omitempty"`
}

// agentEndpointDetail is the shape returned by GET /v1/endpoints/{endpointId}.
// LatestReport is the raw JSONB blob from endpoint_reports, forwarded as-is so
// the UI can render any field the agent sent without the API defining each one.
type agentEndpointDetail struct {
	agentEndpointListItem
	LatestReport json.RawMessage `json:"latest_report"`
}

// ListAgentEndpoints handles GET /v1/endpoints.
// Auth: JWT (viewer or above).
// Returns paginated agent machine inventory for the requesting user's org.
func (s *Server) ListAgentEndpoints(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	q := r.URL.Query()
	page, perPage := parsePage(q.Get("page"), q.Get("per_page"))

	ctx := r.Context()
	p := store.ListAgentEndpointsParams{
		OrgID:  uc.OrgID,
		Limit:  int32(perPage),
		Offset: int32((page - 1) * perPage),
	}

	endpoints, err := s.queries.ListAgentEndpoints(ctx, p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list endpoints")
		return
	}

	total, err := s.queries.CountAgentEndpoints(ctx, uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to count endpoints")
		return
	}

	data := make([]agentEndpointListItem, len(endpoints))
	for i, e := range endpoints {
		data[i] = toAgentEndpointItem(e)
	}

	writeJSON(w, http.StatusOK, struct {
		Data []agentEndpointListItem `json:"data"`
		Meta PaginationMeta          `json:"meta"`
	}{
		Data: data,
		Meta: PaginationMeta{Total: total, Page: page, PerPage: perPage},
	})
}

// GetAgentEndpoint handles GET /v1/endpoints/{endpointId}.
// Auth: JWT (viewer or above).
// Returns the endpoint row plus its latest full report blob.
func (s *Server) GetAgentEndpoint(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)

	idStr := chi.URLParam(r, "endpointId")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid endpoint ID")
		return
	}

	e, err := s.queries.GetAgentEndpointWithReport(r.Context(), id, uc.OrgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found")
		return
	}

	resp := agentEndpointDetail{
		agentEndpointListItem: toAgentEndpointItem(e.AgentEndpoint),
		LatestReport:          e.LatestReport,
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func toAgentEndpointItem(e store.AgentEndpoint) agentEndpointListItem {
	item := agentEndpointListItem{
		ID:              e.ID.String(),
		OrgID:           e.OrgID.String(),
		AgentID:         e.AgentID,
		Hostname:        e.Hostname,
		AgentVersion:    e.AgentVersion,
		RiskScore:       e.RiskScore,
		AIAppCount:      e.AIAppCount,
		LocalModelCount: e.ModelCount,
		MCPServerCount:  e.MCPCount,
		GPUCount:        e.GPUCount,
	}

	// Decode OS from the os_info JSONB column.
	if len(e.OSInfo) > 0 {
		var osInfo struct {
			OS string `json:"os"`
		}
		if err := json.Unmarshal(e.OSInfo, &osInfo); err == nil {
			item.OS = osInfo.OS
		}
	}

	if e.LastSeen.Valid {
		item.LastSeen = e.LastSeen.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	if e.FirstSeen.Valid {
		item.FirstSeen = e.FirstSeen.Time.UTC().Format("2006-01-02T15:04:05Z")
	}

	if e.GatewayAgentID != nil {
		id := e.GatewayAgentID.String()
		item.GatewayAgentID = &id
	}
	item.GatewayAgentName = e.GatewayAgentName

	return item
}

// ── Endpoint ↔ gateway agent link (B-164/B-165) ─────────────────────────────

// linkEndpointAgentRequest is the PATCH body for LinkEndpointAgent.
// GatewayAgentID nil or "" clears the link; a real gateway_agents UUID
// (string form) sets it.
type linkEndpointAgentRequest struct {
	GatewayAgentID *string `json:"gateway_agent_id"`
}

// LinkEndpointAgent handles PATCH /v1/endpoints/{endpointId}/link-agent.
// Auth: JWT (admin or operator -- same write-access gating this file's
// sibling gateway/* write routes use).
//
// This is the ONLY write path for endpoints.gateway_agent_id -- there is no
// automatic derivation between eami-agent's own free-text discovery
// identity and a governed gateway_agents identity (see
// 000013_endpoint_gateway_agent_link.up.sql's own doc comment for why), so
// linking is always an explicit admin decision, never inferred.
func (s *Server) LinkEndpointAgent(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	endpointID, err := parseUUIDParam(r, "endpointId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid endpointId")
		return
	}

	var req linkEndpointAgentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	var gatewayAgentID *uuid.UUID
	if req.GatewayAgentID != nil && *req.GatewayAgentID != "" {
		id, err := uuid.Parse(*req.GatewayAgentID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "gateway_agent_id must be a valid UUID or null")
			return
		}
		gatewayAgentID = &id
	}

	err = s.queries.LinkEndpointToGatewayAgent(r.Context(), store.LinkEndpointToGatewayAgentParams{
		EndpointID:     endpointID,
		OrgID:          uc.OrgID,
		GatewayAgentID: gatewayAgentID,
	})
	switch {
	case err == nil:
		// fall through to read-back below
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found")
		return
	case errors.Is(err, store.ErrGatewayAgentNotFound):
		writeError(w, http.StatusBadRequest, "bad_request", "gateway agent not found in this org")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update endpoint link")
		return
	}

	e, err := s.queries.GetAgentEndpoint(r.Context(), endpointID, uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to read back updated endpoint")
		return
	}
	writeJSON(w, http.StatusOK, toAgentEndpointItem(*e))
}
