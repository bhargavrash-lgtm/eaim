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
	Condition       string   // human-readable, e.g. "denied_actions_count > 10 in 15m"
	ConditionConfig []byte   // JSONB: {metric, condition, threshold, window_minutes}
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

