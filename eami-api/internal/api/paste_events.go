package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

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
