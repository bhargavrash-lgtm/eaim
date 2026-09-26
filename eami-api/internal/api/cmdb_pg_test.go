package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestCMDBClassification_RBACIsolationLicensingAndFilters_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgA := seedTestOrg(t, ctx, env.pool, "cmdb-b196-a")
	orgB := seedTestOrg(t, ctx, env.pool, "cmdb-b196-b")
	admin := seedTestUser(t, ctx, env.pool, orgA)
	operator := seedTestUser(t, ctx, env.pool, orgA)
	viewer := seedTestUser(t, ctx, env.pool, orgA)
	adminToken := env.token(t, admin, orgA, "admin@cmdb.test", "admin")
	operatorToken := env.token(t, operator, orgA, "operator@cmdb.test", "operator")
	viewerToken := env.token(t, viewer, orgA, "viewer@cmdb.test", "viewer")

	for _, tok := range []string{adminToken, operatorToken, viewerToken} {
		resp := env.do(t, http.MethodGet, "/v1/cmdb/classifications", tok, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("classification read role returned %d: %s", resp.StatusCode, readWSBody(resp))
		}
		resp.Body.Close()
	}
	for _, tok := range []string{operatorToken, viewerToken} {
		resp := env.do(t, http.MethodPost, "/v1/cmdb/categories", tok, map[string]any{"name": "Forbidden"})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("non-admin taxonomy write=%d want 403: %s", resp.StatusCode, readWSBody(resp))
		}
		resp.Body.Close()
	}

	resp := env.do(t, http.MethodPost, "/v1/cmdb/categories", adminToken, map[string]any{"name": "  Business   services  ", "sort_order": 40})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create category=%d: %s", resp.StatusCode, readWSBody(resp))
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodPost, "/v1/cmdb/categories", adminToken, map[string]any{"name": "business services"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("normalized duplicate=%d want 409: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()

	wsID := env.seedWorkspace(t, ctx, orgA, "CMDB workspace")
	var endpointA, agentA, toolA uuid.UUID
	if err := env.pool.QueryRow(ctx, `INSERT INTO endpoints(org_id,agent_id,hostname,workspace_id) VALUES($1,$2,'alpha-endpoint',$3) RETURNING id`, orgA, "b196-"+uuid.NewString(), wsID).Scan(&endpointA); err != nil {
		t.Fatal(err)
	}
	if err := env.pool.QueryRow(ctx, `INSERT INTO gateway_agents(org_id,name,model,owner,scope,workspace_id) VALUES($1,'alpha-agent','model','qa','test',$2) RETURNING id`, orgA, wsID).Scan(&agentA); err != nil {
		t.Fatal(err)
	}
	if err := env.pool.QueryRow(ctx, `INSERT INTO gateway_tools(org_id,name,type,auth_type) VALUES($1,'beta-tool','mcp','api_key') RETURNING id`, orgA).Scan(&toolA); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO gateway_agents(org_id,name,model,owner,scope) VALUES($1,'other-agent','model','qa','test')`, orgB); err != nil {
		t.Fatal(err)
	}

	resp = env.do(t, http.MethodGet, "/v1/cmdb/assets?per_page=1", viewerToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unlicensed mixed inventory=%d: %s", resp.StatusCode, readWSBody(resp))
	}
	var first struct {
		Data []struct {
			AssetKind string `json:"asset_kind"`
		} `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
		EndpointAvailable bool `json:"endpoint_inventory_available"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if first.EndpointAvailable || first.Meta.Total != 2 || len(first.Data) != 1 {
		t.Fatalf("unlicensed response=%+v, want two paginated non-endpoint assets", first)
	}
	resp = env.do(t, http.MethodGet, "/v1/cmdb/assets?kind=endpoint", viewerToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unlicensed endpoint filter=%d want 403: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodGet, "/v1/cmdb/assets?kind=endpoints%3BDELETE", viewerToken, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid kind=%d want 400: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodGet, fmt.Sprintf("/v1/cmdb/assets?workspace_id=%s&q=alpha", wsID), viewerToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("workspace/search filter=%d: %s", resp.StatusCode, readWSBody(resp))
	}
	var filtered struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&filtered); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if filtered.Meta.Total != 1 || len(filtered.Data) != 1 || filtered.Data[0].ID != agentA.String() {
		t.Fatalf("workspace/search result=%+v", filtered)
	}
	seedDiscoveryLicense(t, env, ctx, orgA)
	resp = env.do(t, http.MethodGet, "/v1/cmdb/assets?kind=endpoint", viewerToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("licensed endpoint inventory=%d: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()
	if _, err := env.pool.Exec(ctx, `INSERT INTO endpoints(org_id,agent_id,hostname) SELECT $1,'page-agent-'||g,'page-host-'||g FROM generate_series(1,201) g`, orgA); err != nil {
		t.Fatal(err)
	}
	resp = env.do(t, http.MethodGet, "/v1/cmdb/assets?kind=endpoint&page=9&per_page=25", viewerToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("endpoint page beyond 200=%d: %s", resp.StatusCode, readWSBody(resp))
	}
	var endpointPage struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&endpointPage); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if endpointPage.Meta.Total != 202 || len(endpointPage.Data) != 2 {
		t.Fatalf("page beyond 200=%+v, want total 202 and 2 rows", endpointPage)
	}

	var agentType uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT id FROM ci_types WHERE org_id=$1 AND asset_kind='agent' AND is_default`, orgA).Scan(&agentType); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/v1/cmdb/assets/agent/%s/classification", agentA)
	resp = env.do(t, http.MethodPatch, path, operatorToken, map[string]any{"ci_type_id": agentType})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator assignment=%d want 403: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodPatch, path, adminToken, map[string]any{"ci_type_id": agentType})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin assignment=%d: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()
	assertSource := func(want string) {
		t.Helper()
		get := env.do(t, http.MethodGet, "/v1/cmdb/assets?kind=agent&q=alpha-agent", viewerToken, nil)
		if get.StatusCode != http.StatusOK {
			t.Fatalf("classification read=%d: %s", get.StatusCode, readWSBody(get))
		}
		var body struct {
			Data []struct {
				Classification struct {
					Source string `json:"classification_source"`
				} `json:"classification"`
			} `json:"data"`
		}
		if err := json.NewDecoder(get.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		get.Body.Close()
		if len(body.Data) != 1 || body.Data[0].Classification.Source != want {
			t.Fatalf("classification source=%+v want %s", body.Data, want)
		}
	}
	assertSource("explicit")
	resp = env.do(t, http.MethodPatch, path, adminToken, map[string]any{"ci_type_id": nil})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset assignment=%d: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()
	assertSource("default")

	otherTokenUser := seedTestUser(t, ctx, env.pool, orgB)
	otherToken := env.token(t, otherTokenUser, orgB, "admin@other.test", "admin")
	resp = env.do(t, http.MethodPatch, path, otherToken, map[string]any{"ci_type_id": nil})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-org asset assignment=%d want 404: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()
	_ = endpointA
	_ = toolA
	_ = created
}

func TestCMDBClassification_AdminCRUDAndAtomicDefault_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "cmdb-b196-crud")
	admin := seedTestUser(t, ctx, env.pool, orgID)
	token := env.token(t, admin, orgID, "admin@cmdb-crud.test", "admin")

	resp := env.do(t, http.MethodPost, "/v1/cmdb/categories", token, map[string]any{"name": "Applications", "sort_order": 50})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create category=%d: %s", resp.StatusCode, readWSBody(resp))
	}
	var category struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&category); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodPatch, "/v1/cmdb/categories/"+category.ID, token, map[string]any{"name": "Business applications", "sort_order": 50})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename category=%d: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()

	createType := func(name string) string {
		t.Helper()
		resp := env.do(t, http.MethodPost, "/v1/cmdb/types", token, map[string]any{"category_id": category.ID, "asset_kind": "tool", "name": name})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create type=%d: %s", resp.StatusCode, readWSBody(resp))
		}
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return body.ID
	}
	replacementID := createType("Business connector")
	tempID := createType("Temporary connector")
	resp = env.do(t, http.MethodPatch, "/v1/cmdb/types/"+replacementID, token, map[string]any{"category_id": category.ID, "asset_kind": "tool", "name": "Business connector", "is_default": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set default=%d: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()
	var defaultCount int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM ci_types WHERE org_id=$1 AND asset_kind='tool' AND is_default`, orgID).Scan(&defaultCount); err != nil {
		t.Fatal(err)
	}
	if defaultCount != 1 {
		t.Fatalf("tool default count=%d want 1", defaultCount)
	}
	resp = env.do(t, http.MethodDelete, "/v1/cmdb/types/"+replacementID, token, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete default=%d want 409: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodDelete, "/v1/cmdb/types/"+tempID, token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete unused type=%d: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()

	resp = env.do(t, http.MethodPost, "/v1/cmdb/categories", token, map[string]any{"name": "Disposable"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create disposable category=%d: %s", resp.StatusCode, readWSBody(resp))
	}
	var disposable struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&disposable); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodDelete, "/v1/cmdb/categories/"+disposable.ID, token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete empty category=%d: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()
}
