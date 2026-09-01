// http_org_scoping_pg_test.go -- eami-gateway/internal/episode
//
// B-141's centerpiece proof, episode-read leg -- the confirmed cross-
// tenant episode-content read path -- against REAL Postgres (not
// fakeStore/fakeResolver, unlike http_test.go's other coverage): two real
// orgs, each with an agent sharing the IDENTICAL name, two real minted
// JWTs. A real episode row belongs to Org A only. Org A's own token must
// see it (AC1's third surface); Org B's token, presenting the identical
// agent name, must NOT (AC2's negative control -- the exact scenario the
// original investigation traced).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/episode/... -run TestListEpisodes_OrgScoping -v
package episode_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/episode"
	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/registry"
)

func episodeOrgScopingTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/127.0.0.1:5432 layout) to run episode org-scoping integration tests against a real Postgres")
	}
	return "postgresql://eami_app:" + pw + "@127.0.0.1:5432/eami"
}

func TestListEpisodes_OrgScoping_TwoOrgsSameAgentName_NeverLeaksAcrossOrgs(t *testing.T) {
	dsn := episodeOrgScopingTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(pool.Close)

	orgA := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgA, "b141-ep-org-a-"+orgA.String()[:8], "b141-ep-org-a-"+orgA.String()); err != nil {
		t.Fatalf("insert org A: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgA) })
	orgB := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgB, "b141-ep-org-b-"+orgB.String()[:8], "b141-ep-org-b-"+orgB.String()); err != nil {
		t.Fatalf("insert org B: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgB) })

	const sharedName = "shared-episode-agent"
	agentA := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, 'test-model', 'test-owner', 'test scope')`,
		agentA, orgA, sharedName); err != nil {
		t.Fatalf("insert org A agent: %v", err)
	}
	agentB := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, 'test-model', 'test-owner', 'test scope')`,
		agentB, orgB, sharedName); err != nil {
		t.Fatalf("insert org B agent: %v", err)
	}

	// A real episode, belonging to Org A only -- the exact shape of
	// content a real cross-tenant leak would expose (task description,
	// tool_call steps).
	episodeID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO episodes (id, org_id, agent_id, agent_name, task, steps, outcome)
		VALUES ($1, $2, $3, $4, $5, $6, 'success')`,
		episodeID, orgA, agentA, sharedName, "b141-secret-org-a-task",
		[]byte(`[{"tool_name":"salesforce","action":"delete_records","params":{"id":"top-secret"},"decision":"allowed"}]`),
	); err != nil {
		t.Fatalf("insert org A episode: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM episodes WHERE id = $1`, episodeID) })

	idm, err := identity.NewManager(filepath.Join(t.TempDir(), "gateway.key"), 300, "eami-gateway:primary")
	if err != nil {
		t.Fatalf("identity.NewManager: %v", err)
	}
	reg := registry.New(pool)
	reader := episode.NewReader(pool)
	h := episode.NewHTTPHandler(reader, idm, reg, "b141-unused-service-key")

	tokenA, err := idm.Issue(identity.IssueRequest{AgentID: "agent:" + sharedName, OrgID: orgA.String(), TTLSeconds: 300})
	if err != nil {
		t.Fatalf("issue org A token: %v", err)
	}
	tokenB, err := idm.Issue(identity.IssueRequest{AgentID: "agent:" + sharedName, OrgID: orgB.String(), TTLSeconds: 300})
	if err != nil {
		t.Fatalf("issue org B token: %v", err)
	}

	listAs := func(bearer string) []episode.Episode {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/gateway/episodes", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		h.ListEpisodes(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ListEpisodes: status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Data []episode.Episode `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return body.Data
	}

	// AC1's third surface: Org A's own token sees its own episode.
	gotA := listAs(tokenA.Token)
	foundOwn := false
	for _, e := range gotA {
		if e.ID == episodeID {
			foundOwn = true
		}
	}
	if !foundOwn {
		t.Errorf("Org A's own token: episode %s not found in %d results -- org A must see its own episode", episodeID, len(gotA))
	}

	// AC2, the negative control: Org B's token, presenting the IDENTICAL
	// agent name, must never see Org A's episode. Before B-141, this is
	// exactly the traced leak -- LookupByName's unscoped query could
	// resolve Org B's caller to Org A's "shared-episode-agent" row,
	// handing back Org A's real task/tool-call content.
	gotB := listAs(tokenB.Token)
	for _, e := range gotB {
		if e.ID == episodeID {
			t.Fatalf("Org B's token received Org A's episode (id=%s, task=%q) -- cross-tenant episode-content leak, "+
				"the exact B-141 vulnerability", e.ID, e.Task)
		}
	}
	if len(gotB) != 0 {
		t.Errorf("Org B's token: got %d episodes, want 0 (org B has none of its own)", len(gotB))
	}
}
