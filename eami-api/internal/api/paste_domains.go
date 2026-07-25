package api

import "strings"

// KnownPasteDestinations lists hostnames (or hostname suffixes) for known
// AI-tool web UIs that the paste-detection ingestion path accepts events
// for. Same shape/convention as eami-agent's
// internal/detection/network_activity.KnownAIHosts -- deliberately not the
// same list: that one covers API hostnames (api.openai.com); this one
// covers the browser-facing UI domains a paste event is actually reported
// against (chat.openai.com). Kept as a separate list in eami-api rather
// than imported because eami-agent's list lives under an internal/
// package in a different Go module (go.work), which Go does not allow
// importing across module boundaries.
var KnownPasteDestinations = []string{
	"chat.openai.com",
	"claude.ai",
	"copilot.microsoft.com",
	"gemini.google.com",
	"perplexity.ai",
	"poe.com",
}

// MatchesKnownPasteDestination returns true if domain matches any entry in
// KnownPasteDestinations, exactly or as a subdomain. Exported for testing.
func MatchesKnownPasteDestination(domain string) bool {
	d := strings.ToLower(domain)
	for _, known := range KnownPasteDestinations {
		if d == known || strings.HasSuffix(d, "."+known) {
			return true
		}
	}
	return false
}
