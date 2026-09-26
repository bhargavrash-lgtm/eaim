# B-196 Increment 2 Brief 1 — Verification Record

Written 2026-09-26 by Claude Code in response to a founder verification challenge on Brief 1's "complete" claim. The challenge had three parts: (1) did the mandatory code and security review subagents actually run, (2) run the real Section 6 Playwright acceptance, and (3) prove fixture cleanup and marker state. Everything below comes from primary sources: the Codex session transcripts under `~/.codex/sessions/2026/09/{25,26}/`, `git`, live `psql`, live HTTP, and a real Playwright/Chromium run against the rebuilt shared Docker stack.

## 1. Review provenance

| Pass | Did it run? | Source |
|---|---|---|
| Codex general code review (`/root/b196_general_review`) | **Yes.** It ran on a *pre-fix* snapshot from 2026-09-26 05:10 to 05:14 UTC and found 5 issues. | `rollout-2026-09-26T10-40-28-01a0dc1f-….jsonl`, final message quoted below |
| Codex security review (`/root/b196_security_review`) | **No.** It was spawned at 05:30:48 UTC and failed 2.9 s later with `usage_limit_exceeded`. It produced no report. BUILT.md/CONTEXT.md did disclose this. | `rollout-2026-09-26T11-00-48-01a0dc31-….jsonl`, error quoted below |
| Independent security review (Claude Code subagent, this session) | **Yes.** It reviewed committed `6924729`. | Report quoted in full in §3 |
| Independent post-fix code review (Claude Code subagent, this session) | **Yes.** It reviewed committed `6924729` and re-checked the 5 earlier findings. | Report quoted in full in §4 |

### 1a. Codex general review — final message, verbatim

```
General review findings:

1. **High — type-name uniqueness violates the approved data model.**  
   [000024_cmdb_classification.up.sql](C:\AI\EAIM\eaim\schema\migrations-v2\000024_cmdb_classification.up.sql:30) and [schema.sql](C:\AI\EAIM\eaim\schema\schema.sql:668) use `UNIQUE (org_id, asset_kind, normalized_name)`. The brief and Part A require `UNIQUE (org_id, normalized_name)`, so duplicate normalized type names are currently allowed across asset kinds.

2. **Medium — the exact-one-default invariant can be bypassed by changing `ci_types.org_id`.**  
   [000024_cmdb_classification.up.sql](C:\AI\EAIM\eaim\schema\migrations-v2\000024_cmdb_classification.up.sql:74) validates only `COALESCE(NEW.org_id, OLD.org_id)`. Moving a default to another org while clearing `is_default` checks the destination but can leave the source org with no default. Make `org_id` immutable or validate both old and new org/kind pairs.

3. **Medium — classification counts do not follow the active filters in the UI.**  
   [AssetsPage.tsx](C:\AI\EAIM\eaim\eami-ui\src\pages\cmdb\AssetsPage.tsx:27) receives filter-aligned counts from `/v1/cmdb/assets` but ignores them. The hierarchy renders unfiltered `/classifications` counts at line 50, so search and kind/classification filters can disagree with the table total.

4. **Medium — the selected classification does not control the page heading.**  
   [AssetsPage.tsx](C:\AI\EAIM\eaim\eami-ui\src\pages\cmdb\AssetsPage.tsx:43) keeps the top-bar heading fixed as “Assets” and the subtitle static, contrary to the required classification-scoped heading.

5. **Low/Medium — async buttons lack the required loading indicator.**  
   Category/type delete actions at [AssetsPage.tsx](C:\AI\EAIM\eaim\eami-ui\src\pages\cmdb\AssetsPage.tsx:79) are merely disabled during mutation, and Retry at line 57 does not show refetch progress. This violates the standing `Button.isLoading` rule.

Coverage also lacks explicit proofs for org-wide normalized type duplication, licensed endpoint inventory, pagination beyond 200 rows, and read-back of `classification_source` after explicit assignment/reset.
```

### 1b. Codex security review — the complete record of that run, verbatim

```
{"type":"task_complete","turn_id":"01a0dc31-d6ec-72b1-9086-ee520e29344a","last_agent_message":null,"error":{"message":"You’ve hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), visit https://chatgpt.com/codex/settings/usage to purchase more credits or try again at 1:33 PM.","codex_error_info":"usage_limit_exceeded"},"started_at":1790400648,"completed_at":1790400651,"duration_ms":2853}
```

The owning Codex agent then did the security review itself, inline and not through an independent subagent. This record treats that as **not** meeting the independent security-review requirement. §3 closes that gap.

## 2. Live Section 6 acceptance (Playwright)

**Environment:**
- The `eami-api` and `eami-ui` containers were rebuilt and restarted from committed `6924729` at 2026-09-26 10:49 UTC. The earlier images were built at 10:20 UTC, 10 minutes before the commit.
- Health checks returned HTTP 200: API `/health` and UI `/`.
- Browser: Playwright 1.63 with Chromium, installed only in the session scratchpad. Nothing was added to `eami-ui`'s manifest.

**Fixtures**, all with the prefix `b196verify` or `B196 Verify`, and all removed (§5):
- **Dev Org:** admin, operator and viewer users, plus one endpoint, one agent, one tool, and 10 extra tools that push the total to 31 assets across 2 pages.
- **Throwaway org B:** its own admin, an `ORGB-SECRET-CAT` category, an `ORGB-SECRET-TYPE` type, and an agent and a tool.
- The UI flows created a `B196 Verify Cat` category and one type per asset kind.
- Write requests were delayed by 1.5 s through route interception so the pending state could be observed.

**Result: 59 checks.** 55 passed on the first run. The 4 failures break down as follows:

| Failure | Classification | Evidence |
|---|---|---|
| Rows 13–14: "Create agent/tool type" | **Script assertion defect, not a product defect.** | DB rows exist with the right kinds (`agent` at 10:53:41, `tool` at 10:53:45). Screenshot `05-types-created.png` shows all three rows and the toast. A re-probe counted exactly 1 row for each. A focused re-run (below) created a new agent type and saw the toast, then deleted it through the UI with spinner, disabled state and toast. |
| Row 25: sidebar counts while a type is selected | **Real product defect (review finding N1, Medium).** Confirmed live. | With `B196 Verify EP Type` selected, "All assets" showed `1` and "AI systems" showed `0`, although the org has 10 agents. |
| Row 33: admin console error | **Expected.** | The only console error was the browser's own `409 (Conflict)` network log for the deliberate duplicate-name test in row 11, which the UI surfaced as an error toast. |

**Focused re-run** (`probe.js`) of the create and delete toast path:
- Create agent type: toast = true, row = 1.
- Delete pending: disabled = true, spinner = 1.
- Delete: toast "Type deleted." = true, rows remaining = 0, DB count = 0.
- Console errors: none.

**Mapping to the 8 Section 6 items:**

| # | Item | Verdict |
|---|---|---|
| 1 | Create category/type | **PASS**: category created through the UI, and one type of each kind (DB + screenshot + focused re-run) |
| 2 | Assign endpoint, agent, tool | **PASS**: each row read back `explicit` with the new type. The endpoint dropdown lists endpoint types only. The API rejects a cross-kind assignment with 404. |
| 3 | Filter/search/page | **PASS**: total 31 matches the DB, page 1 = 25 and page 2 = 6, search narrows to 10, kind=Tools gives 15, and selecting a type narrows to 1 and updates the heading. **Caveat:** N1 sidebar counts are wrong while a selection is active (see above). |
| 4 | Blocked deletion | **PASS**: in the UI, Delete is disabled for a category with types, for the default type, and for an in-use type. Forced API DELETEs return 409 in all three cases. |
| 5 | Reset to default | **PASS**: the endpoint resolved back to `Endpoint / End-user compute · default` |
| 6 | Loading/toast behavior | **PASS**: Add category, Save classification (×4) and Delete type each show the `Button.isLoading` spinner and are disabled while pending, with Cancel disabled too. Success toasts appeared for create category, create type, assign, reset and delete, and the error toast appeared on a duplicate name. |
| 7 | Operator/viewer read-only | **PASS**: both roles see all 31 assets, have no Manage button, and get a read-only asset panel (message shown, no select, no Save). All 5 CMDB write APIs return **403** for both roles, and reads return 200. There were no console errors. |
| 8 | Second-org taxonomy/assets never appear | **PASS**: no `ORGB-SECRET` text appears on either Dev Org page or in the Manage panel. Org B sees only its own 2 assets and taxonomy. All 10 cross-org API attempts were denied or returned empty: org A filtering by org B's category or type → 0 rows; org A assigning org B's type → 404; reclassifying org B's agent → 404; renaming org B's category → 404; deleting org B's type → 404; creating a type under org B's category → 409; org B assigning org A's type → 404; org B deleting org A's category → 404; org B searching for org A's asset → 0 rows. |

Screenshots `01`–`12` are in the session scratchpad (`…/scratchpad/shots/`) and are not committed.

### 2a. Full check log

| # | Section 6 item | Check | Result | Evidence |
|---|---|---|---|---|
| 1 | 8 | Org A admin inventory shows no org-B taxonomy/assets (page 1) | PASS | ORGB-SECRET occurrences=0 |
| 2 | 3 | Unfiltered total matches DB count for org A | PASS | UI="31 assets" DB=31 |
| 3 | 3 | Server pagination shows 2 pages at 25/page | PASS | Page 1 of 2 |
| 4 | 3 | Page 2 renders remaining rows; page1+page2 = total | PASS | page1=25 page2=6 |
| 5 | 8 | Org A admin inventory shows no org-B data (page 2) | PASS | page 2 scanned |
| 6 | 3 | Search narrows to the 10 seeded paging tools | PASS | 10 assets |
| 7 | 3 | Kind=Tools filter narrows to tools only | PASS | 15 assets (expected 15: 4 real + 1 + 10 fixtures) |
| 8 | 6 | Add category shows Button.isLoading spinner + disabled while pending; Cancel disabled | PASS | {"disabled":true,"spinner":1,"cancelDisabled":true} |
| 9 | 6 | Success toast after create category | PASS | "Category created." |
| 10 | 1 | New category listed | PASS | category row present |
| 11 | 6 | Duplicate normalized category name → error toast | PASS | "a classification with that normalized name already exists" |
| 12 | 1 | Create endpoint type "B196 Verify EP Type" | PASS | toast "Type created." + row present |
| 13 | 1 | Create agent type "B196 Verify Agent Type" | FAIL | toast "Type created." + row present |
| 14 | 1 | Create tool type "B196 Verify Tool Type" | FAIL | toast "Type created." + row present |
| 15 | 5 | UI: Delete disabled for category that still has types | PASS | category has 3 types |
| 16 | 5 | UI: Delete disabled for the default Endpoint type | PASS | is_default type |
| 17 | 2 | Assign Endpoint → B196 Verify EP Type (explicit) | PASS | cell="B196 Verify EP Type B196 Verify Cat · explicit" |
| 18 | 6 | Save classification (Endpoint) spinner+disabled while pending; Cancel disabled; success toast | PASS | {"disabled":true,"spinner":1,"cancelDisabled":true,"toast":true} |
| 19 | 2 | Assign Agent → B196 Verify Agent Type (explicit) | PASS | cell="B196 Verify Agent Type B196 Verify Cat · explicit" |
| 20 | 6 | Save classification (Agent) spinner+disabled while pending; Cancel disabled; success toast | PASS | {"disabled":true,"spinner":1,"cancelDisabled":true,"toast":true} |
| 21 | 2 | Assign Tool → B196 Verify Tool Type (explicit) | PASS | cell="B196 Verify Tool Type B196 Verify Cat · explicit" |
| 22 | 6 | Save classification (Tool) spinner+disabled while pending; Cancel disabled; success toast | PASS | {"disabled":true,"spinner":1,"cancelDisabled":true,"toast":true} |
| 23 | 2 | Endpoint assignment dropdown offers endpoint types only | PASS | Use Endpoint default / End-user compute / Endpoint (default) / B196 Verify Cat / B196 Verify EP Type |
| 24 | 3 | Selecting a type filters table to that type and sets heading | PASS | total="1 assets" headingMatches=1 |
| 25 | N1 | Sidebar counts stay truthful while a type is selected (code-review N1) | FAIL | "All assets"→"All assets 1", "AI systems"→"AI systems 0" (real org has 10 agents) |
| 26 | 5 | UI: Delete disabled for a type with assigned assets | PASS | B196 Verify EP Type Endpoint · B196 Verify Cat · 1 assets Edit Delete |
| 27 | 5 | API: forced DELETE of category-with-types rejected | PASS | 409 {"code":"conflict","message":"classification is still in use or its parent does not exist"} |
| 28 | 5 | API: forced DELETE of in-use type rejected | PASS | 409 {"code":"conflict","message":"classification is still in use or its parent does not exist"} |
| 29 | 5 | API: forced DELETE of default type rejected | PASS | 409 {"code":"conflict","message":"the default type cannot be deleted"} |
| 30 | 2 | API: assigning an agent type to an endpoint is rejected | PASS | 404 {"code":"not_found","message":"asset or classification not found"} |
| 31 | 4 | Reset endpoint to default → resolves to default type, source=default | PASS | cell="Endpoint End-user compute · default" |
| 32 | 6 | Reset shows spinner while pending and reset toast | PASS | {"disabled":true,"spinner":1} |
| 33 | console | Org A admin session: zero console/page errors | FAIL | ["Failed to load resource: the server responded with a status of 409 (Conflict)"] |
| 34 | 7 | operator: can read the full inventory | PASS | 31 assets |
| 35 | 7 | operator: no Manage button | PASS | Manage count=0 |
| 36 | 7 | operator: asset panel is read-only (message shown, no select, no Save) | PASS | {"ro":1,"saveBtn":0,"sel":0} |
| 37 | 7 | operator: every CMDB write via API returns 403 | PASS | 403,403,403,403,403 |
| 38 | 7 | operator: API read allowed | PASS | 200 |
| 39 | console | operator session: zero console/page errors | PASS | [] |
| 40 | 7 | viewer: can read the full inventory | PASS | 31 assets |
| 41 | 7 | viewer: no Manage button | PASS | Manage count=0 |
| 42 | 7 | viewer: asset panel is read-only (message shown, no select, no Save) | PASS | {"ro":1,"saveBtn":0,"sel":0} |
| 43 | 7 | viewer: every CMDB write via API returns 403 | PASS | 403,403,403,403,403 |
| 44 | 7 | viewer: API read allowed | PASS | 200 |
| 45 | console | viewer session: zero console/page errors | PASS | [] |
| 46 | 8 | Org B admin sees own taxonomy/assets | PASS | ORGB-SECRET-CAT + ORGB agent visible |
| 47 | 8 | Org B admin sees none of org A taxonomy/assets | PASS | total="2 assets" |
| 48 | 8 | Org B Manage panel lists no org A categories/types | PASS | scanned Manage panel |
| 49 | 8 | API: org A filtering by org B category returns nothing | PASS | 200 total=0 |
| 50 | 8 | API: org A filtering by org B type returns nothing | PASS | 200 total=0 |
| 51 | 8 | API: org A cannot assign org B type to own agent | PASS | 404 {"code":"not_found","message":"asset or classification not found"} |
| 52 | 8 | API: org A cannot reclassify org B agent | PASS | 404 {"code":"not_found","message":"asset or classification not found"} |
| 53 | 8 | API: org A cannot rename org B category | PASS | 404 |
| 54 | 8 | API: org A cannot delete org B type | PASS | 404 |
| 55 | 8 | API: org A cannot create a type under org B category | PASS | 409 {"code":"conflict","message":"classification is still in use or its parent does not exist"} |
| 56 | 8 | API: org B cannot assign org A type | PASS | 404 |
| 57 | 8 | API: org B cannot delete org A category | PASS | 404 |
| 58 | 8 | API: org B search cannot find org A asset | PASS | 200 total=0 |
| 59 | console | Org B session: zero console/page errors | PASS | [] |

## 3. Independent security review (this session), verbatim

> ## Security review: B-196 CMDB Increment 2 Brief 1 (commit 6924729)
>
> I reviewed without changing anything: no file edits, no commits, no database writes. My only contact with the live shared Postgres (`eaim-postgres-1`) was two read-only catalog/SELECT queries.
>
> **Verdict: no High or Medium findings.** Tenant isolation, admin-only RBAC and SQL-injection resistance all hold up when traced through the real code paths. There are three Low findings and a few informational notes.
>
> ---
>
> ### Findings
>
> **L-1 (Low): the endpoint-classification write has no Discovery license check**
> - Where: `eami-api/internal/api/cmdb.go:347-393` (`SetCMDBAssetClassification`), routed at `router.go:275`.
> - The problem: reads apply the Discovery license. `ListCMDBAssets` excludes endpoints when the org is unlicensed and returns 403 for `kind=endpoint` (cmdb.go:134-143). The PATCH route checks the license for no kind at all. An admin in an unlicensed org can send `PATCH /v1/cmdb/assets/endpoint/{id}/classification`. That changes the endpoint row's `ci_type_id`, and the 200-vs-404 response confirms whether the endpoint ID exists within their own org.
> - Impact: small. It stays inside one org, needs the admin role, and the response returns only taxonomy names, never endpoint data. It still conflicts with Part A §8: "Discovery licensing still gates endpoint inventory." There is a precedent for the gap: the existing `LinkEndpointAgent` write (router.go:327) is also ungated.
> - Fix: in the handler, when `kind == "endpoint" && !s.discoveryLicensed(r, uc.OrgID)`, return 403 `module_not_licensed`, mirroring cmdb.go:140-143.
>
> **L-2 (Low): a very large `page` overflows the offset and returns 500**
> - Where: `cmdb.go:144-146`, using `pagination()` in `approvals.go:402-415`.
> - The problem: `page` has no upper bound because `strconv.Atoi` accepts values up to 2^63-1. `(page-1)*perPage` then overflows to a negative number. Postgres rejects a negative OFFSET, and the client gets a 500.
> - Example: `GET /v1/cmdb/assets?page=9223372036854775807&per_page=100`.
> - Impact: only a noisy 500 and a wasted count query. It is not a real DoS, because `per_page` is capped at 100 and `q` is capped at 200 characters (cmdb.go:76). This is the shared helper's behavior, so it is not new to CMDB.
> - Fix: clamp `page` (for example to 1e6) or compute the offset with an overflow check. Best done once in `pagination()`.
>
> **L-3 (Low, integrity): Go and SQL trim names differently, so near-duplicate names are possible**
> - Where: `cmdb.go:174` uses `strings.TrimSpace` (all Unicode whitespace). The migration uses `btrim(NEW.name)` (ASCII spaces only) at `000024_cmdb_classification.up.sql:41-42`.
> - The problem: a name like `"Connector\t"` passes the Go check. It is stored with its trailing tab, and its normalized name is `"connector "` (trailing space). That differs from the seeded `"connector"`, so the `(org_id, normalized_name)` uniqueness check does not catch it. Zero-width and confusable characters also get through.
> - Impact: admin-only and confined to one org, so the only effect is visually duplicate categories or types.
> - Fix: in the trigger, use `regexp_replace(NEW.name, '^\s+|\s+$', '', 'g')`, or send `strings.TrimSpace(name)` to the store.
>
> ### Informational (no action required for this brief)
>
> - **Raw database messages reach clients:** `cmdbWriteError` (cmdb.go:203-205) returns `pe.Message` verbatim for SQLSTATE 23514. Every current 23514 source is a message written in this migration, and the one that includes an org UUID only ever carries the caller's own org, so nothing leaks across tenants today. A future CHECK constraint would expose its constraint and table names, so an allow-listed message map would be safer.
> - **No lasting audit record:** taxonomy and classification changes are recorded only through `slog.Info` (cmdb.go:222, 242, 257, 290, 319, 343, 391). Those log lines contain IDs only, with no names or tenant content, which is fine. But Part A §8 asks for writes to go through "the existing administrative event/audit convention." None exists for admin writes (users, settings and license don't log at all), so this is consistent with the codebase, not a regression.
> - **Tool-call audit table not a fit:** `audit_log` is the gateway tool-call audit and isn't suitable here. A real admin audit trail is a separate gap for the backlog.
> - **Mismatch reported as 404:** in the default-versus-delete race, `DeleteCIType` (cmdb.sql.go:198) re-checks `NOT is_default` inside the statement, so it is safe. If another request makes the type the default between the handler's `GetCIType` and the DELETE, the client gets a misleading 404 instead of 409. This is cosmetic.
> - **Full table scans:** each `GET /v1/cmdb/assets` runs the three-table UNION three times (total count, page, per-type counts), with no index on the `ci_type_id` columns. That is fine at current scale; revisit it for large fleets.
> - **`sort_order` has no bounds** in the handler or OpenAPI. A value above 2^31 fails pgx encoding and returns 500 instead of 400.
> - **Live-DB schema drift (not security):** the shared Postgres is at `schema_migrations` version 24, not dirty. It has an extra constraint, `ci_types_org_id_asset_kind_normalized_name_key UNIQUE (org_id, asset_kind, normalized_name)`, that is not in the committed 000024 or in `schema.sql`. That suggests the shared stack was migrated with an earlier draft of 000024. It is harmless because the stricter `(org_id, normalized_name)` constraint also exists, but the shared stack does not exactly match the committed migration.
> - **Out of scope and codebase-wide:** the role is taken from the JWT and not re-checked against the database. A demoted admin keeps CMDB write access until their token expires, the same as for every other admin route.
>
> ---
>
> ### What I checked and found clean
>
> **1. Tenant isolation: clean**
> - **org_id source:** it comes only from the JWT (`claimsFromContext`, middleware.go:36-71). No handler reads org_id from the body, path or query.
> - **Every query is org-scoped:**
>   - `ListCICategories` and `ListCITypes` filter by `org_id=$1`.
>   - `GetCIType`, `UpdateCICategory` and `DeleteCICategory` use `WHERE id=$1 AND org_id=$2`.
>   - `UpdateCIType` and `DeleteCIType` also add `asset_kind` / `NOT is_default`.
>   - Asset-type updates use `WHERE id=$1 AND org_id=$2`.
>   - Every branch of the asset UNION filters `org_id=$1`, and the workspace, `ci_types` and `ci_categories` joins are all constrained to that org (cmdb.sql.go:205-257).
> - **Cross-org IDs supplied in bodies:** `category_id` in a type create or update is protected by the composite foreign key `(category_id, org_id)`. A foreign-org ID and a nonexistent ID give the identical 409 "parent does not exist", so the response is not an existence oracle.
> - **Cross-org ci_type_id in an assignment:** it is checked three ways — a pre-check (`id AND org_id AND asset_kind`, which returns the same 404 as not-found), the composite FKs on endpoints, gateway agents and gateway tools, and the `check_asset_ci_type_kind` trigger.
> - **Cross-org asset IDs** return 0 rows and a 404 that doesn't reveal existence, confirmed by the test at cmdb_pg_test.go (the `otherToken` case).
> - **Filters:** a cross-org `category_id`, `type_id` or `workspace_id` just matches nothing (an empty result, not an error).
> - **Mutation triggers:** `org_id` and `asset_kind` on `ci_types` can't be changed, enforced both by `keep_ci_type_scope_immutable` and by the handler check (cmdb.go:310-313).
>
> **2. RBAC: clean**
> - All 7 write routes sit inside the `requireRole("admin")` group (router.go:269-275). Operator, viewer, approver and platform_admin all get 403, and there are tests for operator and viewer.
> - Workspace roles never reach these routes, because workspace roles live in `workspace_memberships`, not in the JWT, and these routes don't use `requireWorkspaceRole`.
> - Reads are admin, operator and viewer with `viewerReadOnly` (router.go:384-385).
> - **Workspace scoping of reads:** no org-level list in the codebase filters by workspace membership (`/v1/gateway/agents`, `/v1/endpoints` and so on are org-wide). The CMDB inventory is consistent with that and with Part A §5 ("Admin mode, org-wide"). The `workspace_id` filter narrows results and never widens access.
> - **UI:** the page hides editing controls for non-admins (AssetsPage.tsx:22, 52, 71). The server enforces access regardless.
>
> **3. SQL injection: clean**
> - `cmdbFilteredSQL` (cmdb.sql.go:230-258) uses `fmt.Sprintf` only to insert `$N` placeholder numbers. Every value is bound as a parameter, including `q`, which is concatenated inside SQL as `'%' || $N || '%'`.
> - `kind` is allow-listed before use, and `assetKind` goes through a fixed `map[string]string` lookup of static SQL (cmdb.sql.go:306-314), never string interpolation.
> - LIMIT and OFFSET are bound parameters. There is no user-controlled sort.
> - The injection test `kind=endpoints%3BDELETE` returns 400.
> - Minor, not injection: `%` and `_` in `q` act as ILIKE wildcards. That only affects search semantics.
>
> **4. Integrity and concurrency: clean apart from L-3 and the cosmetic 404**
> - **Default replacement:** both paths take `pg_advisory_xact_lock(org:kind)` inside a transaction (cmdb.sql.go:151-170, 174-195). There is also the partial unique index `ci_types_one_default_per_kind` and the `DEFERRABLE INITIALLY DEFERRED` exactly-one-default constraint trigger. No path outside the lock changes `is_default`: `UpdateCIType` with `is_default=false` keeps the existing value, and `DeleteCIType` excludes the default.
> - **Concurrent default change versus delete:** in either order, the result is a rollback or a 404, never zero defaults.
> - **Asset kind mismatch** is rejected by the pre-check, which filters on `asset_kind`, and by a BEFORE trigger on all three asset tables.
> - **ON DELETE:** deleting a category is RESTRICTed if it has types (409), and deleting a type is RESTRICTed if assets use it (409). Deleting an org cascades. I checked read-only that the RESTRICT-within-cascade ordering works in practice: 0 test orgs with `cmdb-%` slugs remain on the shared database after tests that created custom categories, types and explicit assignments. `require_ci_default` skips its check once the org row is gone.
> - **Seeding** uses deterministic `uuid_generate_v5` IDs, `ON CONFLICT DO NOTHING`, and an AFTER INSERT trigger on `orgs`. That makes it idempotent and covers future orgs.
> - The down migration drops objects in dependency order, with nothing destructive outside CMDB objects.
>
> **5. Input bounds: acceptable**
> - **Enforced:** `per_page` is capped at 100, `q` at 200 characters, `name` at 120 bytes and `description` at 500 bytes. UUIDs are parsed strictly.
> - **Request body size:** CMDB decoding has no `MaxBytesReader`, but the same is true of every sibling handler except `openapi_discover.go`, and the writes are admin-only. It's consistent, not new exposure. See L-2 for `page`.
>
> **6. Information disclosure: clean**
> - 500 responses carry generic messages, and response types hide `OrgID` (`json:"-"`).
> - Log lines emit IDs only.
> - `discoveryLicensed` logs only org_id and the error on a database failure.
> - The UI renders only through React (no `dangerouslySetInnerHTML`), and all calls use the generated client or `apiFetch`.
>
> **7. License gating: consistent apart from L-1**
> - `discoveryLicensed` (cmdb.go:46-56) repeats `requireModuleLicensed`'s logic exactly, including checking that the license was issued for this org (`claims.OrgID() == orgID`), and it denies by default.
> - Endpoint rows and endpoint counts are excluded from both read handlers when the org is unlicensed.
> - Agents and tools remain visible without a license, matching `/v1/gateway/agents` and `/v1/gateway/tools`, which are also ungated.
>
> ### Files reviewed
> - `C:\AI\EAIM\eaim\schema\migrations-v2\000024_cmdb_classification.up.sql` and `.down.sql`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\cmdb.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\store\cmdb.sql.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\router.go` (lines 230-470)
> - `C:\AI\EAIM\eaim\eami-api\internal\api\middleware.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\approvals.go` (`pagination`)
> - `C:\AI\EAIM\eaim\eami-api\internal\api\cmdb_pg_test.go`
> - `C:\AI\EAIM\eaim\eami-ui\src\hooks\useCMDB.ts`
> - `C:\AI\EAIM\eaim\eami-ui\src\pages\cmdb\AssetsPage.tsx`
> - `C:\AI\EAIM\eaim\api\openapi.yaml` (the CMDB diff)
> - `C:\AI\EAIM\eaim\B-196_CMDB_INCREMENT_2_PART_A.md` (§5 and §8)

## 4. Independent post-fix code review (this session), verbatim

> I reviewed commit 6924729 as it was committed; the working tree matches HEAD for every file in scope. Four of the five earlier findings are fixed. Finding 3 is only partly fixed and now causes a different counts bug. Two of the earlier coverage gaps are still open, and BUILT.md claims test coverage that doesn't exist.
>
> ## The five earlier findings
>
> **1. Org-wide type-name uniqueness (High): fixed.**
> - `ci_types` now has `UNIQUE (org_id, normalized_name)` at `schema/migrations-v2/000024_cmdb_classification.up.sql:30` and in `schema.sql`. Uniqueness is per org, not per asset kind.
> - The seed's `ON CONFLICT (org_id, normalized_name)` (up.sql:153) matches that constraint.
> - `migrationtest/cmdb_classification_test.go:76-79` checks it: inserting agent type `'  LAPTOP  '` after endpoint type `'Laptop'` must fail.
>
> **2. Changing `ci_types.org_id` to bypass the one-default trigger (Medium): fixed.**
> - `keep_ci_type_scope_immutable()` rejects any change to `org_id` or `asset_kind` (up.sql:61-75, mirrored in `schema.sql`). The `BEFORE UPDATE OF asset_kind, org_id` trigger fires whenever either column is in the SET list, so there is no way around it.
> - The test for it is weak (`cmdb_classification_test.go:111-114`). It changes `org_id` alone, which the `(category_id, org_id)` composite foreign key rejects even without the trigger. `assertConstraintError` also accepts any Postgres error. The real bypass, `SET org_id=B, category_id=<a category in B>`, is not tested. See T1.
>
> **3. Counts ignored the filtered counts from `/v1/cmdb/assets` (Medium): partly fixed, with a new problem.**
> - `AssetsPage.tsx:31` now builds `filteredCountByType` from `assets.data.counts`.
> - But the server computes those counts with the navigation selection applied (`cmdb.go:155` calls `CountCMDBAssetsByType(r.Context(), f)` with `CategoryID`, `TypeID` and `Kind` set). See N1.
>
> **4. Heading did not reflect the selected classification (Medium): fixed.**
> - `AssetsPage.tsx:34-38` works out the heading from the selected type, then category, then kind. It feeds the `AppTopBar` breadcrumb and a scoped subtitle.
> - `PageHeader` deliberately has no `title` prop (`PageHeader.tsx:4-15`), so the breadcrumb is the right place.
> - Small gap: when only a kind is selected, the subtitle stays generic.
>
> **5. Async buttons without `isLoading` (Low/Medium): fixed.**
> - Delete category and Delete type: `AssetsPage.tsx:88-89`, `isLoading={X.isPending && X.variables === id}`.
> - Retry: line 66, `isLoading={assets.isFetching}`.
> - Save/Add buttons and Save classification: lines 88, 89, 97.
> - Cancel and close buttons are disabled while anything is pending.
> - Toasts go through `useToast`.
>
> ## Earlier coverage-gap note
>
> | Gap | Status |
> |---|---|
> | Org-wide type duplication | Tested at the database level only (`cmdb_classification_test.go:76-79`); no API test for a cross-kind duplicate returning 409. Acceptable. |
> | Licensed endpoint inventory | Tested (`cmdb_pg_test.go:121-126`): `kind=endpoint` returns 403 unlicensed and 200 once licensed. |
> | Pagination beyond 200 | Tested (`cmdb_pg_test.go:127-148`): 202 endpoints, page 9 at 25 per page returns 2 rows and total 202. |
> | `classification_source` read-back after assign/reset | Tested (`cmdb_pg_test.go:165-192`): it reads `explicit` back through GET after assigning and `default` after resetting. |
>
> BUILT.md claims coverage that does not exist:
> - **"Migration upgrade/rollback":** nothing runs `m.Down()` or `Steps(-1)`. Neither `cmdb_classification_test.go` nor `migrate_test.go` has one.
> - **"Workspace-role authorization":** `cmdb_pg_test.go` has no `workspace_admin` or `workspace_member` token.
> - **"Filtered counts":** no test decodes or checks the `counts` field.
>
> ## New findings
>
> **N1 (Medium): navigation counts drop to zero once a category or type is selected.**
> - Where: `eami-api/internal/api/cmdb.go:155`, used at `eami-ui/src/pages/cmdb/AssetsPage.tsx:57,59,60`.
> - Scenario: click the "Endpoint" type in the sidebar. `category_id`, `type_id` and `kind=endpoint` are all sent, so `counts` only has that type's row. Every other type and category in the sidebar shows 0. "All assets" shows `total`, which is now just that type's count.
> - Why it's wrong: the sidebar reports real classifications as empty, so it can't be used to navigate.
> - Fix: compute the navigation counts from a copy of the filter with `CategoryID`, `TypeID` and `Kind` cleared, keeping workspace, search and license filters. Take "All assets" from the sum of those counts, not from `meta.total`.
> - Add an API test that checks `counts` while a `type_id` filter is set.
>
> **N2 (Low): endpoint classification can be written without a Discovery license.**
> - Where: `cmdb.go:347-393`. `SetCMDBAssetClassification` never calls `discoveryLicensed`.
> - Scenario: an unlicensed org admin can `PATCH /v1/cmdb/assets/endpoint/{id}/classification`. Every read path, by contrast, returns 403 or hides endpoints. The impact is limited: it is admin-only, stays within the org, and needs the endpoint ID.
> - Fix: when `kind == "endpoint" && !s.discoveryLicensed(r, uc.OrgID)`, call `writeError(w, 403, "module_not_licensed", …)`.
>
> **N3 (Low): unchecking "Make default" on the current default type silently does nothing.**
> - Where: `store/cmdb.sql.go:187` (`is_default=CASE WHEN $6 THEN TRUE ELSE is_default END`) and `AssetsPage.tsx:86-89`.
> - Scenario: edit the default type, untick the box and save. The server returns 200 with `is_default: true` still set, but the UI shows "Type updated."
> - Fix: either disable the checkbox when editing a type that is already the default, with a hint to promote another type instead, or return 409 when `is_default:false` is sent for the current default.
>
> **N4 (Low): search wildcards are not escaped.**
> - Where: `store/cmdb.sql.go:249-252`.
> - Scenario: `q=%` or `q=_` acts as an `ILIKE` wildcard, so searching for a literal `_` in a host name matches everything.
> - There is no injection risk; the value is passed as a parameter. Fix: escape `\`, `%` and `_` and add `ESCAPE '\'`.
>
> **N5 (Low): a very large `page` value causes a 500.**
> - Where: `cmdb.go:144-146`, via the shared `pagination()` helper at `approvals.go:402`.
> - Scenario: `page=9223372036854775807` overflows `(page-1)*perPage` into a negative `OFFSET`. Postgres errors and the handler returns 500 instead of 400.
> - This is the helper's existing behaviour, not new to this commit. Cap `page` or reject overflow.
>
> **N6 (Low, UI): small UI issues.**
> - `AssetsPage.tsx:64`: the Discovery-license banner shows briefly while classifications are loading, because `!undefined` is true. Gate it on `classifications.data`.
> - `AssetsPage.tsx:65`: the Endpoints option stays selectable for unlicensed orgs. Choosing it gives a 403 error panel.
> - `AssetsPage.tsx:39`: page is reset in an effect, so one request goes out with the old page and the new filters first.
> - The search box has no debounce, so each keystroke sends a request.
>
> **T1 (Low, test quality): several invariant tests would pass even without the protection they claim to test.**
> - `assertConstraintError` accepts any `PgError`.
> - The org-immutability case fails on the composite foreign key regardless of the trigger.
> - The "second default" insert is stopped by the partial unique index, not the deferred trigger.
> - Fix: assert specific SQLSTATEs (23505, 23503, 23514). Add a case that sets `org_id` and `category_id` together.
> - Not tested at all: deleting an org that still has an explicitly classified asset. `ON DELETE RESTRICT` sits alongside cascades from `orgs`. My reasoning is that Postgres queues the cascaded foreign-key checks until after the statement, so this should succeed, but no test runs it. `seedTestOrg`'s cleanup ignores the DELETE error, so a failure there would leak test orgs silently.
>
> **T2 (Low): the down migration is never run.** Details under "Down migration" below.
>
> ## Clean
>
> - **Up migration:**
>   - Composite `(id, org_id)` foreign keys stop cross-tenant links between categories, types and assets.
>   - Triggers enforce that an asset's type matches its kind and org, including when `org_id` changes.
>   - Name normalization also raises 23514 when a name is empty.
>   - The one-default rule is enforced twice: a partial unique index plus a deferred trigger. That trigger skips the check when the org itself has been deleted.
>   - The seed is deterministic and idempotent, and existing orgs are backfilled.
> - **Schema parity:** `schema.sql` matches the migration.
> - **Down migration:** it truly reverses the up migration. It drops the org trigger and seed functions, then the asset triggers and their function, then the three `ci_type_id` columns (which removes the foreign keys), then both tables (which removes their triggers and indexes), then the remaining three functions. Nothing is left behind. It inevitably loses explicit assignments. It has not been executed (T2).
> - **Handlers:** every error path uses `writeError`, with no panics.
>   - Postgres errors are mapped as 23505, 23503 and 23514 to 409, `ErrNoRows` to 404, anything else to 500.
>   - Query parameters are validated: kind allow-list, UUIDs, `q` of 200 characters or fewer.
>   - Unlicensed orgs have endpoints excluded, and `kind=endpoint` returns 403.
>   - Writes are admin-only in `router.go`. Reads allow admin, operator and viewer, the same as `ListAgents`.
>   - The default type can't be deleted, checked in both the handler and the SQL.
>   - Changing `asset_kind` is rejected with 400.
> - **Store:**
>   - All SQL is parameterized, with the WHERE clause built from fixed fragments.
>   - The count and list queries use the same `filtered` CTE, so total and rows agree.
>   - Ordering is stable (`lower(name), asset_kind, id`).
>   - LIMIT/OFFSET parameter numbering is correct.
>   - Switching the default runs in a transaction under a per-org, per-kind advisory lock and unsets the old default before setting the new one, so the partial unique index is never violated.
>   - The `SetCMDBAssetType` pre-check race is backstopped by the trigger.
> - **React Query:** every CMDB mutation invalidates both `cmdb-classifications` and `cmdb-assets`, and the asset panel closes on success.
>   - The `['workspaces']` key doesn't collide with `useWorkspaces.ts`, whose keys are all `['workspaces', x, …]` and have a matching data shape.
>   - Minor: nothing invalidates `['workspaces']` when a workspace is created or deleted, so the dropdown can be stale until refetch.
> - **Test pool lifecycle:** `cmdb_pg_test.go` never creates a pool. It uses `newWorkspaceTestEnv` (`workspaces_pg_test.go:63`), which registers `t.Cleanup(func(){ pool.Close() })` before any seed cleanups. There is no `defer pool.Close()`. The migration test closes its connection with `t.Cleanup` against a throwaway database.
>
> Priority fix: N1. Then correct BUILT.md's coverage claims (rollback, workspace roles, filtered counts) or add those tests. N2 is a one-line fix.

## 5. Fixture cleanup — proof

I deleted everything in a single transaction: org A's fixture endpoint, agent and 11 tools, its 3 `B196 Verify` types and the category, 4 users, and org B, with org B's own taxonomy and assets removed by cascade. The `psql` output was `DELETE 1 / 1 / 11 / 3 / 1 / 4 / 1`, then `COMMIT`.

I then compared the database against the snapshot taken before any fixture was created. `diff before.txt after.txt` reported **identical**:

```
orgs|6  users|13  ci_categories|18  ci_types|18  endpoints|5  agents|12  tools|4  ep_typed|1  ag_typed|0  tl_typed|0
0392716a-b1b6-4603-977d-a436ca802e4a|4142489b-…|Bhargav_tej|Endpoint|t   ← pre-existing explicit assignment, present before this session and left untouched
```

A residual scan for `b196verify`, `B196 Verify` and `ORGB-` across orgs, users, endpoints, agents, tools, categories and types found **0** rows.

## 6. Active-agent marker

Before this session, `CONTEXT.md` read `ACTIVE AGENT: none`, so the Codex session had cleared it correctly. This session set it to `Code — B-196 …` at start and reset it to `none` at close; the committed CONTEXT.md is the proof.

## 7. Open items — Brief 1 is not complete until these are resolved or explicitly waived

**Real B-196 Brief 1 findings (no new B-ID; they belong to this brief's "fix all real findings" requirement):**
1. **N1 (Medium, confirmed live):** navigation counts collapse when a category or type is selected. `cmdb.go:155` computes `counts` using the selected category, type and kind. Fix: compute them with those three cleared, keeping workspace, search and license.
2. **L-1/N2 (Low):** `SetCMDBAssetClassification` has no Discovery license check for `endpoint`.
3. **N3 (Low):** unticking "Make default" on the current default returns 200 without making any change.
4. **L-3 (Low):** Go `TrimSpace` and SQL `btrim` normalize differently, so near-duplicate names are possible.
5. **N4 (Low):** `%` and `_` are not escaped in `ILIKE` search.
6. **N6 (Low, UI):** the license banner flashes while data loads, the Endpoints option stays selectable when unlicensed, one request fires with a stale page, and search has no debounce.
7. **T1/T2 (test quality):** `assertConstraintError` accepts any error, the org-immutability test passes through the FK rather than the trigger, and the down migration is never executed.
8. **BUILT.md over-claims** coverage that no test provides: "migration upgrade/rollback" (no `Down()`/`Steps(-1)`), "workspace-role authorization" (no workspace-role token), and "filtered counts" (the `counts` field is never asserted). BUILT.md's 2026-09-26 entry has been corrected.

**Outside B-196. These need B-IDs, confirmed with the founder before minting:**
- `pagination()` (`approvals.go:402`) overflows on a huge `page` and returns 500. It is a shared helper and affects every paginated route.
- There is no durable admin audit trail for admin writes; CMDB, users and settings log only through `slog`.
- **Live shared-DB drift:** `ci_types_org_id_asset_kind_normalized_name_key UNIQUE (org_id, asset_kind, normalized_name)` exists on the shared Postgres but not in committed `000024` or `schema.sql`. It appears to be left over from an early draft of the migration. It is harmless because the stricter org-wide constraint also exists, but the shared stack does not match the committed schema.
