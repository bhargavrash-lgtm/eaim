// executor_workspace_test.go -- eami-gateway/internal/workflow
//
// B-207: proves WorkspaceID is genuinely threaded through the
// workflow-triggered dispatch path, not just the standalone MCP
// tool_call path. This closes a real gap a mandatory code-review pass
// found before this shipped: http.go's `template` literal and
// executor.go's own preview `pc` literal were both missed when
// WorkspaceID was first added (only mcp.ActionContext.ToPolicyContext()
// and the direct MCP construction site in internal/mcp/handler.go were
// updated) -- every workflow-triggered dispatch would have silently seen
// ac.WorkspaceID == "" regardless of the real triggering agent's
// workspace, meaning a workspace's own added restriction would never
// have applied to a workflow run. Fixed in http.go/executor.go; this
// file is the regression test proving it stays fixed.
//
// Uses testenv_test.go's real dispatch reconstruction (see that file's
// header for why dispatch() itself can't be imported directly) --
// Executor.Run takes its ActionContext template as an explicit argument,
// so this test builds its own (copying env.template() and setting
// WorkspaceID), mirroring exactly how http.go builds the real one from
// registry.AgentRecord.WorkspaceID.
package workflow

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/aiprovider"
	policy "github.com/eami/policy"
)

// TestExecutor_Run_WorkspaceScopedPolicy_AppliesToWorkflowDispatch is the
// fail-open regression test: a workspace-scoped DENY rule with NO
// corresponding global-floor rule for the same action. If WorkspaceID
// were silently empty (the bug the code review found), matchesRule would
// never match this rule at all (a workspace-scoped rule never matches an
// empty ac.WorkspaceID -- see eami-policy/structural.go), the step would
// fall through to the default ALLOW, and this test would fail by seeing
// the step wrongly succeed.
func TestExecutor_Run_WorkspaceScopedPolicy_AppliesToWorkflowDispatch(t *testing.T) {
	env := newWorkflowTestEnv(t)
	ctx := context.Background()

	groupID := uuid.New()
	if _, err := env.pool.Exec(ctx, `INSERT INTO groups (id, org_id, name) VALUES ($1, $2, $3)`,
		groupID, env.orgID, "b207-exec-test-group"); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	workspaceID := uuid.New()
	if _, err := env.pool.Exec(ctx, `INSERT INTO workspaces (id, org_id, group_id, name) VALUES ($1, $2, $3, $4)`,
		workspaceID, env.orgID, groupID, "b207-exec-test-workspace"); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	// Real agent row also carries workspace_id, matching how a real
	// registry.AgentRecord would resolve it -- not load-bearing for this
	// test (the ActionContext below is built explicitly), but keeps the
	// fixture honest/consistent with the real shape.
	if _, err := env.pool.Exec(ctx, `UPDATE gateway_agents SET workspace_id = $1 WHERE id = $2`,
		workspaceID, env.agentID); err != nil {
		t.Fatalf("set agent workspace_id: %v", err)
	}

	// No global-floor rule at all for "risky-action" -- only a
	// workspace-scoped deny. If WorkspaceID doesn't reach policy
	// evaluation, this rule can never match anything, and the step
	// defaults to allow.
	// Rule.ID must be a real UUID string, not a plain literal (B-115) --
	// a matching decision's PolicyID gets written into audit_log.policy_id,
	// a real `uuid` column; this dispatch path (unlike the pure eami-policy
	// unit tests) goes through a real INSERT.
	env.rules = []policy.Rule{
		{ID: uuid.NewString(), WorkspaceID: workspaceID.String(), Name: "workspace: deny risky-action",
			Priority: 1, Action: policy.ActionDeny,
			Conditions: policy.Conditions{ActionTypes: []string{"risky-action"}}},
	}

	toolA := env.insertAIProviderTool(t, "b207-exec-tool", "b207-provider")
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{
		"b207-provider": &fakeAdapter{name: "b207-provider"},
	})
	wfID := seedWorkflow(t, env, "b207-exec-workflow", []struct {
		toolID uuid.UUID
		action string
		params map[string]any
	}{
		{toolA, "risky-action", nil},
	})

	template := env.template()
	template.WorkspaceID = workspaceID.String()

	result, err := de.exec.Run(ctx, template, wfID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "denied" {
		t.Fatalf("REGRESSION: Status = %q, want denied -- the workspace-scoped policy never matched, "+
			"meaning WorkspaceID did not reach policy evaluation for this workflow-triggered dispatch "+
			"(the exact gap this test exists to catch)", result.Status)
	}
	if len(result.Steps) != 1 || result.Steps[0].Outcome != "denied" {
		t.Fatalf("step outcome = %+v, want exactly 1 step with outcome=denied", result.Steps)
	}

	// Also proves the fix in executor.go's own preview literal (pc, not
	// just the real enforced dispatch via stepAC): ProjectedDecision must
	// agree with what real enforcement did.
	if result.Steps[0].ProjectedDecision != policy.ActionDeny {
		t.Errorf("REGRESSION: ProjectedDecision = %q, want %q -- executor.go's preview literal (pc) "+
			"did not receive WorkspaceID, so it disagrees with the real enforced decision",
			result.Steps[0].ProjectedDecision, policy.ActionDeny)
	}
}

// TestExecutor_Run_WorkspaceScopedPolicy_NeverAppliesToOtherWorkspace is
// the cross-workspace-leak counterpart, real-DB-and-real-executor-backed:
// a workflow run triggered by an agent in a DIFFERENT workspace than the
// one the deny rule targets must not be affected by it.
func TestExecutor_Run_WorkspaceScopedPolicy_NeverAppliesToOtherWorkspace(t *testing.T) {
	env := newWorkflowTestEnv(t)
	ctx := context.Background()

	groupID := uuid.New()
	if _, err := env.pool.Exec(ctx, `INSERT INTO groups (id, org_id, name) VALUES ($1, $2, $3)`,
		groupID, env.orgID, "b207-exec-test-group-2"); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	targetWorkspaceID := uuid.New()
	if _, err := env.pool.Exec(ctx, `INSERT INTO workspaces (id, org_id, group_id, name) VALUES ($1, $2, $3, $4)`,
		targetWorkspaceID, env.orgID, groupID, "b207-exec-target-workspace"); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	env.rules = []policy.Rule{
		{ID: uuid.NewString(), WorkspaceID: targetWorkspaceID.String(), Name: "workspace: deny risky-action",
			Priority: 1, Action: policy.ActionDeny,
			Conditions: policy.Conditions{ActionTypes: []string{"risky-action"}}},
		{ID: uuid.NewString(), Name: "allow-rest", Priority: 100, Action: policy.ActionAllow},
	}

	toolA := env.insertAIProviderTool(t, "b207-exec-tool-2", "b207-provider-2")
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{
		"b207-provider-2": &fakeAdapter{name: "b207-provider-2"},
	})
	wfID := seedWorkflow(t, env, "b207-exec-workflow-2", []struct {
		toolID uuid.UUID
		action string
		params map[string]any
	}{
		{toolA, "risky-action", nil},
	})

	// This run's agent belongs to NO workspace (template.WorkspaceID left
	// empty) -- the target workspace's own deny rule must never reach it.
	result, err := de.exec.Run(ctx, env.template(), wfID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "completed" || result.Steps[0].Outcome != "allowed" {
		t.Errorf("cross-workspace leak: status=%q step outcome=%q, want completed/allowed -- "+
			"a different workspace's deny rule incorrectly reached a no-workspace dispatch",
			result.Status, result.Steps[0].Outcome)
	}
}
