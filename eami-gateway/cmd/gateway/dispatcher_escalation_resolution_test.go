// dispatcher_escalation_resolution_test.go -- cmd/gateway
//
// Integration tests for B-124/125: a second, real audit_log row is written
// once an escalation's Hold() genuinely resolves (approved, denied, or
// expired), correctly populated with approval_id/approved_by -- and the
// hash chain remains independently verifiable across both the original
// "escalated" row and the new resolution row. A ctx-cancelled Hold() must
// NOT produce a resolution row.
//
// Reuses dispatcher_test.go's own harness (newDispatcherTestEnv,
// waitForPendingApproval, decideTestApproval) and mirrors
// internal/audit/writer_pg_test.go's own hash-chain-recompute pattern
// (independently re-derive each row's hash from the documented formula,
// not just trust what's stored).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./cmd/gateway/... -run TestDispatch_Escalate_Resolution -v
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	policy "github.com/eami/policy"
)

// insertTestUser seeds a real users row so a resolution audit row's
// ApprovedBy can be a real, non-empty value -- decideTestApproval
// (dispatcher_test.go) deliberately doesn't set approved_by (it predates
// B-124/125, and no pre-existing test using it needs one), so this file
// uses decideTestApprovalAsUser below instead, for the tests that
// specifically verify ApprovedBy.
func insertTestUser(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	userID := uuid.New()
	email := "b124-resolution-test-" + userID.String()[:8] + "@example.com"
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, org_id, email, name, role) VALUES ($1, $2, $3, $4, 'admin')
	`, userID, orgID, email, "B-124 Test Approver"); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	return userID
}

// decideTestApprovalAsUser mirrors decideTestApproval (dispatcher_test.go)
// exactly, additionally setting approved_by -- matching exactly what
// eami-api's real DecideApproval handler does (approvals.go:
// ApprovedBy: uc.UserID).
func decideTestApprovalAsUser(t *testing.T, pool *pgxpool.Pool, approvalID, status string, approvedBy uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		UPDATE approval_requests SET status = $1, decision_reason = 'test decision', decided_at = now(), approved_by = $2 WHERE id = $3
	`, status, approvedBy, approvalID); err != nil {
		t.Fatalf("decide approval as user: %v", err)
	}
	payload := `{"approval_id":"` + approvalID + `"}`
	if _, err := pool.Exec(ctx, `SELECT pg_notify('approval_decision', $1)`, payload); err != nil {
		t.Fatalf("notify approval_decision: %v", err)
	}
}

// auditRow is what these tests read back from audit_log to verify content
// and independently recompute the hash chain.
type auditRow struct {
	id         uuid.UUID
	decision   string
	approvalID *string
	approvedBy string
	agentName  string
	toolName   string
	action     string
	timestamp  time.Time
	prevHash   string
	hash       string
}

// readAuditRowsForOrg returns every audit_log row for orgID, ordered by
// timestamp -- the real write order, since this package's tests never run
// with t.Parallel() (confirmed, dispatcher_test.go's own established
// invariant), so no other test's writes can interleave with this org's.
//
// Code-review finding on this fix: ORDER BY timestamp alone has no
// tiebreaker, so a genuine same-microsecond tie between this test's own two
// writes would make row order (and therefore verifyHashChain's result)
// ambiguous. Deliberately NOT "fixed" with an `id` tiebreaker -- audit_log
// ids are random UUIDs, so sorting by id on a real tie wouldn't recover the
// true write order, it would just make an already-wrong ordering
// deterministic instead of flaky, which isn't a real improvement. The
// actual risk is negligible: Timestamp is real Postgres TIMESTAMPTZ
// (microsecond precision), and this file's own two writes are separated by
// a real network/DB round trip (single-digit milliseconds at least) --
// verified clean across 5+ consecutive full-suite runs. Noted, not solved.
func readAuditRowsForOrg(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) []auditRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT id, decision, approval_id::text, COALESCE(approved_by, ''), agent_name, tool_name, action, timestamp, prev_hash, hash
		FROM audit_log WHERE org_id = $1 ORDER BY timestamp ASC
	`, orgID)
	if err != nil {
		t.Fatalf("query audit_log for org %s: %v", orgID, err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		var approvalID *string
		if err := rows.Scan(&r.id, &r.decision, &approvalID, &r.approvedBy, &r.agentName, &r.toolName, &r.action, &r.timestamp, &r.prevHash, &r.hash); err != nil {
			t.Fatalf("scan audit_log row: %v", err)
		}
		r.approvalID = approvalID
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit_log rows: %v", err)
	}
	return out
}

// verifyHashChain independently recomputes each row's hash from the exact
// formula documented in internal/audit/writer.go (and pinned by
// internal/audit/writer_pg_test.go's own tests), and confirms the SECOND
// row (if any) correctly chains from the first -- proof the resolution-row
// write doesn't perturb the chain, not just "the diff doesn't touch
// writer.go" (which it doesn't -- ResolutionAuditEntry goes through the
// exact same unmodified Writer.Write call as every other audit_log write
// in this codebase). orgID is constant across every row passed in (all
// belong to one test's own single org).
//
// Deliberately trusts rows[0]'s own STORED prev_hash as the chain's
// starting point, rather than asserting it against an externally-captured
// "head before this test ran" snapshot (an earlier draft did this, and it
// failed intermittently under `go test ./...`'s cross-package parallelism:
// audit_log is one GLOBAL chain, so a concurrently-running package's own
// test binary can legitimately write a row between this test's snapshot
// and this test's own first write -- a real, reproducible race in the TEST
// itself, not in the resolution-write code being tested).
//
// Code-review finding on this fix: trusting rows[0].prevHash outright
// leaves one real gap the removed snapshot assertion used to cover --
// this alone can't catch a bug where Write() wrongly RESET the chain (re-
// seeded to the genesis hash) instead of correctly continuing it. Closed
// with one extra, still race-free check: confirm rows[0]'s claimed
// predecessor genuinely exists as either a real prior row's hash, or the
// documented genesis hash if audit_log's real state at that moment could
// legitimately have been empty for this exact value (vanishingly unlikely
// in a real dev DB with prior test history, but not assumed impossible).
// This proves rows[0] extends SOME real, existing chain state, not merely
// that it's internally self-consistent in isolation.
func verifyHashChain(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, rows []auditRow) {
	t.Helper()
	if len(rows) == 0 {
		return
	}

	var predecessorExists bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM audit_log WHERE hash = $1)`, rows[0].prevHash,
	).Scan(&predecessorExists); err != nil {
		t.Fatalf("check rows[0]'s claimed predecessor exists: %v", err)
	}
	if !predecessorExists {
		genesis := sha256.Sum256([]byte("eami-genesis-2026"))
		if rows[0].prevHash != hex.EncodeToString(genesis[:]) {
			t.Fatalf("rows[0] (decision=%q) claims prev_hash = %q, but no row in audit_log has that hash, and it isn't the genesis hash either -- the chain was reset, not continued", rows[0].decision, rows[0].prevHash)
		}
	}

	prevHash := rows[0].prevHash
	for _, r := range rows {
		if r.prevHash != prevHash {
			t.Fatalf("row %s (decision=%q): stored prev_hash = %q, want %q (the preceding row's hash) -- chain broken between this test's OWN rows", r.id, r.decision, r.prevHash, prevHash)
		}
		content := prevHash + r.id.String() + orgID.String() + r.agentName + r.toolName + r.action + r.decision + r.timestamp.UTC().Format(time.RFC3339)
		want := sha256.Sum256([]byte(content))
		wantHash := hex.EncodeToString(want[:])
		if r.hash != wantHash {
			t.Errorf("row %s (decision=%q): stored hash = %q, independently recomputed = %q -- chain invalid", r.id, r.decision, r.hash, wantHash)
		}
		prevHash = r.hash
	}
}

// ─── AC1: approved escalation -> 2 real rows, hash chain valid ────────────

func TestDispatch_Escalate_Resolution_Approved_WritesSecondRow(t *testing.T) {
	e := newDispatcherTestEnv(t, policy.ActionEscalate)
	approverID := insertTestUser(t, e.env.pool, e.env.orgID)

	done := make(chan struct{})
	go func() {
		_, _ = e.dispatcher.Dispatch(context.Background(), e.actionContext("some-tool"))
		close(done)
	}()
	approvalID := waitForPendingApproval(t, e.env.pool, e.env.orgID, 5*time.Second)
	decideTestApprovalAsUser(t, e.env.pool, approvalID, "approved", approverID)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Dispatch to resume after approval")
	}

	rows := readAuditRowsForOrg(t, e.env.pool, e.env.orgID)
	if len(rows) != 2 {
		t.Fatalf("audit_log rows for this org = %d, want 2 (escalated + resolution)", len(rows))
	}
	escalated, resolution := rows[0], rows[1]

	if escalated.decision != "escalated" {
		t.Errorf("row 1 decision = %q, want \"escalated\"", escalated.decision)
	}
	if escalated.approvalID != nil {
		t.Errorf("row 1 (original) approval_id = %v, want NULL -- it's written before Submit() creates the approval_requests row", escalated.approvalID)
	}

	if resolution.decision != "allowed" {
		t.Errorf("row 2 (resolution) decision = %q, want \"allowed\" (approved + successfully dispatched)", resolution.decision)
	}
	if resolution.approvalID == nil || *resolution.approvalID != approvalID {
		t.Errorf("row 2 approval_id = %v, want %q", resolution.approvalID, approvalID)
	}
	if resolution.approvedBy != approverID.String() {
		t.Errorf("row 2 approved_by = %q, want %q", resolution.approvedBy, approverID.String())
	}

	verifyHashChain(t, e.env.pool, e.env.orgID, rows)
}

// ─── AC2: denied escalation -> resolution row with real approval_id/approved_by ─

func TestDispatch_Escalate_Resolution_Denied_WritesSecondRow(t *testing.T) {
	e := newDispatcherTestEnv(t, policy.ActionEscalate)
	denierID := insertTestUser(t, e.env.pool, e.env.orgID)

	done := make(chan struct{})
	go func() {
		_, _ = e.dispatcher.Dispatch(context.Background(), e.actionContext("some-tool"))
		close(done)
	}()
	approvalID := waitForPendingApproval(t, e.env.pool, e.env.orgID, 5*time.Second)
	decideTestApprovalAsUser(t, e.env.pool, approvalID, "denied", denierID)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the Escalate case to resolve")
	}

	rows := readAuditRowsForOrg(t, e.env.pool, e.env.orgID)
	if len(rows) != 2 {
		t.Fatalf("audit_log rows for this org = %d, want 2 (escalated + resolution)", len(rows))
	}
	resolution := rows[1]

	if resolution.decision != "denied" {
		t.Errorf("row 2 (resolution) decision = %q, want \"denied\"", resolution.decision)
	}
	if resolution.approvalID == nil || *resolution.approvalID != approvalID {
		t.Errorf("row 2 approval_id = %v, want %q", resolution.approvalID, approvalID)
	}
	if resolution.approvedBy != denierID.String() {
		t.Errorf("row 2 approved_by = %q, want %q (the DENYING user -- this column records who decided, not just who approved)", resolution.approvedBy, denierID.String())
	}

	verifyHashChain(t, e.env.pool, e.env.orgID, rows)
}

// ─── AC3: expired (timeout, no human decision) -> resolution row as a
// distinct, tamper-evident fact ────────────────────────────────────────────

func TestDispatch_Escalate_Resolution_Expired_WritesSecondRow(t *testing.T) {
	// A short holdTimeout and nobody ever decides -- forces the genuine
	// real Hold() timeout path (not a simulated/injected one).
	e := newDispatcherTestEnvShortHold(t, policy.ActionEscalate, 300*time.Millisecond)

	result, err := e.dispatcher.Dispatch(context.Background(), e.actionContext("some-tool"))
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if result != nil {
		t.Errorf("Result = %v, want nil", result)
	}

	rows := readAuditRowsForOrg(t, e.env.pool, e.env.orgID)
	if len(rows) != 2 {
		t.Fatalf("audit_log rows for this org = %d, want 2 (escalated + resolution)", len(rows))
	}
	resolution := rows[1]

	if resolution.decision != "denied" {
		t.Errorf("row 2 (resolution) decision = %q, want \"denied\" (an expiry blocks the action the same way a denial does)", resolution.decision)
	}
	if resolution.approvedBy != "" {
		t.Errorf("row 2 approved_by = %q, want \"\" -- nobody ever decided this, it timed out", resolution.approvedBy)
	}
	// approval_id IS still populated for an expiry -- Submit() already
	// created the row before Hold() ever started waiting.
	if resolution.approvalID == nil || *resolution.approvalID == "" {
		t.Error("row 2 approval_id is nil/empty, want the real approval_requests.id even for an expiry")
	}

	verifyHashChain(t, e.env.pool, e.env.orgID, rows)
}

// ─── AC4: ctx-cancelled Hold() does NOT produce a second row ──────────────

func TestDispatch_Escalate_Resolution_CtxCancelled_NoSecondRow(t *testing.T) {
	e := newDispatcherTestEnvShortHold(t, policy.ActionEscalate, 10*time.Second) // long hold -- must not be what ends this test

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = e.dispatcher.Dispatch(ctx, e.actionContext("some-tool"))
		close(done)
	}()
	_ = waitForPendingApproval(t, e.env.pool, e.env.orgID, 5*time.Second)
	cancel() // simulate the calling request's context being cancelled

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Dispatch never returned after ctx cancellation")
	}

	rows := readAuditRowsForOrg(t, e.env.pool, e.env.orgID)
	if len(rows) != 1 {
		t.Fatalf("audit_log rows for this org = %d, want exactly 1 (only the original \"escalated\" row -- no resolution row for a cancelled hold)", len(rows))
	}
	if rows[0].decision != "escalated" {
		t.Errorf("the one row's decision = %q, want \"escalated\"", rows[0].decision)
	}
}
