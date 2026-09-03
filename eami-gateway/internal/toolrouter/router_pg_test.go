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
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/testdb"
)

// ─── shared test env ────────────────────────────────────────────────────────

type toolrouterTestEnv struct {
	pool  *pgxpool.Pool
	orgID uuid.UUID
}

// newToolrouterTestEnv (B-122/B-140) provisions a fresh, isolated
// throwaway database per test via internal/testdb -- see that package's
// doc comment for why. No per-org DELETE cleanup is needed any more.
func newToolrouterTestEnv(t *testing.T) *toolrouterTestEnv {
	t.Helper()
	pool := testdb.NewThrowawayPool(t)
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "toolrouter-test-"+orgID.String()[:8], "toolrouter-test-"+orgID.String()); err != nil {
		t.Fatalf("insert test org: %v", err)
	}

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

// insertToolWithActionPaths is insertTool plus an action_paths JSONB value
// (B-046) -- kept as a separate helper rather than widening insertTool's
// signature, since every existing call site relies on positional args and
// most tests genuinely have no mappings (NULL, B-044's flat behavior).
func (e *toolrouterTestEnv) insertToolWithActionPaths(t *testing.T, orgID uuid.UUID, name, toolType, authType string, baseURL *string, credsEncrypted []byte, actionPaths map[string]ActionPathEntry) {
	t.Helper()
	id := uuid.New()
	actionPathsJSON, err := json.Marshal(actionPaths)
	if err != nil {
		t.Fatalf("marshal action_paths: %v", err)
	}
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, base_url, credentials_encrypted, action_paths)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, id, orgID, name, toolType, authType, baseURL, credsEncrypted, actionPathsJSON); err != nil {
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

// TestResolve_PicksUpEditedConfig_NoStaleCache proves B-045's AC4:
// Router.Resolve has no caching layer (confirmed by inspection -- it's a
// direct pool.QueryRow on every call, unlike e.g. internal/registry's 30s
// TTL cache) -- an edit to a gateway_tools row a real eami-api PATCH would
// make (simulated here via a direct UPDATE, since that handler lives in a
// different Go module) takes effect on the very next Resolve/Forward, not
// after some delay or a gateway restart.
func TestResolve_PicksUpEditedConfig_NoStaleCache(t *testing.T) {
	env := newToolrouterTestEnv(t)
	r := newWithDialer(env.pool, nil, unrestrictedDialer)

	var hitA, hitB bool
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitA = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitB = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srvB.Close()

	env.insertTool(t, env.orgID, "editable-tool", "rest_api", "api_key", strPtr(srvA.URL), nil)

	row, err := r.Resolve(context.Background(), env.orgID.String(), "editable-tool")
	if err != nil {
		t.Fatalf("Resolve (before edit): %v", err)
	}
	if _, err := r.Forward(context.Background(), row, toolRequestFor("editable-tool", "call", nil)); err != nil {
		t.Fatalf("Forward (before edit): %v", err)
	}
	if !hitA || hitB {
		t.Fatalf("before edit: hitA=%v hitB=%v, want only hitA", hitA, hitB)
	}

	// Simulate the real eami-api UpdateTool PATCH (a different Go module --
	// this direct UPDATE is the same SQL effect UpdateTool's COALESCE
	// produces for a base_url-only edit).
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE gateway_tools SET base_url = $1 WHERE org_id = $2 AND name = $3`,
		srvB.URL, env.orgID, "editable-tool"); err != nil {
		t.Fatalf("simulate edit: %v", err)
	}

	row2, err := r.Resolve(context.Background(), env.orgID.String(), "editable-tool")
	if err != nil {
		t.Fatalf("Resolve (after edit): %v", err)
	}
	if *row2.BaseURL != srvB.URL {
		t.Fatalf("Resolve after edit returned stale base_url %q, want %q", *row2.BaseURL, srvB.URL)
	}
	if _, err := r.Forward(context.Background(), row2, toolRequestFor("editable-tool", "call", nil)); err != nil {
		t.Fatalf("Forward (after edit): %v", err)
	}
	if !hitB {
		t.Fatal("after edit: the dispatch should have reached the NEW server (srvB), it didn't -- stale routing")
	}
}

// ─── Per-action path mapping (B-046) ────────────────────────────────────────

// TestResolve_ParsesActionPaths proves Resolve reads action_paths back into
// ToolRow.ActionPaths correctly (both a tool with mappings and one without).
func TestResolve_ParsesActionPaths(t *testing.T) {
	env := newToolrouterTestEnv(t)
	r := New(env.pool, nil)

	env.insertToolWithActionPaths(t, env.orgID, "mapped-tool", "rest_api", "api_key", strPtr("https://example.com"), nil,
		map[string]ActionPathEntry{"list": {Path: "/v1/list", Method: "GET"}})
	env.insertTool(t, env.orgID, "unmapped-tool", "rest_api", "api_key", strPtr("https://example.com"), nil)

	mapped, err := r.Resolve(context.Background(), env.orgID.String(), "mapped-tool")
	if err != nil {
		t.Fatalf("Resolve mapped-tool: %v", err)
	}
	if len(mapped.ActionPaths) != 1 || mapped.ActionPaths["list"].Path != "/v1/list" || mapped.ActionPaths["list"].Method != "GET" {
		t.Errorf("ActionPaths = %+v, want {list: {/v1/list GET}}", mapped.ActionPaths)
	}

	unmapped, err := r.Resolve(context.Background(), env.orgID.String(), "unmapped-tool")
	if err != nil {
		t.Fatalf("Resolve unmapped-tool: %v", err)
	}
	if len(unmapped.ActionPaths) != 0 {
		t.Errorf("ActionPaths = %+v, want empty/nil for a tool with no mappings", unmapped.ActionPaths)
	}
}

// TestForward_ActionPathMapping_RoutesCorrectly proves AC1: a tool with
// mappings for two different actions dispatches each to its own path and
// method, joined onto base_url, instead of always POSTing to base_url.
func TestForward_ActionPathMapping_RoutesCorrectly(t *testing.T) {
	env := newToolrouterTestEnv(t)

	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath, gotMethod = req.URL.Path, req.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	env.insertToolWithActionPaths(t, env.orgID, "multi-endpoint", "rest_api", "api_key", strPtr(srv.URL), nil,
		map[string]ActionPathEntry{
			"list_contacts":  {Path: "/contacts", Method: "GET"},
			"create_contact": {Path: "/contacts/new", Method: "PUT"},
		})

	r := newWithDialer(env.pool, nil, unrestrictedDialer)
	row, err := r.Resolve(context.Background(), env.orgID.String(), "multi-endpoint")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if _, err := r.Forward(context.Background(), row, toolRequestFor("multi-endpoint", "list_contacts", nil)); err != nil {
		t.Fatalf("Forward(list_contacts): %v", err)
	}
	if gotPath != "/contacts" || gotMethod != http.MethodGet {
		t.Errorf("list_contacts routed to %s %s, want GET /contacts", gotMethod, gotPath)
	}

	if _, err := r.Forward(context.Background(), row, toolRequestFor("multi-endpoint", "create_contact", nil)); err != nil {
		t.Fatalf("Forward(create_contact): %v", err)
	}
	if gotPath != "/contacts/new" || gotMethod != http.MethodPut {
		t.Errorf("create_contact routed to %s %s, want PUT /contacts/new", gotMethod, gotPath)
	}
}

// TestForward_UnmappedActionOnMappedTool_CleanRejection proves AC3's
// user-confirmed choice: a tool that HAS other action mappings defined,
// called with an action that isn't among them, must be cleanly rejected --
// never silently fall back to base_url (which the admin never authorized
// for this specific, unmapped action).
func TestForward_UnmappedActionOnMappedTool_CleanRejection(t *testing.T) {
	env := newToolrouterTestEnv(t)

	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	env.insertToolWithActionPaths(t, env.orgID, "partially-mapped", "rest_api", "api_key", strPtr(srv.URL), nil,
		map[string]ActionPathEntry{"list": {Path: "/list", Method: "GET"}})

	r := newWithDialer(env.pool, nil, unrestrictedDialer)
	row, err := r.Resolve(context.Background(), env.orgID.String(), "partially-mapped")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	_, fwdErr := r.Forward(context.Background(), row, toolRequestFor("partially-mapped", "delete_everything", nil))
	if fwdErr == nil {
		t.Fatal("expected Forward to reject an action with no mapping on a tool that has other mappings, got nil error")
	}
	if hit {
		t.Fatal("Forward must not have dispatched anything to base_url for an unmapped action, but the server was hit")
	}
}

// TestForward_NoActionPathsAtAll_FlatBaseURLBehaviorUnchanged proves AC2: a
// tool with NO action_paths defined at all keeps working exactly like
// B-044 shipped it -- every action POSTs straight to base_url, unaffected
// by B-046 shipping.
func TestForward_NoActionPathsAtAll_FlatBaseURLBehaviorUnchanged(t *testing.T) {
	env := newToolrouterTestEnv(t)

	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath, gotMethod = req.URL.Path, req.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	env.insertTool(t, env.orgID, "flat-tool", "rest_api", "api_key", strPtr(srv.URL), nil) // no action_paths

	r := newWithDialer(env.pool, nil, unrestrictedDialer)
	row, err := r.Resolve(context.Background(), env.orgID.String(), "flat-tool")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, action := range []string{"anything", "literally_any_action_name"} {
		if _, err := r.Forward(context.Background(), row, toolRequestFor("flat-tool", action, nil)); err != nil {
			t.Fatalf("Forward(%s): %v", action, err)
		}
		if gotPath != "/" || gotMethod != http.MethodPost {
			t.Errorf("action %q routed to %s %q, want POST directly to base_url (root path)", action, gotMethod, gotPath)
		}
	}
}

// TestForward_SSRFStillBlocked_WithActionMapping proves the SSRF guard
// still applies on the mapped-path dispatch path, not just the flat one:
// joining a mapped path onto a base_url pointing at a private/link-local
// address must still be rejected before any connection is attempted.
func TestForward_SSRFStillBlocked_WithActionMapping(t *testing.T) {
	env := newToolrouterTestEnv(t)
	r := New(env.pool, nil) // real guarded dialer

	env.insertToolWithActionPaths(t, env.orgID, "ssrf-mapped-tool", "rest_api", "api_key", strPtr("http://169.254.169.254/"), nil,
		map[string]ActionPathEntry{"list": {Path: "/latest/meta-data", Method: "GET"}})

	row, err := r.Resolve(context.Background(), env.orgID.String(), "ssrf-mapped-tool")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	_, fwdErr := r.Forward(context.Background(), row, toolRequestFor("ssrf-mapped-tool", "list", nil))
	if fwdErr == nil {
		t.Fatal("expected Forward to reject a mapped-path request to a link-local base_url, got nil error")
	}
}
