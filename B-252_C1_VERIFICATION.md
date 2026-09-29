# B-252 C1 — Agent handoff from Assets: verification record

**Date:** 2026-09-29 · **By:** Claude Code · **Roadmap:** Horizon 1, CMDB completion (B-252 step C1; `IA_CONSOLIDATION_MIGRATION_PLAN.md` Q4)

## ⚠ Deliberate deviation from what was approved: the filter is `id`, not `agent_id`

The founder approved "Option A — add the **agent_id** filter to `/v1/cmdb/assets`". **I implemented it as `?id=`, a deliberate call rather than an exact match to the approval.**

- **Why:** `/v1/cmdb/assets` is the CMDB list for **all three asset kinds** (endpoints, agents, tools), and each row's `id` is that asset's own ID. An `agent_id` parameter would:
  - read as agent-only on a kind-agnostic endpoint;
  - collide in meaning with the `agent_id` field that endpoints carry for their discovery identity (`endpoints.agent_id`, a free-text scanner ID);
  - need twin `endpoint_id` and `tool_id` parameters when Endpoint Detail (C2) and Tool Detail (C5) need the same single-row read.
- **How it's used:** with `kind=agent`, as in `/v1/cmdb/assets?kind=agent&id=<agent uuid>`. That gives exactly the behaviour approved: one agent's CMDB row, scoped to the org.
- **Contract:** the parameter is not in `api/openapi.yaml` (Architect-EAMI's file). The drift is logged in NOTES.md, and the UI calls it through the documented `apiFetch` escape hatch.
- **If the founder prefers `agent_id`:** renaming it is a one-line handler change plus the hook and test.

## 1. Part A findings (reported before building; founder decisions 2026-09-29)

- **A.1 Tab bar:** the real order is Overview / Connections / **Lineage** / Actions. A fifth tab needs no layout change (a flex row with `gap-6`; keyboard navigation and the visibility map are generic). **The approved order is Overview, Connections, Lineage, Classification, Actions** (destructive last).
- **A.2 Links to `/gateway/agents/{id}`:** the **only** in-app link was the Agents list row click (`AgentsPage.tsx:308`), plus the route itself. The other `/gateway/agents` references point at the list page, which stays until C9.
- **A.3 `AssetClassificationPanel`:** it was private to `AssetsPage.tsx` and tied to a slide-over. It needed the asset's CMDB row, and there was no way to fetch one row: the agent API has no `ci_type_id`, and `q` is a substring search.
  - Writes are **admin-only** (router admin group).
  - Reads (`/v1/cmdb/classifications`, `/v1/cmdb/assets`) allow admin, operator and viewer.
- **A.4 Orphaned panels:** `AgentAssetPanel` and `ToolAssetPanel` were still referenced only by themselves.
- **A.5 `agent_id` in the three APIs:**
  - Audit: optional; 69 rows have none.
  - FinOps: a string, `""` for usage with no agent.
  - Memory: optional.
  - **46 Dev Org audit rows point at deleted agents.** Audit rows open a panel on click. On Memory, the agent name sat inside the expand `<button>`.
- **Founder decisions:**
  1. Option A (the filter; see the deviation above).
  2. Check stale links against the agents list; show "agent no longer exists" as plain text.
  3. Breadcrumb "Assets › {agent}".
  4. The tab is hidden from approvers and read-only for operator and viewer; the tab order is approved.

## 2. What was built

- **Server:**
  - `store/cmdb.sql.go`: `CMDBAssetFilter.ID` and `a.id=$n` inside the org-scoped CTE.
  - `api/cmdb.go`: `?id=` is parsed with `parseOptionalUUID` (400 if invalid). The sidebar counts reset `ID` like the other navigation filters, so the counts are unchanged.
- **UI, shared classification:**
  - `components/cmdb/AssetClassificationForm.tsx` (new): the panel's logic extracted as-is. That covers the resolved classification, the admin type picker for the asset's kind, the default reset, the toasts, `Button isLoading`, and the close/Cancel buttons disabled while pending (header rendered in-form to keep that).
  - `AssetsPage.tsx`:
    - Agent rows navigate to `/assets/agents/:id`.
    - Endpoint and tool rows keep the panel, which now shows "**Full detail page coming soon — classification available here for now.**"
    - It uses the shared form, `KIND_LABEL` and the error helper.
- **UI, Agent Detail:**
  - `components/agents/AgentClassificationTab.tsx` (new), with `useCMDBAsset` in `hooks/useCMDB.ts`. Its key is `['cmdb-assets', {kind, id}]`, so a save's `['cmdb-assets']` invalidation refreshes it; the form is re-seeded by key.
  - `lib/rbac.ts`: `viewCMDB` (admin/operator/viewer) and `classifyAsset` (admin).
  - `AgentDetailPage.tsx`:
    - tabs are now Overview, Connections, Lineage, **Classification**, Actions;
    - Classification is hidden from approvers and mounted only while open;
    - the breadcrumb is **Assets › {agent}**.
- **Routes:**
  - `router.tsx`: `/assets/agents/:id` renders Agent Detail. `/gateway/agents/:id` is an `AgentDetailRedirect` (`Navigate replace`, carrying `?search` and `#hash`) and is kept only for bookmarks and external links.
  - `AgentsPage.tsx` links to the new path directly.
- **Cross-links:** `components/agents/AgentLink.tsx` (new) checks the `agent_id` against the org's (unpaginated) agents list.
  - A real link if the agent exists.
  - Otherwise the name plus "· agent no longer exists" in plain text.
  - Plain text while loading or when there's no ID.
  - `stopPropagation` keeps clickable rows from also firing.
  - Used on the Audit agent column, the FinOps by-agent table, and Memory episodes. On Memory, the metadata line moved outside the expand button (still one hover row), and the button gained `aria-expanded`.
- **Deleted:** `components/cmdb/AgentAssetPanel.tsx` and `components/cmdb/ToolAssetPanel.tsx`.

## 3. Tests: real Postgres

`eami-api/internal/api/cmdb_id_filter_pg_test.go` (`newWorkspaceTestEnv`, whose pool follows the CLAUDE.md lifecycle rule):
- `?kind=agent&id=<target>` returns **exactly one** row, the target, even with a near-namesake in the same org (`c1-agent-2`, which a name search would also match).
- A same-named agent in **another org**, queried by its ID, returns **nothing**.
- The sidebar counts are identical with and without `id`.
- **Reclassification returns the UPDATED value on the very next read.** This answers the founder's step-1 question.
  - The test first asserts that the resolved type **is** the default.
  - An admin then reclassifies the agent to a **newly created, non-default** type ("C1 Reclassify Target").
  - The very next `?id=` read must return that new type's ID **and** name, with source `explicit`. A stale or pre-edit row can't pass, because the value differs from the default.
  - An admin reset then returns the default type with source `default` on the next read.
  - *(The first draft reclassified to the default type itself, which proved only that the source flipped. It was strengthened before the reviews.)*
- `id=not-a-uuid` returns 400, and an approver gets 403.
- All 12 CMDB tests pass.

## 4. Mutation tests (exact-string; a pattern miss aborts)

All 4 were killed, and the files were restored byte-identical. The full output is in `c1_mutation.log`.

| # | Mutation | Killed by |
|---|---|---|
| M1 | filter never applied (store) | test:72, multiple rows returned |
| M2 | `id` never parsed (handler) | test:72 |
| M3 | sidebar counts narrowed by `id` | test:86, counts 1 vs 2 |
| M4 | invalid `id` accepted | test:118, 200 want 400 |

## 5. Live verification (real stack, real logins, cross-checked with psql)

- **Script:** `c1_live.js`, driven by `c1_run.sh`.
- **Fixtures** were in the Dev Org:
  - users `c1-admin`, `c1-operator`, `c1-approver`, `c1-viewer`;
  - agent `c1-agent`;
  - a fixture classification type created **and deleted through the real API**.
- **Cross-links** read real agents' existing history only.
- **Result: 33/33 PASS.**
- **Admin: the handoff and the tab.**
  - The Assets agent row opens `/assets/agents/:id`.
  - The tabs are exactly Overview, Connections, Lineage, Classification, Actions.
  - The breadcrumb "Assets" links to `/assets`, and the sidebar highlights Assets.
  - The Classification tab's resolved value equals psql (`AI systems / Governed agent | default`).
  - **The admin selects the fixture type and saves.** The DB `ci_type_id` equals the fixture, a toast appears, and **the tab immediately re-renders `AI systems / C1 Fixture Type | explicit`, equal to psql, with no reload.**
  - After a reset, the DB value is NULL and the tab shows the default again.
- **No regression:** Overview, Connections, Lineage and Actions all render at the new route.
- **Redirect:** `/gateway/agents/:id?tab=lineage` lands on `/assets/agents/:id?tab=lineage` with the Lineage tab selected.
- **Direct link:** an Agents list row click goes **straight** to `/assets/agents/:id`; the navigation log shows no `/gateway/agents/:id` hop.
- **Interim note:** an endpoint row (`Bhargav_tej`) and a tool row (`b046-live-verify`) stay on `/assets`, and the panel opens with the transition note.
- **Cross-links on real data:**
  - **Audit:** the `b167-redaction-liveverify` link's href is `/assets/agents/<its audit agent_id>`, and it lands on Agent Detail. A **deleted** agent (`lin-live-agent`) shows plain "agent no longer exists" with no link.
  - **FinOps:** the same agent's link matches its `token_usage` agent_id and lands on Agent Detail.
  - **Memory:** the link matches the episode's agent_id and lands on Agent Detail, and the row still expands through its button.
- **Operator and viewer:** the Classification tab is read-only (value shown, no picker, no Save). Neither session had any 4xx or 5xx.
- **Approver:** only Overview; `?tab=classification` falls back and is rewritten; **no `/v1/cmdb` request** is made.
- **The admin session** had no 4xx or 5xx.
- **Cleanup proof:** a `snapshot6.sql` snapshot plus users, refresh tokens, CI types and categories, audit count, and every agent's `ci_type_id`, taken before and after. **SNAPSHOT_IDENTICAL.** This run created no audit rows.

## 6. Independent reviews (verbatim)

The `id`-vs-`agent_id` deviation (top of this file) was written down **before** the reviews, and both reviewers were asked to judge it explicitly. **Code review: "Sound."** **Security review: no security implication.**

The auto-mode safety check was intermittently unavailable during this brief. It blocked the live run and review launches for a while, and both reviewers note tests they couldn't run in their sandbox. Every such test was run here against real Postgres: the focused and full packages, and the mutations. The harness flagged that the check was down during the security reviewer's run, so I confirmed with `git status` that it modified nothing.

### 6.1 Code review

> ## Code review: B-252 C1 (Agent handoff from Assets)
>
> **What I ran:** `npx tsc --noEmit` in eami-ui exited 0, and `go vet ./internal/api/ ./internal/store/` exited 0. I could not run `go test -run CMDB` because the auto-mode classifier failed to answer twice. The real-Postgres test result below is therefore from reading the code plus the verification record's claim that 12/12 pass. I did not execute it.
>
> ### High
> None.
>
> ### Medium
> None.
>
> ### Low
> 1. **The breadcrumb now sends approvers to a page they can't use.** `eami-ui/src/pages/gateway/AgentDetailPage.tsx:198`
>    - Approvers can open Agent Detail (the Agents list/by-id reads include them), but `/v1/cmdb/*` excludes them.
>    - Before this change, "Agents" in the breadcrumb took them to a list that works. "Assets" now lands them on an Assets page whose queries fail.
>    - The Assets nav item was already visible to approvers, so that part isn't new.
>    - The founder did approve "Assets › {agent}", so this is a note, not a reversal.
>    - **Fix:** for roles without `can.viewCMDB`, fall back to `{label:'Agents', href:'/gateway/agents'}`. Or log it for C9, when the Agents list retires.
>
> 2. **Audit hides the "agent no longer exists" marker.** `eami-ui/src/pages/ops/AuditPage.tsx:147`
>    - AgentLink sits inside `max-w-[140px] truncate block`, and the " · agent no longer exists" suffix comes after the name, so it is usually cut off.
>    - The outer `title={entry.agent_name}` doesn't mention deletion either, so the honest-data signal is mostly invisible in exactly the column with the most deleted-agent rows (46 of them).
>    - **Fix:** put the marker first or use a compact icon/badge. Or make the outer title reflect the deleted state.
>
> 3. **The deleted-agent verdict can be wrong because of the list cache.** `eami-ui/src/components/agents/AgentLink.tsx:11-13`
>    - `useAgents` has `staleTime: 30_000`. An agent created in another session or tab after the list was cached is labelled "agent no longer exists" until a refetch.
>    - The text asserts a fact (deletion) from what may only be a cache miss.
>    - **Fix:** softer wording ("agent not found"), or refetch once when an id isn't in the list before declaring it deleted.
>
> 4. **The Memory row looks clickable where it isn't.** `eami-ui/src/pages/ops/MemoryPage.tsx:257-278`
>    - The `hover:bg-gray-50` wrapper covers the metadata line, but clicking that line (outside the link) no longer expands the row, which it used to do.
>    - The button's focus ring now covers only the top half of the row.
>    - `aria-expanded` was added, which is good. Moving the link out of the `<button>` was the right fix: a link inside a button is invalid HTML.
>    - **Fix:** either confine the hover style to the button, or let the metadata `div` toggle on click while the link keeps its `stopPropagation`.
>
> 5. **The counts check in the test could flake.** `eami-api/internal/api/cmdb_id_filter_pg_test.go:85`
>    - It compares `fmt.Sprint(all.Counts) != fmt.Sprint(one.Counts)`, but `CountCMDBAssetsByType` has no `ORDER BY` (`GROUP BY type_id,category_id` only).
>    - Row order between two separate queries is not guaranteed, so this can fail spuriously.
>    - **Fix:** compare as a `map[type_id]count`, or add `ORDER BY type_id` to the count query.
>
> 6. **§7.7 wasn't updated.** `DESIGN_SYSTEM.md` §7.7 (around line 343), the Agent Detail table
>    - It still has no Classification row. The Endpoint and Tool tables say "Currently in Assets, moves here", but the agent case, which has now moved, isn't recorded.
>    - **Fix:** add a "Classification | Built (2026-09-29, B-252 C1)" row, in the same docs pass as BUILT.md.
>
> ### Nit
> 7. **Query-cache key could collide.** `eami-ui/src/hooks/useCMDB.ts:17`
>    - `['cmdb-assets', {kind, id}]` shares its namespace with `useCMDBAssets`' `['cmdb-assets', params]`, and the two return different data shapes (a single row vs a page).
>    - They can't collide today, because `CMDBAssetParams` has no `id`. If someone later adds `id` to `CMDBAssetParams`, one cache entry could serve both shapes.
>    - **Fix:** use a key like `['cmdb-assets', 'one', kind, id]`. The prefix still matches the `['cmdb-assets']` invalidation.
>
> 8. **Dead branch.** `eami-ui/src/pages/cmdb/AssetsPage.tsx:112`: the `note={asset.asset_kind === 'agent' ? undefined : ...}` check can never be true, because agent rows now navigate instead of opening the panel.
>
> 9. **Small per-row inefficiency.** `AgentLink.tsx:13`: each row runs `.some()` over the whole agents list. That's fine at current sizes; a memoised `Set` of ids would scale better. TanStack already de-duplicates the fetch, so there's no extra network cost.
>
> 10. **In-flight save when leaving the tab.** Classification is mounted only while open, so switching tabs mid-save unmounts the form. This is harmless:
>     - the `useCMDBMutation` `onSuccess` invalidation is hook-level, so it still runs;
>     - the `await` continuation still fires the toast.
>
>     Only the spinner is lost. It's worth a comment, given that Actions is deliberately kept mounted for this same reason.
>
> ### Verified correct
> - **No in-app links to `/gateway/agents/:id` remain.** The only match in `src` is the redirect route. Other hits are API paths and comments.
> - **The deleted panels are unreferenced.** Nothing refers to `AgentAssetPanel` or `ToolAssetPanel`, and tsc passes.
> - **The redirect is correct.**
>   - `useParams` decodes the id and `encodeURIComponent` re-encodes it.
>   - `search` and `hash` are carried across, `replace` is used, and there is no loop, since the target is a different route.
>   - The sidebar `NavLink` for `/assets` prefix-matches, so Assets highlights on the detail page.
> - **The tab refreshes after save.**
>   - The save invalidates `['cmdb-assets']`, which prefix-matches the new key.
>   - The key `${type_id}-${source}` re-seeds the form, including the "explicit pick of the default type" case, because the source is part of the key.
>   - Background refetches don't flash the spinner, since `isLoading` is false once data exists.
> - **Hooks rules hold.** In AgentClassificationTab, AgentLink and AgentDetailRedirect, all hooks run before any early return.
> - **AgentLink handles missing ids.** `""`, `undefined` and `null` all render plain text via `!agentId`. A loading or errored agents query also gives plain text. `stopPropagation` covers Audit's row click; DataTable rows aren't keyboard-activated, so there's no Enter conflict.
> - **The extracted form matches the original.**
>   - The header close button is disabled while pending.
>   - Cancel is disabled while pending.
>   - Save uses `isLoading`.
>   - The success and error toasts have the same text.
>   - The panel closes on success via `onDone`.
>   - Picker seeding, kind filtering and the default reset are the same.
>   - I compared it against the HEAD `AssetClassificationPanel`. The only difference is the footer, which is now hidden when there's no Cancel and no edit rights. That's correct for the tab.
> - **Role gating is right.** The tab is hidden for approvers via `can.viewCMDB`, so no `/v1/cmdb` request is made. `?tab=classification` falls back to Overview through the existing hidden-tab rewrite. Operator and viewer get the read-only text.
> - **The server filter is safe.**
>   - The value is parsed as a UUID (400 on bad input) and bound as a parameter, so there's no injection.
>   - It's applied inside the CTE, whose every branch is `org_id=$1`, so a foreign id matches nothing.
>   - Unlicensed endpoints stay excluded via `$2`.
>   - The navigation counts reset `ID`.
> - **The test is strong.**
>   - The pool follows CLAUDE.md's rule: `newWorkspaceTestEnv` registers `t.Cleanup(pool.Close)` first, and the org cleanups come after.
>   - It asserts the before-state equals the default type.
>   - It reclassifies to a freshly created non-default type.
>   - On the very next read it asserts that type's id, its name and source `explicit`, so a stale row cannot pass.
>   - It then asserts the reset returns to the default.
>   - It covers the foreign-org id, a same-name near-namesake, the 400 case and the approver 403.
>   - Missing: a mismatched pairing such as `kind=agent` with a tool's id returning empty. That is optional.
>
> ### Verdict on `?id=` vs the approved `agent_id`
> **Sound.**
> - `/v1/cmdb/assets` is kind-agnostic, and `id` is the row's own primary key, so `id` is the correct and consistent name.
> - `agent_id` would be misleading next to `endpoints.agent_id` (the scanner's discovery id), and it would force separate `endpoint_id`/`tool_id` parameters for C2 and C5.
> - Behaviour is the same as what was approved when it's used with `kind=agent`, and the parameter works without `kind` too, since ids are unique UUIDs across tables.
> - **Remaining risks:**
>   - It is a deviation from the literal wording, so it needs the founder's explicit acknowledgement, which the verification record already surfaces prominently.
>   - The parameter isn't in `api/openapi.yaml` yet. That drift is logged; Architect-EAMI should add `id` before C2 and C5 reuse it, so the generated client can drop the `apiFetch` escape hatch.
>
> ### Overall verdict
> **Approve with minor follow-ups.** There are no High or Medium findings. I'd suggest fixing #2 and #5 before commit (both are small), handling #6 in the docs pass, and treating #1, #3 and #4 as quick follow-ups or NOTES.md items.

### 6.2 Security review

> **Security review: B-252 C1 (Agent handoff from Assets). Read-only, no files modified.**
>
> I found no Critical, High or Medium issues. There are two Low findings and a few Info notes. `go vet ./internal/api/ ./internal/store/` came back clean.
>
> **Critical:** none.
> **High:** none.
> **Medium:** none.
>
> **Low**
>
> 1. **The `?id=` filter is not in the API contract.**
>    - Where: `eami-ui/src/hooks/useCMDB.ts` (`useCMDBAsset`) calls it through the `apiFetch` escape hatch, and `api/openapi.yaml` doesn't document it.
>    - Why it matters: this isn't exploitable. But any contract-driven security testing, gateway validation or API-review tooling that works from the spec won't see the parameter.
>    - Fix: have Architect-EAMI add `id` (uuid, optional) to `GET /v1/cmdb/assets`. The build record already logs the drift, so this is just a reminder to close it.
>
> 2. **"Agent no longer exists" can be wrong, which is an integrity/UX issue rather than a confidentiality one.**
>    - Where: `eami-ui/src/components/agents/AgentLink.tsx` lines 11-13.
>    - Scenario: `AgentLink` decides whether an agent exists by checking the id against the cached `useAgents()` list (30 s staleTime). An agent created in the last 30 s, or one missing from that response, is labelled "agent no longer exists" on Audit/FinOps/Memory. I didn't find pagination on `ListAgents`, so today it's only the staleness window. An operator could still misread a live agent as deleted in an audit context.
>    - Fix: soften the wording, e.g. "not found in current agent list", or invalidate `['agents']` when the page mounts.
>
> **Info (checked and sound)**
>
> 3. **Org scoping.** The new filter adds `a.id=$n` to the WHERE of the `filtered` CTE (`eami-api/internal/store/cmdb.sql.go` ~255).
>    - Every branch of `cmdbAssetUnion` is already bound to `org_id=$1`: endpoints, gateway_agents and gateway_tools, plus their `ci_types d` joins and the workspaces LEFT JOINs.
>    - The outer `ci_types ct` and `ci_categories cc` joins are also bound to `org_id=$1`.
>    - `OrgID` comes only from `claimsFromContext`, never from the query string.
>    - So a foreign-org id returns nothing, and the new pg test asserts this (`cmdb_id_filter_pg_test.go:79`).
>    - Endpoint rows stay excluded when the org isn't licensed, via `$2 = IncludeEndpoints`, even if `id=` is an endpoint id and no `kind` is given.
>
> 4. **SQL injection: none.**
>    - `id` is parsed with `uuid.Parse` (`cmdb.go:89`); anything else is a 400 "invalid id".
>    - The value is bound as a parameter. Only the placeholder index goes through `fmt.Sprintf`.
>
> 5. **Cross-org existence oracle: none.**
>    - A foreign id and a nonexistent id both get 200 with `data:[]` and `total:0`.
>    - `counts` is computed with `nav.ID=nil`, so it's the same org-wide sidebar count as with no `id` param and leaks nothing about the id.
>    - The query shape is identical in both cases, so there's no meaningful timing difference.
>    - The PATCH path (`SetCMDBAssetType`) was already org-scoped and returns ErrNoRows the same way for both cases.
>
> 6. **The `id` vs `agent_id` naming has no security implication.**
>    - `id` is a generic asset-id filter across all three kinds, not just agents.
>    - It only ever narrows a result set the caller could already page through unfiltered, under the same role gate. Its visibility is a strict subset of the existing endpoint's.
>    - The UI pairs it with `kind=agent`, so an id that happens to belong to an endpoint or tool can't be mistaken for an agent.
>    - One thing to keep in mind: the name won't self-document as "agent" if someone later adds per-kind authorization. If that ever happens, the check must apply to whatever kind the `id` resolves to.
>
> 7. **Role gates. The server is the enforcement point, and the UI mirrors it.**
>    - `GET /v1/cmdb/assets` sits in the admin/operator/viewer group (`router.go:407-412`). Approvers get 403, which the test asserts at line 119.
>    - The classification PATCH is in the admin-only group (`router.go:277`).
>    - UI side: `can.viewCMDB` excludes approvers. `TAB_VISIBLE` filters the tab list, and `activeTab` falls back to `overview` when the tab isn't visible (`AgentDetailPage.tsx:163,170`). So `?tab=classification` never mounts the tab or its CMDB requests for an approver.
>    - Operators and viewers get `canEdit=false` from `can.classifyAsset`, so no select or Save control is rendered. Even a hand-crafted PATCH would be refused by the server's admin gate.
>    - No role gains data it couldn't already read: the tab shows the same row Assets shows to the same roles.
>
> 8. **Redirect (`router.tsx` `AgentDetailRedirect`): no open redirect or route injection.**
>    - The target is always a relative path with the fixed prefix `/assets/agents/`.
>    - `useParams` decodes `:id`, then `encodeURIComponent` re-encodes `/ ? # % .`. So `//evil.com` becomes `/assets/agents/%2F%2Fevil.com`, and `..%2F..` becomes `/assets/agents/..%2F..`, a single segment that doesn't traverse.
>    - `search` and `hash` are copied from the current same-origin location. They are always `?…` and `#…` suffixes, so they can't change the scheme or host.
>    - The worst case is a junk id, which reaches `GetAgent` (uuid-validated server-side) and renders not-found.
>
> 9. **`AgentLink` href and XSS: none.**
>    - A link is rendered only when `agentId` strictly equals an `id` from the org's own `/v1/gateway/agents` response (UUIDs). An attacker-controlled audit, token or episode value can't choose the href.
>    - Names are rendered as React text children and a `title` attribute. There's no `dangerouslySetInnerHTML` and no `javascript:` risk, since router `Link` targets a path.
>    - `stopPropagation` only affects row click handling.
>    - `useAgents` is readable by every role including approver, and Audit, FinOps and Memory already carry `agent_name`, so there's no new exposure. At most, the "no longer exists" hint tells a viewer an agent was deleted, which they could already see by comparing against the agent list they can read.
>
> 10. **Test hygiene (not security).** The new pg test uses `seedTestUser`/`env` helpers. I didn't see `defer pool.Close()` in it, which matches the mandated `t.Cleanup` pattern. I didn't spot a nonexistent-id case next to the foreign-id case. Adding one would lock in the "no oracle" property from finding 5.
>
> **Verdict:** approve from a security standpoint. Org scoping, parameterisation, role gating (server-enforced, UI-mirrored) and redirect handling are all correct. The only follow-ups are the two Low items: add `id` to `openapi.yaml` and soften or refresh the `AgentLink` "no longer exists" label. Neither blocks the change.

## 7. Disposition of findings

| Finding | Action |
|---|---|
| CR-1 approver breadcrumb goes to an unusable Assets page | **Fixed:** roles without CMDB read (approvers) keep "Agents › {agent}" until C9; everyone else gets "Assets › {agent}" as approved |
| CR-2 Audit truncation hides the deleted marker | **Fixed:** `AgentLink stacked` puts "agent no longer exists" on its own line in the Audit column (verified live) |
| CR-3 / SR-2 stale cache could mislabel a new agent as deleted | **Fixed:** on a miss, `AgentLink` refreshes the agents list once (joining any in-flight fetch, so many rows cause one request) before declaring "agent no longer exists" |
| CR-4 Memory metadata line looks clickable but isn't | **Fixed:** the line toggles the row too; the link keeps `stopPropagation` |
| CR-5 counts comparison order-dependent | **Fixed:** compared as a map |
| CR-6 §7.7 lacks the Agent Classification row | **Fixed** in DESIGN_SYSTEM.md |
| CR-7 cache-key namespace | **Fixed:** `['cmdb-assets','one',kind,id]`, still under the invalidated prefix |
| CR-8 dead branch | **Fixed** |
| CR-9 per-row `.some()` | **Fixed:** a memoised `Set` |
| CR-10 mid-save unmount | **Commented** (harmless: the invalidation and toast still run) |
| CR optional / SR Info-10 | **Added:** a nonexistent-id case and a tool-id-as-agent case both return exactly what a foreign id does (no oracle) |
| SR-1 `id` not in `openapi.yaml` | **Logged** for Architect-EAMI (NOTES.md): add it before C2 and C5 reuse it |

After the fixes: `tsc`, `vite build`, `go vet` and the full `go test ./internal/...` all pass. Mutations are 4/4 killed, re-run against the updated test. Live is **33/33 PASS** with the snapshot identical, re-run after the fixes (the first run, also 33/33, is kept as `c1_live_run1_prereview.log`).
