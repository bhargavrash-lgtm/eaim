// Multi-Hop Workflows Brief 2 (B-059): static per-step parameters.
// New file -- workflows.go (B-058) is untouched, per that brief's own
// MUST-NOT-MODIFY boundary carried forward into this one. See
// schema/migrations-v2/000007's comment for why params live in a
// separate table from workflow_steps rather than reusing
// workflow_steps.input_mapping (reserved for a later output->input
// mapping brief).
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// GetWorkflowStepParams handles GET /v1/gateway/workflow-steps/{stepId}/params
func (s *Server) GetWorkflowStepParams(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	stepID, err := parseUUIDParam(r, "stepId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid stepId")
		return
	}
	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "workflow store is not configured")
		return
	}

	belongs, err := s.queries.WorkflowStepBelongsToOrg(r.Context(), stepID, uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !belongs {
		writeError(w, http.StatusNotFound, "not_found", "workflow step not found")
		return
	}

	raw, err := s.queries.GetWorkflowStepParams(r.Context(), stepID, uc.OrgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// A valid step with no params configured yet -- {} is the
			// correct "nothing set" response, not a 404 (the step itself
			// was already confirmed to exist and belong to this org above).
			writeJSON(w, http.StatusOK, json.RawMessage(`{}`))
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(raw))
}

// PutWorkflowStepParams handles PUT /v1/gateway/workflow-steps/{stepId}/params.
// Replaces the step's static params wholesale -- no partial merge, same
// "always sent, replace whole map" convention this codebase already uses
// for action_paths (B-045/B-046).
func (s *Server) PutWorkflowStepParams(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	stepID, err := parseUUIDParam(r, "stepId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid stepId")
		return
	}
	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "workflow store is not configured")
		return
	}

	var body json.RawMessage
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	// Must be a JSON object -- these become mcp.ActionContext.Parameters
	// (map[string]any) at dispatch time; a bare array/scalar would fail
	// there far from this request, with a confusing error.
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "params must be a JSON object")
		return
	}

	raw, err := s.queries.UpsertWorkflowStepParams(r.Context(), stepID, uc.OrgID, body)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "workflow step not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(raw))
}
