# Task: Verify and fix Approvals page end-to-end
**From:** PM-EAMI  
**To:** FE-Ops  
**Priority:** normal  
**Blocked by:** TASK-042 (Slack config), stack must be running

## What I need

The Approvals page exists (`eami-ui/src/pages/ops/ApprovalsPage.tsx`) and the hooks exist (`useApprovals.ts`). But the full approval loop — escalation → Slack → UI appear → human decides → gateway continues — has never been tested end-to-end. Verify it works and fix anything broken.

### Test scenario

1. Create a policy in the UI: action = `escalate`, conditions = `tool_name = "bash"` (or any tool name you can trigger).
2. Make an MCP call through the gateway that matches that policy.
3. Verify:
   - The approval appears in the Approvals page "Pending" tab within 5 seconds.
   - Clicking "Approve" calls `POST /v1/approvals/{id}/decide` with `{"decision": "approved"}` and returns 200.
   - The approval moves from "Pending" to "History".
   - The pending approval count badge in the sidebar decrements.
4. Repeat with "Deny" — verify the call is rejected.

### Known issues to check

- **5s poll**: `ApprovalsPage.tsx` polls pending approvals every 5s via `usePendingApprovalCount`. Verify the `queryFn` is actually calling the API and not silently failing.
- **Optimistic UI**: The decide mutation should optimistically remove the approval from Pending immediately, then refetch. Check that the list doesn't flicker or duplicate.
- **Empty state**: When no approvals are pending, the Pending tab should show `EmptyState`, not a blank white div.
- **Error state**: If the decide API call fails (e.g. double-decide), the UI should show an error toast, not silently break.

### If the API isn't returning approvals

Check `GET /v1/approvals?status=pending` directly:
```bash
curl -H "Authorization: Bearer <token>" http://localhost:8081/v1/approvals?status=pending
```
If it returns an empty array when approvals exist, the gateway's `Submit()` may not be writing to the `approvals` table — flag to PM-EAMI as a blocker.

## Acceptance criteria

- [ ] Pending approvals appear in the UI within 5s of being created
- [ ] "Approve" decision calls the correct endpoint and removes the item from Pending
- [ ] "Deny" decision calls the correct endpoint and removes the item from Pending
- [ ] Approved/denied approvals appear in the History tab
- [ ] Sidebar approval badge shows correct pending count (0 when none pending)
- [ ] Double-deciding an approval (race condition) shows an error, does not crash
- [ ] `tsc --noEmit` exits 0 after any changes

## Files to modify (if needed)

- `eami-ui/src/pages/ops/ApprovalsPage.tsx`
- `eami-ui/src/hooks/useApprovals.ts`

## Files to read first

- `eami-ui/src/hooks/useApprovals.ts` — current implementation
- `eami-ui/src/pages/ops/ApprovalsPage.tsx` — current page
- `eami-ui/src/components/layout/Sidebar.tsx` — badge implementation
- `api/openapi.yaml` — approvals endpoints spec
