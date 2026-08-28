// loader_bench_test.go -- eami-gateway/internal/policyloader
//
// B-129's performance question: does calling Loader.Evaluator() fresh on
// every dispatch (instead of the pre-fix pattern of calling it once and
// holding the resulting policy.Evaluator value) add meaningful cost?
// Evaluator() is documented as a plain atomic.Pointer.Load -- this
// benchmark verifies that claim empirically rather than assuming it,
// comparing the fresh-call pattern against a cached-value baseline that
// represents the theoretical zero-cost floor.
//
// Run:
//
//	go test ./internal/policyloader/... -bench=. -benchtime=2s -run=^$
package policyloader

import (
	"testing"

	policy "github.com/eami/policy"
)

// BenchmarkLoaderEvaluator_FreshCall is the exact marginal cost B-129's
// fix adds to every single dispatch: one Loader.Evaluator() call.
func BenchmarkLoaderEvaluator_FreshCall(b *testing.B) {
	l := New(nil) // queryRules never runs in this benchmark; only store/Evaluator are exercised
	l.store(sampleRules(50))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = l.Evaluator()
	}
}

// BenchmarkCachedEvaluator_Baseline is the pre-fix pattern's cost: using
// an already-obtained policy.Evaluator interface value directly, with no
// further indirection. This is the theoretical floor -- what the old
// (buggy) code paid per dispatch, since it never re-consulted the loader
// at all.
func BenchmarkCachedEvaluator_Baseline(b *testing.B) {
	l := New(nil)
	l.store(sampleRules(50))
	ev := l.Evaluator() // captured once, exactly like the pre-fix main.go snapshot

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ev
	}
}

// BenchmarkLoaderEvaluator_FreshCall_ThenEvaluate measures the fresh-call
// pattern in the context it actually runs in -- immediately followed by
// a real Evaluate() call against a realistic 50-rule set -- so the
// fixed per-dispatch overhead can be compared against the cost of policy
// evaluation itself, not just measured in isolation.
func BenchmarkLoaderEvaluator_FreshCall_ThenEvaluate(b *testing.B) {
	l := New(nil)
	l.store(sampleRules(50))
	ctx := b.Context()
	ac := policy.ActionContext{ToolName: "no-match-tool", ActionType: "read"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = l.Evaluator().Evaluate(ctx, ac)
	}
}

func sampleRules(n int) []policy.Rule {
	rules := make([]policy.Rule, n)
	for i := range rules {
		rules[i] = policy.Rule{
			ID:       "bench-rule",
			Priority: i + 1,
			Action:   policy.ActionAllow,
			Conditions: policy.Conditions{
				ToolNames: []string{"some-other-tool"},
			},
		}
	}
	return rules
}
