// dispatcher_policy_reload_test.go -- cmd/gateway
//
// B-129's adversarial proof: a Dispatcher must observe a policy change
// made to Postgres after construction, on the very next Dispatch call,
// with no restart and no rebuild. Before this fix, main.go passed
// pLoader.Evaluator() (a snapshot of policy.Evaluator taken once, at
// process startup) into NewDispatcher; Dispatch() then evaluated against
// that one frozen value forever after. pLoader.Listen()'s pg_notify
// reload correctly updated the loader's own internal state, but nothing
// downstream of construction ever re-read it -- so a policy created,
// edited, or deleted after the gateway process started had zero effect
// on any live dispatch decision, for any org, until a full restart.
//
// This test uses a REAL *policyloader.Loader (not the fakeEvaluator this
// file's sibling tests use elsewhere) as the Dispatcher's
// policy.EvaluatorSource, exactly matching production's real
// cmd/gateway/main.go wiring -- the fakeEvaluator-backed tests
// deliberately don't exercise this path at all, which is exactly how
// B-129 went unnoticed until a live-verification session actually seeded
// a policy against an already-running gateway.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./cmd/gateway/... -run TestDispatch_PolicyReload -v
package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/aiprovider"
	"github.com/eami/gateway/internal/approval"
	"github.com/eami/gateway/internal/audit"
	"github.com/eami/gateway/internal/episode"
	"github.com/eami/gateway/internal/mcp"
	"github.com/eami/gateway/internal/policyloader"
	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/toolrouter"
)

// TestDispatch_PolicyReload_TakesEffectWithoutRestart is B-129's central
// regression proof. One Dispatcher, constructed once (mirroring
// production's single long-lived process), sees two dispatches to the
// identical tool/action -- with a real policy.Deny row inserted into
// Postgres and loader.Load(ctx) called (simulating exactly what
// pLoader.Listen's pg_notify handler does) in between. The first
// dispatch must succeed (no matching policy yet, default Allow); the
// second, against the SAME Dispatcher instance with no reconstruction,
// must be denied. A Dispatcher wired the pre-fix way (a frozen
// policy.Evaluator snapshot) fails this test: the second dispatch would
// also succeed, since the snapshot never saw the new row.
func TestDispatch_PolicyReload_TakesEffectWithoutRestart(t *testing.T) {
	env := newMainTestEnv(t)
	ctx := context.Background()
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
	auditWriter, err := audit.NewWriter(ctx, env.pool)
	if err != nil {
		t.Fatalf("audit.NewWriter: %v", err)
	}

	// The real, production policyloader.Loader -- no active policy for
	// this fresh org exists yet, so this first Load() (mirroring
	// main.go's startup Load()) produces an evaluator with nothing to
	// match this org's dispatches against.
	pLoader := policyloader.New(env.pool)
	if err := pLoader.Load(ctx); err != nil {
		t.Fatalf("initial Load: %v", err)
	}

	// pLoader itself, not pLoader.Evaluator() -- the fix under test.
	dispatcher := NewDispatcher(
		toolRouter, aiProviderRouter, pLoader,
		auditWriter, episodeRecorder, approvalRouter, fwd,
		"", "", holdTimeout,
	)

	toolName := "reload-test-tool-" + uuid.New().String()[:8]
	ac := mcp.ActionContext{
		OrgID: env.orgID.String(), AgentID: agentID.String(), AgentName: agentName,
		Tool: toolName, Action: "deploy", SessionID: "reload-test-session", ReceivedAt: time.Now(),
	}

	// Before: no policy exists for this org/tool -- default Allow, real
	// dispatch through the real downstream server.
	if _, err := dispatcher.Dispatch(ctx, ac); err != nil {
		t.Fatalf("first dispatch (no policy yet): got error %v, want nil (default Allow)", err)
	}

	// An admin creates a real Deny policy for this org while the
	// Dispatcher (and, in production, the whole gateway process) keeps
	// running -- exactly what a live POST /v1/gateway/policies does,
	// simplified to a direct insert since this test doesn't need the
	// eami-api HTTP layer to prove the gateway-side reload gap.
	policyID := uuid.New()
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO policies (id, org_id, name, priority, action, status)
		VALUES ($1, $2, $3, 1, 'deny', 'active')
	`, policyID, env.orgID, "reload-test-deny-all"); err != nil {
		t.Fatalf("insert deny policy: %v", err)
	}

	// Exactly what pLoader.Listen's pg_notify handler does on receiving
	// a policy_reload notification -- this test calls it directly rather
	// than going through a real LISTEN/NOTIFY round trip, since that
	// plumbing is already proven independently by policyloader's own
	// tests; what's under test here is specifically whether Dispatch()
	// consults the result.
	if err := pLoader.Load(ctx); err != nil {
		t.Fatalf("reload Load: %v", err)
	}

	// After: the SAME Dispatcher, no reconstruction, must now deny.
	_, err = dispatcher.Dispatch(ctx, ac)
	var pd *mcp.PolicyDeniedError
	if !errors.As(err, &pd) {
		t.Fatalf("second dispatch (after live policy reload): err = %v, want *mcp.PolicyDeniedError -- "+
			"a Dispatcher that still allows this after a real policy reload is not observing "+
			"the live policy set (B-129: it is stuck on the snapshot taken at construction)", err)
	}
}

// TestDispatch_PolicyReload_FrozenSnapshotWiring_ReproducesTheBug is
// B-129's "before" proof, run against this same real Postgres and the
// same current, fixed Dispatcher/NewDispatcher -- the one and only
// variable changed versus the test above is which policy.EvaluatorSource
// implementation is handed to NewDispatcher: staticEvaluatorSource{ev:
// pLoader.Evaluator()} here, instead of pLoader itself.
//
// That substitution is not a stand-in or approximation -- it is a
// byte-for-byte reproduction of exactly what pre-fix cmd/gateway/main.go
// did: `NewDispatcher(..., pLoader.Evaluator(), ...)`, called once at
// process construction. staticEvaluatorSource.Evaluator() always returns
// the same already-evaluated policy.Evaluator value it was built with,
// which is precisely what a bare policy.Evaluator field captured once
// and never re-read behaves like. Isolating the fix to this one
// substitution (real Postgres, real Dispatcher, real inserted policy row,
// real Load() call all identical to the test above) proves the causal
// mechanism directly, rather than merely asserting the bug used to exist.
//
// This test asserts the OLD, BROKEN behavior: the second dispatch is
// still allowed even after a real policy reload, because the frozen
// snapshot never observes it. If a future change makes this test start
// failing (i.e. the second dispatch becomes denied even through a frozen
// snapshot), something else papered over the bug this reproduces --
// investigate rather than deleting this test.
func TestDispatch_PolicyReload_FrozenSnapshotWiring_ReproducesTheBug(t *testing.T) {
	env := newMainTestEnv(t)
	ctx := context.Background()
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
	auditWriter, err := audit.NewWriter(ctx, env.pool)
	if err != nil {
		t.Fatalf("audit.NewWriter: %v", err)
	}

	pLoader := policyloader.New(env.pool)
	if err := pLoader.Load(ctx); err != nil {
		t.Fatalf("initial Load: %v", err)
	}

	// The pre-fix pattern, reproduced exactly: pLoader.Evaluator() called
	// ONCE here and frozen inside staticEvaluatorSource, never re-read.
	dispatcher := NewDispatcher(
		toolRouter, aiProviderRouter, staticEvaluatorSource{ev: pLoader.Evaluator()},
		auditWriter, episodeRecorder, approvalRouter, fwd,
		"", "", holdTimeout,
	)

	toolName := "reload-test-tool-frozen-" + uuid.New().String()[:8]
	ac := mcp.ActionContext{
		OrgID: env.orgID.String(), AgentID: agentID.String(), AgentName: agentName,
		Tool: toolName, Action: "deploy", SessionID: "reload-test-session-frozen", ReceivedAt: time.Now(),
	}

	if _, err := dispatcher.Dispatch(ctx, ac); err != nil {
		t.Fatalf("first dispatch (no policy yet): got error %v, want nil (default Allow)", err)
	}

	policyID := uuid.New()
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO policies (id, org_id, name, priority, action, status)
		VALUES ($1, $2, $3, 1, 'deny', 'active')
	`, policyID, env.orgID, "reload-test-deny-all-frozen"); err != nil {
		t.Fatalf("insert deny policy: %v", err)
	}

	// The loader's OWN internal state genuinely reloads here -- proving
	// this isn't a LISTEN/NOTIFY failure, exactly as the real B-129
	// investigation confirmed via gateway logs.
	if err := pLoader.Load(ctx); err != nil {
		t.Fatalf("reload Load: %v", err)
	}

	// The bug: this dispatch still succeeds. staticEvaluatorSource's
	// frozen value never saw the reload, so Dispatch() still evaluates
	// against the empty rule set from before the Deny policy existed.
	if _, err := dispatcher.Dispatch(ctx, ac); err != nil {
		t.Fatalf("second dispatch through a frozen-snapshot-wired Dispatcher: got error %v, "+
			"want nil -- this reproduction is supposed to demonstrate the bug (still Allow "+
			"despite the reload), so an error here means the reproduction itself is broken, "+
			"not that the bug stopped existing", err)
	}
}
