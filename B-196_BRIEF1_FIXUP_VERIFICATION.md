# B-196 Increment 2 Brief 1: Fix-up Pass Verification Record

Written 2026-09-26 by Claude Code. The founder scoped this pass after `B-196_BRIEF1_VERIFICATION.md`.

**Scope**
- **Mandatory:** N1, N2, and the BUILT.md coverage-claim corrections.
- **Also included:** N3, N4, L-3, T1, T2.
- **New B-IDs:** B-223 for the shared `pagination()` overflow fix. B-224 for the missing admin-write audit trail, which is QUEUED as a tracked item.
- **Operational:** reconcile the shared stack's schema drift before any live verification is trusted.
- **Deferred:** N6 and the informational notes.

**Checkability rule.** The founder asked that nothing be claimed to have "run" unless it can be checked independently. Every result below is quoted verbatim from its command output or report. You can re-check them this way:
- **Tests:** re-run the commands in §3. Every real-Postgres test here uses the repo's standard `POSTGRES_PASSWORD` convention; the value was injected from the Postgres container's environment and never printed.
- **Reviews:** both review subagents ran in Claude Code session `b3cde494-55e6-4fd7-b83d-5f55f606e27f`. Their full transcripts are stored with that session's subagent records under `~/.claude/projects/C--AI-EAIM-eaim/`.
- **Live state:** use the §1 and §6 SQL against the shared Postgres.
- **Raw logs:** these live in that session's scratchpad and are not committed; the relevant lines are quoted here. The files are `api_full_final.log`, `migrationtest_final.log`, `mutation.log`, `fixup_run_final.log`, `drift_semantic.txt`, `drift_after.txt`, `before.txt`, `after.txt` and `reconcile.sql`.

## 1. Shared-stack schema drift: reconciled first

**Method**
1. Created a throwaway database `drift_ref_1790420971` on the same server.
2. Migrated it from the committed `schema/migrations-v2` using the pinned compose `migrate/migrate:v4.19.1` image.
3. Ran `pg_dump --schema-only` on both the reference and the shared `eami` database, excluding TimescaleDB-internal schemas.
4. Normalized CRLF and compared the two dumps as multisets of per-object blocks, ignoring whitespace. The script is `blockdiff.js`.

**Drift found.** The earlier review knew about only the first of these two items. Before this fix:

```
ONLY IN SHARED (3):
  seed_default_ci_taxonomy(uuid) ... ON CONFLICT (org_id, asset_kind, normalized_name) DO NOTHING ...
  ci_types ci_types_org_id_asset_kind_normalized_name_key; Type: CONSTRAINT ... UNIQUE (org_id, asset_kind, normalized_name);
  audit_log; Type: ROW SECURITY ... \unrestrict <random per-dump nonce>
ONLY IN REF (2):
  seed_default_ci_taxonomy(uuid) ... ON CONFLICT (org_id, normalized_name) DO NOTHING ...
  audit_log; Type: ROW SECURITY ... \unrestrict <different random nonce>
```

The `audit_log` block differs only by pg_dump's random `\unrestrict` nonce. `keep_ci_type_scope_immutable()` differed only in whitespace (a one-line early-draft body).

**Why the second item matters.** The shared `seed_default_ci_taxonomy()` still targeted the extra constraint in its `ON CONFLICT`. Dropping that constraint alone would have left the function with no matching unique constraint for its `ON CONFLICT`. Every `INSERT INTO orgs` would then have failed, because the `trg_orgs_seed_default_ci_taxonomy` trigger calls it.

**Reconciliation**, applied as one transaction (`reconcile.sql`):
1. `BEGIN;`
2. `CREATE OR REPLACE` both functions, using committed `000024_cmdb_classification.up.sql` lines 61–71 and 131–155 verbatim.
3. `ALTER TABLE ci_types DROP CONSTRAINT ci_types_org_id_asset_kind_normalized_name_key;`
4. `COMMIT;`

psql output: `BEGIN / CREATE FUNCTION / CREATE FUNCTION / ALTER TABLE / COMMIT`. The pre-reconcile dump is kept as the rollback record (`dump_eami.sql`).

**After:**
```
blocks shared=466 ref=466
ONLY IN SHARED (1):  audit_log ... \unrestrict eWnqoY2g...   (nonce only)
ONLY IN REF (1):     audit_log ... \unrestrict bNLO6FgH...   (nonce only)
```

**Proof that org creation still seeds.** The probe ran in a transaction and was rolled back:

```
BEGIN / INSERT 0 1 / cats=3 / defaults=3 / ROLLBACK      → residual probe orgs=0
```

The reference database was dropped afterwards, and 0 `drift_ref_%` databases remain. The independent security review (§5b) separately confirmed the live `ci_*` objects against the migration. The six function bodies' MD5 hashes are identical. No SECURITY DEFINER or custom `search_path` was introduced. Every org × kind has exactly one default, 0 bad out of 18.

## 2. What changed

| Finding | Fix | Where |
|---|---|---|
| **N1** (Medium) | Navigation `counts` are computed from a copy of the filter with `CategoryID`/`TypeID`/`Kind` cleared. Workspace, search and license filters still apply. The UI's "All assets" count is the sum of those counts, not the filtered `meta.total`. | `cmdb.go` `ListCMDBAssets`; `AssetsPage.tsx` |
| **N2** (Low) | The endpoint classification write returns 403 `module_not_licensed` when the org is unlicensed. This check runs before body decoding, so a nonexistent ID gets the same 403 and the response reveals nothing about which endpoints exist. | `cmdb.go` `SetCMDBAssetClassification` |
| **N3** (Low) | UI only. The API's `is_default:false`-is-a-no-op semantics are kept deliberately: a default is replaced by promoting another type. When editing the current default, the "Make default" checkbox is disabled and a hint explains the promotion flow. | `AssetsPage.tsx` |
| **L-3** (Low) | `normalizeCMDBName` trims in place with Unicode-aware `strings.TrimSpace` before validation and storage, on all four name-write paths. | `cmdb.go` |
| **N4** (Low) | Search escapes `\ % _` in a single pass (`strings.NewReplacer`) and uses `ESCAPE E'\\'`, which works regardless of `standard_conforming_strings`. | `store/cmdb.sql.go` |
| **T1** | `assertPgError` requires the exact SQLSTATE plus the constraint name or message. New cases:<br>• `org_id` and `category_id` moved together, where only the trigger can stop it<br>• zero defaults at COMMIT, which exercises the deferred trigger rather than the partial index<br>• deleting an org whose assets are explicitly classified<br>The version is pinned to 24 instead of `latest-1`. | `schema/migrationtest/cmdb_classification_test.go` |
| **T2** | A new test runs the real `000024…down.sql` with explicit assignments present. It then checks four things:<br>• the version is 23 and not dirty<br>• the schema fingerprint is identical to the pre-up version-23 fingerprint<br>• inventory survives<br>• re-up works, and its fingerprint matches the first up<br>The fingerprint covers tables, columns (type, nullability, default), constraint definitions, index definitions, trigger definitions, and function signatures plus body MD5. | same file |
| **B-223** | `pagination()` bounds `page` to `min(page, 1_000_000, MaxInt32/perPage)`, so the offset stays int32-safe for any `maxPerPage`. Two callers were affected: `alerts.go` and `users.go` cast the offset to `int32`, and `approvals.go` does too. | `approvals.go`; `pagination_internal_test.go` |
| Coverage claims | New real-DB tests make BUILT.md's former over-claims true:<br>• filtered `counts` are asserted<br>• workspace-role tokens are tested (`workspace_admin`/`workspace_member` × operator/viewer → 403, and nothing reaches the DB)<br>• the down migration runs | `cmdb_fixup_pg_test.go`, migration tests |

## 3. Automated verification (final code)

**Commands**

```
cd eami-api && go build ./... && go vet ./internal/api ./internal/store   → vet-ok
cd eami-api && POSTGRES_PASSWORD=… go test -count=1 -v ./...              → exit 0
cd schema/migrationtest && GOWORK=off go vet ./... && POSTGRES_PASSWORD=… GOWORK=off go test -count=1 -v ./...  → exit 0
cd eami-ui && npx tsc --noEmit → exit 0 ; npx vite build → exit 0 ; git diff --check → clean ; gofmt → clean on all touched/new Go files
```

**API module** (`api_full_final.log`): every package reports `ok`, with `PASS=474 FAIL=0 SKIP=0`. The CMDB and pagination lines:

```
--- PASS: TestPagination_ClampsPageAndPerPage
--- PASS: TestPagination_WorstCaseOffsetFitsInt32ForAnyMaxPerPage
--- PASS: TestCMDBFixup_NavigationCountsIgnoreSelection_RealDB
--- PASS: TestCMDBFixup_EndpointClassificationWriteRequiresDiscoveryLicense_RealDB
--- PASS: TestCMDBFixup_NameTrimMatchesNormalization_RealDB
--- PASS: TestCMDBFixup_SearchEscapesLikeWildcards_RealDB
--- PASS: TestCMDBFixup_DefaultIsReplacedNotUnset_RealDB
--- PASS: TestCMDBFixup_WorkspaceAdminCannotWriteCMDB_RealDB
--- PASS: TestPaginationOverflow_ReturnsEmptyPageNot500_RealDB
--- PASS: TestCMDBClassification_RBACIsolationLicensingAndFilters_RealDB
--- PASS: TestCMDBClassification_AdminCRUDAndAtomicDefault_RealDB
```

The log contains four `panic(` stack traces. They come from the alerting package's deliberate panic-recovery tests (`TestEvaluateRules_PanicInOneRule_OthersStillEvaluate` and similar, via `recover.go:20`). All of those tests pass.

**Migration tests** (`migrationtest_final.log`), run against throwaway databases:

```
--- PASS: TestCMDBClassificationMigration_RealPostgres
--- PASS: TestCMDBClassificationMigration_DownRestoresPreviousSchemaAndReUpWorks_RealPostgres
--- PASS: TestMigrate_ExistingSeededDatabase_AppliesNewMigrationWithoutDataLoss
--- PASS: TestMigrate_FreshPathAndIncrementalPath_ProduceIdenticalFinalSchema
--- PASS: TestMigrate_RunTwice_NoErrorNoDuplication
--- PASS: TestMigrate_FreshDatabase_MatchesExpectedSchema
ok  github.com/eami/migrationtest
```

**Honest limit on N3.** Only its API half has a test, and that test pins unchanged behaviour. The actual fix is UI-only, and `eami-ui` has no UI test framework. N3 is verified by the live Playwright checks in §4, not by an automated regression test.

### 3a. Mutation check: each test fails when its fix is reverted

I re-broke each fix, ran the targeted test, and restored the file. Backups were used for uncommitted sources, and `git checkout` for the committed migration files. `cmp` or `git status` confirmed each restore. Verbatim from `mutation.log`:

```
=== MUTATION N1: counts computed from f instead of nav
--- FAIL: TestCMDBFixup_NavigationCountsIgnoreSelection_RealDB
    REGRESSION N1: counts with a type selected=[{... Count:3}], want agent 3 and tool 2
=== MUTATION N2: license gate disabled
--- FAIL: TestCMDBFixup_EndpointClassificationWriteRequiresDiscoveryLicense_RealDB
    unlicensed endpoint classification write = 200 want 403
=== MUTATION L3: TrimSpace disabled
--- FAIL: TestCMDBFixup_NameTrimMatchesNormalization_RealDB
    type "Connector\t" vs seeded "Connector" = 201 want 409 ... "name":"Connector\t"
=== MUTATION N4: escaping removed
--- FAIL: TestCMDBFixup_SearchEscapesLikeWildcards_RealDB
    q="_" matched [back\slash pct100%tool plainname under_score] (total 4), want [under_score]
=== MUTATION B223: page = n
--- FAIL: TestPagination_ClampsPageAndPerPage
=== MUTATION B223-realDB: page = n
--- FAIL: TestPaginationOverflow_ReturnsEmptyPageNot500_RealDB
    GET /v1/cmdb/assets?page=9223372036854775807&per_page=100 = 500: {"code":"internal_error","message":"failed to list CMDB assets"}
=== MUTATION B223-int32: drop math.MaxInt32/perPage bound
--- FAIL: TestPagination_WorstCaseOffsetFitsInt32ForAnyMaxPerPage
    maxPerPage=5000: page=1000000 offset=4999995000 does not fit int32
=== MUTATION T1: remove org_id immutability check from keep_ci_type_scope_immutable
--- FAIL: TestCMDBClassificationMigration_RealPostgres
    expected SQLSTATE 23514 matching "org_id is immutable", got 23503 constraint="ci_types_category_org_fk"
=== MUTATION T1b: drop the deferred require_ci_default constraint trigger
--- FAIL: TestCMDBClassificationMigration_RealPostgres
    expected PostgreSQL error 23514 (exactly one default CI type is required), got success
=== MUTATION T2: down leaves normalize_ci_name() behind
--- FAIL: ...DownRestoresPreviousSchemaAndReUpWorks_RealPostgres
    down did not restore the version-23 schema exactly:  extra:   fn:normalize_ci_name()
=== MUTATION T2b: down forgets to drop gateway_tools.ci_type_id
--- FAIL: ...DownRestoresPreviousSchemaAndReUpWorks_RealPostgres
    down (000024_cmdb_classification.down.sql): migration failed: cannot drop table ci_types because other objects depend on it
sources restored OK / migration files clean (no diff vs HEAD)
```

T1 is the reviewer's exact concern made concrete. With the trigger's org check removed, the org-only move fails on the foreign key (23503) instead. The old any-`PgError` assertion would have accepted that and passed.

## 4. Live acceptance on the rebuilt shared stack

**Environment**
- `docker compose build eami-api eami-ui` and `up -d --no-deps` were run from the final working tree. The API container started at 2026-09-26T11:32:16Z.
- Health checks: API `/health` 200, UI 200.
- The served `AssetsPage.tsx` module contains the new code (`navigationTotal` and the N3 hint).
- The browser was Playwright Chromium. The package came from a prior session's scratchpad and nothing was added to `eami-ui`'s manifest.

**Fixtures**, all with the prefix `b196fix`:
- a Dev Org admin
- a throwaway **unlicensed** org, with its own admin and endpoint
- a Dev Org endpoint
- two probe tools, `b196fix_under` and `b196fixXunder`

Fixture passwords were hashed in-database with `pgcrypto crypt(…, gen_salt('bf',12))`, for these new rows only. No existing credential was read. An earlier combined command that queried existing users' hash prefixes was denied by the permission classifier, and that path was not pursued.

**Result: 18 of 18 checks passed** (`fixup_run_final.log`, verbatim):

```
PASS [N1] API: type selected → table narrowed, counts still cover every type :: total=9 counts agent=9 tool=6 endpoint=6
PASS [N2] API: unlicensed org admin endpoint classification write → 403 module_not_licensed :: 403 {"code":"module_not_licensed",...} (licensed flag=false)
PASS [N2] API: unlicensed write to a nonexistent endpoint → same 403 (no existence oracle) :: 403
PASS [N2] API: licensed org admin endpoint write → 200 explicit, reset → 200 default :: 200/explicit then 200/default
PASS [L-3] API: "Connector<TAB>" type and "Integrations<NBSP>" category → 409 duplicates :: 409 ... / 409 ...
PASS [N4] API: q="b196fix_" matches only the literal underscore tool :: total=1 names=b196fix_under
PASS [B-223] API: CMDB page=MaxInt64 → 200, empty page, page clamped to 1000000 :: 200 rows=0 page=1000000 total=21
PASS [B-223] API: /v1/users page=MaxInt64 → 200 with an empty page :: 200 rows=0
PASS [B-223] API: /v1/alerts page=MaxInt64 → 200 with an empty page :: 200 rows=0
PASS [N1] UI: unselected sidebar counts match DB (21 / 6 / 9 / 6) :: {"all":"21","euc":"6","ai":"9","integ":"6"}
PASS [N1] UI: with "Governed agent" selected the table narrows to 9 but sidebar counts stay 21/6/9/6 :: {...,"table":"9 assets"}
PASS [N1] UI: with "Integrations" selected the table narrows to 6, sidebar unchanged :: {"all":"21","ai":"9","table":"6 assets"}
PASS [N4] UI: searching "b196fix_" lists only b196fix_under (not b196fixXunder) :: 1 assets rows=["b196fix_under"]
PASS [N1] UI: search narrows the sidebar counts (All assets = 1) :: All assets=1
PASS [N3] UI: editing the current default → checkbox checked + disabled, hint shown :: {"checked":true,"disabled":true,"hint":1}
PASS [N3] UI: editing a non-default type → checkbox unchecked + enabled, no hint :: {"checked":false,"disabled":false,"hint":0}
PASS [console] UI session: zero console/page errors :: []
PASS [cleanup] temporary type deleted via API :: create 201 delete 204
18 checks, 18 passed, 0 failed
```

**Earlier runs**
- The first run, before the review-driven hardening, also passed the same 18 checks. That was its second attempt: the first attempt crashed on a script defect, an ambiguous `Cancel` selector matched in both panel sections. That attempt had created the temporary type before crashing, and the rerun deleted it first (`removed leftover temp type: 204`).
- The API was then rebuilt with the final code, and the whole script was re-run. The results above are from that final run.

## 5. Independent reviews (verbatim)

Both reviews ran as read-only subagents on the working-tree diff; neither found a High or Medium issue. I then acted on these items:
- **`pagination()` int32 safety was enforced only by a comment** (both reviews): the bound is now `MaxInt32/perPage` inside `pagination()`. The test covers `maxPerPage` up to MaxInt32, and the mutation in §3a is caught.
- **The caller list in the `approvals.go` comment was incomplete:** fixed.
- **`ESCAPE '\'` depended on `standard_conforming_strings`:** now `ESCAPE E'\\'`.
- **Blind spots in the fingerprint:** it now includes defaults, index and trigger definitions, and function-body hashes.

Everything else is recorded in §7.

### 5a. Code review

> **Verdict: the fix-up is sound and can be committed. All eight findings are fixed in code, and nothing I found blocks the commit. N1 and B-223 carry Low-severity follow-ups, and a few comments and docs are inaccurate.** I made no edits. `go build ./...` and `go vet` passed for `eami-api/internal/api` and `internal/store` (test files included) and for `schema/migrationtest` (`GOWORK=off`). `npx tsc --noEmit` in `eami-ui` is clean. The `TestPagination_*` unit tests pass. I did not run the real-Postgres tests, because they create orgs and throwaway databases and you told me not to write to any database.
>
> **Low — B-223 is incomplete: other pagination paths still overflow.** Pagination done without `pagination()` still has no upper bound on `page` and still casts the offset to `int32`:
> - `audit.go:76-95`
> - `paste_events.go:83-119`
> - `gateway_episodes.go:245-258`
> - `parsePage`/`parseIntParam` in `reports.go:240-270` (used by `reports.go:120` and `discover.go:63`). `parseIntParam` wraps silently on overflow, with no error.
>
> Example: `GET /v1/audit?page=4294967297&per_page=100` gives an `int32` offset of 0 and returns page 1's rows labelled as page 4294967297. Other values produce a negative OFFSET and a 500. The original finding said the bug hit "every paginated route through the shared helper", but these routes never used the helper. Fix: clamp these too (reuse `maxPaginationPage`, or route them through `pagination()`), or narrow B-223's wording and log the rest as a follow-up.
>
> **Low — the `int32` safety depends on an unenforced assumption.** Files: `approvals.go:402-406` and `pagination_internal_test.go:39-45`. The clamp only keeps offsets inside `int32` while every caller's `maxPerPage` is at most about 2147. A future `pagination(..., 5000)` caller would wrap again: 999,999 × 5000 ≈ 5e9. `TestPagination_WorstCaseOffsetFitsInt32` passes a hard-coded 500, so it proves nothing about real callers. It is close to tautological. Fix: clamp the offset itself (return it, capped at `math.MaxInt32`), or cap `maxPerPage` inside `pagination()`.
>
> **Low (comment inaccuracy) — `approvals.go:402-405`.** The comment names "the int32 offsets alerts.go and users.go pass". `approvals.go:96` itself also casts to `int32`. The limit (500 is the largest `maxPerPage`) is right; the list of callers is incomplete.
>
> **Low — L-3 residual near-duplicates.** File: `cmdb.go:180-190`. Go's `TrimSpace` only fixes leading and trailing whitespace. Characters Go does not treat as space still get through at the ends, e.g. a trailing zero-width space: "Connector​" is not a duplicate of "Connector". Inside the name, the trigger's `regexp_replace('\s+')` may not match NBSP, depending on locale; I am not certain either way. So "Con nector" and "Con nector" may normalize differently. The fix is API-only; any other writer still goes through `btrim`. More robust fix (not required for this brief): do Unicode-aware trimming and collapsing in the `normalize_ci_name` trigger itself, via a new migration. Otherwise log it in NOTES.md. The length check now runs after trimming, which is correct. `len()` counts bytes while the message says "characters" (pre-existing, Info).
>
> **Low — N1: the contract doesn't document the new `counts` meaning.** `counts` now ignores the category, type and kind filters but honors workspace, search and license. `api/openapi.yaml:179` does not say so, and the new PATCH 403 `module_not_licensed` is not listed for `/v1/cmdb/assets/{assetKind}/{assetId}/classification` (`openapi.yaml:1209-1211`). `openapi.yaml` is Architect-owned, so this is a flag for them, not a Code edit. The UI "All assets" count (the sum of the navigation counts) is correct. It equals `meta.total` when nothing is selected, and it stays narrowed by search and workspace, consistent with the per-type counts.
>
> **Info — N3 has no regression test for the fix itself.** `TestCMDBFixup_DefaultIsReplacedNotUnset_RealDB` only pins API behavior that did not change, so it passes with or without the fix. The real fix is UI-only: the checkbox is disabled for the current default, with a hint. It has no automated coverage because there is no UI test framework. The test comment ("API half") is honest; the fix just shouldn't be reported as tested.
>
> **Info — T1: one case doesn't isolate the trigger.** At `cmdb_classification_test.go:225-228` ("Moved default" to `futureOrg`), the partial unique index `ci_types_one_default_per_kind` would also block that move. The assertion on the trigger message still pins the trigger, because BEFORE triggers run first. The isolating case is the earlier `Laptop` move at 221-224, and its comment is accurate. Harmless.
>
> **Info — blind spots in `schemaFingerprint`** (`cmdb_classification_test.go:367-388`). It fingerprints tables, columns (type and nullability), constraint definitions, index names, trigger names and function signatures. It does not cover column defaults and index definitions; trigger timing or events; function bodies; views, sequences, types, domains, extensions, RLS policies, grants, comments; non-public schemas. So a down migration that left a changed function body or index definition would go undetected. For 000024 this doesn't matter: the up only creates objects and does not replace any that already existed, and the down drops all of them. Adding `pg_get_indexdef`, `pg_get_triggerdef`, `md5(prosrc)` and `column_default` would close most of the gaps cheaply.
>
> **Info — B-223 is not yet in BACKLOG.md.** The code comments cite B-223, but `BACKLOG.md:2390` still reads "Next B-ID: B-223", so nothing has been minted. Your B-ID memory note says to confirm with the founder, and the verification doc says the same, before recording it and committing code that references it. N6 (UI flash, stale page, debounce) was not addressed; that is expected, since it was outside the fixes you listed.
>
> **Verified clean**
> - **Pool lifecycle:** every new real-Postgres test uses `newWorkspaceTestEnv`, which registers `t.Cleanup(pool.Close)` before any seed cleanup. There is no `defer pool.Close()`. The migration tests close a `pgx.Conn` via `t.Cleanup` against a throwaway database.
> - **Test data cleanup:** org, workspace and endpoint rows are cleaned up with `t.Cleanup`. Users, agents, tools, licenses and CMDB rows cascade from `orgs` (checked in migrations 001, 015 and 024). The migration test proves an org delete with explicitly classified assets succeeds.
> - **Error handling:** every handler path uses `writeError`, and the license check runs before the body is decoded, so it can't be used to probe whether an endpoint exists.
> - **N4 escaping:** a single-pass `strings.NewReplacer` avoids double escaping. `ESCAPE '\'` is correct with `standard_conforming_strings=on` (the default, which pgx requires). Backslash is already Postgres's default LIKE escape, so the clause is belt-and-braces; the Replacer is the real fix. `||` binds tighter than ILIKE, so the expression groups correctly.
> - **Test effectiveness:** each new test would fail without its fix. N1: tool count 0 when a type is selected, agent count 0 with workspace plus kind. N2: 200 instead of 403. L-3: `"Connector\t"` would get 201 instead of 409; I confirmed the NBSP literal is real bytes (C2 A0). N4: `q=_` matches all four seeded names. B-223: CMDB returns 500 or `page != 1000000`; users page 4294967297 wraps to offset 0 and returns the admin row, which is a deliberately good choice. T1: `assertPgError` now requires the exact SQLSTATE plus the constraint name or message; the codes 23505, 23503 and 23514 are right, and RESTRICT violations raise 23503. T2: runs the real 24.down, checks the version is clean, compares fingerprints before and after, checks inventory survives, and re-ups.
> - **Comments:** every other code comment I checked is accurate, including the `btrim` → "connector " explanation, the claim that the kind trigger runs before the composite FK, and the claim that the deferred trigger fires only at COMMIT.
>
> | Finding | Status | Evidence |
> |---|---|---|
> | N1 | Fixed | `cmdb.go:155-161` clears `CategoryID`, `TypeID` and `Kind` for the counts. The UI "All assets" count uses `navigationTotal` (`AssetsPage.tsx:33,59`). Test at `cmdb_fixup_pg_test.go` NavigationCounts. The OpenAPI description is still missing (Low). |
> | N2 | Fixed | `cmdb.go:370-374` returns 403 `module_not_licensed` for `kind=endpoint` before decoding or any DB write. Tested for a real and an unknown endpoint, and agent writes are still allowed. |
> | N3 | Fixed (UI only) | The checkbox is disabled with a hint when the type being edited is the default. The API no-op is kept deliberately and pinned by a test; the UI change itself has no test. |
> | L-3 | Fixed for the reported case | `normalizeCMDBName` trims in place before the length check, on all four write paths. Residual Unicode and internal-whitespace cases remain (Low). |
> | N4 | Fixed | Replacer plus `ESCAPE '\'` (`cmdb.sql.go:230,251-255`). Seven literal-match test cases. |
> | T1 | Fixed | `assertPgError` checks the exact code and the constraint name or message. It adds the zero-default (deferred) case, the `org_id` plus `category_id` move, and the org-delete cascade. |
> | T2 | Fixed | New down, re-up and up-to-latest test. The version is pinned to 24 instead of `latest-1`. Fingerprint gaps noted (Info). |
> | B-223 | Partially fixed | `pagination()` is clamped and tested (unit and real-DB). Five other hand-rolled pagination paths are still unbounded (Low). The `int32` safety relies on an unenforced `maxPerPage` limit. The B-ID is not yet in BACKLOG.md. |

**Disposition of the "B-223 not minted" note.** The founder explicitly directed that this pass mint B-IDs for this fix and for the admin audit trail, after confirming them free against BACKLOG.md directly. The check: `Next B-ID: B-223`. A grep of BACKLOG.md for any open item overlapping "pagination"/"page overflow"/"admin audit" found only the B-196 note that names these two as still needing IDs. B-223 and B-224 are minted in this commit.

### 5b. Security review

> **Verdict: no High or Medium findings.** L-1, L-2 and L-3 are fixed as claimed. The new counts query stays inside the caller's org. The ESCAPE change is injection-safe. The live database's `ci_*` objects now match committed migration 000024 exactly, and the reconcile left nothing unsafe behind. What remains is one Low finding and some informational notes; all of it pre-dates this diff or is hardening.
>
> This was read-only. I edited no files and wrote nothing to the database. My only database contact was SELECT and catalog queries through `docker exec eaim-postgres-1 psql -U eami_app`. I also ran `go vet` on `./internal/api` and `./internal/store`, which was clean, and the two pure unit tests in `pagination_internal_test.go`, which passed. I did not run any real-Postgres test.
>
> **Low: pagination paths that bypass the new clamp are still unbounded.** Pre-existing, not introduced by this diff. Where: `eami-api/internal/api/audit.go:78-92` (its own `strconv.Atoi` parse, then `int32((page-1)*perPage)`); `paste_events.go:85-119` (same pattern); `gateway_episodes.go:246-258` (an unbounded int offset forwarded to the gateway); `reports.go:240-270` (`parsePage`/`parseIntParam`, used by `ListEndpoints` at `reports.go:120-134` and `ListAgentEndpoints` at `discover.go:63-73`; `parseIntParam` has no overflow check at all, so a long digit string wraps silently). Scenario: `GET /v1/audit?page=9223372036854775807&per_page=500`. The offset overflows, either wrapping `int32` to an arbitrary or negative value or going negative outright. A negative OFFSET makes Postgres fail, and `audit.go` returns a 500 whose body is `err.Error()`, the raw database message. Impact: every one of these queries is filtered by the JWT's org_id, so there is no cross-tenant effect. The result is a noisy 500 or an odd page, plus a raw database error message in the audit path. B-223's claim is scoped correctly: the comment at `approvals.go:402-406` names only `pagination()` and its `alerts.go`/`users.go` callers. The pass does not make pagination safe codebase-wide. Fix: send these handlers through `pagination()`, or through a shared helper that computes a checked offset, and stop putting `err.Error()` in 500 responses.
>
> **Info: the int32-safety of the clamp is held only by a comment.** 999,999 × 500 = 499,999,500, which is below MaxInt32, so today's callers are safe: alerts caps `per_page` at 200, users at 100, approvals at 500, CMDB at 100, and CMDB's offset is not int32 anyway. The test hardcodes 500 instead of checking the real callers. A future caller with `maxPerPage` of 5000 or more would wrap `int32` and the test would still pass. Fix: a checked int32 offset helper, or a global cap on `maxPerPage` inside `pagination()`.
>
> **Info: the endpoint license gate covers only the classification write.** `router.go:340` `PATCH /v1/endpoints/{endpointId}/link-agent` (`LinkEndpointAgent`) still writes to `endpoints` with no Discovery check. The earlier review flagged this as precedent. Saying "L-1 fixed" is accurate for `SetCMDBAssetClassification` only.
>
> **Info: the L-3 fix is on the Go side only.** The trigger `normalize_ci_name` still uses ASCII `btrim` (`000024...up.sql:41-42`). API writes are now correct: `strings.TrimSpace` handles tab and NBSP, and I confirmed the test string holds real NBSP bytes (`302 240`). Two gaps remain: direct or non-API writes would still store `"x\t"`, and leading or trailing zero-width characters (U+200B) still pass, since they are not whitespace to Go either. Live check: 0 existing `ci_categories`/`ci_types` rows have trailing tab, NBSP or newline. Optional hardening: a Unicode-aware trim in the trigger.
>
> **Info: the ESCAPE clause depends on `standard_conforming_strings`.** `ESCAPE '\'` is correct only while `standard_conforming_strings=on`. The live value is `on`, and it is the Postgres default. If it were off, the literal would not terminate, the query would fail with a syntax error and return 500. That fails closed; it is not an injection. `ESCAPE E'\\'` would remove the dependency.
>
> **Info: the code and tests cite a B-ID that has not been minted.** `approvals.go`, both test files and the CONTEXT.md marker cite "B-223", but `BACKLOG.md:2390` still reads `## Next B-ID: B-223`. This is not a security issue, but it is a claim the repo does not yet back up.
>
> **Info: `meta.page` echoes the clamped value.** A request for `page=9e18` returns `meta.page=1000000`. This is harmless, and the test asserts it.
>
> **Checked and clean**
> 1. **Tenant isolation of the new counts query** (`cmdb.go:155-161`). `nav := f` copies the struct and clears only `CategoryID`, `TypeID` and `Kind`. `OrgID` comes from JWT claims only (`cmdbFilter` at `cmdb.go:70-72`). `IncludeEndpoints = licensed` and `WorkspaceID`/`Query` are kept. Clearing pointers on the copy does not mutate `f`, which is already used by then anyway. Every UNION branch filters `org_id=$1`, and the `ci_types`/`ci_categories`/`workspaces` joins are pinned to `$1`. Clearing `Kind` cannot expose endpoints to an unlicensed org: `$2=false` stays in the endpoint branch, and an unlicensed `kind=endpoint` request is already rejected with 403 before any query runs. The counts expose nothing that `ListCMDBClassifications` did not already return org-wide. Cost: one extra UNION scan, the same order of work as before, still bounded by `q` ≤ 200 characters.
> 2. **License gate** (`cmdb.go:370-374`). Order of checks: kind allow-list, then assetId parse, then the license check, then body decode, then the database lookup. Existing and nonexistent endpoint IDs get the same 403, so the gate is not an existence oracle; the N2 test checks this with `uuid.New()`. `discoveryLicensed` fails closed on database error and checks the license's signed org claim, `claims.OrgID()==orgID`. Agent and tool writes stay ungated, which is intended and matches the reads. `uc` is now taken before the gate, and the later `SetCMDBAssetType` uses the same `uc.OrgID`.
> 3. **Authorization.** All CMDB write routes remain in the `requireRole("admin")` group. The new test checks that the `workspace_admin` and `workspace_member` roles, combined with the operator or viewer org role, get 403 and that nothing reaches the database.
> 4. **SQL injection.** `q` is still a bound parameter. `likeEscaper` is a `strings.NewReplacer`, which replaces in a single pass, so there is no double escaping. `Sprintf` inserts only `$N` placeholder numbers, and the `%%` sequences render as the literal `'%'`. Operator precedence (`||` binds tighter than `ILIKE ... ESCAPE`) is correct. The escaped value is at most about 400 bytes. `kind` goes through a fixed map to static SQL.
> 5. **Pagination clamp.** `min(n, 1_000_000)` is applied after `Atoi`, and values beyond int64 fail `Atoi` and fall back to page 1. Every `pagination()` caller is covered: `alerts.go:283` (per_page cap 200, int32 offset), `approvals.go:79` (500, int32), `users.go:90` (100, int32), `cmdb.go:144` (100, int). The worst-case offset fits in `int32`, and the unit tests pass.
> 6. **Information disclosure.** The new 403 message is fixed text. The new code adds no raw errors to responses; `cmdbWriteError`'s 23514 passthrough is unchanged and was noted in the earlier review.
> 7. **Name normalization.** Trimming happens in place before the 120-byte length check. `validateCMDBType` now takes a pointer, so the trimmed name is what gets stored.
> 8. **Tests do not leak into the shared database.** `newWorkspaceTestEnv` registers `t.Cleanup(pool.Close)` first, which follows the CLAUDE.md rule. `seedTestOrg` registers `DELETE FROM orgs`. The foreign keys to `orgs` for users, licenses, gateway_tools, gateway_agents, endpoints, groups, workspaces, ci_types and ci_categories are all ON DELETE CASCADE (`confdeltype='c'`, queried). Live database: 0 orgs match `cmdb-*`/`b223-*`/`cmdb-fixup*`, 0 recent orgs with a test-style slug, and 0 orphaned `wf-test-` users. The migration tests use `newThrowawayDB`, and no non-default databases remain on the server.
> 9. **UI diff** (`AssetsPage.tsx`): display-only changes (a navigation total and a disabled default checkbox). The server still enforces everything.
> 10. **Live schema versus migration 000024.** `schema_migrations` is at version 24 and not dirty. The `ci_types` constraints are the pkey, `UNIQUE(id,org_id)`, `UNIQUE(org_id,normalized_name)`, the kind CHECK, `category_org_fk RESTRICT`, the `org_id` CASCADE foreign key, and the deferred constraint trigger; `ci_types_org_id_asset_kind_normalized_name_key` is gone. The `ci_categories` constraints match the migration. The indexes match, including the partial `ci_types_one_default_per_kind` and `ci_types_category_idx`. All 11 triggers match the migration's definitions. Columns and defaults match: `ci_type_id` is a nullable uuid on endpoints, gateway_agents and gateway_tools, and all three composite foreign keys are RESTRICT. The live `prosrc` of all six functions (`normalize_ci_name`, `keep_ci_type_scope_immutable`, `require_ci_default`, `check_asset_ci_type_kind`, `seed_default_ci_taxonomy`, `seed_default_ci_taxonomy_for_org`) is identical to the migration text by MD5 over non-blank lines. There is exactly one overload of each, owned by `eami_app`, with no SECURITY DEFINER and no custom `proconfig`/`search_path`. No stray `ci_*` relations or functions exist. Every org × kind has exactly one default (0 bad out of 18). Grants are unchanged (`eami_app` only). The reconcile SQL was a single transaction; it only re-created two functions with the migration's own text and dropped a constraint that a stricter one already covers, so no data was affected.

## 6. Fixture cleanup: proof

All `b196fix` fixtures were deleted in one transaction. The org's cascade removed its user, endpoint and taxonomy. psql output:

```
BEGIN / DELETE 1 / DELETE 2 / DELETE 1 / DELETE 1 / COMMIT
```

The same snapshot SQL was run before fixtures were created and again after cleanup, and `diff before.txt after.txt` found them **identical**:

```
orgs|6  users|13  ci_categories|18  ci_types|18  endpoints|5  agents|12  tools|4  licenses|2  ep_typed|1  ag_typed|0  tl_typed|0
0392716a-…|Bhargav_tej|Endpoint|true      ← pre-existing explicit assignment, untouched
ci_types_by_org|<each of the 6 orgs>|3|connector:true,endpoint:true,governed agent:true
```

The residual scan for `b196fix` across orgs, users, endpoints, tools, categories and types found **0** rows. The real-Postgres test runs left no orgs behind either: the snapshot's org count is unchanged, and the security review independently counted 0 test-style orgs. The fixture password file was deleted from the scratchpad.

## 7. Recorded, not done in this pass

1. **Pre-existing hand-rolled paginators bypass `pagination()`.** Both reviews flagged these (Low):
   - `audit.go` also returns `err.Error()` in its 500 responses.
   - `paste_events.go`, `gateway_episodes.go`, and `reports.go` `parsePage`/`parseIntParam` (`parseIntParam` has no overflow check).

   They are outside B-223 as the founder defined it ("the shared `pagination()` overflow fix"). The founder later approved a separate ID, and they are now tracked as **B-225** (QUEUED).
2. **OpenAPI contract (Architect-EAMI).** Two gaps need documenting:
   - the `counts` semantics, which ignore category/type/kind but honour workspace, search and license;
   - the new PATCH classification `403 module_not_licensed`.

   Recorded under B-196 for Architect-EAMI. Code did not edit the contract.
3. **L-3 residuals** (NOTES.md):
   - the trigger still uses ASCII `btrim`, so non-API writers are affected;
   - trailing zero-width characters are not trimmed;
   - internal NBSP collapsing depends on locale.
4. **`PATCH /v1/endpoints/{id}/link-agent` has no Discovery gate.** This is pre-existing and was already noted as precedent (NOTES.md).
5. **N6** (UI polish): deferred by the founder.
6. **B-224: durable admin-write audit trail.** QUEUED as a tracked future item.
