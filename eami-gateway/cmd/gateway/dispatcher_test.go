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
	"log/slog"
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
	auditWriter, err := audit.NewWriter(context.Background(), env.pool)
	if err != nil {
		t.Fatalf("audit.NewWriter: %v", err)
	}
	return newDispatcherTestEnvFromEnv(t, env, action, auditWriter, extraHooks...)
}

// newDispatcherTestEnvFromEnv is the shared constructor both
// newDispatcherTestEnv (real Postgres-backed audit.Writer, every
// pre-existing test) and newDispatcherTestEnvFailingAudit (B-121, a
// fake-WriterDB-backed audit.Writer that always fails) delegate to, given
// an already-built *mainTestEnv -- identical wiring for everything except
// which *audit.Writer is passed to NewDispatcher. Takes env rather than
// building its own, since newDispatcherTestEnv already needs a real env.pool
// to construct its own real audit.Writer before this function runs.
func newDispatcherTestEnvFromEnv(t *testing.T, env *mainTestEnv, action string, auditWriter *audit.Writer, extraHooks ...DispatchHook) *dispatcherTestEnv {
	t.Helper()
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

	episodeRecorder := episode.New(env.pool)

	dispatcher := NewDispatcher(
		toolRouter, aiProviderRouter, &fakeEvaluator{action: action},
		auditWriter, episodeRecorder, approvalRouter, fwd,
		"", "", holdTimeout, extraHooks...,
	)

	return &dispatcherTestEnv{env: env, dispatcher: dispatcher, downstream: downstream, agentID: agentID, agentName: agentName}
}

// failingAuditDB (B-121) implements audit.WriterDB, always failing
// InsertEntry with a fixed, injected error -- audit.NewWithDB is the
// package's own designed test seam ("Inject a test double... to unit-test
// audit logic without a real Postgres connection"), so this deterministically
// simulates a real audit-log write failure (a full disk, a permissions
// change, a stalled DB) on demand, per branch, without needing to actually
// break the real test Postgres instance shared by every other test in this
// file.
type failingAuditDB struct {
	err error
}

func (f *failingAuditDB) GetLastHash(context.Context) (string, error) {
	// ErrNoRows seeds the hash chain with the genesis hash (writer.go's own
	// lazy-init path) -- irrelevant to what these tests prove (the INSERT
	// failure, not hash-chain seeding), and avoids needing a real prior row.
	return "", audit.ErrNoRows
}

func (f *failingAuditDB) InsertEntry(context.Context, audit.Entry) error {
	return f.err
}

// newDispatcherTestEnvFailingAudit (B-121) is newDispatcherTestEnv with the
// real Postgres-backed audit.Writer swapped for one backed by
// failingAuditDB -- every audit_log write this Dispatcher attempts fails
// deterministically with injectedErr. Everything else (the real toolRouter/
// aiProviderRouter/approvalRouter/episodeRecorder, the real downstream
// httptest server) stays genuinely real, matching this file's own
// established "no full MCP/SSE server needed, but everything Dispatch
// actually touches is real" precedent.
func newDispatcherTestEnvFailingAudit(t *testing.T, action string, injectedErr error, extraHooks ...DispatchHook) *dispatcherTestEnv {
	t.Helper()
	env := newMainTestEnv(t)
	auditWriter := audit.NewWithDB(&failingAuditDB{err: injectedErr})
	return newDispatcherTestEnvFromEnv(t, env, action, auditWriter, extraHooks...)
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
	dispatched    bool
	decision      string
	episodeSteps  int
	auditWriteErr error // B-121
}

func newRecordingHook() (hook DispatchHook, calls func() []hookCall) {
	var mu sync.Mutex
	var recorded []hookCall
	hook = func(_ context.Context, _ mcp.ActionContext, o DispatchOutcome) {
		mu.Lock()
		defer mu.Unlock()
		recorded = append(recorded, hookCall{dispatched: o.Dispatched, decision: o.Decision, episodeSteps: len(o.EpisodeSteps), auditWriteErr: o.AuditWriteErr})
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

// ---- B-121: a failed audit_log write is now logged identically for every
// decision type, via the new logAuditWriteFailureHook, instead of being
// silently discarded on Deny/Escalate/Allow-proxy-error while only
// Allow-success checked it. ----

// capturedLog is one slog record captured by captureAuditFailureLogs below.
type capturedLog struct {
	msg   string
	attrs map[string]any
}

// captureHandler is a minimal slog.Handler that appends every record it
// receives to a shared, mutex-guarded slice -- just enough to assert on
// message/attributes in a test, not a general-purpose logging facility.
//
// Deliberately does NOT forward to the real previous default handler --
// an earlier draft of this fix tried that (to avoid silently swallowing
// unrelated log output, e.g. this package's own pre-existing "episode: db
// write failed" warning, for the duration of every test using this
// capture) and it deadlocked: Go's slog.defaultHandler (the bridge to the
// classic `log` package installed when nothing has customized
// slog.Default()) internally re-enters through `log.Logger.output`'s own
// non-reentrant mutex when called from inside another handler's Handle --
// confirmed by a real hang, `go test -timeout 30s` panicking with two
// goroutines both blocked in log.(*Logger).output on the identical mutex,
// one nested inside the other's call stack. captureAuditFailureLogs below
// dumps captured records via t.Logf on failure instead, which gets the
// same "don't silently hide info from someone debugging a failure"
// outcome without touching the live default handler chain at runtime.
type captureHandler struct {
	mu      *sync.Mutex
	records *[]capturedLog
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	*h.records = append(*h.records, capturedLog{msg: r.Message, attrs: attrs})
	h.mu.Unlock()
	return nil
}

// WithAttrs/WithGroup return the receiver unchanged -- no code under test
// here ever calls slog.Default().With(...)/WithGroup(...)
// (logAuditWriteFailureHook always logs directly via slog.Error), so a
// full attrs-merging implementation would be untested, unused complexity.
// Documented here instead of silently implemented wrong.
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// captureAuditFailureLogs swaps slog's package-level default logger for one
// that records every entry, restoring the original via t.Cleanup. If the
// test using it fails, every captured record is dumped via t.Logf first --
// see captureHandler's own doc comment for why this doesn't forward to the
// real default handler live (a real, confirmed deadlock in an earlier
// draft), and this is the safe alternative that keeps failures debuggable.
//
// slog.SetDefault is genuinely global process state, so this is safe only
// under two conditions, both true today: (1) no test in this file calls
// t.Parallel() -- verified, it appears nowhere in this package outside
// this comment; (2) no OTHER slog call in this package's test files,
// including background goroutines that outlive their own test
// (recordEpisodeHook's detached `go Record(...)`, approval.Router.Run's
// LISTEN loop), ever emits a message matching findAuditFailureLog's
// "dispatch: audit_log write failed" filter -- so a leftover goroutine
// from an EARLIER test logging into a LATER test's still-installed
// capture window cannot produce a false positive, only an uncounted,
// harmless record.
func captureAuditFailureLogs(t *testing.T) func() []capturedLog {
	t.Helper()
	prev := slog.Default()
	var mu sync.Mutex
	var records []capturedLog
	slog.SetDefault(slog.New(&captureHandler{mu: &mu, records: &records}))
	t.Cleanup(func() {
		slog.SetDefault(prev)
		if t.Failed() {
			mu.Lock()
			defer mu.Unlock()
			for i, l := range records {
				t.Logf("captured log %d: msg=%q attrs=%+v", i, l.msg, l.attrs)
			}
		}
	})
	return func() []capturedLog {
		mu.Lock()
		defer mu.Unlock()
		out := make([]capturedLog, len(records))
		copy(out, records)
		return out
	}
}

// findAuditFailureLog returns the first captured "dispatch: audit_log
// write failed" record whose decision attribute matches wantDecision, or
// nil if none matches.
func findAuditFailureLog(logs []capturedLog, wantDecision string) *capturedLog {
	for i := range logs {
		if logs[i].msg == "dispatch: audit_log write failed" && logs[i].attrs["decision"] == wantDecision {
			return &logs[i]
		}
	}
	return nil
}

// ---- AC1: Deny ----

func TestDispatch_AuditWriteFailure_Deny_LoggedByHook(t *testing.T) {
	injectedErr := errors.New("b121-simulated-audit-write-failure")
	getLogs := captureAuditFailureLogs(t)
	e := newDispatcherTestEnvFailingAudit(t, policy.ActionDeny, injectedErr)

	_, err := e.dispatcher.Dispatch(context.Background(), e.actionContext("some-tool"))
	var pd *mcp.PolicyDeniedError
	if !errors.As(err, &pd) {
		t.Fatalf("Err = %v, want *mcp.PolicyDeniedError -- the audit-write failure must not change the Deny decision itself", err)
	}

	found := findAuditFailureLog(getLogs(), "denied")
	if found == nil {
		t.Fatal("expected a logged audit-write-failure record for the Deny branch, found none -- this is the exact silent discard B-121 closes")
	}
	if found.attrs["err"] == nil {
		t.Error("logged record is missing the err attribute")
	}
	if found.attrs["tool"] != "some-tool" {
		t.Errorf("logged record's tool attribute = %v, want %q", found.attrs["tool"], "some-tool")
	}
}

// ---- AC2: Escalate, both exit points that share the same pre-Hold() write ----

func TestDispatch_AuditWriteFailure_Escalate_SubmitFails_LoggedByHook(t *testing.T) {
	injectedErr := errors.New("b121-simulated-audit-write-failure")
	getLogs := captureAuditFailureLogs(t)
	e := newDispatcherTestEnvFailingAudit(t, policy.ActionEscalate, injectedErr)

	ac := e.actionContext("some-tool")
	ac.OrgID = "" // Submit() rejects this deterministically (router.go), same trick as TestDispatch_Escalate_SubmitFails_StillConverges

	_, err := e.dispatcher.Dispatch(context.Background(), ac)
	if err == nil {
		t.Fatal("expected an error from Submit's own validation, got nil")
	}

	if findAuditFailureLog(getLogs(), "escalated") == nil {
		t.Fatal("expected a logged audit-write-failure record for the Escalate/Submit-failure branch, found none -- this exit point shares the SAME pre-Hold() write as the resumed case below, and must report the same AuditWriteErr")
	}
}

func TestDispatch_AuditWriteFailure_Escalate_Approved_LoggedByHook(t *testing.T) {
	injectedErr := errors.New("b121-simulated-audit-write-failure")
	getLogs := captureAuditFailureLogs(t)
	e := newDispatcherTestEnvFailingAudit(t, policy.ActionEscalate, injectedErr)

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

	if findAuditFailureLog(getLogs(), "escalated") == nil {
		t.Fatal("expected a logged audit-write-failure record for the resumed Escalate branch, found none")
	}
}

// ---- AC3: Allow's existing correct behavior is unaffected -- still logged,
// still doesn't block the underlying dispatched call from succeeding. ----

func TestDispatch_AuditWriteFailure_Allow_LoggedByHook(t *testing.T) {
	injectedErr := errors.New("b121-simulated-audit-write-failure")
	getLogs := captureAuditFailureLogs(t)
	e := newDispatcherTestEnvFailingAudit(t, policy.ActionAllow, injectedErr)

	result, err := e.dispatcher.Dispatch(context.Background(), e.actionContext("some-tool"))
	if err != nil {
		t.Fatalf("unexpected error: %v -- an audit-write failure must not block the underlying dispatched call from succeeding, exactly as before this fix", err)
	}
	if string(result) != `{"ok":true}` {
		t.Errorf("Result = %s, want the real downstream body -- unaffected by the audit-write failure", result)
	}

	if findAuditFailureLog(getLogs(), "allowed") == nil {
		t.Fatal("expected a logged audit-write-failure record for the Allow branch, found none -- this must remain logged exactly as it always was, now via the shared hook instead of an inline special case")
	}
}

// TestDispatch_AuditWriteFailure_AllowProxyError_LoggedWithCorrectDecision
// is the regression test for a real bug this fix's own mandatory
// security-review pass found: the Allow-proxy-failure branch sets
// DispatchOutcome.Decision="allowed" (the policy-branch label -- see its
// own doc comment) but writes auditEntry.Decision="denied" to audit_log
// (a failed downstream call is audited as denied, even though the POLICY
// branch was Allow). Logging Decision there would have told an operator
// reconciling a lost row to look for a "denied" gap filed under
// "allowed" -- exactly backwards. This forces a REAL proxy failure and
// asserts the logged decision is "denied", matching AuditDecision (what
// was actually attempted), not Decision.
func TestDispatch_AuditWriteFailure_AllowProxyError_LoggedWithCorrectDecision(t *testing.T) {
	injectedErr := errors.New("b121-simulated-audit-write-failure")
	getLogs := captureAuditFailureLogs(t)

	env := newMainTestEnv(t)
	agentID, agentName := env.insertAgent(t)
	toolRouter := toolrouter.New(env.pool, nil)
	aiProviderRouter := aiprovider.New(env.pool, nil, map[string]aiprovider.Adapter{})
	holdTimeout := 5 * time.Second
	// A real listener, started then immediately closed, deterministically
	// and portably refuses the next connection attempt (the OS actually
	// held this port and released it) -- unlike pointing at an arbitrary
	// low/unused port number, which internal/proxy/proxy_test.go's own
	// TestProxy_UnreachableDownstream_ReturnsError does (127.0.0.1:1) but
	// which this test found hangs under this environment's Windows TCP
	// stack instead of refusing promptly, timing the whole test out.
	deadServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadServer.Close()
	brokenFwd := proxy.New(proxy.Config{DownstreamURL: deadServer.URL}, http.DefaultClient)
	approvalRouter := approval.New(env.pool, brokenFwd, holdTimeout, "", "", toolRouter, aiProviderRouter)
	runCtx, cancel := context.WithCancel(context.Background())
	go approvalRouter.Run(runCtx)
	t.Cleanup(cancel)
	episodeRecorder := episode.New(env.pool)
	auditWriter := audit.NewWithDB(&failingAuditDB{err: injectedErr})

	dispatcher := NewDispatcher(
		toolRouter, aiProviderRouter, &fakeEvaluator{action: policy.ActionAllow},
		auditWriter, episodeRecorder, approvalRouter, brokenFwd,
		"", "", holdTimeout,
	)
	ac := mcp.ActionContext{
		AgentID: "agent:" + agentName, AgentUUID: agentID.String(), AgentName: agentName,
		OrgID: env.orgID.String(), Tool: "some-tool", Action: "test-action",
		Parameters: map[string]any{"k": "v"}, Environment: "development",
		SessionID: "dispatch-test-" + uuid.NewString()[:8], ReceivedAt: time.Now(),
	}

	// Bounded as a safety net, not the primary mechanism -- the closed
	// listener above should refuse promptly on its own; this just ensures
	// the test fails fast with a clear timeout instead of hanging if
	// connection-refused timing is ever unreliable in some environment.
	ctx, cancelDispatch := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelDispatch()
	_, err := dispatcher.Dispatch(ctx, ac)
	if err == nil {
		t.Fatal("expected a proxy error from the unreachable downstream, got nil")
	}

	if findAuditFailureLog(getLogs(), "denied") == nil {
		t.Fatal(`expected a logged audit-write-failure record with decision="denied" for the Allow-proxy-failure branch -- got none, or it was mislabeled "allowed" (the exact bug this test regression-guards)`)
	}
}

// TestDispatch_NoAuditWriteFailure_NothingLogged is the negative-case
// sanity check: a successful write (the real Postgres-backed audit.Writer,
// same as every pre-existing test in this file) must never produce a false
// positive.
func TestDispatch_NoAuditWriteFailure_NothingLogged(t *testing.T) {
	getLogs := captureAuditFailureLogs(t)
	e := newDispatcherTestEnv(t, policy.ActionAllow)

	if _, err := e.dispatcher.Dispatch(context.Background(), e.actionContext("some-tool")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if found := findAuditFailureLog(getLogs(), "allowed"); found != nil {
		t.Errorf("expected no audit-write-failure log on a successful write, got one: %+v", found)
	}
}

// ---- AC4: B-121's own version of B-102's AC4 proof -- ONE production
// change (the AuditWriteErr field + the single logAuditWriteFailureHook,
// both added once in dispatcher.go) correctly logs a failed audit write
// for every one of the three decision types, not three independently
// hand-verified branch sites that could silently drift apart again
// later. Modeled directly on TestDispatch_NewHook_FiresForAllThreeDecisionTypes. ----

func TestDispatch_AuditWriteFailure_LoggedForAllThreeDecisionTypes(t *testing.T) {
	injectedErr := errors.New("b121-simulated-audit-write-failure")
	getLogs := captureAuditFailureLogs(t)

	eDeny := newDispatcherTestEnvFailingAudit(t, policy.ActionDeny, injectedErr)
	if _, err := eDeny.dispatcher.Dispatch(context.Background(), eDeny.actionContext("some-tool")); err == nil {
		t.Fatal("expected a PolicyDeniedError for the Deny case, got nil")
	}

	eAllow := newDispatcherTestEnvFailingAudit(t, policy.ActionAllow, injectedErr)
	if _, err := eAllow.dispatcher.Dispatch(context.Background(), eAllow.actionContext("some-tool")); err != nil {
		t.Fatalf("unexpected error for the Allow case: %v", err)
	}

	// Escalate, denied by the approver -- simpler setup than approve-and-
	// resume; the approve-and-resume sub-case is already separately proven
	// by TestDispatch_AuditWriteFailure_Escalate_Approved_LoggedByHook above.
	eEscalate := newDispatcherTestEnvFailingAudit(t, policy.ActionEscalate, injectedErr)
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

	logs := getLogs()
	for _, decision := range []string{"denied", "allowed", "escalated"} {
		if findAuditFailureLog(logs, decision) == nil {
			t.Errorf("no audit-write-failure log found for decision=%q -- the single logAuditWriteFailureHook must cover every decision type from one code change", decision)
		}
	}
	// getLogs() captures EVERY slog line emitted during this test (LISTEN
	// startup, proxy response, approval state changes, etc.), not just
	// audit-write-failure records -- count only messages matching this
	// hook's own, not the raw total.
	matching := 0
	for _, l := range logs {
		if l.msg == "dispatch: audit_log write failed" {
			matching++
		}
	}
	if matching != 3 {
		t.Errorf("audit-write-failure logs = %d, want exactly 3 (one per decision type, no duplicates, no extra firings)", matching)
	}
}
