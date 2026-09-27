package api_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/eami/api/internal/store"
)

// B-237: POST /v1/approvals stored the body's agent_id/policy_rule_id with no
// org check. approval_requests' agent/policy FKs are NO ACTION, so another
// org could permanently block a tenant from deleting its own agent or policy,
// and a nonexistent id returned a 500 echoing the FK error (an existence
// oracle). Same adversarial shape as B-141/B-232/B-233.

func approvalBody(agentID uuid.UUID, policyID *uuid.UUID) map[string]any {
	b := map[string]any{
		"agent_id": agentID.String(), "agent_name": "x", "tool_name": "t", "action": "a",
		"gateway_session_id": "b237-" + uuid.NewString()[:8], "justification": "j", "risk_level": "low",
	}
	if policyID != nil {
		b["policy_rule_id"] = policyID.String()
	}
	return b
}

func seedApprovalPolicy(t *testing.T, env *workspaceTestEnv, ctx context.Context, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := env.pool.QueryRow(ctx, `INSERT INTO policies(org_id,name,priority,action,status) VALUES($1,$2,$3,'deny','draft') RETURNING id`,
		orgID, "b237-"+uuid.NewString()[:8], 900000+time.Now().Nanosecond()%99999).Scan(&id); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	return id
}

func approvalsReferencing(t *testing.T, env *workspaceTestEnv, ctx context.Context, agentID, policyID uuid.UUID) int {
	t.Helper()
	var n int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM approval_requests WHERE agent_id=$1 OR policy_id=$2`, agentID, policyID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateApproval_CrossOrgReferencesRejected_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgA := seedTestOrg(t, ctx, env.pool, "b237-attacker")
	orgB := seedTestOrg(t, ctx, env.pool, "b237-victim")
	attackerOperator := env.token(t, seedTestUser(t, ctx, env.pool, orgA), orgA, "operator@b237-a.test", "operator")
	attackerAdmin := env.token(t, seedTestUser(t, ctx, env.pool, orgA), orgA, "admin@b237-a.test", "admin")
	victimAdmin := env.token(t, seedTestUser(t, ctx, env.pool, orgB), orgB, "admin@b237-b.test", "admin")
	ownAgent := seedConfigAgent(t, env, ctx, orgA, "b237-own-agent")
	victimAgent := seedConfigAgent(t, env, ctx, orgB, "b237-victim-agent")
	victimPolicy := seedApprovalPolicy(t, env, ctx, orgB)
	missing := uuid.New()
	// Registered after seedTestOrg, so it runs first (LIFO): if the fix ever
	// regresses, org A's rows referencing org B would otherwise (NO ACTION
	// FKs) block org B's cleanup delete and leak it.
	t.Cleanup(func() {
		env.pool.Exec(context.Background(), `DELETE FROM approval_requests WHERE agent_id=$1 OR policy_id=$2 OR org_id=$3`, victimAgent, victimPolicy, orgA)
	})

	for _, tok := range []struct{ name, token string }{{"operator", attackerOperator}, {"admin", attackerAdmin}} {
		// Foreign agent vs nonexistent agent: identical, pinned to the
		// ownership check's own response (the oracle is closed).
		foreign := expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/approvals", tok.token, approvalBody(victimAgent, nil)), http.StatusNotFound, tok.name+": approval for another org's agent")
		absent := expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/approvals", tok.token, approvalBody(missing, nil)), http.StatusNotFound, tok.name+": approval for a nonexistent agent")
		if foreign != absent {
			t.Fatalf("%s: foreign-agent 404 %q differs from nonexistent-agent 404 %q (existence oracle)", tok.name, foreign, absent)
		}
		if foreign != "{\"code\":\"not_found\",\"message\":\"agent not found\"}\n" {
			t.Fatalf("%s: foreign-agent response %q, want the ownership check's \"agent not found\"", tok.name, foreign)
		}
		// Own agent + foreign policy vs own agent + nonexistent policy.
		foreignPol := expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/approvals", tok.token, approvalBody(ownAgent, &victimPolicy)), http.StatusNotFound, tok.name+": approval citing another org's policy")
		absentPol := expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/approvals", tok.token, approvalBody(ownAgent, &missing)), http.StatusNotFound, tok.name+": approval citing a nonexistent policy")
		if foreignPol != absentPol || foreignPol != "{\"code\":\"not_found\",\"message\":\"policy not found\"}\n" {
			t.Fatalf("%s: foreign-policy 404 %q / nonexistent-policy 404 %q, want both the ownership check's \"policy not found\"", tok.name, foreignPol, absentPol)
		}
		// Foreign agent + foreign policy together.
		expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/approvals", tok.token, approvalBody(victimAgent, &victimPolicy)), http.StatusNotFound, tok.name+": foreign agent and policy")
	}
	// An unparseable policy_rule_id is rejected, not silently dropped.
	bad := approvalBody(ownAgent, nil)
	bad["policy_rule_id"] = "not-a-uuid"
	expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/approvals", attackerOperator, bad), http.StatusBadRequest, "unparseable policy_rule_id")

	if n := approvalsReferencing(t, env, ctx, victimAgent, victimPolicy); n != 0 {
		t.Fatalf("REGRESSION B-237: %d approval rows reference org B's agent/policy", n)
	}
	// The victim can still delete its own agent and policy.
	expectCMDBStatus(t, env.do(t, http.MethodDelete, fmt.Sprintf("/v1/gateway/policies/%s", victimPolicy), victimAdmin, nil), http.StatusNoContent, "victim deletes own policy")
	expectCMDBStatus(t, env.do(t, http.MethodDelete, fmt.Sprintf("/v1/gateway/agents/%s", victimAgent), victimAdmin, nil), http.StatusNoContent, "victim deletes own agent")
}

func TestCreateApproval_SameOrgStillWorks_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b237-same-org")
	operator := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "operator@b237.test", "operator")
	viewer := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "viewer@b237.test", "viewer")
	agent := seedConfigAgent(t, env, ctx, orgID, "b237-agent")
	policy := seedApprovalPolicy(t, env, ctx, orgID)
	t.Cleanup(func() { env.pool.Exec(context.Background(), `DELETE FROM approval_requests WHERE org_id=$1`, orgID) })

	expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/approvals", operator, approvalBody(agent, nil)), http.StatusCreated, "same-org approval, no policy")
	expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/approvals", operator, approvalBody(agent, &policy)), http.StatusCreated, "same-org approval citing own policy")
	var n int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM approval_requests WHERE org_id=$1 AND agent_id=$2`, orgID, agent).Scan(&n); err != nil || n != 2 {
		t.Fatalf("same-org approvals stored=%d err=%v, want 2", n, err)
	}
	expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/approvals", viewer, approvalBody(agent, nil)), http.StatusForbidden, "viewer create approval")
}

// Layer 2 in isolation: the store insert itself refuses a foreign or
// nonexistent agent/policy (no FK error, no row), so a caller that skipped
// the handler checks still could not create a cross-org reference.
func TestCreateApprovalStore_OrgScopedInsert_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgA := seedTestOrg(t, ctx, env.pool, "b237-store-a")
	orgB := seedTestOrg(t, ctx, env.pool, "b237-store-b")
	ownAgent := seedConfigAgent(t, env, ctx, orgA, "b237-store-own")
	victimAgent := seedConfigAgent(t, env, ctx, orgB, "b237-store-victim")
	ownPolicy := seedApprovalPolicy(t, env, ctx, orgA)
	victimPolicy := seedApprovalPolicy(t, env, ctx, orgB)
	t.Cleanup(func() {
		env.pool.Exec(context.Background(), `DELETE FROM approval_requests WHERE org_id=$1 OR agent_id=$2 OR policy_id=$3`, orgA, victimAgent, victimPolicy)
	})
	q := store.New(env.pool)
	params := func(agent uuid.UUID, policy *uuid.UUID) store.CreateApprovalParams {
		p := store.CreateApprovalParams{OrgID: orgA, AgentID: agent, AgentName: "x", ToolName: "t", Action: "a",
			Parameters: []byte(`{}`), Justification: "j", RiskLevel: "low", ExpiresAt: time.Now().Add(time.Hour),
			GatewaySessionID: "b237-store", GatewayNodeAddress: "n"}
		if policy != nil {
			p.PolicyID.Bytes, p.PolicyID.Valid = *policy, true
		}
		return p
	}
	for name, p := range map[string]store.CreateApprovalParams{
		"foreign agent":             params(victimAgent, nil),
		"nonexistent agent":         params(uuid.New(), nil),
		"own agent, foreign policy": params(ownAgent, &victimPolicy),
		"own agent, missing policy": func() store.CreateApprovalParams { m := uuid.New(); return params(ownAgent, &m) }(),
	} {
		if _, err := q.CreateApproval(ctx, p); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("store insert with %s: err=%v, want pgx.ErrNoRows (no row, no FK error)", name, err)
		}
	}
	if n := approvalsReferencing(t, env, ctx, victimAgent, victimPolicy); n != 0 {
		t.Fatalf("store-level wrong-org inserts created %d rows referencing org B", n)
	}
	if _, err := q.CreateApproval(ctx, params(ownAgent, &ownPolicy)); err != nil {
		t.Fatalf("store insert with own agent and policy: %v", err)
	}
}
