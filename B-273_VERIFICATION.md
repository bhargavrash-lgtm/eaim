# B-273 — Verification record

Packaged Linux/macOS agents never read their installed config. Fixed and live-verified on 2026-09-30 by Claude Code.

## The defect

- `eami-agent/cmd/agent/main.go` defaults `--config` to the **relative** path `eami-agent.yaml`, and has done since the initial commit (`7d7709c`).
- The systemd unit and launchd plist passed no arguments, so under systemd or launchd the working directory is `/` and the file was never found.
- `config.Load` accepts a missing file, so the agent ran with an empty collector URL (stdout-only mode). Every packaged Linux install has never reported, and the same applies to macOS by the same mechanism.
- Windows is unaffected for connectivity: it takes the collector values from the registry fallback (ADR-014).

## The fix

| File | Change |
|---|---|
| `eami-agent/installer/linux/eami-agent.service` | `ExecStart=/usr/bin/eami-agent --config /etc/eami/agent.yaml` |
| `eami-agent/installer/macos/io.eami.agent.plist` | `ProgramArguments` gains `--config`, `/etc/eami/agent.yaml` (validated with `plistlib`) |
| `eami-agent/installer/linux/postinstall.sh`, `eami-agent/installer/macos/postinstall` | **No `http://localhost:8888` / `REPLACE_WITH_YOUR_API_KEY` fallback.** `agent.yaml` is rewritten only when **both** URL and key are supplied. With no config yet, it is written with **both** empty (a URL-only value would post keyless every cycle): no-send mode, with a stderr warning. An existing config is otherwise kept. The write is atomic: a `mktemp` in `/etc/eami` under umask 077, `chmod 600`, then `mv`, so an existing file's looser mode is never inherited and a failed write never truncates it. A rewrite without a CA keeps an already-installed `/etc/eami/collector-ca.pem`. On macOS, `/var/log/eami-agent.log` is pre-created as 0600, because a no-send agent prints its report to stdout and launchd would otherwise create that file world-readable. |
| `eami-agent/installer/README.md` | The Linux "Writes config" row and the macOS "basic install" note match the new behaviour. |
| `.gitattributes` | `eami-agent/installer/linux/* text eol=lf`, `eami-agent/installer/macos/* text eol=lf` |

**Why the postinstall change is in B-273's scope:**
- Both the reviewer and the security reviewer rated it HIGH, and it is caused by this fix.
- Before the fix, nobody read `agent.yaml`, so its placeholder fallback was harmless.
- After the fix, the root agent would POST reports to, and take remote scan config from, `localhost:8888`. That is an unprivileged port any local user can bind.
- Every upgrade or reinstall run without the values would also clobber a working config.

## Live evidence

**Environment:**
- WSL Ubuntu with systemd as PID 1, running the real `eami-agent_*.deb` built from the repo's own `installer/linux/nfpm.yaml` (nfpm v2.41.1, linux/amd64 static build).
- The real dev stack: `eami-collector` on `:8888`, forwarding to `eami-api`, backed by Postgres, reached from WSL at the Windows host IP `172.31.160.1`.
- A dedicated collector key was minted for the test identity `Bhargavtej` (the WSL hostname that `postinstall` bakes in). It was revoked after each run; `list-keys` shows both keys revoked.

**Discovery (pre-fix, stub collector):**
- The installed `/etc/eami/agent.yaml` held the URL, but the journal showed `collector_url=""`, the report was dumped to stdout, and the stub received 0 requests.
- Control: the same binary run with an explicit `--config` sent `POST /v1/ingest`.

**Adversarial run 1 (real collector, the same install path that found the bug):**

| Step | Package | Result |
|---|---|---|
| 1 | Pre-fix `.deb` (unit from `HEAD`) | `argv[]=/usr/bin/eami-agent`, `collector_url=""`, 1 JSON dump to the journal, 0 collector log lines, **0 endpoint rows / 0 reports** in Postgres |
| 2 | **Upgrade in place** to the fixed `.deb` | `argv[]=/usr/bin/eami-agent --config /etc/eami/agent.yaml`, `collector_url=http://172.31.160.1:8888`, 0 stdout dumps. The collector logged `report buffered agent=Bhargavtej`, and Postgres gained a `Bhargavtej` endpoint with **1 report, `os=linux`**. The config poll got the expected 404, since the endpoint is unlinked. |
| 3 | Purge | Succeeded through the now-LF `prerm`. Before the `.gitattributes` fix, a Windows-built `.deb`'s `prerm` failed with "No such file or directory". |

**Adversarial run 2 (after the postinstall fix, real collector):**

| Scenario | Result |
|---|---|
| A: fresh install, **no values** | Warning printed; `agent.yaml` is `600 root` with `url: ""`, `api_key: ""`; `collector_url=""`; **0 collector log lines**. The extra check of binding a listener on WSL `127.0.0.1:8888` couldn't run, because WSL's localhost relay already holds that port. The decisive evidence is `collector_url=""`: `runLoop` only calls `Send` when the URL is non-empty. |
| B: reinstall **with values** | Config written (`600 root`); the collector logged `report buffered`; the Postgres report count went up to 2 |
| C: reinstall, **no values** | "keeping the existing /etc/eami/agent.yaml unchanged"; the URL and key were preserved; the collector logged `report buffered`; the report count went up to 3 |
| Purge | Clean (binary, launcher, unit and `/etc/eami` removed) |

**Final run 3 (after the re-review fixes; `1.0.3~b273fix3` `.deb`, real collector):**

| # | Scenario | Result |
|---|---|---|
| 1 | Fresh, no values | Warning; both empty; `600 root`; 0 temp files left; `collector_url=""` |
| 2 | Existing, URL only | "keeping the existing ... unchanged"; still empty; no-send |
| 3 | Existing, both | Written; `collector_url=http://172.31.160.1:8888`; 0 stdout dumps |
| 4 | Existing, none | Kept; still reporting |
| 5 | Fresh, URL only (after purge) | **Both** written empty (not URL-only); `collector_url=""` |

The real collector logged exactly **2** `report buffered agent=Bhargavtej` lines during scenarios 1–4 (from #3 and #4), and the Postgres report count went from 3 to 5. Purge was clean each time. Three test keys were minted over the session, all revoked, and the scratch key file was deleted.

**Left in the dev DB:**
- One `Bhargavtej` endpoint (3 reports, unlinked, `os=linux`) remains, in the same way as the earlier `live-verify-agent-*` fixtures.
- It was not deleted; deleting it is a founder call.

**Not live-verified:**
- macOS: no hardware. The plist change is validated structurally; the upgrade path is `postinstall`'s unload then load.
- `.rpm`: the reviewer found a pre-existing rpm scriptlet-order bug that leaves the service stopped on upgrade; see the follow-ups.

**Tests:**
- No Go code changed.
- `go test ./internal/config/... ./internal/collector/...` pass.
- `bash -n` passes on both postinstall scripts.

## B-271 live confirmation (same session, real dev DB, read-only)

- The real linked endpoint `Bhargav_tej` (this Windows machine) has `agent_configs.enabled_scanners = {ai_apps,mcp_servers,cloud_clients,network_activity,browser}`. That is the 6 defaults minus `models`, per B-194's mitigation.
- Across its **3,486 reports, `gpus` is present in only 10**, and each of those is an isolated report followed by `null`.
- Example, 2026-09-19 01:01:58: one report has `gpus` and `local_models` null; the next has `gpus` null and `local_models` populated.
- That is the first scan after a restart running on built-in defaults (all scanners) before the remote config is applied. The scanner works, and the config suppresses it.
- `ai_processes`, `python_envs` and `node_projects` are never present. For those, `null` is ambiguous, because the agent also marshals an empty result as `null`, and B-193 blinds `python_envs`/`nodejs_ai` under LocalSystem anyway.

## Reviews

- **Code review:** the fix is correct. Flag syntax, plist validity, the deb and macOS upgrade paths and the `.gitattributes` globs were all confirmed. No other service launcher lacks `--config`.
- **Security review:** the diff is safe, and the `/etc/eami` trust chain is sound (root 0755, CA source ownership checked). One HIGH finding (the localhost fallback plus clobbering on upgrade) was fixed here. The rest are pre-existing issues that this fix makes reachable; see the follow-ups.
- **Focused re-review** of the postinstall delta: no path still writes a localhost or placeholder value; `set -e` and bash 3.2 are fine. Its introduced findings were fixed and re-verified in run 3:
  - a partial fresh install wrote the URL alone;
  - the umask didn't apply to an existing file, so the write is now temp-file then `mv`;
  - the macOS log file would be created world-readable.
  The re-review also found a pre-existing CA-path drop on rewrite, fixed here as well (the same block). Its remaining pre-existing findings are the follow-ups below.

## Follow-ups (proposed, not minted: B-IDs need founder confirmation)

1. **HIGH: rpm upgrade leaves the service stopped and disabled.**
   - rpm runs the old `%preun` after the new `%post`, and `preremove.sh` doesn't check `$1`. It also deletes the nmhost launcher and manifests.
   - Needs an rpm `posttrans` in `nfpm.yaml` that re-enables the service, plus a `$1`-aware `preremove.sh`.
   - Existing RPM installs won't get the B-273 fix running on upgrade until this is fixed.
2. **MEDIUM: the native-messaging host on Linux/macOS never finds a config.**
   - It is browser-spawned as the user with no args, and `/etc/eami/agent.yaml` is root 0600, so paste events are silently dropped.
   - Needs a design that doesn't make the key world-readable.
   - `eami-browser-extension/MANUAL_TESTING.md:109-113` is wrong about it.
3. **MEDIUM: `docs/quickstart.md` is wrong.**
   - macOS: wrong path (`/etc/eami-agent.yaml`) and wrong label (`com.eami.agent`).
   - Linux: the wrong env var (`EAMI_API_KEY` instead of `EAMI_COLLECTOR_API_KEY`).
4. **MEDIUM: remote config is unbounded.** `model_scan_paths` can steer a root filesystem walk (e.g. `/`), `scan_interval_seconds` has no minimum, and the response has no size cap. This is newly reachable on Linux/macOS; relevant to B-269 and B-194.
5. **MEDIUM: data minimisation.** `ai_processes` sends every user's full command lines on Linux/macOS (secrets passed as `--token`/`--api-key` arguments), and `mcp_servers` sends args verbatim. Same class as B-194.
6. **LOW:** `X-API-Key` is forwarded on redirects (no `CheckRedirect`).
7. **LOW:** a missing config file is still silent when running as a service. This is B-273's own open question: should it be loud?
8. **MEDIUM, unverified: the macOS value sources probably don't reach the pkg `postinstall`.**
   - Jamf `$4`–`$6` go to Jamf *Scripts*, not to a package's embedded postinstall.
   - `sudo VAR=x installer -pkg` likely doesn't pass the environment through `installd`.
   - If so, every macOS install takes the no-values branch (now safe), and the README's Jamf and env-var flows don't work as documented. This needs real macOS hardware.
9. **LOW:** values go unescaped into double-quoted YAML. A `"` or `\` in a URL or key breaks the file, and the agent then restart-loops. Not injectable: expansion is single-pass and the source is a root admin.
10. **LOW:** supplying both values regenerates the whole file, so hand edits (`enabled_scanners`, `interval_secs`, ...) are lost. Documented in the README; not changed.
