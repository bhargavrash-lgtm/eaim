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
}

type APIKeyResp struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"created_at"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
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
	Name        string              `json:"name"`
	Description *string             `json:"description"`
	Priority    int32               `json:"priority"`
	Conditions  *PolicyConditionsReq `json:"conditions"`
	Action      string              `json:"action"`
	Alert       bool                `json:"alert"`
	Status      string              `json:"status"`
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

type TokenSpendSummary struct {
	PeriodStart    time.Time    `json:"period_start"`
	PeriodEnd      time.Time    `json:"period_end"`
	TotalCostUSD   float64      `json:"total_cost_usd"`
	TotalTokensIn  int64        `json:"total_tokens_in"`
	TotalTokensOut int64        `json:"total_tokens_out"`
	ByAgent        []AgentSpend `json:"by_agent"`
	ByTeam         []TeamSpend  `json:"by_team"`
	ByModel        []ModelSpend `json:"by_model"`
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
