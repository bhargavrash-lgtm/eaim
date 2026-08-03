# Native messaging host registration

`eami-agent` can run as a browser native-messaging host (`eami-agent
--native-messaging-host`) so a future browser extension (B1) can hand off
captured paste events in real time, without opening any local network
port — the browser launches the registered host process itself and talks
to it exclusively over stdin/stdout, using the standard length-prefixed
JSON native messaging protocol.

Registration differs per platform because the two things a native
messaging manifest needs — a JSON file describing the host, and a
browser-specific pointer to that file — work differently on each OS.

## The manifest points at a launcher, not the real binary

```json
{
  "name": "com.eami.agent",
  "description": "EAMI Agent native messaging host (real-time paste-event relay)",
  "path": "<absolute path to a hard-linked 'eami-agent-nmhost' copy of the binary>",
  "type": "stdio",
  "allowed_origins": [
    "chrome-extension://ngmdfnecljeoleiancdedbmhjdihaoaa/"
  ]
}
```

`ngmdfnecljeoleiancdedbmhjdihaoaa` is B1's real, stable extension ID (no
longer a placeholder as of B1) — derived deterministically from the RSA
public key embedded in `eami-browser-extension/manifest.json`'s `key`
field. See `eami-browser-extension/README.md` for the full derivation and
how to regenerate it with a different key if ever needed.

**Why not point `path` straight at `eami-agent`/`eami-agent.exe`:**
Chrome/Edge's native-messaging manifest schema has no `args`/`arguments`
field — the browser execs whatever `path` names with only its own fixed
positional arguments (the calling extension's origin, and on Windows a
window handle), never anything the manifest itself supplies. A manifest
pointing directly at the real binary would make a real browser launch it
with **no** `--native-messaging-host` flag at all, so it would silently
fall through to the ordinary poll loop instead of acting as a host — this
was caught by code review before any real extension existed to catch it
in practice.

The fix: every platform creates a **hard link** (not a copy — same
underlying file, zero extra disk space, can't fall out of sync when the
binary is upgraded in place) named `eami-agent-nmhost` /
`eami-agent-nmhost.exe` next to the real binary, and the manifest's
`"path"` points at *that* name instead. `cmd/agent/main.go`'s
`invokedAsNativeMessagingLauncher()` detects invocation under that name
(checking both `os.Args[0]` and the resolved `os.Executable()` path) and
behaves exactly as if `--native-messaging-host` had been passed
explicitly.

## Defense-in-depth: parent-process verification

Native messaging's real enforcement point is the browser itself — it
checks `allowed_origins` before ever launching the host, and nothing in
the protocol gives the host process a way to verify who launched it
afterward (a universal property of the mechanism, not a gap specific to
this implementation). A mandatory security review flagged that without
any check at all, any local process could invoke
`eami-agent-nmhost.exe` directly and have it forward fabricated data
using the agent's own already-configured collector credentials.

`internal/nmlauncher.VerifyLaunchedByBrowser()` is a best-effort
mitigation: it checks whether the immediate parent process is a
recognized browser (`chrome`, `msedge`, etc. — Windows via a toolhelp
snapshot, Linux via `/proc/<ppid>/exe`, macOS via `ps`). If the parent
can't be determined at all, this **fails open** with a loud warning
(an inconclusive check must not break the feature entirely); if it *can*
be determined and isn't a recognized browser, this **fails closed** and
the process refuses to serve any message. Operators can set
`EAMI_NM_SKIP_PARENT_CHECK=1` to bypass this if it proves too strict in
some environment. This is **not bulletproof** — a capable local attacker
could rename their process or launch it as a child of a real browser
process — but it meaningfully raises the bar above no check at all.

## Windows

`eami-agent.exe --register-native-messaging install`
(`internal/nmregister/nmregister_windows.go`):
1. Hard-links `eami-agent-nmhost.exe` next to the real binary.
2. Writes the manifest (pointing at the launcher) next to both.
3. Points both browsers' registries at the manifest:

```
HKLM\Software\Google\Chrome\NativeMessagingHosts\com.eami.agent  (Default) = <path>\com.eami.agent-native-messaging.json
HKLM\Software\Microsoft\Edge\NativeMessagingHosts\com.eami.agent (Default) = <path>\com.eami.agent-native-messaging.json
```

`Product.wxs` runs this as a `CustomAction` immediately after the binary
is installed. `--register-native-messaging uninstall` removes the
registry keys, the manifest file, and the launcher hard link — none of
the three are declared as WiX `File`/`Component` entries (they're
generated dynamically), so nothing else would clean them up.

## Linux (Chrome/Chromium)

`postinstall.sh` runs `ln -f /usr/bin/eami-agent /usr/bin/eami-agent-nmhost`,
then writes the manifest (pointing at the launcher) system-wide, per
Chrome's own documented convention:

```
/etc/opt/chrome/native-messaging-hosts/com.eami.agent.json
/etc/chromium/native-messaging-hosts/com.eami.agent.json   (Chromium's separate directory)
```

Both directories are written since either browser might be the one
actually installed; an unused manifest for the absent one is harmless.
`preremove.sh` removes the launcher and both manifest files on uninstall
(neither is tracked in `nfpm.yaml`'s `contents:` list, so dpkg/rpm removal
wouldn't touch them otherwise).

## macOS (Chrome)

`postinstall` runs `ln -f /usr/local/bin/eami-agent /usr/local/bin/eami-agent-nmhost`,
then writes the manifest (pointing at the launcher) system-wide:

```
/Library/Google/Chrome/NativeMessagingHosts/com.eami.agent.json
```

`uninstall.sh` removes the launcher and manifest on uninstall (neither is
part of the `.pkg` payload, so `pkgutil`'s removal wouldn't touch them
otherwise).

## What's verified live vs. not, this session

- **Windows**: fully live-verified on this development machine — install
  creates the real hard link + manifest + both registry keys with correct
  content; uninstall removes all of them cleanly and idempotently; a real
  subprocess invoked under the `eami-agent-nmhost` name (no explicit flag)
  correctly entered host mode and correctly refused to proceed because its
  parent process wasn't a recognized browser (proving both the
  launcher-name detection and the parent-process check work together).
- **Linux/macOS**: the `postinstall`/`preremove`/`uninstall.sh` changes are
  new lines in already-established scripts, written to match their exact
  style, and the `/proc`-based (Linux) and `ps`-based (macOS) parent-process
  lookups are implemented — but neither was exercised on a live Linux or
  macOS host in this session (this development machine is Windows).
  Flagged here rather than claimed as verified.
