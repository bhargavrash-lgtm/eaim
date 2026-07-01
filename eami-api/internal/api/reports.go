package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/store"
)

// sensitiveHeaders lists header names that must never be stored, to prevent
// credential leakage in the discovered_endpoints.raw_headers column.
var sensitiveHeaders = map[string]bool{
	"authorization": true,
	"x-api-key":     true,
	"x-service-key": true,
	"cookie":        true,
	"set-cookie":    true,
}

// ReportEvent is a single observed HTTP endpoint call sent by a collector agent.
type ReportEvent struct {
	OrgID      string            `json:"org_id"`
	SourceHost string            `json:"source_host"`
	Method     string            `json:"method"`
	Host       string            `json:"host"`
	Path       string            `json:"path"`
	Port       *int32            `json:"port,omitempty"`
	TLS        bool              `json:"tls"`
	Tags       []string          `json:"tags,omitempty"`
	RawHeaders map[string]string `json:"raw_headers,omitempty"`
}

// IngestReports handles POST /v1/reports.
// Authentication: X-Service-Key header (requireServiceKey middleware).
// Body: JSON array of ReportEvent objects.
// On success returns 202 Accepted with a count of accepted events.
func (s *Server) IngestReports(w http.ResponseWriter, r *http.Request) {
	var events []ReportEvent
	if err := decodeJSON(r, &events); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	if len(events) == 0 {
		writeJSON(w, http.StatusAccepted, map[string]int{"accepted": 0})
		return
	}

	ctx := r.Context()
	accepted := 0

	for i := range events {
		ev := &events[i]

		// Validate required fields.
		orgID, err := uuid.Parse(ev.OrgID)
		if err != nil {
			// Skip events with invalid org_id rather than failing the whole batch.
			continue
		}
		if ev.SourceHost == "" || ev.Method == "" || ev.Host == "" || ev.Path == "" {
			continue
		}

		// Scrub sensitive headers before storage.
		clean := make(map[string]string, len(ev.RawHeaders))
		for k, v := range ev.RawHeaders {
			if !sensitiveHeaders[strings.ToLower(k)] {
				clean[k] = v
			}
		}
		rawHeaders, err := json.Marshal(clean)
		if err != nil {
			rawHeaders = []byte("{}")
		}

		// Build port param.
		var port pgtype.Int4
		if ev.Port != nil {
			port = pgtype.Int4{Int32: *ev.Port, Valid: true}
		}

		tags := ev.Tags
		if tags == nil {
			tags = []string{}
		}

		p := store.UpsertEndpointParams{
			OrgID:      orgID,
			SourceHost: ev.SourceHost,
			Method:     strings.ToUpper(ev.Method),
			Host:       ev.Host,
			Path:       ev.Path,
			Port:       port,
			TLS:        ev.TLS,
			Tags:       tags,
			RawHeaders: rawHeaders,
		}
		if err := s.queries.UpsertEndpoint(ctx, p); err != nil {
			// Log and continue -- best-effort ingestion.
			continue
		}
		accepted++
	}

	writeJSON(w, http.StatusAccepted, map[string]int{"accepted": accepted})
}

// ListEndpoints handles GET /v1/discover/endpoints.
// Query params: host, source_host, tag, page (1-based), per_page.
func (s *Server) ListEndpoints(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	q := r.URL.Query()

	page, perPage := parsePage(q.Get("page"), q.Get("per_page"))

	hostFilter := optionalStr(q.Get("host"))
	sourceFilter := optionalStr(q.Get("source_host"))
	tagFilter := optionalStr(q.Get("tag"))

	ctx := r.Context()
	p := store.ListEndpointsParams{
		OrgID:      uc.OrgID,
		Host:       hostFilter,
		SourceHost: sourceFilter,
		Tag:        tagFilter,
		Limit:      int32(perPage),
		Offset:     int32((page - 1) * perPage),
	}

	endpoints, err := s.queries.ListEndpoints(ctx, p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list endpoints")
		return
	}

	total, err := s.queries.CountEndpoints(ctx, p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to count endpoints")
		return
	}

	data := make([]endpointResp, len(endpoints))
	for i, e := range endpoints {
		data[i] = toEndpointResp(e)
	}

	writeJSON(w, http.StatusOK, struct {
		Data []endpointResp `json:"data"`
		Meta PaginationMeta `json:"meta"`
	}{
		Data: data,
		Meta: PaginationMeta{Total: total, Page: page, PerPage: perPage},
	})
}

// GetEndpoint handles GET /v1/discover/endpoints/{endpointId}.
func (s *Server) GetEndpoint(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)

	idStr := chi.URLParam(r, "endpointId")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid endpoint ID")
		return
	}

	e, err := s.queries.GetEndpoint(r.Context(), id, uc.OrgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found")
		return
	}

	writeJSON(w, http.StatusOK, toEndpointResp(*e))
}

// -- Response type ------------------------------------------------------------

type endpointResp struct {
	ID         string            `json:"id"`
	OrgID      string            `json:"org_id"`
	SourceHost string            `json:"source_host"`
	Method     string            `json:"method"`
	Host       string            `json:"host"`
	Path       string            `json:"path"`
	Port       *int32            `json:"port,omitempty"`
	TLS        bool              `json:"tls"`
	Tags       []string          `json:"tags"`
	RawHeaders map[string]string `json:"raw_headers"`
	HitCount   int32             `json:"hit_count"`
	LastSeen   string            `json:"last_seen"`
	CreatedAt  string            `json:"created_at"`
}

func toEndpointResp(e store.DiscoveredEndpoint) endpointResp {
	resp := endpointResp{
		ID:         e.ID.String(),
		OrgID:      e.OrgID.String(),
		SourceHost: e.SourceHost,
		Method:     e.Method,
		Host:       e.Host,
		Path:       e.Path,
		TLS:        e.TLS,
		Tags:       e.Tags,
		RawHeaders: e.RawHeadersMap(),
		HitCount:   e.HitCount,
	}
	if e.Port.Valid {
		v := e.Port.Int32
		resp.Port = &v
	}
	if e.LastSeen.Valid {
		resp.LastSeen = e.LastSeen.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	if e.CreatedAt.Valid {
		resp.CreatedAt = e.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	if resp.Tags == nil {
		resp.Tags = []string{}
	}
	return resp
}

// -- Helpers ------------------------------------------------------------------

// optionalStr converts an empty string to nil, otherwise returns a pointer.
func optionalStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// parsePage parses page/per_page query params with safe defaults.
func parsePage(pageStr, perPageStr string) (page, perPage int) {
	page = 1
	perPage = 25
	if pageStr != "" {
		var p int
		if _, err := parseIntParam(pageStr, &p); err == nil && p > 0 {
			page = p
		}
	}
	if perPageStr != "" {
		var pp int
		if _, err := parseIntParam(perPageStr, &pp); err == nil && pp > 0 && pp <= 200 {
			perPage = pp
		}
	}
	return
}

func parseIntParam(s string, out *int) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, &parseIntError{}
		}
		n = n*10 + int(c-'0')
	}
	if out != nil {
		*out = n
	}
	return n, nil
}

type parseIntError struct{}

func (e *parseIntError) Error() string { return "not an integer" }

// ── Token usage ingestion ─────────────────────────────────────────────────────

// TokenUsageRequest is the body posted by the gateway after each proxied call.
type TokenUsageRequest struct {
	OrgID        string `json:"org_id"`
	AgentID      string `json:"agent_id"`
	AgentName    string `json:"agent_name"`
	Model        string `json:"model"`
	InputTokens  int32  `json:"input_tokens"`
	OutputTokens int32  `json:"output_tokens"`
	RecordedAt   string `json:"recorded_at"` // RFC3339
}

// IngestTokenUsage handles POST /v1/internal/token-usage.
// Auth: X-Service-Key header (requireServiceKey middleware).
// Inserts one row into token_usage; looks up model price for cost computation.
// Returns 202 on success; cost defaults to 0.00 when model is not in model_pricing.
func (s *Server) IngestTokenUsage(w http.ResponseWriter, r *http.Request) {
	var req TokenUsageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}

	// Validate org_id.
	orgID, err := uuid.Parse(req.OrgID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid org_id: must be a UUID")
		return
	}

	// Validate agent_id (zero UUID allowed when agent is unknown).
	agentID, err := uuid.Parse(req.AgentID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid agent_id: must be a UUID")
		return
	}

	// Validate recorded_at.
	recordedAt, err := time.Parse(time.RFC3339, req.RecordedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid recorded_at: must be RFC3339")
		return
	}

	ctx := r.Context()

	// Look up model pricing — zero cost when model is unknown (not an error).
	mp, err := s.queries.GetModelPricing(ctx, req.Model)
	if err != nil {
		// pgx.ErrNoRows or any scan error → treat as model-not-found, cost = 0.
		mp.CostPer1kIn = 0
		mp.CostPer1kOut = 0
	}
	cost := (float64(req.InputTokens)/1000.0)*mp.CostPer1kIn +
		(float64(req.OutputTokens)/1000.0)*mp.CostPer1kOut

	p := store.InsertTokenUsageParams{
		OrgID:      orgID,
		AgentID:    agentID,
		AgentName:  req.AgentName,
		Model:      req.Model,
		TokensIn:   req.InputTokens,
		TokensOut:  req.OutputTokens,
		CostUSD:    cost,
		RecordedAt: recordedAt,
	}
	if err := s.queries.InsertTokenUsage(ctx, p); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to record token usage")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}
