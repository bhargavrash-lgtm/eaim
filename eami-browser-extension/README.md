# EAMI Paste Detection (browser extension) — B1

Detects paste events into known AI-tool web UIs and relays them, via
native messaging, to `eami-agent` (B0) for enterprise AI-usage
visibility. Known-destination detection only — no content
classification, no DLP. Raw pasted text never leaves the page: only
destination domain, timestamp, content length, and a client-computed
SHA-256 hash are ever reported.

## Architecture

```
content-script.js  --(chrome.runtime.sendMessage)-->  background.js (service worker)
                                                              |
                                                     chrome.storage.local
                                                     (durable buffer, survives
                                                      service-worker restarts)
                                                              |
                                              chrome.alarms (periodic flush)
                                              or count threshold (10 events)
                                                              |
                                              chrome.runtime.connectNative
                                                     ("com.eami.agent")
                                                              |
                                                    B0's eami-agent-nmhost.exe
                                                    (native messaging host)
                                                              |
                                        collector.Sender.Send() -- immediate,
                                        out-of-band from eami-agent's poll cycle
                                                              |
                                          eami-collector (SQLite buffer)
                                                              |
                                             eami-api (POST /v1/ingest/batch)
                                                              |
                                      endpoint_reports (raw JSON blob today --
                                      completing the wiring into B-032's
                                      paste_events table is B-035, tracked
                                      separately, not done by this brief)
```

## Files

- `manifest.json` — Manifest V3. `key` field pins a stable, deterministic
  extension ID (see below) — not random per-load, not dependent on file
  path, and independent of eventual Chrome Web Store publication.
- `domains.js` — the allowlist, mirroring
  `eami-api/internal/api/paste_domains.go`'s `KnownPasteDestinations`
  exactly. Loaded by both the content script and the background service
  worker so there's exactly one copy, not two that could drift.
- `content-script.js` — injects only on allowlisted domains (enforced by
  Chrome itself via `manifest.json`'s `content_scripts.matches`), listens
  for `paste` events, computes length + SHA-256 hash client-side, sends
  only that to the background worker.
- `background.js` — buffers events in `chrome.storage.local`, flushes on
  a 1-minute alarm or once 10 events accumulate, via one native-messaging
  `Port` connection per flush (not one host-process spawn per event).
- `scripts/generate-key.sh` — regenerates the keypair/ID (not needed
  unless you deliberately want a *different* stable ID; the current one
  is already committed via `manifest.json`'s `key` field).

## Why a bundled domain list, not a fetched one

There is no backend endpoint that serves this list (`eami-api` is out of
scope for this brief, and no such endpoint exists regardless), and a
bundled, hardcoded list matches this codebase's established convention
for every other domain/extension allowlist
(`network_activity.KnownAIHosts`, `browser.knownExtensions`,
`paste_domains.go` itself) — updated by hand with each release, not
fetched at runtime. **If `paste_domains.go`'s `KnownPasteDestinations`
ever changes, `domains.js` and `manifest.json`'s `host_permissions`/
`content_scripts.matches` must be updated by hand in the same change** —
there is no automated sync.

## Extension ID: how it's pinned, and why

Chrome extension IDs are normally derived from the extension's install
path when loaded unpacked without a manifest `key` — meaning the ID
changes every time the folder moves, which is unworkable for a native
messaging host (B0's manifest must name a *fixed* allowed origin).

Instead, `manifest.json` embeds an RSA public key (DER-encoded, base64)
in its `key` field. Chrome derives the extension ID deterministically
from that key: SHA-256 the DER bytes, take the first 16 bytes, map each
hex nibble to a letter a–p. This is independent of file path, and
**stays valid whether the extension is ever published to the Chrome Web
Store or not** (Web Store publication assigns its own separate permanent
ID from the developer account and does not read `key` at all — if this
extension is ever submitted there, `key` should be removed from the
*uploaded* package, though keeping it in this dev-loaded copy is fine and
recommended so the ID here stays stable for local force-install testing
either way).

Current computed ID (verified two independent ways — a shell/`openssl`
script and an independent Go re-implementation of the same algorithm,
both agreeing): **`ngmdfnecljeoleiancdedbmhjdihaoaa`**

This exact ID is what's now in B0's `nmregister_windows.go`
`AllowedExtensionID` constant and the Linux/macOS postinstall scripts'
embedded manifests — see those files' diffs for this change.

To regenerate with a *different* key (not needed unless you want a
different ID on purpose): `sh scripts/generate-key.sh`, then update
`manifest.json`'s `key` field and re-run B0's manifest update.

## Loading the extension locally (dev / enterprise force-install testing)

1. Open `chrome://extensions` (or `edge://extensions`).
2. Enable "Developer mode" (top-right toggle).
3. "Load unpacked" → select this `eami-browser-extension/` directory.
4. Confirm the extension's ID shown matches `ngmdfnecljeoleiancdedbmhjdihaoaa`
   exactly (proves the `key` field is doing its job).
5. Make sure B0 (`eami-agent`) is installed and registered
   (`eami-agent.exe --register-native-messaging install`) with this same
   ID in its `allowed_origins` — otherwise Chrome will refuse to connect
   to the native messaging host at all (by design; this is the
   browser-side half of the enforcement B0's `nmlauncher` package
   complements from the host side).

## Permissions this extension requests, and why

- `nativeMessaging` — required to talk to B0 at all. Chrome's own
  permission warning: "Communicate with cooperating native applications."
- `storage` — the durable event buffer (`chrome.storage.local`).
- `alarms` — the periodic flush timer (`chrome.alarms`, not `setInterval`,
  which does not survive Manifest V3 service-worker suspension).
- `host_permissions` scoped to exactly the 6 allowlisted domains (each as
  `https://*.<domain>/*`, matching `paste_domains.go`'s exact-or-subdomain
  semantics) — **not** `<all_urls>`. Chrome's warning here names only
  those specific sites, not "all your data on all websites."

No other permissions are requested. If Chrome/Edge ever shows a warning
beyond "communicate with native applications" + "read and change your
data on" the 6 listed sites, something has regressed — that would be a
real finding, not expected behavior.

## Firefox / Safari (not built — noted per this brief's scope)

- **Firefox**: native messaging manifests use `allowed_extensions` (an
  internal extension UUID from the add-on's own `browser_specific_settings.gecko.id`,
  not `allowed_origins`/a Chrome-style ID) and are placed in a
  differently-named OS directory
  (`~/.mozilla/native-messaging-hosts/` on Linux, an analogous per-OS
  path elsewhere). Firefox also still supports MV2 background pages
  as an alternative to MV3 service workers, though MV3 works there too.
  `eami-agent`'s existing browser scanner (`internal/detection/browser`)
  only covers Chrome/Edge already, so Firefox support would be new scope
  on both the extension and the B0 native-messaging-manifest side, not
  attempted here.
- **Safari**: a completely different extension model — Safari Web
  Extensions are packaged and signed via Xcode, distributed through the
  Mac App Store (or enterprise-signed for internal distribution), and
  native messaging is not supported the same way at all (Safari extensions
  talk to a companion macOS *app*, not an arbitrary native-messaging host
  manifest) — porting this would be a materially different build, not a
  manifest tweak.

## Enterprise force-install (documented, not configured/tested here)

Chrome/Edge support force-installing an extension org-wide via Group
Policy / Chrome Browser Cloud Management
(`ExtensionInstallForcelist`/`ExtensionSettings` policy keys), pointing
at either a Chrome Web Store listing or an internally-hosted `update.xml`
+ signed `.crx`. This requires either Web Store publication or an
internal update server — out of scope for this brief (which only covers
local unpacked-extension loading for dev/testing), but the same
`key`-pinned extension ID here is exactly what a force-install policy
would need to reference.

## Manual test checklist

See the COMPLETION REPORT for this task (B1) for the exact steps and
what was already verified automatically vs. what's worth spot-checking
by hand in your own everyday Chrome profile.
