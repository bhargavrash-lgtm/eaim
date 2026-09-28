package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/config"
	"github.com/eami/api/internal/store"
)

// B-236: both agent-config read paths used to serve defaults (all scanners
// on) on ANY database error, silently undoing an admin's deliberate config.
// These tests force a REAL Postgres error on one targeted read -- faultDB
// rewrites that one query to `SELECT 1/0`, so the shared database itself
// returns division_by_zero (SQLSTATE 22012); nothing is taken down -- and
// assert the response is a generic error, never defaults. A genuinely
// missing config row (created deliberately: the gateway_agents insert
// trigger normally prevents it) must still yield defaults.

// faultDB forwards to the real pool, except QueryRow calls whose SQL
// contains a configured marker, which are replaced with another statement.
type faultDB struct {
	pool *pgxpool.Pool
	mu   sync.Mutex
	rule map[string]string // SQL marker -> replacement SQL
}

func (f *faultDB) set(marker, replacement string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rule = map[string]string{}
	if marker != "" {
		f.rule[marker] = replacement
	}
}

func (f *faultDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return f.pool.Exec(ctx, sql, args...)
}

func (f *faultDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return f.pool.Query(ctx, sql, args...)
}

func (f *faultDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	f.mu.Lock()
	for marker, replacement := range f.rule {
		if strings.Contains(sql, marker) {
			f.mu.Unlock()
			// Replacement statements take no parameters.
			return f.pool.QueryRow(ctx, replacement)
		}
	}
	f.mu.Unlock()
	return f.pool.QueryRow(ctx, sql, args...)
}

const (
	markerAgentConfig = "FROM agent_configs"
	markerGetAgent    = "-- name: GetAgent :one"
	markerDefaultOrg  = "FROM orgs ORDER BY created_at ASC LIMIT 1"
	realDBError       = "SELECT 1/0"
	genericLoadError  = "{\"code\":\"internal_error\",\"message\":\"failed to load agent config\"}\n"
)

type configReadResp struct {
	ScanIntervalSeconds int      `json:"scan_interval_seconds"`
	EnabledScanners     []string `json:"enabled_scanners"`
}

func readConfigResp(t *testing.T, resp *http.Response) (int, string, configReadResp) {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var c configReadResp
	_ = json.Unmarshal(b, &c)
	return resp.StatusCode, string(b), c
}

// setDistinctiveConfig gives the agent a config that cannot be mistaken for
// the defaults: one scanner only, and a non-default interval.
func setDistinctiveConfig(t *testing.T, pool *pgxpool.Pool, agentID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE agent_configs SET scan_interval_seconds = 777, enabled_scanners = ARRAY['ai_apps'] WHERE agent_id = $1`, agentID); err != nil {
		t.Fatalf("set distinctive config: %v", err)
	}
}

func assertIsError(t *testing.T, what string, status int, body string, want int) {
	t.Helper()
	if status != want || (want == http.StatusInternalServerError && body != genericLoadError) {
		t.Fatalf("%s: %d %q, want %d generic error (never defaults)", what, status, body, want)
	}
	for _, leak := range []string{"division", "22012", "SQLSTATE"} {
		if strings.Contains(body, leak) {
			t.Fatalf("%s: response leaks DB text %q: %s", what, leak, body)
		}
	}
}

func TestAgentRemoteConfig_DBErrorIsNotDefaults_RealDB(t *testing.T) {
	env := newEndpointAgentLinkEnv(t) // real pool (closed via t.Cleanup first), default org, helpers
	fdb := &faultDB{pool: env.pool}
	const serviceKey = "test-service-key-b236"
	ts := httptest.NewServer(api.NewServer(store.New(fdb), nil, nil, &config.Config{ServiceKey: serviceKey}).Handler())
	t.Cleanup(ts.Close)
	get := func(agent string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/agents/"+agent+"/config", nil)
		req.Header.Set("X-Service-Key", serviceKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	agentID := "b236-remote-" + uuid.NewString()[:8]
	endpointID := env.insertEndpoint(t, env.orgID, agentID, "b236-host")
	gw := env.insertGatewayAgent(t, env.orgID, "b236-governed-"+uuid.NewString()[:8])
	if err := env.queries.LinkEndpointToGatewayAgent(context.Background(), store.LinkEndpointToGatewayAgentParams{EndpointID: endpointID, OrgID: env.orgID, GatewayAgentID: &gw}); err != nil {
		t.Fatalf("link: %v", err)
	}
	setDistinctiveConfig(t, env.pool, gw)

	// Control: the real, deliberately configured settings are served.
	if status, body, c := readConfigResp(t, get(agentID)); status != 200 || c.ScanIntervalSeconds != 777 || len(c.EnabledScanners) != 1 {
		t.Fatalf("control: %d %s, want the real config (777s, [ai_apps])", status, body)
	}

	// A real DB error reading the config row: an error, not defaults.
	fdb.set(markerAgentConfig, realDBError)
	status, body, _ := readConfigResp(t, get(agentID))
	assertIsError(t, "config read DB error", status, body, http.StatusInternalServerError)

	// A real DB error resolving the default org: a 500, not "no org".
	fdb.set(markerDefaultOrg, realDBError)
	status, body, _ = readConfigResp(t, get(agentID))
	assertIsError(t, "default-org DB error", status, body, http.StatusInternalServerError)

	// A genuinely empty org lookup is still the 503 setup hint.
	fdb.set(markerDefaultOrg, "SELECT id FROM orgs WHERE false")
	if status, body, _ = readConfigResp(t, get(agentID)); status != http.StatusServiceUnavailable || !strings.Contains(body, "no_org") {
		t.Fatalf("no org at all: %d %s, want 503 no_org", status, body)
	}
	fdb.set("", "")

	// A genuinely missing config row (deleted deliberately; the insert
	// trigger normally prevents this) still yields defaults.
	if _, err := env.pool.Exec(context.Background(), `DELETE FROM agent_configs WHERE agent_id = $1`, gw); err != nil {
		t.Fatal(err)
	}
	if status, body, c := readConfigResp(t, get(agentID)); status != 200 || c.ScanIntervalSeconds != int(store.AgentConfigDefaults.ScanIntervalSeconds) {
		t.Fatalf("missing row: %d %s, want 200 with defaults", status, body)
	}
}

func TestAdminGetAgentConfig_DBErrorIsNotDefaults_RealDB(t *testing.T) {
	env := newEndpointAgentLinkEnv(t) // for its pool; the admin route needs no default org
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b236-admin")
	userID := seedTestUser(t, ctx, env.pool, orgID)
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@b236.test", "admin")
	if err != nil {
		t.Fatal(err)
	}
	fdb := &faultDB{pool: env.pool}
	ts := httptest.NewServer(api.NewServer(store.New(fdb), authSvc, nil, &config.Config{ServiceKey: "unused-b236"}).Handler())
	t.Cleanup(ts.Close)
	get := func(id uuid.UUID) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/gateway/agents/"+id.String()+"/config", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	agent := env.insertGatewayAgent(t, orgID, "b236-admin-agent-"+uuid.NewString()[:8])
	setDistinctiveConfig(t, env.pool, agent)

	if status, body, c := readConfigResp(t, get(agent)); status != 200 || c.ScanIntervalSeconds != 777 || len(c.EnabledScanners) != 1 {
		t.Fatalf("control: %d %s, want the real config", status, body)
	}

	// A real DB error on the ownership lookup: a 500, not "agent not found".
	fdb.set(markerGetAgent, realDBError)
	status, body, _ := readConfigResp(t, get(agent))
	assertIsError(t, "agent lookup DB error", status, body, http.StatusInternalServerError)

	// A real DB error reading the config row: an error, not defaults.
	fdb.set(markerAgentConfig, realDBError)
	status, body, _ = readConfigResp(t, get(agent))
	assertIsError(t, "config read DB error", status, body, http.StatusInternalServerError)
	fdb.set("", "")

	// A genuinely unknown agent is still a 404.
	if status, body, _ := readConfigResp(t, get(uuid.New())); status != http.StatusNotFound {
		t.Fatalf("unknown agent: %d %s, want 404", status, body)
	}

	// A genuinely missing config row still yields defaults.
	if _, err := env.pool.Exec(ctx, `DELETE FROM agent_configs WHERE agent_id = $1`, agent); err != nil {
		t.Fatal(err)
	}
	if status, body, c := readConfigResp(t, get(agent)); status != 200 || c.ScanIntervalSeconds != int(store.AgentConfigDefaults.ScanIntervalSeconds) {
		t.Fatalf("missing row: %d %s, want 200 with defaults", status, body)
	}
}
