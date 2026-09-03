// router_race_test.go -- eami-gateway/internal/approval
//
// Adversarial reproduction + regression test for B-100: Hold()'s timeout
// backstop and resolve() (the LISTEN/NOTIFY path) could both reach
// outcomeFromStatus -> dispatchApproved for the same approvalID when a
// decision lands right at the hold-timeout deadline while the real
// dispatch triggered by resolve() is still in flight. Before the fix, this
// genuinely double-dispatched to the downstream connector, and the caller
// always received the redundant SECOND call's result while the first,
// real dispatch's result was silently discarded.
//
// TestHold_ResolveRace_DoesNotDoubleDispatch deterministically reproduces
// the exact race (a slow downstream fake + a hold timeout shorter than the
// downstream's response latency) and asserts the fix closes it -- an
// adversarial proof, not a theoretical argument, per this brief's own
// standing requirement. TestHold_ResolveRace_NormalTiming_SingleDispatch
// is the negative control (normal timing, no race) proving the fix
// doesn't change behavior outside the race window.
//
// Run with -race (mandatory for this file, per the task brief):
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/approval/... -run TestHold_ResolveRace -race -v
package approval

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/testdb"
)

// newRacyApprovalTestEnv mirrors newApprovalTestEnv (router_pg_test.go)
// exactly, except the fake downstream's response is delayed by
// responseDelay -- letting a test control precisely how long
// dispatchApproved's real network round-trip takes, which is what makes
// the race in Hold()/resolve() reproducible on demand. Every real
// downstream hit increments hitCount atomically, the test's actual proof
// of dispatch count (independent of which of the two competing callers'
// result the test observes).
//
// B-122/B-140: provisions its own fresh, isolated throwaway database via
// internal/testdb, same as newApprovalTestEnv -- no per-org DELETE cleanup
// needed any more.
func newRacyApprovalTestEnv(t *testing.T, holdTimeout, responseDelay time.Duration) (*approvalTestEnv, *int32) {
	t.Helper()
	pool := testdb.NewThrowawayPool(t)
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "approval-race-test-"+orgID.String()[:8], "approval-race-test-"+orgID.String()); err != nil {
		t.Fatalf("seed org: %v", err)
	}

	agentID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		agentID, orgID, "race-test-agent-"+agentID.String()[:8], "test-model", "test-owner", "test scope",
	); err != nil {
		t.Fatalf("seed gateway_agents: %v", err)
	}

	var hitCount int32
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitCount, 1)
		if responseDelay > 0 {
			time.Sleep(responseDelay)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(downstream.Close)
	fwd := proxy.New(proxy.Config{DownstreamURL: downstream.URL}, downstream.Client())

	router := New(pool, fwd, holdTimeout, "", "", nil, nil)

	return &approvalTestEnv{pool: pool, router: router, orgID: orgID, agentID: agentID}, &hitCount
}

// waitForPendingEntry polls until Hold() (running in another goroutine)
// has registered its pendingEntry in r.pending -- resolve() silently
// no-ops if called before this, which would defeat the reproduction
// entirely rather than exercising the race.
func waitForPendingEntry(t *testing.T, r *Router, approvalID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := r.pending.Load(approvalID); ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Hold() never registered its pendingEntry -- test setup broken, not a real finding")
}

// TestHold_ResolveRace_DoesNotDoubleDispatch is the adversarial
// reproduction: a hold timeout deliberately shorter than the real
// downstream's response latency (a 5x margin -- widened from an initial
// 50ms/300ms after B-100's own mandatory code-review pass correctly
// flagged that a tighter margin risks flakiness under CI/machine load,
// since goroutine-scheduling delay and the real DB round trips between
// starting Hold() and starting resolve() both eat into the window before
// either side's timer meaningfully starts racing), with resolve() invoked
// directly (simulating the LISTEN/NOTIFY trigger) once Hold() is confirmed
// pending -- exactly the B-100 scenario. Pre-fix this reliably produced
// hitCount==2; post-fix it must be exactly 1.
func TestHold_ResolveRace_DoesNotDoubleDispatch(t *testing.T) {
	const holdTimeout = 150 * time.Millisecond
	const downstreamDelay = 750 * time.Millisecond

	env, hitCount := newRacyApprovalTestEnv(t, holdTimeout, downstreamDelay)
	req := env.newRequest()

	approvalID, err := env.router.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	type holdOutcome struct {
		err error
	}
	holdDone := make(chan holdOutcome, 1)
	go func() {
		outcome := env.router.Hold(context.Background(), approvalID, req)
		holdDone <- holdOutcome{err: outcome.Err}
	}()

	waitForPendingEntry(t, env.router, approvalID)

	// Simulate eami-api's DecideApproval committing the decision right as
	// Hold() is about to hit its (very short) timeout -- the exact real
	// production scenario (a decision landing near the hold deadline).
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE approval_requests SET status = 'approved', decided_at = now() WHERE id = $1`, approvalID,
	); err != nil {
		t.Fatalf("simulate DecideApproval: %v", err)
	}

	// Simulate the LISTEN/NOTIFY-triggered call directly -- resolve()'s
	// real dispatch (the slow downstream) is still in flight when Hold()'s
	// holdTimeout (well under the downstream delay, by a 5x margin) fires.
	go env.router.resolve(context.Background(), approvalID)

	select {
	case outcome := <-holdDone:
		if outcome.err != nil {
			t.Fatalf("Hold() returned an error for a genuinely approved+dispatched call: %v", outcome.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Hold() never returned")
	}

	// Let resolve()'s goroutine finish its own (now-deduplicated) send
	// into entry.ch -- harmless post-Hold(), just draining the test's own
	// background goroutine before asserting. Generous margin, same
	// robustness reasoning as the widened holdTimeout/downstreamDelay
	// margin above.
	time.Sleep(downstreamDelay + 500*time.Millisecond)

	got := atomic.LoadInt32(hitCount)
	if got != 1 {
		t.Fatalf("downstream connector was dispatched %d times for one approval, want exactly 1 (B-100 double-dispatch race)", got)
	}
}

// TestHold_ResolveRace_NormalTiming_SingleDispatch is the negative
// control: a hold timeout comfortably longer than the downstream's real
// latency, so Hold()'s own fast entry.ch read path is what returns --
// proving the fix doesn't change behavior outside the race window.
func TestHold_ResolveRace_NormalTiming_SingleDispatch(t *testing.T) {
	const holdTimeout = 2 * time.Second
	const downstreamDelay = 20 * time.Millisecond

	env, hitCount := newRacyApprovalTestEnv(t, holdTimeout, downstreamDelay)
	req := env.newRequest()

	approvalID, err := env.router.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	holdDone := make(chan error, 1)
	go func() {
		outcome := env.router.Hold(context.Background(), approvalID, req)
		holdDone <- outcome.Err
	}()

	waitForPendingEntry(t, env.router, approvalID)

	if _, err := env.pool.Exec(context.Background(),
		`UPDATE approval_requests SET status = 'approved', decided_at = now() WHERE id = $1`, approvalID,
	); err != nil {
		t.Fatalf("simulate DecideApproval: %v", err)
	}
	env.router.resolve(context.Background(), approvalID)

	select {
	case holdErr := <-holdDone:
		if holdErr != nil {
			t.Fatalf("Hold() returned an error for a genuinely approved+dispatched call: %v", holdErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Hold() never returned")
	}

	got := atomic.LoadInt32(hitCount)
	if got != 1 {
		t.Fatalf("downstream connector was dispatched %d times, want exactly 1 (normal, non-racing timing)", got)
	}
}
