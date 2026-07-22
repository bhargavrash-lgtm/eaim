# CLAUDE.md

## Product
EAMI — **Enterprise AI Monitoring & Intelligence**: an on-prem/SaaS hybrid platform that (a) discovers AI tooling on employee endpoints and (b) governs AI agent tool calls through a policy-enforcing MCP gateway, with audit logging, human-approval escalation, FinOps token-spend tracking, and episode/memory recall.

> Naming note: `pm/PM_PROMPT.md`, `pm/STATUS.md`, `CONTINUATION_PROMPT.md`, and `HANDOFF-B-001.md` describe EAMI as "Enterprise AI **Maturity Index**" and claim a product pivot. No code, `ARCHITECTURE.md`, `ROADMAP.md`, or `CHANGELOG.md` supports that — there was no pivot. This file and `BUILT.md` use the real name. Those four files are out of Code's boundary (owned by PM/founder) and were left untouched.

## Architecture (see `ARCHITECTURE.md` for full detail — still accurate, not legacy)
Five Go services (Go 1.25, one `go.work` workspace) + one React SPA:
- `eami-agent` — Windows/macOS/Linux endpoint scanner (12 detection domains), ships reports to the collector.
- `eami-collector` — on-prem ingest server, SQLite write-ahead buffer, forwards to `eami-api`.
- `eami-gateway` — MCP proxy: policy enforcement, approval routing, audit writing, episode recording, JWT agent identity.
- `eami-policy` — policy library (structural rules real; semantic/LLM rules stubbed, see `BUILT.md`).
- `eami-api` — SaaS REST backend (Chi router), Postgres (pgvector + TimescaleDB), serves `eami-ui`.
- `eami-ui` — React 18 + TS + Vite SPA, TanStack Query, generated OpenAPI client.

Contract: `api/openapi.yaml` (owned by Architect-EAMI per `BOUNDARIES.md`). DB: `schema/schema.sql` + `schema/migrations/001`–`007`.

## Stack
Go 1.25 · PostgreSQL 16 (pgvector, TimescaleDB) · SQLite (collector buffer) · React 18/TS/Vite 5 · TanStack Query · Zustand · React Hook Form + Zod · Chi router · pgx · JWT RS256 · Docker Compose · GitHub Actions.

## Conventions
- **Naming:** Go — standard `internal/<domain>/` package layout, one file per concern; sqlc-style `*.sql.go` in `eami-api/internal/store`. TS — `PascalCase` components, `useX.ts` resource hooks in `src/hooks/`, pages under `src/pages/<section>/`.
- **Error handling (Go):** wrap and return errors up to the HTTP handler layer; handlers call `writeError(w, status, code, msg)`. No panics for expected failures.
- **Frontend API access:** only through the generated client / `apiFetch()` in `src/api/client.ts` — no raw `fetch`/`axios` in components (documented escape hatch exists in `client.ts` for endpoints not yet in the OpenAPI spec).
- **Commit format:** Conventional-commit-ish — `type: summary` or `type(scope): summary`. Types observed: `fix`, `chore`, `wip`. Reference B-IDs in commit messages once B-numbered work starts.
- **Test frameworks:** Go — stdlib `testing`, table-driven. Frontend — **none configured yet** (`tsc --noEmit` / `type-check` only; no vitest/jest/playwright present despite `BOUNDARIES.md` assigning that ownership to QA-EAMI — see `BACKLOG.md`).
- **Toolchain note:** Go is installed on this machine at `C:\Program Files\Go\bin\go.exe` (confirmed `go1.26.5`, 2026-07-22) but is **not on `PATH`** in fresh shell sessions — prefix commands with the full path, or run `$env:PATH = "C:\Program Files\Go\bin;$env:PATH"` (PowerShell) / add it to `PATH` for the session before invoking `go`. Node/npm are still not installed as of this note. Backend build/test status in `BUILT.md` reflects real `go build`/`go test` runs where explicitly marked "Verified" with a date; anything still marked "Not executed" is from static source review only — check the date on each module's note rather than assuming the whole toolchain situation is uniform.

## Hard rules (verbatim)
- At session start, read CONTEXT.md before anything else — before ARCHITECTURE.md, before the task brief. If CONTEXT.md conflicts with any other doc or your own prior assumption, CONTEXT.md wins; flag the conflict, don't silently resolve it. At session end, update CONTEXT.md's Active decision thread and Last updated line — mandatory, same as the BUILT.md update.
- No refactoring outside the assigned task scope; log suggestions in NOTES.md instead.
- Never modify files outside the active task's scope.
- Never touch .env, secrets, or CI config unless the task explicitly says so.
- At session start: read BUILT.md and BACKLOG.md before touching code.
- Only work on BACKLOG.md NEXT items or an explicitly pasted task brief.
- At session end, before the final commit: update BUILT.md with a full entry for what you built (files, interfaces, tests, limitations); update BACKLOG.md statuses (completed → DONE as one line; new discovered work → QUEUED with B-IDs; unfinished → BLOCKED with reason); commit docs and code together, commit message referencing B-IDs; push to origin. A session that changes code but not BUILT.md is an incomplete session.
