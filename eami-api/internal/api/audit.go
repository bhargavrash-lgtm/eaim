package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eami/api/internal/store"
	"github.com/google/uuid"
)

const (
	auditExportQueryTimeout = 15 * time.Second
	auditExportMaxBytes     = 20 << 20
)

var errAuditExportTooLarge = errors.New("audit export exceeds the 10,000-row or 20 MiB limit; narrow the filters and try again")

// auditParamsFromRequest keeps listing and exporting on exactly the same
// filter contract. Invalid date bounds must fail closed: silently discarding a
// malformed date can turn a deliberately narrow export into an all-history
// download.
func auditParamsFromRequest(r *http.Request, orgID uuid.UUID) (store.ListAuditParams, error) {
	q := r.URL.Query()
	strPtr := func(key string) *string {
		if v := q.Get(key); v != "" {
			return &v
		}
		return nil
	}
	timePtr := func(key string) (*time.Time, error) {
		v := q.Get(key)
		if v == "" {
			return nil, nil
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, fmt.Errorf("%s must be RFC3339", key)
		}
		return &t, nil
	}

	from, err := timePtr("from")
	if err != nil {
		return store.ListAuditParams{}, err
	}
	to, err := timePtr("to")
	if err != nil {
		return store.ListAuditParams{}, err
	}
	if from != nil && to != nil && to.Before(*from) {
		return store.ListAuditParams{}, errors.New("to must not be before from")
	}

	return store.ListAuditParams{
		OrgID:     orgID,
		AgentName: strPtr("agent_name"),
		ToolName:  strPtr("tool_name"),
		Decision:  strPtr("decision"),
		From:      from,
		To:        to,
	}, nil
}

// ListAudit handles GET /v1/audit
func (s *Server) ListAudit(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	// Pagination defaults.
	page := 1
	perPage := 50
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := r.URL.Query().Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			perPage = n
		}
	}

	p, err := auditParamsFromRequest(r, uc.OrgID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	p.Limit = int32(perPage)
	p.Offset = int32((page - 1) * perPage)

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

// ExportAudit handles GET /v1/audit/export. It returns a complete, bounded
// export or a clear error; partial compliance exports are intentionally not
// produced.
func (s *Server) ExportAudit(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	p, err := auditParamsFromRequest(r, uc.OrgID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !s.beginAuditExport(uc.OrgID.String()) {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "export_in_progress", "an audit export is already running for this organization; try again shortly")
		return
	}
	defer s.endAuditExport(uc.OrgID.String())
	if ok, retryAfter := s.auditExportLimiter.Allow(uc.OrgID.String()); !ok {
		setRetryAfter(w, retryAfter)
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many audit export requests for this organization; try again later")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), auditExportQueryTimeout)
	defer cancel()
	tx, err := s.queries.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not prepare audit export")
		return
	}
	exportQueries := s.queries.WithTx(tx)
	stats, err := exportQueries.AuditExportStats(ctx, p)
	if err != nil {
		writeAuditExportQueryError(w, err)
		return
	}
	if stats.Rows > store.ExportAuditMaxRows || stats.MaxFieldBytes > store.ExportAuditMaxFieldBytes || stats.EstimatedBytes > auditExportMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "export_too_large", errAuditExportTooLarge.Error())
		return
	}
	csvBytes, err := auditExportCSVFromQuery(ctx, exportQueries, p)
	if err != nil {
		if errors.Is(err, errAuditExportTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "export_too_large", err.Error())
			return
		}
		writeAuditExportQueryError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeAuditExportQueryError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="eami-audit-export.csv"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(csvBytes)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(csvBytes)
}

func (s *Server) beginAuditExport(orgID string) bool {
	s.auditExportMu.Lock()
	defer s.auditExportMu.Unlock()
	if s.auditExportsInFlight == nil {
		s.auditExportsInFlight = make(map[string]struct{})
	}
	if _, exists := s.auditExportsInFlight[orgID]; exists {
		return false
	}
	s.auditExportsInFlight[orgID] = struct{}{}
	return true
}

func (s *Server) endAuditExport(orgID string) {
	s.auditExportMu.Lock()
	defer s.auditExportMu.Unlock()
	delete(s.auditExportsInFlight, orgID)
}

func writeAuditExportQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		writeError(w, http.StatusRequestEntityTooLarge, "export_too_large", "audit export timed out; narrow the filters and try again")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "could not prepare audit export")
}

type auditCSVBuffer struct {
	bytes.Buffer
	maxBytes int
}

func (b *auditCSVBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.maxBytes {
		return 0, errAuditExportTooLarge
	}
	return b.Buffer.Write(p)
}

func csvSafeText(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func optionalCSVInt(value int32, valid bool) string {
	if !valid {
		return ""
	}
	return strconv.Itoa(int(value))
}

func auditExportCSV(rows []store.AuditExportRow) ([]byte, error) {
	buf := &auditCSVBuffer{maxBytes: auditExportMaxBytes}
	w := csv.NewWriter(buf)
	if err := w.Write([]string{"timestamp", "agent", "tool", "action", "decision", "latency_ms", "tokens_in", "tokens_out", "hash"}); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if err := writeAuditCSVRow(w, row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func auditExportCSVFromQuery(ctx context.Context, queries *store.Queries, p store.ListAuditParams) ([]byte, error) {
	buf := &auditCSVBuffer{maxBytes: auditExportMaxBytes}
	w := csv.NewWriter(buf)
	if err := w.Write([]string{"timestamp", "agent", "tool", "action", "decision", "latency_ms", "tokens_in", "tokens_out", "hash"}); err != nil {
		return nil, err
	}
	if err := queries.ExportAudit(ctx, p, func(row store.AuditExportRow) error {
		return writeAuditCSVRow(w, row)
	}); err != nil {
		return nil, err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeAuditCSVRow(w *csv.Writer, row store.AuditExportRow) error {
	return w.Write([]string{
		row.Timestamp.UTC().Format(time.RFC3339Nano),
		csvSafeText(row.AgentName),
		csvSafeText(row.ToolName),
		csvSafeText(row.Action),
		csvSafeText(row.Decision),
		optionalCSVInt(row.LatencyMS.Int32, row.LatencyMS.Valid),
		optionalCSVInt(row.TokenIn.Int32, row.TokenIn.Valid),
		optionalCSVInt(row.TokenOut.Int32, row.TokenOut.Valid),
		csvSafeText(row.Hash),
	})
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
	if e.DataHandling.Valid {
		resp.DataHandling = &e.DataHandling.String
	}
	if e.RedactedCount.Valid {
		v := e.RedactedCount.Int32
		resp.RedactedCount = &v
	}
	return resp
}
