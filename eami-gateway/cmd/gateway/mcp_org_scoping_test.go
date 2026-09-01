// mcp_org_scoping_test.go -- cmd/gateway
//
// B-141's centerpiece proof, dispatch/tool-call auth leg: two real orgs,
// each with an agent sharing the IDENTICAL name, two real minted JWTs
// (each carrying its own real org_id claim). ServeSSE must resolve each
// token to ITS OWN org's agent -- never the other's -- confirmed by
// capturing the real mcp.ActionContext a real tool_call dispatch receives,
// not merely a status code.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./cmd/gateway/... -run TestServeSSE_OrgScoping -v
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/mcp"
	"github.com/eami/gateway/internal/registry"
)

func TestServeSSE_OrgScoping_TwoOrgsSameAgentName_ResolvesCorrectAgentEachTime(t *testing.T) {
	env := newMainTestEnv(t) // org A
	ctx := context.Background()

	orgB := uuid.New()
	if _, err := env.pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgB, "b141-sse-org-b-"+orgB.String()[:8], "b141-sse-org-b-"+orgB.String()); err != nil {
		t.Fatalf("insert org B: %v", err)
	}
	t.Cleanup(func() { env.pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgB) })

	const sharedName = "shared-sse-agent"
	agentA := uuid.New()
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, 'test-model', 'test-owner', 'test scope')`,
		agentA, env.orgID, sharedName); err != nil {
		t.Fatalf("insert org A agent: %v", err)
	}
	agentB := uuid.New()
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, 'test-model', 'test-owner', 'test scope')`,
		agentB, orgB, sharedName); err != nil {
		t.Fatalf("insert org B agent: %v", err)
	}

	called := make(chan mcp.ActionContext, 1)
	dispatch := func(ctx context.Context, ac mcp.ActionContext) (json.RawMessage, error) {
		called <- ac
		return json.RawMessage(`{"ok":true}`), nil
	}

	idm, err := identity.NewManager(filepath.Join(t.TempDir(), "gateway.key"), 300, "eami-gateway:primary")
	if err != nil {
		t.Fatalf("identity.NewManager: %v", err)
	}
	reg := registry.New(env.pool)
	h := mcp.NewHandler(idm, reg, dispatch, func(ctx context.Context, orgID string) ([]mcp.ToolDefinition, error) {
		return nil, nil
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/mcp/sse", h.ServeSSE)
	mux.HandleFunc("/v1/mcp/messages", h.ServeMessages)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	tokenA, err := idm.Issue(identity.IssueRequest{AgentID: "agent:" + sharedName, OrgID: env.orgID.String(), TTLSeconds: 300})
	if err != nil {
		t.Fatalf("issue org A token: %v", err)
	}
	tokenB, err := idm.Issue(identity.IssueRequest{AgentID: "agent:" + sharedName, OrgID: orgB.String(), TTLSeconds: 300})
	if err != nil {
		t.Fatalf("issue org B token: %v", err)
	}

	// Reuses toolsListTestEnv's openSSE/postMessage methods (defined in
	// main_tools_list_pg_test.go, same package) -- only .server and .token
	// are consulted by those methods, so two values sharing one real
	// server but distinct tokens correctly represent two different
	// callers hitting the same running gateway process.
	envA := &toolsListTestEnv{mainTestEnv: env, server: server, agentName: sharedName, token: tokenA.Token}
	envB := &toolsListTestEnv{mainTestEnv: env, server: server, agentName: sharedName, token: tokenB.Token}

	doCall := func(e *toolsListTestEnv) mcp.ActionContext {
		t.Helper()
		sessionID, _, reader := e.openSSE(t)
		e.postMessage(t, sessionID, `{"jsonrpc":"2.0","id":1,"method":"tool_call","params":{"name":"probe/action","arguments":{}}}`)
		f := readSSEFrame(t, reader)
		if f.Event != "message" {
			t.Fatalf("event = %q, want message (data=%q)", f.Event, f.Data)
		}
		select {
		case ac := <-called:
			return ac
		default:
			t.Fatal("dispatch was never called for a real tool_call request")
			return mcp.ActionContext{}
		}
	}

	acA := doCall(envA)
	if acA.OrgID != env.orgID.String() || acA.AgentUUID != agentA.String() {
		t.Errorf("org A's tool_call: OrgID=%s AgentUUID=%s, want OrgID=%s AgentUUID=%s -- "+
			"org A's own agent, never org B's identically-named one", acA.OrgID, acA.AgentUUID, env.orgID, agentA)
	}

	acB := doCall(envB)
	if acB.OrgID != orgB.String() || acB.AgentUUID != agentB.String() {
		t.Errorf("org B's tool_call: OrgID=%s AgentUUID=%s, want OrgID=%s AgentUUID=%s -- "+
			"org B's own agent, never org A's identically-named one", acB.OrgID, acB.AgentUUID, orgB, agentB)
	}
}

// TestServeSSE_PreCutoverToken_ReturnsUnauthorized (B-141): a token minted
// before Claims gained OrgID is rejected outright, before the registry is
// ever consulted.
func TestServeSSE_PreCutoverToken_ReturnsUnauthorized(t *testing.T) {
	env := newMainTestEnv(t)
	agentID, agentName := env.insertAgent(t)
	_ = agentID

	idm, err := identity.NewManager(filepath.Join(t.TempDir(), "gateway.key"), 300, "eami-gateway:primary")
	if err != nil {
		t.Fatalf("identity.NewManager: %v", err)
	}
	reg := registry.New(env.pool)
	h := mcp.NewHandler(idm, reg, func(ctx context.Context, ac mcp.ActionContext) (json.RawMessage, error) {
		t.Fatal("dispatch must never be reached for a pre-cutover token")
		return nil, nil
	}, func(ctx context.Context, orgID string) ([]mcp.ToolDefinition, error) { return nil, nil })

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/mcp/sse", h.ServeSSE)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	resp, err := idm.Issue(identity.IssueRequest{AgentID: "agent:" + agentName, TTLSeconds: 300}) // no OrgID
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/mcp/sse", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+resp.Token)
	client := &http.Client{Timeout: 5 * time.Second}
	got, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/mcp/sse: %v", err)
	}
	defer got.Body.Close()

	if got.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 -- a pre-cutover token (no org_id claim) must be rejected outright", got.StatusCode)
	}
}
