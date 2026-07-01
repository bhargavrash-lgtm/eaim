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
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/proxy"
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
}

// New creates a Router.
//   - holdTimeout is the maximum time Hold() will wait before auto-denying.
//   - slackWebhook may be empty to disable Slack notifications.
func New(
	pool *pgxpool.Pool,
	fwd *proxy.Proxy,
	holdTimeout time.Duration,
	slackWebhook string,
	uiBaseURL string,
) *Router {
	return &Router{
		pool:         pool,
		fwd:          fwd,
		holdTimeout:  holdTimeout,
		slackWebhook: slackWebhook,
		uiBaseURL:    uiBaseURL,
	}
}

// Submit persists an approval request to approval_requests, fires a Slack
// notification, and returns the new approval UUID. The caller must then call
// Hold() with the returned ID to block until a decision arrives.
func (r *Router) Submit(ctx context.Context, req Request) (string, error) {
	approvalID, err := newUUID()
	if err != nil {
		return "", fmt.Errorf("approval: generate id: %w", err)
	}

	params, err := json.Marshal(req.Parameters)
	if err != nil {
		return "", fmt.Errorf("approval: marshal parameters: %w", err)
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO approval_requests
			(id, org_id, agent_id, agent_name, tool_name, action, parameters, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
	`,
		approvalID,
		nilIfEmpty(req.OrgID),
		nilIfEmpty(req.AgentID),
		req.AgentName,
		req.Tool,
		req.Action,
		params,
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
//   - a decision arrives via Run() → if "allowed", forwards via proxy; if "denied", returns error
//   - holdTimeout elapses → auto-denies and returns an error
//   - ctx is cancelled → returns ctx.Err()
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
		// Record the timeout in the DB so eami-api knows the request expired.
		_, _ = r.pool.Exec(context.Background(), `
			UPDATE approval_requests
			SET decision = 'denied', reason = 'timed out', decided_at = now()
			WHERE id = $1 AND decision IS NULL
		`, approvalID)

		if holdCtx.Err() == context.DeadlineExceeded {
			slog.Warn("approval: hold timed out", "approval_id", approvalID, "timeout", r.holdTimeout)
			return nil, fmt.Errorf("approval timed out after %s", r.holdTimeout)
		}
		return nil, holdCtx.Err()
	}
}

// Run subscribes to the Postgres "approval_decision" channel and resolves
// pending Hold() calls as decisions arrive. It reconnects automatically on
// transient errors. Stops cleanly when ctx is cancelled.
//
// Call as: go approvalRouter.Run(ctx)
func (r *Router) Run(ctx context.Context) {
	for {
		if err := r.listenLoop(ctx); err != nil {
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

		var payload struct {
			ApprovalID string `json:"approval_id"`
		}
		if err := json.Unmarshal([]byte(notification.Payload), &payload); err != nil {
			slog.Warn("approval: malformed notify payload",
				"payload", notification.Payload,
				"err", err,
			)
			continue
		}
		if payload.ApprovalID == "" {
			slog.Warn("approval: notify payload missing approval_id", "payload", notification.Payload)
			continue
		}

		// resolve runs synchronously: it fetches the DB row and signals the waiter.
		// This is safe because WaitForNotification is the only goroutine using conn.
		r.resolve(ctx, payload.ApprovalID)
	}
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

	var decision, reason string
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(decision, ''), COALESCE(reason, '')
		FROM approval_requests
		WHERE id = $1
	`, approvalID).Scan(&decision, &reason)
	if err != nil {
		slog.Error("approval: fetch decision failed",
			"approval_id", approvalID,
			"err", err,
		)
		entry.ch <- decisionResult{err: fmt.Errorf("approval: fetch decision: %w", err)}
		return
	}

	switch decision {
	case "allowed":
		slog.Info("approval: approved — forwarding to proxy", "approval_id", approvalID)
		tr, proxyErr := r.fwd.Forward(ctx, proxy.ToolRequest{
			ToolName:  entry.req.Tool,
			Action:    entry.req.Action,
			Params:    entry.req.Parameters,
			SessionID: entry.req.SessionID,
		})
		entry.ch <- decisionResult{data: tr.Body, err: proxyErr}

	case "denied":
		msg := "approval denied"
		if reason != "" {
			msg = "approval denied: " + reason
		}
		slog.Info("approval: denied", "approval_id", approvalID, "reason", reason)
		entry.ch <- decisionResult{err: fmt.Errorf("%s", msg)}

	default:
		// Decision not yet set — notification arrived before eami-api committed?
		// Return an error; the waiter will see it.
		slog.Warn("approval: decision row has no decision", "approval_id", approvalID, "decision", decision)
		entry.ch <- decisionResult{err: fmt.Errorf("approval: decision row has no decision for %s", approvalID)}
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

	payload := map[string]any{
		"text": fmt.Sprintf(
			"*EAMI Gateway — Approval Required*\n*Agent:* %s\n*Tool:* %s\n*Action:* %s\n*Session:* %s\n<%s|✅ Approve> | <%s|❌ Deny>",
			req.AgentName, req.Tool, req.Action, req.SessionID,
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

// nilIfEmpty returns nil for empty strings (for nullable UUID columns in Postgres).
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
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
