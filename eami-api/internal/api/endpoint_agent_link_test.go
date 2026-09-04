// endpoint_agent_link_test.go -- eami-api/internal/api
//
// Integration tests for B-164/B-165's identity-linkage fix, against a real
// Postgres:
//   - store.LinkEndpointToGatewayAgent / store.ResolveEndpointGatewayAgent
//     (endpoints.gateway_agent_id, B-164 -- the only write/read path for
//     that column). Exercised directly against the store rather than
//     through the JWT-gated PATCH /v1/endpoints/{endpointId}/link-agent
//     HTTP route: this package's machine-facing test envs all build the
//     server with authSvc=nil (matching ingest_paste_relay_test.go's own
//     convention), so there's no real *auth.Service here to mint a
//     validly-signed session token against. LinkEndpointAgent itself is a
//     thin, already-reviewed wrapper around exactly this store method
//     (parse params, call it, map its outcomes to 200/404/400) -- these
//     tests prove the real behavior that wrapper depends on. The full
//     HTTP path (a real logged-in admin session) is confirmed separately
//     by live verification (see BUILT.md).
//   - GET /v1/agents/{agent_id}/config (service-key-gated, B-165 -- the
//     route that never existed in this router until now; eami-agent's own
//     remote-config poll has been silently 404ing against this exact path
//     in production). This one IS exercised through the real HTTP route,
//     since it needs no JWT at all -- X-Service-Key only, matching this
//     package's other service-key tests.
//
// Follows ingest_paste_relay_test.go's own established pattern for tests
// that depend on GetDefaultOrgID ("oldest org"): resolves the server's
// real default org rather than assuming a disposable one, and cleans up
// only what each test itself creates.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run 'TestLinkEndpointToGatewayAgent|TestAgentRemoteConfig' -v
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/config"
	"github.com/eami/api/internal/store"
)

// ─── shared test env ────────────────────────────────────────────────────────

type endpointAgentLinkEnv struct {
	pool       *pgxpool.Pool
	queries    *store.Queries
	srv        *httptest.Server
	orgID      uuid.UUID // the server's actual GetDefaultOrgID result, asked for, not assumed
	serviceKey string
}

func newEndpointAgentLinkEnv(t *testing.T) *endpointAgentLinkEnv {
	t.Helper()
	dsn := pasteEventsTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(pool.Close)

	q := store.New(pool)

	orgID, err := q.GetDefaultOrgID(ctx)
	if err != nil {
		t.Skipf("skipping: no default org exists yet (run reseed.sql): %v", err)
	}

	const serviceKey = "test-service-key-endpoint-link"
	cfg := &config.Config{ServiceKey: serviceKey}
	s := api.NewServer(q, nil, nil, cfg)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return &endpointAgentLinkEnv{pool: pool, queries: q, srv: ts, orgID: orgID, serviceKey: serviceKey}
}

// insertEndpoint inserts a real endpoints row directly, returning its UUID.
// agentID is eami-agent's own free-text discovery identity (its config-file
// agent_id or hostname fallback), not a gateway_agents.id.
func (e *endpointAgentLinkEnv) insertEndpoint(t *testing.T, orgID uuid.UUID, agentID, hostname string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	// agent_version explicitly '' (not omitted -- schema leaves it nullable,
	// but the real ingest path, UpsertAgentEndpoint, always writes a real
	// Go string there, never SQL NULL, since rep.AgentVersion is a plain
	// string that defaults to "" when absent from a real report's JSON).
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO endpoints (id, org_id, agent_id, hostname, agent_version)
		VALUES ($1, $2, $3, $4, '')
	`, id, orgID, agentID, hostname); err != nil {
		t.Fatalf("insert endpoint: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM endpoints WHERE id = $1`, id)
	})
	return id
}

// insertGatewayAgent inserts a real gateway_agents row directly (the
// create_default_agent_config trigger auto-seeds its agent_configs row),
// returning its UUID.
func (e *endpointAgentLinkEnv) insertGatewayAgent(t *testing.T, orgID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, 'claude-sonnet-5', 'test@example.com', 'read:test')
	`, id, orgID, name); err != nil {
		t.Fatalf("insert gateway_agents: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM gateway_agents WHERE id = $1`, id)
	})
	return id
}

func (e *endpointAgentLinkEnv) getRemoteConfig(t *testing.T, serviceKey, agentID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.srv.URL+"/v1/agents/"+agentID+"/config", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if serviceKey != "" {
		req.Header.Set("X-Service-Key", serviceKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET remote config: %v", err)
	}
	return resp
}

// ─── AgentRemoteConfig (B-165): service-key-gated, no JWT involved ─────────

func TestAgentRemoteConfig_UnregisteredEndpoint_Returns404(t *testing.T) {
	env := newEndpointAgentLinkEnv(t)
	resp := env.getRemoteConfig(t, env.serviceKey, "never-seen-agent-id-"+uuid.NewString()[:8])
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (unregistered endpoint)", resp.StatusCode)
	}
}

func TestAgentRemoteConfig_RegisteredButUnlinked_Returns404(t *testing.T) {
	env := newEndpointAgentLinkEnv(t)
	agentID := "b165-unlinked-" + uuid.NewString()[:8]
	env.insertEndpoint(t, env.orgID, agentID, "unlinked-host")

	resp := env.getRemoteConfig(t, env.serviceKey, agentID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (endpoint exists but isn't linked to a governed agent)", resp.StatusCode)
	}
}

// TestAgentRemoteConfig_Linked_ReturnsRealConfig is B-165's centerpiece:
// this is the exact request eami-agent's FetchConfig has been making
// (through eami-collector's proxy) and silently receiving a 404 for since
// this path was built -- the first time it gets back a real 200 with a
// real config body.
func TestAgentRemoteConfig_Linked_ReturnsRealConfig(t *testing.T) {
	env := newEndpointAgentLinkEnv(t)
	agentID := "b165-linked-" + uuid.NewString()[:8]
	endpointID := env.insertEndpoint(t, env.orgID, agentID, "linked-host")
	gatewayAgentID := env.insertGatewayAgent(t, env.orgID, "b165-governed-"+uuid.NewString()[:8])

	if err := env.queries.LinkEndpointToGatewayAgent(context.Background(), store.LinkEndpointToGatewayAgentParams{
		EndpointID:     endpointID,
		OrgID:          env.orgID,
		GatewayAgentID: &gatewayAgentID,
	}); err != nil {
		t.Fatalf("LinkEndpointToGatewayAgent: %v", err)
	}

	resp := env.getRemoteConfig(t, env.serviceKey, agentID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (linked, real config expected)", resp.StatusCode)
	}

	var got struct {
		ScanIntervalSeconds int      `json:"scan_interval_seconds"`
		ModelScanPaths      []string `json:"model_scan_paths"`
		MaxReportSizeBytes  int      `json:"max_report_size_bytes"`
		EnabledScanners     []string `json:"enabled_scanners"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ScanIntervalSeconds != int(store.AgentConfigDefaults.ScanIntervalSeconds) {
		t.Errorf("scan_interval_seconds = %d, want the real default %d", got.ScanIntervalSeconds, store.AgentConfigDefaults.ScanIntervalSeconds)
	}
	if len(got.EnabledScanners) == 0 {
		t.Error("enabled_scanners is empty, want the real default scanner list")
	}
}

func TestAgentRemoteConfig_MissingServiceKey_Returns401(t *testing.T) {
	env := newEndpointAgentLinkEnv(t)
	resp := env.getRemoteConfig(t, "", "whatever-agent-id")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (no X-Service-Key)", resp.StatusCode)
	}
}

func TestAgentRemoteConfig_WrongServiceKey_Returns401(t *testing.T) {
	env := newEndpointAgentLinkEnv(t)
	resp := env.getRemoteConfig(t, "definitely-not-the-real-key", "whatever-agent-id")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (wrong X-Service-Key)", resp.StatusCode)
	}
}

// ─── LinkEndpointToGatewayAgent (store layer): set/clear/validation ────────

// TestLinkEndpointToGatewayAgent_SetThenClear is AC1's real, working trace:
// given a real endpoint's discovery data and a real gateway_agents
// identity, link them, then confirm the link (and the linked agent's real
// name) is genuinely readable back through GetAgentEndpoint -- the exact
// scenario the investigation found impossible before this fix.
func TestLinkEndpointToGatewayAgent_SetThenClear(t *testing.T) {
	env := newEndpointAgentLinkEnv(t)
	agentID := "b164-link-" + uuid.NewString()[:8]
	endpointID := env.insertEndpoint(t, env.orgID, agentID, "link-host")
	gatewayAgentID := env.insertGatewayAgent(t, env.orgID, "b164-agent-"+uuid.NewString()[:8])

	ctx := context.Background()
	if err := env.queries.LinkEndpointToGatewayAgent(ctx, store.LinkEndpointToGatewayAgentParams{
		EndpointID: endpointID, OrgID: env.orgID, GatewayAgentID: &gatewayAgentID,
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	got, err := env.queries.GetAgentEndpoint(ctx, endpointID, env.orgID)
	if err != nil {
		t.Fatalf("GetAgentEndpoint: %v", err)
	}
	if got.GatewayAgentID == nil || *got.GatewayAgentID != gatewayAgentID {
		t.Fatalf("GatewayAgentID = %v, want %v", got.GatewayAgentID, gatewayAgentID)
	}
	if got.GatewayAgentName == nil || *got.GatewayAgentName == "" {
		t.Error("GatewayAgentName is empty, want the real linked agent's name")
	}

	// Clear it.
	if err := env.queries.LinkEndpointToGatewayAgent(ctx, store.LinkEndpointToGatewayAgentParams{
		EndpointID: endpointID, OrgID: env.orgID, GatewayAgentID: nil,
	}); err != nil {
		t.Fatalf("clear link: %v", err)
	}
	got2, err := env.queries.GetAgentEndpoint(ctx, endpointID, env.orgID)
	if err != nil {
		t.Fatalf("GetAgentEndpoint (after clear): %v", err)
	}
	if got2.GatewayAgentID != nil {
		t.Errorf("GatewayAgentID after clear = %v, want nil", got2.GatewayAgentID)
	}
}

func TestLinkEndpointToGatewayAgent_UnknownEndpoint_ReturnsError(t *testing.T) {
	env := newEndpointAgentLinkEnv(t)
	gatewayAgentID := env.insertGatewayAgent(t, env.orgID, "b164-agent2-"+uuid.NewString()[:8])

	err := env.queries.LinkEndpointToGatewayAgent(context.Background(), store.LinkEndpointToGatewayAgentParams{
		EndpointID: uuid.New(), OrgID: env.orgID, GatewayAgentID: &gatewayAgentID,
	})
	if err == nil {
		t.Fatal("expected an error for a nonexistent endpoint, got nil")
	}
}

func TestLinkEndpointToGatewayAgent_UnknownGatewayAgent_ReturnsErrGatewayAgentNotFound(t *testing.T) {
	env := newEndpointAgentLinkEnv(t)
	agentID := "b164-link3-" + uuid.NewString()[:8]
	endpointID := env.insertEndpoint(t, env.orgID, agentID, "link3-host")
	bogus := uuid.New()

	err := env.queries.LinkEndpointToGatewayAgent(context.Background(), store.LinkEndpointToGatewayAgentParams{
		EndpointID: endpointID, OrgID: env.orgID, GatewayAgentID: &bogus,
	})
	if err != store.ErrGatewayAgentNotFound {
		t.Fatalf("err = %v, want store.ErrGatewayAgentNotFound", err)
	}
}

// TestLinkEndpointToGatewayAgent_CrossOrg_Rejected proves an endpoint in
// one org can never be linked to a different org's gateway agent, even by
// a caller who somehow knows both real UUIDs -- the existence check is
// itself org-scoped, so a cross-org attempt is indistinguishable from
// "that agent doesn't exist at all."
func TestLinkEndpointToGatewayAgent_CrossOrg_Rejected(t *testing.T) {
	env := newEndpointAgentLinkEnv(t)
	agentID := "b164-crossorg-" + uuid.NewString()[:8]
	endpointID := env.insertEndpoint(t, env.orgID, agentID, "crossorg-host")

	otherOrgID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		otherOrgID, "b164-other-org-"+otherOrgID.String()[:8], "b164-other-org-"+otherOrgID.String()); err != nil {
		t.Fatalf("insert other org: %v", err)
	}
	t.Cleanup(func() { _, _ = env.pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, otherOrgID) })
	otherOrgAgentID := env.insertGatewayAgent(t, otherOrgID, "b164-other-org-agent")

	err := env.queries.LinkEndpointToGatewayAgent(context.Background(), store.LinkEndpointToGatewayAgentParams{
		EndpointID: endpointID, OrgID: env.orgID, GatewayAgentID: &otherOrgAgentID,
	})
	if err != store.ErrGatewayAgentNotFound {
		t.Fatalf("err = %v, want store.ErrGatewayAgentNotFound (cross-org agent must be indistinguishable from nonexistent)", err)
	}
}
