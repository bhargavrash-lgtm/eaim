// Multi-Hop Workflows (Thread B): definition CRUD only. Nothing here
// executes a workflow -- eami-gateway/internal/workflow does that,
// reading these same tables directly; this file never calls out to
// eami-gateway at all. Originally schema+CRUD foundation only (B-058);
// B-063 additively extended WorkflowStepDTO/InsertWorkflowStep with a
// caller-reusable step id and input_mapping passthrough so a step's
// output->input extraction wiring (resolved at execution time in
// eami-gateway) survives a full-replace UpdateWorkflow save -- see that
// entry's own comments below for the full reasoning. See BACKLOG.md's
// Thread B entry for the rest of the epic (durable escalation
// pause/resume, chain-aware approval UI, audit linkage).
//
// api/openapi.yaml deliberately not touched -- ships undocumented,
// matching B-038/B-045's precedent (openapi.yaml is Architect-EAMI-owned
// per BOUNDARIES.md/CLAUDE.md).
package api

import (
	"encoding/json"
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

// ExtractionRefDTO (B-063) is one parameter's extraction reference: a
// prior step's own stable id plus a gjson path into that step's real
// recorded execution result. Not validated against the workflow's own
// step set at write time (from_step could name a step not yet created
// in the same request, a later step, or nothing at all) -- deliberately
// deferred to execution-time resolution in eami-gateway/internal/
// workflow, which already fails cleanly and correctly for every one of
// those cases (see resolveParams). Mirrors action_paths' own established
// precedent of validating shape here but not cross-referencing meaning
// until dispatch time.
type ExtractionRefDTO struct {
	FromStep string `json:"from_step"`
	Path     string `json:"path"`
}

// WorkflowStepDTO is one step in a workflow request/response body. ID
// (B-063) is always populated in a response (the real workflow_steps.id)
// and, if echoed back unchanged in a later UpdateWorkflow request, lets
// that step's real database identity survive the full-replace update
// path instead of being deleted and re-minted with a fresh id -- see
// UpdateWorkflow's own comment for why that stability matters (a
// reused id is what makes InputMapping's from_step references durable
// across a save, including a pure reorder). Omitting ID (or echoing an
// id that isn't a real step of THIS workflow) is always treated as "this
// is a new step" -- exactly today's behavior for any caller that has
// never seen this field, e.g. CreateWorkflow, or an external API caller
// that hasn't adopted it yet.
type WorkflowStepDTO struct {
	ID            string                      `json:"id,omitempty"`
	GatewayToolID string                      `json:"gateway_tool_id"`
	ToolName      string                      `json:"tool_name,omitempty"` // response-only, populated from the joined gateway_tools.name
	Action        string                      `json:"action"`
	InputMapping  map[string]ExtractionRefDTO `json:"input_mapping,omitempty"`
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
	dto := WorkflowStepDTO{ID: s.ID.String(), Action: s.Action}
	if s.GatewayToolID.Valid {
		dto.GatewayToolID = uuid.UUID(s.GatewayToolID.Bytes).String()
	}
	if s.ToolName.Valid {
		dto.ToolName = s.ToolName.String
	}
	if len(s.InputMapping) > 0 {
		// Malformed stored JSON would only happen via a direct DB write
		// bypassing this API's own marshaling -- surfacing it as "no
		// mapping" rather than a 500 keeps GetWorkflow/ListWorkflows
		// resilient to that, matching toolToResp's identical precedent
		// (tools.go) for the same class of defensive unmarshal.
		_ = json.Unmarshal(s.InputMapping, &dto.InputMapping)
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
// (AC3). Also does minimal shape validation of input_mapping (B-063):
// well-formed from_step UUID and non-empty path, if present -- NOT a
// check that from_step names a real/earlier/same-workflow step, which
// is deliberately deferred to execution-time resolution (see
// ExtractionRefDTO's doc comment). Returns a client-facing error message
// on the first invalid step (400), or nil if every step is valid.
func (s *Server) validateWorkflowSteps(r *http.Request, orgID uuid.UUID, steps []WorkflowStepDTO) ([]uuid.UUID, [][]byte, string) {
	if len(steps) == 0 {
		return nil, nil, "at least one step is required"
	}
	toolIDs := make([]uuid.UUID, 0, len(steps))
	inputMappings := make([][]byte, 0, len(steps))
	// seenIDs catches the same echoed step id submitted on more than one
	// step in this request -- without this, resolveStepIDs would assign
	// that id to both, and the second INSERT would hit workflow_steps'
	// primary-key uniqueness (23505), a code not handled by
	// isForeignKeyViolation (23503 only), surfacing as a raw 500 with a
	// leaked Postgres error string instead of a clean 400 (code review
	// finding).
	seenIDs := make(map[string]bool, len(steps))
	for i, st := range steps {
		idx := strconv.Itoa(i)
		if st.Action == "" {
			return nil, nil, "step " + idx + ": action is required"
		}
		if st.ID != "" {
			if seenIDs[st.ID] {
				return nil, nil, "step " + idx + ": id " + st.ID + " is echoed on more than one step"
			}
			seenIDs[st.ID] = true
		}
		toolID, err := uuid.Parse(st.GatewayToolID)
		if err != nil {
			return nil, nil, "step " + idx + ": gateway_tool_id is required and must be a valid UUID"
		}
		belongs, err := s.queries.ToolBelongsToOrg(r.Context(), toolID, orgID)
		if err != nil {
			return nil, nil, "step " + idx + ": failed to validate connector"
		}
		if !belongs {
			return nil, nil, "step " + idx + ": gateway_tool_id does not reference a connector in your organization"
		}
		toolIDs = append(toolIDs, toolID)

		var inputMappingJSON []byte
		if len(st.InputMapping) > 0 {
			for param, ref := range st.InputMapping {
				if _, err := uuid.Parse(ref.FromStep); err != nil {
					return nil, nil, "step " + idx + ": input_mapping[" + param + "].from_step must be a valid UUID"
				}
				if ref.Path == "" {
					return nil, nil, "step " + idx + ": input_mapping[" + param + "].path is required"
				}
			}
			inputMappingJSON, err = json.Marshal(st.InputMapping)
			if err != nil {
				return nil, nil, "step " + idx + ": failed to encode input_mapping"
			}
		}
		inputMappings = append(inputMappings, inputMappingJSON)
	}
	return toolIDs, inputMappings, ""
}

// resolveStepIDs (B-063) decides, per submitted step, whether to reuse
// an existing workflow_steps.id or mint a fresh one -- the mechanism
// that makes InputMapping's from_step references (and a step's own
// input_mapping) durable across a full-replace UpdateWorkflow save,
// including a pure reorder. A submitted id is reused only if it's a
// real, currently-existing step id of THIS workflow (existingIDs);
// anything else (omitted, malformed, belongs to a different workflow or
// a step that no longer exists) is treated as a new step and gets a
// fresh id -- exactly today's behavior for a caller that never sends
// this field at all.
func resolveStepIDs(steps []WorkflowStepDTO, existingIDs map[uuid.UUID]bool) []uuid.UUID {
	out := make([]uuid.UUID, len(steps))
	for i, st := range steps {
		if id, err := uuid.Parse(st.ID); err == nil && existingIDs[id] {
			out[i] = id
			continue
		}
		out[i] = uuid.New()
	}
	return out
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

	toolIDs, inputMappings, errMsg := s.validateWorkflowSteps(r, uc.OrgID, body.Steps)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, "bad_request", errMsg)
		return
	}
	// CreateWorkflow has no pre-existing steps to reuse ids from -- every
	// step is new, always a fresh id (resolveStepIDs with an empty
	// existingIDs set does exactly this, but a direct uuid.New() per step
	// is simpler and equally correct here).
	stepIDs := make([]uuid.UUID, len(body.Steps))
	for i := range stepIDs {
		stepIDs[i] = uuid.New()
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
		if err := txQueries.InsertWorkflowStep(r.Context(), stepIDs[i], wf.ID, int32(i), toolID, body.Steps[i].Action, inputMappings[i]); err != nil {
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
	var inputMappings [][]byte
	if body.Steps != nil {
		var errMsg string
		toolIDs, inputMappings, errMsg = s.validateWorkflowSteps(r, uc.OrgID, *body.Steps)
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

	// B-063: read the CURRENT step ids of this workflow before replacing
	// them, so a submitted step that echoes one back can reuse its real
	// database identity instead of always getting a fresh id -- the
	// mechanism that makes input_mapping's from_step references (and a
	// step's own input_mapping) durable across this full-replace save,
	// including a pure reorder. A non-transactional read here is safe:
	// worst case under a concurrent edit, a step's id-reuse decision is
	// made against slightly stale data, but the replace itself still runs
	// correctly and atomically inside the transaction below either way --
	// this is only ever a "reuse vs. fresh id" choice, never a correctness
	// hazard for the write itself.
	// carriedParams (B-063, found live during this brief's own
	// verification, not part of the originally approved plan) captures
	// the CURRENT static params (workflow_step_params, B-059) of any step
	// whose id is about to be reused, before DeleteWorkflowSteps's
	// cascade (workflow_step_params.workflow_step_id ON DELETE CASCADE,
	// migration 000007) removes them. Without this, a reused step id
	// would preserve its own identity and input_mapping (this brief's own
	// fix) but silently lose its static params on the very same save --
	// the identical class of bug the id-stability finding described,
	// just on workflow_step_params instead of workflow_steps.
	// input_mapping, and just as real: reproduced live while verifying
	// this brief (a step's params round-tripped to `{}` after an
	// unrelated sibling-step edit that happened to reuse this step's id).
	var stepIDs []uuid.UUID
	carriedParams := make(map[uuid.UUID][]byte)
	if body.Steps != nil {
		existingSteps, err := s.queries.ListWorkflowSteps(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		existingIDs := make(map[uuid.UUID]bool, len(existingSteps))
		for _, st := range existingSteps {
			existingIDs[st.ID] = true
		}
		stepIDs = resolveStepIDs(*body.Steps, existingIDs)
		for _, sid := range stepIDs {
			if !existingIDs[sid] {
				continue // a genuinely new step has no prior params to carry
			}
			raw, err := s.queries.GetWorkflowStepParams(r.Context(), sid, uc.OrgID)
			if err != nil {
				if !errors.Is(err, pgx.ErrNoRows) {
					writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
					return
				}
				continue // no params row existed for this step -- nothing to carry
			}
			carriedParams[sid] = raw
		}
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
			if err := txQueries.InsertWorkflowStep(r.Context(), stepIDs[i], id, int32(i), toolID, stepActions[i], inputMappings[i]); err != nil {
				if isForeignKeyViolation(err) {
					writeError(w, http.StatusBadRequest, "bad_request", "one of the referenced connectors no longer exists -- it may have been deleted while this request was in flight")
					return
				}
				writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
				return
			}
			// Re-attach any static params carried forward from this same
			// step id's PRE-delete row -- restores what DeleteWorkflowSteps'
			// cascade just removed, atomically within this same
			// transaction (so a failure anywhere in this loop still rolls
			// back the whole replace, params included).
			if raw, ok := carriedParams[stepIDs[i]]; ok {
				if _, err := txQueries.UpsertWorkflowStepParams(r.Context(), stepIDs[i], uc.OrgID, raw); err != nil {
					writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
					return
				}
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
