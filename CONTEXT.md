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
- **B-002: DONE and fully closed on `master`** — all 3 briefs complete
  and merged (`3eab113`, `adcd3e9`, `292d6a4`). Exactly one path to full
  episode content exists in the running system, and it enforces org
  isolation. History:
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
  - Brief 3 (memory.go + MemoryPage.tsx cutover): **DONE, merged to
    master** (merge commit `292d6a4`, branch `b-002-memory-cutover`,
    since deleted), plan at
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
    shape-verification only.

## Standing facts Code and PM must both know
- Desktop app: planned future feature, not yet built. Gateway auth should
  support it (Bearer JWT path) without a live consumer yet. Brief 1's dual
  auth already supports this path (Bearer AI-token JWT, org resolved
  server-side via the agent registry) with no live consumer.
- B-002's org-isolation logic is built, merged, and verified end-to-end
  (Briefs 2+3), but **do not provision `GATEWAY_EPISODE_READ_SERVICE_KEY`
  anywhere a caller other than eami-api's proxy could use it directly
  against eami-gateway** — see BACKLOG B-015 (Medium, still open: this is
  now a standalone network-hardening item, not a B-002 blocker — Brief
  1's gateway endpoint itself still enforces nothing on its own if
  reached directly, bypassing eami-api entirely).
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
2026-07-24 by Claude Code — B-026: eami-api's RS256 user-session JWT
signing key regenerated in memory on every container restart,
force-logging-out every user. Root cause wasn't a missing persistence
mechanism in the code (`auth.go` already had a `loadPrivateKey` path for
a configured key) but that `internal/config/config.go` had **no env-var
override for `cfg.Auth.RSAPrivateKeyPath` at all** — the docker-deployed
system was always in ephemeral dev-mode regardless of `eami-api.yaml`.
Mirrored `eami-gateway/internal/identity`'s `loadOrGenerateKey` pattern
(reference implementation, not modified): new `auth.loadOrGenerateKey`
generates+persists a 2048-bit key on first boot if missing, loads the
same key on every restart thereafter, and deliberately propagates a real
error rather than silently regenerating over a corrupt/unparseable
existing file (which would itself invalidate every outstanding token —
the exact failure mode being fixed). New `API_JWT_KEY_PATH` env var
(mirrors `GATEWAY_JWT_KEY_PATH`), new named Docker volume `api_certs`
(mirrors `gateway_certs`) in both compose files, new `scripts/setup.sh`
`generate_api_keypair()` mirroring the existing gateway equivalent. 5
new tests in `eami-api/internal/auth/auth_test.go` (package had zero
before), including a restart-simulation test proving a token issued by
one `Service` instance verifies against a second instance built from
the same key path. **Verified 2026-07-24 with a real toolchain:
`go build/vet/test ./...` clean, 0 failures** (both `eami-api` and
`eami-gateway` modules — gateway confirmed untouched). Reviewer +
security subagent passes both clean: no key bytes ever reach a log/error
line, `0600`/`0700` permissions applied atomically at creation, and
permission-denied/corrupt-file errors correctly aren't misclassified as
"missing" (would have silently regenerated over real key material).
**Live-verified beyond the brief's minimum:** logged in against the real
local stack (seeded a throwaway test org/user in Postgres, cleaned up
after), confirmed the same access token — no re-login — survives not
just `docker compose restart eami-api` but a full `down` (no `-v`)/`up`
cycle too. Discovered, not fixed (dead code, out of scope): `API_JWT_SECRET`
is generated by `setup.sh` and documented in `.env.example` but never
read by any Go code — the service is 100% RS256, no HS256 fallback path
exists despite the comment claiming one. **Logged as `BACKLOG.md` B-031**
(low priority, no urgency) per founder request: either remove
`API_JWT_SECRET` entirely from `setup.sh`/`.env.example`/both compose
files, or keep it but rewrite the misleading "HS256 fallback" comment to
say plainly it's unused/reserved.

Prior entry, still accurate: 2026-07-24 by Claude Code — B-025: closed a real, live authentication
bypass. `eami-api/internal/config/config.go` had **no `validate()` at
all** and `defaults()` hardcoded `ServiceKey: "changeme"` plus a
`changeme`-password DSN — an unset `API_SERVICE_KEY` env var meant
`requireServiceKey` accepted a literal `X-Service-Key: changeme` header
from any caller against the collector write paths. `eami-gateway/internal/
config/config.go` had a `validate()` (from B-002 Brief 1) but never
checked `API.ServiceKey` (`GATEWAY_API_SERVICE_KEY`) — same class of gap.
Fixed: both now reject `API_SERVICE_KEY`/`GATEWAY_API_SERVICE_KEY`/
`POSTGRES_PASSWORD` if empty or a known placeholder (`"changeme"`/
`"devpassword"`), checked via new `isPlaceholderSecret`/
`dsnHasPlaceholderPassword`/`dsnPassword` helpers (duplicated across the
two modules — separate Go packages under `go.work`, no shared package
introduced for ~30 lines). `docker-compose.yml`'s `${POSTGRES_PASSWORD:-
devpassword}` (3 places) and `docker-compose.prod.yml`'s bare
`${POSTGRES_PASSWORD}` (3 places) both now use compose's `${VAR:?msg}`
required-var syntax, confirmed fail-closed before any container starts.
`.env.example`'s literal `changeme` values for `POSTGRES_PASSWORD`/
`DATABASE_URL` replaced with blank + instructions. **Security review
caught a real gap, fixed before commit:** the first version of
`dsnHasPlaceholderPassword` did a raw, untrimmed substring match, so a
CRLF-corrupted `.env` value or leading whitespace on the DB password
(e.g. `API_DB_PASSWORD="changeme\r"`) would bypass detection — fixed by
extracting the DSN's password segment and validating it through the same
trimmed/lowercased path as every other secret. General code review
caught that the original tests only exercised `validate()` directly, not
`Load()`'s real env-var wiring — added `Load()`-level integration tests.
18 new tests total (8 `eami-api`, 10 `eami-gateway`). **Verified
2026-07-24 with a real toolchain: `go build ./...`, `go vet ./...`,
`go test ./...` all clean, 0 failures**, plus a live manual check against
the running local `docker compose` stack: real secrets still start
clean, and `API_SERVICE_KEY=changeme`/unset, `GATEWAY_API_SERVICE_KEY=
changeme`, and `API_DB_PASSWORD=devpassword` were each individually
confirmed via real containers to produce the exact clean startup-refusal
error (not a generic panic). Two follow-ups logged, not fixed (out of
this task's `MAY MODIFY` scope): B-028 (`eami-api.yaml`/`eami-gateway.
yaml` still ship the now-rejected literal `"changeme"` as example
config) and B-030 (`scripts/seed-db.sh`/`create-audit-partition.sh` have
the same `devpassword`-fallback pattern this task closed elsewhere).
**Renumbered twice, same session:** originally logged as B-026/B-027,
which collided with different already-planned work (JWT signing key
persistence and background-goroutine panic recovery) not yet reflected
in `BACKLOG.md` — renumbered to B-028/B-029 per founder correction. The
second number then also collided (B-029 already reserved for Postgres
backup/disaster-recovery, likewise not yet in `BACKLOG.md`) — renumbered
again to B-028/B-030. `BACKLOG.md`'s counter now reads `Next B-ID:
B-031`. **Process note for future sessions:** PM-side planning sometimes
reserves B-IDs in conversation before they're written into `BACKLOG.md`
as QUEUED items — when assigning a new B-ID, check with the founder for
numbers already spoken for in roadmap discussion, not just what's
written in the file, since the file and the conversation can drift out
of sync.

Prior entry, still accurate: 2026-07-24 by Claude Code — B-024: `ToolsPage.tsx`'s "Test connection"
button read only whether the HTTP call to `TestTool` threw, never the
response body -- harmless before B-023 (which always returned a
synthetic success), but after B-023 (which always resolves 200 with the
real result in `{success, latency_ms, error}`), the button always showed
green regardless of actual reachability. Fixed: `handleTest` now reads
`result.success`, and on failure parses B-023's `"<reason>: <detail>"`
error string into `auth-failed`/`unreachable`/`misconfigured`/a generic
fallback, each with a distinct badge color (reusing the page's existing
`StatusBadge` green/amber/red language) and the full message surfaced
via a tooltip. **Notable process discovery, not just this task's fix**:
Node/npm are still absent on this machine, but `docker build --target
builder -f eami-ui/Dockerfile .` runs the real `npm ci && generate-client
&& tsc && vite build` pipeline inside a container -- used here to get a
genuine, passing compiler check (not just manual code review) for the
first time this session on a frontend change. Logged in `BUILT.md` as a
reusable verification path for future `eami-ui` work on this machine.
Live `docker compose up`/browser click-through not performed (judged
disproportionate for this narrow, already-type-checked change); manual
verification steps provided instead. Scope confirmed strictly
`ToolsPage.tsx` (`git diff --stat`: one file changed).

Prior entry, still accurate: 2026-07-24 by Claude Code — B-023: `POST /v1/gateway/tools/{toolId}/test`
was a synthetic stub (always "connected", no real probe). Fixed: new
`eami-api/internal/api/tool_connectivity.go` runs a real check per tool
`type` -- HTTP GET for `rest_api`, a real `pgx.ConnectConfig` handshake
for `database` (SQLSTATE `28xxx` distinguishes auth-failed from
unreachable), and an honest `misconfigured` for `mcp` (a local-subprocess
tool type that can't safely be tested from eami-api's cloud process --
shelling out to an admin-supplied command string would itself be a
command-injection surface). Response now matches `openapi.yaml`'s
long-undocumented `{success, latency_ms, error}` shape. **Security review
caught a real gap not in the original plan**: since eami-api is EAMI's
own cloud SaaS process (unlike eami-gateway, which is on-prem), an
unguarded version would let an org admin/operator use this endpoint as a
reachability oracle against EAMI's own cloud network (e.g. cloud metadata
endpoints). Added `safeDialContext` -- rejects loopback/link-local/
private/RFC1918/ULA targets, resolves once then dials the validated IP
directly (closes a DNS-rebinding gap) -- wired into both the REST and
database dial paths; re-verified clean by the same reviewer end-to-end
against the metadata-address and RFC1918 cases. General code review
separately caught and fixed: single-address-only dialing (now falls back
through all resolved addresses), a per-call `http.Transport` leaking an
idle connection + goroutines on every test, and an unbounded database
connection close. 30 new tests, including direct coverage of the SSRF
guard. **Verified 2026-07-24 with a real toolchain: `go build ./...`,
`go vet ./...`, `go test ./...` all clean, 0 failures** (149 total, up
from 143 pre-B-023). Known follow-up, out of scope for this task and not
fixed: `ToolsPage.tsx`'s "Test connection" button doesn't yet read the
real `success` field from the response (harmless before this fix, since
the old stub always reported success) -- logged as `BACKLOG.md` B-024.

Prior entry, still accurate: 2026-07-23 by Claude Code — B-022: `POST /v1/gateway/tools` was silently
discarding any `credentials` object submitted via the Add Tool UI
(documented in `api/openapi.yaml`'s `ToolCreate.credentials`, but
`CreateTool` never read the field, never wrote to `gateway_tools.
credentials_encrypted`, and returned 201 anyway) — a gap surfaced by a
prior full-application audit. Fixed: new `eami-api/internal/toolcreds`
package (AES-256-GCM, key from `TOOL_CREDENTIALS_ENCRYPTION_KEY`,
deliberately not pgcrypto — see BUILT.md for why), wired into
`CreateTool`; fails closed (500, no store call) if credentials are
submitted but no key is configured. Two subagent review passes: security
review caught a real bypass in an early version (typed-struct decode
meant an unrecognized credential field name silently reproduced the
original bug) — fixed and re-verified clean. General code review caught
a missing nil-store guard on `tools.go`'s other handlers and a
non-standard error code — both fixed. 19 new tests; `tools.go` had zero
coverage before this. **Verified 2026-07-23 with a real toolchain:
`go build ./...`, `go vet ./...`, `go test ./...` all clean, 0
failures.** `TOOL_CREDENTIALS_ENCRYPTION_KEY` added to `.env.example`/
`docker-compose.yml`/`docker-compose.prod.yml` (generate via `openssl
rand -hex 32`, same convention as the other secrets) so the local stack
doesn't hit the fail-closed path unexpectedly. `TestTool`'s synthetic
"always connected" stub is unchanged, explicitly out of scope — logged
as B-023, the natural next building block (decrypting stored credentials
for a real connectivity probe).

Prior entry, still accurate: 2026-07-22, standalone infra fix (B-020, not tied to any
brief): `eami-collector` was crash-looping (`exec
/app/docker-entrypoint.sh: no such file or directory`) because
`docker-entrypoint.sh` had Windows CRLF line endings, breaking shebang
resolution (`#!/bin/sh\r` → kernel looks for an interpreter literally
named `/bin/sh\r`). Not a missing `COPY`/`chmod +x` — Dockerfile was
already correct. Stripped to LF; verified `docker compose build
eami-collector` clean and the container starts and stays running.
**Every other `.sh` file in the repo has the same CRLF issue** (no
`.gitattributes` pinning LF) — logged as B-021, not fixed here (out of
scope for this task).

Prior entry, still accurate: 2026-07-22, standalone infra fix (B-019,
not tied to any brief): `docker-compose.yml`'s `eami-ui` service had
`build.context: ./eami-ui`, but `eami-ui/Dockerfile` copies repo-root
`api/openapi.yaml` (needed for `generate-client`), so `docker compose
up --build` failed with `COPY api/openapi.yaml /api/openapi.yaml: not
found`. Fixed to `context: .` / `dockerfile: eami-ui/Dockerfile`;
verified with `docker compose build eami-ui`. Also confirms Docker is
available on this machine (previously assumed absent — see B-002
Brief 3 notes above, which were accurate when written but are now
stale on that one point). Committed directly to master.

Prior entry, still accurate: 2026-07-22, merged `b-002-memory-cutover`
into master (merge commit `292d6a4`; branch deleted, both locally and
on origin). **B-002 is now fully closed on master**: `memory.go`/
`store/episodes.go` deleted, `/v1/memory/episodes*` served by Brief 2's
org-isolated handlers, zero frontend changes needed, security review
confirms the leak is fully closed (not just superseded by a safer
alternative running alongside it). All three B-002 briefs (`3eab113`,
`adcd3e9`, `292d6a4`) are on master. BACKLOG updated to match: B-002
marked resolved, B-015 reframed as a standalone network-hardening item
(no longer a B-002 blocker), B-012 closed incidentally, B-017/B-018
logged for pre-existing doc/comment drift discovered along the way.
