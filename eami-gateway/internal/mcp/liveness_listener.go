package mcp

// LivenessListener (B-301) ends open MCP sessions promptly when a governed
// agent is suspended, revoked or deleted, or a token is revoked, on every
// gateway node.
//
// NOTIFICATIONS ARE AN OPTIMISATION ONLY. The guarantee is the dispatcher's
// per-call database check (registry.CheckLive): a node that never receives
// a notification still refuses the next call. This listener only closes the
// session sooner, and keeps each node's in-memory revoked set current.
//
// Channels (payloads are validated; anything else is ignored):
//   - agent_status:  a governed agent's UUID (sent by eami-api on any status
//     change or delete). Closes that agent's sessions on this node.
//   - token_revoked: a token JTI (sent when a revocation is persisted).
//     Marks it revoked in memory and closes sessions opened with it.
//
// On every (re)connect it reloads the full revoked set from the database and
// re-checks each open session's governed agent, catching anything missed
// while disconnected.

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/registry"
	"github.com/eami/gateway/internal/safego"
)

// jtiPattern matches the JTIs identity.Manager issues (16 hex characters);
// the gateway never issues anything else.
var jtiPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// LiveChecker is registry.Registry's CheckLive (an interface for tests).
type LiveChecker interface {
	CheckLive(ctx context.Context, agentID, orgID, jti, keyID string) (registry.Liveness, error)
}

// catchUpEvery bounds how long a node whose listener has silently stopped
// receiving notifications keeps a non-live session open (B-301 review). The
// per-call check refuses its calls regardless.
const catchUpEvery = 60 * time.Second

// checkTimeout bounds each database query the listener makes.
const checkTimeout = 2 * time.Second

// LivenessListener closes sessions on agent_status / token_revoked.
type LivenessListener struct {
	pool     *pgxpool.Pool
	handler  *Handler
	ids      *identity.Manager
	live     LiveChecker
	retry    time.Duration
	onListen func() // test hook: called after LISTEN succeeds
}

// NewLivenessListener builds a listener for one gateway node.
func NewLivenessListener(pool *pgxpool.Pool, h *Handler, ids *identity.Manager, live LiveChecker) *LivenessListener {
	return &LivenessListener{pool: pool, handler: h, ids: ids, live: live, retry: 5 * time.Second}
}

// SetOnListen registers f to run after each successful LISTEN and catch-up
// (a test hook: lets a test wait until notifications are being received).
func (l *LivenessListener) SetOnListen(f func()) { l.onListen = f }

// Run listens until ctx is cancelled, reconnecting after errors.
func (l *LivenessListener) Run(ctx context.Context) {
	go func() {
		t := time.NewTicker(catchUpEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				safego.Guard("liveness-periodic-catchup", func() { l.catchUp(ctx) })
			}
		}
	}()
	for {
		err := safego.GuardErr("liveness-listen-loop", func() error { return l.listenLoop(ctx) })
		if ctx.Err() != nil {
			return
		}
		slog.Error("mcp/liveness: listener error, reconnecting", "err", err, "in", l.retry)
		select {
		case <-time.After(l.retry):
		case <-ctx.Done():
			return
		}
	}
}

func (l *LivenessListener) listenLoop(ctx context.Context) error {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN agent_status"); err != nil {
		return fmt.Errorf("LISTEN agent_status: %w", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN token_revoked"); err != nil {
		return fmt.Errorf("LISTEN token_revoked: %w", err)
	}
	slog.Info("mcp/liveness: LISTEN agent_status, token_revoked active")
	l.catchUp(ctx)
	if l.onListen != nil {
		l.onListen()
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("wait for notification: %w", err)
		}
		l.handle(n.Channel, n.Payload)
	}
}

// catchUp runs after every (re)connect: reload the revoked set, then end
// any open session whose governed agent or token is no longer live.
func (l *LivenessListener) catchUp(ctx context.Context) {
	if l.ids != nil {
		if err := l.ids.ReloadRevoked(ctx); err != nil {
			slog.Error("mcp/liveness: revoked-set reload failed", "err", err)
		}
	}
	if l.live == nil {
		return
	}
	closed := l.handler.RecheckSessions(func(s *Session) bool {
		if s.Agent == nil || s.Claims == nil {
			return false
		}
		qctx, cancel := context.WithTimeout(ctx, checkTimeout)
		defer cancel()
		lv, err := l.live.CheckLive(qctx, s.Agent.ID, s.Agent.OrgID, s.Claims.ID, s.Claims.APIKeyID)
		if err != nil {
			return true // undecided: keep; the per-call check still guards it
		}
		return lv == registry.Live
	})
	if closed > 0 {
		slog.Info("mcp/liveness: closed sessions on reconnect", "count", closed)
	}
}

// agentInactive reports whether the governed agent is missing or not active.
func (l *LivenessListener) agentInactive(id uuid.UUID) bool {
	if l.pool == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	var active bool
	err := l.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM gateway_agents WHERE id = $1 AND status = 'active')`, id).Scan(&active)
	return err == nil && !active
}

// tokenRevoked reports whether jti is in revoked_ai_tokens.
func (l *LivenessListener) tokenRevoked(jti string) bool {
	if l.pool == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	var revoked bool
	err := l.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM revoked_ai_tokens WHERE jti = $1)`, jti).Scan(&revoked)
	return err == nil && revoked
}

// handle applies one notification. Unknown channels and malformed payloads
// are ignored (and never echoed in full to the log).
func (l *LivenessListener) handle(channel, payload string) {
	safego.Guard("liveness-notification", func() {
		switch channel {
		case "agent_status":
			id, err := uuid.Parse(payload)
			if err != nil {
				slog.Warn("mcp/liveness: ignored malformed agent_status payload", "len", len(payload))
				return
			}
			// Confirm in the database: anyone who can NOTIFY could otherwise
			// close an active agent's sessions (B-301 review, Low 7). An
			// error keeps the sessions (the per-call check still applies).
			if !l.agentInactive(id) {
				return
			}
			if n := l.handler.CloseSessionsForAgent(id.String()); n > 0 {
				slog.Info("mcp/liveness: closed sessions for governed agent", "agent_uuid", id.String(), "count", n)
			}
		case "token_revoked":
			if !jtiPattern.MatchString(payload) {
				slog.Warn("mcp/liveness: ignored malformed token_revoked payload", "len", len(payload))
				return
			}
			// Confirm in the database before trusting the payload, so a
			// forged notification can't grow the in-memory set or end
			// sessions (B-301 review, Low 7).
			if !l.tokenRevoked(payload) {
				return
			}
			if l.ids != nil {
				l.ids.MarkRevoked(payload)
			}
			if n := l.handler.CloseSessionsForToken(payload); n > 0 {
				slog.Info("mcp/liveness: closed sessions for revoked token", "count", n)
			}
		}
	})
}
