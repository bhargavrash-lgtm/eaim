# Task: Fix 5 TypeScript errors in ApprovalsPage.tsx blocking npm run build
**From:** PM-EAMI  
**To:** FE-Ops  
**Priority:** high  
**Blocked by:** none

## What needs fixing

`npm run build` exits 2 with 5 errors, all in `ApprovalsPage.tsx`.
These are prop name mismatches against shared components — same pattern as AlertsPage.

**File:** `eami-ui/src/pages/ops/ApprovalsPage.tsx`

| Line | Error | Fix |
|------|-------|-----|
| 70 | `level=` on `<RiskPill>` — prop doesn't exist | Change to `tier=` |
| 121 | `string \| undefined` passed where `string` required | Add `?? ''` or `!` non-null assertion |
| 179 | `level=` on `<RiskPill>` — prop doesn't exist | Change to `tier=` |
| 306 | Icon component passed directly to `EmptyState icon=` | Wrap: `icon={<CheckCircle className="h-8 w-8" />}` |
| 341 | `isLoading=` on `<DataTable>` — prop doesn't exist | Change to `loading=` |

Look at `AlertsPage.tsx` for the exact pattern — FE-Dashboard already fixed the same issues there.

## Acceptance criteria

- [ ] `npm run build` exits 0 (zero tsc errors)
- [ ] All 5 fixes applied — no other changes to ApprovalsPage.tsx
- [ ] Files written to **Repo** (`D:\AI\EAMI\Repo\eami-ui\src\pages\ops\ApprovalsPage.tsx`)
- [ ] Verify with Read after writing

## File to modify

- `eami-ui/src/pages/ops/ApprovalsPage.tsx` — lines 70, 121, 179, 306, 341 only
