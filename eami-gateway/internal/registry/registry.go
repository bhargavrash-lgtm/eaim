// Package registry resolves AI agent identity from the gateway_agents table.
//
// Each tool_call arrives with a JWT whose sub is "agent:<name>". The registry
// maps that name to the full agent row so the pipeline uses real org_id and
// agent UUIDs in audit log entries.
//
// Results are cached for cacheTTL (30 s) to avoid a DB round-trip per call.
package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const cacheTTL = 30 * time.Second

// AgentRecord is the subset of gateway_agents fields the pipeline needs.
type AgentRecord struct {
	ID       string // UUID
	OrgID    string // UUID
	Name     string
	Scope    string
	RiskTier string // "low" | "medium" | "high"
	Status   string // "active" | "suspended" | "revoked"
}

// ErrAgentNotFound is returned when the agent name is unknown.
var ErrAgentNotFound = errors.New("registry: agent not found")

// ErrAgentSuspended is returned when the agent is suspended or revoked.
var ErrAgentSuspended = errors.New("registry: agent is suspended or revoked")

type cacheEntry struct {
	record    AgentRecord
	expiresAt time.Time
}

// Registry looks up agents from the database with a short-lived in-memory cache.
type Registry struct {
	pool  *pgxpool.Pool
	mu    sync.RWMutex
	cache map[string]cacheEntry
}

// New creates a Registry backed by the given connection pool.
func New(pool *pgxpool.Pool) *Registry {
	return &Registry{pool: pool, cache: make(map[string]cacheEntry)}
}

// LookupByName resolves an agent by its short name (JWT sub without "agent:"
// prefix), with NO org scoping -- name alone is not unique in gateway_agents
// (only (org_id, name) is, schema.sql's UNIQUE (org_id, name) constraint), so
// this can resolve the wrong org's identically-named agent.
//
// B-141/B-142: this used to be the standard resolution path for
// internal/mcp/internal/episode, on the reasoning that their name always
// came from a signature-verified JWT sub whose issuing org could be trusted
// implicitly -- that reasoning was never actually sound (a JWT's sub carries
// only the name string, never an org id) and produced a real, confirmed
// cross-tenant identity-resolution bug (B-141: a name collision between two
// orgs' agents could resolve the wrong org's record, including in
// internal/episode's own content-read path). Every real caller has since
// been migrated to the org-scoped LookupByNameAndOrg below -- confirmed by a
// repo-wide grep (production AND test code) that this method now has ZERO
// remaining callers anywhere. Genuinely dead code as of B-141/B-142, not
// removed here (out of this brief's doc-comment-only scope; a legitimate
// small follow-up for whoever picks it up next, see BACKLOG.md's B-142
// entry). Do not add a new caller of this method -- use LookupByNameAndOrg
// instead, with a real org id from a trustworthy source (a JWT's own OrgID
// claim, or a service-key-authenticated caller-supplied org_id).
func (r *Registry) LookupByName(ctx context.Context, name string) (*AgentRecord, error) {
	r.mu.RLock()
	if e, ok := r.cache[name]; ok && time.Now().Before(e.expiresAt) {
		r.mu.RUnlock()
		return &e.record, checkStatus(e.record.Status)
	}
	r.mu.RUnlock()

	rec, err := r.queryByName(ctx, name)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.cache[name] = cacheEntry{record: *rec, expiresAt: time.Now().Add(cacheTTL)}
	r.mu.Unlock()
	slog.Debug("registry: agent resolved", "name", name, "org_id", rec.OrgID[:8]+"...")
	return rec, checkStatus(rec.Status)
}

// Invalidate removes a name from the cache (call after status changes).
func (r *Registry) Invalidate(name string) {
	r.mu.Lock()
	delete(r.cache, name)
	r.mu.Unlock()
}

func (r *Registry) queryByName(ctx context.Context, name string) (*AgentRecord, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id::text, org_id::text, name, scope, risk_tier, status
		FROM gateway_agents
		WHERE name = $1
		LIMIT 1
	`, name)
	var rec AgentRecord
	if err := row.Scan(&rec.ID, &rec.OrgID, &rec.Name, &rec.Scope, &rec.RiskTier, &rec.Status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, name)
		}
		return nil, fmt.Errorf("registry: query agent %q: %w", name, err)
	}
	return &rec, nil
}

// LookupByNameAndOrg resolves an agent by name, scoped to a specific org --
// this is the correct, real resolution path for every current production
// caller (internal/mcp's ServeSSE, internal/episode's authenticateCaller,
// internal/identity's revoke handler, internal/workflow's HandleRun and
// RateLimitRunMiddleware).
//
// name alone is NOT unique in gateway_agents -- only (org_id, name) is
// (schema.sql's UNIQUE (org_id, name) constraint), so an unscoped lookup can
// resolve the wrong org's identically-named agent.
//
// B-141/B-142, correcting this comment's own prior stale reasoning: an
// earlier version of this comment asserted LookupByName's unscoped query was
// "safe" for internal/mcp/internal/episode because their name always came
// from a signature-verified JWT sub, itself implicitly scoped to whichever
// org issued the token. That reasoning was never actually sound -- a JWT's
// sub carries only the name string, never an org id, so a name collision
// between two orgs' agents could still resolve the wrong org's record
// regardless of the JWT's own signature validity -- and it produced a real,
// confirmed cross-tenant identity-resolution bug (B-141: this exact gap let
// one org's caller read another org's episode content via
// internal/episode's authenticateCaller). The fix: every real caller now
// supplies a genuinely trustworthy org id -- either a JWT's own
// server-set OrgID claim (B-141, never client-settable) for the JWT-backed
// callers, or an explicit caller-supplied org_id on internal/identity's
// revoke handler (B-042), whose service-key auth has no JWT/session concept
// at all, matching internal/episode/http.go's own equivalent trust boundary
// for its own service-key path.
//
// Deliberately bypasses the name-only cache above: caching by name alone
// would serve the wrong org's cached record, and every real caller here
// needs a correct result on every call, not a 30s-stale one.
func (r *Registry) LookupByNameAndOrg(ctx context.Context, name, orgID string) (*AgentRecord, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id::text, org_id::text, name, scope, risk_tier, status
		FROM gateway_agents
		WHERE name = $1 AND org_id = $2
		LIMIT 1
	`, name, orgID)
	var rec AgentRecord
	if err := row.Scan(&rec.ID, &rec.OrgID, &rec.Name, &rec.Scope, &rec.RiskTier, &rec.Status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, name)
		}
		return nil, fmt.Errorf("registry: query agent %q in org %q: %w", name, orgID, err)
	}
	return &rec, checkStatus(rec.Status)
}

func checkStatus(status string) error {
	if status == "suspended" || status == "revoked" {
		return fmt.Errorf("%w: status=%s", ErrAgentSuspended, status)
	}
	return nil
}
