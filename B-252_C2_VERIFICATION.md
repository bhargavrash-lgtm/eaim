# B-252 C2 + C3 (minimal) + C4 — Admin rename, Endpoint Detail, Discover retirement: verification record

**Date:** 2026-10-05 · **By:** Claude Code · **Roadmap:** Horizon 1, CMDB completion (B-252 steps C2, C3 minimal, C4; `IA_CONSOLIDATION_MIGRATION_PLAN.md`) · **Folds:** B-228 (server-side OS filter). B-229 stays out (founder D2).

## 1. Part A and founder decisions

Part A was reported before building (facts recorded under B-252 in `BACKLOG.md`, "C2/C3/C4 brief: Part A done"). Founder decisions:

- **D1 — Option A:** extend the CMDB assets endpoint rows (`GET /v1/cmdb/assets`) with the endpoint fields, rather than having Assets call `/v1/endpoints` separately. The founder named this **AI ITAM item 7's first real implementation**; how it sets the convention is in §6.
- **D2 — Option A:** build the server-side OS filter (B-228's scope). B-229 (`has_ai`/`has_local_model`) stays out.
- **D3 — Option A:** endpoint-specific columns appear only when the kind filter is "Endpoints". The mixed list is unchanged.
- **Standing (2026-09-30):** the agent-link control is **shown** on an endpoint with no scan report. Approvers get an honest role state. An unlicensed org gets the §7.4 pending state.

## 2. What was built

**Server (`eami-api`)**

- `store/cmdb.sql.go`: `CMDBAsset` gains nullable endpoint-only fields:
  - `os`, `last_seen`, `ai_app_count`, `local_model_count`, `mcp_server_count`, `gpu_count`, `has_report`, `scanner_status`;
  - they are null for agents and tools.
- **The union carries only `os`:** a JSON field extraction, so it can be filtered. Everything else is filled **per page** by `enrichCMDBEndpoints`:
  - one query, scoped `org_id = $1 AND id = ANY($2)`;
  - it uses `/v1/endpoints`' own `latestReportJoinSQL`, the latest report by server `received_at` (B-284).
- **New `os` filter:**
  - checked against the allowlist `windows | linux | darwin`; anything else gets a 400 with a fixed message, no echo of the input;
  - excluded from the sidebar navigation counts, like `kind`, category and type.
- **GPU count is array-guarded** in all four queries (CMDB enrichment, plus the three in `endpoints.sql.go`). A report whose `gpus` is not an array counts 0 instead of failing the list with a 500.
- `api/cmdb.go`: both 500 paths now log `slog.Error` with `org_id`.

**UI (`eami-ui`)**

- **Settings → Admin** (`pages/admin/AdminPage.tsx`, a git mv):
  - `/settings` redirects to `/admin` and keeps `?tab=` and the rest of the query string.
  - New **Discovery Hub** tab, with Agent-Based and Agentless sub-tabs in `?sub=` and full WAI-ARIA wiring.
  - New **CMDB** tab.
  - Both are honest roadmap placeholders. This is the first real implementation of `DESIGN_SYSTEM.md` §7.8, which now reads "planned".
- **Endpoint Detail** (`pages/cmdb/EndpointDetailPage.tsx`) at `/assets/endpoints/:id`:
  - **Overview:** a summary grid plus Discover's detections, extracted verbatim into `components/endpoints/EndpointDetections.tsx`, with item 4's honest states. "Never reported" (`has_report = false`) is kept separate from "Report data unavailable".
  - **Agent Link:** shown even with no report.
  - **Classification:** the shared `components/cmdb/AssetClassificationTab.tsx`, which Agent Detail's tab now wraps too. Hidden from approvers.
  - **No Connections tab** (C6).
  - **Other states:** approver role state, licence-off §7.4 pending state, not-found, and load error.
- **Assets** (`pages/cmdb/AssetsPage.tsx`):
  - **Endpoints view** (`?kind=endpoint`) columns: Name, OS, Agent version, Last seen, AI apps, Local models, MCPs, GPUs, Classification, Risk, Workspace. Each count shows its honest state.
  - **Filters:** a Platform filter (server-side).
  - **URL state:** every filter, the search text and the page live in the URL, so Back from a detail page restores the exact view. Search is debounced by 250 ms and capped at 200 characters.
  - **Licence:** an unlicensed org gets the §7.4 pending state, not a red error.
  - **Row clicks:** endpoint rows open Endpoint Detail.
- **Discover retired:**
  - `/discover` redirects to `/assets?kind=endpoint`.
  - The nav entry is removed.
  - `DiscoverPage.tsx` and `EndpointDrawer.tsx` are deleted. Agent Detail's graph endpoint node now opens Endpoint Detail.
  - FinOps and Agent Lineage links now point to `/admin?tab=model-pricing`.
- `lib/rbac.ts`: `viewEndpoints` (admin, operator, viewer).

**Deliberate difference from Discover:** the Endpoints view sorts by name, as the CMDB list always has. Discover sorted by last seen. The difference is recorded, not changed. A sortable column is a C3-follow-up decision.

## 3. Verification

**Builds and tests (2026-10-05):**
- `go build ./...`, `go vet ./internal/...`, and the full `go test ./...` for eami-api (real Postgres) all pass.
- `npx tsc --noEmit` and `npx vite build` pass.
- **New real-Postgres tests** in `api/cmdb_endpoint_fields_pg_test.go`:
  - endpoint fields are populated for endpoints and null for agents and tools;
  - never-reported (`has_report=false`, `scanner_status=null`) and legacy (`has_report=true`, `scanner_status=null`) rows;
  - values equal to `GET /v1/endpoints` for the same endpoints;
  - `os=linux` and `kind=endpoint&os=windows` narrow correctly; `os=WINDOWS` and `os=freebsd` return 400;
  - an unlicensed org sees no endpoints even with `os`;
  - **the sidebar counts ignore `os`** (review M2);
  - **a non-array `gpus` returns 200 with GPU count 0** on both lists (review L3).

**Query cost (the C3 Part A measurement):**
- `EXPLAIN ANALYZE` of the enrichment query for one page, on the dev DB (8 endpoints, 4,945 reports): **0.606 ms execution**, 81 shared-buffer hits.
- The latest-report lookup uses `idx_reports_endpoint_received` (an index scan per row).
- The per-domain counts use per-endpoint indexes. At this scale the planner chose seq scans on the 3-row tables.
- The cost is bounded by the page size (≤ 25 rows), not by fleet size. The union itself only adds a JSON field extraction.
- Dev scale is small. A fleet-scale measurement is still worth taking when a large seeded org exists.

**Baseline:** captured before any change, from the live Discover page:
- 8 endpoints covering all five honest states:

  | Endpoint | States covered |
  |---|---|
  | WSL Ubuntu | real packaged agent, a real FIFO hang → models "Scan failed", gpu "Disabled", real 0s |
  | Windows | real counts, "Not known" |
  | `item4-paste-only` | "Never reported" |
  | five older endpoints | "Not known" |

- The list and drawer values for each endpoint, mapped by ID through psql.

**Live run (Playwright, real logins, real API and Postgres): 92/92 PASS.**
- **Admin:**
  - nav shows Admin, with no Discover or Settings;
  - `/settings?tab=model-pricing` redirects and keeps the tab;
  - all 6 existing tabs render;
  - Discovery Hub sub-tabs work by URL, ARIA attributes and the keyboard (ArrowLeft moves the selection and focus);
  - the CMDB placeholder renders.
- **Discover → Assets:**
  - `/discover` redirects;
  - the endpoint columns are exact;
  - the row count equals the baseline;
  - **all 8 rows match the Discover baseline cell by cell** (OS and the four counts with their honest states);
  - agent version matches psql for all 8;
  - the OS filter equals psql;
  - the mixed-list columns are unchanged;
  - search is debounced (not in the URL at 150 ms, in the URL after the debounce);
  - `maxLength` is 200;
  - a reload keeps the filtered view.
  - A separate run confirmed **Back from Endpoint Detail restores `?kind=endpoint&os=windows&q=Bhargav`**, the same rows, the search text and the Platform select.
- **Endpoint Detail, per endpoint, all 8:**
  - tabs are Overview, Agent Link, Classification;
  - **the Overview equals the baseline drawer for every detection section**;
  - "Never reported" shows on the paste-only endpoint;
  - **the Agent Link control is shown on every endpoint, including the no-report one.**
- **Classification:**
  - the admin picker saves (`ci_type_id` checked in psql), re-renders, and resets to NULL (checked in psql);
  - Agent Detail's tab is the same form.
- **Agent Detail:** the graph endpoint node opens Endpoint Detail.
- **Dead-link sweep:** 17 pages, with no `/discover` or `/settings` hrefs. The admin session had no unexpected 4xx or 5xx.
- **Viewer:** Classification and Agent Link are read-only, with no 4xx or 5xx.
- **Approver:**
  - sees the role state;
  - **no `/v1/endpoints` or `/v1/cmdb` request is made**;
  - no tabs render.
- **Unlicensed org (fixture):**
  - Endpoint Detail shows the pending state on a real 403 `module_not_licensed`;
  - Assets' Endpoints view shows the pending state, with no red error and no Retry.

Screenshots (scratchpad, not committed): `admin_cmdb.png`, `endpoint_overview.png`, `approver.png`, `nolicense.png`, `nolicense_assets.png`.

**Fixture side effect handled:** linking the WSL endpoint to the fixture agent would have pushed all 10 scanners as remote config, which would have erased the "Disabled" GPU state the baseline relies on. The fixture agent's `agent_configs` was set to the same 9 scanners (gpu excluded) at 60 s first.

## 4. Reviews

**Security review: clean** (no Critical, High or Medium). It checked:
- org scoping of the enrichment (`org_id` plus `ANY($2)` over the page's own IDs);
- the `os` allowlist, with no reflection of input in the 400;
- licence gating kept, with endpoints excluded for unlicensed orgs and a test for it;
- approvers making no endpoint requests;
- no raw error text crossing the trust boundary (standing check): the 400 is a fixed string, and the 500 detail goes only to the server log.

**Code review:** no Critical or High. Every finding was fixed in this brief, except where noted:

| # | Finding | Disposition |
|---|---|---|
| M1 | Assets filters lost on Back from Endpoint Detail | **Fixed.** All filters, search and the page are in the URL. Verified live (Back and reload). |
| M2 | The `os` filter narrowed the sidebar navigation counts | **Fixed.** Reset with the other navigation filters, with a test. |
| L1 | "Never reported" conflated with a missing report body | **Fixed.** Separate "Report data unavailable" state. |
| L2 | Agent version column dropped; sort changed from last seen to name | **Fixed** (column restored from the row's existing `detail`). The sort difference is recorded as deliberate (§2). |
| L3 | `jsonb_array_length` on a non-array `gpus` would 500 | **Fixed** in all 4 queries, with a test. |
| L4 | Unlicensed Endpoints view showed a red error | **Fixed.** §7.4 pending state, verified live. |
| L5 | Discovery Hub sub-tabs lacked full ARIA tab wiring | **Fixed.** `aria-controls`, tabpanel, roving tabIndex and arrow keys, verified live. |
| L6 | `DESIGN_SYSTEM.md` referenced the deleted `EndpointDrawer`; this file was cited but missing | **Fixed.** |
| L7 | `api/openapi.yaml` drift | **Logged** in NOTES.md (Architect-EAMI owns the file). |
| P1 | No server log on the CMDB 500 paths | **Fixed.** |
| P2 | No search debounce or length cap | **Fixed** (250 ms, 200 characters). |
| P3 | Assets' page number isn't clamped when a filter shrinks the result | Pre-existing. **Logged** in NOTES.md. |
| P4 | `EndpointDetections`' collapsible-section state resets on refetch | Pre-existing (moved verbatim). **Logged.** |
| P5 | Stale "Discover/Settings" comment in `UserMenu.tsx` | **Fixed.** |

**Orphaned actions and links (standing check):**
- Every action Discover offered is still reachable:
  - **list:** Assets' Endpoints view;
  - **platform filter:** server-side now;
  - **search;**
  - **drawer content:** Endpoint Detail's Overview;
  - **agent link:** the Agent Link tab;
  - **classification:** the tab, previously the Assets panel.
- The live dead-link sweep found no `/discover` or `/settings` hrefs. Both old URLs redirect.
- The Assets panel's endpoint branch is no longer reachable from endpoint rows; tool rows still use it until C5.

## 5. Cleanup

- **WSL:** the package is purged, `/etc/eami` is removed, and the FIFO fixture is removed (`systemctl` shows the agent inactive).
- **Collector:** the C2 key for `Bhargavtej` is revoked.
- **Dev DB, in one transaction:**
  - the WSL endpoint is unlinked;
  - `c2-graph-agent` is deleted (its `agent_configs` row cascaded);
  - the fixture CI type is deleted;
  - the `c2-nolicense` fixture org is deleted (with its user and endpoint);
  - the `c2-admin`, `c2-viewer` and `c2-approver` users are soft-deleted (`deleted_at`);
  - 16 refresh tokens are deleted.
- **Audit log untouched:** 1706 rows before and after, with the same max id.
- Scratch key and password files are deleted.

## 6. How D1 fits the general API convention (master-sequence item 7)

The founder named D1 item 7's first real implementation. Item 7's deliverable is still **one written convention** that the founder locks, and this brief doesn't write or lock it. Item 7 stays open. What this brief does is give it a concrete, tested precedent. It follows the master sequence's standing rule: *every new data type gets a real, general, filterable, versioned endpoint, with `GET /v1/cmdb/assets` as the reference shape*. The precedent, as built:

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

**Still open for item 7, not decided here:**
- B-137's real state;
- whether the convention requires openapi-first;
- sort and order parameters: Assets sorts by name with no `sort` parameter, and Discover sorted by last seen;
- the versioning policy for a breaking change.
