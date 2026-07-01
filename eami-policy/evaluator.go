package policy

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Evaluator evaluates an ActionContext against a PolicySet and returns a Decision.
type Evaluator interface {
	// Evaluate runs all rules in priority order against the action context.
	// Returns the first matching rule's decision, or the default decision if
	// no rule matches. The default is ActionAllow unless overridden by
	// WithDefaultDecision.
	Evaluate(ctx context.Context, ac ActionContext) (Decision, error)
}

// evaluatorConfig holds optional configuration for an Evaluator.
type evaluatorConfig struct {
	defaultAction string

	// Semantic LLM config (unused in structural-only mode).
	semanticEndpoint string
	semanticAPIKey   string
	semanticTimeout  time.Duration
}

// Option is a functional option for NewEvaluator.
type Option func(*evaluatorConfig)

// WithDefaultDecision sets the action returned when no rule matches.
// Defaults to ActionAllow.
func WithDefaultDecision(action string) Option {
	return func(cfg *evaluatorConfig) {
		cfg.defaultAction = action
	}
}

// WithSemanticLLM configures the LLM endpoint used for semantic rule evaluation.
// Semantic evaluation is a stub in this release — this option is accepted but
// the endpoint is not called.
func WithSemanticLLM(endpoint, apiKey string, timeout time.Duration) Option {
	return func(cfg *evaluatorConfig) {
		cfg.semanticEndpoint = endpoint
		cfg.semanticAPIKey   = apiKey
		cfg.semanticTimeout  = timeout
	}
}

// evaluator is the concrete implementation of Evaluator.
type evaluator struct {
	rules []Rule // sorted by Priority ascending (lower = higher priority)
	cfg   evaluatorConfig
}

// NewEvaluator constructs an Evaluator with the given rules and options.
// Rules are sorted internally by Priority (ascending); callers need not sort.
func NewEvaluator(rules []Rule, opts ...Option) Evaluator {
	cfg := evaluatorConfig{
		defaultAction:   ActionAllow,
		semanticTimeout: 2 * time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}

	// Copy and sort so the caller's slice is not mutated.
	sorted := make([]Rule, len(rules))
	copy(sorted, rules)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})

	return &evaluator{rules: sorted, cfg: cfg}
}

// Evaluate iterates rules in priority order and returns the first match.
// If no rule matches, it returns the configured default decision (ALLOW by default).
// It never returns a nil Decision — the zero-value is always populated.
func (e *evaluator) Evaluate(ctx context.Context, ac ActionContext) (Decision, error) {
	start := time.Now()

	for i := range e.rules {
		rule := &e.rules[i]

		// Structural match first (fast, in-process).
		if !matchesRule(ac, *rule) {
			continue
		}

		// If the rule has a semantic condition, evaluate it.
		// In this release the semantic evaluator is a stub: if the semantic rule
		// does not match (or is skipped), we continue to the next rule.
		if rule.Conditions.SemanticRule != "" {
			matched, err := evaluateSemantic(ctx, ac, rule.Conditions.SemanticRule, e.cfg)
			if err != nil || !matched {
				// Semantic evaluation failed or said NO — skip this rule.
				continue
			}
		}

		// Rule matched.
		id := rule.ID
		name := rule.Name
		elapsed := int(time.Since(start).Milliseconds())

		return Decision{
			Action:     rule.Action,
			PolicyID:   &id,
			PolicyName: &name,
			Reason:     fmt.Sprintf("matched rule %q (priority %d)", rule.Name, rule.Priority),
			EvalTimeMS: elapsed,
		}, nil
	}

	// No rule matched — return the configured default.
	elapsed := int(time.Since(start).Milliseconds())
	return Decision{
		Action:     e.cfg.defaultAction,
		PolicyID:   nil,
		PolicyName: nil,
		Reason:     "no matching rule; default action applied",
		EvalTimeMS: elapsed,
	}, nil
}
