package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"testing"

	"github.com/google/uuid"
)

// B-196 Brief 1 fix-up pass: real-Postgres coverage for review findings
// N1–N4, L-3, the workspace-role authorization claim, and B-223.

type cmdbAssetsBody struct {
	Data []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		AssetKind string `json:"asset_kind"`
	} `json:"data"`
	Meta struct {
		Total   int64 `json:"total"`
		Page    int   `json:"page"`
		PerPage int   `json:"per_page"`
	} `json:"meta"`
	Counts []struct {
		TypeID     uuid.UUID `json:"type_id"`
		CategoryID uuid.UUID `json:"category_id"`
		Count      int64     `json:"count"`
	} `json:"counts"`
}

func (b cmdbAssetsBody) countFor(typeID uuid.UUID) int64 {
	for _, c := range b.Counts {
		if c.TypeID == typeID {
			return c.Count
		}
	}
	return 0
}

func (b cmdbAssetsBody) names() []string {
	out := make([]string, 0, len(b.Data))
	for _, d := range b.Data {
		out = append(out, d.Name)
	}
	sort.Strings(out)
	return out
}

func getCMDBAssets(t *testing.T, env *workspaceTestEnv, token string, query url.Values) cmdbAssetsBody {
	t.Helper()
	resp := env.do(t, http.MethodGet, "/v1/cmdb/assets?"+query.Encode(), token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/cmdb/assets?%s = %d: %s", query.Encode(), resp.StatusCode, readWSBody(resp))
	}
	defer resp.Body.Close()
	var body cmdbAssetsBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func defaultCIType(t *testing.T, env *workspaceTestEnv, ctx context.Context, orgID uuid.UUID, kind string) (typeID, categoryID uuid.UUID) {
	t.Helper()
	if err := env.pool.QueryRow(ctx, `SELECT id, category_id FROM ci_types WHERE org_id=$1 AND asset_kind=$2 AND is_default`, orgID, kind).Scan(&typeID, &categoryID); err != nil {
		t.Fatalf("default %s type: %v", kind, err)
	}
	return typeID, categoryID
}

func seedCMDBTool(t *testing.T, env *workspaceTestEnv, ctx context.Context, orgID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := env.pool.QueryRow(ctx, `INSERT INTO gateway_tools(org_id,name,type,auth_type) VALUES($1,$2,'mcp','api_key') RETURNING id`, orgID, name).Scan(&id); err != nil {
		t.Fatalf("seed tool %s: %v", name, err)
	}
	return id
}

func seedCMDBAgent(t *testing.T, env *workspaceTestEnv, ctx context.Context, orgID uuid.UUID, name string, workspaceID *uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := env.pool.QueryRow(ctx, `INSERT INTO gateway_agents(org_id,name,model,owner,scope,workspace_id) VALUES($1,$2,'model','qa','test',$3) RETURNING id`, orgID, name, workspaceID).Scan(&id); err != nil {
		t.Fatalf("seed agent %s: %v", name, err)
	}
	return id
}

func expectCMDBStatus(t *testing.T, resp *http.Response, want int, what string) string {
	t.Helper()
	body := readWSBody(resp)
	if resp.StatusCode != want {
		t.Fatalf("%s = %d want %d: %s", what, resp.StatusCode, want, body)
	}
	return body
}

// N1: selecting a category/type/kind must not zero the sidebar's counts for
// every other classification, while workspace, search, and license filters
// must still narrow them.
func TestCMDBFixup_NavigationCountsIgnoreSelection_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "cmdb-fixup-nav")
	viewer := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "viewer@cmdb-nav.test", "viewer")

	wsID := env.seedWorkspace(t, ctx, orgID, "Nav workspace")
	seedCMDBAgent(t, env, ctx, orgID, "nav-agent-1", nil)
	seedCMDBAgent(t, env, ctx, orgID, "nav-agent-2", nil)
	seedCMDBAgent(t, env, ctx, orgID, "nav-agent-ws", &wsID)
	seedCMDBTool(t, env, ctx, orgID, "nav-tool-1")
	seedCMDBTool(t, env, ctx, orgID, "nav-tool-2")
	seedCMDBEndpoint(t, env, ctx, orgID, "nav-endpoint-1")
	agentType, aiCategory := defaultCIType(t, env, ctx, orgID, "agent")
	toolType, _ := defaultCIType(t, env, ctx, orgID, "tool")
	endpointType, _ := defaultCIType(t, env, ctx, orgID, "endpoint")

	all := getCMDBAssets(t, env, viewer, url.Values{})
	if all.Meta.Total != 5 || all.countFor(agentType) != 3 || all.countFor(toolType) != 2 || all.countFor(endpointType) != 0 {
		t.Fatalf("unlicensed unfiltered: total=%d counts=%+v, want 5 with agent 3 / tool 2 / endpoint 0", all.Meta.Total, all.Counts)
	}

	selected := getCMDBAssets(t, env, viewer, url.Values{"kind": {"agent"}, "category_id": {aiCategory.String()}, "type_id": {agentType.String()}})
	if selected.Meta.Total != 3 {
		t.Fatalf("type selection total=%d want 3 (the table stays filtered)", selected.Meta.Total)
	}
	if selected.countFor(agentType) != 3 || selected.countFor(toolType) != 2 {
		t.Fatalf("REGRESSION N1: counts with a type selected=%+v, want agent 3 and tool 2 (navigation counts must ignore the selection)", selected.Counts)
	}

	searched := getCMDBAssets(t, env, viewer, url.Values{"q": {"nav-tool"}, "type_id": {agentType.String()}})
	if searched.Meta.Total != 0 || searched.countFor(toolType) != 2 || searched.countFor(agentType) != 0 {
		t.Fatalf("search + selection: total=%d counts=%+v, want total 0 and counts narrowed by search only (tool 2, agent 0)", searched.Meta.Total, searched.Counts)
	}

	scoped := getCMDBAssets(t, env, viewer, url.Values{"workspace_id": {wsID.String()}, "kind": {"tool"}})
	if scoped.countFor(agentType) != 1 || scoped.countFor(toolType) != 0 {
		t.Fatalf("workspace + kind: counts=%+v, want counts narrowed by workspace only (agent 1, tool 0)", scoped.Counts)
	}

	seedDiscoveryLicense(t, env, ctx, orgID)
	licensed := getCMDBAssets(t, env, viewer, url.Values{"kind": {"agent"}})
	if licensed.countFor(endpointType) != 1 || licensed.countFor(agentType) != 3 {
		t.Fatalf("licensed + kind=agent: counts=%+v, want endpoint 1 and agent 3", licensed.Counts)
	}
}

// N2: the endpoint classification write is gated by the Discovery license,
// exactly like every endpoint read path. Agent/tool writes are not.
func TestCMDBFixup_EndpointClassificationWriteRequiresDiscoveryLicense_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "cmdb-fixup-lic")
	admin := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "admin@cmdb-lic.test", "admin")
	endpointID := seedCMDBEndpoint(t, env, ctx, orgID, "lic-endpoint")
	agentID := seedCMDBAgent(t, env, ctx, orgID, "lic-agent", nil)
	endpointType, _ := defaultCIType(t, env, ctx, orgID, "endpoint")
	agentType, _ := defaultCIType(t, env, ctx, orgID, "agent")

	endpointPath := fmt.Sprintf("/v1/cmdb/assets/endpoint/%s/classification", endpointID)
	body := expectCMDBStatus(t, env.do(t, http.MethodPatch, endpointPath, admin, map[string]any{"ci_type_id": endpointType}), http.StatusForbidden, "unlicensed endpoint classification write")
	var apiErr struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(body), &apiErr); err != nil || apiErr.Code != "module_not_licensed" {
		t.Fatalf("unlicensed endpoint write error=%s, want code module_not_licensed", body)
	}
	// A nonexistent endpoint gets the same 403, so the gate is not an existence oracle.
	expectCMDBStatus(t, env.do(t, http.MethodPatch, fmt.Sprintf("/v1/cmdb/assets/endpoint/%s/classification", uuid.New()), admin, map[string]any{"ci_type_id": nil}), http.StatusForbidden, "unlicensed write to unknown endpoint")
	var stored *uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT ci_type_id FROM endpoints WHERE id=$1`, endpointID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != nil {
		t.Fatalf("unlicensed write changed endpoints.ci_type_id to %s", stored)
	}
	expectCMDBStatus(t, env.do(t, http.MethodPatch, fmt.Sprintf("/v1/cmdb/assets/agent/%s/classification", agentID), admin, map[string]any{"ci_type_id": agentType}), http.StatusOK, "unlicensed agent classification write")

	seedDiscoveryLicense(t, env, ctx, orgID)
	expectCMDBStatus(t, env.do(t, http.MethodPatch, endpointPath, admin, map[string]any{"ci_type_id": endpointType}), http.StatusOK, "licensed endpoint classification write")
	if err := env.pool.QueryRow(ctx, `SELECT ci_type_id FROM endpoints WHERE id=$1`, endpointID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == nil || *stored != endpointType {
		t.Fatalf("licensed write stored %v, want %s", stored, endpointType)
	}
}

// L-3: names are trimmed with Unicode-aware TrimSpace before storage, so a
// trailing tab or NBSP can no longer create a visual duplicate that escapes
// the (org_id, normalized_name) uniqueness check.
func TestCMDBFixup_NameTrimMatchesNormalization_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "cmdb-fixup-trim")
	admin := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "admin@cmdb-trim.test", "admin")
	_, integrations := defaultCIType(t, env, ctx, orgID, "tool")

	expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/cmdb/types", admin, map[string]any{"category_id": integrations, "asset_kind": "tool", "name": "Connector\t"}), http.StatusConflict, `type "Connector\t" vs seeded "Connector"`)
	expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/cmdb/categories", admin, map[string]any{"name": "Integrations "}), http.StatusConflict, `category "Integrations<NBSP>" vs seeded "Integrations"`)
	expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/cmdb/categories", admin, map[string]any{"name": "\t  "}), http.StatusBadRequest, "whitespace-only category name")

	body := expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/cmdb/categories", admin, map[string]any{"name": " Padded\t"}), http.StatusCreated, "padded category name")
	var created struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	var storedName, normalized string
	if err := env.pool.QueryRow(ctx, `SELECT name, normalized_name FROM ci_categories WHERE org_id=$1 AND normalized_name='padded'`, orgID).Scan(&storedName, &normalized); err != nil {
		t.Fatalf("padded category not stored with normalized name 'padded': %v", err)
	}
	if created.Name != "Padded" || storedName != "Padded" {
		t.Fatalf("padded name returned %q stored %q, want Padded", created.Name, storedName)
	}
}

// N4: search is a literal substring match — ILIKE metacharacters in q no
// longer act as wildcards.
func TestCMDBFixup_SearchEscapesLikeWildcards_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "cmdb-fixup-like")
	viewer := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "viewer@cmdb-like.test", "viewer")
	for _, name := range []string{"under_score", "pct100%tool", "plainname", `back\slash`} {
		seedCMDBTool(t, env, ctx, orgID, name)
	}
	cases := []struct {
		q    string
		want []string
	}{
		{"_", []string{"under_score"}},
		{"%", []string{"pct100%tool"}},
		{`\`, []string{`back\slash`}},
		{"der_sc", []string{"under_score"}},
		{"plain_ame", nil},
		{"pct100%", []string{"pct100%tool"}},
		{"PLAIN", []string{"plainname"}},
	}
	for _, c := range cases {
		got := getCMDBAssets(t, env, viewer, url.Values{"q": {c.q}})
		if fmt.Sprint(got.names()) != fmt.Sprint(append([]string{}, c.want...)) || got.Meta.Total != int64(len(c.want)) {
			t.Fatalf("q=%q matched %v (total %d), want %v", c.q, got.names(), got.Meta.Total, c.want)
		}
	}
}

// N3 (API half): is_default:false on the current default is a documented
// no-op — a default is only ever replaced by promoting another type, which
// keeps exactly one default per kind. The UI now disables the checkbox for
// the current default instead of offering an untick that does nothing.
func TestCMDBFixup_DefaultIsReplacedNotUnset_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "cmdb-fixup-default")
	admin := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "admin@cmdb-default.test", "admin")
	toolType, integrations := defaultCIType(t, env, ctx, orgID, "tool")

	body := expectCMDBStatus(t, env.do(t, http.MethodPatch, "/v1/cmdb/types/"+toolType.String(), admin, map[string]any{"category_id": integrations, "asset_kind": "tool", "name": "Connector", "is_default": false}), http.StatusOK, "untick default")
	var got struct {
		IsDefault bool `json:"is_default"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	var defaults int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM ci_types WHERE org_id=$1 AND asset_kind='tool' AND is_default AND id=$2`, orgID, toolType).Scan(&defaults); err != nil {
		t.Fatal(err)
	}
	if !got.IsDefault || defaults != 1 {
		t.Fatalf("is_default:false on the default returned is_default=%v, stored defaults=%d; want the default kept", got.IsDefault, defaults)
	}
}

// BUILT.md coverage claim "workspace-role authorization": a workspace_admin
// membership grants no CMDB write — CMDB writes are org-admin only.
func TestCMDBFixup_WorkspaceAdminCannotWriteCMDB_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "cmdb-fixup-wsrole")
	wsID := env.seedWorkspace(t, ctx, orgID, "CMDB role workspace")
	agentID := seedCMDBAgent(t, env, ctx, orgID, "wsrole-agent", &wsID)
	agentType, aiCategory := defaultCIType(t, env, ctx, orgID, "agent")

	for _, role := range []string{"workspace_admin", "workspace_member"} {
		userID := seedTestUser(t, ctx, env.pool, orgID)
		env.seedMembership(t, ctx, userID, wsID, role)
		for _, orgRole := range []string{"operator", "viewer"} {
			tok := env.token(t, userID, orgID, role+"@cmdb-wsrole.test", orgRole)
			what := role + "/" + orgRole
			expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/cmdb/categories", tok, map[string]any{"name": "ws-" + what}), http.StatusForbidden, what+" create category")
			expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/cmdb/types", tok, map[string]any{"category_id": aiCategory, "asset_kind": "agent", "name": "ws-type-" + what}), http.StatusForbidden, what+" create type")
			expectCMDBStatus(t, env.do(t, http.MethodPatch, fmt.Sprintf("/v1/cmdb/assets/agent/%s/classification", agentID), tok, map[string]any{"ci_type_id": agentType}), http.StatusForbidden, what+" classify workspace agent")
			expectCMDBStatus(t, env.do(t, http.MethodGet, "/v1/cmdb/assets?workspace_id="+wsID.String(), tok, nil), http.StatusOK, what+" read")
		}
	}
	var explicit int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM gateway_agents WHERE id=$1 AND ci_type_id IS NOT NULL`, agentID).Scan(&explicit); err != nil {
		t.Fatal(err)
	}
	if explicit != 0 {
		t.Fatal("a workspace-role write reached the database")
	}
}

// B-223: a huge page no longer overflows the shared pagination() offset into
// a 500; it reads an empty page at the clamp. Covers an int64-offset caller
// (CMDB) and an int32-offset caller (users).
func TestPaginationOverflow_ReturnsEmptyPageNot500_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b223-page")
	admin := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "admin@b223.test", "admin")
	seedCMDBTool(t, env, ctx, orgID, "b223-tool")

	for _, page := range []string{"9223372036854775807", "4294967297", "1000001"} {
		got := getCMDBAssets(t, env, admin, url.Values{"page": {page}, "per_page": {"100"}})
		if len(got.Data) != 0 || got.Meta.Total != 1 || got.Meta.Page != 1_000_000 {
			t.Fatalf("CMDB page=%s: rows=%d total=%d page=%d, want 0 rows, total 1, page clamped to 1000000", page, len(got.Data), got.Meta.Total, got.Meta.Page)
		}
		body := expectCMDBStatus(t, env.do(t, http.MethodGet, "/v1/users?per_page=100&page="+page, admin, nil), http.StatusOK, "users page="+page)
		var users struct {
			Data []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &users); err != nil || len(users.Data) != 0 {
			t.Fatalf("users page=%s returned %d rows (err %v), want an empty page", page, len(users.Data), err)
		}
	}
}
