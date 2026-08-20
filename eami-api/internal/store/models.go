package store

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// OrgSettings holds org-level config fields added by migration 002.
type OrgSettings struct {
	ID              uuid.UUID
	Name            string
	Slug            string
	Plan            string
	Timezone        string
	DefaultRiskTier string
	UpdatedAt       time.Time
}

// NotificationConfig mirrors the notification_config table (migration 002).
type NotificationConfig struct {
	OrgID           uuid.UUID
	SlackEnabled    bool
	SlackWebhookURL pgtype.Text
	EmailEnabled    bool
	EmailSMTPHost   pgtype.Text
	EmailSMTPPort   int32
	EmailFrom       pgtype.Text
	UpdatedAt       time.Time
}

// GatewayAgent mirrors the gateway_agents table.
type GatewayAgent struct {
	ID              uuid.UUID
	OrgID           uuid.UUID
	Name            string
	Model           string
	Owner           string
	Scope           string
	RiskTier        string
	Status          string
	TokenTTLSeconds int32
	CreatedBy       pgtype.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastSeen        pgtype.Timestamptz
}

// Policy mirrors the policies table.
type Policy struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	Name        string
	Description pgtype.Text
	Priority    int32
	Action      string
	Alert       bool
	Status      string
	CreatedBy   pgtype.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PolicyCondition mirrors the policy_conditions table.
type PolicyCondition struct {
	ID               uuid.UUID
	PolicyID         uuid.UUID
	AgentNamePattern pgtype.Text
	ToolNames        []string
	ActionTypes      []string
	Environments     []string
	RecordCountGT    pgtype.Int4
	SemanticRule     pgtype.Text
	ScopeDrift       bool
}

// PolicyRow is the combined result of a policy + conditions join.
type PolicyRow struct {
	Policy
	Condition PolicyCondition
}

// AuditEntry mirrors the audit_log table.
type AuditEntry struct {
	ID         uuid.UUID
	OrgID      uuid.UUID
	AgentID    pgtype.UUID
	AgentName  string
	ToolName   string
	Action     string
	Parameters []byte
	Decision   string
	PolicyID   pgtype.UUID
	ApprovalID pgtype.UUID
	ApprovedBy pgtype.Text
	LatencyMS  pgtype.Int4
	TokenIn    pgtype.Int4
	TokenOut   pgtype.Int4
	Timestamp  time.Time
	PrevHash   string
	Hash       string
}

// User mirrors the users table (auth fields only).
type User struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	Email        string
	Name         pgtype.Text
	PasswordHash pgtype.Text
	Role         string
}

// RefreshToken mirrors the refresh_tokens table.
type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	Revoked   bool
}

// APIKey mirrors the api_keys table.
type APIKey struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	Name      string
	KeyHash   string
	Prefix    string
	Scopes    []string
	CreatedBy pgtype.UUID
	CreatedAt time.Time
	LastUsed  pgtype.Timestamptz
	Revoked   bool
}

// ApprovalRequest mirrors the approval_requests table.
type ApprovalRequest struct {
	ID                 uuid.UUID
	OrgID              uuid.UUID
	AgentID            uuid.UUID
	AgentName          string
	ToolName           string
	Action             string
	Parameters         []byte
	Justification      string
	RiskLevel          string
	EstimatedRecords   pgtype.Int4
	Reversible         pgtype.Bool
	Environment        pgtype.Text
	DataTypes          []string
	PolicyID           pgtype.UUID
	Status             string
	ApprovedBy         pgtype.UUID
	DecisionReason     pgtype.Text
	DecidedAt          pgtype.Timestamptz
	ExpiresAt          time.Time
	CreatedAt          time.Time
	GatewaySessionID   string
	GatewayNodeAddress string
}

// AlertRule mirrors the alert_rules table.
type AlertRule struct {
	ID              uuid.UUID
	OrgID           uuid.UUID
	Name            string
	Description     pgtype.Text
	Condition       string // human-readable, e.g. "denied_actions_count > 10 in 15m"
	ConditionConfig []byte // JSONB: {metric, condition, threshold, window_minutes}
	Severity        string
	Channels        []string
	Enabled         bool
	CreatedBy       pgtype.UUID
	CreatedAt       time.Time
}

// Alert mirrors the alerts table (includes migration 003 columns).
type Alert struct {
	ID             uuid.UUID
	OrgID          uuid.UUID
	RuleID         uuid.UUID
	RuleName       string // joined from alert_rules (not a real column)
	Severity       string
	Message        string
	Context        []byte
	FiredAt        time.Time
	ResolvedAt     pgtype.Timestamptz
	Notified       bool
	Status         string // 'open'|'acknowledged'|'resolved' (migration 003)
	AcknowledgedBy pgtype.UUID
	AcknowledgedAt pgtype.Timestamptz
	MetricValue    pgtype.Float8 // migration 003
}

// GatewayTool mirrors the gateway_tools table.
type GatewayTool struct {
	ID         uuid.UUID
	OrgID      uuid.UUID
	Name       string
	Type       string // mcp | rest_api | database
	AuthType   string // oauth2 | api_key | basic | db_connection_string
	MCPCommand pgtype.Text
	BaseURL    pgtype.Text
	Status     string // connected | degraded | disconnected
	LastUsed   pgtype.Timestamptz
	LastTested pgtype.Timestamptz
	CreatedAt  time.Time
	// ActionPaths is the raw JSONB bytes of gateway_tools.action_paths
	// (B-046): {"<action>": {"path": "...", "method": "..."}}, or nil when
	// unset. Same raw-passthrough convention as alert_rules.ConditionConfig.
	ActionPaths []byte
	// Provider is the AI provider identifier for type=ai_provider tools
	// (AI Provider Connector, Thread A Model 1), e.g. "claude" -- nil for
	// every other type.
	Provider pgtype.Text
	// AuditMode is "full" or "structural_metadata_only" (NOT NULL,
	// defaults to the latter -- schema/migrations-v2/000004). Governs
	// only whether eami-gateway's audit_log writes include this
	// connector's call parameters; meaningful only for type=ai_provider
	// but present (at its DB default) on every row.
	AuditMode string
	// DataHandlingDesignation is "zero_retention" | "standard_retention" |
	// "unknown" (NOT NULL, defaults to "unknown" -- schema/migrations-v2/
	// 000008). Visibility only, per B-078: EAMI cannot enforce what a
	// third-party AI provider does with dispatched data, this records
	// what the admin has confirmed the actual commercial agreement says.
	// Meaningful only for type=ai_provider but present (at its DB
	// default) on every row, same as AuditMode above.
	DataHandlingDesignation string
	// DataHandlingNote is a free-text citation of the actual agreement
	// (e.g. "Anthropic Enterprise Agreement dated 2026-03-01, ZDR
	// Addendum") -- nullable, since a structured designation alone may be
	// all an admin has confirmed.
	DataHandlingNote pgtype.Text
}

// GatewayNode mirrors the gateway_nodes table (joined with latest metrics).
type GatewayNode struct {
	ID            uuid.UUID
	OrgID         uuid.UUID
	Name          string
	Role          string // primary | edge | dr_standby
	Status        string // healthy | degraded | standby | offline
	Address       string
	Hostname      pgtype.Text
	Version       pgtype.Text
	LastHeartbeat pgtype.Timestamptz
	// Latest metrics (LEFT JOIN gateway_node_metrics)
	CPUPct         pgtype.Float8
	RequestsPerMin pgtype.Int4
}

// Workflow mirrors the workflows table (Multi-Hop Workflows, Thread B
// Brief 1, B-058). Definition-only: nothing in eami-gateway's dispatch
// path reads this table yet.
type Workflow struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	Name      string
	Status    string // active | draft | disabled
	CreatedBy pgtype.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
	// StepCount is populated only by ListWorkflows (a joined COUNT, not a
	// real column) -- GetWorkflow returns the real Steps slice instead.
	StepCount int64
}

// WorkflowStep mirrors the workflow_steps table, LEFT JOINed with
// gateway_tools for display. GatewayToolID/ToolName are both invalid/nil
// when the step's connector has been deleted (ON DELETE SET NULL, see
// migration 000006) -- a deliberately visible, not-hidden dangling
// reference; see workflows.go's GetWorkflow for how this surfaces in the
// API response.
type WorkflowStep struct {
	ID            uuid.UUID
	WorkflowID    uuid.UUID
	StepOrder     int32
	GatewayToolID pgtype.UUID
	ToolName      pgtype.Text // joined from gateway_tools.name; invalid if tool deleted or (defensively) not yet joined
	Action        string
	// InputMapping is the raw JSONB bytes of workflow_steps.input_mapping:
	// a per-param map of {from_step, path} extraction references (B-063),
	// resolved at execution time by eami-gateway/internal/workflow against
	// a prior step's real recorded result within the same run. This layer
	// never interprets the contents -- only passes the bytes through.
	InputMapping []byte
}
