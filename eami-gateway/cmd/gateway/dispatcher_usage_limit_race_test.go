// dispatcher_usage_limit_race_test.go -- cmd/gateway
//
// Real-Postgres integration test for B-173: the concurrency guard
// license.Store.ReserveIfNearLimit adds on top of WithinUsageLimit's own
// check-then-act read (dispatcher_usage_limit_test.go). Proves AC2 -- a
// genuine concurrency test, not a sequential one -- multiple real,
// concurrent Dispatch calls racing the SAME near-exhausted usage limit
// never all proceed at once; at most one is ever in flight per org.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./cmd/gateway/... -run TestDispatch_UsageLimitNearCap -v
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eami/gateway/internal/aiprovider"
	"github.com/eami/gateway/internal/approval"
	"github.com/eami/gateway/internal/audit"
	"github.com/eami/gateway/internal/episode"
	"github.com/eami/gateway/internal/license"
	"github.com/eami/gateway/internal/mcp"
	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/toolrouter"
	policy "github.com/eami/policy"
)

// newDispatcherTestEnvRealLicenseSlowDownstream is
// newDispatcherTestEnvRealLicense (dispatcher_license_test.go) with one
// difference: the static downstream responds only after delay, not
// immediately. This test needs a genuinely wide, reliably-observable
// in-flight window -- without an artificial delay, a real dispatch's own
// in-flight duration could be too short to deterministically prove several
// concurrent callers' checks actually overlapped it, exactly the kind of
// flakiness a "genuine concurrency test, not a sequential one" (this
// brief's own TEST REQUIREMENT) must not have. Duplicated rather than
// parameterizing the shared helper: that helper has no existing caller
// needing a configurable delay, and every other test in this package
// depends on its current fast-response behavior.
func newDispatcherTestEnvRealLicenseSlowDownstream(t *testing.T, delay time.Duration) *dispatcherTestEnv {
	t.Helper()
	env := newMainTestEnv(t)
	auditWriter, err := audit.NewWriter(context.Background(), env.pool)
	if err != nil {
		t.Fatalf("audit.NewWriter: %v", err)
	}
	agentID, agentName := env.insertAgent(t)

	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(downstream.Close)
	fwd := proxy.New(proxy.Config{DownstreamURL: downstream.URL}, downstream.Client())

	toolRouter := toolrouter.New(env.pool, nil)
	aiProviderRouter := aiprovider.New(env.pool, nil, map[string]aiprovider.Adapter{})
	episodeRecorder := episode.New(env.pool)
	// margin/TTL match config.go's own documented defaults (5%, 120s) --
	// this test relies on the real 5% margin to put its seeded usage inside
	// the near-cap zone.
	licenseStore := license.New(env.pool, 5, 120)
	approvalRouter := approval.New(env.pool, fwd, 5*time.Second, "", "", toolRouter, aiProviderRouter, licenseStore)
	runCtx, cancel := context.WithCancel(context.Background())
	go approvalRouter.Run(runCtx)
	t.Cleanup(cancel)

	dispatcher := NewDispatcher(
		toolRouter, aiProviderRouter, licenseStore, staticEvaluatorSource{ev: &fakeEvaluator{action: policy.ActionAllow}},
		auditWriter, episodeRecorder, approvalRouter, fwd,
		"", "", 5*time.Second,
	)
	return &dispatcherTestEnv{env: env, dispatcher: dispatcher, downstream: downstream, agentID: agentID, agentName: agentName}
}

// TestDispatch_UsageLimitNearCap_ConcurrentDispatchesSerialize (B-173, AC2)
// is this brief's own centerpiece: once usage is within the configured
// safety margin of a real, configured cap, several genuinely concurrent
// Dispatch calls racing that same near-exhausted limit must never all be
// allowed to proceed -- at most one is ever in flight per org at a time,
// closing the race WithinUsageLimit's own check-then-act read leaves open
// (see license.Store.ReserveIfNearLimit's own doc comment).
func TestDispatch_UsageLimitNearCap_ConcurrentDispatchesSerialize(t *testing.T) {
	// A full second of artificial delay, not just a few hundred ms: the 5
	// reservation checks below are themselves serialized through a single
	// Postgres advisory lock and should each take single-digit
	// milliseconds, but a generous margin keeps this reliable on a slow or
	// loaded CI machine rather than becoming a rare, hard-to-reproduce
	// flake.
	env := newDispatcherTestEnvRealLicenseSlowDownstream(t, time.Second)

	limit := int64(100)
	raw := signTestLicenseWithUsageLimit(t, env.env.orgID.String(), []string{"gateway"}, time.Now().Add(365*24*time.Hour), &limit)
	if _, err := env.env.pool.Exec(context.Background(),
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW(), $4)`,
		env.env.orgID, raw, []string{"gateway"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("insert license: %v", err)
	}
	// 96/100 -- inside the real 5% safety margin (threshold = 100 - 5 =
	// 95), so ReserveIfNearLimit's serialization guard is genuinely
	// engaged, not just WithinUsageLimit's coarser under/over check.
	env.insertUsageRow(t, 48, 48)

	const concurrency = 5
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("some-tool"))
			results[i] = err
		}(i)
	}
	close(start) // release all goroutines at once
	wg.Wait()

	var succeeded, contended int
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case strings.Contains(err.Error(), "another call is currently in flight"):
			contended++
		default:
			t.Errorf("unexpected dispatch error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Errorf("succeeded = %d, want exactly 1 -- a near-cap reservation must serialize concurrent dispatches to one in flight at a time", succeeded)
	}
	if contended != concurrency-1 {
		t.Errorf("contended = %d, want %d", contended, concurrency-1)
	}

	// The one dispatch that succeeded must have released its reservation on
	// completion -- no row should be left behind to needlessly block a
	// follow-up dispatch.
	var remaining int
	if err := env.env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM usage_dispatch_inflight WHERE org_id = $1 AND expires_at > now()`,
		env.env.orgID).Scan(&remaining); err != nil {
		t.Fatalf("count remaining reservations: %v", err)
	}
	if remaining != 0 {
		t.Errorf("remaining in-flight reservations = %d, want 0 after the successful dispatch completed and released", remaining)
	}
}

// TestDispatchApproved_UsageLimitNearCap_ConcurrentResumesSerialize (B-173,
// wired into the escalation-resume path per this brief's own mandatory
// code-review AND security-review findings) proves the gap both
// independent reviews flagged: Dispatch's own near-cap reservation is
// deliberately released BEFORE Hold() blocks on a human approval wait (see
// Dispatch's own comment), so the real downstream call for an ESCALATED
// request only happens later, in approval.Router.dispatchApproved -- and
// without its OWN reservation there, two escalated near-cap requests for
// the same org, approved close together, could both pass the plain
// WithinUsageLimit re-check (B-172) and both genuinely dispatch. This test
// proves that's now closed: at most one of two concurrently-resumed,
// near-cap escalations actually reaches the real downstream call.
//
// A single in-process approval.Router's LISTEN goroutine already
// serializes every resolve()-triggered dispatchApproved call onto one
// goroutine (see router.go's own doc comments), so genuine concurrency
// between two dispatchApproved calls requires two INDEPENDENT Router
// instances, each with its own LISTEN connection and its own pending map
// -- exactly mirroring a real multi-node deployment (approval.Router's own
// package doc comment: "If no gateway node has a pending Hold() waiter ...
// other nodes handle their own pending map"). Both routers here share the
// same real Postgres pool/license.Store/downstream, matching what two
// real gateway nodes would share (one on-prem Postgres).
func TestDispatchApproved_UsageLimitNearCap_ConcurrentResumesSerialize(t *testing.T) {
	env := newMainTestEnv(t)
	auditWriter, err := audit.NewWriter(context.Background(), env.pool)
	if err != nil {
		t.Fatalf("audit.NewWriter: %v", err)
	}
	agentID, agentName := env.insertAgent(t)

	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second) // see the file-level comment on the sibling test above
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(downstream.Close)
	fwd := proxy.New(proxy.Config{DownstreamURL: downstream.URL}, downstream.Client())

	toolRouter := toolrouter.New(env.pool, nil)
	aiProviderRouter := aiprovider.New(env.pool, nil, map[string]aiprovider.Adapter{})
	licenseStore := license.New(env.pool, 5, 120)

	newNode := func() *Dispatcher {
		approvalRouter := approval.New(env.pool, fwd, 5*time.Second, "", "", toolRouter, aiProviderRouter, licenseStore)
		runCtx, cancel := context.WithCancel(context.Background())
		go approvalRouter.Run(runCtx)
		t.Cleanup(cancel)
		episodeRecorder := episode.New(env.pool)
		return NewDispatcher(
			toolRouter, aiProviderRouter, licenseStore, staticEvaluatorSource{ev: &fakeEvaluator{action: policy.ActionEscalate}},
			auditWriter, episodeRecorder, approvalRouter, fwd,
			"", "", 5*time.Second,
		)
	}
	dispatcherA := newNode()
	dispatcherB := newNode()

	limit := int64(100)
	raw := signTestLicenseWithUsageLimit(t, env.orgID.String(), []string{"gateway"}, time.Now().Add(365*24*time.Hour), &limit)
	if _, err := env.pool.Exec(context.Background(),
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW(), $4)`,
		env.orgID, raw, []string{"gateway"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("insert license: %v", err)
	}
	// 96/100 -- inside the real 5% safety margin, same as the sibling
	// Dispatch-level test above.
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO token_usage (org_id, agent_id, agent_name, model, tokens_in, tokens_out, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		env.orgID, agentID, agentName, "usage-limit-race-test-model", 48, 48,
	); err != nil {
		t.Fatalf("insert token_usage: %v", err)
	}

	actionContext := func(sessionSuffix string) mcp.ActionContext {
		return mcp.ActionContext{
			AgentID:     "agent:" + agentName,
			AgentUUID:   agentID.String(),
			AgentName:   agentName,
			OrgID:       env.orgID.String(),
			Tool:        "some-tool",
			Action:      "test-action",
			Parameters:  map[string]any{"k": "v"},
			Environment: "development",
			SessionID:   "dispatch-approved-race-test-" + sessionSuffix,
			ReceivedAt:  time.Now(),
		}
	}

	type dispatchResult struct {
		result []byte
		err    error
	}
	resultCh := make(chan dispatchResult, 2)

	// Submit A, then wait for ITS pending approval before starting B --
	// deliberately sequential here, not concurrent: Dispatch's own initial
	// near-cap reservation (this same brief) is ALSO exclusive per org, and
	// A holds it from the top of Dispatch() until right before Hold()
	// (Escalate's own explicit early release). Starting B concurrently
	// with A would just race Dispatch's own initial gate -- A would win it,
	// B would be rejected before ever reaching Submit(), and only ONE
	// approval_requests row would ever exist, proving nothing about
	// dispatchApproved's OWN reservation (what this test targets). Once A
	// reaches Hold() it has already released Dispatch's initial
	// reservation (per that branch's own comment), so B's own initial
	// check proceeds cleanly -- the real race this test needs happens
	// later, when both are decided approved and resume concurrently.
	go func() {
		r, err := dispatcherA.Dispatch(context.Background(), actionContext("a"))
		resultCh <- dispatchResult{r, err}
	}()
	approvalIDA := waitForPendingApproval(t, env.pool, env.orgID, 5*time.Second)

	go func() {
		r, err := dispatcherB.Dispatch(context.Background(), actionContext("b"))
		resultCh <- dispatchResult{r, err}
	}()
	var approvalIDB string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var id string
		err := env.pool.QueryRow(context.Background(), `
			SELECT id FROM approval_requests WHERE org_id = $1 AND status = 'pending' AND id != $2
			ORDER BY created_at DESC LIMIT 1
		`, env.orgID, approvalIDA).Scan(&id)
		if err == nil {
			approvalIDB = id
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if approvalIDB == "" {
		t.Fatal("timed out waiting for B's own pending approval_requests row")
	}
	approvalIDs := []string{approvalIDA, approvalIDB}

	// NOW decide both approved back-to-back -- as close together as this
	// process can manage, so both routers' independent LISTEN goroutines
	// pick up their own approval and call dispatchApproved at nearly the
	// same instant. This is the real race: both A and B are genuinely
	// blocked in Hold() at this point, neither holding any reservation.
	decideTestApproval(t, env.pool, approvalIDs[0], "approved")
	decideTestApproval(t, env.pool, approvalIDs[1], "approved")

	var results []dispatchResult
	for i := 0; i < 2; i++ {
		select {
		case r := <-resultCh:
			results = append(results, r)
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for both dispatches to return")
		}
	}

	var succeeded, contended int
	for _, r := range results {
		switch {
		case r.err == nil:
			succeeded++
		case strings.Contains(r.err.Error(), "another call is currently in flight"):
			contended++
		default:
			t.Errorf("unexpected dispatch error: %v", r.err)
		}
	}
	if succeeded != 1 {
		t.Errorf("succeeded = %d, want exactly 1 -- two concurrently-resumed near-cap escalations must serialize to one real dispatch at a time", succeeded)
	}
	if contended != 1 {
		t.Errorf("contended = %d, want 1", contended)
	}

	var outcome0, outcome1 string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COALESCE(resume_outcome, '') FROM approval_requests WHERE id = $1`, approvalIDs[0]).Scan(&outcome0); err != nil {
		t.Fatalf("read resume_outcome[0]: %v", err)
	}
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COALESCE(resume_outcome, '') FROM approval_requests WHERE id = $1`, approvalIDs[1]).Scan(&outcome1); err != nil {
		t.Fatalf("read resume_outcome[1]: %v", err)
	}
	contentionOutcomes := 0
	for _, o := range []string{outcome0, outcome1} {
		if o == "usage_limit_contention" {
			contentionOutcomes++
		}
	}
	if contentionOutcomes != 1 {
		t.Errorf("resume_outcome='usage_limit_contention' count = %d (outcomes: %q, %q), want 1", contentionOutcomes, outcome0, outcome1)
	}
}

// TestDispatch_UsageLimitComfortablyUnderMargin_NoSerialization is the
// no-regression companion: usage well under the safety margin never pays
// any extra serialization at all -- several concurrent dispatches all
// succeed, exactly like before B-173 existed.
func TestDispatch_UsageLimitComfortablyUnderMargin_NoSerialization(t *testing.T) {
	env := newDispatcherTestEnvRealLicenseSlowDownstream(t, 200*time.Millisecond)

	limit := int64(1_000_000)
	raw := signTestLicenseWithUsageLimit(t, env.env.orgID.String(), []string{"gateway"}, time.Now().Add(365*24*time.Hour), &limit)
	if _, err := env.env.pool.Exec(context.Background(),
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW(), $4)`,
		env.env.orgID, raw, []string{"gateway"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("insert license: %v", err)
	}
	env.insertUsageRow(t, 10, 10) // nowhere near the margin around 1,000,000

	const concurrency = 5
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("some-tool"))
			results[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range results {
		if err != nil {
			t.Errorf("dispatch %d: expected success (usage comfortably under the margin), got: %v", i, err)
		}
	}
}
