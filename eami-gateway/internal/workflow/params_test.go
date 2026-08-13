// params_test.go -- eami-gateway/internal/workflow
//
// Pure unit tests for resolveParams (B-063): no Postgres needed, every
// input is a plain Go value. Covers AC3 (bad path fails cleanly), AC4
// (a reference outside priorResults -- covering both "future step" and
// "cross-run/cross-workflow" cases, which manifest identically: absence
// from the map), AC5 (static-only unaffected), AC6 (precedence).
package workflow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestResolveParams_StaticOnly_Unaffected(t *testing.T) {
	step := Step{Params: map[string]any{"foo": "bar", "n": float64(3)}}
	got, err := resolveParams(step, nil)
	if err != nil {
		t.Fatalf("resolveParams: %v", err)
	}
	if got["foo"] != "bar" || got["n"] != float64(3) {
		t.Errorf("got = %+v, want static params passed through unchanged", got)
	}
}

func TestResolveParams_Extraction_ResolvesNestedPath(t *testing.T) {
	priorID := uuid.New()
	prior := StepResult{
		WorkflowStepID: priorID,
		Outcome:        "allowed",
		Result:         json.RawMessage(`{"contact":{"id":"c-123","tags":["vip","new"]}}`),
	}
	step := Step{
		Params: map[string]any{"note": "static"},
		InputMapping: map[string]ExtractionRef{
			"contact_id": {FromStep: priorID, Path: "contact.id"},
			"first_tag":  {FromStep: priorID, Path: "contact.tags.0"},
		},
	}
	got, err := resolveParams(step, map[uuid.UUID]StepResult{priorID: prior})
	if err != nil {
		t.Fatalf("resolveParams: %v", err)
	}
	if got["contact_id"] != "c-123" {
		t.Errorf("contact_id = %v, want c-123", got["contact_id"])
	}
	if got["first_tag"] != "vip" {
		t.Errorf("first_tag = %v, want vip", got["first_tag"])
	}
	if got["note"] != "static" {
		t.Errorf("note = %v, want static param preserved alongside extraction", got["note"])
	}
}

// TestResolveParams_ReferenceNotInPriorResults_FailsCleanly proves AC4:
// a from_step that isn't in priorResults -- whether because it belongs
// to a different workflow entirely, a different run, or hasn't executed
// yet in this run -- is indistinguishable at this layer and fails the
// same clean way in every case, never silently substituting nothing.
func TestResolveParams_ReferenceNotInPriorResults_FailsCleanly(t *testing.T) {
	someOtherStepID := uuid.New() // never present in priorResults below
	step := Step{
		InputMapping: map[string]ExtractionRef{
			"x": {FromStep: someOtherStepID, Path: "a.b"},
		},
	}
	_, err := resolveParams(step, map[uuid.UUID]StepResult{})
	if err == nil {
		t.Fatal("expected an error for a step reference absent from priorResults, got nil")
	}
	if !strings.Contains(err.Error(), "has not executed in this run") {
		t.Errorf("error = %q, want it to name the unresolved-reference reason", err.Error())
	}
}

func TestResolveParams_PriorStepNotAllowed_FailsCleanly(t *testing.T) {
	priorID := uuid.New()
	prior := StepResult{WorkflowStepID: priorID, Outcome: "blocked", Result: json.RawMessage(`{"a":1}`)}
	step := Step{InputMapping: map[string]ExtractionRef{"x": {FromStep: priorID, Path: "a"}}}
	_, err := resolveParams(step, map[uuid.UUID]StepResult{priorID: prior})
	if err == nil {
		t.Fatal("expected an error extracting from a non-allowed prior step, got nil")
	}
	if !strings.Contains(err.Error(), "did not produce a usable result") {
		t.Errorf("error = %q, want it to explain the prior step's outcome", err.Error())
	}
}

// TestResolveParams_PathNotFound_FailsCleanly proves AC3 directly.
func TestResolveParams_PathNotFound_FailsCleanly(t *testing.T) {
	priorID := uuid.New()
	prior := StepResult{WorkflowStepID: priorID, Outcome: "allowed", Result: json.RawMessage(`{"contact":{"id":"c-123"}}`)}
	step := Step{InputMapping: map[string]ExtractionRef{"missing": {FromStep: priorID, Path: "contact.email"}}}
	_, err := resolveParams(step, map[uuid.UUID]StepResult{priorID: prior})
	if err == nil {
		t.Fatal("expected an error for a path that doesn't exist in the prior result, got nil")
	}
	if !strings.Contains(err.Error(), `path "contact.email" not found`) {
		t.Errorf("error = %q, want it to name the missing path", err.Error())
	}
}

// TestResolveParams_Precedence_ExtractionWinsOverStatic proves AC6: the
// documented precedence rule (params.go's own doc comment) actually
// holds when a key is configured both ways.
func TestResolveParams_Precedence_ExtractionWinsOverStatic(t *testing.T) {
	priorID := uuid.New()
	prior := StepResult{WorkflowStepID: priorID, Outcome: "allowed", Result: json.RawMessage(`{"id":"real-extracted-value"}`)}
	step := Step{
		Params:       map[string]any{"target_id": "stale-static-value"},
		InputMapping: map[string]ExtractionRef{"target_id": {FromStep: priorID, Path: "id"}},
	}
	got, err := resolveParams(step, map[uuid.UUID]StepResult{priorID: prior})
	if err != nil {
		t.Fatalf("resolveParams: %v", err)
	}
	if got["target_id"] != "real-extracted-value" {
		t.Errorf("target_id = %v, want the extracted value to win over the static one", got["target_id"])
	}
}

// TestResolveParams_NoInputMapping_ReturnsCopyNotAlias guards against a
// future refactor accidentally returning step.Params itself (a caller
// mutating the returned map should never mutate the step definition).
func TestResolveParams_NoInputMapping_ReturnsCopyNotAlias(t *testing.T) {
	step := Step{Params: map[string]any{"k": "v"}}
	got, err := resolveParams(step, nil)
	if err != nil {
		t.Fatalf("resolveParams: %v", err)
	}
	got["k"] = "mutated"
	if step.Params["k"] != "v" {
		t.Error("resolveParams returned an alias of step.Params instead of a copy")
	}
}
