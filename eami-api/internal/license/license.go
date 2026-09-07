// Package license verifies signed license files for the modular licensing
// & entitlement system (B-157 epic, Brief 1 built as B-169).
//
// Deliberately duplicated from eami-gateway/internal/license -- identical
// public key, identical verification logic -- rather than shared:
// eami-api and eami-gateway are separate Go modules per go.work and
// cannot import each other's internal packages (the same established
// constraint this codebase already accepts elsewhere, e.g.
// eami-gateway/internal/workflow/connector.go duplicating a small
// security-relevant helper from internal/approval for the identical
// reason, rather than building shared cross-module infrastructure for it).
//
// eami-api uses this package at license UPLOAD time (verify before ever
// persisting a row) -- eami-gateway uses its own identical copy at
// dispatch/read time, independently re-verifying the same raw JWT rather
// than trusting eami-api's already-decoded columns. Two independent
// verifications of the same untrusted input is the point: a privileged
// direct-DB write that hand-crafted a licenses row with fabricated
// modules/valid_until values (bypassing this package entirely) would
// still be caught the moment eami-gateway re-verifies the stored
// raw_license against its own copy of the same public key.
//
// Trust model, DELIBERATELY INVERTED from eami-gateway/internal/identity's
// agent-identity JWT pattern (that package generates its own keypair and
// both signs AND verifies with it -- correct for agent identity, wrong
// for licensing): this package contains ONLY verification. The matching
// private key lives exclusively in EAMI's own off-appliance vendor
// tooling -- never in this repository, never in any Docker image, never
// in Postgres, never on the appliance filesystem.
//
// Verified entirely offline: no network call, matching ADR-020 Model A's
// own explicit commitment ("zero data ever reaches EAMI-operated
// infrastructure").
package license

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// Issuer/Audience must match eami-gateway/internal/license's identical
// constants exactly -- both packages verify the same real licenses.
const (
	Issuer   = "eami-license-authority"
	Audience = "eami-appliance"
)

// vendorPublicKeyPEM MUST be byte-for-byte identical to eami-gateway/
// internal/license's own copy -- both packages verify the same real
// licenses issued by the same vendor key. A TEST keypair for this
// brief's own build-and-live-verify pass, not the real production vendor
// key (vendor-side issuance tooling is explicitly out of this brief's
// scope).
const vendorPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAwbkhA32I+Up2E+bXeSQu
URwnzvCzrb5XkcBt0RFtHp+lto3U0igR/M+0otvP3j+KdpPBVHbSclno9JxtcfBb
Wj5nT7LGrohei1C3BX0/WFpSbMbjvMc/bJdF3xgPT+sJdRPmu838QEcEOj9Vrt5W
3o8M/vqzHqh2gZjGuHDOuqxxQXyRZI5+AURe/ibomcTxcpeJE7qz9hoisbXjz18n
De31jpP3Tj4CFNloe5+cd8lBS5Uwspgtk42aFwimMkWazGGnE5IKZpUWNXlkEstq
uhOOPUqEiWQTrwm1Lu86yOeKsBh7gIE7Bf96jjSrkKEe10LRbptUcl75mePBn6vy
zwIDAQAB
-----END PUBLIC KEY-----`

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
// string) this license was issued for.
type Claims struct {
	jwt.RegisteredClaims
	Modules []string `json:"modules"`
}

// OrgID returns the license's Subject claim.
func (c *Claims) OrgID() string { return c.Subject }

// Verify parses and validates a raw signed license string against the
// embedded vendor public key. Entirely offline. Returns a clean error for
// every failure mode -- never a panic.
func Verify(rawLicense string) (*Claims, error) {
	if rawLicense == "" {
		return nil, errors.New("license: empty license")
	}
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(rawLicense, claims, func(t *jwt.Token) (any, error) {
		// Explicitly require RS256 -- rejects both "alg: none" and the
		// classic RS256/HS256 key-confusion attack (re-signing with
		// HS256 using the public key's own bytes as an HMAC secret).
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
// Claims always returns false -- fail closed, never fail open.
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
