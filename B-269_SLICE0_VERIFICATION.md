# B-269 Slice 0 — B-277 path rules and depth limit, plus the B-194 file-type filter: verification record

**Date:** 2026-10-06 · **By:** Claude Code · **Agent build:** 1.3.2 · **Plan:** `B-269_SLICE0_PLAN.md` · **Decisions:** `DISCOVERY_PRESETS_DESIGN.md` §12 (D9, D10, S1–S7).

## 1. What was built

**Agent (1.3.2):**
- **`internal/scanpath` (new):** path-shape rules shared by the scanner and remoteconfig.
  - `IsNetwork`, `IsAbsolute`, `IsRoot`.
  - **`IsNormalized`:** no `.` or `..` part, and no part ending in a dot or a space.
  - **`HasStrayColon`:** a `:` after the drive letter, which on Windows selects an NTFS stream.
- **`models` scanner, one shared walk (`scanDir`):**
  - an **extension set** per source. LM Studio keeps `.gguf` and GPT4All `.gguf`/`.bin`. **Configured paths** use `ModelFileExtensions`: `.gguf .ggml .safetensors .bin .pt .pth .ckpt .onnx .tflite .h5 .keras .pb .mlmodel .llamafile`.
  - a **depth limit of 8** (`MaxWalkDepth`), recorded as `depth_limited` when hit.
  - **stopping at the scan deadline** (a ctx check per entry).
- **Configured paths are resolved through links before walking** (`resolveScanRoot`: `EvalSymlinks`, then root, network and same-file-as-root checks). They are skipped with the note `path_root` or `path_network`.
  - Hits are labelled **`scan_path`** (S2), which used to be mislabelled `lm_studio`.
  - The HF walk gets the same depth limit and ctx stop.
- **Report:** a new optional `scanner_notes: {"models": [...]}` (codes only).
- **`remoteconfig.Validate`:** new codes `path_root` and `path_not_normalized`; a stray colon is `path_invalid_chars`. The whole config is refused and last-known-good kept.
- **`payload.collect`:** a scanner that stops at the deadline and returns the context's error is recorded as **`timeout`**, not `error` (code review M1).

**API:**
- **`store/agent_config_limits.go`:**
  - the same rules, as stable codes: `ValidateModelScanPaths`, always applied, now adds `path_root`, `path_not_normalized` and the stray colon;
  - **`ValidateModelScanPathsFull`** adds `path_profile_parent`: `/home`, `/Users`, `X:\Users`, and the aliases `X:\Documents and Settings`, `/System/Volumes/Data/Users`, `/var/home`;
  - **`PathWarnings`**.
- **Config PUT:**
  - every 400 is `{code, field, message}`. The message is fixed text and **never echoes the value**, so the old `unknown scanner %q` echo is gone.
  - The full rules apply **only when the paths change**, checked **after** the org ownership check (S5).
  - Responses carry `path_warnings`.
- `ErrorResponse.Field`; the `config_error` allowlist gains `path_root`, `path_not_normalized` and `path_profile_parent`.
- **Ingest:** the agent's `lm_studio` is stored as `lmstudio`, and `gpt4all` and `scan_path` are stored as themselves. **Before this, both `lm_studio` and `gpt4all` were stored as `unknown`.**
- **`AgentConfigDefaults.ModelScanPaths` is empty** (S4).

**Schema (migration 000028, with a down migration):**
- the `agent_configs.model_scan_paths` default becomes `'{}'`;
- the `endpoint_model_files.source` CHECK adds `gpt4all` and `scan_path`, added `NOT VALID` (the list only widens, so there's no table-scan lock).
- **Down:** relabels the two new values `unknown`, permanently, and restores the old default. Roll back the API binary first.
- **Dev note:** the dev DB was migrated before the `NOT VALID` change, so its constraint is validated. That's equivalent.

**Shared fixture:** `testdata/agent_config_vectors.json`. It holds:
- the limits;
- the `c1` version vectors;
- **42 path cases**, each with the agent code, the server's always-on code and the server's full code.

Both Go modules load it, and each module's constants must equal the fixture's limits.

**UI:**
- **Endpoint Detail, Local Models:** source labels (`formatModelSource`: "Configured path", "LM Studio", …), plus notes for `depth_limited`, `path_root` and `path_network`.
- **Configure panel:**
  - paths are optional (needed with S4);
  - help text (S1: "`.bin`, `.pb` and `.h5` can also match non-model files");
  - the `path_warnings` banners. The root banner says plainly that updated agents refuse the whole config.

**Docs:** `API_CONTRACT_DRIFT.md` rows C21–C23 cover the error body and codes, `path_warnings`, the empty default, `scanner_notes`, `scan_path` and the stored sources.

## 2. Every consumer of `source` (S2)

| Consumer | Handles `scan_path`? |
|---|---|
| Agent report (`local_models[].source`) | Emits it (1.3.2) |
| Collector | Forwards the raw body; nothing to change |
| API ingest → `endpoint_model_files.source` | **Yes:** CHECK + allowlist (000028); `lm_studio` → `lmstudio` alias |
| Endpoint Detail (`EndpointDetections.tsx`) | **Yes:** "Configured path" (`formatModelSource`) |
| CMDB / Assets (`local_model_count`) | Counts rows only; unaffected |
| Agent Lineage | Doesn't read model sources |
| Dashboard | Doesn't read model sources |

Orphaned-link check: nothing removed. The UI only relaxed the "at least one path" rule and added text.

## 3. Tests

**Agent:** the full suite passes on Windows. The Linux test binaries pass in WSL **as root**, run from their package directories (needed for the fixture path). New tests:
- **`models`:** only model file types count, labelled `scan_path`; the depth boundary (a model exactly 8 levels down is found, 9 isn't, and `depth_limited` is noted); stop at the deadline; root skipped and noted; a symlinked directory not followed; **a link to the root refused, with or without a trailing separator, including `/proc/self/root/` and `/proc/1/root/` on Linux**; non-normal forms refused.
- **`scanpath`:** a shape table.
- **`remoteconfig`:** root and non-normal codes, and the shared-fixture tests (limits, versions, 42 path cases).
- **`payload`:** a deadline error is labelled `timeout`.

**API:** the full suite passes against real Postgres. New tests:
- **Store:** shared-fixture limits, versions and path cases; `PathWarnings`; the empty default.
- **`TestAgentConfig_Slice0_CodesNeverEchoValues_RealDB`:** every code and field, a secret marker never echoed, the bypass forms (`C:\Users.`, `::$INDEX_ALLOCATION`, `C:\ProgramData\..\Users`, `C:\Documents and Settings`, `/System/Volumes/Data/Users`), and specific subfolders allowed.
- **`…LegacyPathsAcceptedAndFlagged_RealDB`:** a legacy row saves other fields; the same set re-sent, including the stored doubled-backslash `C:\\Users`, isn't a change; a changed list is checked in full; clean paths unflag.
- **`…CrossOrg_RealDB`:** another org gets 404 for every body, whether the paths are the same, changed or valid. The victim row is unchanged, and GET returns 404.
- **`TestIngest_Slice0_ModelSourcesStored`:** `lm_studio`→`lmstudio`, `gpt4all`, `scan_path`, `ollama`, `huggingface`, and an unknown value → `unknown`.

**Migrations:** `schema/migrationtest` passes, including the new `TestMigrate_B269_Slice0_DefaultsAndModelSources` (the default, the CHECK, rollback relabelling, existing rows untouched) and fresh-versus-incremental parity.

**UI:** `tsc --noEmit` and `vite build` pass.

## 4. Live verification: the real packaged agent in WSL, real stack

**Setup:**
- WSL endpoint `Bhargavtej`, linked to a fixture governed agent; collector key; fixture admin; config changed through the real admin API.
- The fixture agent's **default model paths were empty**, which is S4 confirmed live through migration 000028.
- `/srv/s0` holds sparse 150 MB files: `weights.gguf`, plus decoys `backup.iso`, `movie.mp4`, `disk.vmdk`, plus `l1/…/l10/deep.gguf` at **depth 10**.
- A slow tree for the deadline test: **30,000 directories on `/mnt/c`**. Over WSL's 9p, `find` took **151 s**, far past the 30 s scan deadline.

| Test | **Before**: agent 1.3.1 | **After**: agent 1.3.2 |
|---|---|---|
| **A large non-model file under a configured path is not reported** | `backup.iso`, `movie.mp4` and `disk.vmdk` were **reported as models**, labelled `lm_studio` | **Not reported** |
| **A `.gguf` is reported** | `weights.gguf[lm_studio]` | **`weights.gguf[scan_path]`**, stored as `scan_path`; Endpoint Detail shows **"Configured path"** |
| **A tree deeper than 8 is cut off, with `depth_limited`** | `deep.gguf` (depth 10) **reported**, no note | `deep.gguf` **not reported**; `scanner_notes.models = ["depth_limited"]`; Endpoint Detail shows the note |
| **A walk past the scan deadline stops** | 13:48:42 `timeout`, then 13:49:42 and 13:50:42 **`still_running`**: the walk kept running, so later cycles couldn't scan models at all | 14:04:33, 14:07:03 and 14:08:33: **`timeout` every time, never `still_running`**. The walk stops at the deadline. |
| **A root path is refused with `path_root`, and the saved config is kept** (fake collector serving a hash-valid config with only `/`, minimum model size at the maximum so nothing would be collected) | 1.3.1 **accepted** it (version `c1:058a…` applied) and **persisted `['/']`** to its state file | **On upgrade:** the persisted `['/']` was ignored (`state_invalid`), the served `/` was refused (`path_root`), and the agent ran on local config. **With a good config saved:** `/` was refused (`path_root`, reported in `config_error`), and **the saved config was kept** (state file unchanged since 14:02:38, version `c1:da8d…`) |

**Stored sources:**
- The 1.3.1 report's five rows were stored as `lmstudio`: the new ingest alias, where the old code stored `unknown`.
- The 1.3.2 report's row was stored as `scan_path`.

**Browser** (Playwright, fixture admin): all PASS.
- Local Models shows `weights.gguf` labelled "Configured path".
- No raw `lm_studio` label.
- The `depth_limited` note is shown.
- The decoys are absent.
- The Configure panel shows "`.bin`, `.pb` and `.h5` can also match non-model files".

Screenshots: `ui_s0_endpoint.png`, `ui_s0_configure.png` (scratchpad).

**Windows:** the installed MSI was not touched. The Windows walk and path rules are covered by native unit tests (`scanpath`, `remoteconfig`, `models` and the shared fixture, on Windows). Live Windows verification stays gated, per 8a.

## 5. Reviews (both mandatory; security required)

**Security review.** No Critical or High. **Two Mediums, both fixed:**

| # | Finding | Disposition |
|---|---|---|
| **M-1** | The profile-parent rule compared text only. `C:\Users.`, `C:\Users::$INDEX_ALLOCATION`, `C:\ProgramData\..\Users` and `C:\Documents and Settings\` each walked every profile (the reviewer's Windows probe). | **Fixed on both sides:** paths must be in normal form (`path_not_normalized`), a stray colon is refused (`path_invalid_chars`), and aliases were added. All the forms are in the shared fixture and the PUT test. |
| **M-2** | A link plus a trailing separator defeated the root and UNC rules: `/proc/1/root/` and `/proc/self/cwd/` give a whole-disk walk, and a symlink to `\\host\share` gives machine-account authentication. | **Fixed:** configured paths are resolved through links and the target is checked (root, same file as root, network); only the resolved path is walked. Tested on real Linux as root with `/proc/self/root/`, `/proc/1/root/` and a link to `/`. |
| L-1 | Windows root forms with trailing dots or spaces, or a stream suffix | Fixed by normal form |
| L-2 | ctx can't interrupt a blocked syscall (a hung NFS/CIFS/FUSE mount); mapped or mounted network filesystems pass `IsNetwork` | **Documented, not fixed.** B-281's `still_running` guard bounds the pile-up. Logged on B-277 as the remaining gap (skip remote filesystem types). |
| L-3 | Migration 000028 locks the table for a full re-check; the down relabels permanently; rollback order | **Fixed:** `NOT VALID`; the down migration documents the relabelling and "roll back the API binary first" |
| L-4 | `scanner_notes` is stored without filtering | Accepted: the report JSONB is stored raw as for every field, and the UI reacts only to the three known codes. Noted. |

**Code review.** No Critical or High.

| # | Finding | Disposition |
|---|---|---|
| **M1** | A deadline-stopped models scan was labelled `error`, depending on a race | **Fixed** in `collect`; test `TestCollect_DeadlineErrorIsTimeout`. Live: `timeout` on every slow cycle. |
| **M2** | A stored root path blocks the whole config, but the banner said "still applies" | **Banner corrected** (a separate red message for `path_root`). Roots stay always-rejected, since the agent refuses them. **There are 0 stored rows** with a root or non-normal path (checked on the dev DB). |
| L1 | Profile-parent bypass via dot segments | Fixed (normal form) |
| L2 | `depth_limited` can come from default locations | UI text reworded to "Some model folders…" |
| L3 | Walk tests depended on the host's default model folders | **Fixed:** the default locations are package vars, pointed at empty dirs in tests |
| L4 | `unknown_scanner` message hard-coded the list | **Fixed:** built from `store.AllScanners` |
| L5 | Allowlist missing `path_profile_parent` | **Fixed** |
| Info | The reviewer couldn't run real-DB tests in their shell | Run here: all pass (§3) |
| Info | `.pb`/`.h5` also match non-model files | Help text extended |
| Info | `agent_version` is blank (B-282) | Untouched by this slice, as directed |

**Standing checks:** no raw error text crosses a trust boundary (codes only, fixed messages); no orphaned actions or links; drift rows C21–C23 are in the same commit.

## 6. Rollout reality

- **This is an agent change, 1.3.2.** Endpoints get the type filter, depth limit, deadline stop and root and link refusal **only once they update.**
- **Agents older than 1.3.0 can't have paths removed remotely** (they merge, B-293). **Endpoint Detail keeps listing any large file under their configured paths, labelled "LM Studio", until they update.**
- **Agents on 1.3.0–1.3.1** apply path changes but have no type filter or depth limit. They still accept `/` if a non-server source serves it; the real server can no longer serve `/`.
- **The server-side rules take effect immediately** for new or changed paths. Stored rows keep their paths, flagged; cleaning them is **B-296**.
- **At upgrade, an endpoint's model count can drop visibly.** Non-model files under configured paths stop being reported. That's the fix, not a regression.
- **Untouched by this slice:** **B-282** (blank agent version) and **B-294** (the MSI state folder). **S7** (the HKLM key readable by local users) still needs a disposable VM. Its consequence is recorded on B-293.

## 7. Cleanup

- **WSL:** the package is purged (and `/var/lib/eami-agent` with it); `/srv/s0` and the fake collector are removed.
- **Windows scratch:** the 30k-directory slow tree is deleted.
- **Collector:** the key for `Bhargavtej` is revoked.
- **Dev DB, in one transaction:** the endpoint is unlinked, the fixture agent deleted (its config cascades), the fixture admin soft-deleted, and 3 refresh tokens removed.
- **Audit log untouched:** 1706 rows before and after.
- Scratch password, key and token files are deleted.
- Migrations 000027 and 000028 stay applied to the dev DB.
