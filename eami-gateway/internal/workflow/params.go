package workflow

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// resolveParams (B-063) builds one step's real dispatch parameters by
// merging its static params (workflow_step_params, B-059) with any
// extracted values (workflow_steps.input_mapping, B-063). priorResults
// holds every step that has ALREADY completed in THIS SAME run, keyed by
// workflow_steps.id -- built incrementally by Executor.Run as steps
// finish, never re-read from the database. This is the whole isolation
// mechanism for AC4 (cross-run/cross-workflow/future-step references
// can never resolve): there is no code path here that queries Postgres,
// so a reference to a step outside this exact run's own map simply
// isn't found -- structurally impossible to leak another run's or
// another workflow's data, not just a runtime check that happens to
// reject it.
//
// A step is referenced by its own stable workflow_steps.id, never by
// step_order: step_order is a mutable presentation detail (B-060's
// reorder UI changes it freely), and resolving by position would let a
// reorder silently redirect an extraction to a different step's data --
// exactly the misdirection AC2 exists to prevent. Referencing by ID also
// means a step may extract from ANY prior step, not only the one
// immediately before it -- already compatible with a future branching
// model (multiple possible ancestors), per CONTEXT.md's Thread B
// extensibility principle, not a linear-only assumption.
//
// Precedence, deliberate and documented: when a key is configured both
// as a static param AND an extraction, the EXTRACTED value wins -- an
// admin who wired up an extraction for a param did so deliberately and
// more specifically; a lingering static value for the same key is more
// likely stale leftover than an intended override.
//
// Any single unresolvable extraction fails the WHOLE step -- never a
// partial merge where some keys silently stay static/missing while
// others resolve. The caller must not dispatch on a partial result.
func resolveParams(step Step, priorResults map[uuid.UUID]StepResult) (map[string]any, error) {
	merged := make(map[string]any, len(step.Params)+len(step.InputMapping))
	for k, v := range step.Params {
		merged[k] = v
	}
	for key, ref := range step.InputMapping {
		prior, ok := priorResults[ref.FromStep]
		if !ok {
			return nil, fmt.Errorf("extraction for param %q: referenced step %s has not executed in this run (wrong workflow, wrong run, or a not-yet-executed step)", key, ref.FromStep)
		}
		if prior.Outcome != "allowed" || len(prior.Result) == 0 {
			// Defensive, not currently reachable: Run() already stops the
			// whole run at the first non-allowed step, so no later step
			// can ever observe a non-"allowed" prior result. Guarded
			// anyway, same precedent as resolveDynamicTool's empty-org
			// check in cmd/gateway/main.go -- never trust an invariant
			// silently, even one enforced elsewhere today.
			return nil, fmt.Errorf("extraction for param %q: referenced step %s did not produce a usable result (outcome=%q)", key, ref.FromStep, prior.Outcome)
		}
		val := gjson.GetBytes(prior.Result, ref.Path)
		if !val.Exists() {
			return nil, fmt.Errorf("extraction for param %q: path %q not found in step %s's result", key, ref.Path, ref.FromStep)
		}
		merged[key] = val.Value()
	}
	return merged, nil
}
