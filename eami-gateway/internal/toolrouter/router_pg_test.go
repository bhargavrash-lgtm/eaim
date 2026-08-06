// router_pg_test.go -- eami-gateway/internal/toolrouter
//
// Integration tests for Router.Resolve/Forward against a REAL Postgres,
// following the pattern established by internal/approval/router_pg_test.go
// (B-039) and internal/identity/tokens_pg_test.go (B-041): TEST_DATABASE_URL/
// POSTGRES_PASSWORD fallback, throwaway orgs/gateway_tools fixtures cleaned
// up via t.Cleanup.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/toolrouter/... -v
package toolrouter

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/proxy"
)

// ─── shared test env ────────────────────────────────────────────────────────

func toolrouterTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/127.0.0.1:5432 layout) to run toolrouter integration tests against a real Postgres")
	}
	return "postgresql://eami_app:" + pw + "@127.0.0.1:5432/eami"
}

type toolrouterTestEnv struct {
	pool  *pgxpool.Pool
	orgID uuid.UUID
}

func newToolrouterTestEnv(t *testing.T) *toolrouterTestEnv {
	t.Helper()
	dsn := toolrouterTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(pool.Close)

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "toolrouter-test-"+orgID.String()[:8], "toolrouter-test-"+orgID.String()); err != nil {
		t.Fatalf("insert test org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID)
	})

	return &toolrouterTestEnv{pool: pool, orgID: orgID}
}

// insertTool inserts a real gateway_tools row.
func (e *toolrouterTestEnv) insertTool(t *testing.T, orgID uuid.UUID, name, toolType, authType string, baseURL *string, credsEncrypted []byte) {
	t.Helper()
	id := uuid.New()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, base_url, credentials_encrypted)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, id, orgID, name, toolType, authType, baseURL, credsEncrypted); err != nil {
		t.Fatalf("insert gateway_tools row: %v", err)
	}
}

// unrestrictedDialer is a plain net.Dialer with no SSRF restrictions --
// used only in tests that need to reach a local httptest server (always
// 127.0.0.1, which the real safeDialContext correctly blocks). Production
// (New) always uses the real safeDialContext; see newWithDialer's doc
// comment.
func unrestrictedDialer(ctx context.Context, network, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

// testKeyHex is a fixed, valid 32-byte AES-256 key for tests only.
const testKeyHex = "7105980461784fcb4998a025fa91b7a762f46f345248b41b6cff6265d14d8b1f"

// encryptForTest seals plaintext the same way toolcreds.Cipher.Encrypt
// (B-022, eami-api) does -- nonce||ciphertext -- so Router.Forward's real
// Decrypt path has a genuine, correctly-shaped blob to open. Deliberately
// not exposed via toolrouter.Cipher itself (eami-gateway never encrypts in
// production, only decrypts); this is a test-only helper using the same
// primitives directly, via the real Cipher's own aead field.
func encryptForTest(t *testing.T, hexKey string, plaintext []byte) []byte {
	t.Helper()
	c, err := NewCipher(hexKey)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("generate nonce: %v", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil)
}

func strPtr(s string) *string { return &s }

func toolRequestFor(tool, action string, params map[string]any) proxy.ToolRequest {
	return proxy.ToolRequest{ToolName: tool, Action: action, Params: params, SessionID: "test-session"}
}

// ─── Resolve ────────────────────────────────────────────────────────────────

func TestResolve_FindsRestAPITool(t *testing.T) {
	env := newToolrouterTestEnv(t)
	r := New(env.pool, nil)

	env.insertTool(t, env.orgID, "salesforce", "rest_api", "api_key", strPtr("https://salesforce.example.com"), nil)

	row, err := r.Resolve(context.Background(), env.orgID.String(), "salesforce")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if row.Type != "rest_api" {
		t.Errorf("Type = %q, want rest_api", row.Type)
	}
	if row.BaseURL == nil || *row.BaseURL != "https://salesforce.example.com" {
		t.Errorf("BaseURL = %v, want https://salesforce.example.com", row.BaseURL)
	}
}

func TestResolve_NotFound_ReturnsErrNotFound(t *testing.T) {
	env := newToolrouterTestEnv(t)
	r := New(env.pool, nil)

	_, err := r.Resolve(context.Background(), env.orgID.String(), "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve: err = %v, want ErrNotFound", err)
	}
}

// TestResolve_CrossOrg_NotFound proves org scoping (AC4): a tool named
// identically in a different org must not resolve -- same org-safety
// discipline as B-042's registry.LookupByNameAndOrg fix.
func TestResolve_CrossOrg_NotFound(t *testing.T) {
	env := newToolrouterTestEnv(t)
	r := New(env.pool, nil)

	env.insertTool(t, env.orgID, "shared-name", "rest_api", "api_key", strPtr("https://example.com"), nil)

	otherOrgID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		otherOrgID, "toolrouter-test-other-"+otherOrgID.String()[:8], "toolrouter-test-other-"+otherOrgID.String()); err != nil {
		t.Fatalf("insert other org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, otherOrgID)
	})

	_, err := r.Resolve(context.Background(), otherOrgID.String(), "shared-name")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve from other org: err = %v, want ErrNotFound (cross-org isolation)", err)
	}
}

// ─── Forward ────────────────────────────────────────────────────────────────

// TestForward_RealRESTCall_Success proves AC1 end-to-end at the Forward
// layer: a real HTTP round trip to the tool's real base_url, with the
// exact envelope Forward builds, reaching a real server. Uses the
// unrestricted test dialer -- an httptest.Server always binds to
// 127.0.0.1, which the real safeDialContext correctly blocks (see
// TestForward_SSRFBlocked_PrivateAddress); this test is about proving the
// dispatch/envelope/response-handling logic, not re-proving the guard.
func TestForward_RealRESTCall_Success(t *testing.T) {
	env := newToolrouterTestEnv(t)

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewDecoder(req.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	env.insertTool(t, env.orgID, "internal-api", "rest_api", "api_key", strPtr(srv.URL), nil)

	r := newWithDialer(env.pool, nil, unrestrictedDialer)
	row, err := r.Resolve(context.Background(), env.orgID.String(), "internal-api")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	resp, err := r.Forward(context.Background(), row, toolRequestFor("internal-api", "list", map[string]any{"x": float64(1)}))
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Errorf("Status = %d, want 200", resp.Status)
	}
	if gotBody["tool"] != "internal-api" || gotBody["action"] != "list" {
		t.Errorf("server received %+v, want tool=internal-api action=list", gotBody)
	}
}

// TestForward_APIKeyCredentials_HeaderSent proves credential decryption and
// application actually happen end-to-end -- the real server sees the real
// decrypted Authorization header, not just "Forward doesn't crash."
func TestForward_APIKeyCredentials_HeaderSent(t *testing.T) {
	env := newToolrouterTestEnv(t)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	credsJSON, _ := json.Marshal(Credentials{APIKey: "sk-real-secret-key"})
	encrypted := encryptForTest(t, testKeyHex, credsJSON)
	env.insertTool(t, env.orgID, "keyed-api", "rest_api", "api_key", strPtr(srv.URL), encrypted)

	cipher, err := NewCipher(testKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	r := newWithDialer(env.pool, cipher, unrestrictedDialer)
	row, err := r.Resolve(context.Background(), env.orgID.String(), "keyed-api")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if _, err := r.Forward(context.Background(), row, toolRequestFor("keyed-api", "call", nil)); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if gotAuth != "Bearer sk-real-secret-key" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer sk-real-secret-key")
	}
}

// TestForward_UndecryptableCredentials_CleanRejection proves the
// "unconfigured" half of AC5: a row with stored credentials but no
// decryption key configured must cleanly reject, never panic, never
// proceed with an empty Authorization header.
func TestForward_UndecryptableCredentials_CleanRejection(t *testing.T) {
	env := newToolrouterTestEnv(t)

	credsJSON, _ := json.Marshal(Credentials{APIKey: "sk-real-secret-key"})
	encrypted := encryptForTest(t, testKeyHex, credsJSON)
	env.insertTool(t, env.orgID, "undecryptable", "rest_api", "api_key", strPtr("https://example.com"), encrypted)

	r := newWithDialer(env.pool, nil, unrestrictedDialer) // no cipher configured
	row, err := r.Resolve(context.Background(), env.orgID.String(), "undecryptable")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	_, fwdErr := r.Forward(context.Background(), row, toolRequestFor("undecryptable", "call", nil))
	if fwdErr == nil {
		t.Fatal("expected Forward to reject a tool with stored credentials but no cipher, got nil error")
	}
}

// TestForward_EmptyBaseURL_CleanRejection proves the other half of AC5: a
// resolved rest_api row with no base_url configured must cleanly reject.
func TestForward_EmptyBaseURL_CleanRejection(t *testing.T) {
	env := newToolrouterTestEnv(t)
	env.insertTool(t, env.orgID, "no-url", "rest_api", "api_key", nil, nil)

	r := New(env.pool, nil)
	row, err := r.Resolve(context.Background(), env.orgID.String(), "no-url")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	_, fwdErr := r.Forward(context.Background(), row, toolRequestFor("no-url", "call", nil))
	if fwdErr == nil {
		t.Fatal("expected Forward to reject a tool with no base_url, got nil error")
	}
}

// TestForward_NilRow_Rejected proves Forward's nil-row guard (code review
// finding during B-044): the single production call site never passes nil,
// but Router is an importable package -- a future caller shouldn't be able
// to panic it.
func TestForward_NilRow_Rejected(t *testing.T) {
	env := newToolrouterTestEnv(t)
	r := New(env.pool, nil)

	_, err := r.Forward(context.Background(), nil, toolRequestFor("whatever", "call", nil))
	if err == nil {
		t.Fatal("expected Forward to reject a nil row, got nil error")
	}
}

// TestForward_WrongType_Rejected proves Forward's own defense-in-depth:
// dispatch (main.go's resolveDynamicTool) should never call Forward for a
// non-rest_api row, but Forward itself still refuses rather than silently
// proceeding if it's ever called that way.
func TestForward_WrongType_Rejected(t *testing.T) {
	env := newToolrouterTestEnv(t)
	env.insertTool(t, env.orgID, "an-mcp-tool", "mcp", "api_key", nil, nil)

	r := New(env.pool, nil)
	row, err := r.Resolve(context.Background(), env.orgID.String(), "an-mcp-tool")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	_, fwdErr := r.Forward(context.Background(), row, toolRequestFor("an-mcp-tool", "call", nil))
	if fwdErr == nil {
		t.Fatal("expected Forward to reject a non-rest_api row, got nil error")
	}
}

// TestForward_SSRFBlocked_PrivateAddress proves AC3: the SSRF guard is
// active on the real dispatch path (Router.Forward itself, via the real
// New(), not the unrestricted test dialer used above), not just eami-api's
// connectivity-test button. A tool configured (as an admin could
// misconfigure, or an attacker with tool-creation access could deliberately
// configure) to point at a loopback or link-local address must be
// rejected before any connection is attempted.
func TestForward_SSRFBlocked_PrivateAddress(t *testing.T) {
	env := newToolrouterTestEnv(t)
	r := New(env.pool, nil) // the real, guarded dialer -- not unrestrictedDialer

	for _, target := range []string{
		"http://127.0.0.1:1/",     // loopback
		"http://169.254.169.254/", // cloud metadata / link-local
	} {
		t.Run(target, func(t *testing.T) {
			toolName := "ssrf-target-" + uuid.New().String()[:8]
			env.insertTool(t, env.orgID, toolName, "rest_api", "api_key", strPtr(target), nil)

			row, err := r.Resolve(context.Background(), env.orgID.String(), toolName)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			_, fwdErr := r.Forward(context.Background(), row, toolRequestFor(toolName, "call", nil))
			if fwdErr == nil {
				t.Fatal("expected Forward to reject a private/link-local target, got nil error")
			}
		})
	}
}
