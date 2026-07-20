package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/eami/api/internal/store"
)

// ListAudit handles GET /v1/audit
func (s *Server) ListAudit(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	q := r.URL.Query()

	// Pagination defaults.
	page := 1
	perPage := 50
	if v := q.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := q.Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			perPage = n
		}
	}

	// Optional filters.
	strPtr := func(key string) *string {
		if v := q.Get(key); v != "" {
			return &v
		}
		return nil
	}
	timePtr := func(key string) *time.Time {
		v := q.Get(key)
		if v == "" {
			return nil
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil
		}
		return &t
	}

	p := store.ListAuditParams{
		OrgID:     uc.OrgID,
		AgentName: strPtr("agent_name"),
		ToolName:  strPtr("tool_name"),
		Decision:  strPtr("decision"),
		From:      timePtr("from"),
		To:        timePtr("to"),
		Limit:     int32(perPage),
		Offset:    int32((page - 1) * perPage),
	}

	entries, err := s.queries.ListAudit(r.Context(), p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	total, err := s.queries.CountAudit(r.Context(), p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	data := make([]AuditEntryResp, 0, len(entries))
	for _, e := range entries {
		data = append(data, auditEntryToResp(e))
	}

	writeJSON(w, http.StatusOK, AuditListResponse{
		Data: data,
		Meta: PaginationMeta{
			Total:   total,
			Page:    page,
			PerPage: perPage,
		},
	})
}


// ExportAudit handles GET /v1/audit/export
// Returns a CSV file with up to 100,000 rows. If the result is truncated,
// the response includes X-Truncated: true.
func (s *Server) ExportAudit(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	q := r.URL.Query()

	strPtr := func(key string) *string {
		if v := q.Get(key); v != "" {
			return &v
		}
		return nil
	}
	timePtr := func(key string) *time.Time {
		v := q.Get(key)
		if v == "" {
			return nil
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil
		}
		return &t
	}

	p := store.ListAuditParams{
		OrgID:     uc.OrgID,
		AgentName: strPtr("agent_name"),
		ToolName:  strPtr("tool_name"),
		Decision:  strPtr("decision"),
		From:      timePtr("from"),
		To:        timePtr("to"),
	}

	rows, truncated, err := s.queries.ExportAudit(r.Context(), p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	fromStr := q.Get("from")
	toStr := q.Get("to")
	if fromStr == "" {
		fromStr = "all"
	}
	if toStr == "" {
		toStr = "now"
	}
	filename := fmt.Sprintf("eami-audit-%s-%s.csv", fromStr, toStr)

	if truncated {
		w.Header().Set("X-Truncated", "true")
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	// Header row
	_ = cw.Write([]string{"id", "timestamp", "agent_id", "tool", "action", "decision", "latency_ms", "org_id", "prev_hash", "hash"})

	for _, e := range rows {
		agentID := ""
		if e.AgentID.Valid {
			agentID = e.AgentID.String()
		}
		latencyMS := ""
		if e.LatencyMS.Valid {
			latencyMS = strconv.Itoa(int(e.LatencyMS.Int32))
		}
		_ = cw.Write([]string{
			e.ID.String(),
			e.Timestamp.UTC().Format(time.RFC3339),
			agentID,
			e.ToolName,
			e.Action,
			e.Decision,
			latencyMS,
			e.OrgID.String(),
			e.PrevHash,
			e.Hash,
		})
	}
	cw.Flush()
}

// VerifyAuditChain handles GET /v1/audit/verify.
// Auth: JWT (viewer or above).
//
// Streams the audit log in chronological order (within an optional time range)
// and recomputes the SHA-256 hash chain. Returns whether the chain is intact
// and, if not, the UUID of the first broken row.
//
// Query params:
//
//	from=<RFC3339>  — start of verification window (inclusive)
//	to=<RFC3339>    — end of verification window (inclusive)
//
// Omitting both params verifies the entire org log.
func (s *Server) VerifyAuditChain(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	q := r.URL.Query()

	parseTime := func(key string) *time.Time {
		v := q.Get(key)
		if v == "" {
			return nil
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil
		}
		return &t
	}

	result, err := s.queries.VerifyAuditChain(r.Context(), store.AuditVerifyParams{
		OrgID: uc.OrgID,
		From:  parseTime("from"),
		To:    parseTime("to"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ── converter ─────────────────────────────────────────────────────────────────

func auditEntryToResp(e store.AuditEntry) AuditEntryResp {
	resp := AuditEntryResp{
		ID:        e.ID.String(),
		AgentName: e.AgentName,
		ToolName:  e.ToolName,
		Action:    e.Action,
		Decision:  e.Decision,
		Timestamp: e.Timestamp,
		PrevHash:  e.PrevHash,
		Hash:      e.Hash,
	}
	if e.AgentID.Valid {
		s := e.AgentID.String()
		resp.AgentID = &s
	}
	if e.PolicyID.Valid {
		s := e.PolicyID.String()
		resp.PolicyID = &s
	}
	if e.ApprovalID.Valid {
		s := e.ApprovalID.String()
		resp.ApprovalID = &s
	}
	if e.ApprovedBy.Valid {
		resp.ApprovedBy = &e.ApprovedBy.String
	}
	if e.LatencyMS.Valid {
		v := e.LatencyMS.Int32
		resp.LatencyMS = &v
	}
	if e.TokenIn.Valid {
		v := e.TokenIn.Int32
		resp.TokenIn = &v
	}
	if e.TokenOut.Valid {
		v := e.TokenOut.Int32
		resp.TokenOut = &v
	}
	if len(e.Parameters) > 0 {
		var params interface{}
		if err := json.Unmarshal(e.Parameters, &params); err == nil {
			resp.Parameters = params
		}
	}
	return resp
}
