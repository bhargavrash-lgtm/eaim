package license

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// genuineTestVendorPrivateKeyPEM is the PRIVATE half matching this
// package's own embedded vendorPublicKeyPEM -- exists ONLY in this
// _test.go file (never compiled into any shipped binary) so tests can
// construct genuinely, validly-signed licenses to test non-forgery
// failure modes against (expiry, tampering, wrong issuer, ...). This is
// still just the throwaway TEST vendor keypair generated for this
// brief's own build-and-verify pass, not the real production vendor key
// -- the same disclosed scope as vendorPublicKeyPEM itself.
const genuineTestVendorPrivateKeyPEM = `-----BEGIN PRIVATE KEY-----
MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQDBuSEDfYj5SnYT
5td5JC5RHCfO8LOtvleRwG3REW0en6W2jdTSKBH8z7Si28/eP4p2k8FUdtJyWej0
nG1x8FtaPmdPssauiF6LULcFfT9YWlJsxuO8xz9sl0XfGA9P6wl1E+a7zfxARwQ6
P1Wu3lbejwz++rMeqHaBmMa4cM66rHFBfJFkjn4BRF7+JuiZxPFyl4kTurP2GiKx
tePPXycN7fWOk/dOPgIU2Wh7n5x3yUFLlTCymC2TjZoXCKYyRZrMYacTkgpmlRY1
eWQSy2q6E449SoSJZBOvCbUu7zrI54qwGHuAgTsF/3qONKuQoR7XQtFum1RyXvmZ
48Gfq/LPAgMBAAECggEASBsJzDxKIwwRpje6hRcv/DXAJXkXT/i0rIYU+ggD9y2S
J0BkcjLC+zgucp3hocZB2gAGKlOt4i1QFdgxroK55f2rQ5F1/Vm54x4QeYUUcmTw
IBfphYceNuOZeMACVwtTclYNgGLb3OryCmIvmM6eQ+m3+yJCUIuAzJ0afmVStU07
Zdo/QmqlxTDJ+tj5EYnRF9WXJPhYwaoU2IjW5blMyfUTTSPMk6h9LkCm+7dJyn6D
Upy1SktmWWZRnapCUD3YhJ5W2kImkQMd1BCn1wvxzbpbsE6mnwBcZhOkVOY0PMeS
uxv1Bl5pfYHMfVupUjs+8E9mOD/xDbvNqb/Oc+kOUQKBgQDkpobkwPmjw1sqG2eu
tRtrMQ87Lx/HVLkvkMCLDMCb2GON5sp7oZ0qMxyHESvCdom0C0zAdKF0EHZWY044
Qfr0vr+UFoVkJvFmJliAi1rGRrvuR1KFGZevIneTIYrLz4WlNo3ZluPH7uNcfc89
ywKSK4Vkdo966x+PcRnRkPUVXwKBgQDY5RjkoD7CExDuniI/slTQ3vnJr8fcHfTg
UOKhXHfS5nnLY3oFXoF8s8HqW83ymJPB0TaKen/3XTV3vrT3nK3wySGQF/PIx4Qs
k1r3hj7yYFsZdH5Jla8hL1d1a8UWWklJVDcsOlscBHaQrzWaYAfp2hCW/NyS2GjB
Bcd20PwokQKBgE0x46zrcdzWIbsvkWusfVtNLuU+Xa5Abl0es8K+RXDYN5Q67PWc
dKFArEr1gx6eQpNklT8MoU28GRfFYy0fKYjjtW5bxCEx/KIOJCcR5U23p88kiTmi
kFFyg4hK9L8miupiZrWlebWQc3ZQi11DYtTSmLB4TqyjIP6eoqbcF8JlAoGAFP0m
iYlQSWua6dx3p/5T4tqRBYlzJ8PmXIa3R7IxDkGra5k2x6o7kZu7mjhEF8PYGJts
Ub5E/+UPNYVI8eVBl9l+2/jVaIqWKdIgrW9aTA4zAqWZSvmnNujj58MEEYOvL99s
b2U+R9nOt3WdFFFSsridfl794V/70yICCWdz32ECgYAS777OZaKjD6N+pA6x9dBa
NQRKuTRTKVIPHHOLeghPJeKwn8UOnEaxczHzta0nfUakYgK91ptWSx/UaxwTf46+
M2U06OdWTUyo0uSY11SDrhV2O2+hJrjeUJtkSBbXxJnIluNJND5bAW4hMCc/MHld
/kOn6nJcxyFq4SM8Trgz2w==
-----END PRIVATE KEY-----`

func genuineTestVendorKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	block, _ := pem.Decode([]byte(genuineTestVendorPrivateKeyPEM))
	if block == nil {
		t.Fatal("genuineTestVendorKey: invalid PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("genuineTestVendorKey: parse: %v", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("genuineTestVendorKey: not an RSA key")
	}
	return rsaKey
}

// signTestLicense builds and signs a license JWT with the given key --
// used both for the "real, validly-signed license" happy-path tests
// (signed with a SEPARATE test vendor key, not this package's embedded
// one -- see genuineTestVendorKey below) and for the adversarial forgery
// tests (signed with a key an attacker who only has appliance-side
// access could plausibly generate themselves).
func signTestLicense(t *testing.T, key *rsa.PrivateKey, claims Claims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign test license: %v", err)
	}
	return signed
}

func validClaims(orgID string) Claims {
	now := time.Now().UTC()
	return Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   orgID,
			Issuer:    Issuer,
			Audience:  jwt.ClaimStrings{Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Hour)),
			ExpiresAt: jwt.NewNumericDate(now.Add(365 * 24 * time.Hour)),
		},
		Modules: []string{"discovery", "gateway"},
	}
}

// ─── Happy path: a genuine, validly-signed license is accepted ────────────

func TestVerify_GenuinelySignedLicense_Accepted(t *testing.T) {
	orgID := "99999999-9999-9999-9999-999999999999"
	genuine := signTestLicense(t, genuineTestVendorKey(t), validClaims(orgID))

	claims, err := Verify(genuine)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.OrgID() != orgID {
		t.Errorf("OrgID() = %q, want %q", claims.OrgID(), orgID)
	}
	if !claims.HasModule("discovery") || !claims.HasModule("gateway") {
		t.Errorf("Modules = %v, want discovery and gateway both present", claims.Modules)
	}
	if claims.HasModule("ai_infrastructure") {
		t.Error("HasModule(ai_infrastructure) = true, want false (not in this license)")
	}
}

// ─── AC4: adversarial forgery proof ─────────────────────────────────────────
//
// Every test below uses ONLY what a Go program running on the appliance
// itself could construct -- crypto/rand, crypto/rsa, the jwt library
// already vendored, and the PUBLIC key this package itself already
// exposes by necessity (it has to, to verify anything). None of these
// tests have access to, or attempt to reconstruct, the real vendor
// private key -- that's the entire point being proven: nothing available
// on the appliance is sufficient.

// TestVerify_ForgedWithAttackerOwnKey_Rejected is the central adversarial
// proof: an attacker generates their OWN, entirely real, valid RSA
// keypair (exactly what tokens.go's own loadOrGenerateKey does for agent
// identity, and exactly what's available to anyone with a Go toolchain on
// the appliance) and signs a license granting every module, unlimited
// validity. Verify must reject it, because the SIGNATURE doesn't match
// the embedded vendor public key -- not because anything about the
// claims themselves looks suspicious.
func TestVerify_ForgedWithAttackerOwnKey_Rejected(t *testing.T) {
	attackerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate attacker key: %v", err)
	}
	forged := signTestLicense(t, attackerKey, validClaims("11111111-1111-1111-1111-111111111111"))

	claims, err := Verify(forged)
	if err == nil {
		t.Fatal("expected a forged license (attacker's own keypair) to be rejected, got nil error")
	}
	if claims != nil {
		t.Errorf("expected nil claims alongside the rejection, got %+v", claims)
	}
}

// TestVerify_AlgNoneAttack_Rejected proves the classic "alg: none"
// downgrade -- a token that claims it needs no signature verification at
// all -- is rejected. Built by hand (jwt.SigningMethodNone requires the
// library's own "I understand the risk" sentinel key to sign at all,
// which this test deliberately supplies only to construct the attack
// token, never to make Verify accept it).
func TestVerify_AlgNoneAttack_Rejected(t *testing.T) {
	claims := validClaims("22222222-2222-2222-2222-222222222222")
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	forged, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("construct alg=none token: %v", err)
	}

	if _, err := Verify(forged); err == nil {
		t.Fatal("expected an alg=none token to be rejected, got nil error")
	}
}

// TestVerify_HMACConfusionUsingPublicKeyBytes_Rejected proves the classic
// RS256/HS256 key-confusion attack -- re-signing the token with HS256
// using the EMBEDDED PUBLIC KEY's own PEM bytes as the HMAC secret (a
// real, well-known JWT library vulnerability class when a verifier keyfunc
// naively returns "the key" for whatever alg the attacker's token header
// claims, regardless of which alg was actually expected). This package's
// own keyfunc explicitly checks `t.Method.(*jwt.SigningMethodRSA)` before
// ever returning vendorPublicKey -- this test proves that guard actually
// holds, not just that it's present in the source.
func TestVerify_HMACConfusionUsingPublicKeyBytes_Rejected(t *testing.T) {
	claims := validClaims("33333333-3333-3333-3333-333333333333")
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	forged, err := token.SignedString([]byte(vendorPublicKeyPEM))
	if err != nil {
		t.Fatalf("construct HMAC-confusion token: %v", err)
	}

	if _, err := Verify(forged); err == nil {
		t.Fatal("expected an HS256 key-confusion token to be rejected, got nil error")
	}
}

// TestVerify_TamperedClaimsAfterGenuineSigning_Rejected proves a genuinely
// signed license (by the real test vendor key used elsewhere in this
// file) can't have its claims edited post-signature -- e.g. an attacker
// with a real, expired or module-limited license tries to extend
// valid_until or add a module by editing the JWT's base64 payload segment
// directly, without re-signing (since they don't have the private key to
// re-sign).
func TestVerify_TamperedClaimsAfterGenuineSigning_Rejected(t *testing.T) {
	genuine := signTestLicense(t, genuineTestVendorKey(t), validClaims("44444444-4444-4444-4444-444444444444"))
	parts := strings.Split(genuine, ".")
	if len(parts) != 3 {
		t.Fatalf("expected a 3-part JWT, got %d parts", len(parts))
	}
	// Corrupt the payload segment (flip a character) -- simulates editing
	// the claims without possessing the private key to re-sign.
	tamperedPayload := parts[1][:len(parts[1])-1] + "X"
	tampered := parts[0] + "." + tamperedPayload + "." + parts[2]

	if _, err := Verify(tampered); err == nil {
		t.Fatal("expected a tampered-payload license to be rejected, got nil error")
	}
}

// TestVerify_WrongIssuerOrAudience_Rejected proves the issuer/audience
// checks are real, not decorative -- a token genuinely signed by the
// SAME test vendor key (so the signature itself is valid) but claiming a
// different issuer is still rejected. Guards against, e.g., a different
// EAMI-issued RS256 JWT (an agent identity token from tokens.go, if it
// somehow used the same keypair) being replayed here.
func TestVerify_WrongIssuerOrAudience_Rejected(t *testing.T) {
	key := genuineTestVendorKey(t)
	c := validClaims("55555555-5555-5555-5555-555555555555")
	c.Issuer = "not-eami-license-authority"
	wrongIssuer := signTestLicense(t, key, c)
	if _, err := Verify(wrongIssuer); err == nil {
		t.Fatal("expected a wrong-issuer license to be rejected, got nil error")
	}

	c2 := validClaims("66666666-6666-6666-6666-666666666666")
	c2.Audience = jwt.ClaimStrings{"not-eami-appliance"}
	wrongAudience := signTestLicense(t, key, c2)
	if _, err := Verify(wrongAudience); err == nil {
		t.Fatal("expected a wrong-audience license to be rejected, got nil error")
	}
}

func TestVerify_MalformedInput_Rejected(t *testing.T) {
	for _, in := range []string{"", "not-a-jwt-at-all", "a.b", "a.b.c.d"} {
		if _, err := Verify(in); err == nil {
			t.Errorf("Verify(%q): expected an error, got nil", in)
		}
	}
}

func TestVerify_ExpiredLicense_Rejected(t *testing.T) {
	key := genuineTestVendorKey(t)
	c := validClaims("77777777-7777-7777-7777-777777777777")
	now := time.Now().UTC()
	c.IssuedAt = jwt.NewNumericDate(now.Add(-400 * 24 * time.Hour))
	c.NotBefore = jwt.NewNumericDate(now.Add(-400 * 24 * time.Hour))
	c.ExpiresAt = jwt.NewNumericDate(now.Add(-1 * time.Hour)) // expired 1 hour ago
	expired := signTestLicense(t, key, c)

	if _, err := Verify(expired); err == nil {
		t.Fatal("expected an expired license to be rejected, got nil error")
	}
}

func TestVerify_NotYetValidLicense_Rejected(t *testing.T) {
	key := genuineTestVendorKey(t)
	c := validClaims("88888888-8888-8888-8888-888888888888")
	now := time.Now().UTC()
	c.NotBefore = jwt.NewNumericDate(now.Add(1 * time.Hour)) // not valid until an hour from now
	notYetValid := signTestLicense(t, key, c)

	if _, err := Verify(notYetValid); err == nil {
		t.Fatal("expected a not-yet-valid license to be rejected, got nil error")
	}
}

// ─── HasModule ───────────────────────────────────────────────────────────────

func TestClaims_HasModule(t *testing.T) {
	c := &Claims{Modules: []string{"discovery", "gateway"}}
	if !c.HasModule("discovery") {
		t.Error("HasModule(discovery) = false, want true")
	}
	if c.HasModule("ai_infrastructure") {
		t.Error("HasModule(ai_infrastructure) = true, want false")
	}
}

func TestClaims_HasModule_NilClaims_FailsClosed(t *testing.T) {
	var c *Claims
	if c.HasModule("discovery") {
		t.Error("HasModule on a nil Claims = true, want false (fail closed on no license at all)")
	}
}

func TestClaims_OrgID(t *testing.T) {
	c := &Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "org-123"}}
	if c.OrgID() != "org-123" {
		t.Errorf("OrgID() = %q, want org-123", c.OrgID())
	}
}
