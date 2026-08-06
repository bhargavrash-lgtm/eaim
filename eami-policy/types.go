// Package policy provides the EAMI policy engine for evaluating AI agent actions
// against a set of rules. It is a pure library — no HTTP server, no binary.
package policy

import "time"

// Decision action constants.
const (
	ActionAllow   = "allow"
	ActionDeny    = "deny"
	ActionEscalate = "escalate"
)

// ActionContext describes a single tool call from an AI agent.
// It is the input to every policy evaluation.
type ActionContext struct {
	AgentID    string
	AgentName  string
	// Scope is the canonical declared task scope (preferred over AgentScope).
	// Format: plain language ("file assistant") or verb-prefixed ("read:crm").
	// BE-Gateway populates this from gateway_agents.scope via ToPolicyContext().
	Scope      string
	AgentScope string // deprecated alias for Scope; kept for backward compatibility

	Model    string
	Owner    string
	RiskTier string // "low" | "medium" | "high"

	ToolName   string
	ActionType string // verb: "read" | "write" | "delete" | "create" | "execute"
	ActionVerb string // more specific: "delete_records" | "send_email" | etc.

	// ToolServerID is the resolved gateway_tools.id UUID for this call, set
	// by the dispatch caller only when the tool name resolved to a real,
	// usable row (B-044's dynamic routing). Empty when unresolved (no
	// matching row, or a row of a type dynamic routing doesn't yet cover) --
	// rules using ToolServerIDs simply never match in that case, the same
	// "empty = wildcard, never a match" convention every other Conditions
	// field already follows.
	ToolServerID string

	Parameters  map[string]any
	Environment string // "production" | "staging" | "development" | "unknown"

	EstimatedRecordCount int
	SessionActions       []string // previous tool names in this session

	Timestamp time.Time
}

// Decision is the outcome of policy evaluation.
type Decision struct {
	Action     string  // "allow" | "deny" | "escalate"
	PolicyID   *string // UUID of the matching rule, nil if default
	PolicyName *string // name of the matching rule, nil if default
	Reason     string  // human-readable explanation
	EvalTimeMS int
}

// Conditions mirrors the policy_conditions table.
// An empty/nil field means "match any" for that field.
type Conditions struct {
	// AgentNamePattern is a glob pattern matched against ActionContext.AgentName.
	// Empty = match any agent name.
	AgentNamePattern string

	// ToolNames is a set of allowed tool names.
	// Empty = match any tool.
	ToolNames []string

	// ToolServerIDs is a set of allowed gateway_tools.id UUIDs (B-044) --
	// pins a rule to a specific, immutable resolved tool row, unlike
	// ToolNames' by-string-name match (a tool can be renamed, or deleted
	// and recreated under the same name, without ToolServerIDs following
	// it). Empty = match any (including unresolved calls).
	ToolServerIDs []string

	// ActionTypes is a set of allowed action types (read, write, delete, create, execute).
	// Empty = match any action type.
	ActionTypes []string

	// Environments is a set of allowed environments.
	// Empty = match any environment.
	Environments []string

	// RecordCountGT triggers when EstimatedRecordCount > *RecordCountGT.
	// Nil = no threshold check.
	RecordCountGT *int

	// SemanticRule is a natural-language policy statement evaluated by an LLM.
	// Empty = skip semantic evaluation.
	SemanticRule string

	// ScopeDrift triggers when the agent's tool category is outside the implied
	// categories of AgentScope. Uses a heuristic mapping.
	ScopeDrift bool
}

// Rule represents a single policy rule loaded from the database.
type Rule struct {
	ID         string
	Name       string
	Priority   int        // lower number = higher priority (1 is highest)
	Conditions Conditions
	Action     string // "allow" | "deny" | "escalate"
	Alert      bool   // whether to emit an alert on match
}

// PolicySet is an ordered collection of rules. The evaluator sorts them by
// priority before evaluation, so callers need not pre-sort.
type PolicySet []Rule
