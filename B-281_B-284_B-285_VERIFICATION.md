# B-281 + B-285 (+ B-284) — Verification record

Built, reviewed and live-verified on 2026-10-01 by Claude Code.
- **B-281:** one hung scanner no longer stops an endpoint from reporting.
- **B-285:** scanner failures are logged locally in full, and server-side as reason codes.
- **B-284:** "latest report" is chosen by server receive time, and agent- or browser-reported timestamps are labelled as such.

## Part A (reported before building)

- **A.1:** since item 4, each scanner already runs in its own goroutine (`runScan`), and the only blocking point was one unbounded `wg.Wait()`. `models`, `ai_apps`, `mcp_servers` and `cloud_clients` never consult `ctx`. A goroutine can't be killed, so the fix bounds the **wait**, not the scanner. It also exposed a race: a straggler finishing after `Build` returned would write into the report being sent.
- **A.2:**
  - No UI shows a scan report's own timestamp.
  - "Latest report" ordered by the agent's `collected_at` everywhere (item 4's `LATERAL` pick), while the list counts were rebuilt in arrival order, so the two could already disagree under a skewed clock.
  - **Paste Detection showed the browser-reported `occurred_at` as an unlabelled "Timestamp".**
  - So B-284 needed **two** fixes.
- **A.3:** today a hang can't pile up, because it blocks forever. Once the wait is bounded, each cycle would start a fresh copy of the hung scanner (288 a day at 5 minutes), each holding an OS thread in its syscall. Prevention: a per-scanner in-flight flag.
- **Founder decisions:**
  - D1: only short **reason codes** leave the endpoint; full detail stays local.
  - D2: `received_at` (the API insert time) is the ordering clock, with a new index.
  - D3: paste "Occurred (browser-reported)" plus a real "Received" column.

## Fix

| Area | Change |
|---|---|
| Agent `payload/builder.go` | `collect()` runs scanners in goroutines that report over a **buffered** channel, and **stops waiting at the 30 s deadline**, so a still-running scanner is `error`/`timeout`. On the deadline it first drains results that are already waiting, so a finished scanner is never mislabelled. Results are written only via `commit`, under the mutex, and only while `collect` hasn't returned, so **late writes are dropped**. A package-level **`inFlight`** map means a scanner whose previous run hasn't returned isn't relaunched (`still_running`): at most one stuck goroutine per scanner, ever. `BuildWith(cfg, log)`, with a nil-logger fallback. |
| Agent report | `scanner_errors`: per failed scanner, a code: `timeout` / `still_running` / `panic` / `error`. Panic value, stack and error text go to the **local** log only (D1). |
| API `ingest.go` | `logScannerFailures`: one structured `slog.Warn` per failed scanner (org, endpoint, report, agent, scanner, reason). Only allowlisted scanner names and codes are logged; anything else is counted, never echoed. |
| API `store/endpoints.sql.go` | `latestReportJoinSQL` orders by **`received_at`** (server, set on insert), id tie-break. |
| Migration `000026` | `idx_reports_endpoint_received (endpoint_id, received_at DESC)`. Mirrored in `schema.sql`. |
| Paste (B-284 fix 2) | The API returns `received_at`. The UI header becomes **"Occurred (browser-reported)"** and gains a **"Received"** column. The `PasteEventResp` field-count guard test is updated with a review note (a timestamp, no paste content). |

## Tests

- **Agent:** `collect` covers:
  - ok / error / panic / disabled reasons;
  - deadline-passed is `timeout`;
  - a **hung scanner doesn't block, and its late write is dropped**;
  - **no relaunch across 5 cycles** (1 launch);
  - every scanner gets a status.

  These pass **under `-race`** (3 runs, Linux Go container), as does the full agent suite.
- **API, real Postgres:** a **future-dated `collected_at` report received first** doesn't win (list and detail). Ingest of `scanner_errors` produces log lines for known entries only, the junk entries are counted, nothing injected is echoed, and the data is queryable in `endpoint_reports`.
- **Item 4's tie test** now sets equal `received_at`, so it tests a real tie.
- **The full API suite** and the **migration suite** (with 000026) pass.
- **Mutations, 4/4 caught:**
  1. ordering by `collected_at`;
  2. logging call removed;
  3. *(earlier)* `has_report` / `COALESCE`;
  4. *(earlier)* mixed-row tie.

## Live verification (a real hang, real stack)

**The real hang:** a **FIFO** placed where the `models` scanner opens a Hugging Face `config.json` (`/root/.cache/huggingface/hub/models--b281--hang/snapshots/s1/config.json`). Opening a FIFO with no writer blocks in the kernel (`wait_for_partner`). The WSL Ubuntu agent ran as a real `.deb` under systemd (root), at a 60 s interval.

| | Pre-fix agent (1.1.1, HEAD before this change) | Fixed agent (1.2.0, then 1.2.1 after the review fixes), upgraded in place, FIFO still present |
|---|---|---|
| Scans completed | **0 in 4 min** ("starting", then nothing) | **one every cycle**: 7/7 cycles (1.2.0), then 4/4 (1.2.1) |
| Reports received by the API | **0** | every cycle |
| Threads blocked on the FIFO | 1 | **exactly 1 across all cycles** (no pile-up) |
| Total OS threads | 11 | 10 → 14, then **flat at 14** (normal Go runtime growth, not per-cycle) |
| `models` in the report | — | `error`: `timeout` (cycle 1), then `still_running` |
| Other scanners | — | **9 × `ok`** with real data every cycle |
| API server log | — | one `WARN endpoint scanner failed … scanner=models reason=timeout\|still_running` per report |
| Agent local log | — | "still running at the scan deadline; reporting without it", then "not started: its previous run has not returned" |

**Skewed clock (B-284):**
- **Method:** between two real cycles, a report was sent through the real collector, as the same endpoint and with its key, carrying `collected_at` = **2027-10-01** (one year ahead) and marked `models=disabled`.
- **Why not change the system clock:** WSL2 distros share one VM clock with Docker Desktop, so that would skew the server too. Agent skew acts only through the reported `collected_at`.
- **Result:** the **old rule** (`collected_at`) would pick the skewed report (`models=disabled`) for a year. The **new rule** (`received_at`) picks the real next report (`models=error`).
- **Playwright** (fixture viewer, real login), **8/8 PASS**, cross-checked with psql:
  - Discover's list and the drawer show **"Scan failed"** for Local Models, from the real latest report.
  - Paste Detection shows "Occurred (browser-reported)" and "Received" with a real value (15:54 UTC, displayed as 09:24 PM IST), and no bare "Timestamp".
- This also gives item 4's **"Scan failed"** state its first **real** (non-synthetic) reproduction.

**Cleanup:**
- the WSL package was purged and the FIFO removed;
- the test key was revoked;
- the one future-dated test report was deleted;
- the fixture viewer was soft-deleted (the product's own delete) and its sessions removed;
- the scratch secrets were deleted.

## Reviews

- **Code review:** no blockers.
  - Fixed:
    - Medium: a nil logger would panic past the recover. Fixed with a fallback.
    - Low: select fairness at the deadline. Fixed by draining ready results first.
    - Low: gofmt drift in the touched files.
    - Low: the item-4 tie test wasn't a tie.
    - Low: the logging test's "unrecognised" line wasn't filtered by endpoint.
  - Accepted: the `inFlight` clear/check race costs at most one extra `still_running` cycle.
- **Security review:** **no HIGH.**
  - Confirmed:
    - `received_at` is DB-set only;
    - D1 holds (only constant codes leave the endpoint);
    - server logging can't be injected into and is bounded (at most 11 lines per report);
    - `inFlight` bounds stuck goroutines to 10.
  - Residual by design: a compromised agent can still send old content as a new report.

## Follow-ups (proposed, not minted: B-IDs need the founder)

1. **MEDIUM, pre-existing:** paste `occurred_at` is unbounded (`validatePasteEvent`). `paste_events` is a hypertable partitioned on it, with 90-day retention, so a compromised agent or service-key holder can backdate pastes past retention, pin future ones at the top of the list, or spread timestamps across many chunks (a TimescaleDB catalogue DoS). Clamp to now−7d…now+5m, or reject outside it.
2. **MEDIUM, pre-existing:** the API's ingest routes (`/v1/ingest/batch`, `/v1/reports`) have no `http.MaxBytesReader`. The collector caps agent bodies at 10 MB, but a direct API caller with the service key isn't capped. Also cap `agent_id`/`hostname` length.
3. **LOW:** `idx_reports_endpoint (endpoint_id, collected_at DESC)` now has no reader, so it's write overhead. Migration 000026 is a plain `CREATE INDEX`, which blocks writes while it builds; note that for large deployments (use `CONCURRENTLY` out of band if needed).
4. **LOW:** alert when a scanner is persistently `still_running` (a planted FIFO or a hung mount can keep it blind; it's now visible but not alerted).
5. **LOW, pre-existing:** unbounded waits outside the scan deadline (`osVersion()` on darwin runs `sw_vers` with no timeout).
6. **LOW:** a code-review rule for detection packages: never put secrets in errors or panics, because they are logged locally.
7. **Info:** `received_at` and `scanner_errors` are not in `api/openapi.yaml` (Architect-EAMI).
