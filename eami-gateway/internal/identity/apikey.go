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

// ResolvedAgent is the subset of a gateway_agents row IssueHandler needs,
// returned by ValidateAndResolveAgent's combined lookup below (B-107).
// Deliberately NOT registry.AgentRecord -- field-for-field compatible by
// convention, not by type, to avoid a new cross-package coupling for one
// call site (registry.Registry/AgentResolver stay completely untouched,
// still used unmodified by revoke_http.go).
type ResolvedAgent struct {
	ID       string // UUID
	Name     string
	Status   string // "active" | "suspended" | "revoked"
	Scope    string // gateway_agents.scope -- authoritative, B-116
	Model    string // gateway_agents.model -- authoritative, B-116
	Owner    string // gateway_agents.owner -- authoritative, B-116
	RiskTier string // gateway_agents.risk_tier -- authoritative, B-116

	// TokenTTLSeconds (gateway_agents.token_ttl_seconds, B-116's mandatory
	// code-review pass -- not in B-116's original backlog description,
	// which only named Scope/Task/Model/Owner/RiskTier, but the same bug
	// class): unlike those four claims, TTL is NOT latent -- Manager.Issue
	// (tokens.go) honors req.TTLSeconds directly and Manager.Validate
	// enforces exp from it. Leaving this client-controlled while "fixing"
	// four claims nothing downstream reads would have hardened the wrong
	// fields entirely; see issue_http.go's HandleIssue for where this is
	// applied.
	TokenTTLSeconds int
}

// APIKeyValidator resolves a raw API key (as presented in the X-API-Key
// header), together with a requested agent name, to the records it
// authorizes -- or an error if the key is missing, unknown, revoked, or
// expired. Defined as its own interface (mirroring AgentResolver in
// revoke_http.go) purely as a test seam.
//
// Used to also expose a separate ValidateAPIKey(ctx, rawKey) method (the
// original B-098 shape, key lookup only, no agent resolution). B-107
// combined it with the agent lookup into ValidateAndResolveAgent below, and
// code review on that batch confirmed via a repo-wide grep that nothing
// outside this file's own declaration/implementation still called the old
// method -- IssueHandler was its only real caller and now calls the
// combined method instead -- so it was removed rather than left as a dead,
// untested extra surface on a security-relevant interface.
type APIKeyValidator interface {
	// ValidateAndResolveAgent (B-107) combines what was originally two
	// separate calls (ValidateAPIKey, then a separate AgentResolver.
	// LookupByNameAndOrg) into ONE query -- org_id comes from the SAME
	// validated key row the agent is joined against, never client input,
	// preserving the exact scoping guarantee the two-query version had.
	// Returns (nil key, ...) for any invalid key (ErrInvalidAPIKey); returns
	// a valid key with a nil *ResolvedAgent when the key is valid but
	// agentName doesn't resolve to an active, non-suspended agent in that
	// key's own org -- the caller maps that to the same generic "not
	// authorized" rejection the old two-query version produced for both
	// "not found" and "suspended", unchanged.
	ValidateAndResolveAgent(ctx context.Context, rawKey, agentName string) (*APIKeyRecord, *ResolvedAgent, error)
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

// ValidateAndResolveAgent (B-107) -- see the interface doc comment for the
// contract. LEFT JOIN, not INNER: a row with a valid, unrevoked, unexpired
// key but no matching gateway_agents row (ga.id scans NULL) is a genuinely
// different, real outcome from "no such key at all" (pgx.ErrNoRows on the
// whole query) -- the same distinction the removed two-query version drew
// between its key lookup succeeding and its separate agent lookup failing.
func (v *pgAPIKeyValidator) ValidateAndResolveAgent(ctx context.Context, rawKey, agentName string) (*APIKeyRecord, *ResolvedAgent, error) {
	if rawKey == "" {
		return nil, nil, ErrInvalidAPIKey
	}
	row := v.pool.QueryRow(ctx, `
		SELECT ak.id::text, ak.org_id::text, COALESCE(ak.agent_id::text, ''),
		       ga.id::text, ga.name, ga.status, ga.scope, ga.model, ga.owner, ga.risk_tier, ga.token_ttl_seconds
		FROM api_keys ak
		LEFT JOIN gateway_agents ga ON ga.org_id = ak.org_id AND ga.name = $2
		WHERE ak.key_hash = $1 AND ak.revoked = FALSE AND (ak.expires_at IS NULL OR ak.expires_at > NOW())
		LIMIT 1
	`, hashAPIKey(rawKey), agentName)

	var rec APIKeyRecord
	var agentID, agentNameCol, agentStatus, agentScope, agentModel, agentOwner, agentRiskTier *string
	var agentTokenTTL *int
	if err := row.Scan(&rec.ID, &rec.OrgID, &rec.AgentID, &agentID, &agentNameCol, &agentStatus, &agentScope, &agentModel, &agentOwner, &agentRiskTier, &agentTokenTTL); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil, ErrInvalidAPIKey
		}
		return nil, nil, fmt.Errorf("identity: validate api key and resolve agent: %w", err)
	}
	if agentID == nil {
		// Key is genuinely valid; agentName just doesn't exist in this
		// key's org -- same "not found" case LookupByNameAndOrg's
		// ErrAgentNotFound covered.
		return &rec, nil, nil
	}
	// The 5 pointer dereferences below are safe only because
	// scope/model/owner/risk_tier/token_ttl_seconds are all NOT NULL on
	// gateway_agents (schema.sql) -- confirmed, not assumed, by this
	// brief's own mandatory review passes. agentID != nil here (checked
	// above) guarantees the LEFT JOIN matched a real row, so a NULL in any
	// of these five would mean the column itself went nullable, not a
	// missing join. If a future migration ever relaxes one of those NOT
	// NULL constraints, this panics on a real request instead of silently
	// misbehaving -- deliberately not defended with COALESCE, since a
	// silently-substituted empty scope/risk_tier on an identity/auth path
	// is a worse failure mode than a loud crash.
	return &rec, &ResolvedAgent{
		ID: *agentID, Name: *agentNameCol, Status: *agentStatus,
		Scope: *agentScope, Model: *agentModel, Owner: *agentOwner, RiskTier: *agentRiskTier,
		TokenTTLSeconds: *agentTokenTTL,
	}, nil
}
