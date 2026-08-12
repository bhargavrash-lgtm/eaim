// Multi-Hop Workflows (Thread B), Brief 1 (B-058): schema + CRUD
// foundation only. Nothing here executes a workflow -- eami-gateway's
// dispatch closure (cmd/gateway/main.go) has zero references to
// workflows/workflow_steps, and this file never calls out to eami-gateway
// at all. See BACKLOG.md's Thread B entry for the full epic (Briefs 2-7:
// execution, output->input mapping, per-hop TOCTOU pinning,
// escalation/approval integration, audit linkage).
//
// api/openapi.yaml deliberately not touched -- ships undocumented,
// matching B-038/B-045's precedent (openapi.yaml is Architect-EAMI-owned
// per BOUNDARIES.md/CLAUDE.md).
package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/eami/api/internal/store"
)

// isForeignKeyViolation reports whether err is a Postgres FK-violation
// (SQLSTATE 23503), mirroring tool_connectivity.go's classifyPgConnectError
// convention for checking pgErr.Code. Used only to distinguish "the
// connector this step references no longer exists" (a real, if narrow,
// TOCTOU race: validateWorkflowSteps confirms a tool exists moments before
// the transaction's INSERT runs, and it can be deleted by a concurrent
// request in between) from a genuine internal error -- without this, that
// race surfaces as an opaque 500 instead of a clear 400 (code review
// finding).
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// ── Request/response DTOs ────────────────────────────────────────────────────

// WorkflowStepDTO is one step in a workflow request/response body.
type WorkflowStepDTO struct {
	GatewayToolID string `json:"gateway_tool_id"`
	ToolName      string `json:"tool_name,omitempty"` // response-only, populated from the joined gateway_tools.name
	Action        string `json:"action"`
}

// WorkflowCreateRequest is CreateWorkflow's request body.
type WorkflowCreateRequest struct {
	Name   string            `json:"name"`
	Status string            `json:"status"` // optional; defaults to "draft"
	Steps  []WorkflowStepDTO `json:"steps"`
}

// WorkflowUpdateRequest is UpdateWorkflow's request body. Steps, if
// present, fully replaces the existing step list -- omitted leaves
// existing steps untouched. Unlike ToolUpdate's action_paths (where an
// empty {} is a valid "clear all mappings" state), an empty steps array
// is NOT a way to clear a workflow to zero steps: validateWorkflowSteps
// rejects it with the same "at least one step is required" rule Create
// enforces -- a workflow with no steps isn't a meaningful chain of
// connectors, so there's no supported all-clear operation here (code
// review finding: a prior version of this comment incorrectly implied
// {"steps": []} was a supported way to clear the list).
type WorkflowUpdateRequest struct {
	Name   *string            `json:"name"`
	Status *string            `json:"status"`
	Steps  *[]WorkflowStepDTO `json:"steps"`
}

// WorkflowResp is a workflow returned to the client.
type WorkflowResp struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Status    string            `json:"status"`
	StepCount int64             `json:"step_count,omitempty"` // ListWorkflows only
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
	Steps     []WorkflowStepDTO `json:"steps,omitempty"` // GetWorkflow/Create/Update only
}

func workflowToResp(w store.Workflow) WorkflowResp {
	return WorkflowResp{
		ID:        w.ID.String(),
		Name:      w.Name,
		Status:    w.Status,
		StepCount: w.StepCount,
		CreatedAt: w.CreatedAt,
		UpdatedAt: w.UpdatedAt,
	}
}

// workflowStepToDTO converts a joined store row to the response shape. A
// step whose connector was deleted (ON DELETE SET NULL, migration 000006)
// comes back with GatewayToolID/ToolName both "" -- deliberately visible
// to the caller, not hidden or defaulted, so an admin editing the
// workflow can see the broken step and fix it (AC4).
func workflowStepToDTO(s store.WorkflowStep) WorkflowStepDTO {
	dto := WorkflowStepDTO{Action: s.Action}
	if s.GatewayToolID.Valid {
		dto.GatewayToolID = uuid.UUID(s.GatewayToolID.Bytes).String()
	}
	if s.ToolName.Valid {
		dto.ToolName = s.ToolName.String
	}
	return dto
}

var validWorkflowStatuses = map[string]bool{"active": true, "draft": true, "disabled": true}

// ── Handlers ──────────────────────────────────────────────────────────────────

// ListWorkflows handles GET /v1/gateway/workflows
func (s *Server) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "workflow store is not configured")
		return
	}
	rows, err := s.queries.ListWorkflows(r.Context(), uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	resp := make([]WorkflowResp, 0, len(rows))
	for _, wf := range rows {
		resp = append(resp, workflowToResp(wf))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": resp})
}

// GetWorkflow handles GET /v1/gateway/workflows/{workflowId}
func (s *Server) GetWorkflow(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "workflowId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workflowId")
		return
	}
	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "workflow store is not configured")
		return
	}

	_, resp, ok := s.loadWorkflowWithSteps(w, r, id, uc.OrgID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// loadWorkflowWithSteps fetches a workflow (org-scoped) plus its ordered
// steps, writing a 404/500 response and returning ok=false on any
// failure. Shared by GetWorkflow and the Create/Update handlers' final
// response (both need the exact same "workflow + steps" shape after a
// write).
func (s *Server) loadWorkflowWithSteps(w http.ResponseWriter, r *http.Request, id, orgID uuid.UUID) (store.Workflow, WorkflowResp, bool) {
	wf, err := s.queries.GetWorkflow(r.Context(), id, orgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "workflow not found")
		} else {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return store.Workflow{}, WorkflowResp{}, false
	}
	steps, err := s.queries.ListWorkflowSteps(r.Context(), wf.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return store.Workflow{}, WorkflowResp{}, false
	}
	resp := workflowToResp(*wf)
	resp.Steps = make([]WorkflowStepDTO, 0, len(steps))
	for _, st := range steps {
		resp.Steps = append(resp.Steps, workflowStepToDTO(st))
	}
	return *wf, resp, true
}

// validateWorkflowSteps checks every step's gateway_tool_id is a
// non-empty, well-formed UUID that resolves to a real gateway_tools row
// IN THE CALLER'S OWN ORG. This is the load-bearing cross-org guard: the
// FK constraint alone only proves the id exists *somewhere* in
// gateway_tools, never that it belongs to this org -- without this
// explicit check, org A could reference org B's connector id directly
// (AC3). Returns a client-facing error message on the first invalid step
// (400), or nil if every step is valid.
func (s *Server) validateWorkflowSteps(r *http.Request, orgID uuid.UUID, steps []WorkflowStepDTO) ([]uuid.UUID, string) {
	if len(steps) == 0 {
		return nil, "at least one step is required"
	}
	toolIDs := make([]uuid.UUID, 0, len(steps))
	for i, st := range steps {
		idx := strconv.Itoa(i)
		if st.Action == "" {
			return nil, "step " + idx + ": action is required"
		}
		toolID, err := uuid.Parse(st.GatewayToolID)
		if err != nil {
			return nil, "step " + idx + ": gateway_tool_id is required and must be a valid UUID"
		}
		belongs, err := s.queries.ToolBelongsToOrg(r.Context(), toolID, orgID)
		if err != nil {
			return nil, "step " + idx + ": failed to validate connector"
		}
		if !belongs {
			return nil, "step " + idx + ": gateway_tool_id does not reference a connector in your organization"
		}
		toolIDs = append(toolIDs, toolID)
	}
	return toolIDs, ""
}

// CreateWorkflow handles POST /v1/gateway/workflows
func (s *Server) CreateWorkflow(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "workflow store is not configured")
		return
	}

	var body WorkflowCreateRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	status := body.Status
	if status == "" {
		status = "draft"
	} else if !validWorkflowStatuses[status] {
		writeError(w, http.StatusBadRequest, "bad_request", `status must be "active", "draft", or "disabled"`)
		return
	}

	toolIDs, errMsg := s.validateWorkflowSteps(r, uc.OrgID, body.Steps)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, "bad_request", errMsg)
		return
	}

	tx, err := s.queries.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not start transaction")
		return
	}
	defer tx.Rollback(r.Context()) // no-op once Commit has succeeded

	txQueries := s.queries.WithTx(tx)
	createdBy := uc.UserID
	wf, err := txQueries.CreateWorkflow(r.Context(), store.CreateWorkflowParams{
		OrgID:     uc.OrgID,
		Name:      body.Name,
		Status:    status,
		CreatedBy: &createdBy,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	for i, toolID := range toolIDs {
		if err := txQueries.InsertWorkflowStep(r.Context(), wf.ID, int32(i), toolID, body.Steps[i].Action); err != nil {
			if isForeignKeyViolation(err) {
				writeError(w, http.StatusBadRequest, "bad_request", "one of the referenced connectors no longer exists -- it may have been deleted while this request was in flight")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not save workflow")
		return
	}

	_, resp, ok := s.loadWorkflowWithSteps(w, r, wf.ID, uc.OrgID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// UpdateWorkflow handles PATCH /v1/gateway/workflows/{workflowId}. Partial
// update: name/status only change what's present in the body. steps, if
// present at all (including an explicit empty array), fully replaces the
// existing step list inside the same transaction -- deliberately not a
// diff/patch-individual-steps mechanism, matching action_paths' "always
// sent, replace whole map" precedent (B-045) for this class of nested-
// collection update.
func (s *Server) UpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "workflowId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workflowId")
		return
	}
	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "workflow store is not configured")
		return
	}

	var body WorkflowUpdateRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.Name != nil && *body.Name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name cannot be blank")
		return
	}
	if body.Status != nil && !validWorkflowStatuses[*body.Status] {
		writeError(w, http.StatusBadRequest, "bad_request", `status must be "active", "draft", or "disabled"`)
		return
	}

	var toolIDs []uuid.UUID
	var stepActions []string
	if body.Steps != nil {
		var errMsg string
		toolIDs, errMsg = s.validateWorkflowSteps(r, uc.OrgID, *body.Steps)
		if errMsg != "" {
			writeError(w, http.StatusBadRequest, "bad_request", errMsg)
			return
		}
		stepActions = make([]string, len(*body.Steps))
		for i, st := range *body.Steps {
			stepActions[i] = st.Action
		}
	}

	// Confirm the workflow exists and belongs to this org BEFORE opening a
	// transaction -- UpdateWorkflow's own WHERE id=$1 AND org_id=$2 would
	// catch a cross-org/missing id too, but failing fast here avoids
	// starting a transaction (and, for a steps update, deleting the old
	// step rows) for a request that can never succeed.
	if _, err := s.queries.GetWorkflow(r.Context(), id, uc.OrgID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "workflow not found")
		} else {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}

	tx, err := s.queries.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	txQueries := s.queries.WithTx(tx)
	// Captured (not discarded) -- reused directly for the response below
	// instead of a second GetWorkflow after commit, which would just
	// re-read the exact row this RETURNING clause already produced (code
	// review finding: an earlier version threw this away and re-fetched).
	updated, err := txQueries.UpdateWorkflow(r.Context(), store.UpdateWorkflowParams{
		ID: id, OrgID: uc.OrgID, Name: body.Name, Status: body.Status,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if body.Steps != nil {
		if err := txQueries.DeleteWorkflowSteps(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		for i, toolID := range toolIDs {
			if err := txQueries.InsertWorkflowStep(r.Context(), id, int32(i), toolID, stepActions[i]); err != nil {
				if isForeignKeyViolation(err) {
					writeError(w, http.StatusBadRequest, "bad_request", "one of the referenced connectors no longer exists -- it may have been deleted while this request was in flight")
					return
				}
				writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
				return
			}
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not save workflow")
		return
	}

	// Steps always re-fetched fresh (even when body.Steps was nil) since
	// GetWorkflow/ListWorkflowSteps' joined tool_name -- e.g. from a tool
	// renamed concurrently -- must reflect the current state either way;
	// only the redundant workflow-row re-fetch is what's eliminated here.
	steps, err := s.queries.ListWorkflowSteps(r.Context(), updated.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	resp := workflowToResp(*updated)
	resp.Steps = make([]WorkflowStepDTO, 0, len(steps))
	for _, st := range steps {
		resp.Steps = append(resp.Steps, workflowStepToDTO(st))
	}
	writeJSON(w, http.StatusOK, resp)
}

// DeleteWorkflow handles DELETE /v1/gateway/workflows/{workflowId}.
// Unconditional -- workflow_steps cascade-delete via ON DELETE CASCADE
// (migration 000006). This never touches gateway_tools.
func (s *Server) DeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "workflowId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workflowId")
		return
	}
	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "workflow store is not configured")
		return
	}
	found, err := s.queries.DeleteWorkflow(r.Context(), uc.OrgID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "workflow not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
