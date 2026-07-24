# BACKLOG.md — EAMI

Generated 2026-07-21 during bootstrap (B-001). See `BUILT.md` for the evidence behind each item.

## NEXT
_(empty — founder/PM assigns from QUEUED)_

## QUEUED

### B-002 — Resolve ADR-019 vs. ADR-010 conflict: episode content in the SaaS API — **RESOLVED, fully closed 2026-07-22**
**Objective:** `eami-api`'s memory endpoints stop violating (or are explicitly granted an exception to) the data-sovereignty rule in ADR-010.
**Resolution (2026-07-21):** full episode content stays on-prem; `eami-api` never serves it directly. Implementation split into 3 briefs — all done, all merged.
- [x] **Brief 1 — DONE, merged to master** (`b-002-gateway-episode-endpoint`, merge commit `3eab113`): `eami-gateway` gets a new dual-auth read endpoint (`GET /v1/gateway/episodes`, `/search`, `/{id}`) serving full episode content from its own on-prem Postgres. Dedicated secret (`GATEWAY_EPISODE_READ_SERVICE_KEY`), full unit test coverage including the security-critical forged-org_id and cross-org-404 cases. Reviewer + security subagent passes: clean. **Verified 2026-07-22 with a real toolchain: `go build ./...` and `go test ./... -v` both clean, 0 failures (18/18 new tests).**
- [x] **Brief 2 — DONE, merged to master** (`b-002-eami-api-proxy-layer`, merge commit `adcd3e9`): `eami-api` proxy layer (`internal/api/gateway_episodes.go`) forwarding UI requests to Brief 1's endpoint. The hard requirement is satisfied: `org_id` sent to the gateway is always the authenticated caller's own session org (`claimsFromContext(r).OrgID`), never client input — an optional `org_id` query param is accepted only as a tamper-check that 403s on mismatch before the gateway is ever called (structurally impossible for a forged org to reach the gateway, not just checked-and-rejected). Purely additive: `memory.go` had zero lines changed at this point. 11 tests including the centerpiece `TestGatewayEpisodes_List_MismatchedOrgIDSupplied_Returns403_GatewayNeverCalled`. **Verified 2026-07-22: `go build ./...`, `go vet ./...`, `go test ./...` all clean, 0 failures.** Reviewer + security subagent passes: clean.
- [x] **Brief 3 — DONE, merged to master** (`b-002-memory-cutover`, merge commit `292d6a4`): `eami-api/internal/api/memory.go` **deleted entirely**, along with `eami-api/internal/store/episodes.go` (the direct, unprotected `episodes` table query — confirmed zero other callers before removal). `/v1/memory/episodes` and `/v1/memory/episodes/search` (the actual frontend-facing, `api/openapi.yaml`-documented routes `MemoryPage.tsx` calls) now point at Brief 2's already-secure handlers directly — same functions, new mount, zero duplicated logic. Added `GET /v1/memory/episodes/{episodeId}`, documented in `openapi.yaml` but never implemented before now. `MemoryPage.tsx` needed **zero changes** — response shapes verified byte-identical to the old ones. Security review: **leak confirmed fully closed**, not just a safer alternative added alongside — re-verified the org-isolation chain from `jwtMiddleware` through `checkOrgID` at the new mount points specifically, not assumed to carry over from Brief 2's review. 8 new tests in `memory_test.go`, reusing Brief 2's test fixtures with zero duplication; `gateway_episodes_test.go` itself has zero diff. **Verified 2026-07-22: `go build ./...`, `go vet ./...`, `go test ./...` all clean, 0 failures.** Frontend build/lint/typecheck **not run** — Node/npm confirmed genuinely absent from this machine (not just off-PATH like Go was), so `MemoryPage.tsx`'s correctness rests on manual shape-verification, not a compiler/test run. AC #2's "manually verified via docker compose up" **not performed** — no Docker in this environment; flagged before building, not discovered after.
- [x] `DECISIONS.md` ADR-019 updated to Accepted with the resolution (2026-07-22, formalized as a full entry replacing its own Pending row — same number, not renumbered).
**Dependencies:** none — all three briefs done and merged to `master` (`3eab113`, `adcd3e9`, `292d6a4`).
**Severity:** was High (shipped code contradicting an accepted ADR); now resolved — **there is exactly one path to full episode content, and it enforces org isolation.**
**⚠️ B-015 stays open** — Brief 1's gateway endpoint itself still enforces nothing on its own if reached directly, bypassing eami-api. Not related to this closure; separate item.

### B-017 — `EpisodeStep` schema in `api/openapi.yaml` doesn't match real step JSON
**Objective:** Either fix the documented schema or the runtime shape so they agree — currently neither has ever matched the other.
**Context:** Discovered while verifying B-002 Brief 3 (2026-07-22). `openapi.yaml`'s `EpisodeStep` schema documents `step_number`, `tool`, `action`, `reasoning`, `decision`, `token_in`, `token_out`. The real runtime shape (`eami-gateway/internal/episode/recorder.go`'s `Step` struct, what actually gets written to and read from the `episodes.steps` JSONB column) uses `tool_name`, `action`, `params`, `result`, `decision`, `timestamp`. Only `action`/`decision` line up. Predates this branch entirely — same raw-JSONB passthrough existed in the now-deleted `memory.go`/`store/episodes.go` too — not introduced by any B-002 brief, just newly visible now that `GET /v1/memory/episodes/{episodeId}` is the first real, working route where this exact documented schema is checkable against actual output.
**Acceptance criteria:**
- [ ] Decide which is authoritative (real shape vs. documented shape) — likely the real shape, since it's what's actually recorded and consumed
- [ ] Update `openapi.yaml`'s `EpisodeStep` schema to match (Architect-EAMI's file, not Code's to silently change)
**Dependencies:** none. Owner: Architect-EAMI (per `BOUNDARIES.md`, `openapi.yaml` changes are theirs).

### B-018 — Stale comment in `eami-gateway/internal/episode/store.go` references a deleted type
**Objective:** Fix a doc-comment that now points at nothing.
**Context:** `eami-gateway/internal/episode/store.go:17`'s comment says its `Episode` struct's fields "intentionally match `eami-api/internal/store.Episode` 1:1" — that type was deleted in B-002 Brief 3. `eami-gateway` is frozen for B-002 (out of scope for that effort), so not fixed there. Purely cosmetic — no functional impact, `eami-gateway`'s `Episode` struct is unaffected and still correct on its own.
**Acceptance criteria:**
- [ ] Update the comment to stop referencing the deleted type (e.g. point at `eami-api/internal/api.GatewayEpisode`, the shape it should now be compared against, if a comparison is still useful)
**Dependencies:** none. Trivial/cosmetic.

### B-003 — Approval flow integration/e2e test
**Objective:** An automated test proves the full escalate → Slack → UI decide → resume/deny loop works, closing `tasks/TASK-044` which was never delivered.
**Acceptance criteria:**
- [ ] Integration test exercises: policy ESCALATE → approval_request created → (mocked) Slack notify → decide API call → gateway resumes or errors correctly
- [ ] Test lives under `eami-gateway` or `eami-api` `*_test.go` per existing convention
**Dependencies:** none.

### B-004 — Run the gateway load test for real
**Objective:** `tasks/TASK-050`'s k6 script produces actual numbers instead of all-PENDING thresholds (was blocked — "stack not running").
**Acceptance criteria:**
- [ ] Stack running via `docker compose up`, k6 script executed against it
- [ ] `tasks/TASK-050-results.md` thresholds table filled with real p95/p99/error-rate/memory numbers
- [ ] Any threshold miss filed as its own backlog item
**Dependencies:** a machine that can run the full docker-compose stack (this bootstrap machine cannot — no Docker/Go/Node confirmed).

### B-005 — Test coverage for eami-collector's security boundary
**Objective:** `internal/api/ingest.go` (API-key validation, the collector's only auth boundary) and `internal/db`/`internal/models` get test coverage — currently 1 test file covers only `forwarder.go`.
**Acceptance criteria:**
- [ ] Tests for valid/invalid/missing API key on ingest
- [ ] Tests for malformed report payloads (schema validation path)
- [ ] `go test ./...` passes in eami-collector
**Dependencies:** none.

### B-006 — Stand up a frontend test suite
**Objective:** `eami-ui` has automated test coverage beyond `tsc --noEmit`. `BOUNDARIES.md` assigns `vitest.config.ts` + `playwright/` to QA-EAMI but neither exists — 0% UI test coverage today.
**Acceptance criteria:**
- [ ] vitest configured, at least the resource hooks (`src/hooks/*.ts`) unit-tested
- [ ] Playwright configured with at least one E2E smoke path (login → dashboard load)
- [ ] Wired into CI (`.github/workflows/test.yml` per `BOUNDARIES.md`, verify it exists first)
**Dependencies:** none.

### B-007 — Implement real semantic policy evaluation
**Objective:** `eami-policy/semantic.go` does real LLM-based intent evaluation instead of always returning no-match.
**Acceptance criteria:**
- [ ] Configurable LLM endpoint (local or API) per ADR-009's decision
- [ ] `TestEvaluate_SemanticRuleSkippedByStub` replaced/updated to test real matching behavior
- [ ] 2s timeout + ESCALATE-on-timeout default preserved (per ADR-009's accepted design)
**Dependencies:** ADR-009 (local vs. API LLM choice) — still open (blocking, see BLOCKED below).

### B-008 — Real episode embeddings + vector similarity search
**Objective:** Replace the SHA-256 placeholder embedding with real embeddings; wire episode search (`eami-api`'s `SearchGatewayEpisodes` → `eami-gateway`'s `SearchEpisodes`, post-B-002 the only search path — `memory.go`'s old `SearchMemoryEpisodes` no longer exists) to pgvector similarity instead of `ILIKE` text match.
**Acceptance criteria:**
- [ ] `internal/episode/recorder.go`'s `placeholderEmbedding()` replaced with a real embedding call
- [ ] `SearchEpisodes` uses pgvector `<->`/HNSW query instead of `task ILIKE`
- [ ] p95 < 200ms per `ARCHITECTURE.md` §11 NFR
**Dependencies:** ADR-009 (same LLM/embedding endpoint decision as B-007) — blocking. Also depends on B-002's resolution if embeddings/content stay SaaS-side.

### B-009 — Multi-tenancy defense-in-depth review
**Objective:** Confirm every `eami-api/internal/store` query is correctly `org_id`-scoped, and decide whether to add Postgres RLS as a second layer (ADR-013, still Pending).
**Acceptance criteria:**
- [ ] Audit pass over every query in `internal/store/*.sql.go` confirming `org_id` filter presence
- [ ] ADR-013 resolved (RLS vs. app-level-only, explicitly)
- [ ] If RLS chosen: migration adding row-level security policies
**Dependencies:** none for the audit; ADR-013 decision needed before the RLS half.

### B-010 — Fix Linux DNS-cache correlation stub
**Objective:** `eami-agent/internal/detection/network_activity/scanner_linux.go:181` (`linuxDNSCache`) actually returns DNS-hit data instead of always `nil`.
**Acceptance criteria:**
- [ ] Real hostname correlation on Linux (e.g. via `resolvectl` per-connection query, or `/etc/resolv.conf` + local cache parsing)
- [ ] Test added
**Dependencies:** none. Low priority (minor detection-completeness gap).

### B-011 — Remove or re-wire the dead `notification_channels` table
**Objective:** `schema.sql`'s `notification_channels` table has no Go code referencing it (superseded by `notification_config`). Either drop it via migration or explain why it's still needed.
**Acceptance criteria:**
- [ ] Confirmed dead (repo-wide grep) or a use is found and documented
- [ ] If dead: migration to drop it, or explicit decision to leave as reserved-for-future with a comment
**Dependencies:** none. Low priority.

### B-013 — Verify builds/tests actually pass
**Objective:** Every module's `go test ./...` and `eami-ui`'s `type-check`/`build` are executed and confirmed green on a real machine — this bootstrap session could not run any of them (no Go/Node/npm installed here).
**Acceptance criteria:**
- [ ] `go build ./... && go test ./...` run and passing in all 5 Go modules, output captured
- [ ] `npm ci && npm run generate-client && npm run type-check && npm run build` run and passing in eami-ui
- [ ] `BUILT.md` per-module "Test coverage" lines updated from "not executed" to actual pass/fail + coverage %
**Dependencies:** a machine (or CI run) with the toolchain installed.

### B-014 — macOS agent hardware verification
**Objective:** Close the deferred half of `tasks/TASK-054` — `.pkg` install/uninstall smoke-tested on real macOS hardware (Intel + Apple Silicon), not just CI-built.
**Acceptance criteria:**
- [ ] `.pkg` installs cleanly on macOS 14 (Intel) and macOS 15 (Apple Silicon)
- [ ] Agent registers with collector, appears in Discover
- [ ] Uninstall removes plist + binary cleanly
**Dependencies:** access to Mac hardware (none available in CI per `tasks/TASK-054-results.md`).

### B-015 — Restrict direct network reachability of eami-gateway's episode endpoint
**Objective:** Ensure `eami-api`'s proxy is the *only* actual caller of `eami-gateway`'s `GET /v1/gateway/episodes*` — not just that it's the only one the app code assumes.
**Context:** Brief 1's security review (2026-07-21) confirmed: `eami-gateway`'s `GET /v1/gateway/episodes*` service-key auth path trusts a client-supplied `org_id` with no independent authorization check — by design, since that check is Brief 2's job. **Brief 2 (merge `adcd3e9`) and Brief 3 (merge `292d6a4`) are both now merged to master** — B-002 itself is closed, and `eami-api`'s proxy correctly enforces org isolation for all traffic that goes through it. What's left, and the reason this item stays open: Brief 1's gateway endpoint itself is unchanged — anyone who holds `GATEWAY_EPISODE_READ_SERVICE_KEY` and can reach `eami-gateway` directly (bypassing `eami-api` entirely, e.g. if both are on the same network segment with no firewalling between them) still gets zero enforcement from the gateway side. This is a network/deployment-topology question, not something either brief's application code can fix.
**Acceptance criteria:**
- [x] Merge `b-002-eami-api-proxy-layer` to master (done, `adcd3e9`)
- [ ] Confirm whether any environment reachable from outside the gateway's own trust boundary can hit `eami-gateway`'s episode route directly (not via `eami-api`'s proxy) — e.g. is `GATEWAY_EPISODE_READ_SERVICE_KEY` provisioned anywhere a non-`eami-api` caller could use it?
- [ ] If yes: restrict network reachability so only `eami-api` can reach `eami-gateway`'s episode endpoint (the proxy is the intended sole caller)
- [ ] Close this item once that network assumption is confirmed
**Dependencies:** none remaining from B-002 (fully closed); this is now a standalone network-hardening item.
**Severity:** Medium — `eami-api`'s org-isolation logic is built, merged, and verified; residual risk is purely about network reachability of `eami-gateway`'s endpoint outside the intended `eami-api`-only caller, same class of trust-boundary assumption as the gateway's existing unauthenticated `POST /v1/gateway/tokens` and `GET /healthz` routes.

### B-016 — Fix pre-existing nil-`s.queries` panics in FinOps time-series tests
**Objective:** `TestFinOpsTimeSeries_*` subtests in `eami-api/internal/api/finops_test.go` stop panicking internally.
**Context:** Discovered while verifying B-002 Brief 2 (2026-07-22, first real `go test` run this repo has had) — several `TestFinOpsTimeSeries_ValidGranularities`/`_ValidAgentID_PassesValidation`/`_MissingGranularity_UsesDefault` subtests panic with a nil-pointer dereference in `finops.go:269` (`s.queries.DB()`, `s.queries` is nil in `newFinOpsTestEnv`'s `NewServer(nil, authSvc, nil, nil)`). chi's `Recoverer` middleware catches the panic and returns 500, and the tests don't assert against that specific case, so they still report `--- PASS` — meaning this has been silently masking broken behavior, possibly for a long time. Confirmed pre-existing and unrelated to B-002 Brief 2 (`git diff --stat master -- finops.go finops_test.go` is empty on that branch).
**Acceptance criteria:**
- [ ] Root-cause why these specific FinOps time-series requests reach `s.queries.DB()` instead of failing validation first (per `newFinOpsTestEnv`'s own comment, they're expected not to)
- [ ] Fix so the panic no longer occurs (either the test fixture provides a working store, or the handler validates further before touching `s.queries`)
- [ ] Test assertions strengthened so a future regression here fails the test instead of passing on 500
**Dependencies:** none.
**Severity:** Medium — doesn't block anything currently, but a passing-test-that-panics is exactly the kind of gap that lets real bugs ship unnoticed.

## BLOCKED
- **B-007** — blocked on ADR-009 (local vs. API LLM endpoint decision), open since 2026-05-31.
- **B-008** — blocked on the same ADR-009 decision as B-007, and secondarily on B-002's Brief 2/3 outcome.

## DONE
_(one line each; full detail in `BUILT.md` / `CHANGELOG.md`)_
- **v1.0.0** (2026-07-01) — first customer release: gateway proxy/policy/audit/approvals, endpoint discovery agent (all major platforms), full web UI, installers for Windows/macOS/Linux, CI/CD, `setup.sh`.
- **v1.0.1** (2026-07-05, `84028bb`) — nginx `/v1/` proxy fix, Policies/Tools/Nodes/Audit pages completed.
- **Security hardening (TASK-051 findings, all HIGH closed)** — JWT revocation persisted + issuer-validated (TASK-062/063), audit-log DB-error propagation (TASK-064), audit chain verify endpoint (TASK-065), bcrypt cost 10→12 (TASK-066).
- **Unreleased, on HEAD `d8b9483`** — endpoint agent detection scanners (browser extensions, scheduled tasks), alerting engine metrics (`scope_drift_count`, `failed_delivery_count`), `/v1/discover`, `/v1/reports`, `/v1/internal/token-usage` ingest APIs, episode recorder (TASK-069, placeholder embeddings), Memory/Episode library UI page (TASK-070) — **see B-002, this last item shipped ahead of its blocking ADR (now being fixed, Brief 1 of 3 done).**
- **B-002 Brief 1** (2026-07-22, merge commit `3eab113`) — `eami-gateway` dual-auth episode read endpoint, verified with a real toolchain (`go build`/`go test` clean, 18/18 new tests). Closes the ADR-019 half of the data-sovereignty fix that's on the gateway side; `eami-api`/`eami-ui` sides remain in Briefs 2–3.
- **B-002 Brief 2** (2026-07-22, merge commit `adcd3e9`) — `eami-api` proxy layer for episode content, with the actual org-isolation enforcement Brief 1 deferred. Verified with a real toolchain (`go build`/`go vet`/`go test` clean, 11/11 new tests). `memory.go`/`MemoryPage.tsx` cutover remains in Brief 3 — see B-002's own entry for why that's the piece that actually closes this out.
- **B-002 Brief 3 / B-002 fully closed** (2026-07-22, merge commit `292d6a4`) — `memory.go` and `store/episodes.go` (the last direct, unprotected episode-content query path) deleted entirely; `/v1/memory/episodes*` now served by Brief 2's org-isolated handlers. `MemoryPage.tsx` needed zero changes. Security review confirmed the leak fully closed, not just a safer alternative added. **B-002 is done — exactly one path to full episode content exists, and it enforces org isolation.**
- **B-012** (2026-07-22, incidental to B-002 Brief 3) — the stale `router.go` "Memory episodes (stubs...)" comment is gone; that whole block was rewritten as part of the memory.go cutover.
- **TASK-031 → TASK-068** (34 of ~40 tasks) — confirmed DONE via source cross-check; see full per-task table from the bootstrap survey if needed (not reproduced here to keep this file scannable — ask if you need the raw table).
- **B-019** (2026-07-22) — standalone infra fix, not tied to any brief: `docker-compose.yml`'s `eami-ui` service had the wrong build context (`./eami-ui`) for a Dockerfile that copies repo-root `api/openapi.yaml`, breaking `docker compose up --build`. Fixed to `context: .` / `dockerfile: eami-ui/Dockerfile`. Verified with `docker compose build eami-ui`. Incidentally confirms Docker is available on this machine — see `BUILT.md` cross-cutting note (relevant to B-004, still QUEUED, not re-attempted here).
- **B-020** (2026-07-22) — standalone infra fix: `eami-collector` was crash-looping (`exec /app/docker-entrypoint.sh: no such file or directory`) because `docker-entrypoint.sh` had CRLF line endings, breaking shebang resolution. Stripped to LF. Verified: `docker compose build eami-collector` clean, container starts and stays running (not just builds). No Dockerfile change needed.
- **B-022** (2026-07-23) — `POST /v1/gateway/tools` now actually encrypts and persists the `credentials` object it always documented but silently discarded (decoded, never read, never written to `gateway_tools.credentials_encrypted`, 201 returned anyway — a gap found during a full-application audit, not previously tracked under its own B-ID). New `eami-api/internal/toolcreds` package (AES-256-GCM, key from `TOOL_CREDENTIALS_ENCRYPTION_KEY`); `CreateTool` fails closed (500, no store call) if credentials are submitted but no key is configured. A security review pass caught a real bypass in an early version of the fix (deciding "were credentials submitted?" via the typed `ToolCredentials` struct meant an unrecognized field name decoded to an all-empty struct and reproduced the original silent-discard bug) — fixed by deciding presence structurally from the raw JSON and encrypting the raw bytes, not a re-marshaled struct; re-verified clean by the same reviewer. A general code-review pass caught `tools.go`'s other three handlers calling `s.queries` with no nil guard (unlike every other handler file in this package) and a non-standard `"config_error"` code; both fixed. 19 new tests (`toolcreds_test.go` + `tools_test.go`, `tools.go` had zero coverage before this). **Verified 2026-07-23 with a real toolchain: `go build ./...`, `go vet ./...`, `go test ./...` all clean, 0 failures.** `TestTool`'s synthetic-connectivity stub is unchanged, explicitly out of scope — see B-023.
- **B-023** (2026-07-24) — `POST /v1/gateway/tools/{toolId}/test` now runs a real connectivity check instead of unconditionally returning a synthetic "connected" result. New `eami-api/internal/api/tool_connectivity.go`: real HTTP GET for `rest_api` (Bearer `api_key`, or Basic using oauth client creds as a best-effort fallback), real `pgx.ConnectConfig` handshake for `database` (proves reachability + auth in one step via SQLSTATE class `28xxx`), and an honest `misconfigured` for `mcp` (a local-subprocess tool type that can't be tested from eami-api's cloud process without a command-injection/RCE surface — a deliberate non-goal, not a gap). Response shape now matches `openapi.yaml`'s long-undocumented `{success, latency_ms, error}` (the old stub returned a different, never-matching shape). **Security-critical addition caught by review, not in the original plan:** since `eami-api` is EAMI's own cloud SaaS process (unlike `eami-gateway`, which is on-prem), an unguarded version would let an org admin/operator use this endpoint as a reachability oracle against EAMI's own cloud network (e.g. `169.254.169.254` metadata endpoints, internal services) — added `safeDialContext` (rejects loopback/link-local/private/RFC1918/ULA targets via `net.IP`'s own classification, resolve-once-then-dial-the-validated-IP to close a DNS-rebinding gap), wired into both the REST and database dial paths; re-verified clean by the security reviewer against the real 169.254.169.254/RFC1918 cases end-to-end. General code review separately caught and fixed: single-address-only dialing (now falls back through all resolved addresses), a per-call `http.Transport` leaking an idle connection + goroutines on every test (fixed with `DisableKeepAlives: true`), and an unbounded `pgx` connection close (fixed with a dedicated 2s close-only context). Credentials are read back only via a new `GetToolForTest` (raw SQL through `s.queries.DB()`, same escape hatch `finops.go` uses — `store/tools.sql.go` untouched) and never reach a log line or the HTTP response; `toolcreds.Decrypt`'s first production caller handles a wrong/rotated key as a clean `misconfigured` result, never a panic. 30 new tests, including direct coverage of the SSRF guard (loopback, AWS/GCP metadata address, RFC1918 x3, IPv6 ULA, and confirming real public IPs are *not* blocked). One deliberate test-only seam, `Server.toolDialOverride`, unexported, assigned only in `_test.go` files (confirmed via grep by the security reviewer) — production always uses the real `safeDialContext`. **Verified 2026-07-24 with a real toolchain: `go build ./...`, `go vet ./...`, `go test ./...` all clean, 0 failures** (149 total in the module, up from 143 pre-B-023). Known follow-up: the frontend doesn't read the real result yet — see B-024, QUEUED.

## QUEUED (added this session)

### B-024 — `ToolsPage.tsx`'s "Test connection" button ignores the real result now returned by B-023
**Objective:** `handleTest` (`eami-ui/src/pages/gateway/ToolsPage.tsx`) reads `response.success` (and ideally surfaces `response.error`) to decide OK/Failed, instead of only checking whether the HTTP call itself threw.
**Context:** Found during B-023's code review. Before B-023, `TestTool` always returned HTTP 200 with a synthetic `{"status":"connected"}` body, so the frontend's "only check if the request threw" logic was harmless (it could never see a real failure anyway). B-023 makes `TestTool` return real results — `connected`/`auth-failed`/`unreachable`/`misconfigured` — but *always* as HTTP 200 with `{success, latency_ms, error}` in the body (matching `openapi.yaml`'s long-documented-but-never-implemented response shape). `useTestTool`/`handleTest` never read `data.success` — they only branch on whether the generated client's `error` (HTTP-level, non-2xx) is set, which it never will be for a completed test. **Net effect: the "Test connection" button now always shows the green "OK" state, regardless of the tool's actual reachability** — a real, user-visible regression in what the button communicates, even though the backend fix itself is correct and more capable than before.
**Acceptance criteria:**
- [ ] `handleTest` branches on `response.success`, not just the absence of a thrown error
- [ ] `response.error` (when present) is surfaced to the user (e.g. in the button's title/tooltip, or a toast) rather than discarded
- [ ] Manual check: a tool pointed at unreachable/wrong credentials shows a real "Failed" state in the UI
**Dependencies:** B-023 (done) — the backend now returns real, meaningful results; this is purely a frontend consumption fix. Frontend was out of scope for B-023's own brief.

### B-021 — Every `.sh` file in the repo has CRLF line endings except the now-fixed collector entrypoint
**Objective:** Prevent the same shebang-resolution crash (B-020) from recurring in any other script, and stop silent `\r`-in-heredoc corruption in scripts that build SQL/config strings.
**Context:** Discovered 2026-07-22 while fixing B-020. `file` on every `.sh` in the repo (`scripts/setup.sh`, `scripts/seed-db.sh`, `scripts/create-audit-partition.sh`, `scripts/generate-api-client.sh`, `eami-collector/scripts/create_api_key.sh`, all of `eami-agent/installer/{linux,macos}/*.sh`) reports CRLF terminators. No `.gitattributes` exists to pin LF for shell scripts, so a Windows checkout (`core.autocrlf=true` or similar) rewrites them on clone. These haven't crash-looped yet only because they're invoked as `bash script.sh` rather than exec'd via shebang — same landmine as B-020 for anything that changes to direct exec, and CRLF inside heredocs (`setup.sh`'s inline `psql` blocks) is a latent correctness risk even without a crash.
**Acceptance criteria:**
- [ ] Add `.gitattributes` pinning `*.sh text eol=lf` (and likely `Dockerfile`, `docker-entrypoint.sh` by name)
- [ ] Normalize existing scripts to LF
- [ ] Confirm `scripts/setup.sh`'s inline heredoc SQL still runs clean after normalization (it already worked with CRLF since bash tolerates `\r` mid-heredoc-line in most cases, but worth confirming, not assuming)
**Dependencies:** none. Not fixed as part of B-020 — that task was scoped to the collector only.

## Next B-ID: B-025
