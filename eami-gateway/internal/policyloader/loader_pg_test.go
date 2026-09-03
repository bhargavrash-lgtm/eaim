// loader_pg_test.go -- eami-gateway/internal/policyloader
//
// Integration test for Loader.Load's real DB round trip of
// policy_conditions.tool_server_ids (B-044/migration 009), against a REAL
// Postgres, following the pattern established throughout this session
// (TEST_DATABASE_URL/POSTGRES_PASSWORD fallback, throwaway fixtures via
// t.Cleanup).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/policyloader/... -v
package policyloader

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/testdb"
	policy "github.com/eami/policy"
)

// TestLoad_ToolServerIDs_RealDBRoundTrip proves AC2's DB-level wiring: a
// policy rule authored with tool_server_ids in Postgres is correctly
// scanned into Conditions.ToolServerIDs by queryRules, and the resulting
// evaluator honors it -- matches a call resolved to that specific
// gateway_tools.id, does not match a different one or an unresolved call.
//
// B-122/B-140: provisions its own fresh, isolated throwaway database via
// internal/testdb -- no per-org DELETE cleanup needed any more.
func TestLoad_ToolServerIDs_RealDBRoundTrip(t *testing.T) {
	pool := testdb.NewThrowawayPool(t)
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "policyloader-test-"+orgID.String()[:8], "policyloader-test-"+orgID.String()); err != nil {
		t.Fatalf("insert test org: %v", err)
	}

	targetServerID := uuid.New().String()

	policyID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO policies (id, org_id, name, priority, action, status)
		VALUES ($1, $2, $3, 1, 'deny', 'active')
	`, policyID, orgID, "policyloader-test-rule"); err != nil {
		t.Fatalf("insert policy: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO policy_conditions (id, policy_id, tool_server_ids)
		VALUES ($1, $2, $3)
	`, uuid.New(), policyID, []string{targetServerID}); err != nil {
		t.Fatalf("insert policy_conditions: %v", err)
	}

	loader := New(pool)
	if err := loader.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ev := loader.Evaluator()

	// OrgID set to the org this test itself seeded (B-128) -- isolates
	// ToolServerID as the one variable under test in each case below,
	// rather than incidentally relying on the org guard to also produce
	// a "not deny" result for the wrong reason.
	matching, err := ev.Evaluate(ctx, policy.ActionContext{OrgID: orgID.String(), ToolServerID: targetServerID})
	if err != nil {
		t.Fatalf("Evaluate (matching): %v", err)
	}
	if matching.Action != policy.ActionDeny {
		t.Errorf("Evaluate with matching ToolServerID: Action = %q, want %q", matching.Action, policy.ActionDeny)
	}

	nonMatching, err := ev.Evaluate(ctx, policy.ActionContext{OrgID: orgID.String(), ToolServerID: uuid.New().String()})
	if err != nil {
		t.Fatalf("Evaluate (non-matching): %v", err)
	}
	if nonMatching.Action == policy.ActionDeny {
		t.Errorf("Evaluate with a different ToolServerID: Action = %q, want default (not deny)", nonMatching.Action)
	}

	unresolved, err := ev.Evaluate(ctx, policy.ActionContext{OrgID: orgID.String(), ToolServerID: ""})
	if err != nil {
		t.Fatalf("Evaluate (unresolved): %v", err)
	}
	if unresolved.Action == policy.ActionDeny {
		t.Errorf("Evaluate with an unresolved (empty) ToolServerID: Action = %q, want default (not deny)", unresolved.Action)
	}
}

// TestLoad_OrgScoping_CrossOrgDispatchNoLongerMatches is B-128's negative-
// control proof (AC2). It reproduces, almost verbatim, the exact
// adversarial repro used in the original investigation (a throwaway probe
// program run once against the real dev DB, then deleted): an
// ActionContext structurally belonging to Org B, calling a tool name
// ("claude") that matches an active policy actually owned by Org A --
// mirroring the investigation's real find of a dispatch for
// "b121-liveverify" matching Dev Org's own "Escalate Claude connector
// calls" policy. Before the B-128 fix this matched and returned escalate;
// after the fix, queryRules scans Rule.OrgID and matchesRule's org guard
// must reject it, falling through to the default (not escalate). A
// same-org positive control runs in the same test so a passing negative
// control can't be explained by an org filter that's simply too broad
// (e.g. one that broke the tool_names match itself).
// B-122/B-140: provisions its own fresh, isolated throwaway database via
// internal/testdb -- no per-org DELETE cleanup needed any more.
func TestLoad_OrgScoping_CrossOrgDispatchNoLongerMatches(t *testing.T) {
	pool := testdb.NewThrowawayPool(t)
	ctx := context.Background()

	orgA := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgA, "b128-neg-org-a-"+orgA.String()[:8], "b128-neg-org-a-"+orgA.String()); err != nil {
		t.Fatalf("insert org A: %v", err)
	}
	orgB := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgB, "b128-neg-org-b-"+orgB.String()[:8], "b128-neg-org-b-"+orgB.String()); err != nil {
		t.Fatalf("insert org B: %v", err)
	}

	// Org A owns a real active policy matching a tool literally named
	// "claude" -- the exact shape of the investigation's real repro
	// against Dev Org's own "Escalate Claude connector calls" policy.
	policyID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO policies (id, org_id, name, priority, action, status)
		VALUES ($1, $2, $3, 2, 'escalate', 'active')
	`, policyID, orgA, "b128-neg-escalate-claude"); err != nil {
		t.Fatalf("insert org A policy: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO policy_conditions (id, policy_id, tool_names)
		VALUES ($1, $2, $3)
	`, uuid.New(), policyID, []string{"claude"}); err != nil {
		t.Fatalf("insert org A conditions: %v", err)
	}

	loader := New(pool)
	if err := loader.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	ev := loader.Evaluator()

	// Org B's dispatch, calling the same tool name -- pre-fix, this
	// matched Org A's policy and returned escalate. Post-fix it must not.
	d, err := ev.Evaluate(ctx, policy.ActionContext{
		OrgID: orgB.String(), AgentName: "b128-neg-org-b-agent", ToolName: "claude", ActionType: "execute",
	})
	if err != nil {
		t.Fatalf("Evaluate (cross-org): %v", err)
	}
	if d.Action == policy.ActionEscalate {
		t.Errorf("Org B's dispatch matched Org A's policy: action=%s policyID=%v -- "+
			"cross-org policy match is still possible, the B-128 fix did not close it",
			d.Action, d.PolicyID)
	}

	// Positive control: Org A's OWN dispatch on the same tool must still
	// match its own policy -- proves the fix filters by org, not that it
	// accidentally broke the tool_names match itself.
	dOwn, err := ev.Evaluate(ctx, policy.ActionContext{
		OrgID: orgA.String(), AgentName: "b128-neg-org-a-agent", ToolName: "claude", ActionType: "execute",
	})
	if err != nil {
		t.Fatalf("Evaluate (own org): %v", err)
	}
	if dOwn.Action != policy.ActionEscalate || dOwn.PolicyID == nil || *dOwn.PolicyID != policyID.String() {
		t.Errorf("Org A's own dispatch: action=%s policyID=%v, want escalate/%s -- "+
			"the org filter must not break same-org matching", dOwn.Action, dOwn.PolicyID, policyID)
	}
}
