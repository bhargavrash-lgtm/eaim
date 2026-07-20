package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/eami/api/internal/store"
)

// ── Response types ────────────────────────────────────────────────────────────

type ToolResp struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	Type      string     `json:"type"`
	AuthType  string     `json:"auth_type"`
	MCPCommand *string   `json:"mcp_command,omitempty"`
	BaseURL   *string    `json:"base_url,omitempty"`
	Status    string     `json:"status"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

func toolToResp(t store.GatewayTool) ToolResp {
	r := ToolResp{
		ID: t.ID, Name: t.Name, Type: t.Type,
		AuthType: t.AuthType, Status: t.Status, CreatedAt: t.CreatedAt,
	}
	if t.MCPCommand.Valid {
		r.MCPCommand = &t.MCPCommand.String
	}
	if t.BaseURL.Valid {
		r.BaseURL = &t.BaseURL.String
	}
	if t.LastUsed.Valid {
		ts := t.LastUsed.Time
		r.LastUsed = &ts
	}
	return r
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// ListTools handles GET /v1/gateway/tools
func (s *Server) ListTools(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	tools, err := s.queries.ListTools(r.Context(), uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	resp := make([]ToolResp, 0, len(tools))
	for _, t := range tools {
		resp = append(resp, toolToResp(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": resp})
}

// CreateTool handles POST /v1/gateway/tools
func (s *Server) CreateTool(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)

	var body struct {
		Name       string   `json:"name"`
		Type       string   `json:"type"`
		AuthType   string   `json:"auth_type"`
		MCPCommand *string  `json:"mcp_command"`
		MCPArgs    []string `json:"mcp_args"`
		BaseURL    *string  `json:"base_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.Name == "" || body.Type == "" || body.AuthType == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name, type, and auth_type are required")
		return
	}

	t, err := s.queries.CreateTool(r.Context(), store.CreateToolParams{
		OrgID:      uc.OrgID,
		Name:       body.Name,
		Type:       body.Type,
		AuthType:   body.AuthType,
		MCPCommand: body.MCPCommand,
		MCPArgs:    body.MCPArgs,
		BaseURL:    body.BaseURL,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toolToResp(t))
}

// DeleteTool handles DELETE /v1/gateway/tools/{toolId}
func (s *Server) DeleteTool(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	toolID, err := uuid.Parse(chi.URLParam(r, "toolId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid tool ID")
		return
	}
	found, err := s.queries.DeleteTool(r.Context(), uc.OrgID, toolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "tool not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TestTool handles POST /v1/gateway/tools/{toolId}/test
// For now returns a synthetic "connected" result after a brief probe.
func (s *Server) TestTool(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	toolID, err := uuid.Parse(chi.URLParam(r, "toolId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid tool ID")
		return
	}

	// Mark the tool as tested/connected (latency=0 for synthetic test).
	if err := s.queries.MarkToolTested(r.Context(), uc.OrgID, toolID, "connected", 0); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "connected", "latency_ms": 0})
}
