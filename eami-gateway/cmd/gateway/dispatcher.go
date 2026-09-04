// dispatcher.go -- cmd/gateway
//
// Dispatcher and its DecisionHandler method, Dispatch, are what dispatch
// used to be: an inline closure inside run() in main.go, not independently
// constructable by any test (B-101), whose three policy branches (plus
// Allow's own proxy-failure case and Escalate's own Submit-failure case --
// five return points in total, pre-B-102) each repeated their own copy of
// the post-dispatch steps (episode recording, token-usage recording) --
// exactly how B-099 happened: one of those five return points simply never
// called recordTokenUsage, and nothing in the language or the structure
// caught it.
//
// Split into its own file (code-review finding on this brief) so this
// package's process-wiring code (run(), in main.go) and its dispatch
// business logic (here) stay separated -- one file per concern, matching
// this codebase's own stated Go convention (CLAUDE.md) and dispatcher_test.go's own
// separation from main_test.go.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/gateway/internal/aiprovider"
	"github.com/eami/gateway/internal/approval"
	"github.com/eami/gateway/internal/audit"
	"github.com/eami/gateway/internal/episode"
	"github.com/eami/gateway/internal/mcp"
	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/safego"
	"github.com/eami/gateway/internal/toolrouter"
	policy "github.com/eami/policy"
)

// DispatchOutcome is what one call to Dispatcher.Dispatch produced, built
// once by whichever policy branch ran and then handed unchanged to every
// hook in Dispatcher.hooks -- B-102, closing B-101 and the bug class
// B-099 found (a post-dispatch step manually repeated per branch, with no
// structural guarantee nothing gets missed).
//
// EpisodeSteps/EpisodeOutcome exist as fields here, not derived generically
// from Decision, because episode.Step.Decision's own vocabulary
// ("blocked"/"escalated"/"allowed") already diverges per branch from
// audit.Entry.Decision's ("denied"/"escalated"/"allowed") -- carrying the
// already-built step/outcome through the struct is what lets every hook
// stay generic without re-deriving (and risking silently changing) what
// gets written to episodes.
type DispatchOutcome struct {
	// Decision documents which policy branch produced this outcome
	// ("denied" | "escalated" | "allowed"). Not read by either hook below
	// today; kept for a future hook that might care, not for its own sake.
	Decision string
	// Result is the value Dispatch returns as its first result.
	Result json.RawMessage
	// Err is the value Dispatch returns as its second result.
	Err error
	// Dispatched is true only if a real downstream call genuinely
	// executed -- an immediate Allow that succeeded, or an Escalate whose
	// approval was granted AND whose resumed dispatch itself succeeded.
	// Deny, an Escalate that never even reached Hold() (a failed Submit),
	// a denied/expired/failed-resume Escalate, and a failed Allow proxy
	// call are all Dispatched=false: nothing happened against a real
	// downstream system, so nothing should be billed as usage.
	//
	// Deliberately NOT set by each branch's literal: Dispatch computes it
	// once, centrally, as `Err == nil`, right before the hook loop (see
	// Dispatch's tail) -- every branch already sets Err correctly, and
	// duplicating that same boolean per-branch would be exactly the "two
	// things that must independently be kept consistent" shape this
	// refactor exists to eliminate (a code-review finding on this brief
	// itself: the field was originally set manually in each branch,
	// redundant with Err in every one of them).
	Dispatched bool
	// EpisodeSteps/EpisodeOutcome are exactly the two positional
	// arguments (steps, outcome) each branch already builds today for its
	// episodeRecorder.Record call.
	EpisodeSteps   []episode.Step
	EpisodeOutcome string
	// AuditWriteErr (B-121) is the error returned by this branch's own
	// d.auditWriter.Write call, if any -- nil on a successful write. Every
	// branch below calls Write() at its OWN correct moment (Escalate
	// writes before Hold() blocks; the others write after their outcome is
	// known) -- that timing is deliberately NOT unified into the hook loop
	// (see Dispatch's own doc comment), but the resulting error's HANDLING
	// is: carrying it through here lets logAuditWriteFailureHook below
	// react identically regardless of which branch produced it, closing
	// the asymmetry B-121 found (Allow's proxy-error and Deny/Escalate all
	// silently discarded this error via `_ = ...`; only Allow-success
	// checked it) with one change instead of three independently-patched
	// call sites that could drift apart again later.
	AuditWriteErr error
	// AuditDecision (B-121) is the exact value that was set on
	// auditEntry.Decision for the write AuditWriteErr came from --
	// deliberately NOT the same as Decision above. Found by this fix's own
	// mandatory security-review pass: the Allow-proxy-failure branch sets
	// Decision:"allowed" (the policy branch label) but
	// auditEntry.Decision="denied" (what was actually attempted for
	// audit_log) -- logging Decision there would have told an operator
	// reconciling a lost row to look for a "denied" gap under "allowed" in
	// their own investigation, exactly backwards. AuditDecision is always
	// the value genuinely passed to d.auditWriter.Write, so
	// logAuditWriteFailureHook's log line is trustworthy by construction,
	// not by the three-out-of-four branches happening to agree with
	// Decision.
	AuditDecision string

	// ResolutionAuditEntry (B-124/125) is the fully-built second audit_log
	// entry to write once an Escalate branch's Hold() genuinely resolved
	// (approved, denied, or expired) -- nil for every non-Escalate
	// outcome, the Submit-failure exit, and a ctx-cancelled Hold() (none
	// of those have a confirmed resolution to record). Built here, in
	// Dispatch() itself, not inside the hook that writes it: the fields
	// it needs to clone correctly (already-masked Parameters, DataHandling,
	// PolicyID, WorkflowRunID/StepIndex) only exist as Dispatch()-local
	// state (resolvedProvider/resolvedTool, auditEntry) that a hook
	// (mcp.ActionContext + DispatchOutcome only) has no access to --
	// keeping construction here means there's exactly one place that
	// knows how to build a correct audit_log row for this call, not two.
	// A *audit.Entry pointer (not a value) specifically so "no resolution
	// to write" is unambiguous (nil), distinct from a genuinely
	// zero-valued entry.
	ResolutionAuditEntry *audit.Entry
}

// DispatchHook reacts to one completed Dispatcher.Dispatch call. Every
// hook in Dispatcher.hooks runs for every outcome, regardless of which
// policy branch produced it -- that convergence, not the hook type itself,
// is what B-102 adds: see Dispatcher.Dispatch's single exit point.
type DispatchHook func(ctx context.Context, ac mcp.ActionContext, o DispatchOutcome)

// Dispatcher holds dispatch's real collaborators (identical set to what
// the pre-B-102 closure captured from run()) plus an explicit, literal
// hook list built once at construction -- deliberately NOT a
// Publish/Subscribe registry, NOT channels, NOT global/init()-time
// registration. Mirrors aiprovider.Router's own explicit
// map[string]Adapter (internal/aiprovider/router.go), the codebase's
// existing convention for "a fixed set of things resolved at construction
// time, not discovered at runtime." The 2026-08-23 extensibility
// investigation found exactly one site in eami-gateway/eami-api with the
// multi-branch fan-out shape this solves (this one) -- do not generalize
// this into a repo-wide event-bus abstraction without a second,
// independently-confirmed site needing it.
type Dispatcher struct {
	toolRouter       *toolrouter.Router
	aiProviderRouter *aiprovider.Router
	// policyEvalSource is read fresh (policyEvalSource.Evaluator()) on
	// every Dispatch call, not cached -- see policy.EvaluatorSource's doc
	// comment (B-129: a Dispatcher that instead stored a plain
	// policy.Evaluator captured once at construction silently never
	// observed any policy change made after the process started).
	policyEvalSource policy.EvaluatorSource
	auditWriter      *audit.Writer
	episodeRecorder  *episode.Recorder
	approvalRouter   *approval.Router
	fwdProxy         *proxy.Proxy
	apiBaseURL       string
	apiServiceKey    string
	holdTimeout      time.Duration
	hooks            []DispatchHook
}

// NewDispatcher builds a Dispatcher with the two hooks that exist in
// production today (token-usage recording, episode recording -- both
// migrated here unchanged from the pre-B-102 closure) plus any extraHooks.
// extraHooks is a test seam only: run() never passes any, so production's
// hook list is always exactly these two, fixed at construction -- a test
// appends a counter/spy hook to prove new hooks apply to every branch
// (dispatcher_test.go) without making the mechanism runtime-discoverable
// in production.
func NewDispatcher(
	toolRouter *toolrouter.Router,
	aiProviderRouter *aiprovider.Router,
	policyEvalSource policy.EvaluatorSource,
	auditWriter *audit.Writer,
	episodeRecorder *episode.Recorder,
	approvalRouter *approval.Router,
	fwdProxy *proxy.Proxy,
	apiBaseURL, apiServiceKey string,
	holdTimeout time.Duration,
	extraHooks ...DispatchHook,
) *Dispatcher {
	d := &Dispatcher{
		toolRouter:       toolRouter,
		aiProviderRouter: aiProviderRouter,
		policyEvalSource: policyEvalSource,
		auditWriter:      auditWriter,
		episodeRecorder:  episodeRecorder,
		approvalRouter:   approvalRouter,
		fwdProxy:         fwdProxy,
		apiBaseURL:       apiBaseURL,
		apiServiceKey:    apiServiceKey,
		holdTimeout:      holdTimeout,
	}
	// logAuditWriteFailureHook and recordEscalationResolutionAuditHook are
	// both registered before recordTokenUsageHook/recordEpisodeHook
	// (code-review finding on B-121, extended here to the new hook for the
	// same reason): the hook loop below has no panic recovery, and both
	// audit hooks perform (or report on) writes into the tamper-evident
	// hash chain -- the highest-priority side effects here, ahead of
	// token-usage/episode bookkeeping. recordEscalationResolutionAuditHook
	// must be its OWN hook, not merged into logAuditWriteFailureHook (which
	// this brief's own MUST NOT MODIFY forbids touching): DispatchHook
	// passes DispatchOutcome BY VALUE to every hook independently, so one
	// hook's write result can't be observed by a later hook in the same
	// pass -- the write and its own failure logging have to live together.
	d.hooks = append([]DispatchHook{
		d.logAuditWriteFailureHook,
		d.recordEscalationResolutionAuditHook,
		d.recordTokenUsageHook,
		d.recordEpisodeHook,
	}, extraHooks...)
	return d
}

// recordEscalationResolutionAuditHook (B-124/125) writes a SECOND, real
// audit_log row once an escalation's Hold() genuinely resolved --
// approved, denied, or expired -- closing the gap where the only
// tamper-evident record of an escalation was the pre-Hold "escalated" row,
// which structurally cannot carry approval_id/approved_by (neither exists
// at that write's own moment -- it's written before Submit() even creates
// the approval_requests row). No-op for every outcome without a built
// ResolutionAuditEntry: non-Escalate branches, the Submit-failure exit,
// and a ctx-cancelled Hold() (o.Resolved is false in Router.HoldOutcome
// for that last case, so Dispatch's Escalate branch never builds one).
//
// Fits the hook mechanism where the ORIGINAL "escalated" write does not
// (see that write's own doc comment, established by B-121): by the time
// this hook runs, Hold() has already returned -- no further unbounded
// blocking happens before the hook loop executes, so there's no
// crash-durability argument for writing synchronously inline instead of
// here, unlike the original write, which must survive a crash during
// Hold()'s own potentially multi-minute wait.
// Wrapped in safego.Guard (code-review finding on this fix): this hook sits
// second in d.hooks, ahead of token-usage/episode recording, and the hook
// loop itself has no panic recovery -- an unrecovered panic inside
// d.auditWriter.Write would have skipped both of those AND propagated an
// error out of Dispatch for a call that may have already genuinely
// executed downstream. Uses context.WithoutCancel(ctx) for the same reason
// Hold()'s own expiry UPDATE uses context.Background() (router.go): both
// of Dispatch's real production callers already pass a WithoutCancel'd ctx
// (mcp/handler.go, workflow/http.go), so this is currently a no-op in
// practice, but a future third caller without that wrapping would
// otherwise make this write silently lossy on client disconnect -- cheap
// to make structurally safe now rather than depend on that invariant
// holding forever.
func (d *Dispatcher) recordEscalationResolutionAuditHook(ctx context.Context, ac mcp.ActionContext, o DispatchOutcome) {
	if o.ResolutionAuditEntry == nil {
		return
	}
	safego.Guard("escalation-resolution-audit-write", func() {
		if writeErr := d.auditWriter.Write(context.WithoutCancel(ctx), *o.ResolutionAuditEntry); writeErr != nil {
			slog.Error("dispatch: escalation resolution audit_log write failed",
				"org_id", ac.OrgID,
				"agent", ac.AgentName,
				"agent_uuid", ac.AgentUUID,
				"session_id", ac.SessionID,
				"tool", ac.Tool,
				"action", ac.Action,
				"approval_id", o.ResolutionAuditEntry.ApprovalID,
				"decision", o.ResolutionAuditEntry.Decision,
				"workflow_run_id", ac.WorkflowRunID,
				"err", writeErr,
			)
		}
	})
}

// logAuditWriteFailureHook (B-121) logs a failed audit_log write at Error
// level, uniformly for every decision type -- closing the asymmetry where
// only the Allow-success branch checked d.auditWriter.Write's returned
// error (via an inline slog.Error) while Deny/Escalate/Allow-proxy-error
// all silently discarded it (`_ = ...`). No-op on a successful write
// (the overwhelmingly common case).
//
// Logging, not a stronger alert/metric, is the deliberate choice here
// (confirmed with the user before building): eami-gateway has no existing
// metrics or alerting infrastructure to escalate into today (no
// Prometheus, no expvar -- only an opt-in pprof endpoint), and
// eami-api's internal/alerting engine is a separate Go module evaluating
// business-metric alert rules, not reachable from here and not a natural
// fit for an in-process DB-write failure signal. Building new alerting
// infrastructure for this one gap would be well beyond this fix's scope;
// if real paging is wanted later, that's a separate, larger decision.
//
// Log fields (code-review finding on this fix): the lost audit_log row's
// own id is generated inside audit.Writer.Write and never returned, so
// these fields are the ONLY correlation key an operator investigating a
// gap has. workflow_run_id/step_index/session_id/agent_uuid were added
// after the first draft only logged org_id/agent-name/tool/action/
// decision -- insufficient to correlate against workflow_run_steps (which
// has no FK to/from audit_log at all, per ActionContext's own doc
// comment) or the approval subsystem (keyed on session, not agent name).
func (d *Dispatcher) logAuditWriteFailureHook(_ context.Context, ac mcp.ActionContext, o DispatchOutcome) {
	if o.AuditWriteErr == nil {
		return
	}
	var stepIndex any
	if ac.StepIndex != nil {
		stepIndex = *ac.StepIndex
	}
	slog.Error("dispatch: audit_log write failed",
		"org_id", ac.OrgID,
		"agent", ac.AgentName,
		"agent_uuid", ac.AgentUUID,
		"session_id", ac.SessionID,
		"tool", ac.Tool,
		"action", ac.Action,
		// AuditDecision, not Decision -- see AuditDecision's own doc
		// comment (security-review finding on this fix): they diverge for
		// the Allow-proxy-failure branch (Decision="allowed", the policy
		// branch label, but the audit_log write that failed was
		// actually "denied").
		"decision", o.AuditDecision,
		"workflow_run_id", ac.WorkflowRunID,
		"step_index", stepIndex,
		"err", o.AuditWriteErr,
	)
}

// recordTokenUsageHook is the pre-B-102 recordTokenUsage call, migrated
// unchanged: fires only for a genuinely dispatched outcome (o.Dispatched),
// exactly the "immediate-Allow and escalate-then-approved paths, never a
// call that didn't genuinely execute" contract B-099 established.
func (d *Dispatcher) recordTokenUsageHook(_ context.Context, ac mcp.ActionContext, o DispatchOutcome) {
	if !o.Dispatched {
		return
	}
	recordTokenUsage(d.apiBaseURL, d.apiServiceKey, o.Result, ac)
}

// recordEpisodeHook is the pre-B-102 `go episodeRecorder.Record(...)`
// calls, migrated unchanged: still uses context.Background() (episodes
// outlive the request that produced them, same as before B-102), and
// still invoked via `go` at this call site -- episode.Recorder.Record's
// own doc comment requires the caller to provide the goroutine, since
// Record doesn't spawn its own.
//
// A nil/empty o.EpisodeSteps means the branch that produced this outcome
// never wrote an episode before B-102 either (the Escalate branch's
// Submit-failure case, which returned immediately, before any episode
// was ever recorded) -- skipping here preserves that exactly, rather than
// this refactor silently starting to write episodes for a case that
// never wrote one.
func (d *Dispatcher) recordEpisodeHook(_ context.Context, ac mcp.ActionContext, o DispatchOutcome) {
	if len(o.EpisodeSteps) == 0 {
		return
	}
	go d.episodeRecorder.Record(context.Background(), ac.OrgID, ac.AgentUUID, ac.AgentName, o.EpisodeSteps, o.EpisodeOutcome)
}

// newEpisodeStep builds the one episode.Step every branch needs, varying
// only in decision/result -- extracted so the four (five, counting the
// Escalate Submit-failure case, which passes no step at all) call sites
// can't independently typo or drop a field like Timestamp.
func newEpisodeStep(ac mcp.ActionContext, decision string, result json.RawMessage) episode.Step {
	return episode.Step{
		ToolName:  ac.Tool,
		Action:    ac.Action,
		Params:    ac.Parameters,
		Result:    result,
		Decision:  decision,
		Timestamp: ac.ReceivedAt,
	}
}

// Dispatch is dispatch's pre-B-102 closure body, unchanged in every branch's
// actual logic (resolution order, policy evaluation, audit-write timing,
// TOCTOU pinning, Submit/Hold call order and arguments) -- restructured so
// every return point (Deny, Escalate-Submit-failure, Escalate-resumed,
// Allow-proxy-failure, Allow-success) stops returning independently and
// instead converges on one shared exit that runs every hook in d.hooks.
// That convergence is the actual fix for B-099's bug class: a hook can no
// longer be silently absent from one branch, because there is only one
// `return` statement in this entire method.
//
// Audit writes deliberately stay exactly where and when they are today --
// Escalate writes "escalated" before Hold() blocks, Allow writes
// allowed/denied after the proxy call resolves -- and are NOT moved into
// the hook list: their timing genuinely differs per branch (predates
// B-102), unlike token-usage/episode recording which always ran, for every
// branch, only after the branch's own outcome was fully known.
func (d *Dispatcher) Dispatch(reqCtx context.Context, ac mcp.ActionContext) (json.RawMessage, error) {
	start := time.Now()

	// Resolve ac.Tool against gateway_tools, org-scoped, before policy
	// evaluation (B-044) -- so a rule can target the resolved
	// gateway_tools.id via ToolServerID, and so the ActionAllow branch
	// below already knows whether to dynamically dispatch.
	resolvedTool := resolveDynamicTool(reqCtx, d.toolRouter, ac.OrgID, ac.Tool)
	// AI Provider Connector: identical resolution, a separate lookup
	// against gateway_tools' type='ai_provider' rows (Thread A Model 1).
	// A tool name resolves to at most one of resolvedTool/resolvedProvider
	// -- gateway_tools' UNIQUE(org_id, name) means at most one row can
	// ever match ac.Tool for a given org, regardless of type. Only
	// queried when the first lookup didn't already find a match (code
	// review finding, this task): the common case -- an already-
	// registered rest_api tool, the vast majority of real traffic --
	// pays exactly one DB round trip on the hot dispatch path, exactly
	// as it did before this brief.
	var resolvedProvider *aiprovider.ToolRow
	if resolvedTool == nil {
		resolvedProvider = resolveAIProviderTool(reqCtx, d.aiProviderRouter, ac.OrgID, ac.Tool)
	}

	pc := ac.ToPolicyContext()
	switch {
	case resolvedTool != nil:
		pc.ToolServerID = resolvedTool.ID
	case resolvedProvider != nil:
		pc.ToolServerID = resolvedProvider.ID
	}
	decision, evalErr := d.policyEvalSource.Evaluator().Evaluate(reqCtx, pc)
	if evalErr != nil {
		// Semantic evaluation errors are non-fatal; log and default to allow.
		slog.Warn("policy eval error — defaulting to allow", "err", evalErr)
		decision.Action = policy.ActionAllow
	}
	latencyMS := time.Since(start).Milliseconds()

	// PolicyID is *string in Decision; dereference once for the audit entry.
	policyID := ""
	if decision.PolicyID != nil {
		policyID = *decision.PolicyID
	}

	orgID, _ := uuid.Parse(ac.OrgID)
	agentID, _ := uuid.Parse(ac.AgentUUID)
	// WorkflowRunID (B-093): parses to uuid.Nil (audit.Entry's own "not a
	// workflow call" sentinel) for a standalone dispatch, where
	// ac.WorkflowRunID is always "" -- workflow/executor.go's runStep is
	// the only caller that ever sets it.
	workflowRunID, _ := uuid.Parse(ac.WorkflowRunID)
	auditEntry := audit.Entry{
		OrgID:         orgID,
		AgentID:       agentID,
		AgentName:     ac.AgentName,
		ToolName:      ac.Tool,
		Action:        ac.Action,
		Parameters:    ac.Parameters,
		LatencyMS:     latencyMS,
		PolicyID:      policyID,
		Timestamp:     ac.ReceivedAt,
		WorkflowRunID: workflowRunID,
		StepIndex:     ac.StepIndex,
	}
	// AI Provider Connector: per-connector audit logging mode
	// (schema/migrations-v2/000004). Applied once, here, so it covers
	// every branch below uniformly (denied/escalated/allowed) --
	// scoped strictly to the audit_log write; approval_requests
	// (B-039, frozen) and episodes keep showing full parameters,
	// unchanged, since a human reviewer needs full visibility to make
	// a real approve/deny decision (Thread A investigation, Part 0 §6
	// and §7). Default for a newly created connector is
	// "structural_metadata_only" (DB DEFAULT, schema/migrations-v2/
	// 000004) -- a new ai_provider connector never logs raw prompt
	// content into audit_log until an admin explicitly opts into "full".
	if resolvedProvider != nil && resolvedProvider.AuditMode != "full" {
		auditEntry.Parameters = nil
	}
	// Data-handling visibility (B-078): snapshot the connector's
	// data_handling_designation into this call's audit_log entry, same
	// call site and same "applied once, covers every branch below
	// uniformly" reasoning as AuditMode immediately above. This is
	// what makes AC3 real: a later change to the connector's own
	// designation only affects future dispatches' auditEntry
	// construction (a fresh resolvedProvider read), never this
	// already-built value for the current call.
	if resolvedProvider != nil {
		auditEntry.DataHandling = resolvedProvider.DataHandling
	}

	var outcome DispatchOutcome

	switch decision.Action {
	case policy.ActionDeny:
		auditEntry.Decision = "denied"
		auditWriteErr := d.auditWriter.Write(reqCtx, auditEntry)
		outcome = DispatchOutcome{
			Decision: "denied",
			// Return a typed error so the MCP handler builds a structured -32600 response.
			Err: &mcp.PolicyDeniedError{
				Reason:   decision.Reason,
				PolicyID: policyID,
			},
			EpisodeSteps:   []episode.Step{newEpisodeStep(ac, "blocked", nil)},
			EpisodeOutcome: "blocked",
			AuditWriteErr:  auditWriteErr,
			AuditDecision:  auditEntry.Decision,
		}

	case policy.ActionEscalate:
		// Write "escalated" audit entry before blocking on the approval waiter.
		auditEntry.Decision = "escalated"
		// auditWriteErr (B-121) is carried into BOTH of this branch's exit
		// points below (the Submit-failure early exit and the
		// resumed-after-Hold exit) -- this single write happens before
		// either is reached, so both outcomes must report the same result.
		auditWriteErr := d.auditWriter.Write(reqCtx, auditEntry)

		approvalReq := approval.Request{
			OrgID:      ac.OrgID,
			AgentID:    ac.AgentUUID,
			AgentName:  ac.AgentName,
			Tool:       ac.Tool,
			Action:     ac.Action,
			Parameters: ac.Parameters,
			SessionID:  ac.SessionID,
		}
		// Pin the resolved connector's identity + config fingerprint at
		// the exact moment it was resolved for policy evaluation -- what
		// the human approver's review is actually based on. Submit()
		// persists both; dispatchApproved re-verifies neither changed
		// before resuming, closing a real TOCTOU gap found live during
		// this brief's own verification (see approval.Request's doc
		// comment): without this, a lower-privileged admin/operator role
		// could edit the connector while the escalation was pending and
		// silently redirect the approved call to a different destination
		// than the approver reviewed.
		switch {
		case resolvedProvider != nil:
			approvalReq.ResolvedToolID = resolvedProvider.ID
			approvalReq.ResolvedConfigHash = approval.ComputeConfigHash("ai_provider", resolvedProvider.Provider, resolvedProvider.CredentialsEncrypted, nil, resolvedProvider.RedactionRules)
		case resolvedTool != nil:
			baseURL := ""
			if resolvedTool.BaseURL != nil {
				baseURL = *resolvedTool.BaseURL
			}
			// action_paths is security-relevant, not cosmetic: it
			// determines which sub-path/method a given action actually
			// dispatches to (toolrouter.Forward), so it must be part of
			// the pinned fingerprint too -- found live by this brief's
			// own mandatory security review (see ComputeConfigHash's
			// doc comment for the exact gap this closes).
			var actionPathsJSON []byte
			if len(resolvedTool.ActionPaths) > 0 {
				actionPathsJSON, _ = json.Marshal(resolvedTool.ActionPaths)
			}
			approvalReq.ResolvedToolID = resolvedTool.ID
			approvalReq.ResolvedConfigHash = approval.ComputeConfigHash("rest_api", baseURL, resolvedTool.CredentialsEncrypted, actionPathsJSON, nil)
		}
		approvalID, submitErr := d.approvalRouter.Submit(reqCtx, approvalReq)
		if submitErr != nil {
			// Converges through the same single exit as every other
			// branch (a code-review finding on this brief itself: this
			// case originally `return`ed directly here, independently of
			// every other branch, undermining the very guarantee B-102
			// exists to provide). No EpisodeSteps -- pre-B-102, this path
			// never wrote an episode either (it returned before Hold()
			// was ever reached), and recordEpisodeHook's empty-steps
			// guard preserves that exactly.
			outcome = DispatchOutcome{
				Decision:      "escalated",
				Err:           fmt.Errorf("approval submit: %w", submitErr),
				AuditWriteErr: auditWriteErr,
				AuditDecision: auditEntry.Decision,
			}
			break
		}
		slog.Info("dispatch: holding for approval decision",
			"approval_id", approvalID,
			"agent", ac.AgentName,
			"hold_timeout", d.holdTimeout,
		)
		holdStart := time.Now()
		holdOutcome := d.approvalRouter.Hold(reqCtx, approvalID, approvalReq)

		episodeOutcome := "success"
		if holdOutcome.Err != nil {
			episodeOutcome = "failed"
		}

		// resolutionEntry (B-124/125): built here, not inside the hook that
		// writes it, so it can clone auditEntry's already-correctly-masked
		// Parameters/DataHandling/PolicyID/WorkflowRunID/StepIndex --
		// dispatchApproved's own TOCTOU re-verification (approval.Request's
		// doc comment) already proves the resolved connector's identity and
		// config are unchanged since escalation time, so reusing those
		// cloned values is provably still accurate, not stale.
		var resolutionEntry *audit.Entry
		if holdOutcome.Resolved {
			re := auditEntry
			// Decision follows the SAME vocabulary precedent B-121's own
			// AuditDecision fix established on the Allow-proxy-failure
			// branch: "allowed" only when the call genuinely, successfully
			// executed; every other case (a real denial, an expiry, or an
			// approval whose actual dispatch technically failed -- TOCTOU
			// rejection, connector deleted, a real downstream error) is
			// "denied" in this schema's existing 3-value vocabulary, not a
			// new invented category.
			re.Decision = "denied"
			if holdOutcome.Status == "approved" && holdOutcome.Err == nil {
				re.Decision = "allowed"
			}
			re.ApprovalID = approvalID
			// RedactedCount (B-156/B-167): overwrite the clone's inherited
			// value (always !Valid on the original pre-Hold "escalated"
			// row -- nothing was dispatched yet at that write) with the
			// count from this resumed dispatch. holdOutcome.RedactedCount
			// is nil unless dispatchApproved's ai_provider branch actually
			// reached a real Router.Dispatch call -- a denial, expiry, or
			// any fail-closed resume rejection (config-changed, connector-
			// deleted) leaves it nil, correctly recording "not applicable"
			// (SQL NULL) rather than fabricating a 0 that would falsely
			// claim redaction ran and matched nothing. Found by this
			// brief's own mandatory code-review pass: an earlier draft
			// used a bare int here and set Valid:true unconditionally
			// whenever resolvedProvider != nil, which produced exactly
			// that false claim for every one of those fail-closed exits.
			if holdOutcome.RedactedCount != nil {
				re.RedactedCount = pgtype.Int4{Int32: int32(*holdOutcome.RedactedCount), Valid: true}
			}
			// ApprovedBy is deliberately NOT copied unconditionally --
			// found by BOTH of this fix's own mandatory review passes,
			// independently: an approval whose actual dispatch technically
			// failed AFTER a genuine human approval (dispatchApproved's
			// TOCTOU rejection -- connector deleted/config changed -- or a
			// real downstream error) lands on Decision="denied" above, per
			// the same enforcement-outcome vocabulary B-121 established.
			// Pairing that "denied" with the REAL approver's identity
			// inside the tamper-evident chain would read as "this person
			// denied it," which is false -- they approved it; the
			// downstream call failed for unrelated technical reasons.
			// ApprovedBy is populated only when it can't be misread this
			// way: a genuine human denial (Status != "approved", where it
			// truthfully names who denied it) or a genuine success
			// (Decision == "allowed", where it truthfully names who
			// approved it). The approver's real identity for the
			// technical-failure case is still recoverable from
			// approval_requests.approved_by (mutable, not tamper-evident,
			// for deeper investigation) -- this row just doesn't assert a
			// misleading attribution inside the hash chain itself.
			if re.Decision == "allowed" || holdOutcome.Status != "approved" {
				re.ApprovedBy = holdOutcome.ApprovedBy
			}
			// Timestamp/LatencyMS deliberately NOT cloned from auditEntry:
			// this row describes an event that happened later (resolution),
			// not at the original escalation instant -- reusing the old
			// Timestamp would assert a false fact into the hash chain
			// (Write's hash formula includes Timestamp). LatencyMS here
			// measures the real approval wait, a genuinely different and
			// more useful metric than the original row's policy-eval
			// latency.
			re.Timestamp = time.Now()
			re.LatencyMS = time.Since(holdStart).Milliseconds()
			// re.ID is already uuid.Nil (auditEntry never set it either --
			// the original write's own local copy was never mutated by
			// Writer.Write, which takes Entry by value), so Write() assigns
			// this row a genuinely fresh id, distinct from the original.
			resolutionEntry = &re
		}

		outcome = DispatchOutcome{
			Decision:             "escalated",
			Result:               holdOutcome.Result,
			Err:                  holdOutcome.Err,
			EpisodeSteps:         []episode.Step{newEpisodeStep(ac, "escalated", holdOutcome.Result)},
			EpisodeOutcome:       episodeOutcome,
			AuditWriteErr:        auditWriteErr,
			AuditDecision:        auditEntry.Decision,
			ResolutionAuditEntry: resolutionEntry,
		}

	default: // policy.ActionAllow
		toolReq := proxy.ToolRequest{
			ToolName:  ac.Tool,
			Action:    ac.Action,
			Params:    ac.Parameters,
			SessionID: ac.SessionID,
		}
		var tr proxy.ToolResponse
		var proxyErr error
		switch {
		case resolvedProvider != nil:
			// AI Provider Connector: dispatch to the resolved ai_provider
			// connector's real provider adapter (Claude first). Converted
			// into proxy.ToolResponse's shape so every line below this
			// switch (token-usage extraction, episode recording, the
			// final return) handles an ai_provider call exactly like any
			// other tool call, unchanged.
			var presp aiprovider.Response
			var redactedCount int
			presp, redactedCount, proxyErr = d.aiProviderRouter.Dispatch(reqCtx, resolvedProvider, ac.Action, ac.Parameters)
			tr = proxy.ToolResponse{Status: presp.StatusCode, Body: presp.Body}
			// RedactedCount (B-156/B-167): set regardless of proxyErr --
			// redaction runs before the real dispatch attempt inside
			// Router.Dispatch, so it has a real, meaningful value (usually
			// 0) even on a downstream failure. 0 for every non-ai_provider
			// dispatch (resolvedTool/fwdProxy branches never touch this
			// field, matching AuditMode/DataHandling's identical "only
			// meaningful for ai_provider" convention above).
			auditEntry.RedactedCount = pgtype.Int4{Int32: int32(redactedCount), Valid: true}
		case resolvedTool != nil:
			// B-044: dynamically dispatch to this org's registered
			// rest_api tool's real base_url/credentials, instead of the
			// static fwdProxy every other call below still uses
			// unchanged.
			tr, proxyErr = d.toolRouter.Forward(reqCtx, resolvedTool, toolReq)
		default:
			tr, proxyErr = d.fwdProxy.Forward(reqCtx, toolReq)
		}
		if proxyErr != nil {
			auditEntry.Decision = "denied"
			auditWriteErr := d.auditWriter.Write(reqCtx, auditEntry)
			outcome = DispatchOutcome{
				Decision:       "allowed",
				Err:            fmt.Errorf("proxy error: %w", proxyErr),
				EpisodeSteps:   []episode.Step{newEpisodeStep(ac, "allowed", nil)},
				EpisodeOutcome: "failed",
				AuditWriteErr:  auditWriteErr,
				AuditDecision:  auditEntry.Decision,
			}
		} else {
			auditEntry.Decision = "allowed"
			// B-121: the failure log this used to do inline here now
			// happens uniformly for every branch via logAuditWriteFailureHook
			// (see AuditWriteErr on DispatchOutcome) -- same observable
			// signal on failure, silent on success, just no longer
			// duplicated as this branch's own special case.
			auditWriteErr := d.auditWriter.Write(reqCtx, auditEntry)
			outcome = DispatchOutcome{
				Decision:       "allowed",
				Result:         tr.Body,
				EpisodeSteps:   []episode.Step{newEpisodeStep(ac, "allowed", tr.Body)},
				EpisodeOutcome: "success",
				AuditWriteErr:  auditWriteErr,
				AuditDecision:  auditEntry.Decision,
			}
		}
	}

	// Dispatched is computed once, here, from Err -- not set independently
	// by each branch above -- specifically so a branch can never set Err
	// without correctly implying Dispatched (see DispatchOutcome's doc
	// comment): true for every branch above iff a real downstream call
	// genuinely executed (Allow-success, Escalate-resumed-successfully).
	outcome.Dispatched = outcome.Err == nil

	for _, h := range d.hooks {
		h(reqCtx, ac, outcome)
	}
	return outcome.Result, outcome.Err
}
