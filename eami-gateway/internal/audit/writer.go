// Package audit writes immutable, hash-chained audit log entries (ADR-007).
//
// Each row in audit_log includes:
//   - prev_hash: hash of the previous row (or the genesis hash for the first row)
//   - hash: SHA-256(prevHash || id || orgID || agentName || toolName || action || decision || timestamp)
//
// First row seeds with prevHash = SHA-256("eami-genesis-2026").
// Append-only guarantee enforced at DB level via RLS (schema.sql).
// This package never issues UPDATE or DELETE statements.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoRows is returned by WriterDB.GetLastHash when the audit_log table is empty.
var ErrNoRows = errors.New("audit: no rows")

// WriterDB is the persistence interface for the audit writer.
// Inject a test double via NewWithDB to unit-test audit logic
// without a real Postgres connection.
type WriterDB interface {
	// GetLastHash returns the hash of the most recent audit_log row.
	// Returns ("", ErrNoRows) when the table is empty.
	GetLastHash(ctx context.Context) (string, error)

	// InsertEntry appends a fully populated entry (including Hash and PrevHash).
	InsertEntry(ctx context.Context, e Entry) error
}

// Entry is a single audit log record.
// The writer sets Hash and PrevHash before calling InsertEntry.
type Entry struct {
	ID         uuid.UUID
	OrgID      uuid.UUID
	AgentID    uuid.UUID
	AgentName  string
	ToolName   string
	Action     string
	Parameters map[string]any
	Decision   string // "allowed" | "denied" | "escalated"
	PolicyID   string
	ApprovalID string
	ApprovedBy string
	LatencyMS  int64
	TokenIn    int
	TokenOut   int
	Timestamp  time.Time
	// Set by Writer.Write before calling InsertEntry:
	PrevHash string
	Hash     string
}

// Writer appends audit entries to the audit_log Postgres table.
type Writer struct {
	db          WriterDB
	mu          sync.Mutex // serialises writes to maintain hash chain integrity
	lastHash    string
	initialized bool
}

// NewWriter creates a Writer backed by a *pgxpool.Pool.
// Hash-chain initialisation is deferred to the first Write call.
// For tests, prefer NewWithDB.
func NewWriter(ctx context.Context, pool *pgxpool.Pool) (*Writer, error) {
	return NewWithDB(&pgxpoolDB{pool: pool}), nil
}

// NewWithDB creates a Writer backed by any WriterDB implementation.
// Hash-chain initialisation is deferred to the first Write call, so
// this constructor never touches the database.
func NewWithDB(db WriterDB) *Writer {
	return &Writer{db: db}
}

// Write appends an audit entry. Safe for concurrent callers; writes are serialised
// to maintain the hash chain. Sets ID (if zero), PrevHash, and Hash on the entry.
func (w *Writer) Write(ctx context.Context, e Entry) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Lazy initialisation: read the last hash from the DB on the first write.
	if !w.initialized {
		hash, err := w.db.GetLastHash(ctx)
		if err != nil {
			if errors.Is(err, ErrNoRows) {
				// Empty table — seed the hash chain with the genesis hash.
				g := sha256.Sum256([]byte("eami-genesis-2026"))
				w.lastHash = hex.EncodeToString(g[:])
				slog.Info("audit: seeding hash chain with genesis hash", "hash", w.lastHash[:16]+"...")
			} else {
				// Real DB error — do not silently reset the chain.
				return fmt.Errorf("audit: failed to load last hash: %w", err)
			}
		} else {
			w.lastHash = hash
			slog.Info("audit: resumed hash chain", "last_hash", w.lastHash[:16]+"...")
		}
		w.initialized = true
	}

	// Ensure the entry has a stable ID before hashing.
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}

	// Hash formula (must match verify-audit-log.sh):
	//   SHA-256(prevHash || id || orgID || agentName || toolName || action || decision || timestamp)
	content := w.lastHash +
		e.ID.String() +
		e.OrgID.String() +
		e.AgentName +
		e.ToolName +
		e.Action +
		e.Decision +
		e.Timestamp.UTC().Format(time.RFC3339)
	h := sha256.Sum256([]byte(content))
	rowHash := hex.EncodeToString(h[:])

	e.PrevHash = w.lastHash
	e.Hash = rowHash

	if err := w.db.InsertEntry(ctx, e); err != nil {
		return fmt.Errorf("audit: insert failed: %w", err)
	}
	w.lastHash = rowHash
	return nil
}

// ─── pgxpool adapter ─────────────────────────────────────────────────────────

// pgxpoolDB adapts *pgxpool.Pool to the WriterDB interface.
type pgxpoolDB struct {
	pool *pgxpool.Pool
}

func (d *pgxpoolDB) GetLastHash(ctx context.Context) (string, error) {
	var hash string
	err := d.pool.QueryRow(ctx,
		`SELECT hash FROM audit_log ORDER BY timestamp DESC LIMIT 1`,
	).Scan(&hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNoRows
		}
		return "", err
	}
	return hash, nil
}

func (d *pgxpoolDB) InsertEntry(ctx context.Context, e Entry) error {
	params, err := json.Marshal(e.Parameters)
	if err != nil || params == nil {
		params = []byte("{}")
	}

	var agentID *uuid.UUID
	if e.AgentID != uuid.Nil {
		agentID = &e.AgentID
	}
	var policyID, approvalID *string
	if e.PolicyID != "" {
		policyID = &e.PolicyID
	}
	if e.ApprovalID != "" {
		approvalID = &e.ApprovalID
	}

	_, dbErr := d.pool.Exec(ctx, `
		INSERT INTO audit_log (
			id, org_id, agent_id, agent_name, tool_name, action,
			parameters, decision, policy_id, approval_id, approved_by,
			latency_ms, token_in, token_out,
			timestamp, prev_hash, hash
		) VALUES (
			$1,$2,$3,$4,$5,$6, $7,$8,$9,$10,$11, $12,$13,$14, $15,$16,$17
		)`,
		e.ID, e.OrgID, agentID, e.AgentName, e.ToolName, e.Action,
		params, e.Decision, policyID, approvalID, e.ApprovedBy,
		e.LatencyMS, e.TokenIn, e.TokenOut,
		e.Timestamp, e.PrevHash, e.Hash,
	)
	return dbErr
}
