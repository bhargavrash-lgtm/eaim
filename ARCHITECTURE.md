# EAMI — Enterprise AI Monitoring & Intelligence
## System Architecture Document
**Version:** 0.1.0  
**Status:** Living document — update on every significant design change  
**Owner:** Architect-EAMI agent  
**Last updated:** 2026-05-31

---

## 1. Product Overview

EAMI is an enterprise AI governance platform with two distinct planes:

| Plane | What it does | Who cares |
|---|---|---|
| **Data plane** | Discovers AI activity on endpoints (apps, models, MCP servers, GPU, Python/Node envs) | CIO, IT |
| **Control plane** | Governs all AI agent actions through a policy-enforcing MCP gateway cluster | CISO, Compliance |

The two planes share a common backend but are operationally independent. The data plane can be deployed alone (discovery-only mode). The control plane requires the gateway cluster.

---

## 2. High-Level Component Map

```
┌─────────────────────────────────────────────────────────┐
│                    AI Agents (any)                      │
│   Claude · GPT · Cursor · Custom · Local LLMs           │
└────────────────────────┬────────────────────────────────┘
                         │ MCP protocol (all traffic)
                         ▼
┌─────────────────────────────────────────────────────────┐
│              MCP Gateway Cluster  [ON-PREM]             │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │  Primary     │  │  Edge nodes  │  │  DR standby  │  │
│  │  (server)    │  │  (laptops)   │  │  (server)    │  │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  │
│         └─────────────────┴──────────────────┘          │
│                    gossip mesh (Serf)                   │
│                                                         │
│  ┌─────────────┐  ┌─────────────┐  ┌────────────────┐  │
│  │Policy engine│  │Intent scorer│  │Approval router │  │
│  └─────────────┘  └─────────────┘  └────────────────┘  │
│  ┌─────────────┐  ┌─────────────┐  ┌────────────────┐  │
│  │AI token mgr │  │Audit writer │  │Episode recorder│  │
│  └─────────────┘  └─────────────┘  └────────────────┘  │
└────────────────────────┬────────────────────────────────┘
                         │ verified, policy-checked
                         ▼
┌─────────────────────────────────────────────────────────┐
│              Connected Systems                          │
│  Salesforce · GitHub · Jira · PostgreSQL · SAP · etc.   │
│  (connected via MCP adapter, REST API, or DB driver)    │
└─────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────┐
│         Endpoint Discovery Agent  [ON-PREM, Windows]    │
│  Scans: AI processes, apps, local models, MCP servers,  │
│  GPU, Python envs, Node AI, browser, cloud clients      │
└────────────────────────┬────────────────────────────────┘
                         │ HTTPS + API key (compressed JSON)
                         ▼
┌─────────────────────────────────────────────────────────┐
│           On-Prem Data Collector  [ON-PREM]             │
│  Receives agent reports, validates, normalises, buffers  │
└────────────────────────┬────────────────────────────────┘
                         │ HTTPS (batched, encrypted)
                         ▼
┌─────────────────────────────────────────────────────────┐
│              EAMI SaaS Backend  [CLOUD]                 │
│  REST API · PostgreSQL · pgvector · TimescaleDB         │
│  Analytics engine · Alert engine · Notification svc     │
└────────────────────────┬────────────────────────────────┘
                         │ REST API
                         ▼
┌─────────────────────────────────────────────────────────┐
│              EAMI Web UI  [CLOUD / SELF-HOST]           │
│  React + TypeScript · Vite · TanStack Query             │
│  Dashboard · Gateway config · Approvals · FinOps        │
└─────────────────────────────────────────────────────────┘
```

---

## 3. Services & Repositories

### 3.1 eami-agent  (`/eami-agent`)
**Language:** Go 1.25  
**Purpose:** Lightweight Windows endpoint agent. Runs as a Windows Service. Scans at configurable intervals, ships JSON reports to the collector.  
**Key packages:**
- `internal/detection/*` — one package per detection domain
- `internal/payload` — assembles the full report struct
- `internal/collector` — HTTP sender with retry/backoff
- `internal/config` — YAML config loader
- `cmd/agent` — main entrypoint + Windows service wiring

**Binary:** single static Go binary, ~12 MB. No runtime dependencies.

---

### 3.2 eami-collector  (`/eami-collector`)
**Language:** Go 1.25  
**Purpose:** On-prem HTTP server. Receives reports from endpoint agents, validates schema, normalises fields, buffers to local SQLite, then forwards batches to the SaaS backend.  
**Key packages:**
- `internal/ingest` — HTTP handler, validation, normalisation
- `internal/buffer` — SQLite write-ahead buffer (survives network outages)
- `internal/forwarder` — batch sender to SaaS with exponential backoff
- `internal/config` — YAML config (listen port, SaaS URL, API key, TLS)
- `cmd/collector` — main entrypoint

**Deployment:** single binary or Docker container, on-prem server or VM.

---

### 3.3 eami-gateway  (`/eami-gateway`)
**Language:** Go 1.25  
**Purpose:** The MCP control plane. Intercepts all MCP traffic between AI agents and downstream tools. Enforces policies, scores risk, routes approvals, records episodes, writes audit log.  
**Key packages:**
- `internal/mcp` — MCP protocol handler (stdio + SSE transports)
- `internal/identity` — agent registration, AI token issuance/validation
- `internal/policy` — policy engine (rule evaluation, semantic scoring via LLM call)
- `internal/risk` — blast radius estimator
- `internal/approval` — approval router (Slack/Teams/email webhooks)
- `internal/audit` — immutable audit writer (append-only Postgres table)
- `internal/episode` — episode recorder (writes to vector DB)
- `internal/node` — Serf-based gossip mesh, node health, leader election
- `internal/proxy` — downstream tool proxy (MCP, HTTP, DB)
- `cmd/gateway` — main entrypoint

**Deployment:** one or more on-prem nodes. Primary node(s) on servers, edge nodes optional on desktops/laptops.

---

### 3.4 eami-policy  (`/eami-policy`)
**Language:** Go 1.25  
**Purpose:** Shared policy engine library. Imported by eami-gateway. Evaluates policy rules against action context. Supports both structural rules (JSON-match) and semantic rules (LLM-evaluated).  
**Note:** This is a library (`package policy`), not a standalone service.

---

### 3.5 eami-api  (`/eami-api`)
**Language:** Go 1.25  
**Purpose:** SaaS REST API backend. Receives forwarded data from collectors, serves the web UI, handles auth, analytics queries, alert rules, notification dispatch.  
**Key packages:**
- `internal/api` — HTTP router (Chi), middleware, handlers
- `internal/auth` — JWT auth, org/user management, API key validation
- `internal/analytics` — token spend queries, trend aggregation
- `internal/alerting` — alert rule evaluation, notification dispatch
- `internal/store` — PostgreSQL repository layer (sqlc-generated)
- `internal/vector` — pgvector queries for episode retrieval
- `cmd/api` — main entrypoint

**Database:** PostgreSQL 16 + pgvector extension + TimescaleDB extension

---

### 3.6 eami-ui  (`/eami-ui`)
**Language:** TypeScript + React 18  
**Bundler:** Vite 5  
**Purpose:** Web UI. Single-page app served by the SaaS backend (or self-hosted). Connects to eami-api via REST.  
**Key directories:**
- `src/pages/` — one directory per nav section (dashboard, discover, gateway, approvals, finops, memory, audit, settings)
- `src/components/` — shared UI components
- `src/api/` — typed API client (generated from OpenAPI spec via openapi-typescript)
- `src/stores/` — Zustand global state
- `src/hooks/` — TanStack Query hooks per resource

---

## 4. Technology Stack

| Layer | Technology | Rationale |
|---|---|---|
| Backend language | Go 1.25 | Single static binary, low memory, excellent concurrency for gateway |
| Frontend | React 18 + TypeScript | Type safety, ecosystem, TanStack Query for server state |
| Primary DB | PostgreSQL 16 | ACID, mature, pgvector for episode embeddings |
| Time-series | TimescaleDB extension | Token spend trends without a separate TSDB |
| Vector search | pgvector | Keeps episode memory in Postgres, avoids separate vector DB infra |
| Local buffer | SQLite (collector) | Zero-dependency buffer for offline/outage resilience |
| API style | REST + JSON (OpenAPI 3.1) | Broad client compatibility, easy to generate typed clients |
| Auth | JWT (RS256) + short-lived AI tokens | Standard for SaaS; AI tokens are a custom claim extension |
| Gateway mesh | Serf (HashiCorp) | Gossip protocol for node health; runs embedded in gateway binary |
| MCP transport | stdio + SSE | Standard MCP transports; gateway acts as transparent proxy |
| Containerisation | Docker + docker-compose | Single `docker-compose up` for full stack |
| CI | GitHub Actions | Standard, free for open-source, matrix builds for Go/Node |

---

## 5. Data Flows

### 5.1 Discovery flow (endpoint → SaaS)
```
1. eami-agent wakes on interval (default 5 min)
2. Runs all detection scans in parallel goroutines
3. Assembles Report{} struct
4. Sends POST /ingest to eami-collector (HTTPS, API key auth, gzipped JSON)
5. eami-collector validates schema, writes to SQLite buffer
6. Forwarder goroutine batches and sends to eami-api POST /v1/ingest/batch (HTTPS, org API key)
7. eami-api normalises, upserts endpoint record, appends to event log
8. UI queries eami-api GET /v1/endpoints for discover view
```

### 5.2 Agent action flow (AI agent → tool via gateway)
```
1. AI agent connects to eami-gateway MCP endpoint (stdio or SSE)
2. Gateway checks agent identity — validates AI token (JWT)
3. Agent sends tool_call request (e.g. salesforce.delete_records)
4. Gateway extracts: agent_id, tool, action, parameters
5. Policy engine evaluates rules in priority order:
   a. Structural rules (fast, no LLM): match on agent/tool/action/param patterns
   b. Semantic rules (LLM call if needed): evaluate intent against policy statement
6. Decision: ALLOW → proxy to downstream | DENY → return error + audit | ESCALATE → hold
7. If ESCALATE: create approval_request, notify approver via Slack/Teams/email
8. Approver clicks approve/deny → webhook → gateway resumes or cancels
9. Episode recorder writes full episode (request + reasoning + decision + outcome) to vector DB
10. Audit writer appends immutable record to audit_log table
11. Token usage recorded for FinOps
```

### 5.3 Episode retrieval flow (memory)
```
1. New agent session begins with a task description
2. Gateway embeds task description → vector
3. pgvector similarity search against episodes table
4. Top-N similar episodes returned to agent as context
5. Agent can replicate prior successful action patterns
```

---

## 6. Security Model

### 6.1 Authentication layers

| Layer | Mechanism | Scope |
|---|---|---|
| Endpoint agent → collector | Static API key (per-agent, enrolled at install) | Write-only ingest |
| Collector → SaaS API | Org-scoped API key (rotatable) | Ingest batch only |
| UI users → SaaS API | JWT (RS256, 1-hour TTL) + refresh token | Full UI scope |
| AI agents → gateway | AI Token (JWT, RS256, short-lived, task-scoped) | Defined at registration |
| Gateway → downstream tools | Per-tool credentials (OAuth tokens, API keys, DB passwords) stored encrypted in gateway config | Tool-specific |

### 6.2 AI Token design
AI Tokens are JWTs with custom claims:
```json
{
  "sub": "agent:claude-support-01",
  "iss": "eami-gateway:primary-01",
  "aud": "eami-gateway",
  "iat": 1748736000,
  "exp": 1748736900,
  "scope": "read:crm reply:email",
  "task": "Triage and reply to support tickets",
  "model": "claude-sonnet-4-6",
  "owner": "support-team",
  "risk_tier": "low"
}
```
- TTL: configurable per agent (default 15 min, max 4 hr)
- Revocable: gateway maintains an in-memory revocation list (synced across nodes via gossip)
- Scope drift: if agent attempts action outside declared scope → policy escalation

### 6.3 Data sovereignty
- Prompt content never leaves the on-prem gateway
- Only metadata (agent_id, tool, action type, token counts, decision, timestamp) flows to SaaS
- Episode full content stored in on-prem vector DB (pgvector on same host as gateway primary, or separate on-prem Postgres)
- SaaS never receives raw prompts or response content

### 6.4 Audit log integrity
- Audit log rows are append-only (Postgres row-level security + no DELETE/UPDATE grants)
- Each row includes a SHA-256 hash chained from the previous row (hash of prev_hash + row content)
- Verifiable by auditors without trusting EAMI: export CSV, recompute hashes

---

## 7. Gateway Node Architecture

### 7.1 Node roles

| Role | Description | Minimum count |
|---|---|---|
| Primary | Full policy engine, audit writer, approval router. Handles all traffic by default. | 1 |
| Edge | Lightweight relay. Caches current policy set. Routes to primary. Activates as failover if primary unreachable. | 0 (optional) |
| DR standby | Full replica of primary. Passive. Activates if primary unreachable > 10s. Policy + token store replicated. | 0 (recommended 1) |

### 7.2 Gossip mesh (Serf)
- All nodes run embedded Serf agent
- Serf handles: node discovery, health checks, failure detection, custom event propagation
- Policy updates broadcast as Serf custom events → all nodes update their in-memory policy cache within 500ms
- Token revocations broadcast the same way

### 7.3 Edge node design
- Runs as a lightweight Go binary on existing Windows laptops/desktops
- Memory footprint: < 30 MB
- CPU: idle < 0.1%
- State: policy cache (read-only copy), own identity cert, primary addresses
- No sensitive data (credentials, episode content) stored on edge nodes
- If laptop lost/stolen: revoke edge node cert → gossip broadcasts → node excluded from mesh within 10s

### 7.4 Failover sequence
```
1. Primary node stops responding to gossip heartbeat
2. Serf detects failure after 3 missed heartbeats (~3s)
3. DR standby node wins leader election (Raft-lite within Serf)
4. DR promotes to primary, begins accepting traffic
5. Edge nodes reroute to new primary
6. Audit log replication lag < 1s (Postgres streaming replication)
7. Total failover time: < 15 seconds
```

---

## 8. Deployment Topology

### 8.1 Minimum viable deployment (single org, proof of concept)
```
1 server (on-prem):
  - eami-collector     (Docker container)
  - eami-gateway       (Docker container, primary node)
  - PostgreSQL 16      (Docker container, with pgvector + TimescaleDB)

N Windows endpoints:
  - eami-agent         (Windows Service, installed via MSI or Ansible)

Cloud (EAMI-hosted SaaS):
  - eami-api
  - eami-ui
```

### 8.2 Production deployment (recommended)
```
Server A (on-prem, primary):
  - eami-gateway primary
  - eami-collector
  - PostgreSQL (primary)

Server B (on-prem, DR):
  - eami-gateway DR standby
  - PostgreSQL replica (streaming replication from A)

Laptops/desktops (optional edge nodes):
  - eami-gateway edge (lightweight, auto-enrolled)

Cloud:
  - eami-api (Kubernetes or single VM)
  - eami-ui  (static, CDN)
  - PostgreSQL analytics DB (separate from on-prem)
```

---

## 9. API Surface Summary

Full spec: `api/openapi.yaml`

| Group | Base path | Notes |
|---|---|---|
| Ingest | `/v1/ingest` | Collector → SaaS. API key auth. |
| Endpoints | `/v1/endpoints` | Discovered endpoint inventory |
| Agents | `/v1/gateway/agents` | AI agent registry |
| Policies | `/v1/gateway/policies` | Policy CRUD |
| Tools | `/v1/gateway/tools` | Tool connection management |
| Nodes | `/v1/gateway/nodes` | Node health + config |
| Approvals | `/v1/approvals` | Approval queue, webhook |
| Audit | `/v1/audit` | Audit log query |
| FinOps | `/v1/finops` | Token spend, ROI |
| Memory | `/v1/memory/episodes` | Episode library |
| Alerts | `/v1/alerts` | Alert rules + history |
| Auth | `/v1/auth` | Login, token refresh, API keys |

---

## 10. Frontend Architecture

### 10.1 Page structure
```
/                    → redirect to /dashboard
/dashboard           → KPIs, active sessions, recent alerts
/discover            → endpoint inventory, AI asset map
/gateway/agents      → agent registry
/gateway/policies    → policy rule builder
/gateway/tools       → tool connection manager
/gateway/nodes       → node cluster view
/approvals           → approval queue
/finops              → token spend, ROI dashboard
/memory              → episode library, knowledge base
/audit               → audit log with filters
/settings            → org settings, SSO, notifications
```

### 10.2 State management
- **Server state:** TanStack Query (React Query) — all API data. 30s stale time for most resources, 5s for approvals (near-realtime).
- **Global UI state:** Zustand — current org, user, theme, sidebar state.
- **Form state:** React Hook Form + Zod validation.

### 10.3 Real-time updates
- Approvals and active sessions use polling (5s interval) for MVP.
- V2: WebSocket or Server-Sent Events from eami-api for push notifications.

### 10.4 API client
- Generated from `api/openapi.yaml` using `openapi-typescript` + `openapi-fetch`.
- Never write raw `fetch()` calls in components — always use generated client.
- All types derived from the spec; no hand-written API types.

---

## 11. Non-Functional Requirements

| Requirement | Target | Notes |
|---|---|---|
| Gateway latency overhead | < 50ms p99 per request | Structural policy eval is sync; semantic eval is async with timeout |
| Gateway availability | 99.9% (with DR node) | Failover < 15s |
| Agent ingest throughput | 10,000 reports/hour | Per collector instance |
| Audit log write | < 10ms per entry | Append-only, no index contention |
| Episode retrieval | < 200ms p95 | pgvector HNSW index |
| UI initial load | < 2s on 10 Mbps | Code-split by route |
| Binary size (agent) | < 20 MB | No CGO, static binary |
| Edge node memory | < 30 MB resident | Policy cache is bounded |

---

## 12. Open Questions (to resolve before v1)

- [ ] Semantic policy evaluation: use local small model or call external LLM API? (latency vs. privacy trade-off)
- [ ] Episode embedding model: sentence-transformers local vs. API? (same trade-off)
- [ ] Approval notification: Slack webhook (MVP) or full Slack app with interactive buttons?
- [ ] Multi-tenancy model for SaaS: org-per-schema or row-level-security?
- [ ] Windows agent packaging: MSI installer or NSIS or Ansible role?
- [ ] macOS/Linux agent: post-v1, what is the priority?
