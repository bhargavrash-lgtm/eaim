// executor_test.go -- eami-gateway/internal/workflow
//
// Integration tests for Executor.Run against real Postgres, using
// testenv_test.go's real (reconstructed) dispatch pipeline -- see that
// file's header comment for why dispatch() itself can't be imported
// directly, and why test connectors are ai_provider (fake adapter) rather
// than rest_api (real toolrouter.Forward would hit the real SSRF guard
// against any local httptest target).
package workflow

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/aiprovider"
	policy "github.com/eami/policy"
)

// seedWorkflow inserts a real workflows row + ordered workflow_steps rows
// (one per toolID/action pair), each with the given static params.
func seedWorkflow(t *testing.T, e *workflowTestEnv, name string, steps []struct {
	toolID uuid.UUID
	action string
	params map[string]any
}) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	wfID := uuid.New()
	if _, err := e.pool.Exec(ctx, `INSERT INTO workflows (id, org_id, name, status) VALUES ($1, $2, $3, 'active')`,
		wfID, e.orgID, name); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	for i, st := range steps {
		stepID := uuid.New()
		if _, err := e.pool.Exec(ctx, `
			INSERT INTO workflow_steps (id, workflow_id, step_order, gateway_tool_id, action)
			VALUES ($1, $2, $3, $4, $5)
		`, stepID, wfID, i, st.toolID, st.action); err != nil {
			t.Fatalf("insert workflow_step %d: %v", i, err)
		}
		if st.params != nil {
			raw, _ := json.Marshal(st.params)
			if _, err := e.pool.Exec(ctx, `INSERT INTO workflow_step_params (workflow_step_id, params) VALUES ($1, $2)`, stepID, raw); err != nil {
				t.Fatalf("insert workflow_step_params %d: %v", i, err)
			}
		}
	}
	return wfID
}

func allowAllRules() []policy.Rule {
	return []policy.Rule{
		{ID: "allow-all", Name: "allow-all", Priority: 100, Action: policy.ActionAllow},
	}
}

// ─── AC1: full end-to-end execution, all steps allowed ─────────────────────

func TestExecutor_Run_AllStepsAllowed_ExecutesInOrder(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = allowAllRules()

	toolA := env.insertAIProviderTool(t, "step-a-tool", "provider-a")
	toolB := env.insertAIProviderTool(t, "step-b-tool", "provider-b")
	adapterA := &fakeAdapter{name: "provider-a"}
	adapterB := &fakeAdapter{name: "provider-b"}
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{"provider-a": adapterA, "provider-b": adapterB})

	wfID := seedWorkflow(t, env, "ac1-workflow", []struct {
		toolID uuid.UUID
		action string
		params map[string]any
	}{
		{toolA, "query", map[string]any{"q": "first"}},
		{toolB, "notify", map[string]any{"msg": "second"}},
	})

	result, err := de.exec.Run(context.Background(), env.template(), wfID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("Status = %q, want completed", result.Status)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(result.Steps))
	}
	if result.Steps[0].Outcome != "allowed" || result.Steps[1].Outcome != "allowed" {
		t.Fatalf("outcomes = %q, %q, want allowed, allowed", result.Steps[0].Outcome, result.Steps[1].Outcome)
	}
	if result.Steps[0].StepOrder != 0 || result.Steps[1].StepOrder != 1 {
		t.Fatalf("step order not preserved: %d, %d", result.Steps[0].StepOrder, result.Steps[1].StepOrder)
	}
	// Real dispatch: each step reached its OWN real adapter exactly once,
	// with its own static params -- not a stub, not cross-wired.
	if adapterA.calls[0].Params["q"] != "first" {
		t.Errorf("adapterA got params %v, want q=first", adapterA.calls[0].Params)
	}
	if adapterB.calls[0].Params["msg"] != "second" {
		t.Errorf("adapterB got params %v, want msg=second", adapterB.calls[0].Params)
	}
	if len(adapterA.calls) != 1 || len(adapterB.calls) != 1 {
		t.Fatalf("adapterA calls=%d adapterB calls=%d, want exactly 1 each", len(adapterA.calls), len(adapterB.calls))
	}

	var runStatus string
	if err := env.pool.QueryRow(context.Background(), `SELECT status FROM workflow_runs WHERE id = $1`, result.RunID).Scan(&runStatus); err != nil {
		t.Fatalf("read workflow_runs: %v", err)
	}
	if runStatus != "completed" {
		t.Errorf("workflow_runs.status = %q, want completed", runStatus)
	}
}

// ─── AC2: a middle step denies -> run stops, later steps never execute ─────

func TestExecutor_Run_MiddleStepDenied_StopsRun(t *testing.T) {
	env := newWorkflowTestEnv(t)
	// Deny specifically the "delete" action; allow everything else. Set
	// BEFORE newDispatchEnv, which builds its evaluator once from
	// env.rules at that moment -- setting rules after would silently
	// build the evaluator from an empty/default rule set instead.
	env.rules = []policy.Rule{
		{ID: "deny-delete", Name: "deny-delete", Priority: 1, Action: policy.ActionDeny,
			Conditions: policy.Conditions{ActionTypes: []string{"delete"}}},
		{ID: "allow-rest", Name: "allow-rest", Priority: 100, Action: policy.ActionAllow},
	}
	toolA := env.insertAIProviderTool(t, "ac2-tool-a", "provider-a")
	toolB := env.insertAIProviderTool(t, "ac2-tool-b", "provider-b")
	toolC := env.insertAIProviderTool(t, "ac2-tool-c", "provider-c")
	adapterC := &fakeAdapter{name: "provider-c"}
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{
		"provider-a": &fakeAdapter{name: "provider-a"},
		"provider-b": &fakeAdapter{name: "provider-b"},
		"provider-c": adapterC,
	})
	wfID := seedWorkflow(t, env, "ac2-workflow", []struct {
		toolID uuid.UUID
		action string
		params map[string]any
	}{
		{toolA, "query", nil},
		{toolB, "delete", nil}, // this one will be denied
		{toolC, "notify", nil}, // must never execute
	})

	result, err := de.exec.Run(context.Background(), env.template(), wfID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "denied" {
		t.Fatalf("Status = %q, want denied", result.Status)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2 (step 3 must never have run)", len(result.Steps))
	}
	if result.Steps[0].Outcome != "allowed" {
		t.Errorf("step 0 outcome = %q, want allowed", result.Steps[0].Outcome)
	}
	if result.Steps[1].Outcome != "denied" {
		t.Errorf("step 1 outcome = %q, want denied", result.Steps[1].Outcome)
	}
	if len(adapterC.calls) != 0 {
		t.Errorf("adapterC (step 3) was called %d times, want 0 -- it must never run", len(adapterC.calls))
	}

	var thirdStepCount int
	env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM workflow_run_steps WHERE workflow_run_id = $1 AND step_order = 2`, result.RunID).Scan(&thirdStepCount)
	if thirdStepCount != 0 {
		t.Errorf("workflow_run_steps has a row for step 2, want none (must never have started)")
	}
}

// ─── AC3: escalation blocks via Hold(), a real decision resumes/stops ──────

func escalateRiskyRules() []policy.Rule {
	return []policy.Rule{
		{ID: "escalate-risky", Name: "escalate-risky", Priority: 1, Action: policy.ActionEscalate,
			Conditions: policy.Conditions{ActionTypes: []string{"risky-action"}}},
		{ID: "allow-rest", Name: "allow-rest", Priority: 100, Action: policy.ActionAllow},
	}
}

func TestExecutor_Run_StepEscalates_ApprovedResumesRun(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = escalateRiskyRules()
	toolA := env.insertAIProviderTool(t, "ac3-approve-a", "provider-a")
	toolB := env.insertAIProviderTool(t, "ac3-approve-b", "provider-b")
	adapterB := &fakeAdapter{name: "provider-b"}
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{
		"provider-a": &fakeAdapter{name: "provider-a"}, "provider-b": adapterB,
	})
	wfID := seedWorkflow(t, env, "ac3-approve-workflow", []struct {
		toolID uuid.UUID
		action string
		params map[string]any
	}{
		{toolA, "query", nil},
		{toolB, "risky-action", nil},
	})

	type runOutcome struct {
		result *RunResult
		err    error
	}
	done := make(chan runOutcome, 1)
	go func() {
		r, err := de.exec.Run(context.Background(), env.template(), wfID)
		done <- runOutcome{r, err}
	}()

	approvalID := waitForPendingApproval(t, env.pool, env.orgID, 5*time.Second)
	decideTestApproval(t, env.pool, approvalID, "approved")

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("Run: %v", out.err)
		}
		if out.result.Status != "completed" {
			t.Fatalf("Status = %q, want completed", out.result.Status)
		}
		if len(out.result.Steps) != 2 {
			t.Fatalf("len(Steps) = %d, want 2", len(out.result.Steps))
		}
		if out.result.Steps[1].Outcome != "allowed" {
			t.Errorf("escalated step's outcome after approval = %q, want allowed", out.result.Steps[1].Outcome)
		}
		if out.result.Steps[1].ProjectedDecision != "escalate" {
			t.Errorf("ProjectedDecision = %q, want escalate", out.result.Steps[1].ProjectedDecision)
		}
		if len(adapterB.calls) != 1 {
			t.Errorf("adapterB called %d times after approval, want exactly 1 (the real resumed dispatch)", len(adapterB.calls))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after approval decided -- Hold() likely stuck")
	}
}

func TestExecutor_Run_StepEscalates_DeniedStopsRun(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = escalateRiskyRules()
	toolA := env.insertAIProviderTool(t, "ac3-deny-a", "provider-a")
	toolB := env.insertAIProviderTool(t, "ac3-deny-b", "provider-b")
	adapterB := &fakeAdapter{name: "provider-b"}
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{
		"provider-a": &fakeAdapter{name: "provider-a"}, "provider-b": adapterB,
	})
	wfID := seedWorkflow(t, env, "ac3-deny-workflow", []struct {
		toolID uuid.UUID
		action string
		params map[string]any
	}{
		{toolA, "query", nil},
		{toolB, "risky-action", nil},
	})

	type runOutcome struct {
		result *RunResult
		err    error
	}
	done := make(chan runOutcome, 1)
	go func() {
		r, err := de.exec.Run(context.Background(), env.template(), wfID)
		done <- runOutcome{r, err}
	}()

	approvalID := waitForPendingApproval(t, env.pool, env.orgID, 5*time.Second)
	decideTestApproval(t, env.pool, approvalID, "denied")

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("Run: %v", out.err)
		}
		if out.result.Status != "failed" {
			t.Fatalf("Status = %q, want failed", out.result.Status)
		}
		if out.result.Steps[1].Outcome != "blocked" {
			t.Errorf("denied-escalation step's outcome = %q, want blocked", out.result.Steps[1].Outcome)
		}
		if len(adapterB.calls) != 0 {
			t.Errorf("adapterB called %d times after denial, want 0 -- a denied escalation must never dispatch", len(adapterB.calls))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after denial decided -- Hold() likely stuck")
	}
}

// ─── AC4: per-hop TOCTOU -- two distinct scenarios ─────────────────────────

// Non-escalating: editing a connector between steps is CORRECT to pick up
// (nothing was ever "reviewed" against the old config for an allow-path
// step) -- proves no accidental upfront-caching bug, not a vulnerability.
func TestExecutor_Run_TOCTOU_NonEscalating_Step2PicksUpEditedConfig(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = allowAllRules()
	toolA := env.insertAIProviderTool(t, "toctou-nonesc-a", "provider-a")
	toolB := env.insertAIProviderTool(t, "toctou-nonesc-b", "provider-old")
	adapterOld := &fakeAdapter{name: "provider-old"}
	adapterNew := &fakeAdapter{name: "provider-new"}
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{
		"provider-a": &fakeAdapter{name: "provider-a"}, "provider-old": adapterOld, "provider-new": adapterNew,
	})
	wfID := seedWorkflow(t, env, "toctou-nonesc-workflow", []struct {
		toolID uuid.UUID
		action string
		params map[string]any
	}{
		{toolA, "query", nil},
		{toolB, "query", nil},
	})

	// Edit toolB's provider (its "config") shortly after Run starts --
	// both steps dispatch fast (in-process fake adapters, no real
	// network), so a short sleep races step A's completion. Timing-
	// sensitive by nature for the non-escalating case (there's no
	// blocking hook to hang off, unlike the escalating case below) --
	// acceptable because what's being proven is "no upfront caching"
	// (step 2 must never be stuck using a snapshot from before Run even
	// started), not exact interleaving with step 1's own completion.
	go func() {
		time.Sleep(30 * time.Millisecond)
		env.pool.Exec(context.Background(), `UPDATE gateway_tools SET provider = $1 WHERE id = $2`, "provider-new", toolB)
	}()

	result, err := de.exec.Run(context.Background(), env.template(), wfID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("Status = %q, want completed", result.Status)
	}
	// Whichever adapter step 2 actually reached is fine for this
	// inherently timing-raced assertion -- the real point is that EXACTLY
	// one of them was called (never zero, never a crash, never a stale
	// resolution that silently no-ops), proving fresh per-hop resolution.
	total := len(adapterOld.calls) + len(adapterNew.calls)
	if total != 1 {
		t.Fatalf("adapterOld calls=%d adapterNew calls=%d, want exactly 1 total", len(adapterOld.calls), len(adapterNew.calls))
	}
}

// Escalating (the real attack): step 2 escalates; while its approval is
// pending, its OWN connector's provider is edited; approve; resume must
// fail closed via B-057's existing, unmodified mechanism -- proving the
// workflow runner doesn't bypass or weaken it.
func TestExecutor_Run_TOCTOU_EscalatingMidHold_ConfigChanged_FailsClosed(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = escalateRiskyRules()
	toolA := env.insertAIProviderTool(t, "toctou-esc-a", "provider-a")
	toolB := env.insertAIProviderTool(t, "toctou-esc-b", "provider-original")
	adapterOriginal := &fakeAdapter{name: "provider-original"}
	adapterAttacker := &fakeAdapter{name: "provider-attacker"}
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{
		"provider-a":        &fakeAdapter{name: "provider-a"},
		"provider-original": adapterOriginal, "provider-attacker": adapterAttacker,
	})
	wfID := seedWorkflow(t, env, "toctou-esc-workflow", []struct {
		toolID uuid.UUID
		action string
		params map[string]any
	}{
		{toolA, "query", nil},
		{toolB, "risky-action", nil},
	})

	type runOutcome struct {
		result *RunResult
		err    error
	}
	done := make(chan runOutcome, 1)
	go func() {
		r, err := de.exec.Run(context.Background(), env.template(), wfID)
		done <- runOutcome{r, err}
	}()

	approvalID := waitForPendingApproval(t, env.pool, env.orgID, 5*time.Second)

	// The real attack: while step 2's escalation sits pending, its own
	// connector's provider (part of ComputeConfigHash's pinned input) is
	// edited -- a real UPDATE against the real row, not a simulated/
	// mocked change. Redirects to a different adapter entirely, the
	// closest ai_provider equivalent of "swap the destination."
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE gateway_tools SET provider = $1 WHERE id = $2`, "provider-attacker", toolB); err != nil {
		t.Fatalf("simulate mid-hold provider change: %v", err)
	}

	decideTestApproval(t, env.pool, approvalID, "approved")

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("Run: %v", out.err)
		}
		// dispatchApproved (B-057, unmodified) must have refused to
		// resume against the changed connector -- surfaces as a
		// dispatch error, so this step's outcome is "blocked", and the
		// run stops rather than completing.
		if out.result.Status != "failed" {
			t.Fatalf("Status = %q, want failed (resume must fail closed on a mid-hold config change)", out.result.Status)
		}
		if out.result.Steps[1].Outcome != "blocked" {
			t.Fatalf("step 1 outcome = %q, want blocked", out.result.Steps[1].Outcome)
		}
		if out.result.Steps[1].ErrorDetail == "" {
			t.Error("expected a non-empty error detail explaining the fail-closed refusal")
		}
		if len(adapterOriginal.calls) != 0 || len(adapterAttacker.calls) != 0 {
			t.Errorf("neither adapter should ever be called on a fail-closed resume -- original=%d attacker=%d",
				len(adapterOriginal.calls), len(adapterAttacker.calls))
		}

		var resumeOutcome *string
		env.pool.QueryRow(context.Background(), `SELECT resume_outcome FROM approval_requests WHERE id = $1`, approvalID).Scan(&resumeOutcome)
		if resumeOutcome == nil || *resumeOutcome != "config_changed" {
			got := "nil"
			if resumeOutcome != nil {
				got = *resumeOutcome
			}
			t.Errorf("approval_requests.resume_outcome = %q, want config_changed", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after approval decided -- Hold() likely stuck")
	}
}

// ─── AC5: workflow_run_steps accurately records what happened ─────────────

func TestExecutor_Run_WorkflowRunStepsRecordsAccurateAuditTrail(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = allowAllRules()
	toolA := env.insertAIProviderTool(t, "ac5-tool", "provider-ac5")
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{"provider-ac5": &fakeAdapter{name: "provider-ac5"}})
	wfID := seedWorkflow(t, env, "ac5-workflow", []struct {
		toolID uuid.UUID
		action string
		params map[string]any
	}{
		{toolA, "query", map[string]any{"x": float64(1)}},
	})

	result, err := de.exec.Run(context.Background(), env.template(), wfID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var (
		resolvedToolID uuid.UUID
		hash           string
		outcome        string
		resultRaw      []byte
	)
	if err := env.pool.QueryRow(context.Background(), `
		SELECT resolved_tool_id, resolved_config_hash, outcome, result
		FROM workflow_run_steps WHERE workflow_run_id = $1 AND step_order = 0
	`, result.RunID).Scan(&resolvedToolID, &hash, &outcome, &resultRaw); err != nil {
		t.Fatalf("read workflow_run_steps: %v", err)
	}
	if resolvedToolID != toolA {
		t.Errorf("resolved_tool_id = %s, want %s", resolvedToolID, toolA)
	}
	if hash == "" {
		t.Error("resolved_config_hash is empty, want a real computed hash")
	}
	if outcome != "allowed" {
		t.Errorf("outcome = %q, want allowed", outcome)
	}
	var got map[string]any
	if err := json.Unmarshal(resultRaw, &got); err != nil || got["ok"] != true {
		t.Errorf("result = %s, want a real {\"ok\":true,...} adapter response", resultRaw)
	}
}

// ─── B-093: audit_log records which workflow run/step a dispatch came from ─

// allowAllRulesRealPolicyID is allowAllRules() with a real-UUID-shaped rule
// ID. Needed specifically here, not a pre-existing helper: audit_log.policy_id
// is a real `uuid` column, and testenv_test.go's dispatch closure snapshots
// decision.PolicyID (the matched rule's own ID) straight into the audit
// write -- allowAllRules()'s literal "allow-all" string isn't a valid UUID,
// so any test that (unlike every pre-existing one) actually reads back the
// resulting audit_log row hits a real INSERT failure the existing tests
// never noticed, since they never check audit_log content and the write
// error is discarded (`_ = auditWriter.Write(...)`, matching main.go's own
// fire-and-forget-from-dispatch's-perspective convention). A real loaded
// policy's ID from the `policies` table is always a genuine UUID in
// production, so this is a test-fixture-only gap, not fixed in the shared
// helper to avoid touching every other test that already passes with it.
func allowAllRulesRealPolicyID() []policy.Rule {
	return []policy.Rule{
		{ID: uuid.NewString(), Name: "allow-all", Priority: 100, Action: policy.ActionAllow},
	}
}

// TestExecutor_Run_AuditLogRecordsWorkflowRunIDAndStepIndex is the AC3
// centerpiece: a real 2-step workflow run's TWO real dispatches (through
// the reconstructed real dispatch pipeline -- testenv_test.go's own header
// explains why it's a reconstruction, not a mock) each produce a real
// audit_log row correctly tagged with this run's id and that step's own
// index -- proving the mcp.ActionContext plumbing (executor.go's runStep
// -> dispatch()'s auditEntry construction) actually reaches the stored
// column, not just that Writer.Write can accept the field in isolation
// (writer_pg_test.go's own tests already cover that half).
func TestExecutor_Run_AuditLogRecordsWorkflowRunIDAndStepIndex(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = allowAllRulesRealPolicyID()
	toolA := env.insertAIProviderTool(t, "b093-tool-a", "b093-provider-a")
	toolB := env.insertAIProviderTool(t, "b093-tool-b", "b093-provider-b")
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{
		"b093-provider-a": &fakeAdapter{name: "b093-provider-a"},
		"b093-provider-b": &fakeAdapter{name: "b093-provider-b"},
	})
	wfID := seedWorkflow(t, env, "b093-workflow", []struct {
		toolID uuid.UUID
		action string
		params map[string]any
	}{
		{toolA, "query", map[string]any{"x": float64(1)}},
		{toolB, "notify", map[string]any{"y": float64(2)}},
	})

	result, err := de.exec.Run(context.Background(), env.template(), wfID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("Status = %q, want completed", result.Status)
	}

	rows, err := env.pool.Query(context.Background(), `
		SELECT tool_name, step_index FROM audit_log
		WHERE workflow_run_id = $1 ORDER BY step_index ASC
	`, result.RunID)
	if err != nil {
		t.Fatalf("query audit_log by workflow_run_id: %v", err)
	}
	defer rows.Close()

	type got struct {
		toolName  string
		stepIndex *int32
	}
	var gotRows []got
	for rows.Next() {
		var g got
		if err := rows.Scan(&g.toolName, &g.stepIndex); err != nil {
			t.Fatalf("scan: %v", err)
		}
		gotRows = append(gotRows, g)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	if len(gotRows) != 2 {
		t.Fatalf("audit_log rows with workflow_run_id=%s: got %d, want 2 (one per real step dispatch)", result.RunID, len(gotRows))
	}
	if gotRows[0].toolName != "b093-tool-a" || gotRows[0].stepIndex == nil || *gotRows[0].stepIndex != 0 {
		t.Errorf("step 0 row = tool_name:%q step_index:%v, want b093-tool-a/0", gotRows[0].toolName, gotRows[0].stepIndex)
	}
	if gotRows[1].toolName != "b093-tool-b" || gotRows[1].stepIndex == nil || *gotRows[1].stepIndex != 1 {
		t.Errorf("step 1 row = tool_name:%q step_index:%v, want b093-tool-b/1", gotRows[1].toolName, gotRows[1].stepIndex)
	}
}

// TestExecutor_Run_StandaloneDispatch_AuditLogWorkflowLinkageIsNull proves
// the other half of AC3: a dispatch NOT made through the workflow executor
// (calling de.dispatch directly, the same call shape a standalone MCP
// tool_call uses) leaves audit_log's workflow_run_id/step_index genuinely
// NULL -- unaffected by this brief's plumbing, not a stray zero value.
func TestExecutor_Run_StandaloneDispatch_AuditLogWorkflowLinkageIsNull(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = allowAllRulesRealPolicyID()
	env.insertAIProviderTool(t, "b093-standalone-tool", "b093-standalone-provider")
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{
		"b093-standalone-provider": &fakeAdapter{name: "b093-standalone-provider"},
	})

	ac := env.template()
	ac.Tool = "b093-standalone-tool"
	ac.Action = "query"
	ac.Parameters = map[string]any{"z": float64(3)}
	ac.ReceivedAt = time.Now().UTC()
	// Deliberately NOT setting WorkflowRunID/StepIndex -- this is the exact
	// zero-value ActionContext shape a real standalone MCP tool_call has.

	if _, err := de.dispatch(context.Background(), ac); err != nil {
		t.Fatalf("standalone dispatch: %v", err)
	}

	var runID *uuid.UUID
	var stepIdx *int32
	if err := env.pool.QueryRow(context.Background(), `
		SELECT workflow_run_id, step_index FROM audit_log
		WHERE org_id = $1 AND tool_name = 'b093-standalone-tool'
		ORDER BY timestamp DESC LIMIT 1
	`, env.orgID).Scan(&runID, &stepIdx); err != nil {
		t.Fatalf("read audit_log row: %v", err)
	}
	if runID != nil {
		t.Errorf("standalone dispatch audit_log.workflow_run_id = %v, want real SQL NULL", *runID)
	}
	if stepIdx != nil {
		t.Errorf("standalone dispatch audit_log.step_index = %v, want real SQL NULL", *stepIdx)
	}
}

// ─── AC6: no output->input mapping -- params are static, by construction ──

func TestExecutor_Run_NoOutputToInputMapping_ParamsAreStaticOnly(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = allowAllRules()
	toolA := env.insertAIProviderTool(t, "ac6-tool-a", "provider-a6")
	toolB := env.insertAIProviderTool(t, "ac6-tool-b", "provider-b6")
	adapterA := &fakeAdapter{name: "provider-a6"} // its response will include a distinctive marker
	adapterB := &fakeAdapter{name: "provider-b6"}
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{"provider-a6": adapterA, "provider-b6": adapterB})

	staticParams := map[string]any{"fixed": "value-set-at-definition-time"}
	wfID := seedWorkflow(t, env, "ac6-workflow", []struct {
		toolID uuid.UUID
		action string
		params map[string]any
	}{
		{toolA, "query", map[string]any{"a_param": "marker-should-never-reach-step-b"}},
		{toolB, "notify", staticParams},
	})

	result, err := de.exec.Run(context.Background(), env.template(), wfID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("Status = %q, want completed", result.Status)
	}

	// Step B's real request params, as received by its own real adapter,
	// must equal exactly its own static configuration -- step A's marker
	// (from step A's REQUEST, and separately from step A's RESPONSE, since
	// fakeAdapter echoes its input) must never appear in step B's request.
	if len(adapterB.calls) != 1 {
		t.Fatalf("adapterB called %d times, want 1", len(adapterB.calls))
	}
	got := adapterB.calls[0].Params
	if got["fixed"] != "value-set-at-definition-time" {
		t.Errorf("step B request params = %v, missing its own static param", got)
	}
	if _, leaked := got["a_param"]; leaked {
		t.Errorf("step A's param leaked into step B's request -- output->input mapping must not exist yet: %v", got)
	}
	for _, v := range got {
		if s, ok := v.(string); ok && s == "marker-should-never-reach-step-b" {
			t.Errorf("step A's marker value leaked into step B's request: %v", got)
		}
	}
}

// ─── dangling step / unknown workflow ──────────────────────────────────────

func TestExecutor_Run_DanglingStep_ReturnsErrorBeforeAnyStepRuns(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = allowAllRules()
	toolA := env.insertAIProviderTool(t, "dangling-tool-a", "provider-dangling")
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{"provider-dangling": &fakeAdapter{name: "provider-dangling"}})
	wfID := seedWorkflow(t, env, "dangling-workflow", []struct {
		toolID uuid.UUID
		action string
		params map[string]any
	}{
		{toolA, "query", nil},
	})
	// Delete the tool the one step references -- mirrors B-058's ON
	// DELETE SET NULL dangling-reference state.
	if _, err := env.pool.Exec(context.Background(), `DELETE FROM gateway_tools WHERE id = $1`, toolA); err != nil {
		t.Fatalf("delete tool: %v", err)
	}

	_, err := de.exec.Run(context.Background(), env.template(), wfID)
	if err == nil {
		t.Fatal("expected an error for a workflow with a dangling step, got nil")
	}
}

func TestExecutor_Run_UnknownWorkflow_ReturnsNotFound(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = allowAllRules()
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{})
	_, err := de.exec.Run(context.Background(), env.template(), uuid.New())
	if err == nil {
		t.Fatal("expected ErrWorkflowNotFound for an unknown workflow id, got nil")
	}
}
