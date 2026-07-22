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
  - Brief 2 (eami-api proxy layer): **DONE, merged to master** (merge
    commit `adcd3e9`, branch `b-002-eami-api-proxy-layer`, since
    deleted), plan at
    `C:\Users\bharg\.claude\plans\unified-wandering-karp.md`. New file
    `eami-api/internal/api/gateway_episodes.go` proxies
    `GET /v1/gateway/episodes*` to Brief 1's gateway endpoint. The hard
    requirement Brief 1 deferred is now satisfied: `org_id` sent to the
    gateway is always `claimsFromContext(r).OrgID` (the caller's own
    session org), never client input — an optional `org_id` query param
    is accepted only as a tamper-check that 403s on mismatch *before* the
    gateway is ever called, so a forged org can't structurally reach the
    gateway at all. Purely additive: `memory.go` has zero lines changed,
    old `/v1/memory/episodes*` routes untouched. Reviewer + security
    subagent passes both clean (2 low-severity test-coverage gaps found
    and closed before commit). **Verified 2026-07-22 with a real
    toolchain: `go build ./...`, `go vet ./...`, `go test ./...` all
    clean, 0 failures, 11/11 new tests passing** (includes the centerpiece
    `TestGatewayEpisodes_List_MismatchedOrgIDSupplied_Returns403_
    GatewayNeverCalled`, asserting both the 403 and that the fake gateway
    client's call count is zero). Fixed a nil-`cfg` panic in `NewServer`
    along the way (pre-existing latent bug, surfaced by wiring in the new
    config — `finops_test.go` already called `NewServer(nil, ...)`).
  - Brief 3 (memory.go + MemoryPage.tsx rewire): **READY TO START — no
    remaining blockers.** This is the piece that actually closes B-002:
    until it lands, `eami-api/internal/api/memory.go`'s original
    `/v1/memory/episodes` and `/v1/memory/episodes/search` routes still
    exist, completely unchanged, and still query the `episodes` table
    directly — the exact ADR-010/ADR-019 violation this whole effort
    exists to fix. That unprotected direct-query path runs today in
    parallel with Brief 2's new, compliant `/v1/gateway/episodes*`
    proxy routes. Two route families serving the same underlying data
    by two different paths, one of them still non-compliant, is an
    intermediate state, not a resolution — don't read Brief 1+2 landing
    as "ADR-019 is now fully enforced in the running system."

## Standing facts Code and PM must both know
- Desktop app: planned future feature, not yet built. Gateway auth should
  support it (Bearer JWT path) without a live consumer yet. Brief 1's dual
  auth already supports this path (Bearer AI-token JWT, org resolved
  server-side via the agent registry) with no live consumer.
- Brief 2's org-isolation logic is now built and verified, but **do not
  provision `GATEWAY_EPISODE_READ_SERVICE_KEY` anywhere a caller other
  than eami-api's proxy could use it directly against eami-gateway** —
  see BACKLOG B-015 (downgraded to Medium, not closed: Brief 1's gateway
  endpoint itself still enforces nothing on its own, Brief 2 only
  protects traffic that actually goes through it).
- Pre-existing, unrelated issue discovered 2026-07-22 while verifying
  Brief 2: `finops_test.go`'s `TestFinOpsTimeSeries_*` subtests panic
  internally (nil `s.queries`) but still report PASS because chi's
  Recoverer catches it — see BACKLOG B-016. Not fixed, out of scope for
  B-002.
- No deploy infrastructure exists in this repo (no deploy.yml, no IaC).
  Nothing is live in production. api.eami.io in openapi.yaml is a spec
  placeholder, not a real deployment.
- Solo founder, pre-first-customer, evening/weekend hours.

## Last updated
2026-07-22 by Claude Code — merged `b-002-eami-api-proxy-layer` into
master (merge commit `adcd3e9`; branch deleted, both locally and on
origin). B-002 Brief 2 (eami-api proxy layer) is now live on master
alongside Brief 1. Org-isolation hard requirement satisfied and
verified. BACKLOG updated: Brief 2 DONE/merged, Brief 3 READY TO START
with an explicit note that `memory.go`'s original unprotected routes
still run in parallel until Brief 3's cutover — B-002 is not fully
closed yet.
