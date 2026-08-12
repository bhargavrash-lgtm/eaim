package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/approval"
	"github.com/eami/gateway/internal/mcp"
	policy "github.com/eami/policy"
)

// Executor runs a workflow by calling the gateway's existing dispatch
// closure once per step, in order.
type Executor struct {
	pool       *pgxpool.Pool
	dispatch   mcp.DecisionHandler
	policyEval policy.Evaluator
}

// New creates an Executor. dispatch is the SAME closure cmd/gateway/
// main.go builds and wires into mcp.NewHandler -- passed by value (it's
// just a function value), not reimplemented or wrapped, so every step
// gets exactly the resolve+policy+TOCTOU-pin+dispatch+audit+episode
// behavior a standalone MCP tool_call would get, completely unmodified.
// policyEval is the same *pLoader.Evaluator() dispatch itself calls,
// used here only for an informational pre-dispatch decision preview
// (never for enforcement -- see StepResult.ProjectedDecision's comment).
func New(pool *pgxpool.Pool, dispatch mcp.DecisionHandler, policyEval policy.Evaluator) *Executor {
	return &Executor{pool: pool, dispatch: dispatch, policyEval: policyEval}
}

// Run executes workflowID's steps in order against template's agent
// identity (OrgID/AgentUUID/AgentName/AgentScope/SessionID/Environment --
// Tool/Action/Parameters are ignored and overwritten per-step). Returns
// an error only when the run could not be attempted at all (workflow not
// found, a step's connector was deleted at definition time -- see
// ErrDanglingStep --, or a DB failure creating the run record) -- a run
// that executes and then legitimately stops on a denied/blocked step is
// NOT an error: that outcome is fully captured in the returned
// RunResult.Status/Steps, the same way a single denied MCP call isn't a
// transport-level error either.
//
// TOCTOU pinning is per-hop by construction: each step independently
// calls resolveConnectorByID (for this package's own audit record) and
// then dispatch() (which does its own fully independent resolve+pin+
// enforce) immediately before that step runs -- there is no phase where
// every step's connector is resolved upfront, so there is nothing to go
// stale across steps. See connector.go/dispatch's own TOCTOU pinning
// (approval/router.go, B-057) for the actual enforcement mechanism,
// completely untouched here.
func (e *Executor) Run(ctx context.Context, template mcp.ActionContext, workflowID uuid.UUID) (*RunResult, error) {
	orgID, err := uuid.Parse(template.OrgID)
	if err != nil {
		return nil, fmt.Errorf("workflow: invalid org id in template: %w", err)
	}
	agentID, err := uuid.Parse(template.AgentUUID)
	if err != nil {
		return nil, fmt.Errorf("workflow: invalid agent id in template: %w", err)
	}

	def, err := Resolve(ctx, e.pool, orgID, workflowID)
	if err != nil {
		return nil, err
	}

	runID := uuid.New()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO workflow_runs (id, org_id, workflow_id, agent_id, status)
		VALUES ($1, $2, $3, $4, 'running')
	`, runID, orgID, def.WorkflowID, agentID); err != nil {
		return nil, fmt.Errorf("workflow: create run: %w", err)
	}

	// SessionID is set once per run (not per call) so every step's Slack
	// escalation message and downstream proxy.ToolRequest carries a
	// correlatable identifier for this specific run -- the caller-supplied
	// template's own SessionID (if any, e.g. an MCP session that
	// triggered this run) is preserved when present rather than always
	// overwritten.
	if template.SessionID == "" {
		template.SessionID = "workflow-run-" + runID.String()
	}

	result := &RunResult{RunID: runID}
	finalStatus := "completed"

	for _, step := range def.Steps {
		sr := e.runStep(ctx, template, orgID, runID, step)
		result.Steps = append(result.Steps, sr)
		if sr.Outcome != "allowed" {
			// Fail-safe, no partial-failure recovery or step-skipping (v1
			// scope, explicit): the first non-allowed step stops the run.
			if sr.Outcome == "denied" {
				finalStatus = "denied"
			} else {
				finalStatus = "failed"
			}
			break
		}
	}

	result.Status = finalStatus
	if _, err := e.pool.Exec(ctx, `
		UPDATE workflow_runs SET status = $2, finished_at = NOW() WHERE id = $1
	`, runID, finalStatus); err != nil {
		slog.Warn("workflow: failed to finalize run status", "run_id", runID, "status", finalStatus, "err", err)
	}
	return result, nil
}

// runStep executes exactly one step: resolves its connector (for this
// package's own informational pinning record), records a pre-dispatch
// workflow_run_steps row, calls the SAME unmodified dispatch() closure
// every standalone MCP call uses, then records the real outcome.
func (e *Executor) runStep(ctx context.Context, template mcp.ActionContext, orgID, runID uuid.UUID, step Step) StepResult {
	sr := StepResult{
		StepOrder:      step.StepOrder,
		WorkflowStepID: step.WorkflowStepID,
		StartedAt:      time.Now().UTC(),
	}
	rowID := uuid.New()

	conn, err := resolveConnectorByID(ctx, e.pool, orgID, step.GatewayToolID)
	if err != nil {
		sr.Outcome = "blocked"
		sr.ErrorDetail = err.Error()
		sr.FinishedAt = time.Now().UTC()
		e.insertRunStep(ctx, rowID, runID, sr)
		e.updateRunStep(ctx, rowID, sr)
		return sr
	}
	sr.ResolvedToolID = step.GatewayToolID
	sr.ResolvedConfigHash = approval.ComputeConfigHash(conn.toolType, conn.baseURLOrProvider, conn.credentialsEncrypted, conn.configHashJSON())

	// Independent, informational pre-dispatch policy preview -- the exact
	// same Evaluate method dispatch() itself calls, invoked a second time
	// purely so the audit record can show "this step escalated" (dispatch()'s
	// own return alone can't distinguish that from "always allowed").
	// Never used to skip or alter dispatch()'s own enforcement below.
	pc := policy.ActionContext{
		AgentName:   template.AgentName,
		ToolName:    conn.name,
		ActionType:  step.Action,
		Environment: template.Environment,
		Parameters:  step.Params,
		Scope:       template.AgentScope,
	}
	if decision, evalErr := e.policyEval.Evaluate(ctx, pc); evalErr == nil {
		sr.ProjectedDecision = decision.Action
	}
	e.insertRunStep(ctx, rowID, runID, sr)

	stepAC := template
	stepAC.Tool = conn.name
	stepAC.Action = step.Action
	stepAC.Parameters = step.Params
	stepAC.ReceivedAt = time.Now().UTC()

	// The real, enforced dispatch -- completely unmodified. If this
	// step's policy escalates, this call blocks inside Submit()+Hold()
	// exactly as it would for a standalone MCP call (B-039/B-057,
	// untouched) -- the accepted interim behavior for this brief.
	resultBody, dispatchErr := e.dispatch(ctx, stepAC)
	sr.FinishedAt = time.Now().UTC()
	if dispatchErr != nil {
		var pde *mcp.PolicyDeniedError
		if errors.As(dispatchErr, &pde) {
			sr.Outcome = "denied"
		} else {
			sr.Outcome = "blocked"
		}
		sr.ErrorDetail = dispatchErr.Error()
	} else {
		sr.Outcome = "allowed"
		sr.Result = resultBody
	}
	e.updateRunStep(ctx, rowID, sr)
	return sr
}

// insertRunStep writes the pre-dispatch snapshot (what was resolved and
// projected, before the real dispatch() call runs).
func (e *Executor) insertRunStep(ctx context.Context, rowID, runID uuid.UUID, sr StepResult) {
	var toolID *uuid.UUID
	if sr.ResolvedToolID != uuid.Nil {
		toolID = &sr.ResolvedToolID
	}
	var hash, proj *string
	if sr.ResolvedConfigHash != "" {
		hash = &sr.ResolvedConfigHash
	}
	if sr.ProjectedDecision != "" {
		proj = &sr.ProjectedDecision
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO workflow_run_steps
			(id, workflow_run_id, step_order, workflow_step_id, resolved_tool_id, resolved_config_hash, projected_decision, started_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, rowID, runID, sr.StepOrder, sr.WorkflowStepID, toolID, hash, proj, sr.StartedAt); err != nil {
		slog.Warn("workflow: failed to record run step", "run_id", runID, "step_order", sr.StepOrder, "err", err)
	}
}

// updateRunStep writes the real, post-dispatch outcome.
func (e *Executor) updateRunStep(ctx context.Context, rowID uuid.UUID, sr StepResult) {
	var resultRaw []byte
	if len(sr.Result) > 0 {
		resultRaw = sr.Result
	}
	var errDetail *string
	if sr.ErrorDetail != "" {
		errDetail = &sr.ErrorDetail
	}
	if _, err := e.pool.Exec(ctx, `
		UPDATE workflow_run_steps SET outcome = $2, result = $3, error_detail = $4, finished_at = $5
		WHERE id = $1
	`, rowID, sr.Outcome, resultRaw, errDetail, sr.FinishedAt); err != nil {
		slog.Warn("workflow: failed to update run step outcome", "row_id", rowID, "err", err)
	}
}
