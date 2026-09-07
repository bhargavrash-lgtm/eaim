// dispatcher_license_test.go -- cmd/gateway
//
// Real-Postgres integration tests for B-157's Brief 1 (built as B-169):
// Dispatcher.Dispatch's licensing gate for Module 2/Gateway. Proves the
// gate blocks an unlicensed org before tool resolution, policy
// evaluation, or the Deny/Escalate/Allow switch is ever reached (AC2),
// and that a properly-licensed org sees zero behavioral change (AC6).
//
// Uses a REAL license.Store against the real test pool (not
// alwaysLicensedChecker{}, dispatcher_test.go's shared fake for every
// OTHER test in this package) -- this file is specifically about proving
// the real Store + real Verify + real Postgres round trip works, not
// just the gate's own call-site wiring.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./cmd/gateway/... -run TestDispatch_License -v
package main

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/eami/gateway/internal/aiprovider"
	"github.com/eami/gateway/internal/approval"
	"github.com/eami/gateway/internal/audit"
	"github.com/eami/gateway/internal/episode"
	"github.com/eami/gateway/internal/license"
	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/toolrouter"
	policy "github.com/eami/policy"
)

// genuineTestVendorPrivateKeyPEM is the PRIVATE half matching internal/
// license's own embedded public key -- exists ONLY here (never compiled
// into any shipped binary) so this file can insert genuinely, validly-
// signed license rows to test the real Store+Verify+Postgres round trip
// against. Duplicated from internal/license/license_test.go's identical
// constant rather than exported from the production package itself,
// which deliberately contains no signing capability of any kind, in
// tests or otherwise (see that package's own doc comment) -- a
// verification-only package that could sign things would undercut the
// exact property this brief exists to prove. The same disclosed-test-key
// scope as everywhere else this constant appears: not the real
// production vendor key.
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

// signTestLicense builds and signs a real license JWT.
func signTestLicense(t *testing.T, orgID string, modules []string, validUntil time.Time) string {
	t.Helper()
	now := time.Now().UTC()
	claims := license.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   orgID,
			Issuer:    license.Issuer,
			Audience:  jwt.ClaimStrings{license.Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Hour)),
			ExpiresAt: jwt.NewNumericDate(validUntil),
		},
		Modules: modules,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(genuineTestVendorKey(t))
	if err != nil {
		t.Fatalf("sign test license: %v", err)
	}
	return signed
}

// newDispatcherTestEnvRealLicense is newDispatcherTestEnv (dispatcher_test.go)
// with one difference: it wires a REAL license.Store (against the real
// test pool) instead of alwaysLicensedChecker{} -- this file's whole
// point is proving that real component, not just the gate's call-site.
func newDispatcherTestEnvRealLicense(t *testing.T, action string) *dispatcherTestEnv {
	t.Helper()
	env := newMainTestEnv(t)
	auditWriter, err := audit.NewWriter(context.Background(), env.pool)
	if err != nil {
		t.Fatalf("audit.NewWriter: %v", err)
	}
	agentID, agentName := env.insertAgent(t)

	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(downstream.Close)
	fwd := proxy.New(proxy.Config{DownstreamURL: downstream.URL}, downstream.Client())

	toolRouter := toolrouter.New(env.pool, nil)
	aiProviderRouter := aiprovider.New(env.pool, nil, map[string]aiprovider.Adapter{})
	episodeRecorder := episode.New(env.pool)
	licenseStore := license.New(env.pool)
	// The REAL licenseStore, not nil -- this file is specifically where
	// the escalation-resume re-check (code review finding, this brief)
	// needs a real Store to prove anything against.
	approvalRouter := approval.New(env.pool, fwd, 5*time.Second, "", "", toolRouter, aiProviderRouter, licenseStore)
	runCtx, cancel := context.WithCancel(context.Background())
	go approvalRouter.Run(runCtx)
	t.Cleanup(cancel)

	dispatcher := NewDispatcher(
		toolRouter, aiProviderRouter, licenseStore, staticEvaluatorSource{ev: &fakeEvaluator{action: action}},
		auditWriter, episodeRecorder, approvalRouter, fwd,
		"", "", 5*time.Second,
	)
	return &dispatcherTestEnv{env: env, dispatcher: dispatcher, downstream: downstream, agentID: agentID, agentName: agentName}
}

// TestDispatch_NoLicenseAtAll_BlockedBeforeAnythingElse (AC2) proves the
// gate blocks an org with NO licenses row whatsoever -- the common
// "never purchased/never uploaded" case -- before tool resolution,
// policy evaluation (deliberately ActionAllow here: if the gate didn't
// pre-empt everything, this dispatch would otherwise succeed), or any
// audit/episode/token-usage side effect beyond the rejection's own.
func TestDispatch_NoLicenseAtAll_BlockedBeforeAnythingElse(t *testing.T) {
	env := newDispatcherTestEnvRealLicense(t, policy.ActionAllow)

	result, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("some-tool"))
	if err == nil {
		t.Fatal("expected a clean rejection for an org with no license, got nil error")
	}
	if result != nil {
		t.Errorf("expected nil result, got %s", result)
	}
	if !strings.Contains(err.Error(), "not licensed for the Gateway module") {
		t.Errorf("expected a clear licensing rejection message, got: %v", err)
	}

	var decision string
	var parameters []byte
	if err := env.env.pool.QueryRow(context.Background(),
		`SELECT decision, parameters FROM audit_log WHERE org_id = $1 AND tool_name = 'some-tool'`,
		env.env.orgID).Scan(&decision, &parameters); err != nil {
		t.Fatalf("read audit_log row: %v", err)
	}
	if decision != "denied" {
		t.Errorf("audit_log decision = %q, want denied", decision)
	}
	if len(parameters) > 0 && string(parameters) != "null" {
		t.Errorf("audit_log parameters = %s, want empty/absent -- no excess raw content for an org that was never even resolved a connector", parameters)
	}
}

// TestDispatch_LicenseMissingGatewayModule_Blocked (AC2) proves a REAL,
// validly-signed, currently-valid license that simply doesn't include
// "gateway" (e.g. an org that only purchased Discovery) is correctly
// blocked -- not just "no license at all."
func TestDispatch_LicenseMissingGatewayModule_Blocked(t *testing.T) {
	env := newDispatcherTestEnvRealLicense(t, policy.ActionAllow)

	raw := signTestLicense(t, env.env.orgID.String(), []string{"discovery"}, time.Now().Add(365*24*time.Hour))
	if _, err := env.env.pool.Exec(context.Background(),
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW(), $4)`,
		env.env.orgID, raw, []string{"discovery"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("insert license: %v", err)
	}

	_, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("some-tool"))
	if err == nil {
		t.Fatal("expected a clean rejection for a license that doesn't include gateway, got nil error")
	}
	if !strings.Contains(err.Error(), "not licensed for the Gateway module") {
		t.Errorf("expected a clear licensing rejection message, got: %v", err)
	}
}

// TestDispatch_LicenseIncludesGateway_DispatchesNormally (AC6) is the
// central no-regression proof: a real, validly-signed license that DOES
// include "gateway" behaves completely unaffected by this brief -- the
// call reaches the real Allow branch and genuinely dispatches, exactly
// as it would have before this brief existed.
func TestDispatch_LicenseIncludesGateway_DispatchesNormally(t *testing.T) {
	env := newDispatcherTestEnvRealLicense(t, policy.ActionAllow)

	raw := signTestLicense(t, env.env.orgID.String(), []string{"discovery", "gateway"}, time.Now().Add(365*24*time.Hour))
	if _, err := env.env.pool.Exec(context.Background(),
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW(), $4)`,
		env.env.orgID, raw, []string{"discovery", "gateway"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("insert license: %v", err)
	}

	result, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("some-tool"))
	if err != nil {
		t.Fatalf("expected a real, properly-licensed dispatch to succeed, got: %v", err)
	}
	if result == nil {
		t.Error("expected a real result from the static downstream, got nil")
	}
}

// TestDispatch_ExpiredLicense_BlockedEvenThoughRowExists (AC2/AC6
// boundary) proves the gate re-verifies the license fresh every call --
// a license row that exists but has genuinely expired (per its own real
// JWT exp claim, not just the denormalized valid_until column) is
// treated identically to no license at all.
func TestDispatch_ExpiredLicense_BlockedEvenThoughRowExists(t *testing.T) {
	env := newDispatcherTestEnvRealLicense(t, policy.ActionAllow)

	raw := signTestLicense(t, env.env.orgID.String(), []string{"gateway"}, time.Now().Add(-time.Hour)) // expired 1h ago
	if _, err := env.env.pool.Exec(context.Background(),
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW() - interval '2 days', $4)`,
		env.env.orgID, raw, []string{"gateway"}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("insert expired license: %v", err)
	}

	_, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("some-tool"))
	if err == nil {
		t.Fatal("expected a clean rejection for an expired license, got nil error")
	}
	if !strings.Contains(err.Error(), "not licensed for the Gateway module") {
		t.Errorf("expected a clear licensing rejection message, got: %v", err)
	}
}

// TestDispatch_MultipleLicenseRows_UsesMostRecent (append-only design
// proof) inserts an OLDER license without "gateway" followed by a NEWER
// one that includes it -- proves the Store resolves the most recently
// uploaded row, not an arbitrary or the first one, matching the
// licenses table's own append-only "a renewal is a new row" design.
func TestDispatch_MultipleLicenseRows_UsesMostRecent(t *testing.T) {
	env := newDispatcherTestEnvRealLicense(t, policy.ActionAllow)

	oldRaw := signTestLicense(t, env.env.orgID.String(), []string{"discovery"}, time.Now().Add(365*24*time.Hour))
	if _, err := env.env.pool.Exec(context.Background(),
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until, created_at) VALUES ($1, $2, $3, NOW(), $4, NOW() - interval '1 day')`,
		env.env.orgID, oldRaw, []string{"discovery"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("insert old license: %v", err)
	}
	newRaw := signTestLicense(t, env.env.orgID.String(), []string{"discovery", "gateway"}, time.Now().Add(365*24*time.Hour))
	if _, err := env.env.pool.Exec(context.Background(),
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW(), $4)`,
		env.env.orgID, newRaw, []string{"discovery", "gateway"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("insert new license: %v", err)
	}

	if _, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("some-tool")); err != nil {
		t.Fatalf("expected the most recent (gateway-including) license to apply, got: %v", err)
	}
}

// TestDispatch_LicenseRowOrgMismatch_Blocked is the CRITICAL security
// review finding's own regression proof on the gateway side (mirrors
// license_pg_test.go's identical proof at eami-api's HTTP layer): a
// licenses row whose org_id column does not match the org_id signed
// inside its own raw_license must never grant entitlement, even though
// the signature itself is genuinely valid. Simulates the adversary
// capability this brief's own threat model grants (full Postgres access
// on one's own appliance) directly against Store.ModuleLicensed.
func TestDispatch_LicenseRowOrgMismatch_Blocked(t *testing.T) {
	env := newDispatcherTestEnvRealLicense(t, policy.ActionAllow)

	otherOrgID := "22222222-2222-2222-2222-222222222222"
	// Genuinely signed for a DIFFERENT org (otherOrgID), then direct-DB
	// inserted under THIS test's own org -- exactly what UploadLicense's
	// cross-org check exists to prevent at write time, bypassed here to
	// prove the read path independently re-derives the same binding.
	raw := signTestLicense(t, otherOrgID, []string{"gateway"}, time.Now().Add(365*24*time.Hour))
	if _, err := env.env.pool.Exec(context.Background(),
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW(), $4)`,
		env.env.orgID, raw, []string{"gateway"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("insert mismatched-org license row: %v", err)
	}

	_, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("some-tool"))
	if err == nil {
		t.Fatal("expected a clean rejection for a license row whose org_id doesn't match its own signed org_id, got nil error")
	}
	if !strings.Contains(err.Error(), "not licensed for the Gateway module") {
		t.Errorf("expected a clear licensing rejection message, got: %v", err)
	}
}

// TestDispatch_LicenseRevokedDuringApprovalHold_ResumeBlocked (code review
// finding, this brief) is the central proof for approval.Router's new
// licenseChecker re-check. Dispatcher.Dispatch's own gate only runs once,
// at escalation-SUBMIT time -- an org can be genuinely licensed then, pass
// the gate, and only have its license revoked/expire WHILE the request
// sits in the (potentially long) approval hold window. The real downstream
// call for an approved escalation happens in dispatchApproved, not by
// looping back through Dispatch(), so without its own re-check the
// resumed call would dispatch anyway despite the org no longer being
// licensed -- exactly the gap this test proves is now closed.
func TestDispatch_LicenseRevokedDuringApprovalHold_ResumeBlocked(t *testing.T) {
	env := newDispatcherTestEnvRealLicense(t, policy.ActionEscalate)

	raw := signTestLicense(t, env.env.orgID.String(), []string{"gateway"}, time.Now().Add(365*24*time.Hour))
	if _, err := env.env.pool.Exec(context.Background(),
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW(), $4)`,
		env.env.orgID, raw, []string{"gateway"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("insert license: %v", err)
	}

	type dispatchResult struct {
		result []byte
		err    error
	}
	resultCh := make(chan dispatchResult, 1)
	go func() {
		result, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("some-tool"))
		resultCh <- dispatchResult{result: result, err: err}
	}()

	approvalID := waitForPendingApproval(t, env.env.pool, env.env.orgID, 5*time.Second)

	// The org's license is revoked WHILE the request is genuinely pending
	// approval -- the exact TOCTOU window this fix closes. A real customer
	// scenario: a subscription lapses, or an admin uploads a new license
	// that no longer includes Gateway, minutes before an approver actually
	// clicks "approve" on a request submitted while the old license was
	// still valid.
	if _, err := env.env.pool.Exec(context.Background(), `DELETE FROM licenses WHERE org_id = $1`, env.env.orgID); err != nil {
		t.Fatalf("revoke license: %v", err)
	}

	decideTestApproval(t, env.env.pool, approvalID, "approved")

	select {
	case dr := <-resultCh:
		if dr.err == nil {
			t.Fatal("expected the resumed call to be blocked by the now-revoked license, got nil error")
		}
		if !strings.Contains(dr.err.Error(), "no longer licensed for the Gateway module") {
			t.Errorf("expected a clear licensing rejection message, got: %v", dr.err)
		}
		if dr.result != nil {
			t.Errorf("expected nil result alongside the rejection, got %s", dr.result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for Dispatch to return after approval decision")
	}

	var resumeOutcome string
	if err := env.env.pool.QueryRow(context.Background(),
		`SELECT resume_outcome FROM approval_requests WHERE id = $1`, approvalID).Scan(&resumeOutcome); err != nil {
		t.Fatalf("read resume_outcome: %v", err)
	}
	if resumeOutcome != "license_revoked" {
		t.Errorf("resume_outcome = %q, want license_revoked", resumeOutcome)
	}
}
