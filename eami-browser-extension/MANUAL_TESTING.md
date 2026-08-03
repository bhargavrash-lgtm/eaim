# Manual test checklist — B1

What's already verified automatically (see `BUILT.md`'s B1 entry / the
COMPLETION REPORT for this task) vs. what needs a real browser click-through
by a human:

- **Fully verified already, no need to re-check:** the native-messaging
  protocol shape, the extension ID computation, B0's registration with
  the real ID, and the entire native-messaging→backend leg (a message
  matching exactly what `background.js` sends was sent through the real
  registered B0 host into a real Docker Postgres/collector/API stack and
  confirmed landing in `endpoint_reports`).
- **Needs a real browser** (no browser-automation tool was available in
  the environment this was built in — see the COMPLETION REPORT for why):
  everything below.

## Prerequisites

1. `eami-agent` installed and its native-messaging host registered with
   this extension's real ID (`ngmdfnecljeoleiancdedbmhjdihaoaa`) —
   already the case if you installed via the MSI/`.deb`/`.pkg` built
   after this task, or run `eami-agent.exe --register-native-messaging install`
   once by hand.
2. Chrome or Edge, developer mode enabled in `chrome://extensions`.

## 1. Load the extension and confirm its ID

- `chrome://extensions` → enable "Developer mode" → "Load unpacked" →
  select `eami-browser-extension/`.
- **Check:** the ID shown under the extension's name is exactly
  `ngmdfnecljeoleiancdedbmhjdihaoaa`. If it's different, something about
  the `key` field or the loaded folder doesn't match what's documented —
  worth investigating before continuing.
- **Check (AC5 — permissions):** the extension's detail page should only
  ever have shown a permission request for native-messaging + the 6
  listed sites + storage — never a broad "Read and change all your data
  on all websites" warning.

## 2. Paste on an allowlisted domain (AC1)

- Navigate to any of: `chat.openai.com`, `claude.ai`,
  `copilot.microsoft.com`, `gemini.google.com`, `perplexity.ai`, `poe.com`.
- Paste some text into any text field on the page (the login/landing
  page is fine — the content script doesn't require being logged in).
- Open `chrome://extensions` → find "EAMI Paste Detection" → click
  "service worker" (or "Inspect views: service worker") to open its
  DevTools console.
- In that console, run:
  ```js
  chrome.storage.local.get(console.log)
  ```
- **Check:** the buffer (`eami_paste_buffer`) contains a new entry with
  the correct `destination_domain` (matching the site you pasted on),
  a recent `occurred_at`, `content_length` matching what you pasted, and
  a `content_hash` (a 64-character hex string). **Check specifically that
  the pasted text itself does not appear anywhere in this output** — that's
  the core guarantee this whole extension exists to uphold.

## 3. Paste on a non-allowlisted domain (AC2)

- Note the current buffer length from step 2's console output.
- Navigate to any other site (e.g. `https://example.com` or
  `https://en.wikipedia.org`).
- Paste some text into any text field there (if one exists; a URL bar
  paste doesn't count — it needs to be a page's own text field).
- Re-run `chrome.storage.local.get(console.log)` in the same service
  worker console.
- **Check:** the buffer length is unchanged — no new entry was added.
  (You can also check `chrome://extensions` → this extension → "Inspect
  views" while on the non-allowlisted page — there should be no content
  script execution context listed for that page at all, since Chrome
  never injects one there.)

## 4. Service worker restart doesn't lose a buffered event (AC4)

- Paste on an allowlisted domain again (per step 2) to buffer a fresh
  event.
- **Immediately** (within a few seconds, before the 1-minute flush timer
  can fire) go to `chrome://extensions` and click the extension's
  "Reload" button (the circular arrow icon on its card) — this fully
  terminates and restarts the service worker, discarding all in-memory
  state.
- Re-open the service worker's DevTools console (it will be a fresh
  instance) and run `chrome.storage.local.get(console.log)` again.
- **Check:** the event you just buffered is still there — it survived
  the restart because it was written to `chrome.storage.local`
  synchronously before any send was attempted, not held in memory.

## 5. Confirm it actually sends (cross-check, AC3 already proven separately)

- In the service worker console, run `flush()` directly (a global
  function in `background.js`) to trigger an immediate send instead of
  waiting up to a minute for the alarm.
- **Check:** no error appears in the console, and a subsequent
  `chrome.storage.local.get(console.log)` shows the buffer emptied (or
  down to whatever arrived since). If you have access to `eami-collector`'s
  logs (`docker compose logs eami-collector`) or the `endpoint_reports`
  table, you can confirm the event landed there too — this exact path was
  already verified end-to-end with synthetic data in this task's own
  build, so this step is mostly a sanity check that your specific local
  setup (B0 installed, registered, collector reachable) is wired up
  correctly, not new verification of the extension's own logic.

## If something doesn't work

- **`connectNative` fails / nothing ever sends:** confirm B0's native
  messaging manifest actually has this extension's ID in
  `allowed_origins` (Windows: check the manifest JSON next to
  `eami-agent-nmhost.exe`; re-run `--register-native-messaging install`
  if it's stale) and that `eami-agent`'s config (registry on Windows,
  `/etc/eami/agent.yaml` on Linux/macOS) has a real `collector.url`/
  `api_key` — the native-messaging host has no `--config` flag available
  to it since the browser invokes it with no arguments of its own.
- **Content script doesn't seem to run at all:** double-check you're on
  `https://` (not `http://`) for one of the 6 domains — `host_permissions`
  is scheme-specific.
