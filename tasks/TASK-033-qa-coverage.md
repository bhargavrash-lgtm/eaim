# Task: Add test coverage — eami-api and eami-gateway
**From:** PM-EAMI
**To:** QA-EAMI
**Priority:** normal
**Blocked by:** none

## What I need

Meaningful test coverage for the two most critical services. Both currently have near-zero coverage. Focus on correctness, not line coverage — test the paths that would mask real bugs.

## eami-api (target: auth + agents + policies handlers)

### Test files to create

**`eami-api/internal/api/auth_test.go`**
- `TestLogin_Success` — valid credentials → 200 + access_token + refresh_token
- `TestLogin_WrongPassword` → 401
- `TestLogin_UnknownEmail` → 401
- `TestLogin_MissingFields` → 400

Use `httptest.NewServer`, real SQLite or an in-memory postgres (use `pgxmock` or `testcontainers-go`). If testcontainers adds complexity, use interface injection to mock the store (preferred for unit tests).

**`eami-api/internal/api/agents_test.go`**
- `TestListAgents_RequiresAuth` — no token → 401
- `TestCreateAgent_AdminOnly` — operator token → 200, viewer token → 403
- `TestCreateAgent_ValidationError` — missing name → 400
- `TestDeleteAgent_NotFound` → 404

**`eami-api/internal/api/policies_test.go`**
- `TestListPolicies_Empty` → `{"data":[]}`
- `TestCreatePolicy_InvalidPriority` — priority < 1 → 400
- `TestReorderPolicies_WrongOrg` — policy from different org → 404 or 403

### Approach
- Define a `Store` interface in `internal/api/` (extract from `*store.Queries` usage) — use a mock struct in tests
- `authSvc` can use the real `auth.NewService("", ...)` (generates ephemeral dev key)
- No real database needed for handler unit tests

## eami-gateway (target: audit writer + policy evaluator)

### Test files to create

**`eami-gateway/internal/audit/writer_test.go`**
- `TestAuditWriter_HashChain` — write 3 entries, verify each hash is SHA-256 of (prevHash + entry JSON)
- `TestAuditWriter_GenesisOnEmptyTable` — first write seeds from "genesis" hash
- Use `testcontainers-go` with `timescale/timescaledb-ha:pg16` OR a pgxmock that intercepts the INSERT

**`eami-gateway/internal/proxy/proxy_test.go`**
- `TestProxy_ForwardsRequest` — httptest downstream, verify tool+action+params forwarded correctly
- `TestProxy_DownstreamError` → wraps error

## Acceptance criteria

- [ ] All new test files compile: `go test ./...` exits 0 from both `eami-api/` and `eami-gateway/`
- [ ] `go test -count=1 ./internal/api/...` from `eami-api/` passes all auth + agents + policies tests
- [ ] `go test -count=1 ./internal/audit/... ./internal/proxy/...` from `eami-gateway/` passes
- [ ] No `t.Skip()` in any test (tests must actually run)
- [ ] No new external deps beyond what is already in the respective `go.mod` files
- [ ] `go 1.24` on both modules — do not bump

## Files to create

- `eami-api/internal/api/auth_test.go`
- `eami-api/internal/api/agents_test.go`
- `eami-api/internal/api/policies_test.go`
- `eami-api/internal/api/store_mock.go` — mock Store interface for tests
- `eami-gateway/internal/audit/writer_test.go`
- `eami-gateway/internal/proxy/proxy_test.go`
