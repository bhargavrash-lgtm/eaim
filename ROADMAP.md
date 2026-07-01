# EAMI — First-Cut Product Roadmap
**Owner:** PM-EAMI  
**Created:** 2026-06-11  
**Target:** Self-hostable v1.0 — a real customer can run `setup.sh`, install the agent, configure policies, and use EAMI daily.  
**Timeline:** 7 weeks (Week 1 starts immediately)

---

## Definition of Done — v1.0

A customer can:
1. Run `./scripts/setup.sh` on a Linux/macOS server and bring up all services in under 10 minutes.
2. Install the endpoint agent on Windows/macOS/Linux machines via MSI/pkg/deb. The Discover page shows those machines within 5 minutes.
3. Connect Claude Desktop (or any MCP client) through the EAMI gateway. All AI tool calls are proxied, logged, and policy-evaluated in real time.
4. Create a policy that DENIES or ESCALATEs a tool call. The escalation appears in the Approvals UI; approving it resumes the call; denying it blocks it. Slack notification fires.
5. See real token spend in the FinOps page — per agent, per model, with time-series charts.
6. Create an alert rule (e.g., "token spend > $100/day") that fires a Slack message.
7. `go test ./...` is clean across all Go services. `npm test` is clean in eami-ui.

---

## Current State — What's Complete

The stack is running (`docker compose up` → 5 containers healthy). Login works.

| Area | Status | Notes |
|---|---|---|
| eami-policy | ✅ Complete | 95% test coverage, all rule types |
| eami-api — core CRUD | ✅ Complete | Agents, policies, audit, approvals, settings, users, alerts |
| eami-api — FinOps endpoints | ✅ Complete | Handlers exist; need real data from gateway |
| eami-ui — all pages | ✅ Complete | Dashboard, Discover, FinOps, Settings, Gateway, Approvals, Audit, Alerts |
| eami-gateway — proxy + audit | ✅ Complete | SSE transport, DENY/ALLOW/ESCALATE dispatch, audit hash chain |
| eami-gateway — approval router | ✅ Complete | pg_notify waiter, Slack webhook, Hold() mechanism |
| eami-collector — forwarder | ✅ Complete | SQLite buffer → API forwarding, dead-letter table |
| Agent detection | ✅ Complete | MCP servers, AI apps, models, cloud clients, network, Python/Node envs — cross-platform |
| Agent installers | ✅ Complete | MSI (Windows), .pkg (macOS), .deb/.rpm (Linux) |
| CI/CD | ✅ Complete | GitHub Actions — build + package on every push, release on v* tag |
| Setup script | ✅ Complete | `setup.sh` tested on Ubuntu 22.04 + Debian 12 |
| `go vet ./...` | ✅ Clean | All test files compile as of 2026-06-11 |

---

## Gap Analysis — What's Missing for v1.0

### P0 — Blocks the core value prop

| Gap | Owner | Why it matters |
|---|---|---|
| **Gateway does not write `token_usage`** | BE-Gateway | FinOps shows nothing. Token spend is a primary CIO buying reason. |
| **Collector `ingest.go` is PARTIAL** | BE-Collector | Agent reports arrive at collector but aren't buffered → Discover page has no data. |
| **`eami-agent` Windows service start/stop incomplete** | BE-Collector | Agent doesn't survive reboot. Not deployable. |
| **`go test ./...` not yet verified** | QA-EAMI | Tests compile (vet clean) but runtime correctness unverified. |

### P1 — Required for "self-hostable"

| Gap | Owner | Why it matters |
|---|---|---|
| **DB migrations 002+003 not reflected in `docker-compose` init** | DevOps-EAMI | `settings` + `alerts` endpoints will fail without the extra columns. |
| **Browser extension scanner is STUB** | BE-Collector | Discover page misses Chrome/Edge AI extensions — a key finding for CISOs. |
| **`scope_drift_count` + `failed_delivery_count` alert metrics stubbed** | BE-Policy | Alert rules using these metrics silently return zero. |
| **Agent config management (TASK-018) not built** | BE-Collector + FE-Gateway | Customers can't push updated agent config from the UI. |
| **eami-ui: `@ts-expect-error` directives** | FE-Dashboard | Left over from before `npm run generate-client`. Must be removed for clean build. |
| **Gateway JSON-RPC error format for DENY** | BE-Gateway | Current: returns Go error string. MCP spec requires structured JSON-RPC error object. |

### P2 — Quality and operations

| Gap | Owner | Why it matters |
|---|---|---|
| **Load test: gateway under concurrent MCP sessions** | QA-EAMI | Gateway is in the hot path for every AI call. Need to know its headroom. |
| **Security review: JWT, API keys, audit integrity, RLS** | QA-EAMI / Architect | CISO buyer will ask. |
| **`setup.sh` not tested on macOS** | DevOps-EAMI | macOS is likely the first install target. |
| **Customer-facing README / quick-start guide** | PM-EAMI | Without docs, no one can self-host. |
| **Scheduled tasks scanner is STUB** | BE-Collector | Minor gap — can slip to v1.1. |

---

## Milestone Plan

### M1 — Week 1: Close the ingest path (Discover works with real data)

**Goal:** Install agent on one machine → see it in the Discover UI within 5 minutes.

| Task | Owner | File(s) |
|---|---|---|
| Fix `eami-collector/internal/api/ingest.go` — receive report, validate API key, write to SQLite buffer | BE-Collector | `eami-collector/internal/api/ingest.go` |
| Fix `eami-agent/cmd/agent/main.go` — Windows service start/stop/pause/continue, gzip payload | BE-Collector | `eami-agent/cmd/agent/main.go`, `eami-agent/internal/service/service.go` |
| Fix `eami-collector/internal/api/middleware.go` — API key validation | BE-Collector | `eami-collector/internal/api/middleware.go` |
| Add eami-api endpoint `POST /v1/reports` to receive forwarded agent reports → write to `discovered_endpoints` | BE-Policy | `eami-api/internal/api/reports.go`, store query |
| Architect: add `discovered_endpoints` table to schema.sql + migration | Architect-EAMI | `schema/schema.sql`, `schema/migrations/004_endpoints.sql` |
| DevOps: verify migrations 002+003 apply cleanly in docker-compose init | DevOps-EAMI | `docker-compose.yml`, `schema/schema.sql` |
| QA: end-to-end smoke test — agent reports arrive, Discover page shows endpoints | QA-EAMI | — |

**Acceptance:** Discover page shows at least one real endpoint with AI apps/models populated.

---

### M2 — Week 2: Token usage → FinOps works with real data

**Goal:** Every proxied MCP call writes a `token_usage` row. FinOps charts show real numbers.

| Task | Owner | File(s) |
|---|---|---|
| Gateway dispatch: after `fwdProxy.Forward()` succeeds, parse response for token counts and write to `token_usage` via API call or direct DB write | BE-Gateway | `eami-gateway/cmd/gateway/main.go` (dispatch func) |
| Add `POST /v1/internal/token-usage` endpoint to eami-api (gateway-to-api write path) | BE-Policy | `eami-api/internal/api/token_usage.go` |
| Fix gateway DENY response: return structured JSON-RPC error `{code: -32600, message: "...", data: {reason, policy_id}}` instead of a Go error string | BE-Gateway | `eami-gateway/internal/mcp/handler.go` |
| QA: verify FinOps summary + timeseries return real data after proxied calls | QA-EAMI | — |

**Acceptance:** After 10 proxied MCP calls, FinOps summary shows non-zero spend. DENY path returns well-formed JSON-RPC error to client.

---

### M3 — Week 2-3: Approvals end-to-end (human-in-the-loop verified)

**Goal:** A policy ESCALATE fires a Slack notification, appears in UI, and a human decision resumes or drops the call.

| Task | Owner | File(s) |
|---|---|---|
| Write Slack webhook URL to `notification_config` table and have approval router read it from there (currently from config YAML) | BE-Gateway | `eami-gateway/internal/approval/router.go` |
| DevOps: add `APPROVAL_SLACK_WEBHOOK_URL` to `.env.example` and `docker-compose.yml` | DevOps-EAMI | `.env.example`, `docker-compose.yml` |
| FE: Approvals page — confirm that deciding an approval (approve/deny) triggers the correct API call and the UI refreshes immediately | FE-Ops | `eami-ui/src/pages/ops/ApprovalsPage.tsx` |
| QA: end-to-end approval test — escalating tool call → Slack message → UI approve → call continues; Slack message → UI deny → call returns error | QA-EAMI | — |

**Acceptance:** Full approval loop works. Slack message fires within 5s of escalation. UI reflect decision within 5s.

---

### M4 — Week 3-4: Alert rules fire and reach Slack

**Goal:** Create "token spend > $X" or "scope drift detected" alert rule → it fires → Slack message arrives.

| Task | Owner | File(s) |
|---|---|---|
| Wire `scope_drift_count` metric — query `audit_log` for `decision='escalated'` in the window | BE-Policy | `eami-api/internal/alerting/engine.go` |
| Wire `failed_delivery_count` metric — query `dead_letter` table from collector buffer via new API endpoint | BE-Policy | `eami-api/internal/alerting/engine.go` |
| Verify alert rule evaluation loop fires at correct interval; add Slack test to `POST /v1/settings/notifications/test` | BE-Policy | `eami-api/internal/alerting/engine.go`, `eami-api/internal/api/settings.go` |
| QA: create a rule that fires immediately (low threshold), verify Slack message arrives | QA-EAMI | — |

**Acceptance:** A configured alert rule fires within 2 minutes of threshold breach. Slack message contains rule name, metric value, and link to UI.

---

### M5 — Week 4-5: Discover completeness + agent config push

**Goal:** Discover page shows a comprehensive picture including browser extensions. Admins can push config to agents from the UI.

| Task | Owner | File(s) |
|---|---|---|
| Implement browser extension scanner — Chrome/Edge: read `%APPDATA%\...\Extensions` and match known AI extension IDs | BE-Collector | `eami-agent/internal/detection/browser/scanner.go` (Windows + macOS) |
| Build agent config management: eami-api `GET /v1/agents/{id}/config` returns current config; agent polls on interval | BE-Policy + BE-Collector | `eami-api/internal/api/agent_config.go`, `eami-agent/internal/collector/sender.go` |
| FE-Gateway: add "Config" tab to Agents page — show/edit agent config (scan interval, allowed model paths) | FE-Gateway | `eami-ui/src/pages/gateway/AgentsPage.tsx` |
| FE-Dashboard: remove `@ts-expect-error` directives, run `npm run generate-client`, verify `tsc --noEmit` clean | FE-Dashboard | `eami-ui/src/**` |

**Acceptance:** Discover shows AI browser extensions. Changing scan interval in the UI updates the agent's behavior within 2 intervals.

---

### M6 — Week 5-6: Test coverage, security hardening, setup polish

**Goal:** `go test ./...` clean. Security reviewed. `setup.sh` works on macOS. Customer can self-host.

| Task | Owner | File(s) |
|---|---|---|
| Run `go test ./...` in all Go services; fix any test failures (not just vet) | QA-EAMI | All `*_test.go` files |
| Security review: JWT algorithm confusion, API key entropy, audit log RLS, collector API key brute-force | QA-EAMI / Architect | `eami-api/internal/auth/`, `eami-gateway/internal/identity/`, `schema/schema.sql` |
| Load test: 100 concurrent MCP sessions through gateway for 5 minutes — measure p99 latency and memory | QA-EAMI | `eami-gateway/` |
| Fix `setup.sh` for macOS: detect `brew` vs `apt`, install dependencies correctly | DevOps-EAMI | `scripts/setup.sh` |
| Write `docs/quickstart.md` — 15-minute path from zero to first AI call through gateway | PM-EAMI | `docs/quickstart.md` |

**Acceptance:** All tests green. p99 gateway latency < 50ms at 100 concurrent sessions (excluding downstream AI latency). `setup.sh` works on macOS Sequoia.

---

### M7 — Week 6-7: Package and ship v1.0

**Goal:** Tagged GitHub release. Installers tested on real machines. Changelog published.

| Task | Owner | File(s) |
|---|---|---|
| Smoke-test MSI installer on a fresh Windows 11 VM — service starts, reports to collector | DevOps-EAMI | `eami-agent/installer/` |
| Smoke-test `.pkg` on macOS 14 (Intel) and macOS 15 (Apple Silicon) | DevOps-EAMI | `eami-agent/installer/macos/` |
| Smoke-test `.deb` on Ubuntu 24.04, `.rpm` on RHEL 9 | DevOps-EAMI | `eami-agent/installer/linux/` |
| Verify CI/CD: all 5 GitHub Actions jobs green on `main` | DevOps-EAMI | `.github/workflows/build.yml` |
| Tag `v1.0.0` — GitHub Release auto-publishes MSI, .pkg (arm64 + amd64), .deb, .rpm | DevOps-EAMI | Git tag |
| Write CHANGELOG.md | PM-EAMI | `CHANGELOG.md` |

**Acceptance:** `v1.0.0` GitHub Release exists with 5 downloadable artifacts. All CI jobs green.

---

## Critical Path

The following must complete in order — they block everything downstream:

```
M1: Collector ingest fix
  └── M1: discovered_endpoints schema + API
        └── M1: Discover page shows real data
              └── M5: Agent config push

M2: Gateway token_usage writes
  └── M2: FinOps shows real data
        └── M4: Alert rules (spend threshold) fire correctly

M3: Approval loop verified end-to-end  ← parallel to M2, no dependency

M6: All tests green + security review
  └── M7: Ship
```

Items that can run in parallel with the critical path:
- Browser scanner (M5) — does not block anything
- Alert metrics (M4) — parallel to M5
- macOS setup.sh fix (M6) — parallel to test coverage
- Installer smoke tests (M7) — parallel to final fix sprint

---

## Agent Work Assignments — Week 1 (Start Now)

These 6 tasks can be opened in parallel immediately:

| Agent | Task | Task file |
|---|---|---|
| **BE-Collector** | Fix `ingest.go` + agent Windows service | `tasks/TASK-035-collector-ingest.md` |
| **Architect-EAMI** | Add `discovered_endpoints` table + migration 004 | `tasks/TASK-036-schema-endpoints.md` |
| **BE-Policy** | Add `POST /v1/reports` endpoint to eami-api | `tasks/TASK-037-api-reports-endpoint.md` |
| **DevOps-EAMI** | Verify migrations 002+003 in docker-compose; fix if missing | `tasks/TASK-038-migrations-verify.md` |
| **BE-Gateway** | Token usage writes + JSON-RPC DENY error format | `tasks/TASK-039-gateway-token-usage.md` |
| **QA-EAMI** | Run `go test ./...` across all services; report failures | `tasks/TASK-040-go-test-clean.md` |

---

## What's Explicitly Deferred to v1.1

| Feature | Reason |
|---|---|
| Episode recorder + vector DB (memory) | Requires significant infrastructure; not a CIO/CISO priority for v1 |
| Semantic policy evaluation (LLM-based) | Depends on episode recorder; ADR-012 pending |
| Serf gossip mesh (multi-node gateway) | Single-node is sufficient for first customers |
| Scheduled tasks scanner | Low signal — deferred |
| Central agent remote-kill | Nice to have, not blocking |

---

## Weekly Cadence

Each Friday: update `PROJECT-STATUS.md` with what shipped, what's blocked, what's next.

Each Monday: PM-EAMI reviews status and issues new tasks for the week.
