package main

import (
	"context"
	"testing"
	"time"
)

// TestSafeWriteTokenUsage_PanicRecovered proves acceptance criterion #2 for
// the token-usage writer: a panic inside the fire-and-forget write doesn't
// crash the goroutine (and, by extension, the process).
func TestSafeWriteTokenUsage_PanicRecovered(t *testing.T) {
	orig := tokenUsageWriteFunc
	defer func() { tokenUsageWriteFunc = orig }()

	tokenUsageWriteFunc = func(ctx context.Context, apiBase, serviceKey string, p tokenUsagePayload) error {
		panic("simulated write failure")
	}

	done := make(chan struct{})
	go func() {
		safeWriteTokenUsage("http://eami-api", "service-key", tokenUsagePayload{AgentName: "agent-1"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("safeWriteTokenUsage did not return — panic escaped")
	}
}

// TestSafeWriteTokenUsage_NextCallStillWritesNormally proves that after one
// call panics, a subsequent call (the next dispatched event) still performs
// a normal write — the panic doesn't leave the writer disabled.
func TestSafeWriteTokenUsage_NextCallStillWritesNormally(t *testing.T) {
	orig := tokenUsageWriteFunc
	defer func() { tokenUsageWriteFunc = orig }()

	tokenUsageWriteFunc = func(ctx context.Context, apiBase, serviceKey string, p tokenUsagePayload) error {
		panic("simulated write failure")
	}
	safeWriteTokenUsage("http://eami-api", "service-key", tokenUsagePayload{})

	var gotPayload tokenUsagePayload
	called := make(chan struct{})
	tokenUsageWriteFunc = func(ctx context.Context, apiBase, serviceKey string, p tokenUsagePayload) error {
		gotPayload = p
		close(called)
		return nil
	}

	go safeWriteTokenUsage("http://eami-api", "service-key", tokenUsagePayload{AgentName: "agent-2"})

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("second call to safeWriteTokenUsage never invoked the write func")
	}
	if gotPayload.AgentName != "agent-2" {
		t.Fatalf("expected agent-2's payload to be written, got %+v", gotPayload)
	}
}

// TestSafeWriteTokenUsage_NoPanic_BehavesLikeBefore is a regression guard:
// with no panic, the wrapper is transparent to writeTokenUsage's own error
// handling (still just logged, not surfaced).
func TestSafeWriteTokenUsage_NoPanic_BehavesLikeBefore(t *testing.T) {
	orig := tokenUsageWriteFunc
	defer func() { tokenUsageWriteFunc = orig }()

	called := false
	tokenUsageWriteFunc = func(ctx context.Context, apiBase, serviceKey string, p tokenUsagePayload) error {
		called = true
		return nil
	}
	safeWriteTokenUsage("http://eami-api", "service-key", tokenUsagePayload{})
	if !called {
		t.Fatal("tokenUsageWriteFunc was not called")
	}
}
