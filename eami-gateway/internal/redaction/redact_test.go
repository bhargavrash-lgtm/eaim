package redaction

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseConfig_NilOrEmpty_ReturnsDefaultEnabled(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, []byte("null"), []byte("  null  ")} {
		cfg, err := ParseConfig(raw)
		if err != nil {
			t.Fatalf("ParseConfig(%q): %v", raw, err)
		}
		if !cfg.Enabled {
			t.Errorf("ParseConfig(%q).Enabled = false, want true (fail-safe default)", raw)
		}
	}
}

// TestParseConfig_OmittedEnabled_StillDefaultsTrue proves the *bool
// indirection actually works: a real admin config that never mentions
// "enabled" at all (only sets custom_patterns) must still redact.
func TestParseConfig_OmittedEnabled_StillDefaultsTrue(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"custom_patterns":[{"name":"employee_id","pattern":"EMP-[0-9]{6}"}]}`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if !cfg.Enabled {
		t.Error("Enabled = false, want true when the field is simply omitted")
	}
	if len(cfg.CustomPatterns) != 1 || cfg.CustomPatterns[0].Name != "employee_id" {
		t.Errorf("CustomPatterns = %+v, want one employee_id entry", cfg.CustomPatterns)
	}
}

func TestParseConfig_ExplicitDisabled_Honored(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"enabled": false}`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Enabled {
		t.Error("Enabled = true, want false for an explicit {\"enabled\": false}")
	}
}

func TestParseConfig_InvalidCustomPattern_Rejected(t *testing.T) {
	_, err := ParseConfig([]byte(`{"custom_patterns":[{"name":"bad","pattern":"("}]}`))
	if err == nil {
		t.Fatal("expected an error for an uncompilable custom regex, got nil")
	}
}

func TestParseConfig_EmptyCustomPatternName_Rejected(t *testing.T) {
	_, err := ParseConfig([]byte(`{"custom_patterns":[{"name":"","pattern":"abc"}]}`))
	if err == nil {
		t.Fatal("expected an error for an empty custom pattern name, got nil")
	}
}

func TestParseConfig_MalformedJSON_Rejected(t *testing.T) {
	_, err := ParseConfig([]byte(`not json`))
	if err == nil {
		t.Fatal("expected an error for malformed JSON, got nil")
	}
}

// TestParseConfig_TooManyCustomPatterns_Rejected proves the security-review
// finding's fix: an unbounded custom_patterns list is a per-dispatch CPU
// amplification lever (each pattern is recompiled and evaluated against
// every string in every dispatch through this connector), so it's capped.
func TestParseConfig_TooManyCustomPatterns_Rejected(t *testing.T) {
	patterns := make([]CustomPattern, MaxCustomPatterns+1)
	for i := range patterns {
		patterns[i] = CustomPattern{Name: "p", Pattern: "x"}
	}
	raw, _ := json.Marshal(rawConfig{CustomPatterns: patterns})
	_, err := ParseConfig(raw)
	if err == nil {
		t.Fatal("expected an error for exceeding MaxCustomPatterns, got nil")
	}
}

func TestParseConfig_AtMaxCustomPatterns_Accepted(t *testing.T) {
	patterns := make([]CustomPattern, MaxCustomPatterns)
	for i := range patterns {
		patterns[i] = CustomPattern{Name: "p", Pattern: "x"}
	}
	raw, _ := json.Marshal(rawConfig{CustomPatterns: patterns})
	if _, err := ParseConfig(raw); err != nil {
		t.Errorf("ParseConfig at exactly MaxCustomPatterns: %v", err)
	}
}

// TestParseConfig_ZeroWidthCustomPattern_Rejected proves the other
// security-review fix: a pattern that matches the literal empty string
// (as opposed to a zero-width ASSERTION like `\b`, which only fires
// within real content and isn't caught by this specific MatchString("")
// check -- a narrower, deliberately simple heuristic, not exhaustive
// zero-width-assertion analysis) fires at every rune boundary in real
// content -- a resource-amplification lever, never a legitimate
// sensitive-value detector.
func TestParseConfig_ZeroWidthCustomPattern_Rejected(t *testing.T) {
	for _, pattern := range []string{"", "a*", "x?"} {
		raw, _ := json.Marshal(rawConfig{CustomPatterns: []CustomPattern{{Name: "zw", Pattern: pattern}}})
		if _, err := ParseConfig(raw); err == nil {
			t.Errorf("ParseConfig(pattern=%q): expected a zero-width rejection, got nil", pattern)
		}
	}
}

// ─── Redact: built-in patterns ─────────────────────────────────────────────

func TestRedact_Email_Masked(t *testing.T) {
	params := map[string]any{"text": "contact me at jane.doe@example.com please"}
	out, count, err := Redact(params, DefaultConfig())
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	s := out["text"].(string)
	if strings.Contains(s, "jane.doe@example.com") {
		t.Errorf("original email still present in output: %q", s)
	}
	if !strings.Contains(s, "[REDACTED:EMAIL]") {
		t.Errorf("mask token missing from output: %q", s)
	}
}

func TestRedact_SSN_Masked(t *testing.T) {
	params := map[string]any{"text": "SSN is 123-45-6789 on file"}
	out, count, err := Redact(params, DefaultConfig())
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	s := out["text"].(string)
	if strings.Contains(s, "123-45-6789") {
		t.Errorf("original SSN still present in output: %q", s)
	}
}

// TestRedact_CreditCard_OnlyValidLuhnMatched proves the checksum validator
// actually filters candidates, not just the regex shape -- a real test
// card number (Luhn-valid) is redacted; a same-length-and-shape but
// Luhn-invalid number is left alone.
func TestRedact_CreditCard_OnlyValidLuhnMatched(t *testing.T) {
	params := map[string]any{
		"valid":   "card 4532015112830366 on file", // real Luhn-valid test number
		"invalid": "reference 1234567890123456 for this ticket",
	}
	out, count, err := Redact(params, DefaultConfig())
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (only the Luhn-valid candidate)", count)
	}
	if strings.Contains(out["valid"].(string), "4532015112830366") {
		t.Error("Luhn-valid card number was not redacted")
	}
	if !strings.Contains(out["invalid"].(string), "1234567890123456") {
		t.Error("Luhn-invalid number was incorrectly redacted")
	}
}

func TestRedact_APIKeyShaped_Masked(t *testing.T) {
	params := map[string]any{"text": "use key sk-ant-abcdefghijklmnopqrstuvwxyz1234 to auth"}
	out, count, err := Redact(params, DefaultConfig())
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	if strings.Contains(out["text"].(string), "sk-ant-abcdefghijklmnopqrstuvwxyz1234") {
		t.Error("original API key still present in output")
	}
}

// ─── Redact: structural correctness ────────────────────────────────────────

func TestRedact_NestedStructure_AllStringsScanned(t *testing.T) {
	params := map[string]any{
		"model": "claude-opus-4-6",
		"messages": []any{
			map[string]any{"role": "user", "content": "my email is a@b.com"},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "ssn 111-22-3333"},
			}},
		},
	}
	out, count, err := Redact(params, DefaultConfig())
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (one email, one SSN, across nested structure)", count)
	}
	msgs := out["messages"].([]any)
	first := msgs[0].(map[string]any)
	if strings.Contains(first["content"].(string), "a@b.com") {
		t.Error("nested email was not redacted")
	}
	second := msgs[1].(map[string]any)
	nested := second["content"].([]any)[0].(map[string]any)
	if strings.Contains(nested["text"].(string), "111-22-3333") {
		t.Error("deeply nested SSN was not redacted")
	}
}

// TestRedact_NoMatch_ReturnsEquivalentButDistinctTree proves AC5: content
// with no matching pattern comes back logically unchanged (deep-equal),
// even though Redact always returns a new tree (see Redact's own doc
// comment on why it never mutates the input).
func TestRedact_NoMatch_ReturnsEquivalentButDistinctTree(t *testing.T) {
	params := map[string]any{
		"model":      "claude-opus-4-6",
		"max_tokens": float64(1024),
		"messages":   []any{map[string]any{"role": "user", "content": "hello, how are you today?"}},
	}
	out, count, err := Redact(params, DefaultConfig())
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
	if !reflect.DeepEqual(params, out) {
		t.Errorf("no-match content changed shape:\n  in:  %#v\n  out: %#v", params, out)
	}
}

// TestRedact_NeverMutatesInput is the direct proof behind Redact's own doc
// comment: the caller's original map must be untouched, since it's also
// used to build the audit_log/episode snapshot elsewhere in the real call
// chain (aiprovider.Router.Dispatch, cmd/gateway/dispatcher.go).
func TestRedact_NeverMutatesInput(t *testing.T) {
	original := map[string]any{"text": "email me at test@example.com"}
	snapshot := map[string]any{"text": "email me at test@example.com"}

	if _, _, err := Redact(original, DefaultConfig()); err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if !reflect.DeepEqual(original, snapshot) {
		t.Errorf("Redact mutated its input: got %#v, want unchanged %#v", original, snapshot)
	}
}

// ─── Redact: configuration ─────────────────────────────────────────────────

func TestRedact_Disabled_PassesThroughUnchanged(t *testing.T) {
	params := map[string]any{"text": "email test@example.com and ssn 123-45-6789"}
	out, count, err := Redact(params, Config{Enabled: false})
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 when redaction is disabled", count)
	}
	if out["text"].(string) != params["text"].(string) {
		t.Errorf("content changed while redaction was disabled: %q", out["text"])
	}
}

func TestRedact_DisabledPattern_SkipsThatEntityOnly(t *testing.T) {
	params := map[string]any{"text": "email test@example.com and ssn 123-45-6789"}
	cfg := Config{Enabled: true, DisabledPatterns: []string{"email"}}
	out, count, err := Redact(params, cfg)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (SSN only, email pattern disabled)", count)
	}
	s := out["text"].(string)
	if !strings.Contains(s, "test@example.com") {
		t.Error("email was redacted despite being in DisabledPatterns")
	}
	if strings.Contains(s, "123-45-6789") {
		t.Error("SSN was not redacted")
	}
}

func TestRedact_CustomPattern_Applied(t *testing.T) {
	params := map[string]any{"text": "employee EMP-123456 requested access"}
	cfg := Config{Enabled: true, CustomPatterns: []CustomPattern{{Name: "employee_id", Pattern: `EMP-\d{6}`}}}
	out, count, err := Redact(params, cfg)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	s := out["text"].(string)
	if strings.Contains(s, "EMP-123456") {
		t.Error("original custom-pattern match still present in output")
	}
	if !strings.Contains(s, "[REDACTED:EMPLOYEE_ID]") {
		t.Errorf("custom mask token missing: %q", s)
	}
}

// TestRedact_MaskToken_NeverContainsOriginalValue is a direct proof of this
// brief's own audit contract ("never the actual redacted values") at the
// masking layer itself: for a range of real-shaped sensitive inputs, the
// mask token is a fixed, generic string containing no fragment of the
// original value.
func TestRedact_MaskToken_NeverContainsOriginalValue(t *testing.T) {
	inputs := []string{
		"jane.doe@example.com",
		"123-45-6789",
		"sk-ant-abcdefghijklmnopqrstuvwxyz1234",
	}
	for _, in := range inputs {
		params := map[string]any{"text": in}
		out, count, err := Redact(params, DefaultConfig())
		if err != nil {
			t.Fatalf("Redact(%q): %v", in, err)
		}
		if count == 0 {
			t.Fatalf("Redact(%q) matched nothing -- test input isn't exercising a real pattern", in)
		}
		if strings.Contains(out["text"].(string), in) {
			t.Errorf("mask output for %q still contains the original value: %q", in, out["text"])
		}
	}
}
