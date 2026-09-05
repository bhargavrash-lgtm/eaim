// dispatcher_provider_resolve_error_test.go -- cmd/gateway
//
// Real-Postgres integration test for B-168: a genuine (non-ErrNotFound)
// error resolving an ai_provider connector must fail the whole Dispatch
// call closed -- never falling into the resolvedTool/default switch,
// which before this fix silently rerouted the call to fwdProxy (a
// legacy, unauthenticated, Claude-incompatible static forwarder) with
// zero governance applied. Mirrors dispatcher_test.go's own
// newDispatcherTestEnvFromEnv exactly, except aiProviderRouter is wired
// to a SEPARATE, already-closed pool -- everything else (audit writer,
// toolRouter, approvalRouter, the downstream httptest server) stays
// genuinely real and healthy, so only the ai_provider resolution query
// itself fails, precisely isolating the branch this fix changes.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./cmd/gateway/... -run TestDispatch_AIProviderResolveError -v
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eami/gateway/internal/aiprovider"
	"github.com/eami/gateway/internal/approval"
	"github.com/eami/gateway/internal/audit"
	"github.com/eami/gateway/internal/episode"
	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/testdb"
	"github.com/eami/gateway/internal/toolrouter"
	policy "github.com/eami/policy"
)

// newDispatcherTestEnvBrokenAIProviderResolve is newDispatcherTestEnv
// (dispatcher_test.go) with one deliberate difference: aiProviderRouter
// is constructed against a second, already-closed pool, forcing every
// real aiprovider.Router.Resolve call to fail with a genuine,
// non-ErrNotFound error -- the exact scenario resolveAIProviderTool
// previously swallowed into a silent nil. downstreamHits counts real
// requests reaching the static fwdProxy, proving AC1's "zero raw content
// reaches fwdProxy" directly, not by inference.
func newDispatcherTestEnvBrokenAIProviderResolve(t *testing.T, action string) (*dispatcherTestEnv, *int32) {
	t.Helper()
	env := newMainTestEnv(t)
	auditWriter, err := audit.NewWriter(context.Background(), env.pool)
	if err != nil {
		t.Fatalf("audit.NewWriter: %v", err)
	}
	agentID, agentName := env.insertAgent(t)

	var downstreamHits int32
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&downstreamHits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(downstream.Close)
	fwd := proxy.New(proxy.Config{DownstreamURL: downstream.URL}, downstream.Client())

	toolRouter := toolrouter.New(env.pool, nil)

	// The deliberate break: a real, separate throwaway pool, closed
	// immediately -- every subsequent query against it returns a real,
	// deterministic, non-ErrNotFound error (pgxpool's own documented
	// closed-pool behavior), exactly like a live DB connectivity fault
	// would, without touching env.pool (which audit/toolRouter/
	// approvalRouter all still use, genuinely healthy).
	brokenPool := testdb.NewThrowawayPool(t)
	brokenPool.Close()
	aiProviderRouter := aiprovider.New(brokenPool, nil, map[string]aiprovider.Adapter{})

	holdTimeout := 5 * time.Second
	approvalRouter := approval.New(env.pool, fwd, holdTimeout, "", "", toolRouter, aiProviderRouter)
	runCtx, cancel := context.WithCancel(context.Background())
	go approvalRouter.Run(runCtx)
	t.Cleanup(cancel)

	episodeRecorder := episode.New(env.pool)

	dispatcher := NewDispatcher(
		toolRouter, aiProviderRouter, staticEvaluatorSource{ev: &fakeEvaluator{action: action}},
		auditWriter, episodeRecorder, approvalRouter, fwd,
		"", "", holdTimeout,
	)

	return &dispatcherTestEnv{env: env, dispatcher: dispatcher, downstream: downstream, agentID: agentID, agentName: agentName}, &downstreamHits
}

// TestDispatch_AIProviderResolveError_RejectsClosedNeverFallsBackToProxy
// is B-168's own central proof. The fake evaluator is deliberately set to
// ActionEscalate, not ActionAllow: this proves the resolution failure
// pre-empts the policy decision entirely (a hard deny regardless of what
// an active policy would have done), matching a real live policy like
// this session's own "Escalate Claude connector calls" rule -- the exact
// shape of the gap the investigation found.
func TestDispatch_AIProviderResolveError_RejectsClosedNeverFallsBackToProxy(t *testing.T) {
	env, downstreamHits := newDispatcherTestEnvBrokenAIProviderResolve(t, policy.ActionEscalate)

	result, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("claude"))
	if err == nil {
		t.Fatal("expected a clean rejection, got nil error")
	}
	if result != nil {
		t.Errorf("expected nil result alongside the rejection, got %s", result)
	}
	if !strings.Contains(err.Error(), "ai_provider connector resolution failed") {
		t.Errorf("expected the new clean-rejection error, got: %v", err)
	}
	if strings.Contains(err.Error(), "proxy:") {
		t.Errorf("error mentions the static proxy -- the old fallback ran instead of failing closed: %v", err)
	}

	// AC1: zero raw content reaches fwdProxy -- proven directly, not by
	// inference from the error message alone.
	if hits := atomic.LoadInt32(downstreamHits); hits != 0 {
		t.Errorf("downstream (static proxy) received %d requests, want 0 -- raw content reached the unauthenticated legacy path", hits)
	}

	// The resolution failure must pre-empt Escalate entirely -- no
	// approval_requests row means Submit() was never reached, proving
	// this never fell into the Escalate branch (and therefore never
	// exposed the identical ResolvedToolID-pinning gap that branch has).
	var approvalCount int
	if err := env.env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM approval_requests WHERE org_id = $1`, env.env.orgID).Scan(&approvalCount); err != nil {
		t.Fatalf("count approval_requests: %v", err)
	}
	if approvalCount != 0 {
		t.Errorf("approval_requests count = %d, want 0 -- the resolution failure reached the Escalate branch instead of pre-empting it", approvalCount)
	}

	// AC4: exactly one real audit_log row, Decision=denied, and critically
	// NO excess raw content -- there is no resolvedProvider to consult
	// for the connector's real AuditMode, so this must default to the
	// safe posture (no parameters), not silently include everything the
	// way the original bug's fallback effectively did.
	var decision string
	var parameters []byte
	if err := env.env.pool.QueryRow(context.Background(),
		`SELECT decision, parameters FROM audit_log WHERE org_id = $1 AND tool_name = 'claude'`,
		env.env.orgID).Scan(&decision, &parameters); err != nil {
		t.Fatalf("read audit_log row: %v", err)
	}
	if decision != "denied" {
		t.Errorf("audit_log decision = %q, want denied", decision)
	}
	if len(parameters) > 0 && string(parameters) != "{}" && string(parameters) != "null" {
		t.Errorf("audit_log parameters = %s, want empty/absent -- no excess raw content", parameters)
	}
}

// TestDispatch_AIProviderResolveError_AllowDecision_StillRejectsClosed is
// the same proof under an ActionAllow decision -- confirming the
// pre-emption isn't Escalate-specific: an immediate-Allow call also never
// reaches the resolvedTool/default switch that used to silently reroute
// it to fwdProxy.
func TestDispatch_AIProviderResolveError_AllowDecision_StillRejectsClosed(t *testing.T) {
	env, downstreamHits := newDispatcherTestEnvBrokenAIProviderResolve(t, policy.ActionAllow)

	_, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("claude"))
	if err == nil {
		t.Fatal("expected a clean rejection, got nil error")
	}
	if !strings.Contains(err.Error(), "ai_provider connector resolution failed") {
		t.Errorf("expected the new clean-rejection error, got: %v", err)
	}
	if hits := atomic.LoadInt32(downstreamHits); hits != 0 {
		t.Errorf("downstream (static proxy) received %d requests, want 0", hits)
	}
}
