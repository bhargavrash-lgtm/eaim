package api

import (
	"context"
	"encoding/json"
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
