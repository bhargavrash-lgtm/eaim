// dispatcher_org_scoping_test.go -- cmd/gateway
//
// B-128's centerpiece proof (AC1): the policy evaluation pipeline had no
// org_id scoping anywhere -- policyloader.queryRules() never selected
// org_id, and eami-policy's Rule/Conditions/ActionContext carried no
// OrgID field at all, so Loader.store() built exactly ONE global
// policy.Evaluator shared across every org's dispatches, process-wide.
// Confirmed live against the real dev DB before this fix: a dispatch
// structurally belonging to one org could be matched, decided, and
// escalated/denied by a policy belonging to a completely different org.
//
// This test seeds two REAL orgs with genuinely conflicting policies at
// the SAME priority on the SAME tool name (Org A denies, Org B allows),
// both loaded into the one real *policyloader.Loader a production
// gateway process actually runs (see main.go), and dispatches through a
// real *Dispatcher for each org. Org A's dispatch must be denied and Org
// B's must be allowed, in either order -- proving the fix isolates by
// org_id, not by incidental DB row/priority ordering.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./cmd/gateway/... -run TestDispatch_OrgScoping -v
package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/aiprovider"
	"github.com/eami/gateway/internal/approval"
	"github.com/eami/gateway/internal/audit"
	"github.com/eami/gateway/internal/episode"
	"github.com/eami/gateway/internal/mcp"
	"github.com/eami/gateway/internal/policyloader"
	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/toolrouter"
)

func TestDispatch_OrgScoping_TwoOrgsConflictingPolicies(t *testing.T) {
	env := newMainTestEnv(t)
	ctx := context.Background()
	agentAID, agentAName := env.insertAgent(t)

	// A second, real org and its own agent -- Org B. Same pattern as
	// TestResolveDynamicTool_CrossOrg_ReturnsNil's inline second-org setup.
	orgB := uuid.New()
	if _, err := env.pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgB, "b128-org-b-"+orgB.String()[:8], "b128-org-b-"+orgB.String()); err != nil {
		t.Fatalf("insert org B: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgB)
	})
	agentBID := uuid.New()
	agentBName := "b128-org-b-agent-" + agentBID.String()[:8]
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		agentBID, orgB, agentBName, "test-model", "test-owner", "test scope",
	); err != nil {
		t.Fatalf("insert org B agent: %v", err)
	}

	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(downstream.Close)
	fwd := proxy.New(proxy.Config{DownstreamURL: downstream.URL}, downstream.Client())

	toolRouter := toolrouter.New(env.pool, nil)
	aiProviderRouter := aiprovider.New(env.pool, nil, map[string]aiprovider.Adapter{})

	holdTimeout := 5 * time.Second
	approvalRouter := approval.New(env.pool, fwd, holdTimeout, "", "", toolRouter, aiProviderRouter)
	runCtx, cancel := context.WithCancel(context.Background())
	go approvalRouter.Run(runCtx)
	t.Cleanup(cancel)

	episodeRecorder := episode.New(env.pool)
	auditWriter, err := audit.NewWriter(ctx, env.pool)
	if err != nil {
		t.Fatalf("audit.NewWriter: %v", err)
	}

	toolName := "b128-conflict-tool-" + uuid.New().String()[:8]

	// Org A: DENY this tool, priority 1.
	policyA := uuid.New()
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO policies (id, org_id, name, priority, action, status)
		VALUES ($1, $2, $3, 1, 'deny', 'active')
	`, policyA, env.orgID, "b128-org-a-deny"); err != nil {
		t.Fatalf("insert org A policy: %v", err)
	}
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO policy_conditions (id, policy_id, tool_names)
		VALUES ($1, $2, $3)
	`, uuid.New(), policyA, []string{toolName}); err != nil {
		t.Fatalf("insert org A conditions: %v", err)
	}

	// Org B: ALLOW the identical tool name, the SAME priority -- a real,
	// genuinely conflicting policy, not merely a different one.
	policyB := uuid.New()
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO policies (id, org_id, name, priority, action, status)
		VALUES ($1, $2, $3, 1, 'allow', 'active')
	`, policyB, orgB, "b128-org-b-allow"); err != nil {
		t.Fatalf("insert org B policy: %v", err)
	}
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO policy_conditions (id, policy_id, tool_names)
		VALUES ($1, $2, $3)
	`, uuid.New(), policyB, []string{toolName}); err != nil {
		t.Fatalf("insert org B conditions: %v", err)
	}

	// One real Loader, one real shared Evaluator built from BOTH orgs'
	// rows combined -- exactly main.go's production wiring, and exactly
	// the structural shape B-128 found unsafe.
	pLoader := policyloader.New(env.pool)
	if err := pLoader.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}

	dispatcher := NewDispatcher(
		toolRouter, aiProviderRouter, pLoader,
		auditWriter, episodeRecorder, approvalRouter, fwd,
		"", "", holdTimeout,
	)

	// Org A's dispatch must be denied by ITS OWN policy.
	acA := mcp.ActionContext{
		OrgID: env.orgID.String(), AgentID: agentAID.String(), AgentName: agentAName,
		Tool: toolName, Action: "deploy", SessionID: "b128-org-a-session", ReceivedAt: time.Now(),
	}
	_, errA := dispatcher.Dispatch(ctx, acA)
	var pd *mcp.PolicyDeniedError
	if !errors.As(errA, &pd) {
		t.Fatalf("Org A dispatch: err = %v, want *mcp.PolicyDeniedError -- "+
			"Org A's own deny policy must apply", errA)
	}

	// Org B's dispatch on the IDENTICAL tool name must be allowed by ITS
	// OWN policy, completely unaffected by Org A's deny rule. Without
	// org scoping, whichever policy sorts first by priority/id would win
	// for BOTH orgs -- this is the exact cross-org bleed B-128 found.
	acB := mcp.ActionContext{
		OrgID: orgB.String(), AgentID: agentBID.String(), AgentName: agentBName,
		Tool: toolName, Action: "deploy", SessionID: "b128-org-b-session", ReceivedAt: time.Now(),
	}
	if _, errB := dispatcher.Dispatch(ctx, acB); errB != nil {
		t.Fatalf("Org B dispatch: got error %v, want nil -- "+
			"Org B's own allow policy must apply, unaffected by Org A's deny rule on the "+
			"identically-named tool", errB)
	}
}
