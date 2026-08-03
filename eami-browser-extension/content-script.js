// content-script.js -- runs only on allowlisted AI-tool domains (enforced
// by Chrome itself via manifest.json's content_scripts.matches, which
// mirror domains.js's EAMI_KNOWN_DOMAINS exactly). Detects paste events
// and reports destination_domain/occurred_at/content_length/content_hash
// to the background service worker -- NEVER the pasted text itself.
//
// Runs in the default isolated world (no "world": "MAIN" in manifest.json)
// -- the page's own JavaScript cannot see or tamper with anything in this
// file's scope, and vice versa. This is the standard, secure Manifest V3
// content-script model, not a special hardening measure added here.
(function () {
  "use strict";

  const hostname = location.hostname;

  // Defense in depth only: Chrome's own content_scripts.matches already
  // guarantees this file never executes on any page outside the
  // allowlist. If eamiIsKnownDomain (from domains.js, loaded immediately
  // before this file per manifest.json's content_scripts.js order) ever
  // disagrees, something is misconfigured -- fail safe by doing nothing,
  // not by reporting an unexpected domain.
  if (typeof eamiIsKnownDomain !== "function" || !eamiIsKnownDomain(hostname)) {
    return;
  }

  async function sha256Hex(text) {
    const bytes = new TextEncoder().encode(text);
    const digest = await crypto.subtle.digest("SHA-256", bytes);
    const digestBytes = new Uint8Array(digest);
    let hex = "";
    for (let i = 0; i < digestBytes.length; i++) {
      hex += digestBytes[i].toString(16).padStart(2, "0");
    }
    return hex;
  }

  document.addEventListener(
    "paste",
    function (event) {
      // The pasted text is read into a local variable ONLY long enough
      // to compute its length and a hash. It is never assigned to any
      // object that gets stored (chrome.storage), logged (console.*),
      // or sent anywhere (chrome.runtime.sendMessage below carries only
      // length/hash/domain/timestamp) -- this is the load-bearing
      // guarantee this whole extension exists to uphold.
      let pastedText;
      try {
        pastedText = event.clipboardData
          ? event.clipboardData.getData("text/plain")
          : "";
      } catch (err) {
        // Clipboard access can throw in some contexts (permissions
        // policy, non-text clipboard content); nothing to report.
        return;
      }
      if (!pastedText) {
        return;
      }

      const contentLength = pastedText.length;
      const occurredAt = new Date().toISOString();

      sha256Hex(pastedText)
        .then(function (contentHash) {
          chrome.runtime.sendMessage({
            type: "eami_paste_event", // internal extension message type --
            // distinct from B0's own wire-protocol "paste_event" type,
            // which the background script uses separately when it
            // actually talks to the native messaging host.
            destination_domain: hostname,
            occurred_at: occurredAt,
            content_length: contentLength,
            content_hash: contentHash,
          });
        })
        .catch(function () {
          // Hashing failed (e.g. crypto.subtle unavailable in some
          // unusual context) -- drop the event rather than send one with
          // a missing/fabricated hash.
        });

      // pastedText and contentHash go out of scope here. Nothing else in
      // this file, or anywhere else in the extension, ever sees the raw
      // pasted text -- only contentHash (already computed) and
      // contentLength cross into chrome.runtime.sendMessage above.
    },
    true // capture phase: observe the paste without needing to intercept
    // or block it. This extension only detects paste events; it never
    // prevents, modifies, or delays the paste itself.
  );
})();
