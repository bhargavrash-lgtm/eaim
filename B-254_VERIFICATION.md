# B-254 Verification Record — "Never fires" warnings for policies with a semantic rule

Written 2026-09-28 by Claude Code, at founder direction (the revised scope). Roadmap: **Horizon 0 hardening**; the real fix is **B-007**, blocked on ADR-009. Raw logs are in session `b3cde494-55e6-4fd7-b83d-5f55f606e27f`'s scratchpad: `b254_live.log`, `b254_mutation.log`, `b254_before.txt`, `b254_after.txt` and `shots_b254/`.

## 1. Part A findings, and the premise correction
- **The form field** is in `components/policies/PolicyPanel.tsx`. **The conditions display** is `ConditionSummary` in `components/policies/PolicyBadges.tsx`. Both are shared by the org Policies page and the workspace "Our Policies" page. The code says the extraction was **B-214** (the brief said B-215).
- **The stub is unconditional:** `evaluateSemantic` returns `(false, nil)` always (`eami-policy/semantic.go:28-30`).
- **Dev DB:** 0 of 2 policies carry a semantic rule, so live verification used fixtures.
- **Correction** (founder-accepted): the original brief assumed only semantic-*only* policies never fire. `Evaluate` skips the **whole rule** whenever its semantic check says no, so **every policy with a semantic rule never fires, mixed ones included.** The wording is therefore one message for all of them.

## 2. Change (UI only; evaluation, API and saving unchanged)
- **`PolicyBadges.tsx`:** adds `SemanticNeverFiresBadge` (warning tokens `bg-status-warning`/`text-status-warning-text`, the existing badge shape) and `SEMANTIC_NEVER_FIRES_TOOLTIP`. `ConditionSummary` shows the badge whenever `conditions.semantic_rule` is set, so it appears on **both** list pages.
- **`PolicyPanel.tsx`:** an always-visible warning note under the semantic-rule field: "Not enforced yet: any policy with a semantic rule never fires, even if its other conditions match."
- **`eami-policy/policy_test.go`:** adds `TestEvaluate_MixedSemanticRuleSkippedByStub`. It is **temporary** and pins the current behaviour, with a structural control proving the ALLOW is the semantic skip. It changes deliberately when B-007 or B-258 lands.

## 3. Verification
```
eami-policy: go vet ./... ; go test -count=1 ./... → ok (policy_test.go was already gofmt-flagged at HEAD for CRLF; unchanged)
eami-ui: npx tsc --noEmit → ok
```
**Mutation check:** making the evaluator stop skipping on a semantic "no" (`b254_mutation.log`, verbatim):
```
--- FAIL: TestEvaluate_SemanticRuleSkippedByStub (0.00s)
    policy_test.go:612: got "deny", want ALLOW (semantic stub skips rule)
--- FAIL: TestEvaluate_MixedSemanticRuleSkippedByStub (0.00s)
    policy_test.go:650: got "deny" (policy 0x14d75df14470), want default ALLOW with no policy: a mixed rule is skipped entirely by the semantic stub
FAIL
FAIL	github.com/eami/policy	0.693s
FAIL
```
**Post-review fixes** (code review §6, both Mediums plus Low 6):
- The badge now carries its reason in an `sr-only` span, so it reaches keyboard, touch and screen-reader users, not only the mouse-only `title`.
- The note has `id="semantic-rule-not-enforced"`, and the textarea has `aria-describedby` pointing at it.
- The pinning test also asserts the default-decision `Reason`, so a coincidental allow-rule match can't satisfy it.
- `go test` and `tsc` were re-run and pass.

**Live verification** (the API is unchanged, so no rebuild was needed; the UI is served from the edited source by the Vite dev server). A throwaway org, an admin who is also workspace_admin, and three **draft** org-floor policies created through the real API. There were four runs, disclosed in full:
1. **Run 1**, before the review fixes (`b254_live_run1.log`): 12/12 PASS.
2. **Run 2**, after the fixes (`b254_live_run2_scriptmismatch.log`): failed on the **check script**, not the product. Its exact-text `"Never fires"` matcher no longer matched once the badge's text content included the sr-only reason. The non-semantic row was still correctly unflagged.
3. **Run 3** hit the login rate limiter (the UI login never navigated).
4. **The final run**, after the 5-minute window, with the matcher updated plus new checks that the sr-only reason is present and visually hidden and that `aria-describedby` is wired (`b254_live.log`, verbatim):
```
PASS three policies created through the real API (saving not blocked) :: 201,201,201
PASS org-policies: semantic-only shows "Never fires" ::  901 b254-semantic-only Global floor Never fires : The semantic rule isn't evaluated yet, so this whole policy is skipped -- even when its other conditions matc
PASS org-policies: mixed shows "Never fires" ::  902 b254-mixed Global floor Never fires : The semantic rule isn't evaluated yet, so this whole policy is skipped -- even when its other conditions match. tools
PASS org-policies: non-semantic is NOT flagged, conditions shown normally ::  903 b254-non-semantic Global floor tools: read_file Deny Draft 
PASS org-policies: the reason is also in the badge for screen readers, visually hidden (sr-only) :: sr=": The semantic rule isn't evaluated yet, so this whole policy is skipped -- even when its other conditions match." hidden=true
PASS org-policies: tooltip explains the whole policy is skipped :: The semantic rule isn't evaluated yet, so this whole policy is skipped -- even when its other conditions match.
PASS org-policies: form shows the not-enforced note at the semantic-rule field, linked to the textarea via aria-describedby :: note visible; textarea aria-describedby count=1; note id=semantic-rule-not-enforced
PASS workspace-our-policies: semantic-only shows "Never fires" :: 901 b254-semantic-only Global floor Never fires : The semantic rule isn't evaluated yet, so this whole policy is skipped -- even when its other conditions match
PASS workspace-our-policies: mixed shows "Never fires" :: 902 b254-mixed Global floor Never fires : The semantic rule isn't evaluated yet, so this whole policy is skipped -- even when its other conditions match. tools:
PASS workspace-our-policies: non-semantic is NOT flagged, conditions shown normally :: 903 b254-non-semantic Global floor tools: read_file Deny Draft org floor
PASS workspace-our-policies: the reason is also in the badge for screen readers, visually hidden (sr-only) :: sr=": The semantic rule isn't evaluated yet, so this whole policy is skipped -- even when its other conditions match." hidden=true
PASS workspace-our-policies: tooltip explains the whole policy is skipped :: The semantic rule isn't evaluated yet, so this whole policy is skipped -- even when its other conditions match.
PASS workspace-our-policies: form shows the not-enforced note at the semantic-rule field, linked to the textarea via aria-describedby :: note visible; textarea aria-describedby count=1; note id=semantic-rule-not-enforced
PASS no uncaught page errors :: []
ALL PASS
```
Screenshots are in `shots_b254/`: both lists and both forms. In the form, the note sits below the slide-over's initial fold, next to the field; the check confirmed it's rendered.

**Cleanup** (after every run): the throwaway org was deleted (cascading its user, group, workspace, membership, policies and conditions), along with the refresh tokens and the fixture password file. `diff b254_before.txt b254_after.txt` is **identical**, including the audit_log and policy counts. The final diff was re-taken after the last run.

**Review items recorded in NOTES.md, not fixed here (out of scope):**
- the Conditions column's inconsistent truncation (Low 3);
- the "Never fires" and "Escalate" badges share a colour (Low 4; a product call);
- the semantic-rule `<label>` has no `htmlFor` (existed before);
- a stale `semantic.go:15` comment claiming the stub returns ESCALATE.

**Noted, not in scope:** the workspace sidebar highlights "Overview" while on Our Policies. This existed before and is unrelated to this change.

## 4. End-to-end trace (deny / escalate / allow with a semantic rule)
**Evaluation path.** The gateway's `policyloader` builds `policy.NewEvaluator(rules)` with no options, so the **default decision is ALLOW** (`loader.go:81`, `evaluator.go:76`). It copies `semantic_rule` into each rule (`loader.go:137, :199`). `Evaluate` (`evaluator.go:122-166`) then does the following for each rule in order (org floor first, then workspace rules, by priority):
1. If the structural conditions don't match, the rule is skipped (`matchesRule`).
2. If they match and the rule has **any** semantic rule, `evaluateSemantic` (a stub returning `(false, nil)`) runs, and the rule is **skipped** (`:136-141`).
3. Otherwise the first matching rule wins; if none matches, the default ALLOW applies.

**Standalone MCP call** (`cmd/gateway/dispatcher.go:544-629`):
- **Deny with a semantic rule:** skipped. The next matching rule decides, or the **default ALLOW**. The audit row records the *later* rule's decision and ID (none on the default). **The deny never happens, and this policy leaves no trace.**
- **Escalate with a semantic rule:** skipped the same way. **No approval request is created;** the call proceeds under the fallback.
- **Allow with a semantic rule:** skipped. A lower-priority deny or escalate that also matches applies instead, so the intended exception doesn't exist. Otherwise the default ALLOW makes it *look* as if it worked.

**Workflow step** (`internal/workflow/executor.go:172-203`): the same evaluator is called twice. First an informational preview recorded in the run step's `ProjectedDecision` (`:182-184`), then the enforced `dispatch()` (`:203`) through the same dispatcher path. **Both skip the semantic-rule policy identically,** so the projected decision shows the fallback too.

## 5. Evaluator-error trace (founder request, read-only)
**Verdict: unreachable today.** `Evaluate` returns a `nil` error on both of its exits (`evaluator.go:155, :166`), and `evaluateSemantic` never errors. The only production `Evaluator` is `eami-policy`'s (the others are test stubs). So the dispatcher's "evaluator error → ALLOW" branch (`dispatcher.go:545-548`) is **latent**: it becomes live when B-007 makes semantic evaluation able to fail.

**Every condition type:**
- `OrgID` and `WorkspaceID`: plain comparisons, **checked first**, before any condition is parsed (`structural.go:77, :94`).
- `AgentNamePattern`: `path.Match` glob. **Its error is swallowed:** a malformed pattern makes the rule not match (`:101-105`).
- `ToolNames`, `ToolServerIDs`, `ActionTypes`, `Environments`: case-insensitive set membership.
- `RecordCountGT`: an integer comparison.
- `ScopeDrift`: a string heuristic.
- None of these can error or panic.

**Workspace-admin-writable input:** the same condition shape, through the workspace policy routes.
- **Nothing validates the glob on save** (org or workspace). A pattern like `agent-[` saves and then **silently disables its own policy**.
- Because the org and workspace checks run first and the error is per-rule, **one org's (or workspace's) malformed rule cannot affect another org's evaluation**, or any other rule.

**Adjacent fail-open:**
- At startup, if the DB policy load fails **and** the YAML fallback fails or is absent, the loader serves an **empty evaluator, so every call is allowed**. It is logged only as a Warn (`cmd/gateway/main.go:116-121`, `policyloader/loader.go:50-55`).
- A later reload failure keeps the previous rule set (safe).

Both were recorded in **B-259**. On 2026-09-29 the unvalidated glob was split out to **B-260** (reachable today); the error branch and the startup fallback stay in B-259, tied to B-007.

## 6. Code review (mandatory; quoted verbatim)
> ## B-254 review: "Never fires" badge, semantic-rule note, and pinning test
>
> **Verdict:** The change is correct and in scope. There are no High findings. There are two Medium accessibility findings, and both are small to fix.
>
> ### Is the evaluator claim right?
> Yes.
> - **The stub:** `eami-policy/semantic.go:21-31` always returns `(false, nil)`.
> - **The skip:** `eami-policy/evaluator.go:136-142` runs `continue` whenever `SemanticRule != ""` and the result is not a match. That comes after `matchesRule` has already passed at :129, so mixed rules are skipped too, not just semantic-only ones.
> - **Workspace path:** `eami-gateway/internal/policyloader/loader.go:137,198-200` loads `pc.semantic_rule` for global and workspace rows alike. NULL becomes `""` and non-NULL is copied verbatim, with no trimming. A skipped global rule just falls through to later rules, which can be workspace rules. The claim holds for both scopes.
> - **Every consumer shares this evaluator:** `dispatcher.go`, `main.go` (which also covers YAML-file rule sets via `loadPolicySet`), `internal/mcp/handler.go` and `internal/workflow/executor.go`. No other path evaluates policies.
> - **Stale comment (not in this diff):** `semantic.go:15` says the stub "always returns ESCALATE". That is wrong; the result is a skip and then the default decision. Worth a NOTES.md entry.
>
> ### Does the badge appear exactly when the policy never fires?
> Yes, in every case I traced:
>
> | Stored value | Gateway | API response | UI | Consistent? |
> |---|---|---|---|---|
> | NULL | `""`, rule evaluated | field omitted (`types.go:137` omitempty, guarded by `.Valid` in `policies.go:541` and `workspace_policies.go:356`) | falsy, no badge | yes |
> | `""` (possible via direct API: `toPgtypeTextStr`, `policies.go:576-581`, stores `""` as a valid value) | `""`, rule evaluated | `"semantic_rule":""` (omitempty keeps a non-nil pointer) | falsy, no badge | yes |
> | `"   "` (whitespace; API only, the UI trims) | non-empty, rule skipped | `"   "` | truthy, badge shown | yes |
> | real text | skipped | text | badge shown | yes |
>
> - The UI save path trims before sending: `PolicyPanel.tsx:65` sends `semanticRule.trim() || undefined`. The API writes nil as NULL, and the update path overwrites with `EXCLUDED.semantic_rule`, so clearing the field genuinely stops the skip.
> - The `'any request'` early return (`PolicyBadges.tsx:91`) can't fire when a semantic rule is set, because that rule adds to `parts`.
> - `ConditionSummary` is the only Conditions renderer on both pages (`PoliciesPage.tsx:121`, `WorkspacePoliciesPage.tsx:72`).
>
> ### Findings
>
> **Medium**
> 1. **The badge's reason is only in a `title` tooltip** (`PolicyBadges.tsx:73-78`). Keyboard and touch users can't reach a `title`, and screen readers announce it inconsistently. So in the list view the reason ("the whole policy is skipped even when other conditions match") is effectively hidden from them; only "Never fires" is conveyed. Fix: add an `sr-only` span holding `SEMANTIC_NEVER_FIRES_TOOLTIP` inside the badge (keep `title` for mouse users), or use `aria-describedby`.
> 2. **The panel note isn't linked to the textarea** (`PolicyPanel.tsx:213-220`). `role="note"` is a valid, passive landmark-less role. But a screen-reader user who tabs into the textarea won't hear the note, because focus skips non-focusable `<p>` content. Fix: give the `<p>` an `id` and set `aria-describedby` on the textarea. The textarea's `<label>` also has no `htmlFor`/`id`. That was already the case before this change and is out of scope, but it's worth logging.
>
> **Low**
> 3. **Truncation now behaves differently between rows** (`PolicyBadges.tsx:92-98`).
>    - Before this change, `truncate max-w-xs` sat on a plain inline `<span>`. Inline elements ignore `max-width`, and `overflow: hidden` has no effect on them, so the summary never actually truncated. It just widened the `whitespace-nowrap` `<td>` (`DataTable.tsx:231`).
>    - Inside the new `inline-flex` wrapper, the summary becomes a flex item, so `max-w-xs` and the ellipsis now work. They work only on semantic-rule rows.
>    - Net effect: semantic rows cap at about 20rem plus the badge (roughly 6rem plus a gap), while other rows still don't truncate. Nothing breaks, but the column is inconsistent. A proper fix (`inline-block` or `block` on the summary in both branches) is a separate concern for NOTES.md, not this change.
> 4. **The badge colour is identical to the `escalate` ActionBadge** (`PolicyBadges.tsx:16` vs. :74). A row can show two identical amber pills ("Escalate" and "Never fires") that mean different things. The badge's `font-semibold` also matches the action pill, not StatusBadge's `font-medium`. The visual weight is acceptable; just noting the ambiguity.
> 5. **The badge also shows on `draft`/`disabled` policies**, where "Never fires" is true for another reason already. It's harmless, and arguably correct because the fact survives re-activation, so no change is needed.
> 6. **The pinning test proves the skip but only asserts part of the result** (`policy_test.go:623-652`).
>    - The control is sound: the same rule with `SemanticRule=""` returns DENY, so the structural match is proven. The struct copy only changes a string field, so no slice aliasing matters.
>    - It asserts `ActionAllow` and a nil `PolicyID`, which pins "skipped entirely" rather than "matched with allow". That's good.
>    - Optional: also assert `d.Reason == "no matching rule; default action applied"`, or use a non-default fallback such as `WithDefaultDecision(ActionEscalate)`. Then the test couldn't pass through a coincidental ALLOW-action match.
>    - Correctly marked TEMPORARY, with the B-007/B-258 references.
>
> **Info**
> 7. **Design system:**
>    - §2 compliance: `bg-status-warning`/`text-status-warning-text` map to `tailwind.config.ts:73` (`#FBF1DD`/`#8A6512`), which exactly matches §2's Warning. No invented values.
>    - §8 ("status colours only for real state"): "Never fires" describes the policy's actual enforcement state right now, not decoration or wayfinding, so warning colours are defensible.
>    - One could argue for danger instead: an inert deny rule is closer to "broken", and `disabled` already uses danger. That's a product call, not a violation.
>    - The in-form note is effectively a §7.4-style honest disclosure. Its solid warning fill is fine; §7.4's dashed "Not yet available" treatment is for missing metrics, not inline warnings.
> 8. **Copy:** "Not enforced yet: any policy with a semantic rule never fires…" is accurate. The note shows even when the textarea is empty, which is intentional per its comment and the right call.
> 9. **Idiom and scope:** No changes to evaluation, the API, the OpenAPI spec or saving, as the brief says. The badge follows the local-badge convention in the file header rather than using `StatusPill`. Exporting the tooltip constant is harmless.
>
> ### Commands run
> - `git diff HEAD -- eami-ui eami-policy`
> - Read-only `grep`/`sed` over `eami-policy`, `eami-gateway/internal/policyloader/loader.go`, `eami-api/internal/api/{types,policies,workspace_policies}.go`, `api/openapi.yaml`, `eami-ui/src/**`, `eami-ui/tailwind.config.ts` and `DESIGN_SYSTEM.md` §2/§7.4/§8.
> - In `eami-policy`: `go vet ./...` was clean. `go test ./...` passed. `go test -run Semantic -v ./...` passed, including `TestEvaluate_MixedSemanticRuleSkippedByStub`. `gofmt -d policy_test.go` flagged every line, most likely because of CRLF line endings, so it doesn't say anything about the new code; the new hunk is visually gofmt-aligned.
> - In `eami-ui`: `npx tsc --noEmit` exited 0.
> - I did not modify any files.
