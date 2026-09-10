package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// genesisHash is the SHA-256 seed used as the prev_hash of the first audit row.
// It must stay in sync with eami-gateway/internal/audit/writer.go.
var genesisHash = func() string {
	h := sha256.Sum256([]byte("eami-genesis-2026"))
	return hex.EncodeToString(h[:])
}()

// AuditVerifyParams selects the range of audit rows to verify.
// Both fields are optional; omitting them verifies the entire org log.
type AuditVerifyParams struct {
	OrgID uuid.UUID
	From  *time.Time
	To    *time.Time
}

// AuditVerifyResult is the response produced by VerifyAuditChain.
type AuditVerifyResult struct {
	Valid         bool    `json:"valid"`
	TotalRows     int64   `json:"total_rows"`
	FirstBrokenAt *string `json:"first_broken_at,omitempty"` // UUID of first bad row
	Message       string  `json:"message"`
	CheckedFrom   *string `json:"checked_from,omitempty"` // RFC3339 timestamp
	CheckedTo     *string `json:"checked_to,omitempty"`   // RFC3339 timestamp
}

// VerifyAuditChain checks, for the requested org's rows (optionally bounded by
// From/To), that each row is:
//
//  1. self-consistent: row.hash == SHA-256(prevHash || id || orgID || agentName
//     || toolName || action || decision || timestamp)
//  2. linked: row.prev_hash is either the genesis hash ("eami-genesis-2026") or
//     the recorded hash of some real row that exists in audit_log.
//
// audit_log's real hash chain is a single sequence spanning every org (see
// eami-gateway/internal/audit/writer.go's GetLastHash, which has no org_id
// filter) — an org's own rows are frequently chained through other orgs'
// rows as their real predecessor. Linkage is therefore checked against a
// hash set built from the WHOLE table, not just this org's slice, and does
// NOT depend on wall-clock timestamp order: eami-gateway's Writer captures
// e.Timestamp before acquiring its serializing mutex, so under concurrent
// writers, two rows' chain (insert) order can differ from their relative
// timestamps. Each row's checks are independent of every other row's, so
// this is correct regardless of write concurrency or how many other orgs'
// rows interleave with this org's — no attempt is made to reconstruct a
// single global total order.
//
// Deliberately NOT checked: that a row's prev_hash is claimed by exactly
// one row (i.e. fork/reuse detection — is this genuinely the UNIQUE real
// successor of its predecessor, not just A row that resolves). A stricter
// check along those lines was considered during this function's security
// review and rejected after checking it against this database's own real
// data: this table has 100+ real, confirmed-untampered row pairs/triples
// across different orgs sharing the same prev_hash (two rows recorded
// within milliseconds of each other, evidently two independent
// audit.Writer instances — most likely separate real-Postgres-test-suite
// process runs sharing this dev database — both reading the same
// GetLastHash() value before either write committed). Rejecting a shared
// prev_hash as "broken" would misreport all of that genuine history as
// tampered. This is a real gap in the Writer's cross-process concurrency
// safety (its mutex only serializes writes within one process) — logged
// as its own backlog item, not fixed here since audit/writer.go is out of
// this function's scope. Existence-only linkage checking is therefore the
// correct choice given what the Writer actually guarantees today, not
// merely a simplification: it detects every case this fix's own root
// causes require (a genuinely deleted or altered predecessor), without
// rejecting real, characteristic output of the system as built. It does
// not, and cannot without a stronger writer-side guarantee, detect an
// attacker with direct database write access fabricating a new row whose
// prev_hash reuses an already-claimed real hash — a fundamental limit of
// hash-chaining without an external anchor, shared by the walk this
// function replaced.
//
// A row's report (TotalRows/CheckedFrom/CheckedTo/FirstBrokenAt) stays
// scoped to the requested org: if a row's real predecessor (in any org)
// can no longer be found — because it was altered or deleted — the row
// reported as first-broken is always this org's own row that failed to
// resolve, never the other org's row, so a response never names a row ID
// outside the caller's own org.
//
// Hash formula must match eami-gateway/internal/audit/writer.go.
func (q *Queries) VerifyAuditChain(ctx context.Context, p AuditVerifyParams) (AuditVerifyResult, error) {
	toTS := func(t *time.Time) pgtype.Timestamptz {
		if t == nil {
			return pgtype.Timestamptz{}
		}
		return pgtype.Timestamptz{Time: *t, Valid: true}
	}

	// Global linkage set: every row's hash, across every org, unbounded by
	// From/To. Deliberately not org- or time-scoped -- a real predecessor can
	// belong to another org, or (under the timestamp-capture race described
	// above) can carry a timestamp outside this org's requested window even
	// though it genuinely precedes this org's row in real chain order.
	existingHashes := make(map[string]struct{})
	hashRows, err := q.db.Query(ctx, `SELECT hash FROM audit_log`)
	if err != nil {
		return AuditVerifyResult{}, err
	}
	for hashRows.Next() {
		var h string
		if err := hashRows.Scan(&h); err != nil {
			hashRows.Close()
			return AuditVerifyResult{}, err
		}
		existingHashes[h] = struct{}{}
	}
	if err := hashRows.Err(); err != nil {
		hashRows.Close()
		return AuditVerifyResult{}, err
	}
	hashRows.Close()

	rows, err := q.db.Query(ctx, `
		SELECT id, org_id, agent_name, tool_name, action, decision,
		       timestamp, prev_hash, hash
		FROM audit_log
		WHERE org_id = $1
		  AND ($2::timestamptz IS NULL OR timestamp >= $2)
		  AND ($3::timestamptz IS NULL OR timestamp <= $3)
		ORDER BY timestamp ASC
	`, toPgtypeUUID(p.OrgID), toTS(p.From), toTS(p.To))
	if err != nil {
		return AuditVerifyResult{}, err
	}
	defer rows.Close()

	var (
		totalRows   int64
		firstBroken *string
		firstTS     *time.Time
		lastTS      *time.Time
	)

	for rows.Next() {
		var (
			id         uuid.UUID
			orgID      uuid.UUID
			agentName  string
			toolName   string
			action     string
			decision   string
			ts         time.Time
			prevHash   string
			storedHash string
		)
		if err := rows.Scan(&id, &orgID, &agentName, &toolName, &action, &decision,
			&ts, &prevHash, &storedHash); err != nil {
			return AuditVerifyResult{}, err
		}

		totalRows++
		tsUTC := ts.UTC()
		if firstTS == nil {
			firstTS = &tsUTC
		}
		lastTS = &tsUTC

		if firstBroken != nil {
			// Already found a break — keep counting rows but skip re-verification.
			continue
		}

		// Self-consistency: recompute the expected hash from this row's own
		// stored fields (prevHash is an input to the formula, taken as
		// recorded — its own validity is checked separately below).
		content := prevHash +
			id.String() +
			orgID.String() +
			agentName +
			toolName +
			action +
			decision +
			tsUTC.Format(time.RFC3339)
		h := sha256.Sum256([]byte(content))
		expectedHash := hex.EncodeToString(h[:])
		selfConsistent := storedHash == expectedHash

		// Linkage: prevHash must be the genesis hash or the real hash of some
		// row that exists anywhere in audit_log (see existingHashes above).
		_, linked := existingHashes[prevHash]
		linked = linked || prevHash == genesisHash

		if !selfConsistent || !linked {
			s := id.String()
			firstBroken = &s
		}
	}
	if err := rows.Err(); err != nil {
		return AuditVerifyResult{}, err
	}

	res := AuditVerifyResult{
		Valid:         firstBroken == nil,
		TotalRows:     totalRows,
		FirstBrokenAt: firstBroken,
	}
	switch {
	case totalRows == 0:
		res.Message = "no audit rows found in the specified range"
	case res.Valid:
		res.Message = fmt.Sprintf("chain intact (%d rows verified)", totalRows)
	default:
		res.Message = fmt.Sprintf("chain broken at row %s", *firstBroken)
	}
	if firstTS != nil {
		s := firstTS.Format(time.RFC3339)
		res.CheckedFrom = &s
	}
	if lastTS != nil {
		s := lastTS.Format(time.RFC3339)
		res.CheckedTo = &s
	}
	return res, nil
}
