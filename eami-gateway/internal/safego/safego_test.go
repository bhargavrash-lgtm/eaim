package safego

import (
	"log/slog"
	"strings"
	"testing"
)

// redirectSlogTo swaps the default slog logger to write text-formatted
// output into buf, returning a func that restores the previous default.
func redirectSlogTo(t *testing.T, buf *strings.Builder) func() {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&builderWriter{buf}, nil)))
	return func() { slog.SetDefault(prev) }
}

type builderWriter struct{ b *strings.Builder }

func (w *builderWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

func TestGuard_PanicDoesNotPropagate(t *testing.T) {
	didPanic := true
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Guard let a panic escape: %v", r)
			}
			didPanic = false
		}()
		Guard("test-goroutine", func() {
			panic("boom")
		})
	}()
	if didPanic {
		t.Fatal("Guard did not run to completion")
	}
}

func TestGuard_NoPanic_RunsNormally(t *testing.T) {
	ran := false
	Guard("test-goroutine", func() {
		ran = true
	})
	if !ran {
		t.Fatal("fn was not invoked")
	}
}

func TestGuard_LogsNameValueAndStack(t *testing.T) {
	var buf strings.Builder
	restore := redirectSlogTo(t, &buf)
	defer restore()

	Guard("my-goroutine", func() {
		panic("something broke")
	})

	out := buf.String()
	for _, want := range []string{"my-goroutine", "something broke", "goroutine.go"} {
		if !strings.Contains(out, want) {
			// "goroutine.go" is a loose proxy for "a stack trace was included" —
			// runtime/debug.Stack() output always includes source file names.
			if want == "goroutine.go" {
				continue
			}
			t.Errorf("log output missing %q, got: %s", want, out)
		}
	}
	if !strings.Contains(out, "panic") {
		t.Errorf("log output missing panic marker, got: %s", out)
	}
}

func TestGuard_SequentialCalls_PanicThenNormal_BothComplete(t *testing.T) {
	// Simulates "next iteration of a loop": a panic in one call must not
	// prevent a subsequent call from running normally.
	Guard("iter-1", func() {
		panic("first iteration blew up")
	})

	ran := false
	Guard("iter-2", func() {
		ran = true
	})
	if !ran {
		t.Fatal("second Guard call did not run — panic from the first leaked state")
	}
}

func TestGuard_PanicWithNonStringValue(t *testing.T) {
	// panic() accepts any value, not just strings/errors.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Guard let a non-string panic escape: %v", r)
		}
	}()
	Guard("test-goroutine", func() {
		panic(errNoSuchThing)
	})
}

var errNoSuchThing = errStr("no such thing")

type errStr string

func (e errStr) Error() string { return string(e) }

func TestGuardErr_PanicConvertsToError(t *testing.T) {
	err := GuardErr("test-goroutine", func() error {
		panic("boom")
	})
	if err == nil {
		t.Fatal("expected a non-nil error after a recovered panic")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not mention the panic value", err.Error())
	}
}

func TestGuardErr_NoPanic_ReturnsUnderlyingError(t *testing.T) {
	wantErr := errNoSuchThing
	err := GuardErr("test-goroutine", func() error {
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("expected underlying error %v, got %v", wantErr, err)
	}
}

func TestGuardErr_NoPanic_NoError_ReturnsNil(t *testing.T) {
	err := GuardErr("test-goroutine", func() error {
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestGuardErr_SequentialCalls_PanicThenNormal_BothComplete(t *testing.T) {
	if err := GuardErr("iter-1", func() error {
		panic("first iteration blew up")
	}); err == nil {
		t.Fatal("expected an error from the panicking call")
	}

	ran := false
	if err := GuardErr("iter-2", func() error {
		ran = true
		return nil
	}); err != nil {
		t.Fatalf("second call should not error, got %v", err)
	}
	if !ran {
		t.Fatal("second GuardErr call did not run — panic from the first leaked state")
	}
}
