# Task: Fix collector ingest handler + Windows agent service lifecycle
**From:** PM-EAMI  
**To:** BE-Collector  
**Priority:** high  
**Blocked by:** none

## What I need

Two related fixes that together make the data plane work end-to-end.

### Part 1 — `eami-collector/internal/api/ingest.go`

The ingest handler is PARTIAL. A POST to `/v1/ingest` from the agent must:

1. Authenticate the request via the API key middleware (already exists in `middleware.go` — wire it up if not already).
2. Decompress the gzip body (agent sends gzip).
3. Parse the JSON payload into the `Report` struct (`internal/models/report.go`).
4. Validate required fields: `agent_id`, `org_id`, `hostname`, `reported_at`.
5. Write the report to the SQLite buffer (`internal/db/db.go`). Use `INSERT INTO reports (...)`.
6. Return `202 Accepted` with `{"status": "accepted"}`.

On any error, return an appropriate HTTP status with a JSON error body. Do not panic.

The forwarder already reads from the SQLite buffer and forwards to the SaaS API — you do not need to touch `forwarder.go`.

### Part 2 — `eami-agent/cmd/agent/main.go` + `internal/service/service.go`

The Windows service wiring is incomplete. Fix it so:

1. When run as a Windows service (`--service install` / `--service start`), the agent registers the Windows Service Control Manager callbacks: Start, Stop, Pause, Continue.
2. On Stop, the agent finishes the current scan tick (does not interrupt mid-scan), then exits cleanly within 10s. If it doesn't finish in 10s, force exit.
3. On startup (whether as a service or CLI), the agent reads config from:
   - YAML file (path from `--config` flag, default `eami-agent.yaml`)
   - Windows registry (`HKLM\SOFTWARE\EAMI\Agent`) as fallback
4. The agent gzips the report payload before sending to the collector.

Do not break the existing detection logic or the sender.

## Context

The Discover page in the UI has zero data because reports never reach the collector's buffer. The forwarder then has nothing to forward to the API. Fixing ingest + the agent service is the single highest-impact fix for v1.0.

## Acceptance criteria

- [ ] `POST /v1/ingest` with a valid API key and well-formed gzip JSON body returns `202`
- [ ] The report appears in the SQLite buffer DB (`SELECT * FROM reports LIMIT 1`)
- [ ] `POST /v1/ingest` with a missing/wrong API key returns `401`
- [ ] `POST /v1/ingest` with a malformed body returns `400`
- [ ] Running `eami-agent.exe --service install && eami-agent.exe --service start` installs and starts the Windows service without error
- [ ] The service appears in `services.msc` as `Running`
- [ ] `eami-agent.exe --service stop` stops the service cleanly (exit 0 within 10s)
- [ ] Reports sent by the agent are gzip-compressed (verify with `Content-Encoding: gzip` header)
- [ ] `go vet ./...` exits 0 in both `eami-collector/` and `eami-agent/`

## Files to create or modify

- `eami-collector/internal/api/ingest.go` — fix the handler
- `eami-collector/internal/api/middleware.go` — verify API key middleware is wired to `/v1/ingest`
- `eami-collector/cmd/collector/main.go` — register the ingest handler on the mux
- `eami-agent/cmd/agent/main.go` — fix service start/stop/pause/continue
- `eami-agent/internal/service/service.go` — implement service lifecycle
- `eami-agent/internal/collector/sender.go` — add gzip compression

## Files to read first

- `eami-collector/internal/db/db.go` — SQLite schema and helpers
- `eami-collector/migrations/001_init.sql` — buffer schema (table definitions)
- `eami-collector/internal/models/report.go` — Report struct
- `eami-collector/internal/forwarder/forwarder.go` — see how it reads from the buffer (replicate the insert pattern)
- `eami-agent/internal/config/config.go` — config struct
