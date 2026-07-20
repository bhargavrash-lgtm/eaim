package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

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

	return item
}
