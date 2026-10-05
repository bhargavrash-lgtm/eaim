# B-269 Slice 0 plan: B-277 path rules and depth limit, plus the B-194 file-type filter

**Date:** 2026-10-05 · **By:** Claude Code · **Plan only, no code.** Build after founder approval.
**Decisions in force:** D9 (server path rules; agent depth limit and root rejection) and D10 (stable codes, shared fixtures). See `DISCOVERY_PRESETS_DESIGN.md` §12.

## 1. What the `models` scanner does today (traced: `eami-agent/internal/detection/models/scanner.go`)

| Source (`source` value reported) | Where it looks | Which files count | What it reports per model |
|---|---|---|---|
| `ollama` | HTTP `GET localhost:11434/api/tags` (1 MiB cap) | n/a (API) | `name`, `size_bytes`, `modified_at`. **No path.** |
| `lm_studio` (default location) | Linux `~/lm-studio/models`; macOS `~/Documents/LM Studio/models`; Windows `%LOCALAPPDATA%\LM-Studio\models` | **`.gguf` only** (`scanDir(…, ".gguf", …)`) | `name` (file name), `file_path`, `size_bytes`, `modified_at` |
| `huggingface` | `$HF_HOME/hub` or `~/.cache/huggingface/hub` | Every `config.json` in a snapshot. The model's size is the **sum of all files** in that snapshot directory (no type filter). | `name` (from `models--org--repo`), `file_path` (the snapshot **directory**), the summed `size_bytes`, `modified_at` (of `config.json`), `model_type` and `architecture` (parsed from `config.json`, 64 KiB cap) |
| `gpt4all` | Linux `~/.local/share/nomic.ai/GPT4All`; macOS `~/Library/Application Support/nomic.ai/GPT4All`; Windows `%LOCALAPPDATA%\nomic.ai\GPT4All` | **`.gguf` or `.bin`** | As `lm_studio` |
| **Extra paths** (`model_scan_paths`, remote or local) | Each configured path | **Every file, of any type**, at or over `model_file_size_mb` (`scanDir(p, "", SourceLMStudio, …)`) | As `lm_studio`, **labelled `lm_studio`** even though it isn't |

**The B-194 root cause, confirmed:**
- Extra paths have **no type filter**, so a 100 MB+ ISO, video or VM disk under `/home` is reported as an "LM Studio model", with its full path and name.
- The old default row's paths `/home`, `/Users`, `C:\Users` made this happen on every linked endpoint.

**`scanDir` today:**
- `filepath.WalkDir`: it doesn't follow symlinked directories, which is good. It has **no depth limit**.
- It **ignores ctx**, so it can run past the 30 s scan deadline; B-281's `still_running` contains that.
- Errors are swallowed per entry.
- `scanHuggingFace` is a separate walk.

**Data minimisation is unchanged by Slice 0:** only file name, path, size and mtime leave the machine. The one exception is HF's `config.json` `model_type` and `architecture`.

## 2. Which file types count as model files (the B-194 filter for extra paths)

**Evidence in the code** is limited to `.gguf` (LM Studio) and `.gguf`/`.bin` (GPT4All). The HF cache sums whatever is in a snapshot (typically `.safetensors`, `.bin`, `.gguf` and tokenizer files).

**Proposed allowlist for extra paths** (case-insensitive extension):
- **Weights:**

  | Extension | Format |
  |---|---|
  | `.gguf`, `.ggml` | llama.cpp |
  | `.safetensors` | HF |
  | `.bin` | GPT4All / old PyTorch |
  | `.pt`, `.pth` | PyTorch |
  | `.ckpt` | |
  | `.onnx` | |
  | `.tflite` | |
  | `.h5`, `.keras` | Keras |
  | `.pb` | TensorFlow frozen |
  | `.mlmodel` | Core ML |
  | `.llamafile` | |

- **Directory-shaped formats** (`.mlpackage`, TF SavedModel directories) are **out of scope** for Slice 0, since the walk reports files. They're noted as a known gap.
- **`.bin` is ambiguous** (any binary blob). It stays in the list, as GPT4All and older PyTorch use it, and still has to clear `model_file_size_mb`.
  - **Decision S1:** keep `.bin` (more recall, some noise) or drop it for extra paths only (less noise; misses GPT4All files dropped into a custom path).
  - **Recommendation: keep it**, and say so in the editor's note.
- **Source label (decision S2):** extra-path hits are labelled `lm_studio` today, which is wrong.
  - **Recommendation:** a new value, `scan_path`.
  - That's a report value change: a drift row; Endpoint Detail's Local Models section labels it "Configured path"; older reports keep `lm_studio`.

**How B-277 and the B-194 filter share `scanDir`:** one walk with three new parameters, used by every filesystem source:

1. **An extension allowlist** (a set). LM Studio keeps `{.gguf}`, GPT4All keeps `{.gguf,.bin}`, and extra paths get the list above.
2. **A depth limit** (D9, agent-side, a fixed constant): **8** directory levels below the walk root.
   - LM Studio's own layout is `models/<publisher>/<repo>/<file>` (depth 3), so 8 leaves room without allowing a full-disk crawl.
   - `fs.SkipDir` stops descending past the limit.
3. **Context cancellation:** the walk returns `ctx.Err()` once the scan deadline passes, so it stops instead of running on behind `still_running`. This is small, and it makes the depth limit's purpose (bounded work) real.

- **The HF walk** gets the same depth limit and ctx check, but no type filter. HF sizes are directory sums by design.
- **True-root rejection** (D9, agent-side):
  - `filepath.Clean(path)` equal to `/`, or a volume root (`X:\`, `X:/`, a drive-relative `X:`), is rejected.
  - **Remote config:** `remoteconfig.Validate` returns a new code, **`path_root`**, so the whole config is rejected and last-known-good is kept (consistent with B-293).
  - **Local YAML paths** (`model_file_scan_paths`) aren't validated by remoteconfig, so the scanner itself **skips** a root and logs it locally. That's defence in depth for a hand-edited YAML.
- **Whole-profile parents** (`/home`, `/Users`, `C:\Users`) are **server-side only** (D9). The agent doesn't reject them, so migrated presets (D8, "keep today's behaviour") keep working. With the type filter and depth limit, the over-collection they used to cause shrinks to genuine model files.

## 3. Server side (D9, D10)

- **`store.ValidateModelScanPaths`** gains relative (exists), UNC (exists, B-293), **filesystem roots**, and **whole-profile parents**.
  - The parents are an exact match after normalising separators and case on the Windows form: `/home`, `/Users`, `C:\Users`, and `C:\\Users` (the stored doubled form).
  - Children such as `/home/alice/models` stay allowed.
- **D10 codes:** PUT errors become `{"code": "<reason>", "field": "<field>"}`.
  - The codes are the agent's own, plus `path_root` and `path_profile_parent`.
  - `field` comes from a fixed list (`model_scan_paths`, `enabled_scanners`, `scan_interval_seconds`, `model_file_size_mb`, `max_report_size_bytes`) and **never echoes a value**.
  - It replaces the free-text messages, and also fixes B-271's `unknown scanner %q` echo (NOTES).
  - Drift rows go in the same commit.
- **Shared fixtures:** `testdata/agent_config_vectors.json` at the repo root, loaded by both `eami-api/internal/store` and `eami-agent/internal/remoteconfig` tests. It holds:
  - the existing `c1` golden vectors;
  - one case per bound and per path rule, each with the expected code and with whether it is agent-enforced, server-only (whole-profile) or both.

## 4. Rollout tail and what old agents show (question 2)

**Yes: this is an agent change with a rollout tail** (agent 1.3.2). The server-side half (D9 and D10) takes effect immediately for **new** configs; the agent half only once each endpoint updates.

**What Endpoint Detail shows for an old agent until it updates:**
- **Agents older than 1.3.0** (today's Windows MSI, for example):
  - Local Models keeps listing **any** 100 MB+ file under configured paths, labelled "LM Studio", with no depth limit. `config_version` and `config_source` are null ("not known").
  - Old merge semantics also mean a preset that **removes** `/home` can't clear it on these agents, so they keep walking it until they update.
- **Agents on 1.3.0–1.3.1:**
  - Configs whose paths break the new server rules are no longer served for new publishes. But a **migrated** preset (D8) still serves `/home` etc., and these agents walk it **without** the type filter or depth limit, so they still over-collect until they update.
  - No root walk is possible: the server never serves `/` (new rule), and these agents accept `/` only if served.
- **After the update:**
  - Local Models drops the non-model files. The **count can go down visibly** at upgrade. That's expected, not a regression, and the verification and release notes should say so.
  - Extra-path hits show "Configured path".

**Fleet gap in plain terms:** until an endpoint runs 1.3.2, its exposure is whatever its paths were plus no type filter. The server can't fix that remotely for agents older than 1.3.0 (merge). That's why D8's "needs review" flag and the B-194 warning matter for migrated presets.

## 5. Conflicts with decisions (question 3)

**Do any of the 12 Part A conflicts, or anything found here, change a decision above?** Two do:

1. **D4's credential location conflicts with an existing rule (C11 and B-278).**
   - "Stored where the existing key is stored" means `/etc/eami/agent.yaml` on Linux and macOS: root 0600, written by `postinstall`.
   - The agent would have to **rewrite the admin's YAML**, which B-293 and B-278 #4 rule out (hand edits lost).
   - On Windows the existing key is in **`HKLM\SOFTWARE\EAMI\Agent`**. The default `HKLM\SOFTWARE` ACL grants **Users read**, so **any local user can read the agent key today** (pre-existing; not verified live on this machine) and could impersonate that endpoint's reports. "Same permissions" would inherit that.
   - **Suggest:** store the minted credential in the **B-293 state directory** (root 0700/0600; protected DACL for SYSTEM and Administrators only, chain-checked). That keeps the YAML read-only to the agent and doesn't copy the registry's read exposure.
   - It is gated by the Windows live-service check (C11) either way.
   - **Founder to confirm whether D4 meant "same protection level" or "same place".** Also: log the registry-readable key as its own item?
2. **D9 versus the legacy config path.**
   - The `agent_configs` column **default is still `/home`, `/Users`, `C:\Users`** (baseline schema), and `AgentConfigPanel` always sends paths on save.
   - If Slice 0's server rules apply to the legacy `PUT /v1/gateway/agents/{id}/config`, then **saving any field** on a default-row agent returns `path_profile_parent` until the paths are edited. And every newly created governed agent seeds a row the server itself would reject.
   - **Suggest, in Slice 0:**
     - change the column default to **empty** (a migration). Only **newly** created governed agents are affected: their linked endpoints get no extra paths instead of walking every profile. This is the B-194 fix, but a behaviour change, so it's named.
     - apply the new rules to the legacy PUT **only when `model_scan_paths` itself changes**.
   - Configure is disabled in Slice 1 anyway (D6).

No other Part A conflict changes D1–D10: C1 → D1, C2 → D2, C3 → D3, C5 → D6, C6/C7 → D7, C8 → D8, C9 → D9, C10 → D10, C12 needs nothing. C4/E1 → D4 and C11 are the credential-location point above.

## 6. Slice 0 build outline (for approval; no code yet)

- **Agent (1.3.2):**
  - `scanDir`: extension set, depth limit 8, ctx cancellation; extra paths use the allowlist and `source=scan_path`.
  - Root skip in the scanner, and `path_root` in `remoteconfig.Validate`.
  - Unit tests: each type in and out, depth boundary, ctx stop, symlinked directories not followed, root rejection on POSIX and Windows forms.
- **API:**
  - `ValidateModelScanPaths` with roots and whole-profile parents.
  - JSON error `{code, field}` on the config PUT.
  - `path_root` and `path_profile_parent` in the endpoint `config_error` allowlist.
  - Column default migration (000028, with a down).
  - Legacy-PUT rule only on path change.
- **Shared fixtures** in `testdata/`, loaded by both Go modules.
- **Drift rows** for the codes, the `field` key and the `scan_path` source value, in the same commit.
- **Live, on the WSL packaged agent:**
  - a `/srv` tree with real model extensions plus decoys (`.iso`, `.mp4`, `.vmdk`, large `.txt`) and a deep tree past depth 8; before (1.3.1) and after (1.3.2);
  - a root path rejected end to end;
  - a migrated-style `/home` config still served and walked, with the filter applied.
- **Reviews:** both mandatory, security required. The installed Windows MSI is not touched.

## Decisions for the founder (Slice 0)

| # | Decision | Recommendation |
|---|---|---|
| S1 | Keep `.bin` in the extra-path allowlist | Keep (min-size gate; note it in the editor) |
| S2 | A new `source` value for extra-path hits | `scan_path` ("Configured path") |
| S3 | Depth limit | Fixed 8 |
| S4 | Legacy `agent_configs` column default | Empty, in Slice 0 (named behaviour change for newly created agents) |
| S5 | New path rules on the legacy PUT | Only when `model_scan_paths` changes |
| S6 | D4's credential location | The B-293 state directory (same or stronger protection, not the same place); confirm |
| S7 | `HKLM\SOFTWARE\EAMI\Agent` readable by local users | Verify on a disposable VM and mint an item if confirmed |
