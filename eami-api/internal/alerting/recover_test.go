package alerting

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestGuard_PanicDoesNotPropagate(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("guard let a panic escape: %v", r)
		}
	}()
	guard("test-goroutine", func() {
		panic("boom")
	})
}

func TestGuard_NoPanic_RunsNormally(t *testing.T) {
	ran := false
	guard("test-goroutine", func() {
		ran = true
	})
	if !ran {
		t.Fatal("fn was not invoked")
	}
}

func TestGuard_SequentialCalls_PanicThenNormal_BothComplete(t *testing.T) {
	guard("iter-1", func() {
		panic("first iteration blew up")
	})

	ran := false
	guard("iter-2", func() {
		ran = true
	})
	if !ran {
		t.Fatal("second guard call did not run — panic from the first leaked state")
	}
}

func TestGuard_LogsNameAndPanicValue(t *testing.T) {
	var buf bytes.Buffer
	prevFlags := log.Flags()
	prevOut := log.Writer()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()

	guard("alerting-rule-xyz", func() {
		panic("evaluation exploded")
	})

	out := buf.String()
	for _, want := range []string{"alerting-rule-xyz", "evaluation exploded"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q, got: %s", want, out)
		}
	}
}
