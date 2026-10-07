# Changelog

All notable changes to EAMI are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## eami-gateway and eami-api — 2026-10-06 (B-301, B-302)

### Changed
- **Suspending or revoking a governed agent now stops it on its next call**, including on an MCP session it already had open, on every gateway node, and for every workflow step. Each call checks the agent's status and its token against the database; the call is refused and audited as denied, and the session ends.
- **Revoking an API key also revokes every live token issued with it**, recorded in the admin audit trail. Tokens now carry their key's id and the gateway refuses any token whose key is revoked, on every call. If the revocation can't be recorded completely, nothing is revoked and the request fails with `key_revocation_failed`.
- **Tokens are bound to the exact governed agent** they were issued for (`agent_uuid`, `api_key_id` claims), so deleting an agent and re-creating one with the same name never revives old tokens.
- **Every governed-agent route checks liveness against the database:** tool calls, `tools/list`, new MCP sessions and episode reads.
- **An approved escalation is re-checked before it runs.** If the governed agent was suspended or revoked while the call waited for approval, it doesn't run (`resume_outcome = agent_not_active`).
- **Gateway 401 and 403 responses use fixed text** and no longer reveal whether a governed agent exists or is suspended (B-302).

### Upgrade and deploy
- **Run migration 000030 first** (it widens `approval_requests.resume_outcome`), then deploy eami-api, then eami-gateway.
- **Open MCP sessions end at deploy.** Sessions live in the gateway process's memory, so restarting the gateway closes every open SSE session.
- **Every token issued before the deploy is refused** (it has no `agent_uuid` claim): agents must request a new token from `POST /v1/gateway/tokens` with their API key, then reconnect. A suspended or revoked agent can't get one.
- **A database error refuses the call and ends the session** (fail closed): during a database outage, agents reconnect once it's back.
- **In-flight calls:** a call that already passed the check when a suspension lands runs to completion: at most **30 seconds** (the downstream timeout of the static forward, tool router and Claude adapter).
- MCP clients see a refused call as JSON-RPC error `-32001` with message `unauthorized: session ended`, and the end-of-session SSE event now reads `session ended` (was `session expired`).

---

## eami-agent 1.3.2 — 2026-10-06 (B-269 Slice 0)

### Changed
- **Configured model scan paths count only model files.** The extensions are `.gguf`, `.ggml`, `.safetensors`, `.bin`, `.pt`, `.pth`, `.ckpt`, `.onnx`, `.tflite`, `.h5`, `.keras`, `.pb`, `.mlmodel` and `.llamafile`. Hits are labelled **"Configured path"** (they used to show as "LM Studio"). `.bin`, `.pb` and `.h5` can still match non-model files.
- **Scans of configured paths go at most 8 folders deep**, and stop at the scan deadline. When the depth limit cuts a scan short, Endpoint Detail says so.
- **Root paths are refused, and so are links that lead to a root or a network share.** A remote config containing one is refused whole, and the agent keeps its last good config.

### Upgrade note
- **After upgrading, an endpoint's model count may drop.** Large non-model files under configured paths (videos, disk images, archives) are no longer counted as models. That's the fix for over-collection (B-194), not lost data.
- Endpoints still configured with whole-profile paths (`/home`, `/Users`, `C:\Users`) will see the biggest drop. Cleaning those configs is tracked as B-296.
- Agents older than 1.3.0 can't have paths removed remotely, and keep listing any large file under their configured paths until they update.

---

## v1.0.0 — 2026-07-01

First customer release.

### What's new

**MCP Gateway**
- Proxy layer intercepts every MCP tool call between AI agents and MCP servers
- Policy engine: allow / deny / escalate rules evaluated per tool call
- Append-only audit log with hash chain for tamper detection
- Approval workflow: escalated calls routed to human reviewers via Slack webhook
- JWT-signed agent tokens (RS256) with in-memory revocation list
- pprof endpoint for production profiling (opt-in via `GATEWAY_PPROF_ADDR`)
- Serf gossip mesh for multi-node deployments

**Endpoint Discovery Agent**
- Detects AI applications, local models, MCP servers, browser extensions, GPU state, Python/Node environments, and cloud CLI credentials
- Reports to the on-prem EAMI collector on a configurable interval (default 5 minutes)
- Statically linked binary — no runtime dependencies
- Installer packages for all major platforms (see below)

**Web UI**
- Dashboard: organisation-wide risk and activity summary
- Discover: per-endpoint AI asset inventory
- FinOps: real token spend tracking per agent per model
- Gateway: agent registration, policy editor, token management
- Approvals: review queue for escalated tool calls
- Audit: searchable append-only event log
- Alerts: configurable threshold rules with Slack notifications
- Settings: org profile, notification config, user management

**Infrastructure**
- PostgreSQL 16 with TimescaleDB (time-series metrics) and pgvector (AI embeddings)
- Partitioned `audit_log` table with monthly partitions
- Single-command on-prem setup: `./scripts/setup.sh` (Linux + macOS 14+)

### Agent installers

| Platform | Artifact | Install method |
|----------|----------|----------------|
| Windows 11 / Server 2022 | `eami-agent-1.0.0-windows-amd64.msi` | MSI silent install / Group Policy |
| macOS 14+ Intel | `eami-agent-1.0.0-darwin-amd64.pkg` | `installer -pkg` / MDM |
| macOS 14+ Apple Silicon | `eami-agent-1.0.0-darwin-arm64.pkg` | `installer -pkg` / MDM |
| Ubuntu 20.04+ / Debian 11+ | `eami-agent_1.0.0_amd64.deb` | `dpkg -i` / apt |
| RHEL 8+ / Rocky 8+ / AlmaLinux 8+ | `eami-agent-1.0.0-1.x86_64.rpm` | `rpm -i` / dnf |

### Requirements

- Docker 24+ and Docker Compose v2
- Linux (amd64) or macOS 14+ server — Windows Server via WSL2
- PostgreSQL 16 with TimescaleDB + pgvector (included in docker-compose)
- 2 GB RAM minimum for the full stack; 4 GB recommended

### Known issues and deferred items

- **JWT-002 (High):** Revocation list is in-memory — tokens become valid again after gateway restart. Workaround: set short token TTL (`GATEWAY_JWT_TTL`). Fix scheduled post-v1.0 (TASK-062).
- **TASK-054 Tier B:** End-to-end agent → Discover UI smoke test deferred; requires collector ingest implementation (TASK-035).
- **TASK-050:** Gateway load test deferred to post-v1.0.
- macOS agent smoke test deferred (no Mac runner available in CI environment).
