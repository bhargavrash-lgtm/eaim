# Task: TASK-040 Revision — fix policies_test.go reorder method
**From:** PM-EAMI  
**To:** QA-EAMI  
**Priority:** high  
**Blocked by:** none

## What needs fixing

TASK-040 delivery claimed `policies_test.go` reorder tests were changed from PUT to PATCH.
However the Repo still shows `http.MethodPut` on all three reorder test calls (lines 326, 349, 366).
The router registers: `r.Post("/v1/gateway/policies/reorder", s.ReorderPolicies)`.

All three tests will 405 as long as they use PUT or PATCH.

## Fix

Change the three reorder test calls from `http.MethodPut` to `http.MethodPost`:

```go
// Line ~326
resp := ts.do(t, http.MethodPost, "/v1/gateway/policies/reorder", token, payload)

// Line ~349
resp := ts.do(t, http.MethodPost, "/v1/gateway/policies/reorder", token, payload)

// Line ~366  
resp := ts.do(t, http.MethodPost, "/v1/gateway/policies/reorder", token, payload)
```

Also update the comment on line ~313:
```go
// ─── POST /v1/gateway/policies/reorder ───
```

## Acceptance criteria

- [ ] All three `TestReorderPolicies_*` tests use `http.MethodPost`
- [ ] `go test ./...` in `eami-api/` does not 405 on reorder tests
  (they may still fail on Store wiring until TASK-057 lands, but the HTTP method must be correct)

## File to modify

- `eami-api/internal/api/policies_test.go`
