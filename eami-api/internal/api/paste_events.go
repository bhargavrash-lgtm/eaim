package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/store"
)

// PasteEventReport is a single observed browser paste event sent by a
// future browser extension client. Deliberately has no field capable of
// carrying the pasted text itself -- only coarse indicators (length,
// hash), mirroring reports.go's sensitiveHeaders scrub and B-022's
// toolcreds "no field to carry it" guarantee. Adding a field here to carry
// raw content would require a code change to this struct, which is the
// point: it can't happen by accident, and it's the one place a reviewer
// needs to check to know the guarantee still holds.
type PasteEventReport struct {
	OrgID             string  `json:"org_id"`
	AgentID           string  `json:"agent_id"`
	Hostname          string  `json:"hostname"`
	DestinationDomain string  `json:"destination_domain"`
	OccurredAt        string  `json:"occurred_at"` // RFC3339
	ContentLength     *int32  `json:"content_length,omitempty"`
	ContentHash       *string `json:"content_hash,omitempty"`
	OSUsername        *string `json:"os_username,omitempty"`
}

// IngestPasteEvents handles POST /v1/reports/paste-events.
// Authentication: X-Service-Key header (requireServiceKey middleware).
// Body: JSON array of PasteEventReport objects.
// On success returns 202 Accepted with counts of accepted/rejected events.
//
// Append-only: every valid event becomes its own row in paste_events (no
// upsert/dedup collapsing, unlike discovered_endpoints). Batch-first: all
// events in the request are written via a single multi-row INSERT
// (store.BatchInsertPasteEvents), not one round trip per event.
func (s *Server) IngestPasteEvents(w http.ResponseWriter, r *http.Request) {
	var events []PasteEventReport
	if err := decodeJSON(r, &events); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	if len(events) == 0 {
		writeJSON(w, http.StatusAccepted, map[string]int{"accepted": 0, "rejected": 0})
		return
	}

	ctx := r.Context()

	// Resolve source_endpoint_id once per distinct (org_id, agent_id) pair
	// in the batch, not once per event -- a batch is typically one machine.
	type endpointKey struct {
		orgID   uuid.UUID
		agentID string
	}
	resolved := make(map[endpointKey]uuid.UUID)

	var toInsert []store.PasteEventInput
	rejected := 0

	for i := range events {
		ev := &events[i]

		orgID, err := uuid.Parse(ev.OrgID)
		if err != nil {
			rejected++
			continue
		}
		if ev.AgentID == "" || ev.Hostname == "" || ev.DestinationDomain == "" {
			rejected++
			continue
		}
		occurredAt, err := time.Parse(time.RFC3339, ev.OccurredAt)
		if err != nil {
			rejected++
			continue
		}
		// Server-side allowlist check -- never trust the client's own
		// classification of what counts as a known AI-tool destination.
		if !MatchesKnownPasteDestination(ev.DestinationDomain) {
			rejected++
			continue
		}

		key := endpointKey{orgID: orgID, agentID: ev.AgentID}
		endpointID, ok := resolved[key]
		if !ok {
			endpointID, err = s.queries.ResolvePasteSourceEndpoint(ctx, store.ResolvePasteSourceEndpointParams{
				OrgID:    orgID,
				AgentID:  ev.AgentID,
				Hostname: ev.Hostname,
			})
			if err != nil {
				rejected++
				continue
			}
			resolved[key] = endpointID
		}

		toInsert = append(toInsert, store.PasteEventInput{
			OrgID:             orgID,
			SourceEndpointID:  endpointID,
			DestinationDomain: strings.ToLower(ev.DestinationDomain),
			ContentLength:     ev.ContentLength,
			ContentHash:       ev.ContentHash,
			OSUsername:        ev.OSUsername,
			OccurredAt:        occurredAt,
		})
	}

	accepted := 0
	if len(toInsert) > 0 {
		n, err := s.queries.BatchInsertPasteEvents(ctx, toInsert)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to write paste events")
			return
		}
		accepted = int(n)
	}

	writeJSON(w, http.StatusAccepted, map[string]int{"accepted": accepted, "rejected": rejected})
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
