package policy

import (
	"context"
	"math/rand"
	"testing"
)

// ---- matchesRule: WorkspaceID -------------------------------------------------

// TestMatchesRule_WorkspaceID is B-207's unit-level proof of the workspace
// guard added to matchesRule, mirroring TestMatchesRule_OrgID's structure
// exactly. Unlike OrgID's empty-is-wildcard convention (a test-literal-only
// convenience -- real policies.org_id is never empty), an empty
// ActionContext.WorkspaceID is a real, common production value (most agents
// belong to no workspace) and must NEVER match a workspace-scoped rule --
// that asymmetry is the specific thing this test exists to prove, not
// assume.
func TestMatchesRule_WorkspaceID(t *testing.T) {
	cases := []struct {
		name        string
		ruleWS      string
		acWS        string
		want        bool
		explanation string
	}{
		{"global rule matches a workspace agent", "", "ws-a", true, "org floor applies everywhere"},
		{"global rule matches a no-workspace agent", "", "", true, "org floor applies even with no workspace"},
		{"workspace rule matches its own workspace", "ws-a", "ws-a", true, "the real add-a-restriction case"},
		{"workspace rule never matches a different workspace", "ws-a", "ws-b", false, "cross-workspace leak, same shape as B-128's cross-org case"},
		{"workspace rule never matches a no-workspace agent", "ws-a", "", false, "the critical asymmetry vs OrgID's empty-is-wildcard convention -- nothing to restrict for an agent not in this workspace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := baseAC()
			ac.OrgID = "org-a"
			ac.WorkspaceID = tc.acWS
			rule := Rule{
				OrgID:       "org-a",
				WorkspaceID: tc.ruleWS,
				Conditions:  Conditions{}, // maximally permissive otherwise
				Action:      ActionDeny,
			}
			if got := matchesRule(ac, rule); got != tc.want {
				t.Errorf("%s: ruleWS=%q acWS=%q: got %v, want %v", tc.explanation, tc.ruleWS, tc.acWS, got, tc.want)
			}
		})
	}
}

// ---- NewEvaluator: the composite sort key --------------------------------------

// TestNewEvaluator_GlobalAlwaysBeforeWorkspace_RegardlessOfInputOrder is the
// single most important test case in this brief (explicit founder
// requirement, not an implementation detail): it proves the Go-side
// sort.Slice comparator in NewEvaluator cannot silently discard the
// intended ordering, even under conditions deliberately engineered to
// expose exactly that regression.
//
// The regression this guards against: if a future edit reverts the
// comparator to comparing Priority alone (the pre-B-207 behavior),
// this test fails, because it specifically constructs a workspace-scoped
// rule with a LOWER priority number than a global rule targeting the same
// action -- the one shape where a priority-only sort and a
// workspace-aware sort disagree. It also feeds rules in several different
// input orderings (forward, reverse, and a fixed pseudo-random shuffle) to
// prove the guarantee holds regardless of what order the caller -- e.g. a
// SQL query whose own ORDER BY might drift out of sync with this
// comparator -- happens to hand rules in. NewEvaluator unconditionally
// re-sorts its input, so the comparator itself, not the caller's order,
// must be what's under test.
func TestNewEvaluator_GlobalAlwaysBeforeWorkspace_RegardlessOfInputOrder(t *testing.T) {
	// Deliberately adversarial priorities: the workspace rule's raw
	// Priority (1) is numerically LOWER than the global rule's (500) --
	// a priority-only sort would place the workspace rule first. The
	// correct, workspace-aware sort must place the global rule first
	// regardless.
	globalDeny := Rule{
		ID: "global-deny", OrgID: "org-a", WorkspaceID: "", Name: "org floor: deny shared-tool",
		Priority: 500, Action: ActionDeny,
		Conditions: Conditions{ToolNames: []string{"shared-tool"}},
	}
	workspaceAllow := Rule{
		ID: "workspace-allow", OrgID: "org-a", WorkspaceID: "ws-a", Name: "workspace: allow shared-tool",
		Priority: 1, Action: ActionAllow,
		Conditions: Conditions{ToolNames: []string{"shared-tool"}},
	}

	orderings := map[string][]Rule{
		"forward":  {globalDeny, workspaceAllow},
		"reverse":  {workspaceAllow, globalDeny},
		"shuffled": shuffledCopy([]Rule{globalDeny, workspaceAllow}, 42),
	}

	for label, rules := range orderings {
		t.Run(label, func(t *testing.T) {
			ev := NewEvaluator(rules)
			// Reach into the concrete type to assert on final sorted
			// order directly, not just the eventual Evaluate() outcome --
			// a direct proof the comparator itself is correct, not an
			// inference from a single evaluation.
			e, ok := ev.(*evaluator)
			if !ok {
				t.Fatalf("NewEvaluator did not return *evaluator")
			}
			if len(e.rules) != 2 {
				t.Fatalf("expected 2 rules, got %d", len(e.rules))
			}
			if e.rules[0].ID != "global-deny" {
				t.Fatalf("REGRESSION: global-deny (WorkspaceID=\"\") must sort before workspace-allow "+
					"(WorkspaceID=\"ws-a\") regardless of Priority (500 vs 1) -- got order [%s, %s]. "+
					"This means the comparator is comparing Priority alone again, silently discarding "+
					"workspace precedence -- the exact failure class this mechanism exists to prevent.",
					e.rules[0].ID, e.rules[1].ID)
			}

			// End-to-end proof, not just internal ordering: Evaluate()
			// must return the org floor's decision, never the
			// workspace's attempted override.
			ac := baseAC()
			ac.OrgID = "org-a"
			ac.WorkspaceID = "ws-a"
			ac.ToolName = "shared-tool"
			d, err := ev.Evaluate(context.Background(), ac)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if d.Action != ActionDeny || d.PolicyID == nil || *d.PolicyID != "global-deny" {
				t.Errorf("REGRESSION: workspace policy overrode the org floor -- got action=%q policyID=%v, "+
					"want deny/global-deny", d.Action, d.PolicyID)
			}
		})
	}
}

// shuffledCopy returns a deterministically-shuffled copy of rules (fixed
// seed, so the test is reproducible, not flaky).
func shuffledCopy(rules []Rule, seed int64) []Rule {
	out := make([]Rule, len(rules))
	copy(out, rules)
	r := rand.New(rand.NewSource(seed))
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// TestEvaluate_WorkspaceCanAddRestrictionOrgFloorNeverSet is the mirror
// case: the org floor has no rule for this tool at all (falls through to
// the default ALLOW), and the workspace's own deny rule correctly fires --
// proving workspace policies CAN add restrictions on top of a silent
// floor, not just that they're blocked from loosening an active one.
func TestEvaluate_WorkspaceCanAddRestrictionOrgFloorNeverSet(t *testing.T) {
	rules := []Rule{
		{
			ID: "workspace-deny", OrgID: "org-a", WorkspaceID: "ws-a", Name: "workspace: deny risky-tool",
			Priority: 1, Action: ActionDeny,
			Conditions: Conditions{ToolNames: []string{"risky-tool"}},
		},
	}
	ev := NewEvaluator(rules)

	ac := baseAC()
	ac.OrgID = "org-a"
	ac.WorkspaceID = "ws-a"
	ac.ToolName = "risky-tool"
	d, err := ev.Evaluate(context.Background(), ac)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Action != ActionDeny || d.PolicyID == nil || *d.PolicyID != "workspace-deny" {
		t.Errorf("workspace's own added restriction did not fire: got action=%q policyID=%v, want deny/workspace-deny",
			d.Action, d.PolicyID)
	}

	// A different workspace (or no workspace) in the same org must NOT
	// be affected by ws-a's own restriction.
	acOther := baseAC()
	acOther.OrgID = "org-a"
	acOther.WorkspaceID = "ws-b"
	acOther.ToolName = "risky-tool"
	dOther, err := ev.Evaluate(context.Background(), acOther)
	if err != nil {
		t.Fatalf("Evaluate (ws-b): %v", err)
	}
	if dOther.Action != ActionAllow {
		t.Errorf("cross-workspace leak: ws-a's own restriction incorrectly reached ws-b -- got action=%q, want allow (default)",
			dOther.Action)
	}

	acNoWS := baseAC()
	acNoWS.OrgID = "org-a"
	acNoWS.WorkspaceID = ""
	acNoWS.ToolName = "risky-tool"
	dNoWS, err := ev.Evaluate(context.Background(), acNoWS)
	if err != nil {
		t.Fatalf("Evaluate (no workspace): %v", err)
	}
	if dNoWS.Action != ActionAllow {
		t.Errorf("cross-workspace leak: ws-a's own restriction incorrectly reached a no-workspace agent -- got action=%q, want allow (default)",
			dNoWS.Action)
	}
}

// TestEvaluate_CrossWorkspacePolicyNeverLeaks is the same-shape test as
// policy_test.go's existing cross-org proof, one level deeper: a real
// two-workspace, single-org scenario where each workspace has its own
// conflicting policy for the same tool, plus a shared org floor -- proving
// all three resolve independently and correctly.
func TestEvaluate_CrossWorkspacePolicyNeverLeaks(t *testing.T) {
	rules := []Rule{
		{
			ID: "org-floor-escalate", OrgID: "org-a", WorkspaceID: "", Name: "org floor: escalate prod-tool",
			Priority: 1, Action: ActionEscalate,
			Conditions: Conditions{ToolNames: []string{"prod-tool"}, Environments: []string{"production"}},
		},
		{
			ID: "ws-a-allow", OrgID: "org-a", WorkspaceID: "ws-a", Name: "ws-a: allow dev-tool",
			Priority: 1, Action: ActionAllow,
			Conditions: Conditions{ToolNames: []string{"dev-tool"}},
		},
		{
			ID: "ws-b-deny", OrgID: "org-a", WorkspaceID: "ws-b", Name: "ws-b: deny dev-tool",
			Priority: 1, Action: ActionDeny,
			Conditions: Conditions{ToolNames: []string{"dev-tool"}},
		},
	}
	ev := NewEvaluator(rules)

	// ws-a's own dev-tool call: its own allow rule fires, ws-b's deny never reached.
	acA := baseAC()
	acA.OrgID, acA.WorkspaceID, acA.ToolName = "org-a", "ws-a", "dev-tool"
	dA, err := ev.Evaluate(context.Background(), acA)
	if err != nil {
		t.Fatalf("ws-a Evaluate: %v", err)
	}
	if dA.Action != ActionAllow || dA.PolicyID == nil || *dA.PolicyID != "ws-a-allow" {
		t.Errorf("ws-a: got action=%q policyID=%v, want allow/ws-a-allow (its own rule, never ws-b's)", dA.Action, dA.PolicyID)
	}

	// ws-b's own dev-tool call: its own deny rule fires, ws-a's allow never reached.
	acB := baseAC()
	acB.OrgID, acB.WorkspaceID, acB.ToolName = "org-a", "ws-b", "dev-tool"
	dB, err := ev.Evaluate(context.Background(), acB)
	if err != nil {
		t.Fatalf("ws-b Evaluate: %v", err)
	}
	if dB.Action != ActionDeny || dB.PolicyID == nil || *dB.PolicyID != "ws-b-deny" {
		t.Errorf("ws-b: got action=%q policyID=%v, want deny/ws-b-deny (its own rule, never ws-a's)", dB.Action, dB.PolicyID)
	}

	// Either workspace's prod-tool call in production: the shared org
	// floor still fires for both, unaffected by either workspace's own
	// unrelated dev-tool rules.
	for _, ws := range []string{"ws-a", "ws-b", ""} {
		ac := baseAC()
		ac.OrgID, ac.WorkspaceID, ac.ToolName, ac.Environment = "org-a", ws, "prod-tool", "production"
		d, err := ev.Evaluate(context.Background(), ac)
		if err != nil {
			t.Fatalf("org floor Evaluate (ws=%q): %v", ws, err)
		}
		if d.Action != ActionEscalate || d.PolicyID == nil || *d.PolicyID != "org-floor-escalate" {
			t.Errorf("org floor (ws=%q): got action=%q policyID=%v, want escalate/org-floor-escalate", ws, d.Action, d.PolicyID)
		}
	}
}
