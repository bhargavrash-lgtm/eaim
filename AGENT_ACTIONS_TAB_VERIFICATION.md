# Agent Detail Actions Tab — Verification Record

Written 2026-09-27 by Claude Code. The brief was: add a DESIGN_SYSTEM.md §7.7 section, then give Agent Detail a real Actions tab.

**Founder decisions (all approved):**
- three tabs (Overview, Connections, Actions);
- the Actions tab is read-only for viewers;
- revoked-agent behaviour stays identical to the list page, with the gap logged as B-230;
- the dead "More actions" button is removed;
- `AgentConfigPanel` is extracted into its own file;
- after a delete, Agent Detail redirects to the list;
- step-up authentication gets its own B-ID, B-231, and is not built here.

**Checkability.** Everything below quotes command output or review reports verbatim.
- **Raw logs** are in Claude Code session `b3cde494-55e6-4fd7-b83d-5f55f606e27f`'s scratchpad: `act_run.log`, `act_run2.log`, `act_before.txt`, `act_after.txt`, `act_after2.txt`, `act_ids.txt`.
- **Review transcripts** are in that session's subagent records.

## 1. What changed

| File | Change |
|---|---|
| `DESIGN_SYSTEM.md` | **Commit `8840335`, committed separately (Part 0).** Adds §7.7, verbatim, with one attributed verification note. This commit also updates the Actions row to Built. |
| `eami-ui/src/components/agents/AgentConfigPanel.tsx` (new) | `ConfigPanel` moved out of `AgentsPage.tsx` unchanged. The diff against HEAD shows only the rename/export and one trailing blank line. **Then one deliberate change:** a failed save now shows an error instead of an unhandled rejection. |
| `eami-ui/src/pages/gateway/AgentsPage.tsx` | Imports the moved panel. The list's Configure/Suspend/Reactivate/Delete buttons and handlers are otherwise byte-identical (code review confirmed). |
| `eami-ui/src/components/agents/AgentActionsTab.tsx` (new) | The four actions call the same hooks with the same toggle rule, ConfirmDialog copy and error handling. Approved differences: the redirect after delete, read-only for viewers, and the shared `Button` with `isLoading`. |
| `eami-ui/src/pages/gateway/AgentDetailPage.tsx` | Tab bar in `?tab=`, following SettingsPage's pattern, with Overview as the default. Accessibility: `type=button`, `aria-controls`, roving `tabIndex`, Left/Right arrow keys, and `role=tabpanel`. The Actions panel stays mounted while hidden. The "More actions" button is removed. |
| `eami-ui/src/hooks/useAgents.ts` | **(a) Configure was broken on both surfaces before this brief.** `useAgentConfig`/`useUpdateAgentConfig` read `localStorage['access_token']`, which nothing writes, so every config request got a 401. They now use `apiFetch`, as CLAUDE.md's API-access rule requires. **(b) `useDeleteAgent`** refreshes every agents query except the deleted agent's own detail entry, which is marked stale without being refetched. That removes the redirect-delaying 404-and-retry and prevents a cached "ghost" of the agent after a list-page delete. |

## 2. Verification

**Builds and checks:** `npx tsc --noEmit` → 0, `npx vite build` → built, `git diff --check` → clean. `eami-ui` has no UI test framework, so these changes are covered by the live runs below. No Go code changed.

**Live acceptance, final run: 23 of 23** (`act_run2.log`, verbatim):
```
PASS [AC3] found a real (non-fixture) agent with real connections for regression checks :: b059-live-agent: tools=3 policies=1 workflows=1 endpoint=false
PASS [AC3] Agent Detail shows exactly the three tabs, Overview selected by default :: ["Overview","Connections","Actions"]
PASS [AC3] Overview: OWNER and SCOPE grid shows the real values :: owner=b059-verify
PASS [AC3] dead "More actions — not built yet" button is gone :: count=0
PASS [AC3] Connections: relationship graph renders the real connected node :: node "b046-live-verify" visible
PASS [AC3] Connections: clicking a graph node still opens its SlideOverPanel :: panel with node name opened
PASS [AC3] deep link ?tab=connections opens the Connections tab :: aria-selected=true
PASS [safety] deep link ?tab=actions fires no write request on load :: writes on load=0
PASS [AC1] TAB Configure: opens the same panel, saves, server reads back scan_interval=777 :: server=777 pending={"disabled":true,"spinner":1}
PASS [AC1] TAB Suspend: server status=suspended, button flips to Reactivate, header pill updates, spinner while pending :: server=suspended pending={"spinner":1}
PASS [AC1] TAB Reactivate: server status=active, button flips back to Suspend :: server=active
PASS [AC1] TAB Delete with history: 409 message shown inside the still-open dialog; agent still exists :: server GET=200 url=/gateway/agents/73fa980e-f188-4a7a-83ec-f01e5902a81f
PASS [AC1] TAB Delete: server 404 afterwards, redirected to /gateway/agents, no "Agent not found" shown :: server GET=404 url=/gateway/agents
PASS [AC2] LIST Configure: moved panel opens from the row, saves, server reads back 888 :: server=888
PASS [AC2] LIST Suspend: server status=suspended :: server=suspended
PASS [AC2] LIST Reactivate: server status=active :: server=active
PASS [AC2] LIST Delete with history: 409 shown in dialog, agent still exists :: server GET=200
PASS [AC2] LIST Delete: server 404, row gone, still on the list page :: server GET=404
PASS [AC2] LIST Delete then revisit cached detail URL → "Agent not found" (no cached ghost) :: Agent not found shown
PASS [console] admin session: no console/page errors except the deliberate 409 and ghost-check 404 network logs :: [2× 409 (Conflict), 4× 404 (Not Found)]
PASS [viewer] viewer: Actions tab is read-only (notice, no Configure/Suspend/Delete buttons) :: notice shown, 0 action buttons
PASS [viewer] viewer: list page unchanged (row buttons still rendered, as before this brief) :: Configure/Suspend/Delete present
PASS [console] viewer session: zero console/page errors :: []
23 checks, 23 passed, 0 failed
```

The four 404s come from the deliberate ghost-check revisit: 2 queries (agent and connections), each tried twice under the global `retry: 1` (`lib/query.ts:7`).

**Earlier run.** The first run stopped at TAB Configure, because "Config saved" never appeared. That was the pre-existing 401 bug in (a) above, confirmed by the security review (L-1) and by reading the hook. It was fixed, rerun (22 of 22), then rerun again with the ghost check after the final hook change (23 of 23).

**Server-side audit trail is identical on both surfaces.** Lifecycle events, verbatim:
```
actfix-detail: suspended → reactivated → deleted  (performed_by=actfix-admin@example.test)
actfix-list: suspended → reactivated → deleted  (performed_by=actfix-admin@example.test)
```
Refused (409) deletes correctly wrote no event.

**Fixture cleanup, both cycles.**
- Fixture passwords were hashed in-DB via `pgcrypto` for the new rows only.
- Each cycle ran in one transaction, deleting in dependency order: test-generated lifecycle events, fixture approval requests, fixture agents, fixture users. Output was `DELETE 6/2/2/2` both times.
- `diff act_before.txt act_after.txt` and `diff act_before.txt act_after2.txt` were both **identical**. The snapshot covers org, user, agent, config, lifecycle and approval counts, and every agent's status and config row.
- The residual scan found **0** both times.

## 3. Reviews (all completed; quoted verbatim)

| Pass | Completed? | On |
|---|---|---|
| Code review | Yes | initial build |
| Security review | Yes | initial build |
| Code review, delta | Yes | fixes for its Medium and 2 Lows, plus the Configure auth fix |
| Security review, delta | Yes | the same delta |
| Code review, final confirmation | Yes | the stale-mark change (its own suggested fix for its delta Low) |

### 3a. Code review, initial

> **Verdict: approve with one Medium fix recommended.** The move is verbatim, AgentsPage is otherwise untouched, and `tsc --noEmit` passes (exit 0). The only real issue is how a successful delete navigates away from the detail page.
>
> **[Medium] The delete success path refetches a 404 before it navigates. The navigation also depends on microtask ordering.**
> - Location: `eami-ui/src/components/agents/AgentActionsTab.tsx:49-58`, together with `eami-ui/src/hooks/useAgents.ts:123`.
> - `useDeleteAgent`'s hook-level `onSuccess: () => qc.invalidateQueries({ queryKey: ['agents'] })` returns a promise.
> - TanStack Query v5.101 awaits that promise (`query-core/src/mutation.ts:246`) before it dispatches success. The mutate-level `onSuccess` (the `navigate`) only runs after that dispatch.
> - The invalidation prefix-matches the page's active `['agents', id]` query, so it refetches right away.
> - Scenario: the admin confirms delete, the DELETE returns 204, then `GET /agents/{id}` returns 404. The global `retry: 1` (`src/lib/query.ts:7`) waits about 1s and gets a second 404. Only then does `navigate('/gateway/agents')` fire. So the user sees the dialog spinner for about 1s longer, and 2 wasted 404 requests go out. That is the exact thing the code comment says it avoids ("so the page doesn't refetch a 404").
> - Fragility: when the query errors, `AgentDetailPage.tsx:160` renders "Agent not found." and unmounts `AgentActionsTab`. `MutationObserver.#notify` (`mutationObserver.ts:164`) skips mutate-level callbacks once the observer has no listeners. `navigate` survives today only because the mutation's microtask continuation runs before React's `setTimeout`-scheduled re-render. If the retry or scheduler behaviour changes, the user is stranded on "Agent not found." with no redirect. That is inferred from reading the library source, not reproduced in a browser.
> - Fix: in `useDeleteAgent`, have `onSuccess` call `qc.removeQueries({ queryKey: ['agents', id], exact: true })` before invalidating `['agents']`. The mutation fn gets `id`, so use `(_d, id) => ...`. Alternatively, cancel that query in `onMutate`. This changes a shared hook, but it is safe for the list page because that page doesn't observe the detail key.
>
> **[Low] Switching tabs during a suspend/reactivate silently loses its outcome.**
> - Location: `AgentActionsTab.tsx:41-47`, and the tab unmount in `AgentDetailPage.tsx:271`.
> - The suspend button is not modal, so a tab click mid-request unmounts the tab. The observer unsubscribes and a 403/409 `onError` is dropped with no message.
> - On returning to Actions, the new `useMutation` instance shows `isPending=false` even if the first request is still in flight, so it can be double-clicked.
> - This does not happen on delete or configure, because ConfirmDialog and SlideOverPanel overlays block the tab bar.
> - Fix (optional): keep `AgentActionsTab` mounted and hidden, or give the mutation a `mutationKey` and use `useIsMutating`. Or accept this; a successful change still updates the page because the hook-level invalidation runs regardless.
>
> **[Low] The tab bar has only partial tab semantics.**
> - Location: `AgentDetailPage.tsx:193-209`.
> - It has `role="tablist"`, `role="tab"` and `aria-selected`, but no `id`/`aria-controls`, no `role="tabpanel"` on the content, no roving `tabIndex`, and no arrow-key navigation. The buttons also lack `type="button"`, which is harmless here because they aren't inside a form.
> - It is still an improvement over SettingsPage, which has no roles at all.
> - Fix: add `id={`tab-${id}`}` and `aria-controls`, wrap each panel in `role="tabpanel" aria-labelledby`, and set `tabIndex={selected ? 0 : -1}` with Left/Right key handling.
>
> **[Info] Error-banner behaviour differs slightly from the list.**
> - Location: `AgentActionsTab.tsx:103, 132`.
> - On the list, cancelling a failed delete keeps the error banner on the page (`AgentsPage.tsx` `onCancel={() => setDeleteTarget(null)}`). The tab clears it on cancel and hides the banner while the dialog is open.
> - This is arguably better, but it is not one of the listed approved differences.
>
> **[Info] Overview is now the default tab.**
> - The relationship graph used to be the first thing on the page. It now needs a click on Connections, as the brief specifies.
> - `useAgentConnections` still fetches on every tab (same as before, no regression).
>
> **[Info] Role is read from the persisted auth store, not decoded from the JWT.**
> - `user.role` comes from the login response. The Go `requireRole("admin","operator")` check on POST/PATCH/DELETE agents and PUT config (`router.go:329-337`) reads the JWT's role.
> - These match at login. If a role changes mid-session, the UI just shows buttons that return a 403 with the server message, which the tab already displays.
> - `platform_admin` users see the read-only notice. That is correct, because `requireRole` checks the exact role and does not include platform_admin.
>
> **Verified clean**
> - **Verbatim move.** I extracted the schema and ConfigPanel block from `git show HEAD:.../AgentsPage.tsx` and diffed it against `AgentConfigPanel.tsx`. The only differences are `function ConfigPanel` → `export function AgentConfigPanel` and one trailing blank line. The imports cover everything used. The local toast was moved as is (pre-existing, as you noted).
> - **AgentsPage.** The diff only removes the panel and its two now-unused hook imports, adds the import, and swaps the JSX name at line 295. `handleToggleSuspend`, `handleConfirmDelete`, the row buttons (lines ~216-223), the banner and the ConfirmDialog are all byte-identical. The remaining `useState`/`useForm`/`z`/`zodResolver`/`SlideOverPanel` imports are still used by AddAgentPanel, and `noUnusedLocals: true` plus a clean tsc confirms that.
> - **Behaviour parity in AgentActionsTab.** The toggle rule is the same (`suspended ? 'active' : 'suspended'`). Error extraction is equivalent (`err?.message ?? fallback`, same fallback strings). The ConfirmDialog title, description, confirmLabel, `destructive` and `isLoading` are identical. The dialog stays open on error, with the error shown in its children slot.
> - **Hook invalidation.** `useUpdateAgent` invalidates `['agents']`, which prefix-matches `['agents', id]`. The detail page's status and label refresh, and the button spinner stays up until the refetch finishes, because the awaited hook `onSuccess` keeps `isPending` true.
> - **CLAUDE.md rules.** Suspend uses `Button isLoading`. Delete is disabled during the update. ConfirmDialog's Cancel is `disabled={isLoading}` (`ConfirmDialog.tsx:35`). The Button variants `outline`/`destructive` and size `sm` exist. No new hand-rolled toast. Tokens (`brand-600`/`brand-700`, `ink`, `shadow-l1`, and the hand-applied metadata-card border and padding) match existing usage and SettingsPage.
> - **"More actions" removal.** `MoreVertical`, `Wrench` and `WorkflowIcon` were removed from the imports. No references to `agentDetailMoreActions` remain; the only remaining hit is a comment. `AppTopBar` without `action` compiles.
> - **Graph panels.** The `selected?.kind` SlideOverPanels are still rendered outside the tab conditionals, so they work from Connections. They are modal, so you can't switch tabs while one is open.
> - **`?tab=` handling.** It follows SettingsPage exactly (`setSearchParams({tab}, {replace:true})`). An invalid or missing value falls back to overview. The page had no other search params to clobber.
> - **Scope.** CONTEXT.md's diff is only the ACTIVE AGENT marker line.
>
> Not verified: nothing was run in a real browser, and the timing claims in the Medium finding come from reading the TanStack source, not from a reproduction.

**Disposition:**
- **Medium:** fixed. The final form, after the delta review, is stale-without-refetch in the hook.
- **Both Lows:** fixed.
- **Info (banner):** kept, and disclosed here.
- **Info (default tab):** as specified.
- **Info (role):** accepted; the server enforces it.

### 3b. Security review, initial

> **Verdict:** The diff (the Actions tab on Agent Detail plus the extraction of AgentConfigPanel) introduces no new server-side exposure and no new vulnerability. It calls the same endpoints through the same hooks and the same role-gated routes, so it is safe to ship as a UI change. While checking the endpoints it depends on, I found two pre-existing issues worth fixing separately: one High (a cross-tenant write) and one Medium (clickjacking). `npx tsc --noEmit` in eami-ui passes cleanly.
>
> **H-1 (High, pre-existing, not introduced here): cross-tenant write on PUT /v1/gateway/agents/{id}/config.**
> - **Where:** `eami-api/internal/api/agents.go:688-699`, with `eami-api/internal/store/agent_configs.sql.go:30-35` and `:60-72`.
> - **What happens:** `UpdateAgentConfig` calls `GetAgentConfig(agentID)`, which looks up by agent id only (`WHERE agent_id = $1`, no org filter). The org check `GetAgent(agentID, uc.OrgID)` runs only on the error branch, when no config row exists. `CreateAgent` always seeds a config row (`agents.go:183`). The live DB confirms 0 agents without a config row. So the org check effectively never runs.
> - **Scenario:** an admin or operator in org A who knows an agent UUID from org B can send `PUT .../config` and overwrite B's `scan_interval_seconds`, `model_scan_paths` and `enabled_scanners`. That means they could turn off B's endpoint scanners or redirect scan paths. The 200 response also echoes B's config back.
> - **RLS won't catch it:** RLS is off on `agent_configs` and `gateway_agents` (`relrowsecurity=f`), and `eami_app` is superuser/bypassrls.
> - **What limits it:** agent ids are random v4 UUIDs, so the attacker needs a leaked id. I did not audit whether ids leak across tenants.
> - **Relation to this diff:** none, in either direction. The UI loads the agent through the org-scoped GET first, so a foreign id shows "Agent not found" (`AgentDetailPage.tsx:161-163`) and the tab never renders. The attack is a direct API call either way.
> - **Fix:** always call `GetAgent(agentID, uc.OrgID)` before reading or upserting, or scope the config SQL by joining `gateway_agents` on `org_id`. Add a cross-org real-Postgres test. This needs its own B-ID.
>
> **M-1 (Medium, pre-existing, made slightly worse by this diff): SPA HTML is served without X-Frame-Options, so the app can be framed (clickjacking).**
> - **Where:** `eami-ui/nginx.conf:18-20`, `:32-34`, `:37`.
> - **Cause:** the server-level `add_header X-Frame-Options "SAMEORIGIN" always` is not inherited by `location = /index.html`, because that block declares its own `add_header Cache-Control`. That is standard nginx behaviour: a location that has any `add_header` drops all inherited ones. Every SPA route falls through `try_files ... /index.html` into that block, so the HTML has no frame protection. There is also no CSP `frame-ancestors`.
> - **Evidence:** this is from static reading of the config. The local container is the Vite dev server on port 5173, which sends only `Cache-Control: no-cache`, so I could not test production nginx live.
> - **Why the new tab matters:** `/gateway/agents/<id>?tab=actions` puts a one-click **Suspend/Reactivate** button (no confirmation) in a fixed position for a known agent. That is a better clickjacking target than the list, where row position varies. Delete still needs a second click in ConfirmDialog.
> - **Fix:** repeat the security headers inside `location = /index.html` (or use an include) and add `Content-Security-Policy: frame-ancestors 'self'`.
>
> **L-1 (Low, pre-existing, affects both surfaces): Configure fails closed and never shows the failure.**
> - **Where:** `eami-ui/src/hooks/useAgents.ts:151` and `:168`.
> - **Cause:** `useAgentConfig` and `useUpdateAgentConfig` read `localStorage.getItem('access_token')`. The auth store persists under `'eami-auth'` (`authStore.ts:49`), and nothing in the codebase writes an `access_token` key.
> - **Effect:** every config GET/PUT sends `Bearer ` and gets a 401 from `jwtMiddleware`. It fails safe, but the feature is effectively broken. `AgentConfigPanel`'s `onSubmit` awaits `mutateAsync` with no catch, so the rejection is unhandled and the user sees no error.
> - **Fix:** use `apiFetch()` (or the generated client) so the token comes from the auth store, and surface the mutation error. This also brings the hooks in line with the rule against raw `fetch` in `CLAUDE.md`.
>
> **Info-1: B-230 and B-231 are cited but not minted.**
> - **Where:** `AgentActionsTab.tsx:9` and `:40`.
> - **Detail:** the comments cite B-230 (the revoked-agent gap) and B-231 (step-up auth), but `BACKLOG.md:2479` still reads "Next B-ID: B-230", so neither exists yet. Mint them with founder confirmation, per the memory note, or reword the comments.
>
> **Info-2: error text shown to users is the same as on the list.**
> - `AgentActionsTab` shows `err.message`, which is the server's `{message}` body.
> - `UpdateAgent`'s 500 path (`agents.go:249`) and `DeleteAgent`'s 500 paths (`:293`, `:311`) echo `err.Error()`, so the new tab would display raw driver/DB text on a 500.
> - The UI only sends `active` or `suspended`, both valid under the CHECK constraint, so reaching a 500 needs an infrastructure fault. Only admins and operators see it, which is the same exposure as the list.
> - The long-term fix is server-side: return a generic 500 message and log the detail.
>
> **Checked and clean**
> - **Server-side authorization is unchanged.** `router.go:329-337`: PATCH, DELETE, POST and PUT `/config` sit in `requireRole("admin","operator")`, inside the `jwtMiddleware` group (`:234-235`). `requireRole` (`middleware.go:115-131`) is an exact match on the JWT role. GETs are admin/operator/viewer plus `viewerReadOnly` (`:380-393`). The tab's `WRITE_ROLES` matches the server exactly. The UI gate is presentation only: a viewer who bypasses it gets a 403. The UI gate is never the only control.
> - **Org scoping is by JWT org** (except the config write, H-1). `UpdateAgent` passes `OrgID: uc.OrgID`, and the SQL is `WHERE id=$1 AND org_id=$2` (`agents.sql.go:110-118`); a foreign id returns ErrNoRows, which becomes a 404. `DeleteAgent` runs `GetAgent(id, uc.OrgID)`, then `DELETE ... WHERE id=$1 AND org_id=$2` (`agents.sql.go:137-144`). `GetAgent` and `GetAgentConfig` are org-checked. A foreign `:id` in the URL gives "Agent not found".
> - **CSRF does not apply.** Auth is a Bearer header injected from the Zustand store (`client.ts:15-20`, `:55`). `authStore` persists to localStorage, not cookies, so a browser holds no ambient credentials. The API sits behind the same-origin nginx `/v1/` proxy.
> - **Deep links cannot trigger actions.** `?tab=` only selects which panel renders. It is validated against a fixed `TABS` allowlist, and anything else falls back to Overview (`AgentDetailPage.tsx:151-156`). Nothing mutates in render, effects or load. Every mutation sits in an `onClick` handler, and delete needs the dialog's explicit Confirm.
> - **The delete dialog holds up.** The title uses `agent.name` as React text, so it is escaped. `isLoading` disables Cancel and spinner-locks Confirm (`ConfirmDialog.tsx:35-41`). The dialog stays open on error to show the 409. Navigation happens only in `onSuccess`, and the delete targets `agent.id` from the org-scoped fetch.
> - **Audit events fire exactly as before.** Both surfaces call the same `useUpdateAgent` and `useDeleteAgent` (`useAgents.ts:99-125`) with the same bodies (`{status:'active'|'suspended'}`), so `InsertAgentLifecycleEvent` runs identically (`agents.go:258-270`, `:314-316`), with `PerformedBy` taken from the JWT. The `['agents']` invalidation also refreshes the detail query key `['agents', id]`, so the toggle label does not go stale.
> - **The list page behaves the same after the refactor.** `AgentsPage` now renders the extracted `AgentConfigPanel`, whose logic moved over verbatim, and its toggle and delete logic is unchanged.
>
> **How reachable the revoked-agent gap is today**
> - **What can set `status='revoked'`:** only a direct `PATCH /v1/gateway/agents/{id}` with `{"status":"revoked"}` from an admin or operator using the raw API (`UpdateAgent` accepts any status, and the DB CHECK allows `active`/`suspended`/`revoked`), or a direct DB write.
> - **Nothing else writes it.** No UI sends it. No other code writes `gateway_agents.status`: grep found only `agents.sql.go:110`. `eami-gateway/internal/identity/events.go:64` writes "revoked" to the token event log, not to agent status. The live DB has 0 revoked agents (8 active, 4 suspended). Revocation is also not audited, since `UpdateAgent`'s switch has no revoked case.
> - **Real-world effect:** the UI can turn revoked into suspended, and suspended back into active, in two clicks. Those two clicks are logged as "suspended" then "reactivated". The same role could already `PATCH {"status":"active"}` directly, so the UI grants no privilege the server doesn't already allow.
> - **Assessment:** low reachability and low severity today. It becomes real once anything starts issuing revocations; at that point the server should enforce revoked as terminal.
>
> **Uncertainty:** H-1 needs a leaked foreign agent UUID; I did not audit cross-tenant id leakage. M-1 comes from reading the nginx config, not from a live production test.

**Disposition:**
- **L-1:** fixed in this brief, because it blocked AC1. Configure was broken on both surfaces.
- **H-1, M-1, and Info-2 (the raw `err.Error()` 500 echo):** pre-existing, and outside this frontend brief. They are **recorded as needing founder-confirmed B-IDs** (§4), with H-1 recommended as urgent.
- **Info-1:** resolved. B-230 and B-231 are minted in this commit under the founder's explicit direction, after a direct check against BACKLOG.md.

### 3c. Code review, delta

> **Verdict: approve.** All four delta items are correct. There is one Low regression on the list-page delete path, and a few Info notes. `npx tsc --noEmit` passes (exit 0).
>
> **[Low] Deleting from the list page now leaves the deleted agent's detail page cached as if it still exists.**
> - Location: `eami-ui/src/hooks/useAgents.ts:126-127`.
> - The predicate now skips `['agents', id]` on every delete, but only `AgentActionsTab` removes that entry afterwards. Deletes from `AgentsPage` never clear it.
> - Before this delta, the list delete marked the cached detail query stale. Now it stays fresh for the global `staleTime: 30_000`.
> - Scenario: open Agent X's detail page, go back to the list, and delete X within 30s. Then use browser Back (or any link to `/gateway/agents/X`). The page renders the deleted agent from cache with no refetch, with a working-looking Actions tab. Its buttons would then fail with a 404.
> - This lasts until the entry goes stale (30s) or is garbage-collected (5 min). It is a UI ghost only; no data is at risk.
> - Fix: in the same `onSuccess`, also mark that entry stale without refetching it: `qc.invalidateQueries({ queryKey: ['agents', id], exact: true, refetchType: 'none' })`. Alternatively, move the `removeQueries` into the hook so both callers get it.
>
> **[Info] Removing the detail query while the page still shows it does not refetch, but only by luck of ordering.**
> - Location: `AgentActionsTab.tsx:55-58`. The reasoning comes from reading the library code, not from a browser test.
> - `navigate` runs first. React Router 7's `createBrowserRouter` applies route changes as a transition. Then `removeQueries` destroys the cached query.
> - Removing a query from the cache does not tell the page's `useAgent` to re-render, so nothing triggers a new query before the route change unmounts the page. I found no other update that would re-render `AgentDetailPage` in that window: the only other re-render at that moment is `AgentActionsTab` itself (a child), from the mutation finishing; the `['agents']` invalidation has already finished; and `['agent-connections', id]` is not invalidated.
> - If something did re-render the page in that window, `useAgent` would build a fresh empty query. That would flash "Loading agent…" and send one 404 GET before the route change lands. It would be cosmetic, not a stuck page, because `navigate` has already been called.
> - Applying the Low fix above instead (stale without refetch, no remove) avoids this question entirely.
>
> **[Info] Two small gaps in the tab accessibility.**
> - Location: `AgentDetailPage.tsx:209`.
> - Overview and Connections are still conditionally mounted, so the inactive tabs' `aria-controls` point at ids that don't exist in the DOM. That is harmless in practice.
> - There is no Home/End key support, and the panels have no `tabIndex={0}`. Both are optional under the WAI-ARIA tabs pattern.
>
> **Verified clean**
> - **1. Delete refresh (`useAgents.ts:126-127`).** The predicate works together with the `['agents']` key filter; it does not replace it. The list query `['agents']` has `queryKey[1] === undefined`, and `undefined !== id`, so the list is still invalidated and refreshed. The only other `['agents', x]` consumer is `useAgent`, which I checked with a repo-wide grep; other agents' detail queries are still invalidated. `onSuccess` uses the `(_data, id)` signature, so `id` is the string passed to `mutate`. The redirect no longer waits on a 404 refetch plus retry.
> - **2. Actions tab kept mounted.** `AgentActionsTab` sits inside `<div role="tabpanel" hidden=...>`. `ConfirmDialog` and `SlideOverPanel` use no portals, so they render inside that div. The "hidden while the dialog is open" case can't happen: both overlays are full-screen and block the tab bar. Tab changes use `replace`, so browser Back/Forward can't switch tabs. An in-flight suspend's `onError` now survives a tab switch, and `isPending` stays accurate. No extra fetches result: `AgentActionsTab` runs only mutation hooks, and `AgentConfigPanel` still mounts only when opened.
> - **3. Tab accessibility.** Tabs have `type="button"`, `id`, `aria-controls`, `aria-selected`, and a roving `tabIndex` (0 on the active tab, -1 on the rest). Left/Right wrap around using `(i + len - 1) % len`, and focus moves to the next tab by id. All tab buttons are always rendered, so the target exists. Each panel has `role="tabpanel"` and `aria-labelledby`.
> - **4. Config fetch fix (`useAgents.ts`).** `apiFetch` (`src/api/client.ts:49`) takes the token from `useAuthStore`, JSON-encodes the object `body` and sends `Content-Type: application/json`. On failure it throws `ApiFetchError` carrying the server's `{message}`. The response and request shapes match `AgentConfigResp` and `AgentConfigUpdateRequest` (`eami-api/internal/api/agents.go:597-612`). A 50 MB report size fits the int32 field. The GET returns defaults when no config row exists (`agents.go:631`), so a new agent's form is filled in. The only consumer is `AgentConfigPanel` (grep).
> - **AgentConfigPanel change.** Diffed against `HEAD`'s `ConfigPanel`: the only changes are the rename/export, the `saveError` state, the try/catch around `mutateAsync`, and the red message block. The success toast still appears only on success, as before, because a failure throws before `setToast`. The button still uses `isLoading={update.isPending}` and Cancel is still disabled while saving. Not addressed and unchanged from before: if the GET itself fails, the panel shows an empty form with no error. The form's validation still blocks submitting it.

**Disposition:**
- **Low:** fixed with the suggested stale-without-refetch mark, and the tab's `removeQueries` removed (which also settles the Info). Live-verified by the ghost check.
- **Accessibility Info items:** accepted as optional.
- **"GET failure shows an empty form":** recorded as pre-existing.

### 3d. Security review, delta

> **Verdict:** Clean. The delta adds no security exposure and does not change how exploitable H-1 or M-1 are. I found two Info items, both server-side or UX, neither blocking. `npx tsc --noEmit` in eami-ui passes.
>
> **Info-A: the new config save error display shows raw server text on a 500.**
> - **Where:** `eami-ui/src/components/agents/AgentConfigPanel.tsx:73-75` shows `err.message`. `apiFetch` (`eami-ui/src/api/client.ts:66-73`) passes through the server's `{message}`.
> - **Error paths in `UpdateAgentConfig`:** every 400 message is static text (`agents.go:659-686`). The 404 says only "agent not found" (`:691-693`); that is the same text for "doesn't exist" and "belongs to another org", so it reveals nothing. The only raw message is the 500 when the config save fails (`agents.go:720-722`, `writeError(..., err.Error())`). It can show pgx/Postgres text such as constraint or table names.
> - **Who sees it:** only an authenticated admin or operator, and only on a database fault. The UI sends values that pass the server's validation (the zod ranges match the server's). This is the same class of issue as the existing `UpdateAgent`/`DeleteAgent` 500 paths.
> - **Fix:** server-side. Return a generic "internal error" and log the detail. That can go under the same future B-ID as the other `err.Error()` 500 echoes. Nothing to change in this brief.
>
> **Info-B: deleting from the list page can leave a stale cached copy of the detail page.**
> - **Where:** `eami-ui/src/hooks/useAgents.ts:123-127`.
> - **What happens:** the predicate now skips invalidating `['agents', <deletedId>]` for every caller, but only `AgentActionsTab` removes that entry afterwards (`AgentActionsTab.tsx:55-58`). Say an admin opened the agent's detail page earlier, then deleted it from the list, then went back to `/gateway/agents/<id>`. React Query shows the cached agent while `useAgent` refetches: it has no `staleTime` (`useAgents.ts:36-45`), so it refetches as soon as the page opens. The refetch returns 404, and the page switches to "Agent not found" (`AgentDetailPage.tsx:161-163`).
> - **Security relevance:** none. Any click during that brief window still reaches server routes that are org-scoped and role-gated, and gets a 404. Nothing crosses tenants, since the cache is per-browser and per-session.
> - **Optional tidy-up:** call `removeQueries` for that key inside the hook's own `onSuccess`, so both surfaces behave the same.
>
> **The three questions**
>
> **1. Does the config hook fix change H-1's practical exposure?** No.
> - H-1 is exploited with a direct API call using any valid admin or operator token. The UI was never part of that path.
> - The UI builds the URL only from `agent.id` (`AgentConfigPanel.tsx:62`, from the `agent` prop). On Agent Detail that comes from the org-scoped `GET /v1/gateway/agents/{id}`; on the list it comes from the org-scoped list.
> - A foreign id in the page URL gives "Agent not found", so the Configure panel never mounts.
> - Making Configure work adds legitimate same-org writes only. It gives an attacker no new way to supply a foreign id.
> - The config GET is org-checked first (`agents.go:620-624`), so the panel can't be used to read another tenant's config either.
> - H-1's severity and preconditions are unchanged: a server fix is still needed, and a leaked foreign agent UUID is still a precondition.
> - Side effect: with a working token, config GET/PUT now go through `apiFetch`'s Bearer from the auth store. That is consistent with every other call, and there are no cookies, so CSRF still does not apply.
>
> **2. Does the `useDeleteAgent` change matter for security?** No.
> - The predicate `q.queryKey[1] !== id` still invalidates the list (`['agents']`, where `key[1]` is undefined) and every other agent's detail query.
> - The only change is which cached entries refetch or clear, all within one user's session.
> - The navigate-then-`removeQueries` order in `AgentActionsTab.tsx:55-58` is fine: the removed entry belongs to a page that has already been left.
> - See Info-B for the harmless UX gap.
>
> **3. Can anything fire from the hidden Actions tab?** No.
> - **The panel is fully removed from rendering when hidden.** It is `<div ... hidden={activeTab !== 'actions'}>` (`AgentDetailPage.tsx:285`). Tailwind 3.4.19's preflight enforces `[hidden]{display:none}`, and nothing on the div overrides it. With `display:none`, none of its children are rendered, clickable, or focusable. That includes the `position:fixed` SlideOverPanel and ConfirmDialog.
> - **Nothing escapes that hiding.** Neither the panel nor the dialog uses `createPortal`: grep found none in `SlideOverPanel.tsx` or `ConfirmDialog.tsx`; both render `fixed inset-0` inside the tree.
> - **Clickjacking (M-1) has nothing to hit.** A hidden button is not hit-testable, so a framing attacker can't reach it. The only visible targets are the ones on the active tab, which is the same as my earlier M-1 analysis. The fix is still the nginx header change.
> - **Nothing runs on load or on a tab switch.** Every mutation is in an `onClick` or a confirmed dialog handler. `showConfig` and `confirmDelete` are local state, so no URL can set them. The arrow-key handler (`AgentDetailPage.tsx:158-164`) only changes `?tab=` and moves focus.
> - **Switching tabs with a dialog open cancels nothing and fires nothing.** The overlay covers the tab bar. If a switch did happen (keyboard focus would have to be on the tab bar), the dialog just becomes invisible. Coming back shows it again, and it still needs an explicit Confirm.
> - **An in-flight mutation finishing while hidden only updates state.** It sets the error message or navigates away after a successful delete. Nothing new is sent.
> - **The role gate is unchanged.** Mounting the panel while hidden does not bypass the viewer read-only branch (`AgentActionsTab.tsx:65-71`), and the server's `requireRole("admin","operator")` still applies.
>
> **Verified clean:** `apiFetch` attaches the Bearer token from `useAuthStore.getState()` and sends the JSON body. `PUT` goes to the same admin/operator route (`router.go:337`). The config GET is org-checked. The unhandled-rejection fix is correct: the error is caught and shown in the panel. Neither this delta nor the parent diff touches the lifecycle-audit calls; config writes never logged lifecycle events, before or after. TypeScript check passes.

**Disposition:**
- **Info-A:** belongs to the recorded `err.Error()` echo item.
- **Info-B:** fixed by the same stale-mark change as §3c's Low.

### 3e. Code review, final confirmation (stale-mark change)

> **Verdict: confirmed correct.** I found no new issues. `npx tsc --noEmit` passes (exit 0).
>
> **Findings:** none.
>
> **Verified clean**
>
> **Marking the detail entry stale cannot trigger a refetch.** I checked this in the TanStack Query library source, not in a browser.
> - In `queryClient.ts:291-310`, `invalidateQueries` marks each matched query invalidated. It then returns `Promise.resolve()` when `refetchType === 'none'`, so `refetchQueries` is never called.
> - The invalidate step does notify the page's `useAgent`. That notification (`queryObserver.ts:722-727`) only recomputes the result and resets its timers. It never fetches.
> - The re-render of `AgentDetailPage` that follows does not fetch either: `shouldFetchOptionally` (`queryObserver.ts:791-803`) only fetches when the query object changed or `enabled` was false before, and neither is true here. A mount-time fetch doesn't apply, because the page is already mounted. `refetchOnWindowFocus` is `false` globally, and `useAgent` sets no `refetchInterval`.
> - The only thing still able to refetch it is a browser reconnect event in the tiny gap before `navigate`. That is not realistic.
> - The query stays in success state with its data, so there is no "Agent not found" flash. That matches your live check.
>
> **The returned `Promise.all` keeps the mutation pending until the list refresh finishes.**
> - `mutation.ts:246` awaits `this.options.onSuccess`, and the arrow function returns the `Promise.all`.
> - So `isPending` stays true and success isn't announced until the list refresh settles. Only then does the caller's `onSuccess` run: `navigate` in the Actions tab, `setDeleteTarget(null)` in `AgentsPage`.
> - On `AgentsPage` the `['agents']` list is being watched, so the dialog closes only after the list has refreshed. That is the same timing as `HEAD`.
> - On the detail page the list isn't being watched, so the first invalidate resolves at once and the redirect is immediate. It is marked stale, so it reloads when the Agents list mounts.
> - `refetchQueries` swallows fetch errors unless told otherwise, so a failed list refresh cannot turn a successful delete into an error.
>
> **Keys and filters.** The first call's predicate still refreshes `['agents']` (its `queryKey[1]` is `undefined`) and every other agent's detail entry. The second call targets exactly the deleted agent's entry. The two calls don't overlap, so order doesn't matter. For list deletes, a cached detail entry is stale; a later visit refetches it and gets "Agent not found", which matches your live result.
>
> **`AgentActionsTab.tsx`.** `useQueryClient` and `removeQueries` are gone, with no leftover references (grep). `onSuccess` is only `navigate('/gateway/agents', { replace: true })`, and `onError` is unchanged. `noUnusedLocals` plus the clean tsc confirms there are no dead imports.

## 4. Recorded, not fixed here

**Minted in this commit, with the founder's explicit direction** (free-ID check: the counter read B-230, and no open item overlapped):
- **B-230:** revoked-agent status transitions are not enforced.
- **B-231:** step-up authentication for sensitive actions.

**Need founder-confirmed B-IDs** (pre-existing; found by §3b):
1. **H-1 (High), recommended urgent.** A cross-tenant write on `PUT /v1/gateway/agents/{id}/config`: the org check never runs, because a config row always exists.
2. **M-1 (Medium).** The nginx `location = /index.html` drops the server-level `X-Frame-Options`, and there is no CSP `frame-ancestors`.
3. **The raw `err.Error()` echo in API 500 responses.** It appears in UpdateAgent, DeleteAgent, UpdateAgentConfig and others; it is an app-wide hygiene issue.

**Other notes, not given B-IDs:**
- The roadmap file has no step-up entry. B-231 records Horizon 1 as its intended placement; the roadmap itself was not edited.
- `AgentConfigPanel` still uses a page-local success message instead of `useToast` (moved verbatim, pre-existing).
- A failed config GET shows an empty form with no error (pre-existing).
