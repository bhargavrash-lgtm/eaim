// bootstrap_test.go -- eami-api/internal/api
// Real-Postgres integration tests for the first-boot setup wizard
// (internal/api/bootstrap.go). "System unconfigured" (SELECT count(*) FROM
// orgs = 0) is central to what's being tested, but the shared dev database
// already has orgs in it -- so each test here creates its own throwaway
// database and applies the real schema/migrations-v2/*.up.sql files against
// it, mirroring schema/migrationtest's own per-test throwaway-DB pattern.
// That module can't be imported directly (deliberately excluded from
// go.work per B-051 -- see its own go.mod comment), so migrations are
// applied here by reading the same .sql files pgx's simple query protocol
// (which, unlike the default extended protocol, supports a file containing
// multiple ;-separated statements in one Exec call).
//
// Run against the project's docker-compose Postgres:
//   docker compose up -d postgres
//   POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestBootstrap -v
//   POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestValidateSetupToken -v
//   POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestSetupStatus -v
package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/config"
	"github.com/eami/api/internal/store"
)

// ─── connection / schema plumbing ──────────────────────────────────────────────

type bootstrapPgConn struct {
	host, user, pass string
}

// bootstrapTestPgConn resolves real-Postgres connection details for this
// test file's own admin-connection needs (CREATE/DROP DATABASE), which
// need host/user/pass split out rather than one connection string.
//
// TEST_DATABASE_URL, when set, is parsed as a full DSN -- this matches
// every other eami-api real-Postgres test file's convention
// (paste_events_test.go, tools_update_pg_test.go, etc., which return it
// directly as the DSN) and, concretely, matches what CI's matrix `test`
// job actually provides for eami-api: only TEST_DATABASE_URL, not
// POSTGRES_PASSWORD (see .github/workflows/build.yml -- POSTGRES_PASSWORD
// is only additionally set for the separate, standalone
// test-migrationtest job, whose own testPgConn() in
// schema/migrationtest/migrate_test.go has a narrower, presence-only
// check against TEST_DATABASE_URL; copying that narrower pattern here was
// a real bug caught by a live CI failure -- this file runs under the
// matrix job, not test-migrationtest, and has no dbname requirement of
// its own to justify only checking TEST_DATABASE_URL's presence instead
// of actually using it).
func bootstrapTestPgConn(t *testing.T) bootstrapPgConn {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatalf("parse TEST_DATABASE_URL: %v", err)
		}
		return bootstrapPgConn{
			host: fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			user: cfg.User,
			pass: cfg.Password,
		}
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/localhost:5432 layout) to run setup-wizard integration tests against a real Postgres")
	}
	// 127.0.0.1, not localhost -- this machine resolves localhost to ::1
	// first and Docker Desktop's IPv6 port-forwarding silently drops
	// traffic (documented in CONTEXT.md).
	return bootstrapPgConn{host: "127.0.0.1:5432", user: "eami_app", pass: pw}
}

func (c bootstrapPgConn) adminURL() string {
	return fmt.Sprintf("postgres://%s:%s@%s/postgres?sslmode=disable", c.user, c.pass, c.host)
}

func (c bootstrapPgConn) dbURL(dbName string) string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", c.user, c.pass, c.host, dbName)
}

// newThrowawayDB creates a fresh, uniquely-named database and registers its
// cleanup. Mirrors schema/migrationtest/migrate_test.go's newThrowawayDB.
func newThrowawayDB(t *testing.T, c bootstrapPgConn) string {
	t.Helper()
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, c.adminURL())
	if err != nil {
		t.Fatalf("connect to admin DB: %v", err)
	}
	defer conn.Close(ctx)

	dbName := fmt.Sprintf("bootstraptest_%d_%d", os.Getpid(), rand.Int63())
	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, dbName)); err != nil {
		t.Fatalf("create throwaway database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		cc, err := pgx.Connect(context.Background(), c.adminURL())
		if err != nil {
			t.Errorf("cleanup: connect to admin DB (throwaway database %s may be leaked): %v", dbName, err)
			return
		}
		defer cc.Close(context.Background())
		_, _ = cc.Exec(context.Background(),
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName)
		if _, err := cc.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, dbName)); err != nil {
			t.Errorf("cleanup: drop throwaway database %s (leaked): %v", dbName, err)
		}
	})
	return dbName
}

// applyMigrations runs every schema/migrations-v2/*.up.sql file, in order,
// against dbName -- the exact files docker-compose's migrate service (B-051)
// applies in production, not a copy or a reduced subset.
func applyMigrations(t *testing.T, c bootstrapPgConn, dbName string) {
	t.Helper()
	ctx := context.Background()

	dir, err := filepath.Abs("../../../schema/migrations-v2")
	if err != nil {
		t.Fatalf("resolve migrations-v2 path: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations-v2 dir: %v", err)
	}
	var upFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			upFiles = append(upFiles, e.Name())
		}
	}
	sort.Strings(upFiles)
	if len(upFiles) == 0 {
		t.Fatalf("no *.up.sql files found in %s", dir)
	}

	cfg, err := pgx.ParseConfig(c.dbURL(dbName))
	if err != nil {
		t.Fatalf("parse migration connect config: %v", err)
	}
	// Simple protocol -- these files contain multiple ;-separated
	// statements (and PL/pgSQL DO $$ ... $$ blocks), which the extended
	// protocol's one-statement-per-Parse restriction can't run in a single
	// Exec call.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect for migrations: %v", err)
	}
	defer conn.Close(ctx)

	for _, name := range upFiles {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := conn.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

// ─── test server plumbing ──────────────────────────────────────────────────────

type bootstrapTestEnv struct {
	pool *pgxpool.Pool
	url  string
	http *http.Client
}

func newBootstrapTestEnv(t *testing.T) *bootstrapTestEnv {
	t.Helper()
	c := bootstrapTestPgConn(t)
	dbName := newThrowawayDB(t, c)
	applyMigrations(t, c, dbName)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, c.dbURL(dbName))
	if err != nil {
		t.Fatalf("connect pool to throwaway db: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach throwaway database: %v", err)
	}
	t.Cleanup(pool.Close)

	q := store.New(pool)
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("init auth service: %v", err)
	}
	cfg := &config.Config{ServiceKey: "test-service-key-bootstrap"}
	srv := api.NewServer(q, authSvc, nil, cfg)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &bootstrapTestEnv{pool: pool, url: ts.URL, http: ts.Client()}
}

// hashSetupTokenForTest mirrors bootstrap.go's private hashSetupToken
// (SHA-256 hex) -- can't call the unexported function from this external
// _test package, so it's reimplemented directly against the same stdlib
// primitives, same as tools_update_pg_test.go builds its own cipher fixture
// directly rather than reaching into unexported internals.
func hashSetupTokenForTest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// insertSetupToken seeds a setup_tokens row directly (bypassing
// appliance/scripts/eami-stack.sh, which isn't present in this test
// environment) so each test can construct exactly the token state
// (valid / expired / already-consumed) it needs to exercise.
func insertSetupToken(t *testing.T, pool *pgxpool.Pool, raw string, expiresAt time.Time, consumed bool) {
	t.Helper()
	ctx := context.Background()
	var err error
	if consumed {
		_, err = pool.Exec(ctx,
			`INSERT INTO setup_tokens (token_hash, expires_at, consumed_at) VALUES ($1, $2, now())`,
			hashSetupTokenForTest(raw), expiresAt)
	} else {
		_, err = pool.Exec(ctx,
			`INSERT INTO setup_tokens (token_hash, expires_at) VALUES ($1, $2)`,
			hashSetupTokenForTest(raw), expiresAt)
	}
	if err != nil {
		t.Fatalf("seed setup token: %v", err)
	}
}

func scalarInt(t *testing.T, pool *pgxpool.Pool, query string) int {
	t.Helper()
	var v int
	if err := pool.QueryRow(context.Background(), query).Scan(&v); err != nil {
		t.Fatalf("scalar query %q: %v", query, err)
	}
	return v
}

func (e *bootstrapTestEnv) get(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := e.http.Get(e.url + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (e *bootstrapTestEnv) postJSON(t *testing.T, path string, body any) *http.Response {
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

func (e *bootstrapTestEnv) postValidate(t *testing.T, token string) *http.Response {
	t.Helper()
	return e.postJSON(t, "/v1/setup/token/validate", map[string]string{"setup_token": token})
}

func (e *bootstrapTestEnv) postBootstrap(t *testing.T, token, orgName, email, password string) *http.Response {
	t.Helper()
	return e.postJSON(t, "/v1/setup/bootstrap", map[string]string{
		"setup_token":    token,
		"org_name":       orgName,
		"admin_email":    email,
		"admin_password": password,
	})
}

func (e *bootstrapTestEnv) postLogin(t *testing.T, email, password string) *http.Response {
	t.Helper()
	return e.postJSON(t, "/v1/auth/login", map[string]string{"email": email, "password": password})
}

func decodeSetupBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}

func readBody(resp *http.Response) string {
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// ─── tests ──────────────────────────────────────────────────────────────────────

func TestValidateSetupToken_Cases(t *testing.T) {
	env := newBootstrapTestEnv(t)

	t.Run("missing token is bad request", func(t *testing.T) {
		resp := env.postValidate(t, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readBody(resp))
		}
	})

	t.Run("unknown token is unauthorized", func(t *testing.T) {
		resp := env.postValidate(t, "does-not-exist")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401: %s", resp.StatusCode, readBody(resp))
		}
	})

	t.Run("expired token is unauthorized", func(t *testing.T) {
		raw := "expired-token-fixture"
		insertSetupToken(t, env.pool, raw, time.Now().Add(-time.Minute), false)
		resp := env.postValidate(t, raw)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401: %s", resp.StatusCode, readBody(resp))
		}
	})

	t.Run("already-consumed token is unauthorized", func(t *testing.T) {
		raw := "consumed-token-fixture"
		insertSetupToken(t, env.pool, raw, time.Now().Add(30*time.Minute), true)
		resp := env.postValidate(t, raw)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401: %s", resp.StatusCode, readBody(resp))
		}
	})

	t.Run("valid unconsumed token is accepted without consuming it", func(t *testing.T) {
		raw := "valid-token-fixture"
		insertSetupToken(t, env.pool, raw, time.Now().Add(30*time.Minute), false)
		resp := env.postValidate(t, raw)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204: %s", resp.StatusCode, readBody(resp))
		}
		// Validate must not consume -- confirm the same token can still be
		// used for a real Bootstrap afterward.
		bresp := env.postBootstrap(t, raw, "Validate Org", "validate-admin@example.com", "supersecret123")
		if bresp.StatusCode != http.StatusCreated {
			t.Fatalf("bootstrap after validate: status = %d, want 201: %s", bresp.StatusCode, readBody(bresp))
		}
	})
}

// AC1 + AC2 (real login proof) + AC3 (token dead after success).
func TestBootstrap_Success_LoginWorksAndTokenIsDeadAfterward(t *testing.T) {
	env := newBootstrapTestEnv(t)
	raw := "login-proof-token"
	insertSetupToken(t, env.pool, raw, time.Now().Add(30*time.Minute), false)

	resp := env.postBootstrap(t, raw, "Acme Corp", "admin@acme.test", "supersecret123")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap: status = %d, want 201: %s", resp.StatusCode, readBody(resp))
	}
	var created struct {
		OrgID      string `json:"org_id"`
		OrgName    string `json:"org_name"`
		AdminEmail string `json:"admin_email"`
	}
	decodeSetupBody(t, resp, &created)
	if created.OrgID == "" || created.OrgName != "Acme Corp" || created.AdminEmail != "admin@acme.test" {
		t.Fatalf("unexpected bootstrap response: %+v", created)
	}

	// AC2: log in for real with the credentials just created.
	loginResp := env.postLogin(t, "admin@acme.test", "supersecret123")
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login: status = %d, want 200: %s", loginResp.StatusCode, readBody(loginResp))
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
		User        struct {
			Role  string `json:"role"`
			Email string `json:"email"`
		} `json:"user"`
	}
	decodeSetupBody(t, loginResp, &loginBody)
	if loginBody.AccessToken == "" {
		t.Fatal("expected a real access token after login")
	}
	if loginBody.User.Role != "admin" {
		t.Fatalf("created user role = %q, want admin", loginBody.User.Role)
	}

	// AC3: the same token cannot be reused.
	resp2 := env.postBootstrap(t, raw, "Another Org", "admin2@acme.test", "supersecret123")
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused token: status = %d, want 401: %s", resp2.StatusCode, readBody(resp2))
	}
}

// AC1: without a valid token, nothing is created regardless of how
// well-formed the rest of the request is.
func TestBootstrap_WithoutValidToken_CreatesNothing(t *testing.T) {
	env := newBootstrapTestEnv(t)

	resp := env.postBootstrap(t, "not-a-real-token", "Should Not Exist", "nope@example.com", "supersecret123")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", resp.StatusCode, readBody(resp))
	}
	if n := scalarInt(t, env.pool, `SELECT count(*) FROM orgs`); n != 0 {
		t.Fatalf("orgs count = %d, want 0 -- an invalid token must create nothing", n)
	}
}

// AC4/AC6 (state transition): status flips after a successful bootstrap,
// and the independent system-state check (not just token validity) rejects
// a second attempt even with a different, still-valid token.
func TestSetupStatus_TransitionsAfterBootstrap(t *testing.T) {
	env := newBootstrapTestEnv(t)

	var before struct {
		Configured bool `json:"configured"`
	}
	decodeSetupBody(t, env.get(t, "/v1/setup/status"), &before)
	if before.Configured {
		t.Fatal("expected configured=false before any setup")
	}

	raw := "status-transition-token"
	insertSetupToken(t, env.pool, raw, time.Now().Add(30*time.Minute), false)
	resp := env.postBootstrap(t, raw, "Status Org", "status-admin@example.com", "supersecret123")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap: status = %d, want 201: %s", resp.StatusCode, readBody(resp))
	}

	var after struct {
		Configured bool `json:"configured"`
	}
	decodeSetupBody(t, env.get(t, "/v1/setup/status"), &after)
	if !after.Configured {
		t.Fatal("expected configured=true after a successful setup")
	}

	// A second, otherwise-valid token still can't bootstrap a second org --
	// the system-state check is independent of token validity.
	raw2 := "status-transition-token-2"
	insertSetupToken(t, env.pool, raw2, time.Now().Add(30*time.Minute), false)
	resp2 := env.postBootstrap(t, raw2, "Second Org", "second-admin@example.com", "supersecret123")
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second bootstrap: status = %d, want 409: %s", resp2.StatusCode, readBody(resp2))
	}
	if n := scalarInt(t, env.pool, `SELECT count(*) FROM orgs`); n != 1 {
		t.Fatalf("orgs count = %d, want 1", n)
	}
}

// AC5, proven concurrently for real: two goroutines race the same valid
// token. Exactly one must win.
func TestBootstrap_ConcurrentRequests_ExactlyOneWins(t *testing.T) {
	env := newBootstrapTestEnv(t)
	raw := "race-token"
	insertSetupToken(t, env.pool, raw, time.Now().Add(30*time.Minute), false)

	const n = 5
	codes := make([]int, n)
	bodies := make([]string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			resp := env.postBootstrap(t, raw, "Race Corp", "race-admin@example.com", "supersecret123")
			codes[idx] = resp.StatusCode
			bodies[idx] = readBody(resp)
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, c := range codes {
		if c == http.StatusCreated {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly 1 of %d concurrent requests to succeed, got %d -- codes=%v bodies=%v",
			n, successes, codes, bodies)
	}
	for _, c := range codes {
		if c != http.StatusCreated && c != http.StatusUnauthorized {
			t.Errorf("unexpected status among concurrent requests: %d (want 201 or 401)", c)
		}
	}

	if orgCount := scalarInt(t, env.pool, `SELECT count(*) FROM orgs`); orgCount != 1 {
		t.Fatalf("orgs count after race = %d, want exactly 1", orgCount)
	}
	if userCount := scalarInt(t, env.pool, `SELECT count(*) FROM users`); userCount != 1 {
		t.Fatalf("users count after race = %d, want exactly 1", userCount)
	}
}

// The same race, but with two DIFFERENT valid, unconsumed tokens -- proves
// the guard is a real, general-purpose DB-level lock (pg_advisory_xact_lock
// in Bootstrap), not just a side effect of two requests contending for the
// same setup_tokens row. Two different tokens hold no row lock in common,
// so without the advisory lock, both could independently observe
// orgCount == 0 under READ COMMITTED and both insert an org. In normal
// appliance operation only one unconsumed token ever exists at a time
// (eami-stack.sh deletes any prior one before minting a new one), but that
// is an operational invariant enforced by a shell script, not a database
// guarantee -- this test constructs the scenario directly rather than
// relying on that invariant, per the task brief's explicit requirement for
// a real database-level lock.
func TestBootstrap_ConcurrentRequests_DifferentValidTokens_ExactlyOneWins(t *testing.T) {
	env := newBootstrapTestEnv(t)
	rawA := "race-token-a"
	rawB := "race-token-b"
	insertSetupToken(t, env.pool, rawA, time.Now().Add(30*time.Minute), false)
	insertSetupToken(t, env.pool, rawB, time.Now().Add(30*time.Minute), false)

	var wg sync.WaitGroup
	codes := make([]int, 2)
	bodies := make([]string, 2)
	start := make(chan struct{})
	tokens := []string{rawA, rawB}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			resp := env.postBootstrap(t, tokens[idx], "Race Corp", "race-admin@example.com", "supersecret123")
			codes[idx] = resp.StatusCode
			bodies[idx] = readBody(resp)
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, c := range codes {
		if c == http.StatusCreated {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly 1 of 2 concurrent requests (different tokens) to succeed, got %d -- codes=%v bodies=%v",
			successes, codes, bodies)
	}
	// The loser must be rejected for system-state (409, the other token's
	// org already exists), not a token problem of its own -- its own token
	// was perfectly valid.
	for _, c := range codes {
		if c != http.StatusCreated && c != http.StatusConflict {
			t.Errorf("unexpected status among concurrent requests: %d (want 201 or 409)", c)
		}
	}

	if orgCount := scalarInt(t, env.pool, `SELECT count(*) FROM orgs`); orgCount != 1 {
		t.Fatalf("orgs count after race = %d, want exactly 1", orgCount)
	}
	if userCount := scalarInt(t, env.pool, `SELECT count(*) FROM users`); userCount != 1 {
		t.Fatalf("users count after race = %d, want exactly 1", userCount)
	}
}

// Abandoned-session recovery: a setup attempt that begins (locks the token)
// but never commits -- simulating a crashed process or a dropped connection
// mid-request, the exact scenario Bootstrap's single-transaction design is
// meant to survive -- must leave the token valid for a real, subsequent
// attempt to succeed.
func TestBootstrap_InterruptedTransaction_TokenStaysValidForRetry(t *testing.T) {
	env := newBootstrapTestEnv(t)
	raw := "abandoned-session-token"
	insertSetupToken(t, env.pool, raw, time.Now().Add(30*time.Minute), false)

	ctx := context.Background()
	tx, err := env.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin simulated interrupted transaction: %v", err)
	}
	var tokenID string
	if err := tx.QueryRow(ctx,
		`SELECT id FROM setup_tokens WHERE token_hash = $1 FOR UPDATE`,
		hashSetupTokenForTest(raw),
	).Scan(&tokenID); err != nil {
		t.Fatalf("lock token row: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE setup_tokens SET consumed_at = now() WHERE id = $1`, tokenID); err != nil {
		t.Fatalf("simulate in-flight consumption: %v", err)
	}
	// The interruption: roll back instead of committing (crash / dropped
	// connection / any failure after the lock but before Bootstrap's own
	// tx.Commit would have run).
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback simulated interrupted transaction: %v", err)
	}

	if n := scalarInt(t, env.pool, `SELECT count(*) FROM orgs`); n != 0 {
		t.Fatalf("orgs count after interrupted attempt = %d, want 0", n)
	}

	// A fresh, real attempt with the same still-valid token now succeeds.
	resp := env.postBootstrap(t, raw, "Recovered Corp", "recovered-admin@example.com", "supersecret123")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("retry after interrupted attempt: status = %d, want 201: %s", resp.StatusCode, readBody(resp))
	}
}

func TestBootstrap_RateLimiting_TooManyAttempts(t *testing.T) {
	env := newBootstrapTestEnv(t)

	// Every attempt here uses a token that doesn't exist -- purely testing
	// the request counter, not token validity.
	var lastCode int
	for i := 0; i < 11; i++ {
		resp := env.postBootstrap(t, "rate-limit-probe", "Org", "rl@example.com", "supersecret123")
		lastCode = resp.StatusCode
		if i < 10 && lastCode == http.StatusTooManyRequests {
			t.Fatalf("rate limited too early, on attempt %d", i+1)
		}
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("11th attempt: status = %d, want 429", lastCode)
	}
}

func TestBootstrap_ValidationRejectsBadInput(t *testing.T) {
	env := newBootstrapTestEnv(t)
	raw := "validation-token"
	insertSetupToken(t, env.pool, raw, time.Now().Add(30*time.Minute), false)

	cases := []struct {
		name, org, email, pass string
	}{
		{"empty org name", "", "a@b.com", "supersecret123"},
		{"invalid email", "Org", "not-an-email", "supersecret123"},
		{"short password", "Org", "a@b.com", "short"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := env.postBootstrap(t, raw, tc.org, tc.email, tc.pass)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", resp.StatusCode, readBody(resp))
			}
		})
	}

	// None of the rejected attempts above should have touched the token --
	// it's still valid for a real, correct attempt.
	resp := env.postBootstrap(t, raw, "Finally Valid Org", "valid@example.com", "supersecret123")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("final valid attempt: status = %d, want 201: %s", resp.StatusCode, readBody(resp))
	}
}
