package policy

import (
	"path"
	"strings"
)

// toolCategories maps tool name prefixes / keywords to broad categories.
// Used by scope-drift detection to determine whether a tool is consistent
// with the agent's declared scope.
var toolCategories = map[string][]string{
	"file":     {"read_file", "write_file", "delete_file", "list_files", "create_file"},
	"code":     {"run_code", "execute_code", "bash", "shell", "python", "javascript"},
	"web":      {"fetch_url", "browse", "search_web", "http_get", "http_post"},
	"email":    {"send_email", "read_email", "draft_email", "reply_email"},
	"calendar": {"create_event", "delete_event", "list_events", "update_event"},
	"database": {"query_db", "insert_db", "delete_records", "update_records", "sql"},
	"slack":    {"send_message", "read_channel", "create_channel"},
	"github":   {"create_pr", "merge_pr", "push_commit", "create_issue"},
}

// scopeImpliesCategories returns the set of tool categories that the given
// scope string implies. A scope like "file assistant" implies "file"; a scope
// of "email manager" implies "email". This is a heuristic, not exhaustive.
func scopeImpliesCategories(scope string) map[string]bool {
	implied := make(map[string]bool)
	scopeLower := strings.ToLower(scope)
	for cat := range toolCategories {
		if strings.Contains(scopeLower, cat) {
			implied[cat] = true
		}
	}
	// A few common aliases
	if strings.Contains(scopeLower, "coding") || strings.Contains(scopeLower, "developer") {
		implied["code"] = true
		implied["github"] = true
	}
	if strings.Contains(scopeLower, "communication") || strings.Contains(scopeLower, "messaging") {
		implied["email"] = true
		implied["slack"] = true
	}
	if strings.Contains(scopeLower, "data") || strings.Contains(scopeLower, "analyst") {
		implied["database"] = true
	}
	return implied
}

// toolCategory returns the broad category for a tool name, or "" if unknown.
func toolCategory(toolName string) string {
	toolLower := strings.ToLower(toolName)
	for cat, tools := range toolCategories {
		for _, t := range tools {
			if toolLower == t || strings.Contains(toolLower, t) {
				return cat
			}
		}
	}
	// Fallback: check if the tool name itself starts with a category keyword.
	for cat := range toolCategories {
		if strings.HasPrefix(toolLower, cat) {
			return cat
		}
	}
	return ""
}

// matchesRule returns true if ac satisfies all non-empty conditions in r.
// ALL conditions must match (AND logic). An empty/nil condition is a wildcard.
func matchesRule(ac ActionContext, r Rule) bool {
	c := r.Conditions

	// --- AgentNamePattern (glob) ---
	if c.AgentNamePattern != "" {
		matched, err := path.Match(c.AgentNamePattern, ac.AgentName)
		if err != nil || !matched {
			return false
		}
	}

	// --- ToolNames (set membership) ---
	if len(c.ToolNames) > 0 {
		if !stringInSlice(ac.ToolName, c.ToolNames) {
			return false
		}
	}

	// --- ActionTypes (set membership) ---
	if len(c.ActionTypes) > 0 {
		if !stringInSlice(ac.ActionType, c.ActionTypes) {
			return false
		}
	}

	// --- Environments (set membership) ---
	if len(c.Environments) > 0 {
		if !stringInSlice(ac.Environment, c.Environments) {
			return false
		}
	}

	// --- RecordCountGT (numeric threshold) ---
	if c.RecordCountGT != nil {
		if ac.EstimatedRecordCount <= *c.RecordCountGT {
			return false
		}
	}

	// --- ScopeDrift (heuristic) ---
	if c.ScopeDrift {
		if !detectScopeDrift(ac) {
			return false
		}
	}

	// SemanticRule is handled separately in semantic.go — skip here.

	return true
}

// detectScopeDrift returns true when the agent's current action is outside the
// permissions implied by its declared scope. Returns false (no drift) when the
// scope is empty, the tool category is unknown, or the scope is too vague to
// infer categories — safe defaults in all ambiguous cases.
//
// Two heuristic modes:
//
//  1. Verb-prefix scopes ("read:crm", "write:database") — the part before ":"
//     defines the maximum allowed action verb. E.g. "read:*" permits only read
//     actions; any write/delete/execute action is drift.
//
//  2. Keyword scopes ("file assistant", "email manager") — the scope text is
//     checked against known category keywords to infer allowed tool categories.
//     If the current tool's category is not among them, that's drift.
func detectScopeDrift(ac ActionContext) bool {
	// Prefer Scope; fall back to AgentScope for backward compatibility.
	scope := ac.Scope
	if scope == "" {
		scope = ac.AgentScope
	}
	if scope == "" {
		return false
	}

	// Mode 1: verb-prefix scope ("read:crm", "write:database").
	if idx := strings.Index(scope, ":"); idx > 0 {
		scopeVerb := strings.ToLower(scope[:idx])
		if verbScopeDrift(scopeVerb, ac.ActionType) {
			return true
		}
		// Verb check is authoritative — don't fall through to category check.
		return false
	}

	// Mode 2: keyword scope — category-based heuristic.
	cat := toolCategory(ac.ToolName)
	if cat == "" {
		return false // unknown tool — cannot determine drift
	}
	implied := scopeImpliesCategories(scope)
	if len(implied) == 0 {
		return false // scope too vague to infer categories
	}
	return !implied[cat]
}

// verbScopeDrift returns true when the action type exceeds what the scope verb
// permits.
//
//   - "read"  scope → only "read" actions allowed; write/delete/create/execute are drift.
//   - "write" scope → "read", "write", and "create" are allowed; delete/execute are drift.
//
// Unknown scope verbs are treated as no restriction (returns false).
func verbScopeDrift(scopeVerb, actionType string) bool {
	action := strings.ToLower(actionType)
	switch scopeVerb {
	case "read":
		return action != "read"
	case "write":
		return action != "read" && action != "write" && action != "create"
	}
	return false
}

// stringInSlice returns true if s is in the slice (case-insensitive).
func stringInSlice(s string, slice []string) bool {
	sLower := strings.ToLower(s)
	for _, item := range slice {
		if strings.ToLower(item) == sLower {
			return true
		}
	}
	return false
}
