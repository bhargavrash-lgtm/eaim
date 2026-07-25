package alerting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/eami/api/internal/store"
)

// fakeDBTX lets tests control exactly what the engine's DB calls do —
// including deliberately panicking — without a real Postgres connection.
type fakeDBTX struct {
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (f *fakeDBTX) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (f *fakeDBTX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if f.queryFn != nil {
		return f.queryFn(ctx, sql, args...)
	}
	return &emptyRows{}, nil
}

func (f *fakeDBTX) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if f.queryRowFn != nil {
		return f.queryRowFn(ctx, sql, args...)
	}
	return &zeroRow{}
}

// zeroRow is a no-op pgx.Row; Scan leaves destinations at their zero value.
type zeroRow struct{}

func (r *zeroRow) Scan(dest ...any) error { return nil }

// emptyRows is a no-op pgx.Rows reporting zero rows.
type emptyRows struct{}

func (r *emptyRows) Close()                                       {}
func (r *emptyRows) Err() error                                   { return nil }
func (r *emptyRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *emptyRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *emptyRows) Next() bool                                   { return false }
func (r *emptyRows) Scan(dest ...any) error                       { return errors.New("no rows") }
func (r *emptyRows) Values() ([]any, error)                       { return nil, errors.New("no rows") }
func (r *emptyRows) RawValues() [][]byte                          { return nil }
func (r *emptyRows) Conn() *pgx.Conn                               { return nil }

func denyRule(id uuid.UUID) store.AlertRule {
	return store.AlertRule{
		ID:              id,
		OrgID:           uuid.New(),
		Name:            "rule-" + id.String(),
		ConditionConfig: []byte(`{"metric":"denied_actions_count","condition":"gt","threshold":999999,"window_minutes":5}`),
		Severity:        "low",
		Enabled:         true,
	}
}

// TestEvaluateRules_PanicInOneRule_OthersStillEvaluate proves acceptance
// criterion #1: a panic evaluating one rule doesn't stop the remaining
// rules in the same tick from being evaluated.
func TestEvaluateRules_PanicInOneRule_OthersStillEvaluate(t *testing.T) {
	var queryRowCalls int
	db := &fakeDBTX{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			queryRowCalls++
			if queryRowCalls == 1 {
				panic("simulated db panic evaluating rule 1")
			}
			return &zeroRow{}
		},
	}
	e := NewEngine(store.New(db), "", "")

	rules := []store.AlertRule{denyRule(uuid.New()), denyRule(uuid.New())}

	done := make(chan struct{})
	go func() {
		e.evaluateRules(context.Background(), rules)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("evaluateRules did not return — panic escaped the loop")
	}

	if queryRowCalls != 2 {
		t.Fatalf("expected both rules to reach their metric query (2 QueryRow calls), got %d — "+
			"the second rule was not evaluated after the first panicked", queryRowCalls)
	}
}

// TestEvaluateRules_NoPanic_BothRulesEvaluateNormally is the control case:
// confirms evaluateRules doesn't change behavior when nothing panics.
func TestEvaluateRules_NoPanic_BothRulesEvaluateNormally(t *testing.T) {
	var queryRowCalls int
	db := &fakeDBTX{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			queryRowCalls++
			return &zeroRow{}
		},
	}
	e := NewEngine(store.New(db), "", "")

	rules := []store.AlertRule{denyRule(uuid.New()), denyRule(uuid.New())}
	e.evaluateRules(context.Background(), rules)

	if queryRowCalls != 2 {
		t.Fatalf("expected 2 QueryRow calls, got %d", queryRowCalls)
	}
}

// TestSafeTick_PanicListingRules_DoesNotCrash_AndNextTickWorks proves the
// outer, tick-level recovery: a panic anywhere in tick (here, listing rules,
// before the per-rule loop even starts) doesn't crash the engine, and the
// next tick still runs normally.
func TestSafeTick_PanicListingRules_DoesNotCrash_AndNextTickWorks(t *testing.T) {
	var queryCalls int
	db := &fakeDBTX{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			queryCalls++
			if queryCalls == 1 {
				panic("simulated panic listing rules")
			}
			return &emptyRows{}, nil
		},
	}
	e := NewEngine(store.New(db), "", "")

	done := make(chan struct{})
	go func() {
		e.safeTick(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("safeTick did not return — panic escaped")
	}

	done2 := make(chan struct{})
	go func() {
		e.safeTick(context.Background())
		close(done2)
	}()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("second safeTick did not return normally after the first panicked")
	}

	if queryCalls != 2 {
		t.Fatalf("expected 2 Query calls (one per tick), got %d", queryCalls)
	}
}
