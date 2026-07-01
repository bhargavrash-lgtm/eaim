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

// LookupByName resolves an agent by its short name (JWT sub without "agent:" prefix).
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

func checkStatus(status string) error {
	if status == "suspended" || status == "revoked" {
		return fmt.Errorf("%w: status=%s", ErrAgentSuspended, status)
	}
	return nil
}
