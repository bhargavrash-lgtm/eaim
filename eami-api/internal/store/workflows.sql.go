package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Multi-Hop Workflows (Thread B), Brief 1 (B-058). Mirrors what sqlc would
// generate from internal/store/query/workflows.sql -- hand-written because
// the sqlc CLI is not available on this machine (confirmed directly), the
// same reason tools.sql.go is hand-written. Regenerate for real with
// `sqlc generate` on a machine that has it, once workflows.sql exists as
// the canonical source (it does, see query/workflows.sql).
//
// Every method here is a plain (q *Queries) method backed by q.db (a
// DBTX -- either *pgxpool.Pool or, via Queries.WithTx, a pgx.Tx), so the
// same methods work identically inside or outside a transaction. The
// handler-level multi-step write (CreateWorkflow's org-ownership
// validation + workflow row + N step rows, and the equivalent replace-all
// path in UpdateWorkflow) needs that: it runs entirely inside one
// transaction via Queries.WithTx(tx), reusing these exact methods rather
// than duplicating SQL for a transactional vs non-transactional path.

const listWorkflowsQuery = `-- name: ListWorkflows :many
SELECT
    w.id, w.org_id, w.name, w.status, w.created_by, w.created_at, w.updated_at,
    COUNT(ws.id) AS step_count
FROM workflows w
LEFT JOIN workflow_steps ws ON ws.workflow_id = w.id
WHERE w.org_id = $1
GROUP BY w.id
ORDER BY w.name ASC
`

// ListWorkflows returns every workflow for an org, with a joined step
// count (not the steps themselves -- see ListWorkflowSteps for those).
func (q *Queries) ListWorkflows(ctx context.Context, orgID uuid.UUID) ([]Workflow, error) {
	rows, err := q.db.Query(ctx, listWorkflowsQuery, toPgtypeUUID(orgID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Workflow
	for rows.Next() {
		var w Workflow
		if err := rows.Scan(
			&w.ID, &w.OrgID, &w.Name, &w.Status, &w.CreatedBy, &w.CreatedAt, &w.UpdatedAt,
			&w.StepCount,
		); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

const getWorkflowQuery = `-- name: GetWorkflow :one
SELECT id, org_id, name, status, created_by, created_at, updated_at
FROM workflows
WHERE id = $1 AND org_id = $2
LIMIT 1
`

// GetWorkflow returns a single workflow scoped to orgID (never trusts a
// caller-supplied org). Returns pgx.ErrNoRows if not found or not owned
// by this org -- callers must not distinguish those two cases in the
// response (404 either way), matching every other resource's org-
// isolation convention in this package.
func (q *Queries) GetWorkflow(ctx context.Context, id, orgID uuid.UUID) (*Workflow, error) {
	var w Workflow
	err := q.db.QueryRow(ctx, getWorkflowQuery, toPgtypeUUID(id), toPgtypeUUID(orgID)).Scan(
		&w.ID, &w.OrgID, &w.Name, &w.Status, &w.CreatedBy, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

const listWorkflowStepsQuery = `-- name: ListWorkflowSteps :many
SELECT
    ws.id, ws.workflow_id, ws.step_order, ws.gateway_tool_id,
    gt.name AS tool_name, ws.action, ws.input_mapping
FROM workflow_steps ws
LEFT JOIN gateway_tools gt ON gt.id = ws.gateway_tool_id
WHERE ws.workflow_id = $1
ORDER BY ws.step_order ASC
`

// ListWorkflowSteps returns a workflow's steps in order, LEFT JOINed with
// gateway_tools for display. gateway_tool_id/tool_name both come back
// invalid (NULL) for a step whose connector was deleted (ON DELETE SET
// NULL, migration 000006) -- deliberately visible to the caller, not
// filtered out or defaulted, so an admin editing the workflow can see the
// broken step and fix it.
//
// Not org-scoped by its own WHERE clause -- callers must have already
// verified workflowID belongs to the caller's org (via GetWorkflow) before
// calling this, exactly as ListWorkflowSteps has no independent way to
// check org ownership (workflow_steps carries no org_id column at all, by
// design -- see migration 000006's comment).
func (q *Queries) ListWorkflowSteps(ctx context.Context, workflowID uuid.UUID) ([]WorkflowStep, error) {
	rows, err := q.db.Query(ctx, listWorkflowStepsQuery, toPgtypeUUID(workflowID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WorkflowStep
	for rows.Next() {
		var s WorkflowStep
		if err := rows.Scan(
			&s.ID, &s.WorkflowID, &s.StepOrder, &s.GatewayToolID,
			&s.ToolName, &s.Action, &s.InputMapping,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CreateWorkflowParams holds fields for inserting a new workflow row
// (steps are inserted separately via InsertWorkflowStep, in the same
// transaction -- see workflows.go's CreateWorkflow handler).
type CreateWorkflowParams struct {
	OrgID     uuid.UUID
	Name      string
	Status    string
	CreatedBy *uuid.UUID
}

const createWorkflowQuery = `-- name: CreateWorkflow :one
INSERT INTO workflows (org_id, name, status, created_by)
VALUES ($1, $2, $3, $4)
RETURNING id, org_id, name, status, created_by, created_at, updated_at
`

func (q *Queries) CreateWorkflow(ctx context.Context, p CreateWorkflowParams) (*Workflow, error) {
	var createdBy pgtype.UUID
	if p.CreatedBy != nil {
		createdBy = toPgtypeUUID(*p.CreatedBy)
	}
	var w Workflow
	err := q.db.QueryRow(ctx, createWorkflowQuery,
		toPgtypeUUID(p.OrgID), p.Name, p.Status, createdBy,
	).Scan(&w.ID, &w.OrgID, &w.Name, &w.Status, &w.CreatedBy, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

const insertWorkflowStepQuery = `-- name: InsertWorkflowStep :exec
INSERT INTO workflow_steps (id, workflow_id, step_order, gateway_tool_id, action, input_mapping)
VALUES ($1, $2, $3, $4, $5, $6)
`

// InsertWorkflowStep inserts one step row with a caller-assigned id
// (B-063 -- previously DB DEFAULT-generated) so UpdateWorkflow can reuse
// an existing step's real id across a full-replace save instead of
// always minting a fresh one, which is what makes a workflow_steps.id
// stable enough for another step's input_mapping to reference. stepOrder
// is always caller-assigned from array position (workflows.go), never
// client-supplied directly, so there is no duplicate/gap ordering case
// to validate here. inputMapping is raw JSONB passthrough, nil = NULL.
func (q *Queries) InsertWorkflowStep(ctx context.Context, id, workflowID uuid.UUID, stepOrder int32, gatewayToolID uuid.UUID, action string, inputMapping []byte) error {
	_, err := q.db.Exec(ctx, insertWorkflowStepQuery,
		toPgtypeUUID(id), toPgtypeUUID(workflowID), stepOrder, toPgtypeUUID(gatewayToolID), action, inputMapping,
	)
	return err
}

const deleteWorkflowStepsQuery = `-- name: DeleteWorkflowSteps :exec
DELETE FROM workflow_steps WHERE workflow_id = $1
`

// DeleteWorkflowSteps removes every step for a workflow -- the "delete"
// half of UpdateWorkflow's full-replace semantics when a PATCH includes a
// new steps array (workflows.go).
func (q *Queries) DeleteWorkflowSteps(ctx context.Context, workflowID uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteWorkflowStepsQuery, toPgtypeUUID(workflowID))
	return err
}

// UpdateWorkflowParams holds fields for a partial workflow update. Nil
// fields leave the column unchanged (COALESCE), matching UpdateTool's
// established convention.
type UpdateWorkflowParams struct {
	ID     uuid.UUID
	OrgID  uuid.UUID
	Name   *string
	Status *string
}

const updateWorkflowQuery = `-- name: UpdateWorkflow :one
UPDATE workflows SET
    name       = COALESCE($3, name),
    status     = COALESCE($4, status),
    updated_at = NOW()
WHERE id = $1 AND org_id = $2
RETURNING id, org_id, name, status, created_by, created_at, updated_at
`

func (q *Queries) UpdateWorkflow(ctx context.Context, p UpdateWorkflowParams) (*Workflow, error) {
	var w Workflow
	err := q.db.QueryRow(ctx, updateWorkflowQuery,
		toPgtypeUUID(p.ID), toPgtypeUUID(p.OrgID), toPgtypeText(p.Name), toPgtypeText(p.Status),
	).Scan(&w.ID, &w.OrgID, &w.Name, &w.Status, &w.CreatedBy, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

const deleteWorkflowQuery = `-- name: DeleteWorkflow :exec
DELETE FROM workflows WHERE id = $1 AND org_id = $2
`

// DeleteWorkflow removes a workflow by ID scoped to an org. Steps cascade
// via workflow_steps.workflow_id's ON DELETE CASCADE (migration 000006) --
// a workflow deleting its own steps is uncontroversial, unlike deleting a
// shared gateway_tools row (which uses ON DELETE SET NULL instead; see
// that migration's comment). Returns false if not found/not owned.
func (q *Queries) DeleteWorkflow(ctx context.Context, orgID, workflowID uuid.UUID) (bool, error) {
	tag, err := q.db.Exec(ctx, deleteWorkflowQuery, toPgtypeUUID(workflowID), toPgtypeUUID(orgID))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

const toolBelongsToOrgQuery = `-- name: ToolBelongsToOrg :one
SELECT EXISTS(SELECT 1 FROM gateway_tools WHERE id = $1 AND org_id = $2)
`

// ToolBelongsToOrg is the load-bearing cross-org guard for
// CreateWorkflow/UpdateWorkflow: a workflow_steps.gateway_tool_id FK alone
// only proves the id exists *somewhere* in gateway_tools, not that it
// belongs to the caller's own org -- without this check, org A could
// create a workflow step referencing org B's connector id directly.
func (q *Queries) ToolBelongsToOrg(ctx context.Context, toolID, orgID uuid.UUID) (bool, error) {
	var exists bool
	err := q.db.QueryRow(ctx, toolBelongsToOrgQuery, toPgtypeUUID(toolID), toPgtypeUUID(orgID)).Scan(&exists)
	return exists, err
}
