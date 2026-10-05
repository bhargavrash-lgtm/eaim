# B-293 (master-sequence item 8a) — The agent applies remote config correctly: verification record

**Date:** 2026-10-05 · **By:** Claude Code · **Roadmap:** AI ITAM master sequence, Phase 2, item 8a (a prerequisite for B-269 presets and B-270's "applied config").
**Part A:** `B-293_PART_A_INVESTIGATION.md`. **Agent build:** 1.3.1.

## 1. Founder decisions (all approved 2026-10-05, with guards)

| # | Decision |
|---|---|
| D-a | An empty scanner list is allowed and means **no scanners**. Confirm it renders as all disabled, and that no "empty means all" remains anywhere. |
| D-b | Explicit allow-list. Release rule in CLAUDE.md: new scanner names ship server-side first, agent second. |
| D-c | A 404 deletes the saved config and reverts to local, **only after two consecutive 404s** and only for config tied to the agent's current identity. |
| D-d | The MSI leaving the state folder is accepted; Low follow-up **B-294**. |
| D-e | Enforce `max_report_size_bytes` with the `too_large` marker and a deterministic, documented drop order. Never drop `scanner_status` or the config fields. |
| D-f | `model_file_size_mb` is remotely settable, with a rollback and a default of 100. |
| D-g | Agent-side bounds from B-277, with specific reason codes (never generic). The path allowlist and walk-depth limit stay in B-277. |
| Security | A hash-valid but hostile config test: the hash proves integrity, not authenticity, so the bounds checks carry the security weight. |

## 2. The contract, as built

**Wire format.** `GET /v1/agents/{agent_id}/config` (the agent's poll, through the collector proxy), plus `GET` and `PUT /v1/gateway/agents/{agentId}/config` (admin):

```json
{ "agent_id": "…", "updated_at": "…",
  "scan_interval_seconds": 60, "enabled_scanners": [], "model_scan_paths": [],
  "model_file_size_mb": 100, "max_report_size_bytes": 5242880,
  "config_version": "c1:e75c2c45…" }
```

- Every field is always present. `[]` is a real value and is never sent as `null`. `enabled_scanners: []` means **no scanners** (D-a).
- **PUT:** `[]` is accepted for both lists; an absent field still means unchanged.

**`config_version` canonical form** (the `c1` scheme):

1. Take the five fields.
2. Sort and de-duplicate both lists. `nil` is the same as `[]`.
3. Encode compact JSON with keys in **this** order and no HTML escaping:
   `enabled_scanners`, `max_report_size_bytes`, `model_file_size_mb`, `model_scan_paths`, `scan_interval_seconds`.
4. Compute SHA-256. The version is `"c1:" + lowercase hex`.

It's implemented twice: `eami-api/internal/store/agent_config_version.go` and `eami-agent/internal/remoteconfig/remoteconfig.go`. Both are pinned by the same three golden vectors, which were also recomputed independently in Python.

**It proves integrity, not authenticity.** Anyone serving a config can compute its hash, so it is used only to detect corruption, both in transit and in the state file. The bounds checks carry the security weight.

**Agent bounds** (D-g; the server checks the same on PUT). Every rejection keeps last-known-good and has its own reason code:

| Check | Code |
|---|---|
| Response over 256 KiB (enough for the largest server-valid config even fully escaped; raised from 64 KiB after code review L6) | `response_too_large` |
| Not JSON | `malformed_json` |
| Wrong JSON type | `wrong_type` |
| Versioned but a field missing or `null` | `incomplete` |
| Hash doesn't match the content | `version_mismatch` |
| Interval outside 60–86400 (rejected, never clamped) | `interval_out_of_range` |
| `max_report_size_bytes` outside 1–50 MiB | `report_size_out_of_range` |
| `model_file_size_mb` outside 1–100000 | `model_size_out_of_range` |
| More than 32 paths | `too_many_paths` |
| Empty path | `path_empty` |
| Path over 1024 bytes | `path_too_long` |
| Path not absolute (POSIX `/`, `X:\`, `X:/`; checked by shape on every OS) | `path_not_absolute` |
| Network, UNC or device path (`\\host\x`, `//host/x`, `\\?\…`, `\\.\…`): the SYSTEM agent would authenticate to that host as the machine account (security review M-2) | `path_network` |
| Control character or NUL in a path | `path_invalid_chars` |
| More than 32 scanner names | `too_many_scanners` |
| Config request failed / non-200, non-404 | `fetch_failed` |
| Saved file: wrong owner, loose permissions, or a symlink/reparse point | `state_untrusted` |
| Saved file: unreadable, oversized, unknown field, or hash mismatch | `state_corrupt` |
| Saved file: hash-valid but out of bounds | `state_invalid` |
| Saved file: written for another agent ID or collector | `state_stale` |
| Saved file: write or delete failed | `state_write_failed` |

- **`/` is still accepted.** Restricting which roots may be walked is B-277's path allowlist, which stays open. `TestParseResponse_RootPathIsHashValidAndInBounds` pins this so it stays visible.

**Compatibility.**
- Replace semantics apply only to a response with a non-empty `config_version` that is complete and hash-verified.
- Anything else is merged exactly as before B-293: a field applies only when it is non-zero or non-empty, and `max_report_size_bytes` stays ignored. A merged config is never persisted, but its fields are still bounds-checked.
- A body without the four fields every pre-B-293 server sends (`{}`, `null`, a captive portal page decoded as JSON) is not a config at all and is rejected as `incomplete` (security review L-1).
- An **old agent** (1.2.x) decodes the new response with Go's lenient decoder: it ignores `config_version` and `model_file_size_mb`, and reads `[]` as "unchanged". So it keeps merging.

**Persistence.**

| OS | State file | Protection |
|---|---|---|
| Linux | `/var/lib/eami-agent/remote-config.json` | `StateDirectory=eami-agent`, mode 0700; file 0600; owned by root (the agent's euid) |
| macOS | `/Library/Application Support/EAMI/Agent/remote-config.json` | same checks |
| Windows | `%ProgramData%\EAMI\Agent\remote-config.json` | protected DACL: SYSTEM, Administrators and the agent's own user, full control |

- **Writes:** temp file in the same directory → fsync → rename. Only verified versioned configs are written, and only when the version changes.
- **The admin's YAML is never rewritten** (B-278 #4).
- **Purge:** `.deb` purge and `.rpm` erase remove the directory (`postremove.sh`). The MSI doesn't yet (B-294).
- **Every directory from the OS base down is checked, on every load, save and delete** (`%ProgramData%` → `EAMI` → `Agent`; `/var/lib` → `eami-agent`; `/Library/Application Support` → `EAMI` → `Agent`), not just the last one (security review H-1: a squatted or junctioned `%ProgramData%\EAMI` would otherwise redirect the SYSTEM agent's create, ACL, read and delete operations).
  - A missing directory is created **with** its protected DACL in the same `CreateDirectory` call (Windows) or `Mkdir 0700` (Unix), one level at a time, so there is no window to plant something inside it.
  - An existing directory that is a reparse point or symlink, or that is owned by anyone untrusted, is **never followed or taken over**: the store is refused (`state_untrusted`) and the agent runs on local config.
  - Only a loosened DACL or mode on a directory a trusted identity already owns, under an already-verified parent, is re-protected.
  - The file itself is then checked and opened by path; that is safe because every directory above it is controlled only by trusted identities, so nobody else can swap it in between.
- **Windows trust check:** the owner must be SYSTEM, Administrators or the agent's own user. Any allow-ACE granting write to anyone else is rejected; inherit-only ACEs are skipped; reparse points are rejected.
- **Known residual:** a standard user can still pre-create `%ProgramData%\EAMI` before the agent ever runs and so **disable persistence** on that machine (fail-closed, local config only). B-294 adds MSI pre-creation of the directory with its DACL, which closes this.

**Two consecutive 404s** (D-c, with the founder's guard):
- One 404 changes nothing.
- A second **consecutive** 404 deletes the saved config and reverts to local config. Any other response resets the count.
- It applies only to config tied to the current identity. A file for another identity was never loaded, so it is never deleted by this rule.

**Fetch timing:** a fetch runs before every scan, including the first after a restart, capped at 10 s. It runs before `BuildWith`, so it never eats into the scanners' 30 s deadline (B-281). Each scan gets a private snapshot of the config, so a scanner still running from an earlier cycle keeps the parameters it started with.

**Report and API.**
- Every report carries `config_version` (empty unless a versioned config is in force), `config_source` (`remote`, `persisted`, `local` or `defaults`) and `config_error` (the last rejection's code).
- `GET /v1/endpoints/{id}` adds `applied_config_version`, `config_source`, `config_error` and `expected_config_version`.
  - The agent-supplied codes are allowlisted; anything else comes back as `unrecognised`, and a malformed version as `null`.
  - The fields are `null` for reports from older agents, and `expected_config_version` is `null` when the endpoint is unlinked.

**Size cap** (D-e): `max_report_size_bytes` is enforced on the marshalled report.
- **Drop order** (fixed, documented in `payload.DropOrder`): `models`, `ai_processes`, `nodejs_ai`, `python_envs`, `network_activity`, `browser`, `mcp_servers`, `ai_apps`, `cloud_clients`, `gpu`.
- Whole sections are dropped one at a time until the report fits. Each dropped scanner is marked `scanner_status: error` and `scanner_errors: too_large`.
- Report identity, `scanner_status` and the config fields are never dropped.
- The cap applies only while a versioned remote config is in force. Local-only agents stay uncapped, as before.

**`model_file_size_mb`** (D-f): migration 000027 adds the column, default 100 (the agent's own default) with a range check. The `down` migration drops the column. It also backfills any `'{}'` scanner list to the full list, because `'{}'` meant "all" before D-a.
## 3. Reviews (both mandatory; run on the first complete build, all fixes re-tested and re-verified live on 1.3.1)

**Security review** (`hash-valid but hostile` required by the founder).

The verdict was that the hash is used only to detect corruption, and the bounds checks carry the security weight on both the live fetch path and the persisted-state load path. The reviewer confirmed this with extra tests. Findings and dispositions:

| # | Finding | Disposition |
|---|---|---|
| **H-1** | Windows: only the last path component was checked. A standard user could pre-create `%ProgramData%\EAMI` as a junction they own and redirect the SYSTEM agent's create, re-ACL, read and delete, including arbitrary file deletion via `\RPC Control`. | **Fixed.** Every directory from `%ProgramData%` (or `/var/lib`, `/Library/Application Support`) down is checked on every load, save and delete. Missing ones are created *with* the protected DACL in one `CreateDirectory` call. A reparse point, symlink or untrusted owner is refused, never followed or taken over. Tests: `TestStore_WindowsJunctionInChainIsRefused` (a real `mklink /J`; the target stays untouched) and `TestStore_UnixSymlinkInChainIsRefused`. Residual: a pre-created `EAMI` disables persistence (fail-closed); B-294 adds MSI pre-creation. |
| **M-1** | The saved file held the *filtered* scanner list with the server's hash of the *unfiltered* list, so it failed as `state_corrupt` on every restart in exactly the mixed-version fleet D-b is for. Confirmed by the reviewer. | **Fixed.** The server's config is persisted as received; filtering happens only when it is applied. Test: `TestManager_PersistedConfigWithUnknownScannerSurvivesRestart`. |
| **M-2** | UNC and device paths (`\\host\x`, `//host/x`, `\\?\`) counted as absolute. A hostile server could make the SYSTEM agent authenticate as the machine account to any host, and that is now persisted. | **Fixed** on the agent and the API: new code `path_network`, PUT 400. Tests on both sides. Live: fake-collector step f8. |
| **L-1** | `{}` or `null` with status 200 was treated as an older server's config and quietly dropped the versioned-only settings. | **Fixed.** A legacy response must carry the four fields every pre-B-293 server sends, else `incomplete`. Test: `TestParseResponse_EmptyBodyIsNotALegacyConfig`. Live: step f9. |
| L-2 | Redirects are followed with `X-API-Key`, and plain `http` is allowed. Both pre-existing, but they matter more now that config persists. | **Logged on B-276** (refuse redirects; warn on non-https). |
| L-3 | `unknown scanner %q` echoes the input (pre-existing, B-271). | **Logged** in NOTES.md. |
| Info | D-c 404 handling, resource limits, raw-error check, API org scoping, migration, `postremove.sh` | All confirmed sound. |

**Code review.** No Critical or High findings. All three standing checks passed, with one drift gap (L5, now fixed).

| # | Finding | Disposition |
|---|---|---|
| **M1** | Existing `agent_configs` rows could hold paths the new rules reject, so the agent would reject the whole config indefinitely. | **Pre-deploy check** query (§6). It was run on the dev DB and found **0 rows**. Logged in NOTES as a must-run before any deploy with existing data. |
| **M2** | Two 404s after a *legacy merge* could delete another identity's state file. | **Fixed.** The file is deleted only when it was loaded or written for this identity during this run. Test: `Test404sAfterLegacyMergeLeaveAnotherIdentitysFile`. |
| L1, L2 | Stale comments (`""` handling; "empty means all" in `builder.go`) | **Fixed.** |
| L3 | `EnforceMaxSize` counted empty slices as data and could overwrite a `timeout` reason. | **Fixed:** only non-empty sections of scanners with status `ok` are dropped. Test: `TestEnforceMaxSize_SkipsEmptyAndFailedScanners`. |
| L4 | A shutdown during the fetch still ran a full scan. | **Fixed:** the loop returns if ctx is done after the fetch. |
| L5 | Drift rows missed the new 500 on `GET /v1/endpoints/{id}`, and C18 cited a file not yet present. | **Fixed:** C19 records the 500; this file lands in the same commit. |
| L6 | A server-valid config, fully escaped, could exceed the 64 KiB cap. | **Fixed:** the cap is now 256 KiB (agent response and state file). |
| Info | Non-root noise; UI can't express `[]`; a `c2` hash-scheme release rule; applied-equals-expected with an unknown scanner; 404 counter resets on restart; the DB's doubled-backslash default; a connection not closed in the migration test; the rpm erase comment | Connection close and comment **fixed**. The B-270-relevant items are **logged on B-270**; the rest in NOTES. The 404 counter reset and the doubled backslash are by design or pre-existing (B-277). |
## 4. Live verification: the real packaged agent (WSL Ubuntu, `.deb`), real stack

**Setup:**
- `eami-agent_1.3.0_amd64.deb` built from this branch with nfpm v2.41.1, installed over the earlier 1.2.1 (so it is also an upgrade test).
- **Two runs.** Tests c, a, D-a, d and b below ran on **1.3.0**, the first complete build. The reviews then changed agent code, so every test was **repeated on 1.3.1** (the committed build), along with the size cap, the remote model size and the state-file cases: see "Re-run on the final build" below. e and f are reported on 1.3.1.
- Collector at `172.31.160.1:8888`, with a per-agent key.
- The WSL endpoint `Bhargavtej` (`41b86b46…`) linked to the fixture governed agent `b293-fixture-agent`.
- Config changed through the **real admin API** (`PUT /v1/gateway/agents/{id}/config`, a fixture admin login).
- Reports read from `endpoint_reports` (psql) and from `GET /v1/endpoints/{id}`.
- Model fixtures: `/srv/b293-models/b293-weights.bin` (150 MB, sparse) and `/srv/b293-many` (8000 sparse 2 MB files).

**An honesty note on timing:** the machine suspended for about an hour, 09:13 to 10:10 UTC, part-way through test c's "before" run. The agent process carried on afterwards with the same PID; no data was lost or affected.

### c. First scan already reflects remote config

**Server config:** `models` off.

| | Report | `models` | `config_source` | `config_version` |
|---|---|---|---|---|
| **Before** (old agent 1.2.1, just installed) | 09:11:34, first report | **ok**: the first scan ran on built-in defaults, ignoring the server | (absent) | (absent) |
| | 09:12:34, second report | disabled (only after the post-send fetch) | (absent) | (absent) |
| **After** (1.3.0, upgraded at 10:11:22) | 10:11:32, **first report** | **disabled** | `remote` | `c1:14ea8368…` (= the server's) |

The state file was written immediately: `600 root`, in a `700 root` directory, holding version `c1:14ea8368…`. systemd reports `StateDirectory=eami-agent` with mode `0700`.

### a. Replace, not merge (same process throughout, PID 22392, no restart)

| Step | Report | `models` | `local_models` | Version |
|---|---|---|---|---|
| **Before:** models off | 10:11:32 | disabled | n/a | `c1:14ea…` |
| PUT the **full** scanner list (models back on) | 10:12:32 | **ok** | **1** (the 150 MB file under `/srv/b293-models`) | `c1:c5be…` |
| PUT `model_scan_paths: []` (clear) | 10:14:32 | ok | **none**: the path is really cleared | `c1:bec3…` |

- The state file after the clear records `"model_scan_paths": []`.
- Under the old merge, neither step was possible: a list was applied only when non-empty, and the server rejected `[]`.

### D-a. An empty scanner list means none, never "all"

- PUT `enabled_scanners: []` → the 10:15:32 report has **all 10 scanners `disabled`** (version `c1:e75c…`).
- **Browser check** (Playwright, fixture admin, Endpoint Detail Overview): every category (MCP Servers, AI Apps, Local Models, Cloud Clients, GPUs, Network Activity, Python Environments, Node.js AI Projects) renders **"Disabled: This scanner was turned off for this endpoint in its latest scan."** None shows 0 or "None". PASS.
- Screenshot: `ui_all_disabled.png` (scratchpad).
- **No leftover "empty means all"**, by grep plus tests:
  - **agent:** `IsEnabled` is plain membership; an absent YAML key becomes the explicit full list at load (`TestLoad_EnabledScannersAbsentVersusEmpty`); `TestScannerNameListsAgree` builds with `[]` and gets all 10 `disabled`.
  - **API:** stores and serves `[]`.
  - **migration:** 000027 converts any `'{}'` row to the full list, so no endpoint silently stops scanning.
  - **UI:** `AgentConfigPanel` still requires at least one scanner, which is a UI constraint, not "empty means all".

### d. Reported vs expected config

| Moment | `applied_config_version` | `expected_config_version` |
|---|---|---|
| **Before** the PUT | `c1:bec335…` | `c1:bec335…` (in sync) |
| **Right after** `PUT enabled_scanners: []` (the agent hasn't polled) | `c1:bec335…` | **`c1:e75c2c…` (visible mismatch)** |
| After the agent's next poll (10:15:32 report) | `c1:e75c2c…` | `c1:e75c2c…` (in sync) |

### b. Persisted config survives a restart with the server unreachable

1. **Before:** saved config `c1:e75c…` (empty scanner list). Built-in defaults would run all 10.
2. `docker stop eaim-eami-api-1` at 10:16:18, then `systemctl restart eami-agent` (new PID 22594).
3. The collector's config proxy returned **502** (upstream `eami-api` not resolvable). The agent logged `remote config: not applied, keeping current config reason=fetch_failed` (a code only), then `scan complete … config_source=persisted`. The collector buffered the report.
4. `docker start eaim-eami-api-1` at 10:16:41. The buffered report was forwarded.

| Report | `config_source` | `config_error` | Disabled scanners | Version |
|---|---|---|---|---|
| 10:16:42 (the scan after the restart) | **persisted** | **fetch_failed** | **10** (the saved config, not defaults) | `c1:e75c…` |
| 10:17:32 (next cycle, API back) | remote | (none) | 10 | `c1:e75c…` |

### Re-run on the final build (1.3.1, after the review fixes)

Every live test was repeated on 1.3.1, the build being committed. Upgrading from 1.3.0 also re-pointed the agent from the fake collector back to the real one. On that first start the saved config from the fake collector's identity was ignored (`state_stale`) and the real server's config was applied and saved.

| Test | Result on 1.3.1 |
|---|---|
| **c** | The first scan after the upgrade ran `config_source=remote` with the server's empty scanner list (all 10 `disabled`). |
| **a1 + d** | PUT the full list plus `/srv/b293-models`. Right after the PUT, the endpoint showed applied `c1:e75c…` against expected `c1:c5be…` (mismatch). The next report (10:32:01) had `models: ok`, 1 model found, and applied equal to expected. |
| **a2** | PUT `model_scan_paths: []`. The 10:33:01 report found no models; version `c1:bec3…`. |
| **D-f** (remote `model_file_size_mb`) | PUT `model_file_size_mb: 1`, path `/srv/b293-many`, cap 5 MiB. The 10:34:02 report listed **8000** model files (2 MB each, invisible at the default of 100), raw size 1,269,711 bytes. |
| **D-e** (size cap) | PUT `max_report_size_bytes: 1048576`. In the 10:35:01 report, **only `models`** was dropped: `scanner_status.models = error`, `scanner_errors = {"models": "too_large"}`. The other 9 scanners stayed `ok`, the config fields were intact, and the report was 845 bytes. |
| **b** | Config set to 2 scanners (8 disabled). `docker stop` on the API at 10:36:19, then an agent restart. The agent logged `reason=fetch_failed` and scanned with `config_source=persisted`. The forwarded report (10:37:51) had `persisted`, `fetch_failed`, 8 disabled, version `c1:01e7…`. |
| **f, tampered state file** | Interval changed 60 → 61 in the file (hash no longer matches); the API was still down. Logged `saved config ignored reason=state_corrupt`, then `config_source=local` (0 disabled, version empty). |
| **f, loosened state file** | Valid contents, mode 644. Logged `reason=state_untrusted`, then `config_source=local`. |
| **f, restored** | Valid file, mode 600. `config_source=persisted` again. |
| **API network-path rule** (rebuilt API) | PUT `//nas/models` returned 400 "must be local paths, not network (UNC) paths"; a local path returned 200. |

**Reporting note:** with the API down, the two rejected-file scans report `config_error=fetch_failed`. That field holds the **latest** rejection, and the fetch failure came after the state rejection. The state reason code is in the agent's local log. This is recorded as a limitation, and is relevant to B-270.

### e and f: hostile and older-server responses (fake collector in WSL, agent 1.3.1)

**How it ran:** the agent was pointed at `127.0.0.1:18888`, which is a new identity, so the saved config was ignored with `state_stale`. The fake serves one crafted response per agent cycle and records each report.

**"State" below** is the state file's version and modification time: the last-known-good file was untouched (10:38:23) through every rejection.

| Step | Served | Agent log | Report | State |
|---|---|---|---|---|
| f0 | A good versioned config | (applied) | `c1:da8d…`, `remote` | written |
| f1 | A 300 KiB body | `response_too_large` | `c1:da8d…`, `config_error=response_too_large` | unchanged |
| f2 | Interval sent as a string | `wrong_type` | `c1:da8d…`, `wrong_type` | unchanged |
| f3 | **Hash-valid** config with interval 1 s | `interval_out_of_range` | `c1:da8d…`, `interval_out_of_range` | unchanged |
| f4 | **Hash-valid** config with path `../../etc` | `path_not_absolute` | `c1:da8d…`, `path_not_absolute` | unchanged |
| f5 | Versioned, `model_scan_paths` missing | `incomplete` | `c1:da8d…`, `incomplete` | unchanged |
| f6 | Version doesn't match the content | `version_mismatch` | `c1:da8d…`, `version_mismatch` | unchanged |
| f8 | **Hash-valid** config with path `\\attacker\share` | `path_network` | `c1:da8d…`, `path_network` | unchanged |
| f9 | `{}` (captive portal) | `incomplete` | `c1:da8d…`, `incomplete` | unchanged |
| f7 | A good change (`ai_apps`, `gpu`, `models`) | (applied) | `c1:0a65…`, no error | written (10:48:23) |
| **e1** | An **older-server** response with no `config_version`: interval 90, `enabled_scanners: []`, paths `["/srv/b293-models"]` | (merged) | version `""`, `remote`. **`[]` was ignored** (scanners unchanged), **the path was applied** (1 model found). | **not rewritten** (still `c1:0a65…`, 10:48:23) |
| e0 | Back to a versioned config | (applied) | `c1:0a65…` | unchanged (same version) |
| c1 | First 404 | (nothing) | `c1:0a65…`, `remote` | kept |
| c2 | **Second consecutive 404** | `endpoint has no remote config; reverted to local config` | version `""`, `config_source=local`, all 10 scanners (the YAML's absent list means all) | **deleted** |

The same sequence on 1.3.0, before the review fixes, gave the same results for f0–f7 and e1.

## 5. Tests added

**Agent `internal/remoteconfig`:**
- Golden vectors, the same as the API's and recomputed in Python.
- A parse table covering every reason code, including the hash-valid hostile cases.
- Legacy merge.
- `{}` and `null` rejected as `incomplete`.
- Absolute versus network paths.
- **Manager:** replace (re-enabling a scanner, clearing paths), empty list means none, private snapshots, persistence across a restart, last-known-good kept on rejection, two consecutive 404s, another identity's file kept (including after a legacy merge), the unfiltered config persisted (M-1), unknown scanners ignored.
- **Store:** round trip; garbage, oversized, edited, unknown-field, hash-valid out-of-bounds and stale files.
- **Unix:** permissions and symlinks, including a symlink in the directory chain.
- **Windows (native):** DACL loosened and re-secured, and a real junction in the chain refused with its target untouched.

**Agent `config`:** an absent list versus an explicit `[]`.

**Agent `payload`:** fixed drop order, `too_large` marking, status and config fields never dropped, no relabelling of empty or failed scanners, and the builder, `DropOrder` and `AllScanners` name lists agreeing.

**API:**
- `store.Version` golden vectors and order and duplicate independence.
- Real Postgres: full config and empty lists, PUT bounds including network paths, an unchanged config keeps its version; endpoint applied versus expected, the mismatch, unlinked, older agent, and allowlisting of hostile agent-supplied text.
- An existing store test was updated for the new required field.

**Migration:** up, backfill, CHECK constraint and down (`b293_agent_configs_test.go`). Fresh-versus-incremental schema parity also passes with 000027.

**Suites:** the full agent suite on Windows, the Linux test binaries in WSL as root, the full API suite, and `schema/migrationtest` all pass.

## 6. Pre-deploy check (code review M1)

Run this before deploying B-293 anywhere with existing `agent_configs` data. Any row it returns would be rejected by the agent as a whole, every cycle, until fixed. On the dev DB it returned **0 rows**.

```sql
SELECT ac.agent_id, g.name, ac.model_scan_paths
FROM agent_configs ac JOIN gateway_agents g ON g.id = ac.agent_id
WHERE cardinality(ac.model_scan_paths) > 32
   OR cardinality(ac.enabled_scanners) > 32
   OR ac.scan_interval_seconds NOT BETWEEN 60 AND 86400
   OR ac.max_report_size_bytes NOT BETWEEN 1048576 AND 52428800
   OR EXISTS (SELECT 1 FROM unnest(ac.model_scan_paths) p
              WHERE p = '' OR octet_length(p) > 1024
                 OR EXISTS (SELECT 1 FROM generate_series(1, length(p)) i WHERE ascii(substr(p, i, 1)) < 32 OR ascii(substr(p, i, 1)) = 127)
                 OR (substr(p, 1, 1) IN ('/', chr(92)) AND substr(p, 2, 1) IN ('/', chr(92)))
                 OR NOT (substr(p, 1, 1) = '/'
                         OR (substr(p, 1, 1) ~ '[A-Za-z]' AND substr(p, 2, 1) = ':' AND substr(p, 3, 1) IN ('/', chr(92)))));
```

## 7. Rollout reality and limitations

- **Old agents keep merging until they update.** Agents older than 1.3.0 ignore `config_version` and read `[]` as "unchanged". They can't clear model paths or switch a scanner back on, and they report no config fields, so the API shows `null` ("not known"). Clearing and re-enabling reach an endpoint only once its agent is on 1.3.0 or later.
- **B-277 remainder:** `/` is still accepted as a path. The path allowlist or local opt-in and the walk-depth limit stay in B-277.
- **Windows:**
  - A standard user who pre-creates `%ProgramData%\EAMI` before the agent first runs disables persistence on that machine. It fails closed: the agent runs on local config.
  - The MSI leaves the folder on uninstall.
  - B-294 covers both, via MSI pre-creation and removal.
  - The installed Windows MSI on this machine was **not touched**. The Windows code was verified by native unit tests, including a real junction, not by a live Windows service.
- **`config_error` is the latest rejection only;** see the reporting note above.
- **UI:** `AgentConfigPanel` still can't express `[]` (logged on B-270; B-269 presets are the editor).
- **The legacy downgrade is as approved:** an older server's response merges onto the current config and drops the versioned-only settings (size cap, remote model size) until the next versioned response. It is never persisted, so a restart restores the saved versioned config.

## 8. Cleanup

- **WSL:** the package is purged (`/var/lib/eami-agent` was removed by `postremove.sh` on purge), `/etc/eami` is removed, and the model fixtures and fake collector are removed.
- **Collector:** the key for `Bhargavtej` is revoked.
- **Dev DB, in one transaction:**
  - the WSL endpoint is unlinked;
  - `b293-fixture-agent` is deleted (its `agent_configs` row cascaded);
  - the fixture admin is soft-deleted;
  - 9 refresh tokens are deleted.
- **Audit log untouched:** 1706 rows before and after, with the same max id.
- Scratch password, key and token files are deleted.
- Migration 000027 **stays applied** to the dev DB (it is part of the change).
