package redaction

import (
	"fmt"
	"regexp"
	"strings"
)

// maskToken is the fixed, irreversible replacement text for one matched
// entity (mask-only design, v1 -- see this package's doc comment). Never
// contains the original value, satisfying this brief's own contract that a
// redaction event records only a count, never the redacted content.
func maskToken(name string) string {
	return "[REDACTED:" + strings.ToUpper(name) + "]"
}

// compiledCustom is one admin-defined pattern, pre-compiled once per
// Redact call rather than once per string encountered.
type compiledCustom struct {
	name string
	re   *regexp.Regexp
}

// Redact walks params (a decoded JSON tree -- map[string]any/[]any/string/
// other scalars, exactly req.Params' own shape in aiprovider.Request)
// recursively, replacing every real pattern match with a fixed mask token,
// and returns a NEW tree plus the total number of items redacted.
//
// params itself is never mutated -- Redact always returns a fresh copy,
// even when nothing matches. This is deliberate, not an optimization
// left on the table: the caller (aiprovider.Router.Dispatch) uses the
// SAME params map for both the real dispatch AND the audit_log/episode
// snapshot built earlier in the call chain (cmd/gateway/dispatcher.go's
// auditEntry.Parameters, ac.Parameters). Mutating params in place would
// silently also change what audit_log/episodes record, well beyond this
// brief's own contract ("redacted/tokenized before any external API call
// is made" -- nothing wider). Only the copy handed to the adapter is ever
// redacted.
func Redact(params map[string]any, cfg Config) (map[string]any, int, error) {
	// nil is a real, meaningful distinct value from an empty map (a nil
	// map marshals to JSON `null`, an empty map to `{}`) -- preserved
	// exactly rather than normalized to {}, so a caller passing nil
	// (no real dispatch ever does, but AC5 requires zero behavioral
	// change even for this edge case) gets nil back, not a new empty map.
	if params == nil || !cfg.Enabled {
		return params, 0, nil
	}
	custom := make([]compiledCustom, 0, len(cfg.CustomPatterns))
	for _, cp := range cfg.CustomPatterns {
		re, err := regexp.Compile(cp.Pattern)
		if err != nil {
			// ParseConfig already validates every custom pattern compiles,
			// so this should be unreachable in production -- but Redact
			// must never panic on a malformed pattern that somehow reaches
			// it (e.g. a row written directly to the DB, bypassing
			// tools.go's validation), so it's a clean error, not an
			// assumption.
			return nil, 0, fmt.Errorf("redaction: custom pattern %q does not compile: %w", cp.Name, err)
		}
		custom = append(custom, compiledCustom{name: cp.Name, re: re})
	}
	disabled := make(map[string]bool, len(cfg.DisabledPatterns))
	for _, n := range cfg.DisabledPatterns {
		disabled[n] = true
	}

	count := 0
	out, ok := redactValue(params, disabled, custom, &count).(map[string]any)
	if !ok {
		// params is declared map[string]any, so redactValue (which
		// preserves the input's own kind) always returns a map[string]any
		// here -- defensive, not expected to ever trigger.
		return nil, 0, fmt.Errorf("redaction: internal error -- redacted root was not a map")
	}
	return out, count, nil
}

func redactValue(v any, disabled map[string]bool, custom []compiledCustom, count *int) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[k] = redactValue(vv, disabled, custom, count)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = redactValue(vv, disabled, custom, count)
		}
		return out
	case string:
		return redactString(val, disabled, custom, count)
	default:
		// Numbers, bools, nil -- nothing to scan.
		return v
	}
}

func redactString(s string, disabled map[string]bool, custom []compiledCustom, count *int) string {
	for _, b := range builtins {
		if disabled[b.name] {
			continue
		}
		s = b.re.ReplaceAllStringFunc(s, func(match string) string {
			if b.validate != nil && !b.validate(match) {
				return match
			}
			*count++
			return maskToken(b.name)
		})
	}
	for _, c := range custom {
		s = c.re.ReplaceAllStringFunc(s, func(match string) string {
			*count++
			return maskToken(c.name)
		})
	}
	return s
}
