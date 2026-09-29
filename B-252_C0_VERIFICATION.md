# B-252 C0 — Foundations for the IA consolidation: verification record

**Date:** 2026-09-29 · **By:** Claude Code · **Roadmap:** Horizon 1, "CMDB completion" (B-252 umbrella, step C0)
**Brief scope:** (a) move `EndpointDrawer` and the link control out of `DiscoverPage.tsx`, behaviour unchanged; (c) hide UI write controls per B-253's role rules on the Agents list, Tools, Discover's link control and Agent Detail's Actions tab; approvers see Agent Detail's Overview only.
**Not in this brief:** the plan's C0(b) (delete the orphaned `components/cmdb/AgentAssetPanel.tsx` and `ToolAssetPanel.tsx`; confirmed 2026-09-29 that nothing imports either). It is still open under C0.

## 1. Part A (reported before building; founder approved 2026-09-29)

- **A.1 export boundary.**
  - `EndpointDrawer` was exported from `DiscoverPage.tsx` and imported by `AgentDetailPage.tsx`.
  - Its file-local dependencies were `LinkedAgentControl` (private), `Section`, `formatBytes` and `formatRelativeTime`.
  - `formatRelativeTime` is also used by the Discover list, so it moved to a shared `format.ts`.
- **A.2 button-to-role map**, from `B-253_VERIFICATION.md` §1 and `router.go`:

| Control | admin | operator | approver | viewer |
|---|---|---|---|---|
| Agents: + Add agent | ✓ | – | – | – |
| Agents: Configure | ✓ | ✓ | – | – |
| Agents: Suspend | ✓ | ✓ | – | – |
| Agents: Reactivate / Delete | ✓ | – | – | – |
| Tools: Add / Remove | ✓ | – | – | – |
| Tools: Test | ✓ | ✓ | – | – |
| Tools: Edit (full panel) | ✓ | – | – | – |
| Tools: Edit data handling note (ai_provider only) | (full panel) | ✓ | – | – |
| Discover link select | ✓ | read-only text | page not reachable (403 on endpoints) | read-only text |
| Agent Detail tabs | all 3 | all 3 | Overview only | all 3 (Actions = read-only notice) |

- **A.3.** `AgentActionsTab` used one `WRITE_ROLES = ['admin','operator']` gate for all actions. It was split per action. Approver Overview-only was confirmed necessary: `/connections` excludes approvers, and the page fetched it unconditionally.
- **Founder decisions:**
  - Tool Edit for operators is **option (b)**: a trimmed panel, on ai_provider tools only, that sends `{data_handling_note}` alone.
  - Before relying on (b), confirm the server's echo-dropping handles a single-field request.
  - Everything else was approved as reported.

## 2. Server check for the note-only request (asked for by the founder)

- **By reading `tools.go` `UpdateTool` and `rbac_fields.go`:**
  - `data_handling_note` is not a classified field.
  - With only the note present, every admin-only field is nil, so `toolAdminOnlyChange` returns false.
  - The ai_provider type check runs, and the store's `COALESCE` keeps every omitted column.
- **Pinned by a new real-Postgres block in `rbac_split_pg_test.go` (`TestRBACSplit_ToolFields_RealDB`):**
  - An operator note-only PATCH returns 200 and changes **only** the note column; the other 10 snapshot columns are unchanged.
  - `""` clears the note.
  - A note on a rest_api tool returns 400 and nothing is written.

## 3. Build

- **`eami-ui/src/components/endpoints/`** (new): `EndpointDrawer.tsx` (with `Section`), `LinkedAgentControl.tsx` and `format.ts`.
  - Moved verbatim. A line-set diff against `HEAD:DiscoverPage.tsx` shows no body changes apart from `export` keywords. Both reviewers independently confirmed this.
  - `LinkedAgentControl` then gained its role branch: the admin select, or read-only text plus "an admin can link it manually."
  - `DiscoverPage.tsx` and `AgentDetailPage.tsx` import from the new location.
- **`eami-ui/src/lib/rbac.ts`** (new): `can.*` predicates, one per server route or field rule, each commented with its route, plus `useOrgRole()`. This is the single place the UI mirrors B-253.
- **`AgentsPage.tsx`:**
  - "+ Add agent" (top bar and empty state) is admin-only.
  - Row actions are per-predicate.
  - There is no Actions column for viewers and approvers.
  - Non-admin suspend shows a toast: "Agent suspended. Reactivating it requires an admin."
- **`ToolsPage.tsx`:**
  - Add (both entry points) and Remove are admin-only; Test is admin + operator.
  - The pencil opens `EditToolPanel` for admins and the new `EditToolNotePanel` for operators on ai_provider tools. That panel has one field, sends `{data_handling_note}`, and uses `Button isLoading`, Cancel/x/backdrop disabled while pending, and `useToast`.
  - There is no actions column for viewers and approvers.
- **`AgentActionsTab.tsx`:**
  - Per-action rows: Configure and Suspend are admin + operator; Reactivate and Delete are admin-only.
  - It shows the read-only notice when no row applies (viewer).
  - It shows the same non-admin suspend toast.
- **`AgentDetailPage.tsx`:**
  - Tabs are filtered by role, so approvers see Overview only.
  - A hidden `?tab=` renders Overview and is rewritten to `?tab=overview`.
  - The connections query gets a null id for approvers (disabled, never requested).
  - The Actions panel is not mounted for approvers.
  - Arrow-key tab navigation uses the filtered list.

## 4. Verification

- **Checks:**
  - `npx tsc --noEmit` passes.
  - `npx vite build` passes.
  - `go vet ./internal/api/` passes.
  - `go test ./internal/api/ -count=1` with real Postgres: `ok` (129.95s, full package).
  - `git diff --check` is clean.
- **Live, on the real stack** (Vite dev UI on :5173 and the API on :8081), with **real logins** for four throwaway users: `c0-admin`, `c0-operator`, `c0-approver` and `c0-viewer`.
  - The fixtures were created in the **Dev Org**, because it holds the only valid discovery license; licenses are signed per org, so a probe org cannot reach Discover. The fixtures were two agents, two tools, and one endpoint with a report.
  - The script only acted on `c0-*` rows.
  - Script: `c0_live.js`, driven by `c0_run.sh`.
  - **Final run: 77 PASS, 0 FAIL.**
  - A correction to the interim chat report: the first run had **74** checks, all passing, not 77. Three checks were added after the review fixes.
- **What the live run proves:**
  - **Admin** (no visible change):
    - every button is present on both lists;
    - Edit opens the full panel;
    - the Discover select links and unlinks, with the DB checked each time;
    - drawer sections render;
    - Agent Detail has 3 tabs, the graph shows the linked endpoint, and its node opens the **relocated** drawer;
    - the Actions tab shows Configure/Suspend/Delete, and Configure/Reactivate/Delete when suspended;
    - Suspend works, Reactivate is then offered, and no operator toast appears;
    - the session saw no 403s.
  - **Operator:**
    - no Add agent;
    - on the agent rows, Configure and Suspend only, and Configure only when suspended;
    - on Tools, no Add, `c0-ai` shows Test + EditNote, and `c0-rest` shows Test only;
    - the note panel has exactly one field; the PATCH body was exactly `{"data_handling_note":"C0 operator note"}` and returned 200; the DB note was updated and every other tool column was unchanged; the success toast appeared;
    - Discover shows no select, only a read-only linked agent name;
    - Agent Detail has 3 tabs and Connections loads;
    - Suspend works, the confirmation toast appears, and Reactivate is not offered;
    - the session saw no 403s.
  - **Approver:**
    - no Add buttons, no action columns, no tool controls;
    - Agent Detail has the Overview tab only, and Overview renders;
    - `?tab=connections` and `?tab=actions` both render Overview, the URL is rewritten, and the Actions panel is not mounted;
    - `/connections` and `/config` are **never requested**;
    - no 403s on agents, tools or detail;
    - Discover returns 403 on `GET /v1/endpoints` (expected: the route excludes approvers), and no link select is reachable.
  - **Viewer:**
    - no controls on either list;
    - Discover shows the read-only label;
    - Agent Detail has 3 tabs and Connections loads;
    - the Actions tab shows the read-only notice;
    - the session saw no 403s.
- **Cleanup proof.** The snapshot is `snapshot6.sql`, plus users, reports, refresh tokens, link counts and every tool's note. It was taken before and after each run.
  - The only difference in each run is `endpoint_reports` +1. That row comes from the live endpoint agent on this machine (`Bhargav_tej`), which reports every 60s during the run; it is not a fixture.
  - Zero orphaned reports, and zero `c0-*` users, agents, tools or endpoints remain.
  - The org count is 6, the baseline.
  - The mutation run's snapshot diff was fully identical.
  - Audit-log rows written by fixture logins and actions remain; audit_log is never deleted because of the hash chain.
- **Screenshots:** the scratchpad `shots_c0/` directory.

## 5. Mutation tests

| # | Mutation | Caught by |
|---|---|---|
| S1 | `tools.go`: any note write is admin-only (`\|\| body.DataHandlingNote != nil`) | `TestRBACSplit_ToolFields_RealDB`, by the **pre-existing** echo-all-fields operator assertion |
| S2 | `tools.go`: refuse **only** note-only requests (note set, name and provider nil) | the **new** assertion: `rbac_split_pg_test.go:247: operator note-only edit = 403 want 200` |
| U1 | `rbac.ts`: `reactivateAgent: adminOrOperator` | live run: 3 FAILs (operator suspended row, Actions tab and after-suspend all show Reactivate) |
| U2 | `rbac.ts`: `viewAgentConnections` includes approver | live run: 6 FAILs (approver sees the Connections tab, `?tab=connections` isn't rewritten, `/connections` requested and returns 403) |

The UI mutations ran in one live pass: 65 PASS, 9 FAIL, all expected. The files were restored afterwards: `tools.go` has no git diff, and `rbac.ts` matches the backup byte for byte.

## 6. Independent reviews (verbatim)

The first launch of both reviewers stopped early on the account's session usage limit (HTTP 429) before producing any report. Both were re-launched from scratch and completed. Their reports follow unedited.

### 6.1 Code review

> ## B-252 C0 review — no High or Medium findings
>
> I changed no files. Checks run: `npx tsc --noEmit` in eami-ui passed (exit 0), and `go vet ./internal/api/` passed, which compiles `rbac_split_pg_test.go`. I did not run the real-Postgres test.
>
> **Predicates against `router.go` (lines 338–420) and `rbac_fields.go`:** every `can.*` entry in `eami-ui/src/lib/rbac.ts` matches the server.
> - createAgent, deleteAgent, linkEndpointAgent, createTool and deleteTool are admin-only in both.
> - PATCH agent, PUT config, PATCH tool and POST test are admin+operator. The field rules match: suspend is fine for an operator, reactivate (→active) is admin-only, and an operator may change only the tool's note.
> - Connections and config reads allow admin, operator and viewer, not approver. Approvers can read the agent list, a single agent and the tool list only.
> - The note being ai_provider-only matches the 400 guard in `tools.go:603-616`.
>
> **Other routes to the same actions (checked with grep):** `AgentConfigPanel` is mounted only from AgentsPage (Configure) and AgentActionsTab. `EditToolPanel`, `AddToolPanel` and `ConfirmDialog` are opened only by the gated buttons, and ToolsPage has no row click. `useLinkEndpointAgent` is used only in `LinkedAgentControl`. The only other connections consumer is `components/cmdb/AgentAssetPanel.tsx`, which is under CMDB, a route approvers can't read. For approvers, `EndpointDrawer` and the policy/workflow panels open only by clicking a connections-graph node, and the connections query is disabled for them. That is safe: TanStack Query is v5, so a disabled query reports `isLoading = false`.
>
> **Relocation:** confirmed identical. I diffed HEAD's `DiscoverPage.tsx` against the new files:
> - `format.ts` and `EndpointDrawer.tsx` (Section plus drawer) differ only by `export` keywords, the moved header comment and blank lines.
> - `LinkedAgentControl` differs only by the intended `canLink` branch and its comment.
> - `DiscoverPage` only lost code and added the three new imports. Nothing else imported `EndpointDrawer` from `DiscoverPage` (only `router.tsx` imports `DiscoverPage`).
>
> **Hooks and closures:** no conditional hooks. `useOrgRole` is called at the top level in every component and before AgentDetailPage's early returns. `useToast` and `useUpdateTool` are unconditional in `EditToolNotePanel`. Columns and rows are rebuilt on every render, so nothing holds a stale role. React keys are unchanged and still unique.
>
> **`(cond ? setEditTarget : setNoteTarget)(tool)` (ToolsPage.tsx:1034):** correct. Both setters are the same `Dispatch<SetStateAction<ToolWithActions|null>>` type, and `tool` is a plain object, so it can't be mistaken for an updater function. The button only renders when `canEditTool(tool)` is true, so an operator can never reach the note panel for a non-ai_provider tool.
>
> **Tab keyboard navigation (AgentDetailPage.tsx:163-174):** correct. `activeTab` always falls back to `'overview'`, which is always visible, so `findIndex` is never -1. Wrap-around uses the filtered `tabs.length`, and roving `tabIndex` and focus ids use the filtered list. With one tab, the arrow keys just re-select Overview.
>
> **Note panel:** the body is exactly `{ data_handling_note: note }`, so no admin fields are echoed. Errors show the server's `ApiFetchError.message` via `showToast`, falling back to a generic message. Save uses `isLoading`, and Cancel and the header x are disabled while saving, which follows the CLAUDE.md rules.
>
> **Server test addition:** correct. `f[6]` is `data_handling_note` in `snap`'s `concat_ws` order. It checks that nothing else changed, that "" clears the note, and that the REST-tool 400 wrote nothing.
>
> **Admin sees no change:** confirmed. Every admin predicate is true, so the Actions columns, "Add" buttons, Edit/Remove icons (title still "Edit"), link select, all three agent tabs and the connections query behave exactly as before.
>
> ## Findings
>
> 1. **Low — ToolsPage.tsx:915 (`EditToolNotePanel` → `SlideOverPanel`, backdrop `onClick={onClose}` at SlideOverPanel.tsx:39).** Clicking the backdrop still closes the panel while the PATCH is in flight, even though Cancel and x are disabled. The request completes and the toast still fires, so nothing breaks; it just doesn't fully honour the "disabled while pending" intent. `EditToolPanel` behaves the same way already. Fix (optional): pass `onClose={update.isPending ? () => {} : onClose}` to `SlideOverPanel`.
>
> 2. **Low — AgentActionsTab.tsx:72-83 and AgentsPage.tsx:226-234.** When an operator suspends an agent, the Suspend button disappears straight away: the refetched status is `suspended`, and Reactivate is admin-only. There is no success confirmation, and the operator can't undo it. The behaviour is correct, but it looks like the control vanished. Fix: `showToast('Agent suspended — an admin can reactivate it', { type: 'success' })` in the mutation's `onSuccess`, for non-admins at least.
>
> 3. **Nit — `stores/authStore.ts:9`.** `role` is typed `'admin' | 'operator' | 'viewer'` and doesn't include `'approver'`. `rbac.ts` avoids the problem by widening `Role` to `string | undefined`, so nothing breaks, but the store type is stale. Fix: add `'approver'` in a follow-up. It's outside this change's scope, so log it in NOTES.md.
>
> 4. **Nit — AgentDetailPage.tsx:163.** When an approver deep-links with `?tab=connections` or `?tab=actions`, Overview renders correctly but the stale `tab=` stays in the URL. Optional fix: rewrite it with `setSearchParams({ tab: 'overview' }, { replace: true })` in an effect when `tabParam` is set but not in `tabs`.
>
> 5. **Nit — ToolsPage.tsx:1021.** `{can.testTool(role) && <button …>}` inside the actions column can never be false, because the column is only added when `can.testTool(role)` is true (line 1010). Fix: drop the inner check, or leave it as defence if the column condition changes later.
>
> 6. **Nit — components/endpoints/format.ts:1-2.** The comment says both helpers are "shared by the Discover list and EndpointDrawer". Only `formatRelativeTime` is used by `DiscoverPage`; `formatBytes` is used only by the drawer. It's a comment-accuracy issue only.
>
> ## Verdict
>
> This is correct and ready to merge; none of the findings block it. Every predicate matches `router.go` and `rbac_fields.go`. No control is left reachable for a role the server refuses, through any entry point I could find: buttons, panels, row clicks, deep links, the keyboard tab path or the connections-graph drawers. The relocation is identical apart from the intended `LinkedAgentControl` role gate. There are no hooks-rule violations, stale closures or key problems, and admins see no visible change. The trimmed note panel sends only the note, handles errors through `useToast`, and follows the `isLoading`/Cancel-disabled rule. The new server test checks the right column and the no-write guarantee. The items above are optional polish (1–2) or clean-up to log (3–6).

### 6.2 Security review

> **Security review: B-252 C0 (uncommitted change set). Read-only; no files were modified.**
>
> **What I checked:** the full diff; `eami-api/internal/api/tools.go` UpdateTool (lines 537–700); `rbac_fields.go` (`toolAdminOnlyChange`, `loadToolAdminFields`); `store/tools.sql.go` UpdateTool; `store/convert.go` `toPgtypeText`; `router.go` role groups (lines 330–410). I also confirmed that the moved EndpointDrawer and format helpers are verbatim moves: a line-set diff shows the only content changes are in LinkedAgentControl's role branch.
>
> **Checks run:**
> - `npx tsc --noEmit` passes.
> - `go vet ./internal/api/` passes.
> - `go test -run TestRBACSplit_ToolFields_RealDB` **was skipped** because no `TEST_DATABASE_URL`/`POSTGRES_PASSWORD` is set here, and I did not touch `.env`. So the new test block has not actually run in my review.
>
> ### Findings
>
> **Critical: none. High: none. Medium: none.**
>
> **1. Low: no length or body-size cap on `data_handling_note`, which operators can write**
> - Where: `eami-api/internal/api/tools.go:537–564` (body decode) and `:684` (`DataHandlingNote` passed straight through). The column is plain `TEXT` (`schema/migrations-v2/000008_ai_provider_data_handling.up.sql:23`). No `MaxBytesReader` on this route, and no global body-limit middleware (`router.go:191–193`; the only `MaxBytesReader` is in `openapi_discover.go:41`). The only bound is the server's `ReadTimeout`.
> - Scenario: an operator (admins too) PATCHes a note of tens or hundreds of MB. It is stored, then returned by `toolToResp` (`tools.go:~335`) in every GET `/v1/gateway/tools` for every role, including viewer and approver. That bloats the list response and the UI for the whole org: a cheap persistent DoS/nuisance from a non-admin role.
> - This predates C0 (B-253 made the note operator-writable). C0 adds a first-class UI path to it but no new server exposure.
> - Fix: in UpdateTool (and CreateTool), reject notes over a sane cap (for example 2–4 KB) with 400. Optionally add `http.MaxBytesReader` on JSON write routes. Pin it with one test row.
>
> **2. Low / Info: operator note panel is last-write-wins on the note**
> - Where: `eami-ui/src/pages/gateway/ToolsPage.tsx:891–897`.
> - The panel sends the whole note taken from the list snapshot. An operator's stale panel can silently overwrite (or clear, `""`) a note an admin just changed. Only the note is affected; the server correctly drops no other field.
> - This is a data-integrity nit, not a privilege issue. The fix would be optimistic concurrency (an ETag or `updated_at` precondition) if it ever matters.
>
> **3. Info: the role in the UI can be stale or edited, but that only affects which controls show**
> - Where: `authStore.ts:9` and `lib/rbac.ts`.
> - The role comes from the login response and is persisted in localStorage. A role change on the server isn't reflected until the next login, and a user can edit localStorage to reveal admin buttons. Either way the server still refuses the request (`requireRole` groups plus the in-handler field checks), so this is UX only, as intended.
> - The `User.role` type omits `'approver'` (and `platform_admin`). This does **not** affect `rbac.ts`, which types the role as `string | undefined` and compares exact strings. Approver and platform_admin get no write controls, which matches the server (`requireRole` uses exact membership; platform_admin is not in the admin/operator groups).
> - Recommended: widen the type to `'admin'|'operator'|'viewer'|'approver'|'platform_admin'` so future code can't narrow on a wrong union.
>
> **4. Info: the test pins the property well, with minor gaps**
> - Where: `eami-api/internal/api/rbac_split_pg_test.go:288–308`.
> - What it covers well:
>   - The note-only PATCH changes only field 6 of an 11-column snapshot. The snapshot includes name, base_url, action_paths, provider, audit_mode, designation, redaction_rules, mcp_command, mcp_args and credentials hex.
>   - Clearing with `""` works.
>   - A note on a non-`ai_provider` tool is refused with 400 and nothing is written.
>   - Together with the existing "mixed note + restricted rename → 403, nothing written" row and the approver/viewer 403 rows, the security property is genuinely pinned.
> - Gaps:
>   - `coalesce(...,'')`/`concat_ws` can't tell NULL from `""`, and a note containing `|` would break the positional split. Neither affects the security claim.
>   - There is no cross-org case, though the org scoping is verified by code: `GetToolForTest`, `loadToolAdminFields` and the store UPDATE all filter on `org_id`.
>   - It needs a real-Postgres run before commit; it only skipped here.
>
> ### Answers to the five questions
>
> **(1) Can the note-only PATCH change anything beyond the note server-side? No.** Verified order in UpdateTool:
> - decode, then enum validation (provider, audit_mode, designation, redaction_rules), then empty-name check;
> - then the `ai_provider` type check, which runs because `DataHandlingNote != nil`, is org-scoped, and returns 404/400;
> - then `credentialsProvided` and action_paths validation;
> - then the non-admin role check: `loadToolAdminFields` (org-scoped) and `toolAdminOnlyChange`, which rejects any changed restricted field or any credential write with 403 before any write, and strips unchanged echoed fields;
> - then encryption, which happens only after the role check;
> - then the store UPDATE.
>
> With `{data_handling_note}` alone, every other param is nil or empty. `toPgtypeText(nil)` gives `Valid:false` (SQL NULL), and every column is `COALESCE($n, col)` with `WHERE id=$1 AND org_id=$2`, so all omitted columns are preserved. `""` becomes a valid empty text and correctly overwrites. JSON `null` for the note decodes to nil, which is a no-op. An empty note is safe. A very long note: see finding 1.
>
> **(2) Does any UI change weaken the server, or expose data a role shouldn't see? No.** Every hidden control maps to a route the server already gates.
>
> What the approver session actually requests:
> - AgentsPage: only `useAgents` fires; the mutation hooks are idle.
> - ToolsPage: only `useTools` fires.
> - AgentDetailPage: `useAgent` fires; `useAgentConnections` gets a null id, so the query is disabled. `?tab=connections` falls back to Overview because the tabs are filtered before the active tab is resolved.
> - The Policy, Workflow, Tool and Endpoint detail panels mount only after a graph-node selection, which approvers can't reach.
>
> All of this is within B-253 Q-E (agents plus tools reads). In the DOM:
> - The approver Overview renders only agent name, model, risk_tier, status, owner and scope, all from the already-permitted GET agent.
> - The trimmed panel shows the tool name, designation and note, all already in the tools list response every role can read.
> - The read-only link label (viewer and operator; approvers can't reach endpoints) shows the linked agent's name, falling back to the raw id. Both are already readable by those roles via the agents list and endpoint GET.
>
> No false-security claim: `rbac.ts` and the component comments state the server is the enforcement point.
>
> **(3) XSS or injection: none.** All new values (agent names, note, linked id, tool name, designation) are rendered as React text children or attribute values, so they are auto-escaped. There is no `dangerouslySetInnerHTML` in the touched directories. The note is parameterized in SQL (`$12`), and the gateway never reads it (no Go consumer outside `eami-api` store/api).
>
> **(4) Role-string mismatch:** harmless today; see finding 3.
>
> **(5) Does the test pin the property?** Yes, with the caveats in finding 4.
>
> ### Verdict
>
> The change set is sound from a security standpoint. The server remains the sole enforcement point, and the operator note-only path cannot modify any field other than `data_handling_note`: the type check, the org-scoped role check with reject-before-write, and COALESCE preservation all hold. The UI changes only remove controls. The one new request pattern, disabling `/connections` for approvers, reduces what the approver's session requests; approvers' requests stay within Q-E. No Critical, High or Medium issues. Recommended follow-ups:
> - Cap the note length server-side (Low, predates C0).
> - Widen the `User.role` type.
> - Run `TestRBACSplit_ToolFields_RealDB` against a real Postgres before committing, since it skipped in this review.

## 7. Disposition of findings

| Finding | Action |
|---|---|
| CR-1 backdrop closes the note panel mid-save | **Fixed:** `SlideOverPanel onClose` is a no-op while pending (the new panel only; `EditToolPanel`'s identical existing behaviour is out of scope and logged in NOTES.md) |
| CR-2 operator suspend: the control silently vanishes | **Fixed:** a success toast for non-admins, on the Agents list and the Actions tab. Admin gets no toast, verified live. |
| CR-3 / SR-3 `authStore` `User.role` type stale | **Logged** in NOTES.md (outside scope; harmless because `rbac.ts` types the role as `string`) |
| CR-4 stale `?tab=` for a hidden tab | **Fixed:** rewritten to `?tab=overview` (verified live) |
| CR-5 inner `can.testTool` check is redundant | **Kept** as defence if the column condition changes, as the reviewer allowed |
| CR-6 `format.ts` comment | **Fixed** |
| SR-1 no length cap on `data_handling_note` (predates C0) | **Not fixed** (server change outside this UI brief). **Proposed to the founder as a new B-ID**, not minted without confirmation. |
| SR-2 note last-write-wins | **Logged** in NOTES.md |
| SR-4 test ran only skipped in review | **Resolved:** it ran against real Postgres here: the focused run, both mutation runs, and the full package `ok` |

After the fixes: `tsc` and `vite build` pass, and the final live run is 77/77 PASS.
