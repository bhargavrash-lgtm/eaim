// writer_test.go — eami-gateway/internal/audit
// QA-EAMI — unit tests for Writer (hash-chained audit log).
//
// Actual API (confirmed from writer.go source):
//   audit.NewWithDB(db WriterDB) *Writer   — no ctx, no error
//   audit.ErrNoRows                        — returned by GetLastHash on empty table
//   audit.Entry{
//       ID uuid.UUID, OrgID uuid.UUID, AgentID uuid.UUID,
//       AgentName, ToolName, Action, Decision string,
//       Timestamp time.Time,
//       PrevHash string, Hash string,       -- set by Writer.Write
//   }
//
// Hash formula (must match verify-audit-log.sh):
//   SHA-256(prevHash || id || orgID || agentName || toolName || action || decision || timestamp.RFC3339)
// Genesis:
//   SHA-256("eami-genesis-2026")
//
// Security regression test (marked SECURITY-FAILING):
//   TestWriter_DBErrorOnInit_PropagatesError → TASK-054 (AUDIT-001)
//
// Run:
//   go test ./internal/audit/... -race -count=1

package audit_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/eami/gateway/internal/audit"
	"github.com/google/uuid"
)

// ─── fakeDB ───────────────────────────────────────────────────────────────────

// fakeDB is a thread-safe in-memory WriterDB.
// It returns ErrNoRows when empty (matching the real pgxpoolDB behaviour).
type fakeDB struct {
	mu      sync.Mutex
	entries []audit.Entry

	// Error injection.
	getHashErr error // returned by GetLastHash (set to non-nil to simulate DB failure)
	insErr     error // returned by InsertEntry

	// Call counters.
	getHashCalls int
	insCalls     int
}

func newFakeDB() *fakeDB { return &fakeDB{} }

func (f *fakeDB) GetLastHash(_ context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getHashCalls++
	if f.getHashErr != nil {
		return "", f.getHashErr
	}
	if len(f.entries) == 0 {
		return "", audit.ErrNoRows
	}
	return f.entries[len(f.entries)-1].Hash, nil
}

func (f *fakeDB) InsertEntry(_ context.Context, e audit.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.insCalls++
	if f.insErr != nil {
		return f.insErr
	}
	f.entries = append(f.entries, e)
	return nil
}

func (f *fakeDB) all() []audit.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]audit.Entry, len(f.entries))
	copy(cp, f.entries)
	return cp
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

var genesisHash = func() string {
	h := sha256.Sum256([]byte("eami-genesis-2026"))
	return hex.EncodeToString(h[:])
}()

// computeExpectedHash replicates the writer.go hash formula for test assertions.
func computeExpectedHash(prevHash string, e audit.Entry) string {
	content := prevHash +
		e.ID.String() +
		e.OrgID.String() +
		e.AgentName +
		e.ToolName +
		e.Action +
		e.Decision +
		e.Timestamp.UTC().Format(time.RFC3339)
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// sampleEntry returns a fully-populated Entry with a stable Timestamp
// (truncated to second so RFC3339 round-trip is lossless).
func sampleEntry(agentName, tool, action, decision string) audit.Entry {
	return audit.Entry{
		ID:        uuid.New(),
		OrgID:     uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001"),
		AgentID:   uuid.New(),
		AgentName: agentName,
		ToolName:  tool,
		Action:    action,
		Decision:  decision,
		Timestamp: time.Now().UTC().Truncate(time.Second),
	}
}

// ─── Hash chain tests ─────────────────────────────────────────────────────────

// TestWriter_FirstWrite_UsesGenesisHash verifies the genesis seed on an empty table.
func TestWriter_FirstWrite_UsesGenesisHash(t *testing.T) {
	db := newFakeDB()
	w := audit.NewWithDB(db)

	e := sampleEntry("billing-bot", "stripe", "charge", "allowed")
	if err := w.Write(context.Background(), e); err != nil {
		t.Fatalf("Write: %v", err)
	}

	rows := db.all()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].PrevHash != genesisHash {
		t.Errorf("PrevHash: got %q\n want genesis %q", rows[0].PrevHash, genesisHash)
	}
	want := computeExpectedHash(genesisHash, rows[0])
	if rows[0].Hash != want {
		t.Errorf("Hash mismatch:\n got:  %s\n want: %s", rows[0].Hash, want)
	}
}

// TestWriter_SecondWrite_ChainContinues verifies row[1].PrevHash == row[0].Hash.
func TestWriter_SecondWrite_ChainContinues(t *testing.T) {
	db := newFakeDB()
	w := audit.NewWithDB(db)
	ctx := context.Background()

	e1 := sampleEntry("billing-bot", "stripe", "charge", "allowed")
	e2 := sampleEntry("billing-bot", "stripe", "refund", "denied")
	if err := w.Write(ctx, e1); err != nil {
		t.Fatalf("Write e1: %v", err)
	}
	if err := w.Write(ctx, e2); err != nil {
		t.Fatalf("Write e2: %v", err)
	}

	rows := db.all()
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[1].PrevHash != rows[0].Hash {
		t.Errorf("chain broken:\n row[1].PrevHash=%q\n row[0].Hash=%q",
			rows[1].PrevHash, rows[0].Hash)
	}
	want := computeExpectedHash(rows[0].Hash, rows[1])
	if rows[1].Hash != want {
		t.Errorf("row[1] Hash mismatch:\n got:  %s\n want: %s", rows[1].Hash, want)
	}
}

// TestWriter_ThreeWrites_FullChainVerifies walks the full 3-row chain.
func TestWriter_ThreeWrites_FullChainVerifies(t *testing.T) {
	db := newFakeDB()
	w := audit.NewWithDB(db)
	ctx := context.Background()

	entries := []audit.Entry{
		sampleEntry("agent-a", "salesforce", "query", "allowed"),
		sampleEntry("agent-a", "salesforce", "update", "escalated"),
		sampleEntry("agent-a", "salesforce", "delete", "denied"),
	}
	for i, e := range entries {
		if err := w.Write(ctx, e); err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}
	}

	rows := db.all()
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	prev := genesisHash
	for i, row := range rows {
		if row.PrevHash != prev {
			t.Errorf("row[%d] PrevHash=%q want=%q", i, row.PrevHash, prev)
		}
		want := computeExpectedHash(prev, row)
		if row.Hash != want {
			t.Errorf("row[%d] Hash mismatch:\n got:  %s\n want: %s", i, row.Hash, want)
		}
		prev = row.Hash
	}
}

// TestWriter_EmptyID_AutoAssigned verifies uuid.Nil is replaced with a fresh UUID.
func TestWriter_EmptyID_AutoAssigned(t *testing.T) {
	db := newFakeDB()
	w := audit.NewWithDB(db)

	e := sampleEntry("bot", "tool", "action", "allowed")
	e.ID = uuid.Nil

	if err := w.Write(context.Background(), e); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows := db.all()
	if rows[0].ID == uuid.Nil {
		t.Error("ID was not assigned: still uuid.Nil after Write")
	}
}

// TestWriter_TimestampZero_AutoSet verifies a zero Timestamp is set to now.
func TestWriter_TimestampZero_AutoSet(t *testing.T) {
	db := newFakeDB()
	w := audit.NewWithDB(db)

	e := sampleEntry("bot", "tool", "action", "allowed")
	e.Timestamp = time.Time{}

	if err := w.Write(context.Background(), e); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows := db.all()
	if rows[0].Timestamp.IsZero() {
		t.Error("Timestamp was not set: still zero after Write")
	}
}

// TestWriter_InsertError_Propagated verifies InsertEntry errors surface and
// do NOT advance the in-memory lastHash.
func TestWriter_InsertError_Propagated(t *testing.T) {
	db := newFakeDB()
	db.insErr = fmt.Errorf("disk full")
	w := audit.NewWithDB(db)

	e := sampleEntry("bot", "tool", "action", "allowed")
	err := w.Write(context.Background(), e)
	if err == nil {
		t.Fatal("expected error from InsertEntry, got nil")
	}

	// After a failed write the hash chain must NOT have advanced.
	// A subsequent successful write should still chain from genesis.
	db.insErr = nil
	e2 := sampleEntry("bot", "tool", "action2", "denied")
	if err2 := w.Write(context.Background(), e2); err2 != nil {
		t.Fatalf("second Write: %v", err2)
	}
	rows := db.all()
	if len(rows) != 1 {
		t.Fatalf("expected 1 persisted row (failed write must not persist), got %d", len(rows))
	}
	if rows[0].PrevHash != genesisHash {
		t.Errorf("PrevHash after error: got %q want genesis — lastHash must not have advanced", rows[0].PrevHash)
	}
}

// TestWriter_GetLastHashErrNoRows_SeedsGenesis verifies ErrNoRows → genesis.
func TestWriter_GetLastHashErrNoRows_SeedsGenesis(t *testing.T) {
	db := newFakeDB() // empty → GetLastHash returns ErrNoRows
	w := audit.NewWithDB(db)

	e := sampleEntry("bot", "tool", "action", "allowed")
	if err := w.Write(context.Background(), e); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows := db.all()
	if rows[0].PrevHash != genesisHash {
		t.Errorf("PrevHash: got %q want genesis", rows[0].PrevHash)
	}
}

// TestWriter_ChainContinuesAfterRestart simulates a gateway restart by creating
// a new Writer backed by the same fakeDB. It must resume from the last hash.
func TestWriter_ChainContinuesAfterRestart(t *testing.T) {
	db := newFakeDB()
	ctx := context.Background()

	// First writer: two entries.
	w1 := audit.NewWithDB(db)
	e1 := sampleEntry("bot", "stripe", "charge", "allowed")
	e2 := sampleEntry("bot", "stripe", "refund", "denied")
	if err := w1.Write(ctx, e1); err != nil {
		t.Fatalf("w1 Write e1: %v", err)
	}
	if err := w1.Write(ctx, e2); err != nil {
		t.Fatalf("w1 Write e2: %v", err)
	}
	lastHashAfterW1 := db.all()[1].Hash

	// Simulate restart: new Writer backed by same DB.
	w2 := audit.NewWithDB(db)
	e3 := sampleEntry("bot", "stripe", "void", "allowed")
	if err := w2.Write(ctx, e3); err != nil {
		t.Fatalf("w2 Write e3: %v", err)
	}

	rows := db.all()
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[2].PrevHash != lastHashAfterW1 {
		t.Errorf("chain not resumed:\n row[2].PrevHash=%q\n want=%q",
			rows[2].PrevHash, lastHashAfterW1)
	}
}

// TestWriter_ConcurrentWrites_NoRace runs 20 concurrent writes.
// Run with: go test -race
func TestWriter_ConcurrentWrites_NoRace(t *testing.T) {
	db := newFakeDB()
	w := audit.NewWithDB(db)
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			e := sampleEntry(fmt.Sprintf("agent-%d", i), "tool", "action", "allowed")
			if err := w.Write(ctx, e); err != nil {
				t.Errorf("Write[%d]: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	rows := db.all()
	if len(rows) != n {
		t.Errorf("expected %d rows, got %d — concurrent writes lost", n, len(rows))
	}
}

// ─── SECURITY-FAILING TEST ────────────────────────────────────────────────────
// This test documents a security requirement that is NOT yet enforced.
// It will fail until BE-Gateway resolves TASK-054 (AUDIT-001).
// DO NOT remove or skip this test — it is the acceptance criterion.

// TestWriter_DBErrorOnInit_PropagatesError verifies that when GetLastHash
// returns a non-ErrNoRows error, Write propagates the error rather than
// silently resetting to genesis. Linked: AUDIT-001 / TASK-054.
//
// CURRENTLY FAILING: writer.go:101 uses `if err != nil || hash == ""` which
// treats ANY DB error the same as ErrNoRows, silently forking the hash chain
// from genesis on transient DB failures at startup.
func TestWriter_DBErrorOnInit_PropagatesError(t *testing.T) {
	db := newFakeDB()
	// Inject a real DB error — NOT ErrNoRows. Simulates postgres being down.
	db.getHashErr = errors.New("connection refused: postgres:5432")
	w := audit.NewWithDB(db)

	e := sampleEntry("bot", "tool", "action", "allowed")

	// WANT: Write returns an error — cannot initialize chain without last hash.
	// GOT (until TASK-054 is fixed): Write succeeds and seeds genesis,
	// silently forking the chain.
	err := w.Write(context.Background(), e)
	if err == nil {
		t.Fatal(
			"SECURITY-FAILING [AUDIT-001/TASK-054]: " +
				"Write() succeeded despite GetLastHash returning a non-ErrNoRows error. " +
				"This silently resets the hash chain to genesis on any DB hiccup at startup. " +
				"Fix writer.go: change the condition to errors.Is(err, ErrNoRows) || (err == nil && hash == \"\"); " +
				"propagate all other errors to the caller.",
		)
	}
}
