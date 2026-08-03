// domains.js -- shared domain allowlist for eami-browser-extension.
//
// Deliberately mirrors eami-api/internal/api/paste_domains.go's
// KnownPasteDestinations exactly (same values, same exact-or-subdomain
// match semantics) -- kept as a bundled, hardcoded list rather than
// fetched from a backend, matching this codebase's established
// convention for every other domain/extension allowlist
// (network_activity.KnownAIHosts, browser.knownExtensions,
// paste_domains.go itself). There is no endpoint to fetch this from
// anyway (eami-api is out of scope for this brief), and B-032's list is
// itself just Go source, not a runtime API.
//
// IMPORTANT: if paste_domains.go's KnownPasteDestinations ever changes,
// this file (and manifest.json's host_permissions/content_scripts.matches,
// which are derived from it) must be updated by hand in the same release
// -- there is no automated sync between the two.
//
// Loaded as a classic (non-module) script by both the content script
// (via manifest.json's content_scripts.js array, evaluated before
// content-script.js in the same isolated-world scope) and the background
// service worker (via importScripts()), so both share exactly one
// definition -- no copy-paste drift between the two contexts.

const EAMI_KNOWN_DOMAINS = [
  "chat.openai.com",
  "claude.ai",
  "copilot.microsoft.com",
  "gemini.google.com",
  "perplexity.ai",
  "poe.com",
];

// eamiIsKnownDomain mirrors paste_domains.go's MatchesKnownPasteDestination
// exactly: case-insensitive exact match, or subdomain (suffix) match.
function eamiIsKnownDomain(hostname) {
  const h = String(hostname || "").toLowerCase();
  for (const known of EAMI_KNOWN_DOMAINS) {
    if (h === known || h.endsWith("." + known)) {
      return true;
    }
  }
  return false;
}
