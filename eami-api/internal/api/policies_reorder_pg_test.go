// policies_reorder_pg_test.go -- eami-api/internal/api
// Real-Postgres integration test for ReorderPolicies' transaction fix
// (B-086). Reuses toolsUpdateTestDSN (tools_update_pg_test.go) and
// seedTestOrg (workflows_test.go)'s established conventions -- same
// package, same TEST_DATABASE_URL/POSTGRES_PASSWORD pattern.
//
// Before the fix, store.Queries.ReorderPolicies looped plain q.db.Exec
// calls with no transaction, so policies' UNIQUE (org_id, priority)
// DEFERRABLE INITIALLY DEFERRED constraint was checked after every single
// row instead of once at commit -- an ordinary adjacent-pair swap (assign
// an id the priority a not-yet-updated row still currently holds) 500'd
// with a spurious unique-violation even though the final state was
// perfectly valid. Reproduced live against the real running dev stack
// before this fix existed; this test proves it against a real, isolated
// Postgres so it can't silently regress.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestReorderPolicies_RealDB -v
package api_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/store"
)

func seedTestPolicy(t *testing.T, ctx context.Context, q *store.Queries, orgID uuid.UUID, name string, priority int32) uuid.UUID {
	t.Helper()
	pol, err := q.CreatePolicy(ctx, store.CreatePolicyParams{
		OrgID: orgID, Name: name, Priority: priority, Action: "deny", Alert: false, Status: "active",
	})
	if err != nil {
		t.Fatalf("seed policy %s: %v", name, err)
	}
	return pol.ID
}

// TestReorderPolicies_RealDB_AdjacentSwap_NoSpuriousUniqueViolation proves
// AC1 against real Postgres: swapping two adjacent policies (the exact
// shape an admin's up/down click produces) succeeds -- no
// policies_org_id_priority_key violation -- and the new order persists
// correctly on reload.
func TestReorderPolicies_RealDB_AdjacentSwap_NoSpuriousUniqueViolation(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// t.Cleanup, not a plain defer -- registered first so it runs AFTER the
	// org-delete t.Cleanup below (LIFO), matching workflows_test.go's own
	// documented CLAUDE.md pool-lifecycle convention.
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "b086-reorder-test")

	// Already-sequential starting state (1, 2, 3) -- the realistic case:
	// a swap of the first two means policy2 must claim priority=1 while
	// policy1 (which still holds priority=1 until its own row updates)
	// hasn't been touched yet. This is exactly the intermediate state the
	// pre-fix non-transactional loop collided on.
	p1 := seedTestPolicy(t, ctx, q, orgID, "b086-p1", 1)
	p2 := seedTestPolicy(t, ctx, q, orgID, "b086-p2", 2)
	p3 := seedTestPolicy(t, ctx, q, orgID, "b086-p3", 3)

	// Swap p1/p2, leave p3 last -- an ordinary adjacent-pair move.
	if err := q.ReorderPolicies(ctx, orgID, []uuid.UUID{p2, p1, p3}); err != nil {
		t.Fatalf("ReorderPolicies (adjacent swap): unexpected error, the exact class this fix closes: %v", err)
	}

	rows, err := q.ListPolicies(ctx, orgID, nil)
	if err != nil {
		t.Fatalf("ListPolicies after reorder: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 policies, got %d", len(rows))
	}
	// ListPolicies orders by priority ASC -- row order IS the new rank.
	wantOrder := []uuid.UUID{p2, p1, p3}
	for i, row := range rows {
		if row.Policy.ID != wantOrder[i] {
			t.Errorf("position %d: got policy %s, want %s", i, row.Policy.ID, wantOrder[i])
		}
		if int32(i+1) != row.Policy.Priority {
			t.Errorf("position %d (policy %s): got priority %d, want %d", i, row.Policy.ID, row.Policy.Priority, i+1)
		}
	}
}

// TestReorderPolicies_RealDB_GenuineCollision_RollsBackCleanly proves the
// no-corruption guarantee: a reorder call whose final state is genuinely
// invalid (not just an intermediate-step artifact of the old bug) still
// correctly fails -- the DEFERRABLE constraint catches a real problem at
// commit -- and leaves zero partial state behind. Passing only p3 (not
// p1/p2) assigns it priority=1, which p1 -- untouched, not in this call --
// already holds; this is a genuine final-state collision the transaction
// is right to reject, distinct from the pre-fix bug's spurious rejection
// of perfectly valid reorders.
func TestReorderPolicies_RealDB_GenuineCollision_RollsBackCleanly(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
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
	orgID := seedTestOrg(t, ctx, pool, "b086-reorder-fail-test")

	p1 := seedTestPolicy(t, ctx, q, orgID, "b086-fail-p1", 1)
	p2 := seedTestPolicy(t, ctx, q, orgID, "b086-fail-p2", 2)
	p3 := seedTestPolicy(t, ctx, q, orgID, "b086-fail-p3", 3)

	err = q.ReorderPolicies(ctx, orgID, []uuid.UUID{p3})
	if err == nil {
		t.Fatalf("expected a genuine unique-violation (p3 -> priority 1 collides with untouched p1), got nil")
	}

	rows, err := q.ListPolicies(ctx, orgID, nil)
	if err != nil {
		t.Fatalf("ListPolicies after failed reorder: %v", err)
	}
	got := map[uuid.UUID]int32{}
	for _, row := range rows {
		got[row.Policy.ID] = row.Policy.Priority
	}
	if got[p1] != 1 || got[p2] != 2 || got[p3] != 3 {
		t.Fatalf("failed reorder left partial state: p1=%d (want 1) p2=%d (want 2) p3=%d (want 3) -- the transaction did not roll back cleanly",
			got[p1], got[p2], got[p3])
	}
}

// ─── B-090: concurrent overlapping reorders ────────────────────────────────

func isDeadlockError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

// TestReorderPolicies_RealDB_ConcurrentOverlappingReorders_NoCorruption is
// the AC2 centerpiece: two goroutines call ReorderPolicies concurrently
// against the SAME org with overlapping (here, fully reversed) id lists,
// many iterations, proving the batched single-statement design (B-090)
// never corrupts the priority ordering under real concurrency -- the bar
// this test holds to, matching B-090's own severity note: "no corruption"
// is required; an occasional real 40P01 deadlock is an accepted, safe,
// retryable failure mode, not something this test demands never happens.
func TestReorderPolicies_RealDB_ConcurrentOverlappingReorders_NoCorruption(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
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
	orgID := seedTestOrg(t, ctx, pool, "b090-concurrent-reorder-test")

	const n = 5
	ids := make([]uuid.UUID, n)
	for i := 0; i < n; i++ {
		ids[i] = seedTestPolicy(t, ctx, q, orgID, fmt.Sprintf("b090-concurrent-p%d", i), int32(i+1))
	}
	reversed := make([]uuid.UUID, n)
	for i, id := range ids {
		reversed[n-1-i] = id
	}

	const iterations = 20
	for iter := 0; iter < iterations; iter++ {
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs[0] = q.ReorderPolicies(ctx, orgID, ids)
		}()
		go func() {
			defer wg.Done()
			errs[1] = q.ReorderPolicies(ctx, orgID, reversed)
		}()
		wg.Wait()

		for i, err := range errs {
			if err != nil && !isDeadlockError(err) {
				t.Fatalf("iteration %d, request %d: unexpected error: %v", iter, i, err)
			}
		}

		// The core proof: whichever request "won" (or both, serialized),
		// the result is always a valid permutation of {1..n} across exactly
		// these n ids -- never a duplicate priority, never a missing one,
		// never a priority outside 1..n.
		rows, err := q.ListPolicies(ctx, orgID, nil)
		if err != nil {
			t.Fatalf("iteration %d: ListPolicies: %v", iter, err)
		}
		seen := map[int32]bool{}
		for _, row := range rows {
			if seen[row.Policy.Priority] {
				t.Fatalf("iteration %d: duplicate priority %d after concurrent reorder -- corruption", iter, row.Policy.Priority)
			}
			seen[row.Policy.Priority] = true
			if row.Policy.Priority < 1 || row.Policy.Priority > n {
				t.Fatalf("iteration %d: priority %d out of range 1..%d -- corruption", iter, row.Policy.Priority, n)
			}
		}
		if len(seen) != n {
			t.Fatalf("iteration %d: only %d distinct priorities, want %d -- corruption", iter, len(seen), n)
		}
	}
}
