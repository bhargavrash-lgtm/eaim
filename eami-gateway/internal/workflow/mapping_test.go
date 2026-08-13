// mapping_test.go -- eami-gateway/internal/workflow
//
// Real-Postgres, end-to-end integration tests for B-063's output->input
// mapping, extending executor_test.go/testenv_test.go's established
// harness (real dispatch pipeline, ai_provider fake-adapter connectors --
// see testenv_test.go's header for why rest_api isn't used here).
package workflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/aiprovider"
)

// mappingStep is seedWorkflowWithMapping's per-step spec -- a separate
// named type from executor_test.go's seedWorkflow (whose anonymous
// struct parameter every existing call site already matches
// structurally; changing it would require touching all 9 of them for a
// need only this file's new tests have).
type mappingStep struct {
	toolID       uuid.UUID
	action       string
	params       map[string]any
	inputMapping map[string]ExtractionRef
}

// seedWorkflowWithMapping inserts a real workflows row + ordered
// workflow_steps rows, each optionally carrying static params
// (workflow_step_params) and/or an input_mapping (workflow_steps.
// input_mapping) -- returns the workflow id and each step's real,
// caller-visible id, in order, since tests need real step ids to build
// from_step references.
func seedWorkflowWithMapping(t *testing.T, e *workflowTestEnv, name string, steps []mappingStep) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	wfID := uuid.New()
	if _, err := e.pool.Exec(ctx, `INSERT INTO workflows (id, org_id, name, status) VALUES ($1, $2, $3, 'active')`,
		wfID, e.orgID, name); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	stepIDs := make([]uuid.UUID, len(steps))
	for i, st := range steps {
		stepID := uuid.New()
		stepIDs[i] = stepID
		var inputMappingRaw []byte
		if len(st.inputMapping) > 0 {
			raw, err := json.Marshal(st.inputMapping)
			if err != nil {
				t.Fatalf("marshal input_mapping %d: %v", i, err)
			}
			inputMappingRaw = raw
		}
		if _, err := e.pool.Exec(ctx, `
			INSERT INTO workflow_steps (id, workflow_id, step_order, gateway_tool_id, action, input_mapping)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, stepID, wfID, i, st.toolID, st.action, inputMappingRaw); err != nil {
			t.Fatalf("insert workflow_step %d: %v", i, err)
		}
		if st.params != nil {
			raw, _ := json.Marshal(st.params)
			if _, err := e.pool.Exec(ctx, `INSERT INTO workflow_step_params (workflow_step_id, params) VALUES ($1, $2)`, stepID, raw); err != nil {
				t.Fatalf("insert workflow_step_params %d: %v", i, err)
			}
		}
	}
	return wfID, stepIDs
}

// TestExecutor_Run_Extraction_RealEndToEnd proves AC1: step 2's real
// dispatched Parameters contain a value extracted from step 1's real
// recorded response -- not mocked at the extraction layer, resolveParams
// runs for real inside the real Run loop, against a real fake-adapter
// response (the adapter itself is the only fake -- see testenv_test.go).
func TestExecutor_Run_Extraction_RealEndToEnd(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = allowAllRules()

	toolA := env.insertAIProviderTool(t, "lookup-tool", "provider-lookup")
	toolB := env.insertAIProviderTool(t, "notify-tool", "provider-notify")

	// adapterA returns a real, structured response containing the field
	// step B will extract -- fakeAdapter's own Dispatch (testenv_test.go)
	// always wraps params/action into a real JSON body, so this is
	// exercising resolveParams against genuine dispatch output shape, not
	// a synthetic StepResult constructed by the test itself.
	adapterA := &fakeAdapterWithBody{
		name: "provider-lookup",
		body: json.RawMessage(`{"ok":true,"contact":{"id":"c-999","email":"c-999@example.com"}}`),
	}
	adapterB := &fakeAdapter{name: "provider-notify"}
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{"provider-lookup": adapterA, "provider-notify": adapterB})

	wfID, stepIDs := seedWorkflowWithMapping(t, env, "extraction-e2e-workflow", []mappingStep{
		{toolID: toolA, action: "lookup", params: map[string]any{"q": "smith"}},
		{toolID: toolB, action: "notify"}, // step 2 has NO static params -- entirely from extraction
	})
	// Step 2's input_mapping references step 1's real id -- can only be
	// set now that stepIDs[0] is known (a real DB round trip, not
	// something available at seed time in one shot).
	if _, err := env.pool.Exec(context.Background(), `UPDATE workflow_steps SET input_mapping = $2 WHERE id = $1`,
		stepIDs[1], mustMarshalMapping(t, map[string]ExtractionRef{
			"contact_id":    {FromStep: stepIDs[0], Path: "contact.id"},
			"contact_email": {FromStep: stepIDs[0], Path: "contact.email"},
		})); err != nil {
		t.Fatalf("set step 2 input_mapping: %v", err)
	}

	result, err := de.exec.Run(context.Background(), env.template(), wfID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("Status = %q, want completed (steps: %+v)", result.Status, result.Steps)
	}
	if len(adapterB.calls) != 1 {
		t.Fatalf("adapterB calls = %d, want exactly 1", len(adapterB.calls))
	}
	gotParams := adapterB.calls[0].Params
	if gotParams["contact_id"] != "c-999" {
		t.Errorf("contact_id = %v, want the real extracted c-999", gotParams["contact_id"])
	}
	if gotParams["contact_email"] != "c-999@example.com" {
		t.Errorf("contact_email = %v, want the real extracted address", gotParams["contact_email"])
	}
}

// TestExecutor_Run_ExtractionPathNotFound_BlocksCleanly proves AC3: an
// extraction whose path doesn't exist in the prior step's real result
// fails the step cleanly (workflow_run_steps.outcome='blocked' with a
// clear error_detail) and dispatch is NEVER called for that step -- no
// partial/empty value is ever substituted and sent downstream.
func TestExecutor_Run_ExtractionPathNotFound_BlocksCleanly(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = allowAllRules()

	toolA := env.insertAIProviderTool(t, "lookup-tool-2", "provider-lookup-2")
	toolB := env.insertAIProviderTool(t, "notify-tool-2", "provider-notify-2")

	adapterA := &fakeAdapterWithBody{name: "provider-lookup-2", body: json.RawMessage(`{"contact":{"id":"c-1"}}`)}
	adapterB := &fakeAdapter{name: "provider-notify-2"}
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{"provider-lookup-2": adapterA, "provider-notify-2": adapterB})

	wfID, stepIDs := seedWorkflowWithMapping(t, env, "extraction-badpath-workflow", []mappingStep{
		{toolID: toolA, action: "lookup"},
		{toolID: toolB, action: "notify"},
	})
	if _, err := env.pool.Exec(context.Background(), `UPDATE workflow_steps SET input_mapping = $2 WHERE id = $1`,
		stepIDs[1], mustMarshalMapping(t, map[string]ExtractionRef{
			"phone": {FromStep: stepIDs[0], Path: "contact.phone"}, // does not exist in adapterA's response
		})); err != nil {
		t.Fatalf("set step 2 input_mapping: %v", err)
	}

	result, err := de.exec.Run(context.Background(), env.template(), wfID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("Status = %q, want failed", result.Status)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2 (step 2 must still be recorded, as blocked)", len(result.Steps))
	}
	sr := result.Steps[1]
	if sr.Outcome != "blocked" {
		t.Errorf("step 2 Outcome = %q, want blocked", sr.Outcome)
	}
	if sr.ErrorDetail == "" {
		t.Error("step 2 ErrorDetail is empty, want a clear reason naming the unresolved path")
	}
	if len(adapterB.calls) != 0 {
		t.Fatalf("adapterB was called %d times, want 0 -- dispatch must never be reached when extraction fails", len(adapterB.calls))
	}

	// Confirm the real DB row (not just the in-memory result) shows the
	// same clean failure -- what an admin reviewing the run would see.
	var dbOutcome, dbErrorDetail string
	if err := env.pool.QueryRow(context.Background(), `
		SELECT outcome, error_detail FROM workflow_run_steps WHERE workflow_run_id = $1 AND step_order = 1
	`, result.RunID).Scan(&dbOutcome, &dbErrorDetail); err != nil {
		t.Fatalf("read workflow_run_steps: %v", err)
	}
	if dbOutcome != "blocked" {
		t.Errorf("workflow_run_steps.outcome = %q, want blocked", dbOutcome)
	}
	if dbErrorDetail == "" {
		t.Error("workflow_run_steps.error_detail is empty, want a clear visible reason")
	}
}

// TestExecutor_Run_ExtractionCrossWorkflow_NeverResolves proves AC4 with
// a REAL second workflow's real step id (not a synthetic UUID): step 2
// of workflow A references a real, currently-existing step id that
// belongs to workflow B -- it must never resolve, because resolveParams
// only ever consults THIS run's own in-memory priorResults, which never
// contains another workflow's step regardless of what's really in the
// database.
func TestExecutor_Run_ExtractionCrossWorkflow_NeverResolves(t *testing.T) {
	env := newWorkflowTestEnv(t)
	env.rules = allowAllRules()

	toolShared := env.insertAIProviderTool(t, "shared-tool", "provider-shared")
	adapterShared := &fakeAdapterWithBody{name: "provider-shared", body: json.RawMessage(`{"secret":"do-not-leak"}`)}
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{"provider-shared": adapterShared})

	// Workflow B: a real, separate workflow with a real step, run to
	// completion first so its step has a genuine recorded result.
	wfB, stepIDsB := seedWorkflowWithMapping(t, env, "other-workflow-b", []mappingStep{
		{toolID: toolShared, action: "fetch"},
	})
	resultB, err := de.exec.Run(context.Background(), env.template(), wfB)
	if err != nil {
		t.Fatalf("Run (workflow B): %v", err)
	}
	if resultB.Status != "completed" {
		t.Fatalf("workflow B Status = %q, want completed", resultB.Status)
	}

	// Workflow A: step 2 references workflow B's real step id directly.
	toolA := env.insertAIProviderTool(t, "step-a-tool-2", "provider-a-2")
	toolB2 := env.insertAIProviderTool(t, "step-b-tool-2", "provider-b-2")
	adapterA2 := &fakeAdapter{name: "provider-a-2"}
	adapterB2 := &fakeAdapter{name: "provider-b-2"}
	de2 := newDispatchEnv(t, env, map[string]aiprovider.Adapter{"provider-a-2": adapterA2, "provider-b-2": adapterB2})

	wfA, stepIDsA := seedWorkflowWithMapping(t, env, "workflow-a-crossref", []mappingStep{
		{toolID: toolA, action: "start"},
		{toolID: toolB2, action: "finish"},
	})
	if _, err := env.pool.Exec(context.Background(), `UPDATE workflow_steps SET input_mapping = $2 WHERE id = $1`,
		stepIDsA[1], mustMarshalMapping(t, map[string]ExtractionRef{
			"leaked": {FromStep: stepIDsB[0], Path: "secret"}, // real step id, WRONG workflow
		})); err != nil {
		t.Fatalf("set step 2 input_mapping: %v", err)
	}

	resultA, err := de2.exec.Run(context.Background(), env.template(), wfA)
	if err != nil {
		t.Fatalf("Run (workflow A): %v", err)
	}
	if resultA.Status != "failed" {
		t.Fatalf("workflow A Status = %q, want failed (cross-workflow reference must never resolve)", resultA.Status)
	}
	if resultA.Steps[1].Outcome != "blocked" {
		t.Errorf("workflow A step 2 Outcome = %q, want blocked", resultA.Steps[1].Outcome)
	}
	if len(adapterB2.calls) != 0 {
		t.Fatalf("adapterB2 (workflow A's step 2) was called %d times, want 0", len(adapterB2.calls))
	}
}

func mustMarshalMapping(t *testing.T, m map[string]ExtractionRef) []byte {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal input_mapping: %v", err)
	}
	return raw
}

// fakeAdapterWithBody is fakeAdapter (testenv_test.go) with a
// caller-controlled response body instead of the always-{"ok":true,...}
// shape -- needed here so tests can control the exact JSON structure a
// downstream extraction reads from.
type fakeAdapterWithBody struct {
	name  string
	body  json.RawMessage
	calls []aiprovider.Request
}

func (f *fakeAdapterWithBody) Provider() string { return f.name }
func (f *fakeAdapterWithBody) Dispatch(_ context.Context, _ aiprovider.Credentials, req aiprovider.Request) (aiprovider.Response, error) {
	f.calls = append(f.calls, req)
	return aiprovider.Response{StatusCode: 200, Body: f.body}, nil
}

