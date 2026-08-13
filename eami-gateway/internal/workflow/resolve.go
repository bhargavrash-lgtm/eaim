package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrDanglingStep is returned by Resolve when a workflow has a step whose
// connector was deleted (gateway_tool_id NULL, B-058's ON DELETE SET
// NULL). Such a workflow is a valid, viewable definition (B-058's own UI
// surfaces this for an admin to fix) but is not executable -- there is
// nothing to dispatch that step to. Fails the whole run before any step
// executes, rather than running steps 1..N-1 and only then discovering
// step N can't run.
var ErrDanglingStep = errors.New("workflow: a step references a deleted connector and cannot execute")

// ErrWorkflowNotFound is returned when workflowID doesn't resolve to a
// real workflow in orgID -- never distinguishes "doesn't exist" from
// "belongs to another org", matching this codebase's established
// cross-org convention (never confirm existence in another org).
var ErrWorkflowNotFound = errors.New("workflow: not found")

// Resolve reads a workflow and its ordered steps (with static params)
// directly via the gateway's own pool -- mirrors toolrouter.Resolve's
// established pattern of the gateway reading eami-api-owned tables
// directly (both services share one physical Postgres in this
// deployment model; gateway_tools/policies/approval_requests are already
// read this way).
func Resolve(ctx context.Context, pool *pgxpool.Pool, orgID, workflowID uuid.UUID) (*Definition, error) {
	def := &Definition{WorkflowID: workflowID, OrgID: orgID}
	err := pool.QueryRow(ctx, `
		SELECT name FROM workflows WHERE id = $1 AND org_id = $2
	`, workflowID, orgID).Scan(&def.Name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWorkflowNotFound
		}
		return nil, fmt.Errorf("workflow: resolve workflow: %w", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT ws.id, ws.step_order, ws.gateway_tool_id, ws.action,
		       COALESCE(wsp.params, '{}'::jsonb), ws.input_mapping
		FROM workflow_steps ws
		LEFT JOIN workflow_step_params wsp ON wsp.workflow_step_id = ws.id
		WHERE ws.workflow_id = $1
		ORDER BY ws.step_order ASC
	`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("workflow: resolve steps: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var st Step
		var gatewayToolID *uuid.UUID
		var paramsRaw, inputMappingRaw []byte
		if err := rows.Scan(&st.WorkflowStepID, &st.StepOrder, &gatewayToolID, &st.Action, &paramsRaw, &inputMappingRaw); err != nil {
			return nil, fmt.Errorf("workflow: scan step: %w", err)
		}
		if gatewayToolID == nil {
			return nil, fmt.Errorf("%w (step_order=%d)", ErrDanglingStep, st.StepOrder)
		}
		st.GatewayToolID = *gatewayToolID
		if len(paramsRaw) > 0 {
			if err := json.Unmarshal(paramsRaw, &st.Params); err != nil {
				return nil, fmt.Errorf("workflow: unmarshal step %d params: %w", st.StepOrder, err)
			}
		}
		if len(inputMappingRaw) > 0 {
			if err := json.Unmarshal(inputMappingRaw, &st.InputMapping); err != nil {
				return nil, fmt.Errorf("workflow: unmarshal step %d input_mapping: %w", st.StepOrder, err)
			}
		}
		def.Steps = append(def.Steps, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow: iterate steps: %w", err)
	}
	if len(def.Steps) == 0 {
		return nil, fmt.Errorf("workflow: %q has no steps", def.Name)
	}
	return def, nil
}
