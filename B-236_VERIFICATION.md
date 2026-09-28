# B-236 Verification Record — agent-config reads no longer fall back to defaults on a database error

Written 2026-09-28 by Claude Code, at founder direction (task brief plus Part A decisions). Roadmap: **Horizon 0 hardening**; this fixes the already-shipped Discovery item's remote scanner config. Raw logs are in session `b3cde494-55e6-4fd7-b83d-5f55f606e27f`'s scratchpad:
- `b236_mutation.log` (server);
- `b236_ui_mutation_final.log` (UI);
- `b236_live.log`, plus `b236_live_run1_failed.log`, the first run that found the UI bug;
- `b236_api_full.log`;
- `b236_before.txt` and `b236_after.txt`;
- `shots_b236/`.

## 1. Part A findings (reported before building)
**Server:**
- **Endpoint config route** (`GET /v1/agents/{agent_id}/config`, `agent_config_remote.go`):
  - any `GetAgentConfig` error served **200 + defaults** (the bug);
  - any `GetDefaultOrgID` error served 503 "no org / run reseed";
  - endpoint resolution was already correct.
- **Admin route** (`GET /v1/gateway/agents/{id}/config`, `agents.go`):
  - any `GetAgent` error returned 404;
  - any `GetAgentConfig` error returned **200 + defaults**.
- `pgx.ErrNoRows` is the only case where defaults are correct. The store returns the raw `Scan` error, so `errors.Is` can tell it apart.

**Endpoint agent:**
- The collector proxy passes the status and body through unchanged.
- `eami-agent` `FetchConfig`: 404 changes nothing; **any other non-200 returns an error before touching the config**, so the agent keeps its last-known-good in-memory config and retries on the next cycle.
- **The server fix is therefore safe with no agent change.** The agent was deliberately not changed. Two existing behaviours stay out of scope:
  - the failure is logged at Debug, now B-249;
  - after a restart, the agent uses its local file until the next successful fetch (founder: existing, out of scope).

**UI:**
- `AgentConfigPanel` is shared by Agent Detail's Actions tab and the Agents list.
- Before this fix it showed server-served defaults as real, and **Save would write them over the real config**.
- After the server fix alone, it would have shown an empty but submittable form, because it checked only `isLoading`.

## 2. The fix
- **`agent_config_remote.go`:**
  - `GetDefaultOrgID`: 503 `no_org` **only on `ErrNoRows`**, otherwise a generic 500;
  - endpoint resolve: 500 (now logged);
  - config read: **defaults only on `ErrNoRows`**, otherwise 500 `{"code":"internal_error","message":"failed to load agent config"}`, with `slog`.
- **`agents.go` `GetAgentConfig`:**
  - `GetAgent`: 404 **only on `ErrNoRows`**, otherwise the same generic 500;
  - config read: defaults only on `ErrNoRows`, otherwise 500.
- **`AgentConfigPanel.tsx`:** on a query error it shows an error panel instead of the form:
  - §2 Danger tokens `bg-status-danger`/`text-status-danger-text`, and `shadow-l1`;
  - a **Retry** button using `Button isLoading`;
  - **no form and no Save button**; Cancel becomes Close.
  - A `retrying` state keeps the panel mounted during Retry. See §4: the first live run found that with no cached data, a TanStack Query refetch (`@tanstack/query-core` 5.101.0) resets the query to `pending`, so the panel flipped to "Loading…" and the Retry spinner was never visible.
- **Design-system check (`DESIGN_SYSTEM.md` read in full):**
  - mode: Admin;
  - this is a slide-over, not a standalone page, so the §6 top bar doesn't apply;
  - §7.4 honest data: no fabricated settings;
  - §8: no left-border accent, and status colour is used only for the real "broken" state.

## 3. Automated verification
```
eami-api: go build ./... ; go vet ./... → clean; gofmt clean on all changed/new files
eami-api: go test -count=1 -v ./... → every package ok; top-level PASS=498 FAIL=0 SKIP=0 (496 + 2 new)
eami-ui:  npx tsc --noEmit → ok ; npx vite build → ok
```
**New test:** `agent_config_read_fail_pg_test.go` (real Postgres).
- **How the error is forced:** `faultDB` wraps the real pool and rewrites one targeted `QueryRow` to `SELECT 1/0`. **The shared Postgres itself returns division_by_zero (SQLSTATE 22012)** on exactly that read, and nothing is taken down.
- **Both routes:** a distinctive real config (777 s, `[ai_apps]`) is served normally, and a forced DB error returns the exact generic 500 (checked for any leaked DB text), never defaults.
- **Endpoint route, org lookup:** a forced DB error returns 500, not 503. A real empty result (`WHERE false`) still returns 503 `no_org`.
- **Admin route:** a forced DB error on `GetAgent` returns 500, not 404, and an unknown agent still returns 404.
- **Both routes, missing row:** a **genuinely missing row**, deleted deliberately because the insert trigger normally prevents it, still returns 200 with defaults.

**Server mutation checks** (`b236_mutation.log`, verbatim):
```
=== M1: both handlers reverted to HEAD (the B-236 bug itself)
--- FAIL: TestAgentRemoteConfig_DBErrorIsNotDefaults_RealDB (0.07s)
    agent_config_read_fail_pg_test.go:148: config read DB error: 200 "{\"agent_id\":\"89eda4b1-e576-4242-a488-fec020dca08d\",\"scan_interval_seconds\":300,\"model_scan_paths\":[\"/home\",\"/Users\",\"C:\\\\Users\"],\"max_report_si
--- FAIL: TestAdminGetAgentConfig_DBErrorIsNotDefaults_RealDB (0.11s)
    agent_config_read_fail_pg_test.go:208: agent lookup DB error: 404 "{\"code\":\"not_found\",\"message\":\"agent not found\"}\n", want 500 generic error (never defaults)
FAIL	github.com/eami/api/internal/api	0.337s

=== M2: endpoint route — config read serves defaults on ANY error
--- FAIL: TestAgentRemoteConfig_DBErrorIsNotDefaults_RealDB (0.08s)
    agent_config_read_fail_pg_test.go:148: config read DB error: 200 "{\"agent_id\":\"496f904a-d33c-4f0b-9bec-d2c3387f361d\",\"scan_interval_seconds\":300,\"model_scan_paths\":[\"/home\",\"/Users\",\"C:\\\\Users\"],\"max_report_si
FAIL	github.com/eami/api/internal/api	0.383s

=== M3: endpoint route — default-org lookup returns 503 "no org" on ANY error
--- FAIL: TestAgentRemoteConfig_DBErrorIsNotDefaults_RealDB (0.07s)
    agent_config_read_fail_pg_test.go:153: default-org DB error: 503 "{\"code\":\"no_org\",\"message\":\"no org found; run reseed.sql before agents can fetch remote config\"}\n", want 500 generic error (never defaults)
FAIL	github.com/eami/api/internal/api	0.301s

=== M4: endpoint route — missing row returns 500 instead of defaults
--- FAIL: TestAgentRemoteConfig_DBErrorIsNotDefaults_RealDB (0.10s)
    agent_config_read_fail_pg_test.go:168: missing row: 500 {"code":"internal_error","message":"failed to load agent config"}
FAIL	github.com/eami/api/internal/api	0.386s

=== M5: admin route — agent lookup returns 404 on ANY error
--- FAIL: TestAdminGetAgentConfig_DBErrorIsNotDefaults_RealDB (0.25s)
    agent_config_read_fail_pg_test.go:208: agent lookup DB error: 404 "{\"code\":\"not_found\",\"message\":\"agent not found\"}\n", want 500 generic error (never defaults)
FAIL	github.com/eami/api/internal/api	0.616s

=== M6: admin route — config read serves defaults on ANY error
--- FAIL: TestAdminGetAgentConfig_DBErrorIsNotDefaults_RealDB (0.10s)
    agent_config_read_fail_pg_test.go:213: config read DB error: 200 "{\"agent_id\":\"efd2d556-9481-4d48-8c90-45a6db489e20\",\"scan_interval_seconds\":300,\"model_scan_paths\":[\"/home\",\"/Users\",\"C:\\\\Users\"],\"max_report_si
FAIL	github.com/eami/api/internal/api	0.316s

=== M7: admin route — missing row returns 500 instead of defaults
--- FAIL: TestAdminGetAgentConfig_DBErrorIsNotDefaults_RealDB (0.19s)
    agent_config_read_fail_pg_test.go:226: missing row: 500 {"code":"internal_error","message":"failed to load agent config"}
FAIL	github.com/eami/api/internal/api	0.535s

=== control: sources restored
ok  	github.com/eami/api/internal/api	0.378s
```
**UI mutation checks.** Each part of the panel fix was broken in the running Vite dev server and the live Playwright check re-run (`b236_ui_mutation_final.log`, verbatim):
```
(run 1: U1 output truncated by the log filter, U3/control invalid due to the login rate limiter -- re-run below after the 300 s window)

=== U1: error branch removed (form rendered on a failed load)
PASS real config served :: 200 interval=777 scanners=ai_apps
PASS genuinely missing row -> defaults :: 200 interval=300 scanners=ai_apps,models,mcp_servers,cloud_clients,network_activity,browser
PASS unknown agent -> 404 :: 404 {"code":"not_found","message":"agent not found"}
THREW: locator.waitFor: Timeout 15000ms exceeded. | Call log:

=== U2: Save button no longer hidden on a failed load
PASS real config served :: 200 interval=777 scanners=ai_apps
PASS genuinely missing row -> defaults :: 200 interval=300 scanners=ai_apps,models,mcp_servers,cloud_clients,network_activity,browser
PASS unknown agent -> 404 :: 404 {"code":"not_found","message":"agent not found"}
FAIL agent-detail-actions-tab: error panel shown, NO form inputs, NO Save, Close instead of Cancel :: inputs=0 save=1 close=1 served500=2
PASS agent-detail-actions-tab: Retry shows Button isLoading (spinner + disabled) while refetching :: spinner=1 disabled=true
PASS agent-detail-actions-tab: Retry recovers the REAL config (777), Save available again :: interval=777 save=1
FAIL agents-list: error panel shown, NO form inputs, NO Save, Close instead of Cancel :: inputs=0 save=1 close=1 served500=2
PASS agents-list: Retry shows Button isLoading (spinner + disabled) while refetching :: spinner=1 disabled=true
PASS agents-list: Retry recovers the REAL config (777), Save available again :: interval=777 save=1
PASS no write request fired at any point :: []
PASS no uncaught page errors :: []
PASS server config unchanged after the UI runs :: interval=777 scanners=ai_apps
2 FAIL

=== U3: retrying state removed (Retry spinner never visible; panel flips to loading)
PASS real config served :: 200 interval=777 scanners=ai_apps
PASS genuinely missing row -> defaults :: 200 interval=300 scanners=ai_apps,models,mcp_servers,cloud_clients,network_activity,browser
PASS unknown agent -> 404 :: 404 {"code":"not_found","message":"agent not found"}
PASS agent-detail-actions-tab: error panel shown, NO form inputs, NO Save, Close instead of Cancel :: inputs=0 save=0 close=1 served500=2
THREW: locator.isDisabled: Timeout 30000ms exceeded. | Call log:

=== control: source restored (run after the rate-limit window, unmutated source)
ALL PASS
```

## 4. Live verification (rebuilt API container, created 2026-09-28T05:52:46Z; UI from the Vite dev server serving the edited component)
The shared Postgres was never taken down. The UI error was forced with **Playwright request interception**, which answers the config GET with the server's real 500 body; the server paths were forced at test level (§3).

**Run 1 found a real bug** (`b236_live_run1_failed.log`): Retry's button vanished on click, because of the query reset described in §2. This was fixed with the `retrying` state, and the code reviewer re-reviewed and approved the delta (§5c).

**Final run** (`b236_live.log`, verbatim):
```
── (1) admin config route on the rebuilt API
PASS real config served :: 200 interval=777 scanners=ai_apps
PASS genuinely missing row -> defaults :: 200 interval=300 scanners=ai_apps,models,mcp_servers,cloud_clients,network_activity,browser
PASS unknown agent -> 404 :: 404 {"code":"not_found","message":"agent not found"}
── (2) UI error state, both entry points
PASS agent-detail-actions-tab: error panel shown, NO form inputs, NO Save, Close instead of Cancel :: inputs=0 save=0 close=1 served500=2
PASS agent-detail-actions-tab: Retry shows Button isLoading (spinner + disabled) while refetching :: spinner=1 disabled=true
PASS agent-detail-actions-tab: Retry recovers the REAL config (777), Save available again :: interval=777 save=1
PASS agents-list: error panel shown, NO form inputs, NO Save, Close instead of Cancel :: inputs=0 save=0 close=1 served500=2
PASS agents-list: Retry shows Button isLoading (spinner + disabled) while refetching :: spinner=1 disabled=true
PASS agents-list: Retry recovers the REAL config (777), Save available again :: interval=777 save=1
PASS no write request fired at any point :: []
PASS no uncaught page errors :: []
PASS server config unchanged after the UI runs :: interval=777 scanners=ai_apps
ALL PASS
```
Screenshots are in `shots_b236/`: error and recovered states from each entry point.

**Not live-verified: the endpoint service-key route over HTTP.** Calling it needs the platform service key or collector credentials from `.env`, and secrets are not touched. It is covered by the real-Postgres route test through the real HTTP handler.

**Cleanup:**
- deleted the fixture admin (and its refresh tokens) and both fixture agents (their configs cascade); removed the fixture password file;
- `diff b236_before.txt b236_after.txt` is **identical**, including the audit_log count.

## 5. Reviews (both mandatory passes completed; quoted verbatim)
### 5a. Code review
> I found no High or Medium issues in the B-236 review. The change is correct, and within the eami-api handlers no agent-config read falls back to defaults on a non-`ErrNoRows` error. The only remaining unconditional-defaults path is the `s.queries == nil` test branch at `agents.go:655-663`, which production never reaches.
>
> **Commands run (all read-only):**
> - `git diff HEAD -- eami-api eami-ui`; `cat` of the new test file.
> - Greps for `AgentConfigDefaults`, `.GetAgentConfig(` and `GetDefaultOrgID(`, and for the marker SQL strings. Read the store funcs, `newEndpointAgentLinkEnv`, `seedTestOrg`/`seedTestUser`, the FK cascade rules in `schema/`, `useAgentConfig`, the `QueryClient` defaults, Button variants, `tailwind.config.ts` tokens, `DESIGN_SYSTEM.md` §2/§4/§7.4/§8, eami-agent `sender.go`/`main.go`, and the collector `config_proxy.go`.
> - `go build ./...` in eami-api: OK. `go vet ./internal/api/` (compiles the new test): OK. `npx tsc --noEmit` in eami-ui: OK. I did not run any DB tests.
>
> **Correctness**
> - **Info – `errors.Is` works through the store.** `GetAgentConfig` (`store/agent_configs.sql.go:38-49`), `GetAgent` (`store/agents.sql.go:51-63`) and `GetDefaultOrgID` (`store/endpoints.sql.go:259-267`) all return the raw `row.Scan` error, unwrapped. With pgx v5.9.2 that is `pgx.ErrNoRows` for a missing row and `*pgconn.PgError` for `1/0`, so the branches split as intended.
> - **Info – every `AgentConfigDefaults` use was checked:**
>   - `agent_config_remote.go:98-103` and `agents.go:640-646`: only on `ErrNoRows`.
>   - `UpdateAgentConfig` (`agents.go:718-728`): already `ErrNoRows`-gated before this change.
>   - `agents.go:660` and `:770`: the `storeIface` test path only.
>   - No other callers of `GetAgentConfig`.
> - **Info – the agent-side claim is correct.**
>   - `eami-agent/internal/collector/sender.go:160-165`: 404 returns nil and changes nothing; any other non-200 returns an error before `dst` is changed. The in-memory config is kept, including for the 503 `no_org` case.
>   - `cmd/agent/main.go:253-255` just logs the error.
>   - `eami-collector/internal/api/config_proxy.go:59-60` passes the upstream status and body through verbatim.
>   - Leaving the agent unchanged is justified.
> - **Info – agent log level.** The agent logs a fetch failure at Debug (`main.go:254`), so a persistent 500 is invisible on the endpoint at default verbosity. The new server-side `slog.Error` covers that. Possible NOTES.md item; not in scope.
> - **Info – same pattern elsewhere.** `ingest.go:115-120` still maps any `GetDefaultOrgID` error to 503 `no_org`. That is a different path, not a config read, so it is correctly out of scope, but worth a NOTES.md or backlog line.
>
> **Tests (`agent_config_read_fail_pg_test.go`)**
> - **Info – the `faultDB` approach produces a genuine DB error.**
>   - `SELECT 1/0` raises SQLSTATE 22012 at execution, which surfaces at `Scan`: a real `PgError`, not `ErrNoRows`.
>   - `SELECT id FROM orgs WHERE false` (line 156) correctly produces a real `ErrNoRows` for the 503 case.
>   - Each marker matches exactly one store query: `FROM agent_configs` appears only in `getAgentConfigSQL`, `-- name: GetAgent :one` does not match `GetAgentByName`, and the default-org SQL is matched verbatim. Upsert goes through `QueryRow` but is never reached in these GET tests.
>   - The mutex use is correct.
> - **Info – each assertion would fail if its branch were broken.**
>   - Under the old code, lines 148, 153 and 208/213 would get 200 with defaults, 503 and 404 respectively.
>   - `genericLoadError` is an exact body match (including `writeJSON`'s trailing `\n`), and the leak check guards against echoing DB text.
>   - The missing-row checks (lines 164-169 and 222-227) would fail if the `ErrNoRows` branch were turned into a 500.
>   - The distinctive config (777s, one scanner) makes the control reads meaningful.
> - **Info – CLAUDE.md pool rule is met.** `newEndpointAgentLinkEnv` (`endpoint_agent_link_test.go:73`) registers `t.Cleanup(pool.Close)` right after Ping and before any other cleanup, so it runs last. The new file has no `defer pool.Close()`; its only `defer` is `resp.Body.Close()`. The `httptest` servers use `t.Cleanup(ts.Close)`.
> - **Info – rows in the shared default org are cleaned up.**
>   - The remote test inserts an endpoint and a `gateway_agent` into the default org, each with its own `t.Cleanup` DELETE (LIFO order).
>   - `agent_configs` cascades from `gateway_agents`, and `endpoints.gateway_agent_id` is `ON DELETE SET NULL`, so the agent delete cannot be blocked by the link.
>   - The admin test uses a fresh `seedTestOrg`, whose cleanup is registered after the pool's, so it runs before `pool.Close`. Users and `gateway_agents` cascade on org delete.
>   - Nothing leaks.
> - **Low – admin test depends on the default-org env.** `TestAdminGetAgentConfig_DBErrorIsNotDefaults_RealDB` (lines 173-175) uses `newEndpointAgentLinkEnv` only for its pool. It therefore skips when no default org exists, even though the admin route doesn't need one, and it builds an unused `httptest` server. Harmless and documented in the comment.
> - **Low – one error path untested.** The endpoint-resolve 500 path (`agent_config_remote.go` ~74-80) has no test. Only logging was added there, so this is acceptable.
>
> **UI (`AgentConfigPanel.tsx`)**
> - **Info – nothing is saveable in the error state.**
>   - The `<form id="config-form">` is not rendered when `loadFailed` (lines 109-121), and the only external submit, the footer Save button, is hidden (lines 197-201). Enter-to-submit is impossible without a form. The `onSubmit` handler is reachable only via `form.handleSubmit` on that form.
>   - The Retry button correctly uses `isLoading={isFetching}`. In TanStack v5, status stays `error` during a refetch, so the spinner shows on the Retry button while the error panel remains.
>   - The Close button stays enabled because `update.isPending` is false. That is correct.
> - **Low – a later failed refetch hides the form.** If the query first loads and a later refetch (remount or post-save invalidation) fails, `isError` becomes true while `data` is still cached. The panel then switches to the error state and hides the form. `refetchOnWindowFocus` is false, so this is rare. It is arguably the honest behaviour, and react-hook-form keeps its values while unmounted, so this is a note, not a defect.
> - **Info – design-system compliance is met.**
>   - `bg-status-danger` / `text-status-danger-text` map to the documented §2 Danger pair (`#8A2E2E` on `#FBEAEA`), used for genuinely broken state, as §8 requires.
>   - `shadow-l1` matches §4. `rounded-[10px]` matches existing card usage (`AgentActionsTab.tsx:64`, `AgentDetailPage.tsx:271`).
>   - There is no left-border accent (§8), and the panel shows no fabricated defaults (§7.4).
>   - `Button variant="outline"` exists.
> - **Info – pre-existing, out of scope.** The page-local toast (`useState`+`setTimeout`, lines 46, 75-76, 95-99) predates this change and violates the B-182 `useToast()` rule. The save-error box uses raw `bg-red-50` (lines 101-105). Both could be logged to NOTES.md.
>
> **Idiom and scope**
> - **Info – idiom.** The code matches the established `UpdateAgentConfig` pattern exactly: `errors.Is` then `slog.Error` then the generic `writeError`. Comments are clear. `const loadFailed = isError` is a trivial alias, fine for readability.
> - **Info – scope.** The diff touches only the three stated files plus the new test. Other untracked files in the tree (`AGENTS.md`, `npm_audit*.json`, `.local-test-agent/`, etc.) are unrelated and should not go into the B-236 commit. `CONTEXT.md` is modified, presumably by the session docs.
>
> **Verdict:** Approve. There are no blocking findings; the Low items are optional follow-ups.

### 5b. Security review
> ## B-236 security review: agent-config fail-open fix
>
> **Verdict: approve.** The fail-open is closed on every server path that reads `agent_configs`. The new error handling does not leak database text and does not create a cross-org oracle. The only new availability cost is that config updates stop arriving during a database outage, and the agent keeps its current config while that lasts. I found no High or Medium issues.
>
> **What I ran** (all read-only): `git diff HEAD`; greps over every consumer of `AgentConfigDefaults`, `GetAgentConfig` and `agent_configs`, and over `FetchConfig`, the collector proxy, `faultDB` and service-key handling. `go build ./...` passed for eami-api, eami-agent and eami-collector. `go vet ./internal/api/ ./internal/store/` in eami-api was clean. I compiled the eami-api test package with `go test -run XXX_none ./internal/api/`, so no DB tests ran. `npx tsc --noEmit` in eami-ui passed.
>
> ### 1. Is the fail-open closed on every path?
> Yes.
> - **Every place that serves defaults:** there are four uses of `store.AgentConfigDefaults` in production code.
>   - `agent_config_remote.go:100-105` and `agents.go:640-646` now use defaults only on `pgx.ErrNoRows`.
>   - `UpdateAgentConfig` (`agents.go:718-728`) already did the same (from B-232), so a failed read can't reset the fields a request didn't send.
>   - The `storeIface` fallback (`agents.go:659-663`) only runs when `s.queries == nil`, which is the test wiring.
>   - Nothing in eami-gateway, eami-policy or eami-agent reads `agent_configs`.
> - **The store returns a plain `ErrNoRows`:** `GetAgentConfig`, `GetAgent`, `GetDefaultOrgID` and `ResolveEndpointGatewayAgent` return the raw `row.Scan` error, so the `errors.Is` checks are correct. `GetAgent` filters on org in the query (`id AND org_id`), so another org's agent is `ErrNoRows` and gets a 404.
> - **The full chain to the agent:**
>   - The collector proxy (`eami-collector/internal/api/config_proxy.go`) passes the upstream status and body through unchanged. It returns 502 if the upstream call fails (line 51) and 503 in standalone mode (line 33).
>   - `FetchConfig` (`eami-agent/internal/collector/sender.go`) does nothing on 404 (line 160). Any other non-200 returns an error before the body is decoded (line 163). So 500, 502 and 503 never change the config. `main.go:253` logs the error at Debug level and carries on.
>   - Even on a 200, only non-zero fields are applied (lines 173-181). An empty, `null` or unparseable body therefore can't clear the scanner list or the interval.
> - **UI:** the query hook has no `placeholderData` or `initialData`, so there is no client-side source of defaults. On `isError` the form and Save button are not rendered at all.
>
> ### 2. Does the new error handling leak anything?
> No.
> - **Response bodies:** every new 500 uses a fixed `internal_error` / "failed to load agent config" (the existing endpoint-resolve 500 says "failed to resolve endpoint"). The error text goes only to `slog`. The test asserts the exact body and checks that it contains no "division", "22012" or "SQLSTATE".
> - **Oracle on the admin route:** an unknown agent and another org's agent both get the same 404 (`agents.go:627-630`). A 500 now comes only from a real database error: a timeout, a lost connection or a cancelled context. None of these depend on whether the ID exists or which org owns it. An attacker can't cause one for a chosen agent ID. The ID is a UUID checked by `parseUUIDParam`, and the query is parameterised. A slow or overloaded database fails every ID the same way, so the difference between 500 and 404 reveals nothing across orgs.
> - **Service-key route:** the new 503-versus-500 split for "no org" tells a service-key holder whether the orgs table is empty. That caller is the fleet identity and already had this information.
>
> ### 3. Is there a new availability risk?
> Only a small one, and it's acceptable. A database blip now stops config delivery instead of serving all-scanners-enabled defaults. The agent keeps its current in-memory config and keeps scanning at its current interval. Nothing on the error path disables or reduces scanning.
>
> ### 4. Is `faultDB` test-only?
> Yes. It is defined only in `eami-api/internal/api/agent_config_read_fail_pg_test.go` (package `api_test`, a `_test.go` file). No other file in the repo references it. It is wired in only through `store.New(fdb)` inside the tests, so production code can't reach it.
>
> ### Findings
>
> **Low**
> - **L1: The agent's "last-known-good" config is only in memory.** `eami-agent/cmd/agent/main.go:142` loads the local file, and remote config is never saved to disk. If the agent restarts while the API database is failing, it runs on its local file config, which may have more scanners enabled than the admin chose, until a fetch succeeds. Also, `FetchConfig` only runs after a successful `Send`, so the first scan after every restart always uses the local config. This existed before B-236 and B-236 doesn't make it worse, but it limits the "keeps last-known-good" guarantee. Worth logging as a follow-up (save the last applied remote config to disk).
> - **L2: A remote config fetch failure is logged at Debug level** (`main.go:254`). A config fetch that keeps failing after B-236 is invisible on endpoints at default log levels. Suggest Warn for non-404 statuses.
>
> **Info**
> - **I1: Unsafe `agent_id` handling in the collector proxy (existing issue, not part of this change).** `config_proxy.go:37` puts the decoded `r.PathValue("agent_id")` straight into the upstream URL without escaping. When the agent authenticates with the shared key rather than a per-agent credential, the identity check at line 24 doesn't apply. A value such as `x%3Fy` or `..%2F..` can then change which upstream path is requested with the collector's service key. Only `requireServiceKey` routes are reachable this way, and it doesn't touch B-236's logic. Suggest `url.PathEscape(agentID)` and a charset or length limit. The API's service-key route itself only checks that `agent_id` is non-empty (`agent_config_remote.go:48-51`) and uses it only as a query parameter, which is safe.
> - **I2: Logging is clean.** The new log lines record only `agent_id` or `org_id` (UUIDs) and the database error, with no secrets or request data. pgx error text can include parameter context, but these queries take only UUIDs and the endpoint's `agent_id`.
> - **I3: A background refetch error hides the form.** If a refetch fails after the config has loaded (for example on window refocus), `isError` becomes true, the form disappears and unsaved edits are lost. That is the safe direction; it's only a UX note.
> - **I4: `AgentConfigPanel` breaks two UI rules (existing, outside B-236's diff).** It still uses a page-local toast built from `useState` and `setTimeout` instead of `useToast()`. Its error panel uses `rounded-[10px]` and `shadow-l1`, and `tsc` passes, but I did not check those classes against `DESIGN_SYSTEM.md`.
> - **I5: Remote config can't turn off every scanner.** The agent ignores an empty `enabled_scanners` list, and the API rejects an empty list on PUT. This is by design and is not a fail-open introduced here.
>
> **Relevant files**
> - `C:\AI\EAIM\eaim\eami-api\internal\api\agent_config_remote.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\agents.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\agent_config_read_fail_pg_test.go`
> - `C:\AI\EAIM\eaim\eami-ui\src\components\agents\AgentConfigPanel.tsx`
> - `C:\AI\EAIM\eaim\eami-agent\internal\collector\sender.go`
> - `C:\AI\EAIM\eaim\eami-agent\cmd\agent\main.go`
> - `C:\AI\EAIM\eaim\eami-collector\internal\api\config_proxy.go`

### 5c. Code review — re-review of the UI Retry delta
> I re-reviewed only the `AgentConfigPanel.tsx` delta: it is correct and I found no High, Medium or Low issues. The live run was right and my earlier claim was wrong. The error status is kept during a refetch only when the query already has cached data; with no data, the refetch resets it to pending, so `isLoading` goes true and the error panel would unmount.
>
> **Commands run (all read-only):** `git diff HEAD -- eami-ui/src/components/agents/AgentConfigPanel.tsx`; greps of `node_modules/@tanstack/query-core/build/modern/query.js` and `queryObserver.js`; version checks in `package.json` and `node_modules`; `npx tsc --noEmit` in eami-ui, which passed.
>
> **Installed versions:**
> - `eami-ui/package.json:17` declares `"@tanstack/react-query": "^5.56.2"`.
> - Installed: `@tanstack/react-query` 5.101.0 and `@tanstack/query-core` 5.101.0.
> - React 18.3.1.
>
> **Why the live run saw the loading state (the correction to my earlier report):**
> - In query-core 5.101.0, `query.js:414-418` sets `fetchStatus: "fetching"` on fetch start, plus `...data === void 0 && { error: null, status: "pending" }`.
> - So a refetch with no cached data clears the error and returns to pending, and `isLoading` (pending and fetching) becomes true.
> - Status stays `error` only when data is already cached.
>
> **Correctness of the delta:**
> - **Retry that fails again.**
>   - `refetch()` does not reject by default: `queryObserver.js:183-184` does `promise.catch(noop)` unless `throwOnError` is set, which `useAgentConfig` does not set.
>   - `observer.fetch` (`queryObserver.js:168-175`) calls `updateResult()` before resolving, so the query is already settled in `status: 'error'` when the `finally` runs `setRetrying(false)`.
>   - The next render therefore has `isError` true and `retrying` false, which gives the error panel again.
>   - There is no frame showing "Loading config…" in between. `useBaseQuery` reads the result during render through `getOptimisticResult`, which by then reflects the settled state.
>   - The query's default `retry: 1` (`lib/query.ts:7`) means one automatic retry runs inside that refetch, so the spinner lasts for both attempts. This is expected.
> - **Retry that succeeds.** `retrying` becomes false, `isError` is false and `cfg` is defined, so the form renders with the real values via `useForm({ values })`.
> - **Unmount mid-retry.** The `setRetrying(false)` in `finally` runs after unmount. React 18.3.1 treats that as a silent no-op (the unmounted-setState warning was removed in 18). There is no leak and no crash, and the refetch still completes into the cache, which is harmless. No cleanup guard is needed.
> - **No path renders the form, or a submit control, while `retrying` is true.**
>   - The loading branch is `isLoading && !retrying`, which is false while retrying.
>   - The next branch is `loadFailed = isError || retrying`, which is true while retrying, so the error panel is shown.
>   - The form is only the final `else` branch.
>   - The footer Save button is gated on `!loadFailed`, so it is also hidden while retrying.
>   - With no `<form id="config-form">` mounted, there is no other submit path.
> - **Double-click.** `Button isLoading={retrying}` disables the button, so a second concurrent `onRetry` cannot start from the UI.
> - **Retry with cached data** (an error after an earlier success). Status stays `error` during the refetch, so `retrying` is redundant but harmless there, and the behaviour is the same.
> - **Close during retry.** Close stays enabled, which is correct: nothing is being written.
>
> **Info and idiom:**
> - `try/finally` around `await refetch()` is idiomatic. A `catch` is not needed because `refetch` does not reject here.
> - `onClick={() => void onRetry()}` correctly marks the floating promise as intentionally ignored.
> - The code comment at lines 43-46 accurately describes the pending-reset behaviour. The B-236 record (`BUILT.md` or the commit note) should state it the same way and name query-core 5.101.0.
> - Dropping `isFetching` in favour of `retrying` means the Retry spinner reflects only a user-started retry. This is correct: background fetches were never Retry's concern.
> - The `DESIGN_SYSTEM.md` compliance points from the first review are unchanged: the danger tokens, `shadow-l1` and `Button isLoading` are all still used.
>
> **Verdict:** Approve the delta.

## 6. Follow-ups
**Minted: B-249** (founder direction). Raise `FetchConfig`'s failure log from Debug to Warn.

**Proposed, not minted** (from the reviews; the founder decides):
- **Collector config proxy doesn't escape `agent_id`** (security review I1). `config_proxy.go` puts the decoded `agent_id` straight into the upstream URL. With the shared (non-per-agent) credential, an agent can reshape which service-key upstream path is requested. The only reachable route is a GET under `requireServiceKey`. Fix: `url.PathEscape` plus a charset and length limit.
- **`ingest.go` maps any `GetDefaultOrgID` error to 503 "no_org"** (code review Info). This is the same misclassification fixed here, on a different, non-config path.
- **`AgentConfigPanel` still uses a page-local success toast** (`useState`+`setTimeout`, a B-182 rule violation) **and a raw `bg-red-50` save-error box.** Both existed before this change and are outside B-236's diff.
- **The agent's last-known-good config lives in memory only** (security L1). After a restart it uses the local file until a fetch succeeds. The founder has already said this is existing and out of scope; recorded here for completeness.
