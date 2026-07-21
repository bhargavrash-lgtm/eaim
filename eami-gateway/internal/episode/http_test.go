// http_test.go — eami-gateway/internal/episode
// Unit tests for the dual-auth HTTP handler. First use of httptest in this
// module (net/http/httptest is stdlib-only and already established in
// eami-api's tests; introducing it here is low-risk). Uses fakeStore from
// reader_test.go (same package) plus a fake AgentResolver and a real
// identity.Manager backed by a temp-dir RSA key, mirroring tokens_test.go's
// newManager helper.
package episode_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/episode"
	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/registry"
)

const testServiceKey = "test-episode-read-service-key"

// fakeResolver is a hand-rolled episode.AgentResolver double.
type fakeResolver struct {
	records map[string]*registry.AgentRecord
	err     error // when set, returned regardless of records (mirrors registry's suspended/not-found contract)
}

func (f *fakeResolver) LookupByName(_ context.Context, name string) (*registry.AgentRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	rec, ok := f.records[name]
	if !ok {
		return nil, errors.New("not found: " + name)
	}
	return rec, nil
}

func newTestManager(t *testing.T) *identity.Manager {
	t.Helper()
	keyPath := filepath.Join(t.TempDir(), "gateway.key")
	m, err := identity.NewManager(keyPath, 300, "eami-gateway:primary")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

// issueBearerFor issues a token whose subject is "agent:<agentName>",
// matching the "agent:<name>" convention documented in internal/registry
// and used by internal/mcp/handler.go's buildActionContext.
func issueBearerFor(t *testing.T, m *identity.Manager, agentName string) string {
	t.Helper()
	resp, err := m.Issue(identity.IssueRequest{AgentID: "agent:" + agentName, TTLSeconds: 300})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return resp.Token
}

func newHandler(t *testing.T, store *fakeStore, resolver *fakeResolver, idm *identity.Manager) *episode.Handler {
	t.Helper()
	reader := episode.NewReaderWithStore(store)
	return episode.NewHTTPHandler(reader, idm, resolver, testServiceKey)
}

// ─── Service-key path ──────────────────────────────────────────────────────

func TestHandler_ListEpisodes_ValidServiceKey_ReturnsEpisodes(t *testing.T) {
	store := &fakeStore{listEpisodes: []episode.Episode{{ID: uuid.New()}}, listTotal: 1}
	h := newHandler(t, store, &fakeResolver{}, newTestManager(t))

	orgID := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes?org_id="+orgID.String(), nil)
	req.Header.Set("X-Service-Key", testServiceKey)
	rec := httptest.NewRecorder()

	h.ListEpisodes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []episode.Episode `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Data) != 1 {
		t.Errorf("got %d episodes, want 1", len(body.Data))
	}
}

func TestHandler_ListEpisodes_MissingAuth_ReturnsUnauthorized(t *testing.T) {
	h := newHandler(t, &fakeStore{}, &fakeResolver{}, newTestManager(t))

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes", nil)
	rec := httptest.NewRecorder()

	h.ListEpisodes(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestHandler_ListEpisodes_WrongServiceKey_ReturnsUnauthorized(t *testing.T) {
	h := newHandler(t, &fakeStore{}, &fakeResolver{}, newTestManager(t))

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes?org_id="+uuid.New().String(), nil)
	req.Header.Set("X-Service-Key", "not-the-right-key")
	rec := httptest.NewRecorder()

	h.ListEpisodes(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestHandler_ListEpisodes_ServiceKeyMissingOrgID_ReturnsBadRequest(t *testing.T) {
	h := newHandler(t, &fakeStore{}, &fakeResolver{}, newTestManager(t))

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes", nil)
	req.Header.Set("X-Service-Key", testServiceKey)
	rec := httptest.NewRecorder()

	h.ListEpisodes(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_ListEpisodes_ServiceKeyMalformedOrgID_ReturnsBadRequest(t *testing.T) {
	h := newHandler(t, &fakeStore{}, &fakeResolver{}, newTestManager(t))

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes?org_id=not-a-uuid", nil)
	req.Header.Set("X-Service-Key", testServiceKey)
	rec := httptest.NewRecorder()

	h.ListEpisodes(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// ─── Bearer-JWT path ────────────────────────────────────────────────────────

func TestHandler_ListEpisodes_ValidBearerJWT_ResolvesOrgFromRegistry(t *testing.T) {
	orgID := uuid.New()
	resolver := &fakeResolver{records: map[string]*registry.AgentRecord{
		"test-agent": {ID: uuid.New().String(), OrgID: orgID.String(), Name: "test-agent", Status: "active"},
	}}
	store := &fakeStore{}
	idm := newTestManager(t)
	h := newHandler(t, store, resolver, idm)

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes", nil)
	req.Header.Set("Authorization", "Bearer "+issueBearerFor(t, idm, "test-agent"))
	rec := httptest.NewRecorder()

	h.ListEpisodes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Confirms the store was actually queried (org resolution didn't
	// short-circuit before reaching it) with the default pagination.
	if store.gotLimit != 25 || store.gotOffset != 0 {
		t.Errorf("store queried with limit=%d offset=%d, want defaults 25/0", store.gotLimit, store.gotOffset)
	}
}

func TestHandler_ListEpisodes_BearerJWT_UnknownAgent_ReturnsForbidden(t *testing.T) {
	idm := newTestManager(t)
	h := newHandler(t, &fakeStore{}, &fakeResolver{}, idm)

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes", nil)
	req.Header.Set("Authorization", "Bearer "+issueBearerFor(t, idm, "ghost-agent"))
	rec := httptest.NewRecorder()

	h.ListEpisodes(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestHandler_ListEpisodes_BearerJWT_SuspendedAgent_ReturnsForbidden(t *testing.T) {
	idm := newTestManager(t)
	resolver := &fakeResolver{err: registry.ErrAgentSuspended}
	h := newHandler(t, &fakeStore{}, resolver, idm)

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes", nil)
	req.Header.Set("Authorization", "Bearer "+issueBearerFor(t, idm, "suspended-agent"))
	rec := httptest.NewRecorder()

	h.ListEpisodes(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

// TestHandler_ListEpisodes_BearerJWT_ClientSuppliedOrgIDIgnored_UsesRegistryOrg
// is the security-critical case for this brief's trust model: a forged
// org_id query param must never override the registry-resolved org on the
// bearer path (only the service-key path trusts a client-supplied org_id).
func TestHandler_ListEpisodes_BearerJWT_ClientSuppliedOrgIDIgnored_UsesRegistryOrg(t *testing.T) {
	realOrg := uuid.New()
	forgedOrg := uuid.New()
	resolver := &fakeResolver{records: map[string]*registry.AgentRecord{
		"test-agent": {ID: uuid.New().String(), OrgID: realOrg.String(), Name: "test-agent", Status: "active"},
	}}

	store := &captureOrgStore{fakeStore: &fakeStore{}}
	idm := newTestManager(t)
	reader := episode.NewReaderWithStore(store)
	h := episode.NewHTTPHandler(reader, idm, resolver, testServiceKey)

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes?org_id="+forgedOrg.String(), nil)
	req.Header.Set("Authorization", "Bearer "+issueBearerFor(t, idm, "test-agent"))
	rec := httptest.NewRecorder()

	h.ListEpisodes(rec, req)

	if store.gotOrgID != realOrg {
		t.Errorf("query executed with org_id = %s, want registry-resolved %s (forged param %s must be ignored)",
			store.gotOrgID, realOrg, forgedOrg)
	}
}

func TestHandler_ListEpisodes_BothHeadersPresent_ServiceKeyTakesPrecedence(t *testing.T) {
	// A bearer token for an agent that does NOT exist in the resolver — if the
	// bearer path were mistakenly consulted, this would 403. Service-key
	// success proves precedence held.
	orgID := uuid.New()
	h := newHandler(t, &fakeStore{}, &fakeResolver{}, newTestManager(t))

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes?org_id="+orgID.String(), nil)
	req.Header.Set("X-Service-Key", testServiceKey)
	req.Header.Set("Authorization", "Bearer not-even-a-real-jwt")
	rec := httptest.NewRecorder()

	h.ListEpisodes(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (service key must win over the bogus bearer header)", rec.Code)
	}
}

// ─── GetEpisode ─────────────────────────────────────────────────────────────

func TestHandler_GetEpisode_ValidServiceKey_ReturnsFullSteps(t *testing.T) {
	steps := json.RawMessage(`[{"tool_name":"salesforce","action":"delete_records","params":{"id":"001"},"decision":"allowed"}]`)
	store := &fakeStore{getEpisode: &episode.Episode{ID: uuid.New(), Steps: steps, Outcome: "success"}}
	h := newHandler(t, store, &fakeResolver{}, newTestManager(t))

	id := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes/"+id.String()+"?org_id="+uuid.New().String(), nil)
	req.SetPathValue("id", id.String())
	req.Header.Set("X-Service-Key", testServiceKey)
	rec := httptest.NewRecorder()

	h.GetEpisode(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got episode.Episode
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Steps) == 0 {
		t.Fatal("response Steps is empty — the regression check for ADR-019/ADR-010 failed")
	}
}

func TestHandler_GetEpisode_CrossOrgID_ReturnsNotFound(t *testing.T) {
	// The store itself enforces (id, org_id) scoping; a fake configured to
	// return ErrNotFound stands in for "episode exists but belongs to a
	// different org" — the handler must surface this as 404, not 403, so a
	// cross-org probe can't distinguish "wrong org" from "doesn't exist".
	store := &fakeStore{getErr: episode.ErrNotFound}
	h := newHandler(t, store, &fakeResolver{}, newTestManager(t))

	id := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes/"+id.String()+"?org_id="+uuid.New().String(), nil)
	req.SetPathValue("id", id.String())
	req.Header.Set("X-Service-Key", testServiceKey)
	rec := httptest.NewRecorder()

	h.GetEpisode(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (not 403 — must not leak existence across orgs)", rec.Code)
	}
}

func TestHandler_GetEpisode_UnknownID_ReturnsNotFound(t *testing.T) {
	store := &fakeStore{getErr: episode.ErrNotFound}
	h := newHandler(t, store, &fakeResolver{}, newTestManager(t))

	id := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes/"+id.String()+"?org_id="+uuid.New().String(), nil)
	req.SetPathValue("id", id.String())
	req.Header.Set("X-Service-Key", testServiceKey)
	rec := httptest.NewRecorder()

	h.GetEpisode(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// ─── SearchEpisodes ─────────────────────────────────────────────────────────

func TestHandler_SearchEpisodes_EmptyQuery_ReturnsBadRequest(t *testing.T) {
	h := newHandler(t, &fakeStore{}, &fakeResolver{}, newTestManager(t))

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes/search?org_id="+uuid.New().String(), nil)
	req.Header.Set("X-Service-Key", testServiceKey)
	rec := httptest.NewRecorder()

	h.SearchEpisodes(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_SearchEpisodes_ValidServiceKey_ReturnsMatches(t *testing.T) {
	store := &fakeStore{searchEpisodes: []episode.Episode{{ID: uuid.New()}}}
	h := newHandler(t, store, &fakeResolver{}, newTestManager(t))

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes/search?q=salesforce&org_id="+uuid.New().String(), nil)
	req.Header.Set("X-Service-Key", testServiceKey)
	rec := httptest.NewRecorder()

	h.SearchEpisodes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if store.gotQuery != "salesforce" {
		t.Errorf("gotQuery = %q, want %q", store.gotQuery, "salesforce")
	}
}

// ─── Method enforcement ─────────────────────────────────────────────────────

func TestHandler_AnyEndpoint_NonGETMethod_ReturnsMethodNotAllowed(t *testing.T) {
	h := newHandler(t, &fakeStore{}, &fakeResolver{}, newTestManager(t))

	req := httptest.NewRequest(http.MethodPost, "/v1/gateway/episodes", nil)
	req.Header.Set("X-Service-Key", testServiceKey)
	rec := httptest.NewRecorder()

	h.ListEpisodes(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

// ─── org_id-capturing store (for the forged-param test) ────────────────────

// captureOrgStore wraps fakeStore and records the orgID it was actually
// queried with, so the forged-param test can assert on it directly rather
// than inferring from response contents.
type captureOrgStore struct {
	*fakeStore
	gotOrgID uuid.UUID
}

func (c *captureOrgStore) ListByOrg(ctx context.Context, orgID uuid.UUID, outcome string, limit, offset int) ([]episode.Episode, int64, error) {
	c.gotOrgID = orgID
	return c.fakeStore.ListByOrg(ctx, orgID, outcome, limit, offset)
}
