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
  has been removed, not renumbered). **Now fully enforced in the running
  system, not just decided on paper — see B-002 Brief 3 below.**
- **B-002: DONE, all 3 briefs complete** (Brief 3 built and tested on
  branch `b-002-memory-cutover`, pending merge — merge before treating
  this as fully live on `master`). History:
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
  - Brief 3 (memory.go + MemoryPage.tsx cutover): **DONE**, branch
    `b-002-memory-cutover`, plan at
    `C:\Users\bharg\.claude\plans\unified-wandering-karp.md`. Chose
    option (a): re-pointed the existing, `api/openapi.yaml`-documented
    `/v1/memory/episodes` and `/v1/memory/episodes/search` URLs at
    Brief 2's already-secure handlers (`ListGatewayEpisodes`/
    `SearchGatewayEpisodes`) instead of moving the frontend to new URLs
    — verified the response shapes are byte-identical, so this needed
    **zero `MemoryPage.tsx` changes**. Added `GET /v1/memory/episodes/
    {episodeId}` → `GetGatewayEpisode`, filling an `openapi.yaml`-
    documented route that was never implemented. **`eami-api/internal/
    api/memory.go` and `eami-api/internal/store/episodes.go` (the last
    direct, unprotected `episodes`-table query path) are deleted
    entirely** — verified zero other callers first. Security review for
    this brief specifically re-verified the org-isolation chain at the
    new `/v1/memory/episodes*` mount points (not assumed to carry over
    from Brief 2's review) and confirmed **the leak is fully closed**,
    not just superseded by a safer alternative running alongside it. 8
    new tests in `memory_test.go`, reusing Brief 2's fixtures with zero
    duplication; `gateway_episodes_test.go` itself has zero diff.
    **Verified 2026-07-22: `go build ./...`, `go vet ./...`,
    `go test ./...` all clean, 0 failures.** Two things NOT done, flagged
    before building rather than discovered after: frontend build/lint/
    typecheck (Node/npm confirmed genuinely absent from this machine —
    checked install locations directly, not just PATH) and `docker
    compose up`-based manual verification (no Docker in this
    environment) — `MemoryPage.tsx`'s correctness rests on manual
    shape-verification only. **Not yet merged to master.**

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
2026-07-22 by Claude Code — B-002 Brief 3 (memory.go + MemoryPage.tsx
cutover) built, tested, reviewed, on branch `b-002-memory-cutover` (not
yet merged). `memory.go`/`store/episodes.go` deleted; `/v1/memory/
episodes*` now served by Brief 2's org-isolated handlers with zero
frontend changes needed. Security review confirms the leak is fully
closed. B-002 is DONE pending this branch's merge. BACKLOG updated:
Brief 3 DONE, B-002 marked resolved, B-012 closed incidentally, new
B-017/B-018 logged for pre-existing doc/comment drift discovered along
the way.
