// Package api implements the EAMI REST API HTTP handlers, middleware and router.
// All request/response types in this file are derived directly from api/openapi.yaml.
package api

import (
	"time"

	"github.com/google/uuid"
)

// ── Shared ────────────────────────────────────────────────────────────────────

type PaginationMeta struct {
	Total   int64 `json:"total"`
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ── Auth ──────────────────────────────────────────────────────────────────────

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresIn    int       `json:"expires_in"`
	User         *UserResp `json:"user,omitempty"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type UserResp struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
	Role  string `json:"role"`
	OrgID string `json:"org_id"`
}

type CreateAPIKeyRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	// ExpiresAt is optional, RFC3339 or YYYY-MM-DD (B-098 -- schema always
	// had this column, this request never accepted it until now).
	ExpiresAt string `json:"expires_at,omitempty"`
	// AgentID optionally scopes this key to one gateway_agents row (B-098),
	// required for the key to authorize POST /v1/gateway/tokens for that
	// agent. Deviates from api/openapi.yaml's current CreateAPIKeyRequest
	// shape, which doesn't document either new field yet -- logged as a
	// new B-ID for Architect-EAMI rather than edited here (openapi.yaml is
	// out of this session's file boundary per BOUNDARIES.md), same
	// disclosed-not-silent precedent as B-086's usePolicies.ts deviation.
	AgentID string `json:"agent_id,omitempty"`
}

type APIKeyResp struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"created_at"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	AgentID   *string    `json:"agent_id,omitempty"`
}

type CreateAPIKeyResponse struct {
	Key  string     `json:"key"`
	Meta APIKeyResp `json:"meta"`
}

// ── Agents ────────────────────────────────────────────────────────────────────

type AgentResp struct {
	ID              string     `json:"id"`
	OrgID           string     `json:"org_id"` // explicit per task requirement for gateway lookup
	Name            string     `json:"name"`
	Model           string     `json:"model"`
	Owner           string     `json:"owner"`
	Scope           string     `json:"scope"`
	RiskTier        string     `json:"risk_tier"`
	Status          string     `json:"status"`
	TokenTTLSeconds int32      `json:"token_ttl_seconds"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	LastSeen        *time.Time `json:"last_seen,omitempty"`
}

type AgentCreateRequest struct {
	Name            string `json:"name"`
	Model           string `json:"model"`
	Owner           string `json:"owner"`
	Scope           string `json:"scope"`
	RiskTier        string `json:"risk_tier"`
	TokenTTLSeconds *int32 `json:"token_ttl_seconds,omitempty"`
}

type AgentUpdateRequest struct {
	Scope           *string `json:"scope,omitempty"`
	RiskTier        *string `json:"risk_tier,omitempty"`
	Status          *string `json:"status,omitempty"`
	TokenTTLSeconds *int    `json:"token_ttl_seconds,omitempty"`
}

type AgentListResponse struct {
	Data []AgentResp `json:"data"`
}

// ── Policies ──────────────────────────────────────────────────────────────────

type PolicyConditionsResp struct {
	AgentNamePattern *string  `json:"agent_name_pattern,omitempty"`
	ToolNames        []string `json:"tool_names,omitempty"`
	ActionTypes      []string `json:"action_types,omitempty"`
	Environments     []string `json:"environments,omitempty"`
	RecordCountGT    *int32   `json:"record_count_gt,omitempty"`
	SemanticRule     *string  `json:"semantic_rule,omitempty"`
	ScopeDrift       bool     `json:"scope_drift"`
}

type PolicyResp struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description *string              `json:"description,omitempty"`
	Priority    int32                `json:"priority"`
	Conditions  PolicyConditionsResp `json:"conditions"`
	Action      string               `json:"action"`
	Alert       bool                 `json:"alert"`
	Status      string               `json:"status"`
	CreatedBy   *string              `json:"created_by,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

type PolicyConditionsReq struct {
	AgentNamePattern *string  `json:"agent_name_pattern"`
	ToolNames        []string `json:"tool_names"`
	ActionTypes      []string `json:"action_types"`
	Environments     []string `json:"environments"`
	RecordCountGT    *int     `json:"record_count_gt"`
	SemanticRule     *string  `json:"semantic_rule"`
	ScopeDrift       bool     `json:"scope_drift"`
}

type PolicyCreateRequest struct {
	Name        string               `json:"name"`
	Description *string              `json:"description"`
	Priority    int32                `json:"priority"`
	Conditions  *PolicyConditionsReq `json:"conditions"`
	Action      string               `json:"action"`
	Alert       bool                 `json:"alert"`
	Status      string               `json:"status"`
}

type PolicyUpdateRequest struct {
	Name        *string              `json:"name,omitempty"`
	Description *string              `json:"description,omitempty"`
	Priority    *int                 `json:"priority,omitempty"`
	Conditions  *PolicyConditionsReq `json:"conditions,omitempty"`
	Action      *string              `json:"action,omitempty"`
	Alert       *bool                `json:"alert,omitempty"`
	Status      *string              `json:"status,omitempty"`
}

type PolicyListResponse struct {
	Data []PolicyResp `json:"data"`
}

type PolicyReorderRequest struct {
	Order []uuid.UUID `json:"policy_ids"`
}

// ── Audit ─────────────────────────────────────────────────────────────────────

type AuditEntryResp struct {
	ID         string      `json:"id"`
	AgentID    *string     `json:"agent_id,omitempty"`
	AgentName  string      `json:"agent_name"`
	ToolName   string      `json:"tool_name"`
	Action     string      `json:"action"`
	Parameters interface{} `json:"parameters,omitempty"`
	Decision   string      `json:"decision"`
	PolicyID   *string     `json:"policy_id,omitempty"`
	ApprovalID *string     `json:"approval_id,omitempty"`
	ApprovedBy *string     `json:"approved_by,omitempty"`
	LatencyMS  *int32      `json:"latency_ms,omitempty"`
	TokenIn    *int32      `json:"token_in,omitempty"`
	TokenOut   *int32      `json:"token_out,omitempty"`
	Timestamp  time.Time   `json:"timestamp"`
	PrevHash   string      `json:"prev_hash"`
	Hash       string      `json:"hash"`
	// DataHandling (B-078) is the dispatching ai_provider connector's
	// data_handling_designation snapshotted at dispatch time -- empty/absent
	// for every non-ai_provider call and every row written before B-078.
	// Added to the API response by B-094; was previously written to
	// audit_log but never read back anywhere.
	DataHandling *string `json:"data_handling_designation,omitempty"`
}

type AuditListResponse struct {
	Data []AuditEntryResp `json:"data"`
	Meta PaginationMeta   `json:"meta"`
}

// -- FinOps ------------------------------------------------------------------

type AgentSpend struct {
	AgentID      string  `json:"agent_id"`
	AgentName    string  `json:"agent_name"`
	CostUSD      float64 `json:"cost_usd"`
	TokensIn     int64   `json:"tokens_in"`
	TokensOut    int64   `json:"tokens_out"`
	RequestCount int64   `json:"request_count"`
}

type TeamSpend struct {
	Team      string  `json:"team"`
	CostUSD   float64 `json:"cost_usd"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
}

type ModelSpend struct {
	Model     string  `json:"model"`
	CostUSD   float64 `json:"cost_usd"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
}

// ToolSpend is a per-connector cost breakdown (B-108). Not yet declared in
// api/openapi.yaml (Architect-EAMI-owned, out of this session's file
// boundary) -- disclosed, not silently edited, same precedent as B-098's
// agent_id/expires_at deviation. Tool is "unknown" for a call whose
// tool_name wasn't resolved at dispatch time (see finops.go's toolQ).
type ToolSpend struct {
	Tool      string  `json:"tool"`
	CostUSD   float64 `json:"cost_usd"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
}

type TokenSpendSummary struct {
	PeriodStart    time.Time `json:"period_start"`
	PeriodEnd      time.Time `json:"period_end"`
	TotalCostUSD   float64   `json:"total_cost_usd"`
	TotalTokensIn  int64     `json:"total_tokens_in"`
	TotalTokensOut int64     `json:"total_tokens_out"`
	// AvgCostPerOutcome is TotalCostUSD / the number of recorded
	// token_usage rows in the period -- each row already represents one
	// recorded dispatch outcome (B-108; documented in openapi.yaml since
	// before this brief but never actually computed until now). 0 when
	// there are zero rows in the period, not a divide-by-zero NaN.
	AvgCostPerOutcome float64      `json:"avg_cost_per_outcome"`
	ByAgent           []AgentSpend `json:"by_agent"`
	ByTeam            []TeamSpend  `json:"by_team"`
	ByModel           []ModelSpend `json:"by_model"`
	ByTool            []ToolSpend  `json:"by_tool"`
}

type SpendPoint struct {
	Timestamp time.Time `json:"timestamp"`
	CostUSD   float64   `json:"cost_usd"`
	Tokens    int64     `json:"tokens"`
}

type SpendTimeSeries struct {
	Granularity string       `json:"granularity"`
	Series      []SpendPoint `json:"series"`
}

// ── Paste events (B-038 read UI) ───────────────────────────────────────────
// Deliberately carries only the coarse fields paste_events itself has --
// there is no raw-content column anywhere upstream of this struct, so it
// cannot expose pasted text even by accident.

type PasteEventResp struct {
	ID                string    `json:"id"`
	DestinationDomain string    `json:"destination_domain"`
	OccurredAt        time.Time `json:"occurred_at"`
	ContentLength     *int32    `json:"content_length,omitempty"`
	ContentHash       *string   `json:"content_hash,omitempty"`
	OSUsername        *string   `json:"os_username,omitempty"`
}

type PasteEventListResponse struct {
	Data []PasteEventResp `json:"data"`
	Meta PaginationMeta   `json:"meta"`
}

type PasteEventDomainPoint struct {
	Bucket time.Time `json:"bucket"`
	Domain string    `json:"domain"`
	Count  int64     `json:"count"`
}

type PasteEventTimeSeries struct {
	Granularity string                  `json:"granularity"`
	Series      []PasteEventDomainPoint `json:"series"`
}
