package redaction

import "regexp"

// builtin is one built-in detector: a named regex plus an optional extra
// validator (e.g. a Luhn checksum) -- mirroring Microsoft Presidio's own
// PatternRecognizer design (regex + optional checksum + confidence),
// replicated natively here rather than adopting Presidio itself (see this
// package's doc comment).
type builtin struct {
	name     string
	re       *regexp.Regexp
	validate func(match string) bool // nil = no extra validation
}

var (
	// emailRe is a deliberately simple, practical email matcher -- not a
	// full RFC 5322 implementation (which would also match far more
	// false positives than it's worth for a redaction gate).
	emailRe = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)

	// ssnRe matches the standard US SSN display format (###-##-####).
	// Deliberately not the 9-digit-no-dashes form -- that shape collides
	// with far too many other numeric identifiers to redact safely by
	// pattern alone.
	ssnRe = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)

	// creditCardRe is a broad candidate matcher (13-19 digits, optionally
	// grouped by spaces or dashes) -- every match is then Luhn-validated
	// (see luhnValid) before being counted as real, matching Presidio's
	// own regex-plus-checksum design so an arbitrary 16-digit number isn't
	// redacted just because it happens to look card-shaped.
	creditCardRe = regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`)

	// apiKeyRe matches several real, well-known secret-key shapes:
	// Anthropic/OpenAI-style ("sk-..."), AWS access keys, and GitHub
	// personal-access-token prefixes. Not a generic "any long random
	// string" pattern -- that would false-positive on hashes, UUIDs, and
	// other everyday content far too often to be usable.
	apiKeyRe = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,})\b`)
)

// builtins is the fixed built-in pattern set (email, SSN, credit-card,
// API-key-shaped), matching the entity list the B-156 task brief itself
// specifies. Order matters only in that earlier patterns' replacements
// happen first within one string -- since replacements use distinct,
// non-overlapping mask tokens, order has no effect on the final result.
var builtins = []builtin{
	{name: "email", re: emailRe},
	{name: "ssn", re: ssnRe},
	{name: "credit_card", re: creditCardRe, validate: luhnValid},
	{name: "api_key", re: apiKeyRe},
}

// luhnValid reports whether s (after stripping spaces/dashes) passes the
// Luhn checksum -- the standard validity check for payment card numbers.
// A regex-only match on "12-19 digits" would redact ordinary long numbers
// (invoice IDs, phone numbers with an area code prefix, etc.) that happen
// to be the right length; requiring a valid checksum too is what Presidio's
// own design (regex + validator) is built around.
func luhnValid(s string) bool {
	var digits []int
	for _, r := range s {
		switch {
		case r == ' ' || r == '-':
			continue
		case r >= '0' && r <= '9':
			digits = append(digits, int(r-'0'))
		default:
			return false
		}
	}
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := digits[i]
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}
