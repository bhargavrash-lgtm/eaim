// router_holdconvergence_test.go -- eami-gateway/internal/approval
//
// AC5's proof (B-124/125): Hold()'s convergence onto one shared HoldOutcome
// construction point. Three of the four exits (approved/denied via
// resolve(), denied via the timeout-window race backstop, and the genuine
// timeout) are already proven by router_pg_test.go's own enhanced
// Resolved/Status/ApprovedBy assertions on
// TestHold_TimeoutRace_HonorsAlreadyApprovedRow,
// TestHold_TimeoutRace_HonorsAlreadyDeniedRow, and
// TestHold_Timeout_WritesExpiredStatusUsingRealColumns. This file covers
// the fourth and completes the proof: a genuinely ctx-cancelled Hold()
// must NOT be treated as a resolution -- Resolved must stay false, by
// explicit design (the original "escalated" audit_log row already stands
// as the complete record; no decision was ever made).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/approval/... -run TestHold_CtxCancelled -v
package approval

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHold_CtxCancelled_NotResolved(t *testing.T) {
	env := newApprovalTestEnv(t, 10*time.Second) // long hold -- the real timeout must never fire in this test
	req := env.newRequest()

	approvalID, err := env.router.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan HoldOutcome, 1)
	go func() {
		done <- env.router.Hold(ctx, approvalID, req)
	}()

	waitForPendingEntry(t, env.router, approvalID)
	cancel() // simulate the caller's own request context disconnecting

	select {
	case outcome := <-done:
		if outcome.Resolved {
			t.Error("Resolved = true, want false -- a ctx cancellation is not a decision; no resolution audit row should ever be written for this")
		}
		if !errors.Is(outcome.Err, context.Canceled) {
			t.Errorf("Err = %v, want context.Canceled", outcome.Err)
		}
		if outcome.Status != "" || outcome.ApprovedBy != "" {
			t.Errorf("Status/ApprovedBy = %q/%q, want both empty", outcome.Status, outcome.ApprovedBy)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Hold() never returned after ctx cancellation")
	}

	// The row itself must be untouched -- still "pending", not clobbered
	// to 'expired' or anything else by the cancelled Hold().
	var status string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT status FROM approval_requests WHERE id = $1`, approvalID,
	).Scan(&status); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if status != "pending" {
		t.Errorf("status after cancelled Hold() = %q, want \"pending\" (unchanged)", status)
	}
}
