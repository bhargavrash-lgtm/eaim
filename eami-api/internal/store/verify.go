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

// VerifyAuditChain walks the audit_log in chronological order, recomputes each
// row's expected SHA-256 hash, and checks that:
//
//  1. row.prev_hash == hash of the preceding row (chain linkage)
//  2. row.hash == SHA-256(prevHash || id || orgID || agentName || toolName || action || decision || timestamp)
//
// The first row is expected to carry prev_hash = genesisHash ("eami-genesis-2026").
// For partial-range verification (From set), the method first fetches the hash of
// the last row before the range to correctly seed the walk.
//
// Hash formula must match eami-gateway/internal/audit/writer.go.
func (q *Queries) VerifyAuditChain(ctx context.Context, p AuditVerifyParams) (AuditVerifyResult, error) {
	toTS := func(t *time.Time) pgtype.Timestamptz {
		if t == nil {
			return pgtype.Timestamptz{}
		}
		return pgtype.Timestamptz{Time: *t, Valid: true}
	}

	// Seed the hash walk. For a filtered range, use the hash of the last row
	// before the window; otherwise use the genesis hash (empty log or full scan).
	seedHash := genesisHash
	if p.From != nil {
		var prev string
		err := q.db.QueryRow(ctx,
			`SELECT hash FROM audit_log
			 WHERE org_id = $1 AND timestamp < $2
			 ORDER BY timestamp DESC LIMIT 1`,
			toPgtypeUUID(p.OrgID), toTS(p.From),
		).Scan(&prev)
		if err == nil {
			seedHash = prev
		}
		// err != nil → no rows before the range → keep genesisHash
	}

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
		lastHash    = seedHash
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

		// Recompute the expected hash from the stored fields.
		content := lastHash +
			id.String() +
			orgID.String() +
			agentName +
			toolName +
			action +
			decision +
			tsUTC.Format(time.RFC3339)
		h := sha256.Sum256([]byte(content))
		expectedHash := hex.EncodeToString(h[:])

		if prevHash != lastHash || storedHash != expectedHash {
			s := id.String()
			firstBroken = &s
		}

		// Advance the chain using the stored hash (so link-check works for the
		// next row even if recomputed != stored, which is already flagged above).
		lastHash = storedHash
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
