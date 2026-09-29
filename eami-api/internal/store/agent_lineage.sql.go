// Agent Lineage (Horizon 1 "Agent lineage", 2026-09-29): per-agent activity
// aggregates for Agent Detail's Lineage tab. Hand-written in the sqlc style
// of agent_connections.sql.go (no query/*.sql source file, the same as that
// file). Read-only; every query is scoped by BOTH org_id and agent_id, and
// every join is additionally org-matched, so a row from another org can
// never contribute even if an id were somehow shared.
//
// Built only from data the gateway already records: audit_log (decisions,
// tools, policies), workflow_runs (runs) and token_usage (cost). No data
// classification (redaction records only a count) and no new
// instrumentation. Time bounds are passed in by the caller (one "now" per
// request) so every number on the page uses the same instant.
package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// isCall excludes an escalation's RESOLUTION row. When an escalated call is
// approved/denied/expired, the gateway writes a second audit_log row: a
// clone of the original with approval_id set and decision allowed/denied
// (eami-gateway/cmd/gateway/dispatcher.go, "holdOutcome.Resolved"). The
// original escalated row has no approval_id. Counting both would count one
// call twice, so every call count uses this predicate: each call counts
// once, as the gateway's first decision on it (an approved-after-escalation
// call is an escalation, not also an allow).
const isCall = `NOT (a.approval_id IS NOT NULL AND a.decision <> 'escalated')`

// costExpr is FinOps' per-row cost (finops.go's totalQ/agentQ): the stored
// cost_usd when present, else tokens priced from model_pricing, plus the
// cache-tier terms (B-111). Kept textually identical so a lineage figure
// cross-checks against FinOps for the same agent and span.
const costExpr = `(CASE WHEN tu.cost_usd IS NOT NULL THEN tu.cost_usd
          ELSE (tu.tokens_in  * mp.cost_per_1k_in  / 1000.0)
             + (tu.tokens_out * mp.cost_per_1k_out / 1000.0)
     END)
    + (tu.cache_creation_5m_tokens * COALESCE(mp.cost_per_1k_cache_write_5m,0) / 1000.0)
    + (tu.cache_creation_1h_tokens * COALESCE(mp.cost_per_1k_cache_write_1h,0) / 1000.0)
    + (tu.cache_read_tokens        * COALESCE(mp.cost_per_1k_cache_read,0)    / 1000.0)`

// unpricedExpr counts rows whose cost silently resolves to NULL (no stored
// cost_usd and no model_pricing match), which SUM() would otherwise hide as
// $0. Deliberately stricter than FinOps' B-112 count (mp.model IS NULL): a
// row with a stored cost_usd is priced even when its model has no rate.
const unpricedExpr = `tu.cost_usd IS NULL AND mp.model IS NULL`

// usageWindow bounds a token_usage row to [since, now): the same half-open
// span FinOps uses. recorded_at can be client-supplied (reports.go), so the
// upper bound keeps a future-dated row out.
const usageWindow = `tu.recorded_at >= $3 AND tu.recorded_at < $4`

// AgentLineageSummary is the agent-level audit aggregate. FirstSeen/LastSeen
// cover every audit row (activity, including resolutions) and are nil when
// the agent has no audit_log rows at all.
type AgentLineageSummary struct {
	ToolsEverTouched int64
	CallsInWindow    int64
	Escalations30d   int64
	Denials30d       int64
	FirstSeen        *time.Time
	LastSeen         *time.Time
}

const getAgentLineageSummary = `-- name: GetAgentLineageSummary :one
SELECT
    count(DISTINCT a.tool_name),
    count(*) FILTER (WHERE a.timestamp >= $3 AND ` + isCall + `),
    count(*) FILTER (WHERE a.decision = 'escalated' AND a.timestamp >= $4),
    count(*) FILTER (WHERE a.decision = 'denied'    AND a.timestamp >= $4 AND ` + isCall + `),
    min(a.timestamp),
    max(a.timestamp)
FROM audit_log a
WHERE a.org_id = $1 AND a.agent_id = $2
`

func (q *Queries) GetAgentLineageSummary(ctx context.Context, orgID, agentID uuid.UUID, since, since30d time.Time) (AgentLineageSummary, error) {
	var s AgentLineageSummary
	var first, last pgtype.Timestamptz
	err := q.db.QueryRow(ctx, getAgentLineageSummary, toPgtypeUUID(orgID), toPgtypeUUID(agentID), since, since30d).
		Scan(&s.ToolsEverTouched, &s.CallsInWindow, &s.Escalations30d, &s.Denials30d, &first, &last)
	s.FirstSeen = timePtrFromPgtype(first)
	s.LastSeen = timePtrFromPgtype(last)
	return s, err
}

// AgentLineageCost is the agent's token_usage cost. RowsEver == 0 means no
// AI usage was ever recorded for this agent (the UI shows "—", not $0).
type AgentLineageCost struct {
	RowsEver       int64
	CostInWindow   float64
	UnpricedWindow int64
}

const getAgentLineageCost = `-- name: GetAgentLineageCost :one
SELECT
    count(*),
    COALESCE(SUM(` + costExpr + `) FILTER (WHERE ` + usageWindow + `), 0)::float8,
    count(*) FILTER (WHERE ` + usageWindow + ` AND ` + unpricedExpr + `)
FROM token_usage tu
LEFT JOIN model_pricing mp ON mp.model = tu.model
WHERE tu.org_id = $1 AND tu.agent_id = $2
`

func (q *Queries) GetAgentLineageCost(ctx context.Context, orgID, agentID uuid.UUID, since, now time.Time) (AgentLineageCost, error) {
	var c AgentLineageCost
	err := q.db.QueryRow(ctx, getAgentLineageCost, toPgtypeUUID(orgID), toPgtypeUUID(agentID), since, now).
		Scan(&c.RowsEver, &c.CostInWindow, &c.UnpricedWindow)
	return c, err
}

// AgentLineageTool is one tool this agent has ever dispatched through (the
// same set Connections draws), with window counts of calls (see isCall).
// ToolID/ToolType are nil when the audit_log tool_name no longer resolves to
// a connector in this org (renamed or deleted). LastCallAt is nil only if
// the tool has resolution rows and no call row, which the gateway doesn't
// produce but the scan tolerates.
type AgentLineageTool struct {
	ToolID     *uuid.UUID
	ToolType   *string
	ToolName   string
	Calls      int64
	Allowed    int64
	Escalated  int64
	Denied     int64
	CallsTotal int64
	LastCallAt *time.Time
}

const listAgentLineageTools = `-- name: ListAgentLineageTools :many
SELECT
    gt.id,
    gt.type,
    a.tool_name,
    count(*) FILTER (WHERE a.timestamp >= $3 AND ` + isCall + `),
    count(*) FILTER (WHERE a.timestamp >= $3 AND ` + isCall + ` AND a.decision = 'allowed'),
    count(*) FILTER (WHERE a.timestamp >= $3 AND a.decision = 'escalated'),
    count(*) FILTER (WHERE a.timestamp >= $3 AND ` + isCall + ` AND a.decision = 'denied'),
    count(*) FILTER (WHERE ` + isCall + `),
    max(a.timestamp) FILTER (WHERE ` + isCall + `)
FROM audit_log a
LEFT JOIN gateway_tools gt ON gt.org_id = a.org_id AND gt.name = a.tool_name
WHERE a.org_id = $1 AND a.agent_id = $2
GROUP BY gt.id, gt.type, a.tool_name
ORDER BY 4 DESC, 8 DESC, a.tool_name ASC
`

func (q *Queries) ListAgentLineageTools(ctx context.Context, orgID, agentID uuid.UUID, since time.Time) ([]AgentLineageTool, error) {
	rows, err := q.db.Query(ctx, listAgentLineageTools, toPgtypeUUID(orgID), toPgtypeUUID(agentID), since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentLineageTool
	for rows.Next() {
		var t AgentLineageTool
		var id pgtype.UUID
		var typ pgtype.Text
		var last pgtype.Timestamptz
		if err := rows.Scan(&id, &typ, &t.ToolName, &t.Calls, &t.Allowed, &t.Escalated, &t.Denied, &t.CallsTotal, &last); err != nil {
			return nil, err
		}
		t.ToolID = uuidPtrFromPgtype(id)
		if typ.Valid {
			s := typ.String
			t.ToolType = &s
		}
		t.LastCallAt = timePtrFromPgtype(last)
		out = append(out, t)
	}
	return out, rows.Err()
}

// AgentLineageToolCost is token_usage cost for one named tool. RowsEver is
// all-time, so a tool with no usage ever gets "—" like the agent level.
// Rows with a NULL tool_name (an unresolved tool, B-108) can't be tied to a
// tool: they are excluded here and still count in the agent total.
type AgentLineageToolCost struct {
	ToolName string
	RowsEver int64
	Cost     float64
	Unpriced int64
}

const listAgentLineageToolCosts = `-- name: ListAgentLineageToolCosts :many
SELECT
    tu.tool_name,
    count(*),
    COALESCE(SUM(` + costExpr + `) FILTER (WHERE ` + usageWindow + `), 0)::float8,
    count(*) FILTER (WHERE ` + usageWindow + ` AND ` + unpricedExpr + `)
FROM token_usage tu
LEFT JOIN model_pricing mp ON mp.model = tu.model
WHERE tu.org_id = $1 AND tu.agent_id = $2 AND tu.tool_name IS NOT NULL
GROUP BY tu.tool_name
`

func (q *Queries) ListAgentLineageToolCosts(ctx context.Context, orgID, agentID uuid.UUID, since, now time.Time) ([]AgentLineageToolCost, error) {
	rows, err := q.db.Query(ctx, listAgentLineageToolCosts, toPgtypeUUID(orgID), toPgtypeUUID(agentID), since, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentLineageToolCost
	for rows.Next() {
		var c AgentLineageToolCost
		if err := rows.Scan(&c.ToolName, &c.RowsEver, &c.Cost, &c.Unpriced); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AgentLineagePolicy is one policy that decided at least one of this
// agent's audit_log rows (the set Connections draws), with window decisions
// counted once per call (see isCall).
type AgentLineagePolicy struct {
	PolicyID  uuid.UUID
	Name      string
	Action    string
	Hits      int64
	LastHitAt *time.Time
}

const listAgentLineagePolicies = `-- name: ListAgentLineagePolicies :many
SELECT p.id, p.name, p.action,
    count(*) FILTER (WHERE a.timestamp >= $3 AND ` + isCall + `),
    max(a.timestamp) FILTER (WHERE ` + isCall + `)
FROM audit_log a
JOIN policies p ON p.id = a.policy_id AND p.org_id = a.org_id
WHERE a.org_id = $1 AND a.agent_id = $2
GROUP BY p.id, p.name, p.action
ORDER BY 4 DESC, p.name ASC
`

func (q *Queries) ListAgentLineagePolicies(ctx context.Context, orgID, agentID uuid.UUID, since time.Time) ([]AgentLineagePolicy, error) {
	rows, err := q.db.Query(ctx, listAgentLineagePolicies, toPgtypeUUID(orgID), toPgtypeUUID(agentID), since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentLineagePolicy
	for rows.Next() {
		var p AgentLineagePolicy
		var last pgtype.Timestamptz
		if err := rows.Scan(&p.PolicyID, &p.Name, &p.Action, &p.Hits, &last); err != nil {
			return nil, err
		}
		p.LastHitAt = timePtrFromPgtype(last)
		out = append(out, p)
	}
	return out, rows.Err()
}

// AgentLineageWorkflow is one workflow this agent has a real run of (the set
// Connections draws), with window runs.
type AgentLineageWorkflow struct {
	WorkflowID uuid.UUID
	Name       string
	Runs       int64
	LastRunAt  time.Time
}

const listAgentLineageWorkflows = `-- name: ListAgentLineageWorkflows :many
SELECT w.id, w.name,
    count(*) FILTER (WHERE wr.started_at >= $3),
    max(wr.started_at)
FROM workflow_runs wr
JOIN workflows w ON w.id = wr.workflow_id AND w.org_id = wr.org_id
WHERE wr.org_id = $1 AND wr.agent_id = $2
GROUP BY w.id, w.name
ORDER BY 3 DESC, w.name ASC
`

func (q *Queries) ListAgentLineageWorkflows(ctx context.Context, orgID, agentID uuid.UUID, since time.Time) ([]AgentLineageWorkflow, error) {
	rows, err := q.db.Query(ctx, listAgentLineageWorkflows, toPgtypeUUID(orgID), toPgtypeUUID(agentID), since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentLineageWorkflow
	for rows.Next() {
		var w AgentLineageWorkflow
		if err := rows.Scan(&w.WorkflowID, &w.Name, &w.Runs, &w.LastRunAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func timePtrFromPgtype(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
