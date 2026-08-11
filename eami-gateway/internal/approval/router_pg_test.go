// router_pg_test.go -- eami-gateway/internal/approval
//
// Integration tests for Submit/Hold/resolve against a REAL Postgres,
// deliberately deviating from this package's usual fake-DB test
// convention (see router_test.go, audit.WriterDB-style fakes elsewhere
// in this module): the three bugs this file exists to catch -- an
// INSERT violating real NOT NULL columns, queries referencing columns
// that don't exist, and a status-vocabulary mismatch against a real
// CHECK constraint -- are exactly the class of bug a fake/mock DB can
// never surface, since a fake has no real schema to violate. Router.pool
// is a concrete *pgxpool.Pool (not an interface), so this is also the
// only way to exercise Submit/Hold/resolve's actual SQL without
// introducing a new DB-access abstraction, which would go beyond this
// fix's Submit/Hold/resolve scope.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/approval/... -run TestSubmit -v
//	  go test ./internal/approval/... -run TestHold -v
//	  go test ./internal/approval/... -run TestResolve -v
package approval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/proxy"
)

// ─── shared test env ────────────────────────────────────────────────────────

func approvalTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/localhost:5432 layout) to run approval router integration tests against a real Postgres")
	}
	return "postgresql://eami_app:" + pw + "@localhost:5432/eami"
}

// approvalTestEnv wires a real *pgxpool.Pool against a real Postgres, a
// throwaway org + gateway_agents row for FK targets, and a Router backed
// by that pool. Everything created is cleaned up via t.Cleanup.
type approvalTestEnv struct {
	pool    *pgxpool.Pool
	router  *Router
	orgID   uuid.UUID
	agentID uuid.UUID
}

func newApprovalTestEnv(t *testing.T, holdTimeout time.Duration) *approvalTestEnv {
	t.Helper()
	dsn := approvalTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(pool.Close)

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "approval-router-test-"+orgID.String()[:8], "approval-router-test-"+orgID.String()); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID)
	})

	agentID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		agentID, orgID, "test-agent-"+agentID.String()[:8], "test-model", "test-owner", "test scope",
	); err != nil {
		t.Fatalf("seed gateway_agents: %v", err)
	}

	// Fake downstream MCP server -- lets resolve()'s "approved" branch
	// exercise a real proxy.Forward call end-to-end without a real
	// eami-gateway proxy target.
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(downstream.Close)
	fwd := proxy.New(proxy.Config{DownstreamURL: downstream.URL}, downstream.Client())

	// toolRouter/aiProviderRouter deliberately nil here -- this file's
	// existing tests exercise Submit/Hold/resolve's own SQL correctness,
	// not resume-time dynamic dispatch (see router_dispatch_test.go for
	// that). A nil pair means dispatchApproved falls straight through to
	// fwd, unchanged from this file's pre-existing behavior.
	router := New(pool, fwd, holdTimeout, "", "", nil, nil)

	return &approvalTestEnv{pool: pool, router: router, orgID: orgID, agentID: agentID}
}

func (e *approvalTestEnv) newRequest() Request {
	return Request{
		OrgID:     e.orgID.String(),
		AgentID:   e.agentID.String(),
		AgentName: "test-agent",
		Tool:      "test-tool",
		Action:    "test-action",
		Parameters: map[string]any{
			"key": "value",
		},
		SessionID: "test-session-" + uuid.NewString()[:8],
	}
}

// ─── AC1: Submit() writes a row satisfying every NOT NULL/CHECK constraint ──

func TestSubmit_WritesAllNotNullColumns(t *testing.T) {
	env := newApprovalTestEnv(t, 5*time.Second)
	req := env.newRequest()

	approvalID, err := env.router.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit failed -- this is exactly the bug this task fixes (a real NOT NULL violation): %v", err)
	}
	if approvalID == "" {
		t.Fatal("Submit returned an empty approval ID")
	}

	var (
		justification, riskLevel, status, gatewaySessionID, gatewayNodeAddress string
		expiresAt                                                              time.Time
	)
	err = env.pool.QueryRow(context.Background(), `
		SELECT justification, risk_level, status, gateway_session_id, gateway_node_address, expires_at
		FROM approval_requests WHERE id = $1`, approvalID,
	).Scan(&justification, &riskLevel, &status, &gatewaySessionID, &gatewayNodeAddress, &expiresAt)
	if err != nil {
		t.Fatalf("query written row: %v", err)
	}

	if justification == "" {
		t.Error("justification is empty, want a non-empty synthesized value")
	}
	if riskLevel != defaultRiskLevel {
		t.Errorf("risk_level = %q, want %q", riskLevel, defaultRiskLevel)
	}
	if status != "pending" {
		t.Errorf("status = %q, want \"pending\" (the schema's own DEFAULT)", status)
	}
	if gatewaySessionID != req.SessionID {
		t.Errorf("gateway_session_id = %q, want %q (req.SessionID)", gatewaySessionID, req.SessionID)
	}
	if gatewayNodeAddress == "" {
		t.Error("gateway_node_address is empty, want a non-empty hostname")
	}
	if !expiresAt.After(time.Now()) {
		t.Errorf("expires_at = %v, want a time in the future", expiresAt)
	}
}

// ─── Hold()'s timeout path uses the real columns and the schema's own
// distinct "expired" status ─────────────────────────────────────────────────

func TestHold_Timeout_WritesExpiredStatusUsingRealColumns(t *testing.T) {
	env := newApprovalTestEnv(t, 200*time.Millisecond) // short hold, nothing will ever decide it
	req := env.newRequest()

	approvalID, err := env.router.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	_, holdErr := env.router.Hold(context.Background(), approvalID, req)
	if holdErr == nil {
		t.Fatal("want Hold to return an error on timeout, got nil")
	}

	var status, decisionReason string
	err = env.pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(decision_reason, '') FROM approval_requests WHERE id = $1`, approvalID,
	).Scan(&status, &decisionReason)
	if err != nil {
		t.Fatalf("query row after timeout: %v", err)
	}
	if status != "expired" {
		t.Errorf("status after timeout = %q, want \"expired\"", status)
	}
	if decisionReason != "timed out" {
		t.Errorf("decision_reason after timeout = %q, want \"timed out\"", decisionReason)
	}
}

// TestHold_TimeoutRace_HonorsAlreadyApprovedRow proves the security-review
// finding's fix: if the row was already decided by the time Hold()'s
// timeout path runs (the narrow race window this whole mechanism guards
// against), Hold() must forward the action instead of reporting a bogus
// timeout and clobbering the real decision with 'expired'. Deterministic,
// not timing-dependent: the row is set to "approved" directly before
// Hold() ever runs, so its own timeout-path UPDATE ... WHERE status =
// 'pending' is guaranteed to match zero rows immediately, exercising the
// exact ErrNoRows fallback this fix adds.
func TestHold_TimeoutRace_HonorsAlreadyApprovedRow(t *testing.T) {
	env := newApprovalTestEnv(t, 100*time.Millisecond) // short hold -- no real decision ever signals entry.ch
	req := env.newRequest()

	approvalID, err := env.router.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Simulate DecideApproval already having committed "approved" before
	// Hold() ever reaches its timeout branch -- entry.ch is never signaled
	// (no resolve() call happens in this test), so the ONLY way Hold()
	// can discover this is the fallback re-check this fix adds.
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE approval_requests SET status = 'approved', decided_at = now() WHERE id = $1`, approvalID,
	); err != nil {
		t.Fatalf("simulate DecideApproval: %v", err)
	}

	data, holdErr := env.router.Hold(context.Background(), approvalID, req)
	if holdErr != nil {
		t.Fatalf("want Hold to forward the already-approved action instead of timing out, got error: %v", holdErr)
	}
	var body map[string]bool
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal forwarded response: %v", err)
	}
	if !body["ok"] {
		t.Errorf("want the fake downstream's {\"ok\":true} response, got %s", data)
	}

	// The row must still read "approved" -- not clobbered by a stale
	// 'expired' write from the timeout path.
	var status string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT status FROM approval_requests WHERE id = $1`, approvalID,
	).Scan(&status); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if status != "approved" {
		t.Errorf("status after Hold() = %q, want \"approved\" (must not be overwritten)", status)
	}
}

func TestHold_TimeoutRace_HonorsAlreadyDeniedRow(t *testing.T) {
	env := newApprovalTestEnv(t, 100*time.Millisecond)
	req := env.newRequest()

	approvalID, err := env.router.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	if _, err := env.pool.Exec(context.Background(),
		`UPDATE approval_requests SET status = 'denied', decision_reason = 'nope', decided_at = now() WHERE id = $1`, approvalID,
	); err != nil {
		t.Fatalf("simulate DecideApproval: %v", err)
	}

	_, holdErr := env.router.Hold(context.Background(), approvalID, req)
	if holdErr == nil {
		t.Fatal("want an error for an already-denied row, got nil")
	}

	var status string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT status FROM approval_requests WHERE id = $1`, approvalID,
	).Scan(&status); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if status != "denied" {
		t.Errorf("status after Hold() = %q, want \"denied\" (must not be overwritten to expired)", status)
	}
}

// ─── resolve(): vocabulary and column correctness for every real outcome ────

func TestResolve_ApprovedStatus_ForwardsViaProxy(t *testing.T) {
	env := newApprovalTestEnv(t, 5*time.Second)
	req := env.newRequest()

	approvalID, err := env.router.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	entry := &pendingEntry{ch: make(chan decisionResult, 1), req: req}
	env.router.pending.Store(approvalID, entry)

	// Simulate exactly what eami-api's DecideApproval writes on approval:
	// status = "approved" (never "allowed" -- the original bug's mismatch).
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE approval_requests SET status = 'approved', decided_at = now() WHERE id = $1`, approvalID,
	); err != nil {
		t.Fatalf("simulate DecideApproval: %v", err)
	}

	env.router.resolve(context.Background(), approvalID)

	select {
	case res := <-entry.ch:
		if res.err != nil {
			t.Fatalf("want no error forwarding an approved request, got: %v", res.err)
		}
		var body map[string]bool
		if err := json.Unmarshal(res.data, &body); err != nil {
			t.Fatalf("unmarshal forwarded response: %v", err)
		}
		if !body["ok"] {
			t.Errorf("want the fake downstream's {\"ok\":true} response to come through, got %s", res.data)
		}
	default:
		t.Fatal("resolve() did not signal the pending entry")
	}
}

func TestResolve_DeniedStatus_ReturnsError(t *testing.T) {
	env := newApprovalTestEnv(t, 5*time.Second)
	req := env.newRequest()

	approvalID, err := env.router.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	entry := &pendingEntry{ch: make(chan decisionResult, 1), req: req}
	env.router.pending.Store(approvalID, entry)

	if _, err := env.pool.Exec(context.Background(),
		`UPDATE approval_requests SET status = 'denied', decision_reason = 'too risky', decided_at = now() WHERE id = $1`, approvalID,
	); err != nil {
		t.Fatalf("simulate DecideApproval: %v", err)
	}

	env.router.resolve(context.Background(), approvalID)

	select {
	case res := <-entry.ch:
		if res.err == nil {
			t.Fatal("want an error for a denied approval, got nil")
		}
	default:
		t.Fatal("resolve() did not signal the pending entry")
	}
}

func TestResolve_ExpiredStatus_ReturnsError(t *testing.T) {
	env := newApprovalTestEnv(t, 5*time.Second)
	req := env.newRequest()

	approvalID, err := env.router.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	entry := &pendingEntry{ch: make(chan decisionResult, 1), req: req}
	env.router.pending.Store(approvalID, entry)

	if _, err := env.pool.Exec(context.Background(),
		`UPDATE approval_requests SET status = 'expired', decision_reason = 'timed out', decided_at = now() WHERE id = $1`, approvalID,
	); err != nil {
		t.Fatalf("set expired: %v", err)
	}

	env.router.resolve(context.Background(), approvalID)

	select {
	case res := <-entry.ch:
		if res.err == nil {
			t.Fatal("want an error for an expired approval (must block, same as denied), got nil")
		}
	default:
		t.Fatal("resolve() did not signal the pending entry")
	}
}
