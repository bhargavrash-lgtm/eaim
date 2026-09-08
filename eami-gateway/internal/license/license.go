// Package license verifies signed license files for the modular licensing
// & entitlement system (B-157 epic, Brief 1 built as B-169).
//
// Trust model, DELIBERATELY INVERTED from this codebase's existing
// eami-gateway/internal/identity/tokens.go agent-identity JWT pattern:
// tokens.go generates its own RSA keypair on first boot and both signs
// AND verifies with it (correct for agent identity, where the appliance
// itself is the trusted issuer). A license must never work that way -- the
// whole point is that the appliance can never mint its own valid license.
// So this package contains ONLY verification: a fixed public key baked in
// at build time, parsed once, never generated, never paired with a
// private key anywhere in this codebase. The matching private key lives
// exclusively in EAMI's own off-appliance vendor tooling -- not in this
// repository, not in any Docker image, not in Postgres, not on the
// appliance filesystem, ever.
//
// Verified entirely offline: no network call, no dependency on any
// EAMI-operated service, matching ADR-020 Model A's own explicit
// commitment ("zero data ever reaches EAMI-operated infrastructure").
//
// Deliberately duplicated in eami-api/internal/license (identical public
// key, identical logic) rather than shared: eami-api and eami-gateway are
// separate Go modules per go.work and cannot import each other's internal
// packages (the same established constraint documented in, e.g.,
// eami-gateway/internal/workflow/connector.go's own doc comment, which
// duplicates a small security-relevant helper from internal/approval for
// the identical reason). eami-gateway independently re-verifies the raw
// JWT at read/dispatch time rather than trusting eami-api's already-
// decoded gateway_tools-style columns -- closing the gap where a
// privileged direct-DB write could otherwise hand-craft a licenses row
// with fabricated modules/valid_until values that bypassed eami-api's own
// upload-time verification entirely.
package license

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// Issuer/Audience are checked on every verification, the same defense-in-
// depth reasoning as tokens.go's own jwt.WithIssuer/jwt.WithAudience use --
// closes any alg-confusion or wrong-token-type mixing risk (e.g. a
// gateway-issued agent JWT being fed into this verifier by mistake or by
// design, since both are RS256 JWTs).
const (
	Issuer   = "eami-license-authority"
	Audience = "eami-appliance"
)

// vendorPublicKeyPEM is a TEST keypair's public half, generated solely for
// this brief's own build-and-live-verify pass -- NOT the real production
// vendor key. Real vendor-side issuance (generating the actual production
// keypair, keeping the private half exclusively off-appliance, and
// replacing this constant with the real public key before any real
// customer-facing build) is explicitly out of this brief's scope per its
// own SCOPE section ("signing tooling is vendor-side, separate from this
// brief's appliance-facing scope").
const vendorPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAwbkhA32I+Up2E+bXeSQu
URwnzvCzrb5XkcBt0RFtHp+lto3U0igR/M+0otvP3j+KdpPBVHbSclno9JxtcfBb
Wj5nT7LGrohei1C3BX0/WFpSbMbjvMc/bJdF3xgPT+sJdRPmu838QEcEOj9Vrt5W
3o8M/vqzHqh2gZjGuHDOuqxxQXyRZI5+AURe/ibomcTxcpeJE7qz9hoisbXjz18n
De31jpP3Tj4CFNloe5+cd8lBS5Uwspgtk42aFwimMkWazGGnE5IKZpUWNXlkEstq
uhOOPUqEiWQTrwm1Lu86yOeKsBh7gIE7Bf96jjSrkKEe10LRbptUcl75mePBn6vy
zwIDAQAB
-----END PUBLIC KEY-----`

// vendorPublicKey is parsed once at package init. A parse failure here is
// a build-time/deployment defect (a corrupted constant), not a runtime
// condition to handle gracefully -- panicking on init is the same
// discipline this codebase already applies to other must-never-fail
// startup invariants.
var vendorPublicKey *rsa.PublicKey

func init() {
	block, _ := pem.Decode([]byte(vendorPublicKeyPEM))
	if block == nil {
		panic("license: embedded vendor public key is not valid PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic(fmt.Sprintf("license: parse embedded vendor public key: %v", err))
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		panic("license: embedded vendor public key is not an RSA key")
	}
	vendorPublicKey = rsaPub
}

// Claims are a license's signed contents. Subject is the org_id (a UUID
// string) this license was issued for -- the standard JWT "who this token
// is about" field, mirroring tokens.go's own Subject convention (there,
// the agent_id; here, the org_id). ExpiresAt/NotBefore (both from
// jwt.RegisteredClaims) ARE the license's real validity window --
// deliberately not duplicated as separate custom fields.
type Claims struct {
	jwt.RegisteredClaims
	Modules []string `json:"modules"`
	// UsageLimits (B-157 epic, Brief 2) is the license's optional, SEPARATE
	// volume-metering axis -- migration 000015's own doc comment:
	// feature-gating (Modules) and usage-metering (how much of a module)
	// are different concerns. Nil/absent means unlimited, the same
	// "absence of a claim means no restriction" convention HasModule's
	// own nil-Claims check already establishes. Identically duplicated in
	// eami-api/internal/license, same reason as the rest of this package.
	UsageLimits *UsageLimits `json:"usage_limits,omitempty"`
}

// UsageLimits defines a license's volume caps. Brief 2 defines its first
// and only dimension: a rolling-calendar-month token budget (input +
// output combined), enforced by Store.WithinUsageLimit against the SAME
// token_usage aggregation infrastructure B-097/108/111/112 already built
// -- never a separate counter.
type UsageLimits struct {
	MaxTokensPerMonth *int64 `json:"max_tokens_per_month,omitempty"`
}

// OrgID returns the license's Subject claim -- named for callers so they
// don't need to know Subject is where this package puts it.
func (c *Claims) OrgID() string { return c.Subject }

// Verify parses and validates a raw signed license string against the
// embedded vendor public key. Entirely offline -- no network call, no
// dependency on any external service. Returns a clean error for every
// failure mode (malformed input, wrong/unexpected signing algorithm,
// bad signature, expired/not-yet-valid, wrong issuer/audience) -- never a
// panic, matching this codebase's established "clean rejection, never a
// panic" discipline for every other trust-boundary parser (ClaudeAdapter,
// aiprovider.Router.Dispatch, tokens.go's own Validate).
func Verify(rawLicense string) (*Claims, error) {
	if rawLicense == "" {
		return nil, errors.New("license: empty license")
	}
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(rawLicense, claims, func(t *jwt.Token) (any, error) {
		// Explicitly require RS256 -- rejects both a completely different
		// algorithm and the classic "alg: none" / HMAC-confusion attack
		// (an attacker re-signing with HS256 using the PUBLIC key bytes
		// as an HMAC secret, since jwt.Parse would otherwise use whatever
		// key this callback returns as-is for whatever alg the token
		// claims). Mirrors tokens.go's identical guard.
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("license: unexpected signing method: %v", t.Header["alg"])
		}
		return vendorPublicKey, nil
	}, jwt.WithIssuer(Issuer), jwt.WithAudience(Audience), jwt.WithValidMethods([]string{"RS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return nil, fmt.Errorf("license: invalid license: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("license: license claims invalid")
	}
	if claims.Subject == "" {
		return nil, errors.New("license: license has no org_id (subject)")
	}
	return claims, nil
}

// HasModule reports whether claims licenses the named module. A nil
// Claims (e.g., a caller that couldn't resolve any license at all for an
// org) always returns false -- fail closed, never fail open on a missing
// license.
func (c *Claims) HasModule(module string) bool {
	if c == nil {
		return false
	}
	for _, m := range c.Modules {
		if m == module {
			return true
		}
	}
	return false
}
