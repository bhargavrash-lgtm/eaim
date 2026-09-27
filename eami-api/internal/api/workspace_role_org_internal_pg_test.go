package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/store"
)

// B-233, layer 1 in isolation: requireWorkspaceRole's org-admin branch must
// never invoke the wrapped handler for a workspace outside the admin's own
// org. A sentinel handler stands in for the real ones, so this fails the
// moment the ownership check is removed -- independent of any handler-level
// (SQL) scoping, which workspace_member_org_pg_test.go's store test covers.
func TestRequireWorkspaceRole_AdminOwnershipCheck_RealDB(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		pw := os.Getenv("POSTGRES_PASSWORD")
		if pw == "" {
			t.Skip("skipping: set TEST_DATABASE_URL or POSTGRES_PASSWORD to run against a real Postgres")
		}
		dsn = fmt.Sprintf("postgresql://eami_app:%s@localhost:5432/eami", pw)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// t.Cleanup registered before the org DELETE cleanups below, so it runs
	// last (CLAUDE.md's mandatory real-Postgres pool lifecycle rule).
	t.Cleanup(func() { pool.Close() })

	newOrg := func(label string) uuid.UUID {
		id := uuid.New()
		name := label + "-" + id.String()[:8]
		if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`, id, name, name); err != nil {
			t.Fatalf("seed org: %v", err)
		}
		t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, id) })
		return id
	}
	newWorkspace := func(orgID uuid.UUID) uuid.UUID {
		var groupID, wsID uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO groups (org_id, name) VALUES ($1, $2) RETURNING id`, orgID, "b233-mw-"+uuid.NewString()[:8]).Scan(&groupID); err != nil {
			t.Fatalf("seed group: %v", err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO workspaces (org_id, group_id, name) VALUES ($1, $2, $3) RETURNING id`, orgID, groupID, "b233 mw workspace").Scan(&wsID); err != nil {
			t.Fatalf("seed workspace: %v", err)
		}
		return wsID
	}
	orgA, orgB := newOrg("b233-mw-a"), newOrg("b233-mw-b")
	wsA, wsB := newWorkspace(orgA), newWorkspace(orgB)

	s := &Server{queries: store.New(pool)}
	for _, minRole := range []string{"workspace_admin", "workspace_member"} {
		called := false
		r := chi.NewRouter()
		r.With(s.requireWorkspaceRole("workspaceId", minRole)).Get("/w/{workspaceId}", func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusNoContent)
		})
		serve := func(ws uuid.UUID) (int, bool) {
			called = false
			req := httptest.NewRequest(http.MethodGet, "/w/"+ws.String(), nil)
			req = req.WithContext(context.WithValue(req.Context(), ctxClaims, userClaims{UserID: uuid.New(), OrgID: orgA, Role: "admin"}))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			return rec.Code, called
		}
		if code, reached := serve(wsB); reached || code != http.StatusNotFound {
			t.Fatalf("%s: org-A admin on org B's workspace: status=%d handlerReached=%v, want 404 and never reached", minRole, code, reached)
		}
		if code, reached := serve(uuid.New()); reached || code != http.StatusNotFound {
			t.Fatalf("%s: org-A admin on a nonexistent workspace: status=%d handlerReached=%v, want 404 and never reached", minRole, code, reached)
		}
		if code, reached := serve(wsA); !reached || code != http.StatusNoContent {
			t.Fatalf("%s: org-A admin on its own workspace: status=%d handlerReached=%v, want the handler reached", minRole, code, reached)
		}
	}
}
