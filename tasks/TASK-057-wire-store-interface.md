# Task: Wire Store interface into Server — production handlers use s.storeIface
**From:** PM-EAMI  
**To:** BE-Policy  
**Priority:** high  
**Blocked by:** none

## Background

`NewHandler(s Store, authSvc *auth.Service)` was added to `router.go` and sets
`s.storeIface`. However, all production handlers (agents, policies, finops, approvals,
etc.) still read from `s.queries` directly. `s.storeIface` is only used by tests via
`MockStore`.

This means:
1. Integration tests calling real handlers go through `s.queries`, not `s.storeIface`
2. You cannot inject a mock for an individual handler in isolation
3. The `risk_tier` field on agent create/update has no enum validation

## What I need

### 1. Wire handlers to s.storeIface (or merge paths)

The cleanest fix for v1 is to ensure production `NewServer(q *store.Queries, ...)` also
sets `storeIface` to a thin adapter so both paths converge:

```go
// In NewServer (production path):
s.storeIface = &queriesAdapter{q: q}
```

Where `queriesAdapter` implements `Store` by delegating to `*store.Queries`.
This makes `s.storeIface` the single path for all handlers, and lets tests inject
`MockStore` without any code divergence.

### 2. Add risk_tier validation

The `gateway_agents` table has `risk_tier TEXT NOT NULL DEFAULT 'medium'`.
On `POST /v1/gateway/agents` and `PUT /v1/gateway/agents/{id}`, validate:

```go
var validRiskTiers = map[string]bool{
    "low": true, "medium": true, "high": true, "critical": true,
}

func isValidRiskTier(s string) bool {
    return validRiskTiers[s]
}
```

If `risk_tier` is provided but not in the allowed set → `400 {"error": "invalid risk_tier, must be one of: low, medium, high, critical"}`.

## Acceptance criteria

- [ ] `NewServer` sets `storeIface` to a `queriesAdapter` wrapping `*store.Queries`
- [ ] `NewHandler` (test path) continues to set `storeIface` to the injected mock
- [ ] `POST /v1/gateway/agents` with `risk_tier: "critical"` succeeds
- [ ] `POST /v1/gateway/agents` with `risk_tier: "extreme"` returns `400`
- [ ] `go vet ./...` exits 0
- [ ] `go test ./...` in `eami-api` still passes for agents/policies/auth/finops

## Files to modify

- `eami-api/internal/api/router.go` — `NewServer` sets `storeIface` via adapter
- `eami-api/internal/api/store_adapter.go` — new file: `queriesAdapter` implementing `Store`
- `eami-api/internal/api/agents.go` — add `risk_tier` validation to create/update handlers
