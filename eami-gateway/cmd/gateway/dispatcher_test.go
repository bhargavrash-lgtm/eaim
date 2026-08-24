// dispatcher_test.go -- cmd/gateway
//
// Real-Postgres integration tests for Dispatcher.Dispatch (B-102), proving
// AC1-AC4 of that brief and closing B-101: dispatch used to be an inline
// closure inside run(), not independently constructable by any test.
// Dispatcher.Dispatch is now a real method, callable directly here with a
// real test Postgres pool and a fake policy.Evaluator -- no full MCP/SSE
// server needed.
//
// Built on the existing mainTestEnv harness (main_pg_test.go) plus one new
// helper, insertAgent (approval_requests.agent_id is NOT NULL REFERENCES
// gateway_agents(id), schema.sql:273, so any test exercising the Escalate
// branch needs a real row there), following the same real-toolRouter/real-
// aiProviderRouter/real-approvalRouter-with-LISTEN-NOTIFY-started pattern
// internal/workflow/testenv_test.go already proved for reconstructing
// dispatch's real pipeline -- duplicated here (not imported) because main
// and workflow are separate packages and package main cannot be imported
// by any other package's tests.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./cmd/gateway/... -run TestDispatch -v
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/aiprovider"
	"github.com/eami/gateway/internal/approval"
	"github.com/eami/gateway/internal/audit"
	"github.com/eami/gateway/internal/episode"
	"github.com/eami/gateway/internal/mcp"
	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/toolrouter"
	policy "github.com/eami/policy"
)

// insertAgent seeds a real gateway_agents row.
func (e *mainTestEnv) insertAgent(t *testing.T) (agentID uuid.UUID, agentName string) {
	t.Helper()
	agentID = uuid.New()
	agentName = "dispatch-test-agent-" + agentID.String()[:8]
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		agentID, e.orgID, agentName, "test-model", "test-owner", "test scope",
	); err != nil {
		t.Fatalf("insert gateway_agents: %v", err)
	}
	return agentID, agentName
}

// fakeEvaluator implements policy.Evaluator, returning a fixed Decision
// regardless of input -- lets each test force Deny/Escalate/Allow directly
// without seeding real policy rules per case.
type fakeEvaluator struct {
	action string
}

func (f *fakeEvaluator) Evaluate(_ context.Context, _ policy.ActionContext) (policy.Decision, error) {
	return policy.Decision{Action: f.action}, nil
}

// dispatcherTestEnv bundles one test's real, wired Dispatcher.
type dispatcherTestEnv struct {
	env        *mainTestEnv
	dispatcher *Dispatcher
	downstream *httptest.Server
	agentID    uuid.UUID
	agentName  string
}

// newDispatcherTestEnv wires a real toolrouter.Router/aiprovider.Router
// (both against the real test pool; no tool is ever registered by these
// tests, so both always fall through to nil resolution, exactly like an
// unregistered tool name in production) and a real approval.Router with
// its LISTEN/NOTIFY loop actually running, then builds a Dispatcher via
// the same exported NewDispatcher constructor run() itself calls -- with
// apiBaseURL/apiServiceKey left empty, matching
// internal/workflow/testenv_test.go's own disclosed simplification
// (writeTokenUsage returns nil immediately when apiBase == "", so no real
// HTTP call happens; irrelevant to what this file proves -- convergence,
// not the token-usage HTTP write itself, which main_test.go already
// covers directly).
func newDispatcherTestEnv(t *testing.T, action string, extraHooks ...DispatchHook) *dispatcherTestEnv {
	t.Helper()
	env := newMainTestEnv(t)
	agentID, agentName := env.insertAgent(t)

	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(downstream.Close)
	fwd := proxy.New(proxy.Config{DownstreamURL: downstream.URL}, downstream.Client())

	toolRouter := toolrouter.New(env.pool, nil)
	aiProviderRouter := aiprovider.New(env.pool, nil, map[string]aiprovider.Adapter{})

	holdTimeout := 5 * time.Second
	approvalRouter := approval.New(env.pool, fwd, holdTimeout, "", "", toolRouter, aiProviderRouter)
	runCtx, cancel := context.WithCancel(context.Background())
	go approvalRouter.Run(runCtx)
	t.Cleanup(cancel)

	auditWriter, err := audit.NewWriter(context.Background(), env.pool)
	if err != nil {
		t.Fatalf("audit.NewWriter: %v", err)
	}
	episodeRecorder := episode.New(env.pool)

	dispatcher := NewDispatcher(
		toolRouter, aiProviderRouter, &fakeEvaluator{action: action},
		auditWriter, episodeRecorder, approvalRouter, fwd,
		"", "", holdTimeout, extraHooks...,
	)

	return &dispatcherTestEnv{env: env, dispatcher: dispatcher, downstream: downstream, agentID: agentID, agentName: agentName}
}

func (e *dispatcherTestEnv) actionContext(tool string) mcp.ActionContext {
	return mcp.ActionContext{
		AgentID:     "agent:" + e.agentName,
		AgentUUID:   e.agentID.String(),
		AgentName:   e.agentName,
		OrgID:       e.env.orgID.String(),
		Tool:        tool,
		Action:      "test-action",
		Parameters:  map[string]any{"k": "v"},
		Environment: "development",
		SessionID:   "dispatch-test-" + uuid.NewString()[:8],
		ReceivedAt:  time.Now(),
	}
}

// waitForPendingApproval / decideTestApproval mirror
// internal/workflow/testenv_test.go's identically-named helpers exactly --
// same real mechanism (poll approval_requests, then UPDATE + pg_notify the
// same way eami-api's real DecideApproval handler does) -- duplicated
// rather than shared, since main cannot export helpers for another
// package's tests to import.
func waitForPendingApproval(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var id string
		err := pool.QueryRow(context.Background(), `
			SELECT id FROM approval_requests WHERE org_id = $1 AND status = 'pending'
			ORDER BY created_at DESC LIMIT 1
		`, orgID).Scan(&id)
		if err == nil {
			return id
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for a pending approval_requests row")
	return ""
}

func decideTestApproval(t *testing.T, pool *pgxpool.Pool, approvalID, status string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		UPDATE approval_requests SET status = $1, decision_reason = 'test decision', decided_at = now() WHERE id = $2
	`, status, approvalID); err != nil {
		t.Fatalf("decide approval: %v", err)
	}
	payload, _ := json.Marshal(map[string]string{"approval_id": approvalID})
	if _, err := pool.Exec(ctx, `SELECT pg_notify('approval_decision', $1)`, string(payload)); err != nil {
		t.Fatalf("notify approval_decision: %v", err)
	}
}

// ---- AC1: Dispatch is directly constructable and callable, for each
// decision type, with no full server running. ----

func TestDispatch_Deny_NoFullServerNeeded(t *testing.T) {
	e := newDispatcherTestEnv(t, policy.ActionDeny)

	result, err := e.dispatcher.Dispatch(context.Background(), e.actionContext("some-tool"))

	if result != nil {
		t.Errorf("Result = %v, want nil", result)
	}
	var pd *mcp.PolicyDeniedError
	if !errors.As(err, &pd) {
		t.Fatalf("Err = %v, want *mcp.PolicyDeniedError", err)
	}
}

func TestDispatch_Allow_NoFullServerNeeded(t *testing.T) {
	e := newDispatcherTestEnv(t, policy.ActionAllow)

	result, err := e.dispatcher.Dispatch(context.Background(), e.actionContext("some-tool"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != `{"ok":true}` {
		t.Errorf("Result = %s, want the real downstream body", result)
	}
}

func TestDispatch_Escalate_Approved_NoFullServerNeeded(t *testing.T) {
	e := newDispatcherTestEnv(t, policy.ActionEscalate)

	var result json.RawMessage
	var err error
	done := make(chan struct{})
	go func() {
		result, err = e.dispatcher.Dispatch(context.Background(), e.actionContext("some-tool"))
		close(done)
	}()

	approvalID := waitForPendingApproval(t, e.env.pool, e.env.orgID, 5*time.Second)
	decideTestApproval(t, e.env.pool, approvalID, "approved")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Dispatch to resume after approval")
	}

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The test's tool name was never registered, so ResolvedToolID is
	// empty and dispatchApproved falls back to fwd.Forward (router.go:
	// 648-655) -- the same downstream httptest server as the Allow test.
	if string(result) != `{"ok":true}` {
		t.Errorf("Result = %s, want the real downstream body", result)
	}
}

// ---- AC3: recordTokenUsage's hook (proxied here by a recording hook with
// the identical signature) fires for BOTH Allow and approved-Escalate,
// proven by one shared assertion helper -- proving the convergence, not
// just that both branches happen to work. ----

type hookCall struct {
	dispatched   bool
	decision     string
	episodeSteps int
}

func newRecordingHook() (hook DispatchHook, calls func() []hookCall) {
	var mu sync.Mutex
	var recorded []hookCall
	hook = func(_ context.Context, _ mcp.ActionContext, o DispatchOutcome) {
		mu.Lock()
		defer mu.Unlock()
		recorded = append(recorded, hookCall{dispatched: o.Dispatched, decision: o.Decision, episodeSteps: len(o.EpisodeSteps)})
	}
	calls = func() []hookCall {
		mu.Lock()
		defer mu.Unlock()
		out := make([]hookCall, len(recorded))
		copy(out, recorded)
		return out
	}
	return hook, calls
}

// assertDispatchedHookFired is the ONE assertion both AC3 tests below call
// -- the "same test structure for both" the brief requires.
func assertDispatchedHookFired(t *testing.T, calls []hookCall, wantDecision string) {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("hook fired %d times, want 1", len(calls))
	}
	if !calls[0].dispatched {
		t.Errorf("Dispatched = false, want true (a real downstream call executed, so token usage should be recorded)")
	}
	if calls[0].decision != wantDecision {
		t.Errorf("Decision = %q, want %q", calls[0].decision, wantDecision)
	}
}

func TestDispatch_TokenUsageHook_FiresForAllow(t *testing.T) {
	hook, calls := newRecordingHook()
	e := newDispatcherTestEnv(t, policy.ActionAllow, hook)

	if _, err := e.dispatcher.Dispatch(context.Background(), e.actionContext("some-tool")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertDispatchedHookFired(t, calls(), "allowed")
}

func TestDispatch_TokenUsageHook_FiresForApprovedEscalate(t *testing.T) {
	hook, calls := newRecordingHook()
	e := newDispatcherTestEnv(t, policy.ActionEscalate, hook)

	done := make(chan struct{})
	go func() {
		_, _ = e.dispatcher.Dispatch(context.Background(), e.actionContext("some-tool"))
		close(done)
	}()
	approvalID := waitForPendingApproval(t, e.env.pool, e.env.orgID, 5*time.Second)
	decideTestApproval(t, e.env.pool, approvalID, "approved")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Dispatch to resume after approval")
	}

	assertDispatchedHookFired(t, calls(), "escalated")
}

// ---- AC4: the actual point of this brief -- a NEW hook, added only at
// construction, fires for ALL THREE decision types with zero
// branch-specific code change anywhere in Dispatch. ----

func TestDispatch_NewHook_FiresForAllThreeDecisionTypes(t *testing.T) {
	var mu sync.Mutex
	count := 0
	counterHook := func(_ context.Context, _ mcp.ActionContext, _ DispatchOutcome) {
		mu.Lock()
		count++
		mu.Unlock()
	}

	eDeny := newDispatcherTestEnv(t, policy.ActionDeny, counterHook)
	if _, err := eDeny.dispatcher.Dispatch(context.Background(), eDeny.actionContext("some-tool")); err == nil {
		t.Fatal("expected a PolicyDeniedError for the Deny case, got nil")
	}

	eAllow := newDispatcherTestEnv(t, policy.ActionAllow, counterHook)
	if _, err := eAllow.dispatcher.Dispatch(context.Background(), eAllow.actionContext("some-tool")); err != nil {
		t.Fatalf("unexpected error for the Allow case: %v", err)
	}

	// Escalate, denied -- simpler setup than approve-and-resume; AC4 is
	// about the hook firing at all for this decision type, not about
	// Dispatched being true, which the AC3 tests above already cover.
	eEscalate := newDispatcherTestEnv(t, policy.ActionEscalate, counterHook)
	done := make(chan struct{})
	go func() {
		_, _ = eEscalate.dispatcher.Dispatch(context.Background(), eEscalate.actionContext("some-tool"))
		close(done)
	}()
	approvalID := waitForPendingApproval(t, eEscalate.env.pool, eEscalate.env.orgID, 5*time.Second)
	decideTestApproval(t, eEscalate.env.pool, approvalID, "denied")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the Escalate case to resolve")
	}

	mu.Lock()
	defer mu.Unlock()
	if count != 3 {
		t.Fatalf("counterHook fired %d times, want 3 -- one per decision type (Deny/Escalate/Allow), with zero branch-specific code in Dispatch to register it", count)
	}
}

// TestDispatch_Escalate_SubmitFails_StillConverges is the regression test
// for a mandatory code-review finding on this brief itself: the Escalate
// branch's `if submitErr != nil` case originally `return`ed directly,
// bypassing d.hooks entirely -- exactly the kind of independent early
// return B-102 exists to eliminate (the same shape as B-099's original
// bug, reintroduced inside the mechanism meant to prevent it). Forces
// Submit() to fail deterministically via its own existing validation
// (empty OrgID -- router.go's Submit rejects this before ever touching
// Postgres, no connectivity-breaking or context-cancellation trickery
// needed).
func TestDispatch_Escalate_SubmitFails_StillConverges(t *testing.T) {
	hook, calls := newRecordingHook()
	e := newDispatcherTestEnv(t, policy.ActionEscalate, hook)

	ac := e.actionContext("some-tool")
	ac.OrgID = "" // Submit() rejects this deterministically (router.go:240-242)

	result, err := e.dispatcher.Dispatch(context.Background(), ac)

	if result != nil {
		t.Errorf("Result = %v, want nil", result)
	}
	if err == nil {
		t.Fatal("expected an error from Submit's own validation, got nil")
	}

	got := calls()
	if len(got) != 1 {
		t.Fatalf("hook fired %d times, want 1 -- the Submit-failure case must converge through the same single exit as every other branch", len(got))
	}
	if got[0].dispatched {
		t.Errorf("Dispatched = true, want false -- nothing was ever dispatched")
	}
	if got[0].episodeSteps != 0 {
		t.Errorf("EpisodeSteps had %d entries, want 0 -- pre-B-102 this path never wrote an episode either (it returned before Hold() was ever reached); recordEpisodeHook's empty-steps guard must preserve that exactly, not start writing a new episode for a case that never had one", got[0].episodeSteps)
	}
}
