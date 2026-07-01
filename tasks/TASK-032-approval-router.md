# Task: Implement gateway approval router
**From:** PM-EAMI
**To:** BE-Gateway
**Priority:** normal
**Blocked by:** none

## What I need

When eami-api decides an approval (`POST /v1/approvals/{id}/decide`), it fires:
```sql
SELECT pg_notify('approval_decision', '{"approval_id":"<uuid>"}');
```
The gateway must subscribe to this channel and resume the SSE session that is holding for that approval.

## Context

- `eami-api/internal/store/approvals.sql.go` → `NotifyApprovalDecision()` already sends the notify
- The MCP handler (`eami-gateway/internal/mcp/handler.go`) dispatches tool calls via the `dispatch` func in `main.go`
- Currently `dispatch` returns an error for `ActionEscalate` with the message "approval router not yet available" — this is the hook point
- An approval request has: `approval_id`, `org_id`, `agent_id`, `tool_name`, `action`, `parameters`
- The gateway needs to:
  1. On `ActionEscalate`: persist an approval record to `approval_requests` table (INSERT), then **block** the dispatch goroutine waiting for a decision
  2. Subscribe to `pg_notify('approval_decision')` via `pgx` LISTEN
  3. When a notification arrives with an `approval_id`, look up the decision in `approval_requests`, unblock the waiting goroutine, and either allow (forward to proxy) or deny

## Acceptance criteria

- [ ] New package `eami-gateway/internal/approval/` with:
  - `Router` struct: wraps pgxpool, manages pending approvals map (`sync.Map` keyed by approval UUID)
  - `Hold(ctx, approvalID, ac ActionContext) (json.RawMessage, error)` — blocks until decided or ctx cancelled
  - `Run(ctx)` — goroutine that LISTENs on `approval_decision` channel and resolves held approvals
  - `Submit(ctx, ac ActionContext) (uuid.UUID, error)` — INSERTs into `approval_requests` table
- [ ] `main.go` dispatch func: replace the `ActionEscalate` stub error with a call to `approval.Submit()` then `approval.Hold()`
- [ ] Hold timeout: if no decision within 10 minutes, deny with reason "approval timed out"
- [ ] `Run()` started as a goroutine in `main.go` alongside the HTTP server; stops cleanly on context cancel
- [ ] Slack webhook notification on escalate (use `GATEWAY_APPROVAL_SLACK_WEBHOOK` from config, already in `eami-gateway.yaml`). POST a simple JSON block: agent name, tool, action, approve/deny links using `GATEWAY_UI_BASE_URL/approvals/{id}`
- [ ] `go build ./...` passes from `eami-gateway/`
- [ ] No new external dependencies beyond what is already in `go.mod`

## Files to create or modify

- `eami-gateway/internal/approval/router.go` — new package
- `eami-gateway/cmd/gateway/main.go` — wire approval.Router into dispatch, start Run() goroutine
- `eami-gateway/internal/config/config.go` — confirm `ApprovalSlackWebhook` and `UIBaseURL` fields exist (they should already be present)

## Note on `go 1.24`

`eami-gateway/go.mod` must stay on `go 1.24`.
