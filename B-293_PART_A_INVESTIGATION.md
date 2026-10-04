# B-293 (master-sequence item 8a) — Agent applies remote config correctly: Part A investigation

**Date:** 2026-10-05 · **By:** Claude Code · **Type:** investigation only, no code. Build waits for founder approval.
**Read:** `AI_ITAM_EPIC_MASTER_SEQUENCE.md`, `API_CONVENTION.md`, `DISCOVERY_ADMIN_INVESTIGATION.md`, the scanner capability audit (§1, §4; chat-delivered 2026-09-30), and the code traced below.

## 0. What's true today (traced)

| Fact | Where |
|---|---|
| The agent polls `GET {collector}/v1/agent-config/{agent_id}` **only after a successful send**, so the first scan after a restart always runs on local YAML or built-in defaults. A fetch failure is logged at **Debug** (invisible at the default level). | `cmd/agent/main.go:248-256` |
| **Merge, non-empty only:** `interval > 0`, `len(paths) > 0`, `len(scanners) > 0`. It can't clear paths or switch a scanner back on, and `max_report_size_bytes` is decoded and dropped. | `internal/collector/sender.go:167-182` |
| The response body is decoded with **no size cap and no unknown-field check**. | `sender.go:168` |
| The remote config is applied by **mutating the shared `*config.Config`** in memory, and it's lost on restart. | `sender.go:139`, `main.go:253` |
| **Race:** the `models` scanner closure reads `cfg.Detection.ModelFileScanPaths` and `MinModelSizeMB` through that shared pointer inside its goroutine. A scanner still running past the deadline (B-281's `still_running`) can overlap a later `FetchConfig` write. It is benign in practice, but it is a real data race. | `internal/payload/builder.go:265-268` |
| Local `DetectionConfig.IsEnabled`: **an empty list means all scanners**. | `internal/config/config.go:54` |
| API `GET /v1/agents/{agent_id}/config` (service key): resolves endpoint → linked governed agent → `agent_configs` row; 404 when unregistered or unlinked. The response is `{agent_id, scan_interval_seconds, model_scan_paths, max_report_size_bytes, enabled_scanners, updated_at}`. **The only version-like field is `updated_at`, a timestamp.** | `eami-api/internal/api/agent_config_remote.go`, `agents.go:619,818` |
| **The API rejects empty lists:** `PUT /v1/gateway/agents/{id}/config` returns 400 when `model_scan_paths` or `enabled_scanners` is `[]` (`nil` means "unchanged"). The interval must be 60–86400 and the max report size 1–50 MB. Scanner names are checked against `store.AllScanners` (B-271). | `agents.go:701-729` |
| `agent_configs` columns: `scan_interval_seconds`, `model_scan_paths`, `max_report_size_bytes`, `enabled_scanners`, `updated_at`. **No `model_file_size_mb` column.** | `000001_baseline.up.sql:613`, `000025` |
| **The collector proxies the API response verbatim** (status and body, no cap), so new fields need no collector change. Ingest buffers the **original report body**, so new report fields ride into `endpoint_reports.report` JSONB unchanged, as `scanner_status` did. | `eami-collector/internal/api/config_proxy.go`, `ingest.go` |
| **No state directory exists on any platform.** Linux runs as root via `eami-agent.service` (no `StateDirectory`). Windows uses the registry for collector values only. macOS has nothing. | installer sources |

## 1. Wire format

**Proposed `GET /v1/agents/{agent_id}/config` response** (additive: every existing field stays, with the same meaning):

```json
{
  "agent_id": "…", "updated_at": "…",
  "config_version": "c1:3f9a…(64 hex)",
  "scan_interval_seconds": 300,
  "enabled_scanners": ["ai_apps", "…"],
  "model_scan_paths": [],
  "model_file_size_mb": 100,
  "max_report_size_bytes": 5242880
}
```

- **Every field is always present.** `[]` means empty, never "unchanged". `null` is never sent.
- **`config_version`** = `"c1:" + hex(sha256(canonical))`.
  - `canonical` is compact JSON with **fixed key order** (`enabled_scanners`, `max_report_size_bytes`, `model_file_size_mb`, `model_scan_paths`, `scan_interval_seconds`).
  - Both lists are **sorted and de-duplicated** (order has no meaning for either).
  - The `c1:` prefix versions the canonicalisation itself, so it can change later without ambiguity.
  - It is a content hash, not a timestamp. The same config gives the same version, so re-saving an unchanged config produces no false "changed" signal.
- **Where it's computed:** on read, in Go (one helper). It is not stored, so there is no column to keep in sync.
- **Same helper in the agent.** The agent recomputes it on receipt and on loading persisted state (§3). The canonicalisation lives in two Go modules (`eami-api`, `eami-agent`), so both get the **same golden test vectors** to stop the two drifting.
- **`enabled_scanners` on the wire is always explicit.** "All scanners" is sent as the full name list, never `[]`. `[]` means **no scanners**: the agent still reports, with every `scanner_status` "disabled".
  - **Decision D-a:** allow `[]` (a heartbeat-only endpoint), or keep rejecting it?
  - **Recommendation: allow it.** Under versioned replace it's unambiguous, and B-269 may want a "paused" preset.
- **API change for empty lists:** `PUT` accepts `[]` for `model_scan_paths` (meaning "no extra paths"; this is how an admin clears them) and, per D-a, for `enabled_scanners`. `nil` (field absent) keeps meaning "unchanged" in the PUT, which is unaffected.
- **`model_file_size_mb`:** new column, `INT NOT NULL DEFAULT 100 CHECK (1..100000)` (migration 000027). PUT validates the same range. See §7.
- **Admin route:** `GET /v1/gateway/agents/{id}/config` also gains `config_version` and `model_file_size_mb`, from the same helper. This is additive, and B-270 needs the expected version.

## 2. Compatibility (the main risk)

**Rule: replace semantics apply only to a response that carries a non-empty `config_version`.** Otherwise the agent behaves exactly as today (merge non-empty fields).

I confirm the rule is sufficient, **with two hardening conditions**:

1. **A versioned response must be complete and must verify, or it is rejected whole.** Every field must be present and non-null (strict decode: required-field check plus `DisallowUnknownFields` off for forward compatibility, but nulls refused), the bounds must hold (§6), and the recomputed hash must equal `config_version`.
   - If not, the agent keeps last-known-good and logs a reason code (`incomplete`, `bad_value`, `version_mismatch`).
   - So a buggy or partial server can never cause an "absent read as empty" clear. The only path to replace is a response that proves it is the full config.
2. **Unknown scanner names in a versioned list are ignored**, not fatal, with reason `unknown_scanner` logged once per version. This covers a server that knows a scanner name this older binary doesn't.
   - The reverse case is a real **forward-compatibility trade-off**: a future agent build adds an 11th scanner the server doesn't list yet. Under an explicit allow-list, that scanner is **off** until the server knows it.
   - **Decision D-b. Recommendation: accept it.** Nothing runs on an endpoint that an admin didn't enable. This is the B-271 lesson, inverted deliberately. The release process must add new scanner names to `store.AllScanners` and the migration default **before** shipping the agent.

**Old server → new agent:** today's server sends no `config_version`, so the agent merges exactly as now. Today's server also never sends `[]` (its PUT rejects it), so nothing is cleared.

**New server → old agent:** checked against `sender.go`.
- `json.Decoder` without `DisallowUnknownFields` ignores `config_version` and `model_file_size_mb`.
- Its `len > 0` checks treat a new `[]` as "no change".
- So an old agent keeps merging. It **can't clear** paths, exactly as today, with no breakage.
- That is the honest limit: clearing reaches only upgraded agents. B-270's mismatch display (§5) will show this: an old agent reports no `config_version`, so it shows as "not known", never as "applied".

**Downgrade after persistence:** if a new agent holds persisted versioned state and the server goes back to an unversioned response, the agent merges onto its current effective config, which is the persisted one. That is the same as today's merge onto in-memory config. `config_source` is `remote` and `config_version` is empty.

## 3. Persistence

**A separate state file, never a rewrite of the admin's YAML** (B-278 #4):

| OS | Path | Protection |
|---|---|---|
| Linux | `/var/lib/eami-agent/remote-config.json` | dir `0700`, file `0600`, owner root. Add `StateDirectory=eami-agent` and `StateDirectoryMode=0700` to the unit, and have the agent also create the dir itself (it runs outside systemd too). Purge removes it (nfpm `postremove` on purge only). |
| macOS | `/Library/Application Support/EAMI/Agent/remote-config.json` | dir `0700`, file `0600`, owner root |
| Windows | `%ProgramData%\EAMI\Agent\remote-config.json` | **An explicit protected DACL: SYSTEM and Administrators full control, no inheritance.** The agent sets it when it creates the directory. The default `ProgramData` ACL **lets Users create files**, so without this a standard user could plant a config that steers a SYSTEM-level filesystem walk (the B-277 class). **A pre-created directory is the same risk:** a user could create `EAMI\Agent` before the agent does and own it. So on every start the agent checks the directory's owner and DACL. If they aren't SYSTEM/Administrators-only, it treats any file there as untrusted and re-secures the directory (takes ownership as SYSTEM, re-applies the DACL) before writing. |

- **Contents:** `{format: 1, agent_id, collector_url_sha256, config_version, fetched_at, config: {…}}`.
- **Identity binding:** `agent_id` and the hash of the collector URL. **A stale-identity file is ignored:** if the agent was re-pointed at a different collector or org, it must not run the old org's config.
- **Write:** temp file in the same directory → `fsync` → rename (on Windows, Go's `os.Rename` uses `MoveFileEx` with `MOVEFILE_REPLACE_EXISTING`: it replaces the old file, but isn't strictly atomic across a crash, so a torn write is caught by the load checks below and falls back safely). Only after a **verified** versioned response, and only when `config_version` changes.
- **Load (startup), rejected in this order:**
  1. size cap 64 KiB;
  2. **ownership and permission check**: Unix owner uid 0 and `mode & 0o077 == 0`; Windows owner SYSTEM or Administrators, with no write ACE for anyone else → `state_untrusted`;
  3. strict decode → `state_corrupt`;
  4. hash equals `config_version` → `state_corrupt` (this also catches hand-tampering);
  5. bounds (§6) → `state_invalid`;
  6. identity → `state_stale`.
- **On any rejection:** ignore the file (it is overwritten by the next good fetch, not deleted), fall back to local YAML or defaults, and log **one reason code**. That is never raw error text, per the standing check and B-285; full detail goes to the local log at Debug only.
- **Not chosen:** the Windows registry. It would mean a second, differently-protected store for the same data, and a JSON file keeps one code path for three OSes.
- **404 from the config endpoint (unregistered or unlinked):** delete the persisted state and revert to local, with `config_source` `local` or `defaults`.
  - **Decision D-c. Recommendation: yes.** Unlinking is an admin decision, and "unlinked endpoints get no config" must hold after a restart too.
  - A hostile collector could fake a 404, but it could equally serve a permissive config, so this adds no exposure.

## 4. Fetch before the first scan

**Startup:**
1. Load the persisted state (§3).
2. Fetch with a **10 s timeout** (a `context.WithTimeout`, independent of the 30 s HTTP client timeout).
3. On a verified response: apply it, persist it, `source=remote`. On failure: persisted if valid (`source=persisted`), else local YAML (`local`), else built-in (`defaults`).
4. Then the first scan.

The first scan is never delayed more than 10 s.

**Each cycle:** fetch **at the start of the cycle**, before the scan, with the same 10 s cap. It no longer happens after a successful send, so a config change applies to the very next scan, and a fetch is no longer skipped when the send fails.

**Snapshot per scan:** each cycle builds an **immutable copy** of the effective config and passes it to `BuildWith`. The shared-pointer mutation goes away, and with it the §0 race. The models closure gets values, not the live struct.

**B-281 interaction:**
- The fetch runs **before** `BuildWith`, so it doesn't eat into the 30 s scan deadline.
- A scanner still running from an earlier cycle is skipped (`still_running`) as today, and **keeps the parameters it started with** until it returns. The new config reaches it on its next launch. That is correct: a walk can't be retargeted mid-flight.
- The interval change applies from the next sleep.

## 5. Reporting

**Agent → report** (rides in the JSONB like `scanner_status`):
- `config_version`: the version **in force for this scan**. Empty string when not versioned.
- `config_source`: `remote`, `persisted`, `local` or `defaults`.
  - `local` means a YAML file was read and no remote config applies; `defaults` means no YAML file.
  - `remote` covers both "verified this cycle" and "verified earlier this run".
- `config_error`: an **optional reason code** from §2, §3 and §6 when the last fetch or load was rejected, so a rejected config is visible server-side, not only in the local log.

**API exposure** (additive, `API_CONVENTION.md` principles 1–3 and 7), on **`GET /v1/endpoints/{id}` only** (B-270's consumer is the detail page):
- `applied_config_version` and `config_source`, from the latest report (by `received_at`, B-284). **Null** for reports that predate the field (honest "not known").
- `expected_config_version`: the hash of the linked governed agent's current config, from the same helper. **Null when unlinked.**
- `config_error`, when present.

The mismatch (`applied != expected`) is computed by the consumer. **No UI in 8a** (B-270). The list endpoint and CMDB rows don't get these fields now: per-row hashing would cost per page, and nothing reads them yet.

**Drift rows** (the standing check, in the build commit) in `API_CONTRACT_DRIFT.md`:
- `GET /v1/agents/{agent_id}/config` (`config_version`, `model_file_size_mb`, full-config semantics);
- `GET`/`PUT /v1/gateway/agents/{id}/config` (the same fields; `[]` accepted);
- the `Endpoint` fields above;
- the report payload's `config_version`, `config_source`, `config_error`;
- the new `scanner_errors` code `too_large` (§7).

## 6. Bounds: recommend a minimal B-277 slice here

Persistence makes B-277 worse (a bad value now survives a restart), so **yes**, the agent-side validation belongs in 8a. It applies to **every** remote or persisted config, versioned or not:
- **Response cap:** `io.LimitReader` at **64 KiB**; over the cap → `too_large`.
- **Interval** 60–86400, matching the API. **Rejected, not clamped:** a clamp would make the applied config differ from the version it reports. → `bad_value`.
- **Paths:** at most **32**, each at most **1024 bytes**, **absolute**, with no NUL or control characters. → `bad_value`.
- **Scanner names:** at most 32 entries. Unknown names are ignored (§2).
- **`model_file_size_mb`** 1–100000; **`max_report_size_bytes`** 1–50 MB (the API's range).
- **On any rejection:** the whole config is rejected, last-known-good is kept, and a reason code is logged and reported in `config_error`.

**Left in B-277:** the path **allowlist or local opt-in** and the **walk depth limit**. Those need a policy design (which roots an org may steer a root walk into) and coordinate with B-194's extension filter. 8a only makes sure what's applied is well-formed and bounded in size.

## 7. Two field decisions

- **`max_report_size_bytes`: enforce.** Recommended over dropping it, because B-287 (a server-side ingest size limit) is coming, and without an agent-side budget an over-limit report would be rejected **whole**, which is silent data loss at the server.
  - Enforcement: after the build, if the marshalled report is over the cap, drop the **largest** scanner section, mark it `scanner_status: error` with the new `scanner_errors` code **`too_large`** (B-285 style), and repeat until it fits.
  - The truncation is always visible per scanner, never silent.
  - If it still doesn't fit with every scanner section dropped (pathological), send it anyway and let the server decide.
- **`model_file_size_mb`: add it to remote config.** B-269 needs it, and it's the `models` scanner's only knob besides paths. It is the only new config field in 8a (a column, PUT/GET, the wire, the agent), and it is justified by the brief and B-269.

## 8. Rollout and verification

- **Build order:**
  1. migration 000027 and the API (helper, wire, PUT `[]`, endpoint fields, drift rows), with real-Postgres tests;
  2. the agent (canonical hash with shared golden vectors; state store per OS, Windows DACL; startup and cycle fetch; snapshot; bounds; `too_large`; report fields), with unit tests including the Windows DACL code;
  3. the Linux unit `StateDirectory` and the nfpm purge.

  The collector needs no change.
- **Unit tests run natively here:** this machine is Windows, so the Windows state-store and DACL tests run under `go test` on Windows. **No MSI install or change.**
- **Live tests on the WSL packaged `.deb`**, built from the branch as in B-281. The founder's tests **a–f**, each before and after:
  - **a, c, d:** on the real stack with a fixture governed agent linked to the WSL endpoint.
  - **b:** `docker stop eaim-eami-collector-1`, briefly, on the dev stack, then restart the agent.
  - **e** (an unversioned server response) **and f** (hostile: oversized, wrong types, extreme interval, plus a corrupt or untrusted state file): against a **small fake collector** run in WSL (a Python HTTP server serving crafted responses), with the agent's `collector.url` pointed at it. The state-file cases are edits to the file on disk.
- **Windows:** the installed MSI is **not touched**; it stays the "Not known" example. No Windows live verification is planned. If review finds the DACL path needs a live service check, I'll ask first.
- **MSI uninstall leaves `%ProgramData%\EAMI\Agent`** (removing it needs a `Product.wxs` change). **Decision D-d. Recommendation: accept it as a documented limitation in 8a** (the file holds config only, no secrets), and log a Low follow-up.
- **Cleanup as usual:** WSL purge (which also removes `/var/lib/eami-agent`), key revoke, fixture agent, leaving audit untouched.

## Decisions for the founder

| # | Decision | Recommendation |
|---|---|---|
| D-a | `enabled_scanners: []` allowed (heartbeat-only)? | **Allow** |
| D-b | Explicit allow-list means a new scanner is off until the server knows its name | **Accept** (release-process rule) |
| D-c | A 404 deletes persisted state and reverts to local | **Yes** |
| D-d | MSI uninstall leaves the state dir | **Accept for 8a; Low follow-up** |
| D-e | `max_report_size_bytes` | **Enforce**, with visible `too_large` |
| D-f | `model_file_size_mb` remote | **Add** (migration 000027) |
| D-g | Minimal B-277 slice in 8a (cap, interval, path count, length and shape) | **Yes**; allowlist and depth stay in B-277 |
