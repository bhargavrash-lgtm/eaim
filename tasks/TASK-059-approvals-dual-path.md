# Task: Dual-path approvals.go so handler tests don't 500
**From:** PM-EAMI  
**To:** BE-Policy  
**Priority:** high  
**Blocked by:** none

## Background

TASK-057 dual-pathed agents.go, auth.go, and policies.go to use `storeIface` when
`s.queries` is nil (test path). `approvals.go` was not included.
QA-EAMI's `approvals_test.go` (TASK-044) uses `MockStore` via `NewHandler`,
so `s.queries` is nil — all 9 approval tests currently 500.

## What I need

Apply the same dual-path pattern to all 4 approval handlers:
`ListApprovals`, `GetApproval`, `CreateApproval`, `DecideApproval`.

**Read first:** `internal/api/agents.go` — the dual-path pattern is already established there.

The pattern is:
```go
if s.storeIface != nil {
    // test path: use storeIface
    result, err = s.storeIface.SomeMethod(r.Context(), params)
} else {
    // production path: use s.queries
    result, err = s.queries.SomeMethod(r.Context(), params)
}
```

`MockStore` already has `CreateApproval`, `GetApproval`, `ListApprovals`, `DecideApproval`
and `ErrAlreadyDecided` from TASK-044. Wire those.

One additional thing: `store_adapter.go` needs `queriesAdapter` implementations for these
4 methods so the production path also goes through `storeIface`. Follow the same pattern
as the agents adapter methods.

## Acceptance criteria

- [ ] All 9 `TestApproval*` tests in `approvals_test.go` pass (no 500s from nil queries)
- [ ] `TestDecideApproval_DoubleDecide` returns 409 (not 500)
- [ ] Production path: `NewServer(q, ...)` → `storeIface` set to adapter → approval handlers use it
- [ ] `go test ./...` in `eami-api/` passes

## Files to modify

- `eami-api/internal/api/approvals.go` — dual-path all 4 handlers
- `eami-api/internal/api/store_adapter.go` — add 4 approval adapter methods
