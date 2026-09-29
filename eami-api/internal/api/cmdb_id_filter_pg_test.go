package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// B-252 C1: GET /v1/cmdb/assets?id= narrows to exactly one asset (Agent
// Detail's Classification tab), stays org-scoped, leaves the sidebar counts
// unchanged, and keeps the existing read-role gate.
func TestCMDBAssets_IDFilter_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgA := seedTestOrg(t, ctx, env.pool, "cmdb-c1-a")
	orgB := seedTestOrg(t, ctx, env.pool, "cmdb-c1-b")
	admin := seedTestUser(t, ctx, env.pool, orgA)
	viewer := seedTestUser(t, ctx, env.pool, orgA)
	approver := seedTestUser(t, ctx, env.pool, orgA)
	adminToken := env.token(t, admin, orgA, "admin@c1.test", "admin")
	viewerToken := env.token(t, viewer, orgA, "viewer@c1.test", "viewer")
	approverToken := env.token(t, approver, orgA, "approver@c1.test", "approver")

	seedAgent := func(org uuid.UUID, name string) uuid.UUID {
		var id uuid.UUID
		if err := env.pool.QueryRow(ctx, `INSERT INTO gateway_agents(org_id,name,model,owner,scope) VALUES($1,$2,'m','qa','test') RETURNING id`, org, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	target := seedAgent(orgA, "c1-agent")
	seedAgent(orgA, "c1-agent-2") // a name-search for "c1-agent" would match both
	foreign := seedAgent(orgB, "c1-agent")

	type page struct {
		Data []struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			AssetKind      string `json:"asset_kind"`
			Classification struct {
				TypeID   string `json:"type_id"`
				TypeName string `json:"type_name"`
				Source   string `json:"classification_source"`
			} `json:"classification"`
		} `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
		Counts []struct {
			TypeID string `json:"type_id"`
			Count  int64  `json:"count"`
		} `json:"counts"`
	}
	get := func(tok, query string, want int) page {
		t.Helper()
		body := expectCMDBStatus(t, env.do(t, http.MethodGet, "/v1/cmdb/assets?"+query, tok, nil), want, query)
		var p page
		if want == http.StatusOK {
			if err := json.Unmarshal([]byte(body), &p); err != nil {
				t.Fatalf("%s: decode: %v", query, err)
			}
		}
		return p
	}

	one := get(viewerToken, fmt.Sprintf("kind=agent&id=%s", target), http.StatusOK)
	if one.Meta.Total != 1 || len(one.Data) != 1 || one.Data[0].ID != target.String() || one.Data[0].Name != "c1-agent" || one.Data[0].AssetKind != "agent" {
		t.Fatalf("id filter = %+v, want exactly the target agent", one)
	}
	if one.Data[0].Classification.Source != "default" {
		t.Fatalf("unclassified agent source = %q, want default", one.Data[0].Classification.Source)
	}

	// Org scoping: another org's agent id matches nothing, even with the same name.
	if x := get(viewerToken, fmt.Sprintf("kind=agent&id=%s", foreign), http.StatusOK); x.Meta.Total != 0 || len(x.Data) != 0 {
		t.Fatalf("foreign-org id returned %+v", x)
	}

	// The sidebar counts ignore the id filter (they drive navigation).
	all := get(viewerToken, "kind=agent", http.StatusOK)
	countMap := func(p page) map[string]int64 {
		m := map[string]int64{}
		for _, c := range p.Counts {
			m[c.TypeID] = c.Count
		}
		return m
	}
	if fmt.Sprint(countMap(all)) != fmt.Sprint(countMap(one)) { // fmt sorts map keys
		t.Fatalf("counts changed by id filter: %v vs %v", countMap(one), countMap(all))
	}

	// No existence oracle: a random id and a real id of another KIND return
	// exactly what a foreign-org id does (empty, 200).
	var toolID uuid.UUID
	if err := env.pool.QueryRow(ctx, `INSERT INTO gateway_tools(org_id,name,type,auth_type) VALUES($1,'c1-tool','mcp','api_key') RETURNING id`, orgA).Scan(&toolID); err != nil {
		t.Fatal(err)
	}
	for what, q := range map[string]string{
		"nonexistent id":      fmt.Sprintf("kind=agent&id=%s", uuid.New()),
		"tool id as an agent": fmt.Sprintf("kind=agent&id=%s", toolID),
	} {
		if x := get(viewerToken, q, http.StatusOK); x.Meta.Total != 0 || len(x.Data) != 0 {
			t.Fatalf("%s returned %+v", what, x)
		}
	}

	// After an admin reclassifies it to a DIFFERENT (non-default) type, the
	// very next id read returns the UPDATED value -- not merely a row. The
	// new type is distinct from the resolved default, so a stale read (or a
	// filter returning the pre-edit row) can't pass.
	var defaultType, aiCategory uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT id, category_id FROM ci_types WHERE org_id=$1 AND asset_kind='agent' AND is_default`, orgA).Scan(&defaultType, &aiCategory); err != nil {
		t.Fatal(err)
	}
	if one.Data[0].Classification.TypeID != defaultType.String() {
		t.Fatalf("before edit: type %s, want the default %s", one.Data[0].Classification.TypeID, defaultType)
	}
	body := expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/cmdb/types", adminToken, map[string]any{"category_id": aiCategory, "asset_kind": "agent", "name": "C1 Reclassify Target"}), http.StatusCreated, "create target type")
	var newType struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &newType); err != nil || newType.ID == "" || newType.ID == defaultType.String() {
		t.Fatalf("new type = %q (%v), must differ from default %s", newType.ID, err, defaultType)
	}
	expectCMDBStatus(t, env.do(t, http.MethodPatch, fmt.Sprintf("/v1/cmdb/assets/agent/%s/classification", target), adminToken, map[string]any{"ci_type_id": newType.ID}), http.StatusOK, "admin reclassify")
	after := get(viewerToken, fmt.Sprintf("kind=agent&id=%s", target), http.StatusOK)
	if len(after.Data) != 1 || after.Data[0].Classification.TypeID != newType.ID || after.Data[0].Classification.TypeName != "C1 Reclassify Target" || after.Data[0].Classification.Source != "explicit" {
		t.Fatalf("immediately after reclassify: %+v, want type %s (C1 Reclassify Target, explicit)", after.Data, newType.ID)
	}
	// And a reset returns to the default, again on the very next read.
	expectCMDBStatus(t, env.do(t, http.MethodPatch, fmt.Sprintf("/v1/cmdb/assets/agent/%s/classification", target), adminToken, map[string]any{"ci_type_id": nil}), http.StatusOK, "admin reset")
	if r := get(viewerToken, fmt.Sprintf("kind=agent&id=%s", target), http.StatusOK); len(r.Data) != 1 || r.Data[0].Classification.TypeID != defaultType.String() || r.Data[0].Classification.Source != "default" {
		t.Fatalf("after reset: %+v", r.Data)
	}

	get(viewerToken, "id=not-a-uuid", http.StatusBadRequest)
	get(approverToken, fmt.Sprintf("kind=agent&id=%s", target), http.StatusForbidden)
}
