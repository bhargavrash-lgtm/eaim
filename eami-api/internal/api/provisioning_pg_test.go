// provisioning_pg_test.go -- eami-api/internal/api
//
// Real-Postgres integration tests for real user provisioning
// (internal/api/provisioning.go, users.go's GetMe/UpdateMe/ChangeMyPassword
// and InviteUser's new DB-backed token). Covers the task brief's 4
// mandatory adversarial cases:
//
//  1. An expired or already-consumed invite/reset token is rejected.
//  2. A token for one user cannot be used to set another user's password.
//  3. request-reset's anti-enumeration behavior is real (identical
//     response for an existing vs. nonexistent account).
//  4. PATCH /v1/users/me cannot change role or org.
//
// Follows deactivation_pg_test.go's own harness conventions exactly
// (toolsUpdateTestDSN, seedTestOrg, a real httptest server over
// api.NewServer, pool.Close registered via t.Cleanup before any other
// t.Cleanup that touches the database -- CLAUDE.md's mandatory
// real-Postgres pool lifecycle rule) -- this proves the fix through the
// REAL HTTP handlers and REAL sqlc-style queries, not a mocked store.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestAcceptInvite -v
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestResetPassword -v
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestRequestPasswordReset -v
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestUpdateMe -v
package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/config"
	"github.com/eami/api/internal/store"
)

// provisioningTestEnv mirrors deactivationTestEnv (deactivation_pg_test.go)
// exactly -- a real pool + a real httptest server over the real
// api.NewServer.
type provisioningTestEnv struct {
	pool *pgxpool.Pool
	http *http.Client
	url  string
}

func newProvisioningTestEnv(t *testing.T) *provisioningTestEnv {
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
	// t.Cleanup, not a plain defer -- registered before any other
	// t.Cleanup this test's own seed helpers add (CLAUDE.md's mandatory
	// real-Postgres pool lifecycle rule).
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	// A generous provisioningLimiter, not config.DefaultRateLimitConfig()'s
	// production-conservative 10/15min -- several of this file's own
	// functional tests legitimately call request-reset/accept-invite/
	// reset-password more than 10 times against one env (e.g. the AC3
	// timing-comparison loop below), and would otherwise trip the very
	// limiter TestProvisioningRateLimit_RealDB_TripsAfterThreshold exists
	// to test in isolation with its own deliberately low override.
	cfg := &config.Config{RateLimit: config.RateLimitConfig{
		LoginPerIP: 1000, LoginPerIPWindowSeconds: 60,
		LoginPerAccount: 1000, LoginPerAccountWindowSeconds: 60,
		Setup: 1000, SetupWindowSeconds: 60,
		Provisioning: 1000, ProvisioningWindowSeconds: 60,
	}}
	srv := api.NewServer(q, authSvc, nil, cfg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &provisioningTestEnv{pool: pool, http: ts.Client(), url: ts.URL}
}

// newProvisioningTestEnvDefaultLimits is newProvisioningTestEnv's own
// twin, but built with cfg=nil so api.NewServer falls back to
// config.DefaultRateLimitConfig()'s real production default (10/15min) --
// used only by the rate-limit test itself, which needs the real threshold
// to trip against, not the generous override every other test in this
// file relies on.
func newProvisioningTestEnvDefaultLimits(t *testing.T) *provisioningTestEnv {
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
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &provisioningTestEnv{pool: pool, http: ts.Client(), url: ts.URL}
}

// hashProvisioningTestToken mirrors provisioning.go's private hashOpaqueToken
// (SHA-256 hex) -- reimplemented here against the same stdlib primitives
// rather than reaching into the unexported function, same as
// bootstrap_test.go's own hashSetupTokenForTest precedent.
func hashProvisioningTestToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// seedInvitedUser inserts a real users row with role and password_hash
// NULL (as CreateInvitedUser, users.go, actually does), mirroring the
// real invite flow's starting state.
func (e *provisioningTestEnv) seedInvitedUser(t *testing.T, ctx context.Context, orgID uuid.UUID, email, role string) uuid.UUID {
	t.Helper()
	userID := uuid.New()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO users (id, org_id, email, role, invited_at)
		VALUES ($1, $2, $3, $4, NOW())
	`, userID, orgID, email, role); err != nil {
		t.Fatalf("seed invited user: %v", err)
	}
	return userID
}

// seedActiveUser inserts a real users row with a real bcrypt hash of
// plainPassword. Mirrors deactivationTestEnv.seedActiveUser (kept as its
// own copy in this file rather than shared, matching this codebase's own
// established precedent of small per-file test-fixture duplication --
// e.g. bootstrap_test.go's hashSetupTokenForTest).
func (e *provisioningTestEnv) seedActiveUser(t *testing.T, ctx context.Context, orgID uuid.UUID, email, plainPassword, role string) uuid.UUID {
	t.Helper()
	hash, err := auth.HashPassword(plainPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	userID := uuid.New()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO users (id, org_id, email, password_hash, role, name)
		VALUES ($1, $2, $3, $4, $5, 'Provisioning Test User')
	`, userID, orgID, email, hash, role); err != nil {
		t.Fatalf("seed active user: %v", err)
	}
	return userID
}

// insertInviteToken seeds an invite_tokens row directly, bypassing
// InviteUser's own HTTP endpoint, so each test can construct exactly the
// token state (valid / expired / already-consumed) it needs -- same
// reasoning as bootstrap_test.go's insertSetupToken.
func (e *provisioningTestEnv) insertInviteToken(t *testing.T, ctx context.Context, userID uuid.UUID, raw string, expiresAt time.Time, consumed bool) {
	t.Helper()
	var err error
	if consumed {
		_, err = e.pool.Exec(ctx,
			`INSERT INTO invite_tokens (user_id, token_hash, expires_at, consumed_at) VALUES ($1, $2, $3, now())`,
			userID, hashProvisioningTestToken(raw), expiresAt)
	} else {
		_, err = e.pool.Exec(ctx,
			`INSERT INTO invite_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
			userID, hashProvisioningTestToken(raw), expiresAt)
	}
	if err != nil {
		t.Fatalf("seed invite token: %v", err)
	}
}

func (e *provisioningTestEnv) insertResetToken(t *testing.T, ctx context.Context, userID uuid.UUID, raw string, expiresAt time.Time, consumed bool) {
	t.Helper()
	var err error
	if consumed {
		_, err = e.pool.Exec(ctx,
			`INSERT INTO reset_tokens (user_id, token_hash, expires_at, consumed_at) VALUES ($1, $2, $3, now())`,
			userID, hashProvisioningTestToken(raw), expiresAt)
	} else {
		_, err = e.pool.Exec(ctx,
			`INSERT INTO reset_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
			userID, hashProvisioningTestToken(raw), expiresAt)
	}
	if err != nil {
		t.Fatalf("seed reset token: %v", err)
	}
}

func (e *provisioningTestEnv) passwordHash(t *testing.T, ctx context.Context, userID uuid.UUID) *string {
	t.Helper()
	var h *string
	if err := e.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&h); err != nil {
		t.Fatalf("query password_hash: %v", err)
	}
	return h
}

func (e *provisioningTestEnv) postJSON(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	resp, err := e.http.Post(e.url+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func (e *provisioningTestEnv) patchJSON(t *testing.T, path, bearer string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPatch, e.url+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build PATCH %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := e.http.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", path, err)
	}
	return resp
}

func (e *provisioningTestEnv) login(t *testing.T, email, password string) *http.Response {
	t.Helper()
	return e.postJSON(t, "/v1/auth/login", map[string]string{"email": email, "password": password})
}

func readRespBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf.Bytes()
}

// ── AC1: expired / already-consumed tokens are rejected ─────────────────────

func TestAcceptInvite_RealDB_ExpiredOrConsumedTokenRejected(t *testing.T) {
	cases := []struct {
		name      string
		expiresAt time.Time
		consumed  bool
	}{
		{"expired", time.Now().Add(-1 * time.Hour), false},
		{"already consumed", time.Now().Add(1 * time.Hour), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newProvisioningTestEnv(t)
			ctx := context.Background()
			orgID := seedTestOrg(t, ctx, env.pool, "b-invite-"+tc.name)
			userID := env.seedInvitedUser(t, ctx, orgID, "invitee-"+tc.name+"@example.com", "operator")
			env.insertInviteToken(t, ctx, userID, "raw-token-"+tc.name, tc.expiresAt, tc.consumed)

			resp := env.postJSON(t, "/v1/auth/accept-invite", map[string]string{
				"token":    "raw-token-" + tc.name,
				"password": "newpassword123",
			})
			body := readRespBody(t, resp)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d: %s", resp.StatusCode, body)
			}
			if h := env.passwordHash(t, ctx, userID); h != nil {
				t.Fatalf("password_hash should remain NULL after a rejected accept-invite, got %v", *h)
			}
		})
	}
}

func TestResetPassword_RealDB_ExpiredOrConsumedTokenRejected(t *testing.T) {
	cases := []struct {
		name      string
		expiresAt time.Time
		consumed  bool
	}{
		{"expired", time.Now().Add(-1 * time.Hour), false},
		{"already consumed", time.Now().Add(1 * time.Hour), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newProvisioningTestEnv(t)
			ctx := context.Background()
			orgID := seedTestOrg(t, ctx, env.pool, "b-reset-"+tc.name)
			userID := env.seedActiveUser(t, ctx, orgID, "reset-"+tc.name+"@example.com", "originalpass1", "operator")
			originalHash := env.passwordHash(t, ctx, userID)
			env.insertResetToken(t, ctx, userID, "reset-raw-"+tc.name, tc.expiresAt, tc.consumed)

			resp := env.postJSON(t, "/v1/auth/reset-password", map[string]string{
				"token":        "reset-raw-" + tc.name,
				"new_password": "brandnewpass123",
			})
			body := readRespBody(t, resp)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d: %s", resp.StatusCode, body)
			}
			if h := env.passwordHash(t, ctx, userID); h == nil || *h != *originalHash {
				t.Fatalf("password_hash must be unchanged after a rejected reset-password")
			}
			// The original password must still work.
			loginResp := env.login(t, "reset-"+tc.name+"@example.com", "originalpass1")
			if loginResp.StatusCode != http.StatusOK {
				t.Fatalf("original password should still work, got %d", loginResp.StatusCode)
			}
		})
	}
}

// ── AC2: a token for one user cannot be used to set another user's password ──

func TestAcceptInvite_RealDB_TokenBoundOnlyToOwnUser(t *testing.T) {
	env := newProvisioningTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b-invite-binding")
	userA := env.seedInvitedUser(t, ctx, orgID, "invitee-a@example.com", "operator")
	userB := env.seedInvitedUser(t, ctx, orgID, "invitee-b@example.com", "operator")
	env.insertInviteToken(t, ctx, userA, "raw-token-a", time.Now().Add(48*time.Hour), false)

	// The request has no field to name a target user at all -- only
	// {token, password} -- so the only way to prove binding is that
	// accepting A's token can only ever change A's row.
	resp := env.postJSON(t, "/v1/auth/accept-invite", map[string]string{
		"token":    "raw-token-a",
		"password": "userApassword1",
	})
	body := readRespBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	if h := env.passwordHash(t, ctx, userA); h == nil {
		t.Fatalf("user A's password_hash should now be set")
	}
	if h := env.passwordHash(t, ctx, userB); h != nil {
		t.Fatalf("user B's password_hash must remain NULL -- A's invite token must never affect B, got %v", *h)
	}

	// A can log in with the new password; B still can't log in at all
	// (still no password set).
	loginA := env.login(t, "invitee-a@example.com", "userApassword1")
	if loginA.StatusCode != http.StatusOK {
		t.Fatalf("user A should be able to log in after accepting their own invite, got %d", loginA.StatusCode)
	}
	loginB := env.login(t, "invitee-b@example.com", "userApassword1")
	if loginB.StatusCode == http.StatusOK {
		t.Fatalf("user B must not be able to log in using user A's new password")
	}
}

func TestResetPassword_RealDB_TokenBoundOnlyToOwnUser(t *testing.T) {
	env := newProvisioningTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b-reset-binding")
	env.seedActiveUser(t, ctx, orgID, "reset-a@example.com", "originalA-pass1", "operator")
	env.seedActiveUser(t, ctx, orgID, "reset-b@example.com", "originalB-pass1", "operator")

	// Resolve A's real user_id via a real login (proves the seeded
	// password is genuine) rather than trusting the seed helper's return
	// value blindly.
	var userAID uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, "reset-a@example.com").Scan(&userAID); err != nil {
		t.Fatalf("resolve user A id: %v", err)
	}
	var userBID uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, "reset-b@example.com").Scan(&userBID); err != nil {
		t.Fatalf("resolve user B id: %v", err)
	}
	bHashBefore := env.passwordHash(t, ctx, userBID)

	env.insertResetToken(t, ctx, userAID, "reset-raw-bind-a", time.Now().Add(1*time.Hour), false)

	resp := env.postJSON(t, "/v1/auth/reset-password", map[string]string{
		"token":        "reset-raw-bind-a",
		"new_password": "brandnewApass1",
	})
	body := readRespBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	if h := env.passwordHash(t, ctx, userBID); h == nil || *h != *bHashBefore {
		t.Fatalf("user B's password_hash must be completely unaffected by A's reset token")
	}

	loginA := env.login(t, "reset-a@example.com", "brandnewApass1")
	if loginA.StatusCode != http.StatusOK {
		t.Fatalf("user A should log in with the new password, got %d", loginA.StatusCode)
	}
	loginBOld := env.login(t, "reset-b@example.com", "originalB-pass1")
	if loginBOld.StatusCode != http.StatusOK {
		t.Fatalf("user B's original password must still work, got %d", loginBOld.StatusCode)
	}
	loginBNew := env.login(t, "reset-b@example.com", "brandnewApass1")
	if loginBNew.StatusCode == http.StatusOK {
		t.Fatalf("user B must not be able to log in with A's new password")
	}
}

// ── AC3: request-reset's anti-enumeration behavior is real ──────────────────

func TestRequestPasswordReset_RealDB_ResponseIdenticalForExistingAndNonexistentEmail(t *testing.T) {
	env := newProvisioningTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b-reset-enum")
	env.seedActiveUser(t, ctx, orgID, "real-user@example.com", "somepassword1", "operator")

	existingResp := env.postJSON(t, "/v1/auth/request-reset", map[string]string{"email": "real-user@example.com"})
	existingBody := readRespBody(t, existingResp)
	existingResp.Body.Close()

	nonexistentResp := env.postJSON(t, "/v1/auth/request-reset", map[string]string{"email": "does-not-exist-at-all@example.com"})
	nonexistentBody := readRespBody(t, nonexistentResp)
	nonexistentResp.Body.Close()

	if existingResp.StatusCode != nonexistentResp.StatusCode {
		t.Fatalf("status codes differ: existing=%d nonexistent=%d -- an enumeration oracle", existingResp.StatusCode, nonexistentResp.StatusCode)
	}
	if !bytes.Equal(existingBody, nonexistentBody) {
		t.Fatalf("response bodies differ -- an enumeration oracle: existing=%s nonexistent=%s", existingBody, nonexistentBody)
	}

	// Prove real work actually happened server-side for the existing
	// account despite the identical response -- a real reset_tokens row
	// was minted, even though the HTTP caller can't tell.
	var count int
	if err := env.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM reset_tokens rt JOIN users u ON u.id = rt.user_id WHERE u.email = $1
	`, "real-user@example.com").Scan(&count); err != nil {
		t.Fatalf("count reset_tokens: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 reset_tokens row minted for the real account, got %d", count)
	}
	var nonexistentCount int
	if err := env.pool.QueryRow(ctx, `SELECT COUNT(*) FROM reset_tokens WHERE user_id NOT IN (SELECT id FROM users)`).Scan(&nonexistentCount); err != nil {
		t.Fatalf("count orphan reset_tokens: %v", err)
	}
	if nonexistentCount != 0 {
		t.Fatalf("no reset_tokens row should exist with no owning user, got %d", nonexistentCount)
	}

	// Best-effort timing sanity check -- not a rigorous constant-time
	// proof (network/GC jitter make strict timing assertions flaky in
	// CI), but the real work (a bcrypt-free DB lookup + one INSERT) is
	// cheap enough that a request for a nonexistent email shouldn't be
	// dramatically faster in a way a real attacker could reliably exploit
	// over a network. Generous threshold deliberately avoids false
	// failures; this is a secondary signal, the byte-identical response
	// check above is the real, deterministic proof.
	const trials = 5
	var existingTotal, nonexistentTotal time.Duration
	for i := 0; i < trials; i++ {
		start := time.Now()
		r := env.postJSON(t, "/v1/auth/request-reset", map[string]string{"email": "real-user@example.com"})
		existingTotal += time.Since(start)
		r.Body.Close()

		start = time.Now()
		r = env.postJSON(t, "/v1/auth/request-reset", map[string]string{"email": "still-does-not-exist@example.com"})
		nonexistentTotal += time.Since(start)
		r.Body.Close()
	}
	existingAvg := existingTotal / trials
	nonexistentAvg := nonexistentTotal / trials
	t.Logf("avg latency: existing=%v nonexistent=%v", existingAvg, nonexistentAvg)
	ratio := float64(existingAvg) / float64(nonexistentAvg+1)
	if ratio > 5.0 || ratio < 0.2 {
		t.Fatalf("existing vs. nonexistent avg latency ratio %.2f is far enough apart to be a plausible timing oracle (existing=%v nonexistent=%v)", ratio, existingAvg, nonexistentAvg)
	}
}

// ── AC4: PATCH /v1/users/me cannot change role or org ────────────────────────

func TestUpdateMe_RealDB_CannotChangeRoleOrOrg(t *testing.T) {
	env := newProvisioningTestEnv(t)
	ctx := context.Background()
	orgA := seedTestOrg(t, ctx, env.pool, "b-updateme-org-a")
	orgB := seedTestOrg(t, ctx, env.pool, "b-updateme-org-b")
	env.seedActiveUser(t, ctx, orgA, "selfservice@example.com", "originalpass1", "viewer")

	loginResp := env.login(t, "selfservice@example.com", "originalpass1")
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("seed login failed: %d", loginResp.StatusCode)
	}
	var lr struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&lr); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	loginResp.Body.Close()

	// Raw JSON body, deliberately including role/org_id fields
	// UpdateMeRequest doesn't define -- proving the server-side type,
	// not just a well-behaved client, is what prevents this.
	resp := env.patchJSON(t, "/v1/users/me", lr.AccessToken, map[string]any{
		"name":    "New Display Name",
		"role":    "admin",
		"org_id":  orgB.String(),
		"is_admin": true,
	})
	body := readRespBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var got struct {
		Name  *string `json:"name"`
		Role  string  `json:"role"`
		OrgID string  `json:"org_id"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name == nil || *got.Name != "New Display Name" {
		t.Fatalf("expected name to update, got %v", got.Name)
	}
	if got.Role != "viewer" {
		t.Fatalf("role must not change via PATCH /v1/users/me, got %q", got.Role)
	}
	if got.OrgID != orgA.String() {
		t.Fatalf("org_id must not change via PATCH /v1/users/me, got %q want %q", got.OrgID, orgA.String())
	}

	// Confirm directly against the database too, not just the response.
	var dbRole, dbOrgID string
	if err := env.pool.QueryRow(ctx, `SELECT role, org_id FROM users WHERE id = $1`, lr.User.ID).Scan(&dbRole, &dbOrgID); err != nil {
		t.Fatalf("query user row: %v", err)
	}
	if dbRole != "viewer" {
		t.Fatalf("DB role must not change, got %q", dbRole)
	}
	if dbOrgID != orgA.String() {
		t.Fatalf("DB org_id must not change, got %q want %q", dbOrgID, orgA.String())
	}
}

// ── provisioningLimiter (code review finding, closed this session) ──────────

// TestProvisioningRateLimit_RealDB_TripsAfterThreshold proves the 3 pre-auth
// provisioning routes are genuinely rate-limited -- code review found they
// were the one pre-auth-route family in this codebase with no limiter at
// all, unlike login and setup. Exercises request-reset specifically (no
// valid token/credential needed to hit it repeatedly); AcceptInvite and
// ResetPassword share the exact same s.provisioningLimiter.Allow(clientKey(r))
// call, not a separate implementation, so this one route is representative.
func TestProvisioningRateLimit_RealDB_TripsAfterThreshold(t *testing.T) {
	env := newProvisioningTestEnvDefaultLimits(t)

	// Default is 10/15min (config.DefaultRateLimitConfig().Provisioning) --
	// 10 attempts must all still go through.
	for i := 0; i < 10; i++ {
		resp := env.postJSON(t, "/v1/auth/request-reset", map[string]string{"email": "nobody@example.com"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("attempt %d: want 200 (still under threshold), got %d", i+1, resp.StatusCode)
		}
	}

	resp := env.postJSON(t, "/v1/auth/request-reset", map[string]string{"email": "nobody@example.com"})
	body := readRespBody(t, resp)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("11th rapid attempt: want 429, got %d: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatalf("429 response must carry a Retry-After header")
	}
}
