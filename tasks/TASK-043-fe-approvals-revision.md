# Task: TASK-043 Revision — Build the Approvals page (was placeholder stub)
**From:** PM-EAMI  
**To:** FE-Ops  
**Priority:** high  
**Blocked by:** none

## What was found

`ApprovalsPage.tsx` in both Repo and FE-Dashboard is still this placeholder:
```tsx
export function ApprovalsPage() {
  return <div className="p-6 text-sm text-gray-400">Approvals — coming soon (FE-Ops)</div>
}
```

`useApprovals.ts` in Repo has `refetchInterval: 5_000` already present, but none of the
claimed onMutate/onError/rollback changes. `useDecideApproval` only has `onSuccess`.

**Nothing from the previous TASK-043 delivery landed in Repo.** Start from scratch.

## What I need

### 1. useApprovals.ts — add optimistic UI + error path

Replace `useDecideApproval` with this pattern:

```typescript
export function useDecideApproval() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, decision, reason }: DecideParams) => {
      const { data, error } = await api.POST('/v1/approvals/{approvalId}/decide', {
        params: { path: { approvalId: id } },
        body: { decision, reason },
      })
      if (error) throw error
      return data
    },
    onMutate: async ({ id }) => {
      await qc.cancelQueries({ queryKey: ['approvals'] })
      const snapshot = qc.getQueryData(['approvals', { status: 'pending' }])
      qc.setQueryData(['approvals', { status: 'pending' }], (old: any) => {
        if (!old?.data) return old
        return { ...old, data: old.data.filter((a: ApprovalRequest) => a.id !== id) }
      })
      return { snapshot }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) {
        qc.setQueryData(['approvals', { status: 'pending' }], ctx.snapshot)
      }
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ['approvals'] }),
  })
}
```

### 2. ApprovalsPage.tsx — build the real page

The page must have:

**Tabs:** "Pending" | "All"

**Pending tab:**
- List of pending approvals (calls `useApprovals({ status: 'pending' })`)
- Each row: agent name, tool, action, timestamp, Approve + Deny buttons
- Approve/Deny buttons call `useDecideApproval`, disabled while mutation is in-flight
- **Optimistic removal:** the row disappears immediately on click (from onMutate above)
- **Error banner:** if mutation fails (e.g. 409 double-decide), show dismissible red banner: `"Could not decide: {error message}"`. Clear it on next attempt.
- **Empty state:** when no pending approvals, show: `"No pending approvals"` with a checkmark icon

**All tab:**
- List of all approvals with a status badge (pending/approved/denied/expired)
- Read-only, no action buttons
- Empty state: `"No approvals yet"`

**Loading state:** skeleton rows while query is in-flight (or a spinner — keep it simple)

**5-second poll:** already handled by `refetchInterval: 5_000` in `useApprovals` — no changes needed

### 3. Sync to Repo

Both `eami-ui/src/hooks/useApprovals.ts` and `eami-ui/src/pages/ops/ApprovalsPage.tsx`
must be saved to **Repo** (not just FE-Dashboard).

## Acceptance criteria

- [ ] `ApprovalsPage.tsx` is NOT a placeholder stub — it renders tabs and approval rows
- [ ] Pending tab shows actionable approve/deny buttons
- [ ] Clicking Approve/Deny removes the row immediately (optimistic)
- [ ] On 409 double-decide: the row reappears AND a red dismissible error banner appears
- [ ] Empty state renders when `data.length === 0`
- [ ] `useDecideApproval` has `onMutate` snapshot + `onError` rollback + `onSettled` invalidate
- [ ] `tsc --noEmit` exits 0 in `eami-ui/`
- [ ] Changes are present in **Repo/eami-ui** (not just FE-Dashboard)

## Files to modify

- `eami-ui/src/hooks/useApprovals.ts`
- `eami-ui/src/pages/ops/ApprovalsPage.tsx`
