package registry

// Per-call liveness (B-301): is this governed agent still allowed to
// dispatch, and is the token its session was opened with still unrevoked?
//
// This is THE guarantee behind "suspend stops a governed agent": the
// dispatcher asks on every call, and the database is the source of truth,
// so it holds on every gateway node whether or not any notification
// arrived. agent_status / token_revoked notifications (liveness_listener.go
// in internal/mcp) only close sessions sooner; they are an optimisation.

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Liveness is the outcome of a per-call check.
type Liveness int

const (
	// Live: the governed agent is active and the token isn't revoked.
	Live Liveness = iota
	// AgentNotActive: suspended, revoked, deleted, or not in this org.
	AgentNotActive
	// TokenRevoked: the session's token JTI is in revoked_ai_tokens.
	TokenRevoked
)

// CheckLive reads the governed agent's status (by id AND org), whether jti
// is revoked, and whether the API key the token was issued with (keyID) is
// revoked, in one primary-key query. A malformed id is treated as
// not active. A database error is returned as an error: the caller must
// refuse the call (fail closed), never treat it as live.
func (r *Registry) CheckLive(ctx context.Context, agentID, orgID, jti, keyID string) (Liveness, error) {
	aid, err1 := uuid.Parse(agentID)
	oid, err2 := uuid.Parse(orgID)
	if err1 != nil || err2 != nil {
		return AgentNotActive, nil
	}
	var key pgtype.UUID // NULL when the token carries no key id
	if keyID != "" {
		k, err := uuid.Parse(keyID)
		if err != nil {
			return TokenRevoked, nil
		}
		key = pgtype.UUID{Bytes: k, Valid: true}
	}
	var status string
	var revoked bool
	err := r.pool.QueryRow(ctx, `
		SELECT ga.status,
		       ($3 <> '' AND EXISTS (SELECT 1 FROM revoked_ai_tokens rt WHERE rt.jti = $3))
		       OR EXISTS (SELECT 1 FROM api_keys k WHERE k.id = $4::uuid AND k.revoked)
		FROM gateway_agents ga
		WHERE ga.id = $1 AND ga.org_id = $2`, aid, oid, jti, key).Scan(&status, &revoked)
	if err == pgx.ErrNoRows {
		return AgentNotActive, nil
	}
	if err != nil {
		return AgentNotActive, fmt.Errorf("registry: liveness check: %w", err)
	}
	if status != "active" {
		return AgentNotActive, nil
	}
	if revoked {
		return TokenRevoked, nil
	}
	return Live, nil
}
