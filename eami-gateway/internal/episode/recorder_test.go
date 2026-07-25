package episode

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeEpisodeWriteDB lets tests control exactly what Record's DB write does
// — including deliberately panicking — without a real Postgres connection.
type fakeEpisodeWriteDB struct {
	execFn func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (f *fakeEpisodeWriteDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return f.execFn(ctx, sql, args...)
}

func sampleSteps() []Step {
	return []Step{{
		ToolName:  "github",
		Action:    "create_pr",
		Decision:  "allowed",
		Timestamp: time.Now(),
	}}
}

// TestRecord_PanicDuringDBWrite_DoesNotCrash proves acceptance criterion #2
// for the episode recorder: a panic writing one episode doesn't propagate
// out of Record (and, by extension, out of the goroutine it's always
// launched from at its call sites).
func TestRecord_PanicDuringDBWrite_DoesNotCrash(t *testing.T) {
	db := &fakeEpisodeWriteDB{
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			panic("simulated db panic")
		},
	}
	r := &Recorder{db: db}

	done := make(chan struct{})
	go func() {
		r.Record(context.Background(), uuid.New().String(), uuid.New().String(), "agent-1", sampleSteps(), "success")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Record did not return — panic escaped")
	}
}

// TestRecord_NextCallStillWritesNormally proves that after one call panics,
// a subsequent Record call (the next event) still performs a normal write —
// the panic doesn't leave the recorder disabled.
func TestRecord_NextCallStillWritesNormally(t *testing.T) {
	db := &fakeEpisodeWriteDB{
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			panic("simulated db panic")
		},
	}
	r := &Recorder{db: db}
	r.Record(context.Background(), uuid.New().String(), uuid.New().String(), "agent-1", sampleSteps(), "success")

	called := make(chan struct{})
	db.execFn = func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
		close(called)
		return pgconn.CommandTag{}, nil
	}

	go r.Record(context.Background(), uuid.New().String(), uuid.New().String(), "agent-2", sampleSteps(), "success")

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("second Record call never reached the DB write — recorder left in a broken state")
	}
}

// TestRecord_NoPanic_BehavesLikeBefore is a regression guard: with no panic,
// the wrapper is transparent — the write happens and DB errors are still
// only logged, never returned/panicked.
func TestRecord_NoPanic_BehavesLikeBefore(t *testing.T) {
	called := false
	db := &fakeEpisodeWriteDB{
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			called = true
			return pgconn.CommandTag{}, nil
		},
	}
	r := &Recorder{db: db}
	r.Record(context.Background(), uuid.New().String(), uuid.New().String(), "agent-1", sampleSteps(), "success")
	if !called {
		t.Fatal("expected the DB write to be invoked")
	}
}

// TestRecord_EmptySteps_NoDBCall is a regression guard for the pre-existing
// early return on an empty steps slice — unaffected by the recovery wrapper.
func TestRecord_EmptySteps_NoDBCall(t *testing.T) {
	db := &fakeEpisodeWriteDB{
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			t.Fatal("Exec should not be called for an empty steps slice")
			return pgconn.CommandTag{}, nil
		},
	}
	r := &Recorder{db: db}
	r.Record(context.Background(), uuid.New().String(), uuid.New().String(), "agent-1", nil, "success")
}
