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
- Paste-detection epic (browser-extension-based) is in scoping. B-032
  (2026-07-25) built the backend groundwork only — `paste_events` table +
  `POST /v1/reports/paste-events` — ahead of the extension itself. **No
  browser extension exists yet; this endpoint has no live caller.** An
  investigation earlier that session found `POST /v1/reports` (the
  existing collector-write path) has **zero real producers today** —
  `eami-collector`'s forwarder only ever calls `/v1/ingest/batch`, a
  different table/pipeline — worth knowing before assuming that endpoint
  is exercised in production. B-034 (2026-07-26) added the `eami-agent`
  side — a native-messaging host mode so a future extension can relay
  paste events in real time — but **`eami-agent` has no `org_id`
  anywhere** (only `agent_id`), confirmed by direct investigation, not
  assumed. B-035 (QUEUED) is the still-missing link: paste events
  currently land in the existing agent-report pipeline's raw JSON blob,
  not literally in `paste_events` yet — `eami-collector`/`eami-api` were
  both frozen for B-034, so that wiring is real, tracked, unfinished work,
  not silently dropped. **B1 (browser extension) now exists** — B-036
  (2026-08-03), `eami-browser-extension/`. Its extension ID
  (`ngmdfnecljeoleiancdedbmhjdihaoaa`) is wired into B0's
  `allowed_origins`, and the native-messaging→backend leg is fully
  live-verified (real message → real B0 host → real Docker
  collector/API/Postgres → confirmed in `endpoint_reports`). Still no
  live production install of the extension itself (local unpacked-load
  only; enterprise force-install policy documented, not configured), and
  B-035 (paste events landing in the raw agent-report JSON blob rather
  than literally in B-032's `paste_events` table) is still open — that
  gap is unaffected by B1, since it's purely a server-side
  (`eami-collector`/`eami-api`) wiring task.
- **This machine's shell runs elevated (Administrator)** — discovered
  2026-08-03 while attempting real Chrome/Edge automation for B1's
  verification: elevation causes Edge (and likely Chrome) to
  silently de-elevate-relaunch themselves, breaking reliable
  `--load-extension`/`--remote-debugging-port`/`--user-data-dir`
  passthrough for any future browser-automation attempt in this
  environment. **A `taskkill /F /IM msedge.exe` run while diagnosing this
  very likely killed the user's own real, unrelated Edge session** (all
  running `chrome.exe` processes were separately confirmed to be the
  user's real session, not test artifacts — Edge was not re-checked in
  time to tell). Disclosed directly to the user. Any future session
  attempting real browser automation here should assume the same
  elevation problem exists and avoid blanket `taskkill` by image name —
  filter by command line/working directory first to confirm a process is
  actually one you spawned.
- **A real .NET SDK (8.0.423) + WiX Toolset v4 (4.0.6, matching CI's
  pinned version) are now installed on this machine** (2026-07-26, via
  `winget`/`dotnet tool install`, to debug a CI MSI-build failure) —
  previously only the .NET *runtime* was present, not the SDK, so
  `eami-agent/installer/Product.wxs` could only be reviewed by eye, never
  actually compiled locally. That gap is closed now: `wix build` and the
  project's own `installer/build.ps1` both run for real on this machine.

## Last updated
2026-08-03 by Claude Code — B1 manual-testing follow-up: fixed a real bug
found only by the user's own hands-on click-through (not by review or
automation), in `internal/nmlauncher` (B0's parent-process
defense-in-depth check). Manual test step 5 (`flush()`) failed twice in a
row: first silently (B0 wasn't currently registered on this machine --
re-registered, unrelated to the bug below), then with Chrome's generic
`"Error when communicating with the native messaging host."` after
re-registering. Root-caused by reproducing the exact failure signature
myself (host process exits immediately, code 1, zero bytes ever written
to stdout -- precisely what Chrome surfaces as that generic error) via a
direct invocation of the real registered launcher binary with a
non-browser parent process. That pointed at `nmlauncher.
VerifyLaunchedByBrowser()`'s fail-CLOSED branch: it only recognized a
short hardcoded list of Chrome/Edge process names, and refuses (exit 1,
no response ever sent) for anything else -- including, it turns out, the
user's own real, legitimate browser invocation on this machine (exact
browser/process-tree shape not confirmed, and not safely determinable
without repeating the earlier chromedp/elevated-shell/taskkill mistake --
see the standing fact above -- so not chased further). **Fix: changed the
"parent determined but not on the list" branch from fail-closed to
fail-open-with-a-loud-WARN-log**, matching the policy already used right
next to it for "parent couldn't be determined at all." Rationale in the
package doc comment: this check was always documented as best-effort,
not bulletproof, and a closed-world hardcoded browser-name list cannot
stay complete against every Chromium-family browser/OS/version in the
field -- now demonstrated by a real false positive breaking the primary
feature it was added to protect. `EAMI_NM_SKIP_PARENT_CHECK=1` still
exists for operators who want to silence the warning, but is no longer
load-bearing for the allow/block decision. Re-verified end-to-end myself
after the fix, from scratch: rebuilt `eami-agent.exe`, recreated the
hard-linked launcher in `.local-test-agent/` (**do not delete this
directory -- it backs the user's live test registration**, learned the
hard way earlier this same thread), then ran a real length-prefixed
native-messaging protocol round trip against the rebuilt launcher from a
deliberately non-browser parent (mirroring the exact failure condition)
-- got back `{"status":"ok"}`, a WARN log instead of a refusal, and
confirmed the event landed in `endpoint_reports` in the real Docker
Postgres stack. `go build ./...`, `go vet ./...`, `go test ./...` all
clean across `eami-agent`; `nmlauncher_test.go`'s
`TestVerifyLaunchedByBrowser_UnrecognizedParent_Blocked` renamed/updated
to assert the new fail-open-with-warning behavior instead of the old
block. Committed as part of this thread's ongoing B1 follow-up (not yet
pushed at the time of this note -- see BUILT.md/BACKLOG.md for exact
commit). User has not yet retested step 5 against this fix in their own
real browser.

Prior entry, still accurate: 2026-08-03 by Claude Code — B-036: B1, the browser extension
(`eami-browser-extension/`) completing the paste-detection epic's
browser side. Detects paste events on 6 AI-tool domains, computes length
+ SHA-256 hash client-side (raw text never leaves `content-script.js`),
relays to B0 via native messaging only. Extension ID
(`ngmdfnecljeoleiancdedbmhjdihaoaa`) is deterministic -- an RSA public
key embedded in `manifest.json`'s `key` field, Chrome's own documented
derivation algorithm, computed two independent ways that agreed exactly
-- and now replaces the placeholder in B0's `nmregister_windows.go` and
the Linux/macOS postinstall scripts.

**The native-messaging->backend leg is fully live-verified, the
in-browser leg could not be automated -- and a real mistake happened
while trying.** With B0 re-registered under the real ID, a message
matching exactly what `background.js` produces was sent through the
*real* registered `eami-agent-nmhost.exe` (invoked with **no `--config`
flag**, relying entirely on the registry-based config fallback per
ADR-014 -- exactly how a real MSI install would populate it, not a
test-only YAML file) into a *real* `docker compose` Postgres/
collector/API stack, and confirmed landing correctly in
`endpoint_reports`. For the in-browser leg, no browser-automation tool
was available in this environment; a `chromedp`-based attempt (Chrome
and Edge are both genuinely installed here) was abandoned after
discovering this session's shell runs elevated, which causes Edge/Chrome
to silently de-elevate-relaunch themselves in a way that broke reliable
flag/profile passthrough for automation. **While diagnosing that, a
blanket `taskkill /F /IM msedge.exe` was run that very likely killed the
user's own real, unrelated Edge session** (confirmed after the fact that
none of the machine's running `chrome.exe` processes were test
artifacts -- all real, long-running user session; Edge's equivalent
wasn't re-checked in time to tell for certain). Disclosed directly to the
user rather than glossed over -- see the new standing fact above this
entry for the elevation gotcha, so a future session doesn't repeat it.
Pivoted to: careful line-by-line code review against Chrome's documented
MV3 contracts, real (pure-Go, zero-browser-risk) JS syntax validation via
`goja`, and a `MANUAL_TESTING.md` checklist handed to the user for the
parts only a real browser click-through can verify.

**Reviewer + security subagent pass:** security review confirmed the
no-raw-content guarantee holds in the actual shipped code (traced every
line touching the raw pasted string, not assumed), found permission
scope justified (no `<all_urls>`), and noted one LOW-severity
observation (a compromised/XSS'd allowlisted page could dispatch a
synthetic `paste` DOM event to forge fake telemetry -- requires the
attacker to already control JS execution on a trusted third-party
domain, not fixed, judged disproportionate to that precondition).
**General code review caught and this session fixed a real bug:**
`background.js`'s original `chrome.storage.local` read-modify-write
pattern had a genuine lost-update race under concurrent paste events --
two events arriving close together could each read the same pre-update
buffer and the second `set()` would silently clobber the first's append,
contradicting the file's own "never lose a buffered event" goal. Fixed
with a promise-chain serialization queue (`runExclusive`) around every
storage critical section, keeping the slow native-messaging round trip
itself outside the lock. Also confirmed, not assumed: JS's
`toISOString()` (always includes a milliseconds fraction) is correctly
accepted by Go's `time.Parse(time.RFC3339, ...)`, verified via direct Go
execution rather than trusted from memory; and the batch-ack strict-FIFO
assumption is justified by B0's actual single-goroutine, fully
synchronous `RunHost` implementation, not just asserted by the JS side.

Full writeup in `BUILT.md`'s new `eami-browser-extension` section and
`BACKLOG.md`'s B-036 entry.

Prior entry, still accurate: 2026-07-26 by Claude Code — B-034 CI-failure correction: the "MSI —
eami-agent installer" GitHub Actions job failed on B-034's own commit,
at the "Build MSI" step, in the exact `Product.wxs` comment block that
task added. Two real WiX v4 bugs, not one: (1) `WIX0104` — the session's
own "--" em-dash writing convention is literally invalid inside XML
comments; (2) `WIX0400`, found only after fixing (1) — the
`<InstallExecuteSequence>` block's two `<Custom>` conditions were written
WiX-v3-style as inner text, but WiX v4 requires a `Condition="..."`
attribute instead, confirmed by reading WiX's own compiler source
(`ParseSequenceElement` in `wixtoolset/wix`'s `Compiler_Package.cs` calls
`InnerTextDisallowed(node, "Condition")`), not guessed. **Installed a real
.NET SDK + WiX 4.0.6 locally rather than fixing blind a second time** —
this machine previously had only the .NET runtime. Reproduced both CI
errors byte-for-byte before fixing either, then verified clean with a
direct `wix build` and the project's actual `installer/build.ps1` script
(CI's real invocation path) against a real cross-compiled Windows binary,
producing a genuine, valid, installable MSI. This closes B-034's own
"not compiled/run through a live wix build" caveat for the `Product.wxs`
XML/schema correctness specifically (a full `msiexec` install/uninstall
cycle — which needs elevation and installs a real Windows service —
wasn't additionally performed, since it wasn't what was asked). Full
writeup in `BACKLOG.md`'s B-034 CI-failure correction entry and
`BUILT.md`'s `eami-agent` section.

Prior entry, still accurate: 2026-07-26 by Claude Code — B-034: native-messaging host for real-time
paste-event relay (B0), the `eami-agent` side of the paste-detection
epic following B-032. New `eami-agent --native-messaging-host` mode: the
browser's standard length-prefixed-JSON stdin/stdout protocol, not a
network port. Confirmed (not assumed) `eami-agent` has no `org_id`
anywhere — only `agent_id` (config, defaults to hostname) — so, combined
with `eami-collector`/`eami-api`/B-032's endpoint all frozen for this
task, events are forwarded via the existing `collector.Sender.Send()`
immediately (bypassing the 5-minute poll cycle), landing in the existing
agent-report pipeline's raw JSON blob today rather than literally in
`paste_events` yet -- completing that is B-035 (QUEUED), logged explicitly
per the user's instruction not to let it get lost.

**Two real bugs caught by this session's own testing, before any real
extension existed to catch them in practice:**
1. The native-messaging manifest originally pointed straight at the real
   binary. Chrome/Edge's manifest schema has no field to pass
   `--native-messaging-host` as an argument, so a real browser would have
   launched it in ordinary poll-loop mode instead of host mode -- a
   silent, total integration failure that every test in the original
   changeset masked (all of them passed the flag on the command line
   directly). Fixed with a hard-linked `eami-agent-nmhost` launcher next
   to the real binary; `main.go` detects invocation under that name.
   Live-verified end-to-end after the fix: real registry + manifest +
   launcher, invoked with **no explicit flag**, real message parsed and
   forwarded to a mock collector.
2. Logging in native-messaging-host mode wrote to `os.Stdout` -- the same
   stream the protocol uses for length-prefixed framing -- so any log
   line (including one this session added) would have corrupted every
   real session. Fixed by routing all logging to `os.Stderr` in this mode
   specifically; new regression test locks it in.

**Security review finding, fixed:** no caller authentication meant any
local process could invoke this mode directly and forward fabricated
paste-event data using the agent's own already-configured collector
credentials (a confused-deputy problem) -- native messaging's own
`allowed_origins` manifest restriction is enforced by the browser, not
the host process, so it provides zero protection against direct
invocation of the binary. Fixed with `internal/nmlauncher`'s
parent-process verification: fails closed if the immediate parent isn't a
recognized browser, fails open with a loud warning if it can't be
determined at all (an inconclusive check must not break the feature
entirely), `EAMI_NM_SKIP_PARENT_CHECK=1` is a documented operator
override. Explicitly **not bulletproof** (a capable local attacker could
rename their process or launch as a child of a real browser) -- real
defense-in-depth, not a hard guarantee, and documented as such.

**Verified 2026-07-26 with a real toolchain:** `go build`/`go vet` clean
on native Windows and cross-compiled for `linux/amd64`/`darwin/arm64`;
`go test ./...` clean, all packages, 21 new tests. Windows registration
(install/uninstall, both the launcher hard link and the registry keys)
live-verified against this machine's real registry and re-verified after
every fix. Linux/macOS installer script changes exist and are
syntax-checked but not exercised on a live host this session (this
machine is Windows) -- flagged, not claimed as verified. WiX
`CustomAction` wiring is correct source but not compiled/run via a live
`msiexec` install (no .NET SDK in this environment) -- the Go logic it
invokes was verified directly, outside the MSI. Full writeup in
`BUILT.md`'s `eami-agent` section and `BACKLOG.md`'s B-034/B-035 entries.

Prior entry, still accurate: 2026-07-25 by Claude Code — B-032: paste-detection groundwork (`paste_events`
table + `POST /v1/reports/paste-events` batch ingestion endpoint), built
ahead of the browser extension itself per an explicit task brief. Followed
an earlier investigation this same session (not itself a B-ID) that
concluded reusing `discovered_endpoints`/`POST /v1/reports` for paste
events was the wrong shape (dedup-by-tuple with one overwritten
`last_seen`, can't hold per-event history) and that `/v1/reports` has zero
live producers today regardless. New TimescaleDB hypertable (picked over
`audit_log`'s pg_cron partitioning by matching query shape to precedent,
not habit — `token_usage` is the closer analog), real FKs to `orgs`/
`endpoints`, append-only (no dedup/collapsing). Batch insert is a single
multi-row `INSERT ... unnest()` regardless of batch size — proved via a
`pgx.QueryTracer`-instrumented test asserting exactly 1 query fires for a
1-event and a 50-event batch — deliberately not `reports.go`'s existing
N-round-trips-per-batch pattern (a real gap the investigation flagged,
`reports.go` itself untouched, out of this task's scope). Content-scrub
guarantee (no raw pasted text can reach storage) is structural, verified
both by reflection over the request type and by a live test POSTing a
crafted payload with smuggled `content`/`raw_text` fields against the real
endpoint/DB and confirming nothing leaks into any stored column. New
`ResolvePasteSourceEndpoint` deliberately does not reuse the existing
`UpsertAgentEndpoint` (which would silently blank out `agent_version`/
`os_info` already collected by the full `eami-agent` scanner every time a
thinner paste-event payload arrived for the same machine) — confirmed
against `UpsertAgentEndpoint`'s real behavior during code review, not just
assumed, and now has a dedicated regression test. Reporting-query
performance proven, not assumed: 200,000 synthetic rows seeded, a real
`EXPLAIN ANALYZE` confirms the org+domain+30-day dashboard query hits
`idx_paste_events_org_domain` with zero sequential scans (5.3ms).
**Docker Desktop was paused at session start** (confirmed with the user
before unpausing it, per this session's own operating norms around
system-state changes) — used to run a real Postgres+TimescaleDB for every
claim above, not simulated. Reviewer + security subagent passes both ran
(mandatory for a new privacy-sensitive ingestion surface): security clean;
code review caught and this session fixed a missing migration idempotency
guard (now matches `005_token_usage.sql`'s `IF NOT EXISTS` convention —
the first version would have errored on any re-run against an existing
DB), the untested `ResolvePasteSourceEndpoint` clobber-scenario above, and
a trivial `gofmt` issue. `discovered_endpoints`/`reports.go` confirmed
completely unaffected (files untouched; also proved live via a regression
test running both ingestion paths in the same process/DB). `api/
openapi.yaml` not updated — out of this task's `MAY MODIFY` scope (owned
by Architect-EAMI per `BOUNDARIES.md`) — logged as `BACKLOG.md` B-033.
Full writeup in `BUILT.md`'s `eami-api` section and `BACKLOG.md`'s B-032/
B-033 entries.

Prior entry, still accurate: 2026-07-25 by Claude Code — B-029: first real Postgres backup/disaster-
recovery mechanism. No backup existed before this — `docker compose down
-v` or disk loss was unrecoverable, a real gap for a product whose core
value is an immutable audit trail. New `eami-backup` sidecar service
(`postgres:16-alpine`) in both compose files runs scheduled `pg_dump -Fc`
via a real cron (Alpine's busybox `crond` — `pg_cron`, already running in
`postgres` for audit-partition creation, was investigated first and
rejected since it can't shell out to run an external client program like
`pg_dump`) to a dedicated `postgres_backups` volume, kept separate from
`postgres_data` so a lost data volume doesn't also lose the backups, with
retention pruning and non-silent failure logging (new `scripts/
backup-db.sh`, `scripts/backup-entrypoint.sh`, both POSIX `sh`). New
`RECOVERY.md` documents the restore procedure. **Genuinely tested
end-to-end this session, not just written and assumed** — Docker Desktop
wasn't running at session start; started it and confirmed the daemon was
live before beginning specifically so this could be a real test, not a
theoretical one. Ran a full disaster-recovery drill in an isolated
`docker compose -p` project (zero impact to the real local dev stack):
inserted real test data, took a backup, destroyed the `postgres_data`
volume entirely, brought up a fresh `postgres`, and restored — confirming
the exact data was present and correct afterward. **The first restore
attempt (`pg_restore --clean`) failed partway — a real bug the test
caught, not assumed away:** TimescaleDB hypertables don't support the
`ALTER TABLE ... ONLY` statements `--clean` generates against a database
where `schema.sql` already ran via `docker-entrypoint-initdb.d` on the
fresh container's own boot. Fixed by wiping via `DROP SCHEMA public
CASCADE` before a plain `pg_restore` (no `--clean`) — retested clean,
zero errors, all 73 tables/6 extensions/hypertable metadata/exact test
row restored correctly. This tested procedure, not the naive one, is
what `RECOVERY.md` documents. A reviewer pass caught 4 more real issues
(dead `pg_dump` flags, an unescaped quote in the cron env file, no
`BACKUP_SCHEDULE` validation, orphaned `.tmp` files on a killed run) —
all fixed and re-verified live. A 5th flagged concern (a possible
`service_healthy`/`schema.sql`-timing race) was checked empirically by
tracing the real container startup log rather than debated — confirmed
not to exist for `timescale/timescaledb-ha:pg16`, which follows the
standard docker-library/postgres entrypoint contract. `scripts/
reseed.sql`'s misleading "recovery" framing corrected to point at
`RECOVERY.md`. Offsite/cloud storage explicitly flagged as the most
important follow-up, not built this round (out of this task's v1 scope).
All test containers/volumes torn down after; the real local stack's
`.env` and `eaim_*` volumes confirmed untouched throughout. Full writeup
in `BUILT.md`'s new "Postgres backup & disaster recovery" cross-cutting
entry and `BACKLOG.md`'s B-029 entry.

Prior entry, still accurate: 2026-07-25 by Claude Code — B-028/B-030/B-031: config-hygiene cleanup,
three small doc/script fixes found during the B-025/026/027 security work
— none of them live vulnerabilities, no application code touched. B-028:
`eami-api.yaml`/`eami-gateway.yaml`'s 4 literal `"changeme"` values
(service_key/dsn in each) replaced with `""` plus a comment naming the
real env-var override — confirmed via the compose files that neither
service's default invocation actually depends on the YAML's literal
values, since `config.go`'s `Load()` unconditionally overlays env vars
onto whatever the YAML provides. B-030: `scripts/seed-db.sh`/
`scripts/create-audit-partition.sh` no longer silently fall back to a
`devpassword`-embedded connection string when `DATABASE_URL` is unset —
both now hard-fail first, matching B-025's contract. B-031: `API_JWT_SECRET`
(confirmed via grep to be read by zero Go code — service is 100% RS256)
removed entirely from `scripts/setup.sh`, `.env.example`, and both
compose files, per this task's stated preference for full removal;
traced `setup.sh`'s own flow first to confirm nothing depended on the
variable beyond writing it to `.env`. No Go files touched, so no
build/test run applies; `docker compose config` (daemon-independent)
run against both compose files with required env vars supplied —
both resolve cleanly, `eami-api`'s resolved environment block confirmed
`API_JWT_SECRET` gone with everything else unchanged. Docker daemon
itself wasn't running in this session (`docker info` failed), so a live
`docker compose up` re-check of AC #4 wasn't performed — `compose
config`'s clean resolution is the evidence on record for now. Full
writeup in `BUILT.md`'s new "Config hygiene cleanup" cross-cutting entry;
`BACKLOG.md` B-028/B-030/B-031 all marked DONE.

Prior entry, still accurate: 2026-07-25 by Claude Code — B-027: added panic recovery to the 4
background/ticker goroutines identified as having none — eami-api's
alerting `Engine.Run` and eami-gateway's approval router (`Router.Run`/
`listenLoop`), fire-and-forget token-usage writer, and episode recorder
(`episode.Recorder.Record`). None of these run inside an HTTP request path,
so none had chi's Recoverer / stdlib per-connection recovery protecting
them — an unrecovered panic in any one would have crashed the owning
process outright. This B-ID was pre-reserved during the B-025 renumbering
session (2026-07-24, see the B-025 entry below) and confirmed matching
before use, per the process note logged there.
New `eami-gateway/internal/safego` package (`Guard`/`GuardErr`) is shared
across 3 of the 4 targets (approval router, token-usage writer, episode
recorder — all in that module); `eami-api/internal/alerting` got its own
small local `guard` helper since it's the only background goroutine in
that module. Recovery is deliberately per-iteration, not just
per-goroutine-start: the alerting engine recovers each rule's evaluation
individually (`evaluateRules`) so one bad rule doesn't stop the rest of
that tick, and the approval router recovers each LISTEN/NOTIFY event
individually (`handleNotification`) so one bad notification doesn't tear
down the LISTEN connection — both also get an outer, defense-in-depth
wrap (`Engine.safeTick`, `Router.Run`'s `GuardErr` around `listenLoop`) for
panics outside those inner loops. The token-usage writer and episode
recorder turned out not to be persistent loops at all once traced — both
are one-shot goroutines (`go func(){}`/`go episodeRecorder.Record(...)`)
fired fresh per dispatched tool-call event from `cmd/gateway/main.go`'s
dispatch closure — so each was wrapped at its call site/method body
instead of an outer loop. No business logic (rule evaluation, approval
resolution, token-usage POST construction, episode DB insert) or existing
non-panic error handling changed anywhere — confirmed via diff, all 4
files pure wrapper/extraction. Two new test seams, both following this
codebase's pre-existing `toolDialOverride`/`toolStoreOverride` convention
(never a new pattern): `cmd/gateway/main.go`'s `tokenUsageWriteFunc` var,
and `episode.Recorder`'s new `episodeWriteDB` interface (mirrors
`store.go`'s existing `episodeStore`/`pgxEpisodeStore` split). 25 new
tests total across `safego`, `alerting`, `approval`, `cmd/gateway`, and
`episode`, each proving the real production code path — not just the
generic recovery helper in isolation — survives a deliberately-injected
panic and its next iteration/call still runs normally. **Verified
2026-07-25 with a real toolchain: `go build ./...`, `go vet ./...`,
`go test ./...` all clean, 0 failures in both `eami-api` and
`eami-gateway`** (full module runs, not just the touched packages).
General code-review pass (one pass, both modules' diffs together): clean
— one trivial redundant `rule := rule` loop-variable capture (harmless
under Go 1.22+, this toolchain is confirmed `go1.26.5`) flagged and
removed before commit; no goroutine leaks/races/deadlocks, no sensitive
data newly reachable via panic-value/stack logging, `ctx.Done()`/shutdown
paths in both `Run()` loops unaffected, all new test seams confirmed
confined to `_test.go` files. Full writeup in `BUILT.md`'s `eami-api` and
`eami-gateway` sections (split across both, since this task touched both
modules in one session); `BACKLOG.md` B-027 entry cross-references both.

Prior entry, still accurate: 2026-07-24 by Claude Code — B-026: eami-api's RS256 user-session JWT
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
