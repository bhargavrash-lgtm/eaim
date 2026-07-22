# CONTEXT.md — Living project continuity log
# Updated by: Claude Code (after every task) AND the PM chat (after every
# planning decision). Read by both at the start of every session, before
# anything else.

## Product identity (do not re-litigate without explicit founder instruction)
EAMI = Enterprise AI Monitoring & Intelligence. Gateway, policy engine,
endpoint agent, audit log. NOT a maturity-assessment tool — that's a separate,
unrelated framework the founder uses elsewhere. If any file, commit message,
or prior context suggests otherwise, it is wrong; trust this line.

## Active decision thread (update every time one moves)
- ADR-019: RESOLVED, Accepted — 2026-07-22. Full episode content stays
  on-prem; eami-api never serves it. See DECISIONS.md ADR-019 (now a full
  formal entry, same number — the informal Pending-table row it replaces
  has been removed, not renumbered).
- B-002 resolution in progress, 3-brief split:
  - Brief 1 (gateway dual-auth endpoint): **DONE** — branch
    `b-002-gateway-episode-endpoint`, plan at
    `C:\Users\bharg\.claude\plans\unified-wandering-karp.md`. New
    `eami-gateway` package `internal/episode/{store,reader,http}.go` +
    tests, wired into `cmd/gateway/main.go`, new required config
    `GATEWAY_EPISODE_READ_SERVICE_KEY`. Reviewer + security subagent passes
    both clean (no compile-level defects; one already-known/approved
    trust-boundary tradeoff flagged, tracked as BACKLOG B-015, not a bug).
    Never `go build`/`go test`-verified — no Go toolchain on this dev
    machine (BACKLOG B-013).
  - Brief 2 (eami-api proxy layer): NOT STARTED — depends on Brief 1 (done,
    ready to start). Must independently verify the requesting user has
    access to `org_id` before calling Brief 1's endpoint — Brief 1's
    service-key path trusts this completely and enforces nothing itself.
  - Brief 3 (memory.go + MemoryPage.tsx rewire): NOT STARTED — depends on Brief 2

## Standing facts Code and PM must both know
- Desktop app: planned future feature, not yet built. Gateway auth should
  support it (Bearer JWT path) without a live consumer yet. Brief 1's dual
  auth already supports this path (Bearer AI-token JWT, org resolved
  server-side via the agent registry) with no live consumer.
- **Do not provision `GATEWAY_EPISODE_READ_SERVICE_KEY` in any
  shared/multi-tenant environment before Brief 2 ships** — see BACKLOG
  B-015. Until then, anyone holding that secret can read any org's full
  episode content by supplying any `org_id`.
- No deploy infrastructure exists in this repo (no deploy.yml, no IaC).
  Nothing is live in production. api.eami.io in openapi.yaml is a spec
  placeholder, not a real deployment.
- Solo founder, pre-first-customer, evening/weekend hours.

## Last updated
2026-07-22 by Claude Code — merged `b-002-gateway-episode-endpoint` into
master (B-002 Brief 1: eami-gateway episode read endpoint, verified via
real `go build`/`go test`, both clean). DECISIONS.md ADR-019 formalized
as a full Accepted entry (same number, replacing its own informal
Pending row — briefly misnumbered ADR-020 in an intermediate commit,
reverted). BACKLOG B-015 tracks the pre-Brief-2 deployment risk.
