// tokens_test.go — eami-gateway/internal/identity
// QA-EAMI — unit tests for Manager (JWT issuance, validation, revocation).
//
// Security regression tests (marked SECURITY-FAILING) verify behaviour that
// SHOULD be enforced but currently is NOT. They will fail until the owning
// agent resolves the linked tasks:
//   - TestManager_Validate_WrongIssuer_ReturnsError   → TASK-053 (JWT-001)
//   - TestManager_Validate_RevokedToken_SurvivesRestart → TASK-052 (JWT-002)
//
// Run:
//   go test ./internal/identity/... -race -count=1
// Run only security failing tests:
//   go test ./internal/identity/... -run "WrongIssuer|SurvivesRestart" -v
package identity_test

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eami/gateway/internal/identity"
	"github.com/golang-jwt/jwt/v5"
)

// ─── Test helpers ─────────────────────────────────────────────────────────────

// newManager creates a Manager backed by a fresh temp-dir RSA key.
// The key is generated once per subtest; tests that need two managers sharing
// the same key must pass the same keyPath.
func newManager(t *testing.T, keyPath string, issuer string) *identity.Manager {
	t.Helper()
	if keyPath == "" {
		keyPath = filepath.Join(t.TempDir(), "gateway.key")
	}
	m, err := identity.NewManager(keyPath, 300, issuer)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

// validRequest returns a minimal IssueRequest.
func validRequest() identity.IssueRequest {
	return identity.IssueRequest{
		AgentID:    "test-agent-uuid-1234",
		Scope:      "read:salesforce",
		Task:       "sync contacts",
		Model:      "claude-sonnet-4-6",
		Owner:      "alice@example.com",
		RiskTier:   "low",
		TTLSeconds: 300,
	}
}

// readPrivateKey loads an RSA private key PEM from path (for crafting
// adversarial tokens in security tests).
func readPrivateKey(t *testing.T, path string) *rsa.PrivateKey {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("no PEM block in key file")
	}
	pk, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	return pk
}

// ─── Issue tests ──────────────────────────────────────────────────────────────

func TestManager_Issue_ValidToken(t *testing.T) {
	m := newManager(t, "", "eami-gateway")
	resp, err := m.Issue(validRequest())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if resp.Token == "" {
		t.Error("expected non-empty token")
	}
	if resp.ExpiresAt.Before(time.Now()) {
		t.Errorf("ExpiresAt is in the past: %v", resp.ExpiresAt)
	}
}

func TestManager_Issue_MissingAgentID_ReturnsError(t *testing.T) {
	m := newManager(t, "", "eami-gateway")
	req := validRequest()
	req.AgentID = ""
	_, err := m.Issue(req)
	if err == nil {
		t.Fatal("expected error for missing agent_id, got nil")
	}
}

func TestManager_Issue_TTLClamped_Min(t *testing.T) {
	m := newManager(t, "", "eami-gateway")
	req := validRequest()
	req.TTLSeconds = 1 // below min (60)
	resp, err := m.Issue(req)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// ExpiresAt should reflect at least 60s TTL, not 1s.
	minExpiry := time.Now().Add(55 * time.Second) // small tolerance
	if resp.ExpiresAt.Before(minExpiry) {
		t.Errorf("TTL not clamped to min: ExpiresAt=%v want>=%v", resp.ExpiresAt, minExpiry)
	}
}

func TestManager_Issue_TTLClamped_Max(t *testing.T) {
	m := newManager(t, "", "eami-gateway")
	req := validRequest()
	req.TTLSeconds = 999999 // above max (14400)
	resp, err := m.Issue(req)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	maxExpiry := time.Now().Add(14400*time.Second + 5*time.Second) // tolerance
	if resp.ExpiresAt.After(maxExpiry) {
		t.Errorf("TTL not clamped to max: ExpiresAt=%v want<=%v", resp.ExpiresAt, maxExpiry)
	}
}

func TestManager_Issue_ClaimsRoundTrip(t *testing.T) {
	m := newManager(t, "", "eami-gateway")
	req := validRequest()
	resp, err := m.Issue(req)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := m.Validate(resp.Token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if claims.Subject != req.AgentID {
		t.Errorf("Subject: got %q want %q", claims.Subject, req.AgentID)
	}
	if claims.Scope != req.Scope {
		t.Errorf("Scope: got %q want %q", claims.Scope, req.Scope)
	}
	if claims.Model != req.Model {
		t.Errorf("Model: got %q want %q", claims.Model, req.Model)
	}
	if claims.Owner != req.Owner {
		t.Errorf("Owner: got %q want %q", claims.Owner, req.Owner)
	}
	if claims.RiskTier != req.RiskTier {
		t.Errorf("RiskTier: got %q want %q", claims.RiskTier, req.RiskTier)
	}
}

// ─── Validate — happy path ────────────────────────────────────────────────────

func TestManager_Validate_ValidToken_ReturnsClaims(t *testing.T) {
	m := newManager(t, "", "eami-gateway")
	resp, _ := m.Issue(validRequest())
	claims, err := m.Validate(resp.Token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if claims == nil {
		t.Fatal("expected non-nil claims")
	}
}

// ─── Validate — error cases ───────────────────────────────────────────────────

func TestManager_Validate_ExpiredToken_ReturnsError(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "gateway.key")
	m := newManager(t, keyPath, "eami-gateway")
	pk := readPrivateKey(t, keyPath)

	// Craft a token that expired 10 minutes ago.
	now := time.Now().UTC()
	exp := now.Add(-10 * time.Minute)
	claims := &identity.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "expired-agent",
			Issuer:    "eami-gateway",
			Audience:  jwt.ClaimStrings{"eami-gateway"},
			IssuedAt:  jwt.NewNumericDate(now.Add(-20 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        "test-jti-expired",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(pk)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	_, err = m.Validate(signed)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestManager_Validate_WrongAudience_ReturnsError(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "gateway.key")
	m := newManager(t, keyPath, "eami-gateway")
	pk := readPrivateKey(t, keyPath)

	now := time.Now().UTC()
	claims := &identity.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "test-agent",
			Issuer:    "eami-gateway",
			Audience:  jwt.ClaimStrings{"some-other-service"}, // wrong aud
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
			ID:        "test-jti-wrong-aud",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, _ := tok.SignedString(pk)

	_, err := m.Validate(signed)
	if err == nil {
		t.Fatal("expected error for wrong audience, got nil")
	}
}

func TestManager_Validate_AlgorithmConfusion_HMAC_ReturnsError(t *testing.T) {
	m := newManager(t, "", "eami-gateway")

	// Attempt HMAC algorithm confusion: sign with HS256 using the empty string.
	// The gateway's key function must reject non-RSA methods.
	claims := jwt.MapClaims{
		"sub": "evil-agent",
		"aud": "eami-gateway",
		"iss": "eami-gateway",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
		"jti": "evil-jti",
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := tok.SignedString([]byte(""))

	_, err := m.Validate(signed)
	if err == nil {
		t.Fatal("expected error for HMAC token, got nil (algorithm confusion unguarded)")
	}
	if !strings.Contains(err.Error(), "signing method") && !strings.Contains(err.Error(), "invalid") {
		t.Logf("error: %v", err) // log for diagnosis but don't fail on message format
	}
}

func TestManager_Validate_RevokedToken_ReturnsError(t *testing.T) {
	m := newManager(t, "", "eami-gateway")
	resp, _ := m.Issue(validRequest())

	// Validate before revocation — must succeed.
	claims, err := m.Validate(resp.Token)
	if err != nil {
		t.Fatalf("pre-revocation validate: %v", err)
	}
	jti := claims.ID

	// Revoke the token.
	m.Revoke(jti)

	// Validate after revocation — must fail.
	_, err = m.Validate(resp.Token)
	if err == nil {
		t.Fatal("expected error for revoked token, got nil")
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Errorf("expected 'revoked' in error message, got: %v", err)
	}
}

func TestManager_Validate_MalformedToken_ReturnsError(t *testing.T) {
	m := newManager(t, "", "eami-gateway")
	_, err := m.Validate("not.a.jwt")
	if err == nil {
		t.Fatal("expected error for malformed token, got nil")
	}
}

// ─── SECURITY-FAILING TESTS ───────────────────────────────────────────────────
// These tests document security requirements that are NOT yet enforced.
// They are expected to fail until the linked tasks are resolved.
// DO NOT remove or skip these tests — they are the acceptance criteria.

// TestManager_Validate_WrongIssuer_ReturnsError verifies that a token carrying
// an unexpected issuer is rejected. Linked: JWT-001 / TASK-053.
//
// CURRENTLY FAILING: Validate() does not call jwt.WithIssuer(), so any
// issuer is accepted as long as signature and audience are valid.
func TestManager_Validate_WrongIssuer_ReturnsError(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "gateway.key")
	m := newManager(t, keyPath, "eami-gateway")
	pk := readPrivateKey(t, keyPath)

	now := time.Now().UTC()
	claims := &identity.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "test-agent",
			Issuer:    "evil-service",            // not "eami-gateway"
			Audience:  jwt.ClaimStrings{"eami-gateway"},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
			ID:        "test-jti-evil-issuer",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(pk)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// WANT: error — wrong issuer.
	// GOT (until TASK-053 is fixed): nil — issuer not validated.
	_, err = m.Validate(signed)
	if err == nil {
		t.Fatal(
			"SECURITY-FAILING [JWT-001/TASK-053]: " +
				"Validate() accepted a token with issuer='evil-service'. " +
				"Fix: add jwt.WithIssuer(\"eami-gateway\") to ParseWithClaims call in tokens.go.",
		)
	}
}

// TestManager_Validate_RevokedToken_SurvivesRestart verifies that revocations
// persist across Manager restarts (i.e., are written to the DB). Linked: JWT-002 / TASK-052.
//
// CURRENTLY FAILING: Revoke() only updates the in-memory map. A new Manager
// instance (same key file) has no knowledge of prior revocations.
func TestManager_Validate_RevokedToken_SurvivesRestart(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "gateway.key")

	// Manager A issues and revokes a token.
	mA := newManager(t, keyPath, "eami-gateway")
	resp, err := mA.Issue(validRequest())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claimsA, err := mA.Validate(resp.Token)
	if err != nil {
		t.Fatalf("pre-revocation Validate: %v", err)
	}
	mA.Revoke(claimsA.ID)

	// Simulate restart: new Manager B with the same key file.
	// It should know about the prior revocation (by loading from DB).
	mB := newManager(t, keyPath, "eami-gateway")

	// WANT: error — token was revoked before restart.
	// GOT (until TASK-052 is fixed): nil — mB has no revocation list.
	_, err = mB.Validate(resp.Token)
	if err == nil {
		t.Fatal(
			"SECURITY-FAILING [JWT-002/TASK-052]: " +
				"Revoked token was accepted by a new Manager instance (simulated restart). " +
				"Fix: write to revoked_ai_tokens on Revoke(); load from DB in NewManager().",
		)
	}
}
