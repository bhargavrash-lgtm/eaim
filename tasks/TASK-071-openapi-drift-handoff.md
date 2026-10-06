# Task: openapi.yaml drift list: hand-off and acknowledgement
**From:** Founder (filed by Claude Code, 2026-10-06)
**To:** Architect-EAMI
**Priority:** high
**Blocked by:** none

## What I need

The AI ITAM epic keeps `API_CONTRACT_DRIFT.md` as the single list of
differences between what the API does and what `api/openapi.yaml`
documents. Current counts are in the file header.

Needs from you, in this order:
1. Section A, the documented operations that don't exist. Anyone
   building from the spec gets a 404, so these come first.
2. The undocumented routes, most of them added since the last spec
   update.
3. The field and response mismatches.

Please acknowledge receipt and say when you'll take it. Updating
within the epic, in batches, is fine; it doesn't have to be one pass.
Each commit that changes a route or field now appends to the list in
the same commit (`API_CONVENTION.md` section 10), so it will keep
growing until you take it.

The acknowledgement is required before Slice 2 of the presets work,
which builds UI on the new routes.

**Includes row C24** (`GET /v1/audit/admin-events` and `.../verify`, added by
B-269 Slice 0b on 2026-10-06, after this task was filed). One acknowledgement
covers it: Slice 0b was ticked before the route-acknowledgement rule existed and
is grandfathered on condition that C24 is acknowledged with the rest of this list. Condition met: C24 acknowledged 2026-10-06; the exception is closed.

## Context

- Master sequence item 7a (`AI_ITAM_EPIC_MASTER_SEQUENCE.md`): the gate is
  hand-off **plus acknowledgement**, not completion. Fixing the drift is not a
  precondition for item 8; it proceeds on Architect-EAMI's schedule.
- Decision D1 (`DISCOVERY_PRESETS_DESIGN.md` §12): the acknowledgement was
  waived for B-269 Slices 0–1 and is **required before Slice 2**.
- `api/openapi.yaml` is Architect-EAMI's file (`BOUNDARIES.md`); Code records
  drift and does not edit the contract.

## Acknowledgement

> ACKNOWLEDGED 2026-10-06 by founder, acting in the Architect-EAMI role: drift list received as of 55600e7, scope accepted (sections A, undocumented routes, mismatches, and C24). Updates to api/openapi.yaml will be made in batches by a Code session under founder approval; first batch (section A) planned to be completed before Slice 2 of B-269 starts.

This acknowledgement covers the drift list **as of 55600e7 only**. Routes added later need their own acknowledgement under the `API_CONVENTION.md` rule (§10, founder decisions).

`55600e7` is the latest commit that changed `API_CONTRACT_DRIFT.md` (B-269 Slice 0b, which added C24): 3 operations in §A, 56 undocumented routes in §B, 24 mismatches in §C.

## Acceptance criteria
- [x] Architect-EAMI acknowledges receipt, with a date for taking it, recorded
      on item 7a in `AI_ITAM_EPIC_MASTER_SEQUENCE.md` (and in `CONTEXT.md`).
- [ ] §A fixed first, then §B, then §C, in batches within the epic.
- [ ] Each fixed row is deleted from `API_CONTRACT_DRIFT.md` in the same commit
      as the `openapi.yaml` change, and `eami-ui/src/api/schema.ts` is
      regenerated (the file's §D).

## Files involved
- `api/openapi.yaml` (Architect-EAMI)
- `API_CONTRACT_DRIFT.md` (rows removed as they are fixed)
- `eami-ui/src/api/schema.ts` (regenerated)
