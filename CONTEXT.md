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
  - Brief 1 (gateway dual-auth endpoint): **DONE, merged to master**
    (merge commit `3eab113`, from branch `b-002-gateway-episode-endpoint`,
    plan at `C:\Users\bharg\.claude\plans\unified-wandering-karp.md`). New
    `eami-gateway` package `internal/episode/{store,reader,http}.go` +
    tests, wired into `cmd/gateway/main.go`, new required config
    `GATEWAY_EPISODE_READ_SERVICE_KEY`. Reviewer + security subagent passes
    both clean (no compile-level defects; one already-known/approved
    trust-boundary tradeoff flagged, tracked as BACKLOG B-015, not a bug).
    **Verified 2026-07-22 with a real toolchain: `go build ./...` and
    `go test ./... -v` both clean, 0 failures, 18/18 new tests passing.**
  - Brief 2 (eami-api proxy layer): **READY TO START** — Brief 1's
    dependency is satisfied and merged. Hard requirement inherited from
    Brief 1's design, not optional: must independently verify the
    requesting user actually has access to `org_id` before calling Brief
    1's endpoint — Brief 1's service-key path trusts this completely and
    enforces nothing itself (BACKLOG B-015 tracks the exposure window
    this leaves open until Brief 2 ships).
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
reverted). BACKLOG.md updated to match: Brief 1 marked DONE/merged,
Brief 2 moved to READY TO START. BACKLOG B-015 still tracks the
pre-Brief-2 deployment risk — unchanged, still open.
