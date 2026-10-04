package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

// B-252 C3: CMDB asset rows carry the endpoint fields Assets needs to replace
// Discover's list (os, last_seen, the four counts, has_report,
// scanner_status), null for agents and tools, matching GET /v1/endpoints for
// the same endpoints; and a server-side os filter (B-228).

type cmdbEndpointRow struct {
	ID              string            `json:"id"`
	AssetKind       string            `json:"asset_kind"`
	OS              *string           `json:"os"`
	LastSeen        *time.Time        `json:"last_seen"`
	AIAppCount      *int64            `json:"ai_app_count"`
	LocalModelCount *int64            `json:"local_model_count"`
	MCPServerCount  *int64            `json:"mcp_server_count"`
	GPUCount        *int64            `json:"gpu_count"`
	HasReport       *bool             `json:"has_report"`
	ScannerStatus   map[string]string `json:"scanner_status"`
}

func TestCMDBAssets_EndpointFieldsAndOSFilter_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "c3-cmdb-fields")
	seedDiscoveryLicense(t, env, ctx, orgID)
	viewer := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "viewer@c3.test", "viewer")

	seedEP := func(host, os string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		var osInfo any
		if os != "" {
			osInfo = `{"os":"` + os + `"}`
		}
		if err := env.pool.QueryRow(ctx, `INSERT INTO endpoints (org_id, agent_id, hostname, os_info) VALUES ($1, $2, $2, $3::jsonb) RETURNING id`, orgID, host, osInfo).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	win := seedEP("c3-win", "windows")
	lin := seedEP("c3-lin", "linux")
	never := seedEP("c3-never", "") // no os, no report (like a paste-created endpoint)
	if _, err := env.pool.Exec(ctx, `INSERT INTO endpoint_reports (endpoint_id, org_id, collected_at, report) VALUES
		($1, $3, now(), '{"gpus":[{"name":"g"}],"scanner_status":{"gpu":"ok","models":"disabled"}}'),
		($2, $3, now(), '{"gpus":null}')`, win, lin, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO endpoint_ai_apps (endpoint_id, report_id, name, detected_at)
		SELECT $1, r.id, 'app' || g, now() FROM endpoint_reports r, generate_series(1, 2) g WHERE r.endpoint_id = $1`, win); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO gateway_agents (org_id, name, model, owner, scope) VALUES ($1, 'c3-agent', 'm', 'qa', 'test')`, orgID); err != nil {
		t.Fatal(err)
	}

	list := func(query string) []cmdbEndpointRow {
		t.Helper()
		body := expectCMDBStatus(t, env.do(t, http.MethodGet, "/v1/cmdb/assets?per_page=100&"+query, viewer, nil), http.StatusOK, query)
		var out struct {
			Data []cmdbEndpointRow `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return out.Data
	}

	// Mixed list: endpoint fields populated for endpoints, null for others.
	byID := map[string]cmdbEndpointRow{}
	for _, r := range list("") {
		byID[r.ID] = r
		if r.AssetKind != "endpoint" && (r.OS != nil || r.LastSeen != nil || r.AIAppCount != nil || r.HasReport != nil || r.ScannerStatus != nil) {
			t.Fatalf("%s row %s has endpoint fields set: %+v", r.AssetKind, r.ID, r)
		}
	}
	w := byID[win.String()]
	if w.OS == nil || *w.OS != "windows" || w.LastSeen == nil || w.AIAppCount == nil || *w.AIAppCount != 2 ||
		w.GPUCount == nil || *w.GPUCount != 1 || w.HasReport == nil || !*w.HasReport || w.ScannerStatus["models"] != "disabled" {
		t.Fatalf("windows endpoint row = %+v", w)
	}
	n := byID[never.String()]
	if n.HasReport == nil || *n.HasReport || n.ScannerStatus != nil || n.OS != nil || n.AIAppCount == nil || *n.AIAppCount != 0 {
		t.Fatalf("never-reported endpoint row = %+v (want has_report=false, scanner_status=null, os=null)", n)
	}
	l := byID[lin.String()]
	if l.HasReport == nil || !*l.HasReport || l.ScannerStatus != nil {
		t.Fatalf("legacy linux endpoint row = %+v (want has_report=true, scanner_status=null: not known)", l)
	}

	// Same values as GET /v1/endpoints for the same endpoints.
	resp := env.do(t, http.MethodGet, "/v1/endpoints?per_page=100", viewer, nil)
	var eps struct {
		Data []struct {
			ID                 string            `json:"id"`
			AIAppCount         int64             `json:"ai_app_count"`
			GPUCount           int64             `json:"gpu_count"`
			HasReport          bool              `json:"has_report"`
			ScannerStatus      map[string]string `json:"scanner_status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&eps); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, e := range eps.Data {
		c := byID[e.ID]
		if c.AIAppCount == nil || *c.AIAppCount != e.AIAppCount || *c.GPUCount != e.GPUCount || *c.HasReport != e.HasReport || len(c.ScannerStatus) != len(e.ScannerStatus) {
			t.Fatalf("CMDB and /v1/endpoints disagree for %s: cmdb=%+v endpoints=%+v", e.ID, c, e)
		}
	}

	// OS filter: only matching endpoints; agents, tools and other OSes excluded.
	got := list("os=linux")
	if len(got) != 1 || got[0].ID != lin.String() {
		t.Fatalf("os=linux returned %+v, want only the linux endpoint", got)
	}
	if got := list("kind=endpoint&os=windows"); len(got) != 1 || got[0].ID != win.String() {
		t.Fatalf("kind=endpoint&os=windows returned %+v", got)
	}
	expectCMDBStatus(t, env.do(t, http.MethodGet, "/v1/cmdb/assets?os=WINDOWS", viewer, nil), http.StatusBadRequest, "os=WINDOWS")
	expectCMDBStatus(t, env.do(t, http.MethodGet, "/v1/cmdb/assets?os=freebsd", viewer, nil), http.StatusBadRequest, "os=freebsd")
}

// Without a Discovery license, endpoints (and their fields) stay excluded.
func TestCMDBAssets_EndpointFieldsHiddenWithoutLicense_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "c3-cmdb-nolicense")
	viewer := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "viewer@c3nl.test", "viewer")
	if _, err := env.pool.Exec(ctx, `INSERT INTO endpoints (org_id, agent_id, hostname, os_info) VALUES ($1, 'c3-nl', 'c3-nl', '{"os":"linux"}')`, orgID); err != nil {
		t.Fatal(err)
	}
	body := expectCMDBStatus(t, env.do(t, http.MethodGet, "/v1/cmdb/assets?os=linux", viewer, nil), http.StatusOK, "unlicensed os=linux")
	var out struct {
		Data []cmdbEndpointRow `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 0 {
		t.Fatalf("unlicensed org saw endpoints: %+v", out.Data)
	}
}

// Review M2: the os filter is the Endpoints view's own selection, so the
// sidebar's navigation counts ignore it (like category/type/kind). Review L3:
// a report whose gpus is not an array must not fail the list (gpu_count 0).
func TestCMDBAssets_CountsIgnoreOSAndMalformedGPUsDontFail_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "c3-cmdb-counts")
	seedDiscoveryLicense(t, env, ctx, orgID)
	viewer := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "viewer@c3c.test", "viewer")
	var winID uuid.UUID
	if err := env.pool.QueryRow(ctx, `INSERT INTO endpoints (org_id, agent_id, hostname, os_info) VALUES ($1, 'c3c-win', 'c3c-win', '{"os":"windows"}') RETURNING id`, orgID).Scan(&winID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO endpoints (org_id, agent_id, hostname, os_info) VALUES ($1, 'c3c-lin', 'c3c-lin', '{"os":"linux"}')`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO gateway_agents (org_id, name, model, owner, scope) VALUES ($1, 'c3c-agent', 'm', 'qa', 'test')`, orgID); err != nil {
		t.Fatal(err)
	}
	// A malformed report: gpus is an object, not an array.
	if _, err := env.pool.Exec(ctx, `INSERT INTO endpoint_reports (endpoint_id, org_id, collected_at, report) VALUES ($1, $2, now(), '{"gpus":{"bad":true}}')`, winID, orgID); err != nil {
		t.Fatal(err)
	}

	body := expectCMDBStatus(t, env.do(t, http.MethodGet, "/v1/cmdb/assets?kind=endpoint&os=linux", viewer, nil), http.StatusOK, "kind=endpoint&os=linux")
	var out struct {
		Counts []struct {
			Count int64 `json:"count"`
		} `json:"counts"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, c := range out.Counts {
		total += c.Count
	}
	if total != 3 { // 2 endpoints + 1 agent: not narrowed to the 1 linux endpoint
		t.Fatalf("navigation counts total = %d, want 3 (os must not narrow the sidebar)", total)
	}

	// The malformed report doesn't 500 either list, and counts as 0 GPUs.
	all := expectCMDBStatus(t, env.do(t, http.MethodGet, "/v1/cmdb/assets?per_page=100", viewer, nil), http.StatusOK, "mixed list with a malformed report")
	var rows struct {
		Data []cmdbEndpointRow `json:"data"`
	}
	if err := json.Unmarshal([]byte(all), &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows.Data {
		if r.ID == winID.String() && (r.GPUCount == nil || *r.GPUCount != 0) {
			t.Fatalf("malformed gpus row gpu_count = %v, want 0", r.GPUCount)
		}
	}
	resp := env.do(t, http.MethodGet, "/v1/endpoints?per_page=100", viewer, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/v1/endpoints with a malformed report = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}
