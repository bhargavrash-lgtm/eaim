package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// rateLimitLogin is B-070's middleware guarding POST /v1/auth/login. Both
// keys are checked -- per-IP alone doesn't stop credential stuffing spread
// across many accounts from one IP; per-account alone doesn't stop one IP
// hammering many different accounts. Login() itself is completely
// unmodified: this middleware peeks the request body only to extract the
// email for per-account keying, then restores it byte-for-byte so Login()
// sees exactly what the caller sent.
func (s *Server) rateLimitLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The read error, if any, is deliberately ignored rather than used
		// to skip rate limiting: io.ReadAll can return a non-nil err
		// alongside a complete, fully-valid body (e.g. a malformed chunked
		// trailer arriving after every real body byte has already been
		// delivered) -- treating "read errored" as "skip the limiter"
		// would let a syntactically valid credential-guess slip through
		// uncounted. Whatever bytes were captured are always restored and
		// always counted against the IP limiter below; Login() sees
		// exactly those same bytes and produces its own error if they
		// don't parse.
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))

		if ok, retryAfter := s.loginIPLimiter.Allow(clientKey(r)); !ok {
			setRetryAfter(w, retryAfter)
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts from this address -- try again later")
			return
		}

		// A malformed/missing email just means no account-specific key is
		// checked -- the IP limiter above still applies, and Login() will
		// reject the malformed request itself with the correct 400.
		//
		// Decoded with json.NewDecoder(...).Decode, NOT json.Unmarshal:
		// Login()'s own decodeJSON (middleware.go) uses the streaming
		// Decoder, which reads only the first JSON value and silently
		// ignores anything after it. json.Unmarshal, by contrast, errors on
		// trailing data. A body like `{"email":"victim@x.com",...}X` (one
		// byte of garbage appended) would fail Unmarshal here -- skipping
		// the account limiter entirely -- while Login() still parses it
		// normally via Decode, letting an attacker who appends a byte
		// bypass the tighter per-account limit and fall back to the looser
		// per-IP one. Must match Login()'s own parser exactly so both
		// paths agree on what counts as "the same request".
		var req LoginRequest
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&req); err == nil && req.Email != "" {
			accountKey := normalizeLoginEmail(req.Email)
			if ok, retryAfter := s.loginAccountLimiter.Allow(accountKey); !ok {
				setRetryAfter(w, retryAfter)
				writeError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts for this account -- try again later")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// normalizeLoginEmail folds case/whitespace so "User@x.com" and
// "user@x.com" share one rate-limit bucket rather than two -- a
// deliberately conservative choice independent of GetUserByEmail's own
// query, which is a case-sensitive `=` match (unrelated pre-existing
// behavior, out of this brief's scope to change). Folding here only ever
// makes the limiter stricter, never weaker: it can merge two distinct
// real accounts that differ only by case into one bucket, but never lets
// case variation stretch a single account's effective quota.
func normalizeLoginEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
