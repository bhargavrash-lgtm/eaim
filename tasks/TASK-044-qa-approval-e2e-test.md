# Task: End-to-end approval flow integration test
**From:** PM-EAMI  
**To:** QA-EAMI  
**Priority:** normal  
**Blocked by:** TASK-042 (Slack config), TASK-043 (FE approvals verified)

## What I need

Write an integration test that exercises the full approval loop without a real MCP client. Use the eami-api directly to simulate the gateway submitting an approval and deciding it.

### Test file: `eami-api/internal/api/approvals_test.go`

Use the same `MockStore` + `httptest.Server` pattern as the other test files. You don't need a real DB for the approval CRUD tests.

### Tests to write

**Happy path:**
- `TestCreateApproval_Success` — POST /v1/approvals with valid JWT (operator+) returns 201 with approval ID
- `TestGetApproval_Success` — GET /v1/approvals/{id} returns the created approval
- `TestListApprovals_FilterByStatus` — GET /v1/approvals?status=pending only returns pending approvals
- `TestDecideApproval_Approve` — POST /v1/approvals/{id}/decide `{"decision":"approved"}` returns 200
- `TestDecideApproval_Deny` — POST /v1/approvals/{id}/decide `{"decision":"denied"}` returns 200

**Error paths:**
- `TestDecideApproval_DoublDecide` — deciding an already-decided approval returns 409
- `TestDecideApproval_InvalidDecision` — `{"decision":"maybe"}` returns 400
- `TestListApprovals_RequiresAuth` — no JWT returns 401
- `TestCreateApproval_ViewerForbidden` — viewer role cannot create approvals (403)

### MockStore additions needed

The existing `MockStore` in `store_mock.go` may not have approval methods. Add to the `Store` interface and `MockStore`:

```go
// Store interface additions
CreateApproval(ctx context.Context, arg CreateApprovalParams) (StoreApproval, error)
GetApproval(ctx context.Context, id uuid.UUID) (StoreApproval, error)
ListApprovals(ctx context.Context, orgID uuid.UUID, status string) ([]StoreApproval, error)
DecideApproval(ctx context.Context, id uuid.UUID, decision, decidedBy string) (StoreApproval, error)

// StoreApproval type
type StoreApproval struct {
    ID         uuid.UUID
    OrgID      uuid.UUID
    AgentID    uuid.UUID
    AgentName  string
    Tool       string
    Action     string
    Parameters []byte
    Status     string // "pending" | "approved" | "denied"
    DecidedBy  string
    DecidedAt  *time.Time
    CreatedAt  time.Time
}
```

Check `approvals.go` for the exact handler logic before implementing the mock.

## Acceptance criteria

- [ ] All 9 tests exist and run
- [ ] Happy path tests pass
- [ ] 409 on double-decide passes
- [ ] 400 on invalid decision passes
- [ ] 401 without auth passes
- [ ] 403 for viewer role passes
- [ ] `go vet ./...` exits 0
- [ ] No `t.Skip()`

## Files to create or modify

- `eami-api/internal/api/approvals_test.go` — new file
- `eami-api/internal/api/store_mock.go` — add StoreApproval type + MockStore methods

## Files to read first

- `eami-api/internal/api/approvals.go` — handler implementations
- `eami-api/internal/api/store_mock.go` — existing mock pattern
- `eami-api/internal/api/agents_test.go` — reference for test server setup
