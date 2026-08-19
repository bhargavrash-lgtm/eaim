// middleware_test.go -- eami-collector/internal/api
// Tests for B-073: per-agent API keys (RegisterKey/RevokeKey/ListKeys, and
// APIKeyMiddleware's identity-binding behavior). Uses a real on-disk SQLite
// DB via db.Open (not a hand-rolled schema), so these tests exercise the
// actual migration/index logic a real deployment runs, not a simplified
// stand-in.
package api_test

import (
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/eami/collector/internal/api"
	"github.com/eami/collector/internal/db"
)

// openTestCollectorDB opens a real collector buffer DB (via db.Open, the
// exact code path the production binary uses) at a fresh temp-dir path.
func openTestCollectorDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "buffer.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// ── RegisterKey / RevokeKey / ListKeys ──────────────────────────────────────

func TestRegisterKey_RequiresAgentID(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "some-raw-key", "", "no agent id"); err == nil {
		t.Error("RegisterKey with empty agentID: want error, got nil")
	}
}

func TestRegisterKey_DuplicateActiveAgentID_Rejected(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "key-1", "agent-a", "first"); err != nil {
		t.Fatalf("first RegisterKey: %v", err)
	}
	if err := api.RegisterKey(sqlDB, "key-2", "agent-a", "second, should collide"); err == nil {
		t.Error("second RegisterKey for the same active agent-id: want error (unique constraint), got nil")
	}
}

func TestRevokeKey_ThenReRegister_Succeeds(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "key-1", "agent-a", "first"); err != nil {
		t.Fatalf("first RegisterKey: %v", err)
	}
	n, err := api.RevokeKey(sqlDB, "agent-a")
	if err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	if n != 1 {
		t.Errorf("RevokeKey rows affected = %d, want 1", n)
	}
	// Rotation: a fresh key for the same agent-id must now succeed, since
	// the old row is revoked and the partial unique index only covers
	// active (revoked_at IS NULL) rows.
	if err := api.RegisterKey(sqlDB, "key-2", "agent-a", "rotated"); err != nil {
		t.Fatalf("RegisterKey after revoke (rotation): %v", err)
	}
}

func TestRevokeKey_NoActiveKey_ReturnsZeroRowsNoError(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	n, err := api.RevokeKey(sqlDB, "agent-never-existed")
	if err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	if n != 0 {
		t.Errorf("RevokeKey rows affected for a nonexistent agent = %d, want 0", n)
	}
}

func TestListKeys_ReflectsActiveAndRevoked(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "key-a", "agent-a", "a"); err != nil {
		t.Fatalf("RegisterKey a: %v", err)
	}
	if err := api.RegisterKey(sqlDB, "key-b", "agent-b", "b"); err != nil {
		t.Fatalf("RegisterKey b: %v", err)
	}
	if _, err := api.RevokeKey(sqlDB, "agent-a"); err != nil {
		t.Fatalf("RevokeKey a: %v", err)
	}

	keys, err := api.ListKeys(sqlDB)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("len(keys) = %d, want 2", len(keys))
	}
	byAgent := map[string]api.KeyInfo{}
	for _, k := range keys {
		byAgent[k.AgentID] = k
	}
	if byAgent["agent-a"].RevokedAt == "" {
		t.Error("agent-a's key should show as revoked (non-empty RevokedAt)")
	}
	if byAgent["agent-b"].RevokedAt != "" {
		t.Error("agent-b's key should show as active (empty RevokedAt)")
	}
}

// ── APIKeyMiddleware ─────────────────────────────────────────────────────────

// probeHandler records the AgentIDFromContext result seen by the wrapped
// handler, so tests can assert on exactly what identity (if any) the
// middleware attached.
func probeHandler(gotAgentID *string, gotOK *bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*gotAgentID, *gotOK = api.AgentIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}
}

func TestAPIKeyMiddleware_DBBackedKey_ResolvesAgentIdentity(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "real-key-a", "agent-a", "a"); err != nil {
		t.Fatalf("RegisterKey: %v", err)
	}

	var gotAgentID string
	var gotOK bool
	mw := api.APIKeyMiddleware("", sqlDB, testLogger())
	h := mw(probeHandler(&gotAgentID, &gotOK))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "real-key-a")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !gotOK {
		t.Fatal("AgentIDFromContext: ok = false, want true for a DB-backed key")
	}
	if gotAgentID != "agent-a" {
		t.Errorf("resolved agent_id = %q, want %q", gotAgentID, "agent-a")
	}
}

func TestAPIKeyMiddleware_UnknownKey_Rejected(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	mw := api.APIKeyMiddleware("", sqlDB, testLogger())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not run for an unknown key")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "no-such-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestAPIKeyMiddleware_RevokedKey_Rejected(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "real-key-a", "agent-a", "a"); err != nil {
		t.Fatalf("RegisterKey: %v", err)
	}
	if _, err := api.RevokeKey(sqlDB, "agent-a"); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	mw := api.APIKeyMiddleware("", sqlDB, testLogger())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not run for a revoked key")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "real-key-a")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (revocation must take effect on the very next attempt)", rr.Code)
	}
}

func TestAPIKeyMiddleware_StaticKey_StillWorksWithNoBoundIdentity(t *testing.T) {
	sqlDB := openTestCollectorDB(t)

	var gotAgentID string
	var gotOK bool
	mw := api.APIKeyMiddleware("the-static-fleet-key", sqlDB, testLogger())
	h := mw(probeHandler(&gotAgentID, &gotOK))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "the-static-fleet-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (static-key path must be unaffected by B-073)", rr.Code)
	}
	if gotOK {
		t.Errorf("AgentIDFromContext: ok = true, want false — the legacy static key carries no bound identity, got agent_id=%q", gotAgentID)
	}
}

func TestAPIKeyMiddleware_StaticKeyConfigured_PerAgentKeyStillWorksToo(t *testing.T) {
	// Proves the two paths coexist: a deployment with a static key still
	// set can ALSO mint and use real per-agent keys (gradual migration),
	// not an all-or-nothing switch.
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "real-key-a", "agent-a", "a"); err != nil {
		t.Fatalf("RegisterKey: %v", err)
	}

	var gotAgentID string
	var gotOK bool
	mw := api.APIKeyMiddleware("the-static-fleet-key", sqlDB, testLogger())
	h := mw(probeHandler(&gotAgentID, &gotOK))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "real-key-a")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !gotOK || gotAgentID != "agent-a" {
		t.Errorf("resolved identity = (%q, %v), want (\"agent-a\", true)", gotAgentID, gotOK)
	}
}
