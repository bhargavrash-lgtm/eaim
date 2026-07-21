# BACKLOG.md — EAMI

Generated 2026-07-21 during bootstrap (B-001). See `BUILT.md` for the evidence behind each item.

## NEXT
_(empty — founder/PM assigns from QUEUED)_

## QUEUED

### B-002 — Resolve ADR-019 vs. ADR-010 conflict: episode content in the SaaS API
**Objective:** `eami-api`'s memory endpoints stop violating (or are explicitly granted an exception to) the data-sovereignty rule in ADR-010.
**Acceptance criteria:**
- [ ] Architect-EAMI/founder rules on ADR-019 (still "Pending" in `DECISIONS.md`) — either eami-api gets a scoped exception to serve full episode content, or the memory endpoints are re-scoped to metadata-only with full content served from an on-prem gateway endpoint instead
- [ ] `eami-api/internal/api/memory.go` matches whichever decision is made
- [ ] `DECISIONS.md` ADR-019 updated to Accepted with the resolution
**Dependencies:** Architect-EAMI decision (blocking — see BLOCKED below).
**Severity:** High — currently shipped code contradicts an accepted ADR and an explicit task-brief instruction (`tasks/TASK-069.md`, `tasks/TASK-070.md`).

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

## BLOCKED
- **B-002** — blocked on Architect-EAMI/founder resolving ADR-019 (episode data-sovereignty exception, or not).
- **B-007** — blocked on ADR-009 (local vs. API LLM endpoint decision), open since 2026-05-31.
- **B-008** — blocked on the same ADR-009 decision as B-007, and secondarily on B-002's outcome.

## DONE
_(one line each; full detail in `BUILT.md` / `CHANGELOG.md`)_
- **v1.0.0** (2026-07-01) — first customer release: gateway proxy/policy/audit/approvals, endpoint discovery agent (all major platforms), full web UI, installers for Windows/macOS/Linux, CI/CD, `setup.sh`.
- **v1.0.1** (2026-07-05, `84028bb`) — nginx `/v1/` proxy fix, Policies/Tools/Nodes/Audit pages completed.
- **Security hardening (TASK-051 findings, all HIGH closed)** — JWT revocation persisted + issuer-validated (TASK-062/063), audit-log DB-error propagation (TASK-064), audit chain verify endpoint (TASK-065), bcrypt cost 10→12 (TASK-066).
- **Unreleased, on HEAD `d8b9483`** — endpoint agent detection scanners (browser extensions, scheduled tasks), alerting engine metrics (`scope_drift_count`, `failed_delivery_count`), `/v1/discover`, `/v1/reports`, `/v1/internal/token-usage` ingest APIs, episode recorder (TASK-069, placeholder embeddings), Memory/Episode library UI page (TASK-070) — **see B-002, this last item shipped ahead of its blocking ADR.**
- **TASK-031 → TASK-068** (34 of ~40 tasks) — confirmed DONE via source cross-check; see full per-task table from the bootstrap survey if needed (not reproduced here to keep this file scannable — ask if you need the raw table).

## Next B-ID: B-015
