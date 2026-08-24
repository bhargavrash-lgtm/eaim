package identity

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// APIKeyRecord is the subset of an api_keys row needed to authorize
// POST /v1/gateway/tokens (B-098).
type APIKeyRecord struct {
	ID      string // UUID
	OrgID   string // UUID
	AgentID string // UUID, empty if the key is not scoped to a specific agent
}

// APIKeyValidator resolves a raw API key (as presented in the X-API-Key
// header) to the record it authorizes, or an error if the key is missing,
// unknown, revoked, or expired. Defined as its own interface (mirroring
// AgentResolver in revoke_http.go) purely as a test seam.
type APIKeyValidator interface {
	ValidateAPIKey(ctx context.Context, rawKey string) (*APIKeyRecord, error)
}

// ErrInvalidAPIKey is returned for any key that doesn't resolve to a live,
// unrevoked, unexpired api_keys row -- deliberately one error for "missing",
// "unknown", "revoked", and "expired" so the HTTP layer can't be used to
// distinguish "this key exists but is revoked" from "this key never
// existed" (would otherwise let a caller enumerate valid key hashes).
var ErrInvalidAPIKey = fmt.Errorf("identity: invalid, revoked, or expired API key")

// pgAPIKeyValidator validates API keys against the real api_keys table.
// eami-gateway already holds a direct Postgres pool into the same database
// api_keys lives in (the SaaS DB) -- this is a same-DB query, not a new
// cross-service dependency (see BACKLOG.md B-098's investigation).
type pgAPIKeyValidator struct {
	pool *pgxpool.Pool
}

// NewPostgresAPIKeyValidator returns an APIKeyValidator backed by pool.
func NewPostgresAPIKeyValidator(pool *pgxpool.Pool) APIKeyValidator {
	return &pgAPIKeyValidator{pool: pool}
}

// hashAPIKey matches eami-api/internal/auth.APIKeyFromRaw's exact hash
// format (SHA-256, lowercase hex) -- api_keys.key_hash is written by that
// function and must be looked up the same way here.
func hashAPIKey(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return fmt.Sprintf("%x", sum)
}

func (v *pgAPIKeyValidator) ValidateAPIKey(ctx context.Context, rawKey string) (*APIKeyRecord, error) {
	if rawKey == "" {
		return nil, ErrInvalidAPIKey
	}
	row := v.pool.QueryRow(ctx, `
		SELECT id::text, org_id::text, COALESCE(agent_id::text, '')
		FROM api_keys
		WHERE key_hash = $1 AND revoked = FALSE AND (expires_at IS NULL OR expires_at > NOW())
		LIMIT 1
	`, hashAPIKey(rawKey))
	var rec APIKeyRecord
	if err := row.Scan(&rec.ID, &rec.OrgID, &rec.AgentID); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrInvalidAPIKey
		}
		return nil, fmt.Errorf("identity: validate api key: %w", err)
	}
	return &rec, nil
}
