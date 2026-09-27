# B-226 / B-227 Verification Record: `/v1/endpoints` search, and Discover paging

Written 2026-09-27 by Claude Code.

This builds on the finding in `IA_CONSOLIDATION_INVESTIGATION.md` §A1. The founder approved Part A and chose option (c) for the OS filter: keep it client-side, label it honestly, and log server-side filtering as follow-up B-228.

**Checkability.** Everything below quotes command output or review reports verbatim.
- **Raw logs** are in the scratchpad of Claude Code session `b3cde494-55e6-4fd7-b83d-5f55f606e27f` and are not committed: `b226_api_full2.log`, `b226_mutation.log`, `b226_run_final.log`, `b226_before2.txt`, `b226_after_final.txt`.
- **Review transcripts** are in that session's subagent records.

## 1. Root causes (Part A, confirmed before building)

| Bug | Cause | Layer |
|---|---|---|
| **B-226: search ignored** | `ListAgentEndpoints` (`discover.go`) never read `search`. `ListAgentEndpointsParams` had no filter field. The SQL filtered only by `org_id`, and `CountAgentEndpoints` was unfiltered too. `api/openapi.yaml` already documented `search`, so this was a backend-only fix and needed no contract change. | backend |
| **B-227: only the first 25 reachable** | Server paging already worked: `?per_page=1&page=2` returned the 2nd row live. `DiscoverPage` always requested `per_page: 25`, never sent `page`, and hid `DataTable`'s client pager (`pageSize={1000}`). | frontend |

These are different root causes, so they have separate B-IDs. Both IDs were confirmed free against BACKLOG.md directly: the counter read `B-226`, and no open item used B-226, B-227 or B-228 or covered this scope.

## 2. What changed

- **`eami-api/internal/store/endpoints.sql.go`**
  - One shared filter fragment, `agentEndpointSearchSQL`, performs a case-insensitive literal hostname substring match: `ILIKE` with the store's `likeEscaper` and `ESCAPE E'\\'`.
  - The list and `CountAgentEndpoints` both use it, so `meta.total` always counts exactly the rows being paged.
  - `ORDER BY last_seen DESC, id` adds a tie-breaker so pages are stable.
- **`eami-api/internal/api/discover.go`** reads `search` and trims it. It returns 400 for:
  - NUL bytes or invalid UTF-8 (after the security review);
  - more than 200 characters, counted as runes (after the security review).
- **`eami-ui/src/pages/discover/DiscoverPage.tsx`**
  - **Paging:** server page state, with a Previous/Next pager following the Assets page's pattern.
  - **Search:** a 250 ms debounce, where the search and the page reset are committed together from the trimmed value. The input has `maxLength=200`.
  - **Count label:** shows "Loading…" instead of a false "0 endpoints".
  - **Error state:** an error plus Retry (the Assets page's pattern) instead of a false empty state.
  - **Page clamp:** the page is clamped when the matching set shrinks.
  - **OS filter:** the platform filter is labelled "applies to this page only". It also has a tooltip.
- **`MATURITY_AUDIT.md`** is corrected in place. The original wording is struck through, marked "Found incorrect (2026-09-26)", and linked to B-226/B-227.

**Not changed, and recorded instead:**
- `has_ai` and `has_local_model` are documented but still ignored.
- The contract's "or username" wording is inaccurate: `endpoints` has no username column. This is for Architect-EAMI.
- `parsePage` page overflow stays under B-225.

## 3. Automated verification (final code)

**Commands and results**

```
eami-api: go build ./... ; go vet ./internal/api ./internal/store  → ok
eami-api: POSTGRES_PASSWORD=… go test -count=1 -v ./...           → every package ok; PASS=476 FAIL=0 SKIP=0
eami-ui:  npx tsc --noEmit → 0 ; npx vite build → built ; git diff --check → clean
gofmt: endpoints_search_pg_test.go clean; discover.go / endpoints.sql.go gofmt-diff line counts equal to HEAD's (pre-existing, not introduced)
```

**New real-Postgres tests** (`eami-api/internal/api/endpoints_search_pg_test.go`):
- **`TestListEndpoints_SearchFiltersAndCountMatches_RealDB`**
  - Every case asserts that `meta.total` equals the returned rows.
  - Cases: empty search; case-insensitive search; trimmed search; no match; literal `_`, `%` and `\`; another org's host (isolation).
  - Validation: 201 ASCII characters → 400; 200 CJK characters → 200; 201 CJK characters → 400; NUL → 400; invalid UTF-8 → 400.
- **`TestListEndpoints_PaginationPast25WithSearchIsCompleteAndStable_RealDB`**
  - 30 matching plus 3 non-matching endpoints, all sharing one `last_seen`.
  - Pages 1 and 2 return 25 and the remainder; every ID appears exactly once; totals are exact; page 3 is empty.
  - Both cases are checked, with the search and without it.

### 3a. Mutation check (`b226_mutation.log`, verbatim)

```
=== MUTATION list ignores search (handler passes empty Search)
--- FAIL: TestListEndpoints_SearchFiltersAndCountMatches_RealDB
    search="alpha" returned [ALPHA-db-02 alpha-web-01 back\slash betaXhost beta_host gamma%node], want [ALPHA-db-02 alpha-web-01]
=== MUTATION count ignores search
--- FAIL: TestListEndpoints_SearchFiltersAndCountMatches_RealDB
    search="alpha" meta.total=6 but list has 2 matching rows (count and list must share one filter)
=== MUTATION no LIKE escaping in list+count
--- FAIL: TestListEndpoints_SearchFiltersAndCountMatches_RealDB
    search="beta_host" returned [betaXhost beta_host], want [beta_host]
=== MUTATION no id tie-breaker (run 1/2/3)
ok   (NOT caught — see below)
=== MUTATION UTF-8/NUL guard removed
--- FAIL: TestListEndpoints_SearchFiltersAndCountMatches_RealDB
    NUL byte: status 500 want 400: {"code":"internal_error","message":"failed to list endpoints"}
=== MUTATION rune limit back to byte length
--- FAIL: TestListEndpoints_SearchFiltersAndCountMatches_RealDB
    200 CJK chars: status 400 want 200: {"code":"bad_request","message":"search must be at most 200 characters"}
sources restored OK
```

**Two honest limits**
- **The `e.id` tie-breaker is not test-proven.** Postgres happened to return a stable order for tied rows without it in all 3 runs. The code review agrees it is "code-review-verified, not test-proven".
- **The UI fixes have no automated test**, because `eami-ui` has no UI test framework. The live run in §4 covers them, with one exception: the page-past-end clamp can't be driven live, because focus-refetch is disabled app-wide (`lib/query.ts:8`). It is verified by code review and typecheck only.

## 4. Live acceptance (rebuilt shared stack)

**Environment**
- The API was rebuilt from the working tree and started at 2026-09-27T02:52:46Z; the UI was rebuilt after the final review fixes.
- The served `DiscoverPage.tsx` was confirmed to contain the new code.

**Fixtures**, all removed afterwards (§5):
- a Dev Org fixture admin, with its password hashed in-DB via `pgcrypto` for the new row only;
- 30 `b227fix-host-NN` endpoints;
- `b226fix_under` and `b226fixXunder`.

Dev Org then had 37 endpoints: 5 real plus 32 fixtures.

**Result: 18 of 18 passed** (`b226_run_final.log`, verbatim):

```
PASS [AC2] API: unfiltered total counts the whole inventory (Dashboard uses this) :: total=37
PASS [AC1] API: made-up hostname → 0 rows, total 0 (was: all endpoints) :: rows=0 total=0
PASS [AC1] API: real hostname search on pre-existing data → only matching endpoints :: rows=2 total=2 hosts=Bhargav_tej,Bhargav_tej
PASS [AC1] API: "_" is literal (b226fix_under only, not b226fixXunder) :: total=1 hosts=b226fix_under
PASS [AC2] API: search + paging past 25 → 25 + 5 rows, 30 distinct, total 30 on both pages :: p1=25 p2=5 distinct=30 totals=30/30
PASS [AC1] API: 201-char search → 400 :: 400 {"code":"bad_request","message":"search must be at most 200 characters"}
PASS [AC2] UI: unfiltered → "37 endpoints", 25 rows, "Page 1 of 2" :: rows=25 count="37 endpoints" label="Page 1 of 2"
PASS [AC2] UI: Next → page 2 renders the remaining 12, no endpoint id repeated from page 1 :: rows=12 idOverlap=0 distinct=37
PASS [UX] UI: while page 2 loads the count never shows a false "0 endpoints" :: label during load="Loading…"
PASS [AC1] UI: typing a search → debounced to one request, reset to page 1, 30 matches, 25 rows all matching :: requests while typing 12 chars=1 ["?search=b227fix-host&page=1&per_page=25"] total=30 label="Page 1 of 2"
PASS [AC2] UI: filtered page 2 → 5 rows, all matching, no id repeated :: rows=5
PASS [AC1] UI: made-up hostname → API 0 rows, "0 endpoints" + "No endpoints found" empty state (was: all endpoints) :: api total=0 count="0 endpoints"
PASS [AC1] UI: "b226fix_" → only b226fix_under :: ["b226fix_under"]
PASS [UX] UI: search input enforces maxLength=200 :: maxlength attr
PASS [OS] UI: platform filter shows the honest "applies to this page only" label :: Platform filter applies to this page only — showing 0 of 25 on this page
PASS [OS] UI: platform select carries the page-only tooltip :: title present
PASS [UX] UI: a failed request shows an error + Retry, not a false "0 endpoints" :: label="Endpoints unavailable"
PASS [console] UI session: zero console/page errors :: []
18 checks, 18 passed, 0 failed
```

The error-state check intercepts one request and returns 500. Only that deliberate 500's console line is excluded from the console check.

**Earlier live runs, in order.** Every failure was investigated before it was dismissed.
1. **First run: 14 of 16.**
   - **"Page 2 overlap = 1"** was a **script defect.** It compared hostnames, and two distinct real endpoints are both named `Bhargav_tej`; compared by ID, the overlap was 0.
   - **The missing empty state** was a **script timing defect.** The script waited for the text "0 endpoints", which the old code rendered *while loading*. A re-probe that waited for the API response saw the empty state render. That exposed a **real UX flaw**: a false "0 endpoints" appeared during every load. It is now fixed and checked live (the `[UX] … Loading…` line above).
2. **Later reruns** hit further script defects: a variable-name clash, lost regex backslashes, and a wait on a request that React Query rightly served from cache. After those, the login rate limiter (B-070, 20 per IP per 300 s) returned **429**. This is a working security control. I waited out the window instead of polling, since attempts may count against the limit.

## 5. Fixture cleanup: proof

All fixtures were deleted in one transaction: `DELETE 1` for the user, then `DELETE 32` for the endpoints, then `COMMIT`.

`diff b226_before2.txt b226_after_final.txt` found them **identical**. The before-snapshot was taken ahead of every fixture in this cycle.

```
orgs|6  users|13  ci_categories|18  ci_types|18  endpoints|5  agents|12  tools|4  licenses|2  ep_typed|1  ag_typed|0  tl_typed|0
```

The residual scan for `b226fix`/`b227fix` users and endpoints, and `b22*` org slugs, found **0**. Because the org count is unchanged, the real-Postgres test runs left nothing behind either. The fixture password file was deleted.

## 6. Reviews (both mandatory passes completed, quoted verbatim)

| Pass | Completed? | Notes |
|---|---|---|
| Security review, first pass | **Yes** | on the initial implementation |
| Code review, first attempt | **No: stopped by Claude Code before it reported**, when the usage limit hit | it produced no findings; it is **not** counted as a review |
| Security review, delta re-review | **Yes** | on the fixes for its two Lows |
| Code review, final (fresh, full diff) | **Yes** | on the final backend plus debounce code |

**After those reviews,** its three Lows were fixed: the stale-page clamp, the error state, and the trimmed debounce comparison. Those three changes are UI-only. They are covered by the typecheck, the build and the live run in §4, including a live error-state check. They were **not** re-sent to a reviewer.

### 6a. Security review, first pass

> VERDICT: No blocking security issues. Safe to merge from a security standpoint. I found no High or Medium issues, two Low, and a few Info notes.
>
> **FINDINGS**
>
> 1. **Low: a NUL byte or invalid UTF-8 in `search` returns 500 instead of 400.**
>    - Where: eami-api/internal/api/discover.go:68-72, and the store queries at endpoints.sql.go:440 and :462.
>    - Scenario: `GET /v1/endpoints?search=%00` or `?search=%FF` passes the trim and length checks, then goes to Postgres as a text parameter. Postgres rejects it (it does not accept 0x00 or invalid UTF-8 in text; I know this from Postgres behavior, I did not run the request). The handler returns 500 "failed to list endpoints". The message is generic, so nothing is disclosed. The cost is noisy 500s and error-rate alerting that any viewer can trigger.
>    - Fix: add `if !utf8.ValidString(search) || strings.ContainsRune(search, 0) { 400 }` next to the length check.
>
> 2. **Low: the length bound counts bytes, not characters, and the UI fires one request per keystroke.**
>    - Where: discover.go:69 (`len(search) > 200`), and DiscoverPage.tsx around line 386 (the `onChange` has no debounce, and the input has no `maxLength`).
>    - Detail: 200 bytes is only about 66 CJK characters, yet the error says "200 characters". This is safe for DoS, since the bytes bound is the stricter one. But the message is inaccurate, and the UI shows an error state rather than stopping input.
>    - Every keystroke also runs two org-scoped queries: a list with an ILIKE filter and a count. The cost is bounded (see the DoS section below), so this is performance hygiene, not a vulnerability.
>    - Fix: either say "bytes" or use `utf8.RuneCountInString`. Debounce the search (about 250ms) and add `maxLength={200}` to the input.
>
> 3. **Info: the `gateway_agents` LEFT JOIN is not org-scoped in SQL.** This is unchanged by the diff.
>    - Where: endpoints.sql.go:432 (`LEFT JOIN gateway_agents ga ON ga.id = e.gateway_agent_id`).
>    - Isolation still holds because the only write path, `LinkEndpointToGatewayAgent` at endpoints.sql.go:598-613, checks both the target agent (`WHERE id=$1 AND org_id=$2`) and the endpoint (`id=$2 AND org_id=$3`).
>    - Optional defense-in-depth: add `AND ga.org_id = e.org_id` to the join. Not introduced here.
>
> 4. **Info: the B-225 page overflow is unchanged, not made worse.**
>    - Where: discover.go:79, `Offset: int32((page - 1) * perPage)`, which is the same expression as before.
>    - `parsePage` (reports.go:240-256) caps `per_page` at 200 but leaves `page` unbounded. The new count query takes no page argument, so no new overflow surface was added. BACKLOG.md:2093/2420 already tracks this.
>
> 5. **Info (process, not security):** B-226 and B-227 are cited in code comments and tests but do not exist in BACKLOG.md yet. Its counter still reads "Next B-ID: B-226" (BACKLOG.md:2435). Per the memory rule, B-ID minting needs founder confirmation.
>
> **CHECKED AND CLEAN**
>
> **SQL injection: clean.**
> - Placeholders run $1 (org), $2 (escaped search), $3/$4 (limit/offset) in the list query, and $1/$2 in the count query. Argument order matches at endpoints.sql.go:439-440 and :461-462.
> - The only string concatenation joins two Go constants (`agentEndpointSearchSQL` plus fixed text). User input only ever travels as a bound parameter.
> - The escaper at cmdb.sql.go:230 replaces `\` first, then `%` and `_`, and it is applied in both queries.
> - The Go raw string `E'\\'` reaches Postgres as the E-string `E'\\'`, which is a single backslash. E-strings process escapes regardless of `standard_conforming_strings` (the live DB reports `on`).
> - I verified the escape semantics live with `chr(92)`: a pattern matching a literal backslash matches `a\b` (true); escaped `_` on `ab` is false and on `a_b` is true; escaped `%` on `axb` is false.
> - `$2::text = ''` short-circuits the empty case correctly, because the handler trims before escaping and an empty string escapes to empty.
>
> **Tenant isolation: clean.**
> - `e.org_id = $1` is ANDed in front of the search filter in both queries (endpoints.sql.go:433 and :461).
> - The parenthesized OR inside `agentEndpointSearchSQL` cannot bypass the org predicate.
> - The org comes only from the JWT claims (`uc.OrgID`, discover.go:62/77/88), never from a request parameter.
> - Search and count can only match rows the caller could already list unfiltered. They reveal nothing about other orgs, so there is no cross-org oracle, including timing.
> - The live plan uses `Index Scan using idx_endpoints_org` with the ILIKE as a post-filter, so the scan is bounded to the caller's own org's rows.
> - The test covers a foreign-org hostname ("foreign" returns nothing, total 0).
>
> **License gating: clean.** router.go:436-444 puts `/v1/endpoints` in the nested group with `r.Use(s.requireModuleLicensed("discovery"))`.
>
> **RBAC: clean.** That group is nested inside the read group at router.go:379-381, which applies `requireRole("admin","operator","viewer")` and `viewerReadOnly`. There is no change to route placement.
>
> **DoS and resource bounds: clean.**
> - `per_page` is capped at 200 via `parsePage`.
> - Search is capped at 200 bytes.
> - ILIKE '%…%' with no trigram index is a per-org sequential filter after the `idx_endpoints_org` index scan. It is not a full-table scan.
> - The `e.id` tie-breaker adds an in-memory sort within the org's rows.
> - `hostname` is an unbounded text column, but ingest data, not the request, controls its length.
>
> **Error disclosure: clean.** All new paths return fixed messages ("search must be at most 200 characters", "failed to list/count endpoints"). No `err.Error()` is echoed (discover.go:70, 84, 90).
>
> **Callers and signature change: clean.** `CountAgentEndpoints` has exactly one caller (discover.go:88). `go vet ./internal/api/ ./internal/store/` passed with no output, which also type-checks the new test file.
>
> **Test data leakage: clean.**
> - `newWorkspaceTestEnv` (workspaces_pg_test.go:48-73) registers `t.Cleanup(pool.Close)` first. It therefore runs last, after the per-org DELETEs from `seedTestOrg` (workflows_test.go:37-47, t.Cleanup only). There is no plain `defer pool.Close()`.
> - Seeded endpoints, users and licenses have no explicit cleanup but cascade from the org. Live catalog: the `org_id` FKs on endpoints, users, licenses and gateway_agents are all `ON DELETE CASCADE` (confdeltype c).
> - The `endpoints.ci_type_id` RESTRICT FK cannot block the cascade, because the tests leave `ci_type_id` NULL.
> - Live leftover counts were 0 orgs named `b226-%`/`b227-%` and 0 endpoints with those agent_id prefixes. That could also mean the tests have not been run against this DB yet. As instructed, I did not run them.
>
> **UI rendering: clean.**
> - Hostnames render as JSX text (DiscoverPage.tsx:127 and :351), which React auto-escapes. There is no `dangerouslySetInnerHTML` in the discover page or `DataTable`.
> - The request goes through the generated client (`api.GET('/v1/endpoints', {params:{query}})`, useEndpoints.ts:26-33), which URL-encodes the search. There is no raw fetch.
> - The page resets to 1 when the search changes. The OS filter is honestly labeled as applying to the current page only.
>
> **Not verified:** I did not execute the real-Postgres tests, and I did not run a live HTTP request with NUL or invalid UTF-8 (finding 1 is reasoned from Postgres semantics).

**Disposition:**
- **Low 1 is fixed.** Its reasoned 500 was then confirmed live by the mutation run in §3a.
- **Low 2 is fixed.**
- **Info 5:** the IDs are minted in this commit, under the founder's explicit direction for B-226/B-227/B-228 and after a direct check against BACKLOG.md.

### 6b. Security review, delta re-review

> VERDICT: Findings 1 and 2 are both resolved. The delta introduces no new security issue: nothing High, Medium or Low, and two Info notes.
>
> **FINDINGS**
>
> 1. **Info: a malformed percent-escape silently drops the search instead of returning 400.**
>    - Where: eami-api/internal/api/discover.go, `q := r.URL.Query()` feeding `q.Get("search")`.
>    - `url.Query()` throws away its parse error and leaves out any pair with a bad escape. I checked this in a scratchpad Go program: `search=%zz` decodes to `""`, so the request returns the unfiltered own-org list with 200.
>    - This is not a security issue. The result is still scoped to the caller's org, gated by license and role, and bounded by per_page. It is only a correctness wart. It also predates the delta, since it is how the other handlers read query params too.
>    - Optional fix: use `url.ParseQuery(r.URL.RawQuery)` and return 400 on error.
>
> 2. **Info: server and client define "whitespace" slightly differently for trimming.**
>    - The server uses Go `strings.TrimSpace` (Unicode White_Space). The client uses JS `.trim()`, which is a similar but not identical set.
>    - Verified: `%E3%80%80x%E3%80%80` (U+3000 ideographic space) trims to `"x"` in Go. JS `.trim()` also strips U+3000, so these agree.
>    - Any leftover edge-case difference only changes what gets matched, never what can be reached. No action needed.
>
> **RESOLUTION OF THE PRIOR FINDINGS**
>
> - **Finding 1 (NUL or invalid UTF-8 gave 500): resolved.** discover.go now checks `!utf8.ValidString(search) || strings.ContainsRune(search, 0)` and returns 400 "search must be valid UTF-8 text". The check runs after TrimSpace and before any DB call. NUL is not whitespace in Go, so trimming cannot remove it before the check. Scratchpad probe: `alpha%00` gives nul=true, and `%FF%FE` gives valid=false. Both are caught. The tests pin both cases (endpoints_search_pg_test.go:113-114). Your mutation run's real 500 confirms the original finding.
> - **Finding 2 (byte-based bound, no debounce or maxLength): resolved.** The bound is now `utf8.RuneCountInString(search) > 200`, which matches the "characters" wording in the error message. The UI has a 250 ms debounce effect in DiscoverPage.tsx that commits the search and page=1 together, and `maxLength={200}` on the input. HTML maxLength counts UTF-16 code units. An astral character counts as 2 there but is 1 rune on the server, so the UI limit is never looser than the server limit: 200 code units means at most 200 runes. A user cannot type or paste past the server bound through the UI.
>
> **DoS IMPACT OF THE RUNE BOUND: not meaningful.**
> - The worst case is 200 four-byte runes, which is 800 bytes. After escaping, the pattern is at most 400 bytes, because only ASCII `\ % _` double, and those are 1 byte each.
> - The pattern has exactly one leading and one trailing `%` (user wildcards are escaped), so Postgres's LIKE matcher cannot backtrack exponentially. The worst case is roughly the hostname length times the pattern length, per row.
> - The live plan from the first review showed an `idx_endpoints_org` index scan with ILIKE only as a post-filter, so this cost is limited to the caller's own org's rows and at most 200 rows per page.
> - Going from a 200-byte to an 800-byte ceiling is a small constant factor.
>
> **GO vs POSTGRES % DECODING: clean.** Go percent-decodes the query exactly once. The value is sent to Postgres as a bind parameter, and Postgres does no further URL or percent decoding.
> - `%2500` becomes the literal 3-character string `"%00"` (verified). There is no NUL. `likeEscaper` turns it into `\%00`, which matches a literal "%00".
> - `+` decodes to a space (verified). The generated client encodes spaces consistently.
> - There is no double-decoding path, and a wildcard cannot be smuggled in, because every `%`, `_` and `\` that reaches the store is escaped. The first review verified this live with `chr(92)` tests.
>
> **VERIFIED CLEAN**
> - `go vet ./internal/api/ ./internal/store/` passed (VET_OK). This type-checks the handler and the new test cases.
> - In the new test loop, every response body is closed through `readWSBody` (workspaces_pg_test.go:138-142 uses `defer resp.Body.Close()`), so nothing leaks.
> - The new cases hit only the 400 path or a read-only 200, and seed no extra rows. The pool and org-cascade cleanup analysis from the first review still holds.
> - The 200-CJK case, with 3-byte characters (600 bytes, returns 200), and the 201-CJK case (returns 400) prove the bound is counted in runes.
> - UI debounce logic: the early-return when `searchInput === search` stops a loop once the search is committed. The cleanup function clears the pending timer on every keystroke, so only the final value is sent. Setting page=1 in the same timer callback prevents fetching a stale page.
> - The rest of the security picture is unchanged by the delta: org scoping, license and role gating, error messages, and SQL construction are untouched.
>
> Method: no repo edits, no DB access, and no real-Postgres tests this pass. The only file I created is a throwaway probe, q.go, in the session scratchpad (outside the repo), run with `go run`.

**Disposition:** both Info items are recorded, not fixed. The `%zz` behaviour is shared by every handler that uses `url.Query()`.

### 6c. Code review, final

> **Verdict: approve with minor follow-ups. I found nothing blocking.** B-226's server side is correct, and the list and count are genuinely at parity. B-227's debounce and pager are correct. There are three Low UI or robustness gaps and a few Info items.
>
> `go build ./...` and `go vet ./internal/api/ ./internal/store/` passed; vet compiles the new test file too. `npx tsc --noEmit` in eami-ui passed. I did not run the real-Postgres tests, as instructed, so I haven't seen them pass. The MATURITY_AUDIT "Now Yes" claims rest on tests I couldn't execute.
>
> **Low: DiscoverPage.tsx ~351-352 and pager ~439. A page past the end is never clamped, and the pager can disappear, leaving the user stuck.**
> - Scenario: the user is on page 2 of 2 (26–50 matching endpoints). The endpoint set shrinks to 25 or fewer between refetches (retention, another admin, a window-focus refetch after staleTime).
> - `total` drops, `totalPages` becomes 1, and the whole pager is hidden because it only renders when `totalPages > 1`.
> - `page` stays 2, so the table shows "No endpoints found — Adjust your filters" and there is no Previous button. The only way out is to change the search.
> - AssetsPage has the same shape, so this is inherited rather than new.
> - Fix: `useEffect(() => { if (data && page > totalPages) setPage(totalPages) }, [data, page, totalPages])`, or render the pager whenever `page > 1 || totalPages > 1`.
>
> **Low: DiscoverPage.tsx ~346 and ~414. There is no error state, so a failed request still shows a false "0 endpoints".**
> - The page takes only `{ data, isLoading }`. On any error (500, network, the new 400), `isLoading` is false and `data` is undefined.
> - The page then shows "0 endpoints" plus the "No endpoints found / Adjust your filters or wait for agents to check in" empty state.
> - This partly undercuts B-227's "no false 0 endpoints" claim: it fixes the loading case but not the error case.
> - The new 400s are practically unreachable from the UI: `maxLength=200` counts UTF-16 code units, which is always ≤ the server's rune count, and a text input cannot usually carry NUL.
> - Fix: follow AssetsPage.tsx:68's `isError` + Retry (`Button isLoading={isFetching}`) pattern.
>
> **Low: DiscoverPage.tsx ~335-342. A whitespace-only edit resets the page but sends no new request.**
> - The effect compares the raw `searchInput` with `search`, while the query key uses `search.trim()`.
> - Scenario: on page 3 with "alpha", typing a trailing space commits "alpha " and runs `setPage(1)`. The query key is unchanged, so the user is silently moved to page 1 of the same results.
> - Fix: compare trimmed values, e.g. `if (searchInput.trim() === search) return` and `setSearch(searchInput.trim())`.
>
> **Info: the `e.id` tie-breaker test (endpoints_search_pg_test.go:124-167) is not a reliable mutation detector.**
> - The 30 rows inserted by one `generate_series` INSERT do share `last_seen` (column default `NOW()`, one transaction timestamp), so ties are real.
> - Without the tie-breaker, Postgres may or may not return an inconsistent order between LIMIT 25 OFFSET 0 and OFFSET 25. Top-N heapsort bounds differ, so it is plausible but not deterministic.
> - The test would likely pass without the fix on many runs. The comment's "could" is honest, but the header's "pin … stable" overstates it. Treat the tie-breaker as code-review-verified, not test-proven.
> - The rest of that test, full coverage and exact totals across 2 pages plus an empty page 3, does fail without B-226's search, since the `page-host` total would be 33.
>
> **Info: `has_ai`/`has_local_model` are still ignored, the same class of bug as B-226.**
> - api/openapi.yaml:1227-1233 documents them, and `useEndpoints`'s `EndpointParams` exposes them. `ListAgentEndpoints` reads only `page`, `per_page` and `search`.
> - No UI caller sends them today (grep: only DashboardPage `{per_page:1}` and DiscoverPage), so nothing is broken. It should be logged as a follow-up B-ID.
>
> **Info: the contract still says "Search by hostname or username" (openapi.yaml:1237).**
> - The handler comment correctly notes that `endpoints` has no username column.
> - The contract is Architect-owned per BOUNDARIES.md, so it needs a contract-change note or backlog item, not a silent edit.
>
> **Info (pre-existing, not in the diff): paging overflow and limit drift.**
> - `parsePage` (reports.go:240-273) has no upper bound on `page`, and `parseIntParam` can overflow. `int32((page-1)*perPage)` in discover.go can wrap negative, so Postgres rejects OFFSET and the handler returns 500.
> - `per_page` allows up to 200, while the contract says `maximum: 100`.
> - Worth checking whether B-223's clamp was meant to cover this.
>
> **Info: performance.** `ILIKE '%…%'` cannot use an index, so it seq-scans the org's rows via `idx_endpoints_org`. That is acceptable at current scale; pg_trgm would be the later fix.
>
> **Verified clean**
> - **SQL parameters line up.** The list uses $1 org, $2 escaped search, $3 limit, $4 offset, and the args are passed in that order (endpoints.sql.go:439-440). The count uses $1 and $2 in order (461-462). `e` is aliased in the count's FROM, so the shared fragment's `e.hostname` resolves. `$2::text` fixes the parameter type for its untyped reuse in `'%' || $2 || '%'`.
> - **ESCAPE literal is correct.** In a Go raw string, `E'\\'` is passed through literally, and Postgres reads that escape-string as one backslash. This is identical to the proven cmdb.sql.go:255 precedent, and `likeEscaper` (cmdb.sql.go:230) escapes `\`, `%` and `_` in the right order.
> - **The test cases for `_`, `%` and `\` would each fail without escaping.** For example, a raw `_` would match every host.
> - **List and count share one filter.** `agentEndpointSearchSQL` is used by both. Both get the same trimmed `search` from the handler (discover.go:69, 84, 95), and both apply `likeEscaper` inside the store.
> - **All callers are accounted for.** There is only one Go caller of each store function (discover.go). DashboardPage.tsx:53 sends `{per_page:1}`, so search is "", which is no filter, and `meta.total` is unchanged.
> - **Validation is ordered and measured correctly.** Trimming happens before the UTF-8/NUL check and before the length check. Length is measured in runes, and the test pins 200 vs 201 CJK characters. `%00` and `%FF%FE` decode to raw bytes via `url.Query`, so those tests do exercise the new 400 branches.
> - **Debounce effect has no stale-closure or double-request problems.** It is a no-op on initial render because the two values are equal. Each keystroke's cleanup clears the pending timer, and reverting to the committed value cancels it. Clearing the input commits "" after 250 ms. `setSearch` and `setPage(1)` run in one timeout callback, which React 18 batches into one render and one new query key, so there is no request with a stale page. StrictMode (main.tsx:21) mounts, cleans up and remounts harmlessly. There is no page-level early return, so the input keeps focus during fetches.
> - **DataTable's own pager stays hidden.** With `pageSize=PER_PAGE` and at most 25 rows from the server, DataTable's internal `totalPages` is always 1. No Discover column is `sortable`, so there is no "sort applies to this page only" honesty gap either.
> - **Pager and Button rules are followed.** The pager uses the shared `Button` (size sm, outline), matching AssetsPage, and Next uses `>=` rather than `===`, which is more robust. There are no async-action buttons, so `isLoading` does not apply. The new spans use the page's existing `text-xs text-gray-500` classes.
> - **The OS-filter label is honest.** A `title` tooltip is always present, and a visible "applies to this page only — showing X of Y on this page" appears whenever a filter is active.
> - **The test file follows the CLAUDE.md pool-lifecycle rule.** `newWorkspaceTestEnv` registers `t.Cleanup(pool.Close)` first (workspaces_pg_test.go:63), and `seedTestOrg`'s `DELETE FROM orgs` cleanup is registered later, so it runs before the pool closes. There is no plain `defer pool.Close()`. Seeded endpoints have a NULL `ci_type_id` and `ON DELETE CASCADE` from orgs, so they are removed by the org delete. `agent_id` uniqueness is per org (`UNIQUE(org_id, agent_id)`), so the non-random `'b227-'||g` values cannot collide across runs. Licenses follow the existing `seedDiscoveryLicense` precedent. The cross-org case "foreign" confirms tenancy isolation.
> - **MATURITY_AUDIT.md edits are accurate.** The Discover row and summary bullet are struck through and marked incorrect, with the correction next to them. "1 of 11 at audit time" is right: Audit only, counted as field-scoped search. "Partial" for multi-field filtering is right, since the platform filter is page-local. The earlier IA-investigation correction paragraph was replaced rather than kept; its facts (live-confirmed 5 of 5, §A1) are carried into the row text. IA_CONSOLIDATION_INVESTIGATION.md exists.
> - **CONTEXT.md** changes only the active-agent marker line.
>
> Files reviewed: C:\AI\EAIM\eaim\eami-api\internal\api\discover.go, C:\AI\EAIM\eaim\eami-api\internal\store\endpoints.sql.go, C:\AI\EAIM\eaim\eami-api\internal\api\endpoints_search_pg_test.go, C:\AI\EAIM\eaim\eami-ui\src\pages\discover\DiscoverPage.tsx, C:\AI\EAIM\eaim\MATURITY_AUDIT.md

**Disposition**
- **Fixed after this review:** all three Lows.
  - **Page clamp:** fixed, but not live-drivable (§3).
  - **Error state:** fixed and live-checked.
  - **Trimmed comparison:** fixed.
- **Test header wording:** the tie-breaker "pin … stable" wording is corrected in the test comment below.
- **Recorded as follow-ups:** the `has_ai`/`has_local_model` and "or username" items. The first needs a founder-confirmed B-ID; the second is an Architect-EAMI contract note.
- **Already tracked:** the `parsePage` overflow is under B-225.
- **Recorded:** the `per_page` 200-vs-100 contract drift.
