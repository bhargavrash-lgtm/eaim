package store

import (
	"context"

	"github.com/google/uuid"
)

// Multi-Hop Workflows Brief 2 (B-059): static per-step parameters.
// workflow_step_params is a separate table from workflow_steps
// (migration 000007) -- see that migration's own comment for why. This
// file is new, not an addition to tools.sql.go/workflows.sql.go, keeping
// B-058's existing files untouched.

const workflowStepBelongsToOrgQuery = `
SELECT EXISTS(
    SELECT 1 FROM workflow_steps ws
    JOIN workflows w ON w.id = ws.workflow_id
    WHERE ws.id = $1 AND w.org_id = $2
)`

// WorkflowStepBelongsToOrg confirms stepID is a real workflow_steps row
// belonging to a workflow owned by orgID -- the same load-bearing
// cross-org guard shape as ToolBelongsToOrg (workflows.sql.go), applied
// here since workflow_step_params has no org_id of its own (scoped
// transitively via workflow_step_id -> workflow_id -> org_id, same
// no-org_id-column convention workflow_steps itself already follows).
func (q *Queries) WorkflowStepBelongsToOrg(ctx context.Context, stepID, orgID uuid.UUID) (bool, error) {
	var exists bool
	err := q.db.QueryRow(ctx, workflowStepBelongsToOrgQuery, toPgtypeUUID(stepID), toPgtypeUUID(orgID)).Scan(&exists)
	return exists, err
}

const getWorkflowStepParamsQuery = `
SELECT wsp.params
FROM workflow_step_params wsp
JOIN workflow_steps ws ON ws.id = wsp.workflow_step_id
JOIN workflows w ON w.id = ws.workflow_id
WHERE wsp.workflow_step_id = $1 AND w.org_id = $2`

// GetWorkflowStepParams returns the raw stored params JSON for a step,
// org-scoped via the same join as WorkflowStepBelongsToOrg. Returns
// pgx.ErrNoRows both when the step doesn't belong to this org AND when no
// params row exists yet for an otherwise-valid step -- the handler
// distinguishes those two cases by calling WorkflowStepBelongsToOrg first
// (mirrors GetWorkflow's own "404 either way, never confirm existence in
// another org" convention).
func (q *Queries) GetWorkflowStepParams(ctx context.Context, stepID, orgID uuid.UUID) ([]byte, error) {
	var raw []byte
	err := q.db.QueryRow(ctx, getWorkflowStepParamsQuery, toPgtypeUUID(stepID), toPgtypeUUID(orgID)).Scan(&raw)
	return raw, err
}

const upsertWorkflowStepParamsQuery = `
INSERT INTO workflow_step_params (workflow_step_id, params)
SELECT $1, $3
WHERE EXISTS (
    SELECT 1 FROM workflow_steps ws
    JOIN workflows w ON w.id = ws.workflow_id
    WHERE ws.id = $1 AND w.org_id = $2
)
ON CONFLICT (workflow_step_id) DO UPDATE SET params = EXCLUDED.params
RETURNING params`

// UpsertWorkflowStepParams sets (replacing wholesale, no partial-merge)
// the static params for a step, in one atomic statement that also
// enforces the org-ownership guard -- the INSERT...SELECT...WHERE EXISTS
// shape means a cross-org stepID inserts/updates nothing at all (not a
// separate check-then-write, which would leave a TOCTOU window between
// the two). Returns pgx.ErrNoRows if stepID doesn't belong to orgID (or
// doesn't exist).
func (q *Queries) UpsertWorkflowStepParams(ctx context.Context, stepID, orgID uuid.UUID, params []byte) ([]byte, error) {
	var raw []byte
	err := q.db.QueryRow(ctx, upsertWorkflowStepParamsQuery, toPgtypeUUID(stepID), toPgtypeUUID(orgID), params).Scan(&raw)
	return raw, err
}
