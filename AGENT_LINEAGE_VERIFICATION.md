# Agent Lineage: verification record

**Date:** 2026-09-29 · **By:** Claude Code · **Roadmap:** Horizon 1, "Agent lineage: real, per-agent activity from the dispatch path". The section was added in `c740c15`, pushed, and cited by DESIGN_SYSTEM.md §7.7. **B-ID:** none minted for the feature itself (not requested); related index item **B-265** (queued).

## 1. Part A findings (reported before building; founder decisions 2026-09-29)

- **Coordination with the IA plan (B-252 C1).** There is no functional overlap. Both add an entry to Agent Detail's `TABS` and `TAB_VISIBLE`, so it is purely mechanical. Lineage went first.
- **The roadmap had no lineage section.** This was flagged. On the founder's instruction, the section was added (`c740c15`) with a §7.7 row.
- **`audit_log` shape.**
  - Columns: `agent_id` (nullable), `tool_name` (free text, no tool FK), `decision` (allowed/denied/escalated), `policy_id`, `approval_id`, `workflow_run_id`, token counts, `timestamp`, `data_handling_designation`, `redacted_count`, and the hash columns.
  - There is **no cost column**; cost lives in `token_usage`.
  - `token_usage.audit_log_id` is never populated, so there is **no per-call join** between the two.
  - It has 20 monthly partitions and **no `agent_id` index** (B-265).
- **Data classification is not recorded.**
  - `redaction.Redact` returns only a count, and the gateway writes only `redacted_count`.
  - The masked `[REDACTED:<PATTERN>]` tokens go only to the provider.
  - `audit_log.parameters` holds the original input, or NULL under the default audit mode.
  - So this element was **out of scope**. By founder decision, the redaction count was also left out.
- **FinOps has no caching.** It sums live at query time. Lineage does the same, with measured cost in the tens of milliseconds (section 4).
- **`risk_tier` / `owner`:** both are real NOT NULL text columns; `risk_tier` is CHECK-limited to low, medium or high.
- **Premise correction.** Connections (B-200) was already built from `audit_log`: tools actually dispatched through (with 24h/total counts), policies that actually decided a call, and workflows actually run. Lineage adds the numbers over those same relationships.

## 2. What was built

- **API:** `GET /v1/gateway/agents/{agentId}/lineage?window=24h|7d|30d` (default 7d; anything else returns 400).
  - Handler: `eami-api/internal/api/agent_lineage.go`.
  - Route: `router.go`, in the admin/operator/viewer read group next to `/connections`. **Approvers get a 403.**
  - A foreign-org or unknown agent returns **404** (`pgx.ErrNoRows` only); any other DB error returns a generic 500.
  - Not in `openapi.yaml`, which Architect-EAMI owns; the UI calls it through the documented `apiFetch` escape hatch.
- **Store:** `eami-api/internal/store/agent_lineage.sql.go`, 6 read queries run concurrently (errgroup).
  - Every query is scoped by `org_id` and `agent_id`, and every join is also matched on org: tools on `(org_id, name)`, policies on `p.org_id`, workflows on `w.org_id`.
  - **Counting rule (`isCall`):** each call counts once, by the gateway's first decision. The escalation-resolution clone (`approval_id` set, decision allowed/denied, written at `dispatcher.go` `holdOutcome.Resolved`) is excluded from call counts, so an escalated-then-approved call is one escalation, not also an allow.
  - **Cost** uses FinOps' per-row expression textually, including the cache tiers.
    - It covers the half-open span `[since, now)`, because `recorded_at` can be supplied by the client.
    - **Per-agent cost** matches FinOps' definition, and is null ("—") if the agent never had token usage.
    - **Per-tool cost** is shown only for a current `ai_provider` connector with usage ever; otherwise it is "—".
    - Usage rows with a NULL `tool_name` count in the agent total only.
    - Unpriced rows are counted and shown; this is stricter than FinOps' B-112 count, which also counts rows that have a stored `cost_usd`.
- **UI:** `components/agents/AgentLineageTab.tsx`, mounted only while the tab is open, so Agent Detail's own load makes no extra request.
  - **Summary:** the §7.6a metadata grid (3×3): Risk, Owner, tools ever touched, calls in the window, escalations and denials over 30d, first and last seen, and cost in the window.
  - **Window selector:** a `<select>` with FinOps' input styling. Stale data is dimmed and marked `aria-busy`, and is kept only for the same agent.
  - **Unpriced usage:** the amber banner (FinOps' pattern) plus "+ N unpriced" beside the amount.
  - **Tables:** Tools, Policies and Workflows, as §4 L1 cards around `DataTable`, with `ActionBadge`s and mono timestamps.
  - **Empty state:** an honest `EmptyState` ("No recorded activity") when there are no audit rows, no workflow runs and no usage.
  - **Footnote:** explains the counting rule.
  - **Tab order and visibility:** Overview, Connections, Lineage, Actions. `lib/rbac.ts` has `can.viewAgentLineage` (admin/operator/viewer); approvers still see Overview only.
- **Docs:** the roadmap Horizon 1 item and the DESIGN_SYSTEM §7.7 row (`c740c15`), and B-265.

## 3. Tests: `eami-api/internal/api/agent_lineage_pg_test.go` (real Postgres)

The seed spans every window edge.

**Agent A1:**
- 4 tools: an ai_provider tool; an ai_provider tool with calls but no usage ever; a REST tool; a deleted connector.
- 2 **escalation-resolution rows**.
- Usage: a stored cost, and a **per-run fixture model** priced in every cache tier (1+2+3+4+5 = 15.00).
- Also: an unpriced model, a REST-tool cost, a **NULL tool_name** row, and a **future-dated** row.
- 2 workflow runs.

**Noise that must not count:**
- A second agent in the same org.
- Org-B rows carrying **A1's own agent_id**: audit rows, a $1000 usage row and a workflow run.
- An org-B connector with the same name as A's deleted one.
- An org-A audit row carrying an **org-B policy_id**.
- An org-A run pointing at an **org-B workflow**.

The three tests:
1. **`AggregatesMatchSeedAndDirectQueries`:** for each of 24h, 7d and 30d, every summary field, every per-tool count and cost, policy hits and workflow runs equal **both** hand-derived values **and** independently written direct SQL (including cache terms and `< now()`). It also checks window validation (400) and the default (7d).
2. **`CrossOrg`:** an org-B admin, operator and viewer each get **404** with none of A's strings. A's response holds no org-B names, and the 30d cost is exactly 18.75, so org B's $1000 or A2's $100 would show. An unknown agent returns 404.
3. **`EmptyAndRoles`:** an agent with no history gets null dates, null cost, `[]` lists (not null), real risk and owner. An approver gets 403.

The pool follows the CLAUDE.md lifecycle rule (`t.Cleanup(pool.Close)` registered first). All fixtures are removed, and the org count is back to 6. Full `go test ./internal/...` passes, as do `go vet`, `tsc --noEmit`, `vite build` and `git diff --check`.

## 4. Live verification (real stack, real gateway dispatches, cross-checked with psql)

- **Script:** `lin_live.js`, driven by `lin_run.sh`.
- **Fixtures** were in the Dev Org:
  - users `lin-admin`, `lin-operator`, `lin-approver`, `lin-viewer`, plus `lin-probe` in a separate `linprobe` org;
  - agents `lin-live-agent` and `lin-quiet-agent`;
  - three REST tools on an unresolvable `.invalid` host (no external traffic);
  - an escalate policy and a deny policy scoped to the fixture tool names;
  - a `NOTIFY policy_reload` after seeding and after cleanup.
- **Real dispatches:** an agent API key, then a gateway token, then an MCP SSE session, then 6 `tool_call`s.
- **What the gateway recorded:**
  - 3 dispatch failures on the unmatched tool, which the gateway records as `denied` with no policy;
  - 2 `escalated` by the escalate policy;
  - 1 `denied` by the deny policy.
- **Final run: 42/42 PASS.**
  - **API vs psql:** every window, for both the fixture agent and the real historical agent `b167-redaction-liveverify`. The b167 check covers **11 real escalation-resolution rows**: 24 audit rows count as **13 calls**, equal to psql.
  - **Cost:**
    - The REST tools show "—".
    - The escalate and deny policies are listed with their real hits.
    - b167's real ai_provider cost equals psql. It is $0.00 because all 6 of its usage rows are unpriced, and the UI shows "$0.00 + 6 unpriced" plus the amber notice.
  - **Cross-org, live:** the foreign-org admin gets 404 with no data. The approver gets 403.
  - **UI, admin:**
    - The tabs are Overview, Connections, Lineage, Actions, and opening Agent Detail makes **no `/lineage` request**.
    - For 7d, 24h and 30d, all 9 summary cells equal psql, and every tool table row (calls/allowed/escalated/denied, cost "—") equals psql.
    - b167's 30d cost and calls cells equal psql.
    - The quiet agent shows the honest empty state, with no tables, cost "—" and last seen "—".
    - Overview, Connections (graph) and Actions all still render.
    - There were no 4xx or 5xx responses.
  - **UI, operator and viewer:** the tab is shown and its calls equal psql, with no errors.
  - **UI, approver:** only the Overview tab; `?tab=lineage` falls back; `/lineage` is never requested.
- **Latency:** `/lineage` median 26 ms, max 38 ms over 6 calls in the final run (earlier runs: 16–21 ms median, max ≤ 47 ms).
- **Earlier runs, disclosed:**
  - Run 1: the gateway hadn't reloaded policies, because the fixtures were written by SQL with no NOTIFY.
  - Run 2: the "allowed" tool pointed at the internal API, which the gateway's SSRF guard **correctly refused**.
  - Run 3: passed before the review fixes. Its b167 count of 24 was the double count the review found.
  - Run 4: one check wrongly assumed b167 had direct allowed calls. All 6 of its "allowed" rows are resolutions.
  - In every run, Lineage's numbers equalled psql. The logs are kept as `lin_live_run1_policycache.log`, `lin_live_run2_ssrfguard.log`, `lin_live_run3_prereview.log`, `lin_live_run4_allowedcheck.log` and `lin_live_run5.log`.
- **Not covered live:** a directly **allowed** call.
  - The Dev Org has **0** direct allowed calls in any window.
  - A successful dispatch needs a reachable public upstream, and I did not send traffic to an external service without asking.
  - The allowed path is covered by the real-Postgres tests, and the render path is shared by every decision column.
- **Cleanup proof:** a snapshot of `snapshot6.sql` plus users, policies, conditions, keys, token events, revoked tokens, episodes, usage, in-flight usage, refresh tokens and the audit total, taken before and after each run.
  - The only difference in each run is `audit_log` +6.
  - **Audit rows are never deleted (hash chain), so 36 fixture audit rows across the 6 runs remain in the Dev Org's chain (1,669 to 1,705)**, with agent IDs that no longer resolve. This is the same practice as B-237/B-241.
  - The org count is 6.

## 5. Mutation tests (server; exact-string mutations, a pattern miss aborts)

All **16** were killed, and the files were restored byte-identical. `lin_mutation_final.log` has the full output.

| # | Mutation | Killed by |
|---|---|---|
| M1 | summary loses org scope | test:318, tools 5 calls 5 |
| M2 | tools lose org scope | test:328, 6 tools incl. `lin-foreign` |
| M3 | cost loses org scope | test:321, agent cost wrong |
| M4 | summary calls ignore the window | test:318, calls 7 want 3 |
| M5 | cost for every tool type | test:336, `lin-rest` cost not nil |
| M6 | $0 for an agent with no AI usage | test:452, empty-agent cost not nil |
| M7b | foreign or unknown agent not 404 | test:413, org B got 500 want 404 |
| M8 | workflows lose org scope | test:349, `lin-foreign-wf` listed |
| M9 | unpriced counted as priced | test:321, unpriced 0 |
| M10 | resolution rows counted as calls | test:318, calls 4, denials 2 |
| M11 | NULL tool_name reaches the scan | test:306, 500 |
| M12 | no upper bound, so future-dated usage counts | test:321, cost wrong |
| M13b | per-tool usage guard removed | test:336, `lin-claude-2` cost not nil |
| M14 | policies join loses the org match | test:346, org-B policy listed |
| M15 | workflows join loses the org match | test:349, `lin-foreign-wf-0` listed |
| M16 | tools join loses the org match | test:343, deleted connector resolves to org B's |

Three first attempts were not valid kills, so I re-ran them and disclose them here:
- M7 and M13 broke the build: an unused import and an unused variable.
- An early M4 hit a SQL parameter-type error.

Each was redone as a mutation that compiles and behaves differently.

## 6. Independent reviews (verbatim)

### 6.1 Code review

> ## Agent Lineage review (read-only, no files modified)
>
> **What I ran:** `go vet ./internal/...` in eami-api passed. `npx tsc --noEmit` in eami-ui passed. `go test -run TestAgentLineage` compiled, but all three tests were skipped because neither TEST_DATABASE_URL nor POSTGRES_PASSWORD is set, so none of the real-Postgres tests have actually been run.
>
> ### High
>
> **1. A NULL `token_usage.tool_name` makes the whole endpoint return 500.** The problem is at `eami-api/internal/store/agent_lineage.sql.go:171` and `:189`.
> - `ListAgentLineageToolCosts` groups by `tu.tool_name` and scans it into a Go `string`.
> - The column is nullable (`schema/migrations-v2/000001_baseline.up.sql:416`).
> - Production writes NULL on purpose whenever the tool didn't resolve: `store/token_usage.sql.go:72-76` (B-108) turns an empty ToolName into NULL, and `reports.go:351` passes it through. FinOps handles this with `COALESCE(tu.tool_name,'unknown')`.
> - **Failure:** if an agent has even one unresolved-tool usage row in the window, pgx fails with "cannot scan NULL into *string". The errgroup then fails and `GetAgentLineage` returns 500 "failed to load agent lineage", so the tab shows "Failed to load lineage" for that agent permanently.
> - **Why the tests miss it:** the test seeds the "empty tool_name" row as `""` (`agent_lineage_pg_test.go:204`). Production never writes `""`.
> - **Fix:** add `WHERE tu.tool_name IS NOT NULL` (only named ai_provider tools are looked up anyway), or `COALESCE(tu.tool_name,'')`, or scan into `pgtype.Text`. Change the test fixture to insert a real NULL.
>
> ### Medium
>
> **2. "Calls", "Decisions" and per-tool counts count every escalated call twice.** The problem is at `agent_lineage.sql.go:54`, `:125-129` and `:209`.
> - For an escalated call, the gateway writes a second audit row when the approval resolves (`eami-gateway/cmd/gateway/dispatcher.go:736-795`). That row is a clone with the same tool_name and policy_id, decision `allowed`/`denied`, and `approval_id` set.
> - **Failure:** one escalated-then-approved call shows as Calls=2 (Escalated 1 + Allowed 1) and policy Decisions=2.
> - A call that was approved but then failed technically is also counted in "Denials (30d)".
> - The hand-derived test expectations are internally correct, but they treat each row as a call, so they can't catch this.
> - **Fix:** count calls with `FILTER (WHERE NOT (a.approval_id IS NOT NULL AND a.decision <> 'escalated'))`. First confirm the original escalated row has no approval_id. Keep the allowed/denied splits as final outcomes. Alternatively, relabel the columns as audit decisions rather than calls. Add a fixture for an escalated call plus its resolution row.
>
> ### Low
>
> **3. The empty-state condition ignores agents that have usage but no audit history.** `AgentLineageTab.tsx:89`: `neverActive = last_seen == null && workflows.length === 0`.
> - **Failure:** an agent with token_usage rows but no audit_log rows (usage ingested through `/reports`) gets a real cost in the summary grid and "No recorded activity" directly below it.
> - **Fix:** also require `s.cost_usd_window == null`.
> - Minor related point: the file header says "an explicit empty state, not a grid of zeros", but the zero-count summary grid still renders above the empty state. The zeros are real counts, so this is acceptable, but the comment overstates it.
>
> **4. Cost boundaries and the unpriced definition drift slightly from FinOps.** See `agent_lineage.sql.go:38`, `:90` and `:176`.
> - FinOps filters `[from, to)`. Lineage uses `>= since` with no upper bound, and `reports.go` accepts a `recorded_at` supplied by the client.
> - **Failure:** future-dated rows are counted, so the lineage figure won't cross-check against FinOps for the same span.
> - `unpricedExpr` (`cost_usd IS NULL AND mp.model IS NULL`) is stricter than FinOps' B-112 `mp.model IS NULL`. This is arguably more correct, but the "kept textually identical" comment only covers `costExpr`.
> - **Fix:** add `AND recorded_at < $now` and pass `now` in. Document the unpriced difference.
> - `costExpr` itself matches FinOps `agentQ` exactly, including the cache-tier terms.
>
> **5. Per-tool $0.00 vs the agent-level "—" rule are inconsistent.** `agent_lineage.go:196-201`.
> - An ai_provider tool with no usage rows at all (for example, every call denied, or usage recorded under a NULL tool_name) shows "$0.00".
> - The agent level uses the all-time `RowsEver > 0` guard to decide between "—" and a value. $0 is defensible for denied-only calls, but the two levels apply different rules.
> - **Fix:** apply the same all-time-rows guard per tool, or state the rule explicitly.
>
> **6. Changing the window hides the picker.** `AgentLineageTab.tsx:76` with `useAgents.ts` `useAgentLineage`.
> - A new queryKey sets `isLoading`, so the whole tab, picker included, is replaced by a spinner on every window change. That flickers and loses focus on the select.
> - **Fix:** use `placeholderData: keepPreviousData` in the hook, or render the picker in the loading state as well.
>
> **7. Test gaps.**
> - The gpt-4o expectation depends on the mutable shared `model_pricing` row (0.005/0.015), which can be edited in Settings.
> - There is no fixture for cache-tier tokens, a NULL tool_name (#1), or escalation resolution rows (#2).
> - The "independent" direct cost query (`_test.go:319`) leaves out the cache terms, so it isn't independent on that path.
>
> **8. Performance: every aggregate scans the agent's full history.** The three audit_log queries and the token_usage count scan all of an org's audit/usage history, with no agent_id index, across every monthly audit_log partition. Connections already does the same, so this is not a regression, but Lineage runs 6 such queries per load.
>
> ### Nit
> - `agent_lineage.go:27`: the `rfc3339` constant duplicates `time.RFC3339`.
> - `_test.go:47-51`: when `Ping` fails, `t.Skipf` fires before `pool.Close` is registered, so the pool leaks on the skip path. This matches the template.
> - The test cleanups `DELETE FROM audit_log` under the insert-only RLS policy, and seed back-dated (40-day-old) rows into the shared hash chain. This is the existing repo pattern, but it's worth noting.
> - Timestamps are rendered with `toLocaleString`, not mono type (§3 lists timestamps as Plex Mono). Minor.
>
> ### Checked and found correct
> - **No tool double-count:** `gateway_tools` has `UNIQUE (org_id, name)` (baseline:237) and no migration drops it, so the LEFT JOIN plus GROUP BY cannot double-count.
> - **Query details:** `$3`/`$4` usage, the FILTER windows, and the ordinal ORDER BYs (4/8, 4, 3) are correct.
> - **Org scoping:** every join also matches org, which is stricter than Connections.
> - **Nulls:** first/last seen are NULL-safe. `workflow_runs.started_at` and `audit_log.timestamp`/`tool_name` are NOT NULL, so the `time.Time`/`string` scans are safe.
> - **Errors:** `writeError` conventions and the 404-before-work pattern match `GetAgentConnections`.
> - **Hand-derived numbers:** I re-derived every expectation for 24h/7d/30d (calls 2/3/5, escalations 2, denials 1, costs 1.75/1.77/3.77, lin-claude 0.50/0.52/2.52, policy hits 1/2/2, runs 1/1/2) and they hold.
> - **Pool lifecycle:** follows the CLAUDE.md rule (`t.Cleanup(pool.Close)` registered first). Per-org token_usage/audit deletes and per-run deletes run before the org cascade.
> - **UI:**
>   - Hook calls come before the early returns.
>   - Row IDs are unique.
>   - Retry uses `Button isLoading`.
>   - Approvers are hidden by `TAB_VISIBLE` and a `?tab=lineage` URL falls back to Overview.
>   - The tab mounts only when active.
>   - No toast is needed.
>   - Design system is followed: the `grid-cols-3` §7.6a grid with 9 cells fills cleanly; `text-2xs`, `shadow-l1`, `ink` tokens exist in `tailwind.config.ts`; §4 L1 elevation is used.
>   - The amber unpriced banner mirrors FinOps and reflects real state.
>   - Risk and Owner are plain labels. No data classification or redaction count is shown.
> - **No regressions:** Connections, Overview and Actions are unchanged; the router diff is one line in the correct read group.
>
> **Verdict:** The change set is well structured and mostly faithful to the founder's requirements, and the test expectations are arithmetically correct. It should not merge as-is: finding #1 is a real production 500 for any agent with an unresolved-tool usage row, and the tests hide it by seeding `''` instead of NULL. Finding #2 should be fixed or explicitly relabelled before these numbers are presented as "calls", because escalated calls are counted twice. After #1 and #2, and ideally #3 and the test gaps in #7, it would be ready. The real-Postgres tests also still need to be run against a database, since they were skipped here.

### 6.2 Security review

> ## Security review: Agent Lineage (uncommitted change set, read-only review, no files modified)
>
> **Verdict: I found no cross-org leak and no Critical, High or Medium issues. It is safe to ship as written.** Every query is scoped by both org and agent, every join matches on org, the agent is checked against the caller's org before any aggregate runs, and the route sits in the right role group. There are 3 Low findings and 4 Info findings, none of which block.
>
> ### What I checked
>
> **Query scoping** (`eami-api/internal/store/agent_lineage.sql.go`)
> - All six queries filter on `org_id = $1 AND agent_id = $2`: summary L60, cost L94, tools L133, tool costs L176, policies L213, workflows L250.
> - Joins:
>   - `gateway_tools` is joined on `gt.org_id = a.org_id AND gt.name = a.tool_name` (L132). `UNIQUE (org_id, name)` in `schema.sql:234` means the join can't duplicate rows.
>   - `policies` is joined on `p.id = a.policy_id AND p.org_id = a.org_id` (L212).
>   - `workflows` is joined on `w.id = wr.workflow_id AND w.org_id = wr.org_id` (L249).
>   - `model_pricing` is joined on `model` only (L93, L175). That table is global pricing, not tenant data, which is the same as FinOps. A missing match can't leak anything.
> - All parameters are bound (`$n`). The only string concatenation is the fixed `costExpr` and `unpricedExpr` constants, so there is no injection path.
>
> **Authorization order** (`eami-api/internal/api/agent_lineage.go`)
> - The handler calls `GetAgent(ctx, id, uc.OrgID)` at L111 (`WHERE id=$1 AND org_id=$2`, `agents.sql.go:47`) and returns 404 "agent not found" before the errgroup at L130 runs any aggregate. The 404 body is fixed text with no data.
> - `uc.OrgID` comes from the JWT, never from the request.
>
> **Route and role** (`eami-api/internal/api/router.go:420`)
> - The route is in the `requireRole("admin","operator","viewer")` + `viewerReadOnly` group, the same group as `/connections` (L419), `/v1/audit` (L428) and `/v1/finops/*` (L435-436).
> - It is not in the approver-inclusive group at L399-406, so approvers get a 403, in line with B-253 Q-E.
> - The whole tree is under `jwtMiddleware` (L236-237), so an unauthenticated caller can't reach it.
> - The UI gate `can.viewAgentLineage` in `rbac.ts` matches the server.
>
> **Error responses**
> - A DB failure in any aggregate returns a generic "failed to load agent lineage" (L153). This is better than `ListAudit`, which echoes `err.Error()` (`audit.go` ~L99/104).
> - A `GetAgent` DB error comes back as 404, which hides a 500 but discloses nothing.
>
> **`window` parameter**
> - It is checked against a whitelist map (L104) and anything else gets a 400. Only the resulting `time.Duration` reaches SQL, as a bound timestamp. There is no injection path.
>
> **Newly exposed data:** none. Every field is already readable by a viewer:
> - risk_tier and owner via `GET /v1/gateway/agents/{id}`
> - tool_name, decision, policy_id, timestamp and agent_id per row via `/v1/audit`
> - policy names and actions via `/v1/gateway/policies`
> - workflow names via `/v1/gateway/workflows`
> - per-agent and per-tool cost via `/v1/finops/summary` and `/timeseries?agent_id=`
>
> Lineage only pre-aggregates data the viewer can already reach.
>
> **XSS:** none. All values are rendered as React text children or `title` attributes (`AgentLineageTab.tsx` L97-124, L136-150). `ActionBadge` renders `{action}` as text and uses it only as a lookup key into a class map. There is no `dangerouslySetInnerHTML` in `components/agents` or `components/common`. The only `Link` goes to a fixed path, and the fetch URL uses the server-returned agent UUID and a typed window value.
>
> **Tests** (`eami-api/internal/api/agent_lineage_pg_test.go`)
> - The foreign-org leak is genuinely pinned. Org B seeds audit rows (`lin-claude` escalated, `lin-foreign` denied), a $1000 token_usage row and a workflow run, all carrying A1's agent_id.
> - The aggregate test's exact values would break if any of those leaked: tool count 3, escalations_30d 2, the exact cost figures, 1 workflow with an exact run count.
> - The cross-org test checks that an org-B caller in each of admin, operator and viewer gets a 404 with none of org A's strings, and that org A's response contains no org-B names.
> - Approver → 403 is tested through the real `srv.Handler()` router.
> - Pool lifecycle follows the mandated pattern: `t.Cleanup(pool.Close)` is registered first (L51).
> - I did **not** run the real-Postgres tests, because they need `POSTGRES_PASSWORD` and I did not read `.env`.
>
> **Build check:** `go vet ./internal/...` in `eami-api` is clean. `golang.org/x/sync` is already a direct dependency, and `go.mod` is unchanged.
>
> ### Findings
>
> 1. **Low: unbounded full-history audit scans, fanned out six ways per request.**
>    - Where: `agent_lineage.sql.go` L51-61, L120-136, L207-216; handler L130-151.
>    - Problem: the "ever" aggregates (tools_ever_touched, first/last seen, calls_total, last-hit/last-run) have no time bound, so three of the six queries scan every `audit_log` partition for the org. The only usable index is `idx_audit_log_org_ts(org_id, timestamp)`, with `agent_id` filtered row by row, since there is no agent_id index (B-265 is queued for that). Each request also takes up to 6 pooled connections at once through the errgroup.
>    - Scenario: an authenticated viewer (the lowest role that can reach this) scripts repeated requests against an agent in a large org. Each request does 3 full-history org scans and holds 6 connections, which can starve the API's pgx pool for other tenants. There is no per-route rate limiter; only the audit export has one (`auditExportLimiter`, 4/min).
>    - Mitigation: only authenticated same-org users can do this, and `/connections` already does two such scans per request, so this adds scale rather than a new class of risk.
>    - Fix: land B-265, an `(org_id, agent_id, timestamp)` index. Optionally add a per-org limiter like `auditExportLimiter`, or run the queries sequentially or in a single transaction to cap connection use.
>
> 2. **Low: tests don't pin the org match on each join.**
>    - Where: `agent_lineage_pg_test.go` L160-218.
>    - Problem: the seed never creates a same-named `gateway_tools` row in org B, an org-A audit row whose `policy_id` points at an org-B policy, or an org-A `workflow_runs` row pointing at an org-B workflow. So deleting `AND p.org_id = a.org_id` (L212), `AND w.org_id = wr.org_id` (L249) or `gt.org_id = a.org_id` (L132) would still pass.
>    - Scenario: a future refactor drops one of those predicates, and a foreign policy or workflow name leaks through an audit_log row with a mis-attributed id. `policy_id` and `workflow_id` have no org-matched FK, and nothing in the schema stops such a row existing.
>    - Fix: seed one row of each kind and assert that it is absent or gets a nil `tool_id`.
>
> 3. **Low: a `GetAgent` DB error is reported as 404** (`agent_lineage.go:111-114`).
>    - This is not a disclosure problem; it is fail-closed. But a DB outage looks like "agent not found" to users and monitoring.
>    - Fix: return 404 only for `pgx.ErrNoRows` and a generic 500 otherwise. This is optional and cosmetic from a security standpoint.
>
> 4. **Info: an existing sibling query is missing the org match.** `listAgentPolicyConnections` in `agent_connections.sql.go:88` (the committed `/connections` endpoint) joins `policies p ON p.id = a.policy_id` with no `p.org_id` match. Lineage correctly adds it. This is outside this change's scope; worth logging in `NOTES.md` or `BACKLOG.md` as a defense-in-depth fix.
>
> 5. **Info: tool cost is matched by name only.** Per-tool cost is keyed by `tool_name` (handler L182-201), so a deleted and re-created ai_provider connector with the same name inherits the old usage cost. It stays within one org, so this is a correctness issue, not a security one.
>
> 6. **Info: `schema.sql:217`'s `gateway_tools.type` CHECK doesn't list `ai_provider`.** The test seeds that type, so a migration must extend it. This is schema drift between `schema.sql` and the migrations, not a security issue.
>
> 7. **Info: no general rate limit on authenticated read routes.** This existed before the change, and it is the reason finding 1 matters at all.
>
> **Totals:** Critical none, High none, Medium none, Low 3 (findings 1-3), Info 4 (findings 4-7).

### 6.3 Fix-delta re-review (code + security)

> RE-REVIEW: Agent Lineage fixes (code + security). No files were modified.
>
> Checks run:
> - `go vet ./internal/api/ ./internal/store/` passed.
> - `npx tsc --noEmit` in eami-ui exited 0.
> - `go test ./internal/api/ -run TestAgentLineage -count=1 -v` compiles, but all 3 tests SKIPPED: no TEST_DATABASE_URL/POSTGRES_PASSWORD, and I did not read .env. The real-Postgres expectations below come from tracing the seed by hand; they were not executed.
>
> Verification of the six fixes:
> 1. NULL tool_name crash: FIXED. `ListAgentLineageToolCosts` has `tu.tool_name IS NOT NULL` (agent_lineage.sql.go:196). The agent total query has no such filter, so NULL-tool usage still counts there. The fixture stores a real NULL (test:114-117, 251). audit_log.tool_name is NOT NULL in the schema, so the same crash cannot happen in the audit-side scans.
> 2. Double counting after an escalation resolves: FIXED and applied consistently.
>    - `isCall` is used in: summary calls and denials_30d; per-tool calls, allowed, denied, calls_total and last_call_at; policy hits and last_hit_at.
>    - escalations_30d and per-tool escalated count escalated rows with no filter. That is correct because a resolution row is never 'escalated'.
>    - ToolsEverTouched and first/last seen cover all rows. A resolution row always has the same tool as its escalation, so the distinct-tool count is unaffected.
>    - Gateway sweep: `re.ApprovalID = approvalID` at eami-gateway/cmd/gateway/dispatcher.go:740 (resolution clone only) is the only place ApprovalID is set. All other audit.Entry builders (dispatcher.go:565, 929, 975, 1024, 1072) leave it empty. approval/router.go never writes audit rows. The only INSERT INTO audit_log in the repo is audit/writer.go:242.
>    - So no other row shape can double count today.
>    - The UI footnote (AgentLineageTab.tsx:199-200, "Each call counts once, by the gateway's first decision…") matches the semantics.
> 3. Cost window: FIXED.
>    - `usageWindow` is `>= $3 AND < $4`, and the handler passes `now` as $4 to both cost queries, so every number uses one instant.
>    - Per-tool cost is shown only when `RowsEver>0 && ToolType=="ai_provider"`, and is nil for a deleted connector.
>    - The cache columns are NOT NULL DEFAULT 0 (migrations-v2/000011), so the cache terms cannot turn a row's cost into NULL.
> 4. GetAgent errors: FIXED. `errors.Is(err, pgx.ErrNoRows)` gives 404; any other error gives 500. store.GetAgent returns the Scan error unwrapped, so errors.Is matches.
> 5. UI: FIXED.
>    - `neverActive` now also requires `cost_usd_window == null`.
>    - The v5 `placeholderData(prev, prevQuery)` keeps previous data only when the agent id matches; tsc accepts it.
>    - Timestamps render in font-mono through `Time`.
>    - `last_call_at` / `last_hit_at` are `string | null` in the types and rendered as "—" when null.
> 6. Tests: I traced the new fixtures by hand and every expectation checks out:
>    - Calls: 3/4/6 for 24h/7d/30d. Escalations_30d = 2, denials_30d = 1, tools ever touched = 4.
>    - lin-claude cost: 0.50/15.50/17.50. The fixture model prices 1+2+3+4+5 = 15.00.
>    - The future-dated $50 row is excluded. The 30d agent total is 18.75.
>    - Policy hits: 1/2/2. Workflow runs: 1/1/2.
>    - The org-B policy_id and the org-A run pointing at an org-B workflow are excluded by the org-matched joins.
>    - The org-B tool with the same name "lin-gone" does not resolve for org A.
>    - SQL parameters are consistent within each query:
>      - Summary: $3 = since, $4 = since30d.
>      - Cost queries: $3 = since, $4 = now.
>      - Tools, policies and workflows: $3 only.
>      - Every parameter is referenced, so Postgres can infer its type.
>    - No leftover references to removed identifiers; vet and tsc are clean.
>    - Pool rule: `t.Cleanup(pool.Close)` is registered first (test:51), with no plain `defer pool.Close()`. Cleanup order works out: model_pricing DELETE, then workflow_run DELETEs, then the per-org token_usage/audit_log DELETEs, then seedTestOrg's org delete, then pool close. No foreign key links token_usage.model to model_pricing, so deleting the pricing row first is safe.
>
> Findings:
>
> High: none.
>
> Medium: none.
>
> Low:
> 1. Unconditional DELETE of the model_pricing fixture (agent_lineage_pg_test.go:200-206).
>    - Failure scenario (a): if a real 'lineage-test-model' row already existed, ON CONFLICT DO NOTHING keeps it with its own rates. The 15.00 expectations then fail, and cleanup still deletes that row, which is shared global state.
>    - Failure scenario (b): two runs of the suite at the same time against the same shared dev database (two developers, or CI plus a local run). One run's cleanup deletes the other run's fixture mid-test, and that run fails intermittently (lin-claude cost 0.50 instead of 15.50; unpriced count off by one).
>    - Fix: use a per-run name such as `"lineage-test-model-"+uuid.NewString()[:8]`, passed through to `usageCache`. Or check `RowsAffected()==1` and call t.Fatalf otherwise, registering the DELETE only when this run inserted the row.
> 2. Stale data shown with no loading indicator after a window change (AgentLineageTab.tsx:67-82). When the window changes, placeholderData keeps the previous window's numbers on screen with no sign they are stale. The labels follow `data.window` (the old window) while the picker already shows the new one, so they briefly disagree. Fix: read `isPlaceholderData` from the query and dim the content or show a small spinner beside the picker while it is true.
> 3. Lineage queries scan the whole org's audit history on every tab open (agent_lineage.sql.go; not introduced by these fixes). The query filters `org_id AND agent_id` with no time bound, because of the all-time counts, first/last seen, and calls_total. audit_log has no (org_id, agent_id) index, only (org_id, timestamp), so each open reads the org's entire audit history across all partitions. Three such queries run in parallel per request. Fine at current volumes; log it in NOTES/BACKLOG for when volume grows (an index on (org_id, agent_id, timestamp) would fix it).
>
> Nit:
> 4. Stale seed comment (agent_lineage_pg_test.go:242-244). It still describes "gpt-4o 1000/1000 priced at … 0.02 (2d)" and "empty tool_name". Those rows were replaced by the lineage-test-model fixture (15.00) and a NULL tool; the comment should be updated.
> 5. The test's "independent" direct queries use a different predicate (test:359, 361, 385). They filter with `approval_id IS NULL` instead of the production `isCall` predicate. The two agree for every row the gateway writes today, but the test would not catch a divergence if the gateway ever wrote an 'escalated' row with approval_id set. Acceptable as written; optionally add a comment noting that the two are equivalent only under the dispatcher.go:740 invariant.
> 6. The "Last seen —" line can read oddly (AgentLineageTab.tsx:181). An agent with workflow runs or token usage but no audit rows shows "No calls in the … Last seen —." Consider hiding the "Last seen" part when last_seen is null.
>
> Verdict: all six fixes are correct and consistent, and they introduce no new High or Medium defects. No other audit row shape can double count under the current gateway. Ready to commit. Addressing Low #1 (unique fixture model name) is recommended first, because it is the only item that can cause intermittent test failures or remove a shared pricing row. The real-Postgres tests still need one run against a live database before this is called verified.

All three reviewers noted that the real-Postgres tests skipped in their sandboxes. They were run here against real Postgres after every change: the focused tests, all 16 mutation runs, and the full `./internal/...` package, which passes.

## 7. Disposition of findings

| Finding | Action |
|---|---|
| CR-1 NULL tool_name gives a 500 (High) | **Fixed:** `tool_name IS NOT NULL` on per-tool cost; the fixture is a real NULL; M11 pins it |
| CR-2 escalations double-counted (Medium) | **Fixed:** the `isCall` rule plus a UI footnote; resolution fixtures; M10 pins it; live on b167's 11 real resolution rows (24 rows are 13 calls) |
| CR-3 empty state vs usage-only agent | **Fixed** |
| CR-4 no upper cost bound; unpriced definition | **Fixed:** `[since, now)` (M12); the unpriced difference is documented in code |
| CR-5 per-tool $0 vs agent "—" | **Fixed:** usage-ever guard per tool (M13b) |
| CR-6 picker vanishes on window change | **Fixed:** same-agent `placeholderData`, and stale data dimmed with `aria-busy` (RR-2) |
| CR-7 test gaps | **Fixed:** per-run fixture pricing model with cache tiers (RR-1), NULL tool, resolutions, future-dated row, cache terms in the direct query |
| CR-8 / SR-1 / RR-3 full-history scans | **B-265** (index, queued). A per-route limiter is logged in NOTES.md |
| CR nits | `rfc3339` becomes `time.RFC3339` and timestamps are mono (**fixed**). Pool-on-skip matches the repo template and is **not changed**. The test audit DELETE is the existing pattern (**noted**) |
| SR-2 joins' org match not pinned | **Fixed:** three mismatch fixtures; M14, M15 and M16 pin them |
| SR-3 DB error reported as 404 | **Fixed:** 404 only for `ErrNoRows` (M7b) |
| SR-4 `/connections` policy join lacks the org match | **Logged** in NOTES.md (out of scope) |
| SR-5 tool cost keyed by name across a recreated connector | **Logged** in NOTES.md |
| SR-6 `schema.sql` `gateway_tools.type` drift | **Logged** in NOTES.md |
| SR-7 no general read rate limit | **Logged** in NOTES.md |
| RR-4, 5, 6 | **Fixed:** comment, equivalence note, "Last seen" shown only when known |

## 8. Addendum (2026-09-29): the allowed-call path, verified live

On founder approval, **exactly one real call** went to postman-echo.com.
- **Why a fixture tool:** it went through `lin-live-echo`, a throwaway REST tool with `base_url https://postman-echo.com` and `read → GET /get`, with no policy matching it.
- **Why not `manual_verify_tool`:** the Dev Org's active "manual verify" escalate policy matches it, so a call through it would have escalated rather than been allowed.
- **What left the machine:** `Content-Type` plus the JSON body (tool name, action and an opaque, now-expired MCP session ID). No agent token and no credentials: the fixture tool had none, and the gateway only adds `Authorization` from a tool's own credentials.
- **Script:** `lin_allowed.js`, driven by `lin_allowed_run.sh`. **4/4 PASS:**
  - no policy matched the tool;
  - the gateway wrote exactly one audit row: `lin-live-echo | allowed | no policy | no approval`, a **direct allow**, with the real echo returned over SSE;
  - the API's 24h calls and allowed counts equal psql (1/1), with cost "—" for a REST tool;
  - the UI tool row shows Calls 1, Allowed 1, Escalated 0, Denied 0, Cost "—".
- **Cleanup:** proven by the snapshot diff. The only difference is `audit_log` +1, which stays because of the hash chain. That brings the **total fixture audit rows left by this brief's live runs to 37** (1,669 to 1,706). The org count is 6.

**Observation for the founder (now queued as B-266):** the gateway records a **dispatch failure** (an upstream error, or the SSRF guard refusing the target) as `denied` with no `policy_id`, the same vocabulary as a policy denial (B-121's precedent). Lineage faithfully shows these as denials. It would need new audit vocabulary to separate them, which is out of scope ("no new instrumentation").
