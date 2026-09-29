package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"

	"github.com/eami/api/internal/store"
)

// Agent Lineage (Horizon 1 "Agent lineage", 2026-09-29): what an agent has
// actually done, from data the gateway already records. Read-only; the
// admin/operator/viewer read group (the same as /connections and /audit;
// approvers are excluded because audit reads exclude them).

// lineageWindows are the only accepted ?window= values. 7d is the default:
// 24h would render empty for most agents at current volumes.
var lineageWindows = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

const lineageDefaultWindow = "7d"

type AgentLineageSummaryResp struct {
	RiskTier         string  `json:"risk_tier"`
	Owner            string  `json:"owner"`
	ToolsEverTouched int64   `json:"tools_ever_touched"`
	CallsInWindow    int64   `json:"calls_in_window"`
	Escalations30d   int64   `json:"escalations_30d"`
	Denials30d       int64   `json:"denials_30d"`
	FirstSeen        *string `json:"first_seen"`
	LastSeen         *string `json:"last_seen"`
	// CostUSDWindow is the agent's token_usage cost in the window, defined
	// exactly as FinOps' per-agent cost. nil when no AI usage was ever
	// recorded for this agent (shown as "—", never a fabricated $0).
	CostUSDWindow *float64 `json:"cost_usd_window"`
	// UnpricedCallsWindow counts usage rows in the window with neither a
	// stored cost nor a model_pricing match, which contribute nothing to
	// CostUSDWindow, so the total is a floor, not the real figure.
	UnpricedCallsWindow int64 `json:"unpriced_calls_window"`
}

type AgentLineageToolResp struct {
	ToolID     *string `json:"tool_id"`
	ToolName   string  `json:"tool_name"`
	ToolType   *string `json:"tool_type"`
	Calls      int64   `json:"calls"`
	Allowed    int64   `json:"allowed"`
	Escalated  int64   `json:"escalated"`
	Denied     int64   `json:"denied"`
	CallsTotal int64   `json:"calls_total"`
	LastCallAt *string `json:"last_call_at"`
	// CostUSD is set only for a tool that resolves to a current
	// ai_provider connector AND has recorded token usage at some point
	// (the agent-level rule, per tool); nil ("—") otherwise, including a
	// connector that no longer exists (its type can't be confirmed).
	CostUSD       *float64 `json:"cost_usd"`
	UnpricedCalls int64    `json:"unpriced_calls"`
}

type AgentLineagePolicyResp struct {
	PolicyID  string  `json:"policy_id"`
	Name      string  `json:"name"`
	Action    string  `json:"action"`
	Hits      int64   `json:"hits"`
	LastHitAt *string `json:"last_hit_at"`
}

type AgentLineageWorkflowResp struct {
	WorkflowID string `json:"workflow_id"`
	Name       string `json:"name"`
	Runs       int64  `json:"runs"`
	LastRunAt  string `json:"last_run_at"`
}

type AgentLineageResp struct {
	AgentID     string                     `json:"agent_id"`
	Window      string                     `json:"window"`
	WindowStart string                     `json:"window_start"`
	GeneratedAt string                     `json:"generated_at"`
	Summary     AgentLineageSummaryResp    `json:"summary"`
	Tools       []AgentLineageToolResp     `json:"tools"`
	Policies    []AgentLineagePolicyResp   `json:"policies"`
	Workflows   []AgentLineageWorkflowResp `json:"workflows"`
}

// GetAgentLineage handles GET /v1/gateway/agents/{agentId}/lineage?window=24h|7d|30d
// Auth: admin/operator/viewer JWT.
func (s *Server) GetAgentLineage(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "agentId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid agentId")
		return
	}
	window := r.URL.Query().Get("window")
	if window == "" {
		window = lineageDefaultWindow
	}
	span, ok := lineageWindows[window]
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", `window must be "24h", "7d" or "30d"`)
		return
	}
	// The agent must exist in the caller's org: a foreign-org or unknown
	// agent is a 404, the same as /connections, never an empty 200.
	agent, err := s.queries.GetAgent(r.Context(), id, uc.OrgID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load agent lineage")
		return
	}

	// One instant for every number on the page.
	now := time.Now().UTC()
	since := now.Add(-span)
	since30d := now.Add(-lineageWindows["30d"])

	var (
		summary   store.AgentLineageSummary
		cost      store.AgentLineageCost
		tools     []store.AgentLineageTool
		toolCosts []store.AgentLineageToolCost
		policies  []store.AgentLineagePolicy
		workflows []store.AgentLineageWorkflow
	)
	g, gctx := errgroup.WithContext(r.Context())
	g.Go(func() (err error) {
		summary, err = s.queries.GetAgentLineageSummary(gctx, uc.OrgID, id, since, since30d)
		return err
	})
	g.Go(func() (err error) {
		cost, err = s.queries.GetAgentLineageCost(gctx, uc.OrgID, id, since, now)
		return err
	})
	g.Go(func() (err error) {
		tools, err = s.queries.ListAgentLineageTools(gctx, uc.OrgID, id, since)
		return err
	})
	g.Go(func() (err error) {
		toolCosts, err = s.queries.ListAgentLineageToolCosts(gctx, uc.OrgID, id, since, now)
		return err
	})
	g.Go(func() (err error) {
		policies, err = s.queries.ListAgentLineagePolicies(gctx, uc.OrgID, id, since)
		return err
	})
	g.Go(func() (err error) {
		workflows, err = s.queries.ListAgentLineageWorkflows(gctx, uc.OrgID, id, since)
		return err
	})
	if err := g.Wait(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to load agent lineage")
		return
	}

	resp := AgentLineageResp{
		AgentID:     id.String(),
		Window:      window,
		WindowStart: since.Format(time.RFC3339),
		GeneratedAt: now.Format(time.RFC3339),
		Summary: AgentLineageSummaryResp{
			RiskTier:         agent.RiskTier,
			Owner:            agent.Owner,
			ToolsEverTouched: summary.ToolsEverTouched,
			CallsInWindow:    summary.CallsInWindow,
			Escalations30d:   summary.Escalations30d,
			Denials30d:       summary.Denials30d,
			FirstSeen:        fmtTimePtr(summary.FirstSeen),
			LastSeen:         fmtTimePtr(summary.LastSeen),
		},
		Tools:     make([]AgentLineageToolResp, 0, len(tools)),
		Policies:  make([]AgentLineagePolicyResp, 0, len(policies)),
		Workflows: make([]AgentLineageWorkflowResp, 0, len(workflows)),
	}
	if cost.RowsEver > 0 {
		c := cost.CostInWindow
		resp.Summary.CostUSDWindow = &c
		resp.Summary.UnpricedCallsWindow = cost.UnpricedWindow
	}

	costByTool := make(map[string]store.AgentLineageToolCost, len(toolCosts))
	for _, c := range toolCosts {
		costByTool[c.ToolName] = c
	}
	for _, t := range tools {
		tr := AgentLineageToolResp{
			ToolName: t.ToolName, ToolType: t.ToolType,
			Calls: t.Calls, Allowed: t.Allowed, Escalated: t.Escalated, Denied: t.Denied,
			CallsTotal: t.CallsTotal, LastCallAt: fmtTimePtr(t.LastCallAt),
		}
		if t.ToolID != nil {
			s := t.ToolID.String()
			tr.ToolID = &s
		}
		if c, ok := costByTool[t.ToolName]; ok && c.RowsEver > 0 && t.ToolType != nil && *t.ToolType == "ai_provider" {
			v := c.Cost // $0 here is real: usage exists, none in the window
			tr.CostUSD = &v
			tr.UnpricedCalls = c.Unpriced
		}
		resp.Tools = append(resp.Tools, tr)
	}
	for _, p := range policies {
		resp.Policies = append(resp.Policies, AgentLineagePolicyResp{
			PolicyID: p.PolicyID.String(), Name: p.Name, Action: p.Action,
			Hits: p.Hits, LastHitAt: fmtTimePtr(p.LastHitAt),
		})
	}
	for _, wf := range workflows {
		resp.Workflows = append(resp.Workflows, AgentLineageWorkflowResp{
			WorkflowID: wf.WorkflowID.String(), Name: wf.Name,
			Runs: wf.Runs, LastRunAt: wf.LastRunAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func fmtTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
