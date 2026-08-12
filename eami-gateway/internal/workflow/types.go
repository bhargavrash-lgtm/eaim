// Package workflow executes a B-058-defined workflow (an ordered chain of
// gateway_tools connectors) by calling the gateway's existing dispatch
// closure once per step, in order -- reusing policy evaluation, TOCTOU
// pinning (B-057), audit logging, and episode recording completely
// unmodified. See Executor.Run's doc comment for the full per-step
// contract.
//
// Scope (Multi-Hop Workflows Brief 2, B-059): execution + per-hop TOCTOU
// pinning + static per-step parameters only. Output->input mapping
// between steps, durable pause/resume for escalation, and a chain-aware
// approval UI are all explicitly out of scope -- see BACKLOG.md's Thread
// B entry for the full epic.
package workflow

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Step is one resolved step of a workflow, ready to execute: the
// connector it targets (by ID, per B-058's schema) and its static
// parameters (B-059's workflow_step_params, migration 000007).
type Step struct {
	WorkflowStepID uuid.UUID
	StepOrder      int32
	GatewayToolID  uuid.UUID // never uuid.Nil for an executable step -- see Resolve's dangling-reference handling
	Action         string
	Params         map[string]any
}

// Definition is a workflow ready to execute -- resolved once per Run call.
type Definition struct {
	WorkflowID uuid.UUID
	OrgID      uuid.UUID
	Name       string
	Steps      []Step
}

// StepResult is what actually happened when one step executed -- mirrors
// workflow_run_steps' columns directly.
type StepResult struct {
	StepOrder          int32
	WorkflowStepID     uuid.UUID
	ResolvedToolID     uuid.UUID
	ResolvedConfigHash string
	// ProjectedDecision is this package's own independent pre-dispatch
	// policy.Evaluate() result ("allow"/"deny"/"escalate") -- informational
	// only, recorded so the audit trail can show a step escalated even
	// though dispatch()'s own return value alone can't distinguish
	// escalated-then-approved from always-allowed. Never used to skip or
	// alter what dispatch() itself enforces.
	ProjectedDecision string
	// Outcome is dispatch()'s REAL, enforced result, coarsened to three
	// values: "allowed" (dispatch succeeded), "denied" (dispatch returned
	// *mcp.PolicyDeniedError -- an immediate policy deny), "blocked" (any
	// other dispatch error: escalation denied/timed out, or a downstream
	// proxy/dispatch failure). ErrorDetail always carries the raw message
	// regardless, so "blocked" never hides the real reason.
	Outcome     string
	Result      json.RawMessage
	ErrorDetail string
	StartedAt   time.Time
	FinishedAt  time.Time
}

// RunResult is the full outcome of one workflow execution.
type RunResult struct {
	RunID  uuid.UUID
	Status string // "completed" | "denied" | "failed"
	Steps  []StepResult
}
