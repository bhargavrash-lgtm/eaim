# Task: Build the Memory / Episode library UI page
**From:** PM-EAMI
**To:** FE-Ops
**Priority:** normal
**Blocked by:** TASK-069 (episode recorder must exist and produce real data), ADR-019 (pending — determines which API the page reads from)

## What I need

Replace the placeholder at `eami-ui/src/pages/ops/MemoryPage.tsx` (currently 4 lines: "Memory / Episodes — coming soon (FE-Ops)") with a real episode library page: list view with filters (agent, outcome, date range), detail view showing full step sequence, and a semantic search box.

This is priority #10 on the PM roadmap — the last unbuilt UI page. All other Gateway and Ops pages (Agents, Policies, Tools, Nodes, Approvals, Audit) shipped as of the 2026-07-05 commit (`84028bb`, tagged v1.0.1).

## Context

- Do not start the real implementation until TASK-069 lands (BE-Gateway) and ADR-019 resolves — until then there's no real data to read and no confirmed endpoint to call.
- `useMemory.ts` hook already exists per BOUNDARIES.md ownership map (`eami-ui/src/hooks/useMemory.ts`) — check whether it's a stub or already scaffolded.
- Route/nav entry for Memory likely already exists in `App.tsx`/`router.tsx` (FE-Dashboard owns those files) since the sidebar shows the page today as a placeholder — confirm rather than assuming a new route is needed.
- Reference `Episode`, `EpisodeStep`, `EpisodeSearchRequest` schemas in `api/openapi.yaml` (~lines 666-761) for the data shape.

## Acceptance criteria

- [ ] `MemoryPage.tsx` renders a real episode list (paginated) instead of the placeholder text
- [ ] List view supports filtering by agent and outcome, matching the pattern used in `AuditPage.tsx`
- [ ] Clicking an episode opens a detail view showing the full step sequence
- [ ] Semantic search box calls the search endpoint and displays ranked results
- [ ] Uses the generated API client only (no raw fetch/axios) per ADR established for eami-ui
- [ ] `tsc --noEmit` passes with no errors
- [ ] No changes outside `eami-ui/src/pages/ops/`, `eami-ui/src/hooks/useMemory.ts` (per BOUNDARIES.md FE-Ops boundary) — anything else needed (routing, shared components) goes through a handoff task to the owning agent

## Files to create or modify

- `eami-ui/src/pages/ops/MemoryPage.tsx`
- `eami-ui/src/hooks/useMemory.ts`
