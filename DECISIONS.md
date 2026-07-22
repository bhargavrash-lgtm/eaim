# EAMI — Architectural Decision Log
**Format:** ADR (Architecture Decision Record)  
**Owner:** PM-EAMI (writes) · Architect-EAMI (reviews)  
**Rule:** Every significant technical decision that affects more than one agent must be recorded here before implementation begins.

---

## ADR-001 — Use Go for all backend services

**Date:** 2026-05-31  
**Status:** Accepted  
**Deciders:** PM-EAMI, Architect-EAMI

**Context:**  
Backend services include a Windows endpoint agent, an on-prem collector, an MCP gateway, a policy library, and a SaaS REST API. These span constrained (endpoint agent) to high-concurrency (gateway) workloads.

**Decision:**  
Use Go 1.25 for all backend services.

**Rationale:**
- Single static binary with zero runtime dependencies — critical for the Windows endpoint agent (no installer prerequisites)
- Excellent concurrency model (goroutines) for the gateway's request-interception workload
- Low memory footprint for edge nodes running on laptops
- Cross-compilation to Windows from Linux CI is first-class
- Strong standard library reduces dependency count

**Consequences:**
- All BE agents must know Go. No Python or Node microservices.
- CGO is forbidden (breaks cross-compilation). Use pure-Go alternatives.
- `go vet` and `staticcheck` run in CI on every PR.

---

## ADR-002 — PostgreSQL as the primary database with pgvector and TimescaleDB

**Date:** 2026-05-31  
**Status:** Accepted  
**Deciders:** Architect-EAMI, PM-EAMI

**Context:**  
EAMI needs: relational storage (agents, policies, audit), time-series (token spend trends), and vector similarity search (episode memory retrieval).

**Decision:**  
Use a single PostgreSQL 16 instance with the pgvector and TimescaleDB extensions for all three workloads.

**Rationale:**
- Eliminates two additional infrastructure components (dedicated TSDB + vector DB)
- pgvector HNSW index provides <200ms p95 similarity search at expected episode volumes (<1M rows)
- TimescaleDB hypertables handle token spend time-series with automatic partitioning
- Single backup strategy, single operational model
- Fits in a single Docker container for MVP

**Consequences:**
- schema.sql must declare pgvector and TimescaleDB extension activation
- episode embeddings column type: `vector(1536)` (OpenAI text-embedding-3-small dimensions)
- If episode volume exceeds 10M rows, revisit dedicated vector DB (Weaviate or Qdrant)
- All migrations via numbered SQL files in `schema/migrations/`

---

## ADR-003 — SQLite as the local buffer in eami-collector

**Date:** 2026-05-31  
**Status:** Accepted  
**Deciders:** Architect-EAMI

**Context:**  
The on-prem collector receives reports from endpoint agents and forwards them to the SaaS API. If the SaaS API is unreachable (network outage, maintenance), reports must not be lost.

**Decision:**  
eami-collector maintains a SQLite WAL-mode database as a write-ahead buffer. Reports are written to SQLite first, then forwarded to SaaS, then deleted from SQLite on confirmation.

**Rationale:**
- Zero additional infrastructure (no Redis, no Kafka)
- SQLite WAL mode gives concurrent reads and writes without blocking
- Survives process restart (reports persist on disk)
- Simple to reason about: if SQLite has rows, they haven't been confirmed by SaaS yet

**Consequences:**
- SQLite file path must be configurable (default: `./data/buffer.db`)
- Forwarder runs in a dedicated goroutine, polling SQLite every 10s
- Max buffer size: 100,000 reports (~500 MB). If exceeded, oldest reports dropped with an alert.
- BE-Collector owns the SQLite schema in `eami-collector/migrations/`

---

## ADR-004 — MCP gateway is a transparent proxy, not a sidecar

**Date:** 2026-05-31  
**Status:** Accepted  
**Deciders:** Architect-EAMI, PM-EAMI

**Context:**  
Two patterns exist for intercepting AI agent traffic: (a) sidecar proxy injected per-agent, or (b) central proxy that all agents connect to.

**Decision:**  
Use a central MCP gateway cluster. AI agents are configured to connect to the gateway's MCP endpoint instead of directly to downstream tools.

**Rationale:**
- Sidecar requires modifying every agent's runtime environment — high adoption friction
- Central proxy means one configuration change per agent (point to gateway instead of tool)
- Enables cluster-level policy consistency (all agents see the same policy set)
- Enables cross-agent analytics (single view of all agent activity)

**Consequences:**
- Agents must be reconfigured to point to gateway. This is a deployment step, not automatic.
- Gateway must support all MCP transports used by agents: stdio and SSE (HTTP-based).
- Gateway address must be documented clearly in the agent onboarding guide.

---

## ADR-005 — Serf (gossip protocol) for gateway node coordination

**Date:** 2026-05-31  
**Status:** Accepted  
**Deciders:** Architect-EAMI

**Context:**  
The gateway cluster needs node health monitoring, failure detection, and policy update propagation across primary, edge, and DR nodes.

**Decision:**  
Embed HashiCorp's Serf library in the gateway binary for gossip-based node coordination.

**Rationale:**
- Serf is a pure Go library — embeds directly, no additional binary
- Gossip protocol is resilient to partial network failures (no single coordinator)
- Custom events via Serf handle policy broadcast (<500ms propagation)
- Used in production by Consul, Nomad — battle-tested
- Zero external dependencies for the coordination layer

**Consequences:**
- Each gateway node runs an embedded Serf agent on a configurable port (default 7946)
- Serf port must be open between all gateway nodes on the internal network
- BE-Gateway owns `internal/node/` which wraps the Serf library

---

## ADR-006 — AI Tokens are JWTs with custom claims, signed RS256

**Date:** 2026-05-31  
**Status:** Accepted  
**Deciders:** Architect-EAMI, PM-EAMI

**Context:**  
AI agents need an identity mechanism that: (a) is verifiable by the gateway, (b) encodes the agent's declared task and scope, (c) expires, and (d) can be revoked mid-session.

**Decision:**  
Issue short-lived JWTs signed with RS256 (RSA private key on gateway, public key for verification). Include custom claims: `scope`, `task`, `model`, `owner`, `risk_tier`.

**Rationale:**
- JWTs are stateless and verifiable without a database round-trip (fast path)
- RS256 means the signing key never leaves the gateway cluster
- Custom claims carry task context without a separate lookup
- Revocation handled via in-memory revocation list broadcast via Serf (acceptable for short TTLs)

**Consequences:**
- Gateway generates an RSA keypair on first start, stores private key encrypted on disk
- Public key exposed at `/.well-known/gateway-jwks.json` for external verification
- Revocation list cleared on node restart (acceptable — tokens are short-lived)
- AI token issuance endpoint: `POST /v1/gateway/tokens` (gateway-local, not SaaS API)

---

## ADR-007 — Audit log is append-only with hash chaining

**Date:** 2026-05-31  
**Status:** Accepted  
**Deciders:** Architect-EAMI, PM-EAMI

**Context:**  
Audit logs are a compliance requirement. Customers need to demonstrate to auditors that the logs have not been tampered with.

**Decision:**  
Audit log table has no UPDATE or DELETE grants. Each row includes a `prev_hash` field (SHA-256 of the previous row's content + hash). Auditors can verify the chain by re-computing hashes.

**Rationale:**
- Simple to implement, simple to audit
- Does not require a blockchain or external notary
- Compatible with standard PostgreSQL tooling
- Export to CSV + hash verification script ships with EAMI

**Consequences:**
- `audit_log` table: `ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY` + policy denying DELETE/UPDATE to app user
- First row in a new deployment has `prev_hash = SHA256("genesis")`
- Hash verification script: `scripts/verify-audit-log.sh`
- BE-Gateway owns the audit writer; Architect-EAMI owns the schema

---

## ADR-008 — Frontend uses React + TypeScript + Vite + TanStack Query

**Date:** 2026-05-31  
**Status:** Accepted  
**Deciders:** PM-EAMI

**Context:**  
The web UI needs to be maintainable across three FE agents working on separate pages simultaneously.

**Decision:**  
React 18 with TypeScript, bundled by Vite 5. Server state managed by TanStack Query (React Query). Global UI state by Zustand. Forms by React Hook Form + Zod. API client generated from OpenAPI spec by openapi-typescript.

**Rationale:**
- TypeScript enforces API contracts between agents — type errors at compile time, not runtime
- TanStack Query standardises data fetching, caching, and refetching across all pages
- Generated API client means FE agents never hand-write fetch calls — types come from the spec
- Vite is significantly faster than webpack for development
- Zustand is minimal and avoids Redux boilerplate

**Consequences:**
- All FE agents must run `npm run generate-client` after the OpenAPI spec changes before writing page code
- No raw `fetch()` or `axios` calls anywhere in `src/` — only the generated client
- `tsconfig.json` set to strict mode — no `any` types permitted

---

## ADR-009 — Semantic policy evaluation calls an LLM; structural rules are evaluated in-process

**Date:** 2026-05-31  
**Status:** Accepted (LLM endpoint TBD — see open question)  
**Deciders:** Architect-EAMI, PM-EAMI

**Context:**  
Policy rules can be structural (e.g., "action == delete AND env == production") or semantic (e.g., "agent must not exfiltrate customer data"). Structural rules can be evaluated with fast JSON matching. Semantic rules require natural language understanding.

**Decision:**  
Structural rules evaluated synchronously in-process (no LLM call, <1ms). Semantic rules trigger an async LLM call with a 2-second timeout. If the LLM call times out, semantic rules default to ESCALATE (safe default).

**Open question:** Which LLM? Options:
- Local: run a small model (Phi-3-mini or similar) on the gateway server. Pro: no external call, data stays on-prem. Con: requires GPU or is slow.
- API: call OpenAI/Anthropic. Pro: high quality. Con: prompt content leaves on-prem network (privacy concern for some customers).
- Decision deferred until pilot customer feedback on privacy requirements.

**Consequences:**
- `eami-policy/semantic.go` must accept a configurable LLM endpoint (local or API)
- Gateway config must include `policy.semantic_llm_endpoint` and `policy.semantic_llm_api_key`
- Default config ships with semantic evaluation disabled (structural rules only)

---

## ADR-010 — Data sovereignty: prompt content never reaches SaaS

**Date:** 2026-05-31  
**Status:** Accepted  
**Deciders:** PM-EAMI

**Context:**  
Enterprises will not purchase a product where their AI prompts and responses flow through a vendor's cloud.

**Decision:**  
The SaaS backend (eami-api) never receives raw prompt content or agent response content. It only receives metadata: agent_id, tool_name, action_type, token_count_in, token_count_out, decision, latency_ms, timestamp, org_id.

**Consequences:**
- Episode full content (prompt + response + reasoning) stored exclusively in the on-prem PostgreSQL instance (accessed by eami-gateway)
- SaaS receives only episode_id (UUID), not episode content
- FinOps dashboards work from token counts, not content
- All FE agents must understand: episode content queries go to the on-prem gateway API, not the SaaS API

---

## ADR-011 — MVP approval notifications via webhook (Slack first)

**Date:** 2026-05-31  
**Status:** Accepted  
**Deciders:** PM-EAMI

**Context:**  
Approval requests need to reach humans quickly. Full Slack app (with interactive buttons) requires Slack app registration and OAuth. Incoming webhook is simpler.

**Decision:**  
MVP: Slack incoming webhook. Approver clicks a link in the Slack message that opens the EAMI web UI approval screen. No Slack interactive buttons in MVP.

**V2:** Full Slack app with interactive Approve/Deny buttons directly in the Slack message.

**Consequences:**
- `eami-gateway/internal/approval/templates/slack.go` generates a webhook message with a link to `https://app.eami.io/approvals/{id}`
- Webhook URL configured in gateway config under `approval.slack_webhook_url`
- Email notification is the fallback if Slack is not configured

---

## ADR-014 — Windows agent packaged as MSI; on-prem stack deployed via first-run setup script

**Date:** 2026-06-03
**Status:** Accepted
**Deciders:** PM-EAMI

**Context:**
Enterprise IT teams need a deployment experience that requires minimal manual configuration. The current state — a raw Go binary and a hand-edited YAML config — is not acceptable for a pilot customer. Two things need to work without hand-holding: installing the Windows endpoint agent on N machines, and standing up the on-prem server stack.

**Decision:**
1. **Windows agent:** Package as an MSI installer (built with WiX Toolset). The MSI accepts two install-time parameters: `COLLECTOR_URL` and `COLLECTOR_API_KEY`. These can be passed silently via Group Policy, SCCM/Intune, or a command-line flag (`msiexec /i eami-agent.msi COLLECTOR_URL=https://... COLLECTOR_API_KEY=...`). The agent reads these from registry keys written by the MSI. No manual YAML editing required.

2. **On-prem server stack:** Provide a `scripts/setup.sh` first-run script that generates secrets, writes `.env`, and runs `docker-compose up`. The operator answers three prompts: org name, admin email, admin password. Everything else is auto-generated (DB password, API keys, gateway keypair). Total setup time target: under 5 minutes.

**Rationale:**
- MSI is the enterprise standard for Windows software distribution — IT can push via Group Policy with zero user interaction
- Registry-based config (written by MSI) means no file path issues and survives Windows updates
- First-run script eliminates the most common deployment failure: misconfigured `.env`
- Both approaches are additive — advanced users can still edit YAML/env directly

**Consequences:**
- DevOps-EAMI owns the WiX toolset MSI project (`eami-agent/installer/`)
- DevOps-EAMI owns `scripts/setup.sh`
- The agent's config loader must fall back to registry keys if YAML config fields are empty
- BE-Collector owns the registry fallback in `eami-agent/internal/config/`
- MSI build added to CI: `build.yml` produces `eami-agent-{version}-windows-amd64.msi` as a release artifact

---

## ADR-015 — macOS agent before v1; Linux server agent in parallel; Linux desktop post-v1

**Date:** 2026-06-05
**Status:** Accepted
**Deciders:** PM-EAMI

**Context:**
The endpoint agent is currently Windows-only. Enterprise environments are mixed: Windows dominates business-user machines, but macOS is 40–60% of developer fleets — precisely where MCP servers, local models, and AI coding tools (Cursor, Claude Code) run. Linux servers host shared AI workloads (Ollama, GPU inference, CI/CD agents). A CISO cannot govern what they cannot see.

**Decision:**
1. **macOS** — build before v1. Not post-v1. Developer-heavy orgs cannot be served without it.
2. **Linux server** — build in parallel with macOS. Targets AI workload servers and CI/CD agents. Detection surface is narrower but signals are high-value for CISO.
3. **Linux desktop** — post-v1. Real but low-volume in enterprise today.

**Approach:**
Extend the existing build-tag pattern (`config_windows.go` / `config_other.go`) to per-platform files: `scanner_windows.go`, `scanner_darwin.go`, `scanner_linux.go` per detection package, with a shared interface. Not a rewrite — a port. Detection domains map cleanly across platforms; only the underlying APIs differ.

**Installer packaging per platform:**
- Windows: MSI (done — TASK-010)
- macOS: `.pkg` installer (launchd plist for background service)
- Linux: `.deb` + `.rpm` packages (systemd service unit)

**Rationale:**
- Cloud AI clients scanner: env vars and config files — same on all platforms, trivial to port
- MCP servers scanner: config file paths differ but structure is identical
- Local models scanner: Ollama API is cross-platform; LM Studio and HuggingFace cache paths differ by OS
- Network activity: macOS uses `netstat`-equivalent syscalls; Linux uses `/proc/net/tcp`
- Process enumeration: macOS uses `sysctl` + `kinfo_proc`; Linux uses `/proc/{pid}/`
- GPU: macOS uses `system_profiler`; Linux uses `nvidia-smi` + `/sys/class/drm/`

**Consequences:**
- BE-Collector adds `scanner_darwin.go` and `scanner_linux.go` per detection package
- DevOps-EAMI adds `.pkg` and `.deb`/`.rpm` build targets to CI
- `setup.sh` already works on Linux — macOS support needs a parallel `setup-macos.sh` or detection branch
- ADR-014 MSI work is Windows-only and remains unchanged

---

## ADR-018 — nginx reverse-proxy for eami-ui API calls

**Date:** 2026-07-03  
**Status:** Accepted  
**Owner:** PM-EAMI

**Context:**
The eami-ui React app uses `baseUrl: ''` (relative URLs) so that API calls go to `/v1/...` relative to the page origin. This is correct — it avoids hardcoding an API host. However, the original nginx.conf only served static files and had no proxy block, so any `/v1/` request returned 404. Additionally, Vite's `VITE_API_URL` env var was never passed as a Docker build argument in CI, so the fallback could not be relied upon.

**Decision:**
Add a `location /v1/` proxy block in `eami-ui/nginx.conf` that forwards all API calls to `http://eami-api:8081`. nginx sits in the same Docker network as eami-api, so this is a fast internal proxy with no extra network hop for end users.

**Consequences:**
- No VITE_API_URL build arg needed — the proxy handles routing at runtime
- CORS issues avoided — browser and API share the same origin (port 80)
- Any eami-ui Docker image rebuild must use the updated nginx.conf (already in repo)
- Production deploys must keep eami-ui and eami-api on the same Docker network

---

## ADR-019 — Episode/memory full content stays on-prem; eami-api serves metadata only

**Date:** 2026-07-22  
**Status:** Accepted  
**Deciders:** PM-EAMI

**Context:**  
Formalizes the item previously listed as ADR-019 under Pending Decisions (below) into a full decision record. `eami-api/internal/api/memory.go` was a stub with no clear owner in `BOUNDARIES.md`. Per ADR-010, the SaaS backend (`eami-api`) must never receive full episode content — only metadata. The open question was whether the Memory UI page queries `eami-api` (metadata only) with a separate on-prem gateway endpoint for full episode detail, or whether `eami-api` gets a scoped exception to serve full content directly.

**Decision:**  
Episode/memory full content stays on-prem and is never served by `eami-api`. `eami-api` exposes metadata plus `episode_id` only. Full step content (tool calls, arguments, results) is served by a new on-prem `eami-gateway` endpoint, with dual auth: an `eami-api` service-key (server-to-server) or a future desktop-app Bearer JWT. The UI reaches full episode content via an `eami-api` proxy (server-to-server call from `eami-api` to `eami-gateway`), never by the browser calling `eami-gateway` directly.

**Rationale:**  
"AI activity data never leaves your premises" is a core trust guarantee for a monitoring/governance product. A carve-out exception in `eami-api` would silently break that promise before it's ever made to a customer.

**Consequences:**
- `eami-gateway` — new episode read endpoint delivered (Brief 1, commit `432ce11`, branch `b-002-gateway-episode-endpoint`)
- `eami-api/internal/api/memory.go` — must be rewired to proxy through the gateway endpoint instead of querying `episodes` directly (Brief 2/3, not yet started)
- `eami-ui/src/pages/ops/MemoryPage.tsx` — still calls `eami-api` only once Brief 3 lands; no direct browser-to-gateway path is introduced
- Removes the informal ADR-019 row previously listed under Pending Decisions (below), replaced by this full entry — same number, now formalized rather than reassigned

---

## Pending Decisions

| ID | Question | Owner | Due |
|---|---|---|---|
| ADR-012 | Local vs. API LLM for semantic policy eval | Architect-EAMI | Before policy engine implementation |
| ADR-013 | Multi-tenancy model: org-per-schema vs. RLS | Architect-EAMI | Before eami-api DB design |
| ADR-016 | ✅ Resolved: keep vram_bytes (int64, raw bytes). UI layer formats for display. No breaking change. | PM-EAMI | Closed |
| ADR-017 | Add conda_env_name convenience field to PythonEnv? Currently derivable from last path component. Requires simultaneous Go struct + spec change. | PM-EAMI | Before python_envs UI work |
