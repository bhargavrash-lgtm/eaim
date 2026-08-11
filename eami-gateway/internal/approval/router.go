// Package approval implements the gateway approval router (ADR-011).
//
// When an action matches an ESCALATE policy rule the dispatch pipeline:
//  1. Calls Submit() — persists the approval request to the DB and posts a Slack
//     notification with approve/deny deep-links to the EAMI web UI.
//  2. Calls Hold() — blocks the dispatch goroutine until a decision arrives (via
//     Postgres LISTEN/NOTIFY on "approval_decision") or the hold timeout elapses.
//
// Run() must be started as a long-lived goroutine from main. It holds a dedicated
// Postgres connection and calls LISTEN on "approval_decision". When eami-api
// decides an approval it sends:
//
//	pg_notify('approval_decision', '{"approval_id":"<uuid>"}')
//
// Run() fetches the decision row and signals the matching Hold() waiter.
// If no gateway node has a pending Hold() (e.g. the request timed out), the
// notification is silently dropped — other nodes handle their own pending map.
package approval

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/aiprovider"
	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/safego"
	"github.com/eami/gateway/internal/toolrouter"
)

// Request is the normalised escalation payload passed from the dispatch pipeline.
// It contains only what the approval router needs; it does not import the mcp package.
type Request struct {
	OrgID      string
	AgentID    string // gateway_agents.id UUID
	AgentName  string
	Tool       string
	Action     string
	Parameters map[string]any
	SessionID  string

	// ResolvedToolID/ResolvedConfigHash pin the exact dynamically-resolved
	// connector (gateway_tools.id) and a fingerprint of its security-
	// relevant config (base_url/provider + the encrypted credential
	// bytes -- never plaintext), captured by the caller (cmd/gateway/
	// main.go's dispatch closure) at the SAME moment it resolved the tool
	// for policy evaluation -- i.e. what the human approver's review is
	// actually based on. Submit() persists both; dispatchApproved
	// re-verifies both are unchanged before resuming, failing closed
	// (never falling back to a fresh by-name lookup or the static proxy)
	// if the connector was edited or deleted during the hold window.
	// Closes a real TOCTOU gap found live during this brief's own
	// verification: a lower-privileged admin/operator role (which cannot
	// itself approve/deny) could otherwise silently redirect an approved
	// call to a different destination than what was reviewed. Both empty
	// when Tool never resolved dynamically at escalation time (falls to
	// the static proxy, unaffected by this check -- ResumeOutcome
	// "static_fallback").
	ResolvedToolID     string
	ResolvedConfigHash string
}

// ComputeConfigHash fingerprints a connector's full security-relevant
// config: baseURL (rest_api) XOR provider (ai_provider), the encrypted
// credential bytes as stored (never the decrypted plaintext: this
// function needs no cipher/key, and hashing the ciphertext still changes
// on any credential rotation, since re-encryption always uses a fresh GCM
// nonce), and actionPathsJSON -- a canonical (encoding/json.Marshal of the
// map, which always sorts keys) serialization of a rest_api tool's
// action_paths mappings. actionPathsJSON is security-relevant, not
// cosmetic: it's what determines the actual sub-path/HTTP method a given
// action dispatches to (toolrouter.Forward), so a mid-hold edit to it
// alone -- with base_url/credentials untouched -- would otherwise redirect
// a specific approved action to a different destination undetected. Found
// live by this brief's own mandatory security review: the first version
// of this function omitted it. Empty/nil for ai_provider rows (no such
// concept) or a rest_api tool with no mappings.
//
// toolType is included so an ai_provider and a rest_api row can never
// coincidentally hash equal. Exported so cmd/gateway/main.go's dispatch
// closure -- which already resolves the tool before policy evaluation --
// can compute this once, at escalation time, without this package needing
// its own extra resolve call at Submit() time.
func ComputeConfigHash(toolType, baseURLOrProvider string, credentialsEncrypted []byte, actionPathsJSON []byte) string {
	h := sha256.New()
	h.Write([]byte(toolType))
	h.Write([]byte{0})
	h.Write([]byte(baseURLOrProvider))
	h.Write([]byte{0})
	h.Write(credentialsEncrypted)
	h.Write([]byte{0})
	h.Write(actionPathsJSON)
	return hex.EncodeToString(h.Sum(nil))
}

// decisionResult carries the resolved outcome of an approval to a waiting Hold().
type decisionResult struct {
	data json.RawMessage
	err  error
}

// pendingEntry tracks one blocked Hold() call.
type pendingEntry struct {
	ch  chan decisionResult // buffered(1); sender never blocks
	req Request             // kept so resolve() can call proxy.Forward on approval
}

// Router manages escalation approvals via Postgres LISTEN/NOTIFY.
type Router struct {
	pool         *pgxpool.Pool
	fwd          *proxy.Proxy
	holdTimeout  time.Duration
	slackWebhook string
	uiBaseURL    string
	pending      sync.Map // string(approvalID) → *pendingEntry

	// toolRouter/aiProviderRouter let an approved call resume through the
	// same dynamic-dispatch resolution the original (pre-escalation) call
	// used, instead of always falling back to the static fwd proxy --
	// closes a real gap found live during the AI Provider Connector
	// brief's own verification: resolve()'s "approved" case previously
	// called r.fwd.Forward unconditionally, so an approved escalation for
	// ANY dynamically-routed tool (rest_api, B-044, and ai_provider) never
	// actually reached the tool the approver reviewed -- it silently hit
	// the static proxy instead. B-044's own session had already found and
	// disclosed this exact gap for rest_api without fixing it; this closes
	// it for both types at once, scoped to resolve()'s resume dispatch
	// only. Either may be nil (tests that don't need dynamic resume can
	// omit them) -- see dispatchApproved's nil-safe fallback chain.
	toolRouter       *toolrouter.Router
	aiProviderRouter *aiprovider.Router
}

// New creates a Router.
//   - holdTimeout is the maximum time Hold() will wait before auto-denying.
//   - slackWebhook may be empty to disable Slack notifications.
//   - toolRouter/aiProviderRouter are optional (nil-safe): when set, an
//     approved escalation resumes through them first, falling back to fwd
//     exactly as before if neither resolves the tool. See dispatchApproved.
func New(
	pool *pgxpool.Pool,
	fwd *proxy.Proxy,
	holdTimeout time.Duration,
	slackWebhook string,
	uiBaseURL string,
	toolRouter *toolrouter.Router,
	aiProviderRouter *aiprovider.Router,
) *Router {
	return &Router{
		pool:             pool,
		fwd:              fwd,
		holdTimeout:      holdTimeout,
		slackWebhook:     slackWebhook,
		uiBaseURL:        uiBaseURL,
		toolRouter:       toolRouter,
		aiProviderRouter: aiProviderRouter,
	}
}

// defaultRiskLevel is used for every approval request until this codebase
// has an actual risk-classification concept to derive it from (there is
// none anywhere in the policy/schema today -- confirmed by inspection of
// policies/policy_conditions, not assumed). "medium" is a defensible,
// valid middle value under the risk_level CHECK constraint, not a
// fabricated signal presented as real classification. See BACKLOG.md for
// the follow-up item tracking this as a real, still-open design gap.
const defaultRiskLevel = "medium"

// Submit persists an approval request to approval_requests, fires a Slack
// notification, and returns the new approval UUID. The caller must then call
// Hold() with the returned ID to block until a decision arrives.
//
// approval_requests has five NOT NULL columns this function must supply
// that the Request the dispatch pipeline builds doesn't carry any richer
// signal for today: justification, risk_level, expires_at,
// gateway_session_id, gateway_node_address. Kept deliberately synthesized
// from data already available here (req.Tool/req.Action, r.holdTimeout,
// req.SessionID, this process's own hostname) rather than threading the
// policy engine's real escalation reason or the configured listen address
// through from cmd/gateway/main.go -- a scoped decision, not an oversight;
// see the doc comments on justification/gateway_node_address below for the
// exact tradeoff.
func (r *Router) Submit(ctx context.Context, req Request) (string, error) {
	// org_id/agent_id are NOT NULL FK columns (schema.sql). The original
	// code passed them through a nilIfEmpty helper that turned an empty
	// string into SQL NULL -- for a NOT NULL column that would just
	// reproduce this same bug class as a confusing DB constraint-violation
	// error instead of a clear one (code review, this task). Validated
	// directly here instead; nilIfEmpty had no other caller and was removed.
	if req.OrgID == "" || req.AgentID == "" {
		return "", fmt.Errorf("approval: org_id and agent_id are required")
	}

	approvalID, err := newUUID()
	if err != nil {
		return "", fmt.Errorf("approval: generate id: %w", err)
	}

	params, err := json.Marshal(req.Parameters)
	if err != nil {
		return "", fmt.Errorf("approval: marshal parameters: %w", err)
	}

	// justification: synthesized from the tool/action already in Request,
	// not the policy engine's actual per-rule escalation reason (that
	// lives in policy.Decision.Reason, which never reaches this function
	// -- threading it through would mean changing cmd/gateway/main.go's
	// dispatch closure, out of this fix's scope). Truthful (every
	// escalation genuinely did match some policy for this exact
	// tool/action), just less specific than the real reason would be.
	justification := fmt.Sprintf("Escalated by policy: %s.%s", req.Tool, req.Action)

	// gateway_node_address: this process's own hostname, not the
	// configured listen address (cfg.ListenAddr) -- getting that would
	// require a new Router constructor parameter threaded from main.go,
	// same out-of-scope tradeoff as justification above. os.Hostname()
	// keeps this fix fully self-contained in this package.
	nodeAddress, hostErr := os.Hostname()
	if hostErr != nil || nodeAddress == "" {
		nodeAddress = "unknown"
	}

	expiresAt := time.Now().Add(r.holdTimeout)

	// resolved_tool_id/resolved_config_hash pin the connector identity+
	// config at escalation time (see Request's doc comment) -- nil (SQL
	// NULL) when Tool never resolved dynamically, matching every other
	// optional-FK column in this table.
	var resolvedToolID, resolvedConfigHash any
	if req.ResolvedToolID != "" {
		resolvedToolID = req.ResolvedToolID
		resolvedConfigHash = req.ResolvedConfigHash
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO approval_requests
			(id, org_id, agent_id, agent_name, tool_name, action, parameters,
			 justification, risk_level, expires_at, gateway_session_id,
			 gateway_node_address, resolved_tool_id, resolved_config_hash, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, now())
	`,
		approvalID,
		req.OrgID,
		req.AgentID,
		req.AgentName,
		req.Tool,
		req.Action,
		params,
		justification,
		defaultRiskLevel,
		expiresAt,
		req.SessionID,
		nodeAddress,
		resolvedToolID,
		resolvedConfigHash,
	)
	if err != nil {
		return "", fmt.Errorf("approval: insert request: %w", err)
	}

	slog.Info("approval: request submitted",
		"approval_id", approvalID,
		"agent", req.AgentName,
		"tool", req.Tool,
		"action", req.Action,
	)

	r.notifySlack(approvalID, req)
	return approvalID, nil
}

// Hold registers a pending waiter for approvalID and blocks until:
//   - a decision arrives via Run() → if "approved", forwards via proxy; any
//     other status (denied/expired/etc.) returns an error
//   - holdTimeout elapses → marks the request expired and returns an error,
//     unless a decision already committed in the same instant (see below)
//   - ctx is cancelled (not a timeout) → returns ctx.Err(), no DB write
func (r *Router) Hold(ctx context.Context, approvalID string, req Request) (json.RawMessage, error) {
	entry := &pendingEntry{
		ch:  make(chan decisionResult, 1),
		req: req,
	}
	r.pending.Store(approvalID, entry)
	defer r.pending.Delete(approvalID)

	holdCtx, cancel := context.WithTimeout(ctx, r.holdTimeout)
	defer cancel()

	select {
	case res := <-entry.ch:
		return res.data, res.err

	case <-holdCtx.Done():
		// Security review (this task) flagged a real timing race: Go's
		// select doesn't have to prefer entry.ch just because a decision
		// technically arrived a moment earlier, so a genuinely approved
		// action could otherwise be reported as "timed out" and never
		// forwarded even though the DB itself was never wrong. One last
		// non-blocking check before falling through to the timeout path.
		select {
		case res := <-entry.ch:
			return res.data, res.err
		default:
		}

		if holdCtx.Err() != context.DeadlineExceeded {
			// Parent ctx was cancelled (e.g. caller disconnected), not a
			// real timeout -- don't write a misleading 'expired' status
			// for what was actually a cancellation.
			return nil, holdCtx.Err()
		}

		// Only commit 'expired' if the row is still genuinely pending. If
		// eami-api's DecideApproval already committed a real decision in
		// this same narrow window (after resolve() started but before it
		// reached entry.ch, or before resolve() ran at all), honor that
		// decision instead of overwriting it with a stale 'expired'
		// status and reporting a bogus timeout for an action that was
		// actually approved.
		var status, decisionReason string
		bg := context.Background()
		err := r.pool.QueryRow(bg, `
			UPDATE approval_requests
			SET status = 'expired', decision_reason = 'timed out', decided_at = now()
			WHERE id = $1 AND status = 'pending'
			RETURNING status, COALESCE(decision_reason, '')
		`, approvalID).Scan(&status, &decisionReason)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// The UPDATE's WHERE clause matched nothing -- status was
				// already something other than 'pending'. Fetch the real
				// decision and honor it rather than declaring a timeout.
				if fetchErr := r.pool.QueryRow(bg, `
					SELECT status, COALESCE(decision_reason, '') FROM approval_requests WHERE id = $1
				`, approvalID).Scan(&status, &decisionReason); fetchErr == nil {
					res := r.outcomeFromStatus(ctx, approvalID, req, status, decisionReason)
					return res.data, res.err
				}
			}
			slog.Warn("approval: failed to record timeout", "approval_id", approvalID, "err", err)
		}

		slog.Warn("approval: hold timed out", "approval_id", approvalID, "timeout", r.holdTimeout)
		return nil, fmt.Errorf("approval timed out after %s", r.holdTimeout)
	}
}

// Run subscribes to the Postgres "approval_decision" channel and resolves
// pending Hold() calls as decisions arrive. It reconnects automatically on
// transient errors. Stops cleanly when ctx is cancelled.
//
// Call as: go approvalRouter.Run(ctx)
func (r *Router) Run(ctx context.Context) {
	for {
		// GuardErr converts a panic anywhere in listenLoop (e.g. outside the
		// per-notification loop below, which has its own finer-grained
		// recovery) into an error, so it's handled by the same
		// reconnect-with-backoff logic as any other listenLoop failure.
		err := safego.GuardErr("approval-listen-loop", func() error {
			return r.listenLoop(ctx)
		})
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("approval: listener stopped cleanly")
				return
			}
			slog.Error("approval: listener error — reconnecting in 5s", "err", err)
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}
}

// listenLoop holds a dedicated connection and processes notifications until error.
func (r *Router) listenLoop(ctx context.Context) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN approval_decision"); err != nil {
		return fmt.Errorf("LISTEN: %w", err)
	}
	slog.Info("approval: LISTEN approval_decision active")

	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("wait for notification: %w", err)
		}
		r.handleNotification(ctx, notification.Payload)
	}
}

// handleNotification processes one LISTEN/NOTIFY payload for
// "approval_decision", recovering any panic so a single unexpected failure
// resolving one approval can't tear down the LISTEN connection — the loop
// keeps waiting for the next notification.
func (r *Router) handleNotification(ctx context.Context, rawPayload string) {
	safego.Guard("approval-resolve", func() {
		var payload struct {
			ApprovalID string `json:"approval_id"`
		}
		if err := json.Unmarshal([]byte(rawPayload), &payload); err != nil {
			slog.Warn("approval: malformed notify payload",
				"payload", rawPayload,
				"err", err,
			)
			return
		}
		if payload.ApprovalID == "" {
			slog.Warn("approval: notify payload missing approval_id", "payload", rawPayload)
			return
		}

		// resolve runs synchronously: it fetches the DB row and signals the waiter.
		// This is safe because WaitForNotification is the only goroutine using conn.
		r.resolve(ctx, payload.ApprovalID)
	})
}

// resolve fetches the decision for approvalID and signals the pending Hold() waiter.
func (r *Router) resolve(ctx context.Context, approvalID string) {
	v, ok := r.pending.Load(approvalID)
	if !ok {
		// No pending Hold() on this node — timed out, or handled by another node.
		slog.Debug("approval: no pending hold for approval", "approval_id", approvalID)
		return
	}
	entry := v.(*pendingEntry)

	// status/decision_reason are the real approval_requests columns (the
	// original code queried nonexistent decision/reason columns, which
	// would have failed this query outright even with Submit()'s INSERT
	// fixed). COALESCE keeps decisionReason as "" rather than NULL for
	// the message-building below; status itself is NOT NULL in the
	// schema so no COALESCE is needed for it.
	var status, decisionReason string
	err := r.pool.QueryRow(ctx, `
		SELECT status, COALESCE(decision_reason, '')
		FROM approval_requests
		WHERE id = $1
	`, approvalID).Scan(&status, &decisionReason)
	if err != nil {
		slog.Error("approval: fetch decision failed",
			"approval_id", approvalID,
			"err", err,
		)
		entry.ch <- decisionResult{err: fmt.Errorf("approval: fetch decision: %w", err)}
		return
	}

	entry.ch <- r.outcomeFromStatus(ctx, approvalID, entry.req, status, decisionReason)
}

// outcomeFromStatus converts a fetched approval_requests status into a
// decisionResult -- shared by resolve() (the LISTEN/NOTIFY path) and
// Hold()'s timeout backstop (which re-checks the row directly if it was
// already decided in the race window right at the timeout boundary), so
// the two paths can never interpret the same status differently.
func (r *Router) outcomeFromStatus(ctx context.Context, approvalID string, req Request, status, decisionReason string) decisionResult {
	switch status {
	case "approved":
		// eami-api's DecideApproval (approvals.go) validates and writes
		// only "approved"/"denied" -- matching that vocabulary exactly is
		// the whole point of this fix; the original code checked for
		// "allowed", which eami-api never writes.
		slog.Info("approval: approved — resuming original call", "approval_id", approvalID)
		tr, proxyErr := r.dispatchApproved(ctx, approvalID, req)
		return decisionResult{data: tr.Body, err: proxyErr}

	case "pending":
		// Notification arrived before eami-api's UPDATE actually
		// committed (shouldn't happen -- NOTIFY fires after the row is
		// written -- but defensive, not assumed impossible). Return an
		// error; the waiter will see it, and Hold()'s own timeout is the
		// backstop if this repeats.
		slog.Warn("approval: notified but status still pending", "approval_id", approvalID)
		return decisionResult{err: fmt.Errorf("approval: status still pending for %s", approvalID)}

	default:
		// "denied", "expired", or any other non-approved status all
		// block the action the same way -- the original action must
		// never proceed once anything other than an explicit approval
		// has been recorded.
		msg := fmt.Sprintf("approval %s", status)
		if decisionReason != "" {
			msg += ": " + decisionReason
		}
		slog.Info("approval: not approved", "approval_id", approvalID, "status", status, "reason", decisionReason)
		return decisionResult{err: fmt.Errorf("%s", msg)}
	}
}

// resolvedConnectorConfig is gateway_tools' security-relevant config for
// one row, fetched fresh at resume time by ID (never by name -- see
// dispatchApproved) so it can be compared against what was pinned at
// escalation time.
type resolvedConnectorConfig struct {
	toolType             string
	authType             string
	baseURLOrProvider    string // base_url for rest_api, provider for ai_provider
	credentialsEncrypted []byte
	actionPaths          map[string]toolrouter.ActionPathEntry
}

// fetchResolvedConnector reads gateway_tools by (id, org_id) directly --
// not through toolrouter.Resolve/aiprovider.Resolve, both of which
// resolve by name, the exact thing dispatchApproved must NOT do for an
// already-escalated request (see its doc comment). Org-scoped on orgID,
// which traces back to the authenticated MCP session that originally made
// the call (cmd/gateway/main.go), never anything resume-time-client-
// supplied.
func (r *Router) fetchResolvedConnector(ctx context.Context, orgID, toolID string) (resolvedConnectorConfig, bool, error) {
	var cfg resolvedConnectorConfig
	var baseURL, provider *string
	var actionPathsRaw []byte
	err := r.pool.QueryRow(ctx, `
		SELECT type, auth_type, base_url, provider, credentials_encrypted, action_paths
		FROM gateway_tools
		WHERE id = $1 AND org_id = $2
	`, toolID, orgID).Scan(&cfg.toolType, &cfg.authType, &baseURL, &provider, &cfg.credentialsEncrypted, &actionPathsRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cfg, false, nil
		}
		return cfg, false, err
	}
	switch cfg.toolType {
	case "rest_api":
		if baseURL != nil {
			cfg.baseURLOrProvider = *baseURL
		}
	case "ai_provider":
		if provider != nil {
			cfg.baseURLOrProvider = *provider
		}
	}
	if len(actionPathsRaw) > 0 {
		_ = json.Unmarshal(actionPathsRaw, &cfg.actionPaths)
	}
	return cfg, true, nil
}

// recordResumeOutcome is a best-effort audit record of what actually
// happened at resume time (see approval_requests.resume_outcome's own
// migration comment) -- failures here are logged, not returned: the real
// enforcement is dispatchApproved's own fail-closed return value, this is
// forensic record-keeping on top of it, not the control itself.
func (r *Router) recordResumeOutcome(ctx context.Context, approvalID, outcome string) {
	if _, err := r.pool.Exec(ctx, `UPDATE approval_requests SET resume_outcome = $1 WHERE id = $2`, outcome, approvalID); err != nil {
		slog.Warn("approval: failed to record resume_outcome", "approval_id", approvalID, "outcome", outcome, "err", err)
	}
}

// dispatchApproved resumes req against the SAME connector -- identity and
// security-relevant config both verified unchanged -- that was resolved
// and pinned at escalation time (req.ResolvedToolID/ResolvedConfigHash,
// set by cmd/gateway/main.go's dispatch closure before Submit()). Fails
// closed, never falling back to a fresh by-name lookup or the static
// proxy, if the pinned connector was deleted or its config (base_url/
// provider/credentials) changed during the hold window.
//
// This closes a real TOCTOU gap found live during this brief's own
// verification, more serious than the resume-routing gap dispatchApproved
// originally fixed: re-resolving by NAME at resume time (this function's
// first version) meant a lower-privileged admin/operator role -- which
// cannot itself approve or deny an escalation, and which the approver has
// no visibility into having acted -- could edit a pending escalation's
// connector (base_url, credentials, or provider) while it waited for a
// human decision, silently redirecting the approved call to a different
// destination than the one actually reviewed. The approver-facing UI
// shows only agent/tool/action/justification/risk -- never base_url,
// provider, or a credential fingerprint -- so there was no way for a
// human to detect this from the approval screen itself.
//
// req.ResolvedToolID empty means Tool never resolved dynamically at
// escalation time -- unaffected by any of this, same static-proxy
// fallback as before any of this brief's fixes.
func (r *Router) dispatchApproved(ctx context.Context, approvalID string, req Request) (proxy.ToolResponse, error) {
	if req.ResolvedToolID == "" {
		r.recordResumeOutcome(ctx, approvalID, "static_fallback")
		return r.fwd.Forward(ctx, proxy.ToolRequest{
			ToolName:  req.Tool,
			Action:    req.Action,
			Params:    req.Parameters,
			SessionID: req.SessionID,
		})
	}

	cfg, found, err := r.fetchResolvedConnector(ctx, req.OrgID, req.ResolvedToolID)
	if err != nil {
		return proxy.ToolResponse{}, fmt.Errorf("approval: verify resolved connector %s: %w", req.ResolvedToolID, err)
	}
	if !found {
		r.recordResumeOutcome(ctx, approvalID, "connector_deleted")
		return proxy.ToolResponse{}, fmt.Errorf("approval: resolved connector %s no longer exists -- refusing to resume against an unknown destination", req.ResolvedToolID)
	}
	// Re-marshaled (not the raw stored JSONB bytes) so this matches
	// main.go's own canonical-form computation regardless of the stored
	// bytes' original formatting -- encoding/json.Marshal of a Go map is
	// always deterministic (sorted keys), so an unchanged action_paths
	// value hashes identically here and at escalation time either way.
	var currentActionPathsJSON []byte
	if len(cfg.actionPaths) > 0 {
		currentActionPathsJSON, _ = json.Marshal(cfg.actionPaths)
	}
	currentHash := ComputeConfigHash(cfg.toolType, cfg.baseURLOrProvider, cfg.credentialsEncrypted, currentActionPathsJSON)
	if currentHash != req.ResolvedConfigHash {
		r.recordResumeOutcome(ctx, approvalID, "config_changed")
		return proxy.ToolResponse{}, fmt.Errorf("approval: resolved connector %s configuration changed since this request was approved -- refusing to resume against a different destination than the approver reviewed", req.ResolvedToolID)
	}

	// Identity+config verified unchanged -- dispatch via the pinned
	// connector's own current row, reconstructed directly from cfg (not
	// re-resolved by name, closing the identity-substitution variant of
	// this gap too: a delete-then-recreate-under-the-same-name row would
	// have a different id, so fetchResolvedConnector above would already
	// have returned found=false for the original ResolvedToolID).
	switch cfg.toolType {
	case "ai_provider":
		if r.aiProviderRouter == nil {
			return proxy.ToolResponse{}, fmt.Errorf("approval: resolved connector %s is an ai_provider connector but no aiProviderRouter is configured", req.ResolvedToolID)
		}
		row := &aiprovider.ToolRow{
			ID:                   req.ResolvedToolID,
			Provider:             cfg.baseURLOrProvider,
			AuthType:             cfg.authType,
			CredentialsEncrypted: cfg.credentialsEncrypted,
		}
		resp, dispatchErr := r.aiProviderRouter.Dispatch(ctx, row, req.Action, req.Parameters)
		r.recordResumeOutcome(ctx, approvalID, "dispatched")
		return proxy.ToolResponse{Status: resp.StatusCode, Body: resp.Body}, dispatchErr

	case "rest_api":
		if r.toolRouter == nil {
			return proxy.ToolResponse{}, fmt.Errorf("approval: resolved connector %s is a rest_api tool but no toolRouter is configured", req.ResolvedToolID)
		}
		baseURL := cfg.baseURLOrProvider
		row := &toolrouter.ToolRow{
			ID:                   req.ResolvedToolID,
			Type:                 "rest_api",
			AuthType:             cfg.authType,
			BaseURL:              &baseURL,
			CredentialsEncrypted: cfg.credentialsEncrypted,
			ActionPaths:          cfg.actionPaths,
		}
		resp, dispatchErr := r.toolRouter.Forward(ctx, row, proxy.ToolRequest{
			ToolName:  req.Tool,
			Action:    req.Action,
			Params:    req.Parameters,
			SessionID: req.SessionID,
		})
		r.recordResumeOutcome(ctx, approvalID, "dispatched")
		return resp, dispatchErr

	default:
		// The pinned row's type itself changed since escalation (e.g.
		// rest_api -> mcp) -- ComputeConfigHash includes toolType, so this
		// should already have been caught as a hash mismatch above; kept
		// as an explicit defensive case rather than assuming that
		// invariant can never be violated.
		r.recordResumeOutcome(ctx, approvalID, "config_changed")
		return proxy.ToolResponse{}, fmt.Errorf("approval: resolved connector %s is no longer a dynamically-dispatchable type (%q)", req.ResolvedToolID, cfg.toolType)
	}
}

// notifySlack posts an approval request notification to the configured webhook.
// Runs in a background goroutine; failures are logged and ignored.
func (r *Router) notifySlack(approvalID string, req Request) {
	if r.slackWebhook == "" {
		return
	}

	approveURL := fmt.Sprintf("%s/approvals/%s", r.uiBaseURL, approvalID)
	denyURL := fmt.Sprintf("%s/approvals/%s?action=deny", r.uiBaseURL, approvalID)

	// Security review (this task): AgentName/Tool/Action/SessionID can
	// originate from an untrusted or compromised caller and were
	// previously interpolated into the message text raw -- Slack's
	// mrkdwn treats &, <, > specially (e.g. <url|label> link syntax), so
	// an attacker-influenced value could inject a fake link or misleading
	// formatting into what a human approver reads before making a
	// judgment call. Escaped per Slack's own documented escaping rules;
	// the message-sending mechanics below (client, request, goroutine)
	// are unchanged.
	payload := map[string]any{
		"text": fmt.Sprintf(
			"*EAMI Gateway — Approval Required*\n*Agent:* %s\n*Tool:* %s\n*Action:* %s\n*Session:* %s\n<%s|✅ Approve> | <%s|❌ Deny>",
			escapeSlackText(req.AgentName), escapeSlackText(req.Tool), escapeSlackText(req.Action), escapeSlackText(req.SessionID),
			approveURL, denyURL,
		),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		slog.Error("approval: marshal slack payload", "err", err)
		return
	}

	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		httpReq, err := http.NewRequestWithContext(
			context.Background(),
			http.MethodPost,
			r.slackWebhook,
			bytes.NewReader(body),
		)
		if err != nil {
			slog.Warn("approval: build slack request", "err", err)
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(httpReq)
		if err != nil {
			slog.Warn("approval: slack notify failed", "err", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			slog.Warn("approval: slack returned non-2xx", "status", resp.StatusCode)
		}
	}()
}

// escapeSlackText escapes the three characters Slack's mrkdwn format
// treats specially (&, <, >), per Slack's own documented escaping rules
// (https://api.slack.com/reference/surfaces/formatting#escaping) -- must
// replace & first so escaping < and > doesn't get double-escaped.
func escapeSlackText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// newUUID generates a RFC 4122 v4 UUID without external dependencies.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
