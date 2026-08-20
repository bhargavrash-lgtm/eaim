// main_tools_list_pg_test.go -- cmd/gateway
//
// Integration tests for B-061 (real tools/list support) against a REAL
// Postgres, reusing main_pg_test.go's mainTestEnv/insertTool helpers.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./cmd/gateway/... -run TestListGatewayTools -v
//	go test ./cmd/gateway/... -run TestToolsList -v
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/mcp"
	"github.com/eami/gateway/internal/registry"
	"github.com/eami/gateway/internal/toolrouter"
)

// insertToolWithActions inserts a rest_api gateway_tools row with a real
// action_paths JSONB map, so listGatewayTools has real per-action data to
// enumerate (mirrors insertTool's shape in main_pg_test.go, extended with
// the one extra column this brief's query reads).
func (e *mainTestEnv) insertToolWithActions(t *testing.T, orgID uuid.UUID, name string, actions map[string]toolrouter.ActionPathEntry) {
	t.Helper()
	raw, err := json.Marshal(actions)
	if err != nil {
		t.Fatalf("marshal action_paths: %v", err)
	}
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, action_paths)
		VALUES ($1, $2, $3, 'rest_api', 'api_key', $4)
	`, uuid.New(), orgID, name, raw); err != nil {
		t.Fatalf("insert gateway_tools row with action_paths: %v", err)
	}
}

// ─── listGatewayTools unit-shape tests ─────────────────────────────────────

// TestListGatewayTools_RestAPI_OneEntryPerAction proves AC1: a rest_api
// tool with two named actions yields exactly two ToolDefinitions, each
// carrying the real stored method/path in its description, not fabricated
// data.
func TestListGatewayTools_RestAPI_OneEntryPerAction(t *testing.T) {
	env := newMainTestEnv(t)
	env.insertToolWithActions(t, env.orgID, "crm", map[string]toolrouter.ActionPathEntry{
		"create_contact": {Path: "/contacts", Method: "POST"},
		"get_contact":    {Path: "/contacts/{id}", Method: "GET"},
	})

	defs, err := listGatewayTools(context.Background(), env.pool, env.orgID.String())
	if err != nil {
		t.Fatalf("listGatewayTools: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("len(defs) = %d, want 2: %+v", len(defs), defs)
	}
	byName := map[string]mcp.ToolDefinition{}
	for _, d := range defs {
		byName[d.Name] = d
	}
	create, ok := byName["crm/create_contact"]
	if !ok {
		t.Fatalf("expected a definition named crm/create_contact, got %+v", defs)
	}
	if !strings.Contains(create.Description, "POST") || !strings.Contains(create.Description, "/contacts") {
		t.Errorf("create_contact description = %q, want it to mention POST /contacts", create.Description)
	}
	get, ok := byName["crm/get_contact"]
	if !ok {
		t.Fatalf("expected a definition named crm/get_contact, got %+v", defs)
	}
	if !strings.Contains(get.Description, "GET") || !strings.Contains(get.Description, "/contacts/{id}") {
		t.Errorf("get_contact description = %q, want it to mention GET /contacts/{id}", get.Description)
	}
	for _, d := range defs {
		if d.InputSchema["type"] != "object" {
			t.Errorf("%s InputSchema = %+v, want {type: object}", d.Name, d.InputSchema)
		}
	}
}

// TestListGatewayTools_InputSchema_ReflectsSpecGeneratedSchema proves B-075
// AC3: an action_paths entry carrying a real input_schema (the shape
// openapidiscovery.Parse produces, round-tripped through eami-api's
// ActionPathMapping.InputSchema and stored as-is in the JSONB column) is
// surfaced verbatim in tools/list's InputSchema field, not the generic
// {"type":"object"} B-061 originally shipped with. A sibling action with no
// input_schema (the B-046 manually-configured shape, unaffected by this
// brief) proves that fallback still applies -- both cases exercised in the
// same tool, so this also proves the two coexist correctly.
func TestListGatewayTools_InputSchema_ReflectsSpecGeneratedSchema(t *testing.T) {
	env := newMainTestEnv(t)
	richSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "description": "the pet's name", "in": "query"},
		},
		"required": []any{"name"},
	}
	env.insertToolWithActions(t, env.orgID, "petstore", map[string]toolrouter.ActionPathEntry{
		"createPet": {Path: "/pets", Method: "POST", InputSchema: richSchema},
		"legacy_action": {Path: "/legacy", Method: "GET"}, // no InputSchema -- the pre-B-075 manually-configured shape
	})

	defs, err := listGatewayTools(context.Background(), env.pool, env.orgID.String())
	if err != nil {
		t.Fatalf("listGatewayTools: %v", err)
	}
	byName := map[string]mcp.ToolDefinition{}
	for _, d := range defs {
		byName[d.Name] = d
	}

	rich, ok := byName["petstore/createPet"]
	if !ok {
		t.Fatalf("expected petstore/createPet, got %+v", defs)
	}
	if rich.InputSchema["type"] != "object" {
		t.Errorf("createPet InputSchema[type] = %v, want object", rich.InputSchema["type"])
	}
	props, _ := rich.InputSchema["properties"].(map[string]any)
	nameProp, _ := props["name"].(map[string]any)
	if nameProp == nil || nameProp["description"] != "the pet's name" {
		t.Errorf("createPet InputSchema.properties.name = %+v, want the real spec-derived schema to survive the round trip through Postgres JSONB", nameProp)
	}

	legacy, ok := byName["petstore/legacy_action"]
	if !ok {
		t.Fatalf("expected petstore/legacy_action, got %+v", defs)
	}
	if len(legacy.InputSchema) != 1 || legacy.InputSchema["type"] != "object" {
		t.Errorf("legacy_action (no stored input_schema) InputSchema = %+v, want the honest generic {type: object} fallback unchanged from B-061", legacy.InputSchema)
	}
}

// TestListGatewayTools_CrossOrg_NeverAppears proves AC2/org isolation: a
// tool registered under a different org must never appear in this org's
// tools/list result.
func TestListGatewayTools_CrossOrg_NeverAppears(t *testing.T) {
	env := newMainTestEnv(t)
	env.insertToolWithActions(t, env.orgID, "crm", map[string]toolrouter.ActionPathEntry{
		"create_contact": {Path: "/contacts", Method: "POST"},
	})

	otherOrgID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		otherOrgID, "gateway-tools-list-other-"+otherOrgID.String()[:8], "gateway-tools-list-other-"+otherOrgID.String()); err != nil {
		t.Fatalf("insert other org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, otherOrgID)
	})

	defs, err := listGatewayTools(context.Background(), env.pool, otherOrgID.String())
	if err != nil {
		t.Fatalf("listGatewayTools: %v", err)
	}
	if len(defs) != 0 {
		t.Errorf("expected zero entries for a different org, got %+v", defs)
	}
}

// TestListGatewayTools_NonRestAPITypes_NeverAppear proves AC3: mcp,
// database, and ai_provider gateway_tools rows contribute zero tools/list
// entries -- only type='rest_api' rows are eligible at all, per this
// brief's own design decision (see the plan's understanding-confirmation
// point 3).
func TestListGatewayTools_NonRestAPITypes_NeverAppear(t *testing.T) {
	env := newMainTestEnv(t)
	env.insertTool(t, env.orgID, "some-mcp-tool", "mcp")
	env.insertTool(t, env.orgID, "some-db-tool", "database")
	env.insertTool(t, env.orgID, "some-ai-provider", "ai_provider")

	defs, err := listGatewayTools(context.Background(), env.pool, env.orgID.String())
	if err != nil {
		t.Fatalf("listGatewayTools: %v", err)
	}
	if len(defs) != 0 {
		t.Errorf("expected zero entries when only non-rest_api types are registered, got %+v", defs)
	}
}

// TestListGatewayTools_RestAPI_NoActionPaths_ContributesZero proves the
// natural-consequence case from the plan: a rest_api tool with no
// action_paths configured (B-044's flat base_url fallback) has no fixed
// action list to honestly enumerate, so it contributes zero entries --
// not an error, not a fabricated single "any" entry.
func TestListGatewayTools_RestAPI_NoActionPaths_ContributesZero(t *testing.T) {
	env := newMainTestEnv(t)
	env.insertTool(t, env.orgID, "flat-tool", "rest_api")

	defs, err := listGatewayTools(context.Background(), env.pool, env.orgID.String())
	if err != nil {
		t.Fatalf("listGatewayTools: %v", err)
	}
	if len(defs) != 0 {
		t.Errorf("expected zero entries for a rest_api tool with no action_paths, got %+v", defs)
	}
}

// TestListGatewayTools_EmptyOrg_ReturnsNilNoQuery is a defensive guard,
// mirroring resolveDynamicTool's identical empty-org handling in
// main_pg_test.go.
func TestListGatewayTools_EmptyOrg_ReturnsNilNoQuery(t *testing.T) {
	env := newMainTestEnv(t)
	defs, err := listGatewayTools(context.Background(), env.pool, "")
	if err != nil {
		t.Fatalf("listGatewayTools: %v", err)
	}
	if defs != nil {
		t.Errorf("expected nil for an empty orgID, got %+v", defs)
	}
}

// ─── End-to-end SSE harness ─────────────────────────────────────────────

// sseFrame is one parsed "event: ...\ndata: ...\n\n" block read directly
// off a live SSE response body -- genuinely new test infrastructure (no
// existing test in this module opens a live SSE stream), needed because
// tools/list's real result only ever arrives via the SSE "message" event,
// never as ServeMessages' own HTTP response (which is always a bare 202).
type sseFrame struct {
	Event string
	Data  string
}

func readSSEFrame(t *testing.T, r *bufio.Reader) sseFrame {
	t.Helper()
	var f sseFrame
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE stream: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			f.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			f.Data = strings.TrimPrefix(line, "data: ")
		case line == "" && (f.Event != "" || f.Data != ""):
			return f
		}
	}
}

// toolsListTestEnv wires a real mcp.Handler against real
// identity.Manager + registry.Registry + Postgres, exactly like main.go's
// own run() wires them, plus one live agent registered so a real Bearer
// token resolves through the real registry.
type toolsListTestEnv struct {
	*mainTestEnv
	server    *httptest.Server
	agentName string
	token     string
}

func newToolsListTestEnv(t *testing.T, dispatch mcp.DecisionHandler) *toolsListTestEnv {
	t.Helper()
	env := newMainTestEnv(t)

	idm, err := identity.NewManager(filepath.Join(t.TempDir(), "gateway.key"), 300, "eami-gateway:primary")
	if err != nil {
		t.Fatalf("identity.NewManager: %v", err)
	}
	reg := registry.New(env.pool)

	agentName := "tools-list-test-agent-" + uuid.New().String()[:8]
	agentID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, 'test-model', 'test-owner', 'test-scope')
	`, agentID, env.orgID, agentName); err != nil {
		t.Fatalf("insert gateway_agents row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM gateway_agents WHERE id = $1`, agentID)
	})

	if dispatch == nil {
		dispatch = func(ctx context.Context, ac mcp.ActionContext) (json.RawMessage, error) {
			return nil, fmt.Errorf("dispatch not expected to be called in this test")
		}
	}
	h := mcp.NewHandler(idm, reg, dispatch, func(ctx context.Context, orgID string) ([]mcp.ToolDefinition, error) {
		return listGatewayTools(ctx, env.pool, orgID)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/mcp/sse", h.ServeSSE)
	mux.HandleFunc("/v1/mcp/messages", h.ServeMessages)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	resp, err := idm.Issue(identity.IssueRequest{AgentID: "agent:" + agentName, TTLSeconds: 300})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	return &toolsListTestEnv{mainTestEnv: env, server: server, agentName: agentName, token: resp.Token}
}

// openSSE opens the real SSE stream and reads the initial "endpoint"
// event, returning the sessionId query value it carries plus a reader
// positioned to read whatever event arrives next.
func (e *toolsListTestEnv) openSSE(t *testing.T) (sessionID string, body *http.Response, r *bufio.Reader) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.server.URL+"/v1/mcp/sse", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/mcp/sse: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/mcp/sse status = %d", resp.StatusCode)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	reader := bufio.NewReader(resp.Body)
	f := readSSEFrame(t, reader)
	if f.Event != "endpoint" {
		t.Fatalf("first SSE event = %q, want endpoint (data=%q)", f.Event, f.Data)
	}
	u := strings.TrimPrefix(f.Data, "/v1/mcp/messages?sessionId=")
	if u == f.Data || u == "" {
		t.Fatalf("could not parse sessionId from endpoint event data %q", f.Data)
	}
	return u, resp, reader
}

func (e *toolsListTestEnv) postMessage(t *testing.T, sessionID string, body string) {
	t.Helper()
	url := e.server.URL + "/v1/mcp/messages?sessionId=" + sessionID
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/mcp/messages: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /v1/mcp/messages status = %d, want 202", resp.StatusCode)
	}
}

// TestToolsList_EndToEnd_SSE proves AC1/AC5 together, end to end: a real
// agent JWT opens a real SSE stream, POSTs a real "tools/list" JSON-RPC
// request, and reads back the real "message" SSE event -- asserting the
// exact {"tools": [{"name","description","inputSchema"}]} shape a real
// MCP client library would parse, built from a real seeded rest_api
// connector (not a mock).
func TestToolsList_EndToEnd_SSE(t *testing.T) {
	env := newToolsListTestEnv(t, nil)
	env.insertToolWithActions(t, env.orgID, "crm", map[string]toolrouter.ActionPathEntry{
		"create_contact": {Path: "/contacts", Method: "POST"},
	})
	// An ai_provider row must never surface in the result -- proves AC3 at
	// the full-transport level, not just listGatewayTools in isolation.
	env.insertTool(t, env.orgID, "claude-provider", "ai_provider")

	sessionID, _, reader := env.openSSE(t)
	env.postMessage(t, sessionID, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)

	f := readSSEFrame(t, reader)
	if f.Event != "message" {
		t.Fatalf("event = %q, want message (data=%q)", f.Event, f.Data)
	}

	var resp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(f.Data), &resp); err != nil {
		t.Fatalf("unmarshal tools/list response: %v (data=%q)", err, f.Data)
	}
	if resp.Error != nil {
		t.Fatalf("tools/list returned an error: %+v", resp.Error)
	}
	if len(resp.Result.Tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1: %+v", len(resp.Result.Tools), resp.Result.Tools)
	}
	tool := resp.Result.Tools[0]
	if tool.Name != "crm/create_contact" {
		t.Errorf("Name = %q, want crm/create_contact", tool.Name)
	}
	if !strings.Contains(tool.Description, "POST") || !strings.Contains(tool.Description, "/contacts") {
		t.Errorf("Description = %q, want it to mention POST /contacts", tool.Description)
	}
	if tool.InputSchema["type"] != "object" {
		t.Errorf("InputSchema = %+v, want {type: object}", tool.InputSchema)
	}
	if resp.Result.NextCursor != "" {
		t.Errorf("NextCursor = %q, want empty (omitempty, no real pagination)", resp.Result.NextCursor)
	}
}

// TestToolsList_ToolCallRegression_StillWorks proves AC4: adding the
// tools/list branch did not disturb tool_call's own unchanged code path --
// a real tool_call over the same real SSE/session machinery still
// dispatches and returns its real result.
func TestToolsList_ToolCallRegression_StillWorks(t *testing.T) {
	called := make(chan mcp.ActionContext, 1)
	dispatch := func(ctx context.Context, ac mcp.ActionContext) (json.RawMessage, error) {
		called <- ac
		return json.RawMessage(`{"ok":true}`), nil
	}
	env := newToolsListTestEnv(t, dispatch)

	sessionID, _, reader := env.openSSE(t)
	env.postMessage(t, sessionID, `{"jsonrpc":"2.0","id":7,"method":"tool_call","params":{"name":"crm/create_contact","arguments":{"foo":"bar"}}}`)

	f := readSSEFrame(t, reader)
	if f.Event != "message" {
		t.Fatalf("event = %q, want message (data=%q)", f.Event, f.Data)
	}

	select {
	case ac := <-called:
		if ac.Tool != "crm" || ac.Action != "create_contact" {
			t.Errorf("ActionContext = %+v, want Tool=crm Action=create_contact", ac)
		}
	default:
		t.Fatal("dispatch was never called for a real tool_call request")
	}

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(f.Data), &resp); err != nil {
		t.Fatalf("unmarshal tool_call response: %v (data=%q)", err, f.Data)
	}
	if resp.Error != nil {
		t.Fatalf("tool_call returned an error: %+v", resp.Error)
	}
	if string(resp.Result) != `{"ok":true}` {
		t.Errorf("Result = %s, want {\"ok\":true}", resp.Result)
	}
}
