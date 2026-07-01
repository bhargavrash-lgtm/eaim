# Task: Persist JWT revocations to DB (FINDING JWT-002)
**From:** PM-EAMI  
**To:** BE-Gateway  
**Priority:** high  
**Blocked by:** none

## Problem

The in-memory revocation list in `eami-gateway/internal/identity/tokens.go` is lost on restart.
A revoked token is accepted again after any gateway restart or in a multi-node deployment.

Failing test: `TestManager_Validate_RevokedToken_SurvivesRestart` in `tokens_test.go`

## Fix

1. On `RevokeToken(jti)`: INSERT into `revoked_ai_tokens` (already in schema.sql) before updating the in-memory set.
2. On `NewManager` / startup: SELECT all non-expired JTIs from `revoked_ai_tokens` and load them into the in-memory set.
3. Periodically (or on Validate): prune expired entries from both DB and memory.

```go
// On startup
rows, _ := pool.Query(ctx, `SELECT jti FROM revoked_ai_tokens WHERE expires_at > NOW()`)
for rows.Next() {
    var jti string
    rows.Scan(&jti)
    m.revoked[jti] = struct{}{}
}
```

## Acceptance criteria

- [ ] `TestManager_Validate_RevokedToken_SurvivesRestart` passes (currently FAIL)
- [ ] Revoking a token writes to `revoked_ai_tokens` table
- [ ] Manager hydrates revocation list from DB on startup
- [ ] `go vet ./...` exits 0

## Files to modify

- `eami-gateway/internal/identity/tokens.go` — hydrate from DB on init, write to DB on revoke
