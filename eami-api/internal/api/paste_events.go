package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/store"
)

// Field length caps (security-review finding): these columns are coarse
// indicators by convention/comment, not by any enforced bound -- nothing
// stopped a compromised client from stuffing arbitrarily long values into
// them (e.g. MatchesKnownPasteDestination's suffix match accepts any
// length of subdomain prefix on a known domain). Values chosen generously
// above any real value these fields ever legitimately take: 253 is DNS's
// own max hostname length; 128 comfortably covers a hex-encoded SHA-512
// (the longest hash this codebase uses anywhere); 256 is a generous cap
// for an OS username.
const (
	maxDestinationDomainLen = 253
	maxContentHashLen       = 128
	maxOSUsernameLen        = 256
)

// validatePasteEvent validates one event's destination_domain (must be
// non-empty, within maxDestinationDomainLen, and match the server-side
// allowlist -- never trusts the client's own classification, same
// principle as B1's background.js re-checking domains.js) and occurred_at
// (must be RFC3339), returning a PasteEventInput with OrgID/
// SourceEndpointID left zero for ingest.go's processPasteEventRelayItem
// (B-035), the sole real ingestion path, to fill in from its own
// server-resolved orgID.
//
// Previously also called from IngestPasteEvents (POST
// /v1/reports/paste-events, B-032's original pre-extension design),
// closed 2026-09-19: confirmed to have zero real callers anywhere in this
// codebase (the browser extension only ever talks native messaging, never
// the network -- see eami-browser-extension/background.js's own doc
// comment) and superseded in practice by this same relay path since
// B-035 shipped. An unused, write-capable, client-supplied-org_id
// endpoint was judged a real liability not worth keeping live -- see
// BACKLOG.md's B-19x entry for the full investigation.
func validatePasteEvent(destinationDomain, occurredAtStr string, contentLength *int32, contentHash, osUsername *string) (store.PasteEventInput, bool) {
	if destinationDomain == "" || len(destinationDomain) > maxDestinationDomainLen {
		return store.PasteEventInput{}, false
	}
	if !MatchesKnownPasteDestination(destinationDomain) {
		return store.PasteEventInput{}, false
	}
	if contentHash != nil && len(*contentHash) > maxContentHashLen {
		return store.PasteEventInput{}, false
	}
	if osUsername != nil && len(*osUsername) > maxOSUsernameLen {
		return store.PasteEventInput{}, false
	}
	occurredAt, err := time.Parse(time.RFC3339, occurredAtStr)
	if err != nil {
		return store.PasteEventInput{}, false
	}
	return store.PasteEventInput{
		DestinationDomain: strings.ToLower(destinationDomain),
		ContentLength:     contentLength,
		ContentHash:       contentHash,
		OSUsername:        osUsername,
		OccurredAt:        occurredAt,
	}, true
}

// ── Read path (B-038): admin UI over captured paste events ────────────────

// ListPasteEvents handles GET /v1/paste-events?domain=&from=&to=&page=&per_page=
// Auth: JWT (viewer or above), org-scoped via claimsFromContext -- same
// org-isolation convention as ListAudit.
func (s *Server) ListPasteEvents(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	q := r.URL.Query()

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

	var domain *string
	if v := q.Get("domain"); v != "" {
		d := strings.ToLower(v)
		domain = &d
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

	p := store.ListPasteEventsParams{
		OrgID:  uc.OrgID,
		Domain: domain,
		From:   timePtr("from"),
		To:     timePtr("to"),
		Limit:  int32(perPage),
		Offset: int32((page - 1) * perPage),
	}

	events, err := s.queries.ListPasteEvents(r.Context(), p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	total, err := s.queries.CountPasteEvents(r.Context(), p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	data := make([]PasteEventResp, 0, len(events))
	for _, e := range events {
		data = append(data, pasteEventToResp(e))
	}

	writeJSON(w, http.StatusOK, PasteEventListResponse{
		Data: data,
		Meta: PaginationMeta{
			Total:   total,
			Page:    page,
			PerPage: perPage,
		},
	})
}

func pasteEventToResp(e store.PasteEvent) PasteEventResp {
	resp := PasteEventResp{
		ID:                e.ID.String(),
		DestinationDomain: e.DestinationDomain,
		OccurredAt:        e.OccurredAt,
	}
	if e.ContentLength.Valid {
		v := e.ContentLength.Int32
		resp.ContentLength = &v
	}
	if e.ContentHash.Valid {
		resp.ContentHash = &e.ContentHash.String
	}
	if e.OSUsername.Valid {
		resp.OSUsername = &e.OSUsername.String
	}
	return resp
}

// PasteEventsTimeSeries handles
// GET /v1/paste-events/timeseries?from=DATE&to=DATE&granularity=day|hour|week&domain=
//
// Returns real per-domain, per-bucket counts (not an approximation) --
// unlike FinOpsTimeSeries's total-cost-then-proportionally-distributed
// per-model chart data, this query already groups by (bucket, domain)
// directly, so the frontend can pivot it into a stacked chart without any
// distribution/estimation step.
func (s *Server) PasteEventsTimeSeries(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)

	from, err := parseDateParam(r, "from")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	to, err := parseDateParam(r, "to")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !to.After(from) {
		writeError(w, http.StatusBadRequest, "bad_request", "to must be after from")
		return
	}

	granularity := r.URL.Query().Get("granularity")
	var bucket string
	switch granularity {
	case "hour":
		bucket = "1 hour"
	case "week":
		bucket = "1 week"
	case "day", "":
		bucket = "1 day"
		granularity = "day"
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "granularity must be day, hour, or week")
		return
	}

	var domainFilter pgtype.Text
	if d := r.URL.Query().Get("domain"); d != "" {
		domainFilter = pgtype.Text{String: strings.ToLower(d), Valid: true}
	}

	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "paste events store is not configured")
		return
	}

	orgID := pgtype.UUID{Bytes: uc.OrgID, Valid: true}
	fromTS := pgtype.Timestamptz{Time: from, Valid: true}
	toTS := pgtype.Timestamptz{Time: to, Valid: true}
	db := s.queries.DB()
	ctx := r.Context()

	// time_bucket() is a TimescaleDB function; the bucket interval is
	// injected via format (safe: value is controlled by the switch above,
	// not user input) -- same pattern as FinOpsTimeSeries. Matches
	// idx_paste_events_org_domain when domain is given, idx_paste_events_org
	// otherwise, same as ListPasteEvents.
	sqlQuery := fmt.Sprintf(`
SELECT time_bucket('%s', occurred_at) AS bucket, destination_domain, COUNT(*) AS cnt
FROM paste_events
WHERE org_id = $1
  AND occurred_at >= $2
  AND occurred_at <  $3
  AND ($4::text IS NULL OR destination_domain = $4)
GROUP BY bucket, destination_domain
ORDER BY bucket`, bucket)

	rows, err := db.Query(ctx, sqlQuery, orgID, fromTS, toTS, domainFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer rows.Close()

	series := make([]PasteEventDomainPoint, 0)
	for rows.Next() {
		var pt PasteEventDomainPoint
		if err := rows.Scan(&pt.Bucket, &pt.Domain, &pt.Count); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		series = append(series, pt)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, PasteEventTimeSeries{
		Granularity: granularity,
		Series:      series,
	})
}
