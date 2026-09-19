package nmlauncher

import (
	"testing"
	"time"
)

// TestVerifyNotReusedPID covers the PID-reuse fix's actual decision logic
// (2026-09-19, same underlying mechanism as the original B-037 incident --
// see parentproc_windows.go's doc comment) with synthetic timestamps, no
// real OS process manipulation needed.
func TestVerifyNotReusedPID(t *testing.T) {
	now := time.Now()

	t.Run("genuine parent created well before us -- allowed", func(t *testing.T) {
		parentCreation := now.Add(-10 * time.Second)
		ownCreation := now
		if err := verifyNotReusedPID(1234, parentCreation, ownCreation); err != nil {
			t.Fatalf("expected no error for a genuinely earlier parent, got: %v", err)
		}
	})

	t.Run("reused PID: same instant as our own creation -- refused", func(t *testing.T) {
		// A genuine parent cannot be created at the exact same instant as
		// the child it spawns -- CreateProcess is synchronous from the
		// parent's perspective, so the parent necessarily predates us.
		if err := verifyNotReusedPID(1234, now, now); err == nil {
			t.Fatal("expected an error when parent creation time equals our own, got nil")
		}
	})

	t.Run("reused PID: created after us -- refused", func(t *testing.T) {
		parentCreation := now.Add(5 * time.Second)
		ownCreation := now
		err := verifyNotReusedPID(5678, parentCreation, ownCreation)
		if err == nil {
			t.Fatal("expected an error when the found PID's process was created after us, got nil")
		}
		if got := err.Error(); got == "" {
			t.Fatal("error message must not be empty -- it's what a future incident would grep for")
		}
	})

	t.Run("boundary: parent created 1ns before us -- allowed", func(t *testing.T) {
		ownCreation := now
		parentCreation := now.Add(-1 * time.Nanosecond)
		if err := verifyNotReusedPID(999, parentCreation, ownCreation); err != nil {
			t.Fatalf("expected no error at the 1ns-before boundary, got: %v", err)
		}
	})
}
