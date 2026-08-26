// writer_pg_test.go -- eami-gateway/internal/audit
//
// Integration tests for Writer against a REAL Postgres, following this
// session's established TEST_DATABASE_URL/POSTGRES_PASSWORD pattern.
// Centerpiece: B-078's core contract -- a per-call data-handling
// designation snapshot survives a LATER change to what a fresh resolve
// would return, because each Write() call only ever records the value
// the caller passed in at that moment, never a live re-read.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/audit/... -run RealDB -v
package audit_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/audit"
)

func auditTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/127.0.0.1:5432 layout) to run audit integration tests against a real Postgres")
	}
	return "postgresql://eami_app:" + pw + "@127.0.0.1:5432/eami"
}

func newAuditTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := auditTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func readDataHandling(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) *string {
	t.Helper()
	var v *string
	if err := pool.QueryRow(context.Background(),
		`SELECT data_handling_designation FROM audit_log WHERE id = $1`, id,
	).Scan(&v); err != nil {
		t.Fatalf("read back audit_log row %s: %v", id, err)
	}
	return v
}

func readWorkflowLinkage(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (*uuid.UUID, *int32) {
	t.Helper()
	var runID *uuid.UUID
	var stepIdx *int32
	if err := pool.QueryRow(context.Background(),
		`SELECT workflow_run_id, step_index FROM audit_log WHERE id = $1`, id,
	).Scan(&runID, &stepIdx); err != nil {
		t.Fatalf("read back audit_log row %s: %v", id, err)
	}
	return runID, stepIdx
}

func deleteAuditRow(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	// audit_log has RLS enforcing INSERT-only for the app role in
	// production (schema/migrations-v2/000001) -- this cleanup runs as
	// the same eami_app role the tests connect as, so if RLS is truly
	// enforced end-to-end in this environment, this delete itself will
	// fail loudly (not silently no-op) and t.Cleanup will report it.
	if _, err := pool.Exec(context.Background(), `DELETE FROM audit_log WHERE id = $1`, id); err != nil {
		t.Logf("cleanup: could not delete test audit_log row %s (expected if RLS REVOKE UPDATE/DELETE is applied in this environment -- see schema/migrations-v2/000001's comment): %v", id, err)
	}
}

// TestWriter_RealDB_DataHandlingSnapshot_SurvivesLaterConnectorChange is
// the centerpiece test for B-078 AC2/AC3: two real Write() calls through
// the SAME Writer instance (mirroring main.go's real long-lived
// *audit.Writer), the second carrying a DIFFERENT DataHandling value --
// simulating an admin changing a connector's designation between two real
// dispatches. Proves the FIRST row keeps its original value after the
// second write, not just "a row was written with the right value" in
// isolation.
func TestWriter_RealDB_DataHandlingSnapshot_SurvivesLaterConnectorChange(t *testing.T) {
	pool := newAuditTestPool(t)
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "audit-dh-test-"+orgID.String()[:8], "audit-dh-test-"+orgID.String()); err != nil {
		t.Fatalf("insert test org: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID) })

	w, err := audit.NewWriter(ctx, pool)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// Call 1: connector's designation is "unknown" at this moment (a
	// newly created connector, before any admin has confirmed anything).
	entry1ID := uuid.New()
	if err := w.Write(ctx, audit.Entry{
		ID: entry1ID, OrgID: orgID, AgentName: "test-agent", ToolName: "claude",
		Action: "messages", Decision: "allowed", DataHandling: "unknown",
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Write (call 1): %v", err)
	}
	t.Cleanup(func() { deleteAuditRow(t, pool, entry1ID) })

	// Simulates the admin editing the connector's designation via
	// PATCH /v1/gateway/tools/{toolId} between the two dispatches -- not
	// re-writing entry 1, which has already been durably recorded.

	// Call 2: a fresh dispatch, AFTER the (simulated) admin edit -- a real
	// resolvedProvider.DataHandling read at this later moment would now
	// return "zero_retention".
	entry2ID := uuid.New()
	if err := w.Write(ctx, audit.Entry{
		ID: entry2ID, OrgID: orgID, AgentName: "test-agent", ToolName: "claude",
		Action: "messages", Decision: "allowed", DataHandling: "zero_retention",
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Write (call 2): %v", err)
	}
	t.Cleanup(func() { deleteAuditRow(t, pool, entry2ID) })

	// The core AC3 assertion: entry 1's stored value is untouched by call 2.
	got1 := readDataHandling(t, pool, entry1ID)
	if got1 == nil || *got1 != "unknown" {
		t.Errorf("entry 1's data_handling_designation after a later designation change = %v, want \"unknown\" (must NOT retroactively change)", got1)
	}
	got2 := readDataHandling(t, pool, entry2ID)
	if got2 == nil || *got2 != "zero_retention" {
		t.Errorf("entry 2's data_handling_designation = %v, want \"zero_retention\"", got2)
	}
}

// TestWriter_RealDB_DataHandling_EmptyMeansNull proves a non-ai_provider
// dispatch (DataHandling left as the Go zero value, "") stores real SQL
// NULL, not a fabricated "unknown" -- audit_log's own column is nullable
// specifically so this distinction is possible (see the migration's own
// comment).
func TestWriter_RealDB_DataHandling_EmptyMeansNull(t *testing.T) {
	pool := newAuditTestPool(t)
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "audit-dh-null-"+orgID.String()[:8], "audit-dh-null-"+orgID.String()); err != nil {
		t.Fatalf("insert test org: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID) })

	w, err := audit.NewWriter(ctx, pool)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	entryID := uuid.New()
	if err := w.Write(ctx, audit.Entry{
		ID: entryID, OrgID: orgID, AgentName: "test-agent", ToolName: "some-rest-tool",
		Action: "query", Decision: "allowed", // DataHandling deliberately left unset
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	t.Cleanup(func() { deleteAuditRow(t, pool, entryID) })

	got := readDataHandling(t, pool, entryID)
	if got != nil {
		t.Errorf("data_handling_designation for a non-ai_provider dispatch = %q, want real SQL NULL", *got)
	}
}

// ─── B-093: workflow_run_id/step_index linkage ─────────────────────────────

// TestWriter_RealDB_WorkflowLinkage_PersistsCorrectly proves the core B-093
// write path: a real workflow-originated entry (WorkflowRunID/StepIndex set,
// mirroring what workflow/executor.go's runStep populates on the shared
// mcp.ActionContext) round-trips correctly, and a standalone entry
// (both left at their Go zero value) stores real SQL NULL for both columns
// -- distinguishing "not part of a workflow" from "step 0", which a bare
// `0` for StepIndex could not.
func TestWriter_RealDB_WorkflowLinkage_PersistsCorrectly(t *testing.T) {
	pool := newAuditTestPool(t)
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "audit-wf-test-"+orgID.String()[:8], "audit-wf-test-"+orgID.String()); err != nil {
		t.Fatalf("insert test org: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID) })

	w, err := audit.NewWriter(ctx, pool)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// Workflow-originated entry: a real run id + step 0 (the first step --
	// deliberately 0, not a later step, to prove a real "step zero" value
	// survives and isn't confused with "absent").
	runID := uuid.New()
	var stepZero int32 = 0
	wfEntryID := uuid.New()
	if err := w.Write(ctx, audit.Entry{
		ID: wfEntryID, OrgID: orgID, AgentName: "wf-agent", ToolName: "claude",
		Action: "messages", Decision: "allowed",
		WorkflowRunID: runID, StepIndex: &stepZero,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Write (workflow entry): %v", err)
	}
	t.Cleanup(func() { deleteAuditRow(t, pool, wfEntryID) })

	gotRunID, gotStep := readWorkflowLinkage(t, pool, wfEntryID)
	if gotRunID == nil || *gotRunID != runID {
		t.Errorf("workflow entry workflow_run_id = %v, want %s", gotRunID, runID)
	}
	if gotStep == nil || *gotStep != 0 {
		t.Errorf("workflow entry step_index = %v, want 0 (not NULL -- step zero must survive)", gotStep)
	}

	// Standalone entry: WorkflowRunID/StepIndex both left at their Go zero
	// value (uuid.Nil / nil), exactly what a non-workflow dispatch produces.
	standaloneID := uuid.New()
	if err := w.Write(ctx, audit.Entry{
		ID: standaloneID, OrgID: orgID, AgentName: "standalone-agent", ToolName: "claude",
		Action: "messages", Decision: "allowed",
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Write (standalone entry): %v", err)
	}
	t.Cleanup(func() { deleteAuditRow(t, pool, standaloneID) })

	gotRunID2, gotStep2 := readWorkflowLinkage(t, pool, standaloneID)
	if gotRunID2 != nil {
		t.Errorf("standalone entry workflow_run_id = %v, want real SQL NULL", *gotRunID2)
	}
	if gotStep2 != nil {
		t.Errorf("standalone entry step_index = %v, want real SQL NULL", *gotStep2)
	}
}

// TestWriter_RealDB_HashChainRemainsValid_WithWorkflowLinkage proves AC4: a
// real sequence of Write() calls -- mixing workflow-originated and
// standalone entries -- produces a hash chain that independently
// recomputes correctly end to end, using the exact formula writer.go
// documents (which explicitly excludes WorkflowRunID/StepIndex along with
// every other non-chain column). Same standard as B-078's own
// TestWriter_RealDB_HashChainRemainsValid_WithDataHandling immediately
// below -- empirical proof the new columns don't perturb tamper-evidence,
// not just "the diff doesn't touch that code."
func TestWriter_RealDB_HashChainRemainsValid_WithWorkflowLinkage(t *testing.T) {
	pool := newAuditTestPool(t)
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "audit-wf-hashchain-"+orgID.String()[:8], "audit-wf-hashchain-"+orgID.String()); err != nil {
		t.Fatalf("insert test org: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID) })

	var startPrevHash string
	if err := pool.QueryRow(ctx, `SELECT hash FROM audit_log ORDER BY timestamp DESC LIMIT 1`).Scan(&startPrevHash); err != nil {
		genesis := sha256.Sum256([]byte("eami-genesis-2026"))
		startPrevHash = hex.EncodeToString(genesis[:])
	}

	w, err := audit.NewWriter(ctx, pool)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	runID := uuid.New()
	var step0, step1 int32 = 0, 1
	type written struct {
		id            uuid.UUID
		workflowRunID uuid.UUID
		stepIndex     *int32
	}
	entries := []written{
		{id: uuid.New()}, // standalone
		{id: uuid.New(), workflowRunID: runID, stepIndex: &step0},
		{id: uuid.New(), workflowRunID: runID, stepIndex: &step1},
		{id: uuid.New()}, // standalone again
	}
	for _, e := range entries {
		ts := time.Now().UTC()
		if err := w.Write(ctx, audit.Entry{
			ID: e.id, OrgID: orgID, AgentName: "test-agent", ToolName: "claude",
			Action: "messages", Decision: "allowed",
			WorkflowRunID: e.workflowRunID, StepIndex: e.stepIndex,
			Timestamp: ts,
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		t.Cleanup(func(id uuid.UUID) func() { return func() { deleteAuditRow(t, pool, id) } }(e.id))
		time.Sleep(2 * time.Millisecond)
	}

	prevHash := startPrevHash
	for _, e := range entries {
		var orgIDStr, agentName, toolName, action, decision, hash, storedPrevHash string
		var ts time.Time
		if err := pool.QueryRow(ctx,
			`SELECT org_id::text, agent_name, tool_name, action, decision, timestamp, prev_hash, hash
			 FROM audit_log WHERE id = $1`, e.id,
		).Scan(&orgIDStr, &agentName, &toolName, &action, &decision, &ts, &storedPrevHash, &hash); err != nil {
			t.Fatalf("read back row %s: %v", e.id, err)
		}

		if storedPrevHash != prevHash {
			t.Fatalf("row %s: stored prev_hash = %q, want %q (chain broken)", e.id, storedPrevHash, prevHash)
		}

		content := prevHash + e.id.String() + orgIDStr + agentName + toolName + action + decision + ts.UTC().Format(time.RFC3339)
		want := sha256.Sum256([]byte(content))
		wantHash := hex.EncodeToString(want[:])
		if hash != wantHash {
			t.Errorf("row %s (workflow_run_id=%s): stored hash = %q, independently recomputed = %q -- chain invalid", e.id, e.workflowRunID, hash, wantHash)
		}
		prevHash = hash
	}
}

// TestWriter_RealDB_HashChainRemainsValid_WithDataHandling proves AC5: a
// real sequence of Write() calls -- mixing entries with and without
// DataHandling set -- produces a hash chain that independently
// recomputes correctly end to end, using the exact formula documented in
// writer.go (which explicitly excludes DataHandling and every other
// non-chain column). This is empirical proof the new column doesn't
// perturb tamper-evidence, not just "the diff doesn't touch that code."
func TestWriter_RealDB_HashChainRemainsValid_WithDataHandling(t *testing.T) {
	pool := newAuditTestPool(t)
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "audit-hashchain-"+orgID.String()[:8], "audit-hashchain-"+orgID.String()); err != nil {
		t.Fatalf("insert test org: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID) })

	// The real hash chain is one continuous, process-wide sequence over
	// the whole audit_log table (by design -- ADR-007), not reset per
	// test. Capture whatever the actual last hash is right now, BEFORE
	// this test's own writes, as the true starting point for independent
	// recomputation below -- assuming genesis here would be wrong
	// whenever another test (or a real prior write) already extended the
	// chain in this shared table, which is the normal case, not an edge
	// case.
	var startPrevHash string
	if err := pool.QueryRow(ctx, `SELECT hash FROM audit_log ORDER BY timestamp DESC LIMIT 1`).Scan(&startPrevHash); err != nil {
		genesis := sha256.Sum256([]byte("eami-genesis-2026"))
		startPrevHash = hex.EncodeToString(genesis[:])
	}

	w, err := audit.NewWriter(ctx, pool)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	type written struct {
		id           uuid.UUID
		dataHandling string
	}
	var seq []written
	dhValues := []string{"unknown", "", "zero_retention", "standard_retention", ""}
	for _, dh := range dhValues {
		id := uuid.New()
		ts := time.Now().UTC()
		if err := w.Write(ctx, audit.Entry{
			ID: id, OrgID: orgID, AgentName: "test-agent", ToolName: "claude",
			Action: "messages", Decision: "allowed", DataHandling: dh,
			Timestamp: ts,
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		seq = append(seq, written{id: id, dataHandling: dh})
		t.Cleanup(func(id uuid.UUID) func() { return func() { deleteAuditRow(t, pool, id) } }(id))
		// Force distinct timestamps -- GetLastHash orders by timestamp,
		// and two rows at the identical instant would make "the last
		// row" ambiguous for this test's own sequencing (not a real
		// production concern: production traffic is never this dense at
		// nanosecond granularity in a way that matters for the hash
		// chain itself, which always uses each row's OWN id regardless
		// of ordering).
		time.Sleep(2 * time.Millisecond)
	}

	// Independently recompute the chain from what's actually stored,
	// walking in insertion order, using the exact formula
	// writer.go documents (prevHash || id || orgID || agentName ||
	// toolName || action || decision || timestamp), starting from the
	// real pre-test chain state captured above.
	prevHash := startPrevHash

	for _, s := range seq {
		var orgIDStr, agentName, toolName, action, decision, hash, storedPrevHash string
		var ts time.Time
		if err := pool.QueryRow(ctx,
			`SELECT org_id::text, agent_name, tool_name, action, decision, timestamp, prev_hash, hash
			 FROM audit_log WHERE id = $1`, s.id,
		).Scan(&orgIDStr, &agentName, &toolName, &action, &decision, &ts, &storedPrevHash, &hash); err != nil {
			t.Fatalf("read back row %s: %v", s.id, err)
		}

		if storedPrevHash != prevHash {
			t.Fatalf("row %s: stored prev_hash = %q, want %q (chain broken)", s.id, storedPrevHash, prevHash)
		}

		content := prevHash + s.id.String() + orgIDStr + agentName + toolName + action + decision + ts.UTC().Format(time.RFC3339)
		want := sha256.Sum256([]byte(content))
		wantHash := hex.EncodeToString(want[:])
		if hash != wantHash {
			t.Errorf("row %s (data_handling=%q): stored hash = %q, independently recomputed = %q -- chain invalid", s.id, s.dataHandling, hash, wantHash)
		}
		prevHash = hash
	}
}
