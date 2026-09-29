package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/store"
)

// B-253 -- RBAC split, "operators contain; admins expand or destroy".
// Real Postgres, full HTTP. Covers: every admin-only route (operator,
// approver and viewer get requireRole's exact 403 and nothing is written;
// admin succeeds), every admin-only PATCH field (a changed restricted field
// rejects the whole request, nothing written; echoed unchanged values pass),
// and the narrow approver read group, checked against EVERY GET route in
// the real router (chi.Walk) so a future route can't silently widen it.

type rbacEnv struct {
	*workspaceTestEnv
	org                     uuid.UUID
	admin, op, appr, viewer string
}

func newRBACEnv(t *testing.T) *rbacEnv {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	org := seedTestOrg(t, ctx, env.pool, "b253-rbac")
	tok := func(role string) string {
		return env.token(t, seedTestUser(t, ctx, env.pool, org), org, role+"@b253.test", role)
	}
	return &rbacEnv{workspaceTestEnv: env, org: org, admin: tok("admin"), op: tok("operator"), appr: tok("approver"), viewer: tok("viewer")}
}

func forbiddenBody(role string) string {
	return fmt.Sprintf("{\"code\":\"forbidden\",\"message\":\"your role (%s) does not have access to this resource\"}\n", role)
}

func (e *rbacEnv) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func (e *rbacEnv) seedTool(t *testing.T, name, typ, extraCols, extraVals string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	q := `INSERT INTO gateway_tools (org_id, name, type, auth_type` + extraCols + `) VALUES ($1, $2, $3, 'api_key'` + extraVals + `) RETURNING id`
	if err := e.pool.QueryRow(context.Background(), q, e.org, name, typ).Scan(&id); err != nil {
		t.Fatalf("seed tool %s: %v", name, err)
	}
	return id
}

func (e *rbacEnv) seedNode(t *testing.T, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `INSERT INTO gateway_nodes (org_id, name, role, address) VALUES ($1, $2, 'edge', '10.0.0.9:8080') RETURNING id`, e.org, name).Scan(&id); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return id
}

func (e *rbacEnv) seedEndpoint(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `INSERT INTO endpoints (org_id, agent_id, hostname, agent_version) VALUES ($1, $2, 'b253-host', '') RETURNING id`, e.org, "b253-ep-"+uuid.NewString()[:8]).Scan(&id); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	return id
}

// ── 1. Admin-only routes ──────────────────────────────────────────────────

func TestRBACSplit_AdminOnlyRoutes_RealDB(t *testing.T) {
	e := newRBACEnv(t)
	agent := seedConfigAgent(t, e.workspaceTestEnv, context.Background(), e.org, "b253-agent")
	tool := e.seedTool(t, "b253-tool", "rest_api", ", base_url", ", 'https://api.example.com'")
	node := e.seedNode(t, "b253-node")
	endpoint := e.seedEndpoint(t)

	type route struct {
		name, method, path string
		body               any
		written            func() int // rows that a wrongly-allowed call would change
	}
	agents := func() int { return e.count(t, `SELECT count(*) FROM gateway_agents WHERE org_id=$1`, e.org) }
	tools := func() int { return e.count(t, `SELECT count(*) FROM gateway_tools WHERE org_id=$1`, e.org) }
	restricted := []route{
		{"mint API key", http.MethodPost, "/v1/auth/api-keys", map[string]any{"name": "k", "agent_id": agent.String()}, func() int { return e.count(t, `SELECT count(*) FROM api_keys WHERE org_id=$1`, e.org) }},
		{"create agent", http.MethodPost, "/v1/gateway/agents", map[string]any{"name": "b253-new", "model": "m", "owner": "o", "scope": "s", "risk_tier": "low"}, agents},
		{"delete agent", http.MethodDelete, "/v1/gateway/agents/" + agent.String(), nil, agents},
		{"link endpoint to agent", http.MethodPatch, "/v1/endpoints/" + endpoint.String() + "/link-agent", map[string]any{"gateway_agent_id": agent.String()}, func() int {
			return e.count(t, `SELECT count(*) FROM endpoints WHERE id=$1 AND gateway_agent_id IS NOT NULL`, endpoint)
		}},
		{"create tool: REST with base_url", http.MethodPost, "/v1/gateway/tools", map[string]any{"name": "b253-t1", "type": "rest_api", "auth_type": "api_key", "base_url": "https://x.example.com"}, tools},
		{"create tool: REST with credentials", http.MethodPost, "/v1/gateway/tools", map[string]any{"name": "b253-t2", "type": "rest_api", "auth_type": "api_key", "base_url": "https://x.example.com", "credentials": map[string]any{"api_key": "sk-test"}}, tools},
		{"create tool: AI provider with key", http.MethodPost, "/v1/gateway/tools", map[string]any{"name": "b253-t3", "type": "ai_provider", "auth_type": "api_key", "provider": "claude", "credentials": map[string]any{"api_key": "sk-test"}}, tools},
		{"create tool: MCP command", http.MethodPost, "/v1/gateway/tools", map[string]any{"name": "b253-t4", "type": "mcp", "auth_type": "api_key", "mcp_command": "npx server"}, tools},
		{"create tool: bare (no credential or base_url)", http.MethodPost, "/v1/gateway/tools", map[string]any{"name": "b253-t5", "type": "mcp", "auth_type": "api_key"}, tools},
		{"delete tool", http.MethodDelete, "/v1/gateway/tools/" + tool.String(), nil, tools},
		{"delete node", http.MethodDelete, "/v1/gateway/nodes/" + node.String(), nil, func() int { return e.count(t, `SELECT count(*) FROM gateway_nodes WHERE org_id=$1`, e.org) }},
	}
	for _, r := range restricted {
		for _, role := range []struct{ name, token string }{{"operator", e.op}, {"approver", e.appr}, {"viewer", e.viewer}} {
			before := r.written()
			body := expectCMDBStatus(t, e.do(t, r.method, r.path, role.token, r.body), http.StatusForbidden, role.name+" "+r.name)
			if body != forbiddenBody(role.name) {
				t.Fatalf("%s %s: body %q, want requireRole's %q", role.name, r.name, body, forbiddenBody(role.name))
			}
			if after := r.written(); after != before {
				t.Fatalf("%s %s was refused but changed data: %d -> %d", role.name, r.name, before, after)
			}
		}
	}

	// Admin succeeds on each, with the effect verified (destructive ones
	// last, on their own rows).
	has := func(what, sql string, args ...any) {
		t.Helper()
		if e.count(t, sql, args...) != 1 {
			t.Fatalf("admin %s: effect not found (%s)", what, sql)
		}
	}
	expectCMDBStatus(t, e.do(t, http.MethodPost, "/v1/auth/api-keys", e.admin, map[string]any{"name": "b253-admin-key", "agent_id": agent.String()}), http.StatusCreated, "admin mint key")
	has("mint key", `SELECT count(*) FROM api_keys WHERE org_id=$1 AND name='b253-admin-key' AND agent_id=$2`, e.org, agent)
	expectCMDBStatus(t, e.do(t, http.MethodPost, "/v1/gateway/agents", e.admin, map[string]any{"name": "b253-new", "model": "m", "owner": "o", "scope": "s", "risk_tier": "low"}), http.StatusCreated, "admin create agent")
	has("create agent", `SELECT count(*) FROM gateway_agents WHERE org_id=$1 AND name='b253-new'`, e.org)
	expectCMDBStatus(t, e.do(t, http.MethodPatch, "/v1/endpoints/"+endpoint.String()+"/link-agent", e.admin, map[string]any{"gateway_agent_id": agent.String()}), http.StatusOK, "admin link endpoint")
	has("link endpoint", `SELECT count(*) FROM endpoints WHERE id=$1 AND gateway_agent_id=$2`, endpoint, agent)
	expectCMDBStatus(t, e.do(t, http.MethodPost, "/v1/gateway/tools", e.admin, map[string]any{"name": "b253-t1", "type": "rest_api", "auth_type": "api_key", "base_url": "https://x.example.com"}), http.StatusCreated, "admin create tool")
	has("create tool", `SELECT count(*) FROM gateway_tools WHERE org_id=$1 AND name='b253-t1'`, e.org)
	expectCMDBStatus(t, e.do(t, http.MethodDelete, "/v1/gateway/tools/"+tool.String(), e.admin, nil), http.StatusNoContent, "admin delete tool")
	has("delete tool", `SELECT 1 - count(*) FROM gateway_tools WHERE id=$1`, tool)
	expectCMDBStatus(t, e.do(t, http.MethodDelete, "/v1/gateway/nodes/"+node.String(), e.admin, nil), http.StatusNoContent, "admin delete node")
	has("delete node", `SELECT 1 - count(*) FROM gateway_nodes WHERE id=$1`, node)
	victim := seedConfigAgent(t, e.workspaceTestEnv, context.Background(), e.org, "b253-delete-me")
	expectCMDBStatus(t, e.do(t, http.MethodDelete, "/v1/gateway/agents/"+victim.String(), e.admin, nil), http.StatusNoContent, "admin delete agent")
	has("delete agent", `SELECT 1 - count(*) FROM gateway_agents WHERE id=$1`, victim)

	// Containment and operational writes stay open to operators.
	keyBody := expectCMDBStatus(t, e.do(t, http.MethodGet, "/v1/auth/api-keys", e.op, nil), http.StatusOK, "operator list keys")
	var keyID string
	if err := e.pool.QueryRow(context.Background(), `SELECT id::text FROM api_keys WHERE org_id=$1 LIMIT 1`, e.org).Scan(&keyID); err != nil {
		t.Fatalf("find key: %v (list body %s)", err, keyBody)
	}
	expectCMDBStatus(t, e.do(t, http.MethodDelete, "/v1/auth/api-keys/"+keyID, e.op, nil), http.StatusNoContent, "operator revoke key")
	expectCMDBStatus(t, e.do(t, http.MethodPut, "/v1/gateway/agents/"+agent.String()+"/config", e.op, map[string]any{"scan_interval_seconds": 120}), http.StatusOK, "operator scanner config")
	// Tool test and OpenAPI discover stay operator (not 403; the outcome
	// of the connectivity check itself is irrelevant here).
	testTool := e.seedTool(t, "b253-test-tool", "rest_api", ", base_url", ", 'https://api.example.com'")
	if resp := e.do(t, http.MethodPost, "/v1/gateway/tools/"+testTool.String()+"/test", e.op, nil); resp.StatusCode == http.StatusForbidden {
		t.Fatalf("operator tool test: 403 %s", readWSBody(resp))
	}
	spec := `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{"/x":{"get":{"operationId":"getX","responses":{"200":{"description":"ok"}}}}}}`
	expectCMDBStatus(t, e.do(t, http.MethodPost, "/v1/gateway/openapi/discover", e.op, map[string]any{"spec_content": spec}), http.StatusOK, "operator OpenAPI discover")
}

// ── 2. Agent fields ───────────────────────────────────────────────────────

func TestRBACSplit_AgentFields_RealDB(t *testing.T) {
	e := newRBACEnv(t)
	ctx := context.Background()
	agent := seedConfigAgent(t, e.workspaceTestEnv, ctx, e.org, "b253-fields-agent") // status active, scope "test", risk low, ttl default
	path := "/v1/gateway/agents/" + agent.String()
	state := func() string {
		var s string
		if err := e.pool.QueryRow(ctx, `SELECT status||'|'||scope||'|'||risk_tier||'|'||token_ttl_seconds FROM gateway_agents WHERE id=$1`, agent).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	patch := func(tok string, body map[string]any, want int, what string) string {
		return expectCMDBStatus(t, e.do(t, http.MethodPatch, path, tok, body), want, what)
	}

	// Containment works for operators.
	patch(e.op, map[string]any{"status": "suspended"}, http.StatusOK, "operator suspend")
	suspended := state()
	if !strings.HasPrefix(suspended, "suspended|") {
		t.Fatalf("after suspend: %s", suspended)
	}
	// Each expansion is refused whole, with nothing written.
	for _, c := range []struct {
		what string
		body map[string]any
	}{
		{"reactivate", map[string]any{"status": "active"}},
		{"scope change", map[string]any{"scope": "read:everything"}},
		{"risk_tier change", map[string]any{"risk_tier": "high"}},
		{"token_ttl change", map[string]any{"token_ttl_seconds": 86400}},
		{"mixed: allowed revoke + restricted scope", map[string]any{"status": "revoked", "scope": "read:everything"}},
	} {
		body := patch(e.op, c.body, http.StatusForbidden, "operator "+c.what)
		if body != forbiddenBody("operator") {
			t.Fatalf("operator %s: body %q, want requireRole's", c.what, body)
		}
		if got := state(); got != suspended {
			t.Fatalf("operator %s was refused but wrote: %s -> %s", c.what, suspended, got)
		}
	}
	// Echoing the current values (a form that sends every field) passes.
	cur := strings.Split(suspended, "|")
	patch(e.op, map[string]any{"status": cur[0], "scope": cur[1], "risk_tier": cur[2]}, http.StatusOK, "operator echo unchanged")
	if got := state(); got != suspended {
		t.Fatalf("echo changed state: %s -> %s", suspended, got)
	}
	// Admin can expand.
	patch(e.admin, map[string]any{"status": "active", "scope": "read:crm", "risk_tier": "high", "token_ttl_seconds": 7200}, http.StatusOK, "admin reactivate + edit")
	if got := state(); got != "active|read:crm|high|7200" {
		t.Fatalf("admin edit: %s", got)
	}
	// Approver and viewer can't write at all.
	patch(e.appr, map[string]any{"status": "suspended"}, http.StatusForbidden, "approver suspend")
	patch(e.viewer, map[string]any{"status": "suspended"}, http.StatusForbidden, "viewer suspend")
	// Operator revoke (containment) is allowed.
	patch(e.op, map[string]any{"status": "revoked"}, http.StatusOK, "operator revoke")
}

// ── 3. Tool fields ────────────────────────────────────────────────────────

func TestRBACSplit_ToolFields_RealDB(t *testing.T) {
	e := newRBACEnv(t)
	ctx := context.Background()
	rest := e.seedTool(t, "b253-rest", "rest_api", ", base_url, action_paths, mcp_args", ", 'https://api.example.com', '{\"read\":{\"path\":\"/r\",\"method\":\"GET\"}}', ARRAY[]::text[]")
	ai := e.seedTool(t, "b253-ai", "ai_provider", ", provider", ", 'claude'")
	mcp := e.seedTool(t, "b253-mcp", "mcp", ", mcp_command, mcp_args", ", 'npx server', ARRAY['--a']")
	snap := func(id uuid.UUID) string {
		var s string
		if err := e.pool.QueryRow(ctx, `SELECT concat_ws('|', name, coalesce(base_url,''), coalesce(action_paths::text,''), coalesce(provider,''), audit_mode, data_handling_designation, coalesce(data_handling_note,''), coalesce(redaction_rules::text,''), coalesce(mcp_command,''), coalesce(array_to_string(mcp_args, ','),''), coalesce(encode(credentials_encrypted,'hex'),'')) FROM gateway_tools WHERE id=$1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	patch := func(id uuid.UUID, tok string, body map[string]any, want int, what string) string {
		return expectCMDBStatus(t, e.do(t, http.MethodPatch, "/v1/gateway/tools/"+id.String(), tok, body), want, what)
	}
	for _, c := range []struct {
		what string
		tool uuid.UUID
		body map[string]any
	}{
		{"rename", rest, map[string]any{"name": "b253-renamed"}},
		{"base_url", rest, map[string]any{"base_url": "https://evil.example.com"}},
		{"credentials", rest, map[string]any{"credentials": map[string]any{"api_key": "sk-new"}}},
		{"action_paths", rest, map[string]any{"action_paths": map[string]any{"read": map[string]any{"path": "/delete", "method": "DELETE"}}}},
		{"mcp_command", mcp, map[string]any{"mcp_command": "sh -c evil"}},
		{"mcp_args", mcp, map[string]any{"mcp_args": []string{"--b"}}},
		// provider: only "claude" is a valid provider today, so any change is
		// a 400 at validation before the role check; the admin-only rule is
		// pinned in rbac_fields_test.go (TestToolAdminOnlyChange).
		{"audit_mode", ai, map[string]any{"audit_mode": "full"}},
		{"data_handling_designation", ai, map[string]any{"data_handling_designation": "zero_retention"}},
		{"redaction_rules (disable)", ai, map[string]any{"redaction_rules": map[string]any{"enabled": false}}},
		{"mixed: note + restricted rename", ai, map[string]any{"data_handling_note": "ok", "name": "b253-ai-renamed"}},
	} {
		before := snap(c.tool)
		body := patch(c.tool, e.op, c.body, http.StatusForbidden, "operator "+c.what)
		if body != forbiddenBody("operator") {
			t.Fatalf("operator %s: body %q, want requireRole's", c.what, body)
		}
		if after := snap(c.tool); after != before {
			t.Fatalf("operator %s was refused but wrote:\n%s\n->\n%s", c.what, before, after)
		}
	}
	// Descriptive edit passes, including when the UI echoes every field
	// (redaction_rules stored NULL vs sent as the explicit default).
	before := snap(ai)
	patch(ai, e.op, map[string]any{
		"name": "b253-ai", "provider": "claude", "audit_mode": "structural_metadata_only",
		"data_handling_designation": "unknown", "data_handling_note": "per contract X",
		"redaction_rules": map[string]any{"enabled": true, "disabled_patterns": []string{}},
	}, http.StatusOK, "operator descriptive edit echoing all fields")
	if after := snap(ai); !strings.Contains(after, "|per contract X|") || strings.Split(after, "|")[0] != "b253-ai" {
		t.Fatalf("descriptive edit: %s -> %s", before, after)
	}
	patch(rest, e.op, map[string]any{"name": "b253-rest", "base_url": "https://api.example.com", "action_paths": map[string]any{"read": map[string]any{"path": "/r", "method": "GET"}}}, http.StatusOK, "operator echo REST unchanged")
	// A credentials value that isn't a write (empty object) is not a rotation.
	patch(rest, e.op, map[string]any{"credentials": map[string]any{}}, http.StatusOK, "operator empty credentials (no write)")
	// Resetting redaction to default against a stored NON-default is refused.
	if _, err := e.pool.Exec(ctx, `UPDATE gateway_tools SET redaction_rules='{"enabled":true,"custom_patterns":[{"name":"acct","pattern":"ACC-[0-9]+"}]}' WHERE id=$1`, ai); err != nil {
		t.Fatal(err)
	}
	custom := snap(ai)
	patch(ai, e.op, map[string]any{"redaction_rules": nil}, http.StatusForbidden, "operator reset custom redaction to default")
	patch(ai, e.op, map[string]any{"redaction_rules": map[string]any{"enabled": true, "custom_patterns": []any{}}}, http.StatusForbidden, "operator drop custom redaction pattern")
	if got := snap(ai); got != custom {
		t.Fatalf("redaction refusals wrote: %s -> %s", custom, got)
	}
	// Admin can change restricted fields.
	patch(rest, e.admin, map[string]any{"name": "b253-rest-admin", "base_url": "https://new.example.com"}, http.StatusOK, "admin rename + base_url")
	// Approver/viewer can't write.
	patch(ai, e.appr, map[string]any{"data_handling_note": "x"}, http.StatusForbidden, "approver note")
	patch(ai, e.viewer, map[string]any{"data_handling_note": "x"}, http.StatusForbidden, "viewer note")
}

// ── 4. Approver reads: exactly agents and tools (+ unchanged approvals,
// self-profile, workspace membership) across EVERY GET route ───────────────

func TestRBACSplit_ApproverReadSurface_RealDB(t *testing.T) {
	e := newRBACEnv(t)
	agent := seedConfigAgent(t, e.workspaceTestEnv, context.Background(), e.org, "b253-read-agent")
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	routes, ok := api.NewServer(store.New(e.pool), authSvc, nil, nil).Handler().(chi.Routes)
	if !ok {
		t.Fatal("Handler() is not a chi.Routes")
	}
	allowed := map[string]bool{
		"/v1/gateway/agents": true, "/v1/gateway/agents/{agentId}": true, "/v1/gateway/tools": true, // B-253
		"/v1/approvals": true, "/v1/approvals/{approvalId}": true, "/v1/users/me": true, "/v1/workspaces/mine": true, // unchanged
	}
	public := map[string]bool{"/health": true, "/v1/setup/status": true, "/v1/agents/{agent_id}/config": true} // not JWT routes
	checked := 0
	err = chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method != http.MethodGet || public[route] {
			return nil
		}
		path := route
		for _, p := range []string{"{agentId}", "{approvalId}", "{workspaceId}", "{policyId}", "{workflowId}", "{stepId}", "{episodeId}", "{endpointId}", "{toolId}", "{userId}", "{ruleId}", "{alertId}", "{nodeId}", "{model}", "{categoryId}", "{typeId}", "{keyId}"} {
			id := uuid.NewString()
			if p == "{agentId}" {
				id = agent.String()
			}
			path = strings.ReplaceAll(path, p, id)
		}
		resp := e.do(t, http.MethodGet, path, e.appr, nil)
		body := readWSBody(resp)
		checked++
		if allowed[route] {
			if resp.StatusCode == http.StatusForbidden {
				t.Errorf("approver GET %s: 403, want allowed (%s)", route, body)
			}
			return nil
		}
		// Workspace-scoped routes answer through requireWorkspaceRole
		// (unchanged by B-253): a non-member gets 403 or 404.
		if strings.HasPrefix(route, "/v1/workspaces/{workspaceId}") && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden) {
			return nil
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("approver GET %s: %d %s, want 403 (B-253: approver reads agents and tools only)", route, resp.StatusCode, body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 30 {
		t.Fatalf("walked only %d GET routes; the walk is not covering the router", checked)
	}
	// Viewer is unchanged on the moved and the kept read routes.
	for _, p := range []string{"/v1/gateway/agents", "/v1/gateway/agents/" + agent.String(), "/v1/gateway/tools", "/v1/gateway/agents/" + agent.String() + "/config", "/v1/gateway/agents/" + agent.String() + "/connections", "/v1/cmdb/assets", "/v1/gateway/policies"} {
		expectCMDBStatus(t, e.do(t, http.MethodGet, p, e.viewer, nil), http.StatusOK, "viewer GET "+p)
	}
}
