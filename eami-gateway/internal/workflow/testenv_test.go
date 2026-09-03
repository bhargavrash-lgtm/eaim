// testenv_test.go -- eami-gateway/internal/workflow
//
// Real-Postgres test harness for Executor.Run. dispatch() itself
// (cmd/gateway/main.go) is an unexported local closure -- not importable
// from this package's tests, and refactoring it to be testable is exactly
// the kind of change this brief's own MUST-NOT-MODIFY boundary rules out
// (that's a change to B-057's dispatch/TOCTOU logic, not an extension of
// it). So this file reconstructs dispatch()'s real shape -- real
// policy.Evaluator, real audit.Writer, real episode.Recorder, real
// approval.Router (Submit/Hold/resume, same as main.go wires it), real
// aiprovider resolution/dispatch -- as a test-local buildTestDispatch,
// using ONLY the same exported constructors main.go itself calls.
//
// Test connectors are all type=ai_provider, dispatched via a fake Adapter
// (mirroring approval/router_dispatch_test.go's own fakeProviderAdapter
// pattern) rather than type=rest_api via toolrouter.Forward: toolrouter's
// real, correct SSRF guard (B-023-class, confirmed live during B-044)
// rejects loopback addresses, which any local httptest.Server resolves
// to -- a real security control, not a test inconvenience, and not this
// package's to bypass or weaken. router_dispatch_test.go's own "resumes
// and genuinely dispatches" proof uses ai_provider for the identical
// reason (only its fail-closed-before-ever-dispatching tests use
// rest_api, since those never actually reach Forward). This is a
// deliberate, disclosed simplification (omits main.go's fire-and-forget
// token-usage HTTP write, irrelevant to anything this package tests) but
// is otherwise the same real pipeline, not a mock.
//
// A second, newer disclosed simplification (code-review finding on
// B-124/125): this reconstruction does NOT write a second, resolution
// audit_log row when an escalation resolves (approved/denied/expired) --
// dispatcher.go's real Dispatch does, via recordEscalationResolutionAuditHook.
// Before B-124/125 this file's Escalate branch matched production exactly;
// now it doesn't, for that one behavior. A test written against THIS
// harness asserting a workflow escalation's audit_log row count will
// undercount by one versus what production actually writes -- know this
// before adding one.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/workflow/... -v
package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/aiprovider"
	"github.com/eami/gateway/internal/approval"
	"github.com/eami/gateway/internal/audit"
	"github.com/eami/gateway/internal/episode"
	"github.com/eami/gateway/internal/mcp"
	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/testdb"
	"github.com/eami/gateway/internal/toolrouter"
	policy "github.com/eami/policy"
)

type workflowTestEnv struct {
	pool      *pgxpool.Pool
	orgID     uuid.UUID
	agentID   uuid.UUID
	agentName string
	rules     []policy.Rule // mutated per test before building the evaluator
}

// newWorkflowTestEnv (B-122/B-140) provisions a fresh, isolated throwaway
// database per test via internal/testdb -- see that package's doc comment
// for why. No per-org DELETE cleanup is needed any more: the whole database
// is dropped by testdb.NewThrowawayPool's own t.Cleanup, registered before
// this function ever returns, so the ordering hazard the old comment here
// warned about (a plain defer pool.Close() racing ahead of a later
// t.Cleanup-registered DELETE -- BACKLOG.md's B-056) no longer applies:
// there is no DELETE left to race against.
func newWorkflowTestEnv(t *testing.T) *workflowTestEnv {
	t.Helper()
	pool := testdb.NewThrowawayPool(t)
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "workflow-exec-test-"+orgID.String()[:8], "workflow-exec-test-"+orgID.String()); err != nil {
		t.Fatalf("seed org: %v", err)
	}

	agentID := uuid.New()
	agentName := "wf-exec-agent-" + agentID.String()[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		agentID, orgID, agentName, "test-model", "test-owner", "test scope",
	); err != nil {
		t.Fatalf("seed gateway_agents: %v", err)
	}

	return &workflowTestEnv{pool: pool, orgID: orgID, agentID: agentID, agentName: agentName}
}

// insertAIProviderTool creates a real ai_provider gateway_tools row --
// dispatched via a fake Adapter (registered in the test's aiProviderRouter),
// never a real network call, sidestepping toolrouter's SSRF guard entirely
// (see file header). No credentials stored (nil) -- irrelevant to what
// this package tests; aiprovider/dispatch_test.go already covers real
// decrypt-then-dispatch mechanics.
func (e *workflowTestEnv) insertAIProviderTool(t *testing.T, name, provider string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, provider)
		VALUES ($1, $2, $3, 'ai_provider', 'api_key', $4)
	`, id, e.orgID, name, provider); err != nil {
		t.Fatalf("insert ai_provider tool %s: %v", name, err)
	}
	return id
}

// template returns the run-level ActionContext (agent identity only --
// Tool/Action/Parameters are always overwritten per step by the executor).
func (e *workflowTestEnv) template() mcp.ActionContext {
	return mcp.ActionContext{
		AgentID:     "agent:" + e.agentName,
		AgentUUID:   e.agentID.String(),
		AgentName:   e.agentName,
		OrgID:       e.orgID.String(),
		Environment: "development",
		SessionID:   "wf-run-" + uuid.NewString()[:8],
	}
}

// fakeAdapter mirrors approval/router_dispatch_test.go's fakeProviderAdapter:
// records every call (and the params it received) so tests can assert
// real dispatch reached it, and reads back exactly what request/response
// data actually flowed through -- not a stub that always says success.
type fakeAdapter struct {
	name  string
	calls []aiprovider.Request
}

func (f *fakeAdapter) Provider() string { return f.name }
func (f *fakeAdapter) Dispatch(_ context.Context, _ aiprovider.Credentials, req aiprovider.Request) (aiprovider.Response, error) {
	f.calls = append(f.calls, req)
	body, _ := json.Marshal(map[string]any{"ok": true, "handled_by": f.name, "action": req.Action, "params": req.Params})
	return aiprovider.Response{StatusCode: 200, Body: body}, nil
}

// dispatchEnv bundles the real, wired components one test needs --
// constructed per-test (not shared via workflowTestEnv) because
// approval.Router bakes its aiProviderRouter in at construction
// (New(...)'s signature), and different tests need different registered
// adapters.
type dispatchEnv struct {
	dispatch mcp.DecisionHandler
	exec     *Executor
}

// newDispatchEnv wires a real policy.Evaluator (from env.rules, read at
// call time so a test can mutate env.rules first), a real approval.Router
// (Submit/Hold, LISTEN/NOTIFY started for real), and a real
// aiprovider.Router backed by adapters -- then reconstructs dispatch()'s
// exact resolve->policy->escalate/allow/deny->dispatch->audit->episode
// shape as a closure, using only exported constructors (see file header).
func newDispatchEnv(t *testing.T, e *workflowTestEnv, adapters map[string]aiprovider.Adapter) *dispatchEnv {
	t.Helper()
	ctx := context.Background()

	toolRouter := toolrouter.New(e.pool, nil) // real; every test tool is ai_provider, so this never matches -- matches main.go's own resolution order (rest_api checked first, falls through)
	aiProviderRouter := aiprovider.New(e.pool, nil, adapters)

	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(downstream.Close)
	fwd := proxy.New(proxy.Config{DownstreamURL: downstream.URL}, downstream.Client())

	approvalRouter := approval.New(e.pool, fwd, 5*time.Second, "", "", toolRouter, aiProviderRouter)
	runCtx, cancel := context.WithCancel(ctx)
	go approvalRouter.Run(runCtx)
	t.Cleanup(cancel)

	evaluator := policy.NewEvaluator(e.rules)
	auditWriter, err := audit.NewWriter(ctx, e.pool)
	if err != nil {
		t.Fatalf("audit.NewWriter: %v", err)
	}
	episodeRecorder := episode.New(e.pool)

	// logAuditWriteFailure (B-126) mirrors cmd/gateway/dispatcher.go's
	// logAuditWriteFailureHook -- this closure's own reconstruction of
	// dispatch()'s real shape (see file header) previously discarded
	// auditWriter.Write's returned error at all 4 of its own call sites
	// below (`_ = auditWriter.Write(...)`), identically to the asymmetry
	// B-121 found and fixed in the real dispatcher.go. Fixed here purely so
	// a future internal/workflow test could exercise this scenario the
	// same way cmd/gateway's dispatcher_test.go now can -- not because any
	// existing test in this package currently asserts on it.
	logAuditWriteFailure := func(ac mcp.ActionContext, decision string, err error) {
		if err == nil {
			return
		}
		slog.Error("audit_log write failed",
			"org_id", ac.OrgID, "agent_name", ac.AgentName, "tool", ac.Tool, "action", ac.Action,
			"decision", decision, "session_id", ac.SessionID, "err", err)
	}

	dispatch := func(ctx context.Context, ac mcp.ActionContext) (json.RawMessage, error) {
		resolvedTool := resolveTestTool(ctx, toolRouter, ac.OrgID, ac.Tool)
		var resolvedProvider *aiprovider.ToolRow
		if resolvedTool == nil {
			resolvedProvider = resolveTestAIProvider(ctx, aiProviderRouter, ac.OrgID, ac.Tool)
		}

		pc := ac.ToPolicyContext()
		switch {
		case resolvedTool != nil:
			pc.ToolServerID = resolvedTool.ID
		case resolvedProvider != nil:
			pc.ToolServerID = resolvedProvider.ID
		}
		decision, evalErr := evaluator.Evaluate(ctx, pc)
		if evalErr != nil {
			decision.Action = policy.ActionAllow
		}

		orgUUID, _ := uuid.Parse(ac.OrgID)
		agentUUID, _ := uuid.Parse(ac.AgentUUID)
		// B-093: mirrors dispatcher.go's identical WorkflowRunID/StepIndex
		// wiring -- this closure is an independent reconstruction of
		// dispatch()'s real shape (see file header), so it needs the same
		// fix applied here too, not just in cmd/gateway/dispatcher.go.
		workflowRunUUID, _ := uuid.Parse(ac.WorkflowRunID)
		auditEntry := audit.Entry{
			OrgID: orgUUID, AgentID: agentUUID, AgentName: ac.AgentName,
			ToolName: ac.Tool, Action: ac.Action, Parameters: ac.Parameters,
			Timestamp:     ac.ReceivedAt,
			WorkflowRunID: workflowRunUUID,
			StepIndex:     ac.StepIndex,
		}
		if decision.PolicyID != nil {
			auditEntry.PolicyID = *decision.PolicyID
		}

		switch decision.Action {
		case policy.ActionDeny:
			auditEntry.Decision = "denied"
			logAuditWriteFailure(ac, auditEntry.Decision, auditWriter.Write(ctx, auditEntry))
			go episodeRecorder.Record(context.Background(), ac.OrgID, ac.AgentUUID, ac.AgentName,
				[]episode.Step{{ToolName: ac.Tool, Action: ac.Action, Params: ac.Parameters, Decision: "blocked", Timestamp: ac.ReceivedAt}}, "blocked")
			return nil, &mcp.PolicyDeniedError{Reason: decision.Reason, PolicyID: auditEntry.PolicyID}

		case policy.ActionEscalate:
			auditEntry.Decision = "escalated"
			logAuditWriteFailure(ac, auditEntry.Decision, auditWriter.Write(ctx, auditEntry))

			approvalReq := approval.Request{
				OrgID: ac.OrgID, AgentID: ac.AgentUUID, AgentName: ac.AgentName,
				Tool: ac.Tool, Action: ac.Action, Parameters: ac.Parameters, SessionID: ac.SessionID,
			}
			switch {
			case resolvedProvider != nil:
				approvalReq.ResolvedToolID = resolvedProvider.ID
				approvalReq.ResolvedConfigHash = approval.ComputeConfigHash("ai_provider", resolvedProvider.Provider, resolvedProvider.CredentialsEncrypted, nil)
			case resolvedTool != nil:
				baseURL := ""
				if resolvedTool.BaseURL != nil {
					baseURL = *resolvedTool.BaseURL
				}
				var actionPathsJSON []byte
				if len(resolvedTool.ActionPaths) > 0 {
					actionPathsJSON, _ = json.Marshal(resolvedTool.ActionPaths)
				}
				approvalReq.ResolvedToolID = resolvedTool.ID
				approvalReq.ResolvedConfigHash = approval.ComputeConfigHash("rest_api", baseURL, resolvedTool.CredentialsEncrypted, actionPathsJSON)
			}
			approvalID, submitErr := approvalRouter.Submit(ctx, approvalReq)
			if submitErr != nil {
				return nil, submitErr
			}
			// Compile-compatibility update only (B-124/125's Hold() signature
			// change) -- this file's own reconstructed dispatch shape does
			// NOT build a resolution audit_log row; that's dispatcher.go's
			// job (the real production code path, which this file exists to
			// approximate for internal/workflow's own tests -- see this
			// file's header). Confirmed with the user before touching this
			// file at all, same MAY-MODIFY-exception precedent as B-121's
			// apikey.go.
			holdOutcome := approvalRouter.Hold(ctx, approvalID, approvalReq)
			outcome := "success"
			if holdOutcome.Err != nil {
				outcome = "failed"
			}
			go episodeRecorder.Record(context.Background(), ac.OrgID, ac.AgentUUID, ac.AgentName,
				[]episode.Step{{ToolName: ac.Tool, Action: ac.Action, Params: ac.Parameters, Result: holdOutcome.Result, Decision: "escalated", Timestamp: ac.ReceivedAt}}, outcome)
			return holdOutcome.Result, holdOutcome.Err

		default: // ActionAllow
			toolReq := proxy.ToolRequest{ToolName: ac.Tool, Action: ac.Action, Params: ac.Parameters, SessionID: ac.SessionID}
			var tr proxy.ToolResponse
			var proxyErr error
			switch {
			case resolvedProvider != nil:
				var presp aiprovider.Response
				presp, proxyErr = aiProviderRouter.Dispatch(ctx, resolvedProvider, ac.Action, ac.Parameters)
				tr = proxy.ToolResponse{Status: presp.StatusCode, Body: presp.Body}
			case resolvedTool != nil:
				tr, proxyErr = toolRouter.Forward(ctx, resolvedTool, toolReq)
			default:
				tr, proxyErr = fwd.Forward(ctx, toolReq)
			}
			if proxyErr != nil {
				auditEntry.Decision = "denied"
				logAuditWriteFailure(ac, auditEntry.Decision, auditWriter.Write(ctx, auditEntry))
				return nil, proxyErr
			}
			auditEntry.Decision = "allowed"
			logAuditWriteFailure(ac, auditEntry.Decision, auditWriter.Write(ctx, auditEntry))
			go episodeRecorder.Record(context.Background(), ac.OrgID, ac.AgentUUID, ac.AgentName,
				[]episode.Step{{ToolName: ac.Tool, Action: ac.Action, Params: ac.Parameters, Result: tr.Body, Decision: "allowed", Timestamp: ac.ReceivedAt}}, "success")
			return tr.Body, nil
		}
	}

	return &dispatchEnv{
		dispatch: dispatch,
		exec:     New(e.pool, dispatch, staticEvaluatorSource{ev: evaluator}),
	}
}

// staticEvaluatorSource implements policy.EvaluatorSource (B-129) by
// returning the same, already-constructed Evaluator on every call --
// this test harness's evaluator is built once per newDispatchEnv call
// from e.rules, not backed by a live-reloading policyloader.Loader.
type staticEvaluatorSource struct{ ev policy.Evaluator }

func (s staticEvaluatorSource) Evaluator() policy.Evaluator { return s.ev }

// resolveTestTool/resolveTestAIProvider mirror cmd/gateway/main.go's
// resolveDynamicTool/resolveAIProviderTool exactly (same fail-open
// contract: nil on any non-match, never an error to the caller).
func resolveTestTool(ctx context.Context, tr *toolrouter.Router, orgID, tool string) *toolrouter.ToolRow {
	if orgID == "" || tool == "" {
		return nil
	}
	row, err := tr.Resolve(ctx, orgID, tool)
	if err != nil || row.Type != "rest_api" {
		return nil
	}
	return row
}

func resolveTestAIProvider(ctx context.Context, apr *aiprovider.Router, orgID, tool string) *aiprovider.ToolRow {
	if orgID == "" || tool == "" {
		return nil
	}
	row, err := apr.Resolve(ctx, orgID, tool)
	if err != nil {
		return nil
	}
	return row
}

// waitForPendingApproval polls approval_requests for the most recent
// pending row in orgID -- used by escalation tests where Executor.Run is
// deliberately running in a background goroutine, blocked inside Hold(),
// and the test needs the real approval_id Submit() generated to decide it.
func waitForPendingApproval(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var id string
		err := pool.QueryRow(context.Background(), `
			SELECT id FROM approval_requests WHERE org_id = $1 AND status = 'pending'
			ORDER BY created_at DESC LIMIT 1
		`, orgID).Scan(&id)
		if err == nil {
			return id
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for a pending approval_requests row")
	return ""
}

// decideTestApproval simulates eami-api's DecideApproval handler: writes
// the real decision columns and fires the real pg_notify the running
// approval.Router.Run listen-loop (started by newDispatchEnv) is waiting
// on -- the same mechanism a real admin's decision in the UI triggers,
// not a shortcut around it.
func decideTestApproval(t *testing.T, pool *pgxpool.Pool, approvalID, status string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		UPDATE approval_requests SET status = $1, decision_reason = 'test decision', decided_at = now() WHERE id = $2
	`, status, approvalID); err != nil {
		t.Fatalf("decide approval: %v", err)
	}
	payload, _ := json.Marshal(map[string]string{"approval_id": approvalID})
	if _, err := pool.Exec(ctx, `SELECT pg_notify('approval_decision', $1)`, string(payload)); err != nil {
		t.Fatalf("notify approval_decision: %v", err)
	}
}
