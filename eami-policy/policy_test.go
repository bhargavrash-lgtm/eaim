package policy

import (
	"context"
	"testing"
	"time"
)

// ptr helpers
func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

// baseAC returns a minimal valid ActionContext for tests to build on.
func baseAC() ActionContext {
	return ActionContext{
		AgentID:    "agent-001",
		AgentName:  "test-agent",
		AgentScope: "file assistant",
		Model:      "gpt-4",
		Owner:      "user@example.com",
		RiskTier:   "low",
		ToolName:   "read_file",
		ActionType: "read",
		ActionVerb: "read_file",
		Parameters: map[string]any{},
		Environment: "development",
		EstimatedRecordCount: 10,
		SessionActions:       nil,
		Timestamp:            time.Now(),
	}
}

// ---- Structural match unit tests -----------------------------------------------

func TestMatchesRule_AgentNameGlob(t *testing.T) {
	cases := []struct {
		name      string
		pattern   string
		agentName string
		want      bool
	}{
		{"exact match", "test-agent", "test-agent", true},
		{"wildcard suffix", "test-*", "test-agent", true},
		{"wildcard prefix", "*-agent", "test-agent", true},
		{"full wildcard", "*", "anything", true},
		{"no match", "prod-*", "test-agent", false},
		{"empty pattern matches all", "", "test-agent", true},
		{"question mark wildcard", "test-?gent", "test-agent", true},
		{"question mark no match", "test-?gent", "test-aagent", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := baseAC()
			ac.AgentName = tc.agentName
			rule := Rule{
				ID:       "r1",
				Name:     "glob-test",
				Priority: 1,
				Action:   ActionDeny,
				Conditions: Conditions{
					AgentNamePattern: tc.pattern,
				},
			}
			got := matchesRule(ac, rule)
			if got != tc.want {
				t.Errorf("pattern=%q agentName=%q: got %v, want %v", tc.pattern, tc.agentName, got, tc.want)
			}
		})
	}
}

func TestMatchesRule_ToolNames(t *testing.T) {
	cases := []struct {
		name      string
		toolNames []string
		toolName  string
		want      bool
	}{
		{"in list", []string{"read_file", "write_file"}, "read_file", true},
		{"not in list", []string{"write_file", "delete_file"}, "read_file", false},
		{"empty list matches all", []string{}, "any_tool", true},
		{"case insensitive", []string{"Read_File"}, "read_file", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := baseAC()
			ac.ToolName = tc.toolName
			rule := Rule{
				Conditions: Conditions{ToolNames: tc.toolNames},
				Action:     ActionDeny,
			}
			if matchesRule(ac, rule) != tc.want {
				t.Errorf("tool=%q list=%v: got %v, want %v", tc.toolName, tc.toolNames, !tc.want, tc.want)
			}
		})
	}
}

func TestMatchesRule_ActionTypes(t *testing.T) {
	cases := []struct {
		name        string
		actionTypes []string
		actionType  string
		want        bool
	}{
		{"matches write", []string{"write", "delete"}, "write", true},
		{"no match", []string{"write", "delete"}, "read", false},
		{"empty matches all", []string{}, "read", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := baseAC()
			ac.ActionType = tc.actionType
			rule := Rule{Conditions: Conditions{ActionTypes: tc.actionTypes}, Action: ActionDeny}
			if matchesRule(ac, rule) != tc.want {
				t.Errorf("actionType=%q list=%v: want %v", tc.actionType, tc.actionTypes, tc.want)
			}
		})
	}
}

func TestMatchesRule_Environments(t *testing.T) {
	cases := []struct {
		name         string
		environments []string
		environment  string
		want         bool
	}{
		{"prod match", []string{"production"}, "production", true},
		{"prod no match", []string{"production"}, "staging", false},
		{"empty matches all", []string{}, "production", true},
		{"multi-env match", []string{"production", "staging"}, "staging", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := baseAC()
			ac.Environment = tc.environment
			rule := Rule{Conditions: Conditions{Environments: tc.environments}, Action: ActionDeny}
			if matchesRule(ac, rule) != tc.want {
				t.Errorf("env=%q list=%v: want %v", tc.environment, tc.environments, tc.want)
			}
		})
	}
}

func TestMatchesRule_RecordCountGT(t *testing.T) {
	cases := []struct {
		name      string
		threshold int
		count     int
		want      bool
	}{
		{"above threshold", 100, 101, true},
		{"at threshold", 100, 100, false},  // condition is strictly greater-than
		{"below threshold", 100, 50, false},
		{"zero threshold exceeded", 0, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := baseAC()
			ac.EstimatedRecordCount = tc.count
			rule := Rule{
				Conditions: Conditions{RecordCountGT: intPtr(tc.threshold)},
				Action:     ActionDeny,
			}
			if matchesRule(ac, rule) != tc.want {
				t.Errorf("count=%d threshold=%d: want %v", tc.count, tc.threshold, tc.want)
			}
		})
	}
}

func TestMatchesRule_ScopeDrift(t *testing.T) {
	cases := []struct {
		name       string
		scope      string
		toolName   string
		wantDrift  bool // true = scope drift detected = rule matches
	}{
		{
			name:      "file tool in file scope — no drift",
			scope:     "file assistant",
			toolName:  "read_file",
			wantDrift: false,
		},
		{
			name:      "email tool in file scope — drift",
			scope:     "file assistant",
			toolName:  "send_email",
			wantDrift: true,
		},
		{
			name:      "email tool in communication scope — no drift",
			scope:     "communication assistant",
			toolName:  "send_email",
			wantDrift: false,
		},
		{
			name:      "unknown tool — no drift (safe default)",
			scope:     "file assistant",
			toolName:  "completely_unknown_tool_xyz",
			wantDrift: false,
		},
		{
			name:      "empty scope — no drift (safe default)",
			scope:     "",
			toolName:  "send_email",
			wantDrift: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := baseAC()
			ac.AgentScope = tc.scope
			ac.ToolName = tc.toolName
			rule := Rule{
				Conditions: Conditions{ScopeDrift: true},
				Action:     ActionEscalate,
			}
			got := matchesRule(ac, rule)
			if got != tc.wantDrift {
				t.Errorf("scope=%q tool=%q: got drift=%v, want %v", tc.scope, tc.toolName, got, tc.wantDrift)
			}
		})
	}
}

func TestMatchesRule_MultipleConditions(t *testing.T) {
	// ALL conditions must match (AND logic).
	ac := baseAC()
	ac.ToolName = "delete_records"
	ac.ActionType = "delete"
	ac.Environment = "production"
	ac.EstimatedRecordCount = 500

	rule := Rule{
		Conditions: Conditions{
			ToolNames:     []string{"delete_records"},
			ActionTypes:   []string{"delete"},
			Environments:  []string{"production"},
			RecordCountGT: intPtr(100),
		},
		Action: ActionDeny,
	}

	if !matchesRule(ac, rule) {
		t.Error("expected rule to match when all conditions are satisfied")
	}

	// Flip one condition — should no longer match.
	ac.Environment = "staging"
	if matchesRule(ac, rule) {
		t.Error("expected rule NOT to match when environment condition fails")
	}
}

// ---- Evaluator integration tests -----------------------------------------------

func TestEvaluate_ExplicitAllow(t *testing.T) {
	rules := []Rule{
		{
			ID:       "allow-reads",
			Name:     "Allow read actions",
			Priority: 1,
			Action:   ActionAllow,
			Conditions: Conditions{
				ActionTypes: []string{"read"},
			},
		},
	}
	ev := NewEvaluator(rules)
	ac := baseAC()
	ac.ActionType = "read"

	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("got %q, want %q", d.Action, ActionAllow)
	}
	if d.PolicyID == nil || *d.PolicyID != "allow-reads" {
		t.Errorf("expected PolicyID=allow-reads, got %v", d.PolicyID)
	}
}

func TestEvaluate_ExplicitDeny(t *testing.T) {
	rules := []Rule{
		{
			ID:       "deny-deletes",
			Name:     "Deny delete actions",
			Priority: 1,
			Action:   ActionDeny,
			Conditions: Conditions{
				ActionTypes: []string{"delete"},
			},
		},
	}
	ev := NewEvaluator(rules)
	ac := baseAC()
	ac.ActionType = "delete"

	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Action != ActionDeny {
		t.Errorf("got %q, want %q", d.Action, ActionDeny)
	}
}

func TestEvaluate_ExplicitEscalate(t *testing.T) {
	rules := []Rule{
		{
			ID:       "escalate-prod-writes",
			Name:     "Escalate production writes",
			Priority: 1,
			Action:   ActionEscalate,
			Conditions: Conditions{
				Environments: []string{"production"},
				ActionTypes:  []string{"write"},
			},
		},
	}
	ev := NewEvaluator(rules)
	ac := baseAC()
	ac.Environment = "production"
	ac.ActionType = "write"

	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Action != ActionEscalate {
		t.Errorf("got %q, want %q", d.Action, ActionEscalate)
	}
}

func TestEvaluate_NoMatchDefaultAllow(t *testing.T) {
	// Rules exist but none match — should return default ALLOW.
	rules := []Rule{
		{
			ID:       "deny-prod-deletes",
			Name:     "Deny production deletes",
			Priority: 1,
			Action:   ActionDeny,
			Conditions: Conditions{
				Environments: []string{"production"},
				ActionTypes:  []string{"delete"},
			},
		},
	}
	ev := NewEvaluator(rules)
	ac := baseAC()
	ac.Environment = "development"
	ac.ActionType = "read"

	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("got %q, want %q (default)", d.Action, ActionAllow)
	}
	if d.PolicyID != nil {
		t.Errorf("expected nil PolicyID for default decision, got %v", d.PolicyID)
	}
}

func TestEvaluate_NoMatchCustomDefault(t *testing.T) {
	rules := []Rule{} // no rules
	ev := NewEvaluator(rules, WithDefaultDecision(ActionDeny))
	ac := baseAC()

	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Action != ActionDeny {
		t.Errorf("got %q, want %q (custom default)", d.Action, ActionDeny)
	}
}

func TestEvaluate_PriorityOrdering(t *testing.T) {
	// Lower priority number wins. Rule with priority 1 should be evaluated
	// before rule with priority 2, even if priority 2 was added to the slice first.
	rules := []Rule{
		{
			ID:       "low-priority-allow",
			Name:     "Low priority allow",
			Priority: 10,
			Action:   ActionAllow,
			Conditions: Conditions{
				ActionTypes: []string{"write"},
			},
		},
		{
			ID:       "high-priority-deny",
			Name:     "High priority deny",
			Priority: 1,
			Action:   ActionDeny,
			Conditions: Conditions{
				ActionTypes: []string{"write"},
			},
		},
	}
	ev := NewEvaluator(rules)
	ac := baseAC()
	ac.ActionType = "write"

	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Action != ActionDeny {
		t.Errorf("got %q, want DENY (high-priority rule should win)", d.Action)
	}
	if d.PolicyID == nil || *d.PolicyID != "high-priority-deny" {
		t.Errorf("expected high-priority-deny rule to match, got %v", d.PolicyID)
	}
}

func TestEvaluate_PriorityOrderingSecondRuleWins(t *testing.T) {
	// First rule (priority 1) does NOT match; second rule (priority 5) does match.
	threshold := 100
	rules := []Rule{
		{
			ID:       "bulk-delete-deny",
			Name:     "Deny bulk deletes",
			Priority: 1,
			Action:   ActionDeny,
			Conditions: Conditions{
				ActionTypes:   []string{"delete"},
				RecordCountGT: &threshold,
			},
		},
		{
			ID:       "all-writes-escalate",
			Name:     "Escalate all writes",
			Priority: 5,
			Action:   ActionEscalate,
			Conditions: Conditions{
				ActionTypes: []string{"write"},
			},
		},
	}
	ev := NewEvaluator(rules)
	ac := baseAC()
	ac.ActionType = "write"
	ac.EstimatedRecordCount = 5 // below the delete threshold

	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Action != ActionEscalate {
		t.Errorf("got %q, want ESCALATE (second rule should match)", d.Action)
	}
}

func TestEvaluate_EmptyRuleSet(t *testing.T) {
	ev := NewEvaluator(nil)
	d, err := ev.Evaluate(context.Background(), baseAC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("got %q, want ALLOW for empty ruleset", d.Action)
	}
}

func TestEvaluate_SemanticRuleSkippedByStub(t *testing.T) {
	// A rule with only a SemanticRule (no structural conditions) should be
	// skipped by the stub, falling through to the default ALLOW.
	rules := []Rule{
		{
			ID:     "semantic-only",
			Name:   "Semantic only rule",
			Priority: 1,
			Action: ActionDeny,
			Conditions: Conditions{
				SemanticRule: "The agent must not exfiltrate sensitive data.",
			},
		},
	}
	ev := NewEvaluator(rules)
	d, err := ev.Evaluate(context.Background(), baseAC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Stub always returns false for semantic eval → rule skips → default ALLOW.
	if d.Action != ActionAllow {
		t.Errorf("got %q, want ALLOW (semantic stub skips rule)", d.Action)
	}
}

func TestEvaluate_DecisionAlwaysPopulated(t *testing.T) {
	// Contract: Decision.Action is never empty, even on the default path.
	ev := NewEvaluator(nil)
	d, _ := ev.Evaluate(context.Background(), baseAC())
	if d.Action == "" {
		t.Error("Decision.Action must never be empty")
	}
	if d.Reason == "" {
		t.Error("Decision.Reason must never be empty")
	}
}

// ---- Scope drift unit tests (via evaluator) ------------------------------------

func TestEvaluate_ScopeDriftDenied(t *testing.T) {
	rules := []Rule{
		{
			ID:       "scope-drift-escalate",
			Name:     "Escalate scope drift",
			Priority: 1,
			Action:   ActionEscalate,
			Conditions: Conditions{
				ScopeDrift: true,
			},
		},
	}
	ev := NewEvaluator(rules)
	ac := baseAC()
	ac.AgentScope = "file assistant"
	ac.ToolName = "send_email" // email tool in a file-scoped agent

	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Action != ActionEscalate {
		t.Errorf("got %q, want ESCALATE for scope drift", d.Action)
	}
}

func TestEvaluate_NoScopeDriftAllowed(t *testing.T) {
	rules := []Rule{
		{
			ID:       "scope-drift-escalate",
			Name:     "Escalate scope drift",
			Priority: 1,
			Action:   ActionEscalate,
			Conditions: Conditions{
				ScopeDrift: true,
			},
		},
	}
	ev := NewEvaluator(rules)
	ac := baseAC()
	ac.AgentScope = "file assistant"
	ac.ToolName = "read_file" // consistent with scope

	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No drift → scope-drift rule does not match → default ALLOW
	if d.Action != ActionAllow {
		t.Errorf("got %q, want ALLOW (no scope drift)", d.Action)
	}
}

// ---- Scope field (new canonical field) tests -----------------------------------

// TestEvaluate_ScopeDrift_VerbPrefix_DeleteOutsideReadScope is the acceptance-
// criteria test: agent declares Scope="read:crm", attempts a "delete" action →
// scope drift detected → ESCALATE.
func TestEvaluate_ScopeDrift_VerbPrefix_DeleteOutsideReadScope(t *testing.T) {
	rules := []Rule{
		{
			ID:       "scope-drift-escalate",
			Name:     "Escalate scope drift",
			Priority: 1,
			Action:   ActionEscalate,
			Conditions: Conditions{
				ScopeDrift: true,
			},
		},
	}
	ev := NewEvaluator(rules)
	ac := baseAC()
	ac.Scope = "read:crm"   // declared scope: read-only CRM access
	ac.ActionType = "delete" // attempted action: delete — outside read scope
	ac.ToolName = "crm_delete_record"

	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Action != ActionEscalate {
		t.Errorf("got %q, want ESCALATE (read:crm scope violated by delete action)", d.Action)
	}
}

func TestEvaluate_ScopeDrift_VerbPrefix_ReadWithinReadScope(t *testing.T) {
	// "read:crm" scope + "read" action → no drift → rule does not match → default ALLOW.
	rules := []Rule{
		{
			ID:       "scope-drift-escalate",
			Name:     "Escalate scope drift",
			Priority: 1,
			Action:   ActionEscalate,
			Conditions: Conditions{ScopeDrift: true},
		},
	}
	ev := NewEvaluator(rules)
	ac := baseAC()
	ac.Scope = "read:crm"
	ac.ActionType = "read"
	ac.ToolName = "crm_get_contact"

	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("got %q, want ALLOW (read action is within read:crm scope)", d.Action)
	}
}

func TestEvaluate_ScopeDrift_VerbPrefix_WriteOutsideReadScope(t *testing.T) {
	// "read:crm" scope + "write" action → drift → ESCALATE.
	rules := []Rule{
		{ID: "r1", Priority: 1, Action: ActionEscalate, Conditions: Conditions{ScopeDrift: true}},
	}
	ev := NewEvaluator(rules)
	ac := baseAC()
	ac.Scope = "read:crm"
	ac.ActionType = "write"

	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Action != ActionEscalate {
		t.Errorf("got %q, want ESCALATE (write outside read scope)", d.Action)
	}
}

func TestEvaluate_ScopeDrift_ScopeFieldPreferredOverAgentScope(t *testing.T) {
	// When both Scope and AgentScope are set, Scope takes precedence.
	rules := []Rule{
		{ID: "r1", Priority: 1, Action: ActionEscalate, Conditions: Conditions{ScopeDrift: true}},
	}
	ev := NewEvaluator(rules)
	ac := baseAC()
	ac.Scope = "read:crm"        // restricts to read only
	ac.AgentScope = "file assistant" // would not restrict delete if used
	ac.ActionType = "delete"

	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Scope="read:crm" takes precedence → delete is drift → ESCALATE.
	if d.Action != ActionEscalate {
		t.Errorf("got %q, want ESCALATE (Scope field should take precedence over AgentScope)", d.Action)
	}
}

func TestMatchesRule_ScopeDrift_VerbPrefix(t *testing.T) {
	cases := []struct {
		name       string
		scope      string
		actionType string
		wantDrift  bool
	}{
		{"read scope, read action — no drift", "read:database", "read", false},
		{"read scope, write action — drift", "read:database", "write", true},
		{"read scope, delete action — drift", "read:database", "delete", true},
		{"read scope, execute action — drift", "read:database", "execute", true},
		{"write scope, read action — no drift", "write:database", "read", false},
		{"write scope, write action — no drift", "write:database", "write", false},
		{"write scope, create action — no drift", "write:database", "create", false},
		{"write scope, delete action — drift", "write:database", "delete", true},
		{"unknown verb — no restriction", "admin:database", "delete", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := baseAC()
			ac.Scope = tc.scope
			ac.ActionType = tc.actionType
			rule := Rule{Conditions: Conditions{ScopeDrift: true}, Action: ActionEscalate}
			got := matchesRule(ac, rule)
			if got != tc.wantDrift {
				t.Errorf("scope=%q action=%q: got drift=%v, want %v", tc.scope, tc.actionType, got, tc.wantDrift)
			}
		})
	}
}

// ---- Option tests --------------------------------------------------------------

func TestNewEvaluator_WithSemanticLLM(t *testing.T) {
	// Just verifies the option is accepted without panic.
	_ = NewEvaluator(nil,
		WithSemanticLLM("https://api.example.com/v1/chat", "key-abc", 3*time.Second),
	)
}
