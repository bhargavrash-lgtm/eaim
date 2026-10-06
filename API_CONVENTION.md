# EAMI API convention

**Status: LOCKED v1 (principles 1-10), 2026-10-06; section 11 open.** Master-sequence item 7 (`AI_ITAM_EPIC_MASTER_SEQUENCE.md`). Drafted 2026-10-05 by Claude Code; locked by the founder 2026-10-06. Principles 1–10 are settled for every Phase 2 item. Section 11 is open and closes with B-269 Slice 1's plan.

**Scope:** every `eami-api` route a UI, an agent or a future external consumer reads or writes. It applies the master sequence's standing rule: *every new data type gets a real, general, filterable, versioned endpoint, not a narrow one built only for the screen that needs it now.* The reference shape is `GET /v1/cmdb/assets` (`data`, `meta`, `counts`; `page`/`per_page`; server-side filters).

**Contract ownership:** `api/openapi.yaml` belongs to Architect-EAMI (`BOUNDARIES.md`). Known gaps between it and the code are tracked in `API_CONTRACT_DRIFT.md`.

---

## Principles 1–7 (verbatim from `B-252_C2_VERIFICATION.md` §6, the precedent as built)

1. **Extend the general endpoint, don't add a narrow one.**
   - Assets' endpoint view needed endpoint fields.
   - Instead of a UI-specific `/v1/assets/endpoint-rows`, the fields were added to the existing general, kind-agnostic, filterable, paginated `GET /v1/cmdb/assets`.
   - The response shape (`data`, `meta`, `counts`) is unchanged.
2. **Kind-specific fields are nullable, and null means "not applicable".**
   - Endpoint-only fields are `null` on agent and tool rows, never `0` or `""`.
   - A client can tell "doesn't apply" apart from "zero" without knowing the kind rules.
   - This matches item 4's honest-state principle, applied to the API contract.
3. **One meaning per field name across endpoints.**
   - `ai_app_count`, `has_report`, `scanner_status` and the others carry the same names and the same "latest report" rule (server `received_at`, B-284) as `GET /v1/endpoints`.
   - Both read the same SQL fragment (`latestReportJoinSQL`).
   - A test asserts the two endpoints agree for the same endpoints.
4. **Filters are server-side, allowlisted, and fail closed.**
   - `os` is validated against a fixed set.
   - An unknown value is a 400 with a fixed message, not silently ignored. Silently ignoring is the bug class of B-226 and B-229.
   - It is never reflected back in the error.
   - Navigation counts ignore view-local filters (`kind`, category, type, `id`, `os`).
5. **Cost is bounded per page.**
   - Only cheap, filterable columns enter the union that's filtered and counted. Here that's `os`, a JSON field extraction.
   - Heavy per-row data is enriched once per page, scoped by `org_id` and the page's own IDs.
   - It was measured at 0.6 ms per page on the dev DB.
6. **Authorization and licensing come through unchanged.**
   - The extended rows inherit the endpoint's existing gates: Discovery licence and the B-253 role split.
   - An unlicensed org's endpoints stay absent, and a test covers it.
7. **Contract and versioning.**
   - The API stays at `/v1`, with additive optional fields and parameters only, so no version bump is needed under current practice.
   - `api/openapi.yaml` is Architect-EAMI's file, so the UI types the new fields locally (`useCMDB.ts`'s `CMDBEndpointFields`), and the drift is **logged in NOTES.md**.
   - That's the same handling as C1's `?id=`.
   - Item 7 should decide whether additive fields need an openapi update *before* the build (strict) or within the same epic (as done here).

> Note: the source section numbers these as seven. The B-252 C2 completion report summarised them as six by leaving out #6 (authorization and licensing). This copy keeps all seven. As of this draft, drift is consolidated in `API_CONTRACT_DRIFT.md`, not in NOTES.md (#7's wording is kept verbatim as the record of what was done).

---

## 8. Sorting

- **Server-side.** A list endpoint that is paginated sorts on the server, with a `sort` parameter (field) and an `order` parameter (`asc` | `desc`). The client never sorts only the loaded page and presents it as an ordering of the whole set.
- **Allowlisted per resource.** Each resource documents the fields it can sort by. Anything else, for either `sort` or `order`, is a **400 with a fixed message**. It is never ignored and never reflected back (the same rule as principle 4).
- **Deterministic tie-break on `id`.** Every ordering ends with `id`, in the same direction, so pages never repeat or skip rows when sort keys tie.
- **Nulls last,** in both directions, so "not applicable" and "not known" rows never crowd the top of a list.
- **Default:** when `sort` is absent, the resource's documented default applies (today `GET /v1/cmdb/assets` uses `lower(name), asset_kind, id`).

## 9. Breaking changes

- **Additive under `/v1` is free:** new optional fields, new optional parameters, new endpoints, new enum values on a field documented as open.
- **Breaking:** removing a field, renaming a field, or changing the meaning of a field (including its type, units, nullability rule or the rule that computes it). A required new parameter is also breaking.
- **Until a first external consumer exists:** a breaking change is allowed only if every in-repo consumer (UI, agent, collector, gateway, tests) is updated **in the same commit**, with a note in `API_CONTRACT_DRIFT.md`.
- **After B-137 exposes a public surface:** a breaking change needs a new major version (`/v2` for the affected surface), plus a deprecation period for the old one. The length of that period is set when B-137 is designed.
- **Exception: security fixes** may break a contract immediately at any stage. Each one must be documented: what changed, why, and which consumers were affected. That goes in `API_CONTRACT_DRIFT.md`, and also in the release notes once a public surface exists.

## 10. Drift is recorded in the same commit

- **Any commit that adds or changes an API route, parameter, field, response shape or status code appends a row to `API_CONTRACT_DRIFT.md` in that same commit**, unless the same commit also updates `api/openapi.yaml` through Architect-EAMI.
  - Each row names the route or schema, the drift, its source (B-ID) and its tracking ID.
  - A commit that removes drift (Architect-EAMI fixed the spec) deletes the row in the same commit.
- **Reviewers check it.** Every mandatory code-review pass on a change that touches `eami-api/internal/api/router.go`, a handler's request or response types, or a store type serialised to JSON confirms that the matching `API_CONTRACT_DRIFT.md` row exists. A missing row is a review finding, the same as a missing test.
- **Ticking (founder, at lock):** a Phase 2 slice that adds routes can't be ticked in the master sequence until Architect-EAMI has acknowledged its drift rows.
- Why: before this rule, drift was logged ad hoc in NOTES.md, BACKLOG.md and verification files, and the spec fell 54 routes behind without anyone tracking it.

---

## 11. Not yet specified (to be settled from Slice 1's plan)

These aren't settled yet. **B-269 Slice 1's plan proposes an answer for each, and this section gets the approved answers before Slice 1 builds.** Until then, no route should treat any of them as decided.

- **Pagination:** defaults, maximums, and what happens on overflow (too-large `page`, `per_page` out of range: clamp or 400). Today routes differ (some clamp through `pagination()`, the admin audit route returns a 400).
- **Response envelope and meta fields:** `data`, `meta` (`total`, `page`, `per_page`), `counts`; which are always present; the shape of single-object responses.
- **Error body:** `{code, field, message}`, with stable codes and a `field` from a fixed list, **never echoing a submitted value** (D10).
- **Draft/publish and optimistic locking:** the status code and body for a version conflict (a second admin editing the same draft), and for publish, revert and archive.
- **Action and bulk routes:** how non-CRUD actions (publish, revert, archive, revoke) and bulk operations (bulk adopt) are shaped, and how a bulk operation reports partial results.
- **Idempotency:** which writes are idempotent, and whether retried creates and actions use an idempotency key.
- **IDs, timestamps and naming:** ID format, timestamp format and time zone, and field and route naming (case, plurals, verbs).
- **Body-size limits:** a request-size cap for every write route (B-287).
- **The org always comes from the session, never from a request parameter:** no route accepts an org ID in the path, query or body for scoping; a supplied one is rejected, not honoured.

---

## Founder decisions at lock (2026-10-06)

- **`api/openapi.yaml` is updated by Architect-EAMI within the epic, in batches** (not OpenAPI-first before each build). The hand-off is `tasks/TASK-071-openapi-drift-handoff.md`.
- **A Phase 2 slice that adds routes can't be ticked until its drift rows are acknowledged** by Architect-EAMI (recorded in §10).
- **The one exception, not a precedent: B-269 Slice 0b.** Ticked 2026-10-06, before the route-acknowledgement rule existed. Drift row C24 acknowledgement pending (TASK-071). Grandfathered once; Slice 1 and later follow the rule strictly. C24 is on TASK-071's list, so one acknowledgement covers it. No other slice gets this exception.
- **The deprecation-period length stays unset until B-137 has a design.**
- **Still true:** B-137 is a scoping placeholder only, logged 2026-08-29, with no investigation and no design. The public-surface rules in §9 are therefore forward-looking.
