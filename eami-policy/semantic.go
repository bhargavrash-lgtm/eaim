package policy

import "context"

// evaluateSemantic evaluates a natural-language policy rule against an action
// using an LLM. It returns true when the LLM determines the action VIOLATES
// the policy (i.e. the rule should match).
//
// TODO(BE-Policy): Implement full LLM-based semantic evaluation.
//   - Build the prompt from ac and semanticRule.
//   - POST to cfg.semanticEndpoint with cfg.semanticAPIKey.
//   - Enforce cfg.semanticTimeout via context deadline.
//   - Parse response: "YES" → true (violates), "NO" or error/timeout → false.
//   - On timeout or any error: return false, nil (safe default = rule skips).
//
// Current implementation: stub that always returns ESCALATE by having the
// caller treat a non-match as a skip. The stub returns (false, nil) so that
// any rule with a SemanticRule condition is skipped, and the evaluator
// continues to the next rule. If no structural rule matches first, the default
// decision (usually ALLOW) is returned — callers who need a stricter default
// for semantic-only deployments should use WithDefaultDecision(ActionEscalate).
func evaluateSemantic(
	_ context.Context,
	_ ActionContext,
	_ string,
	_ evaluatorConfig,
) (bool, error) {
	// STUB — always returns false (rule does not match via semantic evaluation).
	// Replace with real LLM call in the semantic evaluation milestone.
	return false, nil
}
