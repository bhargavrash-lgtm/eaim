package store

// B-301: keep eami-gateway's view of governed-agent liveness current.
//
// The gateway's per-call check reads gateway_agents.status and
// revoked_ai_tokens directly on every call; that is the guarantee. The
// notifications sent here only let each gateway node end open sessions
// sooner (agent_status, token_revoked), so they are best-effort.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// NotifyAgentStatus tells every gateway node that a governed agent's status
// changed (or it was deleted). The payload is the UUID only.
func (q *Queries) NotifyAgentStatus(ctx context.Context, agentID uuid.UUID) error {
	_, err := q.db.Exec(ctx, `SELECT pg_notify('agent_status', $1)`, agentID.String())
	return err
}

// ErrAPIKeyNotFound: no key with that id in the caller's org.
var ErrAPIKeyNotFound = errors.New("store: api key not found")

// maxGatewayTokenLifetime is eami-gateway's maxTTL (identity/tokens.go,
// 14400 s = 4 h; keep the two in sync). ai_token_events doesn't store
// expiry, so any token issued longer ago than this is already expired.
//
// This cascade is NOT the guarantee that a revoked key's tokens stop: tokens
// carry the issuing key's id (api_key_id) and the gateway's per-call check
// refuses any token whose key is revoked (B-301 review, High 2). The cascade
// adds revoked_ai_tokens rows and token_revoked notifications so open
// sessions end promptly and the revocation is recorded per token.
const maxGatewayTokenLifetime = "4 hours"

// RevokeAPIKeyAndTokens revokes an API key and every token issued with it
// that could still be live, inside tx (the caller's transaction, so the key
// change, the token revocations and the admin audit event commit together or
// not at all).
//
// For each such token it inserts revoked_ai_tokens, records an
// ai_token_events 'revoked' row, and notifies token_revoked. Tokens whose
// governed agent no longer exists are skipped: the gateway's per-call check
// already refuses them. Returns the key's bound agent (nil if unbound) and
// how many tokens were newly revoked.
func RevokeAPIKeyAndTokens(ctx context.Context, tx pgx.Tx, keyID, orgID uuid.UUID) (*uuid.UUID, int, error) {
	var agentID *uuid.UUID
	err := tx.QueryRow(ctx, `
		UPDATE api_keys SET revoked = TRUE
		WHERE id = $1 AND org_id = $2
		RETURNING agent_id`, keyID, orgID).Scan(&agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrAPIKeyNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("revoke api key: %w", err)
	}

	rows, err := tx.Query(ctx, `
		WITH live AS (
			SELECT DISTINCT ON (e.jti) e.jti, e.agent_id, e.agent_name
			FROM ai_token_events e
			JOIN gateway_agents ga ON ga.id = e.agent_id AND ga.org_id = e.org_id
			WHERE e.org_id = $1 AND e.api_key_id = $2 AND e.event_type = 'issued'
			  AND e.created_at > now() - interval '`+maxGatewayTokenLifetime+`'
		),
		ins AS (
			INSERT INTO revoked_ai_tokens (jti, agent_id, reason)
			SELECT jti, agent_id, 'api_key_revoked' FROM live
			ON CONFLICT (jti) DO NOTHING
			RETURNING jti
		)
		SELECT live.jti, live.agent_id, live.agent_name
		FROM live JOIN ins ON ins.jti = live.jti`, orgID, keyID)
	if err != nil {
		return nil, 0, fmt.Errorf("revoke tokens: %w", err)
	}
	type tok struct {
		jti   string
		agent uuid.UUID
		name  string
	}
	var revoked []tok
	for rows.Next() {
		var t tok
		if err := rows.Scan(&t.jti, &t.agent, &t.name); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("revoke tokens: scan: %w", err)
		}
		revoked = append(revoked, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("revoke tokens: %w", err)
	}

	for _, t := range revoked {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ai_token_events (org_id, agent_id, agent_name, api_key_id, jti, event_type)
			VALUES ($1, $2, $3, $4, $5, 'revoked')`, orgID, t.agent, t.name, keyID, t.jti); err != nil {
			return nil, 0, fmt.Errorf("record token revocation: %w", err)
		}
		if _, err := tx.Exec(ctx, `SELECT pg_notify('token_revoked', $1)`, t.jti); err != nil {
			return nil, 0, fmt.Errorf("notify token revocation: %w", err)
		}
	}
	return agentID, len(revoked), nil
}
