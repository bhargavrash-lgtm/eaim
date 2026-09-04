// router_dispatch_test.go -- eami-gateway/internal/approval
//
// Integration tests for dispatchApproved (resume-time dynamic dispatch,
// AI Provider Connector brief) and its config-pinning/verification fix
// (the TOCTOU gap found live during this brief's own verification): an
// approved escalation for a dynamically-registered tool (rest_api via
// toolRouter, ai_provider via aiProviderRouter) resumes against the
// SPECIFIC connector -- identity AND config both verified unchanged --
// that was pinned at escalation time, failing closed rather than
// re-resolving by name if that connector was edited or deleted during the
// hold window. An unregistered tool name (no pinning ever applied) still
// falls back to the static fwd proxy exactly as before this fix.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/approval/... -run TestDispatchApproved -v
package approval

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/aiprovider"
	"github.com/eami/gateway/internal/toolrouter"
)

// fakeProviderAdapter records that it was actually called, and by which
// name, so tests can assert dispatchApproved reached the resolved
// connector's own adapter, not a generic fallback.
type fakeProviderAdapter struct {
	name     string
	gotCalls int
}

func (f *fakeProviderAdapter) Provider() string { return f.name }
func (f *fakeProviderAdapter) Dispatch(_ context.Context, _ aiprovider.Credentials, req aiprovider.Request) (aiprovider.Response, error) {
	f.gotCalls++
	body, _ := json.Marshal(map[string]any{"handled_by": f.name, "action": req.Action})
	return aiprovider.Response{StatusCode: 200, Body: body}, nil
}

// resumeOutcome reads back approval_requests.resume_outcome for assertions.
func resumeOutcome(t *testing.T, env *approvalTestEnv, approvalID string) string {
	t.Helper()
	var outcome *string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT resume_outcome FROM approval_requests WHERE id = $1`, approvalID).Scan(&outcome); err != nil {
		t.Fatalf("read resume_outcome: %v", err)
	}
	if outcome == nil {
		return ""
	}
	return *outcome
}

// insertPendingApproval creates a real approval_requests row (Submit()'s
// job normally) with the given pinned resolved-connector fields, so
// dispatchApproved's resume-time verification has a real row to check
// resume_outcome against afterward.
func insertPendingApproval(t *testing.T, env *approvalTestEnv, req Request) string {
	t.Helper()
	id, err := env.router.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return id
}

// ─── AI provider: pinned connector unchanged -> dispatches ────────────────

func TestDispatchApproved_AIProviderConnector_UnchangedConfig_Dispatches(t *testing.T) {
	env := newApprovalTestEnv(t, 5*time.Second)

	// No stored credentials (nil) -- deliberately, so this test can use a
	// nil cipher and focus purely on proving the pinning/verification
	// mechanism; real decrypt-then-dispatch mechanics are already covered
	// by aiprovider/dispatch_test.go's own TestDispatch_ValidCredentials_*.
	toolID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, provider, audit_mode)
		VALUES ($1, $2, $3, 'ai_provider', 'api_key', $4, 'full')
	`, toolID, env.orgID, "claude", "claude"); err != nil {
		t.Fatalf("insert ai_provider connector: %v", err)
	}

	adapter := &fakeProviderAdapter{name: "claude"}
	env.router.aiProviderRouter = aiprovider.New(env.pool, nil, map[string]aiprovider.Adapter{"claude": adapter})

	req := env.newRequest()
	req.Tool = "claude"
	req.Action = "messages"
	req.ResolvedToolID = toolID.String()
	req.ResolvedConfigHash = ComputeConfigHash("ai_provider", "claude", nil, nil, nil)

	approvalID := insertPendingApproval(t, env, req)

	body, _, err := env.router.dispatchApproved(context.Background(), approvalID, req)
	if err != nil {
		t.Fatalf("dispatchApproved: %v", err)
	}
	if adapter.gotCalls != 1 {
		t.Fatalf("expected the pinned ai_provider adapter to be called exactly once, got %d calls", adapter.gotCalls)
	}
	var got map[string]any
	if err := json.Unmarshal(body.Body, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["handled_by"] != "claude" {
		t.Errorf("response handled_by = %v, want \"claude\"", got["handled_by"])
	}
	if outcome := resumeOutcome(t, env, approvalID); outcome != "dispatched" {
		t.Errorf("resume_outcome = %q, want \"dispatched\"", outcome)
	}
}

// ─── AI provider: config changed mid-hold -> fails closed ─────────────────

// TestDispatchApproved_AIProviderConnector_CredentialsRotatedMidHold_FailsClosed
// is the direct live proof the user asked for: actually change a
// connector's credentials while an approval is "pending" (simulating the
// exact TOCTOU window -- an admin/operator role edits the tool between
// escalation and approval), then confirm resume refuses to dispatch,
// rather than reasoning about it abstractly.
func TestDispatchApproved_AIProviderConnector_CredentialsRotatedMidHold_FailsClosed(t *testing.T) {
	env := newApprovalTestEnv(t, 5*time.Second)

	originalCreds := []byte("sealed-creds-ORIGINAL")
	toolID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, provider, audit_mode, credentials_encrypted)
		VALUES ($1, $2, $3, 'ai_provider', 'api_key', $4, 'full', $5)
	`, toolID, env.orgID, "claude", "claude", originalCreds); err != nil {
		t.Fatalf("insert ai_provider connector: %v", err)
	}

	adapter := &fakeProviderAdapter{name: "claude"}
	env.router.aiProviderRouter = aiprovider.New(env.pool, nil, map[string]aiprovider.Adapter{"claude": adapter})

	req := env.newRequest()
	req.Tool = "claude"
	req.Action = "messages"
	req.ResolvedToolID = toolID.String()
	// Pinned hash reflects the ORIGINAL credentials -- what a human
	// approver's review would have been based on.
	req.ResolvedConfigHash = ComputeConfigHash("ai_provider", "claude", originalCreds, nil, nil)
	approvalID := insertPendingApproval(t, env, req)

	// The exact attack this fix closes: while the escalation sits
	// "pending" (a human hasn't decided yet), an admin/operator-role
	// action rotates the connector's credentials -- a real UPDATE against
	// the real row, not a simulated/mocked change.
	attackerCreds := []byte("sealed-creds-SWAPPED-BY-OPERATOR")
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE gateway_tools SET credentials_encrypted = $1 WHERE id = $2`, attackerCreds, toolID); err != nil {
		t.Fatalf("simulate mid-hold credential rotation: %v", err)
	}

	// Now the approval is (hypothetically) approved and resume is attempted.
	_, _, err := env.router.dispatchApproved(context.Background(), approvalID, req)
	if err == nil {
		t.Fatal("expected dispatchApproved to refuse resuming against changed credentials, got nil error")
	}
	if !strings.Contains(err.Error(), "configuration changed") {
		t.Errorf("expected a clear config-changed error, got: %v", err)
	}
	if adapter.gotCalls != 0 {
		t.Fatalf("the swapped-credentials adapter call must never happen -- fail-closed was bypassed (adapter called %d times)", adapter.gotCalls)
	}
	if outcome := resumeOutcome(t, env, approvalID); outcome != "config_changed" {
		t.Errorf("resume_outcome = %q, want \"config_changed\"", outcome)
	}
}

// TestDispatchApproved_AIProviderConnector_RedactionRulesWeakenedMidHold_FailsClosed
// (B-156/B-167) is the exact adversarial scenario both this brief's
// mandatory code-review AND security-review passes independently flagged:
// a lower-privileged actor (one who cannot approve/deny this specific
// escalation, and whom the approver has no visibility into having acted)
// weakens a connector's redaction_rules during the hold window, hoping the
// resumed dispatch will silently send more of the original sensitive
// content externally than the approver believed they were authorizing.
// Mirrors TestDispatchApproved_AIProviderConnector_CredentialsRotatedMidHold_FailsClosed
// exactly, proving redaction_rules is now folded into ComputeConfigHash's
// fingerprint the same way credentials/base_url/action_paths already are.
func TestDispatchApproved_AIProviderConnector_RedactionRulesWeakenedMidHold_FailsClosed(t *testing.T) {
	env := newApprovalTestEnv(t, 5*time.Second)

	toolID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, provider, audit_mode, redaction_rules)
		VALUES ($1, $2, $3, 'ai_provider', 'api_key', $4, 'full', NULL)
	`, toolID, env.orgID, "claude", "claude"); err != nil {
		t.Fatalf("insert ai_provider connector (redaction_rules NULL -- fail-safe default, enabled): %v", err)
	}

	adapter := &fakeProviderAdapter{name: "claude"}
	env.router.aiProviderRouter = aiprovider.New(env.pool, nil, map[string]aiprovider.Adapter{"claude": adapter})

	req := env.newRequest()
	req.Tool = "claude"
	req.Action = "messages"
	req.ResolvedToolID = toolID.String()
	// Pinned hash reflects the ORIGINAL redaction_rules (NULL/default-
	// enabled) -- what a human approver's review would have been based on,
	// even though the approval UI itself never displays this setting.
	req.ResolvedConfigHash = ComputeConfigHash("ai_provider", "claude", nil, nil, nil)
	approvalID := insertPendingApproval(t, env, req)

	// The exact attack this fix closes: while the escalation sits
	// "pending", an admin/operator-role action disables redaction on the
	// connector -- a real UPDATE against the real row, not simulated.
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE gateway_tools SET redaction_rules = $1 WHERE id = $2`, []byte(`{"enabled": false}`), toolID); err != nil {
		t.Fatalf("simulate mid-hold redaction_rules weakening: %v", err)
	}

	_, redactedCount, err := env.router.dispatchApproved(context.Background(), approvalID, req)
	if err == nil {
		t.Fatal("expected dispatchApproved to refuse resuming against weakened redaction_rules, got nil error")
	}
	if !strings.Contains(err.Error(), "configuration changed") {
		t.Errorf("expected a clear config-changed error, got: %v", err)
	}
	if adapter.gotCalls != 0 {
		t.Fatalf("the weakened-redaction adapter call must never happen -- fail-closed was bypassed (adapter called %d times)", adapter.gotCalls)
	}
	if redactedCount != nil {
		t.Errorf("redactedCount = %v, want nil -- no real Router.Dispatch call happened", redactedCount)
	}
	if outcome := resumeOutcome(t, env, approvalID); outcome != "config_changed" {
		t.Errorf("resume_outcome = %q, want \"config_changed\"", outcome)
	}
}

// TestDispatchApproved_AIProviderConnector_RedactedCount_PropagatesThroughResume
// (B-156/B-167) proves the OTHER half of the code-review's finding: when a
// resume genuinely reaches Router.Dispatch, the real redaction count comes
// back through dispatchApproved's own return value as a non-nil *int
// (never silently 0-as-nil-in-disguise), which is what
// cmd/gateway/dispatcher.go's resolution-audit-entry construction depends
// on to distinguish "redaction ran and matched nothing" (Valid:true, 0)
// from "never reached Router.Dispatch at all" (Valid:false / nil).
func TestDispatchApproved_AIProviderConnector_RedactedCount_PropagatesThroughResume(t *testing.T) {
	env := newApprovalTestEnv(t, 5*time.Second)

	toolID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, provider, audit_mode)
		VALUES ($1, $2, $3, 'ai_provider', 'api_key', $4, 'full')
	`, toolID, env.orgID, "claude", "claude"); err != nil {
		t.Fatalf("insert ai_provider connector: %v", err)
	}

	adapter := &fakeProviderAdapter{name: "claude"}
	env.router.aiProviderRouter = aiprovider.New(env.pool, nil, map[string]aiprovider.Adapter{"claude": adapter})

	req := env.newRequest()
	req.Tool = "claude"
	req.Action = "messages"
	req.ResolvedToolID = toolID.String()
	req.ResolvedConfigHash = ComputeConfigHash("ai_provider", "claude", nil, nil, nil)
	req.Parameters = map[string]any{"text": "contact me at real@example.com"}
	approvalID := insertPendingApproval(t, env, req)

	_, redactedCount, err := env.router.dispatchApproved(context.Background(), approvalID, req)
	if err != nil {
		t.Fatalf("dispatchApproved: %v", err)
	}
	if redactedCount == nil {
		t.Fatal("redactedCount = nil, want a real non-nil count -- Router.Dispatch genuinely ran")
	}
	if *redactedCount != 1 {
		t.Errorf("redactedCount = %d, want 1 (one email matched)", *redactedCount)
	}
	if adapter.gotCalls != 1 {
		t.Fatalf("expected exactly 1 real dispatch, got %d", adapter.gotCalls)
	}
}

// TestDispatchApproved_RestAPITool_BaseURLChangedMidHold_FailsClosed is the
// identical live proof for rest_api tools (base_url swapped, not just
// credentials -- the more directly "different destination" variant of the
// same attack).
func TestDispatchApproved_RestAPITool_BaseURLChangedMidHold_FailsClosed(t *testing.T) {
	env := newApprovalTestEnv(t, 5*time.Second)

	const originalURL = "https://reviewed-and-approved.example.com/api"
	toolID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, base_url)
		VALUES ($1, $2, $3, 'rest_api', 'api_key', $4)
	`, toolID, env.orgID, "real-tool", originalURL); err != nil {
		t.Fatalf("insert rest_api tool: %v", err)
	}

	env.router.toolRouter = toolrouter.New(env.pool, nil)

	req := env.newRequest()
	req.Tool = "real-tool"
	req.Action = "call"
	req.ResolvedToolID = toolID.String()
	req.ResolvedConfigHash = ComputeConfigHash("rest_api", originalURL, nil, nil, nil)
	approvalID := insertPendingApproval(t, env, req)

	// Real mid-hold edit: base_url swapped to a different (here,
	// attacker-controlled-looking) destination.
	const attackerURL = "http://attacker-controlled.example.net/exfil"
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE gateway_tools SET base_url = $1 WHERE id = $2`, attackerURL, toolID); err != nil {
		t.Fatalf("simulate mid-hold base_url change: %v", err)
	}

	_, _, err := env.router.dispatchApproved(context.Background(), approvalID, req)
	if err == nil {
		t.Fatal("expected dispatchApproved to refuse resuming against a changed base_url, got nil error")
	}
	if !strings.Contains(err.Error(), "configuration changed") {
		t.Errorf("expected a clear config-changed error, got: %v", err)
	}
	if outcome := resumeOutcome(t, env, approvalID); outcome != "config_changed" {
		t.Errorf("resume_outcome = %q, want \"config_changed\"", outcome)
	}
}

// TestDispatchApproved_ConnectorDeletedMidHold_FailsClosed proves the
// delete-during-hold variant: the pinned connector is gone entirely by
// resume time (e.g. deleted, or deleted and a different tool created under
// the same name) -- must refuse, not silently fall back to a by-name
// lookup that could find a different row.
func TestDispatchApproved_ConnectorDeletedMidHold_FailsClosed(t *testing.T) {
	env := newApprovalTestEnv(t, 5*time.Second)

	toolID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, provider, credentials_encrypted)
		VALUES ($1, $2, $3, 'ai_provider', 'api_key', $4, $5)
	`, toolID, env.orgID, "claude", "claude", []byte("creds")); err != nil {
		t.Fatalf("insert ai_provider connector: %v", err)
	}

	env.router.aiProviderRouter = aiprovider.New(env.pool, nil, map[string]aiprovider.Adapter{"claude": &fakeProviderAdapter{name: "claude"}})

	req := env.newRequest()
	req.Tool = "claude"
	req.ResolvedToolID = toolID.String()
	req.ResolvedConfigHash = ComputeConfigHash("ai_provider", "claude", []byte("creds"), nil, nil)
	approvalID := insertPendingApproval(t, env, req)

	// Real mid-hold deletion.
	if _, err := env.pool.Exec(context.Background(), `DELETE FROM gateway_tools WHERE id = $1`, toolID); err != nil {
		t.Fatalf("simulate mid-hold connector deletion: %v", err)
	}

	_, _, err := env.router.dispatchApproved(context.Background(), approvalID, req)
	if err == nil {
		t.Fatal("expected dispatchApproved to refuse resuming against a deleted connector, got nil error")
	}
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("expected a clear connector-deleted error, got: %v", err)
	}
	if outcome := resumeOutcome(t, env, approvalID); outcome != "connector_deleted" {
		t.Errorf("resume_outcome = %q, want \"connector_deleted\"", outcome)
	}
}

// TestDispatchApproved_ActionPathsChangedMidHold_BaseURLUnchanged_FailsClosed
// is the direct live proof for the gap this brief's own mandatory security
// review found in ComputeConfigHash's first version: action_paths alone
// (base_url and credentials both left untouched) determines which real
// sub-path/HTTP method a given action dispatches to (toolrouter.Forward),
// so an admin/operator editing ONLY action_paths mid-hold -- e.g.
// redirecting the approved action to a completely different endpoint on
// the same host -- must be detected exactly like a base_url or credential
// change, not silently allowed through because the hash only covered
// those two fields.
func TestDispatchApproved_ActionPathsChangedMidHold_BaseURLUnchanged_FailsClosed(t *testing.T) {
	env := newApprovalTestEnv(t, 5*time.Second)

	const baseURL = "https://reviewed-and-approved.example.com"
	toolID := uuid.New()
	originalPaths := []byte(`{"transfer":{"path":"/payments/transfer","method":"POST"}}`)
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, base_url, action_paths)
		VALUES ($1, $2, $3, 'rest_api', 'api_key', $4, $5)
	`, toolID, env.orgID, "payments-tool", baseURL, originalPaths); err != nil {
		t.Fatalf("insert rest_api tool: %v", err)
	}

	env.router.toolRouter = toolrouter.New(env.pool, nil)

	req := env.newRequest()
	req.Tool = "payments-tool"
	req.Action = "transfer"
	req.ResolvedToolID = toolID.String()
	// Pinned hash includes the ORIGINAL action_paths -- what the approver's
	// review of "transfer -> POST /payments/transfer" was based on.
	req.ResolvedConfigHash = ComputeConfigHash("rest_api", baseURL, nil, originalPaths, nil)
	approvalID := insertPendingApproval(t, env, req)

	// Real mid-hold edit: base_url and credentials both left completely
	// untouched -- only action_paths changes, redirecting "transfer" to a
	// different endpoint on the same host.
	redirectedPaths := []byte(`{"transfer":{"path":"/admin/wipe-account","method":"DELETE"}}`)
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE gateway_tools SET action_paths = $1 WHERE id = $2`, redirectedPaths, toolID); err != nil {
		t.Fatalf("simulate mid-hold action_paths change: %v", err)
	}

	_, _, err := env.router.dispatchApproved(context.Background(), approvalID, req)
	if err == nil {
		t.Fatal("expected dispatchApproved to refuse resuming after action_paths changed mid-hold (base_url/credentials unchanged), got nil error")
	}
	if !strings.Contains(err.Error(), "configuration changed") {
		t.Errorf("expected a clear config-changed error, got: %v", err)
	}
	if outcome := resumeOutcome(t, env, approvalID); outcome != "config_changed" {
		t.Errorf("resume_outcome = %q, want \"config_changed\"", outcome)
	}
}

// ─── Regression guards: unregistered tool / nil routers still work ────────

func TestDispatchApproved_UnregisteredTool_FallsBackToStaticProxy(t *testing.T) {
	env := newApprovalTestEnv(t, 5*time.Second)

	req := env.newRequest()
	req.Tool = "never-registered-anywhere"
	// ResolvedToolID left empty -- this tool never resolved dynamically at
	// escalation time, exactly like every escalation before this brief.
	approvalID := insertPendingApproval(t, env, req)

	body, _, err := env.router.dispatchApproved(context.Background(), approvalID, req)
	if err != nil {
		t.Fatalf("dispatchApproved: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body.Body, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["ok"] != true {
		t.Errorf("expected the static proxy's fixed {\"ok\":true} response (newApprovalTestEnv's downstream fake), got %v", got)
	}
	if outcome := resumeOutcome(t, env, approvalID); outcome != "static_fallback" {
		t.Errorf("resume_outcome = %q, want \"static_fallback\"", outcome)
	}
}

func TestDispatchApproved_NilRouters_FallsBackToStaticProxy(t *testing.T) {
	env := newApprovalTestEnv(t, 5*time.Second) // toolRouter/aiProviderRouter left nil
	req := env.newRequest()
	approvalID := insertPendingApproval(t, env, req)

	body, _, err := env.router.dispatchApproved(context.Background(), approvalID, req)
	if err != nil {
		t.Fatalf("dispatchApproved: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body.Body, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["ok"] != true {
		t.Errorf("expected the static proxy fallback with nil routers, got %v", got)
	}
}

// ─── ComputeConfigHash unit properties ─────────────────────────────────────

func TestComputeConfigHash_DifferentTypesNeverCollide(t *testing.T) {
	restHash := ComputeConfigHash("rest_api", "same-value", []byte("same-creds"), nil, nil)
	aiHash := ComputeConfigHash("ai_provider", "same-value", []byte("same-creds"), nil, nil)
	if restHash == aiHash {
		t.Error("a rest_api and ai_provider row with identical base_url/provider and credential bytes must not hash equal")
	}
}

func TestComputeConfigHash_CredentialChangeAlwaysChangesHash(t *testing.T) {
	h1 := ComputeConfigHash("ai_provider", "claude", []byte("creds-v1"), nil, nil)
	h2 := ComputeConfigHash("ai_provider", "claude", []byte("creds-v2"), nil, nil)
	if h1 == h2 {
		t.Error("different credential bytes must produce different hashes")
	}
}

func TestComputeConfigHash_Deterministic(t *testing.T) {
	h1 := ComputeConfigHash("rest_api", "https://example.com", []byte("creds"), nil, nil)
	h2 := ComputeConfigHash("rest_api", "https://example.com", []byte("creds"), nil, nil)
	if h1 != h2 {
		t.Error("identical inputs must produce identical hashes")
	}
}
