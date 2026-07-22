# BUILT.md — EAMI (Enterprise AI Monitoring & Intelligence)

Generated 2026-07-21 during bootstrap (B-001), from static source review (no `go`/`node` toolchain available on this machine — build/test status below is NOT from an executed run; see the note in each module). Ground truth sources: actual code, `git log`, `tasks/*-results.md`/`*-findings.md`, `schema/schema.sql`. Cross-checked against `ARCHITECTURE.md`/`ROADMAP.md`/`DECISIONS.md`, which remain accurate as design docs but are ahead-of/behind actual code in places noted below.

Shipped tags: `v1.0.0-rc1` → `v1.0.0` (2026-07-01) → `v1.0.1` (2026-07-05, commit `84028bb`). Current HEAD `d8b9483` is unreleased work past v1.0.1 (endpoint scanners, alerting metrics, discover/ingest APIs, episode recorder, memory UI).

---

## eami-agent
**Purpose:** Lightweight endpoint scanner (Windows primary, macOS/Linux ports per ADR-015). Runs on interval, detects AI tooling footprint, ships JSON reports to the collector.
**Status:** STABLE (core), WORKING-BUT-FRAGILE (Linux DNS correlation, macOS — no test hardware).
**Key files:** `cmd/agent/main.go`; `internal/detection/{ai_apps,ai_processes,browser,cloud_clients,file_changes,gpu,mcp_servers,models,network_activity,nodejs_ai,python_envs,scheduled_tasks}/`; `internal/{payload,collector,config,service}/`.
**Interfaces:** `POST <collector-url>/ingest` — gzipped JSON `Report{}` (endpoint id, hostname, per-detection-domain findings), API-key header auth.
**Data owned:** none (stateless scanner; report shape defined by `internal/payload`).
**Test coverage:** 5 `_test.go` files vs 55 source files (~11:1 file ratio). Not executed this session.
**Known limitations:**
- `internal/detection/network_activity/scanner_linux.go:181` — `linuxDNSCache()` runs `resolvectl statistics` but discards output, always returns `nil` (comment: "resolvectl doesn't expose individual hostnames"). DNS-hit correlation unimplemented on Linux; rest of network scanner (proc-based connection mapping) is real.
- macOS build path exists (ADR-015) but has never run on real macOS hardware — `.pkg` install/uninstall smoke test deferred per `tasks/TASK-054-results.md`.
- Scheduled-tasks scanner exists (`internal/detection/scheduled_tasks/`) despite `ROADMAP.md` listing it as "deferred to v1.1" — it shipped early.

---

## eami-collector
**Purpose:** On-prem HTTP server between endpoint agents and the SaaS API. Validates + buffers reports in SQLite (survives outages), forwards batches.
**Status:** WORKING-BUT-FRAGILE — thinnest test coverage in the repo.
**Key files:** `cmd/collector/main.go`; `internal/{api,db,forwarder,models}/`.
**Interfaces:** receives agent `POST /ingest`; forwards to `eami-api` `POST /v1/ingest/batch` and `POST /v1/reports` (service-key auth).
**Data owned:** local SQLite WAL buffer (`report_buffer` table + dead-letter table per ADR-003), not in Postgres `schema.sql`.
**Test coverage:** 1 `_test.go` file (`forwarder_test.go`) vs 8 source files. `ingest.go`, `db`, and `models` have **no tests**. Not executed this session.
**Known limitations:** API-key validation and ingest handler (`internal/api/ingest.go`) are implemented and read as correct, but are unverified by tests — this is the collector's security boundary (agent → collector auth) and has zero test coverage.

---

## eami-gateway
**Purpose:** MCP control plane. Proxies AI-agent tool calls, enforces policy (via `eami-policy`), routes approvals, writes the audit log, records episodes, issues/validates AI-token JWTs.
**Status:** STABLE (proxy/policy/audit/approval/identity), PARTIAL (episode embeddings — intentional placeholder).
**Key files:** `cmd/gateway/main.go` (orchestration — dispatch, token-usage write, episode-record calls all fire from here, not from `internal/proxy/`); `internal/{mcp,identity,proxy,approval,audit,episode,policyloader}/`. (`internal/node` gossip-mesh package described in `ARCHITECTURE.md` §7 was not confirmed present this session — verify before relying on multi-node/Serf claims.)
**Interfaces:**
- MCP endpoint (stdio + SSE) for AI agents.
- `internal/episode.Recorder.Record(...)` — writes to Postgres `episodes` table (JSONB steps + pgvector embedding column), called via `go episodeRecorder.Record(...)` from `main.go` at all 4 decision outcomes (deny / hold-escalate / forward-error / success).
- `internal/identity` — JWT issuance/validation, revocation list persisted to DB (survives restart, closes JWT-002/TASK-062) with issuer validation (closes JWT-001/TASK-063).
- `internal/audit` — hash-chained writer; DB error on init now propagates instead of silently seeding (closes AUDIT-001/TASK-064).
- pprof endpoint, opt-in via `GATEWAY_PPROF_ADDR` (TASK-061).
- **`internal/episode` read endpoint (added 2026-07-21, branch `b-002-gateway-episode-endpoint`, B-002 Brief 1):** `GET /v1/gateway/episodes` (paginated list, `limit`/`offset`/`outcome` params), `GET /v1/gateway/episodes/search?q=` (ILIKE text search, parity with eami-api's current approach — no vector search yet), `GET /v1/gateway/episodes/{id}` (full episode incl. `steps`). Dual auth via `episode.Handler.authenticateCaller`: (a) `X-Service-Key` against a dedicated `GATEWAY_EPISODE_READ_SERVICE_KEY` (required config, gateway won't start without it) — org-scoped by a **client-supplied** `org_id` query param, trusting the caller (intended: eami-api's Brief 2 proxy) to have already authorized it; (b) `Authorization: Bearer <AI token>` — org resolved server-side via `internal/registry.LookupByName`, never from a client param. `internal/episode/store.go`'s `episodeStore` interface + `pgxEpisodeStore` mirrors the `audit.WriterDB`/fake-seam test pattern. Full unit test coverage in `reader_test.go`/`http_test.go` (18 tests), including the security-critical forged-org_id-ignored and cross-org-404-not-403 cases.
**Data owned:** `episodes`, `audit_log` (+ monthly partitions `audit_log_2026_08`…`_12`), `revoked_ai_tokens`, `gateway_nodes`/`gateway_node_metrics` (schema-level; write path not confirmed this session).
**Test coverage:** 5 `_test.go` files vs 17 source files (post Brief 1). Includes `TestManager_Validate_RevokedToken_SurvivesRestart`, `TestManager_Validate_WrongIssuer_ReturnsError`, `TestWriter_DBErrorOnInit_PropagatesError`, plus the new `episode` package's 18 tests. **Verified 2026-07-22: `go build ./...` and `go test ./... -v` both clean, 0 failures — 10/10 `audit`, 18/18 `episode` (incl. the two security-critical cases: forged `org_id` ignored on the bearer path, cross-org `GetByID` → 404 not 403), 12/12 `identity`, 10/10 `proxy`. Go is installed at `C:\Program Files\Go\bin\go.exe` on this machine but wasn't on `PATH` for the shell sessions used earlier in bootstrap — see `CLAUDE.md`'s toolchain note.**
**Known limitations / landmines:**
- **Episode embeddings are a deterministic SHA-256-derived placeholder** (`internal/episode/recorder.go`, `placeholderEmbedding()`), explicitly commented as pending ADR-009 (LLM endpoint choice, still open). Not semantically meaningful — vector similarity search over episodes will not return relevant results until this is swapped.
- **ADR-010/ADR-019 conflict — resolved 2026-07-21, fix in progress (B-002, 3 briefs, Brief 1 of 3 done here).** Full episode content will stay on-prem in this package; `eami-api` will stop serving it once Briefs 2–3 land.
- **⚠️ New landmine from Brief 1 (see BACKLOG B-015): until Brief 2's eami-api proxy exists to do real per-user org authorization, the new episode read endpoint's service-key auth path trusts a client-supplied `org_id` with no independent check.** Documented extensively in code comments (`internal/episode/http.go`'s `authenticateCaller`). Do not provision `GATEWAY_EPISODE_READ_SERVICE_KEY` in any environment reachable by an untrusted caller before Brief 2 ships.

---

## eami-policy
**Purpose:** Shared policy-evaluation library (not a service), imported by `eami-gateway`. Structural (JSON-match) and semantic (LLM-intent) rule evaluation.
**Status:** STABLE (structural), PARTIAL (semantic — intentional stub).
**Key files:** `evaluator.go`, `structural.go`, `semantic.go`, `types.go` (flat module, no `internal/`).
**Interfaces:** Go library API — `Evaluate(rules, context) Decision`. Imported directly by `eami-gateway`; no network interface.
**Data owned:** none (pure library over caller-supplied rule/context data).
**Test coverage:** 1 `_test.go` file (`policy_test.go`) vs 4 source files, including a test that pins the stub behavior (`TestEvaluate_SemanticRuleSkippedByStub`). Not executed this session.
**Known limitations:** `semantic.go` — semantic/LLM rule evaluation **always returns no-match** (`// TODO(BE-Policy): Implement full LLM-based semantic evaluation`). Any policy relying on a semantic rule silently falls through to the next rule or default action. Blocked on ADR-009/ADR-012 (LLM endpoint choice).

---

## eami-api
**Purpose:** SaaS REST backend. Auth, org/user/RBAC management, gateway-resource CRUD (agents/policies/tools/nodes), audit query, FinOps analytics, alerting, discover/ingest write paths, memory/episode read paths.
**Status:** STABLE (broad — largest, best-tested module). **Data-sovereignty landmine resolved 2026-07-22** (B-002 fully closed — see below; no longer a landmine).
**Key files:** `cmd/api/main.go`; `internal/{api,auth,alerting,config,store}/`. `internal/store/*.sql.go` — one file per domain, sqlc-style, parameterized queries throughout (no `fmt.Sprintf` SQL construction found). **`internal/api/memory.go` and `internal/store/episodes.go` no longer exist** — deleted in B-002 Brief 3 (2026-07-22); see below.
**Interfaces — full route table** (from `internal/api/router.go`, more authoritative than any doc):
- Public: `GET /health`, `POST /v1/auth/login`, `POST /v1/auth/refresh`
- Service-key (collector/gateway → API, no JWT): `POST /v1/reports`, `POST /v1/ingest/batch`, `POST /v1/internal/token-usage`
- JWT + admin: org/notification settings, user management (`/v1/users*`)
- JWT + admin/operator: API keys, agents/policies/tools/nodes CRUD, agent-config, alert-rule CRUD, approval creation
- JWT + admin/operator/approver: approval decide, alert acknowledge/resolve
- JWT + admin/operator/viewer (read): agents/policies/tools/nodes list+get, `/v1/audit`, `/v1/audit/export`, `/v1/audit/verify`, `/v1/alerts*`, `/v1/finops/summary`, `/v1/finops/timeseries`, `/v1/memory/episodes`, `/v1/memory/episodes/search`, `/v1/memory/episodes/{episodeId}` (all three now served by the gateway-proxy handlers, see below), `/v1/gateway/episodes`, `/v1/gateway/episodes/search`, `/v1/gateway/episodes/{episodeId}` (same handlers, second mount, unused by the frontend), `/v1/endpoints*`, `/v1/discover/endpoints*`
- JWT + any role: `GET /v1/approvals`, `GET /v1/approvals/{id}`
**Data owned:** `orgs`, `users`, `refresh_tokens`, `api_keys`, `agents` (gateway_agents), `agent_configs`, `policies`/`policy_conditions`, `gateway_tools`, `gateway_nodes`/`gateway_node_metrics`, `approval_requests`, `audit_log` (read; write owned by gateway), `alert_rules`/`alerts`, `token_usage`, `model_pricing`, `discovered_endpoints` + `endpoint_ai_apps`/`endpoint_mcp_servers`/`endpoint_model_files`/`endpoint_reports`, `notification_config`. **`episodes` table: no longer directly readable from eami-api at all** — the only path to it is via `eami-gateway`'s org-isolated proxy (`internal/api/gateway_episodes.go`, Brief 2, frozen since).
**Interfaces — B-002 Brief 2 (2026-07-22, `internal/api/gateway_episodes.go`), gateway episode proxy — frozen/unchanged by Brief 3:** `GET /v1/gateway/episodes` (+`/search`, `/{episodeId}`) proxy to `eami-gateway`'s Brief-1 endpoint via `X-Service-Key` (config `Gateway.EpisodeReadServiceKey`, env `GATEWAY_EPISODE_READ_SERVICE_KEY` — same var name/value eami-gateway itself validates inbound; `Gateway.URL`/env `API_GATEWAY_URL`, defaults `http://eami-gateway:8080`). Both config fields optional at eami-api startup — unset means the routes 502 per-request ("gateway proxy is not configured"), not a boot failure. Org isolation (the actual point of this brief, since Brief 1's service-key path enforces none): every gateway call's `org_id` is always `claimsFromContext(r).OrgID`, never client input; an optional `org_id` query param is accepted only as a tamper-check that 403s on mismatch before the gateway is ever called. Gateway error responses collapse to a real 404 (episode not found / cross-org, same value) or a generic 502 (everything else, including network failures) — gateway's plain-text body is never read past the status code, so nothing can leak through. Exported types `GatewayEpisode`/`GatewayListResult`/`GatewayError`/`GatewayEpisodeClient` (exported only so the external `api_test` test package can construct fakes/errors — not intended as a public API).
**Interfaces — B-002 Brief 3 (2026-07-22, branch `b-002-memory-cutover`), memory.go cutover — B-002 fully closed:** `eami-api/internal/api/memory.go` and `eami-api/internal/store/episodes.go` **deleted entirely** (verified zero other callers of `store.Queries.ListEpisodes`/`SearchEpisodes`/`ListEpisodesParams`/`store.Episode` before removal). `router.go`'s `/v1/memory/episodes` and `/v1/memory/episodes/search` registrations — the actual `api/openapi.yaml`-documented, `MemoryPage.tsx`-consumed routes — now point directly at Brief 2's `ListGatewayEpisodes`/`SearchGatewayEpisodes` handlers (same functions, new mount, zero duplicated logic). Added `GET /v1/memory/episodes/{episodeId}` → `GetGatewayEpisode`, filling a route `openapi.yaml` documented but no implementation ever existed for. `MemoryPage.tsx` required **zero code changes** — verified the JSON response shapes are byte-identical between the old `memory.go` output and Brief 2's output (same field names, same envelope) before relying on that. Security review for this brief specifically re-verified the org-isolation chain (`jwtMiddleware` → `checkOrgID` → `claimsFromContext(r).OrgID`) at the new `/v1/memory/episodes*` mount points rather than assuming Brief 2's review transferred automatically — confirmed the leak is fully closed, not just superseded by a safer alternative running alongside it.
**Test coverage:** 7 `_test.go` files vs 44 source files (post Brief 3 — 2 files net change: `memory.go`'s production code gone, `memory_test.go` added). Covers agents, approvals, auth, finops, policies, gateway episode proxy (Brief 2, 11 tests), and memory cutover (Brief 3, new `memory_test.go`, 8 tests reusing Brief 2's `fakeGatewayClient`/`newGatewayTestServer` with zero duplication) — including `TestMemoryEpisodes_List_MismatchedOrgIDSupplied_Returns403_GatewayNeverCalled`, re-proving the org-isolation centerpiece at the real frontend-facing route, and `TestMemoryEpisodesDetail_ValidRequest_ReturnsFullSteps`, asserting non-empty step content actually round-trips (not just a 200 status). **Verified 2026-07-22 with a real toolchain: `go build ./...`, `go vet ./...`, `go test ./...` all clean, 0 failures.** Two subagent reviewer/security passes per brief (Brief 2 and Brief 3) all came back clean. **Frontend (`eami-ui`) build/lint/typecheck not run for Brief 3** — Node/npm confirmed genuinely absent from this machine (checked common install locations directly, unlike Go which was merely off-`PATH`) — `MemoryPage.tsx`'s correctness rests on manual shape-verification, not a compiler run. **`docker compose up`-based manual verification (requested by Brief 3's brief) not performed** — no Docker in this environment; flagged before implementation, not discovered after. **Pre-existing, unrelated issue discovered while verifying Brief 2:** several `TestFinOpsTimeSeries_*` subtests in `finops_test.go` panic internally (nil `s.queries` dereferenced in `finops.go:269`) but still report `--- PASS` because chi's `Recoverer` catches the panic; confirmed pre-existing, not fixed (out of scope) — see `BACKLOG.md` B-016.
**Known limitations / landmines:**
- `router.go` — the historical "Memory episodes (stubs...)" comment that used to sit above these routes is gone now that the routes themselves were rewritten in Brief 3 (was previously flagged as stale in `BACKLOG.md` B-012 — worth confirming that item can close too).
- `notification_channels` table (`schema.sql`) appears unused by any Go code — superseded by `notification_config`. Likely dead schema.
- Search over episodes is `ILIKE` text match on the `task` column, not vector similarity, despite pgvector being provisioned — consistent with the embedding placeholder (both wait on ADR-009). See `BACKLOG.md` B-008.
- **New, discovered during Brief 3:** `api/openapi.yaml`'s `EpisodeStep` schema doesn't match the real step JSON shape (`eami-gateway/internal/episode/recorder.go`'s `Step` struct) — predates B-002 entirely, now visible because `/v1/memory/episodes/{episodeId}` is the first real route where the documented schema is checkable against actual output. See `BACKLOG.md` B-017.

---

## eami-ui
**Purpose:** React SPA — Dashboard, Discover, Gateway (Agents/Policies/Tools/Nodes), Approvals, FinOps, Memory, Audit, Alerts, Settings.
**Status:** STABLE (all 13 pages are real implementations, 105–707 lines each; no bare placeholders remain).
**Key files:** `src/pages/{auth,dashboard,discover,finops,gateway,ops,settings}/*.tsx`; `src/hooks/*.ts` (14 resource hooks); `src/api/client.ts` (generated-client wrapper + documented `apiFetch()` escape hatch).
**Interfaces:** consumes `eami-api` exclusively via the OpenAPI-generated client (`npm run generate-client` → `src/api/schema.ts`, gitignored, build-time artifact — not present in a fresh checkout until generated).
**Data owned:** none (client only); Zustand for local UI/auth state.
**Test coverage:** **0.** No vitest/jest/playwright config exists anywhere in the repo, despite `BOUNDARIES.md` assigning `vitest.config.ts` + `playwright/` to QA-EAMI. Only check is `tsc --noEmit` (`npm run type-check`), and that could not be executed this session (no Node/npm on this machine, and it requires `generate-client` to run first since `schema.ts` doesn't exist pre-build).
**Known limitations:**
- `SettingsPage.tsx:494` — email notification channel is explicitly disabled ("Coming soon — email dispatch is stubbed"); Slack/webhook channel is live.
- `MemoryPage.tsx` fetches directly via `apiFetch()` rather than a dedicated `useMemory.ts` hook — inconsistent with every other page's hook-per-resource pattern (all other 12 pages have one). Not broken, just a convention drift.
- `useAgents.ts:84,98` use raw `fetch()` for agent-config GET/PUT — likely a legitimate use of the documented "endpoint not yet in OpenAPI spec" escape hatch, not confirmed against the spec this session.
- `@ts-expect-error` count is 0 (TASK-049 fully closed).

---

## Cross-cutting / shared
- **Schema:** `schema/schema.sql` + `schema/migrations/001`–`007` (sequential, no gaps). Table references in Go store code cross-checked clean except `notification_channels` (dead, see above).
- **Multi-tenancy:** enforced at the application layer — every store query takes an explicit `org_id` param (e.g. `WHERE org_id = $1`), not Postgres RLS (RLS is used only for `audit_log` append-only enforcement per ADR-007). ADR-013 (org-per-schema vs. RLS) is still listed Pending in `DECISIONS.md`. Correctness currently depends on every handler remembering to pass `org_id` — no defense-in-depth. Not verified exhaustively this session.
- **Secrets/auth posture (from `tasks/TASK-051-security-findings.md`, all HIGH findings closed):** bcrypt cost 12 (TASK-066), JWT RS256 with explicit algorithm allowlist, constant-time API-key comparison, SHA-256-hashed API keys, `crypto/rand` throughout, JWT revocation persisted + issuer-validated (TASK-062/063), audit-log DB-error propagation fixed (TASK-064), audit chain verify endpoint added (TASK-065, `GET /v1/audit/verify`).
- **Toolchain gap:** this development machine has no `go` or `node`/`npm` installed. No build or test command in this doc was actually executed — all status above is from static source review. Treat every "STABLE" tag as "reads correct" rather than "verified green."
