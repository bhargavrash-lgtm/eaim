// Package policyloader loads the active policy set from Postgres and provides
// a hot-reload mechanism via pg_notify on the "policy_reload" channel.
//
// Usage:
//
//	loader := policyloader.New(pool)
//	if err := loader.Load(ctx); err != nil { ... }  // initial load
//	go loader.Listen(ctx)                            // background hot-reload
//
//	// In the dispatch closure:
//	decision, _ := loader.Evaluator().Evaluate(ctx, ac.ToPolicyContext())
package policyloader

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"

	policy "github.com/eami/policy"
)

// evaluatorHolder wraps the policy.Evaluator interface so it can be stored in
// an atomic.Pointer without the "pointer to interface" anti-pattern.
type evaluatorHolder struct {
	ev policy.Evaluator
}

// Loader manages the live policy set, reloading from Postgres on demand.
type Loader struct {
	pool      *pgxpool.Pool
	current   atomic.Pointer[evaluatorHolder]
	ruleCount atomic.Int64
}

// New creates a Loader. Call Load before calling Evaluator.
func New(pool *pgxpool.Pool) *Loader {
	return &Loader{pool: pool}
}

// Evaluator returns the current live evaluator. Safe for concurrent use.
func (l *Loader) Evaluator() policy.Evaluator {
	if h := l.current.Load(); h != nil {
		return h.ev
	}
	return policy.NewEvaluator(nil)
}

// Load queries all active policies from the database and atomically replaces
// the current evaluator. Safe to call concurrently.
func (l *Loader) Load(ctx context.Context) error {
	rules, err := l.queryRules(ctx)
	if err != nil {
		return fmt.Errorf("policyloader: query: %w", err)
	}
	l.store(rules)
	slog.Info("policyloader: loaded policies from DB", "count", len(rules))
	return nil
}

// Seed installs a rule set without querying the database. Used for YAML
// bootstrap when the DB is empty or unreachable on startup.
func (l *Loader) Seed(rules []policy.Rule) {
	l.store(rules)
}

// RuleCount returns the number of rules currently loaded.
func (l *Loader) RuleCount() int {
	return int(l.ruleCount.Load())
}

func (l *Loader) store(rules []policy.Rule) {
	ev := policy.NewEvaluator(rules)
	l.current.Store(&evaluatorHolder{ev: ev})
	l.ruleCount.Store(int64(len(rules)))
}

// Listen opens a dedicated Postgres connection and blocks on LISTEN for the
// "policy_reload" channel. Each notification triggers Load. Exits when ctx
// is cancelled.
func (l *Loader) Listen(ctx context.Context) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		slog.Error("policyloader: acquire connection for LISTEN", "err", err)
		return
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN policy_reload"); err != nil {
		slog.Error("policyloader: LISTEN policy_reload failed", "err", err)
		return
	}
	slog.Info("policyloader: LISTEN policy_reload active")

	for {
		_, err := conn.Conn().WaitForNotification(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("policyloader: notification error", "err", err)
			return
		}
		slog.Info("policyloader: policy_reload received -- reloading from DB")
		if loadErr := l.Load(context.Background()); loadErr != nil {
			slog.Error("policyloader: reload failed", "err", loadErr)
		}
	}
}

// queryRules fetches all active policies with their conditions from Postgres.
func (l *Loader) queryRules(ctx context.Context) ([]policy.Rule, error) {
	const q = `
		SELECT
			p.id, p.name, p.priority, p.action, p.alert,
			pc.agent_name_pattern,
			pc.tool_names,
			pc.action_types,
			pc.environments,
			pc.record_count_gt,
			pc.semantic_rule,
			COALESCE(pc.scope_drift, FALSE) AS scope_drift
		FROM policies p
		LEFT JOIN policy_conditions pc ON pc.policy_id = p.id
		WHERE p.status = 'active'
		ORDER BY p.priority ASC, p.id ASC`

	rows, err := l.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []policy.Rule
	seen := map[string]bool{}

	for rows.Next() {
		var (
			id, name, action string
			priority         int
			alert            bool
			agentPattern     *string
			toolNames        []string
			actionTypes      []string
			environments     []string
			recordCountGT    *int
			semanticRule     *string
			scopeDrift       bool
		)
		if err := rows.Scan(
			&id, &name, &priority, &action, &alert,
			&agentPattern, &toolNames, &actionTypes, &environments,
			&recordCountGT, &semanticRule, &scopeDrift,
		); err != nil {
			return nil, fmt.Errorf("policyloader: scan row: %w", err)
		}
		if seen[id] {
			continue
		}
		seen[id] = true

		cond := policy.Conditions{ScopeDrift: scopeDrift}
		if agentPattern != nil {
			cond.AgentNamePattern = *agentPattern
		}
		if len(toolNames) > 0 {
			cond.ToolNames = toolNames
		}
		if len(actionTypes) > 0 {
			cond.ActionTypes = actionTypes
		}
		if len(environments) > 0 {
			cond.Environments = environments
		}
		if recordCountGT != nil {
			v := *recordCountGT
			cond.RecordCountGT = &v
		}
		if semanticRule != nil {
			cond.SemanticRule = *semanticRule
		}

		rules = append(rules, policy.Rule{
			ID:         id,
			Name:       name,
			Priority:   priority,
			Conditions: cond,
			Action:     action,
			Alert:      alert,
		})
	}
	return rules, rows.Err()
}
