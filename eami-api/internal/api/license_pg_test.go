// license_pg_test.go -- eami-api/internal/api
//
// Real-Postgres integration tests for B-157's Brief 1 (built as B-169):
// license upload/read (POST/GET /v1/settings/license), the
// requireModuleLicensed middleware gating Module 1/Discovery routes, the
// AC4 adversarial forgery proof at the real HTTP layer, and AC5's RBAC x
// Licensing composition proof. Mirrors tools_redaction_pg_test.go's own
// structure exactly (same t.Cleanup-only pool-lifecycle convention,
// CLAUDE.md's mandatory pattern).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestLicense_RealDB -v
package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/license"
	"github.com/eami/api/internal/store"
)

// genuineTestVendorPrivateKeyPEM matches eami-api/internal/license's own
// embedded public key -- duplicated from that package's own _test.go
// (never exported from the production package itself, which contains no
// signing capability of any kind -- see its doc comment) so this file can
// construct genuinely, validly-signed test licenses. Same disclosed-test-
// key scope as everywhere else this constant appears in this session's work.
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

func signTestLicenseWithKey(t *testing.T, key *rsa.PrivateKey, orgID string, modules []string, validUntil time.Time) string {
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
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign test license: %v", err)
	}
	return signed
}

func signGenuineTestLicense(t *testing.T, orgID string, modules []string) string {
	t.Helper()
	return signTestLicenseWithKey(t, genuineTestVendorKey(t), orgID, modules, time.Now().Add(365*24*time.Hour))
}

func newLicenseTestServer(t *testing.T, orgSlug string) (*httptest.Server, *pgxpool.Pool, string, string) {
	t.Helper()
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, orgSlug)
	userID := seedTestUser(t, ctx, pool, orgID)

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@"+orgSlug+".test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	return ts, pool, orgID.String(), token
}

func doJSONLicense(t *testing.T, ts *httptest.Server, token, method, path string, body any) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, ts.URL+path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// ─── AC1: real upload + verify + persist ───────────────────────────────────

func TestLicense_RealDB_UploadGenuineLicense_PersistsAndReportsActive(t *testing.T) {
	ts, pool, orgID, token := newLicenseTestServer(t, "lic-upload")
	raw := signGenuineTestLicense(t, orgID, []string{"discovery", "gateway"})

	resp := doJSONLicense(t, ts, token, http.MethodPost, "/v1/settings/license", map[string]any{"raw_license": raw})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		var body bytes.Buffer
		_, _ = body.ReadFrom(resp.Body)
		t.Fatalf("upload: status = %d, want 201: %s", resp.StatusCode, body.String())
	}
	var created api.LicenseResp
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Status != "active" {
		t.Errorf("Status = %q, want active", created.Status)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM licenses WHERE org_id = $1`, orgID).Scan(&count); err != nil {
		t.Fatalf("count licenses: %v", err)
	}
	if count != 1 {
		t.Errorf("licenses row count = %d, want 1", count)
	}

	getResp := doJSONLicense(t, ts, token, http.MethodGet, "/v1/settings/license", nil)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get: status = %d, want 200", getResp.StatusCode)
	}
	var got api.LicenseResp
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.Status != "active" || len(got.Modules) != 2 {
		t.Errorf("GET license = %+v, want active with 2 modules", got)
	}
}

// TestLicense_RealDB_GetWithNoUpload_404 proves an org that never uploaded
// a license gets a clean 404, not a fabricated empty license.
func TestLicense_RealDB_GetWithNoUpload_404(t *testing.T) {
	ts, _, _, token := newLicenseTestServer(t, "lic-none")
	resp := doJSONLicense(t, ts, token, http.MethodGet, "/v1/settings/license", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// ─── AC4: adversarial forgery proof at the real HTTP layer ─────────────────

// TestLicense_RealDB_ForgedWithAttackerOwnKey_RejectedNeverPersisted is
// AC4's central proof at the HTTP+DB layer: an attacker with their own,
// entirely real, valid RSA keypair (exactly what's available to anyone
// with a Go toolchain on the appliance) signs a license granting every
// module. The real upload endpoint must reject it AND never persist it.
func TestLicense_RealDB_ForgedWithAttackerOwnKey_RejectedNeverPersisted(t *testing.T) {
	ts, pool, orgID, token := newLicenseTestServer(t, "lic-forge")

	attackerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate attacker key: %v", err)
	}
	forged := signTestLicenseWithKey(t, attackerKey, orgID, []string{"discovery", "gateway", "ai_infrastructure"}, time.Now().Add(365*24*time.Hour))

	resp := doJSONLicense(t, ts, token, http.MethodPost, "/v1/settings/license", map[string]any{"raw_license": forged})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		var body bytes.Buffer
		_, _ = body.ReadFrom(resp.Body)
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, body.String())
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM licenses WHERE org_id = $1`, orgID).Scan(&count); err != nil {
		t.Fatalf("count licenses: %v", err)
	}
	if count != 0 {
		t.Errorf("licenses row count = %d, want 0 -- a forged license was persisted", count)
	}
}

// TestLicense_RealDB_WrongOrgLicense_Rejected proves a GENUINELY,
// validly-signed license (real vendor key, real signature) issued for a
// DIFFERENT org's org_id is still rejected -- the signature alone being
// valid isn't sufficient; it must also be issued for the uploading org.
func TestLicense_RealDB_WrongOrgLicense_Rejected(t *testing.T) {
	ts, pool, orgID, token := newLicenseTestServer(t, "lic-wrongorg")
	otherOrgLicense := signGenuineTestLicense(t, "11111111-1111-1111-1111-111111111111", []string{"gateway"})

	resp := doJSONLicense(t, ts, token, http.MethodPost, "/v1/settings/license", map[string]any{"raw_license": otherOrgLicense})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM licenses WHERE org_id = $1`, orgID).Scan(&count); err != nil {
		t.Fatalf("count licenses: %v", err)
	}
	if count != 0 {
		t.Errorf("licenses row count = %d, want 0", count)
	}
}

func TestLicense_RealDB_UnrecognizedModule_Rejected(t *testing.T) {
	ts, _, orgID, token := newLicenseTestServer(t, "lic-badmodule")
	raw := signGenuineTestLicense(t, orgID, []string{"discovery", "quantum_computing"})

	resp := doJSONLicense(t, ts, token, http.MethodPost, "/v1/settings/license", map[string]any{"raw_license": raw})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestLicense_RealDB_DirectDBRowWithMismatchedOrgID_Rejected is the
// CRITICAL security review finding's own regression proof (this brief):
// a licenses row whose org_id column does NOT match the org_id signed
// inside its own raw_license JWT must never grant entitlement. Simulates
// exactly the adversary capability this brief's own threat model grants
// (full Postgres access on one's own appliance) -- a genuinely, validly
// signed license for a DIFFERENT org, direct-DB-inserted with THIS org's
// org_id column, bypassing UploadLicense's own cross-org check entirely.
// requireModuleLicensed must independently re-derive the binding from the
// claims themselves (not trust the row's org_id column) and reject it.
func TestLicense_RealDB_DirectDBRowWithMismatchedOrgID_Rejected(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	victimOrgID := seedTestOrg(t, ctx, pool, "lic-orgbind-victim")
	attackerOrgID := seedTestOrg(t, ctx, pool, "lic-orgbind-attacker")
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	attackerAdminID := seedTestUser(t, ctx, pool, attackerOrgID)
	attackerToken, _, err := authSvc.IssueAccessToken(attackerAdminID, attackerOrgID, "admin@lic-orgbind-attacker.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	// Genuinely, validly signed -- for the VICTIM org, never the attacker's.
	victimsRealLicense := signGenuineTestLicense(t, victimOrgID.String(), []string{"discovery", "gateway"})

	// Direct DB insert with org_id relabeled to the attacker's own org --
	// exactly the adversary capability granted (full Postgres access on
	// one's own appliance), bypassing UploadLicense's own cross-org check
	// entirely (which would have rejected this at the HTTP layer).
	if _, err := pool.Exec(ctx,
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW(), $4)`,
		attackerOrgID, victimsRealLicense, []string{"discovery", "gateway"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("insert mismatched-org license row: %v", err)
	}

	resp := doJSONLicense(t, ts, attackerToken, http.MethodGet, "/v1/endpoints", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		var body bytes.Buffer
		_, _ = body.ReadFrom(resp.Body)
		t.Fatalf("status = %d, want 403 -- a license genuinely signed for a DIFFERENT org was accepted via a mismatched org_id column: %s", resp.StatusCode, body.String())
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	if errBody["code"] != "module_not_licensed" {
		t.Errorf("error code = %v, want module_not_licensed", errBody["code"])
	}
}

// ─── AC3: Module 1/Discovery HTTP gate, closing the curl-bypass gap ────────

func TestLicense_RealDB_DiscoveryEndpoint_BlockedWithoutLicense(t *testing.T) {
	ts, _, _, token := newLicenseTestServer(t, "lic-disc-blocked")

	resp := doJSONLicense(t, ts, token, http.MethodGet, "/v1/endpoints", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		var body bytes.Buffer
		_, _ = body.ReadFrom(resp.Body)
		t.Fatalf("status = %d, want 403: %s", resp.StatusCode, body.String())
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	if errBody["code"] != "module_not_licensed" {
		t.Errorf("error code = %v, want module_not_licensed", errBody["code"])
	}
}

func TestLicense_RealDB_DiscoveryEndpoint_AllowedWithLicense(t *testing.T) {
	ts, _, orgID, token := newLicenseTestServer(t, "lic-disc-allowed")
	raw := signGenuineTestLicense(t, orgID, []string{"discovery"})
	uploadResp := doJSONLicense(t, ts, token, http.MethodPost, "/v1/settings/license", map[string]any{"raw_license": raw})
	uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusCreated {
		t.Fatalf("upload license: status = %d", uploadResp.StatusCode)
	}

	resp := doJSONLicense(t, ts, token, http.MethodGet, "/v1/endpoints", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var body bytes.Buffer
		_, _ = body.ReadFrom(resp.Body)
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body.String())
	}
}

// TestLicense_RealDB_DiscoverEndpointsRoute_AlsoGated proves the second
// route-pair under the same gate (/v1/discover/endpoints) behaves
// identically -- not just /v1/endpoints in isolation.
func TestLicense_RealDB_DiscoverEndpointsRoute_AlsoGated(t *testing.T) {
	ts, _, _, token := newLicenseTestServer(t, "lic-disc2-blocked")
	resp := doJSONLicense(t, ts, token, http.MethodGet, "/v1/discover/endpoints", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// ─── AC5: RBAC x Licensing composition ─────────────────────────────────────

// TestLicense_RealDB_RBACAndLicensing_ComposeIndependently is AC5's
// central proof: a valid role with an invalid (missing) license is
// blocked BY LICENSING, and an invalid role with a valid license is
// blocked BY RBAC -- two genuinely independent, AND-ed conditions, never
// one substituting for the other.
func TestLicense_RealDB_RBACAndLicensing_ComposeIndependently(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "lic-rbac")
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// A user with role="admin" (a role /v1/endpoints DOES allow) -- used
	// to upload a real license further below.
	adminID := seedTestUser(t, ctx, pool, orgID)
	adminToken, _, err := authSvc.IssueAccessToken(adminID, orgID, "admin@lic-rbac.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken(admin): %v", err)
	}
	// A user with role="viewer" -- /v1/endpoints DOES allow this role.
	viewerID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, org_id, email, role) VALUES ($1, $2, $3, 'viewer')`,
		viewerID, orgID, "viewer@lic-rbac.test"); err != nil {
		t.Fatalf("seed viewer user: %v", err)
	}
	viewerToken, _, err := authSvc.IssueAccessToken(viewerID, orgID, "viewer@lic-rbac.test", "viewer")
	if err != nil {
		t.Fatalf("IssueAccessToken(viewer): %v", err)
	}
	// A user with role="approver" -- /v1/endpoints does NOT allow this
	// role (its own requireRole list is admin/operator/viewer only).
	approverID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, org_id, email, role) VALUES ($1, $2, $3, 'approver')`,
		approverID, orgID, "approver@lic-rbac.test"); err != nil {
		t.Fatalf("seed approver user: %v", err)
	}
	approverToken, _, err := authSvc.IssueAccessToken(approverID, orgID, "approver@lic-rbac.test", "approver")
	if err != nil {
		t.Fatalf("IssueAccessToken(approver): %v", err)
	}

	// Case 1: valid role (viewer), NO license -- blocked by LICENSING.
	resp1 := doJSONLicense(t, ts, viewerToken, http.MethodGet, "/v1/endpoints", nil)
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusForbidden {
		t.Fatalf("case 1 (valid role, no license): status = %d, want 403", resp1.StatusCode)
	}
	var body1 map[string]any
	_ = json.NewDecoder(resp1.Body).Decode(&body1)
	if body1["code"] != "module_not_licensed" {
		t.Errorf("case 1: error code = %v, want module_not_licensed (blocked by LICENSING, not RBAC)", body1["code"])
	}

	// Upload a real, valid discovery license for this org (as the admin).
	raw := signGenuineTestLicense(t, orgID.String(), []string{"discovery"})
	uploadResp := doJSONLicense(t, ts, adminToken, http.MethodPost, "/v1/settings/license", map[string]any{"raw_license": raw})
	uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusCreated {
		t.Fatalf("upload license: status = %d", uploadResp.StatusCode)
	}

	// Case 2: valid role (viewer) + valid license -- allowed (both checks pass).
	resp2 := doJSONLicense(t, ts, viewerToken, http.MethodGet, "/v1/endpoints", nil)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		var body bytes.Buffer
		_, _ = body.ReadFrom(resp2.Body)
		t.Fatalf("case 2 (valid role, valid license): status = %d, want 200: %s", resp2.StatusCode, body.String())
	}

	// Case 3: invalid role (approver) + valid license -- blocked by RBAC.
	resp3 := doJSONLicense(t, ts, approverToken, http.MethodGet, "/v1/endpoints", nil)
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusForbidden {
		t.Fatalf("case 3 (invalid role, valid license): status = %d, want 403", resp3.StatusCode)
	}
	var body3 map[string]any
	_ = json.NewDecoder(resp3.Body).Decode(&body3)
	if body3["code"] != "forbidden" {
		t.Errorf("case 3: error code = %v, want forbidden (blocked by RBAC, not licensing)", body3["code"])
	}
}
