# Changelog

All notable changes to EAMI are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

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
