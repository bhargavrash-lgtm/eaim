package identity

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TokenEventStore persists the AI-token issuance/revocation audit trail
// (ai_token_events, B-098) -- a new, small, purpose-built table, distinct
// from agent_lifecycle_events (B-087, admin CRUD history with a human
// performed_by) and audit_log (hash-chained dispatch-decision ledger).
// See the migration's own doc comment (schema/migrations-v2/000010) for
// the full "why not reuse" reasoning.
type TokenEventStore interface {
	RecordIssued(ctx context.Context, orgID, agentID, agentName, apiKeyID, jti string) error
	RecordRevoked(ctx context.Context, orgID, agentID, agentName, jti string) error
}

type pgTokenEventStore struct {
	pool *pgxpool.Pool
}

// NewPostgresTokenEventStore returns a TokenEventStore backed by pool.
func NewPostgresTokenEventStore(pool *pgxpool.Pool) TokenEventStore {
	return &pgTokenEventStore{pool: pool}
}

func (s *pgTokenEventStore) insert(ctx context.Context, orgID, agentID, agentName, apiKeyID, jti, eventType string) error {
	agentUUID := pgtype.UUID{}
	if agentID != "" {
		if err := agentUUID.Scan(agentID); err != nil {
			return fmt.Errorf("identity: record %s event: invalid agent_id %q: %w", eventType, agentID, err)
		}
	}
	keyUUID := pgtype.UUID{}
	if apiKeyID != "" {
		if err := keyUUID.Scan(apiKeyID); err != nil {
			return fmt.Errorf("identity: record %s event: invalid api_key_id %q: %w", eventType, apiKeyID, err)
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ai_token_events (org_id, agent_id, agent_name, api_key_id, jti, event_type)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, orgID, agentUUID, agentName, keyUUID, jti, eventType)
	if err != nil {
		return fmt.Errorf("identity: record %s event: %w", eventType, err)
	}
	return nil
}

// RecordIssued logs a successful AI token issuance.
func (s *pgTokenEventStore) RecordIssued(ctx context.Context, orgID, agentID, agentName, apiKeyID, jti string) error {
	return s.insert(ctx, orgID, agentID, agentName, apiKeyID, jti, "issued")
}

// RecordRevoked logs a successful AI token revocation. apiKeyID is always
// empty here -- RevokeHandler (B-042) authenticates via the gateway-local
// X-Service-Key, not a caller-presented api_keys row, so there is no key
// to attribute the event to.
func (s *pgTokenEventStore) RecordRevoked(ctx context.Context, orgID, agentID, agentName, jti string) error {
	return s.insert(ctx, orgID, agentID, agentName, "", jti, "revoked")
}
