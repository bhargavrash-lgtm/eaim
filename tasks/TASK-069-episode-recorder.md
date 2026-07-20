# Task: Build the episode recorder in eami-gateway
**From:** PM-EAMI
**To:** BE-Gateway
**Priority:** high
**Blocked by:** none (embedding approach may use a placeholder — see Context)

## What I need

Implement `eami-gateway/internal/episode/` (directory does not exist yet — this is greenfield). After each MCP tool call is proxied and a decision is recorded (allow/deny/escalate resolved), record an episode: the agent, the sequence of steps (tool calls + args + results), the outcome, and an embedding for semantic search.

This is priority #9 on the PM roadmap and the last gateway-side gap before v1.1. Everything upstream of it (proxy, policy, audit, approvals) is already shipped and running in v1.0.1.

## Context

- Schema already exists: `episodes` table in `schema/schema.sql` (steps JSONB, pgvector HNSW index on embedding). No migration needed unless the column shapes don't fit your design — check with Architect-EAMI first if so.
- `api/openapi.yaml` already defines `Episode`, `EpisodeStep`, `EpisodeSearchRequest` schemas and the `/v1/memory/episodes*` routes (lines ~666-1761). Match your write shape to these.
- `eami-api/internal/api/memory.go` currently stubs `ListMemoryEpisodes` and `SearchMemoryEpisodes` to always return empty — it is NOT wired to the episodes table at all. Do not fix that file yet; ownership of that endpoint is an open cross-boundary question (see **ADR-019, pending** in DECISIONS.md — Architect-EAMI needs to rule on whether eami-api can serve full episode content per ADR-010's data-sovereignty rule). Wiring memory.go to real data is out of scope for this task until ADR-019 resolves.
- Embeddings: ADR-009 (semantic policy LLM endpoint) is still an open question — pending customer feedback on local vs. API LLM. Don't block on that. Use a placeholder embedding strategy for now (e.g., a deterministic local embedding — hash-based or a small local model — anything that populates the pgvector column with a fixed-dimension vector). Document the placeholder clearly in code comments so it's swapped out once ADR-009 resolves. The point of this task is the recording pipeline and DB writes, not embedding quality.

## Acceptance criteria

- [ ] `eami-gateway/internal/episode/` package exists with a recorder that is invoked from `internal/proxy/` after each completed tool call
- [ ] Episode rows are written to the `episodes` table matching the `Episode`/`EpisodeStep` shapes in `api/openapi.yaml`
- [ ] Placeholder embedding is generated and written to the `embedding` column (pgvector) for each episode
- [ ] Unit tests in `eami-gateway/internal/episode/*_test.go` cover: episode creation, step aggregation, and DB write (can use a test DB or mock)
- [ ] `go test ./...` passes in eami-gateway
- [ ] Does NOT modify `eami-api/internal/api/memory.go` — that's blocked on ADR-019

## Files to create or modify

- `eami-gateway/internal/episode/recorder.go` (new)
- `eami-gateway/internal/episode/recorder_test.go` (new)
- `eami-gateway/internal/proxy/` — wire in the recorder call after tool call completion
- `eami-gateway/internal/config/` — add config for embedding strategy if needed
