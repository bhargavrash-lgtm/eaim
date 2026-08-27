// policies_reorder_retry_test.go -- eami-api/internal/api
//
// Tests for B-117's ReorderPolicies deadlock retry (isReorderDeadlock,
// reorderPoliciesWithRetry, reorderPoliciesExecFunc). Internal package (not
// api_test) since all three are unexported -- mirrors agents_internal_test.go's
// TestIsUniqueViolation_* pattern for the pure detection tests, and
// tools_update_pg_test.go's TEST_DATABASE_URL/POSTGRES_PASSWORD convention
// for the one test that needs a real Postgres to prove the eventual
// successful attempt's write genuinely persists (not just that a canned nil
// error was returned).
//
// Run:
//	go test ./internal/api/... -run 'TestIsReorderDeadlock|TestReorderPoliciesWithRetry' -v
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestReorderPoliciesWithRetry_RealDB -v
package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/store"
)

// ─── isReorderDeadlock: pure detection, no DB ──────────────────────────────

func TestIsReorderDeadlock_RealDeadlockCode(t *testing.T) {
	err := &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
	if !isReorderDeadlock(err) {
		t.Errorf("SQLSTATE 40P01 (deadlock_detected): want true, got false")
	}
}

func TestIsReorderDeadlock_OtherPgError(t *testing.T) {
	err := &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
	if isReorderDeadlock(err) {
		t.Errorf("SQLSTATE 23505 (unique_violation, not deadlock): want false, got true")
	}
}

func TestIsReorderDeadlock_NonPgError(t *testing.T) {
	if isReorderDeadlock(errors.New("some other error")) {
		t.Error("a non-PgError: want false, got true")
	}
}

func TestIsReorderDeadlock_NilError(t *testing.T) {
	if isReorderDeadlock(nil) {
		t.Error("nil error: want false, got true")
	}
}

// ─── reorderPoliciesWithRetry: control-flow proofs, no DB needed ──────────
//
// These override the package-level reorderPoliciesExecFunc test seam
// entirely, so the *store.Queries argument reorderPoliciesWithRetry is
// called with is never actually dereferenced -- nil is safe here. Restored
// via t.Cleanup so the override never leaks into another test in this
// package's test binary.

func withReorderExecOverride(t *testing.T, fn func(ctx context.Context, q *store.Queries, orgID uuid.UUID, order []uuid.UUID) error) {
	t.Helper()
	orig := reorderPoliciesExecFunc
	reorderPoliciesExecFunc = fn
	t.Cleanup(func() { reorderPoliciesExecFunc = orig })
}

// TestReorderPoliciesWithRetry_PersistentDeadlock_ExhaustsAttemptsThenFails
// proves the retry is BOUNDED, not infinite: a deadlock on every single
// attempt still eventually surfaces as an error, after exactly
// reorderPoliciesMaxAttempts calls -- not fewer (giving up too early) and
// not more (an unbounded retry loop).
func TestReorderPoliciesWithRetry_PersistentDeadlock_ExhaustsAttemptsThenFails(t *testing.T) {
	deadlock := &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
	calls := 0
	withReorderExecOverride(t, func(context.Context, *store.Queries, uuid.UUID, []uuid.UUID) error {
		calls++
		return deadlock
	})

	err := reorderPoliciesWithRetry(context.Background(), nil, uuid.New(), []uuid.UUID{uuid.New()})
	if err == nil {
		t.Fatal("persistent deadlock: want a non-nil error after exhausting attempts, got nil")
	}
	if !isReorderDeadlock(err) {
		t.Errorf("returned error = %v, want the deadlock error itself surfaced, not wrapped/lost", err)
	}
	if calls != reorderPoliciesMaxAttempts {
		t.Errorf("exec calls = %d, want exactly %d (bounded, not fewer or more)", calls, reorderPoliciesMaxAttempts)
	}
}

// TestReorderPoliciesWithRetry_NonDeadlockError_NotRetried proves only the
// specific 40P01 failure mode gets a second chance -- any other error
// (e.g. a genuine unique-violation from a real final-state collision, per
// TestReorderPolicies_RealDB_GenuineCollision_RollsBackCleanly in
// policies_reorder_pg_test.go) must surface immediately, unretried, exactly
// as it did before this fix.
func TestReorderPoliciesWithRetry_NonDeadlockError_NotRetried(t *testing.T) {
	notADeadlock := &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
	calls := 0
	withReorderExecOverride(t, func(context.Context, *store.Queries, uuid.UUID, []uuid.UUID) error {
		calls++
		return notADeadlock
	})

	err := reorderPoliciesWithRetry(context.Background(), nil, uuid.New(), []uuid.UUID{uuid.New()})
	if err == nil {
		t.Fatal("non-deadlock error: want it surfaced, got nil")
	}
	if calls != 1 {
		t.Errorf("exec calls = %d, want exactly 1 (a non-deadlock error must not be retried)", calls)
	}
}

func TestReorderPoliciesWithRetry_NoError_SingleAttempt(t *testing.T) {
	calls := 0
	withReorderExecOverride(t, func(context.Context, *store.Queries, uuid.UUID, []uuid.UUID) error {
		calls++
		return nil
	})

	if err := reorderPoliciesWithRetry(context.Background(), nil, uuid.New(), []uuid.UUID{uuid.New()}); err != nil {
		t.Fatalf("no-error case: want nil, got %v", err)
	}
	if calls != 1 {
		t.Errorf("exec calls = %d, want exactly 1 (success on the first attempt must not retry)", calls)
	}
}

// ─── AC2 centerpiece: a real deadlock, then a real successful retry ───────

func reorderRetryTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/localhost:5432 layout) to run ReorderPolicies retry integration tests against a real Postgres")
	}
	return fmt.Sprintf("postgresql://eami_app:%s@localhost:5432/eami", pw)
}

func seedReorderRetryTestOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	orgID := uuid.New()
	name := "b117-reorder-retry-" + orgID.String()[:8]
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`, orgID, name, name); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID) })
	return orgID
}

func seedReorderRetryTestPolicy(t *testing.T, ctx context.Context, q *store.Queries, orgID uuid.UUID, name string, priority int32) uuid.UUID {
	t.Helper()
	pol, err := q.CreatePolicy(ctx, store.CreatePolicyParams{
		OrgID: orgID, Name: name, Priority: priority, Action: "deny", Alert: false, Status: "active",
	})
	if err != nil {
		t.Fatalf("seed policy %s: %v", name, err)
	}
	return pol.ID
}

// TestReorderPoliciesWithRetry_RealDB_DeadlockOnFirstAttempt_RetriesAndSucceeds
// is the AC2 centerpiece: the first attempt returns a real, genuine
// *pgconn.PgError{Code:"40P01"} (a real deadlock error VALUE, matching
// B-090's own concurrency test's exact detection shape -- "simulated" per
// the task brief's own ASSUMPTIONS, since deterministically forcing a real
// two-transaction deadlock on demand isn't practical here), and the second
// attempt delegates to the REAL q.ReorderPolicies against a real seeded
// Postgres org -- proving both that the retry fires on 40P01 specifically
// AND that the eventual successful attempt's write genuinely persists the
// correct real permutation, not just that a canned nil was returned.
func TestReorderPoliciesWithRetry_RealDB_DeadlockOnFirstAttempt_RetriesAndSucceeds(t *testing.T) {
	dsn := reorderRetryTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// t.Cleanup, not a plain defer -- registered first so it runs AFTER the
	// org-delete t.Cleanup below (LIFO), per CLAUDE.md's mandatory
	// real-Postgres pool-lifecycle rule.
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedReorderRetryTestOrg(t, ctx, pool)
	p1 := seedReorderRetryTestPolicy(t, ctx, q, orgID, "b117-retry-p1", 1)
	p2 := seedReorderRetryTestPolicy(t, ctx, q, orgID, "b117-retry-p2", 2)

	deadlock := &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
	calls := 0
	withReorderExecOverride(t, func(ctx context.Context, q *store.Queries, orgID uuid.UUID, order []uuid.UUID) error {
		calls++
		if calls == 1 {
			return deadlock // simulated first-attempt deadlock, matching B-090's own accepted failure mode
		}
		return q.ReorderPolicies(ctx, orgID, order) // real attempt against real Postgres
	})

	err = reorderPoliciesWithRetry(ctx, q, orgID, []uuid.UUID{p2, p1})
	if err != nil {
		t.Fatalf("expected the retry to succeed on the second attempt, got error: %v", err)
	}
	if calls != 2 {
		t.Errorf("exec calls = %d, want exactly 2 (deadlock once, then a real success)", calls)
	}

	rows, err := q.ListPolicies(ctx, orgID, nil)
	if err != nil {
		t.Fatalf("ListPolicies after retry: %v", err)
	}
	if len(rows) != 2 || rows[0].Policy.ID != p2 || rows[1].Policy.ID != p1 {
		t.Fatalf("real permutation not applied after the successful retry: got %+v, want [p2, p1]", rows)
	}
}

// TestReorderPoliciesWithRetry_RealDB_DeadlocksThenRealTerminalFailure_LeavesNoPartialState
// covers the "still fails after exhausting the deadlock allowance" path --
// and, per this fix's own mandatory code-review pass, does so with a REAL
// terminal Postgres failure on the final attempt, not another simulated
// deadlock. (An earlier draft of this test simulated a deadlock on every
// attempt, including the last -- so no attempt ever reached real Postgres,
// making its "original order unchanged" assertion vacuously true; the
// review caught that nothing could have corrupted state that was never
// touched.) Here, attempts 1-2 simulate a deadlock; attempt 3 (the final
// one, per reorderPoliciesMaxAttempts) delegates to the REAL
// q.ReorderPolicies with an order that produces a genuine final-state
// priority collision (mirroring
// TestReorderPolicies_RealDB_GenuineCollision_RollsBackCleanly's exact
// shape: reordering only p2 to priority 1 while untouched p1 still holds
// it) -- a real, non-deadlock error that isReorderDeadlock correctly does
// NOT match, so it must surface immediately rather than retry further.
// Proves: (a) the deadlock budget is actually exhausted before falling
// through to a different real failure, (b) that real failure's own
// transaction rolls back cleanly (no lingering partial state), matching
// the existing GenuineCollision test's own no-corruption guarantee.
func TestReorderPoliciesWithRetry_RealDB_DeadlocksThenRealTerminalFailure_LeavesNoPartialState(t *testing.T) {
	dsn := reorderRetryTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedReorderRetryTestOrg(t, ctx, pool)
	p1 := seedReorderRetryTestPolicy(t, ctx, q, orgID, "b117-terminal-p1", 1)
	p2 := seedReorderRetryTestPolicy(t, ctx, q, orgID, "b117-terminal-p2", 2)

	deadlock := &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
	calls := 0
	withReorderExecOverride(t, func(ctx context.Context, q *store.Queries, orgID uuid.UUID, order []uuid.UUID) error {
		calls++
		if calls < reorderPoliciesMaxAttempts {
			return deadlock // simulated deadlock on every attempt but the last
		}
		// Final attempt: a real call against real Postgres, with an order
		// that's a genuine final-state collision, not another deadlock --
		// p2 -> priority 1 collides with untouched p1, which still holds it.
		return q.ReorderPolicies(ctx, orgID, []uuid.UUID{p2})
	})

	err = reorderPoliciesWithRetry(ctx, q, orgID, []uuid.UUID{p2, p1})
	if err == nil {
		t.Fatal("expected the real final-attempt collision error, got nil")
	}
	if isReorderDeadlock(err) {
		t.Errorf("final error = %v, want the real unique-violation surfaced directly, not misreported as a deadlock", err)
	}
	if calls != reorderPoliciesMaxAttempts {
		t.Errorf("exec calls = %d, want exactly %d (2 simulated deadlocks + 1 real terminal failure, no further retry after a non-deadlock error)", calls, reorderPoliciesMaxAttempts)
	}

	rows, listErr := q.ListPolicies(ctx, orgID, nil)
	if listErr != nil {
		t.Fatalf("ListPolicies after failed retry: %v", listErr)
	}
	if len(rows) != 2 || rows[0].Policy.ID != p1 || rows[1].Policy.ID != p2 {
		t.Fatalf("original order disturbed despite the real final attempt failing: got %+v, want unchanged [p1, p2]", rows)
	}
}

// TestReorderPoliciesRetryBackoff_IsShortAndBounded is a cheap regression
// guard against the constants themselves silently growing into something
// that would make a caller's request hang -- the task brief specifically
// scoped this to "a simple, bounded retry ... with a short backoff", not a
// generic/exponential retry framework.
func TestReorderPoliciesRetryBackoff_IsShortAndBounded(t *testing.T) {
	if reorderPoliciesMaxAttempts < 2 || reorderPoliciesMaxAttempts > 3 {
		t.Errorf("reorderPoliciesMaxAttempts = %d, want 2 or 3 (1 initial + 1-2 retries, per the task brief's own sizing)", reorderPoliciesMaxAttempts)
	}
	if reorderPoliciesRetryBackoff <= 0 || reorderPoliciesRetryBackoff > 200*time.Millisecond {
		t.Errorf("reorderPoliciesRetryBackoff = %v, want a short positive duration (not zero, not seconds-long)", reorderPoliciesRetryBackoff)
	}
}
