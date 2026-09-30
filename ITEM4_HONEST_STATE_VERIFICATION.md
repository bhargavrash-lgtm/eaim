# Master-sequence item 4 — Honest scanner state: verification record

Built, reviewed and live-verified on 2026-09-30 by Claude Code. This is `AI_ITAM_EPIC_MASTER_SEQUENCE.md` item 4 (epic B-280). It has no B-ID of its own, following the Agent Lineage precedent.

## The problem

A detection category on Discover's list and in `EndpointDrawer` showed "0" / "None detected" for four indistinguishable cases:
- never reported;
- scanner disabled;
- scan failed;
- ran and found nothing.

## Part A (reported before building)

- **Never reported** is real, and the signal is **"no scan report exists"**, not `last_seen`. A paste event creates an `endpoints` row with no report (`ResolvePasteSourceEndpoint`), and paste events also bump `last_seen`.
  - `first_seen` isn't shown anywhere. `last_seen` is shown in Discover.
- **Per-report scanner enablement wasn't tracked, and can't be recovered.**
  - A disabled scanner, one that ran and found nothing, and one that errored all marshal their field as JSON `null` (Go nil slices).
  - So every pre-existing report is honestly **"not known"**.
- **Shapes:**
  - the list's AI app/model/MCP counts come from child tables rebuilt per report, and GPUs from the latest report;
  - the detail returns `latest_report` raw;
  - the collector buffers and the API stores the raw report, so a new report field reaches Postgres with **no schema migration**.
- **Founder decisions:**
  - D1: "Scan failed" is its own state.
  - D2: the new fields are typed locally, with the `openapi.yaml` drift logged for Architect-EAMI (the contract isn't edited).
  - D3: the Windows MSI agent is left as the real "Not known" example.
  - Handoff: the Admin/Endpoint Detail brief must carry `scanner_status` and `has_report` into Endpoint Detail from the start (noted on item 4 in the master sequence).

## Fix

| Layer | Change |
|---|---|
| Agent `payload/builder.go` | The report gains `scanner_status` (per scanner `ok` / `disabled` / `error`) via `runScan`. `error` covers an error return, a panic, **or the 30 s scan deadline having passed** (several scanners return a partial or empty list with a nil error on `ctx.Done()`, and calling that `ok` would show a truncated result as a real "0"). |
| API `store/endpoints.sql.go`, `api/discover.go` | List, detail and link read-back gain `has_report` and `scanner_status`. All three use **one** `LEFT JOIN LATERAL` latest-report pick (ties broken by id), so status, GPU count and `latest_report` always come from the same row, and the report is read once. |
| API, a pre-existing bug fixed | `COALESCE(e.agent_version, '')`. A paste-created endpoint has a NULL `agent_version`, and scanning it into a Go `string` made `GET /v1/endpoints` return **500 for the whole org**. Found by this item's own test. It blocks the "Never reported" state otherwise. |
| UI `components/endpoints/scannerState.ts`, `CategoryStateLabel.tsx`, `DiscoverPage.tsx`, `EndpointDrawer.tsx` | Per category: **Never reported** / **Disabled** / **Scan failed** / the **real count** (a genuine `0` only when the status is `ok`) / **Not known**. With no status (an older agent), items still prove the scanner ran, so count > 0 shows the count; an empty category shows "Not known", never "0". The drawer shows "Never reported" for a report-less endpoint. |

## Tests

- **Agent:**
  - `TestRunScan_RecordsEachStatus`: ok / error / panic / disabled, and a disabled scanner's function never runs;
  - `TestRunScan_DeadlinePassedIsError`;
  - `TestBuild_ScannerStatusCoversEveryScanner`: all 10 keys, and disabled exactly where config says.
  - The full agent suite passes.
- **API, real Postgres (`endpoint_scanner_status_pg_test.go`):**
  - never reported (through the real paste path), legacy (not known), current (**the latest report wins** over an older one), and **downgraded** (an older report with status, the latest without it, gives "not known", not stale), on both list and detail;
  - a `collected_at` **tie** gives status, GPU count and `latest_report` from one row.
  - The full `eami-api` suite passes.
- **Mutations, 4/4 caught:**
  1. `has_report` always true;
  2. status taken from any report that has one;
  3. `COALESCE` removed;
  4. status from a different row on a tie.
- **UI:** `tsc --noEmit` and `vite build` pass.

## Live verification (real stack; Playwright UI vs psql)

| Endpoint | How the state was produced | State shown |
|---|---|---|
| `item4-paste-only` | **Real** paste relay (`POST /v1/ingest` through the collector, with a dedicated key) from an identity that never scanned: 0 reports, 1 paste, NULL `agent_version` | **Never reported**, on all list cells and in the drawer |
| `Bhargavtej` (WSL, real `.deb` 1.1.0, then **upgraded in place to 1.1.1**, config kept) | **Real** local config `enabled_scanners` without `models`/`gpu`, 60 s interval | Local models and GPUs: **Disabled**. AI apps, MCPs and the other drawer sections: **0** (ok, found nothing) |
| `Bhargav_tej` (the real Windows MSI agent, unchanged, D3) | A real older agent without `scanner_status` | AI apps **3** and GPUs **1** (items prove it ran). Everything empty: **Not known** |
| `item4-synthetic-error` (**synthetic**, labelled) | See below | Network Activity: **Scan failed** |

- **Result:** **41/41 PASS** on the first build, and again after the review fixes, with the rebuilt API and agent 1.1.1.
- **"Scan failed" isn't producible by a real mechanism on this Linux host.** Most Linux scanners swallow errors: `network_activity` treats a missing `/proc/net/tcp` as no data, and `gpu` never errors. As root, the agent can't be denied a file by permissions.
- **Two attempts, both reverted:**
  - A systemd `InaccessiblePaths=/proc/net/tcp` drop-in failed at namespace setup, because `/proc/net` is a per-process symlink, and crash-looped the unit.
  - `ProcSubset=pid` produced a genuine `ok`.
- **So the error state is covered by:** the agent unit tests (error, panic, deadline), the real-Postgres API test, and, for the UI rendering only, **one report sent through the real collector from a clearly named synthetic identity**. That report is the WSL agent's real latest report with exactly one status edited to `error`. It was **deleted after the run**.
- **Cleanup:**
  - the WSL package was purged and the test drop-in removed;
  - 3 test keys were revoked and the scratch secrets deleted;
  - the synthetic endpoint and its report were deleted;
  - the fixture viewer was soft-deleted the way the product deletes users, with its refresh tokens removed.
  - **Kept:** the real `item4-paste-only` endpoint, as a "Never reported" fixture for the Endpoint Detail brief.

## Review

Code review only; the brief requires no security review, because Part A found no new data exposure. The new field carries scanner names and statuses only.
- **M1 (fixed):** a scan cut off by the deadline was recorded as `ok`.
- **L2 (fixed):** separate latest-report subqueries could mix rows on a tie, and each decoded the report again.
- **L3 (fixed):** the stale "store.AgentEndpoint is frozen" comments were reworded. It was already extended after B-051, by B-164.
- **L4 (fixed):** the label's accessible name. The visible label now plus screen-reader text, following the `PolicyBadges` precedent; `gray-500` for about 4.8:1 contrast; no class conflict. Residual: the sr-only reason still contributes to the drawer section button's name, which is verbose but accurate.
- **L5:** the tie case and the deadline case were added.
- **L1 (pre-existing, not fixed):** a hung scanner is not handled; see the follow-ups.

## Found during, not fixed (pre-existing) — proposed follow-ups, not minted

1. **A hung scanner blocks `Build` forever.** `models`, `ai_apps`, `mcp_servers` and `cloud_clients` never consult `ctx`, so `wg.Wait` can hang and no report is sent. Needs a per-scanner timeout that marks stragglers `error`.
2. **The Agent version column is blank for every endpoint.** Stored versions are `''`, and the UI's `?? '—'` only catches null.
3. **`eami-agent.service` puts `StartLimitIntervalSec` in `[Service]`** (systemd ignores it; it belongs in `[Unit]`).
4. **A paste-only endpoint can't be linked from the drawer**, because the link control only renders with a report. This matters for Endpoint Detail.
5. **Clock skew:** `collected_at` comes from the agent, so a skewed agent can make an older scan "latest".
6. **`recover()` in `runScan` swallows panics without logging.**
7. **`Section`'s open state is a `useState` initialiser**, so it doesn't follow a refetch.
8. **`api/openapi.yaml` lacks `has_report` and `scanner_status`** (D2: logged for Architect-EAMI).
