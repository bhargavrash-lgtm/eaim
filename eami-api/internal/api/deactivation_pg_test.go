// deactivation_pg_test.go -- eami-api/internal/api
//
// B-211: real-Postgres regression test for the deactivated-user
// authentication gap. GetUserByEmail (Login) and GetUserByID (used by
// Refresh, via queriesAdapter) never filtered on deleted_at -- a soft-
// deleted (deactivated) user retained full login AND refresh capability.
// Same severity class as B-128/B-141/B-172/B-173, per this brief's own
// framing: a real access-control gap, not a UX nit.
//
// Follows workspaces_pg_test.go's own harness conventions exactly
// (toolsUpdateTestDSN, seedTestOrg, a real httptest server over
// api.NewServer) -- this proves the fix through the REAL HTTP handlers
// and the REAL sqlc-generated queries, not a mocked store.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestDeactivatedUser -v
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/store"
)

// deactivationTestEnv mirrors workspaceTestEnv (workspaces_pg_test.go) --
// a real pool + a real httptest server over the real api.NewServer, not a
// mock. Kept separate rather than reusing workspaceTestEnv directly since
// this test needs its own login-flow helpers (raw JSON request/response,
// not the Bearer-token env.do convenience workspaceTestEnv provides).
type deactivationTestEnv struct {
	pool *pgxpool.Pool
	http *http.Client
	url  string
}

func newDeactivationTestEnv(t *testing.T) *deactivationTestEnv {
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
	// t.Cleanup this test's own seed helper adds (CLAUDE.md's mandatory
	// real-Postgres pool lifecycle rule).
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &deactivationTestEnv{pool: pool, http: ts.Client(), url: ts.URL}
}

// seedActiveUser inserts a real users row with a real bcrypt hash of
// plainPassword, so Login can be exercised with genuine credentials
// rather than a pre-known hash. Mirrors the shape seedTestUser
// (workflows_test.go) already establishes, extended with a real,
// caller-known password.
func (e *deactivationTestEnv) seedActiveUser(t *testing.T, ctx context.Context, orgID uuid.UUID, email, plainPassword string) uuid.UUID {
	t.Helper()
	hash, err := auth.HashPassword(plainPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	userID := uuid.New()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO users (id, org_id, email, password_hash, role, name)
		VALUES ($1, $2, $3, $4, 'admin', 'Deactivation Test User')
	`, userID, orgID, email, hash); err != nil {
		t.Fatalf("seed active user: %v", err)
	}
	return userID
}

func (e *deactivationTestEnv) login(t *testing.T, email, password string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := e.http.Post(e.url+"/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	return resp
}

func (e *deactivationTestEnv) refresh(t *testing.T, refreshToken string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	resp, err := e.http.Post(e.url+"/v1/auth/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("refresh request: %v", err)
	}
	return resp
}

// TestDeactivatedUser_RealDB_LoginAndRefreshBothRejected is the brief's
// own mandatory adversarial test: deactivate a real user with an active,
// valid session (a real refresh token obtained BEFORE deactivation, not
// a fabricated one), and confirm BOTH a fresh Login attempt AND a Refresh
// attempt using that pre-existing token are rejected afterward -- proving
// the fix through real HTTP round trips against the real handlers, not
// just that GetUserByEmail/GetUserByID's SQL compiles with the new
// predicate.
func TestDeactivatedUser_RealDB_LoginAndRefreshBothRejected(t *testing.T) {
	env := newDeactivationTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b211-deactivation")

	const email = "deactivate-me@b211.test"
	const password = "b211-real-password-123"
	userID := env.seedActiveUser(t, ctx, orgID, email, password)

	// ── Baseline, BEFORE deactivation: login and refresh both genuinely work ──
	loginResp := env.login(t, email, password)
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("baseline login: status = %d, want 200", loginResp.StatusCode)
	}
	var loginBody struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&loginBody); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	loginResp.Body.Close()
	if loginBody.AccessToken == "" || loginBody.RefreshToken == "" {
		t.Fatalf("baseline login: missing tokens in response")
	}
	// This is the "active, valid session" the brief's adversarial test
	// requires -- a REAL refresh token minted before deactivation, held
	// UNUSED here and replayed below AFTER deactivation. Deliberately not
	// spent on a "does refresh work at all" sanity check first: Refresh
	// (auth.go) unconditionally revokes a token on every successful use
	// (single-use, RevokeRefreshToken), so consuming it here would make
	// the real post-deactivation assertion below pass for the WRONG
	// reason (GetRefreshToken's own revoked=FALSE filter rejecting an
	// already-spent token) rather than proving the deleted_at fix this
	// test exists for -- a real bug this brief's own mandatory code-review
	// pass caught in this file's first draft, fixed here.
	preDeactivationRefreshToken := loginBody.RefreshToken

	// A genuine positive control that refresh mechanically works before
	// deactivation, using its OWN separate, independently-issued token
	// (a second login) -- so the fact-of-working is proven, not just
	// assumed, without touching preDeactivationRefreshToken above.
	controlLoginResp := env.login(t, email, password)
	if controlLoginResp.StatusCode != http.StatusOK {
		t.Fatalf("positive-control login: status = %d, want 200", controlLoginResp.StatusCode)
	}
	var controlLoginBody struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(controlLoginResp.Body).Decode(&controlLoginBody); err != nil {
		t.Fatalf("decode positive-control login response: %v", err)
	}
	controlLoginResp.Body.Close()
	controlRefreshResp := env.refresh(t, controlLoginBody.RefreshToken)
	if controlRefreshResp.StatusCode != http.StatusOK {
		t.Fatalf("positive-control refresh: status = %d, want 200 (refresh must genuinely work before deactivation)", controlRefreshResp.StatusCode)
	}
	controlRefreshResp.Body.Close()

	// ── Deactivate: the real soft-delete DeleteUser performs ──────────────────
	if _, err := env.pool.Exec(ctx, `UPDATE users SET deleted_at = NOW() WHERE id = $1`, userID); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}

	// ── Adversarial case 1: a FRESH login with genuinely correct credentials ──
	postLoginResp := env.login(t, email, password)
	if postLoginResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("REGRESSION: login after deactivation: status = %d, want 401 -- a deactivated user must not "+
			"be able to authenticate with a still-known-correct password", postLoginResp.StatusCode)
	}
	postLoginResp.Body.Close()

	// ── Adversarial case 2: the PRE-EXISTING refresh token, minted before
	// deactivation -- this is the actual "active, valid session" scenario
	// the brief's own kickoff line requires, not a token minted after the
	// fact (which would trivially fail login already). ──────────────────────
	postRefreshResp := env.refresh(t, preDeactivationRefreshToken)
	if postRefreshResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("REGRESSION: refresh after deactivation, using a token minted BEFORE deactivation: status = %d, "+
			"want 401 -- a deactivated user's existing session must not be extendable via refresh", postRefreshResp.StatusCode)
	}
	postRefreshResp.Body.Close()
}

// TestDeactivatedUser_RealDB_AlreadyDeactivatedAtLogin_Rejected is a
// simpler companion case: a user deactivated BEFORE ever logging in
// (never had a session at all) must also be rejected -- rules out "the
// fix only works if the row happens to already be cached/known," proving
// it's the query predicate itself doing the work.
func TestDeactivatedUser_RealDB_AlreadyDeactivatedAtLogin_Rejected(t *testing.T) {
	env := newDeactivationTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b211-predeactivated")

	const email = "predeactivated@b211.test"
	const password = "b211-real-password-456"
	userID := env.seedActiveUser(t, ctx, orgID, email, password)
	if _, err := env.pool.Exec(ctx, `UPDATE users SET deleted_at = NOW() WHERE id = $1`, userID); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}

	resp := env.login(t, email, password)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("REGRESSION: status = %d, want 401 -- an already-deactivated user must never be able to log in "+
			"for the first time either, not just be blocked from continuing an existing session", resp.StatusCode)
	}
	resp.Body.Close()
}
