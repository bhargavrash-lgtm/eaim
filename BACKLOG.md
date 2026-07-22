# BACKLOG.md — EAMI

Generated 2026-07-21 during bootstrap (B-001). See `BUILT.md` for the evidence behind each item.

## NEXT
_(empty — founder/PM assigns from QUEUED)_

## QUEUED

### B-002 — Resolve ADR-019 vs. ADR-010 conflict: episode content in the SaaS API
**Objective:** `eami-api`'s memory endpoints stop violating (or are explicitly granted an exception to) the data-sovereignty rule in ADR-010.
**Resolution (2026-07-21):** full episode content stays on-prem; `eami-api` never serves it directly. Implementation split into 3 briefs — see `CONTEXT.md`'s Active decision thread for current status.
- [x] **Brief 1 — DONE, merged to master** (`b-002-gateway-episode-endpoint`, merge commit `3eab113`): `eami-gateway` gets a new dual-auth read endpoint (`GET /v1/gateway/episodes`, `/search`, `/{id}`) serving full episode content from its own on-prem Postgres. Dedicated secret (`GATEWAY_EPISODE_READ_SERVICE_KEY`), full unit test coverage including the security-critical forged-org_id and cross-org-404 cases. Reviewer + security subagent passes: clean. **Verified 2026-07-22 with a real toolchain: `go build ./...` and `go test ./... -v` both clean, 0 failures (18/18 new tests).**
- [x] **Brief 2 — DONE** (branch `b-002-eami-api-proxy-layer`, not yet merged): `eami-api` proxy layer (`internal/api/gateway_episodes.go`) forwarding UI requests to Brief 1's endpoint. The hard requirement is satisfied: `org_id` sent to the gateway is always the authenticated caller's own session org (`claimsFromContext(r).OrgID`), never client input — an optional `org_id` query param is accepted only as a tamper-check that 403s on mismatch before the gateway is ever called (structurally impossible for a forged org to reach the gateway, not just checked-and-rejected). Purely additive: `memory.go` has zero lines changed. 11 tests including the centerpiece `TestGatewayEpisodes_List_MismatchedOrgIDSupplied_Returns403_GatewayNeverCalled`. **Verified 2026-07-22: `go build ./...`, `go vet ./...`, `go test ./...` all clean, 0 failures.** Reviewer + security subagent passes: clean (2 low-severity test-coverage suggestions from the reviewer pass, both closed before commit).
- [ ] Brief 3 — READY TO START (unblocked, Brief 2 merged... pending merge — see note): `eami-api/internal/api/memory.go` stops querying `episodes` directly, switches to Brief 2's proxy; `MemoryPage.tsx` rewired to use it. Depends on Brief 2's branch being merged to master first.
- [x] `DECISIONS.md` ADR-019 updated to Accepted with the resolution (2026-07-22, formalized as a full entry replacing its own Pending row — same number, not renumbered).
**Dependencies:** none for Brief 1 (done, merged). Brief 2 done, on its own branch — merge before starting Brief 3.
**Severity:** High — was: shipped code contradicts an accepted ADR. Now: 2 of 3 briefs complete; `memory.go` itself still violates the (now-Accepted) ADR-019 until Brief 3 lands.
**⚠️ Operational risk flagged by Brief 1's security review — see B-015. Now mitigated in practice by Brief 2's org-isolation enforcement, but B-015 itself is about the *service-key path having no enforcement of its own* — still technically true of Brief 1's code in isolation, so leave B-015 open until it's explicitly re-reviewed.**

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
**Objective:** Replace the SHA-256 placeholder embedding with real embeddings; wire `SearchMemoryEpisodes` to pgvector similarity instead of `ILIKE` text match.
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

### B-012 — Fix stale comment in router.go
**Objective:** `eami-api/internal/api/router.go:137` comment ("Memory episodes (stubs - episode recorder not yet built)") no longer matches reality post-TASK-069/070.
**Acceptance criteria:**
- [ ] Comment updated or removed
**Dependencies:** none. Trivial/cosmetic.

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

### B-015 — Do not deploy the gateway episode-read endpoint before Brief 2 ships
**Objective:** Prevent a real tenant-isolation gap between Brief 1 (done) and Brief 2 (done, not yet merged).
**Context:** Brief 1's security review (2026-07-21) confirmed: `eami-gateway`'s `GET /v1/gateway/episodes*` service-key auth path trusts a client-supplied `org_id` with no independent authorization check — by design, since that check is Brief 2's job. **Update 2026-07-22: Brief 2 is now done** (branch `b-002-eami-api-proxy-layer`) and its security review confirmed the org-isolation enforcement works as designed — but Brief 2's protection only applies to traffic going through *its* routes (`eami-api`'s `/v1/gateway/episodes*`). Brief 1's gateway endpoint itself is unchanged: anyone who holds `GATEWAY_EPISODE_READ_SERVICE_KEY` and can reach the gateway directly (bypassing eami-api entirely) still gets zero enforcement from the gateway side. This item stays open until Brief 2 is merged to master and confirmed to be the *only* path that's actually deployed/reachable with that secret provisioned.
**Acceptance criteria:**
- [ ] Merge `b-002-eami-api-proxy-layer` to master
- [ ] Confirm whether any environment reachable from outside the gateway's own trust boundary can hit Brief 1's route directly (not via eami-api's proxy) — e.g. is `GATEWAY_EPISODE_READ_SERVICE_KEY` provisioned anywhere a non-eami-api caller could use it?
- [ ] If yes: restrict network reachability so only eami-api can reach eami-gateway's episode endpoint (the proxy is the intended sole caller)
- [ ] Close this item once Brief 2 is merged and that network assumption is confirmed
**Dependencies:** Brief 2 merge (B-002).
**Severity:** Medium (downgraded from High) — Brief 2's org-isolation logic is now built and verified; residual risk is purely about network reachability of Brief 1's endpoint outside the intended eami-api-only caller, same class of trust-boundary assumption as the gateway's existing unauthenticated `POST /v1/gateway/tokens` and `GET /healthz` routes.

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
- **B-002 Brief 2** (2026-07-22, branch `b-002-eami-api-proxy-layer`, not yet merged) — `eami-api` proxy layer for episode content, with the actual org-isolation enforcement Brief 1 deferred. Verified with a real toolchain (`go build`/`go vet`/`go test` clean, 11/11 new tests). `eami-ui` side remains in Brief 3.
- **TASK-031 → TASK-068** (34 of ~40 tasks) — confirmed DONE via source cross-check; see full per-task table from the bootstrap survey if needed (not reproduced here to keep this file scannable — ask if you need the raw table).

## Next B-ID: B-017
