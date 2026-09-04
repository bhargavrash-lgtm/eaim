// Package redaction implements pattern-based sensitive-data detection and
// masking for outbound ai_provider dispatch content (B-156 investigation,
// built as B-167).
//
// Explicitly scoped to pattern-based detection only -- semantic/ML-based
// detection is a distinct, later tier genuinely requiring the still-parked
// local-LLM capability (sending content to an external AI provider to judge
// whether it's safe to send to an external AI provider is circular). This
// package never calls any external service; every pattern here is a plain
// Go stdlib regexp, evaluated entirely in-process.
//
// Design, per the B-156 investigation's own recommendation (not re-derived
// here -- see BACKLOG.md's B-156 entry for the full reasoning):
//   - Replicates Microsoft Presidio's proven pattern-design (a named regex
//     plus an optional checksum/confidence validator) natively rather than
//     adopting Presidio itself or a third-party Go library -- zero new
//     runtime dependency for this narrow, well-defined entity set on a
//     compliance-critical path.
//   - MASK, not tokenize, for v1: an irreversible replacement needs no
//     token-to-value map, no lifecycle, no new secret-management surface.
//     Tokenization remains deliberately deferred as its own dedicated,
//     separately-security-reviewed brief.
package redaction

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// CustomPattern is one admin-defined regex rule (gateway_tools.redaction_rules
// JSONB, per-connector, B-156 Part C).
type CustomPattern struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// Config is one connector's redaction configuration, parsed from
// gateway_tools.redaction_rules. A nil/empty column means DefaultConfig()
// applies -- redaction ON, every built-in pattern enabled, no custom
// patterns -- the fail-safe default: a newly created ai_provider connector
// redacts by default, matching this codebase's own established "safe
// default, explicit opt-out" convention (audit_mode/data_handling_
// designation both default to their safest value, never their most
// permissive).
type Config struct {
	// Enabled turns redaction off entirely for this connector when false
	// (an explicit admin opt-out, e.g. for a private/on-prem provider
	// already outside the "data leaves the building" concern). Resolved by
	// ParseConfig, not read directly off the wire: an admin submitting
	// {"custom_patterns": [...]} without mentioning "enabled" at all must
	// still redact (fail-safe on), which a plain JSON `bool` field cannot
	// distinguish from an explicit {"enabled": false} -- see the raw parse
	// struct inside ParseConfig.
	Enabled bool
	// DisabledPatterns lists built-in pattern names (see the builtins
	// slice) to skip. Absent/empty means every built-in pattern is active.
	DisabledPatterns []string
	// CustomPatterns are admin-defined additional regexes, applied after
	// every active built-in.
	CustomPatterns []CustomPattern
}

// DefaultConfig is applied when a connector's redaction_rules column is
// SQL NULL -- see Config's own doc comment for why this is "on," not "off."
func DefaultConfig() Config {
	return Config{Enabled: true}
}

// MaxCustomPatterns caps custom_patterns' length (security-review
// finding, B-156/B-167): each pattern is recompiled and evaluated against
// every string in every outbound dispatch's params on the connector's hot
// dispatch path, so an unbounded count is a real per-dispatch CPU
// amplification lever. Mirrors eami-api's own identical
// maxCustomRedactionPatterns write-time check (tools.go) -- this is the
// defense-in-depth copy for a row that reached the DB by any other path
// (a direct write, a future internal tool), so Router.Dispatch never
// trusts eami-api's validation as the only gate.
const MaxCustomPatterns = 50

// rawConfig mirrors Config's wire (JSON) shape exactly, except Enabled is a
// *bool so ParseConfig can distinguish "the admin didn't mention it"
// (defaults true) from "the admin explicitly set it" (honored either way).
type rawConfig struct {
	Enabled          *bool           `json:"enabled"`
	DisabledPatterns []string        `json:"disabled_patterns,omitempty"`
	CustomPatterns   []CustomPattern `json:"custom_patterns,omitempty"`
}

// ParseConfig parses gateway_tools.redaction_rules' raw JSONB bytes. nil or
// empty input returns DefaultConfig(), not a zero-value Config -- a column
// that was never set must behave identically to one explicitly set to
// {"enabled": true}, never to {"enabled": false} (Go's zero value for bool).
func ParseConfig(raw []byte) (Config, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return DefaultConfig(), nil
	}
	var rc rawConfig
	if err := json.Unmarshal(raw, &rc); err != nil {
		return Config{}, fmt.Errorf("redaction: invalid redaction_rules: %w", err)
	}
	cfg := Config{
		Enabled:          rc.Enabled == nil || *rc.Enabled,
		DisabledPatterns: rc.DisabledPatterns,
		CustomPatterns:   rc.CustomPatterns,
	}
	if len(cfg.CustomPatterns) > MaxCustomPatterns {
		return Config{}, fmt.Errorf("redaction: at most %d custom_patterns are allowed, got %d", MaxCustomPatterns, len(cfg.CustomPatterns))
	}
	for _, cp := range cfg.CustomPatterns {
		if strings.TrimSpace(cp.Name) == "" {
			return Config{}, fmt.Errorf("redaction: custom_patterns entry has an empty name")
		}
		re, err := regexp.Compile(cp.Pattern)
		if err != nil {
			return Config{}, fmt.Errorf("redaction: custom pattern %q does not compile: %w", cp.Name, err)
		}
		// A pattern that matches the literal empty string (e.g. "", "a*",
		// "x?") fires at every rune boundary in real content -- a
		// resource-amplification lever, not a correctness bug (Redact
		// still terminates, per ReplaceAllStringFunc's guaranteed forward
		// progress), but never a legitimate sensitive-value detector --
		// rejected outright (security-review finding), mirroring eami-api's
		// identical write-time check. Deliberately simple, not exhaustive:
		// a zero-width ASSERTION that only fires within real content (e.g.
		// `\b`) doesn't match "" and isn't caught by this specific check.
		if re.MatchString("") {
			return Config{}, fmt.Errorf("redaction: custom pattern %q matches the empty string -- refine it to match a real value, not every position", cp.Name)
		}
	}
	return cfg, nil
}
