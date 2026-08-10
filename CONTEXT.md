# CONTEXT.md — Living project continuity log
# Updated by: Claude Code (after every task) AND the PM chat (after every
# planning decision). Read by both at the start of every session, before
# anything else.

## Product identity (do not re-litigate without explicit founder instruction)
EAMI = Enterprise AI Monitoring & Intelligence. Gateway, policy engine,
endpoint agent, audit log. NOT a maturity-assessment tool — that's a separate,
unrelated framework the founder uses elsewhere. If any file, commit message,
or prior context suggests otherwise, it is wrong; trust this line.

## Active decision thread (update every time one moves)
- ADR-019: RESOLVED, Accepted — 2026-07-22. Full episode content stays
  on-prem; eami-api never serves it. See DECISIONS.md ADR-019 (now a full
  formal entry, same number — the informal Pending-table row it replaces
  has been removed, not renumbered). **Now fully enforced in the running
  system, not just decided on paper — see B-002 Brief 3 below.**
- **B-002: DONE and fully closed on `master`** — all 3 briefs complete
  and merged (`3eab113`, `adcd3e9`, `292d6a4`). Exactly one path to full
  episode content exists in the running system, and it enforces org
  isolation. History:
  - Brief 1 (gateway dual-auth endpoint): **DONE, merged to master**
    (merge commit `3eab113`, from branch `b-002-gateway-episode-endpoint`,
    plan at `C:\Users\bharg\.claude\plans\unified-wandering-karp.md`). New
    `eami-gateway` package `internal/episode/{store,reader,http}.go` +
    tests, wired into `cmd/gateway/main.go`, new required config
    `GATEWAY_EPISODE_READ_SERVICE_KEY`. Reviewer + security subagent passes
    both clean (no compile-level defects; one already-known/approved
    trust-boundary tradeoff flagged, tracked as BACKLOG B-015, not a bug).
    **Verified 2026-07-22 with a real toolchain: `go build ./...` and
    `go test ./... -v` both clean, 0 failures, 18/18 new tests passing.**
  - Brief 2 (eami-api proxy layer): **DONE, merged to master** (merge
    commit `adcd3e9`, branch `b-002-eami-api-proxy-layer`, since
    deleted), plan at
    `C:\Users\bharg\.claude\plans\unified-wandering-karp.md`. New file
    `eami-api/internal/api/gateway_episodes.go` proxies
    `GET /v1/gateway/episodes*` to Brief 1's gateway endpoint. The hard
    requirement Brief 1 deferred is now satisfied: `org_id` sent to the
    gateway is always `claimsFromContext(r).OrgID` (the caller's own
    session org), never client input — an optional `org_id` query param
    is accepted only as a tamper-check that 403s on mismatch *before* the
    gateway is ever called, so a forged org can't structurally reach the
    gateway at all. Purely additive: `memory.go` has zero lines changed,
    old `/v1/memory/episodes*` routes untouched. Reviewer + security
    subagent passes both clean (2 low-severity test-coverage gaps found
    and closed before commit). **Verified 2026-07-22 with a real
    toolchain: `go build ./...`, `go vet ./...`, `go test ./...` all
    clean, 0 failures, 11/11 new tests passing** (includes the centerpiece
    `TestGatewayEpisodes_List_MismatchedOrgIDSupplied_Returns403_
    GatewayNeverCalled`, asserting both the 403 and that the fake gateway
    client's call count is zero). Fixed a nil-`cfg` panic in `NewServer`
    along the way (pre-existing latent bug, surfaced by wiring in the new
    config — `finops_test.go` already called `NewServer(nil, ...)`).
  - Brief 3 (memory.go + MemoryPage.tsx cutover): **DONE, merged to
    master** (merge commit `292d6a4`, branch `b-002-memory-cutover`,
    since deleted), plan at
    `C:\Users\bharg\.claude\plans\unified-wandering-karp.md`. Chose
    option (a): re-pointed the existing, `api/openapi.yaml`-documented
    `/v1/memory/episodes` and `/v1/memory/episodes/search` URLs at
    Brief 2's already-secure handlers (`ListGatewayEpisodes`/
    `SearchGatewayEpisodes`) instead of moving the frontend to new URLs
    — verified the response shapes are byte-identical, so this needed
    **zero `MemoryPage.tsx` changes**. Added `GET /v1/memory/episodes/
    {episodeId}` → `GetGatewayEpisode`, filling an `openapi.yaml`-
    documented route that was never implemented. **`eami-api/internal/
    api/memory.go` and `eami-api/internal/store/episodes.go` (the last
    direct, unprotected `episodes`-table query path) are deleted
    entirely** — verified zero other callers first. Security review for
    this brief specifically re-verified the org-isolation chain at the
    new `/v1/memory/episodes*` mount points (not assumed to carry over
    from Brief 2's review) and confirmed **the leak is fully closed**,
    not just superseded by a safer alternative running alongside it. 8
    new tests in `memory_test.go`, reusing Brief 2's fixtures with zero
    duplication; `gateway_episodes_test.go` itself has zero diff.
    **Verified 2026-07-22: `go build ./...`, `go vet ./...`,
    `go test ./...` all clean, 0 failures.** Two things NOT done, flagged
    before building rather than discovered after: frontend build/lint/
    typecheck (Node/npm confirmed genuinely absent from this machine —
    checked install locations directly, not just PATH) and `docker
    compose up`-based manual verification (no Docker in this
    environment) — `MemoryPage.tsx`'s correctness rests on manual
    shape-verification only.

## Standing facts Code and PM must both know
- Desktop app: planned future feature, not yet built. Gateway auth should
  support it (Bearer JWT path) without a live consumer yet. Brief 1's dual
  auth already supports this path (Bearer AI-token JWT, org resolved
  server-side via the agent registry) with no live consumer.
- B-002's org-isolation logic is built, merged, and verified end-to-end
  (Briefs 2+3), but **do not provision `GATEWAY_EPISODE_READ_SERVICE_KEY`
  anywhere a caller other than eami-api's proxy could use it directly
  against eami-gateway** — see BACKLOG B-015 (Medium, still open: this is
  now a standalone network-hardening item, not a B-002 blocker — Brief
  1's gateway endpoint itself still enforces nothing on its own if
  reached directly, bypassing eami-api entirely).
- Pre-existing, unrelated issue discovered 2026-07-22 while verifying
  Brief 2: `finops_test.go`'s `TestFinOpsTimeSeries_*` subtests panic
  internally (nil `s.queries`) but still report PASS because chi's
  Recoverer catches it — see BACKLOG B-016. Not fixed, out of scope for
  B-002.
- No deploy infrastructure exists in this repo (no deploy.yml, no IaC).
  Nothing is live in production. api.eami.io in openapi.yaml is a spec
  placeholder, not a real deployment.
- Solo founder, pre-first-customer, evening/weekend hours.
- Paste-detection epic (browser-extension-based) is in scoping. B-032
  (2026-07-25) built the backend groundwork only — `paste_events` table +
  `POST /v1/reports/paste-events` — ahead of the extension itself. **No
  browser extension exists yet; this endpoint has no live caller.** An
  investigation earlier that session found `POST /v1/reports` (the
  existing collector-write path) has **zero real producers today** —
  `eami-collector`'s forwarder only ever calls `/v1/ingest/batch`, a
  different table/pipeline — worth knowing before assuming that endpoint
  is exercised in production. B-034 (2026-07-26) added the `eami-agent`
  side — a native-messaging host mode so a future extension can relay
  paste events in real time — but **`eami-agent` has no `org_id`
  anywhere** (only `agent_id`), confirmed by direct investigation, not
  assumed. B-035 (QUEUED) is the still-missing link: paste events
  currently land in the existing agent-report pipeline's raw JSON blob,
  not literally in `paste_events` yet — `eami-collector`/`eami-api` were
  both frozen for B-034, so that wiring is real, tracked, unfinished work,
  not silently dropped. **B1 (browser extension) now exists** — B-036
  (2026-08-03), `eami-browser-extension/`. Its extension ID
  (`ngmdfnecljeoleiancdedbmhjdihaoaa`) is wired into B0's
  `allowed_origins`, and the native-messaging→backend leg is fully
  live-verified (real message → real B0 host → real Docker
  collector/API/Postgres → confirmed in `endpoint_reports`). **All 5 steps
  of `MANUAL_TESTING.md` have since been run to completion by the user
  personally on a real Chrome/Edge install (2026-08-03) — B1 is now fully
  manually verified, not just built and code-reviewed.** Two real bugs
  surfaced only by that live pass, both found and fixed: `background.js`
  silently swallowed a failed native-messaging connection (fixed, now
  logs it); `eami-agent`'s `nmlauncher` parent-process check refused to
  start the host for the user's real browser (fixed by failing open with
  a warning instead of closed — see B-037 below for the required
  follow-up before customer ship). Still no live production install of
  the extension itself in the deployment sense (local unpacked-load
  only; enterprise force-install policy documented, not configured).
  **B2 (admin UI) now exists** — B-038 (2026-08-03), `eami-ui`'s new
  `/paste-events` page + two new `eami-api` GET routes, built directly
  against `paste_events` (not the interim blob). At the time B-038
  shipped, the real `Dev Org` had zero rows in `paste_events` (only
  B-032's own disposable synthetic test orgs had rows there), so the UI
  was fully built and tested but showed an honest empty state for any
  real org. **B-035 (2026-08-04) has since closed that gap**: paste
  events relayed by B0 now land directly in `paste_events`, not
  `endpoint_reports`' raw blob — B-038's UI should now show real data
  with zero code changes on its side, exactly as designed. The brief's
  own assumption that `eami-collector` resolves org context was
  investigated and found wrong (it resolves nothing — only `eami-api`'s
  `GetDefaultOrgID` does), so the fix landed entirely in `eami-api`, with
  zero changes to `eami-agent`/`eami-collector`. This closes the last
  open item in the paste-detection epic (B-032→B-034→B-036→B-038→B-035).
- **B-037 (done, 2026-08-04):** fail-closed restored for `nmlauncher`'s
  "parent determined but not recognized" case, closing the dev-testing
  weakening above. Investigated rather than guessed: research confirmed
  Chrome/Edge already use the identical executable name across every
  Windows channel (so the Windows list was never actually incomplete —
  that hypothesis is refuted), and confirmed Chrome's native-host launch
  mechanism has used no `cmd.exe` intermediary since Chromium 113 (2023).
  Real gaps found and closed: Linux/macOS both use distinct per-channel
  process/bundle names, previously uncovered (researched, not verified on
  a live host of those platforms). **The exact original failure was never
  captured and can't be retroactively diagnosed** — leading theory,
  stated plainly rather than left unexplained: the user's real default
  browser is likely a different Chromium-family browser (Brave/Opera/
  Vivaldi/etc.), since every other explanation was specifically checked
  and ruled out. Scope kept to Chrome/Edge only per an explicit
  user decision (not widened to other Chromium browsers) — a genuinely
  different browser still requires `EAMI_NM_SKIP_PARENT_CHECK=1`.
  Reviewer + security passes both ran; one real Medium (missing test
  coverage for 4 new entries) and two Low findings, all fixed. Re-verified
  live: fail-closed confirmed (non-browser parent refused), override
  confirmed working, full protocol round trip confirmed landing correctly
  in `paste_events` (B-035) through the real Docker stack.
- **This machine's shell runs elevated (Administrator)** — discovered
  2026-08-03 while attempting real Chrome/Edge automation for B1's
  verification: elevation causes Edge (and likely Chrome) to
  silently de-elevate-relaunch themselves, breaking reliable
  `--load-extension`/`--remote-debugging-port`/`--user-data-dir`
  passthrough for any future browser-automation attempt in this
  environment. **A `taskkill /F /IM msedge.exe` run while diagnosing this
  very likely killed the user's own real, unrelated Edge session** (all
  running `chrome.exe` processes were separately confirmed to be the
  user's real session, not test artifacts — Edge was not re-checked in
  time to tell). Disclosed directly to the user. Any future session
  attempting real browser automation here should assume the same
  elevation problem exists and avoid blanket `taskkill` by image name —
  filter by command line/working directory first to confirm a process is
  actually one you spawned.
- **A real .NET SDK (8.0.423) + WiX Toolset v4 (4.0.6, matching CI's
  pinned version) are now installed on this machine** (2026-07-26, via
  `winget`/`dotnet tool install`, to debug a CI MSI-build failure) —
  previously only the .NET *runtime* was present, not the SDK, so
  `eami-agent/installer/Product.wxs` could only be reviewed by eye, never
  actually compiled locally. That gap is closed now: `wix build` and the
  project's own `installer/build.ps1` both run for real on this machine.
- **The approval flow (human-in-the-loop AI-governance escalation) works
  end-to-end for the first time — B-039 (2026-08-04)**. An original
  module audit's three claimed root causes in
  `eami-gateway/internal/approval/router.go` (a `Submit()` INSERT missing
  5 `NOT NULL` columns; `Hold()`/`resolve()` querying nonexistent
  `decision`/`reason` columns instead of the real `status`/
  `decision_reason`; a `"allowed"` vs `eami-api`'s real `"approved"`
  vocabulary mismatch) were all re-confirmed directly against current
  code, not assumed still true, and all three fixed. **A fourth,
  previously-unknown root cause was found live**: `internal/mcp/
  handler.go`'s `ServeMessages` ran the real dispatch/`Submit()`/`Hold()`
  logic in a goroutine passed the HTTP request's own context, which
  `net/http` cancels the instant the handler returns (right after its
  `202` response) — every escalation's `Submit()` was racing its own
  context being torn down. Fixed with `context.WithoutCancel`, after
  explicitly confirming with the user this justified going beyond
  `router.go`'s originally stated file scope. `risk_level` remains a
  hardcoded `"medium"` placeholder (no risk-classification concept exists
  anywhere in the policy/schema — real design work queued as B-040, not
  solved here). Proven live against the real `docker-compose` stack, not
  just unit-tested: a real ESCALATE → real approval row → real admin
  decide (approve and, separately, deny) → real gateway resume/block,
  with Slack notification confirmed reached. **Unrelated bug found
  incidentally, queued as B-041, now DONE (2026-08-04)**: see the new
  standing fact immediately below for the fix.
- **B-041 (done, 2026-08-04):** `eami-gateway`'s JWT revocation persistence
  actually works now — both directions, not just the hydration query
  originally reported. Investigation found the write path (`Revoke()`'s
  DB INSERT) was *also* broken (same nonexistent `expires_at` column, plus
  a missing required `agent_id`), so nothing had ever actually reached
  `revoked_ai_tokens` for hydration to load — fixing only the SELECT (the
  bug as originally filed) would not have closed the loop. Fixed both,
  with the user's explicit sign-off to expand scope beyond the original
  framing. Revocation is permanent by design (the table has no expiry
  column at all — confirmed across every migration, not assumed).
  Hydration failure is now fatal (propagates through existing `main.go`
  error handling to `os.Exit(1)`) instead of a silent `slog.Warn` with an
  empty revocation set. **A real, non-obvious gap found during this
  task's own live-verification pass, fixed before shipping rather than
  left for later:** `Claims.Subject` (the JWT `sub`) is `"agent:<name>"`,
  never a `gateway_agents.id` UUID — a naive `Revoke(claims.ID,
  claims.Subject)` call (the obvious-looking one) would fail the FK
  insert on every real revoke. `Revoke()`'s new `agentID` parameter
  requires the resolved UUID (via `registry.LookupByName`, the same
  pattern `internal/mcp`/`internal/episode` already use), documented
  explicitly in its doc comment. `Manager.Revoke` still has **zero
  production callers** — the documented `POST /v1/gateway/tokens/{token}
  /revoke` route was never wired into `main.go`'s router — filed as
  **B-042**, not built here, along with two latent (currently
  unreachable) gaps reviewer + security passes both independently found:
  `Revoke()`/`save()` swallow persistence errors rather than returning
  them, and `Issue()` accepts an unvalidated `AgentID`. **Live-verified
  end-to-end against the real `docker-compose` stack, rebuilt fresh**:
  issued a real token, revoked it via the real `dbRevocationStore.save`
  code path against the real DB, confirmed the already-running gateway
  process still accepted it (expected — no HTTP route calls `Revoke()`
  yet), restarted the container, confirmed the startup log now reads
  `"identity: hydrated revocation list" count=1` (previously the exact
  `WARN` this item closes), and confirmed a real `GET /v1/mcp/sse`
  request with that token now returns `401 ... has been revoked`.
  Separately confirmed hydration-failure-is-fatal by stopping Postgres
  and confirming the gateway crash-loops rather than starting degraded.
  Full writeup in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s
  B-041/B-042 entries.
- **B-042 (done, 2026-08-05):** `POST /v1/gateway/tokens/{jti}/revoke` is
  now real and wired — `Manager.Revoke` has a live HTTP caller for the
  first time. Two deliberate, investigated, user-approved deviations from
  `api/openapi.yaml`: (1) auth is a new gateway-local `X-Service-Key`
  (`GATEWAY_TOKEN_REVOKE_SERVICE_KEY`, required/fail-closed at startup),
  not the documented `BearerAuth` — that scheme is `eami-api`'s
  user-session JWT, which `eami-gateway` architecturally cannot validate.
  A real future proxy (`eami-api`-hosted, `requireRole("admin",
  "operator")`, matching `/v1/gateway/agents*`'s already-shipped
  precedent for this exact class of action) is the intended long-term
  caller, not built here — would have required solving an unscoped
  problem (no "list active tokens" surface exists for an admin to pick a
  `jti` from). (2) the path uses `{jti}` (the token's ID), not `{token}`
  (the full JWT `openapi.yaml` implies), so a live bearer credential
  never lands in a URL/access log. Both documented explicitly in
  `revoke_http.go`'s doc comment. Closes B-041's two flagged latent gaps:
  the handler resolves `agent_name` via the registry before calling
  `Revoke` (never passes a raw JWT `sub`), and `Manager.Revoke` now
  returns an `error` (the one approved exception to `tokens.go` staying
  frozen this task) instead of only `slog.Error`-logging a persistence
  failure — the handler surfaces a real `500`, never a false `204`.
  **A third, more severe gap found during this task's own security
  review, required to be fixed in-task per explicit user direction (not
  deferred the way B-041→B-042 itself was staged):** `registry.
  LookupByName`'s query has no `org_id` filter — safe for every existing
  caller only because their `agent_name` always comes from a
  signature-verified, org-bound JWT `sub`; this handler is the first
  caller passing a raw, client-supplied `agent_name`, so an Org A caller
  could otherwise resolve, and cause the revocation of, Org B's
  identically-named agent's token. Fixed with a new, purely additive
  `registry.LookupByNameAndOrg` (`LookupByName` and all its existing
  callers untouched — confirmed via diff) and a required, UUID-validated
  `org_id` in the revoke request body — caller-supplied and trusted,
  matching `episode/http.go`'s own already-shipped precedent for its
  service-key path's `org_id` param, not a new trust model invented here.
  Reviewer + security passes ran twice (base implementation, then a
  focused follow-up on the org-scoping fix) — both clean against the
  final diff. One more real gap found by the initial passes, **not**
  fixed here per the user's explicit direction: `Manager.Revoke()` only
  updates the in-memory set on whichever node handles the request — no
  real cross-node broadcast exists despite schema/doc comments claiming
  Serf-based multi-node support — filed as **B-043** (predates B-042
  entirely; this task just made it concretely reachable for the first
  time). 9 new tests (5 real-Postgres integration tests including a
  cross-org-rejection test proving the org-scoping fix — a real second
  org, a real agent, wrong `org_id` → `403`, token stays valid — plus 4
  config tests for the new required secret). **Verified 2026-08-05 with a
  real toolchain: `go build`/`go vet`/`go test ./...` clean across the
  full `eami-gateway` module** (26 tests in `internal/identity` alone).
  **Live-verified end-to-end against the real `docker-compose` stack,
  rebuilt fresh**: a wrong-service-key revoke attempt returned real `401`
  with the token confirmed still valid (AC2); a correctly-authenticated
  request returned real `204`, the row landed in `revoked_ai_tokens` with
  the correct `agent_id`, and a subsequent request with that token
  returned `401 ... has been revoked` (AC1) — then, beyond the brief's
  minimum, the gateway was restarted and the same HTTP-route-driven
  revocation was confirmed to survive it too, proving the full
  B-041→B-042 chain works together end-to-end. Full writeup in
  `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s B-042/B-043
  entries.
- **B-044 (done, 2026-08-06):** MCP tool routing is dynamic for the first
  time. `proxy.Forward` always forwarded every tool call to one hardcoded
  static URL, regardless of tool name — `gateway_tools` (built by
  B-022/B-023) had zero live consumers anywhere in `eami-gateway`,
  confirmed by a routing investigation via repo-wide grep. New
  `internal/toolrouter` package resolves an incoming tool_call's parsed
  name against `gateway_tools`, org-scoped, and — for `rest_api`-type rows
  only, `mcp`/`database` explicitly deferred — dispatches to that tool's
  real `base_url` using real decrypted credentials, with policy checked
  against the specific resolved tool first via a new `ActionContext.
  ToolServerID`/`Conditions.ToolServerIDs` (additive, mirrors the existing
  `ToolNames` pattern).
  **Two of the task brief's own stated assumptions were wrong, corrected
  before building rather than silently worked around:** B-023's SSRF guard
  and B-022's credential cipher both live in `eami-api/internal/...` —
  `eami-gateway`/`eami-api` are separate Go modules, so neither is
  importable regardless of export status. Matched this repo's own B-025
  precedent (duplicate small security-relevant helpers rather than build
  shared infrastructure) instead: `toolrouter/dial.go`/`creds.go` are
  deliberate duplicates (`creds.go`'s cipher is decrypt-only — the gateway
  never writes credentials); `eami-api`'s originals are untouched, still
  needed for its own connectivity-test feature.
  **A genuine design fork was presented to the user rather than decided
  unilaterally**: when a tool name matches no `gateway_tools` row at all
  (100% of today's live traffic) — user chose **fall back to the existing
  static path**, not reject. Dynamic routing is strictly additive/opt-in
  per registered `rest_api` tool; `proxy.go` and `internal/approval/
  router.go` are completely untouched, so approved escalations keep using
  the static proxy exactly as before.
  Reviewer + security passes both ran and came back clean — no exploitable
  vulnerabilities. Security review specifically re-derived that Go's
  default redirect-following doesn't bypass the SSRF guard (it's the
  `Transport`'s `DialContext`, re-invoked on every redirect hop) and that
  the duplicated cipher is genuinely interoperable with `eami-api`'s
  original (verified via a real round-trip test). One real bug caught by
  code review before shipping: `Router.Forward` had no nil-row guard
  (unreachable via the single production call site, but the package is
  importable) — fixed with a one-line check + a new test.
  **Live-verified end-to-end against the real `docker-compose` stack,
  rebuilt fresh**: routed a real tool_call to a real public echo endpoint
  with the real decrypted credential arriving as a real header (AC1).
  **A genuinely unplanned live discovery**: the first attempt used
  `host.docker.internal` as a local test target — the real SSRF guard
  correctly rejected it, since that hostname resolves to a private-range
  address inside Docker's own network — organic, not staged, proof the
  guard is active on the real dispatch path (AC3). A second org's agent
  calling the identical tool name fell through to the same static-fallback
  error shape as an unregistered call, proving cross-org isolation live
  (AC4); a real `mcp`-type row did the same, confirming it isn't
  accidentally dynamically dispatched (AC6). Full writeup in `BUILT.md`'s
  `eami-gateway`/`eami-policy` sections and `BACKLOG.md`'s B-044 entry.
- **This machine's `localhost` resolves IPv6 (`::1`) before IPv4
  (`127.0.0.1`), and this Docker Desktop instance's IPv6 port-forwarding
  accepts the TCP handshake but silently drops all subsequent data** —
  discovered 2026-08-04 during B-035's test runs (diagnosed with a
  minimal standalone Go probe) and hit again identically during B-039's
  live verification. Any future session running Go tests or ad-hoc
  clients against this stack's published ports should default to
  `127.0.0.1` explicitly (or `TEST_DATABASE_URL`, already this
  convention) rather than `localhost`, to avoid silent multi-second hangs
  that look like a real connectivity problem but aren't.
- **B-045 (done, 2026-08-06):** Tools page gains Edit — confirmed as a real
  gap during B-044's own manual testing (fixing a wrong `base_url` needed
  a raw SQL `UPDATE`, since the UI had no way to correct it). Investigated
  whether this was a broader pattern first: it wasn't — Agents/Policies/
  Settings all already had `PATCH`/`PUT`, Tools was the only outlier. New
  `PATCH /v1/gateway/tools/{toolId}` reuses `UpdateAgent`'s exact
  `COALESCE($n, column)` partial-update convention; applied to
  `credentials_encrypted` specifically so an admin can edit name/base_url
  without ever being forced to re-enter a working credential they aren't
  changing. `type`/`auth_type` stay immutable (matches `UpdateAgent`'s own
  precedent). `api/openapi.yaml` deliberately not touched — ships
  undocumented, matching B-038's precedent. A real, necessary scope
  correction confirmed with the user before building: `apiFetch()` (the
  documented "no raw fetch in components" escape hatch) was GET-only, so
  it gained optional `{method, body}` params — small and generic, not
  tool-specific. Security review found the core logic sound but flagged a
  real gap (credential-preservation was only proven against a fake store,
  never real Postgres) — closed with 4 new real-Postgres tests reading
  real decrypted bytes back. Code review caught and this session fixed two
  real bugs before shipping: no empty-`name` validation (would have
  silently blanked a tool's name via `COALESCE`), and a whitespace-only
  `mcp_args` input that would have silently wiped a tool's existing stored
  args. AC4 (no stale routing after an edit) verified against B-044's
  `toolrouter` — confirmed no caching exists, then proven with a new test
  that edits a row mid-test and confirms the very next dispatch uses the
  new config. Live-verified against the real stack: edited B-044's own
  "Test" tool via a real admin JWT, and a single subsequent real
  `tool_call` proved both AC2 and AC4 simultaneously — dispatch reached
  the edited `base_url`, while the untouched original credential was still
  applied correctly. Full writeup in `BUILT.md`'s `eami-api`/`eami-ui`
  sections and `BACKLOG.md`'s B-045 entry.
- **B-046 (done, 2026-08-06):** per-endpoint action-to-path mapping closes
  the exact gap B-044's own manual testing surfaced live that same night
  (`"Test/query"` 404'd against a flat `base_url` — every action for a
  tool POSTed to the identical URL). New `gateway_tools.action_paths
  JSONB` (migration `010`); `toolrouter.Forward` routes a mapped action to
  `base_url` + its own path/method instead of the flat default; a tool
  with zero mappings is completely unaffected (B-044's behavior,
  unchanged). **A tool that HAS other mappings, called with an action not
  among them, is a deliberate hard rejection — confirmed with the user via
  an explicit choice before building**, not a silent fallback to
  `base_url`. Security review traced whether an admin-supplied mapped
  `path` could redirect a dispatch to a different host (SSRF-guard
  bypass) — **empirically disproven**, not just argued: `joinURLPath` is
  pure string concatenation forming one URL parsed once (never a
  `url.ResolveReference`-style relative resolution, the pattern with known
  `"//host"` bypass pitfalls); a standalone Go program fed it 6 malicious
  candidate paths against a real base URL and every single one resolved
  to the original host. Added a write-time hardening guard anyway
  (`eami-api` rejects any path containing `"://"`) as a misconfiguration
  catch, not because it was exploitable. **Both automated reviewer/
  security subagent passes failed on a platform-wide session-limit error
  mid-task** (not a finding — a tooling outage) — substituted with a
  direct manual review plus the empirical trace-through above, rather
  than skipping the mandatory step. Live-verified end-to-end against the
  real stack: two real mapped actions routed to two real distinct
  endpoints (GET/POST) with real credentials attached; an unmapped action
  on that same tool cleanly rejected live; a separate zero-mappings tool
  confirmed completely unaffected. **Operational note: the pre-existing
  `dev@example.com` admin account's password was reset to a known value
  during this session's live verification** (no other credential was
  available to obtain a login token) — the user may want to reset it
  again. Full writeup in `BUILT.md`'s `eami-gateway`/`eami-api`/`eami-ui`
  sections and `BACKLOG.md`'s B-046 entry.
- **New epic identified, not investigated (2026-08-06/07), during B-047's
  own manual Approvals-page testing — no B-ID assigned yet, see
  `BACKLOG.md`'s QUEUED section:** approval routing/auto-rules/in-session
  confirmation. Three distinct capabilities: (1) tools/policies need an
  "owner" concept so escalations route to the right person/team, not one
  flat global admin queue — today any `admin`/`operator` sees every
  pending approval regardless of which tool it's for, not viable for a
  real multi-app deployment; (2) auto-approve/auto-deny rules (a
  policy-engine extension, not just a UI change — needs investigation
  into what `policy_conditions`' current schema could support); (3)
  in-session confirmation, prompting the human actually driving the AI
  conversation directly rather than an admin — architecturally novel,
  nothing today has a channel back to an AI session's originating user as
  far as is known, genuinely needs investigating rather than assumed
  either way. **Explicitly needs the same investigate-first treatment as
  every other epic before any brief gets written or scoped/sized** — do
  not start implementation from this bullet alone.
- **`ARCHITECTURE.md` §8 (Deployment Topology) is now stale — flagged
  here for whoever owns that file next, not silently edited.** ADR-020
  (2026-08-07, `DECISIONS.md`) resolved a real inconsistency a VM
  appliance investigation surfaced: §8 describes `eami-api`/`eami-ui` as
  already-separate, EAMI-hosted cloud SaaS, distinct from an on-prem
  `eami-gateway`/`eami-collector` — but `docker-compose.prod.yml`, what
  actually ships today, bundles all five services into one self-contained
  on-prem stack. ADR-020 resolves this as **two sequenced deployment
  models**: Model A (v1, build first) = the fully self-contained
  appliance already implemented by `docker-compose.prod.yml`, matching
  current reality, no architectural change needed. Model B (future, not
  yet scheduled) = the hybrid model §8 currently describes as already
  true — real, not abandoned, just sequenced after Model A ships.
  `ARCHITECTURE.md` §8 needs a correction pass to reflect Model A as
  current and Model B as a labeled future tier, not present reality.
  `ARCHITECTURE.md` is Architect-EAMI-owned per `BOUNDARIES.md` — this
  bullet is the flag, not the fix.

## Last updated
2026-08-10 by Claude Code — B-055: first-boot web setup wizard (console-token
gated), closing the exact gap B-053's own README flagged as needing "its own
race-safety design" for whatever mechanism lets the first visitor claim admin.

New `eami-api/internal/api/bootstrap.go`: three pre-auth routes (`GET
/v1/setup/status`, `POST /v1/setup/token/validate`, `POST
/v1/setup/bootstrap`) plus a new `setup_tokens` table (`schema/
migrations-v2/000003`) and a new `Queries.Begin(ctx)` helper (`internal/
store/db.go`). **Concurrency guard is two real Postgres locks, not
app-level check-then-act** — and the second one only exists because code
review caught a real gap in the first version, see below: `Bootstrap` runs
`SELECT ... FOR UPDATE` on the token row (serializes two requests sharing
the *same* token) **and** a global `pg_advisory_xact_lock(hashtext(...))`
held for the transaction's lifetime (serializes *every* concurrent
`Bootstrap` call, regardless of which token each one holds), inside one
transaction that also does the org+admin insert and the token-consumption
update, all-or-nothing. **Abandoned-session recovery** is the same
transaction's rollback semantics — any failure before `tx.Commit()` (crash,
dropped connection, VM reboot mid-request) discards everything including
token consumption, so a fresh attempt with the same still-valid token
succeeds normally; no separate session-state mechanism was needed since the
wizard is one atomic POST, not a saved multi-step draft.

**The row-lock-only first version had a real gap, found by the mandatory
code-review pass, not caught in this session's own design or initial
tests:** two requests each holding a *different*, independently valid,
unconsumed token share no lock in common — both could observe
`orgCount == 0` under READ COMMITTED and both insert an org, contradicting
the brief's explicit "enforced at the database level... not just an
application-level check-then-act" requirement. In normal operation only one
unconsumed token ever exists (`eami-stack.sh` deletes any prior one before
minting a new one), but that's a shell-script-enforced operational
invariant, not a database guarantee — exactly the class of gap the brief
warned against. Fixed with the advisory lock above. **Reproduced the bug
empirically before trusting the fix**: temporarily disabled the new lock
and ran a new two-different-tokens regression test 5 times — failed 3 of 5
(a genuine, reproducible race) — then restored the fix and confirmed
reliable passes. The original single-token race test could not have caught
this; a new `TestBootstrap_ConcurrentRequests_DifferentValidTokens_
ExactlyOneWins` was added specifically to guard against a regression.

**Trust boundary, reused not reinvented:** the raw token (`openssl rand
-hex 32`, 256-bit) is generated by `appliance/scripts/eami-stack.sh` at
boot and printed only to the VM console/journal — the exact channel B-053
already established as the trusted emergency-access boundary (SSH is
permanently disabled). It is never transmitted or derivable over the
network by `eami-api` itself; network reachability of the wizard's routes
alone is never sufficient to create an org.

**A real, disclosed deviation from B-053's own stated assumption:**
B-053's README said "whatever serves the wizard reads `/data/eami/state`
directly." That's not actually possible — `docker-compose.prod.yml` stays
frozen for this brief too, and nothing bind-mounts `/data/eami` into any
container (only `api_certs`/`gateway_certs` named volumes exist). The real,
implemented gate is DB-based (`SELECT count(*) FROM orgs`) — strictly more
correct anyway (always live, no file-sync step). `/data/eami/state` still
exists, still starts `unconfigured`, but is now informational-only,
best-effort-reconciled to `configured` on a later boot — `appliance/
README.md`'s first-boot contract section is rewritten to say this plainly.

**TLS tradeoff stated, not silently accepted:** these routes run over the
same plain-HTTP-only exposure every other `eami-api` route already has in
this appliance today (`docker-compose.prod.yml` has no TLS anywhere —
`/v1/auth/login` already sends real admin passwords over plaintext HTTP).
No new class of risk introduced; full TLS remains correctly out of scope,
same pre-existing accepted residual.

**Test coverage:** 8 new real-Postgres integration tests
(`bootstrap_test.go`), each against its own throwaway database (mirrors
`schema/migrationtest`'s per-test pattern — that module can't be imported
directly, so migrations are applied by reading the real `.up.sql` files via
pgx's simple query protocol). Includes a real concurrent-race test (5
goroutines racing one token, exactly 1 winner proven, not reasoned about)
and a real interrupted-transaction recovery test (locks + "consumes" a
token in a transaction that's rolled back instead of committed, then
proves a real subsequent attempt still succeeds). **Verified 2026-08-10
with a real toolchain: `go build`/`go vet`/`go test` clean across the full
`eami-api` module** (all new tests plus the full pre-existing suite, zero
regressions). **Node/npm were newly installed this session** (via
`winget`, previously genuinely absent per `CLAUDE.md`'s toolchain note) —
real `npm run type-check`/`npm run build` both clean for the first time
ever on this machine for `eami-ui`, not a Docker-builder-stage proxy like
prior sessions used.

**Live-verified end-to-end against a real, isolated, genuinely-empty
stack, twice** — deliberately not the shared dev stack (see the incidental
discovery below for why relying on it wouldn't even have worked as
described). First pass, pre-code-review-fixes: spun up `docker compose -p
eami-wizard-live-test` with a fresh throwaway Postgres volume, reusing the
already-built local `eami-api`/`eami-gateway`/`eami-ui` images. Generated a
real console-style token via `eami-stack.sh`'s exact commands, confirmed
`/v1/setup/status` → `false`, validated the token (`204`), completed
`/v1/setup/bootstrap` (`201`, real org+admin row created), **logged in for
real with the created credentials** (`/v1/auth/login` → `200`, real signed
JWT) — AC2's actual requirement, not simulated. Confirmed status flipped to
`true`, confirmed the same token was cleanly rejected on reuse (`401`).
Second pass, after rebuilding the `eami-api` image with every code-review
fix below: repeated the identical flow on a fresh `eami-wizard-live-test2`
project, this time deliberately with a dot-less-domain email
(`admin@localhost`) to prove the email-regex fix live — succeeded
end-to-end again. Both throwaway projects fully torn down (`docker compose
down -v`) afterward. **Not performed this session, disclosed rather than
glossed over:** a from-scratch Packer/QEMU appliance image rebuild (B-053
already proved that boot mechanism separately in its own session; this
brief's only change to it — `eami-stack.sh`'s new token-generation step —
was itself exercised for real via its exact commands against a real
Postgres, just not from inside a freshly-booted QEMU VM) and interactive
browser click-through (Claude-in-Chrome was offered and the user chose not
to install it this session; the served `/setup` route and its resolved
`SetupWizardPage` component were confirmed live via direct HTTP requests
and source inspection instead).

**Reviewer + security subagent passes both ran, and code review earned its
keep — a real Medium-severity gap found and fixed, not a clean pass rubber
stamped.** Security: an independent subagent pass plus this session's own
direct trace-through, no exploitable findings — confirmed no network-only
path exists to create an org or derive the raw token, confirmed no SQL
injection (all queries parameterized), confirmed no secret is ever logged
or echoed in a response. Code review found the row-lock-only concurrency
gap described above (fixed with the advisory lock, empirically
reproduced-then-fixed, not just reasoned about) plus three smaller issues,
all fixed: a real UI bug where `onSubmitBootstrap`'s error handler set the
message on a form about to be unmounted (caught in this session's own
manual re-read before the automated pass even ran); the server's email
regex being stricter than the frontend's, rejecting client-accepted
addresses like `admin@localhost`; and the UI unconditionally forcing
re-entry of the 64-character console token on *any* bootstrap failure,
even a fixable one, now gated on the response's actual 401/409 status via
a new status-carrying `ApiFetchError`. One finding intentionally left as a
disclosed limitation (`setupRateLimiter`'s map never prunes empty keys) —
low-severity, and a correct fix needs a background sweep goroutine, judged
not worth the complexity for a single-appliance pre-auth endpoint.

**An incidental, pre-existing discovery, not caused by this task's own
code:** while confirming the shared dev stack's data was untouched, found
its org count read 29 before this session and 31 partway through — turned
out to be 30 synthetic `paste-events-perf-a`/`-b` fixtures accumulated
since 2026-07-25 across many prior sessions' `go test ./...` runs
(`paste_events_read_test.go`'s perf-seed test registers a `t.Cleanup` that
isn't reliably firing — root cause not investigated, out of this task's
scope). Only 1 genuine org (`Dev Org`/`dev@example.com`) actually existed
once the leaked rows were deleted (confirmed by name/timestamp first, and
that the real org was untouched afterward). Logged as **B-056**, not fixed.

B-ID confirmed with the founder before assignment (per this repo's own
past B-ID-collision incident, see prior sessions) — B-055 was free, not
reserved elsewhere. B-056 (the incidental discovery above) was assigned
from the same confirmed-free counter without a second round-trip, given
its low stakes as a not-yet-built, purely informational QUEUED entry.

Full writeup in `BUILT.md`'s `eami-api`/`eami-ui`/`appliance` sections and
`BACKLOG.md`'s B-055 entry.

Prior entry, still accurate: 2026-08-09 by Claude Code — B-053: VM appliance base image + first-boot
detection (Model A per ADR-020), the first appliance-packaging deliverable.

New `appliance/` directory: Packer template (Debian 12 generic cloud qcow2,
`qemu` builder), `provision-base.sh` (installs Docker, wires in two
systemd units, locks the image down before capture), `eami-data-disk.sh`
(idempotently formats/mounts a second virtio disk as `/data`, redirects
**both** Docker's `data-root` and containerd's separate `root` there --
missing the containerd half caused a real bug found live: an OS-image
update reset containerd's state while Docker's persisted container
metadata still referenced old snapshots, `"RWLayer ... unexpectedly
nil"`), `eami-stack.sh` (generates `/data/eami/.env` once via secrets
ported from `scripts/setup.sh`, deliberately never runs `setup.sh`'s
interactive org/admin seeding -- this is what makes "boots with zero orgs"
true by construction, not accident -- runs `docker compose up -d`, writes
the `/data/eami/state=unconfigured` first-boot marker once).

**Secrets and the first-boot marker live on the data disk, not the OS
disk** -- deliberate: an OS update replaces `/opt/eami/` (the static
compose bundle) wholesale; if secrets lived there too, an update would
silently regenerate `POSTGRES_PASSWORD` on next boot, and the already-
initialized Postgres data would survive but become unusable (password
mismatch). Keeping secrets on `/data` makes "survives an update" mean
"still actually works."

**First-boot detection contract for the future setup-wizard brief:**
`/data/eami/state` = `unconfigured` (written by this brief) or
`configured` (never written here -- the wizard's job, once built). No API
endpoint exists for it yet, deliberately (`application code` out of this
brief's scope) -- whatever serves the wizard reads the file directly.

**Real KVM/QEMU boot testing, not just code review** -- this machine has
genuine hardware-accelerated KVM inside WSL2 (confirmed via `kvm-ok` and a
live SeaBIOS boot before relying on it). Real bugs found and fixed live: a
`DefaultDependencies=no`-caused systemd race where the data-disk unit
started concurrently with `systemd-udevd`, missing `/dev/vdb`'s device
node; missing `parted` package; the containerd data-root bug above; a
`mount -a`-vs-systemd's-own-auto-generated-`data.mount` race on any boot
after the first.

**Two mandatory review passes both found real, distinct issues, applied
before shipping:** an SSH-backdoor gap (password-locking doesn't disable
key auth -- fixed by stripping `authorized_keys`) whose first fix (fully
deleting the `packer` build account) turned out to silently break Packer's
own `shutdown_command`, which needs that account's `sudo` -- with
`shutdown_command` unset, Packer's QEMU builder force-kills the VM
immediately rather than waiting for a graceful shutdown, risking a
corrupted captured disk on every build. **Reverted to a considered
trade-off**: keep the account structurally present but unreachable
(password locked, key stripped, ssh disabled) rather than fully deleted,
so `shutdown_command` keeps working and every build gets a real clean
shutdown. Also fixed: `blkid --label` searching every block device
system-wide instead of the specific data-disk partition (a stray label on
an unrelated disk could silently mount the wrong device); file ownership
after Packer's `mv` staging.

**Verified via real boot testing, across multiple clean rebuild/reboot
cycles:** AC1 (stack up, all health endpoints responding), AC2 (indirect
via B-051's existing `migrate` gating, unmodified), AC3 (zero orgs, no
crash, by construction), AC4 (`state` file confirmed exactly
`unconfigured` via host-side `debugfs` inspection, SSH being disabled by
design), AC5 (SSH confirmed disabled two ways: absent from
`multi-user.target.wants`, `/etc/shadow` showing locked passwords). **AC6's
core requirement -- rebuild OS disk, reboot with existing data disk, stack
comes back up healthy -- verified successfully** (clean health-check pass
following a real rebuild+reboot).

**One thing explicitly not claimed verified, disclosed rather than
glossed over:** a stricter secondary check (byte-for-byte comparing the
generated `.env` secrets before/after an OS rebuild via `debugfs`) could
not be completed reliably -- repeated attempts hit nested-virtualization
instability in this environment (QEMU-inside-WSL2, on an already
heavily-loaded host after an extended session), compounded by several
self-inflicted tooling bugs found along the way (a `pgrep -f` wait-loop
matching its own command line, causing a 20+-minute false-stuck wait;
`pgrep -x` silently never matching a 19-character process name; impatient
`kill -9`s corrupting host-side disk-extraction copies mid-read). Per the
user's explicit direction, this specific extra check was stopped rather
than pursued further once AC6's core requirement was already cleanly
proven -- not claimed as verified, unlike everything else above.

Known limitations: `.ova` conversion not attempted (fast-follow, per the
brief's own allowance); Flatcar deferred (founder decision); a console-
triggered temporary-SSH-enable script was considered and explicitly
declined (founder decision -- smallest attack surface). First-boot
auth-gap threat modeling done: not currently exploitable (no self-service
signup route exists anywhere in `eami-api` yet, so no race to have) --
flagged in `appliance/README.md` as something the next brief (setup
wizard) must design against from the start.

Full writeup in `BUILT.md`'s new `appliance` section and `BACKLOG.md`'s
B-053 entry.

Prior entry, still accurate: 2026-08-08 by Claude Code — B-052/B-016: `go test` runs for real in CI for
the first time ever, across all 6 Go modules, gated on a real Postgres —
and the gate caught a real pre-existing bug (B-016) on its very first run.

New `.github/workflows/build.yml` `test` matrix job (5 `go.work` modules,
each with its own `timescale/timescaledb-ha:pg16` service container --
chosen over the stock `postgres` image because the schema needs the
pgvector/TimescaleDB extensions it lacks -- `schema/migrations-v2` applied
via the pinned `golang-migrate` CLI before tests run, mirroring B-051's
real production mechanism) plus a separate `test-migrationtest` job
(`GOWORK=off`, no pre-migration needed). **`build-images` now `needs:
[test, test-migrationtest]` -- a deliberate, explicitly-asked user choice**
(gating vs. running tests fully in parallel with builds) made because
B-051's update mechanism now assumes published GHCR images are
trustworthy; a broken test must never let a broken image reach the
registry. Real cost measured: ~1m40s baseline -> ~2m54s-3m12s post-change,
judged acceptable since `push: true` only fires on main/tags. All 5
matrix legs get a Postgres service uniformly (another explicit user
choice) even though only 2 of 5 modules have real-Postgres tests today --
rejected conditional-per-module wiring as not worth the future maintenance
cost.

**The gate caught a real bug on its first real run:** `eami-api/internal/
api/finops.go`'s `FinOpsSummary`/`FinOpsTimeSeries` called `s.queries.DB()`
with no nil-guard, unlike every other handler's own `"if s.queries != nil"`
convention -- this is B-016, queued since 2026-07-22 (discovered during
B-002 Brief 2's first-ever real `go test` pass in this repo) and never
picked up until CI itself started actually running tests. A request that
passes validation against `finops_test.go`'s deliberately-nil-`s.queries`
test server nil-pointer-panicked, silently recovered into an opaque 500 by
chi's `Recoverer`, masked by the tests' own weak `!= 400` assertions.
**Confirmed with real evidence before fixing, not from a third-party log
summary a review tool produced** -- the user explicitly flagged that lead
as unverified and asked for independent confirmation; reproduced the exact
panic locally with a full stack trace via `go test -count=1`. Fixed with a
guard-clause matching `tools.go`'s `toolQueries()` precedent exactly,
deliberately not a mock-store fix (risks masking other real gaps behind
fabricated responses) -- the user explicitly rejected that alternative
before it was built.

**A real process failure, caught and corrected in-session:** an initial
"all 6 modules verified locally" pass was actually a false negative -- Go's
test-result cache doesn't invalidate on env-var changes without
`-count=1`, so a stale pre-fix cached result was silently reused. CI's own
`go test` steps subsequently gained `-count=1` too, closing the identical
risk inside the gate itself.

**Mandatory code-review pass found the same bug's twin, missed by the
original brief:** `paste_events.go:314`'s `PasteEventsTimeSeries` had the
identical unguarded call, never caught because its tests always require a
real Postgres connection or skip. Fixed identically, with a new
no-Postgres-required proving test.

AC1 (gate blocks on failure) proven twice: organically (the real B-016 bug
failed `test`, `build-images` correctly skipped) and deliberately (a
one-line assertion flip in `eami-policy/policy_test.go`, pushed, confirmed
red + skipped, reverted, confirmed green). 5 separate pushes to master,
each polled to real completion via the GitHub API, `githubstatus.com`
checked "All Systems Operational" before trusting every result. Security
review: no findings. Full writeup in `BUILT.md`'s `Cross-cutting / shared`
section and `BACKLOG.md`'s consolidated B-052/B-016 entry.

**Known limitation, disclosed not hidden:** no branch protection exists on
this repo today, so a red run doesn't block a merge by GitHub's own
enforcement -- the gate's real teeth today are `build-images`' `needs:`
dependency (concretely blocks GHCR pushes) and the visible red commit
status. Branch protection itself is a smaller separate follow-up, not done
here.

Prior entry, still accurate: 2026-08-07 by Claude Code — B-051: real database migration runner, closing
the VM-appliance investigation's own C7 finding — no mechanism existed to
apply a schema change to an already-running, already-seeded database
(`schema.sql` only ever ran once, via Postgres's own
`docker-entrypoint-initdb.d`, on a genuinely empty data directory).

**Tool: golang-migrate via its official `migrate/migrate` Docker image**
(digest-pinned), not embedded as a Go library in any of the 5 existing
service modules — zero new Go dependency added anywhere, matches this
repo's established pattern of using official upstream images directly.

**Reconciliation of the old `schema/migrations/001-010`, investigated not
assumed:** read all 10 before deciding. Only `005`, `007`, and `008` are
actually idempotency-guarded — `001`, `002`, `003`, `004`, `009`, and
`010` all use bare `CREATE TABLE`/`ALTER TABLE ADD COLUMN`/`CREATE INDEX`/
`CREATE TRIGGER` that error on any re-run (the task brief itself assumed
007/010 were the safe examples — 010 is actually one of the unguarded
ones). Chose a fresh baseline over retrofitting: new
`schema/migrations-v2/000001_baseline.up.sql` captures `schema.sql`'s
current, verified-accurate cumulative content, hand-rewritten with
idempotency guards throughout — a code-review pass mechanically diffed it
against `schema.sql` afterward and confirmed zero content discrepancies,
not just eyeballed. `000001_baseline.down.sql` is a deliberate no-op
(pairs with B-029's existing backup/restore mechanism as the real
rollback story, not true reversible down-migrations, per the brief's own
contract). `000002_add_orgs_table_comment` — a deliberately trivial,
genuinely harmless (`COMMENT ON TABLE`, pure metadata) second migration
proving the mechanism end-to-end without an actual schema change, flagged
explicitly to the user as the chosen approach before building, per their
own instruction. Old `schema/migrations/001-010` left in place with a new
`schema/migrations/README.md` marking them historical/non-runnable.

`docker-compose.yml`/`.prod.yml`: removed the old
`docker-entrypoint-initdb.d` schema.sql mount entirely — the new
`migrate` service (runs once, applies pending migrations, exits) is now
the *only* path, fresh installs and upgrades identical, no fork. New
`depends_on: migrate: condition: service_completed_successfully` on
`eami-api`/`eami-gateway`. New `scripts/migrate.sh` wrapper for manual
runs against an already-up stack (`up`/`version`/`force VERSION`).

**A real MEDIUM security finding, found and fixed before commit:**
`POSTGRES_PASSWORD` interpolated unescaped into a `postgres://` URL can
misparse or leak on some golang-migrate versions if it contains
base64-reserved characters — not theoretical, this session's own real dev
password already contains a `+`. Fixed at the root (`.env.example` now
generates hex-only passwords, matching `setup.sh`'s own already-safe
generator) plus defense-in-depth (`scripts/migrate.sh` URL-encodes,
re-verified live against the real `+`-containing password). The
automatic-at-boot compose-service path left as a documented residual —
same risk class already pre-existing, unfixed, in `eami-api`/
`eami-gateway`'s own DSN construction, out of this brief's scope.

New standalone Go module `schema/migrationtest/` — deliberately **not**
added to `go.work`: doing so broke `eami-gateway`'s Docker build (caught
live, not just reasoned about — that Dockerfile copies each workspace
member's `go.mod` individually, and `go.work` requires every listed
member to exist on disk inside the container). Run via `GOWORK=off`. 4
real-Postgres integration tests, each creating/dropping its own throwaway
database (`eami_app`'s `CREATEDB` confirmed, not assumed).

Reviewer + security passes both ran for real (security's first attempt
hit a transient API error mid-run, retried cleanly). Security found the
MEDIUM password-encoding issue above (fixed). Code review found and this
session fixed 3 more: a stale `x/text` CVE reintroduced in the new test
module's own `go.mod` (bumped, re-verified via `govulncheck`); zero CI
wiring for the new test module — investigated and found this is actually
a repo-wide gap (no Go module's tests run in CI at all), logged honestly
as its own item (**B-052**) rather than narrowly patched; a test cleanup
path that swallowed its own failure via `t.Logf` instead of `t.Errorf`
(fixed, so a leaked throwaway database now fails the test that leaked it).

**Live-verified beyond the automated tests:** this session's real,
already-seeded dev database (real `dev@example.com` user, real prior
B-04x data) was stamped at baseline version 1 — only after confirming via
direct SQL that its actual schema genuinely matched the baseline first —
then had migration 2 applied for real: the comment landed, all existing
data confirmed untouched, a second run correctly reported `no change`.
Separately, a fully isolated fresh `docker compose -p eami-b051-test`
project (genuinely empty Postgres volume) confirmed via real container
state transitions that `migrate` completes and exits 0 strictly before
`eami-gateway`/`eami-api` ever begin starting — proving the startup-gating
acceptance criterion holds for real, not just in compose-config theory.
Fully torn down afterward; the real dev stack's `postgres` (briefly
stopped to free port 5432 for the isolated test) restarted with all data
confirmed intact, `eami-api`/`eami-gateway` health endpoints re-confirmed
`200`.

Full writeup in `BUILT.md`'s `Cross-cutting / shared` section and
`BACKLOG.md`'s B-051/B-052 entries.

Prior entry, still accurate: 2026-08-06 by Claude Code — B-047: security dependency updates + container
hardening. 5 confirmed-reachable Go CVEs fixed: `pgx` v5.6.0/5.7.2→v5.9.2
(SQL injection + memory-safety, GO-2026-5004/CVE-2026-33815/33816),
`golang-jwt/jwt` v5.2.1→v5.2.2 (unbounded-allocation DoS, GO-2025-3553),
`golang.org/x/text`→v0.39.0 in both `eami-api`/`eami-gateway` (infinite
loop, GO-2026-5970 — required an explicit `go get`, since `go mod tidy`
alone only floors it at pgx's own v0.29.0 requirement), `go-chi/chi`
v5.1.0→v5.3.0 (RealIP's header-spoofing-class CVEs), `golang.org/x/sys`
v0.21.0→v0.44.0 in `eami-agent` (Windows integer overflow,
GO-2026-5024/CVE-2026-39824, confirmed present-but-unimported, bumped
anyway per the brief's own reasoning). `govulncheck` clean (0 reachable)
across all 5 modules post-bump.

**Two real discrepancies found and resolved with the user before
building, not silently worked around:** (1) `react-router-dom`/`vite`
were assumed patchable by the task brief's own contracts, but neither has
a non-major-version fix available — `react-router-dom`'s own advisory
lists its patched version as literally "None" in the 6.x line (only
`react-router` v7.13.0 fixes it), and `vite`'s two CVEs fix starting at
6.4.2/6.4.3, both a major bump beyond the already-latest-in-its-line
5.4.21 installed. Both left unfixed, logged to `BACKLOG.md` (B-048,
B-049) rather than force-fitting an unscoped major migration into a
dependency-bump session. (2) pgx v5.9.2 and x/text v0.39.0 both declare
`go 1.25.0` in their own `go.mod` (confirmed via the module proxy, not
assumed) — this cascaded into `go.work` and the two affected modules'
`go` directives, `eami-api`/`eami-gateway`'s Dockerfile builder images
(`golang:1.24-alpine`→`golang:1.25-alpine`, empirically required:
`eami-gateway`'s Dockerfile sets `GOTOOLCHAIN=local`, so an unbumped
builder would have hard-failed rather than auto-downloading), and CI's 4
`go-version: '1.24'` pins in `.github/workflows/build.yml`. The CI pin
change was a deliberate, user-confirmed exception to `CLAUDE.md`'s
"never touch CI config" hard rule, not silently done.

chi's deprecated `RealIP` replaced with `ClientIPFromRemoteAddr` in
`eami-api/internal/api/router.go` — **verified functionally inert before
touching it**: a repo-wide grep found zero consumers of the resolved
client IP anywhere in `eami-api` production code, so this closes the
spoofable/deprecated path with zero behavior change today. Chose
`ClientIPFromRemoteAddr` (trust only the raw TCP source) over a
header-trust variant because `eami-api` is reachable both via
`eami-ui`'s nginx proxy (which does set `X-Forwarded-For`/`X-Real-IP`)
and directly on its own published port — no single trusted ingress
exists. 2 new tests (`router_realip_test.go`) proving a forged
`X-Forwarded-For`/`X-Real-IP` doesn't override the resolved IP.

`eami-api`/`eami-gateway` containers now run as non-root (`USER
65532:65532`, distroless's own nonroot UID, numeric — required for
`eami-gateway`'s `FROM scratch` stage, which has no `/etc/passwd`).
**A real HIGH-severity gap, caught by the mandatory code-review pass and
fixed before commit, not by the task's own live-verification run**: both
services self-generate and persist an RSA JWT signing key to disk on
first boot; a brand-new Docker volume mounted over a path with no
pre-existing content in the image defaults to root:root ownership, which
the new non-root user can't write to. Fixed for `eami-api`'s `/certs`,
`eami-gateway`'s `/certs`, AND `eami-gateway`'s hardcoded fallback
`/var/lib/eami-gateway` (used only when `GATEWAY_JWT_KEY_PATH` is unset
— neither shipped compose file unsets it today, which is exactly why the
live-verification pass didn't catch this half and only the code-review
pass, reasoning through the config fallback chain, did) by pre-creating
and `--chown=65532:65532`-ing empty directories in each Dockerfile's
builder stage. Re-verified live against the actual failure scenario
(a throwaway `docker run` with `GATEWAY_JWT_KEY_PATH` unset and a fresh
anonymous volume), not just re-read. This machine's pre-existing dev
`api_certs`/`gateway_certs` volumes (already containing root-owned keys
from prior sessions) needed a one-time manual `chown` — documented as
the same step a real production upgrade to this image would need.

All 4 service Dockerfiles now pin their base images by sha256 digest
(real digests fetched via `docker pull`+`docker inspect`, not
hand-typed); `eami-collector` pinned with no version bump (not exposed
to any in-scope CVE). `eami-ui/Dockerfile`'s comment falsely claiming
nginx's base image sets a non-root `USER` corrected — **verified
empirically wrong** (`docker inspect`/`docker run id` show no `USER`,
root by default; the base image's own `nginx.conf` sets `user nginx;`,
which only worker processes read) — no runtime behavior change, comment
accuracy only. `postcss` patch-bumped 8.5.15→8.5.18. `x/crypto`'s 17
advisories logged as B-050, confirmed unreachable via `govulncheck`
(only `bcrypt` is imported).

Reviewer + security subagent passes both ran as real independent
sub-tasks against the actual diff. Security pass: zero findings ≥7
confidence. Code-review pass: the one real HIGH finding above (fixed),
plus one LOW informational note (the RealIP swap has zero consumers
today — not a defect).

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh (`--no-cache`, all 4 changed images):** `docker top`
confirmed `eami-api`/`eami-gateway` run as UID 65532. Full E2E flow
through the rebuilt stack: login (throwaway admin, direct SQL insert —
no signup route exists) → created a real `escalate` policy via the real
API → issued a real agent JWT → opened a real MCP SSE session → posted a
real `tool_call` → confirmed a genuine `approval_requests` row landed
pending → approved it via the real decide API → confirmed the gateway
resumed and attempted the downstream dispatch (expected
"unsupported protocol scheme" — no real downstream configured for the
synthetic test tool name, not a regression). All throwaway fixtures
cleaned up afterward — confirmed only the original `dev@example.com`
seed user remains, zero leftover policies/approvals.

**CI status, honestly incomplete as of this write-up — not yet confirmed
green, per the user's own explicit "confirm it actually passes, not just
that it should in theory" instruction.** Pushed as `bb2663e`. Run
`31118536388`: `Docker — eami-gateway`/`Docker — eami-ui` both passed
clean (the go1.25 builder bump and digest pins build fine in real CI, not
just locally). `Go — eami-agent (darwin/arm64)` failed twice, both times
before any of this task's code ever ran (`Error: Service Unavailable`
during `actions/setup-go`'s own action-resolution step). Ruled out the
go-version bump as the cause by comparing against the immediately prior
run (`839e8ae`, pre-B-047 baseline) — the identical job/step succeeded
there. Confirmed via `githubstatus.com`: a platform-wide GitHub Actions
major outage (`qcvjkzcs7j74`, started 15:22 UTC, before this push) was
the actual cause, still unresolved after a second retry. **Per explicit
user instruction, stopped polling once this was confirmed external
rather than keep spending CI cycles against a declared incident — this
run should be rechecked once GitHub's status page shows the incident
resolved, not assumed green from here.**

Full writeup in `BUILT.md`'s `Cross-cutting / shared` section and
`BACKLOG.md`'s B-047/B-048/B-049/B-050 entries.

Prior entry, still accurate: 2026-08-06 by Claude Code — B-046: per-endpoint action-to-path mapping for
`rest_api`-type `gateway_tools`, closing the gap B-044's own manual testing
surfaced live that same night (`"Test/query"` 404'd because every action
for a tool POSTed to the identical flat `base_url`, regardless of name).

New `gateway_tools.action_paths JSONB` column (migration `010`,
`{"<action>": {"path": "...", "method": "..."}}`) — JSONB chosen over a new
table, matching this schema's own established convention for small
structured per-row config (`alert_rules.condition_config`,
`notification_config.config`, `episodes.steps`) and avoiding a join on
every dispatch for data that's always read as one small whole per tool.
`toolrouter.Forward` (`eami-gateway`): when a resolved tool's `ActionPaths`
has an entry matching the incoming action, dispatches to
`joinURLPath(base_url, entry.Path)` with `entry.Method` (default `POST`,
matching the flat behavior's own default) instead of always POSTing to
`base_url`. Request body envelope unchanged (`{tool, action, params,
session_id}`) — only URL path/method vary per mapped action; a deliberate
scope boundary against building a generic API-schema importer, per this
task's own brief.

**Fallback behavior for an unmapped action — decided with the user before
building, not assumed either way:** a tool with **no** `action_paths` at
all keeps B-044's exact flat behavior, fully additive, zero effect on any
tool that doesn't define mappings. A tool that **has** other mappings
defined, called with an action that isn't among them, is a **clean hard
rejection** — the user was asked to choose explicitly between that and a
silent fallback to `base_url`, and chose rejection, reasoning that routing
an unmapped action to a URL/credential context the admin never authorized
for it is worse than a clear failure. Matches this same file's existing
precedent of rejecting cleanly on unusable state (nil row, wrong type,
missing `base_url`, bad creds) rather than silently falling through.

**Security review finding, traced and empirically verified, not just
argued:** the one real question worth answering carefully was whether an
admin-supplied `action_paths[action].path` could be crafted to redirect a
dispatch to a different host than `base_url`'s own — an SSRF-guard bypass.
`joinURLPath` is pure string concatenation (`strings.TrimRight(base,"/") +
"/" + strings.TrimLeft(path,"/")`) forming **one** URL string parsed
**once** by `http.NewRequestWithContext` — never a `url.ResolveReference`-
style relative-URL resolution, which is the pattern with known
scheme-relative `"//host"` bypass pitfalls. Verified empirically, not just
by inspection: a standalone Go program fed `joinURLPath` six candidate
malicious `path` values (`//evil.example.com/steal`,
`http://evil.example.com/steal`, `https://evil.example.com/steal`,
`@evil.example.com/steal`, a `..%2F..%2F@evil.example.com` traversal
attempt) against `base = "https://good.example.com"` and parsed every
result with `net/url` — **every single one resolved to
`Host="good.example.com"`**, confirming the SSRF guard (which validates the
dialed address derived from the final request URL's host) cannot be
bypassed via a crafted mapped path. Added a defense-in-depth write-time
guard anyway, in `eami-api`'s `validateActionPaths` — reject any path
containing `"://"` — not because it's exploitable, but because it's a real,
easy admin misconfiguration worth catching early with a clear error rather
than a confusing literal path segment showing up later. `dial.go`/
`creds.go` (the SSRF guard, credential decrypt) confirmed byte-for-byte
untouched by this task, per its explicit scope boundary.

**Reviewer + security subagent passes, both mandatory — both automated
passes failed on a platform-wide session-limit error mid-task, before
completing (a tooling outage, not a finding).** Substituted with a direct,
careful manual review of the actual diff plus the empirical `joinURLPath`
verification above, rather than skipping the mandatory review step
entirely.

**Test coverage:** 22 new. `eami-gateway/internal/toolrouter/router_pg_test.go`
gained 5 real-Postgres tests (parses `ActionPaths` correctly; two different
mapped actions route to two different real paths/methods; an unmapped
action on a partially-mapped tool cleanly rejects; a tool with zero
mappings is completely unaffected; the SSRF guard still applies on the
mapped-path dispatch branch, not just the flat one) plus a new
`insertToolWithActionPaths` fixture helper. `eami-api/internal/api/
tools_action_paths_test.go`: 11 fake-store handler tests (valid mappings
persisted with method uppercased, omitted method defaults to `POST`,
empty-path/unsupported-method/full-URL-path all rejected with the store
never called, omitted-on-update leaves it nil, explicit `{}` produces the
literal clear signal, `ToolResp.ActionPaths` round-trips and is omitted
entirely via `omitempty` for a tool with none). `eami-api/internal/api/
tools_action_paths_pg_test.go`: 3 real-Postgres tests proving the same
tri-state `COALESCE` semantics against real bytes, not a fake store.
**Verified 2026-08-06 with a real toolchain: `go build`/`go vet`/`go test
./...` clean across `eami-gateway` and `eami-api`**, plus real `tsc && vite
build` via the established Docker builder-stage trick, clean.

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh first (`eami-api`/`eami-gateway`/`eami-ui` images, migration
`010` applied to the running Postgres):** created a real `rest_api` tool
(`base_url=https://postman-echo.com`) with two real action mappings via a
real admin JWT, issued a real agent token, opened a real MCP SSE session.
**AC1**: `echo_get` reached `https://postman-echo.com/get` (GET) and
`echo_post` reached `.../post` (POST) — both confirmed via the real echoed
response, both with the real decrypted `Authorization` header attached.
**AC3**: a third call to an undefined action on that same tool returned
the exact designed rejection error live over the SSE stream. **AC2**: a
separate pre-existing tool with zero `action_paths` (left over from
B-044's own manual testing) still flatly POSTed to its `base_url` for an
arbitrary action name, completely unaffected. All fixtures left in place
as verification evidence (harmless echo-API test data, no real secrets).

**Operational note, disclosed directly rather than left implicit:** the
pre-existing `dev@example.com` admin account's password was reset to a
known test value during this session's live verification, since no other
credential for it was available and there is no signup/register HTTP
route exposed to create a throwaway admin the way earlier tasks (B-044,
B-045) did. The user may want to reset it again.

Full writeup in `BUILT.md`'s `eami-gateway`/`eami-api`/`eami-ui` sections
and `BACKLOG.md`'s B-046 entry.

Prior entry, still accurate: 2026-08-06 by Claude Code — B-045: the Tools admin page gains a real Edit
flow, closing a gap B-044's own manual testing surfaced directly (fixing a
wrong `base_url` required a raw SQL `UPDATE`, since the UI had no way to
correct it — only Create/List/Delete/Test existed).

**Investigated the brief's own "is this a broader pattern" question
first, per its explicit instruction:** it wasn't. `Agents` (`PATCH
/v1/gateway/agents/{agentId}`), `Policies` (`PATCH /v1/gateway/policies/
{policyId}`), and `Settings` (`PUT /v1/settings/org`/`/notifications`) all
already had Edit — confirmed via `router.go`'s full route table, not
assumed. Tools was the one isolated outlier.

New `PATCH /v1/gateway/tools/{toolId}` (`store.UpdateTool`/`Server.
UpdateTool`) reuses the exact `COALESCE($n, column)` partial-update
convention `UpdateAgent`/`agents.sql.go` already established elsewhere in
this same file — a `nil` Go value means "leave this column unchanged."
Applied directly to `credentials_encrypted`: the request omitting
`credentials` → `nil` bytes → `COALESCE` preserves the existing encrypted
blob untouched, so an admin can fix a typo in `base_url` without ever
being forced to re-enter a working secret they aren't changing (the
brief's own stated UX goal). `credentials` present → re-encrypted via the
exact same `credentialsProvided`/`s.toolCreds.Encrypt` path `CreateTool`
already uses, same fail-closed-if-cipher-unconfigured guarantee.
`type`/`auth_type` deliberately not editable — matches `UpdateAgent`'s own
precedent of exposing only operational fields, not identity-shaping ones;
changing a tool's fundamental integration type is closer to
delete-and-recreate than a partial edit. `api/openapi.yaml` deliberately
not touched (Architect-EAMI-owned, no PATCH documented for tools) — ships
undocumented, matching B-038's already-established precedent for exactly
this situation.

**A real, necessary scope correction, presented to the user and confirmed
before building, not silently worked around:** `ToolsPage.tsx`'s existing
hooks call the generated OpenAPI client, which has no typed call for an
undocumented route; the documented fallback, `apiFetch()` in `src/api/
client.ts` (CLAUDE.md's own explicit "only through the generated client
or `apiFetch()` — no raw `fetch`/`axios` in components" convention), was
GET-only — no method or body parameters at all. Extended it with optional
`{method, body}` — small, generic, not tied to Tools specifically, so a
future edit flow on any other undocumented endpoint can reuse it instead
of reaching for a raw `fetch()` call, which the convention explicitly
disallows. Confirmed backward compatible: `apiFetch`'s only two existing
callers (`MemoryPage.tsx`, `usePasteEvents.ts`) call it with no second
argument, unaffected by the new optional parameter.

**AC4 (no stale routing after an edit) — verified, not assumed, per the
brief's own explicit instruction:** grepped `eami-gateway/internal/
toolrouter/router.go` (B-044) for any caching layer — none exists;
`Router.Resolve` is a direct `pool.QueryRow` on every single dispatch, so
an edit takes effect on the very next tool call by construction. Proven
directly, not just reasoned about, with a new real-Postgres test
(`TestResolve_PicksUpEditedConfig_NoStaleCache`): dispatch to server A,
edit the row's `base_url` via a real `UPDATE` (the same SQL effect
`UpdateTool`'s `COALESCE` produces for a real PATCH), dispatch again,
confirm server B — not server A — is now hit.

**Reviewer + security subagent passes both ran** (mandatory — credential
handling): security review found the core credential-preservation/
rotation logic sound — specifically re-derived, not assumed, that
`toolcreds.Cipher.Encrypt` can never produce a zero-length ciphertext (a
12-byte nonce plus a 16-byte GCM tag are the unconditional minimum), so
"credentials explicitly submitted" and "credentials omitted" can never be
confused by an empty-blob collision — but flagged a real, specific gap:
every test proving credential preservation ran against `fakeToolStore`
only, never against real Postgres, so the actual `COALESCE`-over-a-real-
bytea-column mechanism was asserted by code-reading, not proven to work.
**Closed, not left as a note**: new `tools_update_pg_test.go`, 4
real-Postgres tests using the real `store.Queries.UpdateTool` against a
real database, reading the real stored bytes back and decrypting them
with a real `toolcreds.Cipher` to confirm the actual end state — including
that a credential-rotation edit produces the NEW plaintext and the OLD
plaintext is genuinely gone, not just that no error occurred.

Code review caught and this session fixed two real, if narrow,
correctness bugs before either ever shipped: (1) **Medium** — `UpdateTool`
had no validation rejecting an explicitly-empty `name`, unlike
`CreateTool`'s identical check on the same field; a bare `{"name":""}`
would have silently blanked a tool's name via `COALESCE` (an empty string
is a real, non-NULL value, so nothing stopped it) — fixed with the same
check `CreateTool` already has, and the only guard preventing this before
the fix was the frontend's HTML `required` attribute, which any direct
API caller trivially bypasses. (2) **Low** — the Edit panel's `mcp_args`
handling treated a whitespace-only input as "provided" (JavaScript
truthiness doesn't distinguish `"   "` from a real value), and
`.split(' ').filter(Boolean)` on it produced an explicit `[]` rather than
`undefined` — silently wiping an existing tool's stored args instead of
leaving them unchanged, the opposite of every other field's "blank means
no change" semantic. Fixed to check `.trim()` before treating the field
as present. A follow-up focused review — including actually running the
new real-Postgres tests live against the real database, not just reading
the diff — confirmed both fixes correct and complete, with no
regressions to the already-passing legitimate-omission cases.

**Test coverage:** 17 new. 10 fake-store unit tests (`tools_update_test.
go`: name/base_url persistence, all three credential cases — omitted/
empty-object/provided — encryption-not-configured fails closed, no
credential leakage in any response, not-found, invalid-tool-id, and the
new empty-name rejection). 4 real-Postgres tests (`tools_update_pg_test.
go`, described above, plus one proving `mcp_args` preservation
specifically — verifying pgx v5's nil-slice-to-SQL-NULL array encoding
directly against real Postgres rather than assuming it, since that field
uses a different encoding path than the `pgtype.Text`-wrapped scalar
fields). 1 new real-Postgres routing test in `eami-gateway` (AC4, above).
`eami-ui`: real `tsc && vite build` via the established Docker
builder-stage trick (Node/npm still absent locally), run twice — once
before and once after the `mcp_args` fix, both clean. **Verified
2026-08-06 with a real toolchain: `go build`/`go vet`/`go test ./...`
clean across `eami-api` and `eami-gateway`.**

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh first:** edited the real "Test" `gateway_tools` row that
B-044's own manual-testing session had created, via a real admin JWT (a
throwaway admin user seeded in the same real org for this purpose,
deleted afterward — the real pre-existing dev user's password wasn't
known and wasn't guessed at). Renamed the tool, then edited its
`base_url` to a distinguishable real `postman-echo.com` path. A single
subsequent real `tool_call` sent through the live gateway proved AC2 and
AC4 simultaneously, in one response: the dispatch reached the **edited**
`base_url` (not stale/cached configuration), while the **original**
credential — set during B-044's own live verification, never touched by
any of these edits — was still applied correctly as a real `Authorization`
header. The tool's `base_url` was restored to a clean value afterward;
all throwaway fixtures (the admin user, tokens, SSE sessions) cleaned up.

**Known limitations, documented not silently left**: no way to clear
(set to empty) `base_url`/`mcp_command` via the Edit panel once set — a
blank field means "no change" (matching the credential field's own
established semantic by design), never "clear it"; a rename that collides
with another tool's name in the same org surfaces as a raw `500` with the
Postgres error text, not a clean `409` (a pre-existing pattern shared with
`CreateTool`, not introduced by this task, out of scope to fix a
convention used elsewhere in the same file).

Full writeup in `BUILT.md`'s `eami-api`/`eami-ui` sections and
`BACKLOG.md`'s B-045 entry.

Prior entry, still accurate: 2026-08-06 by Claude Code — B-044: MCP tool routing is dynamic for the
first time. `eami-gateway/internal/proxy.Forward` had always forwarded
every tool call to one hardcoded static URL (`cfg.Proxy.DownstreamSSEAddr`)
regardless of which tool was named — `gateway_tools` (schema, CRUD,
credential encryption via B-022, connectivity testing via B-023) had zero
live consumers anywhere in `eami-gateway`, confirmed by an investigation
via repo-wide grep before this task was scoped.

New `internal/toolrouter` package resolves an incoming tool_call's parsed
tool name against `gateway_tools`, scoped to the caller's org (same
org-safety discipline as B-042's `registry.LookupByNameAndOrg` fix), and —
for `rest_api`-type rows only, `mcp`/`database` explicitly out of scope
this brief — dispatches to that tool's real `base_url` using its real
decrypted credentials, instead of the static URL. Policy is checked
against the specific resolved tool via new `eami-policy` fields,
`ActionContext.ToolServerID`/`Conditions.ToolServerIDs` (additive, mirrors
the existing `ToolNames` by-string-name matcher exactly, but pins to the
resolved `gateway_tools.id` — immutable, unlike a tool's name).

**Two of the task brief's own stated assumptions were wrong, and were
corrected before building rather than silently worked around, per the
kickoff line's own instruction to confirm understanding first:** the
brief assumed B-023's SSRF guard (`safeDialContext`/`isBlockedTestTarget`)
and B-022's credential cipher (`toolcreds.Cipher`) could simply be
"relocated/exported to a shared location" or reused "as-is." Both live in
`eami-api/internal/...` — `eami-gateway` and `eami-api` are separate Go
modules under `go.work`, and Go's `internal/` package visibility is
scoped to the module tree, so neither is importable across that boundary
regardless of export status; "relocate to a shared location" would
require standing up a new shared Go module, real structural work outside
this brief's scope. This repo already has a precedent for exactly this
situation: B-025 duplicated `isPlaceholderSecret`/
`dsnHasPlaceholderPassword` verbatim into both modules rather than build
shared infrastructure for ~30 lines. Same call here: new
`toolrouter/dial.go` and `toolrouter/creds.go` are deliberate, documented
duplicates (`creds.go`'s `Cipher` is decrypt-only — `eami-gateway` never
writes credentials, only ever reads what `eami-api` already encrypted);
`eami-api`'s originals are completely untouched, still needed for its own
connectivity-test feature.

**A genuine architectural fork was presented to the user before building,
rather than decided unilaterally:** what should happen when a tool_call's
name matches no `gateway_tools` row at all for the caller's org — the
case for 100% of today's live traffic, since nothing has ever been
registered? Two real options existed: fall back to the existing static
path (strictly additive/opt-in dynamic routing, zero regression risk to
live traffic), or reject outright (architecturally "complete," but a real
outage for every existing tool call the moment this shipped). **User
chose fall-back-to-static.** This also meant `proxy.go` and
`internal/approval/router.go` (the escalation/approval path) could stay
**completely untouched** — dynamic routing only applies to `dispatch`'s
immediate-allow branch; an approved escalation still always uses the
static proxy exactly as before, avoiding any exception to "approval flow
internals frozen."

`resolveDynamicTool` (new, `cmd/gateway/main.go`) is `dispatch`'s routing
decision extracted into a small, named, unit-testable function
specifically so this coverage didn't require standing up the full
MCP/SSE/dispatch machinery.

**Reviewer + security subagent passes both ran** (mandatory — new
outbound dispatch to admin-supplied destinations, at least B-023 rigor):
**both came back clean, no exploitable vulnerabilities, no blocking
findings.** Security review specifically re-derived, not trusted from
comments: the duplicated SSRF guard is byte-for-byte equivalent in
protective effect to the original, including the resolve-once/dial-the-
validated-IP DNS-rebinding protection; no code path reaches the network
without the guarded dialer (the only unrestricted dialer is confined to
`router_pg_test.go`, confirmed via grep); Go's default redirect-following
does NOT bypass the guard, since it's implemented as the `Transport`'s
`DialContext` (re-invoked on every redirect hop, not a one-time pre-check
on the original URL) — a malicious `rest_api` target can't 3xx its way
around the block; no decrypted credential ever reaches a log line or
error message across every branch of `Forward`; `toolrouter.Cipher` and
`eami-api`'s `toolcreds.Cipher` are genuinely interoperable (compared
line-by-line — identical key size, AES-GCM construction, nonce framing,
no AAD on either side — and confirmed via a real round-trip test, not
assumed). One informational, not-a-vulnerability note: the guard's
correct, conservative RFC1918 blocking also blocks an on-prem gateway's
legitimate access to a customer's own internal APIs — flagged for product
as a future allowlist decision, not fixed here (fails closed correctly in
the meantime, the safe failure mode). Code review's two other minor
findings (no `gateway_tools.status` filter in `Resolve`; `db_connection_
string` `auth_type` silently ignored on `rest_api` rows, same class as
B-023's already-accepted `basic`-auth gap) left as documented,
non-blocking limitations.

**A real correctness bug was found and fixed during code review, before
it ever shipped:** `Router.Forward` dereferenced `row.Type` with no
nil-guard — unreachable via the single production call site (already
correctly guarded), but `Router` is an importable package and its own doc
comment promises no failure path ever panics. Fixed with a one-line check
plus a new regression test.

22 new tests: `internal/toolrouter/router_pg_test.go` (11 real-Postgres
tests, including an injectable-dialer test seam mirroring B-023's own
`dialContextFunc` convention exactly — production `New()` always uses the
real guard, only `_test.go` files construct the unrestricted one needed
to reach a local `httptest` server without tripping the loopback block),
`cmd/gateway/main_pg_test.go` (6 tests for `resolveDynamicTool`'s every
branch), `eami-policy/policy_test.go` (`TestMatchesRule_ToolServerIDs`),
`eami-gateway/internal/policyloader/loader_pg_test.go` (1 real-DB
round-trip test). **Verified 2026-08-06 with a real toolchain: `go
build`/`go vet`/`go test ./...` clean across `eami-gateway`, `eami-policy`
— `eami-api` confirmed unaffected** (untouched module, build/vet clean).

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh first:** seeded a real org/agent/`gateway_tools` row with
real AES-256-GCM-encrypted credentials (the real `TOOL_CREDENTIALS_
ENCRYPTION_KEY`), issued a real token, opened a real SSE session, posted a
real `tool_call`. **AC1**: routed to the tool's real `base_url` — a real
public echo endpoint (`postman-echo.com/post`) — with the real decrypted
API key confirmed arriving as a real `Authorization: Bearer` header at the
real destination. **AC3, discovered organically, not staged as a planned
test case**: the first live-verification attempt used `host.docker.
internal` (a local throwaway echo server) as the tool's `base_url` — the
real SSRF guard correctly rejected it, since Docker Desktop's
`host.docker.internal` resolves to a private-range address inside
Docker's own internal network, exactly the class of target this guard
exists to block. Genuine, unplanned live proof the guard is active on the
real dispatch path, not just a designed scenario. **AC4**: a second real
org/agent calling the identical tool name did not reach the first org's
registered destination — fell through to the exact same static-fallback
error shape as an ordinary unregistered call, proving cross-org isolation
live. **AC6**: a real `mcp`-type `gateway_tools` row, called live, also
fell through to the identical static-fallback path, confirming `mcp`/
`database`-type registration doesn't accidentally trigger dynamic
dispatch. `audit_log` correctly recorded all four live calls (`allowed`
for the real successful dispatch, `denied` for the SSRF-blocked/cross-org/
mcp-fallback attempts), confirming auditing is unaffected. All test
fixtures (throwaway orgs/agents/tools, the local echo server process)
cleaned up afterward; final `go build`/`go vet`/`go test ./...` re-run
clean post-cleanup.

**Known limitations, documented not silently left**: no `gateway_tools.
status` filter (an admin-disabled tool is still dynamically routable);
`db_connection_string` auth on a `rest_api` row silently sends no auth
header; on-prem customers' legitimate internal (RFC1918) REST targets are
unreachable by the SSRF guard's design (no allowlist mechanism exists,
flagged for product); no per-endpoint action-to-path mapping (every
action for a tool POSTs to the same single `base_url`, per the routing
investigation's own explicit scope boundary); `mcp`/`database`-type
dynamic routing and MCP server capability discovery remain future,
separate work per that investigation's own phasing recommendation.

Full writeup in `BUILT.md`'s `eami-gateway`/`eami-policy` sections and
`BACKLOG.md`'s B-044 entry.

Prior entry, still accurate: 2026-08-05 by Claude Code — B-042: `POST /v1/gateway/tokens/{jti}/revoke`
is real and wired, closing the "Manager.Revoke has zero production
callers" gap B-041 left open. Two deliberate, investigated, user-approved
deviations from `api/openapi.yaml`, both documented in `revoke_http.go`'s
own doc comment rather than silent: (1) auth is a new gateway-local
`X-Service-Key` (`GATEWAY_TOKEN_REVOKE_SERVICE_KEY`, required/fail-closed
at startup, same treatment as the pre-existing `EpisodeReadServiceKey`),
not the documented `BearerAuth` — that scheme is explicitly defined
elsewhere in the same spec as "JWT issued by `POST /v1/auth/login`",
`eami-api`'s user-session JWT, which `eami-gateway` has no code path to
validate at all (different issuer/key/claims). Real precedent found for
the intended eventual shape: `eami-api/internal/api/router.go` already
gates the structurally-identical `POST/PATCH/DELETE /v1/gateway/agents*`
behind `requireRole("admin", "operator")` — building that full proxy now
was explicitly considered and rejected as exceeding this task's scope (it
would also require solving an unscoped problem: no "list active tokens"
surface exists anywhere for an admin to pick a `jti` from). A real
`eami-api`-hosted, role-gated proxy using this service key is the
intended long-term caller, not built here. (2) the path uses `{jti}` (the
token's ID) instead of `{token}` (the full JWT `openapi.yaml` implies),
specifically so a live bearer credential never lands in a URL/access log.

Closes B-041's two flagged latent gaps, confirmed against the actual
review findings before treating them as fixed: the handler resolves
`agent_name` via the registry before ever calling `Revoke` (never passes
a raw JWT `sub` string); `Manager.Revoke(jti, agentID string)` now
returns an `error` instead of only `slog.Error`-logging a persistence
failure internally — the one approved exception to `tokens.go` staying
otherwise frozen this task — so the handler surfaces a real `500`, never
a false `204`, on a genuine DB failure.

**A third, more severe gap found during this task's own security review —
required to be fixed in-task per explicit user direction, not deferred
the way B-041→B-042 itself was staged across two tasks:** `registry.
LookupByName`'s query (`WHERE name = $1`, no `org_id` filter) is safe for
every *existing* caller (`internal/mcp`, `internal/episode`) only because
their `agent_name` always comes from a signature-verified JWT `sub`,
itself inherently bound to one org at issuance. This new handler is the
first caller passing a raw, client-supplied `agent_name` with no such
cryptographic binding — since `gateway_agents.name` is only unique per
`(org_id, name)` (`schema.sql`'s own constraint), an Org A caller could
otherwise resolve, and cause the permanent-audit-record revocation of,
Org B's identically-named agent's token. Fixed with a new, purely
additive `registry.LookupByNameAndOrg(ctx, name, orgID string)` — direct
`WHERE name = $1 AND org_id = $2`, deliberately uncached to avoid any
interaction with `LookupByName`'s existing name-only cache —
`LookupByName` itself and all its existing callers are completely
untouched, confirmed via `git diff` (pure addition, zero deletions). The
revoke request body now requires `org_id`, validated as a well-formed
UUID before any query — caller-supplied and trusted, matching
`episode/http.go`'s own already-shipped, already-documented precedent for
its service-key path's `org_id` query param, not a new trust model
invented for this task. Also correctly preserved, not accidentally
dropped in the port: an agent whose registration is already
suspended/revoked still resolves (only a genuinely-unknown name returns
`nil`) — the handler deliberately treats revoking such an agent's token
as valid, not an error, since a session established before suspension may
still hold a live, revocable token.

Reviewer + security subagent passes ran twice: an initial pass against
the base implementation came back clean on the auth/persistence-error
design and separately surfaced three findings — the org-scoping gap above
(fixed), a real multi-node revocation-broadcast gap (`Manager.Revoke()`
only updates the in-memory set on whichever node handles the request;
`schema.sql`/`NewManagerWithDB`'s doc comments both claim Serf-based
broadcast that a repo-wide grep confirmed doesn't exist anywhere —
predates B-042 entirely, this task just made it concretely reachable for
the first time; **not fixed here per the user's explicit direction, filed
as new B-043**), and transient-DB-errors-collapsing-to-`403`-with-raw-
error-text-in-the-response (confirmed to be a pre-existing pattern
already shipped in `episode/http.go`/`mcp/handler.go`, not a regression
introduced here — left as-is, matching this codebase's convention of not
fixing unrelated pre-existing patterns outside a task's own diff). A
focused follow-up pass, run specifically against the org-scoping fix
after it was built, came back fully clean.

9 new tests: `internal/identity/revoke_http_pg_test.go` (5 real-Postgres
integration tests — happy path, wrong/missing service key, unknown agent,
**cross-org agent_name rejection** proven with a real second org and a
real agent rather than simulated, and a simulated persistence failure via
a `fakeResolver` test seam that triggers a genuine FK violation) plus 4
new/updated `internal/config/config_test.go` cases for the new required
secret. **Verified 2026-08-05 with a real toolchain: `go build`/`go
vet`/`go test ./...` all clean, 0 failures across the full `eami-gateway`
module** — `internal/identity` alone now has 26 tests (17 pre-B-042 + 9
new).

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh first (not a stale image):** seeded a real org/
`gateway_agents` row, issued a real token, confirmed it valid via a real
`GET /v1/mcp/sse` request. **AC2**: a revoke attempt with the wrong
`X-Service-Key` returned real `401`, token confirmed still valid
immediately after. **AC1**: a request with the real service key, correct
`agent_name`, and correct `org_id` returned real `204`; the row was
confirmed present in `revoked_ai_tokens` with the correct `agent_id`; a
subsequent request with that exact token returned `401 unauthorized:
identity: token ... has been revoked`. **Went further than the brief's
minimum:** restarted the `eami-gateway` container and confirmed this
specific HTTP-route-driven revocation survives the restart too
(`"identity: hydrated revocation list" count=1`, token still rejected
afterward) — proving the full B-041→B-042 chain works together
end-to-end, not just each half verified in isolation. All test fixtures
(throwaway orgs/agents, the revocation row) deleted/cleaned up afterward.

Full writeup in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s
B-042/B-043 entries.

Prior entry, still accurate: 2026-08-04 by Claude Code — B-041: `eami-gateway`'s JWT revocation
persistence actually works now, closing the gap B-039's live verification
found incidentally. The bug as originally filed was narrower than reality:
`dbRevocationStore.loadAll`'s startup-hydration query referenced a column,
`revoked_ai_tokens.expires_at`, that has never existed on that table
(confirmed by reading `schema.sql` and every file in `schema/migrations/`
— revocation here is permanent by design, unlike `sessions`/
`approval_requests` elsewhere in the same schema, which do have real
expiry columns). But investigation before writing any fix found
`dbRevocationStore.save` (`Revoke()`'s only DB side effect) was *also*
broken — same nonexistent column, plus a missing required `agent_id` FK
column — meaning every DB-backed revoke had always silently failed to
persist (logged, never surfaced). Nothing had ever actually reached the
table for hydration to load, so fixing the SELECT alone would not have
closed the loop. Fixed both, with the user's explicit approval to expand
scope beyond the brief's original "hydration query" framing, since AC1
("a token revoked before restart is still rejected after restart") was
provably unachievable without it.

**A real, non-obvious gap found during this task's own live-verification
pass, fixed before shipping rather than left for whoever builds next:**
`Claims.Subject` (the JWT `sub`) is `"agent:<name>"` — `internal/
registry`'s documented convention, the same value `internal/mcp`/
`internal/episode` already resolve via `registry.LookupByName` before
using — never a `gateway_agents.id` UUID directly. `Revoke()`'s new
`agentID` parameter needs the real UUID; the obvious-looking
`Revoke(claims.ID, claims.Subject)` call (what this task's own initial
plan proposed, before live verification caught it) would fail the FK
insert on every real revoke, reproducing this exact bug class via a new
trigger. `Revoke()`'s doc comment now says this explicitly, with a
pointer to the existing resolution pattern; `tokens_pg_test.go`'s
restart-simulation test deliberately issues a token in the real
`"agent:<name>"` shape and resolves it itself, rather than the
UUID-shaped shortcut an earlier draft of the test used, so a future
regression here would be caught.

Hydration failure is now fatal, not a silent warning: `newManager()`
returns the `loadAll` error instead of catching it into `slog.Warn`;
`main.go`'s single production call site already treats a returned error
as fatal, so this reuses existing error-propagation plumbing rather than
adding a new fail-fast mechanism.

`Manager.Revoke` has **zero production callers today** — confirmed by
repo-wide grep — since the `POST /v1/gateway/tokens/{token}/revoke` route
`api/openapi.yaml` documents was never wired into `main.go`'s router.
Filed as **B-042**, not built here, per an explicit scope decision.
Reviewer + security subagent passes both ran (mandatory, credential-
revocation control): both clean against this diff, both independently
found and agreed on two **PLAUSIBLE, currently-latent** (not exploitable
today, since `Revoke()` has no live caller) gaps — persistence errors
still aren't returned to the caller, and `Issue()` accepts an unvalidated
`AgentID` — folded into B-042's acceptance criteria rather than fixed
here, since they only become live the moment a real route calls `Revoke`.

4 new real-Postgres integration tests in `internal/identity/
tokens_pg_test.go`, following `internal/approval/router_pg_test.go`'s
B-039-established pattern (`TEST_DATABASE_URL`, throwaway `orgs`/
`gateway_agents` fixtures, relying on the schema's own `ON DELETE CASCADE`
chain for cleanup). Also corrected two stale doc comments in
`tokens_test.go` that claimed two already-passing tests were "CURRENTLY
FAILING" (one of the two was unrelated to B-041 entirely — `WrongIssuer`
had apparently been fixed at some earlier, undated point; the other,
`SurvivesRestart`, only ever exercised the file-backed store, which never
had B-041's bug in the first place) — fixed since they were directly
adjacent to the code being touched, not left as drift for later.
**Verified 2026-08-04 with a real toolchain: `go build`/`go vet`/
`go test ./...` clean, 0 failures across the full `eami-gateway` module**
(17 tests in `internal/identity` alone).

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh first** (Docker Desktop wasn't running at session start;
started with the user's explicit confirmation, per this session's
standing norm around system-state changes): issued a real token via the
real `POST /v1/gateway/tokens` for a real seeded org/`gateway_agents`
row; revoked it via the actual `dbRevocationStore.save` code path against
the real DB (no HTTP route exists yet to call `Manager.Revoke()`
end-to-end — see B-042; this isolates exactly what B-041 fixes rather
than being a gap in the proof); confirmed the row landed in
`revoked_ai_tokens`; confirmed the already-running gateway process still
accepted the token immediately after (expected, since only the DB was
touched directly); restarted the container and confirmed the startup log
now reads `"identity: hydrated revocation list" count=1` — the exact spot
that previously logged the `WARN` this item exists to close; confirmed a
real `GET /v1/mcp/sse` request bearing that exact token now returns
`401 unauthorized: identity: token ... has been revoked` — **AC1, proven
end-to-end.** Separately stopped Postgres entirely and restarted the
gateway container: confirmed it fails fast and crash-loops rather than
starting in a silently-degraded state (this specific scenario is actually
caught upstream of identity hydration by the pre-existing `pool.Ping`
check, but exercises the same "fail loud, not silent" contract end-to-end
— the hydration-specific fatal path itself is covered directly by a unit
test, `TestNewManagerWithDB_HydrationFailure_IsFatal`). All test fixtures
(throwaway org/agent, the revocation row, and a scratch-only test file
used solely to invoke the real revoke code path against this specific
live token) were deleted/cleaned up afterward.

Full writeup in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s
B-041/B-042 entries.

Prior entry, still accurate: 2026-08-04 by Claude Code — B-039: the approval flow (flagship human-in-
the-loop AI-governance mechanism) completes a real cycle for the first
time. An original module audit's three claimed bugs in
`eami-gateway/internal/approval/router.go` were re-verified directly
against current code before touching anything, per the task's own
instruction — all three still held exactly as described: `Submit()`'s
INSERT omitted 5 real `NOT NULL` `approval_requests` columns
(`justification`/`risk_level`/`expires_at`/`gateway_session_id`/
`gateway_node_address`); `Hold()`/`resolve()` queried nonexistent
`decision`/`reason` columns (real: `status`/`decision_reason` — plausible
cause noted: `audit_log`, a different table, has its own `decision`
column with the exact `'allowed'`/`'denied'` vocabulary this code
mistakenly reused); `resolve()` checked for `"allowed"`, but `eami-api`'s
`DecideApproval` (independently re-confirmed correct, unchanged) only
ever writes `"approved"`/`"denied"`. Fixed, confined to `router.go` per
an explicit scope decision the user confirmed: `justification`
synthesized from `req.Tool`/`req.Action` and `gateway_node_address` from
`os.Hostname()`, rather than threading the real policy reason/configured
listen address through from `main.go`; `risk_level` defaults to
`"medium"` (no risk-classification concept exists anywhere in the
policy/schema — new B-040 queued for the real design work).

**A fourth, previously-unknown root cause was found only during live
verification** — outside `router.go`, in `internal/mcp/handler.go` —
and fixed only after explicitly asking the user whether to expand scope,
since it wasn't in the brief's stated file list: `ServeMessages` ran the
real dispatch/`Submit()`/`Hold()` logic in a detached goroutine passed
`r.Context()`, which `net/http` cancels the instant the handler returns
(right after its `202` response) — every escalation's `Submit()` was
racing its own context being torn down and losing almost every time,
regardless of the other three fixes. Fixed with
`context.WithoutCancel(r.Context())`.

Reviewer + security subagent passes both ran (mandatory — this is the
AI-governance human-in-the-loop control). Security review found no
fail-open bypass, one real Medium (a genuine timing race at the `Hold()`
timeout boundary could silently drop an already-approved action — fixed
with a non-blocking channel re-check plus a conditional `UPDATE ...
RETURNING` that honors an already-decided row instead of clobbering it),
one Low (Slack notification text wasn't escaping attacker-influenceable
fields against Slack's own mrkdwn special characters — fixed). Code
review caught a real Medium (`nilIfEmpty` silently turned an empty
`org_id`/`agent_id` into SQL `NULL` for two `NOT NULL` FK columns — would
have reproduced this exact bug class — fixed) and a stale doc comment.

**Proven live, end-to-end, against the real running `docker-compose`
stack — every acceptance criterion, not just unit tests:** rebuilt and
redeployed both `eami-gateway` and `eami-api` first (this session's own
standing lesson about stale images, applied proactively this time).
Seeded a real org/agent/ESCALATE policy/admin user, got a real signed
agent JWT, opened a real SSE session, posted a real `tool_call` — a real
`approval_requests` row appeared, was visible via the real admin-login +
`GET /v1/approvals/{id}`, a real `POST .../decide {"approved"}` correctly
resumed the action via a temporary local fake downstream, a second real
escalation denied the same way was correctly and permanently blocked, and
Slack was confirmed reached via a temporary local fake webhook (the
real configured webhook deliberately left untouched, not spammed with a
test message). A test-environment mistake (a stray anonymous Docker
volume briefly broke the gateway's config load) was caught and fixed
transparently before finishing, with the real stack confirmed healthy
afterward. `go build`/`go vet`/`go test ./...` clean across both
`eami-gateway` and `eami-api`.

One unrelated bug found incidentally during live verification, not fixed
(out of scope, queued as B-041): `eami-gateway`'s JWT revocation list
fails to hydrate on every startup (queries a column, `expires_at`, that
doesn't exist on `revoked_ai_tokens`) — previously-revoked tokens
silently stop staying revoked across a restart.

Full writeup in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s
B-039/B-040/B-041 entries.

Prior entry, still accurate: 2026-08-04 by Claude Code — B-037: `nmlauncher`'s parent-process allowlist
fix, closing tonight's own dev-testing weakening (fail-open) with a
properly researched fix rather than just re-flipping a flag. Investigated
before building, per the task brief: confirmed (Chromium release-channel
documentation, cross-checked against this machine's real running
processes) that Chrome and Edge already use the identical executable name
across every Windows channel — the Windows portion of the allowlist was
never actually incomplete, refuting the hypothesis the original incident
suggested. Also confirmed Chrome's native-messaging-host launch mechanism
has used no `cmd.exe` intermediary since Chromium 113 (2023), ruling out
an intermediary-process theory for any current browser. Real gaps found
and closed instead: Linux and macOS both use distinct per-channel
process/bundle names (unlike Windows), previously missing entirely —
added, flagged as researched-but-unverified on a live host of those
platforms, matching this package's existing per-OS honesty convention.
Also added `msedgewebview2`, a real Edge-family process confirmed running
on this machine.

**The exact original failure was never captured and cannot be
retroactively diagnosed** — surfaced plainly as an open question rather
than left as an unexplained "fixed it anyway," per the user's explicit
request. Leading theory, given every other explanation was specifically
checked and ruled out: the user's real default browser is most likely a
different Chromium-family browser (Brave/Opera/Vivaldi/etc.), not Chrome
or Edge. The user was asked and explicitly chose to keep the allowlist
scoped to Chrome/Edge only (not widened to other Chromium browsers, per
this task's own acceptance criteria) — a genuinely different browser
still requires `EAMI_NM_SKIP_PARENT_CHECK=1`. The logged `Reason` string
still names the actual detected parent on every refusal, so a future
recurrence can be checked against this theory directly rather than
re-diagnosed from scratch.

Fail-closed restored for "parent determined but not recognized"; "parent
could not be determined at all" keeps its original fail-open-with-warning
behavior, confirmed (not just carried over) as the correct original
design. Reviewer + security subagent passes both ran: code review caught
a real Medium (4 new allowlist entries lacked test coverage) and a Low
(misleading comment grouping), both fixed; security review found no
High/Medium issues and one Low (an entry's doc comment overclaimed
verification), fixed by rewording. Re-verified live end-to-end: rebuilt
`eami-agent.exe`, confirmed a non-browser parent is refused (exit 1),
confirmed the override still works, and ran a full native-messaging
protocol round trip through the real Docker stack that landed correctly
in `paste_events` (B-035's table) — proving the whole B0→B1→B-035
pipeline still works with this fix in place. `go build`/`go vet`/
`go test ./...` clean across `eami-agent`.

Full writeup in `BUILT.md`'s `eami-agent` section and `BACKLOG.md`'s
B-037 entry.

Prior entry, still accurate: 2026-08-04 by Claude Code — B-035: paste events wired into the real
`paste_events` table, closing the last open item in the paste-detection
epic (B-032→B-034→B-036→B-038→B-035, in build order). The brief's own
assumption ("`eami-collector` already resolves org context for other
ingest paths") was investigated per its own explicit instruction and
found **not accurate** — confirmed by grep that `eami-collector` resolves
no `org_id` anywhere in its code at all; only `eami-api`'s `IngestBatch`
does, via the pre-existing `GetDefaultOrgID` ("oldest org") single-tenant
placeholder. Traced the wire format end-to-end and found `eami-agent`'s
native-messaging relay (B0) already sends paste events through the
existing, completely unmodified `/v1/ingest`→buffer→forward→
`/v1/ingest/batch` pipeline — so **the entire fix landed in `eami-api`
alone, zero changes to `eami-agent` or `eami-collector`**, a real
simplification over the brief's own initial sketch (which anticipated a
new `eami-collector` relay route).

`processIngestItem` now detects a paste-event-only item (`rep.PasteEvents
!= nil`) and routes it to a new `processPasteEventRelayItem`, which
resolves the endpoint via B-032's non-clobbering `ResolvePasteSourceEndpoint`
(not `UpsertAgentEndpoint`, which would otherwise blank out a real
endpoint's `agent_version`/`os_info` with this item's empty scan fields —
a real correctness bug found and designed around *before* writing any
code, not discovered after) and writes via `BatchInsertPasteEvents`,
never touching `endpoint_reports` for that item. `org_id` is always the
value `IngestBatch` already resolved server-side — `agentReport` has no
field anywhere a compromised `eami-agent` could use to influence it,
structurally, not by convention.

**Reviewer + security subagent passes both ran** (mandatory, org-
resolution trust boundary). Security review: no High/Medium findings;
confirmed the design stays safe if `GetDefaultOrgID` is ever replaced by
real multi-tenant resolution, as long as whatever replaces it keeps
deriving `orgID` from an authenticated channel rather than request-body
fields. Code review caught a real Medium-High bug before ship: the
original branch condition (`len(rep.PasteEvents) > 0`) couldn't
distinguish an absent `paste_events` key from an empty-but-present `[]`
— an edge case that would have silently fallen through to the clobbering
path — fixed (checks `!= nil` instead) and locked in with a new
regression test. Both reviews independently flagged missing length/
array-size caps; added.

6 new integration tests (`ingest_paste_relay_test.go`), all posting the
exact JSON shape a real B0 relay produces. **Verified 2026-08-04 with a
real toolchain: `go build`/`go vet`/`go test ./...` clean across
`eami-api`, plus `eami-agent`/`eami-collector`/`eami-gateway` all
confirmed still building clean** (proving the "zero changes needed"
claim, not just asserting it). A genuine environment issue was hit and
worked around mid-task, unrelated to this code: this machine's
`localhost` resolves IPv6-first, and Docker Desktop's IPv6 port-forward
here accepts the TCP handshake but silently drops all data after —
diagnosed with a minimal standalone Go probe, worked around via
`TEST_DATABASE_URL` forcing IPv4 for test runs (no code changes), full
stack confirmed healthy via `127.0.0.1` afterward.

Full writeup in `BUILT.md`'s `eami-api` section and `BACKLOG.md`'s B-035
entry.

Prior entry, still accurate: 2026-08-03 by Claude Code — B-038: B2, the first admin UI over B-032's
`paste_events` (new `eami-ui` page at `/paste-events`, under the
`Operations` sidebar group, plus two new `eami-api` GET routes:
`/v1/paste-events` and `/v1/paste-events/timeseries`). Placement decided
against Discover and recorded with reasoning: paste events are an
org-wide time-series event log (filter bar + paginated table +
`time_bucket` aggregation), structurally identical to `AuditPage.tsx`/
`FinOpsPage.tsx`, not to Discover's per-endpoint asset-inventory shape.

**Data-source decision, checked before writing code, not assumed:**
confirmed live via a direct `psql` query that the real `Dev Org` has
**zero rows** in `paste_events` right now — the 800,000 rows present all
belong to B-032's own disposable `paste-events-perf-*` synthetic test
orgs from a prior session. The only real, live-captured paste events
(from B1's manual test pass) sit only in `endpoint_reports`' raw JSON
blob, the interim path B-035 exists to retire. Built this UI against
`paste_events` anyway (the correct, indexed, purpose-built table — the
alternative would be throwaway work), which means it shows an honest
empty state for any real org until B-035 ships. **B-035's priority was
raised to High and it is now the explicit next task**, per the user's
explicit direction after reviewing this finding — it is the only thing
separating this shipped, fully-tested feature from showing real data.

Backend: `store.ListPasteEvents`/`CountPasteEvents` (mirrors
`ListAudit`/`CountAudit`'s exact optional-filter/pagination shape, reuses
`idx_paste_events_org`/`idx_paste_events_org_domain`) and
`Server.PasteEventsTimeSeries` (raw `time_bucket()` SQL in the handler,
mirroring `FinOpsTimeSeries`'s structure, but returning real
per-domain-per-bucket counts rather than FinOps's proportionally-
distributed display approximation). `api/openapi.yaml` deliberately not
touched (Architect-EAMI-owned) — widened B-033 to cover the gap; frontend
uses the `apiFetch` escape hatch, same as `MemoryPage.tsx` did pre-B-002.

**Verified 2026-08-03 with a real toolchain:** `go build`/`go vet`/
`go test ./...` clean across `eami-api`, including 2 real `EXPLAIN
ANALYZE` tests (reusing B-032's own 100,000-row seeded-volume
convention) proving the exact new queries use `idx_paste_events_org_domain`/
`idx_paste_events_org`, zero sequential scans. Frontend verified via a
real `tsc && vite build` (the `docker build --target builder` path B-024
established, Node/npm still absent locally) — clean. **No live
interactive browser click-through performed** (same standing
no-safe-browser-automation limitation as B1) — manual verification steps
provided in `BUILT.md`'s new `eami-ui` entry instead.

Full writeup in `BUILT.md`'s `eami-api`/`eami-ui` sections and
`BACKLOG.md`'s B-038 entry.

Prior entry, still accurate: 2026-08-03 by Claude Code — B1 (B-036) is now fully manually verified, closing
the loop from the entry just below. The user personally ran all 5 steps of
`eami-browser-extension/MANUAL_TESTING.md` to completion on a real
Chrome/Edge install: extension ID and scoped permissions confirmed, paste
capture with no raw content confirmed on an allowlisted domain,
non-allowlisted-domain silence confirmed, service-worker-restart
durability confirmed, and `flush()` confirmed sending with the event
landing in `endpoint_reports` in the real Docker Postgres stack. This
closes the one leg (the in-browser side) that couldn't be verified when
B1 was originally built, per the entry below. Two real bugs were found
and fixed by this live pass specifically (neither catchable by review or
by this session's own synthetic testing, since both needed a genuine
browser + genuine OS-level native-messaging registration to surface) —
full detail in the prior entry immediately below and in
`BUILT.md`/`BACKLOG.md`'s B-036 entries: (1) `background.js` silently
discarded a failed native-messaging connection, now logged; (2)
`nmlauncher`'s parent-process check refused the user's real browser, now
fails open with a warning instead of closed, tracked as **B-037** for a
real fix before any customer install (explicitly not the final security
posture — see the new standing fact above).

Prior entry, still accurate: 2026-08-03 by Claude Code — B1 manual-testing follow-up: fixed a real bug
found only by the user's own hands-on click-through (not by review or
automation), in `internal/nmlauncher` (B0's parent-process
defense-in-depth check). Manual test step 5 (`flush()`) failed twice in a
row: first silently (B0 wasn't currently registered on this machine --
re-registered, unrelated to the bug below), then with Chrome's generic
`"Error when communicating with the native messaging host."` after
re-registering. Root-caused by reproducing the exact failure signature
myself (host process exits immediately, code 1, zero bytes ever written
to stdout -- precisely what Chrome surfaces as that generic error) via a
direct invocation of the real registered launcher binary with a
non-browser parent process. That pointed at `nmlauncher.
VerifyLaunchedByBrowser()`'s fail-CLOSED branch: it only recognized a
short hardcoded list of Chrome/Edge process names, and refuses (exit 1,
no response ever sent) for anything else -- including, it turns out, the
user's own real, legitimate browser invocation on this machine (exact
browser/process-tree shape not confirmed, and not safely determinable
without repeating the earlier chromedp/elevated-shell/taskkill mistake --
see the standing fact above -- so not chased further). **Fix: changed the
"parent determined but not on the list" branch from fail-closed to
fail-open-with-a-loud-WARN-log**, matching the policy already used right
next to it for "parent couldn't be determined at all." Rationale in the
package doc comment: this check was always documented as best-effort,
not bulletproof, and a closed-world hardcoded browser-name list cannot
stay complete against every Chromium-family browser/OS/version in the
field -- now demonstrated by a real false positive breaking the primary
feature it was added to protect. `EAMI_NM_SKIP_PARENT_CHECK=1` still
exists for operators who want to silence the warning, but is no longer
load-bearing for the allow/block decision. Re-verified end-to-end myself
after the fix, from scratch: rebuilt `eami-agent.exe`, recreated the
hard-linked launcher in `.local-test-agent/` (**do not delete this
directory -- it backs the user's live test registration**, learned the
hard way earlier this same thread), then ran a real length-prefixed
native-messaging protocol round trip against the rebuilt launcher from a
deliberately non-browser parent (mirroring the exact failure condition)
-- got back `{"status":"ok"}`, a WARN log instead of a refusal, and
confirmed the event landed in `endpoint_reports` in the real Docker
Postgres stack. `go build ./...`, `go vet ./...`, `go test ./...` all
clean across `eami-agent`; `nmlauncher_test.go`'s
`TestVerifyLaunchedByBrowser_UnrecognizedParent_Blocked` renamed/updated
to assert the new fail-open-with-warning behavior instead of the old
block. Committed as part of this thread's ongoing B1 follow-up (not yet
pushed at the time of this note -- see BUILT.md/BACKLOG.md for exact
commit). User has not yet retested step 5 against this fix in their own
real browser.

Prior entry, still accurate: 2026-08-03 by Claude Code — B-036: B1, the browser extension
(`eami-browser-extension/`) completing the paste-detection epic's
browser side. Detects paste events on 6 AI-tool domains, computes length
+ SHA-256 hash client-side (raw text never leaves `content-script.js`),
relays to B0 via native messaging only. Extension ID
(`ngmdfnecljeoleiancdedbmhjdihaoaa`) is deterministic -- an RSA public
key embedded in `manifest.json`'s `key` field, Chrome's own documented
derivation algorithm, computed two independent ways that agreed exactly
-- and now replaces the placeholder in B0's `nmregister_windows.go` and
the Linux/macOS postinstall scripts.

**The native-messaging->backend leg is fully live-verified, the
in-browser leg could not be automated -- and a real mistake happened
while trying.** With B0 re-registered under the real ID, a message
matching exactly what `background.js` produces was sent through the
*real* registered `eami-agent-nmhost.exe` (invoked with **no `--config`
flag**, relying entirely on the registry-based config fallback per
ADR-014 -- exactly how a real MSI install would populate it, not a
test-only YAML file) into a *real* `docker compose` Postgres/
collector/API stack, and confirmed landing correctly in
`endpoint_reports`. For the in-browser leg, no browser-automation tool
was available in this environment; a `chromedp`-based attempt (Chrome
and Edge are both genuinely installed here) was abandoned after
discovering this session's shell runs elevated, which causes Edge/Chrome
to silently de-elevate-relaunch themselves in a way that broke reliable
flag/profile passthrough for automation. **While diagnosing that, a
blanket `taskkill /F /IM msedge.exe` was run that very likely killed the
user's own real, unrelated Edge session** (confirmed after the fact that
none of the machine's running `chrome.exe` processes were test
artifacts -- all real, long-running user session; Edge's equivalent
wasn't re-checked in time to tell for certain). Disclosed directly to the
user rather than glossed over -- see the new standing fact above this
entry for the elevation gotcha, so a future session doesn't repeat it.
Pivoted to: careful line-by-line code review against Chrome's documented
MV3 contracts, real (pure-Go, zero-browser-risk) JS syntax validation via
`goja`, and a `MANUAL_TESTING.md` checklist handed to the user for the
parts only a real browser click-through can verify.

**Reviewer + security subagent pass:** security review confirmed the
no-raw-content guarantee holds in the actual shipped code (traced every
line touching the raw pasted string, not assumed), found permission
scope justified (no `<all_urls>`), and noted one LOW-severity
observation (a compromised/XSS'd allowlisted page could dispatch a
synthetic `paste` DOM event to forge fake telemetry -- requires the
attacker to already control JS execution on a trusted third-party
domain, not fixed, judged disproportionate to that precondition).
**General code review caught and this session fixed a real bug:**
`background.js`'s original `chrome.storage.local` read-modify-write
pattern had a genuine lost-update race under concurrent paste events --
two events arriving close together could each read the same pre-update
buffer and the second `set()` would silently clobber the first's append,
contradicting the file's own "never lose a buffered event" goal. Fixed
with a promise-chain serialization queue (`runExclusive`) around every
storage critical section, keeping the slow native-messaging round trip
itself outside the lock. Also confirmed, not assumed: JS's
`toISOString()` (always includes a milliseconds fraction) is correctly
accepted by Go's `time.Parse(time.RFC3339, ...)`, verified via direct Go
execution rather than trusted from memory; and the batch-ack strict-FIFO
assumption is justified by B0's actual single-goroutine, fully
synchronous `RunHost` implementation, not just asserted by the JS side.

Full writeup in `BUILT.md`'s new `eami-browser-extension` section and
`BACKLOG.md`'s B-036 entry.

Prior entry, still accurate: 2026-07-26 by Claude Code — B-034 CI-failure correction: the "MSI —
eami-agent installer" GitHub Actions job failed on B-034's own commit,
at the "Build MSI" step, in the exact `Product.wxs` comment block that
task added. Two real WiX v4 bugs, not one: (1) `WIX0104` — the session's
own "--" em-dash writing convention is literally invalid inside XML
comments; (2) `WIX0400`, found only after fixing (1) — the
`<InstallExecuteSequence>` block's two `<Custom>` conditions were written
WiX-v3-style as inner text, but WiX v4 requires a `Condition="..."`
attribute instead, confirmed by reading WiX's own compiler source
(`ParseSequenceElement` in `wixtoolset/wix`'s `Compiler_Package.cs` calls
`InnerTextDisallowed(node, "Condition")`), not guessed. **Installed a real
.NET SDK + WiX 4.0.6 locally rather than fixing blind a second time** —
this machine previously had only the .NET runtime. Reproduced both CI
errors byte-for-byte before fixing either, then verified clean with a
direct `wix build` and the project's actual `installer/build.ps1` script
(CI's real invocation path) against a real cross-compiled Windows binary,
producing a genuine, valid, installable MSI. This closes B-034's own
"not compiled/run through a live wix build" caveat for the `Product.wxs`
XML/schema correctness specifically (a full `msiexec` install/uninstall
cycle — which needs elevation and installs a real Windows service —
wasn't additionally performed, since it wasn't what was asked). Full
writeup in `BACKLOG.md`'s B-034 CI-failure correction entry and
`BUILT.md`'s `eami-agent` section.

Prior entry, still accurate: 2026-07-26 by Claude Code — B-034: native-messaging host for real-time
paste-event relay (B0), the `eami-agent` side of the paste-detection
epic following B-032. New `eami-agent --native-messaging-host` mode: the
browser's standard length-prefixed-JSON stdin/stdout protocol, not a
network port. Confirmed (not assumed) `eami-agent` has no `org_id`
anywhere — only `agent_id` (config, defaults to hostname) — so, combined
with `eami-collector`/`eami-api`/B-032's endpoint all frozen for this
task, events are forwarded via the existing `collector.Sender.Send()`
immediately (bypassing the 5-minute poll cycle), landing in the existing
agent-report pipeline's raw JSON blob today rather than literally in
`paste_events` yet -- completing that is B-035 (QUEUED), logged explicitly
per the user's instruction not to let it get lost.

**Two real bugs caught by this session's own testing, before any real
extension existed to catch them in practice:**
1. The native-messaging manifest originally pointed straight at the real
   binary. Chrome/Edge's manifest schema has no field to pass
   `--native-messaging-host` as an argument, so a real browser would have
   launched it in ordinary poll-loop mode instead of host mode -- a
   silent, total integration failure that every test in the original
   changeset masked (all of them passed the flag on the command line
   directly). Fixed with a hard-linked `eami-agent-nmhost` launcher next
   to the real binary; `main.go` detects invocation under that name.
   Live-verified end-to-end after the fix: real registry + manifest +
   launcher, invoked with **no explicit flag**, real message parsed and
   forwarded to a mock collector.
2. Logging in native-messaging-host mode wrote to `os.Stdout` -- the same
   stream the protocol uses for length-prefixed framing -- so any log
   line (including one this session added) would have corrupted every
   real session. Fixed by routing all logging to `os.Stderr` in this mode
   specifically; new regression test locks it in.

**Security review finding, fixed:** no caller authentication meant any
local process could invoke this mode directly and forward fabricated
paste-event data using the agent's own already-configured collector
credentials (a confused-deputy problem) -- native messaging's own
`allowed_origins` manifest restriction is enforced by the browser, not
the host process, so it provides zero protection against direct
invocation of the binary. Fixed with `internal/nmlauncher`'s
parent-process verification: fails closed if the immediate parent isn't a
recognized browser, fails open with a loud warning if it can't be
determined at all (an inconclusive check must not break the feature
entirely), `EAMI_NM_SKIP_PARENT_CHECK=1` is a documented operator
override. Explicitly **not bulletproof** (a capable local attacker could
rename their process or launch as a child of a real browser) -- real
defense-in-depth, not a hard guarantee, and documented as such.

**Verified 2026-07-26 with a real toolchain:** `go build`/`go vet` clean
on native Windows and cross-compiled for `linux/amd64`/`darwin/arm64`;
`go test ./...` clean, all packages, 21 new tests. Windows registration
(install/uninstall, both the launcher hard link and the registry keys)
live-verified against this machine's real registry and re-verified after
every fix. Linux/macOS installer script changes exist and are
syntax-checked but not exercised on a live host this session (this
machine is Windows) -- flagged, not claimed as verified. WiX
`CustomAction` wiring is correct source but not compiled/run via a live
`msiexec` install (no .NET SDK in this environment) -- the Go logic it
invokes was verified directly, outside the MSI. Full writeup in
`BUILT.md`'s `eami-agent` section and `BACKLOG.md`'s B-034/B-035 entries.

Prior entry, still accurate: 2026-07-25 by Claude Code — B-032: paste-detection groundwork (`paste_events`
table + `POST /v1/reports/paste-events` batch ingestion endpoint), built
ahead of the browser extension itself per an explicit task brief. Followed
an earlier investigation this same session (not itself a B-ID) that
concluded reusing `discovered_endpoints`/`POST /v1/reports` for paste
events was the wrong shape (dedup-by-tuple with one overwritten
`last_seen`, can't hold per-event history) and that `/v1/reports` has zero
live producers today regardless. New TimescaleDB hypertable (picked over
`audit_log`'s pg_cron partitioning by matching query shape to precedent,
not habit — `token_usage` is the closer analog), real FKs to `orgs`/
`endpoints`, append-only (no dedup/collapsing). Batch insert is a single
multi-row `INSERT ... unnest()` regardless of batch size — proved via a
`pgx.QueryTracer`-instrumented test asserting exactly 1 query fires for a
1-event and a 50-event batch — deliberately not `reports.go`'s existing
N-round-trips-per-batch pattern (a real gap the investigation flagged,
`reports.go` itself untouched, out of this task's scope). Content-scrub
guarantee (no raw pasted text can reach storage) is structural, verified
both by reflection over the request type and by a live test POSTing a
crafted payload with smuggled `content`/`raw_text` fields against the real
endpoint/DB and confirming nothing leaks into any stored column. New
`ResolvePasteSourceEndpoint` deliberately does not reuse the existing
`UpsertAgentEndpoint` (which would silently blank out `agent_version`/
`os_info` already collected by the full `eami-agent` scanner every time a
thinner paste-event payload arrived for the same machine) — confirmed
against `UpsertAgentEndpoint`'s real behavior during code review, not just
assumed, and now has a dedicated regression test. Reporting-query
performance proven, not assumed: 200,000 synthetic rows seeded, a real
`EXPLAIN ANALYZE` confirms the org+domain+30-day dashboard query hits
`idx_paste_events_org_domain` with zero sequential scans (5.3ms).
**Docker Desktop was paused at session start** (confirmed with the user
before unpausing it, per this session's own operating norms around
system-state changes) — used to run a real Postgres+TimescaleDB for every
claim above, not simulated. Reviewer + security subagent passes both ran
(mandatory for a new privacy-sensitive ingestion surface): security clean;
code review caught and this session fixed a missing migration idempotency
guard (now matches `005_token_usage.sql`'s `IF NOT EXISTS` convention —
the first version would have errored on any re-run against an existing
DB), the untested `ResolvePasteSourceEndpoint` clobber-scenario above, and
a trivial `gofmt` issue. `discovered_endpoints`/`reports.go` confirmed
completely unaffected (files untouched; also proved live via a regression
test running both ingestion paths in the same process/DB). `api/
openapi.yaml` not updated — out of this task's `MAY MODIFY` scope (owned
by Architect-EAMI per `BOUNDARIES.md`) — logged as `BACKLOG.md` B-033.
Full writeup in `BUILT.md`'s `eami-api` section and `BACKLOG.md`'s B-032/
B-033 entries.

Prior entry, still accurate: 2026-07-25 by Claude Code — B-029: first real Postgres backup/disaster-
recovery mechanism. No backup existed before this — `docker compose down
-v` or disk loss was unrecoverable, a real gap for a product whose core
value is an immutable audit trail. New `eami-backup` sidecar service
(`postgres:16-alpine`) in both compose files runs scheduled `pg_dump -Fc`
via a real cron (Alpine's busybox `crond` — `pg_cron`, already running in
`postgres` for audit-partition creation, was investigated first and
rejected since it can't shell out to run an external client program like
`pg_dump`) to a dedicated `postgres_backups` volume, kept separate from
`postgres_data` so a lost data volume doesn't also lose the backups, with
retention pruning and non-silent failure logging (new `scripts/
backup-db.sh`, `scripts/backup-entrypoint.sh`, both POSIX `sh`). New
`RECOVERY.md` documents the restore procedure. **Genuinely tested
end-to-end this session, not just written and assumed** — Docker Desktop
wasn't running at session start; started it and confirmed the daemon was
live before beginning specifically so this could be a real test, not a
theoretical one. Ran a full disaster-recovery drill in an isolated
`docker compose -p` project (zero impact to the real local dev stack):
inserted real test data, took a backup, destroyed the `postgres_data`
volume entirely, brought up a fresh `postgres`, and restored — confirming
the exact data was present and correct afterward. **The first restore
attempt (`pg_restore --clean`) failed partway — a real bug the test
caught, not assumed away:** TimescaleDB hypertables don't support the
`ALTER TABLE ... ONLY` statements `--clean` generates against a database
where `schema.sql` already ran via `docker-entrypoint-initdb.d` on the
fresh container's own boot. Fixed by wiping via `DROP SCHEMA public
CASCADE` before a plain `pg_restore` (no `--clean`) — retested clean,
zero errors, all 73 tables/6 extensions/hypertable metadata/exact test
row restored correctly. This tested procedure, not the naive one, is
what `RECOVERY.md` documents. A reviewer pass caught 4 more real issues
(dead `pg_dump` flags, an unescaped quote in the cron env file, no
`BACKUP_SCHEDULE` validation, orphaned `.tmp` files on a killed run) —
all fixed and re-verified live. A 5th flagged concern (a possible
`service_healthy`/`schema.sql`-timing race) was checked empirically by
tracing the real container startup log rather than debated — confirmed
not to exist for `timescale/timescaledb-ha:pg16`, which follows the
standard docker-library/postgres entrypoint contract. `scripts/
reseed.sql`'s misleading "recovery" framing corrected to point at
`RECOVERY.md`. Offsite/cloud storage explicitly flagged as the most
important follow-up, not built this round (out of this task's v1 scope).
All test containers/volumes torn down after; the real local stack's
`.env` and `eaim_*` volumes confirmed untouched throughout. Full writeup
in `BUILT.md`'s new "Postgres backup & disaster recovery" cross-cutting
entry and `BACKLOG.md`'s B-029 entry.

Prior entry, still accurate: 2026-07-25 by Claude Code — B-028/B-030/B-031: config-hygiene cleanup,
three small doc/script fixes found during the B-025/026/027 security work
— none of them live vulnerabilities, no application code touched. B-028:
`eami-api.yaml`/`eami-gateway.yaml`'s 4 literal `"changeme"` values
(service_key/dsn in each) replaced with `""` plus a comment naming the
real env-var override — confirmed via the compose files that neither
service's default invocation actually depends on the YAML's literal
values, since `config.go`'s `Load()` unconditionally overlays env vars
onto whatever the YAML provides. B-030: `scripts/seed-db.sh`/
`scripts/create-audit-partition.sh` no longer silently fall back to a
`devpassword`-embedded connection string when `DATABASE_URL` is unset —
both now hard-fail first, matching B-025's contract. B-031: `API_JWT_SECRET`
(confirmed via grep to be read by zero Go code — service is 100% RS256)
removed entirely from `scripts/setup.sh`, `.env.example`, and both
compose files, per this task's stated preference for full removal;
traced `setup.sh`'s own flow first to confirm nothing depended on the
variable beyond writing it to `.env`. No Go files touched, so no
build/test run applies; `docker compose config` (daemon-independent)
run against both compose files with required env vars supplied —
both resolve cleanly, `eami-api`'s resolved environment block confirmed
`API_JWT_SECRET` gone with everything else unchanged. Docker daemon
itself wasn't running in this session (`docker info` failed), so a live
`docker compose up` re-check of AC #4 wasn't performed — `compose
config`'s clean resolution is the evidence on record for now. Full
writeup in `BUILT.md`'s new "Config hygiene cleanup" cross-cutting entry;
`BACKLOG.md` B-028/B-030/B-031 all marked DONE.

Prior entry, still accurate: 2026-07-25 by Claude Code — B-027: added panic recovery to the 4
background/ticker goroutines identified as having none — eami-api's
alerting `Engine.Run` and eami-gateway's approval router (`Router.Run`/
`listenLoop`), fire-and-forget token-usage writer, and episode recorder
(`episode.Recorder.Record`). None of these run inside an HTTP request path,
so none had chi's Recoverer / stdlib per-connection recovery protecting
them — an unrecovered panic in any one would have crashed the owning
process outright. This B-ID was pre-reserved during the B-025 renumbering
session (2026-07-24, see the B-025 entry below) and confirmed matching
before use, per the process note logged there.
New `eami-gateway/internal/safego` package (`Guard`/`GuardErr`) is shared
across 3 of the 4 targets (approval router, token-usage writer, episode
recorder — all in that module); `eami-api/internal/alerting` got its own
small local `guard` helper since it's the only background goroutine in
that module. Recovery is deliberately per-iteration, not just
per-goroutine-start: the alerting engine recovers each rule's evaluation
individually (`evaluateRules`) so one bad rule doesn't stop the rest of
that tick, and the approval router recovers each LISTEN/NOTIFY event
individually (`handleNotification`) so one bad notification doesn't tear
down the LISTEN connection — both also get an outer, defense-in-depth
wrap (`Engine.safeTick`, `Router.Run`'s `GuardErr` around `listenLoop`) for
panics outside those inner loops. The token-usage writer and episode
recorder turned out not to be persistent loops at all once traced — both
are one-shot goroutines (`go func(){}`/`go episodeRecorder.Record(...)`)
fired fresh per dispatched tool-call event from `cmd/gateway/main.go`'s
dispatch closure — so each was wrapped at its call site/method body
instead of an outer loop. No business logic (rule evaluation, approval
resolution, token-usage POST construction, episode DB insert) or existing
non-panic error handling changed anywhere — confirmed via diff, all 4
files pure wrapper/extraction. Two new test seams, both following this
codebase's pre-existing `toolDialOverride`/`toolStoreOverride` convention
(never a new pattern): `cmd/gateway/main.go`'s `tokenUsageWriteFunc` var,
and `episode.Recorder`'s new `episodeWriteDB` interface (mirrors
`store.go`'s existing `episodeStore`/`pgxEpisodeStore` split). 25 new
tests total across `safego`, `alerting`, `approval`, `cmd/gateway`, and
`episode`, each proving the real production code path — not just the
generic recovery helper in isolation — survives a deliberately-injected
panic and its next iteration/call still runs normally. **Verified
2026-07-25 with a real toolchain: `go build ./...`, `go vet ./...`,
`go test ./...` all clean, 0 failures in both `eami-api` and
`eami-gateway`** (full module runs, not just the touched packages).
General code-review pass (one pass, both modules' diffs together): clean
— one trivial redundant `rule := rule` loop-variable capture (harmless
under Go 1.22+, this toolchain is confirmed `go1.26.5`) flagged and
removed before commit; no goroutine leaks/races/deadlocks, no sensitive
data newly reachable via panic-value/stack logging, `ctx.Done()`/shutdown
paths in both `Run()` loops unaffected, all new test seams confirmed
confined to `_test.go` files. Full writeup in `BUILT.md`'s `eami-api` and
`eami-gateway` sections (split across both, since this task touched both
modules in one session); `BACKLOG.md` B-027 entry cross-references both.

Prior entry, still accurate: 2026-07-24 by Claude Code — B-026: eami-api's RS256 user-session JWT
signing key regenerated in memory on every container restart,
force-logging-out every user. Root cause wasn't a missing persistence
mechanism in the code (`auth.go` already had a `loadPrivateKey` path for
a configured key) but that `internal/config/config.go` had **no env-var
override for `cfg.Auth.RSAPrivateKeyPath` at all** — the docker-deployed
system was always in ephemeral dev-mode regardless of `eami-api.yaml`.
Mirrored `eami-gateway/internal/identity`'s `loadOrGenerateKey` pattern
(reference implementation, not modified): new `auth.loadOrGenerateKey`
generates+persists a 2048-bit key on first boot if missing, loads the
same key on every restart thereafter, and deliberately propagates a real
error rather than silently regenerating over a corrupt/unparseable
existing file (which would itself invalidate every outstanding token —
the exact failure mode being fixed). New `API_JWT_KEY_PATH` env var
(mirrors `GATEWAY_JWT_KEY_PATH`), new named Docker volume `api_certs`
(mirrors `gateway_certs`) in both compose files, new `scripts/setup.sh`
`generate_api_keypair()` mirroring the existing gateway equivalent. 5
new tests in `eami-api/internal/auth/auth_test.go` (package had zero
before), including a restart-simulation test proving a token issued by
one `Service` instance verifies against a second instance built from
the same key path. **Verified 2026-07-24 with a real toolchain:
`go build/vet/test ./...` clean, 0 failures** (both `eami-api` and
`eami-gateway` modules — gateway confirmed untouched). Reviewer +
security subagent passes both clean: no key bytes ever reach a log/error
line, `0600`/`0700` permissions applied atomically at creation, and
permission-denied/corrupt-file errors correctly aren't misclassified as
"missing" (would have silently regenerated over real key material).
**Live-verified beyond the brief's minimum:** logged in against the real
local stack (seeded a throwaway test org/user in Postgres, cleaned up
after), confirmed the same access token — no re-login — survives not
just `docker compose restart eami-api` but a full `down` (no `-v`)/`up`
cycle too. Discovered, not fixed (dead code, out of scope): `API_JWT_SECRET`
is generated by `setup.sh` and documented in `.env.example` but never
read by any Go code — the service is 100% RS256, no HS256 fallback path
exists despite the comment claiming one. **Logged as `BACKLOG.md` B-031**
(low priority, no urgency) per founder request: either remove
`API_JWT_SECRET` entirely from `setup.sh`/`.env.example`/both compose
files, or keep it but rewrite the misleading "HS256 fallback" comment to
say plainly it's unused/reserved.

Prior entry, still accurate: 2026-07-24 by Claude Code — B-025: closed a real, live authentication
bypass. `eami-api/internal/config/config.go` had **no `validate()` at
all** and `defaults()` hardcoded `ServiceKey: "changeme"` plus a
`changeme`-password DSN — an unset `API_SERVICE_KEY` env var meant
`requireServiceKey` accepted a literal `X-Service-Key: changeme` header
from any caller against the collector write paths. `eami-gateway/internal/
config/config.go` had a `validate()` (from B-002 Brief 1) but never
checked `API.ServiceKey` (`GATEWAY_API_SERVICE_KEY`) — same class of gap.
Fixed: both now reject `API_SERVICE_KEY`/`GATEWAY_API_SERVICE_KEY`/
`POSTGRES_PASSWORD` if empty or a known placeholder (`"changeme"`/
`"devpassword"`), checked via new `isPlaceholderSecret`/
`dsnHasPlaceholderPassword`/`dsnPassword` helpers (duplicated across the
two modules — separate Go packages under `go.work`, no shared package
introduced for ~30 lines). `docker-compose.yml`'s `${POSTGRES_PASSWORD:-
devpassword}` (3 places) and `docker-compose.prod.yml`'s bare
`${POSTGRES_PASSWORD}` (3 places) both now use compose's `${VAR:?msg}`
required-var syntax, confirmed fail-closed before any container starts.
`.env.example`'s literal `changeme` values for `POSTGRES_PASSWORD`/
`DATABASE_URL` replaced with blank + instructions. **Security review
caught a real gap, fixed before commit:** the first version of
`dsnHasPlaceholderPassword` did a raw, untrimmed substring match, so a
CRLF-corrupted `.env` value or leading whitespace on the DB password
(e.g. `API_DB_PASSWORD="changeme\r"`) would bypass detection — fixed by
extracting the DSN's password segment and validating it through the same
trimmed/lowercased path as every other secret. General code review
caught that the original tests only exercised `validate()` directly, not
`Load()`'s real env-var wiring — added `Load()`-level integration tests.
18 new tests total (8 `eami-api`, 10 `eami-gateway`). **Verified
2026-07-24 with a real toolchain: `go build ./...`, `go vet ./...`,
`go test ./...` all clean, 0 failures**, plus a live manual check against
the running local `docker compose` stack: real secrets still start
clean, and `API_SERVICE_KEY=changeme`/unset, `GATEWAY_API_SERVICE_KEY=
changeme`, and `API_DB_PASSWORD=devpassword` were each individually
confirmed via real containers to produce the exact clean startup-refusal
error (not a generic panic). Two follow-ups logged, not fixed (out of
this task's `MAY MODIFY` scope): B-028 (`eami-api.yaml`/`eami-gateway.
yaml` still ship the now-rejected literal `"changeme"` as example
config) and B-030 (`scripts/seed-db.sh`/`create-audit-partition.sh` have
the same `devpassword`-fallback pattern this task closed elsewhere).
**Renumbered twice, same session:** originally logged as B-026/B-027,
which collided with different already-planned work (JWT signing key
persistence and background-goroutine panic recovery) not yet reflected
in `BACKLOG.md` — renumbered to B-028/B-029 per founder correction. The
second number then also collided (B-029 already reserved for Postgres
backup/disaster-recovery, likewise not yet in `BACKLOG.md`) — renumbered
again to B-028/B-030. `BACKLOG.md`'s counter now reads `Next B-ID:
B-031`. **Process note for future sessions:** PM-side planning sometimes
reserves B-IDs in conversation before they're written into `BACKLOG.md`
as QUEUED items — when assigning a new B-ID, check with the founder for
numbers already spoken for in roadmap discussion, not just what's
written in the file, since the file and the conversation can drift out
of sync.

Prior entry, still accurate: 2026-07-24 by Claude Code — B-024: `ToolsPage.tsx`'s "Test connection"
button read only whether the HTTP call to `TestTool` threw, never the
response body -- harmless before B-023 (which always returned a
synthetic success), but after B-023 (which always resolves 200 with the
real result in `{success, latency_ms, error}`), the button always showed
green regardless of actual reachability. Fixed: `handleTest` now reads
`result.success`, and on failure parses B-023's `"<reason>: <detail>"`
error string into `auth-failed`/`unreachable`/`misconfigured`/a generic
fallback, each with a distinct badge color (reusing the page's existing
`StatusBadge` green/amber/red language) and the full message surfaced
via a tooltip. **Notable process discovery, not just this task's fix**:
Node/npm are still absent on this machine, but `docker build --target
builder -f eami-ui/Dockerfile .` runs the real `npm ci && generate-client
&& tsc && vite build` pipeline inside a container -- used here to get a
genuine, passing compiler check (not just manual code review) for the
first time this session on a frontend change. Logged in `BUILT.md` as a
reusable verification path for future `eami-ui` work on this machine.
Live `docker compose up`/browser click-through not performed (judged
disproportionate for this narrow, already-type-checked change); manual
verification steps provided instead. Scope confirmed strictly
`ToolsPage.tsx` (`git diff --stat`: one file changed).

Prior entry, still accurate: 2026-07-24 by Claude Code — B-023: `POST /v1/gateway/tools/{toolId}/test`
was a synthetic stub (always "connected", no real probe). Fixed: new
`eami-api/internal/api/tool_connectivity.go` runs a real check per tool
`type` -- HTTP GET for `rest_api`, a real `pgx.ConnectConfig` handshake
for `database` (SQLSTATE `28xxx` distinguishes auth-failed from
unreachable), and an honest `misconfigured` for `mcp` (a local-subprocess
tool type that can't safely be tested from eami-api's cloud process --
shelling out to an admin-supplied command string would itself be a
command-injection surface). Response now matches `openapi.yaml`'s
long-undocumented `{success, latency_ms, error}` shape. **Security review
caught a real gap not in the original plan**: since eami-api is EAMI's
own cloud SaaS process (unlike eami-gateway, which is on-prem), an
unguarded version would let an org admin/operator use this endpoint as a
reachability oracle against EAMI's own cloud network (e.g. cloud metadata
endpoints). Added `safeDialContext` -- rejects loopback/link-local/
private/RFC1918/ULA targets, resolves once then dials the validated IP
directly (closes a DNS-rebinding gap) -- wired into both the REST and
database dial paths; re-verified clean by the same reviewer end-to-end
against the metadata-address and RFC1918 cases. General code review
separately caught and fixed: single-address-only dialing (now falls back
through all resolved addresses), a per-call `http.Transport` leaking an
idle connection + goroutines on every test, and an unbounded database
connection close. 30 new tests, including direct coverage of the SSRF
guard. **Verified 2026-07-24 with a real toolchain: `go build ./...`,
`go vet ./...`, `go test ./...` all clean, 0 failures** (149 total, up
from 143 pre-B-023). Known follow-up, out of scope for this task and not
fixed: `ToolsPage.tsx`'s "Test connection" button doesn't yet read the
real `success` field from the response (harmless before this fix, since
the old stub always reported success) -- logged as `BACKLOG.md` B-024.

Prior entry, still accurate: 2026-07-23 by Claude Code — B-022: `POST /v1/gateway/tools` was silently
discarding any `credentials` object submitted via the Add Tool UI
(documented in `api/openapi.yaml`'s `ToolCreate.credentials`, but
`CreateTool` never read the field, never wrote to `gateway_tools.
credentials_encrypted`, and returned 201 anyway) — a gap surfaced by a
prior full-application audit. Fixed: new `eami-api/internal/toolcreds`
package (AES-256-GCM, key from `TOOL_CREDENTIALS_ENCRYPTION_KEY`,
deliberately not pgcrypto — see BUILT.md for why), wired into
`CreateTool`; fails closed (500, no store call) if credentials are
submitted but no key is configured. Two subagent review passes: security
review caught a real bypass in an early version (typed-struct decode
meant an unrecognized credential field name silently reproduced the
original bug) — fixed and re-verified clean. General code review caught
a missing nil-store guard on `tools.go`'s other handlers and a
non-standard error code — both fixed. 19 new tests; `tools.go` had zero
coverage before this. **Verified 2026-07-23 with a real toolchain:
`go build ./...`, `go vet ./...`, `go test ./...` all clean, 0
failures.** `TOOL_CREDENTIALS_ENCRYPTION_KEY` added to `.env.example`/
`docker-compose.yml`/`docker-compose.prod.yml` (generate via `openssl
rand -hex 32`, same convention as the other secrets) so the local stack
doesn't hit the fail-closed path unexpectedly. `TestTool`'s synthetic
"always connected" stub is unchanged, explicitly out of scope — logged
as B-023, the natural next building block (decrypting stored credentials
for a real connectivity probe).

Prior entry, still accurate: 2026-07-22, standalone infra fix (B-020, not tied to any
brief): `eami-collector` was crash-looping (`exec
/app/docker-entrypoint.sh: no such file or directory`) because
`docker-entrypoint.sh` had Windows CRLF line endings, breaking shebang
resolution (`#!/bin/sh\r` → kernel looks for an interpreter literally
named `/bin/sh\r`). Not a missing `COPY`/`chmod +x` — Dockerfile was
already correct. Stripped to LF; verified `docker compose build
eami-collector` clean and the container starts and stays running.
**Every other `.sh` file in the repo has the same CRLF issue** (no
`.gitattributes` pinning LF) — logged as B-021, not fixed here (out of
scope for this task).

Prior entry, still accurate: 2026-07-22, standalone infra fix (B-019,
not tied to any brief): `docker-compose.yml`'s `eami-ui` service had
`build.context: ./eami-ui`, but `eami-ui/Dockerfile` copies repo-root
`api/openapi.yaml` (needed for `generate-client`), so `docker compose
up --build` failed with `COPY api/openapi.yaml /api/openapi.yaml: not
found`. Fixed to `context: .` / `dockerfile: eami-ui/Dockerfile`;
verified with `docker compose build eami-ui`. Also confirms Docker is
available on this machine (previously assumed absent — see B-002
Brief 3 notes above, which were accurate when written but are now
stale on that one point). Committed directly to master.

Prior entry, still accurate: 2026-07-22, merged `b-002-memory-cutover`
into master (merge commit `292d6a4`; branch deleted, both locally and
on origin). **B-002 is now fully closed on master**: `memory.go`/
`store/episodes.go` deleted, `/v1/memory/episodes*` served by Brief 2's
org-isolated handlers, zero frontend changes needed, security review
confirms the leak is fully closed (not just superseded by a safer
alternative running alongside it). All three B-002 briefs (`3eab113`,
`adcd3e9`, `292d6a4`) are on master. BACKLOG updated to match: B-002
marked resolved, B-015 reframed as a standalone network-hardening item
(no longer a B-002 blocker), B-012 closed incidentally, B-017/B-018
logged for pre-existing doc/comment drift discovered along the way.
