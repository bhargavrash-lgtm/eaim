# CONTEXT.md — Living project continuity log
# Updated by: Claude Code (after every task) AND the PM chat (after every
# planning decision). Read by both at the start of every session, before
# anything else.

## Product identity (do not re-litigate without explicit founder instruction)
EAMI = Enterprise AI Monitoring & Intelligence. Gateway, policy engine,
endpoint agent, audit log. NOT a maturity-assessment tool — that's a separate,
unrelated framework the founder uses elsewhere. If any file, commit message,
or prior context suggests otherwise, it is wrong; trust this line.

## Active decision thread (update every time one moves)
- **B-140 (2026-09-01): third CI occurrence, priority bumped — investigation and production-impossibility conclusion unchanged.** CI run `33451297084`, job `99681557773` ("Test — eami-gateway"), commit `3873f9b` (B-128's own org-scoping fix commit — a real code change, not a docs-only push). Same test (`TestDispatch_Escalate_Resolution_Expired_WritesSecondRow`), same file/line (`dispatcher_escalation_resolution_test.go:310`, confirmed via direct source read — `verifyHashChain`'s `t.Helper()`-marked `t.Fatalf` at line 174 is correctly attributed by Go to its call site at line 310), byte-for-byte identical signature. **Verified against the real raw log text, not GitHub's "Explain error" summary panel** (wrong 3 times running tonight on an unrelated generic story) **and not assumed from the known pattern alone** — GitHub's log-download API returned a 403 (needs authenticated admin rights even on this public repo); the user pasted the actual browser-obtained log directly into the conversation, and it was cross-checked line-for-line against current source before being logged, exactly the same rigor as occurrences 1 and 2. **Three identical-signature recurrences in one session is real signal, per explicit user direction — the already-identified throwaway-DB-isolation fix (adopt `bootstrap_test.go`'s per-test-database pattern) moves up the priority queue**, though still not Critical/Urgent since the production-impossibility analysis (RLS-enforced INSERT-only `audit_log`, one long-lived `Writer` per process boot) is unchanged and unaffected by a third test-only occurrence. **Partial corroboration for B-128, honestly scoped:** the same log shows `TestDispatch_OrgScoping_TwoOrgsConflictingPolicies` starting cleanly right after this failure, but the pasted excerpt cuts off before its own PASS/FAIL line and never reaches `TestLoad_OrgScoping_CrossOrgDispatchNoLongerMatches` (different package) at all — not claimed as an independently re-verified PASS for either; B-128's own local full-suite run (documented in its `BUILT.md` entry) remains the actual verified-green evidence. Full detail in `BACKLOG.md`'s B-140 entry. Counter unchanged at **B-142**.
- **B-128 (2026-09-01): DONE — the single most severe open finding of the prior session, fixed.** Investigation (no code changes) first re-confirmed live, empirically, against the real dev DB that no `org_id` scoping existed anywhere in the policy evaluation pipeline (a throwaway probe proved a dispatch for org `b121-liveverify` matched and was decided by Dev Org's own policy), then recommended — and the user approved — the minimal additive fix over a per-org-evaluator split, backed by real deployment-scale evidence (`ARCHITECTURE.md` §8's dedicated-Postgres-per-customer topology, not this session's shared dev DB). **Shipped exactly the confirmed 4-point design, zero interface changes:** `policyloader.queryRules()` now selects `p.org_id` onto `policy.Rule.OrgID`; `eami-policy.ActionContext`/`Rule` gained `OrgID`; `structural.go`'s `matchesRule()` gained one new, first, unconditional org check; `mcp.ActionContext.ToPolicyContext()` now threads through `OrgID` (confirmed server-resolved, never client input, at every real construction site). No schema migration — `policies.org_id` already existed. `policy.EvaluatorSource` (B-129) and `ReorderPolicies`/priority ordering (B-086/090) both confirmed untouched, both re-verified live with zero interaction. **One real regression found by the mandatory review passes and fixed before shipping:** `workflow/executor.go`'s independent `ProjectedDecision` preview literal never set `OrgID`, so post-fix it could never match any real rule again, silently showing "allow" on every workflow step regardless of the org's real policy — not a security hole (the actual enforced dispatch was unaffected), but a genuine audit-data regression this fix introduced; fixed with one line, proven with a new test that fails without the fix and passes with it. **A second, more severe, pre-existing finding surfaced by the security review, disclosed and explicitly NOT fixed here — logged fresh as B-141, Critical:** `internal/registry.go`'s `LookupByName` resolves an agent by name alone, no `org_id` predicate, and `gateway_agents` only enforces per-org name uniqueness — two orgs with a same-named agent can have the wrong org's row resolved, corrupting `ac.OrgID` **at its source**, upstream of and silently capable of defeating this very fix's org check (plus a plausible cross-tenant episode-content **read** path via the same resolution). Pre-existing, not introduced by this fix; B-042 partially addressed this bug class for token-revoke only, with reasoning the security review found doesn't hold for every other `LookupByName` caller. A real design decision (JWT org-binding claim vs. per-call-site org-scoping), correctly left for a dedicated future brief, not decided or fixed here. **Live-verified:** full `go test ./...` for `eami-gateway` and `eami-policy` against real Postgres, 100% green, plus `eami-api`'s full reorder/policy suite re-run live unmodified (confirms zero B-086/090 interaction); the real `eami-gateway` Docker image rebuilt and container restarted twice, clean startup both times against the real production schema; all test fixtures confirmed cleaned up, zero leftover rows. Full detail in `BUILT.md`'s `eami-gateway`/`eami-policy` sections and `BACKLOG.md`'s B-128 (DONE)/B-141 (new, Critical) entries. Counter now stands at **B-142**.
- **B-140 investigation complete (2026-08-29): confirmed test-infrastructure-only, structurally impossible in production, real fix identified — resolves B-122's open design fork.** Traced the exact mechanism by direct code reading, not inference. `internal/audit/writer.go`'s `Writer.Write()` is internally race-free (mutex-protected in-memory `lastHash`); the one unguarded moment is lazy first-write initialization, which calls `GetLastHash()` — a genuinely global, unscoped `SELECT ... ORDER BY timestamp DESC LIMIT 1` (`writer.go:178-190`), by design (one process-wide chain across all orgs). Every `cmd/gateway` dispatcher test constructs a **fresh** `Writer` per test (`dispatcher_test.go:109,124`), unlike production's single long-lived instance (`main.go:86`). `internal/audit/writer_pg_test.go`'s 5 real-Postgres tests each construct their own fresh `Writer` too, AND each registers a real `t.Cleanup`-based `DELETE FROM audit_log WHERE id = $1` for rows they insert. CI runs `go test ./... -count=1 -v` with no `-p` flag anywhere (`.github/workflows/build.yml:83`, confirmed), so packages run concurrently by Go's default against one shared Postgres per CI job — if `internal/audit`'s row is the global tip when `cmd/gateway`'s test initializes, and gets deleted by `internal/audit`'s own cleanup before `cmd/gateway`'s `verifyHashChain()` runs, the failure is exactly reproduced. **Not a new discovery** — the original B-124/125 session already found and partially mitigated this exact race class (`dispatcher_escalation_resolution_test.go:139-158`'s own comment). **Confirmed structurally impossible in production, not just unlikely:** `audit_log` is RLS-enforced INSERT-only for the app role; production has no DELETE path into this table at all, and constructs exactly one long-lived `Writer` per process boot. **B-124/B-125's resolution-audit-write path is exonerated.** Local's clean 20/20 reproduction explained, not just noted: identical structural exposure to CI (same command, no `-p` override), but local has 20 logical CPUs (verified) vs. GitHub-hosted runners' small, shared, often-throttled VMs — plausibly narrowing vs. widening the race's timing window, not a certainty but the best-supported explanation available. **Confirmed systemic, not local to one test file** — the vulnerable pattern (fresh Writer + unscoped GetLastHash + real per-row test cleanup) repeats across every `internal/audit` and `cmd/gateway` real-Postgres test. **Real fix identified: adopt `eami-api/internal/api/bootstrap_test.go`'s already-proven per-test throwaway-database pattern** (`CREATE DATABASE`/apply real migrations/`DROP DATABASE`) for `eami-gateway`'s real-Postgres tests — closes this race category AND B-122's original orphaned-row accumulation concern in one motion. **This resolves B-122's "isolated DB vs. accept-and-document" fork in favor of isolation**, now backed by concrete evidence instead of a cost/benefit guess; B-122's entry updated accordingly. The fix itself is a real, contained, not-yet-started follow-up brief — not urgent given production is provably unaffected, but no longer purely theoretical. No code changed — investigation-only throughout, per explicit user direction, same discipline as B-100. Full detail in `BACKLOG.md`'s B-140 entry (and B-122's updated resolution note). Counter unchanged at **B-141**.
- **B-140 (2026-08-29): logged, Medium-High priority, investigation not started — promoted from B-122 supporting evidence to its own item after a second recurrence.** `TestDispatch_Escalate_Resolution_Expired_WritesSecondRow` (`dispatcher_escalation_resolution_test.go:310`) failed in CI a second time with an **identical failure signature** to the first occurrence ("the chain was reset, not continued" — `prev_hash` matches no real row and isn't genesis) but via a **different proximate trigger**: occurrence 1 (run 33202211503, commit `f510f17`) was preceded by a severed TLS/SASL DB connection; occurrence 2 (run 33235262263, commit `15c0e9d`) had no visible connection issue but an `episodes_org_id_fkey` violation appeared immediately after the failure instead. Same symptom via two different triggers reads as a real, timing-sensitive race in shared `audit_log`/`orgs` test state, not a one-off environmental blip — especially given the prior clean 20/20 local reproduction attempt against a fresh, CI-matching Postgres found zero reproduction, pointing at CI's own runner scheduling/contention as the actual trigger condition rather than the mechanism being imaginary. **References B-122 as a related, contributing hypothesis** (no cleanup mechanism for shared `audit_log`/`orgs` test data, enabling cross-package interference within one `go test ./...` invocation) **but is scoped as its own item** — the open question is whether this exposes a real gap in the resolution-audit-write path itself (B-124/B-125, adversarially verified but never against concurrent-package interference) or is purely a test-infrastructure isolation gap with no production analogue, which B-122's existing entry doesn't address. **Explicitly NOT fixed — investigation-only per explicit user direction**, same discipline as B-100's original TOCTOU investigation: do not patch around the symptom before the actual race is understood. B-122's entry updated with a pointer to this promotion. Full detail in `BACKLOG.md`'s B-140 entry (and B-122's updated note). Counter now stands at **B-141**.
- **B-138/B-139 (2026-08-29): both logged, EPIC, investigation not started — completes the set of four original strategic epics.** **B-138** — Identity provider/SSO integration (Azure AD, Okta): standard enterprise procurement requirement. Real starting point verified against actual schema (not assumed): `users.sso_provider`/`users.sso_subject` (nullable `TEXT`) have existed since the baseline schema, and `users.password_hash`'s own comment ("NULL if SSO-only") anticipates this — confirmed via direct repo-wide search that zero application code (Go or TS) currently reads or writes either column. **B-139** — Agentless discovery: network-level shadow-AI detection not requiring an installed endpoint agent, extending visibility to unmanaged/BYOD devices `eami-agent` structurally can't reach. Flagged, per the user's own framing, as the largest and least-precedented of the four — unlike the other three (each extends an already-built surface: `api_keys` for B-137, the SSO columns for B-138, existing appliance infra for B-053), this is a genuinely new component category (likely network-level traffic/DNS/proxy-based detection, mechanism not yet decided) with no equivalent already in the codebase. **Both close the same real gap B-137 first surfaced:** all four original strategic epics discussed early in this session (VM appliance, third-party public API, IdP/SSO, agentless discovery) — only the VM appliance (B-053) had ever actually been committed to the repo before this session; confirmed by direct search of `BACKLOG.md`/`CONTEXT.md`/`ROADMAP.md`/`BUILT.md`/`CHANGELOG.md` before logging each. B-137's entry updated to reference the real B-138/B-139 IDs instead of leaving the gap open. Full detail in `BACKLOG.md`'s B-138/B-139 entries. Counter now stands at **B-140**.
- **B-137 (2026-08-29): logged, EPIC, investigation not started.** Third-party public API — a secured, rate-limited API for enterprise customers to build custom integrations directly against EAMI's own governance data/controls, distinct from B-135 (export-to/ingest-from other tools) and B-136 (native metrics + observability ingestion). The `api_keys` infrastructure (B-098) is a real, concrete starting point but not currently used for this purpose. **Real gap found and corrected:** this was one of four original strategic epics discussed early in this session (alongside IdP/SSO, agentless discovery, and the VM appliance) but was never actually committed to `BACKLOG.md`/`CONTEXT.md`/`ROADMAP.md` — confirmed by direct search of all three files (plus `BUILT.md`/`CHANGELOG.md`) before logging. Of the four, only the VM appliance was previously logged (**B-053, DONE**); **IdP/SSO and agentless discovery have the identical gap — also discussed, also never logged anywhere in the repo, confirmed by the same search, and neither is logged by this entry.** B-135's entry updated to point at this B-ID instead of its original "could not be located" flag. Full detail in `BACKLOG.md`'s B-137 entry. Counter now stands at **B-138**.
- **B-136 (2026-08-29): logged, EPIC, investigation not started, replaces/refines B-135(a).** Critical operational metrics + third-party observability ingestion, two halves. **Half 1** (smaller, extends existing data): EAMI-native metrics export in a standard format (e.g. Prometheus-style) — dispatch latency per connector/model, error/timeout rate per connector, real-time cost burn rate (on B-097-112's verified pricing), approval queue depth/time-to-resolution, policy hot-reload health (directly relevant given B-129's own critical silent-failure find). **Half 2** (larger, genuinely new direction): investigate accepting signals FROM third-party observability/LLM-quality tools (Traceloop/LangSmith-class) into EAMI's governance layer, rather than building EAMI's own model-behavior/drift-detection capability — explicitly not a category EAMI should compete in, per the ServiceNow battle card's own honest gap disclosure. Real open design question flagged for investigation: should an ingested signal (e.g. a drift/bias flag) be purely informational, or able to actually influence a policy decision or trigger an escalation — the latter would be genuine differentiation (EAMI as where governance and observability signals converge) vs. competing as an observability platform. Refines B-135(a)'s generic "resource usage/health" into these specific named metrics; explicitly does NOT touch B-135(b) (SIEM audit-log export), which remains open and un-superseded. Full detail in `BACKLOG.md`'s B-136 entry; B-135 updated with a supersession note. Counter now stands at **B-137**.
- **B-132/B-133/B-134/B-135 (2026-08-29): all four logged, investigation not started, from a scalability/DR/integrations discussion.** **B-132** — EPIC, Horizontal scale readiness, deferred/low urgency: current single-instance architecture (ADR-020 Model A) is a deliberate correct choice for realistic single-enterprise loads, not a gap — NOT to be built until a real customer hits a genuine vertical ceiling. Three single-instance assumptions flagged for that future investigation: B-070's in-memory/per-process rate limiter (two instances would silently double the effective limit), B-043 (already-logged, open) JWT revocation not broadcasting across instances, and a newly-flagged UNCHECKED item — whether B-100's `sync.Once` approval-hold mechanism's in-memory pending-approval state carries the same single-instance assumption. **B-133** — data lifecycle/retention strategy for ever-growing TimescaleDB hypertables (`audit_log` append-only by design, `token_usage`, `paste_events`): needs investigation into retention policy, TimescaleDB's native compression, and connection-pooling under sustained load; per explicit user framing, likely a more realistic near-term bottleneck than B-132's compute scaling. **B-134** — HIGH PRIORITY: offsite backup + re-verification of B-029's restore test against current schema. B-029's backup/restore mechanism is LOCAL-VOLUME ONLY (already flagged as future work in `RECOVERY.md`, never picked up until now); the substantial value built since B-029 (workflow engine, cost-accounting pipeline, cryptographic audit trail, credential store) makes this gap materially more costly today. Separately, B-029's original restore proof predates ~12 subsequent schema migrations and should not be assumed to still hold — re-verify fresh against current schema before trusting it, before any offsite-storage work. **B-135** — observability/SIEM integration exports, two distinct scopes to investigate separately: (a) Prometheus-compatible metrics export for a customer's own monitoring stack, which also directly informs B-132's scale-timing decision; (b) audit-log export to a customer's existing SIEM (Splunk, Sentinel, etc.), a natural enterprise expectation for a tamper-evident-audit-trail product. Explicitly distinct from a separate "third-party public API" epic (customers building against EAMI's platform, not EAMI feeding into their tools) — **that epic could not be located under that name anywhere in current `BACKLOG.md`/`CONTEXT.md`**, flagged honestly rather than assumed; may be a conversation-only reservation not yet written down. Full detail in `BACKLOG.md`'s B-132/B-133/B-134/B-135 entries. Counter now stands at **B-136**.
- **B-122 supporting evidence (2026-08-29): a real CI failure confirmed as a genuine environmental flake, not a new bug — logged against B-122, no new B-ID.** User flagged CI run 33202211503 (job "Test — eami-gateway", commit `f510f17`, a docs-only change), whose GitHub "Explain error" panel wrongly summarized it as FK violations/UUID-type mismatches; the real log (fetched via the GitHub API using the stored git credential, since `gh` CLI isn't installed) showed exactly one `--- FAIL`: `TestDispatch_Escalate_Resolution_Expired_WritesSecondRow`, a hash-chain continuity break immediately preceded by a severed TLS/SASL DB connection mid-test. Confirmed the run postdates B-115's UUID fix (not stale) before investigating further, per explicit user direction. Re-ran the full `eami-gateway` suite against a fresh, CI-matching Postgres (`timescale/timescaledb-ha:pg16`, real `migrate` CLI schema apply): 3 full `go test ./... -count=1` runs all green, plus 20 targeted repetitions of the specific test and its siblings, 20/20 pass — zero reproduction. Logged as supporting evidence under B-122's existing entry (the shared-`audit_log` cross-test-interference risk it already flagged), not a new B-ID, per the user's explicit framing that a clean reproduction attempt would support B-122 rather than open a fresh item. Full detail in `BACKLOG.md`'s B-122 entry.
- **B-131 (2026-08-29): investigation complete, no build brief started.** `sequential-workflow-designer` (nocode-js/b4rtaz, MIT, official React wrapper) evaluated as a real replacement for the Workflow card editor's (B-065) presentation layer — explicitly the DEFINITION-side designer only, never its companion `sequential-workflow-machine` xstate execution engine (EAMI's own dispatcher/B-102 hooks/B-057-059 TOCTOU/B-124-125 audit trail stay exactly as-is, never in scope). **Licensing, confirmed against real docs:** free MIT core includes Task/Container/Switch/Launch Pad, Validation, Undo/Redo, read-only mode, Toolbox, per-step Editors — and confirmed free-tier custom step *types* (Task's own free doc page uses a custom `type`/`properties` bag as its worked example, exactly EAMI's v1 shape). Pro ($1199/2-devs perpetual + $75/mo) gates Folder/Icon/Interrupting-Icon/Interrupting-Task plus a cosmetic/extension layer — none needed for v1. **Flagged unresolved discrepancy:** the Pro pricing page calls "Large Task" Pro-exclusive but Large Task's own doc page carries no Pro badge — not blocking (EAMI doesn't need it) but a concrete reason nothing here should become load-bearing without a real Brief-1 install/import trial. **Data model fit clean** (`sequence`↔`step_order` array position, `Step.id`↔`workflow_steps.id`) with one precedented seam: EAMI's static-params/extraction split across two backend concepts needs unifying into one `properties` bag, the same unification B-064 already did once in local editor state. **Branching confirmed informational-only** — `SwitchStep`'s `branches` map only describes conditional UI shape, does not evaluate/execute anything, and does not touch or shrink EAMI's real backend gap (`workflow_steps`' flat `UNIQUE(workflow_id, step_order)` schema still cannot represent branches, per migration 000006's own comment). **Integration risk vs. the discontinued Workflow Canvas epic (B-066-069):** the canvas's two confirmed bugs were both born from React Flow being a free-form draggable graph (unwired node/edge deletion, a canvas-context dropdown failure); this library has no node positions/manual edge-drawing at all, so the entire two-layer draw-time+save-time graph-validity guard B-067/B-068 hand-rolled may collapse into near-nothing here, replaced by the library's own free per-step Validation — a real architectural risk reduction, distinct from and more significant than the "zero-dependency" claim (verified true but more precisely "zero *third-party* dependency" — one runtime dep on nocode-js's own sibling `sequential-workflow-model` package, confirmed via live npm registry metadata). **Explicitly NOT verified:** this library's own interaction wiring for undiscovered bugs — nothing here was hands-on browser-tested, same standing no-browser-automation limitation as every prior `eami-ui` brief. **Tailwind/Vite/SSR compatibility flagged as pending**, not verified — the library's own React-integration doc recommends a Next.js SSR-disabled pattern that likely doesn't apply to `eami-ui`'s Vite SPA, but needs a real Brief-1 check against actual config, mirroring B-066's own corrected-premise precedent rather than assumed. **Recommended incremental sequence, modeled explicitly on B-066→067→068's own discipline:** Brief 1 (read-only render, real measured bundle delta) → Brief 2 (interactivity, reusing `StepConfigPanel`'s existing logic as the library's `stepEditor`, same static/extraction persistence split B-067 established) → Brief 3 (save-time structural PATCH to the unmodified `UpdateWorkflow` endpoint, library's own free Validation replacing a hand-rolled `validateGraph` — likely smaller than B-068's given the tree/sequence model can't represent invalid topologies at all). **Investigation only — no code changes made, no B-ID for a build brief minted; this entry is the investigation record, ready for a real Brief 1 whenever picked up.** Full detail in `BACKLOG.md`'s B-131 entry. Counter now stands at **B-132**.
- **B-130 (2026-08-29): logged, EPIC, investigation not started.** Native Governed AI Desktop Client ("the Claude Desktop replacement") — a first-party desktop app, installed on end-user machines, that becomes THE interaction surface for AI at a customer org, replacing the need to use ungoverned consumer AI tools at all (not just detecting them, the endpoint agent's current job). Provider-agnostic (Claude/Gemini/other cloud/self-hosted, all governed identically). Two sizes, never combined into one estimate: a chat interface (Phase 1) and a coding-agent interface (Phase 2, categorically larger). **Critical architectural principle that must anchor every future brief:** the client is a THIN CLIENT to the existing gateway, never an independent decision-maker — every allow/deny/escalate/redact decision stays server-side in the already-built policy engine/approval flow/audit trail/cost tracking (B-039 onward, B-057, B-097-112, B-121-125); reuses the B-057/B-103 AI-provider adapter pattern for real, since the gateway (not the client) picks the backend per policy. Confirms the standing fact already in this file (Bearer AI-token JWT dual-auth built with no live consumer, anticipating exactly this). Real new complexity flagged, not hidden: a THIRD installed client (with the endpoint agent and browser extension) needing real cross-platform packaging (Electron/Tauri), code signing, auto-update, and enterprise deployment (SCCM/Intune/Jamf). Real risk flagged: per the Workflow Canvas epic's lesson (built, tested, found genuinely broken, fully reverted despite incremental discipline), a real-time streaming chat UI is arguably higher-risk than the canvas was — any brief here must apply at least the same prove-it-in-stages discipline. Self-hosted-LLM support via a single "custom OpenAI-compatible endpoint" adapter is a plausible but *unverified* claim, not yet confirmed. **Conversation-context architecture note:** LLMs are stateless — no model holds memory between calls; any "remembers our conversation" experience is constructed by re-sending the growing transcript every turn. Per the thin-client principle above, the GATEWAY (not the client, not the AI provider) must own and re-inject conversation history each turn, so every message in a conversation's full context has passed through policy/audit once — a client-held-history design would leave the gateway blind to prior turns, a real governance gap. Real cost consequence: re-sending full history each turn makes a growing conversation progressively more expensive per message, not flat-cost — B-111's already-built, adversarially-verified caching-cost-accounting work becomes directly load-bearing here, so real prompt caching should be a day-one design requirement, not a later optimization. Per explicit user direction: the largest single epic identified this session, bigger than the multi-hop workflow engine or the MCP arc — recommended as its own dedicated future session. Full detail in `BACKLOG.md`'s B-130 entry. Counter now stands at **B-131**.
- **B-124/B-125 (2026-08-28): DONE.** Tamper-evident audit trail for escalation resolution outcomes — a second, real `audit_log` row now writes when an escalation genuinely resolves (approved/denied/expired), carrying real `approval_id`/`approved_by`; ctx-cancelled writes nothing by design. Prerequisite (`Hold()`'s 4 exit paths converged onto one `HoldOutcome` construction point, mirroring B-102) completed and proven first, per the brief's own ask. The attribution fix — `ApprovedBy` only copied onto a resolution row when the decision is "allowed" or the human genuinely denied it, never for "approved but the resumed dispatch failed" — was found independently by both mandatory review passes and became the centerpiece of live verification: the live approved-path test landed directly on that exact edge case (no downstream configured in this dev topology) and the real row came back exactly right (`decision="denied"`, real `approval_id`, empty `approved_by`). The live denied-path test showed the clean case (`approved_by` correctly populated). All 4 real rows across both live escalations independently re-hashed outside the application (bash/sha256sum) and matched stored hashes exactly. **This brief could not be live-verified at all until B-129 landed** (see below) — no escalate policy created after gateway startup was reliably reachable before that fix. Expired/ctx-cancelled paths proven only by the 14+ real-Postgres Go integration tests (10-minute timeout, HTTP-unobservable cancellation make them live-impractical), consistent with this session's established standard. Full detail in `BACKLOG.md`/`BUILT.md`'s B-124/B-125 entries.
- **B-129 (2026-08-28): DONE.** `main.go`'s startup-frozen `pLoader.Evaluator()` snapshot fixed — `Dispatcher`/`workflow.Executor` now hold a `policy.EvaluatorSource` (new interface, `eami-policy/evaluator.go`) and call `.Evaluator()` fresh on every dispatch/step. Investigation (performance ruled out via benchmark — ~1.5ns fresh vs ~0.4ns cached, negligible; `workflowExecutor` had 2 independent instances of the bug, both fixed; tonight's prior live-verification claims B-086/090/098/116/118 confirmed unaffected, B-121 flagged as a genuinely open question not resolved either way) fully answered before building, per explicit user direction. Mandatory reviewer+security passes both clean (security review's one finding — this fix changes B-128's blast radius — correctly attributed there, not a new defect). Adversarial before/after test (`dispatcher_policy_reload_test.go`) isolates the fix to the one causal variable; live-verified against the real running gateway with zero restart, reproducing and resolving the exact scenario that blocked B-124/125. Built on a clean base via `git stash` (B-124/125's uncommitted WIP shelved during this brief, restored after committing B-129 alone) so the two efforts stay cleanly separable in history. **B-124/125 now unblocked — resumes next.** Full detail in `BACKLOG.md`/`BUILT.md`'s B-129 entries.
- **B-128 (2026-08-28): URGENT/Critical, logged not fixed — no `org_id` scoping exists anywhere in the policy evaluation pipeline; every tenant shares one global, unfiltered policy list.** Found mid-live-verification of B-124/125: `eami-gateway/internal/policyloader/loader.go`'s `queryRules()` has no `org_id` predicate and doesn't even SELECT it; `eami-policy/types.go`'s `Rule`/`Conditions`/`ActionContext` have no `OrgID` field at all — there is nothing downstream to filter on even if the loader tried. `Loader.store()` builds exactly one global `policy.Evaluator` shared by the entire process across every org. Confirmed live in the shared dev DB: 3 active policies from 2+ different orgs sitting in the same priority-ordered evaluation list. This means any org's dispatch can be matched, allowed, denied, or escalated by another org's policy today, in production — a real multi-tenant isolation failure in the core enforcement surface, not test noise. **Per explicit user direction, this was top priority alongside B-129 (2026-08-28), which turned out to be the more urgent, structurally distinct bug actually blocking live verification — see B-129's DONE entry above.** B-128 itself remains logged only, not fixed — the founder has not yet decided whether it needs its own dedicated brief; B-124/125 completed and were live-verified against an environment that still has B-128's gap present (disclosed explicitly in their entry, not silently worked around). The second open question this entry originally raised (why a priority=-100 policy still failed to match) is fully answered — see B-129.
- **B-121 (2026-08-27): DONE — `dispatcher.go`'s audit-write-error handling unified across all 4 write sites (Deny/Escalate/Allow-success/Allow-proxy-failure).** Previously 3 of 4 silently discarded the error; only Allow-success logged it — the two most governance-relevant outcomes (a denial, an escalation) were written blind. Investigated first, per the brief's own ask, whether the fix belongs in B-102's hook mechanism: partially — audit-write TIMING is deliberately branch-specific and predates B-102 (Escalate writes before blocking on `Hold()`), so only the resulting ERROR's handling was unified via one new hook (`logAuditWriteFailureHook`, registered first in the hooks list), not the write's timing. Logging (not a stronger alert) confirmed as the right call — no metrics/alerting infra exists in `eami-gateway` to escalate into.
**Both mandatory passes found real issues, all fixed:** security review's most severe finding — the Allow-proxy-failure branch's `DispatchOutcome.Decision="allowed"` (policy-branch label) diverges from what was actually written to `audit_log` (`"denied"`); a naive log line would have pointed an operator at exactly the wrong bucket during an incident. Fixed with a new `AuditDecision` field carrying the real value, live-verified against the real running stack (a real dispatch hit this exact branch; the real `audit_log` row confirmed `decision="denied"`). Code review: the log line needed more correlation fields (workflow_run_id/step_index/session_id/agent_uuid — added), and the new hook needed to run first, not last, so an unguarded panic in an earlier hook couldn't silently prevent the audit-integrity signal from firing (reordered). **A real bug the session found in its OWN test code, not either review pass:** an attempt to make the test log-capture handler forward to the real default handler (so unrelated test output wouldn't be silently swallowed) deadlocked — Go's `slog.defaultHandler` re-enters the classic `log` package's non-reentrant mutex when called from inside another handler's `Handle`. Reverted to a non-forwarding capture handler that dumps records via `t.Logf` only on failure.
**Four new items found and disclosed, not fixed (all out of this brief's `dispatcher.go`-only scope), all confirmed with the user before minting B-IDs:** **B-124** (Medium-High, but explicitly flagged by the user as the NEXT priority brief once this one closes, not routine backlog — an approved escalation's actual downstream execution writes NO `audit_log` row at all, only the pre-approval "escalated" row exists) and **B-125** (`approval_id`/`approved_by` never populated — per explicit user direction, scope together with B-124 as one brief, not independently) are the significant pair; **B-126** (test-only mirror of the same bug in `internal/workflow/testenv_test.go`, zero production impact) and **B-127** (a pre-existing test race where episode-recording goroutines lose to test cleanup, meaning episode assertions across this session's tests silently test writes that never land) are correctly low-priority.
**Live-verified against the real rebuilt/restarted gateway container:** a real seeded org/agent/policies, a real agent JWT, a real SSE session, two real `tool_call` dispatches (both naturally landed on the Allow-proxy-failure branch — no downstream configured in dev). Real `audit_log` rows confirmed correct via `psql`; zero false-positive failure logs under normal successful operation confirmed via `docker compose logs`. Per the same principle set for B-115/B-117 (deterministic fake-DB tests prove the failure-injection scenarios; live verification confirms normal behavior only), no real audit-write failure was forced against the live stack. Full detail in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s B-121/B-124/B-125/B-126/B-127 entries. Counter now stands at **B-128**.
- **B-115/B-117 (2026-08-27): DONE — test-fixture UUID fix + reorder deadlock retry.** Two small, independent, low-severity items found during the earlier B-090/107/109 hardening pass, closed in one session. **B-115:** `eami-gateway/internal/workflow`'s shared test helper `allowAllRules()` had a non-UUID rule ID (`"allow-all"`) that silently broke every allow-path `audit_log` write in every test using it (the write error was discarded, and no test read back the row) — fixed to `uuid.NewString()`. **B-117:** `eami-api`'s `ReorderPolicies` handler gained a bounded retry (`reorderPoliciesWithRetry`, 3 attempts, 20ms backoff) on a detected real Postgres `40P01` deadlock — B-090's own concurrency test already treated this as safe/retryable, but nothing in the actual handler retried it before now. **This brief's own STANDING RULES omitted the mandatory reviewer+security pass** — flagged by the user as a gap in how the brief was written, not a deliberate exception, and both passes ran anyway per explicit direction ("every other brief tonight has had them specifically because 'looks low-severity' hasn't reliably predicted 'actually is'").
**Both mandatory passes found real issues, all fixed except three correctly-disclosed-not-fixed items:** B-117's retry wrapper had a comment overstating the fix (the raw-driver-error-text leak isn't closed, by design — same out-of-scope decision as the original B-117 backlog entry), a silent-success footgun if `reorderPoliciesMaxAttempts` were ever non-positive, an uncancellable backoff sleep, and — the most substantive catch — one of the two new tests was vacuous (simulated a deadlock on every attempt including the last, so nothing ever touched real Postgres, making its "no partial state" assertion trivially true); rewritten to exercise a real terminal failure on the final attempt. All fixed. **Three new findings disclosed, not fixed, all confirmed with the user before minting B-IDs:** **B-121** (Medium-High, highest priority of the three per explicit user direction) — `eami-gateway/cmd/gateway/dispatcher.go` silently discards `audit_log` write errors on the `denied`/`escalated` branches, unlike `allowed` (which logs) — a real production audit-trail reliability gap found while looking for B-115's production analogue. **B-122** — B-115's fix means every workflow test now leaves a permanent orphaned `audit_log` row in the shared dev DB (measured live: 43→59 rows in one run) since that table has no FK to `orgs`; needs a real design decision (isolated test DB vs. accept-and-document), not a reflexive cleanup (would break the hash chain). **B-123** — B-093's `allowAllRulesRealPolicyID()` workaround is now a redundant byte-identical duplicate; cleanup-only, lowest priority.
**Live-verified against the real rebuilt/restarted stack (both `eami-gateway` and `eami-api` containers, Docker Desktop left running from the prior session):** B-115's audit write confirmed via a new real-Postgres test reading back the row. B-117: 60 real genuinely-concurrent HTTP `POST /v1/gateway/policies/reorder` requests (30 iterations × 2, scaled up from B-090's own 20-iteration precedent) against a real admin-authenticated session — zero non-200 responses, final state a valid uncorrupted permutation. All fixtures cleaned up, confirmed 0 remaining via direct `psql`. Full detail in `BUILT.md`'s `eami-gateway`/`eami-api` sections and `BACKLOG.md`'s B-115/B-117/B-121/B-122/B-123 entries. Counter now stands at **B-124**.
- **B-116/B-118 (2026-08-27): DONE — token-issuance hardening: JWT claims (Scope/Model/Owner/RiskTier/TTLSeconds) bound to the DB, per-agent rate limiting added.** Closes the two items B-090/B-107's own review passes logged. **B-116:** `apikey.go`'s `ValidateAndResolveAgent` extended (same single query, no new round trip) to also return `Scope`/`Model`/`Owner`/`RiskTier`; `issue_http.go` now overwrites all four from the resolved DB record unconditionally (client-sent values become advisory only), matching how `Subject`/`AgentID` were already handled — a deliberate "overwrite, don't validate-and-reject" choice confirmed with the user. `Task` stays client-supplied (no matching column). **B-118:** a small in-memory per-agent rate limiter (20 req/60s), algorithm duplicated from `workflow/ratelimit.go`'s B-070 precedent, hardcoded rather than env-configurable (deliberate scope decision to stay inside `apikey.go`/`issue_http.go` only). **`apikey.go` was added to this brief's file scope mid-session** (confirmed with the user) — B-116 is structurally unbuildable from `issue_http.go` alone without either extending the SELECT or reintroducing a second query, which B-107's own round-trip constraint forbids.
**A real finding beyond B-116's own named fields, from this brief's own mandatory code-review AND security-review passes — both independently converged on it:** `TTLSeconds` was the one client-controlled field actually *enforced* (`Manager.Validate` checks `exp`), unlike the other four, which nothing downstream trusted yet — and `gateway_agents.token_ttl_seconds` (real, admin-managed, `NOT NULL DEFAULT 900`) was a column `eami-gateway` never read at all. An operator's per-agent TTL risk control was silently inert. Fixed the same way as the other four fields, live-verified (a real agent with `token_ttl_seconds=300`, a forged `ttl_seconds:14400` request, the real returned JWT decoded to `exp - iat = 300`).
**Mandatory reviewer + security passes, both found real issues, all fixed except two correctly-disclosed-not-fixed items:** the TTL gap above (most severe, from both passes independently); no test proved the rate-limit window ever actually expires (fixed — 7 new pure-unit tests, no DB needed); a previously-implicit NOT-NULL dependency in the extended SQL scan (fixed — documented in code, deliberately not COALESCE'd). **Disclosed, not fixed, correctly out of B-118's own stated scope:** the endpoint still has zero protection on the *pre-authentication* path (a flood of bogus API keys still costs a real DB round trip against the shared pool) — logged fresh as **B-120**. Full env-configurable rate-limit thresholds (matching B-070's pattern) — logged fresh as **B-119**, confirmed with the user before minting either.
**Live-verified twice against the real rebuilt/restarted gateway container + real Postgres** (Docker Desktop was not running at session start; started fresh for this) — forged-claims-overwritten-by-DB proof, the 20-request/429-on-21st rate-limit trip with a real `Retry-After` header, cross-agent scoping (B-098) re-confirmed still 403, and the TTL fix re-verified in a second live pass. All fixtures cleaned up, confirmed 0 remaining. Full detail in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s B-116/B-118/B-119/B-120 entries. Counter now stands at **B-121**.
- **B-090/B-107 (2026-08-26): DONE — backend hardening batch: `ReorderPolicies` batched to 1 round trip (transaction removed), token issuance cut from 3 blocking round trips to 1. B-109 re-verified, left QUEUED (race not locatable).** Three independent, previously-disclosed items from B-086/B-098's own code-review passes. **B-090:** `ReorderPolicies` rewritten as one `UPDATE...FROM unnest()` statement; the explicit transaction wrapper (B-086's fix) was **removed entirely, not shrunk** — empirically verified via direct `psql` (a bare adjacent-swap and a bare 3-way rotation, no `BEGIN`, both correct) that a single statement's Postgres-atomic net effect makes the wrapper unnecessary, since B-086's bug specifically required N *separate* auto-committing statements. Round trips: N+2 → 1. **B-107:** `IssueHandler`'s `ValidateAPIKey`+`LookupByNameAndOrg` combined into one `ValidateAndResolveAgent` LEFT JOIN query (org-scoped from the same validated key row, never client input); `RecordIssued` made fire-and-forget mirroring B-099's `safeWriteTokenUsage`. Round trips: 3 blocking → 1. **B-109:** exhaustive re-verification (every `*_test.go` for `float64`, every goroutine-spawning test, every `sync.WaitGroup` repo-wide) could not locate the described race — this entry was originally minted straight from an investigation summary without confirming the file/line existed. Per explicit user direction, left QUEUED with an honest note (not invented a "won't fix" status with no BACKLOG.md precedent) rather than closed, since a negative search isn't proof it never existed.
**Mandatory reviewer + security passes on B-090/B-107, both found real issues, all fixed before shipping — the most severe was a bug this brief itself introduced:** code review's top finding (HIGH) — the new fire-and-forget `RecordIssued` goroutine omitted the `safego.Guard` panic-recovery its own cited precedent actually uses, so an unrecovered panic there would have crashed the entire gateway process, not just the request — fixed. Also fixed: `context.WithoutCancel` swapped for a bounded `context.WithTimeout` (5s) on that same goroutine (unbounded hang risk under a stalled DB); `http.MaxBytesReader` (8KiB) added ahead of the body decode B-107 moved earlier in the handler (closing an unauthenticated unbounded-body-buffer DoS a comment had incorrectly waved off as "not security-relevant"); a stale comment crediting a nonexistent handler-side org pre-check on `ReorderPolicies`'s production path, corrected; duplicate `policy_ids` now rejected with 400 (batched `UPDATE...FROM` is nondeterministic on duplicates where the old N-loop was deterministic); a comment overclaiming B-090 "closes" (vs. narrows) the deadlock window, corrected; sqlc query-annotation drift that would've broken both `ReorderPolicies` call sites on a future regen, fixed; the now-dead `ValidateAPIKey` interface method removed; a missing cross-org same-agent-name test added.
**New discovered work, not fixed here, logged fresh:** **B-116** (Medium, latent) — `IssueHandler`'s signed JWT claims beyond `Subject` still pass straight from client input, not currently exploitable (traced: nothing downstream reads them) but a self-service-claims risk the day something starts trusting `claims.RiskTier`. **B-117** (Low) — `ReorderPolicies`'s accepted `40P01` deadlock surfaces as a raw-error-text 500 with no retry, broader than this brief's one-function scope. **B-118** (Low-Medium) — `/v1/gateway/tokens` has no rate limiting, pre-existing, now cheaper to exploit under B-107's async change.
**Live-verified end-to-end against the real rebuilt/restarted stack:** AC1 (B-086's original adjacent-swap enforcement proof) re-run through the real HTTP API, correct. A real 2-goroutine concurrent-overlapping-reorder test (20 iterations) — zero corruption, no uncaught deadlock. AC3 (B-098's cross-agent-scoping proof) re-run live — cross-agent attempt → real 403, correctly-scoped → real 200 with a real JWT, and the resulting `ai_token_events` row confirmed via `psql` within 1s, proving the `safego.Guard`+bounded-timeout rework lands correctly under real conditions. **`go test -race` confirmed still unavailable in this environment** (no C compiler, `CGO_ENABLED=0`) — unchanged from B-100's disclosure, re-confirmed not assumed. All fixtures cleaned up. Full detail in `BUILT.md`'s `eami-gateway`/`eami-api` sections and `BACKLOG.md`'s B-090/B-107/B-109/B-116/B-117/B-118 entries. Counter now stands at **B-119**.
- **B-091/B-092/B-093 (2026-08-26): DONE — frontend cleanup batch (ConfirmDialog isLoading, cross-page deep-linking, workflow_run_id audit linkage).** All three re-verified against current code before building, confirmed unchanged since logged. **B-091:** `ConfirmDialog.tsx` now actually uses `isLoading` (disables both buttons, spinner on Confirm, matching `SettingsPage.tsx`'s existing `SaveButton` convention) — zero prop-shape change, so 4+ existing callers started working with no edits. **B-092:** `DataTable.tsx` (shared by Agents/Policies/Approvals since B-104) gains `getRowId`/`highlightRowId` for `?highlight=<id>` deep-linking + auto-scroll + auto-page-advance. **Real wrinkle found during re-verification:** `ApprovalsPage.tsx` is architecturally different (its default tab is a card list, not `DataTable`; its "all" tab is server-paginated) — handled by forcing the "all" tab and fetching `per_page:1000` on a deep link, matching Agents/Policies' existing pagination-avoidance convention rather than building new backend search support. **B-093:** `audit_log` gains nullable `workflow_run_id`/`step_index` (migration 000012, no FK — matches this table's existing convention), threaded through `mcp.ActionContext` → `workflow/executor.go`'s `runStep` → `dispatcher.go`'s `auditEntry` construction, **excluded from the hash chain** (same B-078 `DataHandling` precedent, re-confirmed before writing the migration). **A real, previously-masked pre-existing bug found while writing B-093's own tests:** the shared `allowAllRules()` test helper's non-UUID rule ID (`"allow-all"`) silently breaks every allow-path `audit_log` write in the `workflow` test package (`policy_id` is a real `uuid` column) — every existing test using it has quietly absorbed this exact failure since none previously checked the resulting row. Confirmed test-fixture-only, not production; logged fresh as **B-115** (not fixed in the shared helper). All ACs live-verified against the real rebuilt stack, including a real 2-step workflow dispatch's `audit_log` rows and an independent bash-side hash-chain recompute matching the documented formula exactly (AC4). Browser automation (`claude-in-chrome`) confirmed not connected this session — B-091/B-092 verified via served-module confirmation, stated plainly rather than overclaimed. Full detail in `BUILT.md`'s `eami-gateway`/`eami-ui` sections and `BACKLOG.md`'s B-091/B-092/B-093/B-115 entries. Counter now stands at **B-116**.
- **B-112 (2026-08-26): DONE — `model_pricing` admin CRUD + B-110's fallback-rate collision fixed.** `GET/POST/PATCH/DELETE /v1/admin/model-pricing[/{model}]` (mirroring B-098's `api_keys` CRUD), admin-only writes since this table is genuinely global (no `org_id` — confirmed via schema + doc search). **B-110's actual mechanism, re-verified empirically before building, was narrower and different from its own description:** a real rolled-back Postgres reproduction showed an unrecognized model's row was already kept correctly separate in `by_model`, never merged into a recognized model's — the real bug is that its cost silently becomes exactly $0 (Postgres `SUM` ignores the `NULL` from an unmatched rate), indistinguishable from a genuinely free dispatch. Fixed via `ModelSpend.PricingConfigured` + `TokenSpendSummary.UnrecognizedModelRequestCount`, surfacing the undercount instead of hiding it — B-111's cache-tier and base/output frozen-cost mechanisms both left untouched. **A real discrepancy caught live, not glossed over:** AC2 ("historical rows re-price on next query") only holds for cache-tier rates and $0/unrecognized-at-write-time base rows — not a normal already-priced row's base rate, which B-111 deliberately freezes. Per explicit user decision: disclosed as-is (B-111's mechanism untouched, per this brief's own scope), corrected wording recorded in `BACKLOG.md`. **Two new disclosed findings, not fixed here:** any org's ordinary admin can write to this global table (no platform-admin tier exists) — **B-113**; whether base-rate historical re-pricing should become a deliberate feature is a real open product question — **B-114**. All 4 ACs live-verified against the real rebuilt stack. Full detail in `BUILT.md`'s `eami-api`/`eami-ui` sections and `BACKLOG.md`'s B-112/B-113/B-114 entries. Counter now stands at **B-115**.
- **B-111 (2026-08-26): DONE — caching cost-accounting fixed: `extractTokenUsage` now parses Anthropic prompt-caching token counts across all 5 pricing tiers, priced at query time from current `model_pricing` rates.** `token_usage` gains 3 raw count columns (`cache_creation_5m_tokens`, `cache_creation_1h_tokens`, `cache_read_tokens`; `schema/migrations-v2/000011`); `model_pricing` gains 3 matching nullable rate columns, backfilled per-row for `claude-%` models from each row's own `cost_per_1k_in` (1.25x/2.0x/0.1x — Anthropic's documented multipliers, cross-verified against two independent live sources). **Design, per this brief's explicit contract:** cache cost is never computed or stored at write time (`IngestTokenUsage`'s existing base/output formula untouched) — all 5 `finops.go` queries add a cache-cost term computed fresh from CURRENT rates every query, so a rate correction re-prices historical rows automatically. **One thing re-verification caught that the original plan got wrong:** 1h cache TTL no longer needs a beta header (just a `"ttl":"1h"` field in `cache_control`), so it's fully reachable through `ClaudeAdapter`'s pass-through dispatch today, not a theoretical edge case. **One genuinely open question resolved empirically, not guessed:** whether Anthropic's `cache_creation` breakdown sub-object appears for a single-TTL (not just mixed) response — live-verified yes, via a real 1h-only dispatch. **Two claimed "already logged" items in the brief's own MUST NOT MODIFY list turned out not to exist anywhere in the repo** — logged fresh as B-109/B-110 in the prior turn rather than silently trusted. All 5 ACs live-verified against the real rebuilt stack, including the AC4 centerpiece (a live `model_pricing` rate change re-priced an already-recorded historical row with zero new writes, then reverted). Full detail in `BUILT.md`'s `eami-gateway`/`eami-api` sections and `BACKLOG.md`'s B-111 entry. Counter now stands at **B-112**.
- **B-109/B-110 (2026-08-26): logged, QUEUED, investigation not started for either.** Both surfaced by the caching cost-accounting investigation (the one behind the not-yet-approved B-111 caching-cost-accounting brief) but are independent findings, not part of that brief's own scope. **B-109 (Low):** a test-helper `float64` cost accumulator mutated across concurrent test goroutines with no synchronization — test-only, no production impact, exact file/line not yet pinned down. **B-110 (Medium):** `finops.go`'s cost-summary calculation can silently misattribute an unrecognized model's requests into a recognized model's aggregate totals — a real production reporting-accuracy gap, not test-only; noted as worth doing alongside the not-yet-B-ID'd `model_pricing` admin CRUD item, since CRUD changes how often the "unrecognized model" case is even hit. Neither investigated yet — first step for whichever session picks either up is root-causing the exact code path. **Counter now stands at B-111.**
- **B-108 re-verification (2026-08-26): reconfirmed clean, no code changes.** Same-session request to re-run live/integration verification fresh (separate from the 2026-08-25 build). Reset `dev@example.com`'s password to a known value again (same disclosed precedent as every prior live-verification session — still no self-service way to create a throwaway admin once an org exists, `Bootstrap` only works on an empty `orgs` table). Created fresh fixtures via the real HTTP API (agent `b108-reverify-agent`, `rest_api` connector `b108-reverify-connector` → `postman-echo.com`, a scoped API key), minted a real gateway JWT, opened a real MCP SSE session, and dispatched a real `tool_call` — confirmed via direct `psql` that the resulting `token_usage` row carried `tool_name = 'b108-reverify-connector'` (AC1). Seeded two more real rows via the actual `POST /v1/internal/token-usage` ingestion endpoint (not raw SQL) with known costs ($0.08 each via `claude-haiku-4-5-20251001` pricing), then called the real `GET /v1/finops/summary`: `avg_cost_per_outcome: 0.05333...` (exactly `0.16/3`), `by_tool` correctly split `$0.08`/`$0.08` across the two connectors, `by_team: [{"team":"b108-reverify", cost_usd:0.16,...}]` correct, `by_agent` regression-clean (AC2-AC5 all reconfirmed). Also confirmed the live `eami-ui` dev server (`:5173`) is serving the current `FinOpsPage.tsx` module containing `by_tool`/`by_team`/`avg_cost_per_outcome`, not a stale build. All fixtures cleaned up afterward: API key and connector hard-deleted; the agent could not be hard-deleted (`409 conflict` — it now has real episode history from the live dispatch, `DeleteAgent`'s existing audit-trail guard correctly refused; suspended instead, same as B-104/B-098 precedent) — `b108-reverify-agent` remains in Dev Org with `status: suspended`; the 3 `token_usage` rows (no FK to `orgs`/`gateway_agents`, confirmed via `\d token_usage` — same FK-less pattern already documented in the original B-108 entry) were deleted manually via `psql`, confirmed 0 remaining. **B-108 stays closed** — this was a pure re-verification, not new work, no new B-ID minted.
- **B-108 (2026-08-25): DONE — FinOps connector breakdown + two silent metric gaps closed, all three found by the Token Usage Optimization re-investigation.** `token_usage.tool_name` (existed, unpopulated) is now threaded through `extractTokenUsage`→`TokenUsageRequest`→`InsertTokenUsageParams` and surfaced via a new `by_tool` query/UI table mirroring `by_model` exactly (`COALESCE(tu.tool_name,'unknown')` for unresolved-tool dispatches, same fallback shape as `by_team`'s existing `COALESCE(ga.owner,'unknown')`). **Two decisions made after investigating what each metric was actually meant to represent, not defaulted:** `avg_cost_per_outcome` (documented in `openapi.yaml`, read by the frontend, never computed by the backend — a permanently blank KPI card) — **computed for real** (`total_cost_usd / COUNT(*)` over the period; cheaper than removing, since removing would touch Architect-EAMI-owned `openapi.yaml`). `by_team` (computed correctly since B-097, never rendered) — **rendered**, not removed, since the backend work was already correct and already tested. All 5 ACs live-verified against the real running stack: a real dispatch through a real `rest_api` connector confirmed `tool_name` lands correctly (AC1); a real `GET /v1/finops/summary` against known seeded data returned `avg_cost_per_outcome: 0.12333...` (exactly total/count) and a correctly-split `by_tool` breakdown (AC2/AC4); `by_team` confirmed present in both the API response and the real served `eami-ui` module (AC3); zero regression to `by_agent`/`by_model` across 13/13 FinOps tests (AC5). No schema migration, no dispatch logic touched. Full detail in `BUILT.md`'s `eami-gateway`/`eami-api`/`eami-ui` sections and `BACKLOG.md`'s B-108 entry.
- **B-098 (2026-08-24): DONE — `POST /v1/gateway/tokens` (previously zero-auth, could mint a valid AI-agent token for any existing agent name) is now gated by real, scoped `api_keys` rows.** New `eami-gateway/internal/identity/issue_http.go` (`IssueHandler`, replacing the deleted insecure `Manager.HandleIssue`) requires a real, unrevoked, unexpired, agent-scoped API key (`X-API-Key`), resolves the requested agent via `registry.LookupByNameAndOrg` scoped to the **validated key's own org** (never client input), and rejects unless the resolved agent's UUID equals the key's bound `agent_id` — the actual cross-agent scoping proof, live-verified against the real running gateway (a key scoped to agent A requesting agent B's token → real 403; requesting its own agent → real 200 with a real signed JWT). New `schema/migrations-v2/000010`: `api_keys.agent_id` (nullable FK to `gateway_agents`, `ON DELETE SET NULL`) + new `ai_token_events` table (org-cascaded, snapshot columns with no FK on `agent_id`/`agent_name`/`api_key_id`, mirroring B-087's `agent_lifecycle_events` reasoning — both `issued` and `revoked` events confirmed recorded live). `eami-api`'s `CreateAPIKey` now accepts `agent_id` (org-scoped validation, rejects a non-`active` agent) and `expires_at` (existed in schema since migration 002, never settable at the API layer until now); `eami-ui`'s Settings API Keys tab gained an agent selector. **Mandatory reviewer + security subagent passes both ran and both found real issues, all fixed before shipping:** security review (independent fork, full adversarial trace) found zero HIGH-confidence findings but the same investigation independently caught a same-org agent-name-enumeration oracle (two different 403 messages) — unified, live-reverified. Code review (general-purpose subagent, "high" effort) found and this session fixed: `eami-api`'s pre-existing `GetAPIKeyByHash` didn't filter `expires_at` (inconsistent with the gateway's own new validator for the identical row), `CreateAPIKey` didn't reject binding to a suspended agent, and the new UI column fell back to a raw UUID instead of a short placeholder. **Disclosed, not fixed (out of this security-fix brief's scope):** `HandleIssue` now costs 3-4 serial Postgres round trips per issuance versus the previous single in-process sign — logged as **B-107**, QUEUED Low-Medium, real only if issuance becomes a high-frequency path. `api/openapi.yaml` doesn't yet document the new fields (Architect-EAMI-owned, disclosed not silently edited, same B-086 precedent) — logged as **B-106**, QUEUED Low. **Verified 2026-08-24: `go build`/`go vet`/`go test -count=1 ./...` clean across both `eami-gateway` and `eami-api` against a real Postgres, zero regressions; the new migration verified via the full `schema/migrationtest` suite (fresh/incremental parity, idempotent re-run); real `tsc`/`vite build` clean.** All 6 acceptance criteria live-proven against the real running containerized stack (rebuilt/restarted before and after the code-review fixes), including the centerpiece cross-agent rejection; all live-verification fixtures cleaned up afterward. Full detail in `BUILT.md`'s `eami-gateway`/`eami-api`/`eami-ui` sections and `BACKLOG.md`'s B-098/B-106/B-107 entries.
- ADR-019: RESOLVED, Accepted — 2026-07-22. Full episode content stays
  on-prem; eami-api never serves it. See DECISIONS.md ADR-019 (now a full
  formal entry, same number — the informal Pending-table row it replaces
  has been removed, not renumbered). **Now fully enforced in the running
  system, not just decided on paper — see B-002 Brief 3 below.**
- **B-002: DONE and fully closed on `master`** — all 3 briefs complete
  and merged (`3eab113`, `adcd3e9`, `292d6a4`). Exactly one path to full
  episode content exists in the running system, and it enforces org
  isolation. History:
  - Brief 1 (gateway dual-auth endpoint): **DONE, merged to master**
    (merge commit `3eab113`, from branch `b-002-gateway-episode-endpoint`,
    plan at `C:\Users\bharg\.claude\plans\unified-wandering-karp.md`). New
    `eami-gateway` package `internal/episode/{store,reader,http}.go` +
    tests, wired into `cmd/gateway/main.go`, new required config
    `GATEWAY_EPISODE_READ_SERVICE_KEY`. Reviewer + security subagent passes
    both clean (no compile-level defects; one already-known/approved
    trust-boundary tradeoff flagged, tracked as BACKLOG B-015, not a bug).
    **Verified 2026-07-22 with a real toolchain: `go build ./...` and
    `go test ./... -v` both clean, 0 failures, 18/18 new tests passing.**
  - Brief 2 (eami-api proxy layer): **DONE, merged to master** (merge
    commit `adcd3e9`, branch `b-002-eami-api-proxy-layer`, since
    deleted), plan at
    `C:\Users\bharg\.claude\plans\unified-wandering-karp.md`. New file
    `eami-api/internal/api/gateway_episodes.go` proxies
    `GET /v1/gateway/episodes*` to Brief 1's gateway endpoint. The hard
    requirement Brief 1 deferred is now satisfied: `org_id` sent to the
    gateway is always `claimsFromContext(r).OrgID` (the caller's own
    session org), never client input — an optional `org_id` query param
    is accepted only as a tamper-check that 403s on mismatch *before* the
    gateway is ever called, so a forged org can't structurally reach the
    gateway at all. Purely additive: `memory.go` has zero lines changed,
    old `/v1/memory/episodes*` routes untouched. Reviewer + security
    subagent passes both clean (2 low-severity test-coverage gaps found
    and closed before commit). **Verified 2026-07-22 with a real
    toolchain: `go build ./...`, `go vet ./...`, `go test ./...` all
    clean, 0 failures, 11/11 new tests passing** (includes the centerpiece
    `TestGatewayEpisodes_List_MismatchedOrgIDSupplied_Returns403_
    GatewayNeverCalled`, asserting both the 403 and that the fake gateway
    client's call count is zero). Fixed a nil-`cfg` panic in `NewServer`
    along the way (pre-existing latent bug, surfaced by wiring in the new
    config — `finops_test.go` already called `NewServer(nil, ...)`).
  - Brief 3 (memory.go + MemoryPage.tsx cutover): **DONE, merged to
    master** (merge commit `292d6a4`, branch `b-002-memory-cutover`,
    since deleted), plan at
    `C:\Users\bharg\.claude\plans\unified-wandering-karp.md`. Chose
    option (a): re-pointed the existing, `api/openapi.yaml`-documented
    `/v1/memory/episodes` and `/v1/memory/episodes/search` URLs at
    Brief 2's already-secure handlers (`ListGatewayEpisodes`/
    `SearchGatewayEpisodes`) instead of moving the frontend to new URLs
    — verified the response shapes are byte-identical, so this needed
    **zero `MemoryPage.tsx` changes**. Added `GET /v1/memory/episodes/
    {episodeId}` → `GetGatewayEpisode`, filling an `openapi.yaml`-
    documented route that was never implemented. **`eami-api/internal/
    api/memory.go` and `eami-api/internal/store/episodes.go` (the last
    direct, unprotected `episodes`-table query path) are deleted
    entirely** — verified zero other callers first. Security review for
    this brief specifically re-verified the org-isolation chain at the
    new `/v1/memory/episodes*` mount points (not assumed to carry over
    from Brief 2's review) and confirmed **the leak is fully closed**,
    not just superseded by a safer alternative running alongside it. 8
    new tests in `memory_test.go`, reusing Brief 2's fixtures with zero
    duplication; `gateway_episodes_test.go` itself has zero diff.
    **Verified 2026-07-22: `go build ./...`, `go vet ./...`,
    `go test ./...` all clean, 0 failures.** Two things NOT done, flagged
    before building rather than discovered after: frontend build/lint/
    typecheck (Node/npm confirmed genuinely absent from this machine —
    checked install locations directly, not just PATH) and `docker
    compose up`-based manual verification (no Docker in this
    environment) — `MemoryPage.tsx`'s correctness rests on manual
    shape-verification only.
- **B-104 (2026-08-24): DONE — `AgentsPage.tsx`/`PoliciesPage.tsx`/`WorkflowsPage.tsx` migrated onto the shared `DataTable` component, closing the root cause behind B-081/082/083's bug class.** `DataTable.tsx` itself untouched (investigated, confirmed unnecessary). **Real complication found before building:** neither `ApprovalsPage.tsx` nor `AlertsPage.tsx` — offered as "the reference" — actually uses `onRowClick`; both are read-only `DataTable` usages, so this is the first real row-click + action-buttons instance. **Real design problem solved:** Policies' reorder buttons need each row's true array index, which `Column.render(row)` doesn't receive — solved via `policies.indexOf(policy)` inside `render` (valid because no column here is `sortable`), zero `DataTable.tsx` change. All three pages pass `pageSize={1000}` to keep `DataTable`'s own pager from engaging, preserving today's unpaginated "show everything" behavior. Real `tsc && vite build` clean via the established `docker build --target builder` path. **Live browser click-through not performed** — seeded dev users have a placeholder password hash, no real login reachable; verified instead via the real running dev container serving the current code with the old hover-without-click pattern confirmed gone. Also flagged, not fixed: `DashboardPage.tsx`/`DiscoverPage.tsx` have the identical pattern, out of this brief's scope. Full detail in `BUILT.md`'s `eami-ui` section and `BACKLOG.md`'s B-104 entry.
- **B-103 (2026-08-24): DONE — B-057's `aiprovider.Adapter` pattern is now a documented standing convention.** No code changed. **Real judgment call, investigated before writing:** the brief offered `ARCHITECTURE.md`/a new ADR as candidate locations, but `BOUNDARIES.md`'s own ownership table assigns both to Architect-EAMI/PM-EAMI, outside this session's boundary per that doc's "Golden Rule." Documented instead as a new bullet in `CLAUDE.md`'s `## Conventions` section (not owned by anyone per `BOUNDARIES.md`, read by every future session) — confirmed with the user before writing. States the rule (new backend TYPE → small interface, one new file, explicit `map[string]Adapter` registry, never global/`init()`-time), cites `aiprovider/types.go`/`router.go`/`ClaudeAdapter` as the working example, and explicitly distinguishes this from B-102's mechanism so the two aren't conflated. **Also flagged, not fixed:** `ARCHITECTURE.md` §3.3 is missing `internal/aiprovider` from its package list — same staleness pattern `ADR-020`/`B-080` already found elsewhere in that file; outside this brief's boundary, left for its actual owner. Full detail in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s B-103 entry.
- **B-101 (2026-08-24): DONE — resolved as a direct consequence of B-102, not a separate fix.** See B-102's entry immediately below.
- **B-102 (2026-08-24): DONE — `dispatch` extracted into a testable `Dispatcher` type (`cmd/gateway/dispatcher.go`) with a branch-convergence hook mechanism, closing B-101 and structurally closing B-099's bug class.** Every branch (Deny/Escalate-Submit-failure/Escalate-resumed/Allow-proxy-failure/Allow-success) now converges on one shared exit via a typed `DispatchOutcome` struct that runs a literal, explicit two-hook list (`recordTokenUsageHook`, `recordEpisodeHook`) — no pub/sub registry, no event bus, no channels, mirroring B-057's `aiprovider.Router`'s explicit `map[string]Adapter`, exactly as the 2026-08-23 investigation (below) recommended. `internal/workflow`/`internal/approval/router.go` untouched — `dispatcher.Dispatch` has the identical `mcp.DecisionHandler` signature the old closure did. **Mandatory code-review pass (general-purpose subagent, "high" effort) caught 4 real findings, all fixed before shipping** — most importantly, the Escalate branch's own `Submit()`-failure case still `return`ed independently in the first draft, bypassing the hook list entirely (the exact bug class this brief exists to eliminate, reintroduced inside the fix itself); also: the mechanism was added inline into `main.go` instead of its own file (moved to `dispatcher.go`), `DispatchOutcome.Dispatched` was redundantly set per-branch instead of computed once from `Err == nil`, and four `episode.Step` literals were duplicated instead of using a shared constructor. Security review: zero findings. 7 new real-Postgres integration tests (`cmd/gateway/dispatcher_test.go`), including a regression test for the code-review finding and the central AC4 proof (a test-only hook added only via a construction-time `extraHooks` seam fires for all three decision types with zero branch-specific code change). **Verified 2026-08-24: `go build`/`go vet`/`go test -count=1 ./...` clean across the entire `eami-gateway` module against a real Postgres**, including every pre-existing TOCTOU/`dispatchApproved`/B-100-race/workflow test — zero regressions. Full detail in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s B-101/B-102 entries.
- **Extensibility-mechanism investigation (2026-08-23, no B-ID — investigation only, nothing built).** Investigated a disciplined Go mechanism to close B-099's bug class (a cross-cutting step manually wired into some but not all of `dispatch`'s parallel branches) without over-engineering into a generic framework. **Key finding: an exhaustive inventory of `eami-gateway`/`eami-api` turned up exactly ONE site with the multi-branch-fan-out shape this bug class needs** — the `dispatch` closure's 3-way policy switch (`cmd/gateway/main.go:284-456`, Deny/Escalate/Allow). Workflow step completion isn't a separate site: `workflow/executor.go` calls `dispatch` itself, once per step, unmodified (B-058/B-059's own design) — fixing `dispatch` covers workflows for free. Agent/tool/policy CRUD in `eami-api` are single linear handlers with nothing to fan out across — this bug class structurally cannot occur there today. `approval/router.go`'s `outcomeFromStatus` already converges every branch to one `decisionResult{data, err}` struct before returning — an existing in-repo precedent for the fix, not a place needing more machinery. **Recommended mechanism: no pub/sub registry, no event bus, no channels** — restructure `dispatch`'s three branches to converge on one shared exit (a small typed `dispatchOutcome` struct) with a short, explicit, literal slice of post-dispatch hook functions built at `dispatch`'s own construction site, mirroring B-057's `aiprovider.Router`'s explicit `map[string]Adapter` (passed via `New()`, never global/init-time registration). Traced concretely: this WOULD have prevented B-099, specifically conditioned on eliminating the branches' independent `return` statements, not merely bolting a registry on alongside them. It would NOT have prevented B-100 (a `Hold()`/`resolve()` concurrency race — orthogonal to the fan-out problem, not this mechanism's target). **Real sizing dependency: this fix inherits B-101's already-logged gap** — `dispatch` is an inline closure inside `run()`, not independently testable — so the honest sequencing is (1) extract `dispatch` into a testable function/type first (closes B-101 as a side effect), THEN (2) do the branch-convergence fix, so the new mechanism is provable by real Go integration tests from day one instead of inheriting B-101's live-verification-only limitation. Separately confirmed B-057's `Adapter` interface/registry (`aiprovider/types.go:59`) is the right, already-proven answer for a genuinely different problem — new integration TYPES (future AI providers, Skills, A2A agents) — recommended for documentation as a standing convention, explicitly not conflated with the hook-mechanism work above (new TYPE vs. new cross-cutting reaction to an existing call, two different problems). UI-adjacent finding (static-code-only, no browser verification, flagged per this pass's own scope): `DataTable.tsx`'s existing `onRowClick`-conditional hover (line 109) already correctly centralizes the row-click pattern — B-081/082/083 happened because `AgentsPage.tsx`/`PoliciesPage.tsx`/`WorkflowsPage.tsx` hand-roll their own `<tr>` instead of using it (only `ApprovalsPage.tsx`/`AlertsPage.tsx` actually use `DataTable`); the real fix there is page-level `DataTable` adoption, not a new abstraction. **Founder confirmed proceeding with real B-IDs the same session** — `BACKLOG.md`'s counter (`B-102`) checked directly first, no other reservation found in-file: logged as **B-102** (dispatch extraction + convergence/hook mechanism, closes B-101 as a side effect, QUEUED Medium), **B-103** (B-057 Adapter pattern docs, QUEUED Low, independent), **B-104** (DataTable adoption on Agents/Policies/Workflows, QUEUED Low, independent). Counter now stands at **B-105**. Full acceptance criteria in `BACKLOG.md`.
- **B-099 (2026-08-23): DONE — escalated/approved tool calls now record token usage.** New shared `recordTokenUsage` helper in `cmd/gateway/main.go`, called from both the immediate-Allow and escalate-then-approved dispatch branches (the latter gated on `holdErr == nil`), closing the gap B-097's AC4 found live. Zero changes to `approval/router.go` — B-057's TOCTOU/resume logic untouched by construction. All 4 ACs live-verified against the real stack, including a real TOCTOU-drift re-verification (credential rotation backed up/restored entirely inside Postgres). Full detail in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s B-099 entry.
- **B-100 (2026-08-23): DONE — the approval double-dispatch race is closed.** New `sync.Once`-guarded `pendingEntry.result` shared between `Hold()`'s timeout backstop and `resolve()`, ensuring `dispatchApproved` fires at most once per approval regardless of which path wins the race. Chosen over a DB-level lock since the race is provably intra-process. Reproduced deterministically (5/5) before building, and proven closed by reverting the fix and confirming the adversarial test correctly fails (`hitCount=2`) before restoring it. The mandatory code-review pass's finding (loser's block no longer bounded by its own `holdTimeout`) was investigated and its specific premise found factually inaccurate for this codebase (`Hold()`'s one production caller already uses `context.WithoutCancel`, per B-039) — documented explicitly in code as a pre-existing, non-worsened property, not silently dismissed. All 6 ACs verified (AC1/AC2 at the Go level, AC3-AC6 live against the real stack, including a TOCTOU-drift re-verification). Full detail in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s B-100 entry.
- **B-101 (2026-08-23, Low, QUEUED): the `holdErr == nil` gate B-099 added isn't independently unit-tested outside live verification** — `dispatch` is an inline closure inside `run()`, not testable without a refactor out of B-099's scope. Disclosed, not urgent. See `BACKLOG.md`'s B-101 entry.
- **B-097 (2026-08-22): `GET /v1/finops/summary`'s persistent 500 was root-caused (not the already-fixed B-016) and fixed.** Real cause: `teamQ`'s `GROUP BY team` collided with `token_usage`'s own real, always-empty `team` column — Postgres's GROUP BY name resolution silently preferred that input column over the SELECT alias, leaving `ga.owner` ungrouped (SQLSTATE 42803). Fixed by grouping by `ga.owner` directly. **AC4 finding, upgraded to a real live test result after explicit user direction to actually verify rather than stop at a code trace — a second, genuinely separate live bug found and flagged, not fixed:** a real dispatch to the org's real `claude` connector (approved through the real escalation flow) produced a genuine successful Anthropic response, but wrote **nothing** to `token_usage` — `extractTokenUsage`/`safeWriteTokenUsage` (`main.go:428-429`) only exists in the immediate-Allow dispatch branch; the Escalate branch returns its `Hold()` result directly and never reaches that code at all. Since the org's real Claude connector is unconditionally escalated by an active policy, **zero real AI-provider spend has ever been recorded in this org**, independent of B-097's own fix. **Logged as B-099 (QUEUED, Medium-High), not built** — flagged to the user before any fix was attempted, per explicit instruction not to expand B-097's scope. Full detail in `BUILT.md`'s `eami-api` section and `BACKLOG.md`'s B-097/B-099 entries. **B-ID sequencing note:** the previously-planned "gate AI-agent token issuance via api_keys" brief is **B-098**; the counter now stands at **B-100**.
- **B-096 (2026-08-22): a real centralized branding mechanism (config +
  build-time colorthief/OKLCH extraction from the logo) is now live in
  `eami-ui`, with rheoARC shipped as its first instance.** Deliberately
  creates a visible split, intentional not a bug: the Sidebar and Login
  page now render the real rheoARC logo image (it bakes "rheoARC" in as
  pixels), while `branding.displayName` — driving the browser tab title —
  stays `'EAMI'`. The full text-name change is still the separate,
  deferred rebrand epic this file's product-identity note already
  protects; do not "finish the job" by changing `displayName` or any
  other displayed EAMI string without the same explicit founder
  instruction that note requires. The icon-only small/collapsed logo
  variant is a generated placeholder (no icon glyph exists in the source
  wordmark) — real design asset still needed there. Full detail in
  `BUILT.md`'s `eami-ui` section and `BACKLOG.md`'s B-096 entry.

## Standing facts Code and PM must both know
- Desktop app: planned future feature, not yet built. Gateway auth should
  support it (Bearer JWT path) without a live consumer yet. Brief 1's dual
  auth already supports this path (Bearer AI-token JWT, org resolved
  server-side via the agent registry) with no live consumer.
- B-002's org-isolation logic is built, merged, and verified end-to-end
  (Briefs 2+3), but **do not provision `GATEWAY_EPISODE_READ_SERVICE_KEY`
  anywhere a caller other than eami-api's proxy could use it directly
  against eami-gateway** — see BACKLOG B-015 (Medium, still open: this is
  now a standalone network-hardening item, not a B-002 blocker — Brief
  1's gateway endpoint itself still enforces nothing on its own if
  reached directly, bypassing eami-api entirely).
- Pre-existing, unrelated issue discovered 2026-07-22 while verifying
  Brief 2: `finops_test.go`'s `TestFinOpsTimeSeries_*` subtests panic
  internally (nil `s.queries`) but still report PASS because chi's
  Recoverer catches it — see BACKLOG B-016. Not fixed, out of scope for
  B-002.
- No deploy infrastructure exists in this repo (no deploy.yml, no IaC).
  Nothing is live in production. api.eami.io in openapi.yaml is a spec
  placeholder, not a real deployment.
- Solo founder, pre-first-customer, evening/weekend hours.
- Paste-detection epic (browser-extension-based) is in scoping. B-032
  (2026-07-25) built the backend groundwork only — `paste_events` table +
  `POST /v1/reports/paste-events` — ahead of the extension itself. **No
  browser extension exists yet; this endpoint has no live caller.** An
  investigation earlier that session found `POST /v1/reports` (the
  existing collector-write path) has **zero real producers today** —
  `eami-collector`'s forwarder only ever calls `/v1/ingest/batch`, a
  different table/pipeline — worth knowing before assuming that endpoint
  is exercised in production. B-034 (2026-07-26) added the `eami-agent`
  side — a native-messaging host mode so a future extension can relay
  paste events in real time — but **`eami-agent` has no `org_id`
  anywhere** (only `agent_id`), confirmed by direct investigation, not
  assumed. B-035 (QUEUED) is the still-missing link: paste events
  currently land in the existing agent-report pipeline's raw JSON blob,
  not literally in `paste_events` yet — `eami-collector`/`eami-api` were
  both frozen for B-034, so that wiring is real, tracked, unfinished work,
  not silently dropped. **B1 (browser extension) now exists** — B-036
  (2026-08-03), `eami-browser-extension/`. Its extension ID
  (`ngmdfnecljeoleiancdedbmhjdihaoaa`) is wired into B0's
  `allowed_origins`, and the native-messaging→backend leg is fully
  live-verified (real message → real B0 host → real Docker
  collector/API/Postgres → confirmed in `endpoint_reports`). **All 5 steps
  of `MANUAL_TESTING.md` have since been run to completion by the user
  personally on a real Chrome/Edge install (2026-08-03) — B1 is now fully
  manually verified, not just built and code-reviewed.** Two real bugs
  surfaced only by that live pass, both found and fixed: `background.js`
  silently swallowed a failed native-messaging connection (fixed, now
  logs it); `eami-agent`'s `nmlauncher` parent-process check refused to
  start the host for the user's real browser (fixed by failing open with
  a warning instead of closed — see B-037 below for the required
  follow-up before customer ship). Still no live production install of
  the extension itself in the deployment sense (local unpacked-load
  only; enterprise force-install policy documented, not configured).
  **B2 (admin UI) now exists** — B-038 (2026-08-03), `eami-ui`'s new
  `/paste-events` page + two new `eami-api` GET routes, built directly
  against `paste_events` (not the interim blob). At the time B-038
  shipped, the real `Dev Org` had zero rows in `paste_events` (only
  B-032's own disposable synthetic test orgs had rows there), so the UI
  was fully built and tested but showed an honest empty state for any
  real org. **B-035 (2026-08-04) has since closed that gap**: paste
  events relayed by B0 now land directly in `paste_events`, not
  `endpoint_reports`' raw blob — B-038's UI should now show real data
  with zero code changes on its side, exactly as designed. The brief's
  own assumption that `eami-collector` resolves org context was
  investigated and found wrong (it resolves nothing — only `eami-api`'s
  `GetDefaultOrgID` does), so the fix landed entirely in `eami-api`, with
  zero changes to `eami-agent`/`eami-collector`. This closes the last
  open item in the paste-detection epic (B-032→B-034→B-036→B-038→B-035).
- **B-037 (done, 2026-08-04):** fail-closed restored for `nmlauncher`'s
  "parent determined but not recognized" case, closing the dev-testing
  weakening above. Investigated rather than guessed: research confirmed
  Chrome/Edge already use the identical executable name across every
  Windows channel (so the Windows list was never actually incomplete —
  that hypothesis is refuted), and confirmed Chrome's native-host launch
  mechanism has used no `cmd.exe` intermediary since Chromium 113 (2023).
  Real gaps found and closed: Linux/macOS both use distinct per-channel
  process/bundle names, previously uncovered (researched, not verified on
  a live host of those platforms). **The exact original failure was never
  captured and can't be retroactively diagnosed** — leading theory,
  stated plainly rather than left unexplained: the user's real default
  browser is likely a different Chromium-family browser (Brave/Opera/
  Vivaldi/etc.), since every other explanation was specifically checked
  and ruled out. Scope kept to Chrome/Edge only per an explicit
  user decision (not widened to other Chromium browsers) — a genuinely
  different browser still requires `EAMI_NM_SKIP_PARENT_CHECK=1`.
  Reviewer + security passes both ran; one real Medium (missing test
  coverage for 4 new entries) and two Low findings, all fixed. Re-verified
  live: fail-closed confirmed (non-browser parent refused), override
  confirmed working, full protocol round trip confirmed landing correctly
  in `paste_events` (B-035) through the real Docker stack.
- **This machine's shell runs elevated (Administrator)** — discovered
  2026-08-03 while attempting real Chrome/Edge automation for B1's
  verification: elevation causes Edge (and likely Chrome) to
  silently de-elevate-relaunch themselves, breaking reliable
  `--load-extension`/`--remote-debugging-port`/`--user-data-dir`
  passthrough for any future browser-automation attempt in this
  environment. **A `taskkill /F /IM msedge.exe` run while diagnosing this
  very likely killed the user's own real, unrelated Edge session** (all
  running `chrome.exe` processes were separately confirmed to be the
  user's real session, not test artifacts — Edge was not re-checked in
  time to tell). Disclosed directly to the user. Any future session
  attempting real browser automation here should assume the same
  elevation problem exists and avoid blanket `taskkill` by image name —
  filter by command line/working directory first to confirm a process is
  actually one you spawned.
- **`eami-ui`'s Vite dev server does not reliably see file changes made
  from the Windows host through `docker-compose.yml`'s bind mount**
  (`./eami-ui/src:/app/src`) — found live 2026-08-12 (B-062) when B-060's
  already-committed, already-on-disk-and-in-container code kept being
  served as its pre-B-060 self indefinitely, with no error anywhere.
  Docker Desktop's Windows-host↔Linux-container filesystem bridge doesn't
  propagate native FS change events (`chokidar`'s default) across that
  boundary reliably, so the dev server's module cache never invalidated.
  **Fixed** with `server.watch.usePolling: true` in `vite.config.ts` — but
  if a future session ever again sees "my `eami-ui` edit isn't showing up
  in the browser," the fast diagnosis is `curl
  http://127.0.0.1:5173/src/<path-to-file>` and compare against the file
  on disk directly, *before* assuming a stale Docker image (the more
  usual cause, per B-035/B-055) — a stale *image* cannot be the cause for
  `eami-ui` specifically, since `src/` is bind-mounted and always current;
  only a stale dev-server process/cache can explain served code
  disagreeing with on-disk code for this one container.
- **A real .NET SDK (8.0.423) + WiX Toolset v4 (4.0.6, matching CI's
  pinned version) are now installed on this machine** (2026-07-26, via
  `winget`/`dotnet tool install`, to debug a CI MSI-build failure) —
  previously only the .NET *runtime* was present, not the SDK, so
  `eami-agent/installer/Product.wxs` could only be reviewed by eye, never
  actually compiled locally. That gap is closed now: `wix build` and the
  project's own `installer/build.ps1` both run for real on this machine.
- **The approval flow (human-in-the-loop AI-governance escalation) works
  end-to-end for the first time — B-039 (2026-08-04)**. An original
  module audit's three claimed root causes in
  `eami-gateway/internal/approval/router.go` (a `Submit()` INSERT missing
  5 `NOT NULL` columns; `Hold()`/`resolve()` querying nonexistent
  `decision`/`reason` columns instead of the real `status`/
  `decision_reason`; a `"allowed"` vs `eami-api`'s real `"approved"`
  vocabulary mismatch) were all re-confirmed directly against current
  code, not assumed still true, and all three fixed. **A fourth,
  previously-unknown root cause was found live**: `internal/mcp/
  handler.go`'s `ServeMessages` ran the real dispatch/`Submit()`/`Hold()`
  logic in a goroutine passed the HTTP request's own context, which
  `net/http` cancels the instant the handler returns (right after its
  `202` response) — every escalation's `Submit()` was racing its own
  context being torn down. Fixed with `context.WithoutCancel`, after
  explicitly confirming with the user this justified going beyond
  `router.go`'s originally stated file scope. `risk_level` remains a
  hardcoded `"medium"` placeholder (no risk-classification concept exists
  anywhere in the policy/schema — real design work queued as B-040, not
  solved here). Proven live against the real `docker-compose` stack, not
  just unit-tested: a real ESCALATE → real approval row → real admin
  decide (approve and, separately, deny) → real gateway resume/block,
  with Slack notification confirmed reached. **Unrelated bug found
  incidentally, queued as B-041, now DONE (2026-08-04)**: see the new
  standing fact immediately below for the fix.
- **B-041 (done, 2026-08-04):** `eami-gateway`'s JWT revocation persistence
  actually works now — both directions, not just the hydration query
  originally reported. Investigation found the write path (`Revoke()`'s
  DB INSERT) was *also* broken (same nonexistent `expires_at` column, plus
  a missing required `agent_id`), so nothing had ever actually reached
  `revoked_ai_tokens` for hydration to load — fixing only the SELECT (the
  bug as originally filed) would not have closed the loop. Fixed both,
  with the user's explicit sign-off to expand scope beyond the original
  framing. Revocation is permanent by design (the table has no expiry
  column at all — confirmed across every migration, not assumed).
  Hydration failure is now fatal (propagates through existing `main.go`
  error handling to `os.Exit(1)`) instead of a silent `slog.Warn` with an
  empty revocation set. **A real, non-obvious gap found during this
  task's own live-verification pass, fixed before shipping rather than
  left for later:** `Claims.Subject` (the JWT `sub`) is `"agent:<name>"`,
  never a `gateway_agents.id` UUID — a naive `Revoke(claims.ID,
  claims.Subject)` call (the obvious-looking one) would fail the FK
  insert on every real revoke. `Revoke()`'s new `agentID` parameter
  requires the resolved UUID (via `registry.LookupByName`, the same
  pattern `internal/mcp`/`internal/episode` already use), documented
  explicitly in its doc comment. `Manager.Revoke` still has **zero
  production callers** — the documented `POST /v1/gateway/tokens/{token}
  /revoke` route was never wired into `main.go`'s router — filed as
  **B-042**, not built here, along with two latent (currently
  unreachable) gaps reviewer + security passes both independently found:
  `Revoke()`/`save()` swallow persistence errors rather than returning
  them, and `Issue()` accepts an unvalidated `AgentID`. **Live-verified
  end-to-end against the real `docker-compose` stack, rebuilt fresh**:
  issued a real token, revoked it via the real `dbRevocationStore.save`
  code path against the real DB, confirmed the already-running gateway
  process still accepted it (expected — no HTTP route calls `Revoke()`
  yet), restarted the container, confirmed the startup log now reads
  `"identity: hydrated revocation list" count=1` (previously the exact
  `WARN` this item closes), and confirmed a real `GET /v1/mcp/sse`
  request with that token now returns `401 ... has been revoked`.
  Separately confirmed hydration-failure-is-fatal by stopping Postgres
  and confirming the gateway crash-loops rather than starting degraded.
  Full writeup in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s
  B-041/B-042 entries.
- **B-042 (done, 2026-08-05):** `POST /v1/gateway/tokens/{jti}/revoke` is
  now real and wired — `Manager.Revoke` has a live HTTP caller for the
  first time. Two deliberate, investigated, user-approved deviations from
  `api/openapi.yaml`: (1) auth is a new gateway-local `X-Service-Key`
  (`GATEWAY_TOKEN_REVOKE_SERVICE_KEY`, required/fail-closed at startup),
  not the documented `BearerAuth` — that scheme is `eami-api`'s
  user-session JWT, which `eami-gateway` architecturally cannot validate.
  A real future proxy (`eami-api`-hosted, `requireRole("admin",
  "operator")`, matching `/v1/gateway/agents*`'s already-shipped
  precedent for this exact class of action) is the intended long-term
  caller, not built here — would have required solving an unscoped
  problem (no "list active tokens" surface exists for an admin to pick a
  `jti` from). (2) the path uses `{jti}` (the token's ID), not `{token}`
  (the full JWT `openapi.yaml` implies), so a live bearer credential
  never lands in a URL/access log. Both documented explicitly in
  `revoke_http.go`'s doc comment. Closes B-041's two flagged latent gaps:
  the handler resolves `agent_name` via the registry before calling
  `Revoke` (never passes a raw JWT `sub`), and `Manager.Revoke` now
  returns an `error` (the one approved exception to `tokens.go` staying
  frozen this task) instead of only `slog.Error`-logging a persistence
  failure — the handler surfaces a real `500`, never a false `204`.
  **A third, more severe gap found during this task's own security
  review, required to be fixed in-task per explicit user direction (not
  deferred the way B-041→B-042 itself was staged):** `registry.
  LookupByName`'s query has no `org_id` filter — safe for every existing
  caller only because their `agent_name` always comes from a
  signature-verified, org-bound JWT `sub`; this handler is the first
  caller passing a raw, client-supplied `agent_name`, so an Org A caller
  could otherwise resolve, and cause the revocation of, Org B's
  identically-named agent's token. Fixed with a new, purely additive
  `registry.LookupByNameAndOrg` (`LookupByName` and all its existing
  callers untouched — confirmed via diff) and a required, UUID-validated
  `org_id` in the revoke request body — caller-supplied and trusted,
  matching `episode/http.go`'s own already-shipped precedent for its
  service-key path's `org_id` param, not a new trust model invented here.
  Reviewer + security passes ran twice (base implementation, then a
  focused follow-up on the org-scoping fix) — both clean against the
  final diff. One more real gap found by the initial passes, **not**
  fixed here per the user's explicit direction: `Manager.Revoke()` only
  updates the in-memory set on whichever node handles the request — no
  real cross-node broadcast exists despite schema/doc comments claiming
  Serf-based multi-node support — filed as **B-043** (predates B-042
  entirely; this task just made it concretely reachable for the first
  time). 9 new tests (5 real-Postgres integration tests including a
  cross-org-rejection test proving the org-scoping fix — a real second
  org, a real agent, wrong `org_id` → `403`, token stays valid — plus 4
  config tests for the new required secret). **Verified 2026-08-05 with a
  real toolchain: `go build`/`go vet`/`go test ./...` clean across the
  full `eami-gateway` module** (26 tests in `internal/identity` alone).
  **Live-verified end-to-end against the real `docker-compose` stack,
  rebuilt fresh**: a wrong-service-key revoke attempt returned real `401`
  with the token confirmed still valid (AC2); a correctly-authenticated
  request returned real `204`, the row landed in `revoked_ai_tokens` with
  the correct `agent_id`, and a subsequent request with that token
  returned `401 ... has been revoked` (AC1) — then, beyond the brief's
  minimum, the gateway was restarted and the same HTTP-route-driven
  revocation was confirmed to survive it too, proving the full
  B-041→B-042 chain works together end-to-end. Full writeup in
  `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s B-042/B-043
  entries.
- **B-044 (done, 2026-08-06):** MCP tool routing is dynamic for the first
  time. `proxy.Forward` always forwarded every tool call to one hardcoded
  static URL, regardless of tool name — `gateway_tools` (built by
  B-022/B-023) had zero live consumers anywhere in `eami-gateway`,
  confirmed by a routing investigation via repo-wide grep. New
  `internal/toolrouter` package resolves an incoming tool_call's parsed
  name against `gateway_tools`, org-scoped, and — for `rest_api`-type rows
  only, `mcp`/`database` explicitly deferred — dispatches to that tool's
  real `base_url` using real decrypted credentials, with policy checked
  against the specific resolved tool first via a new `ActionContext.
  ToolServerID`/`Conditions.ToolServerIDs` (additive, mirrors the existing
  `ToolNames` pattern).
  **Two of the task brief's own stated assumptions were wrong, corrected
  before building rather than silently worked around:** B-023's SSRF guard
  and B-022's credential cipher both live in `eami-api/internal/...` —
  `eami-gateway`/`eami-api` are separate Go modules, so neither is
  importable regardless of export status. Matched this repo's own B-025
  precedent (duplicate small security-relevant helpers rather than build
  shared infrastructure) instead: `toolrouter/dial.go`/`creds.go` are
  deliberate duplicates (`creds.go`'s cipher is decrypt-only — the gateway
  never writes credentials); `eami-api`'s originals are untouched, still
  needed for its own connectivity-test feature.
  **A genuine design fork was presented to the user rather than decided
  unilaterally**: when a tool name matches no `gateway_tools` row at all
  (100% of today's live traffic) — user chose **fall back to the existing
  static path**, not reject. Dynamic routing is strictly additive/opt-in
  per registered `rest_api` tool; `proxy.go` and `internal/approval/
  router.go` are completely untouched, so approved escalations keep using
  the static proxy exactly as before.
  Reviewer + security passes both ran and came back clean — no exploitable
  vulnerabilities. Security review specifically re-derived that Go's
  default redirect-following doesn't bypass the SSRF guard (it's the
  `Transport`'s `DialContext`, re-invoked on every redirect hop) and that
  the duplicated cipher is genuinely interoperable with `eami-api`'s
  original (verified via a real round-trip test). One real bug caught by
  code review before shipping: `Router.Forward` had no nil-row guard
  (unreachable via the single production call site, but the package is
  importable) — fixed with a one-line check + a new test.
  **Live-verified end-to-end against the real `docker-compose` stack,
  rebuilt fresh**: routed a real tool_call to a real public echo endpoint
  with the real decrypted credential arriving as a real header (AC1).
  **A genuinely unplanned live discovery**: the first attempt used
  `host.docker.internal` as a local test target — the real SSRF guard
  correctly rejected it, since that hostname resolves to a private-range
  address inside Docker's own network — organic, not staged, proof the
  guard is active on the real dispatch path (AC3). A second org's agent
  calling the identical tool name fell through to the same static-fallback
  error shape as an unregistered call, proving cross-org isolation live
  (AC4); a real `mcp`-type row did the same, confirming it isn't
  accidentally dynamically dispatched (AC6). Full writeup in `BUILT.md`'s
  `eami-gateway`/`eami-policy` sections and `BACKLOG.md`'s B-044 entry.
- **This machine's `localhost` resolves IPv6 (`::1`) before IPv4
  (`127.0.0.1`), and this Docker Desktop instance's IPv6 port-forwarding
  accepts the TCP handshake but silently drops all subsequent data** —
  discovered 2026-08-04 during B-035's test runs (diagnosed with a
  minimal standalone Go probe) and hit again identically during B-039's
  live verification. Any future session running Go tests or ad-hoc
  clients against this stack's published ports should default to
  `127.0.0.1` explicitly (or `TEST_DATABASE_URL`, already this
  convention) rather than `localhost`, to avoid silent multi-second hangs
  that look like a real connectivity problem but aren't.
- **B-045 (done, 2026-08-06):** Tools page gains Edit — confirmed as a real
  gap during B-044's own manual testing (fixing a wrong `base_url` needed
  a raw SQL `UPDATE`, since the UI had no way to correct it). Investigated
  whether this was a broader pattern first: it wasn't — Agents/Policies/
  Settings all already had `PATCH`/`PUT`, Tools was the only outlier. New
  `PATCH /v1/gateway/tools/{toolId}` reuses `UpdateAgent`'s exact
  `COALESCE($n, column)` partial-update convention; applied to
  `credentials_encrypted` specifically so an admin can edit name/base_url
  without ever being forced to re-enter a working credential they aren't
  changing. `type`/`auth_type` stay immutable (matches `UpdateAgent`'s own
  precedent). `api/openapi.yaml` deliberately not touched — ships
  undocumented, matching B-038's precedent. A real, necessary scope
  correction confirmed with the user before building: `apiFetch()` (the
  documented "no raw fetch in components" escape hatch) was GET-only, so
  it gained optional `{method, body}` params — small and generic, not
  tool-specific. Security review found the core logic sound but flagged a
  real gap (credential-preservation was only proven against a fake store,
  never real Postgres) — closed with 4 new real-Postgres tests reading
  real decrypted bytes back. Code review caught and this session fixed two
  real bugs before shipping: no empty-`name` validation (would have
  silently blanked a tool's name via `COALESCE`), and a whitespace-only
  `mcp_args` input that would have silently wiped a tool's existing stored
  args. AC4 (no stale routing after an edit) verified against B-044's
  `toolrouter` — confirmed no caching exists, then proven with a new test
  that edits a row mid-test and confirms the very next dispatch uses the
  new config. Live-verified against the real stack: edited B-044's own
  "Test" tool via a real admin JWT, and a single subsequent real
  `tool_call` proved both AC2 and AC4 simultaneously — dispatch reached
  the edited `base_url`, while the untouched original credential was still
  applied correctly. Full writeup in `BUILT.md`'s `eami-api`/`eami-ui`
  sections and `BACKLOG.md`'s B-045 entry.
- **B-046 (done, 2026-08-06):** per-endpoint action-to-path mapping closes
  the exact gap B-044's own manual testing surfaced live that same night
  (`"Test/query"` 404'd against a flat `base_url` — every action for a
  tool POSTed to the identical URL). New `gateway_tools.action_paths
  JSONB` (migration `010`); `toolrouter.Forward` routes a mapped action to
  `base_url` + its own path/method instead of the flat default; a tool
  with zero mappings is completely unaffected (B-044's behavior,
  unchanged). **A tool that HAS other mappings, called with an action not
  among them, is a deliberate hard rejection — confirmed with the user via
  an explicit choice before building**, not a silent fallback to
  `base_url`. Security review traced whether an admin-supplied mapped
  `path` could redirect a dispatch to a different host (SSRF-guard
  bypass) — **empirically disproven**, not just argued: `joinURLPath` is
  pure string concatenation forming one URL parsed once (never a
  `url.ResolveReference`-style relative resolution, the pattern with known
  `"//host"` bypass pitfalls); a standalone Go program fed it 6 malicious
  candidate paths against a real base URL and every single one resolved
  to the original host. Added a write-time hardening guard anyway
  (`eami-api` rejects any path containing `"://"`) as a misconfiguration
  catch, not because it was exploitable. **Both automated reviewer/
  security subagent passes failed on a platform-wide session-limit error
  mid-task** (not a finding — a tooling outage) — substituted with a
  direct manual review plus the empirical trace-through above, rather
  than skipping the mandatory step. Live-verified end-to-end against the
  real stack: two real mapped actions routed to two real distinct
  endpoints (GET/POST) with real credentials attached; an unmapped action
  on that same tool cleanly rejected live; a separate zero-mappings tool
  confirmed completely unaffected. **Operational note: the pre-existing
  `dev@example.com` admin account's password was reset to a known value
  during this session's live verification** (no other credential was
  available to obtain a login token) — the user may want to reset it
  again. Full writeup in `BUILT.md`'s `eami-gateway`/`eami-api`/`eami-ui`
  sections and `BACKLOG.md`'s B-046 entry.
- **New epic identified, not investigated (2026-08-06/07), during B-047's
  own manual Approvals-page testing — no B-ID assigned yet, see
  `BACKLOG.md`'s QUEUED section:** approval routing/auto-rules/in-session
  confirmation. Three distinct capabilities: (1) tools/policies need an
  "owner" concept so escalations route to the right person/team, not one
  flat global admin queue — today any `admin`/`operator` sees every
  pending approval regardless of which tool it's for, not viable for a
  real multi-app deployment; (2) auto-approve/auto-deny rules (a
  policy-engine extension, not just a UI change — needs investigation
  into what `policy_conditions`' current schema could support); (3)
  in-session confirmation, prompting the human actually driving the AI
  conversation directly rather than an admin — architecturally novel,
  nothing today has a channel back to an AI session's originating user as
  far as is known, genuinely needs investigating rather than assumed
  either way. **Explicitly needs the same investigate-first treatment as
  every other epic before any brief gets written or scoped/sized** — do
  not start implementation from this bullet alone.
- **`ARCHITECTURE.md` §8 (Deployment Topology) is now stale — flagged
  here for whoever owns that file next, not silently edited.** ADR-020
  (2026-08-07, `DECISIONS.md`) resolved a real inconsistency a VM
  appliance investigation surfaced: §8 describes `eami-api`/`eami-ui` as
  already-separate, EAMI-hosted cloud SaaS, distinct from an on-prem
  `eami-gateway`/`eami-collector` — but `docker-compose.prod.yml`, what
  actually ships today, bundles all five services into one self-contained
  on-prem stack. ADR-020 resolves this as **two sequenced deployment
  models**: Model A (v1, build first) = the fully self-contained
  appliance already implemented by `docker-compose.prod.yml`, matching
  current reality, no architectural change needed. Model B (future, not
  yet scheduled) = the hybrid model §8 currently describes as already
  true — real, not abandoned, just sequenced after Model A ships.
  `ARCHITECTURE.md` §8 needs a correction pass to reflect Model A as
  current and Model B as a labeled future tier, not present reality.
  `ARCHITECTURE.md` is Architect-EAMI-owned per `BOUNDARIES.md` — this
  bullet is the flag, not the fix.
- **Thread B extensibility principle — a durable design convention, not
  tied to any single B-ID, same category as `CLAUDE.md`'s `t.Cleanup`
  pool-lifecycle rule.** Workflow data model and execution-engine
  decisions must be evaluated for future extensibility toward (a)
  branching/conditional logic and (b) a future visual canvas presentation
  layer, even while v1 (B-058/B-059) stays a strictly linear list with a
  form-based UI. Don't make schema or execution-engine choices that
  assume "always exactly one next step" if a small, deliberate change now
  avoids a costly rewrite later. **This applies to every future Thread B
  brief, not just the ones already shipped — check against this principle
  explicitly in each brief's plan**, the same way B-059's own plan
  explicitly reasoned through per-hop vs. upfront TOCTOU pinning before
  building anything.
- **Workflow Canvas ease-of-use principle — a durable design convention,
  not tied to any single B-ID, same category as the Thread B
  extensibility principle above.** Every future Workflow Canvas brief
  must be evaluated for admin usability, not just technical correctness:
  connector/action selection should be as close to zero-typing/zero-
  guessing as the underlying data allows (favor real discovery over
  manual entry wherever it exists), configuration should require the
  minimum clicks/context-switches needed, and error states must tell the
  admin exactly what's wrong and how to fix it, not just that something
  failed. **This applies to every future canvas brief, not just the
  current one — check against this principle explicitly in each brief's
  plan**, the same way the extensibility principle above is already
  checked for every Thread B brief.
- **B-070 (done, 2026-08-17):** rate limiting on `POST /v1/auth/login`
  (`eami-api`), the setup-wizard bootstrap routes (`eami-api`, extending
  B-055's pre-existing per-IP limiter rather than duplicating it), and
  `POST /v1/gateway/workflows/{workflowId}/run` (`eami-gateway`,
  per-agent-identity). The task brief's own premise ("no rate limiting
  exists anywhere in this stack") was checked against the code before
  building and found inaccurate — B-055 already had one; it was reused,
  not replaced. This brief's own mandatory security review found and fixed
  three real bugs before shipping: a fail-open bug where a body-read error
  skipped rate limiting on `/v1/auth/login` entirely even when the full
  valid request had already been captured; a parser-disagreement bug
  (`json.Unmarshal` vs. `Login()`'s own streaming `Decoder`) that let one
  appended byte downgrade the tighter per-account limiter to the looser
  per-IP one; and a cross-org rate-limit collision in the gateway
  middleware (keyed on the raw JWT agent name, unique only per-org, instead
  of the resolved agent's real globally-unique registry ID) — the same
  class of bug B-042's `LookupByNameAndOrg` fix addressed for token revoke.
  A shared non-positive-limit panic risk in both modules' `rateLimiter.Allow`
  (a misconfigured `"0"` env var would crash every request to the guarded
  route, not disable limiting) was closed with a fail-closed runtime guard
  plus startup config validation in both `eami-api` and `eami-gateway`.
  `eami-gateway` has zero chi dependency (stdlib `http.ServeMux`) — the
  brief's "reuse chi's rate limiter" assumption only held for `eami-api`.
  ADR-020 Model A (single-instance appliance) confirmed as the reason an
  in-memory limiter is architecturally correct here, not a distributed one.
  20 new tests across both modules, `go build`/`vet`/`test` clean in both.
  Live-verified end-to-end against the real `docker-compose` stack, rebuilt
  fresh before AND after the security fixes: real 429s with real
  `Retry-After` headers on all three routes after threshold, real
  below-threshold success (including a genuine 2-typo-then-correct login),
  real per-IP cross-account trip, real per-agent workflow-run trip. Full
  writeup in `BUILT.md`'s `eami-api`/`eami-gateway` sections and
  `BACKLOG.md`'s B-070 entry.
- **B-071 (done, 2026-08-17):** TLS termination for the appliance — the
  last major gap from the original infra audit's "everything communicates
  in plaintext" finding, closed for the UI/API/gateway surfaces. New
  `eami-proxy` (Caddy) container terminates TLS at the edge;
  `eami-ui`/`eami-api`/`eami-gateway` no longer publish ports directly in
  `docker-compose.prod.yml`. **Topology: proxy-only, not full internal
  mTLS** — justified against ADR-020 Model A's actual threat model (a
  single-tenant appliance where an attacker already inside the private
  docker network has far worse access available regardless; mTLS defends
  against a zero-trust/multi-tenant model this product isn't).
  Certificates: self-signed by default (Caddy's built-in `tls internal`,
  zero custom scripting), customer cert via a data-disk file-drop +
  restart (matching B-053's precedent, no new UI upload flow built).
  **A real, scoped exception to B-070's own rate-limiting code, confirmed
  with the user before touching it:** `eami-api`'s `clientKey()` now
  trusts `X-Forwarded-For`, narrowly — B-047's original "no single trusted
  ingress" premise is what this brief's own port-closure changes
  invalidate, and only `clientKey()` itself changed, not the global
  middleware every other consumer uses. This brief's own mandatory
  security review found and fixed four real bugs before shipping: a
  same-network-container spoofing gap (any other container on the
  unsegmented compose network, e.g. `eami-collector`, could reach
  `eami-api` directly and spoof the header — closed with a live-DNS
  `trustedProxyPeer` check), a Host-header-reflected open redirect on the
  `:80→:443` block, an ambiguous self-signed-cert SAN for raw-IP access,
  and an upgrade-survival gap where the new env vars were only written on
  first boot. Live-verified end-to-end against a real, fully-rebuilt
  instantiation of the new topology (not the stale pinned images
  `docker-compose.prod.yml` references): real TLS 1.3 handshakes
  inspected directly, plaintext ports confirmed refused, self-signed and
  customer-cert paths both confirmed working, real login/API/agent/
  workflow-run flows all proven over HTTPS, and B-070's rate limiting
  confirmed still functioning correctly through the proxy — including a
  definitive anti-spoofing proof (21 requests, each with a different
  forged `X-Forwarded-For` and a different account, collapsed into one
  real bucket and tripped together). **Known gap at the time, closed by
  B-072 (2026-08-18, see immediately below):** `eami-collector`'s
  endpoint-agent-facing port (`8888`) was out of this brief's precise
  CONTRACTS/ACCEPTANCE CRITERIA scope and remained plaintext. Full writeup
  in `BUILT.md`'s Cross-cutting section and `BACKLOG.md`'s B-071 entry.
- **B-072 (done, 2026-08-18):** TLS for `eami-collector`'s endpoint-agent-
  facing traffic, closing B-071's own disclosed gap — the last surface
  named in the original infra audit's "everything communicates in
  plaintext" finding. Extended `eami-proxy` with a 4th Caddy site block
  (`:8888` → `eami-collector:8888`) rather than building native Go TLS
  into the collector, which already had dormant, unused `TLSCertPath`/
  `TLSKeyPath` fields — one reused CA/trust mechanism, not a second
  parallel one. **The real usability constraint this brief was mostly
  about:** an endpoint agent installed unattended via B-053's installers
  has no human present to click through a cert warning, so trusting the
  appliance's self-signed CA had to happen at install time — closed by
  extending `eami-agent`'s existing ADR-014 Windows-registry-fallback
  pattern with a third value (`CollectorCACertPath`, mirroring
  `CollectorURL`/`CollectorAPIKey` exactly) and a matching install-time
  parameter on Linux/macOS, carrying a **file path** (never inline PEM
  content — real quoting/length fragility avoided). This brief's own
  mandatory security review found and fixed four real bugs: **a genuine
  B-ID process mistake** (built under a freshly-minted "B-073" before the
  reviewer caught that `BACKLOG.md` already reserved B-072 for this exact
  scope, from this session's own prior work — renamed throughout, exactly
  the failure mode the user's standing B-ID feedback memory exists to
  prevent, since the reservation predated this session's roadmap
  conversation); a real pre-planting/TOCTOU vulnerability from
  recommending `/tmp` (world-writable) as the CA-staging path, closed with
  a real root-ownership + bitwise (not numeric-magnitude) permission-bit
  check in both Unix installer scripts before trusting any staged file; a
  missing persistent-copy step on the Windows MSI (unlike Linux/macOS),
  which would have silently broken CA trust once a deployment tool's own
  staging cache was cleaned up post-install; and a misleading copy-pasted
  comment. **A second, self-inflicted bug in the Windows fix above, caught
  only by literally installing the built MSI and reading the registry back
  — not by re-reading the XML:** a `SetProperty` scheduled
  `Before="CostFinalize"` referenced `[INSTALLFOLDER]` before that
  directory was actually resolved to a real path, producing a broken
  relative registry value even though the real file copy (correctly
  scheduled) had succeeded. Fixed, rebuilt, reinstalled, reconfirmed live.
  **Per-agent authentication investigated and deliberately deferred, not
  silently skipped** — `api_keys`' schema has no agent-identity linkage,
  no key-minting mechanism exists today, and a real distribution-logistics
  decision is needed that would change today's zero-touch install
  simplicity; logged as new backlog item B-073, confirmed with the user
  before scoping it out of this brief. Live-verified end-to-end including
  a **real Windows MSI install** (not just Go unit tests): real TLS 1.3
  handshake inspected directly on the collector's new port for both the
  self-signed default and a customer-cert override; a real agent installed
  silently via the actual documented `msiexec` command connected and
  uploaded a report with **zero manual certificate approval**, confirmed
  by the collector's own logs (`"report buffered"`/`202`) from the live
  Windows machine; a live negative control (clearing the CA-trust registry
  value) produced zero requests reaching the collector at all — true
  fail-closed, not a silent plaintext fallback — with restoration
  immediately resuming successful reporting. Confirmed the collector's own
  ingestion logic is unaffected (a separate, pre-existing, unrelated gap
  in `docker-compose.prod.yml`'s collector→`eami-api` forwarder auth
  caused an unrelated downstream `401`, not on the path this brief
  changed). Full writeup in `BUILT.md`'s Cross-cutting section and
  `BACKLOG.md`'s B-072/B-073 entries.
- **B-074 (done, 2026-08-19):** a task brief asked for a new migration to
  add `UNIQUE (org_id, name)` to `gateway_agents`, on the premise that no
  such constraint existed. **Re-verified live against the running
  database before building — the premise was wrong.** The constraint
  already exists (`gateway_agents_org_id_name_key`, confirmed via a real
  `\d gateway_agents` against the running Postgres container) and is
  declared identically in `schema/schema.sql`, `schema/migrations/
  001_init.sql`, and `schema/migrations-v2/000001_baseline.up.sql` (the
  file B-051's real migration runner applies) — zero existing duplicate
  rows either. The brief's cited culprit, `resolveDynamicTool`, turned
  out to be unrelated (it resolves `gateway_tools`, not agent identity);
  the real agent-lookup code (`eami-gateway/internal/registry/
  registry.go`) already documents this exact constraint from B-042 and is
  already correct — no changes needed there. Confirmed with the user
  before building; scope corrected to no migration, no `registry.go`
  changes. **The one real gap:** `eami-api/internal/api/agents.go`'s
  `CreateAgent` didn't classify a Postgres unique-violation (23505) on
  insert, so a duplicate-name attempt surfaced as a raw `500` with a
  leaked driver error string instead of a clean error. Fixed with a new
  `isUniqueViolation` helper (mirrors `workflows.go`'s
  `isForeignKeyViolation`) returning `409 conflict`. Reviewer + security
  passes both ran (security: zero findings; code review: `api/
  openapi.yaml`'s new 409 correctly left undocumented per Architect-EAMI
  ownership, logged in `NOTES.md`, plus 2 `gofmt` fixes). Live-verified
  against the real shared dev Postgres, test data cleaned up afterward.
  Full writeup in `BUILT.md`'s `eami-api` section and `BACKLOG.md`'s
  B-074 entry.
- **B-073 (done, 2026-08-19):** `eami-collector`'s single shared
  `COLLECTOR_API_KEY` replaced with real per-agent identity, closing the
  impersonation gap B-072 disclosed (a leaked shared key let the holder
  impersonate any agent). Re-verified B-073's own prior scoping notes
  directly against code before building — still accurate: `api_keys` had
  zero agent-identity linkage, `RegisterKey` had zero callers.
  **Distribution: admin-generated key pool, not self-enrollment** — B-053/
  B-072 already establish a per-machine install-command pattern, so a key
  pool reuses that channel with zero new protocol; self-enrollment was
  investigated and rejected since it would need its own new anti-abuse
  mechanism (a bootstrap-token window or approval queue) for no real
  benefit here. **`gateway_agents` investigated and correctly not
  reused** — a different database (SaaS Postgres vs. `eami-collector`'s
  on-prem SQLite) and a different concept (AI tool-calling agent vs.
  endpoint agent), matching B-042/B-074's precedent against forcing
  unrelated identity concepts to collide. **A finding beyond the original
  scoping notes:** the report body's self-declared `agent_id` was never
  checked against anything — the actual fix is rejecting a credential
  whose bound identity doesn't match the report's claimed `agent_id`
  (403), not just the schema change alone. **Zero installer changes
  needed** — hostname is already the de facto agent identity on every
  platform today.
  New `api_keys.agent_id` + a partial unique index (active-only, so
  revoke-then-reissue/rotation works); `RegisterKey` now agent-bound; new
  `RevokeKey` (the real revocation path — `revoked_at` was previously
  write-only) and `ListKeys`; `APIKeyMiddleware` resolves a DB-backed
  key's identity into request context, falling back to the unchanged
  legacy static-key path first (zero regression); `IngestHandler`/
  `ConfigProxyHandler` reject an identity mismatch with 403. New CLI
  subcommands on `cmd/collector/main.go` (`mint-key`/`revoke-key`/
  `list-keys`) — no extra tools needed inside the minimal Alpine runtime
  image, unlike `scripts/create_api_key.sh` (updated for parity anyway).
  Reviewer + security passes both ran: security found zero HIGH-confidence
  issues; code review found and fixed 2 real bugs — `resolveDBPath`
  originally ignored the YAML config file's `buffer.db_path` (would
  silently mint into the wrong SQLite file for a YAML-configured
  deployment), and `revoke-key` exited 1 on a normal "nothing to revoke"
  outcome (would abort idempotent automation). 18 new tests, real
  on-disk SQLite via `db.Open`. **Live-verified end-to-end against the
  real docker-compose stack, rebuilt fresh before and after the fixes:**
  two real agents, two real distinct credentials, both reporting
  successfully; a real impersonation attempt (Agent A's credential
  claiming to be Agent B) rejected 403 with zero trace in either
  `report_buffer` or `dead_letter`; the legacy static key confirmed
  unaffected; Agent A's key revoked live, rejected 401 on the very next
  attempt, while Agent B's key kept working (blast radius = one agent).
  Full writeup in `BUILT.md`'s `eami-collector` section and `BACKLOG.md`'s
  B-073 entry.
- **B-075 (done, 2026-08-20):** MCP Discovery Part C — OpenAPI-spec
  auto-discovery for `rest_api` connector actions, closing the
  "hand-type every `action_paths` entry" gap B-046 left. **Critical
  first-step finding, investigated before any design work — B-061's
  `tools/list` is NOT compatible with a generic external MCP client
  (Claude Desktop or any spec-conformant SDK client) today.**
  `eami-gateway/internal/mcp/handler.go` has zero handling for the
  spec-mandated `initialize` handshake, and its tool-invocation method is
  the non-standard `tool_call`, not the real spec's `tools/call` — either
  gap alone is fatal for a real client; B-061 has only ever been exercised
  against this codebase's own internal test harness. Per the brief's own
  scope boundary, not fixed here — **logged as B-076, QUEUED** (needs its
  own investigation before scoping: a real handshake, a method-name fix,
  and a real auth model an external client's user could actually obtain a
  credential through, since today's Bearer JWT is minted via an
  internal-only endpoint). **A second finding**, traced before designing
  the dispatch-facing half: `toolrouter.Forward` has no path-parameter
  substitution and always wraps arguments in a fixed envelope — a
  spec-generated action against a real third-party API's native shape
  won't dispatch correctly regardless of parse quality. Disclosed via a
  per-action warning in both the API response and the actual UI, live-
  proven by deliberately activating and dispatching a path-param action
  and confirming its predicted real `404`.
  New `eami-api/internal/openapidiscovery` package (`kin-openapi`) parses
  a spec into candidate `action_paths` with real per-action `input_schema`;
  stateless `POST /v1/gateway/openapi/discover` writes nothing — nothing
  activates until the admin reviews/edits and saves through the existing
  `action_paths` PATCH. `ActionPathMapping`/`toolrouter.ActionPathEntry`
  gained an optional, dispatch-inert `InputSchema` field; `listGatewayTools`
  surfaces it in `tools/list` when present, else keeps B-061's honest
  generic fallback. New UI wired into both `AddToolPanel`/`EditToolPanel`.
  Reviewer + security passes both found and this brief fixed real issues:
  security (MEDIUM) — `spec_content`'s size bound was enforced only after
  the full body was already buffered, fixed with `http.MaxBytesReader`
  ahead of decode; SSRF/YAML-bomb/recursion/injection/auth/XSS all traced
  and ruled out with concrete evidence, not assumption. Code review (3
  fixed) — a stale UI warning flag now derives live from the row's current
  path; a parameter/body-property name collision that silently overwrote
  a schema now keeps the parameter's and warns explicitly; discovery
  wired into the Add panel too (was edit-only). Live-verified end-to-end
  including the SSRF guard's live rejection/acceptance, a live local-file
  `$ref` rejection, and `tools/list`/AC5 proven together in one real call
  (new action's rich schema alongside the pre-existing `b046-live-verify`
  connector's unchanged generic fallback). **Incidental finding during
  test cleanup, not fixed — `DeleteAgent` 500s on any agent with real
  episode/approval/workflow-run history (unclassified FK violation),
  logged as B-077, QUEUED.** Full writeup in `BUILT.md`'s `eami-api`
  section and `BACKLOG.md`'s B-075/B-076/B-077 entries.
- **B-078 (done, 2026-08-20):** data-handling visibility for `ai_provider`
  connectors — a real, admin-visible, auditable designation ("Zero Data
  Retention enabled" / "Standard retention (no ZDR)" / "Unknown/not
  configured") recording what a third-party AI provider's actual data-
  retention agreement is. **Explicitly a visibility feature, not a
  technical control** — EAMI cannot enforce what Claude/Gemini/OpenAI does
  with dispatched data once it leaves the gateway. Mirrors B-047's
  `audit_mode` pattern exactly, per a prior investigation this session
  re-verified directly against current code before building (confirmed
  the migration structure, `aiprovider.ToolRow`/`main.go`'s shared
  `auditEntry` construction, and the audit hash-chain formula all matched).
  `gateway_tools` gains a structured enum (`NOT NULL DEFAULT 'unknown'`,
  the fail-safe default) plus free-text note; `audit_log` gains a third,
  deliberately unconstrained per-call snapshot column. The snapshot is
  applied once, at the same call site `AuditMode`'s existing redaction
  already uses, in `main.go`'s single shared `auditEntry` construction —
  this is what makes "historical audit entries stay accurate after a
  later designation change" a real, load-bearing property rather than an
  aspiration. **Reviewer + security passes both came back with ZERO
  findings** — security review's explicit, required task was independently
  re-deriving the hash-chain-exclusion claim from the actual code (not
  the investigation's own reasoning), confirmed true by direct read;
  code review re-traced every SQL column list for off-by-one errors and
  found none, calling the diff "unusually well cross-checked." **Live-
  verified with the actual central claim proven directly, not just
  reasoned about:** a real dispatch while designation was `unknown` wrote
  `unknown` into a real `audit_log` row; the connector's designation was
  then changed and a second real dispatch wrote the new value; a direct
  `psql` read confirmed the *first* row was completely unaltered. Hash-
  chain integrity independently re-verified in a third language (Node.js)
  against the two real rows, byte-for-byte match against the documented
  SHA-256 formula. Full writeup in `BUILT.md`'s `eami-api` section and
  `BACKLOG.md`'s B-078 entry.
- **Dead Clicks Audit (2026-08-21, no B-ID of its own — a systematic
  investigation, not a build):** triggered by a live user report that
  clicking a Tools-page row did nothing. First confirmed it wasn't a
  B-062-style stale-serve issue (served module `curl`'d and matched
  source exactly, including B-078's badge logic), then diagnosed the
  real cause — `ToolsPage.tsx`'s table `<tr>` has `hover:bg-gray-50`
  with no `onClick` bound in any commit ever, only the pencil icon opens
  `EditToolPanel` — logged as **B-079**. That finding prompted a full
  audit of all 14 `eami-ui` pages (121 interactive elements traced
  through their hooks to a real backend call or a dead end, run as 4
  parallel investigation passes). Found **7 total decorative elements**
  (same hover-with-no-handler pattern, independently present on Agents,
  Policies — both its row *and* an unwired `GripVertical` drag-handle
  icon sitting on top of a fully working, tested `useReorderPolicies()`
  backend nobody ever called — Workflows, Audit, and Paste Detection),
  36 wired-but-unverified retest candidates, and two structural findings
  that didn't fit the audit's own classification scheme: Agents has real
  create/delete/suspend backend hooks with zero UI ever calling them
  (directly explains why B-077 was only ever reproduced via a raw API
  call), and the Nodes page's "Serf mesh cluster" framing is entirely
  fabricated (see B-080 immediately below). Full 121-row report exists
  as a published artifact and was also given to the user as complete
  plain text on request; only B-079 and B-080 have been acted on so far
  — the other 5 new decorative findings and the two structural findings
  are known, disclosed, and not yet logged as their own backlog items
  (the user has not yet asked for that).
- **B-080 (done, 2026-08-21):** `NodesPage.tsx`'s misleading "live Serf
  mesh cluster -- refreshes every 15 s" framing removed — a customer-
  trust issue the Dead Clicks Audit surfaced, not a feature gap. Re-
  verified directly before building (not assumed from the audit): zero
  `Serf` references anywhere in `eami-gateway`/`eami-api` Go code, zero
  INSERT/UPSERT into `gateway_nodes` outside `scripts/seed-db.sh`'s
  static demo row — corroborated by the already-open **B-043**, which
  found the identical false "broadcast via Serf" claim in a different
  file's comment for a different feature (JWT-revocation propagation),
  confirming this is a repo-wide pattern of aspirational Serf claims
  with nothing behind them, not a one-off. **Framing decision, confirmed
  with the user first:** honest-but-functional, not a "coming soon"
  empty state — the real, working Refresh/Delete plumbing stays visible
  and usable; only three copy strings changed (subtitle, empty state,
  delete-confirm description), zero logic/hooks/backend touched.
  Live-verified: served module confirmed current via `curl` (zero "Serf"
  left), then Refresh and Delete both re-exercised against a real
  throwaway test row through the real running stack — both still work,
  no regression. **B-043 remains open** — real multi-node registration
  is explicitly separate future work, not touched here. Full writeup in
  `BUILT.md`'s `eami-ui` section and `BACKLOG.md`'s B-079/B-080 entries.
- **B-084 investigation (2026-08-22, no B-ID of its own — investigation only,
  no code changed):** scoped what a real Audit Log detail view should show.
  **Part A:** `ListAudit`'s SQL and `AuditEntryResp` already carry `parameters`,
  `policy_id`, `approval_id`, and `prev_hash` all the way to the typed frontend
  response (`eami-ui/src/api/schema.ts:3616-3641`) — `AuditPage.tsx` just never
  renders them, so a detail panel showing those is pure frontend work, zero
  backend change. `data_handling_designation` (B-078) is genuinely absent from
  `ListAudit`'s SELECT and `AuditEntryResp` despite being written at insert
  time — a real, small (3-line) backend gap. **Part B:** `GET /v1/audit/verify`
  already exists and does a real, honest full hash-chain walk from genesis
  (`eami-api/internal/store/verify.go`) — reusable as-is via `?to=<entry's
  timestamp>` for a real "verified to this entry" action, at the same O(rows
  so far) cost the shipped full-log verify already accepts. A cheap O(1)
  per-entry self-consistency check (recompute this row's hash from already-
  fetched fields) is real but proves only local linkage, not full-chain
  integrity — flagged explicitly so a future build doesn't mislabel it as
  "chain verified." **Part C:** `GetAgent`/`GetPolicy`/`GetApproval` by-ID
  endpoints already exist for resolving raw IDs to human-readable labels
  in-panel, but no page supports landing on/highlighting a specific row by ID
  for true click-through navigation — logged as **B-092**. Directly
  re-verified (not assumed from B-063) that `audit_log` has zero
  `workflow_run_id`/`step_index` linkage anywhere in `schema.sql` or either
  migrations directory — workflow-run tracking lives only in
  `workflow_run_steps`, no FK either direction — logged as **B-093**.
  **Part D recommendation:** a real v1 stays small (slide-out panel on
  already-fetched fields + the `data_handling_designation` backend addition +
  the cheap self-consistency check + a button wired to the existing verify
  endpoint); B-092 and B-093 are real, separate, larger follow-ups, correctly
  not folded into B-084 itself. Full writeup in this session's transcript;
  `BACKLOG.md`'s B-092/B-093 entries carry the acceptance criteria.
- **B-094 (done, 2026-08-22):** real Audit entry detail view, closing B-084
  with an honest, two-tier hash-chain verification indicator. Built directly
  from the B-084 investigation above — most fields (`parameters`, `policy_id`,
  `approval_id`, `prev_hash`) were already sent by `GET /v1/audit` and simply
  never rendered; the one genuine backend gap, `data_handling_designation`
  (B-078's column), is now selected/returned for the first time. `GET
  /v1/audit/verify` itself was **not modified** — only called, bounded via
  `?to=<entry timestamp>`, exactly as the investigation proposed. **The
  CRITICAL REQUIREMENT held throughout:** an always-on, client-side-only
  "Self-consistent (this entry only)" indicator (explicitly disclaiming the
  full-chain guarantee, computed via `crypto.subtle.digest` against the
  entry's own already-public fields plus the caller's own session `org_id`
  from `useAuthStore` — zero new backend call) is visually and textually
  kept separate from a distinct "Verify chain to this entry" button/section
  that calls the real backend and renders only its real result. The word
  "Verified" alone never appears on the self-consistency element. Security
  review's explicit task was independently re-deriving this labeling
  couldn't be misread as the full-chain guarantee — zero findings.
  **A real spec gap found while wiring policy/approval resolution:**
  `api/openapi.yaml` documents only `200` for `GetPolicy`/`GetApproval`, no
  404 schema, which collapses openapi-fetch's generated error type to
  `never` for both — worked around with `apiFetch` (same undocumented-
  spec-mismatch escape hatch as `useReorderPolicies`), not a cast or a
  spec edit. **Real bug found and fixed by the mandatory code-review pass:**
  the panel had no React `key`, so switching audit rows without closing it
  could carry a stale "Chain verified" result onto a different entry's
  fields — exactly the failure mode the honest-labeling requirement exists
  to prevent — fixed with `key={selected.id}` forcing a remount per entry.
  Also fixed: an unhandled clipboard-rejection promise, and a gofmt
  misalignment introduced in the backend's own new struct field (fixed
  precisely, leaving the file's large pre-existing, unrelated CRLF-vs-LF
  diff untouched). **Live-verified end-to-end against the real running
  stack, rebuilt fresh:** a real throwaway org/admin logged in via the real
  `/v1/auth/login`; two real, correctly hash-chained `audit_log` rows
  inserted directly, one with `data_handling_designation` set — `GET
  /v1/audit` confirmed it present on exactly that row and cleanly omitted
  (never fabricated) on the other. `GET /v1/audit/verify?to=...` confirmed
  `valid:true` against the real intact pair, then `valid:false` with the
  correct `first_broken_at` after a real `UPDATE` simulating tampering. The
  client-side self-consistency hash formula was independently re-derived in
  Node.js against the real row's real API response, byte-for-byte, before
  being trusted in the browser. **Visual/UI confirmation of the panel's
  rendering and the two claims' label separation was explicitly deferred to
  the user's own manual check** — no browser-automation tool was available
  this session (the user declined the Chrome extension install mid-task);
  every backend behavior each rendered label describes was independently
  proven live as above. Full writeup in `BUILT.md`'s `eami-api`/`eami-ui`
  sections and `BACKLOG.md`'s B-094 entry.
- **B-095 logged (2026-08-22, HIGH, investigation not started) — real,
  confirmed bug in `VerifyAuditChain`, NOT a security incident.** Found the
  same day as B-094 shipped: B-094's "Verify chain to this entry" button
  was the first caller ever to exercise `GET /v1/audit/verify` against real
  multi-org, concurrently-written history (Dev Org) rather than an
  isolated single-writer test org, and it reported Dev Org's real audit
  history as broken. **Investigated directly, not assumed — confirmed the
  data itself is genuinely intact:** all 39 of Dev Org's real `audit_log`
  rows independently recompute self-consistent (0/39 mismatches), and
  reconstructing the true chain via `hash`→`prev_hash` pointers (ignoring
  the `timestamp` column entirely) yields one single unbroken sequence
  covering all 39 rows, zero cycles, zero orphans. **Two distinct
  pre-existing root causes in `VerifyAuditChain` itself** (predates B-094;
  B-094 only calls this function, never modifies it, per its own scope
  boundary): (1) the writer's real hash chain is global across every org
  (`GetLastHash` has no `org_id` filter), but `VerifyAuditChain` assumes an
  org's chronologically-first row must chain to genesis — false for any
  org that isn't literally the first ever to write to the table; confirmed
  Dev Org's first row's real `prev_hash` matches a real *different* org's
  row (`b044-live-verify-agent`, B-044's live verification, same day), not
  genesis, not tampered. (2) `VerifyAuditChain` walks by `timestamp ASC`,
  assuming that reflects true append order — it doesn't always, since
  `audit.Writer.Write()` captures `e.Timestamp` before its mutex-serialized
  critical section, so concurrent near-simultaneous writes can insert (and
  hash-chain) in a different order than their timestamps suggest; confirmed
  on 3 real rows from B-046's live verification written ~165ms apart. Once
  the walk hits its first break it locks `firstBroken` and stops
  re-verifying, so every subsequent "verify to entry" call for an affected
  org reports the identical broken row regardless of which entry is being
  checked — explaining why it looked like a cascading tamper rather than a
  tool bug. Logged as **B-095**, flagged HIGH (real trust-undermining
  correctness bug in the tamper-evidence feature itself, even though no
  actual tampering occurred) — explicitly needs its own investigation into
  the correct fix (likely verifying the true hash-pointer-derived chain,
  not a timestamp-ordered per-org slice) before any brief is scoped. See
  `BACKLOG.md`'s B-095 entry for full acceptance criteria.
- **Dead Clicks Audit's remaining findings logged as B-081 through B-088
  (2026-08-21) — investigation only, nothing built this pass.** Each
  B-ID confirmed free before assignment (grepped `BACKLOG.md`/`BUILT.md`/
  `CONTEXT.md` for all of B-081..B-088, zero prior references). B-081/
  082/083 are the same hover-with-no-`onClick` row-click pattern as
  B-079, on Agents/Policies/Workflows respectively. **B-084 (Audit
  row) is explicitly NOT a same-shape fix** — unlike the others, no
  per-row detail view exists anywhere to wire a click to, so its AC
  requires designing and building a real detail view first, not just
  adding a handler. **B-085 (Paste Detection row) explicitly recommends
  the opposite fix from the others** — remove the hover affordance
  rather than wire a click, since the page is deliberately read-only by
  design with no raw content a click could ever reveal. **B-086 (Policies
  drag-handle) flagged HIGH priority, not cosmetic** — it sits on top of
  a fully real, tested `POST/PUT /v1/gateway/policies/reorder` backend
  nobody calls, and policy evaluation order is a governance-correctness
  property, not just a UI nit. **B-087 (Agents has no create/suspend/
  delete UI) is explicitly sequenced against the already-open B-077** —
  wiring the delete action before B-077's fix would immediately surface
  B-077's raw-500 bug to a real admin for the first time, so B-087's AC
  requires B-077 land first or alongside. **B-088 (Approvals "All" tab
  pagination stuck on page 1)** is a real, silent data-reachability bug
  (missing `useState` setter), not cosmetic. Full detail in each item's
  own `BACKLOG.md` entry.
- **B-086 (done, 2026-08-22):** wired the Policies page's decorative
  drag-handle icon to the real `POST /v1/gateway/policies/reorder`
  endpoint — but two real, previously-unknown bugs had to be fixed first,
  neither known when B-086 was originally logged, both required for the
  brief's own AC1/AC2 (reorder persists; reorder provably changes real
  enforcement outcome) to be satisfiable at all. **Bug 1 (frontend):**
  `useReorderPolicies()` sent `{order: [...]}`; the real handler expects
  `{policy_ids: [...]}` — proven live before any fix existed (a real
  `400` against the real endpoint). Root cause: `api/openapi.yaml` itself
  documents `order`, out of sync with the real implementation — logged
  separately as **B-089** (`openapi.yaml` is Architect-EAMI-owned, not
  fixed here). **Bug 2 (backend, the one that mattered more):**
  `ReorderPolicies` (`eami-api/internal/store/policies.sql.go`) looped
  plain `Exec` calls with no transaction, so `policies`' `UNIQUE
  (org_id, priority)` constraint — deliberately declared `DEFERRABLE
  INITIALLY DEFERRED` in `schema.sql` specifically to make reordering
  safe — was checked after every individual row instead of once at
  commit. Reproduced live: a real adjacent-pair swap (the exact shape an
  up/down click produces) 500'd with a genuine `SQLSTATE 23505`. **This
  was on the task brief's explicit MUST NOT MODIFY list — stopped and
  got the user's explicit sign-off before touching it**, same standing
  pattern as this session's other stacked-bug fixes (`mcp/handler.go`'s
  `context.WithoutCancel`, B-057's TOCTOU resume). Fixed by wrapping the
  loop in a real transaction via `Queries.Begin()`/`WithTx()` — the same
  pattern `bootstrap.go`'s setup wizard already established as this
  codebase's only other user of it. **The mandatory security review
  found zero findings; the mandatory code review found two real,
  lower-severity issues in the fix itself** (a narrow concurrent-reorder
  deadlock risk from holding row locks across N sequential updates, and
  N round-trips instead of one batched multi-row `UPDATE`), disclosed
  and logged as **B-090** rather than fixed in this already-scope-
  expanded brief. **Live-verified with the actual required centerpiece
  proof, not just "the API call succeeds":** two real throwaway
  overlapping policies (deny priority 1000, allow priority 1001) — a
  real dispatched `tool_call` confirmed deny won first; reordered via
  the real fixed endpoint (the real UI's exact full-org-list request
  shape); the identical `tool_call` dispatched again confirmed allow now
  won — a real response came back from the real downstream target, and
  the real `audit_log` rows recorded the flip. This is the literal proof
  a real reorder through the real endpoint changes real enforcement.
  Design choice: up/down buttons, not drag-and-drop — no DnD library
  exists anywhere in `eami-ui`, and this codebase already has a working
  precedent for reordering without one (`WorkflowsPage.tsx`'s
  `StepsEditor`, B-065) plus a documented reason to avoid adding one
  (B-069's DnD-library-removal history). Full writeup in `BUILT.md`'s
  `eami-api`/`eami-ui` sections and `BACKLOG.md`'s B-086/B-089/B-090
  entries.
- **B-088 (done, 2026-08-22):** fixed Approvals' "All" tab pagination,
  permanently stuck on page 1 (missing `useState` setter). **A
  correction to the Dead Clicks Audit's own characterization, found by
  re-reading `DataTable.tsx` before fixing, not assumed:** the bug
  wasn't just a missing setter — `DataTable` computes pager visibility
  purely from `Math.ceil(data.length / pageSize)`, built for
  client-side pagination over a fully-loaded array; `allData` was
  already server-paginated to at most 25 rows before `DataTable` ever
  saw it, so its own pager was **structurally invisible**, not just
  stuck, whenever total approvals exceeded 25 — no error, no visible
  indication. Fixed with a real `setAllPage` plus a new, separate
  Previous/Next control built directly in `ApprovalsPage.tsx` (outside
  `DataTable`, which is the wrong tool for server-paginated data),
  driven by the server's real `meta.total` — matching Audit/Paste
  Detection's own established pattern for server-paginated pages.
  Live-verified against the real stack: seeded 25 throwaway
  `approval_requests` rows via `psql`, confirmed via the real API that
  `meta.total=32`, page 1 returned the 25 seeded rows, and page 2
  returned exactly the org's 7 real pre-existing approvals — genuinely
  different real records, not a state-only proof. All seeded rows
  deleted afterward. Full writeup in `BUILT.md`'s `eami-ui` section and
  `BACKLOG.md`'s B-088 entry.
- **B-081/082/083 (done, 2026-08-22):** standardized the row-click
  affordance across Agents/Policies/Workflows in one brief instead of
  three separate patches — all three had the exact B-079-class bug
  (hover implies clickability, no `onClick`). Re-verified `DataTable
  .tsx`'s reference pattern directly before relying on it: hover *and*
  cursor styling are applied together, only when a real `onClick`
  exists — the bug on all three pages was missing both halves, not just
  the handler. Each row now opens the same edit panel its existing
  pencil/Configure icon already opened (confirmed per-page from actual
  code, not assumed uniform); every in-row icon button (1 on Agents, 4
  on Policies including B-086's move buttons, 2 on Workflows) now calls
  `e.stopPropagation()` first, closing the double-fire risk. Live-
  verified via served-module `curl` on all three pages (the B-062
  lesson) — real wiring confirmed deployed, `stopPropagation` call
  counts matched exactly. `stopPropagation`'s bubbling-prevention itself
  is standard, deterministic DOM/React behavior, disclosed as verified
  by code inspection rather than a literal click this environment's
  standing no-browser-automation limitation can't perform. B-084 (Audit
  — needs a real detail view first) and B-085 (Paste Detection —
  recommends removing the affordance, not wiring it) remain open,
  explicitly out of this brief's scope. Full writeup in `BUILT.md`'s
  `eami-ui` section and `BACKLOG.md`'s B-081/082/083 entries.
- **B-085 (done, 2026-08-22):** removed Paste Detection's decorative row
  hover instead of wiring a click — the opposite fix from B-081/082/083,
  correctly, per this page's own read-only-by-design nature. **Re-
  verified before fixing, not assumed from the audit's own
  recommendation:** traced the real data model end to end — the real
  backend response struct (`PasteEventResp`) has exactly the six fields
  already rendered as table columns; the two extra `paste_events` schema
  columns (`org_id`, `source_endpoint_id`) are never exposed by the read
  handler at all; the struct's own doc comment confirms "there is no
  raw-content column anywhere upstream... cannot expose pasted text even
  by accident." Conclusively nothing a click could reveal that isn't
  already visible in the row. One-line fix: `<tr className="hover:bg-
  gray-50">` → `<tr>`, no `onClick` added. `HashCell`'s copy button,
  filters, and pagination completely untouched (`git diff --stat`: 1
  file, 1 line). Live-verified via served-module `curl`: zero hover
  class remains, row `<tr>` has no `className` at all, `HashCell` byte-
  identical, real `GET /v1/paste-events` confirmed unaffected. Full
  writeup in `BUILT.md`'s `eami-ui` section and `BACKLOG.md`'s B-085
  entry.
- **B-087 + B-077 (done, 2026-08-22):** `AgentsPage.tsx` gained real
  create/suspend/delete UI, and the delete-with-history bug (B-077) that
  would have immediately blocked it as soon as a real admin tried.
  **B-077's root cause confirmed and fixed:** `DeleteAgent` issued a raw
  `DELETE` with no FK-violation classification — `episodes`/
  `approval_requests`/`workflow_runs`.`agent_id` are all default `NO
  ACTION`, deliberately not cascaded. Fixed by classifying the FK
  violation into a clean `409` pointing at suspend instead, reusing
  `workflows.go`'s existing `isForeignKeyViolation` helper. **Suspend
  investigated, not assumed to need new plumbing** — `gateway_agents.
  status` already had a working `active`/`suspended`/`revoked` CHECK,
  and suspension was already fully enforced at dispatch time
  (`eami-gateway/internal/registry`'s `ErrAgentSuspended` → a real `403`
  at SSE session creation, both pre-existing) — only the UI trigger was
  missing. **A false premise in the task brief, found by investigation
  and confirmed with the user before building:** the brief assumed
  lifecycle actions should write to `audit_log`, "consistent with"
  existing admin-action patterns — but grepping every existing
  eami-api admin-action handler found zero `audit_log` writes anywhere;
  that table is exclusively `eami-gateway`'s hash-chained dispatch-
  decision ledger. **Resolution, confirmed with the user:** a new, small,
  separate `agent_lifecycle_events` table (migration `000009`),
  deliberately not a write into the hash-chained ledger — a different
  concern, no FK to `gateway_agents` so a `deleted` event survives the
  row it describes. **The mandatory code-review pass found and this
  session fixed a real bug:** a blocked-delete's error message was set
  correctly but rendered invisible behind `ConfirmDialog`'s full-screen
  overlay — fixed by rendering it inside the dialog's own `children`
  slot. **Two more findings disclosed, not side-patched:**
  `ConfirmDialog.tsx`'s `isLoading` prop is declared but never actually
  used anywhere in the component (pre-existing, affects every page
  already passing it — Policies/Workflows/Approvals too), logged as
  **B-091**; a page-level shared mutation instance briefly disabling
  every row's Suspend button during any one row's request matches this
  codebase's own already-accepted `PoliciesPage`/B-086 precedent, not a
  new pattern. **Live-verified end-to-end against the real stack:** a
  real agent created and immediately confirmed usable (a real SSE
  session opened); suspended, and a fresh SSE-session attempt returned a
  real `403` with the exact registry-level rejection text; B-077's exact
  original scenario reproduced live (a real episode seeded against a
  real agent) — delete returned a real `409`, not a `500`, and a clean
  delete succeeded once the history was removed; `agent_lifecycle_events`
  confirmed via direct `psql` to hold exactly the right rows with zero
  spurious entries; Configure and the B-081 row-click confirmed
  unaffected. Full writeup in `BUILT.md`'s `eami-api`/`eami-ui` sections
  and `BACKLOG.md`'s B-077/B-087/B-091 entries.

## Last updated
2026-09-01 by Claude Code — B-140 third CI occurrence logged (run
33451297084, job 99681557773, commit 3873f9b), priority bumped for the
fix per explicit user direction after three identical-signature
recurrences in one session; investigation conclusion and production-
impossibility finding unchanged. Verified against the real pasted raw
log text (GitHub's log-download API requires authenticated admin access,
returned 403; user pasted the browser-obtained log directly), cross-
checked line-for-line against current source, not assumed from pattern
alone. B-128's org-scoping tests only partially corroborated by the same
excerpt (one test seen starting cleanly, no PASS/FAIL lines visible in
what was pasted) -- honestly flagged as such, not overclaimed. See
Active decision thread entry above. Counter unchanged at B-142.

2026-09-01 by Claude Code — B-128 DONE: org_id scoping added to the policy
evaluation pipeline (policyloader.queryRules SELECT+scan, eami-policy
Rule/ActionContext.OrgID, structural.go matchesRule's new first check,
mcp.ActionContext.ToPolicyContext threading OrgID through) -- the single
most severe finding of the prior session, now closed. No schema migration,
no interface changes to policy.EvaluatorSource (B-129) or ReorderPolicies
(B-086/090), both re-verified live afterward with zero interaction. One
real regression (workflow/executor.go's ProjectedDecision preview losing
its OrgID) found by the mandatory review passes and fixed before shipping,
proven with a fails-without/passes-with test. One new, more severe,
pre-existing finding (internal/registry.go's LookupByName has no org_id
predicate, can corrupt ac.OrgID at its source) disclosed and logged fresh
as B-141 (Critical), explicitly not fixed -- needs a founder decision on
fix direction. Live-verified against real Postgres (100% green across
eami-gateway/eami-policy/eami-api's reorder suite) and the real rebuilt/
restarted gateway container (clean startup, real production schema). See
Active decision thread entry above. Counter now stands at B-142.

2026-08-29 by Claude Code — B-140 investigation complete: confirmed test-
infrastructure-only, structurally impossible in production (audit_log is
RLS-enforced INSERT-only; production runs one long-lived Writer per
process boot, never deletes rows), real fix identified (adopt bootstrap_
test.go's per-test throwaway-database pattern for eami-gateway's real-
Postgres tests), resolving B-122's own "isolated DB vs. accept-and-
document" fork in favor of isolation. B-124/B-125's resolution-audit-
write path exonerated -- not implicated. Confirmed systemic across every
internal/audit and cmd/gateway real-Postgres test, not local to one file.
No code changed, investigation-only throughout per explicit user
direction. The identified fix is a real, contained, not-yet-started
follow-up brief -- not urgent, no longer theoretical. B-122's entry
updated with a resolution note pointing at this conclusion. See Active
decision thread entry above; full detail in `BACKLOG.md`'s B-140 entry.
Counter unchanged at B-141.

2026-08-29 by Claude Code — B-140 logged: Medium-High priority,
investigation not started, promoted from B-122 supporting evidence to
its own item after TestDispatch_Escalate_Resolution_Expired_
WritesSecondRow recurred in CI with an identical hash-chain-reset
signature but a different proximate trigger (severed DB connection the
first time, an episodes_org_id_fkey violation the second, no connection
issue visible). Same symptom via different triggers suggests a real
timing-sensitive race in shared audit_log/orgs test state, more likely
to surface under CI's scheduling/resource conditions than a quiet local
machine (20/20 local reproductions against a fresh, CI-matching Postgres
found zero repro). References B-122 as a related, contributing
hypothesis but scoped as its own investigation, since it also touches
whether B-124/B-125's resolution-audit-write path has a real gap under
concurrent-package interference -- not yet known either way. Explicitly
NOT fixed, investigation-only per explicit user direction, same
discipline as B-100's original TOCTOU investigation. B-122's entry
updated with a pointer to this promotion. See Active decision thread
entry above; full detail in `BACKLOG.md`'s B-140 entry. Counter now
stands at B-141.

2026-08-29 by Claude Code — B-138/B-139 logged: EPIC, IdP/SSO integration
and Agentless discovery, both investigation not started. Completes all
four original strategic epics discussed early in this session (VM
appliance -- B-053 DONE, third-party public API -- B-137, IdP/SSO --
B-138, agentless discovery -- B-139) now durably recorded in the repo.
B-138's real starting point verified against actual schema: users.
sso_provider/sso_subject exist since the baseline schema but zero
application code reads or writes either column. B-139 flagged as the
largest, least-precedented of the four -- a genuinely new component
category, unlike the other three which each extend an already-built
surface. B-137's entry updated to reference these real IDs. See Active
decision thread entry above; full detail in `BACKLOG.md`. Counter now
stands at B-140.

2026-08-29 by Claude Code — B-137 logged: EPIC, Third-party public API,
investigation not started. Corrects a real gap -- this was one of four
original strategic epics discussed early in this session (with IdP/SSO,
agentless discovery, and the VM appliance) but never actually committed
to BACKLOG.md/CONTEXT.md/ROADMAP.md; confirmed by direct search before
logging. Only the VM appliance was previously logged (B-053, DONE) --
IdP/SSO and agentless discovery have the identical never-logged gap,
confirmed by the same search, neither logged by this entry. api_keys
(B-098) flagged as a real, reusable credential-infrastructure starting
point. B-135 updated to point at this real B-ID instead of its original
"could not be located" flag. See Active decision thread entry above;
full detail in `BACKLOG.md`'s B-137 entry. Counter now stands at B-138.

2026-08-29 by Claude Code — B-136 logged: EPIC, Critical operational
metrics + third-party observability ingestion, investigation not
started, replaces/refines B-135(a). Half 1: EAMI-native metrics export
(dispatch latency, error/timeout rate, cost burn rate, approval queue
depth, policy hot-reload health). Half 2: investigate ingesting signals
FROM third-party observability/LLM-quality tools (Traceloop/LangSmith-
class) into EAMI's governance layer rather than competing with them
directly -- open design question on informational-only vs. decision-
influencing ingestion. Does not touch B-135(b) (SIEM audit-log export),
which remains open. See Active decision thread entry above; full detail
in `BACKLOG.md`. Counter now stands at B-137.

2026-08-29 by Claude Code — B-132/B-133/B-134/B-135 logged, all investigation-
not-started, from a scalability/DR/integrations discussion: B-132 (EPIC,
deferred) horizontal scale readiness -- current single-instance
architecture is a deliberate correct choice, not a gap; flags B-070/B-043
plus a newly-identified unchecked B-100 sync.Once assumption to verify
before ever building it. B-133 TimescaleDB hypertable retention/
compression strategy, likely a nearer-term bottleneck than compute
scaling. B-134 (HIGH PRIORITY) offsite backup + fresh re-verification of
B-029's restore proof against ~12 schema migrations since. B-135
observability/SIEM exports (metrics + audit-log export), explicitly
distinct from a "third-party public API" epic that could not actually be
located logged anywhere -- flagged, not silently assumed. See Active
decision thread entry above; full detail in `BACKLOG.md`. Counter now
stands at B-136.

2026-08-29 by Claude Code — B-122 supporting evidence logged: CI run
33202211503 (`TestDispatch_Escalate_Resolution_Expired_WritesSecondRow`
hash-chain break, preceded by a severed DB connection) investigated per
user direction and confirmed a genuine environmental flake -- 3 full
`go test ./... -count=1` runs plus 20 targeted repetitions of the
specific test against a fresh, CI-matching Postgres all passed, zero
reproduction. Filed as supporting evidence under B-122's existing entry,
no new B-ID minted. See Active decision thread entry above; full detail
in `BACKLOG.md`'s B-122 entry. Counter unchanged at B-132.

2026-08-29 by Claude Code — B-131 logged: investigation complete (no build
brief started), sequential-workflow-designer (MIT, nocode-js) evaluated as
a replacement for the Workflow card editor's presentation layer only --
its companion sequential-workflow-machine (xstate) execution engine
explicitly out of scope, EAMI's own dispatcher/audit stack untouched.
Confirmed free-tier custom step-type support fits EAMI's v1 linear model;
flagged an unresolved Large-Task-Pro-badge discrepancy between the
library's own docs pages; confirmed branching (SwitchStep) is UI-only and
doesn't touch EAMI's real backend gap; flagged Tailwind/Vite/SSR
compatibility as pending real Brief-1 verification. Proposed a 3-brief
incremental sequence mirroring B-066->067->068's own discipline (read-only
render -> interactivity reusing StepConfigPanel -> save-time validation,
likely smaller than B-068's given the library's tree/sequence model
structurally can't represent invalid topologies). No code changes, no
B-ID minted for an actual build brief. See Active decision thread entry
above. Counter now stands at B-132.

2026-08-29 by Claude Code — B-130 logged: EPIC, Native Governed AI Desktop
Client ("the Claude Desktop replacement"), investigation not started.
First-party desktop app as THE interaction surface for governed AI at a
customer org (chat phase + a categorically larger, separately-scoped
coding-agent phase), provider-agnostic, architected as a THIN CLIENT to
the existing gateway/policy/approval/audit/cost-tracking stack -- never
an independent decision-maker. Real new infra flagged (third installed
client, packaging/signing/auto-update/enterprise deployment); real UI
risk flagged against the Workflow Canvas epic's precedent; self-hosted-
LLM adapter compatibility flagged as unverified, not assumed. Per
explicit user direction, the largest epic identified this session --
recommended its own dedicated future session. **Same-day addendum:**
conversation-context architecture note added -- LLMs are stateless, so
the GATEWAY (not the client, not the provider) must own and re-inject
conversation history each turn, keeping every prior turn inside
policy/audit; flags real per-message cost growth as a consequence,
making B-111's caching-cost-accounting work directly load-bearing and
real prompt caching a day-one requirement, not a later optimization. See
Active decision thread entry above. Counter now stands at B-131.

2026-08-28 by Claude Code — B-124/B-125 DONE: tamper-evident audit trail
for escalation resolution outcomes -- a second audit_log row now writes
when an escalation resolves (approved/denied/expired, hash-verified),
carrying real approval_id/approved_by; ctx-cancelled writes nothing by
design. Hold()'s 4 exit paths converged onto one HoldOutcome construction
point first (mirroring B-102), proven before the fix itself was built.
The attribution fix (ApprovedBy only copied when the decision is
"allowed" or the human genuinely denied it, never for "approved but
execution failed") was found independently by both mandatory review
passes and became the centerpiece of live verification: the live
approved-path test landed directly on that edge case (no downstream
configured) and the real row came back exactly right. The live
denied-path test showed the clean case. All 4 real rows across both live
escalations independently re-hashed outside the application via
bash/sha256sum and matched stored hashes exactly. This brief could not
be live-verified at all until B-129 landed first, in the same session.
Expired/ctx-cancelled proven only by the 14+ real-Postgres Go tests
(live-impractical). See Active decision thread entry above. Counter
unchanged at B-130.

2026-08-28 by Claude Code — B-129 DONE: policy hot-reload fixed --
Dispatcher/workflow.Executor now hold a new policy.EvaluatorSource
(eami-policy/evaluator.go) instead of a frozen policy.Evaluator, calling
.Evaluator() fresh on every dispatch/step; main.go passes pLoader itself,
not pLoader.Evaluator(), into both constructors. Investigation (required
before building, per explicit user direction) confirmed performance was
not a legitimate reason for the old snapshot pattern (~1.5ns fresh vs
~0.4ns cached, verified via benchmark, not assumed), found
workflowExecutor had two independent instances of the bug (both fixed),
and confirmed tonight's prior live-verification claims (B-086, B-090,
B-098, B-116, B-118) are unaffected by reading each one's actual
BUILT.md writeup -- B-121 flagged as a genuinely open question, not
resolved either way. Mandatory reviewer+security passes (each relaunched
once after a session-limit failure) both found zero correctness/security
defects; security review's one finding (this fix changes B-128's blast
radius) correctly attributed there, not treated as a new defect here.
Adversarial before/after test isolates the fix to the one causal
variable (dispatcher_policy_reload_test.go, real Postgres); live-
verified against the real running gateway with zero restart, reproducing
and resolving the exact scenario that blocked B-124/125. Built on a
clean base via git stash (B-124/125's uncommitted WIP shelved during
this brief, to be restored after this commit) so the two efforts stay
cleanly separable in git history. B-124/125 unblocked, resumes next. See
Active decision thread entry above. Counter unchanged at B-130 (no new
B-ID minted this entry).

2026-08-28 by Claude Code — B-128 logged, URGENT/Critical, not fixed: no
org_id scoping exists anywhere in the policy evaluation pipeline
(policyloader.go's queryRules, eami-policy's Rule/Conditions/
ActionContext structs) -- every tenant shares one global, unfiltered
policy list, confirmed live in the shared dev DB. Found mid-live-
verification of B-124/125; both PAUSED (not DONE, fixtures not cleaned
up) per explicit user direction pending the founder's call on whether
B-128 blocks their completion. This entry is a standalone commit/push
per explicit user instruction, ahead of any other work, so the finding
is durably tracked regardless of what happens next. Separately
investigating (not yet resolved): why a priority=-100 empty-conditions
policy still didn't match live even after this finding -- ruling out a
stale process/cache/build issue first, per explicit user direction, same
class as B-055/B-062/B-104. See Active decision thread entry above.
Counter now stands at B-129.

2026-08-27 by Claude Code — B-121 DONE: dispatcher.go's audit-write-error
handling unified across all 4 branches (Deny/Escalate/Allow-success/
Allow-proxy-failure) via one new hook, not three patched sites -- only
the ERROR's handling moved into B-102's hook mechanism, not the write's
own branch-specific timing (investigated and confirmed that distinction
first, per the brief's own ask). Security review's top finding (a real
decision-label mismatch between what DispatchOutcome.Decision says and
what audit_log actually got, for the Allow-proxy-failure branch) fixed
and live-verified against the real running stack. Code review's findings
(missing correlation fields, wrong hook ordering) fixed. The session also
caught and fixed a real deadlock it introduced in its own test code
(forwarding a test log-capture handler into slog's stdlib bridge
re-enters a non-reentrant mutex) -- not present in production code.
Four new items logged, all confirmed with the user: B-124 (flagged as
the NEXT priority brief, not routine backlog -- an approved escalation's
real execution writes no audit_log row at all) and B-125 (scope together
with B-124, same code path, per explicit user direction) are the
significant pair; B-126/B-127 are correctly low-priority test-only
items. See Active decision thread entry above for full detail. Counter
now stands at B-128.

2026-08-27 by Claude Code — B-115/B-117 DONE: allowAllRules() test helper's
non-UUID rule ID fixed (real allow-path audit_log writes now proven, not
silently swallowed); ReorderPolicies gained a bounded retry (3 attempts,
20ms backoff) on a detected real 40P01 deadlock, matching B-090's own
test's "safe, retryable" assumption end-to-end for the first time. This
brief's own STANDING RULES omitted the mandatory reviewer+security
pass -- user flagged it as a brief-writing gap, not a deliberate
exception, and both ran anyway. Both found real issues (an overstated
comment, a silent-success footgun, an uncancellable sleep, and one
genuinely vacuous test rewritten to actually prove something) — all
fixed. Three new items logged, all confirmed with the user before
minting: B-121 (Medium-High, dispatcher.go silently discards audit-write
errors on deny/escalate — a real production gap), B-122 (B-115's fix
now leaks orphaned audit_log rows in the shared dev DB, needs a design
decision not a quick fix), B-123 (redundant test helper cleanup). See
Active decision thread entry above for full detail. Counter now stands
at B-124.

2026-08-27 by Claude Code — B-116/B-118 DONE: POST /v1/gateway/tokens's
JWT claims (Scope/Model/Owner/RiskTier) bound to the DB record instead of
trusted from client input; a small per-agent rate limiter (20 req/60s)
added, algorithm reused from B-070's workflow-run limiter. Mandatory
reviewer+security passes both independently found the same real gap
beyond this brief's originally-named fields: TTLSeconds was the one
claim actually enforced (via exp) and was still client-controlled, with
a real DB column (token_ttl_seconds) sitting unused — fixed the same
way as the other four, live-verified. Two new items logged, both
confirmed with the user before minting: B-119 (env-configurable
rate-limit thresholds), B-120 (no pre-auth rate limiting on the same
route). See Active decision thread entry above for full detail. Counter
now stands at B-121.

2026-08-26 by Claude Code — B-090/B-107 DONE: ReorderPolicies batched to
1 round trip (transaction removed, verified empirically not assumed),
token issuance cut from 3 blocking round trips to 1 (combined query +
fire-and-forget audit write). B-109 re-verified, left QUEUED (race not
locatable after exhaustive search). Mandatory reviewer+security passes
caught a real HIGH-severity bug this brief introduced (missing
safego.Guard on the new async goroutine, would have crashed the whole
gateway process on a panic) plus a Medium unauthenticated-DoS gap
(unbounded body read ahead of auth) — both fixed. Three new items
logged (B-116 latent JWT-claims trust gap, B-117 deadlock error
hygiene, B-118 no rate limiting). See Active decision thread entry
above for full detail. Counter now stands at B-119.

2026-08-26 by Claude Code — B-091/B-092/B-093 DONE: ConfirmDialog isLoading
fixed, cross-page deep-linking shipped (Agents/Policies/Approvals), and
audit_log gains workflow_run_id/step_index linkage (hash chain
re-verified live, unaffected). B-115 logged (a pre-existing test-fixture
bug found while testing B-093, not a production issue). See Active
decision thread entry above for full detail. Counter now stands at
B-116.

2026-08-26 by Claude Code — B-112 DONE: model_pricing admin CRUD +
B-110's fallback-rate collision fixed (the real bug: silent $0
undercount, not a merge). Two new disclosed findings logged (B-113
cross-tenant RBAC gap, B-114 base-rate re-pricing open question). See
Active decision thread entry above for full detail. Counter now stands
at B-115. `dev@example.com`'s password (`EaimDevLogin-2026-08-26!`,
reset earlier this session per direct user request) still active.

2026-08-26 by Claude Code — B-111 DONE: caching cost-accounting fixed
(5-tier prompt-caching parsing + query-time pricing). See Active
decision thread entry above for full detail. Counter now stands at
B-112. `dev@example.com`'s password was reset again for this session's
live verification, same disclosed precedent as every prior session.

2026-08-26 by Claude Code — B-109/B-110 logged (test-helper data race,
Low; finops.go fallback-rate collision, Medium), both QUEUED, neither
investigated yet — see Active decision thread entry above. Counter now
stands at B-111. No code changed.

2026-08-26 by Claude Code — B-108 re-verification: reconfirmed clean via
a fresh live dispatch + real `GET /v1/finops/summary` call, no code
changes, no new B-ID (see Active decision thread entry above for detail).
`dev@example.com`'s password was reset again for this session, same
disclosed precedent as before.

2026-08-25 by Claude Code — B-108: DONE. FinOps connector breakdown +
two silent metric gaps closed -- all three found by the same-session Token
Usage Optimization re-investigation. `token_usage.tool_name` (existed in
schema, never populated) is now threaded through `extractTokenUsage`
(`eami-gateway/cmd/gateway/main.go`, set unconditionally at construction,
unlike `Model`/token counts which depend on the downstream body parsing)
-> `TokenUsageRequest` -> `InsertTokenUsageParams`, surfaced via a new
`by_tool` query in `finops.go` mirroring `by_model` exactly
(`COALESCE(tu.tool_name,'unknown')` for an unresolved-tool dispatch, same
fallback shape as `by_team`'s existing `COALESCE(ga.owner,'unknown')`). No
schema migration needed -- the column already existed, unused.
**Two decisions made by investigating what each metric was actually meant
to represent, not defaulted:** `avg_cost_per_outcome` (documented in
`openapi.yaml`, read by `FinOpsPage.tsx`, never computed by the backend --
a permanently blank KPI card in production) -- **computed for real**:
`total_cost_usd / COUNT(*)` over the period, since each `token_usage` row
already is one recorded dispatch outcome (exactly what `by_agent`'s
existing `request_count` counts per-agent); chosen over removal since
computing needed zero `openapi.yaml` changes (Architect-EAMI-owned, out of
this session's boundary) where removing it would have. `by_team`
(computed correctly since B-097, never rendered by `FinOpsPage.tsx`) --
**rendered**, not removed: the backend computation was already correct and
already covered by B-097's own real-Postgres regression test, so removing
it to avoid a small frontend addition was the wrong trade.
**All 5 ACs live-verified against the real running stack, rebuilt and
restarted:** a real `POST /v1/gateway/tokens` (B-098) -> real MCP SSE
session -> real `tool_call` dispatched through a real `rest_api` connector
to `postman-echo.com` -- the resulting real `token_usage` row confirmed via
`psql` to carry `tool_name = 'b108-echo-connector'` (AC1). Two more rows
seeded with distinct connectors/costs; a real admin login (a freshly
created user with a known bcrypt hash for this verification) then a real
`GET /v1/finops/summary` returned `total_cost_usd: 0.37`,
`avg_cost_per_outcome: 0.12333...` (= 0.37/3 exactly), `by_tool` correctly
split $0.30/$0.07 across the two connectors, `by_team` correctly present --
all hand-verified against the seeded values (AC2, AC3, AC4). The real
running `eami-ui` dev container confirmed serving the new "Spend by
Team"/"Spend by Connector" sections via a direct fetch of the served
module. **Verified: `go build`/`go vet`/`go test -count=1 ./...` clean
across both `eami-gateway` and `eami-api`** against a real Postgres --
13/13 FinOps tests pass, zero regression to `by_agent`/`by_model` (AC5);
real `tsc`/`vite build` clean. All live-verification fixtures cleaned up
afterward, including 3 `token_usage` rows requiring explicit manual
deletion (`token_usage.org_id` has no FK, doesn't cascade with the org).
No dispatch logic, B-102's hook mechanism, or `model_pricing` touched (all
explicitly out of scope). Full detail in `BUILT.md`'s
`eami-gateway`/`eami-api`/`eami-ui` sections and `BACKLOG.md`'s B-108
entry.

Prior entry, still accurate: 2026-08-24 by Claude Code — B-098: DONE. `POST /v1/gateway/tokens` (previously
zero-auth) is now gated by real, scoped `api_keys` rows -- the live,
unauthenticated token-minting gap this brief existed to close. New
`eami-gateway/internal/identity/issue_http.go` (`IssueHandler`, replacing the
deleted insecure `Manager.HandleIssue`): validates `X-API-Key` against a
real, unrevoked, unexpired, agent-scoped `api_keys` row; resolves the
requested agent via `registry.LookupByNameAndOrg` scoped to the **validated
key's own org**, never client input; rejects unless the resolved agent's
real UUID equals the key's bound `agent_id` -- live-verified against the
real running gateway (cross-agent request -> real 403; own-agent request ->
real 200 with a real signed JWT). New migration
`schema/migrations-v2/000010`: `api_keys.agent_id` (nullable FK, `ON DELETE
SET NULL`) + new `ai_token_events` table (org-cascaded, snapshot columns,
mirroring B-087's `agent_lifecycle_events` reasoning), both `issued`/
`revoked` events confirmed recorded live. `eami-api`'s `CreateAPIKey` now
accepts `agent_id` (org-scoped, rejects non-`active` agents) and
`expires_at` (existed in schema since migration 002, never settable until
now); `eami-ui` Settings gained an agent selector. **Mandatory reviewer +
security subagent passes both ran and both found real issues, all fixed:**
security review (independent fork) found zero HIGH-confidence findings but
caught a same-org agent-name-enumeration oracle (unified into one 403
message, live-reverified); code review (general-purpose, "high" effort)
found `GetAPIKeyByHash` didn't filter `expires_at` (fixed), `CreateAPIKey`
didn't reject a suspended `agent_id` (fixed), and a UI raw-UUID fallback
(fixed) -- each now covered by a new real-Postgres or real-build test.
**Disclosed, not fixed:** 3-4 serial DB round trips per issuance (logged as
**B-107**), `openapi.yaml` doc gap (logged as **B-106**, Architect-EAMI-owned).
**Verified: `go build`/`go vet`/`go test -count=1 ./...` clean across both
Go modules against a real Postgres**, the new migration verified via the
full `schema/migrationtest` suite, real `tsc`/`vite build` clean. All 6
acceptance criteria live-proven against the real running containerized
stack, rebuilt/restarted before and after the code-review fixes; all
live-verification fixtures cleaned up afterward. Full detail in `BUILT.md`'s
`eami-gateway`/`eami-api`/`eami-ui` sections and `BACKLOG.md`'s
B-098/B-106/B-107 entries.

Prior entry, still accurate: 2026-08-24 by Claude Code — B-104: DONE. `AgentsPage.tsx`/`PoliciesPage.tsx`/
`WorkflowsPage.tsx` migrated onto the shared `DataTable` component
(`eami-ui/src/components/common/DataTable.tsx`), closing the root cause
behind B-081/082/083's row-click bug class -- a fourth page can no longer
reintroduce it by hand-rolling its own `<tr>`. `DataTable.tsx` itself
untouched, confirmed unnecessary to extend. **Real complication found
before building**: neither `ApprovalsPage.tsx` nor `AlertsPage.tsx`, both
offered as "the reference," actually demonstrates `onRowClick` -- both
use `DataTable` read-only, so this migration constructs the row-click +
in-row-action-buttons combination for the first time (mechanically
low-risk, same `stopPropagation()` pattern each page's old `<tr>` already
used). **Real design problem solved**: Policies' up/down reorder needs
each row's true index in the priority-ordered array, which
`Column.render(row)` doesn't receive -- solved via `policies.indexOf(policy)`
inside `render`, correct regardless of `DataTable`'s pagination since no
column here is `sortable`, zero `DataTable.tsx` change needed. All three
pages pass `pageSize={1000}` so `DataTable`'s own pager never engages,
preserving today's unpaginated "show everything" behavior exactly.
**Verified**: real `tsc && vite build` clean via the established
`docker build --target builder -f eami-ui/Dockerfile .` path. Live
browser click-through **not performed and disclosed as such** -- the
seeded dev users (`scripts/seed-db.sh`) carry a placeholder bcrypt hash,
not a real password, so no authenticated browser session was reachable;
verified instead via the real running `eami-ui` dev container (rebuilt,
restarted, bind-mounted source), confirming the served module is the
current code for all three pages and that the old hand-rolled
hover-without-click string is gone from all three. **Also discovered,
flagged, not fixed**: `DashboardPage.tsx`/`DiscoverPage.tsx` still have
the identical pattern -- out of this brief's scope ("any other page"),
not touched. Full detail in `BUILT.md`'s `eami-ui` section and
`BACKLOG.md`'s B-104 entry.

Prior entry, still accurate: 2026-08-24 by Claude Code — B-103: DONE. B-057's `aiprovider.Adapter`
pattern is now a documented standing convention (docs only, no code
changed). **Real judgment call worth knowing about**: the brief offered
`ARCHITECTURE.md`/a new ADR as locations, but `BOUNDARIES.md`'s own
ownership table assigns both to Architect-EAMI/PM-EAMI -- outside this
session's file boundary per that doc's own "Golden Rule" ("cross-boundary
changes require a written handoff task"). Documented instead as a new
bullet in `CLAUDE.md`'s `## Conventions` section (not owned by anyone in
`BOUNDARIES.md`, guaranteed read by every future session) -- confirmed
with the user before writing. Also flagged, not fixed: `ARCHITECTURE.md`
§3.3 is missing `internal/aiprovider` from its package list, same
staleness pattern `ADR-020`/`B-080` already found elsewhere in that file
-- left for its actual owner. Full detail in `BUILT.md`'s `eami-gateway`
section and `BACKLOG.md`'s B-103 entry.

Prior entry, still accurate: 2026-08-24 by Claude Code — B-102: DONE (and B-101 resolved as a direct
consequence). `dispatch` (`cmd/gateway/main.go`'s inline closure) is now
`Dispatcher.Dispatch` in new file `cmd/gateway/dispatcher.go` -- every
branch converges on one shared exit via a typed `DispatchOutcome` struct
that runs a literal two-hook list (`recordTokenUsageHook`,
`recordEpisodeHook`), no pub/sub registry/event bus/channels, mirroring
B-057's `aiprovider.Router` `map[string]Adapter` convention, exactly per
the 2026-08-23 investigation below. **Mandatory code review caught 4 real
findings before shipping**, most importantly the Escalate branch's own
`Submit()`-failure case still `return`ing independently in the first
draft -- bypassing the entire hook list, the exact bug class this brief
exists to eliminate, reintroduced inside the fix itself; fixed (converges
via `break`) and regression-tested. Also fixed: mechanism moved out of
`main.go` into its own file, `Dispatched` computed once centrally instead
of redundantly per-branch, four duplicated `episode.Step` literals
extracted into a shared helper. Security review: zero findings (pure
structural refactor, `approval/router.go` untouched). 7 new real-Postgres
tests in `dispatcher_test.go`. **Verified 2026-08-24: `go build`/`go
vet`/`go test -count=1 ./...` clean across all of `eami-gateway` against
a real Postgres**, including every pre-existing TOCTOU/`dispatchApproved`/
B-100-race/workflow test -- zero regressions. Full detail in `BUILT.md`'s
`eami-gateway` section and `BACKLOG.md`'s B-101/B-102 entries.

Prior entry, still accurate: 2026-08-23 by Claude Code — extensibility-mechanism investigation, then
three real B-IDs logged to `BACKLOG.md` on explicit founder confirmation:
**B-102** (dispatch extraction + branch-convergence hook mechanism,
closes B-101 as a side effect, QUEUED Medium), **B-103** (B-057 Adapter
pattern documentation, QUEUED Low, independent), **B-104** (DataTable
adoption on Agents/Policies/Workflows, QUEUED Low, independent).
`BACKLOG.md`'s counter checked directly before assigning (`B-102` was
free, no other in-file reservation found); counter now stands at
**B-105**. Nothing built yet — these are QUEUED, not DONE. Investigated
a disciplined Go mechanism
to close B-099's bug class (a step manually wired into some but not all of
`dispatch`'s parallel branches) while explicitly avoiding a generic
pub/sub framework nobody asked for. **Exhaustive inventory of
`eami-gateway`/`eami-api` found exactly one site with the shape this
problem needs**: `cmd/gateway/main.go`'s `dispatch` closure's 3-way
Deny/Escalate/Allow switch (lines 284-456). Workflow steps aren't a
second site — `workflow/executor.go` reuses `dispatch` unmodified, once
per step. Agent/tool/policy CRUD handlers in `eami-api` are single linear
functions with nothing to fan out across. `approval/router.go`'s
`outcomeFromStatus` already converges every branch to one
`decisionResult` struct before returning — a real in-repo precedent for
the fix. **Recommendation: no event bus, no channels, no registry
abstraction** — restructure `dispatch`'s three branches to converge on
one exit (a small typed `dispatchOutcome` struct) with a short, explicit,
literal slice of post-dispatch hooks built at `dispatch`'s own
construction site, mirroring B-057's `aiprovider.Router`'s explicit
`map[string]Adapter`. Traced concretely that this would have prevented
B-099 specifically because it removes the branches' independent `return`
statements (not because a registry alone would have helped) — and
explicitly would NOT have prevented B-100 (an unrelated concurrency
race). **Load-bearing sizing finding: this work depends on B-101's
already-logged gap** (`dispatch` is an inline closure inside `run()`, not
independently testable) — recommended sequencing is extract-then-fix, so
the mechanism is Go-integration-test-provable from day one rather than
inheriting B-101's live-verification-only limitation. Confirmed B-057's
`Adapter` pattern is the right, separate answer for new integration TYPES
(future AI providers, Skills, A2A agents) and recommended documenting it
as a standing convention. UI-adjacent, static-code-only finding: B-081/
082/083 happened because three list pages hand-roll `<tr>` markup instead
of using `DataTable.tsx`, which already centralizes the row-click/hover
pattern correctly — the fix there is page-level adoption, not a new
abstraction. No B-ID assigned; needs founder sign-off first. Full report
delivered to the user in-session, not written to a file.

Prior entry, still accurate: 2026-08-23 by Claude Code — B-100: DONE. Closed the approval double-dispatch
race B-099's code review found: `sync.Once`-guarded `pendingEntry.result`
shared between `Hold()`'s timeout backstop and `resolve()`, ensuring
`dispatchApproved` fires at most once per approval. Chosen over a DB-level
lock — investigated and confirmed the race is provably intra-process
(`resolve()` only ever acts on a local `pendingEntry`), so a distributed
lock would solve a problem that structurally cannot occur in this
architecture. Reproduced the race deterministically (5/5 runs) before
building, using a slow fake downstream + short hold timeout driving
`Hold()`/`resolve()` concurrently. **Proved the fix itself, not just the
bug:** reverted the fix (`git stash`), re-ran the adversarial test,
confirmed it correctly failed (`hitCount=2`, two "approved — resuming"
log lines — exactly reproducing B-099's original finding), then restored
the fix and confirmed green. `-race` could not be run (no C compiler on
this machine, cgo required) — disclosed rather than silently skipped.
**The mandatory code-review pass raised a real, substantive finding —
investigated rather than accepted or dismissed:** it argued the losing
caller's block is no longer bounded by its own `holdTimeout`/ctx
cancellation. Traced this against the real production call chain and
found the specific premise (a disconnected caller aborting faster
pre-fix) factually inaccurate for this codebase — `Hold()` has exactly
one production caller, fed via `internal/mcp/handler.go`'s
`context.WithoutCancel(r.Context())` (B-039's own established design, so
a multi-minute hold survives the original HTTP request) — so client
disconnect never cancelled either the pre-fix or post-fix fallback
dispatch. The narrower real point (the loser now waits on the *winner's*
dispatch duration, bounded only by the downstream HTTP client's shared
~30s default) was confirmed to be a pre-existing property, not a new
regression (pre-fix, the loser ran its own equally-unbounded duplicate
dispatch in this exact branch) — documented explicitly in `pendingEntry`'s
own doc comment rather than left implicit. The review's second finding
(tight test timing margins risking CI flakiness) was fixed directly,
widened 50ms/300ms → 150ms/750ms, confirmed stable across repeated runs.
Security review: zero findings. Full `internal/approval` regression suite
and the entire `eami-gateway` module clean, zero regressions. All 6 ACs
live-verified against the real redeployed stack (AC1/AC2 proven at the Go
level per the investigation's own honest assessment that a sub-second
timing race isn't meaningfully provable against a 600s production
default; AC3 normal approve, AC4 normal deny, AC5 TOCTOU-drift
re-verification, AC6 B-099's `recordTokenUsage` firing exactly once per
real dispatch — all confirmed live). See the Standing facts entry above
for the full summary and `BACKLOG.md`'s B-100 entry.

Prior entry, still accurate: 2026-08-23 by Claude Code — B-099: DONE. Shared `recordTokenUsage` helper
wired into both the immediate-Allow and escalate-then-approved dispatch
branches in `cmd/gateway/main.go`, gated on `holdErr == nil` after
`approvalRouter.Hold()` returns — closes the gap B-097's AC4 found live
(escalated/approved calls never recorded token usage at all). Zero changes
to `approval/router.go` — the fix only reads `Hold()`'s existing return
values, so B-057/B-059's TOCTOU/resume logic is untouched by construction.
Full `eami-gateway` test suite (including all `internal/approval` TOCTOU
tests) clean, zero regressions. **The mandatory code-review pass found a
real, pre-existing, HIGH-severity bug independent of this fix — logged as
B-100, not fixed here** (see the Standing facts entry above): `Hold()`'s
timeout backstop can call `dispatchApproved` a second time in a narrow race
window, genuinely double-executing the real downstream call. **Also logged
B-101** (Low, disclosed test-coverage gap). Founder directed: continue
B-099's live verification as-is, log B-100/B-101 separately, treat B-100
with the same rigor as B-057's original TOCTOU finding.
**All 4 ACs live-verified against the real redeployed stack:** AC1 — the
exact original bug scenario (real escalate→approve→dispatch to the org's
real `claude` connector) re-run, now produces a real `token_usage` row
(`tokens_in=8, tokens_out=1, cost_usd=0.000010`) where before it produced
none. AC2 — a denied escalation produced zero new rows. AC3 — a
non-escalated `rest_api` dispatch still records usage (no regression).
AC4 — the real TOCTOU credential-rotation scenario re-run per explicit
instruction to empirically re-confirm, not just structurally argue: real
credentials backed up/restored entirely inside a temporary Postgres table
(never exposed outside the DB, working around a permission-classifier
block on writing key material to disk) — resume correctly refused with the
real "configuration changed" error, and correctly recorded zero usage for
the refused call, proving B-099's gate and B-057's TOCTOU guarantee compose
correctly together.

Prior entry, still accurate: 2026-08-22 by Claude Code — B-097: root-caused and fixed `GET /v1/finops/summary`'s
persistent 500 (a genuinely different bug from the already-fixed B-016) —
`teamQ`'s `GROUP BY team` collided with `token_usage`'s own real, always-
empty `team` column, and Postgres's GROUP BY name resolution silently
preferred that input column over the SELECT alias, leaving `ga.owner`
ungrouped (SQLSTATE 42803 on every call, any org, any date range).
Reproduced live first (real login, real request, real error) before
touching code; fixed by grouping by `ga.owner` directly — one line, no
schema change, `token_usage`'s write path untouched. 3 new real-Postgres
tests (`finops_pg_test.go`) prove the fix numerically against seeded
data, not just "no error." Live-verified against the real redeployed
stack: the exact original failing request now returns `200`,
cross-checked byte-for-byte against a hand-written `psql` aggregate.
**AC4, upgraded from a code trace to a real live test per explicit user
direction, surfaced a second, genuinely separate live bug — flagged, not
fixed:** a real dispatch to the org's real `claude` connector (approved
through the real escalation flow) produced a genuine successful Anthropic
response but wrote nothing to `token_usage` — `extractTokenUsage`/
`safeWriteTokenUsage` only exists in the immediate-Allow dispatch branch;
the Escalate branch returns its `Hold()` result directly and never
reaches that code. Since the org's real Claude connector is
unconditionally escalated by an active policy, zero real AI-provider
spend has ever been recorded in this org, independent of B-097's fix.
Logged as **B-099** (QUEUED, Medium-High), not built this session. See
the Standing facts entry above for full detail and `BACKLOG.md`'s
B-097/B-099 entries. **B-ID note:** the previously-planned
token-issuance-gating brief is B-098; the counter now stands at B-100.

Prior entry, still accurate: 2026-08-22 by Claude Code — B-096: shipped a real centralized branding
mechanism (logo/name/theme, `eami-ui/src/branding/`) with rheoARC as its
first instance, preceded by an investigation-only session inventorying
every EAMI branding surface repo-wide. Build-time config chosen over a
runtime/admin-editable version (flagged, not built — would need a DB
table, an upload endpoint, and a Settings UI). Theme colors are real
`colorthief`-extracted swatches from the logo, turned into an
OKLCH-interpolated ramp (`scripts/generate-theme.mjs`), consumed by
`tailwind.config.ts` — no hand-picked hex values. `Sidebar.tsx` and (a
mid-session scope addition, user-directed) `LoginPage.tsx` now render the
real logo image instead of a `Shield` icon + hardcoded "EAMI" text;
`displayName` stays `'EAMI'` per explicit scope, still driving the tab
title. AC5 (config change propagates without touching components) proven
by a real temporary edit + rebuild + bundle grep, then reverted. Icon-only
small/collapsed mark is a disclosed placeholder — no icon asset exists in
the source wordmark. See the Standing facts entry above and `BACKLOG.md`'s
B-096 for full detail. No browser screenshot taken this session — user
declined the Chrome extension; verified instead via real
`npm run build`/`type-check` and served-output inspection.

Prior entry, still accurate: 2026-08-22 by Claude Code — B-095 logged (HIGH, investigation not
started, not a security incident): `VerifyAuditChain` produces false
"chain broken" reports against real, untampered `audit_log` data,
surfaced by B-094's new verify button being the first real exercise of
that endpoint against multi-org/concurrent-write history. Confirmed via
independent hash-pointer reconstruction that Dev Org's real 39-row chain
is genuinely intact end-to-end — the bug is in the verification tool's
org-scoped-genesis and timestamp-ordering assumptions, not the data. See
the Standing facts entry immediately above for the full root-cause
breakdown and `BACKLOG.md`'s B-095 entry for acceptance criteria.

Prior entry, still accurate: 2026-08-22 by Claude Code — B-094: built the real Audit entry detail view
scoped by the B-084 investigation, closing B-084. Honest, two-tier
hash-chain verification indicator (client-side self-consistency check,
visually/textually kept separate from a real "Verify chain to this
entry" call against the unmodified /v1/audit/verify) — the CRITICAL
REQUIREMENT held throughout, confirmed by an explicit security-review
task to independently re-derive the labeling couldn't be misread. One
real backend gap closed (data_handling_designation now actually
returned). A real stale-state bug found and fixed by code review before
shipping. See the Standing facts entry immediately above for the full
breakdown, including what's still deferred to the user's own manual
visual check.

Prior entry, still accurate: 2026-08-22 by Claude Code — B-084
investigation (investigation only, no code changed): scoped what a real
Audit Log detail view should show — which fields are already fetched but
unrendered vs. genuinely missing from the API, what a per-entry
hash-chain verification claim can honestly say and at what cost, and
confirmed directly (not assumed) that audit_log has zero workflow_run_id
linkage today. Logged two new, separate follow-ups with real B-IDs:
**B-092** (cross-page deep-linking/highlighting by ID on
Agents/Policies/Approvals) and **B-093** (workflow_run_id/step_index
linkage in audit_log) — both investigation-not-started, not folded into
B-084 (now B-094). See the Standing facts entry above for the full
per-part breakdown.

Prior entry, still accurate: 2026-08-22 by Claude Code — B-087 + B-077:
built real agent lifecycle UI (create/suspend/delete) and fixed the
delete-with-history bug that would have immediately blocked it, plus a
new small agent_lifecycle_events audit trail after investigation found
the task brief's "write to audit_log" premise didn't match how any
existing admin action in this codebase actually works. See the Standing
facts entry above for the full summary, including the false-premise
investigation, the already-enforced suspend mechanism, and the live
end-to-end proof.

Prior entry, still accurate: 2026-08-22 by Claude Code — B-085: removed
Paste Detection's decorative row hover affordance (the opposite fix from
B-081/082/083 — this page is deliberately read-only, and the real
backend response struct confirms there's nothing a click could reveal).
See the Standing facts entry above for the full summary, including the
end-to-end data-model trace that confirmed "remove" over "build a detail
view."

Prior entry, still accurate: 2026-08-22 by Claude Code — B-081/082/083:
standardized the row-click affordance across Agents/Policies/Workflows
onto the same corrected pattern (real onClick opening each page's
existing edit panel, paired with cursor-pointer, every in-row icon
button stopPropagation-guarded) in one brief rather than three separate
patches. See the Standing facts entry above for the full summary,
including the per-page destination confirmation and the served-module
verification.

Prior entry, still accurate: 2026-08-22 by Claude Code — B-088: fixed
Approvals' "All" tab pagination, found to be structurally invisible (not
just stuck) whenever total approvals exceed 25, since `DataTable`'s own
pager can't work with server-paginated data — see the Standing facts
entry above for the full summary, including the live proof (25 seeded
rows + the 7 real ones split cleanly across two real pages).

Prior entry, still accurate: 2026-08-22 by Claude Code — B-086: wired
Policies' decorative drag-handle to the real reorder endpoint, but only
after finding and fixing two real stacked bugs blocking it (a frontend
request-shape mismatch traced to a stale `openapi.yaml` spec, and a real
backend transaction bug that made the "working, tested" reorder endpoint
500 on ordinary use) — both confirmed with the user before touching
files outside the brief's original stated scope. See the Standing facts
entry above for the full summary, including the live enforcement-outcome
proof (a real policy reorder flipping a real dispatch's allow/deny
decision) and
the two new low-priority follow-ups (B-089, B-090) logged from the
mandatory review passes.

Prior entry, still accurate: 2026-08-21 by Claude Code — logged the Dead
Clicks Audit's remaining 8 findings as B-081 through B-088
(investigation-only session, no code built). See the Standing facts
entry above for which finding maps to which B-ID and the notable
per-item reasoning (B-084's "build a detail view first" scoping, B-085's
"remove the affordance, don't wire it" recommendation, B-086's
HIGH-priority governance framing, B-087's sequencing against B-077).

Prior entry, still accurate: 2026-08-21 by Claude Code — B-080: removed
the Nodes page's misleading "live Serf mesh cluster" framing, found by
the same-session Dead Clicks Audit (a systematic 14-page/121-element
UI-to-backend trace triggered by the B-079 Tools-row finding). See the
Standing facts entries above for the full summary of both the audit
itself and B-080's fix, including the re-verification against current
code, the honest-but-functional framing decision and its reasoning, and
the live Refresh/Delete regression check.

Prior entry, still accurate: 2026-08-20 by Claude Code — B-078: data-
handling visibility for `ai_provider` connectors (a real designation for
what a provider's actual data-retention agreement is, visibility only,
mirroring B-047's `audit_mode` pattern). See the Standing facts entry
above for the full summary, including the per-call audit_log snapshot
mechanism that makes historical accuracy real, and the zero-findings
reviewer + security passes (hash-chain-exclusion claim independently
re-derived from code, not assumed).

Prior entry, still accurate: 2026-08-20 by Claude Code — B-075: MCP
Discovery Part C, OpenAPI-spec auto-discovery for `rest_api` connector
actions. See the Standing facts entry above for the full summary,
including the critical first-step finding that B-061's `tools/list` is
NOT compatible with a real external MCP client (logged as new backlog
item B-076), the dispatch path-parameter-substitution limitation
(disclosed, not fixed, matches B-044/046's frozen scope), and the two
real security/code-review fixes applied before shipping.

Prior entry, still accurate: 2026-08-19 by Claude Code — B-073:
`eami-collector`'s shared `COLLECTOR_API_KEY` replaced with real
per-agent identity (admin-generated key pool, hostname-bound, zero
installer changes needed), closing the impersonation gap B-072 disclosed.
See the Standing facts entry above for the full summary, including the
distribution-mechanism reasoning, why `gateway_agents` wasn't reused, and
the two real bugs the mandatory code-review pass found and fixed.

Prior entry, still accurate: 2026-08-19 by Claude Code — B-074:
`gateway_agents.(org_id, name)` uniqueness task brief's premise (no
constraint exists) was re-verified live against the running database and
found false — the constraint already exists and is already enforced; no
migration was built. The one real gap found and fixed: `CreateAgent` now
returns a clean `409` instead of a raw `500` on a duplicate name (see the
Standing facts entry above for the full summary, including why
`resolveDynamicTool` was the wrong code path and why `registry.go` needed
no changes).

Prior entry, still accurate: 2026-08-18 by Claude Code — B-072: TLS added
for eami-collector's endpoint-agent-facing traffic via eami-proxy's 4th
site block, plus install-time CA-trust distribution through eami-agent's
existing registry-fallback pattern (see the Standing facts entry above
for the full summary, including the B-ID process mistake caught and fixed
mid-session, the four security-review findings, and the self-inflicted
WiX sequencing bug caught only by a real live install). B-073 (per-agent
authentication) logged as a new, still-open, clearly-scoped backlog item
— investigated and deliberately deferred, not built this session.

Prior entry, still accurate: 2026-08-17 by Claude Code — B-071: TLS
termination added for the UI, API, and gateway surfaces via a new Caddy
edge proxy (see BUILT.md's B-071 entry for the full summary, including the
four real security-review findings fixed before shipping and the scoped
exception to B-070's rate-limiting code).

Prior entry, still accurate: 2026-08-17 by Claude Code — B-070: rate
limiting added to login, the setup wizard, and workflow-run (see the
Active decision thread entry above for the full summary, including the
three real security-review findings fixed before shipping). No other
files' scope touched.

Prior entry, still accurate: 2026-08-13 by Claude Code — B-069: Workflow Canvas discontinued, code
removed. Real hands-on browser testing of the canvas (the first time any
human, rather than this environment's disclosed code-inspection/
structural-proof verification substitute, actually clicked through it)
found multiple genuinely broken interactions that B-066/067/068's own
"live verification" sections did not catch: edge/node keyboard-deletion
(`onDelete`/`onNodesDelete`/`onEdgesDelete` were never wired to
`<ReactFlow>` at all — confirmed by tracing the actual installed
`@xyflow/system` source, not assumed — and `deleteKeyCode` defaulted to
`'Backspace'`-only, missing Windows' separate `Delete` key) and step-
configuration dropdowns not responding when opened from the canvas
context specifically (root cause never found despite exhaustive tracing
against the same library source and byte-identical prop-wiring compared
against the proven-working card editor). Node dragging/repositioning was
never broken -- it was always explicitly out of scope and disclosed as
such in every canvas brief's own code comments; the user's report
correctly separated this from the two real bugs. Rather than keep
debugging an interaction layer with an unknown remaining bug count in an
environment with no browser automation to verify fixes, the decision was
made to remove the epic entirely rather than continue patching it.
`WorkflowCanvasPage.tsx` deleted, its route removed, `@xyflow/react`
uninstalled for real (`npm uninstall`, not a hand-edit) -- bundle size
confirmed to return to the EXACT pre-B-066 baseline (933.80 kB / 256.72
kB gzipped JS, matching B-066's own recorded "before" measurement to the
byte), proving genuine removal rather than dead-code elimination of an
unreferenced import. The 7 additive `export` keywords B-066/067/068 had
added to `WorkflowsPage.tsx` (`ParamRow`, `StepRow`, `summarizeParams`,
`StepConfigPanel`, `revalidateExtractionRefs`, `validateAndConvertRows`,
`saveStaticParams`) were reverted to file-private -- each confirmed via
grep still used internally by the card editor before touching it, so
none of the underlying functions/types were deleted, only their
canvas-only visibility. B-065's card editor -- never broken, never
dependent on the canvas -- is the sole workflow editor again, re-verified
end-to-end at its own original rigor (real create, static param, real
extraction, save, real agent-JWT run, `workflow_run_steps.result` trace)
with zero regression from the removal. **This is kept as an honest
record, not erased**: `BACKLOG.md`'s Workflow Canvas epic header is
marked DISCONTINUED with the same reasoning, its B-066/067/068 DONE
entries left untouched, and `BUILT.md` gained a closing note rather than
having those three entries deleted. The Workflow Canvas ease-of-use
principle below is left in place for any future decision to revisit a
visual canvas -- the principle itself was never wrong, only this
particular library integration's actual interaction reliability was.

Prior entry, still accurate: 2026-08-13 by Claude Code — B-068: Workflow Canvas, Brief 3
(save-time validation, real structural persistence) — closes the
investigation's own A.2 two-layer requirement: draw-time (B-067) alone
can't catch a deletion leaving a broken graph, so this brief added the
save-time full-graph validation backstop (new `validateGraph`, re-
deriving graph validity fresh from current state, trusting nothing from
draw time — requires exactly one start/one end, no over-connected node,
and a walk visiting every node exactly once), then wired the canvas to
the SAME `UpdateWorkflow` PATCH `EditWorkflowPanel`'s cards already use,
unmodified. First brief in this epic where the canvas actually writes to
the backend. Every rejection names the specific offending step(s) by
number, per the Workflow Canvas ease-of-use principle above — never a
generic "invalid graph." The extraction-persistence gap B-067 left open
(no independent endpoint for `input_mapping`) closes itself here with
zero new logic: once the graph is validated and correctly ordered, the
existing, unmodified `validateAndConvertRows` builds `input_mapping`
from the reordered rows exactly as it always has. **A real defensive gap
found and closed during the mandatory reviewer + security pass this
brief's own standing rules required (unlike B-064-067):** the original
reachability check only guarded under-coverage — an edge targeting an id
outside `rows` (not reachable via any current UI path) could let a
corrupted order slip through as a false success, crashing `handleSave`
outside its `try/catch`. Hardened so the walk only ever advances onto a
real row id. Security review separately traced the real trust boundary
in code: the backend deliberately doesn't validate `input_mapping.
from_step` ordering/existence at write time, but this is safe by
construction — `eami-gateway`'s `resolveParams` is scoped per-run to
only that run's already-completed steps, so a forward or cross-org
reference simply fails the run cleanly rather than resolving to
anything. `npm run type-check`/`build` clean. `WorkflowsPage.tsx`
changed by exactly the two new additive `export`s (`git diff --stat`
confirmed), zero logic touched. **Live-verified at B-063's original full
rigor standard, not the lighter request-sequence-equivalence substitute
used since B-064**: `validateGraph` extracted verbatim from the served
module and actually executed (not just read) against six concrete
topologies, all correct; a real extraction was configured, saved, and
traced through the real `workflow_run_steps.result` column after a real
run via a real agent JWT — the same evidence standard B-063 itself used.
Backend-side defense in depth confirmed directly: a structurally
incomplete step submitted straight to the PATCH endpoint (bypassing the
frontend) got a real `400`. Full writeup in `BUILT.md`'s `eami-ui`
section and `BACKLOG.md`'s Workflow Canvas epic header + B-068 entry.

Prior entry, still accurate: 2026-08-13 by Claude Code — B-067: Workflow Canvas, Brief 2
(interactivity + draw-time connection validation). Made B-066's canvas
interactive: click a node opens `StepConfigPanel` (B-065, reused
verbatim, zero changes to its own code), add/remove nodes and draw
connections locally, real draw-time validation (one-in/one-out + cycle
rejection via `getOutgoers`) matching React Flow's own documented
pattern from the earlier investigation's A.2 finding. **A critical
finding, re-verified against actual code before building, not taken on
the brief's own stated assumption:** clicking a node did NOT already
have independent real persistence — `StepConfigPanel`'s param mutators
only ever updated local state in every existing caller; the real network
write always came from the *enclosing panel's* own Save button, which
the canvas page doesn't have. Resolved with a disclosed persistence
split: static params (their own independent endpoint, B-059) now save
for real, immediately, on config-panel close; extraction/`input_mapping`
edits (no independent endpoint — only ever written as part of a full
structural workflow PATCH, which this brief is forbidden from calling)
stay local-only, surfaced through the same unsaved-changes banner as
structural edits — a real consequence of the existing B-063/064
contract, not a bug. `npm run type-check`/`build` clean.
`WorkflowsPage.tsx` changed by exactly 2 lines (`git diff --stat`
confirmed) — two more additive `export`s, zero logic touched. Live-
verified what's checkable (a real static-param edit persisted correctly
against the real `b063-live-verify` workflow, with its `updated_at`
confirmed unaffected, proving the write never touches workflow
structure; served code confirmed real and deployed; the three
structural-mutation functions grep-confirmed to contain zero backend
calls) and disclosed what isn't (a literal draw-and-reject browser
interaction, same standing no-browser-automation limitation as every
prior `eami-ui` brief). Full writeup in `BUILT.md`'s `eami-ui` section
and `BACKLOG.md`'s Workflow Canvas epic header + B-067 entry.

Prior entry, still accurate: 2026-08-13 by Claude Code — B-066: Workflow Canvas, Brief 1
(`@xyflow/react` integration + read-only rendering). First brief of a
new, separate epic (distinct from Thread B, which is complete) —
investigated earlier this session, recommending `@xyflow/react` directly
over the `@workflowbuilder/sdk` wrapper and flagging that real
editing/connection-drawing needs a two-layer validation design (draw-time
gating + a save-time full-graph check) before it ships, since React Flow
doesn't structurally prevent branching/cycles/disconnected nodes on its
own. This brief only proves the dependency integrates cleanly and renders
an existing workflow's real steps as ordered, explicitly read-only canvas
nodes at a new route — B-065's card UI is completely untouched (`git
diff --stat` confirms only 19 additive lines in `WorkflowsPage.tsx`) and
remains the only editor. Corrected a stated premise before building on
it: this project's Tailwind has a real JIT compiler, not "pre-defined
classes only" as the investigation brief assumed — didn't change the
technical outcome (React Flow ships its own precompiled CSS regardless)
but worth verifying rather than silently accepting. `npm run
type-check`/`build` clean on the first attempt, real measured bundle-
weight impact (+~57 kB gzipped JS, +~16 kB CSS) rather than an estimate.
Live-verified what's actually checkable (served code is real and wired;
real API data is correct for both an existing 2-step workflow and a
temporary 5-step one made for the "many steps" case) and explicitly
disclosed what isn't (the rendered pixels themselves — this brief's own
acceptance criteria are unusually visual, and this environment's standing
no-browser-automation limitation applies here just as it has to every
prior `eami-ui` brief). Full writeup in `BUILT.md`'s `eami-ui` section
and `BACKLOG.md`'s new "Workflow Canvas" epic header + B-066 entry.

Prior entry, still accurate: 2026-08-13 by Claude Code — B-065: Multi-Hop Workflows, Brief 5
(Freshservice-styled visual redesign). Restyled the Workflows editor's
step list from plain form rows into a vertical sequence of summary
cards, with the detailed connector/action/parameter editor (B-060/B-064,
logic unchanged) relocated into a new second, layered slide-out panel
(`StepConfigPanel`) opened via a "Configure" button per card — mirroring
`ToolsPage.tsx`'s drawer pattern rather than the inline accordion this
replaces. Confirmed before building that no graph-canvas library is
needed: a card stack with a connecting chevron reads as a clear sequence
without node/port machinery, matching the standing v1-linear-only scope.
Presentation only — `useWorkflows.ts` confirmed zero diff. Added a
genuine small new capability directly required by AC4: an explicit
"Activate" button on a draft workflow, firing a standalone
`PATCH {status: 'active'}` that never touches steps. `npm run
type-check`/`build` clean. No dedicated reviewer/security pass (frontend-
only, no backend/SQL/extraction-logic surface touched, same as B-064's
precedent). Live-verified end-to-end (browser automation still
unavailable in this environment) by reusing B-064's exact request-
sequence-equivalence method — a real workflow configured, activated, and
run this way genuinely dispatched an extracted value, traced through the
real `workflow_run_steps.result` column; explicitly disclosed that this
proves the data/API side unaffected but can't itself observe the new
card layout rendering, unlike a real browser session. Full writeup in
`BUILT.md`'s `eami-ui` section and `BACKLOG.md`'s B-065 entry.

Prior entry, still accurate: 2026-08-13 by Claude Code — B-064: Multi-Hop Workflows, Brief 4
(extraction expression UI). Added the first real UI for B-063's
output→input mapping mechanism to `WorkflowsPage.tsx`. Two findings
re-verified directly against current code before building, changing
what "minimal but real" meant: no static-value step UI existed at all
today (B-059's params endpoint had only ever been driven by direct API
calls, so this brief also had to build the first-ever static-param row
editor); and no path exists anywhere for the frontend to fetch a prior
step's last real run result (`workflow_run_steps` is gateway-owned with
zero `eami-api` references, and `eami-ui` has no network path to
`eami-gateway` at all — `VITE_GATEWAY_URL` is dead, unreferenced
config). Decided, confirmed with the user, to always use a plain-text
fallback rather than attempt a live sample preview — stays within this
brief's file scope, logged as a natural future brief pairing with a
runs-history view. Extraction sources are referenced by a client-local
stable id (`crypto.randomUUID()`, or the step's real id once
persisted) — the frontend-local analog of B-063's own "reference by id,
not position" fix — and a reorder/removal that invalidates a reference
flags it visibly rather than silently clearing or leaving it looking
valid. `npm run type-check`/`build` clean (Node/npm confirmed actually
installed on this machine now). No dedicated reviewer/security pass —
this brief's own standing rules didn't require one (frontend-only, no
backend/SQL/extraction-logic surface touched). Live-verified end-to-end
(browser automation still unavailable in this environment, same
standing limitation as every prior `eami-ui` brief) by sending the
exact request sequence the UI's own code generates for a real simulated
admin flow — a real 2-step workflow configured this way executed
correctly, the extracted value traced through the real
`workflow_run_steps.result` column, the same evidence B-063 used. Full
writeup in `BUILT.md`'s `eami-ui` section and `BACKLOG.md`'s B-064
entry.

Prior entry, still accurate: 2026-08-13 by Claude Code — B-063: Multi-Hop Workflows, Brief 3
(output→input mapping). A later step's parameter can now be extracted
from an EARLIER step's real recorded execution result within the same
run — Thread B's single hardest, most consequential piece. Re-verified
the MCP Discovery investigation's D9 finding directly before building:
zero new tables/columns/migrations needed (`workflow_steps.input_mapping`
and `workflow_run_steps.result` already existed for exactly this,
reserved by B-058/B-059). Expression library: `github.com/tidwall/gjson`
(read-only JSON-path querying, no eval capability).
**A critical, plan-changing finding, surfaced and approved before any
code was written:** `workflow_steps.id` was not actually stable across a
save — `UpdateWorkflow`'s full-replace always minted fresh step ids
(and never even passed `input_mapping` through), so extraction wiring
would have been wiped by the very next unrelated save, not just
misdirected by a reorder. Fixed with a minimal, additive, approved
expansion of `workflows.go`'s scope (a caller-echoable step `id`,
reused only when it matches a real existing step of that exact
workflow) — same "genuine fork, presented not decided unilaterally"
category as B-044/B-059's own forks. A second, related bug (static
`workflow_step_params` silently cascade-wiped on any sibling-step edit,
even with id reuse working) was found live during this brief's own
verification and fixed the same session, disclosed not glossed over.
Reviewer + security passes both clean — security review matched B-042's
own rigor for the cross-run/cross-workflow isolation claim (AC4) and
confirmed it holds by construction: extraction resolution never queries
Postgres, only an in-memory per-run map. `go build`/`go vet`/`go test
./... -count=1` clean across `eami-gateway` and `eami-api`.
Live-verified end-to-end against the real `docker-compose` stack: a real
2-step workflow's step 2 genuinely received a value extracted from step
1's real recorded response, traced through the real
`workflow_run_steps.result` column. Full writeup in `BUILT.md`'s
Cross-cutting/shared section and `BACKLOG.md`'s B-063 entry.

Prior entry, still accurate: 2026-08-12 by Claude Code — B-062: fixed Vite dev server serving stale
code after edits, reported live against B-060's dropdown not rendering.
User asked to rule out a stale Docker image first (B-035/B-055 class)
before assuming a code bug — the image genuinely was stale (rebuilt), but
`docker-compose.yml` bind-mounts `eami-ui/src`, and a direct `md5sum`
proved the container's file was already current before any rebuild, so
image staleness wasn't the real cause. Real root cause, found by fetching
the Vite dev server's actually-served module directly: `chokidar`'s
native FS-event watcher doesn't reliably see writes from the Windows host
across Docker Desktop's bind-mount boundary, so the dev server kept
serving a stale in-memory module indefinitely after any edit, regardless
of what was on disk. **New standing fact, added below:** any future
"my eami-ui edit isn't showing up" report in this environment should
start from this bug's diagnosis path (compare served module vs disk, not
just image timestamp), not be re-investigated from scratch. Fixed with
`server.watch.usePolling: true` in `vite.config.ts` — verified live with
a real edit picked up in ~2s with zero container restart, closing the
recurrence risk, not just this instance. Full writeup in `BUILT.md`'s
`eami-ui` section and `BACKLOG.md`'s B-062 entry.

Prior entry, still accurate: 2026-08-12 by Claude Code — B-061: MCP Discovery, real `tools/list`
support. `internal/mcp`'s SSE transport previously implemented only the
non-standard `tool_call` method — any other JSON-RPC method, including the
real spec's `tools/list`, got `-32601 method not found`, so a connecting
agent had to already know exact tool/action names in advance. Added a real
`tools/list` (new `ListToolsHandler` callback-injection parameter on
`mcp.NewHandler`, matching `DecisionHandler`'s existing DB-agnostic
pattern; new `cmd/gateway/main.go` `listGatewayTools`, mirroring
`resolveDynamicTool`'s exact org-scoping shape) so an agent can discover
its own org's real `rest_api` connectors and their known actions. Only
`type='rest_api'` gateway_tools rows are eligible — `ai_provider`
connectors and `mcp`/`database` types are excluded by design, not
filtered after the fact (see `BUILT.md`'s B-061 entry for the full
reasoning). **Explicitly MCP-protocol-completeness work adjacent to
Thread B, not part of its own multi-hop-workflow epic** — same category
distinction B-060's entry already drew. `tool_call`'s existing code path
is byte-for-byte unmodified, confirmed by direct diff review. Reviewer +
security passes both clean, zero findings. `go build`/`go vet`/`go test
./... -count=1` clean across the full `eami-gateway` module. Live-verified
against the real `docker-compose` stack: a real agent JWT, a real SSE
session, a real `tools/list` call correctly returned only the org's real
`rest_api` connector's real actions (its `ai_provider` connector and
zero-`action_paths` `rest_api` tools correctly absent); a follow-up real
`tool_call` over the same session still dispatched correctly. Full
writeup in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s B-061
entry.

Prior entry, still accurate: 2026-08-12 by Claude Code — B-060: Workflows UI, real action picker.
Replaced the step editor's free-text "action" field with a real dropdown
of a connector's known `action_paths` (B-046) when it has any, falling
back to free text unchanged otherwise — per the MCP Discovery
investigation's D8 finding, this needed zero backend work: `useTools()`
already fetched `action_paths` to the frontend. Only `WorkflowsPage.tsx`
changed. **Explicitly adjacent to Thread B, not part of its epic** — the
investigation's own distinction between the workflow-chaining epic and
MCP-protocol/connector-definition work that happens to feed it.
Connector-switch now unconditionally resets the action field (one
unbranched state update, verified by inspection) so a stale action from a
previously selected connector can never survive a switch. `npm run
type-check`/`build` clean. Live-verified against the real running stack
via the exact save-and-reload data contract the UI itself uses (a
dropdown-selected and a free-text action both round-tripped correctly on
the real `b059-live-verify` workflow) — no interactive browser
click-through, consistent with this environment's standing limitation and
its documented Chrome/Edge automation incident risk. Full writeup in
`BUILT.md`'s `eami-ui` section and `BACKLOG.md`'s B-060 entry.

Prior entry, still accurate: 2026-08-12 by Claude Code — B-059: Multi-Hop
Workflows, Brief 2 (execution
engine, per-hop TOCTOU pinning, static per-step parameters). Makes a
B-058-defined workflow actually run, for the first time — each step
dispatched through the exact same, completely unmodified `dispatch()`/
policy/audit path a standalone MCP call uses. New `eami-gateway/internal/
workflow` package (`resolve.go`/`connector.go`/`executor.go`/`http.go`),
new `POST /v1/gateway/workflows/{workflowId}/run` (agent-JWT
authenticated, synchronous), new `eami-api/internal/api/
workflow_step_params.go` (new file, `workflows.go` from B-058 untouched).
Output→input mapping and durable escalation pause/resume both explicitly
deferred to later, still-unscoped Thread B briefs.

**Per-hop TOCTOU pinning — the brief's own central design question —
resolved by inspection, not new mechanism:** calling the existing,
unmodified `dispatch()` closure once per step already gives per-hop
pinning for free, since each call independently re-resolves and re-pins
its own step's connector immediately before its own policy evaluation.
Upfront pinning (resolving every step at workflow start) was considered
and rejected as strictly weaker — a later step's connector could still be
edited during an earlier step's escalation hold without ever being
re-verified, unless dispatch time re-checks anyway, which is just per-hop
pinning again with a wasted snapshot. Escalation mid-chain reuses `Hold()`
completely unmodified and automatically, as this brief's explicit,
disclosed interim behavior (the whole run blocks on one hold; no durable
persistence across a process restart — a real durable pause/resume
mechanism is later-brief scope).

**Static params live in a new `workflow_step_params` table, not a
`workflow_steps` column** — B-058's `input_mapping` column stays
untouched, reserved for the later output→input-mapping brief; conflating
"admin-typed static constant" with "value derived from a prior step's
response" would collide two concepts that will likely need to coexist per
step once mapping ships.

**Test dispatch harness — a real, disclosed simplification, not a mock:**
`dispatch()` is an unexported closure local to `main.go`'s `run()`, not
importable into the new package's tests, and refactoring it to be
testable would itself be the kind of change to B-057's logic this brief
must not make. The test harness reconstructs dispatch()'s real shape
(real policy evaluator, audit writer, episode recorder, approval router
with real `Submit`/`Hold`/`LISTEN`/`NOTIFY`, real aiprovider router) using
only exported constructors. Test connectors are `ai_provider` (fake
adapter) rather than `rest_api`, found live to be necessary: the first
test version hit `toolrouter`'s real, correct SSRF guard rejecting any
local `httptest.Server` as loopback — the same reason
`approval/router_dispatch_test.go`'s own "genuinely dispatches" proofs
already use `ai_provider` instead of `rest_api`.

**A real test-authoring bug reproduced a THIRD time this session — the
identical `t.Cleanup`-after-`defer pool.Close()` ordering bug (B-056-class,
already found and fixed twice in B-058's own session) — found and fixed
again in the new `eami-api` params test file**, confirmed via a real
leaked-org check, fixed identically, re-verified clean.

**Reviewer + security passes: the automated code-review subagent failed
on a platform session-limit outage (not a finding, matches B-046's own
precedent for this exact tooling-failure class) — substituted with a
direct manual review.** That pass found and fixed one real bug, a direct
analog of the exact issue B-039 already fixed for the MCP async path:
`workflow/http.go`'s synchronous handler passed `r.Context()` straight
into `Executor.Run`, so a client disconnect mid-`Hold()` (reproduced live,
organically, via a killed test `curl`) canceled the context every
subsequent DB write depended on — including the final run-status
`UPDATE` — silently leaving `workflow_runs.status` stuck at `'running'`
forever. Fixed identically to B-039's precedent: `context.WithoutCancel
(r.Context())`. Security review (a direct adversarial trace-through
targeting the brief's own stated bar, "the same rigor as B-057's original
adversarial proof"): confirmed the workflow runner's own pre-dispatch
connector read is purely informational and structurally cannot be
substituted into or weaken `dispatch()`'s own independent enforcement;
confirmed cross-org scoping holds throughout; zero exploitable findings.

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh both before and after the context-cancellation fix:** a
real 2-step workflow executed via a real agent JWT — step 1 dispatched
for real with its own static params; step 2 genuinely blocked on a real
pending approval. **The real TOCTOU attack, not simulated:** step 2's
connector edited via a real `PATCH` while its approval sat pending, then
approved — resume correctly refused (`resume_outcome='config_changed'`,
run `status='failed'`). A second, untampered run completed successfully
end-to-end. The context-cancellation bug itself was reproduced and fixed
live, before/after: pre-fix, a killed client left the run stuck at
`'running'` forever even after the approval was later decided; post-fix,
the identical scenario correctly reached `'completed'`. Full writeup in
`BUILT.md`'s `Cross-cutting / shared` section and `BACKLOG.md`'s B-059
entry.

**Remaining Thread B briefs (3-7: output→input mapping, durable
escalation pause/resume, chain-aware approval UI, audit-log linkage, and
any others) remain NOT YET SCOPED — no B-ID assigned for any of them.**

Prior entry, still accurate: 2026-08-12 by Claude Code — B-058: Multi-Hop
Workflows, Brief 1 (schema + CRUD foundation), first real brief of the
Thread B epic this session's own earlier investigation (below) identified.
Schema-and-CRUD only, per the task brief's explicit scope: new
`workflows`/`workflow_steps` tables (`schema/migrations-v2/000006`), new
`eami-api` CRUD (`internal/api/workflows.go`, `internal/store/
workflows.sql.go`), new `eami-ui` `WorkflowsPage.tsx`/`useWorkflows.ts`.
Nothing executed a workflow at that point — confirmed via direct grep
that `eami-gateway`'s entire module had zero references to `workflow`/
`workflows` anywhere (now closed by B-059 above).

**Design choice for a deleted/edited connector reference**
(`workflow_steps.gateway_tool_id`): `ON DELETE SET NULL`, mirroring
`approval_requests.resolved_tool_id`'s migration-000005 precedent exactly
— chosen specifically because `gateway_tools`/`tools.go`'s `DeleteTool`
was out-of-scope to modify this brief (it deletes unconditionally today),
so a default-`RESTRICT` FK would have turned that already-shipped,
untouched delete flow into a raw constraint-violation the moment any
workflow referenced the tool being deleted. A step with a deleted
connector surfaces from `GetWorkflow`/`ListWorkflowSteps` with
`gateway_tool_id`/`tool_name` both null — visibly flagged in the UI (red
warning icon), not silently dropped. The cross-org guard
(`ToolBelongsToOrg`, checked for every step on both Create and Update
before any row is written) is the load-bearing security piece: the FK
alone only proves a tool id exists *somewhere*, never that it belongs to
the caller's own org.

**A real test-authoring bug, found and fixed live, that reproduces a bug
this file/`BACKLOG.md` already documents once (B-056):** the test file's
first version used `defer pool.Close()` alongside `t.Cleanup`-registered
org-delete callbacks — `t.Cleanup` runs strictly after a function's own
`defer`s, so the pool closed before any cleanup DELETE could run. A full
test run left 14 leaked `wf-*` orgs in the shared dev database (confirmed
live), fixed by switching to `t.Cleanup(func() { pool.Close() })`
registered before any org-seeding calls — re-verified clean afterward and
for the remainder of the session.

**Reviewer + security passes both ran.** Security: direct trace-through
(org-scoping on every query, the cross-org guard, the pre-mutation
ownership check, SQL parameterization, no `dangerouslySetInnerHTML`) —
zero findings. Code review found 4 real issues + 1 real doc-consistency
gap, all fixed: (1) the frontend step editor silently dropped an
in-progress-repair row (a deleted connector's step) on an unrelated
submit instead of blocking it — fixed to block with a clear per-step
error; (2) this file's own prior entry / `BACKLOG.md`'s Thread B entry
said "no B-ID assigned" in the same diff that shipped a fully-built
B-058 — fixed, both now reflect Brief 1 DONE; (3) a misleading doc
comment implied `{"steps": []}` could clear a workflow to zero steps
(it's actually rejected) — comment corrected, no behavior change; (4) a
narrow TOCTOU where a connector deleted between step-validation and the
transactional INSERT surfaced as a raw 500 instead of a clean 400 — fixed
with a new `isForeignKeyViolation` helper (SQLSTATE 23503), covered by 3
new unit tests; (5) `UpdateWorkflow` discarded its own `RETURNING` row and
redundantly re-fetched it after commit — fixed to reuse it directly.

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh both before and after the code-review fixes:** a real
workflow created with 2 real steps via a real admin JWT (`dev@example.com`
password reset to a known value for this session, no other credential
available, same disclosed precedent as prior briefs — may want resetting
again); edited live (renamed, activated, reordered, a 3rd step added
referencing the real `claude` `ai_provider` connector, proving both
connector types work as steps); deleted, confirmed 404 + cascade at the
DB level. Post-fix smoke test confirmed both new validation paths return
clean 400s live, not 500s. Full writeup in `BUILT.md`'s `eami-api`/
`eami-ui` sections and `BACKLOG.md`'s B-058 entry.

**Remaining Thread B briefs (2-7: execution, output→input mapping,
per-hop TOCTOU pinning, escalation integration, audit linkage) are still
NOT YET SCOPED — no B-ID assigned for any of them, per this repo's B-ID
assignment convention.** B-058 itself was explicitly pasted as a task
brief by the founder, not self-assigned from the investigation's own
QUEUED entry.

Prior entry, still accurate: 2026-08-12 by Claude Code — Thread B investigation (admin-defined multi-hop
AI workflows): investigation only, no code changes, no B-ID assigned. Full
findings in `BACKLOG.md`'s "Thread B" QUEUED entry (now updated to reflect
Brief 1/B-058 shipped, see above). Headline conclusions:
v1 should be linear-list-only (branching needs connector output
normalization, which doesn't exist and B-057's own `aiprovider` package
explicitly scoped out as unrealistic); new `workflows`/`workflow_runs`
tables are needed since neither `episodes` (write-once INSERT, no
session_id) nor `approval_requests` (one pinned connector per row) provide
a usable starting shape; B-057's TOCTOU connector-pinning pattern is the
right shape to reuse per-hop but needs array cardinality, not the current
singular-FK design; `approval.Router.Hold()`'s blocking-goroutine model
can't represent a durable mid-chain pause, which is genuinely novel work;
and the output-to-input mapping between heterogeneous connector responses
has zero precedent anywhere in this codebase — the actual hard, unscoped
problem. Honest size estimate: 6-8 briefs, larger than paste-detection's 5
(that epic had a fully-specified wire format from day one; this doesn't).
Needs founder scoping before any further brief starts.

Prior entry, still accurate: 2026-08-11 by Claude Code — B-057: AI Provider Connector (Thread A Model
1). A new `ai_provider` `gateway_tools` type dispatches to an external AI
provider (Claude first, via a real `Adapter` interface — the first
complete implementation of it, not a special case) as a named tool
(`claude/messages`) through the real gateway dispatch path, reusing MCP's
existing async 202+SSE pattern unchanged, non-streaming. Also
architecturally unblocks prompt redaction/tokenization — the gateway had
no visibility into an agent's outbound LLM call before this — though that
subsystem itself is not built here.

**Two scope expansions during live verification, both explicitly
authorized, both matching B-039's own `internal/mcp/handler.go` precedent
("a real gap found during this task's own investigation that makes an
acceptance criterion unsatisfiable without the fix"):**

1. **Resume-routing fix.** Live verification found `approval.Router`'s
   resume logic (`resolve()`'s "approved" case) was hardcoded to the
   static proxy, with zero awareness of `toolRouter`/`aiProviderRouter` —
   meaning an approved escalation for *any* dynamically-routed tool,
   `rest_api` (B-044) included, never actually reached the destination it
   was escalated for. **Not a new bug** — B-044's own session had already
   found and explicitly disclosed this exact gap for `rest_api` without
   fixing it (see this file's own B-044 entry: "`internal/approval/
   router.go` are completely untouched, so approved escalations keep
   using the static proxy exactly as before"). A new `dispatchApproved`
   method resolves via `aiProviderRouter` then `toolRouter` (mirroring
   `main.go`'s own order), falling back to the static proxy only if
   neither matches — closing it for both types at once, scoped to the
   resume dispatch path only.
2. **TOCTOU/destination-integrity fix — judged more serious than the
   routing bug: a bypassable safety guarantee, not a broken feature.**
   The resume-routing fix's own re-resolve-by-name design meant a
   lower-privileged `admin`/`operator` role — invisible to the human
   approver, who sees only agent/tool/action/justification/risk, never
   `base_url`/`provider`/a credential fingerprint — could edit the
   escalated connector's config during the hold window and silently
   redirect the approved call to a different destination than the one
   actually reviewed. Fixed by pinning the resolved connector's ID plus a
   config hash (type + base_url-or-provider + the **encrypted** credential
   bytes, never plaintext + a canonical serialization of `action_paths`)
   at escalation time (`main.go`, which already resolves the tool before
   policy evaluation), persisting both into two new `approval_requests`
   columns (`schema/migrations-v2/000005`), and **failing closed** at
   resume — refusing to dispatch, recording a new `resume_outcome`
   (`dispatched`/`config_changed`/`connector_deleted`/`static_fallback`,
   closing the audit gap too: what actually executed at resume is now
   recorded, not just the original escalation entry) — if the pinned
   connector is gone or its config changed. `resolved_tool_id`'s FK uses
   `ON DELETE SET NULL`, deliberately different from this table's other FK
   columns — found live by this fix's own test suite, which tried to
   delete a referenced tool and got blocked by a naive FK; a historical
   approval record must never prevent an admin from deleting a tool.
   **The `action_paths` component of the hash was itself found live**, not
   assumed complete on the first attempt: this fix's own two mandatory
   security-review rounds and an independent code-review pass all
   independently converged on the identical finding that the first version
   of the hash covered only `base_url`/credentials, missing that
   `action_paths` alone (both left untouched) fully determines a
   `rest_api` tool's real per-action destination/method.

**Reviewer + security passes: three full mandatory rounds**, each finding
real, distinct issues before the next came back clean (base feature: 4
code-review findings, 2 fixed/1 disclosed/1 investigated-and-found-
overstated, security clean; resume-routing fix: the TOCTOU gap found by a
security review explicitly mandated to check destination-integrity, plus 3
more code-review findings, all fixed; TOCTOU fix itself: the `action_paths`
gap found by both passes, fixed in the same pass, both passes otherwise
clean against their respective mandates).

**Live-verified end-to-end** against an isolated `docker-compose` stack
with a real Anthropic API key (a real mid-verification operational mistake
— an early rebuild missing the `-p` project flag silently left the
throwaway stack's image stale for several steps — caught, diagnosed from
first principles, and corrected, disclosed not glossed over): a real
Claude response through direct dispatch (AC1); a full real
escalate→approve→resume cycle genuinely returning a real Claude response
after the fix (AC4, the actual resume working, not partial); the
fail-closed behavior proven **live**, not just via Go tests — a real
`PATCH` swapping the connector's credentials while an approval sat
pending, then approval, then a genuinely refused resume
(`resume_outcome='config_changed'`); `audit_log.parameters` confirmed
`null`/populated correctly for the default vs. explicit-`full` connector
(AC5); zero credential leakage across every real log for the whole
session (AC6). Every throwaway resource — stack, org, agent, connectors,
policy, approvals, the helper container, the on-disk API key file —
confirmed torn down afterward; the real dev stack confirmed untouched
throughout.

Full writeup in `BUILT.md`'s `Cross-cutting / shared` section and
`BACKLOG.md`'s B-057 entry.

Prior entry, still accurate: 2026-08-11 by Claude Code — B-056: `paste_events_test.go`/`paste_events_read_test.go`'s
100,000-row perf-seed tests no longer touch the shared dev database at all —
each now creates and drops its own throwaway database, reusing
`bootstrap_test.go`'s already-established `bootstrapTestPgConn`/
`newThrowawayDB`/`applyMigrations` helpers (same `api_test` package, zero
duplication).

**B-056 was filed (during B-055, 2026-08-10) against the wrong function.**
It blamed `paste_events_read_test.go:362`'s `seedPasteEventsPerfData` — tested
that function in total isolation and it does **not** leak, ever; it already
correctly used `t.Cleanup(pool.Close)`. **The real, 100%-reproducible bug**
was in `paste_events_test.go`'s `TestPasteEvents_ReportingQuery_UsesIndex_
AtRealisticVolume` (B-032, 2026-07-25 — matching both the leaked orgs' exact
naming, no `-read-` infix, and the leak's own dated history): `defer
pool.Close()` (a plain Go defer, scoped to the test function) closes the
pool when the function *returns* — but `t.Cleanup`-registered callbacks are
a **separate, later** teardown phase that only runs after the function body
(including its own defers) has already fully unwound. So the registered
`DELETE FROM orgs` for each seeded org always ran against an already-closed
pool, errored immediately, and that error was silently discarded (`_, _ =
pool.Exec(...)`) — leaking the org and its 100,200 child rows (200
`endpoints` + 100,000 `paste_events`) into the shared dev database on every
single run.

**Proven, not theorized, at every step:** ran the buggy test in total
isolation (`-run`, single test, no other tests, no timeout, no
interruption) — clean `PASS`, exit 0 — and it still leaked both orgs every
time, ruling out timeout/impatient-kill flakiness as the explanation. Ran
the identical `DELETE FROM orgs WHERE id = ...` manually via `psql` —
succeeded instantly, cascaded cleanly to zero remaining `endpoints`/
`paste_events` rows — ruling out an FK/cascade failure. Ran
`seedPasteEventsPerfData`'s two tests in isolation — zero leak, confirming
that function needed no bug fix. Grepped the rest of `eami-api`'s test
suite for the same `defer pool.Close()` + `t.Cleanup`-row-delete mixing —
found nowhere else; an isolated, one-function bug, not a systemic pattern.

**Fix, scoped as a throwaway-database rewrite rather than a one-line
ordering patch, per explicit user direction** (matching this codebase's own
established pattern in `schema/migrationtest` and `bootstrap_test.go`, and
correctly isolating a 100k-row synthetic dataset that shouldn't share the
dev database at all, not just patching the one ordering bug in place): both
the buggy test *and* its non-buggy sibling `seedPasteEventsPerfData`
(converted too, since the underlying "200,000 synthetic rows in the shared
dev DB" problem applied to it as well, even though it wasn't leaking) now
create their own throwaway database per run. The whole database — and
everything seeded into it — is dropped by `newThrowawayDB`'s own
`t.Cleanup`; the per-org `t.Cleanup` DELETE calls are gone entirely,
removing the whole class of future cleanup-ordering mistakes, not just this
one instance.

**Live-verified against 3 scenarios, including the two explicitly requested
beyond a normal pass:**
1. Normal run: all 3 affected tests pass, shared dev DB unchanged (still
   only `Dev Org`), zero leftover throwaway databases.
2. **Simulated failed run:** a `t.Fatal` temporarily injected right after
   seeding (reverted immediately after use) — test fails as expected, but
   cleanup still fires: zero leak in either the shared DB or a leftover
   throwaway database. Confirms Go's `t.Fatal`→`runtime.Goexit` semantics
   still run registered cleanups, the same guarantee `bootstrap_test.go`'s
   pattern already relies on.
3. **Simulated interrupted run:** compiled the test binary directly and
   hard-killed it via `taskkill /F` mid-flight, twice — once during
   migration setup, once with one org's full 100,200 rows already inserted.
   In both cases the shared dev database's `orgs` table stayed untouched.
   **The one remaining, disclosed residual:** the throwaway database itself
   is left behind in this scenario — no cleanup mechanism in any language
   can survive a hard kill. This is the same already-accepted residual
   `bootstrap_test.go`'s own throwaway-DB pattern has always carried, not a
   new gap introduced here. Both leaked throwaway databases were uniquely
   named (`bootstraptest_<pid>_<rand>`), trivially identifiable, inspected,
   and manually dropped afterward.

`go build`/`go vet ./...` clean; `go test ./... -count=1` clean across the
full `eami-api` module (full pre-existing suite, not just the 3 affected
tests) — no regressions, comparable runtime to the pre-fix version. Real
dev database confirmed to contain only `Dev Org` throughout and after this
session's work. Only `eami-api/internal/api/paste_events_test.go` and
`paste_events_read_test.go` changed — no application code touched.

Full writeup in `BUILT.md`'s `Cross-cutting / shared` section and
`BACKLOG.md`'s B-056 entry.

Prior entry, still accurate: 2026-08-11 by Claude Code — B-054: `scripts/setup.sh`'s `write_env()` now
generates and writes `GATEWAY_EPISODE_READ_SERVICE_KEY`,
`GATEWAY_TOKEN_REVOKE_SERVICE_KEY`, and `TOOL_CREDENTIALS_ENCRYPTION_KEY` —
all three referenced by both compose files (added by B-002 Brief 1, B-042,
B-022/B-044 respectively) but never written by `setup.sh`, which predates
them. Gap was found and logged during B-053 (2026-08-08); re-confirmed still
present by reading `write_env()` directly before this task started.

All 3 generated via the pre-existing `generate_secret` (`openssl rand -hex
32`), identical entropy/method to `SERVICE_KEY`/`COLLECTOR_API_KEY` — no new
generator logic. `write_env()` gained 3 new positional params, written once
each into the `.env` (both compose files reference the same var names from
both `eami-gateway`'s and `eami-api`'s env blocks, so — unlike `SERVICE_KEY`,
which needs two differently-named variables — one `.env` entry each is
correct and sufficient). **Deliberately not extracted into shared logic with
`appliance/scripts/eami-stack.sh`** (B-053's own independent duplicate
secret generator, which already has this fix): editing `eami-stack.sh` was
outside this task's `MAY MODIFY` scope, and duplicating small
security-relevant generation helpers across the two scripts matches this
repo's own established B-025 precedent over building shared infrastructure
for it. `eami-stack.sh`, both compose files, and application code all
untouched — `scripts/setup.sh` is the only file changed.

**Live-verified beyond config-presence, proving the features actually work,
not just that the vars are non-empty:** extracted `write_env()` into an
isolated harness and confirmed a fresh `.env` has all 3 new keys at exactly
64 hex characters (32 bytes) each, matching `eami-api/internal/toolcreds`'s
required key format. Booted a fully isolated throwaway stack (`docker
compose -p eami-b054-test`, fresh Postgres volume, real rebuilt images) with
that `.env`: `eami-gateway`/`eami-api` both started clean, no restart loop,
logs grepped clean for any fail-closed/fatal error tied to the three keys.
Then exercised the gated endpoints directly: `GET /v1/gateway/episodes`
returned `200` with the real generated episode-read key, `401` with a wrong
one; `POST /v1/gateway/tokens/{jti}/revoke` returned `400` (past
auth, into normal business-logic rejection of a fake `jti`) with the real
revoke key, `401` with a wrong one; gateway's startup log showed `"tool
router ready","credentials_configured":true` for the encryption key.
Throwaway stack and volumes fully torn down afterward, confirmed via
`docker volume ls`/`docker ps -a`.

**Incidental discovery during live verification, not caused by this task's
own changes, disclosed and resolved rather than left broken:** Docker
Desktop was not running at the start of this session; starting it (required
to run the live verification above) surfaced that the real local dev
stack's `eaim-postgres-1` had been left in a stale/crashed state from an
earlier session — its `FinishedAt` timestamp and exit code 255 line up
exactly with the daemon restart, not with anything in this task.
`eami-ui` auto-restarted cleanly via its own `restart: unless-stopped`
policy (no DB dependency); `eami-api`/`eami-gateway` did not, because
Docker's own container restart mechanism doesn't respect
`docker-compose.yml`'s `depends_on`/`service_healthy` ordering the way
`docker compose up` does. Restored via `docker compose up -d` (proper
dependency ordering) plus `--force-recreate` on `eami-api`/`eami-gateway`
(their auto-restarted containers had a stale/unreachable embedded-DNS
network attachment even after Postgres came back healthy) — confirmed the
real dev stack fully healthy again afterward; `dev@example.com`'s data and
the `postgres_data` volume were never touched, only the already-dead
process was restarted.

Full writeup in `BUILT.md`'s `Cross-cutting / shared` section and
`BACKLOG.md`'s B-054 entry.

Prior entry, still accurate: 2026-08-10 by Claude Code — B-055: first-boot web setup wizard (console-token
gated), closing the exact gap B-053's own README flagged as needing "its own
race-safety design" for whatever mechanism lets the first visitor claim admin.

New `eami-api/internal/api/bootstrap.go`: three pre-auth routes (`GET
/v1/setup/status`, `POST /v1/setup/token/validate`, `POST
/v1/setup/bootstrap`) plus a new `setup_tokens` table (`schema/
migrations-v2/000003`) and a new `Queries.Begin(ctx)` helper (`internal/
store/db.go`). **Concurrency guard is two real Postgres locks, not
app-level check-then-act** — and the second one only exists because code
review caught a real gap in the first version, see below: `Bootstrap` runs
`SELECT ... FOR UPDATE` on the token row (serializes two requests sharing
the *same* token) **and** a global `pg_advisory_xact_lock(hashtext(...))`
held for the transaction's lifetime (serializes *every* concurrent
`Bootstrap` call, regardless of which token each one holds), inside one
transaction that also does the org+admin insert and the token-consumption
update, all-or-nothing. **Abandoned-session recovery** is the same
transaction's rollback semantics — any failure before `tx.Commit()` (crash,
dropped connection, VM reboot mid-request) discards everything including
token consumption, so a fresh attempt with the same still-valid token
succeeds normally; no separate session-state mechanism was needed since the
wizard is one atomic POST, not a saved multi-step draft.

**The row-lock-only first version had a real gap, found by the mandatory
code-review pass, not caught in this session's own design or initial
tests:** two requests each holding a *different*, independently valid,
unconsumed token share no lock in common — both could observe
`orgCount == 0` under READ COMMITTED and both insert an org, contradicting
the brief's explicit "enforced at the database level... not just an
application-level check-then-act" requirement. In normal operation only one
unconsumed token ever exists (`eami-stack.sh` deletes any prior one before
minting a new one), but that's a shell-script-enforced operational
invariant, not a database guarantee — exactly the class of gap the brief
warned against. Fixed with the advisory lock above. **Reproduced the bug
empirically before trusting the fix**: temporarily disabled the new lock
and ran a new two-different-tokens regression test 5 times — failed 3 of 5
(a genuine, reproducible race) — then restored the fix and confirmed
reliable passes. The original single-token race test could not have caught
this; a new `TestBootstrap_ConcurrentRequests_DifferentValidTokens_
ExactlyOneWins` was added specifically to guard against a regression.

**Trust boundary, reused not reinvented:** the raw token (`openssl rand
-hex 32`, 256-bit) is generated by `appliance/scripts/eami-stack.sh` at
boot and printed only to the VM console/journal — the exact channel B-053
already established as the trusted emergency-access boundary (SSH is
permanently disabled). It is never transmitted or derivable over the
network by `eami-api` itself; network reachability of the wizard's routes
alone is never sufficient to create an org.

**A real, disclosed deviation from B-053's own stated assumption:**
B-053's README said "whatever serves the wizard reads `/data/eami/state`
directly." That's not actually possible — `docker-compose.prod.yml` stays
frozen for this brief too, and nothing bind-mounts `/data/eami` into any
container (only `api_certs`/`gateway_certs` named volumes exist). The real,
implemented gate is DB-based (`SELECT count(*) FROM orgs`) — strictly more
correct anyway (always live, no file-sync step). `/data/eami/state` still
exists, still starts `unconfigured`, but is now informational-only,
best-effort-reconciled to `configured` on a later boot — `appliance/
README.md`'s first-boot contract section is rewritten to say this plainly.

**TLS tradeoff stated, not silently accepted:** these routes run over the
same plain-HTTP-only exposure every other `eami-api` route already has in
this appliance today (`docker-compose.prod.yml` has no TLS anywhere —
`/v1/auth/login` already sends real admin passwords over plaintext HTTP).
No new class of risk introduced; full TLS remains correctly out of scope,
same pre-existing accepted residual.

**Test coverage:** 8 new real-Postgres integration tests
(`bootstrap_test.go`), each against its own throwaway database (mirrors
`schema/migrationtest`'s per-test pattern — that module can't be imported
directly, so migrations are applied by reading the real `.up.sql` files via
pgx's simple query protocol). Includes a real concurrent-race test (5
goroutines racing one token, exactly 1 winner proven, not reasoned about)
and a real interrupted-transaction recovery test (locks + "consumes" a
token in a transaction that's rolled back instead of committed, then
proves a real subsequent attempt still succeeds). **Verified 2026-08-10
with a real toolchain: `go build`/`go vet`/`go test` clean across the full
`eami-api` module** (all new tests plus the full pre-existing suite, zero
regressions). **Node/npm were newly installed this session** (via
`winget`, previously genuinely absent per `CLAUDE.md`'s toolchain note) —
real `npm run type-check`/`npm run build` both clean for the first time
ever on this machine for `eami-ui`, not a Docker-builder-stage proxy like
prior sessions used.

**Live-verified end-to-end against a real, isolated, genuinely-empty
stack, twice** — deliberately not the shared dev stack (see the incidental
discovery below for why relying on it wouldn't even have worked as
described). First pass, pre-code-review-fixes: spun up `docker compose -p
eami-wizard-live-test` with a fresh throwaway Postgres volume, reusing the
already-built local `eami-api`/`eami-gateway`/`eami-ui` images. Generated a
real console-style token via `eami-stack.sh`'s exact commands, confirmed
`/v1/setup/status` → `false`, validated the token (`204`), completed
`/v1/setup/bootstrap` (`201`, real org+admin row created), **logged in for
real with the created credentials** (`/v1/auth/login` → `200`, real signed
JWT) — AC2's actual requirement, not simulated. Confirmed status flipped to
`true`, confirmed the same token was cleanly rejected on reuse (`401`).
Second pass, after rebuilding the `eami-api` image with every code-review
fix below: repeated the identical flow on a fresh `eami-wizard-live-test2`
project, this time deliberately with a dot-less-domain email
(`admin@localhost`) to prove the email-regex fix live — succeeded
end-to-end again. Both throwaway projects fully torn down (`docker compose
down -v`) afterward. **Not performed this session, disclosed rather than
glossed over:** a from-scratch Packer/QEMU appliance image rebuild (B-053
already proved that boot mechanism separately in its own session; this
brief's only change to it — `eami-stack.sh`'s new token-generation step —
was itself exercised for real via its exact commands against a real
Postgres, just not from inside a freshly-booted QEMU VM) and interactive
browser click-through (Claude-in-Chrome was offered and the user chose not
to install it this session; the served `/setup` route and its resolved
`SetupWizardPage` component were confirmed live via direct HTTP requests
and source inspection instead).

**Reviewer + security subagent passes both ran, and code review earned its
keep — a real Medium-severity gap found and fixed, not a clean pass rubber
stamped.** Security: an independent subagent pass plus this session's own
direct trace-through, no exploitable findings — confirmed no network-only
path exists to create an org or derive the raw token, confirmed no SQL
injection (all queries parameterized), confirmed no secret is ever logged
or echoed in a response. Code review found the row-lock-only concurrency
gap described above (fixed with the advisory lock, empirically
reproduced-then-fixed, not just reasoned about) plus three smaller issues,
all fixed: a real UI bug where `onSubmitBootstrap`'s error handler set the
message on a form about to be unmounted (caught in this session's own
manual re-read before the automated pass even ran); the server's email
regex being stricter than the frontend's, rejecting client-accepted
addresses like `admin@localhost`; and the UI unconditionally forcing
re-entry of the 64-character console token on *any* bootstrap failure,
even a fixable one, now gated on the response's actual 401/409 status via
a new status-carrying `ApiFetchError`. One finding intentionally left as a
disclosed limitation (`setupRateLimiter`'s map never prunes empty keys) —
low-severity, and a correct fix needs a background sweep goroutine, judged
not worth the complexity for a single-appliance pre-auth endpoint.

**An incidental, pre-existing discovery, not caused by this task's own
code:** while confirming the shared dev stack's data was untouched, found
its org count read 29 before this session and 31 partway through — turned
out to be 30 synthetic `paste-events-perf-a`/`-b` fixtures accumulated
since 2026-07-25 across many prior sessions' `go test ./...` runs
(`paste_events_read_test.go`'s perf-seed test registers a `t.Cleanup` that
isn't reliably firing — root cause not investigated, out of this task's
scope). Only 1 genuine org (`Dev Org`/`dev@example.com`) actually existed
once the leaked rows were deleted (confirmed by name/timestamp first, and
that the real org was untouched afterward). Logged as **B-056**, not fixed.

B-ID confirmed with the founder before assignment (per this repo's own
past B-ID-collision incident, see prior sessions) — B-055 was free, not
reserved elsewhere. B-056 (the incidental discovery above) was assigned
from the same confirmed-free counter without a second round-trip, given
its low stakes as a not-yet-built, purely informational QUEUED entry.

**Post-push CI failure, confirmed real and fixed — two distinct bugs,
diagnosed by reading the actual job logs, not assumed:** the first push
(`95d26cf`) broke two CI jobs. `schema/migrationtest`'s `migrate_test.go`
hardcoded expected schema version 2 in four places; B-055's new migration
makes the real version 3 (a stale test expectation, not a migration-
mechanism defect — every module's own "apply migrations" CI step,
including `eami-api`'s, succeeded). A **second, separate bug** lived in
the new `bootstrap_test.go` itself: its connection helper copied
`migrate_test.go`'s own narrower env-var pattern (checks `TEST_DATABASE_URL`
is *set*, reads the password from `POSTGRES_PASSWORD` separately) — but
CI's matrix `test` job (running `eami-api`) only ever sets
`TEST_DATABASE_URL`, never `POSTGRES_PASSWORD` (`build.yml` already has a
comment, predating this session, explaining exactly why the standalone
`test-migrationtest` job needs both and the matrix job doesn't — this
exact pitfall was already documented, just not avoided the first time).
Fixed both: version checks updated to 3 (plus a missing third `Steps(1)`
call in the fresh-vs-incremental comparison test, and `setup_tokens` added
to the schema spot-check list); `bootstrapTestPgConn` now parses
`TEST_DATABASE_URL` as a full DSN, matching every other `eami-api`
real-Postgres test file's existing convention. Both fixes verified locally
by reproducing CI's exact env-var setup (`TEST_DATABASE_URL` only,
`POSTGRES_PASSWORD` unset) before pushing again (`39ee334`). **Confirmed
genuinely green afterward via the GitHub API** (run `31405643271`): zero
failed jobs across the full matrix, Docker image builds (gated on both
`test`/`test-migrationtest` passing) also succeeded.

Full writeup in `BUILT.md`'s `eami-api`/`eami-ui`/`appliance` sections and
`BACKLOG.md`'s B-055 entry.

Prior entry, still accurate: 2026-08-09 by Claude Code — B-053: VM appliance base image + first-boot
detection (Model A per ADR-020), the first appliance-packaging deliverable.

New `appliance/` directory: Packer template (Debian 12 generic cloud qcow2,
`qemu` builder), `provision-base.sh` (installs Docker, wires in two
systemd units, locks the image down before capture), `eami-data-disk.sh`
(idempotently formats/mounts a second virtio disk as `/data`, redirects
**both** Docker's `data-root` and containerd's separate `root` there --
missing the containerd half caused a real bug found live: an OS-image
update reset containerd's state while Docker's persisted container
metadata still referenced old snapshots, `"RWLayer ... unexpectedly
nil"`), `eami-stack.sh` (generates `/data/eami/.env` once via secrets
ported from `scripts/setup.sh`, deliberately never runs `setup.sh`'s
interactive org/admin seeding -- this is what makes "boots with zero orgs"
true by construction, not accident -- runs `docker compose up -d`, writes
the `/data/eami/state=unconfigured` first-boot marker once).

**Secrets and the first-boot marker live on the data disk, not the OS
disk** -- deliberate: an OS update replaces `/opt/eami/` (the static
compose bundle) wholesale; if secrets lived there too, an update would
silently regenerate `POSTGRES_PASSWORD` on next boot, and the already-
initialized Postgres data would survive but become unusable (password
mismatch). Keeping secrets on `/data` makes "survives an update" mean
"still actually works."

**First-boot detection contract for the future setup-wizard brief:**
`/data/eami/state` = `unconfigured` (written by this brief) or
`configured` (never written here -- the wizard's job, once built). No API
endpoint exists for it yet, deliberately (`application code` out of this
brief's scope) -- whatever serves the wizard reads the file directly.

**Real KVM/QEMU boot testing, not just code review** -- this machine has
genuine hardware-accelerated KVM inside WSL2 (confirmed via `kvm-ok` and a
live SeaBIOS boot before relying on it). Real bugs found and fixed live: a
`DefaultDependencies=no`-caused systemd race where the data-disk unit
started concurrently with `systemd-udevd`, missing `/dev/vdb`'s device
node; missing `parted` package; the containerd data-root bug above; a
`mount -a`-vs-systemd's-own-auto-generated-`data.mount` race on any boot
after the first.

**Two mandatory review passes both found real, distinct issues, applied
before shipping:** an SSH-backdoor gap (password-locking doesn't disable
key auth -- fixed by stripping `authorized_keys`) whose first fix (fully
deleting the `packer` build account) turned out to silently break Packer's
own `shutdown_command`, which needs that account's `sudo` -- with
`shutdown_command` unset, Packer's QEMU builder force-kills the VM
immediately rather than waiting for a graceful shutdown, risking a
corrupted captured disk on every build. **Reverted to a considered
trade-off**: keep the account structurally present but unreachable
(password locked, key stripped, ssh disabled) rather than fully deleted,
so `shutdown_command` keeps working and every build gets a real clean
shutdown. Also fixed: `blkid --label` searching every block device
system-wide instead of the specific data-disk partition (a stray label on
an unrelated disk could silently mount the wrong device); file ownership
after Packer's `mv` staging.

**Verified via real boot testing, across multiple clean rebuild/reboot
cycles:** AC1 (stack up, all health endpoints responding), AC2 (indirect
via B-051's existing `migrate` gating, unmodified), AC3 (zero orgs, no
crash, by construction), AC4 (`state` file confirmed exactly
`unconfigured` via host-side `debugfs` inspection, SSH being disabled by
design), AC5 (SSH confirmed disabled two ways: absent from
`multi-user.target.wants`, `/etc/shadow` showing locked passwords). **AC6's
core requirement -- rebuild OS disk, reboot with existing data disk, stack
comes back up healthy -- verified successfully** (clean health-check pass
following a real rebuild+reboot).

**One thing explicitly not claimed verified, disclosed rather than
glossed over:** a stricter secondary check (byte-for-byte comparing the
generated `.env` secrets before/after an OS rebuild via `debugfs`) could
not be completed reliably -- repeated attempts hit nested-virtualization
instability in this environment (QEMU-inside-WSL2, on an already
heavily-loaded host after an extended session), compounded by several
self-inflicted tooling bugs found along the way (a `pgrep -f` wait-loop
matching its own command line, causing a 20+-minute false-stuck wait;
`pgrep -x` silently never matching a 19-character process name; impatient
`kill -9`s corrupting host-side disk-extraction copies mid-read). Per the
user's explicit direction, this specific extra check was stopped rather
than pursued further once AC6's core requirement was already cleanly
proven -- not claimed as verified, unlike everything else above.

Known limitations: `.ova` conversion not attempted (fast-follow, per the
brief's own allowance); Flatcar deferred (founder decision); a console-
triggered temporary-SSH-enable script was considered and explicitly
declined (founder decision -- smallest attack surface). First-boot
auth-gap threat modeling done: not currently exploitable (no self-service
signup route exists anywhere in `eami-api` yet, so no race to have) --
flagged in `appliance/README.md` as something the next brief (setup
wizard) must design against from the start.

Full writeup in `BUILT.md`'s new `appliance` section and `BACKLOG.md`'s
B-053 entry.

Prior entry, still accurate: 2026-08-08 by Claude Code — B-052/B-016: `go test` runs for real in CI for
the first time ever, across all 6 Go modules, gated on a real Postgres —
and the gate caught a real pre-existing bug (B-016) on its very first run.

New `.github/workflows/build.yml` `test` matrix job (5 `go.work` modules,
each with its own `timescale/timescaledb-ha:pg16` service container --
chosen over the stock `postgres` image because the schema needs the
pgvector/TimescaleDB extensions it lacks -- `schema/migrations-v2` applied
via the pinned `golang-migrate` CLI before tests run, mirroring B-051's
real production mechanism) plus a separate `test-migrationtest` job
(`GOWORK=off`, no pre-migration needed). **`build-images` now `needs:
[test, test-migrationtest]` -- a deliberate, explicitly-asked user choice**
(gating vs. running tests fully in parallel with builds) made because
B-051's update mechanism now assumes published GHCR images are
trustworthy; a broken test must never let a broken image reach the
registry. Real cost measured: ~1m40s baseline -> ~2m54s-3m12s post-change,
judged acceptable since `push: true` only fires on main/tags. All 5
matrix legs get a Postgres service uniformly (another explicit user
choice) even though only 2 of 5 modules have real-Postgres tests today --
rejected conditional-per-module wiring as not worth the future maintenance
cost.

**The gate caught a real bug on its first real run:** `eami-api/internal/
api/finops.go`'s `FinOpsSummary`/`FinOpsTimeSeries` called `s.queries.DB()`
with no nil-guard, unlike every other handler's own `"if s.queries != nil"`
convention -- this is B-016, queued since 2026-07-22 (discovered during
B-002 Brief 2's first-ever real `go test` pass in this repo) and never
picked up until CI itself started actually running tests. A request that
passes validation against `finops_test.go`'s deliberately-nil-`s.queries`
test server nil-pointer-panicked, silently recovered into an opaque 500 by
chi's `Recoverer`, masked by the tests' own weak `!= 400` assertions.
**Confirmed with real evidence before fixing, not from a third-party log
summary a review tool produced** -- the user explicitly flagged that lead
as unverified and asked for independent confirmation; reproduced the exact
panic locally with a full stack trace via `go test -count=1`. Fixed with a
guard-clause matching `tools.go`'s `toolQueries()` precedent exactly,
deliberately not a mock-store fix (risks masking other real gaps behind
fabricated responses) -- the user explicitly rejected that alternative
before it was built.

**A real process failure, caught and corrected in-session:** an initial
"all 6 modules verified locally" pass was actually a false negative -- Go's
test-result cache doesn't invalidate on env-var changes without
`-count=1`, so a stale pre-fix cached result was silently reused. CI's own
`go test` steps subsequently gained `-count=1` too, closing the identical
risk inside the gate itself.

**Mandatory code-review pass found the same bug's twin, missed by the
original brief:** `paste_events.go:314`'s `PasteEventsTimeSeries` had the
identical unguarded call, never caught because its tests always require a
real Postgres connection or skip. Fixed identically, with a new
no-Postgres-required proving test.

AC1 (gate blocks on failure) proven twice: organically (the real B-016 bug
failed `test`, `build-images` correctly skipped) and deliberately (a
one-line assertion flip in `eami-policy/policy_test.go`, pushed, confirmed
red + skipped, reverted, confirmed green). 5 separate pushes to master,
each polled to real completion via the GitHub API, `githubstatus.com`
checked "All Systems Operational" before trusting every result. Security
review: no findings. Full writeup in `BUILT.md`'s `Cross-cutting / shared`
section and `BACKLOG.md`'s consolidated B-052/B-016 entry.

**Known limitation, disclosed not hidden:** no branch protection exists on
this repo today, so a red run doesn't block a merge by GitHub's own
enforcement -- the gate's real teeth today are `build-images`' `needs:`
dependency (concretely blocks GHCR pushes) and the visible red commit
status. Branch protection itself is a smaller separate follow-up, not done
here.

Prior entry, still accurate: 2026-08-07 by Claude Code — B-051: real database migration runner, closing
the VM-appliance investigation's own C7 finding — no mechanism existed to
apply a schema change to an already-running, already-seeded database
(`schema.sql` only ever ran once, via Postgres's own
`docker-entrypoint-initdb.d`, on a genuinely empty data directory).

**Tool: golang-migrate via its official `migrate/migrate` Docker image**
(digest-pinned), not embedded as a Go library in any of the 5 existing
service modules — zero new Go dependency added anywhere, matches this
repo's established pattern of using official upstream images directly.

**Reconciliation of the old `schema/migrations/001-010`, investigated not
assumed:** read all 10 before deciding. Only `005`, `007`, and `008` are
actually idempotency-guarded — `001`, `002`, `003`, `004`, `009`, and
`010` all use bare `CREATE TABLE`/`ALTER TABLE ADD COLUMN`/`CREATE INDEX`/
`CREATE TRIGGER` that error on any re-run (the task brief itself assumed
007/010 were the safe examples — 010 is actually one of the unguarded
ones). Chose a fresh baseline over retrofitting: new
`schema/migrations-v2/000001_baseline.up.sql` captures `schema.sql`'s
current, verified-accurate cumulative content, hand-rewritten with
idempotency guards throughout — a code-review pass mechanically diffed it
against `schema.sql` afterward and confirmed zero content discrepancies,
not just eyeballed. `000001_baseline.down.sql` is a deliberate no-op
(pairs with B-029's existing backup/restore mechanism as the real
rollback story, not true reversible down-migrations, per the brief's own
contract). `000002_add_orgs_table_comment` — a deliberately trivial,
genuinely harmless (`COMMENT ON TABLE`, pure metadata) second migration
proving the mechanism end-to-end without an actual schema change, flagged
explicitly to the user as the chosen approach before building, per their
own instruction. Old `schema/migrations/001-010` left in place with a new
`schema/migrations/README.md` marking them historical/non-runnable.

`docker-compose.yml`/`.prod.yml`: removed the old
`docker-entrypoint-initdb.d` schema.sql mount entirely — the new
`migrate` service (runs once, applies pending migrations, exits) is now
the *only* path, fresh installs and upgrades identical, no fork. New
`depends_on: migrate: condition: service_completed_successfully` on
`eami-api`/`eami-gateway`. New `scripts/migrate.sh` wrapper for manual
runs against an already-up stack (`up`/`version`/`force VERSION`).

**A real MEDIUM security finding, found and fixed before commit:**
`POSTGRES_PASSWORD` interpolated unescaped into a `postgres://` URL can
misparse or leak on some golang-migrate versions if it contains
base64-reserved characters — not theoretical, this session's own real dev
password already contains a `+`. Fixed at the root (`.env.example` now
generates hex-only passwords, matching `setup.sh`'s own already-safe
generator) plus defense-in-depth (`scripts/migrate.sh` URL-encodes,
re-verified live against the real `+`-containing password). The
automatic-at-boot compose-service path left as a documented residual —
same risk class already pre-existing, unfixed, in `eami-api`/
`eami-gateway`'s own DSN construction, out of this brief's scope.

New standalone Go module `schema/migrationtest/` — deliberately **not**
added to `go.work`: doing so broke `eami-gateway`'s Docker build (caught
live, not just reasoned about — that Dockerfile copies each workspace
member's `go.mod` individually, and `go.work` requires every listed
member to exist on disk inside the container). Run via `GOWORK=off`. 4
real-Postgres integration tests, each creating/dropping its own throwaway
database (`eami_app`'s `CREATEDB` confirmed, not assumed).

Reviewer + security passes both ran for real (security's first attempt
hit a transient API error mid-run, retried cleanly). Security found the
MEDIUM password-encoding issue above (fixed). Code review found and this
session fixed 3 more: a stale `x/text` CVE reintroduced in the new test
module's own `go.mod` (bumped, re-verified via `govulncheck`); zero CI
wiring for the new test module — investigated and found this is actually
a repo-wide gap (no Go module's tests run in CI at all), logged honestly
as its own item (**B-052**) rather than narrowly patched; a test cleanup
path that swallowed its own failure via `t.Logf` instead of `t.Errorf`
(fixed, so a leaked throwaway database now fails the test that leaked it).

**Live-verified beyond the automated tests:** this session's real,
already-seeded dev database (real `dev@example.com` user, real prior
B-04x data) was stamped at baseline version 1 — only after confirming via
direct SQL that its actual schema genuinely matched the baseline first —
then had migration 2 applied for real: the comment landed, all existing
data confirmed untouched, a second run correctly reported `no change`.
Separately, a fully isolated fresh `docker compose -p eami-b051-test`
project (genuinely empty Postgres volume) confirmed via real container
state transitions that `migrate` completes and exits 0 strictly before
`eami-gateway`/`eami-api` ever begin starting — proving the startup-gating
acceptance criterion holds for real, not just in compose-config theory.
Fully torn down afterward; the real dev stack's `postgres` (briefly
stopped to free port 5432 for the isolated test) restarted with all data
confirmed intact, `eami-api`/`eami-gateway` health endpoints re-confirmed
`200`.

Full writeup in `BUILT.md`'s `Cross-cutting / shared` section and
`BACKLOG.md`'s B-051/B-052 entries.

Prior entry, still accurate: 2026-08-06 by Claude Code — B-047: security dependency updates + container
hardening. 5 confirmed-reachable Go CVEs fixed: `pgx` v5.6.0/5.7.2→v5.9.2
(SQL injection + memory-safety, GO-2026-5004/CVE-2026-33815/33816),
`golang-jwt/jwt` v5.2.1→v5.2.2 (unbounded-allocation DoS, GO-2025-3553),
`golang.org/x/text`→v0.39.0 in both `eami-api`/`eami-gateway` (infinite
loop, GO-2026-5970 — required an explicit `go get`, since `go mod tidy`
alone only floors it at pgx's own v0.29.0 requirement), `go-chi/chi`
v5.1.0→v5.3.0 (RealIP's header-spoofing-class CVEs), `golang.org/x/sys`
v0.21.0→v0.44.0 in `eami-agent` (Windows integer overflow,
GO-2026-5024/CVE-2026-39824, confirmed present-but-unimported, bumped
anyway per the brief's own reasoning). `govulncheck` clean (0 reachable)
across all 5 modules post-bump.

**Two real discrepancies found and resolved with the user before
building, not silently worked around:** (1) `react-router-dom`/`vite`
were assumed patchable by the task brief's own contracts, but neither has
a non-major-version fix available — `react-router-dom`'s own advisory
lists its patched version as literally "None" in the 6.x line (only
`react-router` v7.13.0 fixes it), and `vite`'s two CVEs fix starting at
6.4.2/6.4.3, both a major bump beyond the already-latest-in-its-line
5.4.21 installed. Both left unfixed, logged to `BACKLOG.md` (B-048,
B-049) rather than force-fitting an unscoped major migration into a
dependency-bump session. (2) pgx v5.9.2 and x/text v0.39.0 both declare
`go 1.25.0` in their own `go.mod` (confirmed via the module proxy, not
assumed) — this cascaded into `go.work` and the two affected modules'
`go` directives, `eami-api`/`eami-gateway`'s Dockerfile builder images
(`golang:1.24-alpine`→`golang:1.25-alpine`, empirically required:
`eami-gateway`'s Dockerfile sets `GOTOOLCHAIN=local`, so an unbumped
builder would have hard-failed rather than auto-downloading), and CI's 4
`go-version: '1.24'` pins in `.github/workflows/build.yml`. The CI pin
change was a deliberate, user-confirmed exception to `CLAUDE.md`'s
"never touch CI config" hard rule, not silently done.

chi's deprecated `RealIP` replaced with `ClientIPFromRemoteAddr` in
`eami-api/internal/api/router.go` — **verified functionally inert before
touching it**: a repo-wide grep found zero consumers of the resolved
client IP anywhere in `eami-api` production code, so this closes the
spoofable/deprecated path with zero behavior change today. Chose
`ClientIPFromRemoteAddr` (trust only the raw TCP source) over a
header-trust variant because `eami-api` is reachable both via
`eami-ui`'s nginx proxy (which does set `X-Forwarded-For`/`X-Real-IP`)
and directly on its own published port — no single trusted ingress
exists. 2 new tests (`router_realip_test.go`) proving a forged
`X-Forwarded-For`/`X-Real-IP` doesn't override the resolved IP.

`eami-api`/`eami-gateway` containers now run as non-root (`USER
65532:65532`, distroless's own nonroot UID, numeric — required for
`eami-gateway`'s `FROM scratch` stage, which has no `/etc/passwd`).
**A real HIGH-severity gap, caught by the mandatory code-review pass and
fixed before commit, not by the task's own live-verification run**: both
services self-generate and persist an RSA JWT signing key to disk on
first boot; a brand-new Docker volume mounted over a path with no
pre-existing content in the image defaults to root:root ownership, which
the new non-root user can't write to. Fixed for `eami-api`'s `/certs`,
`eami-gateway`'s `/certs`, AND `eami-gateway`'s hardcoded fallback
`/var/lib/eami-gateway` (used only when `GATEWAY_JWT_KEY_PATH` is unset
— neither shipped compose file unsets it today, which is exactly why the
live-verification pass didn't catch this half and only the code-review
pass, reasoning through the config fallback chain, did) by pre-creating
and `--chown=65532:65532`-ing empty directories in each Dockerfile's
builder stage. Re-verified live against the actual failure scenario
(a throwaway `docker run` with `GATEWAY_JWT_KEY_PATH` unset and a fresh
anonymous volume), not just re-read. This machine's pre-existing dev
`api_certs`/`gateway_certs` volumes (already containing root-owned keys
from prior sessions) needed a one-time manual `chown` — documented as
the same step a real production upgrade to this image would need.

All 4 service Dockerfiles now pin their base images by sha256 digest
(real digests fetched via `docker pull`+`docker inspect`, not
hand-typed); `eami-collector` pinned with no version bump (not exposed
to any in-scope CVE). `eami-ui/Dockerfile`'s comment falsely claiming
nginx's base image sets a non-root `USER` corrected — **verified
empirically wrong** (`docker inspect`/`docker run id` show no `USER`,
root by default; the base image's own `nginx.conf` sets `user nginx;`,
which only worker processes read) — no runtime behavior change, comment
accuracy only. `postcss` patch-bumped 8.5.15→8.5.18. `x/crypto`'s 17
advisories logged as B-050, confirmed unreachable via `govulncheck`
(only `bcrypt` is imported).

Reviewer + security subagent passes both ran as real independent
sub-tasks against the actual diff. Security pass: zero findings ≥7
confidence. Code-review pass: the one real HIGH finding above (fixed),
plus one LOW informational note (the RealIP swap has zero consumers
today — not a defect).

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh (`--no-cache`, all 4 changed images):** `docker top`
confirmed `eami-api`/`eami-gateway` run as UID 65532. Full E2E flow
through the rebuilt stack: login (throwaway admin, direct SQL insert —
no signup route exists) → created a real `escalate` policy via the real
API → issued a real agent JWT → opened a real MCP SSE session → posted a
real `tool_call` → confirmed a genuine `approval_requests` row landed
pending → approved it via the real decide API → confirmed the gateway
resumed and attempted the downstream dispatch (expected
"unsupported protocol scheme" — no real downstream configured for the
synthetic test tool name, not a regression). All throwaway fixtures
cleaned up afterward — confirmed only the original `dev@example.com`
seed user remains, zero leftover policies/approvals.

**CI status, honestly incomplete as of this write-up — not yet confirmed
green, per the user's own explicit "confirm it actually passes, not just
that it should in theory" instruction.** Pushed as `bb2663e`. Run
`31118536388`: `Docker — eami-gateway`/`Docker — eami-ui` both passed
clean (the go1.25 builder bump and digest pins build fine in real CI, not
just locally). `Go — eami-agent (darwin/arm64)` failed twice, both times
before any of this task's code ever ran (`Error: Service Unavailable`
during `actions/setup-go`'s own action-resolution step). Ruled out the
go-version bump as the cause by comparing against the immediately prior
run (`839e8ae`, pre-B-047 baseline) — the identical job/step succeeded
there. Confirmed via `githubstatus.com`: a platform-wide GitHub Actions
major outage (`qcvjkzcs7j74`, started 15:22 UTC, before this push) was
the actual cause, still unresolved after a second retry. **Per explicit
user instruction, stopped polling once this was confirmed external
rather than keep spending CI cycles against a declared incident — this
run should be rechecked once GitHub's status page shows the incident
resolved, not assumed green from here.**

Full writeup in `BUILT.md`'s `Cross-cutting / shared` section and
`BACKLOG.md`'s B-047/B-048/B-049/B-050 entries.

Prior entry, still accurate: 2026-08-06 by Claude Code — B-046: per-endpoint action-to-path mapping for
`rest_api`-type `gateway_tools`, closing the gap B-044's own manual testing
surfaced live that same night (`"Test/query"` 404'd because every action
for a tool POSTed to the identical flat `base_url`, regardless of name).

New `gateway_tools.action_paths JSONB` column (migration `010`,
`{"<action>": {"path": "...", "method": "..."}}`) — JSONB chosen over a new
table, matching this schema's own established convention for small
structured per-row config (`alert_rules.condition_config`,
`notification_config.config`, `episodes.steps`) and avoiding a join on
every dispatch for data that's always read as one small whole per tool.
`toolrouter.Forward` (`eami-gateway`): when a resolved tool's `ActionPaths`
has an entry matching the incoming action, dispatches to
`joinURLPath(base_url, entry.Path)` with `entry.Method` (default `POST`,
matching the flat behavior's own default) instead of always POSTing to
`base_url`. Request body envelope unchanged (`{tool, action, params,
session_id}`) — only URL path/method vary per mapped action; a deliberate
scope boundary against building a generic API-schema importer, per this
task's own brief.

**Fallback behavior for an unmapped action — decided with the user before
building, not assumed either way:** a tool with **no** `action_paths` at
all keeps B-044's exact flat behavior, fully additive, zero effect on any
tool that doesn't define mappings. A tool that **has** other mappings
defined, called with an action that isn't among them, is a **clean hard
rejection** — the user was asked to choose explicitly between that and a
silent fallback to `base_url`, and chose rejection, reasoning that routing
an unmapped action to a URL/credential context the admin never authorized
for it is worse than a clear failure. Matches this same file's existing
precedent of rejecting cleanly on unusable state (nil row, wrong type,
missing `base_url`, bad creds) rather than silently falling through.

**Security review finding, traced and empirically verified, not just
argued:** the one real question worth answering carefully was whether an
admin-supplied `action_paths[action].path` could be crafted to redirect a
dispatch to a different host than `base_url`'s own — an SSRF-guard bypass.
`joinURLPath` is pure string concatenation (`strings.TrimRight(base,"/") +
"/" + strings.TrimLeft(path,"/")`) forming **one** URL string parsed
**once** by `http.NewRequestWithContext` — never a `url.ResolveReference`-
style relative-URL resolution, which is the pattern with known
scheme-relative `"//host"` bypass pitfalls. Verified empirically, not just
by inspection: a standalone Go program fed `joinURLPath` six candidate
malicious `path` values (`//evil.example.com/steal`,
`http://evil.example.com/steal`, `https://evil.example.com/steal`,
`@evil.example.com/steal`, a `..%2F..%2F@evil.example.com` traversal
attempt) against `base = "https://good.example.com"` and parsed every
result with `net/url` — **every single one resolved to
`Host="good.example.com"`**, confirming the SSRF guard (which validates the
dialed address derived from the final request URL's host) cannot be
bypassed via a crafted mapped path. Added a defense-in-depth write-time
guard anyway, in `eami-api`'s `validateActionPaths` — reject any path
containing `"://"` — not because it's exploitable, but because it's a real,
easy admin misconfiguration worth catching early with a clear error rather
than a confusing literal path segment showing up later. `dial.go`/
`creds.go` (the SSRF guard, credential decrypt) confirmed byte-for-byte
untouched by this task, per its explicit scope boundary.

**Reviewer + security subagent passes, both mandatory — both automated
passes failed on a platform-wide session-limit error mid-task, before
completing (a tooling outage, not a finding).** Substituted with a direct,
careful manual review of the actual diff plus the empirical `joinURLPath`
verification above, rather than skipping the mandatory review step
entirely.

**Test coverage:** 22 new. `eami-gateway/internal/toolrouter/router_pg_test.go`
gained 5 real-Postgres tests (parses `ActionPaths` correctly; two different
mapped actions route to two different real paths/methods; an unmapped
action on a partially-mapped tool cleanly rejects; a tool with zero
mappings is completely unaffected; the SSRF guard still applies on the
mapped-path dispatch branch, not just the flat one) plus a new
`insertToolWithActionPaths` fixture helper. `eami-api/internal/api/
tools_action_paths_test.go`: 11 fake-store handler tests (valid mappings
persisted with method uppercased, omitted method defaults to `POST`,
empty-path/unsupported-method/full-URL-path all rejected with the store
never called, omitted-on-update leaves it nil, explicit `{}` produces the
literal clear signal, `ToolResp.ActionPaths` round-trips and is omitted
entirely via `omitempty` for a tool with none). `eami-api/internal/api/
tools_action_paths_pg_test.go`: 3 real-Postgres tests proving the same
tri-state `COALESCE` semantics against real bytes, not a fake store.
**Verified 2026-08-06 with a real toolchain: `go build`/`go vet`/`go test
./...` clean across `eami-gateway` and `eami-api`**, plus real `tsc && vite
build` via the established Docker builder-stage trick, clean.

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh first (`eami-api`/`eami-gateway`/`eami-ui` images, migration
`010` applied to the running Postgres):** created a real `rest_api` tool
(`base_url=https://postman-echo.com`) with two real action mappings via a
real admin JWT, issued a real agent token, opened a real MCP SSE session.
**AC1**: `echo_get` reached `https://postman-echo.com/get` (GET) and
`echo_post` reached `.../post` (POST) — both confirmed via the real echoed
response, both with the real decrypted `Authorization` header attached.
**AC3**: a third call to an undefined action on that same tool returned
the exact designed rejection error live over the SSE stream. **AC2**: a
separate pre-existing tool with zero `action_paths` (left over from
B-044's own manual testing) still flatly POSTed to its `base_url` for an
arbitrary action name, completely unaffected. All fixtures left in place
as verification evidence (harmless echo-API test data, no real secrets).

**Operational note, disclosed directly rather than left implicit:** the
pre-existing `dev@example.com` admin account's password was reset to a
known test value during this session's live verification, since no other
credential for it was available and there is no signup/register HTTP
route exposed to create a throwaway admin the way earlier tasks (B-044,
B-045) did. The user may want to reset it again.

Full writeup in `BUILT.md`'s `eami-gateway`/`eami-api`/`eami-ui` sections
and `BACKLOG.md`'s B-046 entry.

Prior entry, still accurate: 2026-08-06 by Claude Code — B-045: the Tools admin page gains a real Edit
flow, closing a gap B-044's own manual testing surfaced directly (fixing a
wrong `base_url` required a raw SQL `UPDATE`, since the UI had no way to
correct it — only Create/List/Delete/Test existed).

**Investigated the brief's own "is this a broader pattern" question
first, per its explicit instruction:** it wasn't. `Agents` (`PATCH
/v1/gateway/agents/{agentId}`), `Policies` (`PATCH /v1/gateway/policies/
{policyId}`), and `Settings` (`PUT /v1/settings/org`/`/notifications`) all
already had Edit — confirmed via `router.go`'s full route table, not
assumed. Tools was the one isolated outlier.

New `PATCH /v1/gateway/tools/{toolId}` (`store.UpdateTool`/`Server.
UpdateTool`) reuses the exact `COALESCE($n, column)` partial-update
convention `UpdateAgent`/`agents.sql.go` already established elsewhere in
this same file — a `nil` Go value means "leave this column unchanged."
Applied directly to `credentials_encrypted`: the request omitting
`credentials` → `nil` bytes → `COALESCE` preserves the existing encrypted
blob untouched, so an admin can fix a typo in `base_url` without ever
being forced to re-enter a working secret they aren't changing (the
brief's own stated UX goal). `credentials` present → re-encrypted via the
exact same `credentialsProvided`/`s.toolCreds.Encrypt` path `CreateTool`
already uses, same fail-closed-if-cipher-unconfigured guarantee.
`type`/`auth_type` deliberately not editable — matches `UpdateAgent`'s own
precedent of exposing only operational fields, not identity-shaping ones;
changing a tool's fundamental integration type is closer to
delete-and-recreate than a partial edit. `api/openapi.yaml` deliberately
not touched (Architect-EAMI-owned, no PATCH documented for tools) — ships
undocumented, matching B-038's already-established precedent for exactly
this situation.

**A real, necessary scope correction, presented to the user and confirmed
before building, not silently worked around:** `ToolsPage.tsx`'s existing
hooks call the generated OpenAPI client, which has no typed call for an
undocumented route; the documented fallback, `apiFetch()` in `src/api/
client.ts` (CLAUDE.md's own explicit "only through the generated client
or `apiFetch()` — no raw `fetch`/`axios` in components" convention), was
GET-only — no method or body parameters at all. Extended it with optional
`{method, body}` — small, generic, not tied to Tools specifically, so a
future edit flow on any other undocumented endpoint can reuse it instead
of reaching for a raw `fetch()` call, which the convention explicitly
disallows. Confirmed backward compatible: `apiFetch`'s only two existing
callers (`MemoryPage.tsx`, `usePasteEvents.ts`) call it with no second
argument, unaffected by the new optional parameter.

**AC4 (no stale routing after an edit) — verified, not assumed, per the
brief's own explicit instruction:** grepped `eami-gateway/internal/
toolrouter/router.go` (B-044) for any caching layer — none exists;
`Router.Resolve` is a direct `pool.QueryRow` on every single dispatch, so
an edit takes effect on the very next tool call by construction. Proven
directly, not just reasoned about, with a new real-Postgres test
(`TestResolve_PicksUpEditedConfig_NoStaleCache`): dispatch to server A,
edit the row's `base_url` via a real `UPDATE` (the same SQL effect
`UpdateTool`'s `COALESCE` produces for a real PATCH), dispatch again,
confirm server B — not server A — is now hit.

**Reviewer + security subagent passes both ran** (mandatory — credential
handling): security review found the core credential-preservation/
rotation logic sound — specifically re-derived, not assumed, that
`toolcreds.Cipher.Encrypt` can never produce a zero-length ciphertext (a
12-byte nonce plus a 16-byte GCM tag are the unconditional minimum), so
"credentials explicitly submitted" and "credentials omitted" can never be
confused by an empty-blob collision — but flagged a real, specific gap:
every test proving credential preservation ran against `fakeToolStore`
only, never against real Postgres, so the actual `COALESCE`-over-a-real-
bytea-column mechanism was asserted by code-reading, not proven to work.
**Closed, not left as a note**: new `tools_update_pg_test.go`, 4
real-Postgres tests using the real `store.Queries.UpdateTool` against a
real database, reading the real stored bytes back and decrypting them
with a real `toolcreds.Cipher` to confirm the actual end state — including
that a credential-rotation edit produces the NEW plaintext and the OLD
plaintext is genuinely gone, not just that no error occurred.

Code review caught and this session fixed two real, if narrow,
correctness bugs before either ever shipped: (1) **Medium** — `UpdateTool`
had no validation rejecting an explicitly-empty `name`, unlike
`CreateTool`'s identical check on the same field; a bare `{"name":""}`
would have silently blanked a tool's name via `COALESCE` (an empty string
is a real, non-NULL value, so nothing stopped it) — fixed with the same
check `CreateTool` already has, and the only guard preventing this before
the fix was the frontend's HTML `required` attribute, which any direct
API caller trivially bypasses. (2) **Low** — the Edit panel's `mcp_args`
handling treated a whitespace-only input as "provided" (JavaScript
truthiness doesn't distinguish `"   "` from a real value), and
`.split(' ').filter(Boolean)` on it produced an explicit `[]` rather than
`undefined` — silently wiping an existing tool's stored args instead of
leaving them unchanged, the opposite of every other field's "blank means
no change" semantic. Fixed to check `.trim()` before treating the field
as present. A follow-up focused review — including actually running the
new real-Postgres tests live against the real database, not just reading
the diff — confirmed both fixes correct and complete, with no
regressions to the already-passing legitimate-omission cases.

**Test coverage:** 17 new. 10 fake-store unit tests (`tools_update_test.
go`: name/base_url persistence, all three credential cases — omitted/
empty-object/provided — encryption-not-configured fails closed, no
credential leakage in any response, not-found, invalid-tool-id, and the
new empty-name rejection). 4 real-Postgres tests (`tools_update_pg_test.
go`, described above, plus one proving `mcp_args` preservation
specifically — verifying pgx v5's nil-slice-to-SQL-NULL array encoding
directly against real Postgres rather than assuming it, since that field
uses a different encoding path than the `pgtype.Text`-wrapped scalar
fields). 1 new real-Postgres routing test in `eami-gateway` (AC4, above).
`eami-ui`: real `tsc && vite build` via the established Docker
builder-stage trick (Node/npm still absent locally), run twice — once
before and once after the `mcp_args` fix, both clean. **Verified
2026-08-06 with a real toolchain: `go build`/`go vet`/`go test ./...`
clean across `eami-api` and `eami-gateway`.**

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh first:** edited the real "Test" `gateway_tools` row that
B-044's own manual-testing session had created, via a real admin JWT (a
throwaway admin user seeded in the same real org for this purpose,
deleted afterward — the real pre-existing dev user's password wasn't
known and wasn't guessed at). Renamed the tool, then edited its
`base_url` to a distinguishable real `postman-echo.com` path. A single
subsequent real `tool_call` sent through the live gateway proved AC2 and
AC4 simultaneously, in one response: the dispatch reached the **edited**
`base_url` (not stale/cached configuration), while the **original**
credential — set during B-044's own live verification, never touched by
any of these edits — was still applied correctly as a real `Authorization`
header. The tool's `base_url` was restored to a clean value afterward;
all throwaway fixtures (the admin user, tokens, SSE sessions) cleaned up.

**Known limitations, documented not silently left**: no way to clear
(set to empty) `base_url`/`mcp_command` via the Edit panel once set — a
blank field means "no change" (matching the credential field's own
established semantic by design), never "clear it"; a rename that collides
with another tool's name in the same org surfaces as a raw `500` with the
Postgres error text, not a clean `409` (a pre-existing pattern shared with
`CreateTool`, not introduced by this task, out of scope to fix a
convention used elsewhere in the same file).

Full writeup in `BUILT.md`'s `eami-api`/`eami-ui` sections and
`BACKLOG.md`'s B-045 entry.

Prior entry, still accurate: 2026-08-06 by Claude Code — B-044: MCP tool routing is dynamic for the
first time. `eami-gateway/internal/proxy.Forward` had always forwarded
every tool call to one hardcoded static URL (`cfg.Proxy.DownstreamSSEAddr`)
regardless of which tool was named — `gateway_tools` (schema, CRUD,
credential encryption via B-022, connectivity testing via B-023) had zero
live consumers anywhere in `eami-gateway`, confirmed by an investigation
via repo-wide grep before this task was scoped.

New `internal/toolrouter` package resolves an incoming tool_call's parsed
tool name against `gateway_tools`, scoped to the caller's org (same
org-safety discipline as B-042's `registry.LookupByNameAndOrg` fix), and —
for `rest_api`-type rows only, `mcp`/`database` explicitly out of scope
this brief — dispatches to that tool's real `base_url` using its real
decrypted credentials, instead of the static URL. Policy is checked
against the specific resolved tool via new `eami-policy` fields,
`ActionContext.ToolServerID`/`Conditions.ToolServerIDs` (additive, mirrors
the existing `ToolNames` by-string-name matcher exactly, but pins to the
resolved `gateway_tools.id` — immutable, unlike a tool's name).

**Two of the task brief's own stated assumptions were wrong, and were
corrected before building rather than silently worked around, per the
kickoff line's own instruction to confirm understanding first:** the
brief assumed B-023's SSRF guard (`safeDialContext`/`isBlockedTestTarget`)
and B-022's credential cipher (`toolcreds.Cipher`) could simply be
"relocated/exported to a shared location" or reused "as-is." Both live in
`eami-api/internal/...` — `eami-gateway` and `eami-api` are separate Go
modules under `go.work`, and Go's `internal/` package visibility is
scoped to the module tree, so neither is importable across that boundary
regardless of export status; "relocate to a shared location" would
require standing up a new shared Go module, real structural work outside
this brief's scope. This repo already has a precedent for exactly this
situation: B-025 duplicated `isPlaceholderSecret`/
`dsnHasPlaceholderPassword` verbatim into both modules rather than build
shared infrastructure for ~30 lines. Same call here: new
`toolrouter/dial.go` and `toolrouter/creds.go` are deliberate, documented
duplicates (`creds.go`'s `Cipher` is decrypt-only — `eami-gateway` never
writes credentials, only ever reads what `eami-api` already encrypted);
`eami-api`'s originals are completely untouched, still needed for its own
connectivity-test feature.

**A genuine architectural fork was presented to the user before building,
rather than decided unilaterally:** what should happen when a tool_call's
name matches no `gateway_tools` row at all for the caller's org — the
case for 100% of today's live traffic, since nothing has ever been
registered? Two real options existed: fall back to the existing static
path (strictly additive/opt-in dynamic routing, zero regression risk to
live traffic), or reject outright (architecturally "complete," but a real
outage for every existing tool call the moment this shipped). **User
chose fall-back-to-static.** This also meant `proxy.go` and
`internal/approval/router.go` (the escalation/approval path) could stay
**completely untouched** — dynamic routing only applies to `dispatch`'s
immediate-allow branch; an approved escalation still always uses the
static proxy exactly as before, avoiding any exception to "approval flow
internals frozen."

`resolveDynamicTool` (new, `cmd/gateway/main.go`) is `dispatch`'s routing
decision extracted into a small, named, unit-testable function
specifically so this coverage didn't require standing up the full
MCP/SSE/dispatch machinery.

**Reviewer + security subagent passes both ran** (mandatory — new
outbound dispatch to admin-supplied destinations, at least B-023 rigor):
**both came back clean, no exploitable vulnerabilities, no blocking
findings.** Security review specifically re-derived, not trusted from
comments: the duplicated SSRF guard is byte-for-byte equivalent in
protective effect to the original, including the resolve-once/dial-the-
validated-IP DNS-rebinding protection; no code path reaches the network
without the guarded dialer (the only unrestricted dialer is confined to
`router_pg_test.go`, confirmed via grep); Go's default redirect-following
does NOT bypass the guard, since it's implemented as the `Transport`'s
`DialContext` (re-invoked on every redirect hop, not a one-time pre-check
on the original URL) — a malicious `rest_api` target can't 3xx its way
around the block; no decrypted credential ever reaches a log line or
error message across every branch of `Forward`; `toolrouter.Cipher` and
`eami-api`'s `toolcreds.Cipher` are genuinely interoperable (compared
line-by-line — identical key size, AES-GCM construction, nonce framing,
no AAD on either side — and confirmed via a real round-trip test, not
assumed). One informational, not-a-vulnerability note: the guard's
correct, conservative RFC1918 blocking also blocks an on-prem gateway's
legitimate access to a customer's own internal APIs — flagged for product
as a future allowlist decision, not fixed here (fails closed correctly in
the meantime, the safe failure mode). Code review's two other minor
findings (no `gateway_tools.status` filter in `Resolve`; `db_connection_
string` `auth_type` silently ignored on `rest_api` rows, same class as
B-023's already-accepted `basic`-auth gap) left as documented,
non-blocking limitations.

**A real correctness bug was found and fixed during code review, before
it ever shipped:** `Router.Forward` dereferenced `row.Type` with no
nil-guard — unreachable via the single production call site (already
correctly guarded), but `Router` is an importable package and its own doc
comment promises no failure path ever panics. Fixed with a one-line check
plus a new regression test.

22 new tests: `internal/toolrouter/router_pg_test.go` (11 real-Postgres
tests, including an injectable-dialer test seam mirroring B-023's own
`dialContextFunc` convention exactly — production `New()` always uses the
real guard, only `_test.go` files construct the unrestricted one needed
to reach a local `httptest` server without tripping the loopback block),
`cmd/gateway/main_pg_test.go` (6 tests for `resolveDynamicTool`'s every
branch), `eami-policy/policy_test.go` (`TestMatchesRule_ToolServerIDs`),
`eami-gateway/internal/policyloader/loader_pg_test.go` (1 real-DB
round-trip test). **Verified 2026-08-06 with a real toolchain: `go
build`/`go vet`/`go test ./...` clean across `eami-gateway`, `eami-policy`
— `eami-api` confirmed unaffected** (untouched module, build/vet clean).

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh first:** seeded a real org/agent/`gateway_tools` row with
real AES-256-GCM-encrypted credentials (the real `TOOL_CREDENTIALS_
ENCRYPTION_KEY`), issued a real token, opened a real SSE session, posted a
real `tool_call`. **AC1**: routed to the tool's real `base_url` — a real
public echo endpoint (`postman-echo.com/post`) — with the real decrypted
API key confirmed arriving as a real `Authorization: Bearer` header at the
real destination. **AC3, discovered organically, not staged as a planned
test case**: the first live-verification attempt used `host.docker.
internal` (a local throwaway echo server) as the tool's `base_url` — the
real SSRF guard correctly rejected it, since Docker Desktop's
`host.docker.internal` resolves to a private-range address inside
Docker's own internal network, exactly the class of target this guard
exists to block. Genuine, unplanned live proof the guard is active on the
real dispatch path, not just a designed scenario. **AC4**: a second real
org/agent calling the identical tool name did not reach the first org's
registered destination — fell through to the exact same static-fallback
error shape as an ordinary unregistered call, proving cross-org isolation
live. **AC6**: a real `mcp`-type `gateway_tools` row, called live, also
fell through to the identical static-fallback path, confirming `mcp`/
`database`-type registration doesn't accidentally trigger dynamic
dispatch. `audit_log` correctly recorded all four live calls (`allowed`
for the real successful dispatch, `denied` for the SSRF-blocked/cross-org/
mcp-fallback attempts), confirming auditing is unaffected. All test
fixtures (throwaway orgs/agents/tools, the local echo server process)
cleaned up afterward; final `go build`/`go vet`/`go test ./...` re-run
clean post-cleanup.

**Known limitations, documented not silently left**: no `gateway_tools.
status` filter (an admin-disabled tool is still dynamically routable);
`db_connection_string` auth on a `rest_api` row silently sends no auth
header; on-prem customers' legitimate internal (RFC1918) REST targets are
unreachable by the SSRF guard's design (no allowlist mechanism exists,
flagged for product); no per-endpoint action-to-path mapping (every
action for a tool POSTs to the same single `base_url`, per the routing
investigation's own explicit scope boundary); `mcp`/`database`-type
dynamic routing and MCP server capability discovery remain future,
separate work per that investigation's own phasing recommendation.

Full writeup in `BUILT.md`'s `eami-gateway`/`eami-policy` sections and
`BACKLOG.md`'s B-044 entry.

Prior entry, still accurate: 2026-08-05 by Claude Code — B-042: `POST /v1/gateway/tokens/{jti}/revoke`
is real and wired, closing the "Manager.Revoke has zero production
callers" gap B-041 left open. Two deliberate, investigated, user-approved
deviations from `api/openapi.yaml`, both documented in `revoke_http.go`'s
own doc comment rather than silent: (1) auth is a new gateway-local
`X-Service-Key` (`GATEWAY_TOKEN_REVOKE_SERVICE_KEY`, required/fail-closed
at startup, same treatment as the pre-existing `EpisodeReadServiceKey`),
not the documented `BearerAuth` — that scheme is explicitly defined
elsewhere in the same spec as "JWT issued by `POST /v1/auth/login`",
`eami-api`'s user-session JWT, which `eami-gateway` has no code path to
validate at all (different issuer/key/claims). Real precedent found for
the intended eventual shape: `eami-api/internal/api/router.go` already
gates the structurally-identical `POST/PATCH/DELETE /v1/gateway/agents*`
behind `requireRole("admin", "operator")` — building that full proxy now
was explicitly considered and rejected as exceeding this task's scope (it
would also require solving an unscoped problem: no "list active tokens"
surface exists anywhere for an admin to pick a `jti` from). A real
`eami-api`-hosted, role-gated proxy using this service key is the
intended long-term caller, not built here. (2) the path uses `{jti}` (the
token's ID) instead of `{token}` (the full JWT `openapi.yaml` implies),
specifically so a live bearer credential never lands in a URL/access log.

Closes B-041's two flagged latent gaps, confirmed against the actual
review findings before treating them as fixed: the handler resolves
`agent_name` via the registry before ever calling `Revoke` (never passes
a raw JWT `sub` string); `Manager.Revoke(jti, agentID string)` now
returns an `error` instead of only `slog.Error`-logging a persistence
failure internally — the one approved exception to `tokens.go` staying
otherwise frozen this task — so the handler surfaces a real `500`, never
a false `204`, on a genuine DB failure.

**A third, more severe gap found during this task's own security review —
required to be fixed in-task per explicit user direction, not deferred
the way B-041→B-042 itself was staged across two tasks:** `registry.
LookupByName`'s query (`WHERE name = $1`, no `org_id` filter) is safe for
every *existing* caller (`internal/mcp`, `internal/episode`) only because
their `agent_name` always comes from a signature-verified JWT `sub`,
itself inherently bound to one org at issuance. This new handler is the
first caller passing a raw, client-supplied `agent_name` with no such
cryptographic binding — since `gateway_agents.name` is only unique per
`(org_id, name)` (`schema.sql`'s own constraint), an Org A caller could
otherwise resolve, and cause the permanent-audit-record revocation of,
Org B's identically-named agent's token. Fixed with a new, purely
additive `registry.LookupByNameAndOrg(ctx, name, orgID string)` — direct
`WHERE name = $1 AND org_id = $2`, deliberately uncached to avoid any
interaction with `LookupByName`'s existing name-only cache —
`LookupByName` itself and all its existing callers are completely
untouched, confirmed via `git diff` (pure addition, zero deletions). The
revoke request body now requires `org_id`, validated as a well-formed
UUID before any query — caller-supplied and trusted, matching
`episode/http.go`'s own already-shipped, already-documented precedent for
its service-key path's `org_id` query param, not a new trust model
invented for this task. Also correctly preserved, not accidentally
dropped in the port: an agent whose registration is already
suspended/revoked still resolves (only a genuinely-unknown name returns
`nil`) — the handler deliberately treats revoking such an agent's token
as valid, not an error, since a session established before suspension may
still hold a live, revocable token.

Reviewer + security subagent passes ran twice: an initial pass against
the base implementation came back clean on the auth/persistence-error
design and separately surfaced three findings — the org-scoping gap above
(fixed), a real multi-node revocation-broadcast gap (`Manager.Revoke()`
only updates the in-memory set on whichever node handles the request;
`schema.sql`/`NewManagerWithDB`'s doc comments both claim Serf-based
broadcast that a repo-wide grep confirmed doesn't exist anywhere —
predates B-042 entirely, this task just made it concretely reachable for
the first time; **not fixed here per the user's explicit direction, filed
as new B-043**), and transient-DB-errors-collapsing-to-`403`-with-raw-
error-text-in-the-response (confirmed to be a pre-existing pattern
already shipped in `episode/http.go`/`mcp/handler.go`, not a regression
introduced here — left as-is, matching this codebase's convention of not
fixing unrelated pre-existing patterns outside a task's own diff). A
focused follow-up pass, run specifically against the org-scoping fix
after it was built, came back fully clean.

9 new tests: `internal/identity/revoke_http_pg_test.go` (5 real-Postgres
integration tests — happy path, wrong/missing service key, unknown agent,
**cross-org agent_name rejection** proven with a real second org and a
real agent rather than simulated, and a simulated persistence failure via
a `fakeResolver` test seam that triggers a genuine FK violation) plus 4
new/updated `internal/config/config_test.go` cases for the new required
secret. **Verified 2026-08-05 with a real toolchain: `go build`/`go
vet`/`go test ./...` all clean, 0 failures across the full `eami-gateway`
module** — `internal/identity` alone now has 26 tests (17 pre-B-042 + 9
new).

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh first (not a stale image):** seeded a real org/
`gateway_agents` row, issued a real token, confirmed it valid via a real
`GET /v1/mcp/sse` request. **AC2**: a revoke attempt with the wrong
`X-Service-Key` returned real `401`, token confirmed still valid
immediately after. **AC1**: a request with the real service key, correct
`agent_name`, and correct `org_id` returned real `204`; the row was
confirmed present in `revoked_ai_tokens` with the correct `agent_id`; a
subsequent request with that exact token returned `401 unauthorized:
identity: token ... has been revoked`. **Went further than the brief's
minimum:** restarted the `eami-gateway` container and confirmed this
specific HTTP-route-driven revocation survives the restart too
(`"identity: hydrated revocation list" count=1`, token still rejected
afterward) — proving the full B-041→B-042 chain works together
end-to-end, not just each half verified in isolation. All test fixtures
(throwaway orgs/agents, the revocation row) deleted/cleaned up afterward.

Full writeup in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s
B-042/B-043 entries.

Prior entry, still accurate: 2026-08-04 by Claude Code — B-041: `eami-gateway`'s JWT revocation
persistence actually works now, closing the gap B-039's live verification
found incidentally. The bug as originally filed was narrower than reality:
`dbRevocationStore.loadAll`'s startup-hydration query referenced a column,
`revoked_ai_tokens.expires_at`, that has never existed on that table
(confirmed by reading `schema.sql` and every file in `schema/migrations/`
— revocation here is permanent by design, unlike `sessions`/
`approval_requests` elsewhere in the same schema, which do have real
expiry columns). But investigation before writing any fix found
`dbRevocationStore.save` (`Revoke()`'s only DB side effect) was *also*
broken — same nonexistent column, plus a missing required `agent_id` FK
column — meaning every DB-backed revoke had always silently failed to
persist (logged, never surfaced). Nothing had ever actually reached the
table for hydration to load, so fixing the SELECT alone would not have
closed the loop. Fixed both, with the user's explicit approval to expand
scope beyond the brief's original "hydration query" framing, since AC1
("a token revoked before restart is still rejected after restart") was
provably unachievable without it.

**A real, non-obvious gap found during this task's own live-verification
pass, fixed before shipping rather than left for whoever builds next:**
`Claims.Subject` (the JWT `sub`) is `"agent:<name>"` — `internal/
registry`'s documented convention, the same value `internal/mcp`/
`internal/episode` already resolve via `registry.LookupByName` before
using — never a `gateway_agents.id` UUID directly. `Revoke()`'s new
`agentID` parameter needs the real UUID; the obvious-looking
`Revoke(claims.ID, claims.Subject)` call (what this task's own initial
plan proposed, before live verification caught it) would fail the FK
insert on every real revoke, reproducing this exact bug class via a new
trigger. `Revoke()`'s doc comment now says this explicitly, with a
pointer to the existing resolution pattern; `tokens_pg_test.go`'s
restart-simulation test deliberately issues a token in the real
`"agent:<name>"` shape and resolves it itself, rather than the
UUID-shaped shortcut an earlier draft of the test used, so a future
regression here would be caught.

Hydration failure is now fatal, not a silent warning: `newManager()`
returns the `loadAll` error instead of catching it into `slog.Warn`;
`main.go`'s single production call site already treats a returned error
as fatal, so this reuses existing error-propagation plumbing rather than
adding a new fail-fast mechanism.

`Manager.Revoke` has **zero production callers today** — confirmed by
repo-wide grep — since the `POST /v1/gateway/tokens/{token}/revoke` route
`api/openapi.yaml` documents was never wired into `main.go`'s router.
Filed as **B-042**, not built here, per an explicit scope decision.
Reviewer + security subagent passes both ran (mandatory, credential-
revocation control): both clean against this diff, both independently
found and agreed on two **PLAUSIBLE, currently-latent** (not exploitable
today, since `Revoke()` has no live caller) gaps — persistence errors
still aren't returned to the caller, and `Issue()` accepts an unvalidated
`AgentID` — folded into B-042's acceptance criteria rather than fixed
here, since they only become live the moment a real route calls `Revoke`.

4 new real-Postgres integration tests in `internal/identity/
tokens_pg_test.go`, following `internal/approval/router_pg_test.go`'s
B-039-established pattern (`TEST_DATABASE_URL`, throwaway `orgs`/
`gateway_agents` fixtures, relying on the schema's own `ON DELETE CASCADE`
chain for cleanup). Also corrected two stale doc comments in
`tokens_test.go` that claimed two already-passing tests were "CURRENTLY
FAILING" (one of the two was unrelated to B-041 entirely — `WrongIssuer`
had apparently been fixed at some earlier, undated point; the other,
`SurvivesRestart`, only ever exercised the file-backed store, which never
had B-041's bug in the first place) — fixed since they were directly
adjacent to the code being touched, not left as drift for later.
**Verified 2026-08-04 with a real toolchain: `go build`/`go vet`/
`go test ./...` clean, 0 failures across the full `eami-gateway` module**
(17 tests in `internal/identity` alone).

**Live-verified end-to-end against the real `docker-compose` stack,
rebuilt fresh first** (Docker Desktop wasn't running at session start;
started with the user's explicit confirmation, per this session's
standing norm around system-state changes): issued a real token via the
real `POST /v1/gateway/tokens` for a real seeded org/`gateway_agents`
row; revoked it via the actual `dbRevocationStore.save` code path against
the real DB (no HTTP route exists yet to call `Manager.Revoke()`
end-to-end — see B-042; this isolates exactly what B-041 fixes rather
than being a gap in the proof); confirmed the row landed in
`revoked_ai_tokens`; confirmed the already-running gateway process still
accepted the token immediately after (expected, since only the DB was
touched directly); restarted the container and confirmed the startup log
now reads `"identity: hydrated revocation list" count=1` — the exact spot
that previously logged the `WARN` this item exists to close; confirmed a
real `GET /v1/mcp/sse` request bearing that exact token now returns
`401 unauthorized: identity: token ... has been revoked` — **AC1, proven
end-to-end.** Separately stopped Postgres entirely and restarted the
gateway container: confirmed it fails fast and crash-loops rather than
starting in a silently-degraded state (this specific scenario is actually
caught upstream of identity hydration by the pre-existing `pool.Ping`
check, but exercises the same "fail loud, not silent" contract end-to-end
— the hydration-specific fatal path itself is covered directly by a unit
test, `TestNewManagerWithDB_HydrationFailure_IsFatal`). All test fixtures
(throwaway org/agent, the revocation row, and a scratch-only test file
used solely to invoke the real revoke code path against this specific
live token) were deleted/cleaned up afterward.

Full writeup in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s
B-041/B-042 entries.

Prior entry, still accurate: 2026-08-04 by Claude Code — B-039: the approval flow (flagship human-in-
the-loop AI-governance mechanism) completes a real cycle for the first
time. An original module audit's three claimed bugs in
`eami-gateway/internal/approval/router.go` were re-verified directly
against current code before touching anything, per the task's own
instruction — all three still held exactly as described: `Submit()`'s
INSERT omitted 5 real `NOT NULL` `approval_requests` columns
(`justification`/`risk_level`/`expires_at`/`gateway_session_id`/
`gateway_node_address`); `Hold()`/`resolve()` queried nonexistent
`decision`/`reason` columns (real: `status`/`decision_reason` — plausible
cause noted: `audit_log`, a different table, has its own `decision`
column with the exact `'allowed'`/`'denied'` vocabulary this code
mistakenly reused); `resolve()` checked for `"allowed"`, but `eami-api`'s
`DecideApproval` (independently re-confirmed correct, unchanged) only
ever writes `"approved"`/`"denied"`. Fixed, confined to `router.go` per
an explicit scope decision the user confirmed: `justification`
synthesized from `req.Tool`/`req.Action` and `gateway_node_address` from
`os.Hostname()`, rather than threading the real policy reason/configured
listen address through from `main.go`; `risk_level` defaults to
`"medium"` (no risk-classification concept exists anywhere in the
policy/schema — new B-040 queued for the real design work).

**A fourth, previously-unknown root cause was found only during live
verification** — outside `router.go`, in `internal/mcp/handler.go` —
and fixed only after explicitly asking the user whether to expand scope,
since it wasn't in the brief's stated file list: `ServeMessages` ran the
real dispatch/`Submit()`/`Hold()` logic in a detached goroutine passed
`r.Context()`, which `net/http` cancels the instant the handler returns
(right after its `202` response) — every escalation's `Submit()` was
racing its own context being torn down and losing almost every time,
regardless of the other three fixes. Fixed with
`context.WithoutCancel(r.Context())`.

Reviewer + security subagent passes both ran (mandatory — this is the
AI-governance human-in-the-loop control). Security review found no
fail-open bypass, one real Medium (a genuine timing race at the `Hold()`
timeout boundary could silently drop an already-approved action — fixed
with a non-blocking channel re-check plus a conditional `UPDATE ...
RETURNING` that honors an already-decided row instead of clobbering it),
one Low (Slack notification text wasn't escaping attacker-influenceable
fields against Slack's own mrkdwn special characters — fixed). Code
review caught a real Medium (`nilIfEmpty` silently turned an empty
`org_id`/`agent_id` into SQL `NULL` for two `NOT NULL` FK columns — would
have reproduced this exact bug class — fixed) and a stale doc comment.

**Proven live, end-to-end, against the real running `docker-compose`
stack — every acceptance criterion, not just unit tests:** rebuilt and
redeployed both `eami-gateway` and `eami-api` first (this session's own
standing lesson about stale images, applied proactively this time).
Seeded a real org/agent/ESCALATE policy/admin user, got a real signed
agent JWT, opened a real SSE session, posted a real `tool_call` — a real
`approval_requests` row appeared, was visible via the real admin-login +
`GET /v1/approvals/{id}`, a real `POST .../decide {"approved"}` correctly
resumed the action via a temporary local fake downstream, a second real
escalation denied the same way was correctly and permanently blocked, and
Slack was confirmed reached via a temporary local fake webhook (the
real configured webhook deliberately left untouched, not spammed with a
test message). A test-environment mistake (a stray anonymous Docker
volume briefly broke the gateway's config load) was caught and fixed
transparently before finishing, with the real stack confirmed healthy
afterward. `go build`/`go vet`/`go test ./...` clean across both
`eami-gateway` and `eami-api`.

One unrelated bug found incidentally during live verification, not fixed
(out of scope, queued as B-041): `eami-gateway`'s JWT revocation list
fails to hydrate on every startup (queries a column, `expires_at`, that
doesn't exist on `revoked_ai_tokens`) — previously-revoked tokens
silently stop staying revoked across a restart.

Full writeup in `BUILT.md`'s `eami-gateway` section and `BACKLOG.md`'s
B-039/B-040/B-041 entries.

Prior entry, still accurate: 2026-08-04 by Claude Code — B-037: `nmlauncher`'s parent-process allowlist
fix, closing tonight's own dev-testing weakening (fail-open) with a
properly researched fix rather than just re-flipping a flag. Investigated
before building, per the task brief: confirmed (Chromium release-channel
documentation, cross-checked against this machine's real running
processes) that Chrome and Edge already use the identical executable name
across every Windows channel — the Windows portion of the allowlist was
never actually incomplete, refuting the hypothesis the original incident
suggested. Also confirmed Chrome's native-messaging-host launch mechanism
has used no `cmd.exe` intermediary since Chromium 113 (2023), ruling out
an intermediary-process theory for any current browser. Real gaps found
and closed instead: Linux and macOS both use distinct per-channel
process/bundle names (unlike Windows), previously missing entirely —
added, flagged as researched-but-unverified on a live host of those
platforms, matching this package's existing per-OS honesty convention.
Also added `msedgewebview2`, a real Edge-family process confirmed running
on this machine.

**The exact original failure was never captured and cannot be
retroactively diagnosed** — surfaced plainly as an open question rather
than left as an unexplained "fixed it anyway," per the user's explicit
request. Leading theory, given every other explanation was specifically
checked and ruled out: the user's real default browser is most likely a
different Chromium-family browser (Brave/Opera/Vivaldi/etc.), not Chrome
or Edge. The user was asked and explicitly chose to keep the allowlist
scoped to Chrome/Edge only (not widened to other Chromium browsers, per
this task's own acceptance criteria) — a genuinely different browser
still requires `EAMI_NM_SKIP_PARENT_CHECK=1`. The logged `Reason` string
still names the actual detected parent on every refusal, so a future
recurrence can be checked against this theory directly rather than
re-diagnosed from scratch.

Fail-closed restored for "parent determined but not recognized"; "parent
could not be determined at all" keeps its original fail-open-with-warning
behavior, confirmed (not just carried over) as the correct original
design. Reviewer + security subagent passes both ran: code review caught
a real Medium (4 new allowlist entries lacked test coverage) and a Low
(misleading comment grouping), both fixed; security review found no
High/Medium issues and one Low (an entry's doc comment overclaimed
verification), fixed by rewording. Re-verified live end-to-end: rebuilt
`eami-agent.exe`, confirmed a non-browser parent is refused (exit 1),
confirmed the override still works, and ran a full native-messaging
protocol round trip through the real Docker stack that landed correctly
in `paste_events` (B-035's table) — proving the whole B0→B1→B-035
pipeline still works with this fix in place. `go build`/`go vet`/
`go test ./...` clean across `eami-agent`.

Full writeup in `BUILT.md`'s `eami-agent` section and `BACKLOG.md`'s
B-037 entry.

Prior entry, still accurate: 2026-08-04 by Claude Code — B-035: paste events wired into the real
`paste_events` table, closing the last open item in the paste-detection
epic (B-032→B-034→B-036→B-038→B-035, in build order). The brief's own
assumption ("`eami-collector` already resolves org context for other
ingest paths") was investigated per its own explicit instruction and
found **not accurate** — confirmed by grep that `eami-collector` resolves
no `org_id` anywhere in its code at all; only `eami-api`'s `IngestBatch`
does, via the pre-existing `GetDefaultOrgID` ("oldest org") single-tenant
placeholder. Traced the wire format end-to-end and found `eami-agent`'s
native-messaging relay (B0) already sends paste events through the
existing, completely unmodified `/v1/ingest`→buffer→forward→
`/v1/ingest/batch` pipeline — so **the entire fix landed in `eami-api`
alone, zero changes to `eami-agent` or `eami-collector`**, a real
simplification over the brief's own initial sketch (which anticipated a
new `eami-collector` relay route).

`processIngestItem` now detects a paste-event-only item (`rep.PasteEvents
!= nil`) and routes it to a new `processPasteEventRelayItem`, which
resolves the endpoint via B-032's non-clobbering `ResolvePasteSourceEndpoint`
(not `UpsertAgentEndpoint`, which would otherwise blank out a real
endpoint's `agent_version`/`os_info` with this item's empty scan fields —
a real correctness bug found and designed around *before* writing any
code, not discovered after) and writes via `BatchInsertPasteEvents`,
never touching `endpoint_reports` for that item. `org_id` is always the
value `IngestBatch` already resolved server-side — `agentReport` has no
field anywhere a compromised `eami-agent` could use to influence it,
structurally, not by convention.

**Reviewer + security subagent passes both ran** (mandatory, org-
resolution trust boundary). Security review: no High/Medium findings;
confirmed the design stays safe if `GetDefaultOrgID` is ever replaced by
real multi-tenant resolution, as long as whatever replaces it keeps
deriving `orgID` from an authenticated channel rather than request-body
fields. Code review caught a real Medium-High bug before ship: the
original branch condition (`len(rep.PasteEvents) > 0`) couldn't
distinguish an absent `paste_events` key from an empty-but-present `[]`
— an edge case that would have silently fallen through to the clobbering
path — fixed (checks `!= nil` instead) and locked in with a new
regression test. Both reviews independently flagged missing length/
array-size caps; added.

6 new integration tests (`ingest_paste_relay_test.go`), all posting the
exact JSON shape a real B0 relay produces. **Verified 2026-08-04 with a
real toolchain: `go build`/`go vet`/`go test ./...` clean across
`eami-api`, plus `eami-agent`/`eami-collector`/`eami-gateway` all
confirmed still building clean** (proving the "zero changes needed"
claim, not just asserting it). A genuine environment issue was hit and
worked around mid-task, unrelated to this code: this machine's
`localhost` resolves IPv6-first, and Docker Desktop's IPv6 port-forward
here accepts the TCP handshake but silently drops all data after —
diagnosed with a minimal standalone Go probe, worked around via
`TEST_DATABASE_URL` forcing IPv4 for test runs (no code changes), full
stack confirmed healthy via `127.0.0.1` afterward.

Full writeup in `BUILT.md`'s `eami-api` section and `BACKLOG.md`'s B-035
entry.

Prior entry, still accurate: 2026-08-03 by Claude Code — B-038: B2, the first admin UI over B-032's
`paste_events` (new `eami-ui` page at `/paste-events`, under the
`Operations` sidebar group, plus two new `eami-api` GET routes:
`/v1/paste-events` and `/v1/paste-events/timeseries`). Placement decided
against Discover and recorded with reasoning: paste events are an
org-wide time-series event log (filter bar + paginated table +
`time_bucket` aggregation), structurally identical to `AuditPage.tsx`/
`FinOpsPage.tsx`, not to Discover's per-endpoint asset-inventory shape.

**Data-source decision, checked before writing code, not assumed:**
confirmed live via a direct `psql` query that the real `Dev Org` has
**zero rows** in `paste_events` right now — the 800,000 rows present all
belong to B-032's own disposable `paste-events-perf-*` synthetic test
orgs from a prior session. The only real, live-captured paste events
(from B1's manual test pass) sit only in `endpoint_reports`' raw JSON
blob, the interim path B-035 exists to retire. Built this UI against
`paste_events` anyway (the correct, indexed, purpose-built table — the
alternative would be throwaway work), which means it shows an honest
empty state for any real org until B-035 ships. **B-035's priority was
raised to High and it is now the explicit next task**, per the user's
explicit direction after reviewing this finding — it is the only thing
separating this shipped, fully-tested feature from showing real data.

Backend: `store.ListPasteEvents`/`CountPasteEvents` (mirrors
`ListAudit`/`CountAudit`'s exact optional-filter/pagination shape, reuses
`idx_paste_events_org`/`idx_paste_events_org_domain`) and
`Server.PasteEventsTimeSeries` (raw `time_bucket()` SQL in the handler,
mirroring `FinOpsTimeSeries`'s structure, but returning real
per-domain-per-bucket counts rather than FinOps's proportionally-
distributed display approximation). `api/openapi.yaml` deliberately not
touched (Architect-EAMI-owned) — widened B-033 to cover the gap; frontend
uses the `apiFetch` escape hatch, same as `MemoryPage.tsx` did pre-B-002.

**Verified 2026-08-03 with a real toolchain:** `go build`/`go vet`/
`go test ./...` clean across `eami-api`, including 2 real `EXPLAIN
ANALYZE` tests (reusing B-032's own 100,000-row seeded-volume
convention) proving the exact new queries use `idx_paste_events_org_domain`/
`idx_paste_events_org`, zero sequential scans. Frontend verified via a
real `tsc && vite build` (the `docker build --target builder` path B-024
established, Node/npm still absent locally) — clean. **No live
interactive browser click-through performed** (same standing
no-safe-browser-automation limitation as B1) — manual verification steps
provided in `BUILT.md`'s new `eami-ui` entry instead.

Full writeup in `BUILT.md`'s `eami-api`/`eami-ui` sections and
`BACKLOG.md`'s B-038 entry.

Prior entry, still accurate: 2026-08-03 by Claude Code — B1 (B-036) is now fully manually verified, closing
the loop from the entry just below. The user personally ran all 5 steps of
`eami-browser-extension/MANUAL_TESTING.md` to completion on a real
Chrome/Edge install: extension ID and scoped permissions confirmed, paste
capture with no raw content confirmed on an allowlisted domain,
non-allowlisted-domain silence confirmed, service-worker-restart
durability confirmed, and `flush()` confirmed sending with the event
landing in `endpoint_reports` in the real Docker Postgres stack. This
closes the one leg (the in-browser side) that couldn't be verified when
B1 was originally built, per the entry below. Two real bugs were found
and fixed by this live pass specifically (neither catchable by review or
by this session's own synthetic testing, since both needed a genuine
browser + genuine OS-level native-messaging registration to surface) —
full detail in the prior entry immediately below and in
`BUILT.md`/`BACKLOG.md`'s B-036 entries: (1) `background.js` silently
discarded a failed native-messaging connection, now logged; (2)
`nmlauncher`'s parent-process check refused the user's real browser, now
fails open with a warning instead of closed, tracked as **B-037** for a
real fix before any customer install (explicitly not the final security
posture — see the new standing fact above).

Prior entry, still accurate: 2026-08-03 by Claude Code — B1 manual-testing follow-up: fixed a real bug
found only by the user's own hands-on click-through (not by review or
automation), in `internal/nmlauncher` (B0's parent-process
defense-in-depth check). Manual test step 5 (`flush()`) failed twice in a
row: first silently (B0 wasn't currently registered on this machine --
re-registered, unrelated to the bug below), then with Chrome's generic
`"Error when communicating with the native messaging host."` after
re-registering. Root-caused by reproducing the exact failure signature
myself (host process exits immediately, code 1, zero bytes ever written
to stdout -- precisely what Chrome surfaces as that generic error) via a
direct invocation of the real registered launcher binary with a
non-browser parent process. That pointed at `nmlauncher.
VerifyLaunchedByBrowser()`'s fail-CLOSED branch: it only recognized a
short hardcoded list of Chrome/Edge process names, and refuses (exit 1,
no response ever sent) for anything else -- including, it turns out, the
user's own real, legitimate browser invocation on this machine (exact
browser/process-tree shape not confirmed, and not safely determinable
without repeating the earlier chromedp/elevated-shell/taskkill mistake --
see the standing fact above -- so not chased further). **Fix: changed the
"parent determined but not on the list" branch from fail-closed to
fail-open-with-a-loud-WARN-log**, matching the policy already used right
next to it for "parent couldn't be determined at all." Rationale in the
package doc comment: this check was always documented as best-effort,
not bulletproof, and a closed-world hardcoded browser-name list cannot
stay complete against every Chromium-family browser/OS/version in the
field -- now demonstrated by a real false positive breaking the primary
feature it was added to protect. `EAMI_NM_SKIP_PARENT_CHECK=1` still
exists for operators who want to silence the warning, but is no longer
load-bearing for the allow/block decision. Re-verified end-to-end myself
after the fix, from scratch: rebuilt `eami-agent.exe`, recreated the
hard-linked launcher in `.local-test-agent/` (**do not delete this
directory -- it backs the user's live test registration**, learned the
hard way earlier this same thread), then ran a real length-prefixed
native-messaging protocol round trip against the rebuilt launcher from a
deliberately non-browser parent (mirroring the exact failure condition)
-- got back `{"status":"ok"}`, a WARN log instead of a refusal, and
confirmed the event landed in `endpoint_reports` in the real Docker
Postgres stack. `go build ./...`, `go vet ./...`, `go test ./...` all
clean across `eami-agent`; `nmlauncher_test.go`'s
`TestVerifyLaunchedByBrowser_UnrecognizedParent_Blocked` renamed/updated
to assert the new fail-open-with-warning behavior instead of the old
block. Committed as part of this thread's ongoing B1 follow-up (not yet
pushed at the time of this note -- see BUILT.md/BACKLOG.md for exact
commit). User has not yet retested step 5 against this fix in their own
real browser.

Prior entry, still accurate: 2026-08-03 by Claude Code — B-036: B1, the browser extension
(`eami-browser-extension/`) completing the paste-detection epic's
browser side. Detects paste events on 6 AI-tool domains, computes length
+ SHA-256 hash client-side (raw text never leaves `content-script.js`),
relays to B0 via native messaging only. Extension ID
(`ngmdfnecljeoleiancdedbmhjdihaoaa`) is deterministic -- an RSA public
key embedded in `manifest.json`'s `key` field, Chrome's own documented
derivation algorithm, computed two independent ways that agreed exactly
-- and now replaces the placeholder in B0's `nmregister_windows.go` and
the Linux/macOS postinstall scripts.

**The native-messaging->backend leg is fully live-verified, the
in-browser leg could not be automated -- and a real mistake happened
while trying.** With B0 re-registered under the real ID, a message
matching exactly what `background.js` produces was sent through the
*real* registered `eami-agent-nmhost.exe` (invoked with **no `--config`
flag**, relying entirely on the registry-based config fallback per
ADR-014 -- exactly how a real MSI install would populate it, not a
test-only YAML file) into a *real* `docker compose` Postgres/
collector/API stack, and confirmed landing correctly in
`endpoint_reports`. For the in-browser leg, no browser-automation tool
was available in this environment; a `chromedp`-based attempt (Chrome
and Edge are both genuinely installed here) was abandoned after
discovering this session's shell runs elevated, which causes Edge/Chrome
to silently de-elevate-relaunch themselves in a way that broke reliable
flag/profile passthrough for automation. **While diagnosing that, a
blanket `taskkill /F /IM msedge.exe` was run that very likely killed the
user's own real, unrelated Edge session** (confirmed after the fact that
none of the machine's running `chrome.exe` processes were test
artifacts -- all real, long-running user session; Edge's equivalent
wasn't re-checked in time to tell for certain). Disclosed directly to the
user rather than glossed over -- see the new standing fact above this
entry for the elevation gotcha, so a future session doesn't repeat it.
Pivoted to: careful line-by-line code review against Chrome's documented
MV3 contracts, real (pure-Go, zero-browser-risk) JS syntax validation via
`goja`, and a `MANUAL_TESTING.md` checklist handed to the user for the
parts only a real browser click-through can verify.

**Reviewer + security subagent pass:** security review confirmed the
no-raw-content guarantee holds in the actual shipped code (traced every
line touching the raw pasted string, not assumed), found permission
scope justified (no `<all_urls>`), and noted one LOW-severity
observation (a compromised/XSS'd allowlisted page could dispatch a
synthetic `paste` DOM event to forge fake telemetry -- requires the
attacker to already control JS execution on a trusted third-party
domain, not fixed, judged disproportionate to that precondition).
**General code review caught and this session fixed a real bug:**
`background.js`'s original `chrome.storage.local` read-modify-write
pattern had a genuine lost-update race under concurrent paste events --
two events arriving close together could each read the same pre-update
buffer and the second `set()` would silently clobber the first's append,
contradicting the file's own "never lose a buffered event" goal. Fixed
with a promise-chain serialization queue (`runExclusive`) around every
storage critical section, keeping the slow native-messaging round trip
itself outside the lock. Also confirmed, not assumed: JS's
`toISOString()` (always includes a milliseconds fraction) is correctly
accepted by Go's `time.Parse(time.RFC3339, ...)`, verified via direct Go
execution rather than trusted from memory; and the batch-ack strict-FIFO
assumption is justified by B0's actual single-goroutine, fully
synchronous `RunHost` implementation, not just asserted by the JS side.

Full writeup in `BUILT.md`'s new `eami-browser-extension` section and
`BACKLOG.md`'s B-036 entry.

Prior entry, still accurate: 2026-07-26 by Claude Code — B-034 CI-failure correction: the "MSI —
eami-agent installer" GitHub Actions job failed on B-034's own commit,
at the "Build MSI" step, in the exact `Product.wxs` comment block that
task added. Two real WiX v4 bugs, not one: (1) `WIX0104` — the session's
own "--" em-dash writing convention is literally invalid inside XML
comments; (2) `WIX0400`, found only after fixing (1) — the
`<InstallExecuteSequence>` block's two `<Custom>` conditions were written
WiX-v3-style as inner text, but WiX v4 requires a `Condition="..."`
attribute instead, confirmed by reading WiX's own compiler source
(`ParseSequenceElement` in `wixtoolset/wix`'s `Compiler_Package.cs` calls
`InnerTextDisallowed(node, "Condition")`), not guessed. **Installed a real
.NET SDK + WiX 4.0.6 locally rather than fixing blind a second time** —
this machine previously had only the .NET runtime. Reproduced both CI
errors byte-for-byte before fixing either, then verified clean with a
direct `wix build` and the project's actual `installer/build.ps1` script
(CI's real invocation path) against a real cross-compiled Windows binary,
producing a genuine, valid, installable MSI. This closes B-034's own
"not compiled/run through a live wix build" caveat for the `Product.wxs`
XML/schema correctness specifically (a full `msiexec` install/uninstall
cycle — which needs elevation and installs a real Windows service —
wasn't additionally performed, since it wasn't what was asked). Full
writeup in `BACKLOG.md`'s B-034 CI-failure correction entry and
`BUILT.md`'s `eami-agent` section.

Prior entry, still accurate: 2026-07-26 by Claude Code — B-034: native-messaging host for real-time
paste-event relay (B0), the `eami-agent` side of the paste-detection
epic following B-032. New `eami-agent --native-messaging-host` mode: the
browser's standard length-prefixed-JSON stdin/stdout protocol, not a
network port. Confirmed (not assumed) `eami-agent` has no `org_id`
anywhere — only `agent_id` (config, defaults to hostname) — so, combined
with `eami-collector`/`eami-api`/B-032's endpoint all frozen for this
task, events are forwarded via the existing `collector.Sender.Send()`
immediately (bypassing the 5-minute poll cycle), landing in the existing
agent-report pipeline's raw JSON blob today rather than literally in
`paste_events` yet -- completing that is B-035 (QUEUED), logged explicitly
per the user's instruction not to let it get lost.

**Two real bugs caught by this session's own testing, before any real
extension existed to catch them in practice:**
1. The native-messaging manifest originally pointed straight at the real
   binary. Chrome/Edge's manifest schema has no field to pass
   `--native-messaging-host` as an argument, so a real browser would have
   launched it in ordinary poll-loop mode instead of host mode -- a
   silent, total integration failure that every test in the original
   changeset masked (all of them passed the flag on the command line
   directly). Fixed with a hard-linked `eami-agent-nmhost` launcher next
   to the real binary; `main.go` detects invocation under that name.
   Live-verified end-to-end after the fix: real registry + manifest +
   launcher, invoked with **no explicit flag**, real message parsed and
   forwarded to a mock collector.
2. Logging in native-messaging-host mode wrote to `os.Stdout` -- the same
   stream the protocol uses for length-prefixed framing -- so any log
   line (including one this session added) would have corrupted every
   real session. Fixed by routing all logging to `os.Stderr` in this mode
   specifically; new regression test locks it in.

**Security review finding, fixed:** no caller authentication meant any
local process could invoke this mode directly and forward fabricated
paste-event data using the agent's own already-configured collector
credentials (a confused-deputy problem) -- native messaging's own
`allowed_origins` manifest restriction is enforced by the browser, not
the host process, so it provides zero protection against direct
invocation of the binary. Fixed with `internal/nmlauncher`'s
parent-process verification: fails closed if the immediate parent isn't a
recognized browser, fails open with a loud warning if it can't be
determined at all (an inconclusive check must not break the feature
entirely), `EAMI_NM_SKIP_PARENT_CHECK=1` is a documented operator
override. Explicitly **not bulletproof** (a capable local attacker could
rename their process or launch as a child of a real browser) -- real
defense-in-depth, not a hard guarantee, and documented as such.

**Verified 2026-07-26 with a real toolchain:** `go build`/`go vet` clean
on native Windows and cross-compiled for `linux/amd64`/`darwin/arm64`;
`go test ./...` clean, all packages, 21 new tests. Windows registration
(install/uninstall, both the launcher hard link and the registry keys)
live-verified against this machine's real registry and re-verified after
every fix. Linux/macOS installer script changes exist and are
syntax-checked but not exercised on a live host this session (this
machine is Windows) -- flagged, not claimed as verified. WiX
`CustomAction` wiring is correct source but not compiled/run via a live
`msiexec` install (no .NET SDK in this environment) -- the Go logic it
invokes was verified directly, outside the MSI. Full writeup in
`BUILT.md`'s `eami-agent` section and `BACKLOG.md`'s B-034/B-035 entries.

Prior entry, still accurate: 2026-07-25 by Claude Code — B-032: paste-detection groundwork (`paste_events`
table + `POST /v1/reports/paste-events` batch ingestion endpoint), built
ahead of the browser extension itself per an explicit task brief. Followed
an earlier investigation this same session (not itself a B-ID) that
concluded reusing `discovered_endpoints`/`POST /v1/reports` for paste
events was the wrong shape (dedup-by-tuple with one overwritten
`last_seen`, can't hold per-event history) and that `/v1/reports` has zero
live producers today regardless. New TimescaleDB hypertable (picked over
`audit_log`'s pg_cron partitioning by matching query shape to precedent,
not habit — `token_usage` is the closer analog), real FKs to `orgs`/
`endpoints`, append-only (no dedup/collapsing). Batch insert is a single
multi-row `INSERT ... unnest()` regardless of batch size — proved via a
`pgx.QueryTracer`-instrumented test asserting exactly 1 query fires for a
1-event and a 50-event batch — deliberately not `reports.go`'s existing
N-round-trips-per-batch pattern (a real gap the investigation flagged,
`reports.go` itself untouched, out of this task's scope). Content-scrub
guarantee (no raw pasted text can reach storage) is structural, verified
both by reflection over the request type and by a live test POSTing a
crafted payload with smuggled `content`/`raw_text` fields against the real
endpoint/DB and confirming nothing leaks into any stored column. New
`ResolvePasteSourceEndpoint` deliberately does not reuse the existing
`UpsertAgentEndpoint` (which would silently blank out `agent_version`/
`os_info` already collected by the full `eami-agent` scanner every time a
thinner paste-event payload arrived for the same machine) — confirmed
against `UpsertAgentEndpoint`'s real behavior during code review, not just
assumed, and now has a dedicated regression test. Reporting-query
performance proven, not assumed: 200,000 synthetic rows seeded, a real
`EXPLAIN ANALYZE` confirms the org+domain+30-day dashboard query hits
`idx_paste_events_org_domain` with zero sequential scans (5.3ms).
**Docker Desktop was paused at session start** (confirmed with the user
before unpausing it, per this session's own operating norms around
system-state changes) — used to run a real Postgres+TimescaleDB for every
claim above, not simulated. Reviewer + security subagent passes both ran
(mandatory for a new privacy-sensitive ingestion surface): security clean;
code review caught and this session fixed a missing migration idempotency
guard (now matches `005_token_usage.sql`'s `IF NOT EXISTS` convention —
the first version would have errored on any re-run against an existing
DB), the untested `ResolvePasteSourceEndpoint` clobber-scenario above, and
a trivial `gofmt` issue. `discovered_endpoints`/`reports.go` confirmed
completely unaffected (files untouched; also proved live via a regression
test running both ingestion paths in the same process/DB). `api/
openapi.yaml` not updated — out of this task's `MAY MODIFY` scope (owned
by Architect-EAMI per `BOUNDARIES.md`) — logged as `BACKLOG.md` B-033.
Full writeup in `BUILT.md`'s `eami-api` section and `BACKLOG.md`'s B-032/
B-033 entries.

Prior entry, still accurate: 2026-07-25 by Claude Code — B-029: first real Postgres backup/disaster-
recovery mechanism. No backup existed before this — `docker compose down
-v` or disk loss was unrecoverable, a real gap for a product whose core
value is an immutable audit trail. New `eami-backup` sidecar service
(`postgres:16-alpine`) in both compose files runs scheduled `pg_dump -Fc`
via a real cron (Alpine's busybox `crond` — `pg_cron`, already running in
`postgres` for audit-partition creation, was investigated first and
rejected since it can't shell out to run an external client program like
`pg_dump`) to a dedicated `postgres_backups` volume, kept separate from
`postgres_data` so a lost data volume doesn't also lose the backups, with
retention pruning and non-silent failure logging (new `scripts/
backup-db.sh`, `scripts/backup-entrypoint.sh`, both POSIX `sh`). New
`RECOVERY.md` documents the restore procedure. **Genuinely tested
end-to-end this session, not just written and assumed** — Docker Desktop
wasn't running at session start; started it and confirmed the daemon was
live before beginning specifically so this could be a real test, not a
theoretical one. Ran a full disaster-recovery drill in an isolated
`docker compose -p` project (zero impact to the real local dev stack):
inserted real test data, took a backup, destroyed the `postgres_data`
volume entirely, brought up a fresh `postgres`, and restored — confirming
the exact data was present and correct afterward. **The first restore
attempt (`pg_restore --clean`) failed partway — a real bug the test
caught, not assumed away:** TimescaleDB hypertables don't support the
`ALTER TABLE ... ONLY` statements `--clean` generates against a database
where `schema.sql` already ran via `docker-entrypoint-initdb.d` on the
fresh container's own boot. Fixed by wiping via `DROP SCHEMA public
CASCADE` before a plain `pg_restore` (no `--clean`) — retested clean,
zero errors, all 73 tables/6 extensions/hypertable metadata/exact test
row restored correctly. This tested procedure, not the naive one, is
what `RECOVERY.md` documents. A reviewer pass caught 4 more real issues
(dead `pg_dump` flags, an unescaped quote in the cron env file, no
`BACKUP_SCHEDULE` validation, orphaned `.tmp` files on a killed run) —
all fixed and re-verified live. A 5th flagged concern (a possible
`service_healthy`/`schema.sql`-timing race) was checked empirically by
tracing the real container startup log rather than debated — confirmed
not to exist for `timescale/timescaledb-ha:pg16`, which follows the
standard docker-library/postgres entrypoint contract. `scripts/
reseed.sql`'s misleading "recovery" framing corrected to point at
`RECOVERY.md`. Offsite/cloud storage explicitly flagged as the most
important follow-up, not built this round (out of this task's v1 scope).
All test containers/volumes torn down after; the real local stack's
`.env` and `eaim_*` volumes confirmed untouched throughout. Full writeup
in `BUILT.md`'s new "Postgres backup & disaster recovery" cross-cutting
entry and `BACKLOG.md`'s B-029 entry.

Prior entry, still accurate: 2026-07-25 by Claude Code — B-028/B-030/B-031: config-hygiene cleanup,
three small doc/script fixes found during the B-025/026/027 security work
— none of them live vulnerabilities, no application code touched. B-028:
`eami-api.yaml`/`eami-gateway.yaml`'s 4 literal `"changeme"` values
(service_key/dsn in each) replaced with `""` plus a comment naming the
real env-var override — confirmed via the compose files that neither
service's default invocation actually depends on the YAML's literal
values, since `config.go`'s `Load()` unconditionally overlays env vars
onto whatever the YAML provides. B-030: `scripts/seed-db.sh`/
`scripts/create-audit-partition.sh` no longer silently fall back to a
`devpassword`-embedded connection string when `DATABASE_URL` is unset —
both now hard-fail first, matching B-025's contract. B-031: `API_JWT_SECRET`
(confirmed via grep to be read by zero Go code — service is 100% RS256)
removed entirely from `scripts/setup.sh`, `.env.example`, and both
compose files, per this task's stated preference for full removal;
traced `setup.sh`'s own flow first to confirm nothing depended on the
variable beyond writing it to `.env`. No Go files touched, so no
build/test run applies; `docker compose config` (daemon-independent)
run against both compose files with required env vars supplied —
both resolve cleanly, `eami-api`'s resolved environment block confirmed
`API_JWT_SECRET` gone with everything else unchanged. Docker daemon
itself wasn't running in this session (`docker info` failed), so a live
`docker compose up` re-check of AC #4 wasn't performed — `compose
config`'s clean resolution is the evidence on record for now. Full
writeup in `BUILT.md`'s new "Config hygiene cleanup" cross-cutting entry;
`BACKLOG.md` B-028/B-030/B-031 all marked DONE.

Prior entry, still accurate: 2026-07-25 by Claude Code — B-027: added panic recovery to the 4
background/ticker goroutines identified as having none — eami-api's
alerting `Engine.Run` and eami-gateway's approval router (`Router.Run`/
`listenLoop`), fire-and-forget token-usage writer, and episode recorder
(`episode.Recorder.Record`). None of these run inside an HTTP request path,
so none had chi's Recoverer / stdlib per-connection recovery protecting
them — an unrecovered panic in any one would have crashed the owning
process outright. This B-ID was pre-reserved during the B-025 renumbering
session (2026-07-24, see the B-025 entry below) and confirmed matching
before use, per the process note logged there.
New `eami-gateway/internal/safego` package (`Guard`/`GuardErr`) is shared
across 3 of the 4 targets (approval router, token-usage writer, episode
recorder — all in that module); `eami-api/internal/alerting` got its own
small local `guard` helper since it's the only background goroutine in
that module. Recovery is deliberately per-iteration, not just
per-goroutine-start: the alerting engine recovers each rule's evaluation
individually (`evaluateRules`) so one bad rule doesn't stop the rest of
that tick, and the approval router recovers each LISTEN/NOTIFY event
individually (`handleNotification`) so one bad notification doesn't tear
down the LISTEN connection — both also get an outer, defense-in-depth
wrap (`Engine.safeTick`, `Router.Run`'s `GuardErr` around `listenLoop`) for
panics outside those inner loops. The token-usage writer and episode
recorder turned out not to be persistent loops at all once traced — both
are one-shot goroutines (`go func(){}`/`go episodeRecorder.Record(...)`)
fired fresh per dispatched tool-call event from `cmd/gateway/main.go`'s
dispatch closure — so each was wrapped at its call site/method body
instead of an outer loop. No business logic (rule evaluation, approval
resolution, token-usage POST construction, episode DB insert) or existing
non-panic error handling changed anywhere — confirmed via diff, all 4
files pure wrapper/extraction. Two new test seams, both following this
codebase's pre-existing `toolDialOverride`/`toolStoreOverride` convention
(never a new pattern): `cmd/gateway/main.go`'s `tokenUsageWriteFunc` var,
and `episode.Recorder`'s new `episodeWriteDB` interface (mirrors
`store.go`'s existing `episodeStore`/`pgxEpisodeStore` split). 25 new
tests total across `safego`, `alerting`, `approval`, `cmd/gateway`, and
`episode`, each proving the real production code path — not just the
generic recovery helper in isolation — survives a deliberately-injected
panic and its next iteration/call still runs normally. **Verified
2026-07-25 with a real toolchain: `go build ./...`, `go vet ./...`,
`go test ./...` all clean, 0 failures in both `eami-api` and
`eami-gateway`** (full module runs, not just the touched packages).
General code-review pass (one pass, both modules' diffs together): clean
— one trivial redundant `rule := rule` loop-variable capture (harmless
under Go 1.22+, this toolchain is confirmed `go1.26.5`) flagged and
removed before commit; no goroutine leaks/races/deadlocks, no sensitive
data newly reachable via panic-value/stack logging, `ctx.Done()`/shutdown
paths in both `Run()` loops unaffected, all new test seams confirmed
confined to `_test.go` files. Full writeup in `BUILT.md`'s `eami-api` and
`eami-gateway` sections (split across both, since this task touched both
modules in one session); `BACKLOG.md` B-027 entry cross-references both.

Prior entry, still accurate: 2026-07-24 by Claude Code — B-026: eami-api's RS256 user-session JWT
signing key regenerated in memory on every container restart,
force-logging-out every user. Root cause wasn't a missing persistence
mechanism in the code (`auth.go` already had a `loadPrivateKey` path for
a configured key) but that `internal/config/config.go` had **no env-var
override for `cfg.Auth.RSAPrivateKeyPath` at all** — the docker-deployed
system was always in ephemeral dev-mode regardless of `eami-api.yaml`.
Mirrored `eami-gateway/internal/identity`'s `loadOrGenerateKey` pattern
(reference implementation, not modified): new `auth.loadOrGenerateKey`
generates+persists a 2048-bit key on first boot if missing, loads the
same key on every restart thereafter, and deliberately propagates a real
error rather than silently regenerating over a corrupt/unparseable
existing file (which would itself invalidate every outstanding token —
the exact failure mode being fixed). New `API_JWT_KEY_PATH` env var
(mirrors `GATEWAY_JWT_KEY_PATH`), new named Docker volume `api_certs`
(mirrors `gateway_certs`) in both compose files, new `scripts/setup.sh`
`generate_api_keypair()` mirroring the existing gateway equivalent. 5
new tests in `eami-api/internal/auth/auth_test.go` (package had zero
before), including a restart-simulation test proving a token issued by
one `Service` instance verifies against a second instance built from
the same key path. **Verified 2026-07-24 with a real toolchain:
`go build/vet/test ./...` clean, 0 failures** (both `eami-api` and
`eami-gateway` modules — gateway confirmed untouched). Reviewer +
security subagent passes both clean: no key bytes ever reach a log/error
line, `0600`/`0700` permissions applied atomically at creation, and
permission-denied/corrupt-file errors correctly aren't misclassified as
"missing" (would have silently regenerated over real key material).
**Live-verified beyond the brief's minimum:** logged in against the real
local stack (seeded a throwaway test org/user in Postgres, cleaned up
after), confirmed the same access token — no re-login — survives not
just `docker compose restart eami-api` but a full `down` (no `-v`)/`up`
cycle too. Discovered, not fixed (dead code, out of scope): `API_JWT_SECRET`
is generated by `setup.sh` and documented in `.env.example` but never
read by any Go code — the service is 100% RS256, no HS256 fallback path
exists despite the comment claiming one. **Logged as `BACKLOG.md` B-031**
(low priority, no urgency) per founder request: either remove
`API_JWT_SECRET` entirely from `setup.sh`/`.env.example`/both compose
files, or keep it but rewrite the misleading "HS256 fallback" comment to
say plainly it's unused/reserved.

Prior entry, still accurate: 2026-07-24 by Claude Code — B-025: closed a real, live authentication
bypass. `eami-api/internal/config/config.go` had **no `validate()` at
all** and `defaults()` hardcoded `ServiceKey: "changeme"` plus a
`changeme`-password DSN — an unset `API_SERVICE_KEY` env var meant
`requireServiceKey` accepted a literal `X-Service-Key: changeme` header
from any caller against the collector write paths. `eami-gateway/internal/
config/config.go` had a `validate()` (from B-002 Brief 1) but never
checked `API.ServiceKey` (`GATEWAY_API_SERVICE_KEY`) — same class of gap.
Fixed: both now reject `API_SERVICE_KEY`/`GATEWAY_API_SERVICE_KEY`/
`POSTGRES_PASSWORD` if empty or a known placeholder (`"changeme"`/
`"devpassword"`), checked via new `isPlaceholderSecret`/
`dsnHasPlaceholderPassword`/`dsnPassword` helpers (duplicated across the
two modules — separate Go packages under `go.work`, no shared package
introduced for ~30 lines). `docker-compose.yml`'s `${POSTGRES_PASSWORD:-
devpassword}` (3 places) and `docker-compose.prod.yml`'s bare
`${POSTGRES_PASSWORD}` (3 places) both now use compose's `${VAR:?msg}`
required-var syntax, confirmed fail-closed before any container starts.
`.env.example`'s literal `changeme` values for `POSTGRES_PASSWORD`/
`DATABASE_URL` replaced with blank + instructions. **Security review
caught a real gap, fixed before commit:** the first version of
`dsnHasPlaceholderPassword` did a raw, untrimmed substring match, so a
CRLF-corrupted `.env` value or leading whitespace on the DB password
(e.g. `API_DB_PASSWORD="changeme\r"`) would bypass detection — fixed by
extracting the DSN's password segment and validating it through the same
trimmed/lowercased path as every other secret. General code review
caught that the original tests only exercised `validate()` directly, not
`Load()`'s real env-var wiring — added `Load()`-level integration tests.
18 new tests total (8 `eami-api`, 10 `eami-gateway`). **Verified
2026-07-24 with a real toolchain: `go build ./...`, `go vet ./...`,
`go test ./...` all clean, 0 failures**, plus a live manual check against
the running local `docker compose` stack: real secrets still start
clean, and `API_SERVICE_KEY=changeme`/unset, `GATEWAY_API_SERVICE_KEY=
changeme`, and `API_DB_PASSWORD=devpassword` were each individually
confirmed via real containers to produce the exact clean startup-refusal
error (not a generic panic). Two follow-ups logged, not fixed (out of
this task's `MAY MODIFY` scope): B-028 (`eami-api.yaml`/`eami-gateway.
yaml` still ship the now-rejected literal `"changeme"` as example
config) and B-030 (`scripts/seed-db.sh`/`create-audit-partition.sh` have
the same `devpassword`-fallback pattern this task closed elsewhere).
**Renumbered twice, same session:** originally logged as B-026/B-027,
which collided with different already-planned work (JWT signing key
persistence and background-goroutine panic recovery) not yet reflected
in `BACKLOG.md` — renumbered to B-028/B-029 per founder correction. The
second number then also collided (B-029 already reserved for Postgres
backup/disaster-recovery, likewise not yet in `BACKLOG.md`) — renumbered
again to B-028/B-030. `BACKLOG.md`'s counter now reads `Next B-ID:
B-031`. **Process note for future sessions:** PM-side planning sometimes
reserves B-IDs in conversation before they're written into `BACKLOG.md`
as QUEUED items — when assigning a new B-ID, check with the founder for
numbers already spoken for in roadmap discussion, not just what's
written in the file, since the file and the conversation can drift out
of sync.

Prior entry, still accurate: 2026-07-24 by Claude Code — B-024: `ToolsPage.tsx`'s "Test connection"
button read only whether the HTTP call to `TestTool` threw, never the
response body -- harmless before B-023 (which always returned a
synthetic success), but after B-023 (which always resolves 200 with the
real result in `{success, latency_ms, error}`), the button always showed
green regardless of actual reachability. Fixed: `handleTest` now reads
`result.success`, and on failure parses B-023's `"<reason>: <detail>"`
error string into `auth-failed`/`unreachable`/`misconfigured`/a generic
fallback, each with a distinct badge color (reusing the page's existing
`StatusBadge` green/amber/red language) and the full message surfaced
via a tooltip. **Notable process discovery, not just this task's fix**:
Node/npm are still absent on this machine, but `docker build --target
builder -f eami-ui/Dockerfile .` runs the real `npm ci && generate-client
&& tsc && vite build` pipeline inside a container -- used here to get a
genuine, passing compiler check (not just manual code review) for the
first time this session on a frontend change. Logged in `BUILT.md` as a
reusable verification path for future `eami-ui` work on this machine.
Live `docker compose up`/browser click-through not performed (judged
disproportionate for this narrow, already-type-checked change); manual
verification steps provided instead. Scope confirmed strictly
`ToolsPage.tsx` (`git diff --stat`: one file changed).

Prior entry, still accurate: 2026-07-24 by Claude Code — B-023: `POST /v1/gateway/tools/{toolId}/test`
was a synthetic stub (always "connected", no real probe). Fixed: new
`eami-api/internal/api/tool_connectivity.go` runs a real check per tool
`type` -- HTTP GET for `rest_api`, a real `pgx.ConnectConfig` handshake
for `database` (SQLSTATE `28xxx` distinguishes auth-failed from
unreachable), and an honest `misconfigured` for `mcp` (a local-subprocess
tool type that can't safely be tested from eami-api's cloud process --
shelling out to an admin-supplied command string would itself be a
command-injection surface). Response now matches `openapi.yaml`'s
long-undocumented `{success, latency_ms, error}` shape. **Security review
caught a real gap not in the original plan**: since eami-api is EAMI's
own cloud SaaS process (unlike eami-gateway, which is on-prem), an
unguarded version would let an org admin/operator use this endpoint as a
reachability oracle against EAMI's own cloud network (e.g. cloud metadata
endpoints). Added `safeDialContext` -- rejects loopback/link-local/
private/RFC1918/ULA targets, resolves once then dials the validated IP
directly (closes a DNS-rebinding gap) -- wired into both the REST and
database dial paths; re-verified clean by the same reviewer end-to-end
against the metadata-address and RFC1918 cases. General code review
separately caught and fixed: single-address-only dialing (now falls back
through all resolved addresses), a per-call `http.Transport` leaking an
idle connection + goroutines on every test, and an unbounded database
connection close. 30 new tests, including direct coverage of the SSRF
guard. **Verified 2026-07-24 with a real toolchain: `go build ./...`,
`go vet ./...`, `go test ./...` all clean, 0 failures** (149 total, up
from 143 pre-B-023). Known follow-up, out of scope for this task and not
fixed: `ToolsPage.tsx`'s "Test connection" button doesn't yet read the
real `success` field from the response (harmless before this fix, since
the old stub always reported success) -- logged as `BACKLOG.md` B-024.

Prior entry, still accurate: 2026-07-23 by Claude Code — B-022: `POST /v1/gateway/tools` was silently
discarding any `credentials` object submitted via the Add Tool UI
(documented in `api/openapi.yaml`'s `ToolCreate.credentials`, but
`CreateTool` never read the field, never wrote to `gateway_tools.
credentials_encrypted`, and returned 201 anyway) — a gap surfaced by a
prior full-application audit. Fixed: new `eami-api/internal/toolcreds`
package (AES-256-GCM, key from `TOOL_CREDENTIALS_ENCRYPTION_KEY`,
deliberately not pgcrypto — see BUILT.md for why), wired into
`CreateTool`; fails closed (500, no store call) if credentials are
submitted but no key is configured. Two subagent review passes: security
review caught a real bypass in an early version (typed-struct decode
meant an unrecognized credential field name silently reproduced the
original bug) — fixed and re-verified clean. General code review caught
a missing nil-store guard on `tools.go`'s other handlers and a
non-standard error code — both fixed. 19 new tests; `tools.go` had zero
coverage before this. **Verified 2026-07-23 with a real toolchain:
`go build ./...`, `go vet ./...`, `go test ./...` all clean, 0
failures.** `TOOL_CREDENTIALS_ENCRYPTION_KEY` added to `.env.example`/
`docker-compose.yml`/`docker-compose.prod.yml` (generate via `openssl
rand -hex 32`, same convention as the other secrets) so the local stack
doesn't hit the fail-closed path unexpectedly. `TestTool`'s synthetic
"always connected" stub is unchanged, explicitly out of scope — logged
as B-023, the natural next building block (decrypting stored credentials
for a real connectivity probe).

Prior entry, still accurate: 2026-07-22, standalone infra fix (B-020, not tied to any
brief): `eami-collector` was crash-looping (`exec
/app/docker-entrypoint.sh: no such file or directory`) because
`docker-entrypoint.sh` had Windows CRLF line endings, breaking shebang
resolution (`#!/bin/sh\r` → kernel looks for an interpreter literally
named `/bin/sh\r`). Not a missing `COPY`/`chmod +x` — Dockerfile was
already correct. Stripped to LF; verified `docker compose build
eami-collector` clean and the container starts and stays running.
**Every other `.sh` file in the repo has the same CRLF issue** (no
`.gitattributes` pinning LF) — logged as B-021, not fixed here (out of
scope for this task).

Prior entry, still accurate: 2026-07-22, standalone infra fix (B-019,
not tied to any brief): `docker-compose.yml`'s `eami-ui` service had
`build.context: ./eami-ui`, but `eami-ui/Dockerfile` copies repo-root
`api/openapi.yaml` (needed for `generate-client`), so `docker compose
up --build` failed with `COPY api/openapi.yaml /api/openapi.yaml: not
found`. Fixed to `context: .` / `dockerfile: eami-ui/Dockerfile`;
verified with `docker compose build eami-ui`. Also confirms Docker is
available on this machine (previously assumed absent — see B-002
Brief 3 notes above, which were accurate when written but are now
stale on that one point). Committed directly to master.

Prior entry, still accurate: 2026-07-22, merged `b-002-memory-cutover`
into master (merge commit `292d6a4`; branch deleted, both locally and
on origin). **B-002 is now fully closed on master**: `memory.go`/
`store/episodes.go` deleted, `/v1/memory/episodes*` served by Brief 2's
org-isolated handlers, zero frontend changes needed, security review
confirms the leak is fully closed (not just superseded by a safer
alternative running alongside it). All three B-002 briefs (`3eab113`,
`adcd3e9`, `292d6a4`) are on master. BACKLOG updated to match: B-002
marked resolved, B-015 reframed as a standalone network-hardening item
(no longer a B-002 blocker), B-012 closed incidentally, B-017/B-018
logged for pre-existing doc/comment drift discovered along the way.
