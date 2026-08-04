package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/eami/api/internal/store"
)

// batchIngestRequest matches the payload sent by eami-collector's forwarder.
type batchIngestRequest struct {
	Reports []batchIngestItem `json:"reports"`
}

type batchIngestItem struct {
	ID         string          `json:"id"`
	AgentID    string          `json:"agent_id"`
	Hostname   string          `json:"hostname"`
	ReceivedAt time.Time       `json:"received_at"`
	Report     json.RawMessage `json:"report"`
}

// agentReport mirrors the top-level fields of an eami-agent EndpointReport.
// We only parse fields we store in normalised tables; the raw JSON blob always
// lands verbatim in endpoint_reports.report (JSONB) regardless.
type agentReport struct {
	AgentID      string    `json:"agent_id"`
	Hostname     string    `json:"hostname"`
	CollectedAt  time.Time `json:"collected_at"`
	AgentVersion string    `json:"agent_version"`
	Platform     struct {
		OS        string `json:"os"`
		Arch      string `json:"arch"`
		OSVersion string `json:"os_version"`
	} `json:"platform"`
	AIApps []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Source  string `json:"source"`
	} `json:"ai_apps"`
	LocalModels []struct {
		Name      string    `json:"name"`
		Source    string    `json:"source"`
		FilePath  string    `json:"file_path"`
		SizeBytes int64     `json:"size_bytes"`
		ModelType string    `json:"model_type"`
		ModifiedAt time.Time `json:"modified_at"`
	} `json:"local_models"`
	MCPServers []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
		Port   int    `json:"port"`
	} `json:"mcp_servers"`
	// PasteEvents (B-035) is populated only by eami-agent's native-messaging
	// relay (internal/nativemsg's outboundReport) -- a report shaped
	// {agent_id, hostname, collected_at, paste_events} with no scan data at
	// all. A real payload.Report scan never sets this field. This is a
	// structural, mutually-exclusive distinction in the wire format today,
	// not a heuristic: processIngestItem uses len(PasteEvents) > 0 to
	// route relay items to processPasteEventRelayItem instead of the normal
	// scan-report path below. If eami-agent's report shapes are ever
	// merged, this distinction needs revisiting.
	PasteEvents []rawPasteEvent `json:"paste_events"`
}

// rawPasteEvent is one paste event as eami-agent's native-messaging relay
// sends it (see nativemsg.PasteEvent) -- no org_id/agent_id/hostname/
// content, mirrors B-032's PasteEventReport no-raw-content guarantee.
type rawPasteEvent struct {
	DestinationDomain string  `json:"destination_domain"`
	OccurredAt        string  `json:"occurred_at"`
	ContentLength     *int32  `json:"content_length"`
	ContentHash       *string `json:"content_hash"`
	OSUsername        *string `json:"os_username"`
}

// allowedModelSources is the set of values accepted by the
// endpoint_model_files.source CHECK constraint.
var allowedModelSources = map[string]bool{
	"ollama": true, "lmstudio": true, "huggingface": true, "unknown": true,
}

// allowedMCPSources is the set of values accepted by the
// endpoint_mcp_servers.source CHECK constraint.
var allowedMCPSources = map[string]bool{
	"claude_desktop": true, "vscode": true, "cursor": true, "live_port": true,
}

// IngestBatch handles POST /v1/ingest/batch.
// Auth: X-Service-Key header (requireServiceKey middleware).
//
// Accepts discovery report batches from eami-collector and writes them to the
// endpoints, endpoint_reports, endpoint_ai_apps, endpoint_model_files, and
// endpoint_mcp_servers tables. Unknown agents are auto-created on first contact.
func (s *Server) IngestBatch(w http.ResponseWriter, r *http.Request) {
	var req batchIngestRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	if len(req.Reports) == 0 {
		writeJSON(w, http.StatusAccepted, map[string]int{"accepted": 0})
		return
	}

	ctx := r.Context()

	// Single-tenant v1: all collector data lands in the first org.
	orgID, err := s.queries.GetDefaultOrgID(ctx)
	if err != nil {
		slog.Error("ingest: GetDefaultOrgID failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "no_org",
			"no org found; run reseed.sql before sending agent reports")
		return
	}

	accepted := 0
	for _, item := range req.Reports {
		if item.AgentID == "" {
			continue
		}
		if err := s.processIngestItem(ctx, orgID, item); err != nil {
			slog.Warn("ingest: skipping failed item",
				"batch_id", item.ID,
				"agent_id", item.AgentID,
				"err", err,
			)
			continue
		}
		accepted++
	}

	writeJSON(w, http.StatusAccepted, map[string]int{"accepted": accepted})
}

// processIngestItem writes one agent report through the full persistence path:
// upsert endpoint → delete stale normalised rows → insert report blob →
// insert normalised ai_apps / model_files / mcp_servers.
//
// Each step is independent (no transaction) so a normalised-data failure never
// discards an already-written report blob.
func (s *Server) processIngestItem(ctx context.Context, orgID uuid.UUID, item batchIngestItem) error {
	// Parse the inner report for field extraction. Failure is non-fatal —
	// we still write the raw blob even if parsing fails.
	var rep agentReport
	_ = json.Unmarshal(item.Report, &rep)

	// Prefer the agent_id from the outer envelope (set by the collector).
	agentID := item.AgentID
	if agentID == "" {
		agentID = rep.AgentID
	}
	hostname := item.Hostname
	if hostname == "" {
		hostname = rep.Hostname
	}

	// B-035: a native-messaging-relayed paste event arrives through this
	// same /v1/ingest/batch pipeline (eami-collector forwards it as opaque
	// bytes, unchanged) but carries no scan data at all -- routing it
	// through the normal path below would call UpsertAgentEndpoint with
	// blank agent_version/os_info, silently clobbering a real endpoint's
	// already-known metadata (the exact bug class ResolvePasteSourceEndpoint
	// exists to avoid, see paste_events.sql.go). Handled entirely
	// separately: resolves the endpoint via the non-clobbering upsert and
	// writes straight to paste_events, never touching endpoint_reports or
	// any normalised table for this item (AC4/AC5).
	//
	// Checked via != nil, not len() > 0 (caught by review): encoding/json
	// only sets a slice field to non-nil when the "paste_events" key is
	// actually present in the source JSON (nil if the key is absent
	// entirely) -- so a relay item that happens to carry an empty-but-
	// present `"paste_events": []` (e.g. a future no-op flush) is still
	// correctly routed to the non-clobbering path instead of silently
	// falling through to the scan-report path below.
	if rep.PasteEvents != nil {
		if rep.AgentVersion != "" {
			// Defensive: today's two report shapes are structurally
			// mutually exclusive (see agentReport's doc comment), so this
			// should never fire. If eami-agent's wire format is ever
			// changed to merge scan data and paste events into one report,
			// this makes that change fail loudly (logged, scan data
			// dropped) instead of silently discarding it forever.
			slog.Warn("ingest: item carries both scan data and paste_events -- scan fields ignored, only paste_events processed",
				"agent_id", agentID, "batch_id", item.ID)
		}
		return s.processPasteEventRelayItem(ctx, orgID, agentID, hostname, rep.PasteEvents)
	}

	// Build os_info JSONB from the parsed platform struct.
	osInfo, _ := json.Marshal(rep.Platform)

	// 1. Upsert the endpoint row and get (or create) its UUID.
	endpointID, err := s.queries.UpsertAgentEndpoint(ctx, store.UpsertAgentEndpointParams{
		OrgID:        orgID,
		AgentID:      agentID,
		Hostname:     hostname,
		AgentVersion: rep.AgentVersion,
		OSInfo:       osInfo,
	})
	if err != nil {
		return err
	}

	// 2. Delete stale normalised rows so we always reflect the latest report.
	if err := s.queries.DeleteEndpointNormalizedData(ctx, endpointID); err != nil {
		slog.Warn("ingest: could not delete stale normalised data",
			"endpoint_id", endpointID, "err", err)
		// Non-fatal: continue writing the new rows anyway.
	}

	// 3. Insert the full report blob.
	collectedAt := rep.CollectedAt
	if collectedAt.IsZero() {
		collectedAt = item.ReceivedAt
	}
	reportID, err := s.queries.InsertEndpointReport(ctx, store.InsertEndpointReportParams{
		EndpointID:  endpointID,
		OrgID:       orgID,
		CollectedAt: collectedAt,
		Report:      item.Report,
	})
	if err != nil {
		return err
	}

	// 4. Insert normalised AI apps.
	for _, app := range rep.AIApps {
		if app.Name == "" {
			continue
		}
		if err := s.queries.InsertEndpointAIApp(ctx, store.InsertEndpointAIAppParams{
			EndpointID: endpointID,
			ReportID:   reportID,
			Name:       app.Name,
			Version:    app.Version,
			Source:     app.Source,
			DetectedAt: collectedAt,
		}); err != nil {
			slog.Warn("ingest: insert ai_app failed", "name", app.Name, "err", err)
		}
	}

	// 5. Insert normalised model files.
	for _, m := range rep.LocalModels {
		if m.Name == "" {
			continue
		}
		src := m.Source
		if !allowedModelSources[src] {
			src = "unknown"
		}
		sizeMB := float64(m.SizeBytes) / 1_048_576
		if err := s.queries.InsertEndpointModelFile(ctx, store.InsertEndpointModelFileParams{
			EndpointID: endpointID,
			ReportID:   reportID,
			Name:       m.Name,
			Path:       m.FilePath,
			SizeMB:     sizeMB,
			Format:     m.ModelType,
			Source:     src,
			DetectedAt: collectedAt,
		}); err != nil {
			slog.Warn("ingest: insert model_file failed", "name", m.Name, "err", err)
		}
	}

	// 6. Insert normalised MCP servers.
	for _, mcp := range rep.MCPServers {
		if mcp.Name == "" {
			continue
		}
		// Infer transport from port presence.
		transport := "stdio"
		if mcp.Port > 0 {
			transport = "sse"
		}
		// Normalise source to CHECK constraint values; nil if unrecognised.
		var mcpSrc *string
		if allowedMCPSources[mcp.Source] {
			s := mcp.Source
			mcpSrc = &s
		}
		var port *int
		if mcp.Port > 0 {
			p := mcp.Port
			port = &p
		}
		if err := s.queries.InsertEndpointMCPServer(ctx, store.InsertEndpointMCPServerParams{
			EndpointID: endpointID,
			ReportID:   reportID,
			Name:       mcp.Name,
			Transport:  transport,
			Port:       port,
			Source:     mcpSrc,
			DetectedAt: collectedAt,
		}); err != nil {
			slog.Warn("ingest: insert mcp_server failed", "name", mcp.Name, "err", err)
		}
	}

	return nil
}

// processPasteEventRelayItem writes the paste events carried by a
// native-messaging-relayed item (see agentReport.PasteEvents' doc comment)
// directly into paste_events -- deliberately bypassing UpsertAgentEndpoint/
// InsertEndpointReport/every normalised-table insert above, both because
// this item has no scan data to write there and because UpsertAgentEndpoint
// would otherwise clobber a real endpoint's agent_version/os_info with the
// blanks this item carries.
//
// orgID is always the server-resolved value from IngestBatch's
// GetDefaultOrgID call, never anything from item.Report -- a compromised
// eami-agent has no field in this wire format that can influence it.
// Endpoint resolution uses ResolvePasteSourceEndpoint (B-032), the same
// non-clobbering upsert paste_events.sql.go's own direct ingestion path
// uses, so an agent that reports through both this relay path and a real
// scan resolves to the identical endpoints row.
// maxPasteEventsPerRelayItem bounds how many events a single relay item
// can carry in one call -- today's real sender (nativemsg.RunHost) always
// sends exactly one event per item, so this is generous headroom rather
// than a tight limit, but this endpoint is reachable directly (bypassing
// eami-agent's native-messaging host and its own protocol-level 1MB
// message cap entirely) by anything holding the collector's service key,
// so it must not be literally unbounded.
const maxPasteEventsPerRelayItem = 100

func (s *Server) processPasteEventRelayItem(ctx context.Context, orgID uuid.UUID, agentID, hostname string, raw []rawPasteEvent) error {
	// agentID is never empty here -- IngestBatch already skips any item
	// with an empty item.AgentID before processIngestItem is ever called,
	// and agentID always resolves to item.AgentID when that's non-empty.
	// hostname CAN legitimately be empty (both item.Hostname and the
	// parsed report's hostname could be blank), so that check is real.
	if hostname == "" {
		return fmt.Errorf("paste event relay: hostname is required")
	}
	if len(raw) > maxPasteEventsPerRelayItem {
		return fmt.Errorf("paste event relay: item carries %d events, exceeds max %d", len(raw), maxPasteEventsPerRelayItem)
	}

	endpointID, err := s.queries.ResolvePasteSourceEndpoint(ctx, store.ResolvePasteSourceEndpointParams{
		OrgID:    orgID,
		AgentID:  agentID,
		Hostname: hostname,
	})
	if err != nil {
		return fmt.Errorf("paste event relay: resolve source endpoint: %w", err)
	}

	var toInsert []store.PasteEventInput
	for _, ev := range raw {
		input, ok := validatePasteEvent(ev.DestinationDomain, ev.OccurredAt, ev.ContentLength, ev.ContentHash, ev.OSUsername)
		if !ok {
			continue
		}
		input.OrgID = orgID
		input.SourceEndpointID = endpointID
		toInsert = append(toInsert, input)
	}
	if len(toInsert) == 0 {
		return fmt.Errorf("paste event relay: no valid paste events in item")
	}

	if _, err := s.queries.BatchInsertPasteEvents(ctx, toInsert); err != nil {
		return fmt.Errorf("paste event relay: batch insert: %w", err)
	}
	return nil
}
