# Jev-Governed Agent Orchestration Roadmap

## Status and decision requested

**Status:** proposed Horizon 2 roadmap; no production implementation authorized by this document.

**Roadmap position:** Horizon 2, items 1–5: model hosting/serving (B-151), grounded retrieval and workspace Memory, guardrails (B-150), the Build/Orchestration layer (including B-155), and autonomy safeguards. It also informs the later unified provider surface (B-152), Chat Engine, and multi-agent coordination. It does not change the published sequence: reliable single-agent orchestration comes before multi-agent coordination.

**Primary decision:** adopt Jev as EAMI's System One decision provider for bounded orchestration decisions, subject to the deployment and customer-data gates in this document. Jev replaces the proposed local 1B “hive” for those decisions; a generative model remains necessary for planning and conversation, while deterministic EAMI policy code remains the enforcement authority.

**Decisions required before implementation begins:**

1. Is a hosted Jev call permitted for the customer and deployment profile, after data-processing, region, subprocessor, retention, and network-egress review?
2. Which fields may leave the on-prem deployment as Jev decision state? The initial assumption is metadata and redacted summaries only; never credentials, raw documents, full prompts, raw tool parameters, or episode bodies.
3. Is a self-hosted decision-provider fallback required for air-gapped customers? The recommended answer is yes; Jev is required for the connected profile, while an on-prem decision engine is required to preserve the product's air-gapped offering.
4. What confidence and disagreement thresholds cause automatic continuation, denial, or human escalation for each decision type?

## Product outcome

EAMI gains a governed agent loop that can turn a user request or operational event into a bounded, explainable workflow. Jev makes rapid typed decisions about what should happen next. Retrieval supplies approved workspace context. A generative model plans or communicates when needed. The existing gateway independently validates every actual tool call.

The customer-visible result is not “an AI that can do anything.” It is an agent that can choose from explicitly registered models, knowledge sources, workflows, tools, and human-review paths while EAMI records the evidence, policy result, cost, and outcome for every action.

## Scope and non-goals

In scope:

- Jev-backed intent routing, retrieval decisions, model selection recommendations, workflow branch decisions, completion checks, risk scoring, and escalation recommendations.
- Workspace-scoped RAG, with source provenance and policy-controlled access to knowledge.
- A governed single-agent loop that invokes existing registered tools only through the gateway.
- Decision and execution telemetry that extends the existing audit, episode, approval, and FinOps records.
- An adapter boundary that permits a future self-hosted decision provider without a second execution or audit path.

Out of scope for the first release:

- Treating Jev output as an authorization decision.
- Direct tool execution by Jev, RAG, the Chat Engine, or a client application.
- Arbitrary user-created tool definitions, endpoints, or credentials proposed by a model.
- A second identity model, separate audit trail, separate vector database, or separate top-level product silo.
- Multi-agent delegation, autonomous parallel workers, training/fine-tuning, or a mobile runtime as prerequisites for the first governed agent loop.

## Architectural principles

1. **The gateway remains the execution choke point.** Every tool call, including calls generated in a workflow, must traverse the existing identity, policy, approval, redaction, audit, episode, and FinOps path.
2. **Jev decides from a bounded menu.** A question supplies valid options such as `retrieve`, `answer_directly`, `start_workflow`, `escalate`, or `stop`. Jev never returns an arbitrary URL, tool name, policy action, or executable command.
3. **Deterministic policy outranks model output.** A Jev decision can propose a route. Existing structural policy decides whether an individual action is allowed, denied, or escalated. A future semantic rule is an additional policy signal, never a policy bypass.
4. **All model inputs are data-governed.** EAMI constructs a minimal decision state after identity, workspace, provider, data-handling, and redaction checks. Model-supplied text is untrusted and cannot modify the decision schema or tool allow-list.
5. **Every non-deterministic decision is attributable.** Persist decision type, schema version, allowed options, selected answer, probability/confidence, policy context version, source identifiers, provider/model version, latency, token/cost data where applicable, and final execution outcome.
6. **Failure is safe.** Timeout, provider error, invalid response, stale configuration, unavailable retrieval, low confidence, and decision disagreement lead to a configured `escalate` or `stop` result. None can imply approval.
7. **One spine, no parallel platform.** Jev, retrieval, model serving, and orchestration use existing Organization → Workspace → Agent → Tool → Policy → Cost/Audit mechanisms.

## Target architecture

```text
User request, schedule, alert, or previous tool result
                         |
                         v
              Orchestration coordinator
                         |
          creates minimal redacted decision state
                         |
                         v
             Jev decision provider (typed output)
              /           |                 \\
             v            v                  v
      direct answer    retrieve          approved workflow
             |            |                  |
             |            v                  v
             |     workspace RAG       step planner / executor
             |            |                  |
             +------------+------------------+
                         |
                         v
  Existing EAMI gateway: identity → policy → approval → redaction
                         |
                         v
    registered tool / model adapter → outcome → audit / episode / FinOps
                         |
                         +------ next Jev decision, if loop continues
```

The coordinator is a new gateway-owned component. It does not proxy tools itself. It asks Jev questions, calls the retrieval interface, invokes a registered generative-model adapter when needed, and starts an already-defined workflow. It hands any action to the existing dispatcher.

## Decision ownership and trust boundaries

| Concern | Authoritative component | Jev's role | Required behavior |
|---|---|---|---|
| Identity, organization, workspace, role | Existing JWT/workspace enforcement | None | Server-derived only; never model input authority |
| Tool availability and credentials | Registered connector/provider registry | Choose only from server-provided candidates | No model-created connectors or credential access |
| Policy enforcement | `eami-policy` evaluator and gateway dispatcher | Risk/evidence recommendation | Structural policy and approval result always win |
| Sensitive-data handling | Connector configuration and redaction rules | Detect or score possible sensitivity | Redact before external call; high uncertainty escalates |
| Retrieval eligibility | Workspace policy and RAG access control | Determine whether retrieval may help | Source query and returned documents are scope-filtered server-side |
| Workflow control flow | Workflow executor plus validated graph | Choose a permitted branch or stop | Executor enforces graph, budgets, retry limits, and per-step dispatch |
| Natural-language response and planning | Registered generative model adapter | Route/select/evaluate | Generated plan is schema-validated before use |
| Audit, cost, episodes | Existing writers | Supply decision evidence | Append records regardless of allow, deny, or escalation |

## Core contracts

### 1. Decision provider interface

Create a small gateway interface rather than binding orchestration code to Jev directly:

```go
type DecisionProvider interface {
    Provider() string
    Decide(ctx context.Context, request DecisionRequest) (DecisionResponse, error)
}
```

`DecisionRequest` contains a versioned decision schema, a question identifier, an enumerated answer set, an already-redacted state payload, and a correlation ID. `DecisionResponse` contains only a valid enumerated answer, probability/confidence data, the provider/model version, and request metadata. The provider implementation validates the response before the coordinator sees it.

The Jev provider is registered explicitly in the same construction path used for other pluggable providers. It must not use global registration or an `init()` side effect. The provider is selected from tenant/deployment configuration and policy-derived eligibility, not a caller-supplied endpoint.

### 2. Decision schema registry

Each decision type is an owned, versioned contract. A schema includes:

- stable ID and version;
- bounded outcomes and an explicit `insufficient_evidence` option;
- required and optional state fields;
- threshold policy per outcome;
- maximum input size and redaction profile;
- owner, audit retention category, and permitted deployment modes;
- test fixtures covering normal, adversarial, ambiguous, and unavailable-provider outcomes.

The initial registry should be code-owned. A policy/admin UI for schemas comes later, after versioning, review, and evaluation controls exist.

### 3. Orchestration state machine

The first state machine should be deliberately small:

`received → classified → [retrieving] → [planning] → executing → [waiting_for_approval] → completed | denied | blocked | cancelled`

State transitions must be persisted with a run ID. Only transition handlers can advance state. Each execution transition has an action budget, elapsed-time deadline, model-call budget, retrieval budget, and maximum loop count. A human approval pauses the run in a durable `waiting_for_approval` state; it must not occupy a process goroutine while waiting.

### 4. Workflow graph contract

Current workflows are ordered, linear steps that stop at the first non-allowed outcome. Conditional orchestration requires an explicit graph representation: step IDs, allowed outgoing edges, typed branch inputs, terminal states, retry semantics, compensation policy, and branch-level budgets. Jev may choose only an outgoing edge listed in the stored graph and only when the step's declared decision schema permits it.

### 5. Retrieval contract

Retrieval has two interfaces: document ingestion and query. Both are organization- and workspace-scoped. Each retrieved chunk includes source ID, source version, classification, owner, access scope, ingestion timestamp, and citation locator. The coordinator passes source identifiers and tightly bounded excerpts to a model only after a policy check.

## Data handling and deployment modes

### Connected governed mode

Jev is called over its managed API with a minimized decision state. The call is permitted only after a customer has approved the applicable data-processing terms and EAMI has recorded the deployment configuration, provider version, and egress policy. TypeSafe's customer terms permit processing submitted inputs to operate the service and retain certain data for telemetry, fraud/abuse, and legal obligations; this must be assessed customer by customer before production use. [TypeSafe Master Customer Agreement](https://typesafe.ai/legal/mca)

Initial Jev payload allow-list:

- workflow/run identifiers that cannot reveal customer content;
- selected candidate labels and policy-relevant connector metadata;
- normalized risk features;
- redacted prompt summary within a strict byte limit;
- retrieved-source metadata and pre-approved, redacted excerpts only where necessary;
- prior decision outcomes, budgets, and failure reasons.

Prohibited payload fields:

- secrets, tokens, API keys, encrypted credentials, or connection strings;
- raw tool parameters and tool results unless a connector's data policy expressly permits an approved minimized derivative;
- full documents, full episodes, customer memory profiles, employee identifiers, and unredacted prompts;
- content classified as local-only by policy.

### Air-gapped / local-only mode

The public product promise requires a mode with no outbound Jev dependency. This mode uses the same `DecisionProvider` contract with a self-hosted provider or deterministic decision rules. It has narrower decision capabilities and never silently fails open into the hosted provider. The product UI must show the active decision-provider mode and which decisions are unavailable offline.

### RAG storage

Use the existing on-prem Postgres/pgvector foundation first. Do not add ChromaDB or Pinecone for the first release. EAMI already has an `episodes` table and pgvector extension, but its current embeddings are deterministic SHA-256 placeholders and similarity search is not yet real; B-008 is therefore a prerequisite, not completed infrastructure. Knowledge-base retrieval adds a distinct ingestion, authorization, lifecycle, and evaluation workload and must be designed separately from episode recall.

## Delivery sequence

### Phase 0 — Architecture, compliance, and measured feasibility

**Goal:** prove that Jev can be used without breaking the on-prem and data-sovereignty promise.

Deliverables:

- Jev API capability and failure-mode spike against representative typed decisions.
- Legal/security review of TypeSafe data terms, DPA, regions, subprocessors, retention, egress controls, incident obligations, and procurement requirements.
- Threat model for prompt injection, model-state poisoning, cross-tenant access, policy bypass, data exfiltration, replay, and confidence manipulation.
- Decision-data classification matrix and the exact payload allow-list.
- Benchmark protocol comparing Jev, deterministic rules, and one constrained local fallback on latency, accuracy, calibration, availability, and cost.
- Explicit go/no-go decision for connected and air-gapped deployment profiles.

Exit gate: no customer content is sent to Jev until legal/security approve the data contract and a redaction/minimization test suite passes.

### Phase 1 — Decision foundation in shadow mode

**Goal:** introduce Jev decisions without changing behavior.

Deliverables:

- `DecisionProvider` interface, Jev adapter, provider registry, configuration validation, health checks, bounded timeout, circuit breaker, and feature flags.
- Versioned schema registry for intent routing, retrieval need, risk triage, and stop/escalate recommendation.
- Durable decision-event record linked to organization, workspace, agent, policy version, episode, and workflow run where present.
- Shadow evaluation: Jev records recommendations while the current deterministic behavior remains authoritative.
- Evaluation fixtures and replay harness from sanitized historical audit/episode data.

Exit gate: defined per-schema precision, calibration, latency, and outage behavior meet the approved threshold. Low confidence and provider failure are verified to produce no unintended execution.

### Phase 2 — Real grounding and workspace RAG

**Goal:** make retrieval reliable, scoped, and auditable.

Deliverables:

- Resolve ADR-009's embedding-provider decision and deliver B-008's real episode embeddings/search.
- Knowledge-source registration, ingestion, malware/file validation, parsing, chunking, embedding, provenance, classification, retention, deletion, and re-indexing.
- Workspace/org ACL filters applied before retrieval and again before context is supplied to a model.
- Retrieval policy conditions, source citations, quality evaluation corpus, and injection-resistant context packaging.
- Jev `retrieval_required` and `retrieval_sufficiency` schemas, initially shadowed then enabled under policy.

Exit gate: adversarial cross-workspace and malicious-document tests show no unauthorized retrieval or instruction override; retrieval results include source provenance and meet evaluation quality targets.

### Phase 3 — Guardrails and model routing

**Goal:** decide which registered model may receive content, while retaining EAMI enforcement.

Deliverables:

- Resolve whether B-150 guardrail classification and model-routing classification are one shared signal engine or distinct mechanisms with a common evidence format.
- Provider-routing policy: organization floor, workspace may only tighten, connector data-handling designation, model residency, cost budget, and fallback order.
- Jev schemas for data sensitivity, prompt-injection likelihood, destination eligibility, and escalation need.
- Pre-dispatch redaction and post-generation validation; model output is treated as untrusted input.
- Registered self-hosted model adapter and B-151 serving feasibility, with GPU/resource operations design before production dependency.

Exit gate: a request marked local-only cannot be sent to Jev or any external generative provider, even if a model router recommends it; policy tests prove this at the gateway boundary.

### Phase 4 — Governed workflow branching and single-agent loop

**Goal:** turn a validated plan into bounded execution.

Deliverables:

- Re-investigate B-155 against the existing jq extraction mechanism; define the persisted workflow graph and typed output requirements.
- Durable orchestration run and transition records; resumable approval waits; cancellation and kill-switch controls.
- Jev schemas for permitted branch selection, result sufficiency, retry/stop/escalate, and completion verification.
- Generative-plan schema validator that maps only to registered workflow templates, approved graph edges, and existing tools.
- Per-step dispatcher reuse: no orchestration path may bypass tool resolution, policy evaluation, approval, redaction, audit, episode recording, or FinOps.
- Action, spend, time, retry, and depth limits at organization, workspace, agent, workflow, and run levels.

Exit gate: a live end-to-end scenario proves that a denied or escalated generated step behaves identically to the same standalone MCP tool call, including audit linkage and approval outcome.

### Phase 5 — Operational hardening and controlled release

**Goal:** safely make the feature available to customers.

Deliverables:

- Admin kill-switch at global, organization, workspace, agent, workflow, and provider levels.
- FinOps attribution for Jev, embedding, retrieval, and generative-model calls; budget enforcement before costly calls.
- Dashboard/audit views for decision trace, evidence, confidence, source provenance, branch path, policy outcome, costs, and failures.
- SLOs, alerting, provider outage playbook, rate limits, queue/back-pressure strategy, and data retention operations.
- Tenant-isolation, red-team, load, chaos, upgrade, rollback, and disaster-recovery exercises.
- Limited beta with explicit connected-mode customer consent, then staged release behind entitlement and feature flags.

Exit gate: operational runbook, threat-model actions, customer controls, and acceptance test evidence are complete before general availability.

### Phase 6 — Later extensions

- B-152 unified multi-provider API after enough genuine provider adapters exist.
- The Chat Engine as the daily user surface once orchestration and controls are proven.
- B-147 customer-controlled training orchestration after serving and evaluation infrastructure exist.
- Multi-agent coordination only after single-agent evidence, budgets, state, and safety controls are mature. Every worker remains an EAMI agent with its own identity, scope, audit trail, and budget.

## Investigation workstreams

| Workstream | Questions to answer | Evidence required | Owner area |
|---|---|---|---|
| Jev product fit | Which questions, response semantics, model versions, quotas, rate limits, retries, and error responses are supported? | Contract tests against the official API; captured latency/error behavior | Gateway / platform |
| Data sovereignty | Can inputs transit to Jev for each deployment type? What are retention, region, DPA, and subprocessor commitments? | Approved legal/security assessment and customer configuration requirements | Security / legal / product |
| Decision quality | Are confidence values calibrated on EAMI-specific cases? Which thresholds are safe? | Frozen evaluation set, confusion matrices, calibration curves, false-negative review | ML / policy |
| Adversarial resilience | Can prompts, retrieved documents, tool output, or a tenant poison decision state? | Threat model and red-team suite with expected fail-safe results | Security |
| RAG | Which embedding model, ingestion pipeline, chunking, authorization filter, retrieval method, and evaluation set work for EAMI? | Quality, isolation, latency, and deletion/re-indexing evidence | Platform / data |
| Policy interaction | How do structural policy, semantic rules, Jev guardrails, and approval rules compose? | Precedence table and exhaustive integration tests | Policy / gateway |
| Workflows | What typed outputs and graph model are necessary for safe branching? | Migration plan, graph validation, replayable runs, approval-resume test | Gateway / API |
| Serving | What self-hosted model stack, hardware profile, monitoring, and capacity design are viable? | B-151 feasibility report and measured capacity test | Platform / operations |
| Operations | How do quotas, budgets, circuit breakers, queues, kill switches, and rollback work? | Load/chaos tests and runbooks | Operations / FinOps |
| UX | How do admins configure decisions and inspect traces without exposing sensitive content? | Design-system-compliant prototypes and usability/security review | UI / product |

## Acceptance gates

The following are release gates, not aspirational qualities:

1. **No policy bypass:** integration tests prove every tool action generated by an orchestrated run uses the normal gateway dispatcher and cannot bypass a deny, escalation, redaction rule, or license/usage limit.
2. **No cross-tenant or cross-workspace retrieval:** adversarial fixtures prove that retrieval and decision records cannot cross organization/workspace boundaries.
3. **No unapproved external data transfer:** payload-capture tests prove secrets, raw restricted content, and prohibited fields never reach Jev or an external model.
4. **Safe uncertainty:** invalid output, timeout, unavailable provider, low confidence, and disagreement take a documented stop/escalate path; no automatic allow result occurs.
5. **Reproducible explanation:** an operator can reconstruct an execution from immutable decision, policy, source, workflow, and tool outcome records without relying on model prose.
6. **Bounded autonomy:** run-level and tenant-level action, spend, time, loop, and retry caps are enforced under concurrent executions.
7. **Measured quality:** each enabled decision schema meets its approved quality/calibration threshold on a holdout evaluation set and after regression tests.
8. **Operational recovery:** provider outage, model version change, configuration rollback, and queued approval recovery are live-tested before beta.

## Risks and decisions to resolve

| Risk | Why it matters | Required mitigation / decision |
|---|---|---|
| Hosted Jev conflicts with air-gapped positioning | A mandatory cloud dependency would invalidate a central product promise | Connected and local profiles; no silent cloud fallback; customer-approved egress only |
| Confidence is mistaken for authorization | A calibrated probability can still be wrong | Deterministic policy/approval retains authority; confidence only controls orchestration transitions |
| Prompt injection through RAG/tool output | Retrieved text can attempt to alter routing or tools | Treat content as data; use fixed schemas, source trust metadata, context isolation, and output validation |
| Duplicate semantic mechanisms | Jev guardrails, B-007 semantic rules, and B-150 could drift | Define a shared evidence/precedence model before implementing more than one |
| Incomplete memory foundation | Placeholder embeddings make current recall unsuitable for RAG | Complete ADR-009 and B-008 before grounding claims |
| Workflow graph ambiguity | Current linear workflow storage cannot safely represent branches | Define and validate graph schema, migration, and branch contracts before UI work |
| Provider lock-in | Jev's hosted service, pricing, and API may change | Provider interface, schema ownership, replay corpus, controlled fallback, and version pinning |
| Cost amplification | Loops can multiply decision, retrieval, and model calls | Pre-call budgets, quotas, run caps, and full FinOps attribution |
| Audit sensitivity | Decision traces can inadvertently recreate raw user content | Minimize/redact state, separate access-controlled detail from immutable metadata, define retention |

## Backlog and roadmap mapping

| Existing item | Relationship to this roadmap | Required action |
|---|---|---|
| ADR-009 | Resolves local versus API model/embedding endpoint choice | Re-open as the foundation decision for embeddings and connected/local provider modes |
| B-007 | Real semantic policy evaluation | Re-scope the interaction with Jev; preserve timeout-to-escalate and policy precedence |
| B-008 | Real embeddings and pgvector similarity search | Prerequisite for credible episode recall; expand separately for knowledge ingestion |
| B-130 | Governed AI client, dynamic agentic loop, RAG, policy-governed model routing | Primary consumer surface; do not make a client responsible for enforcement |
| B-150 | Guardrails | Must share or explicitly separate classifier/evidence design from Jev decision schemas |
| B-151 | Open-weight model hosting/serving | First Horizon 2 prerequisite for local generation and air-gapped capability |
| B-155 | Conditional workflow branching | Re-investigate and subsume into the workflow-graph phase rather than implementing a disconnected branch engine |
| B-152 | Unified provider surface | Later; only after additional real adapters and routing controls exist |
| B-147 | Training orchestration | Later; uses the serving and evaluation foundation but is not needed for Jev orchestration |
| B-153 | Performance benchmarking | Supplies the methodology for claims about latency, throughput, and costs |
| B-160 | Persona workflows | The governing architecture for team-specific workflows once the single-agent loop is proven |

This roadmap proposes a new epic-level planning record rather than marking any existing implementation item done. Backlog grooming should assign a dedicated B-ID only after the Phase 0 decision package has been accepted and its first bounded brief is approved.

## Appendix: proposed decision catalogue

| Decision ID | Valid answers | Initial mode | Minimum evidence |
|---|---|---|---|
| `intent.route.v1` | direct-answer, retrieve, workflow-template, human, reject | Shadow | Request class, caller/workspace eligibility, feature entitlement |
| `retrieval.need.v1` | retrieve, no-retrieval, insufficient-evidence | Shadow → gated | Intent, knowledge-source availability, data classification |
| `retrieval.sufficiency.v1` | sufficient, insufficient, conflicting, escalate | Shadow → gated | Source IDs, relevance metadata, confidence distribution |
| `risk.triage.v1` | routine, elevated, high-risk, unknown | Shadow | Connector class, action type, redacted parameter features, policy facts |
| `model.route.v1` | local-provider-ID, external-provider-ID, human, stop | Shadow → gated | Provider eligibility produced by policy, data handling, budget, capability requirements |
| `workflow.branch.v1` | declared-edge IDs, retry, escalate, stop | Shadow → gated | Typed prior-step output, graph node, budgets, policy result |
| `workflow.completion.v1` | complete, continue, retry, escalate, failed | Shadow → gated | Declared success criteria, validated outcome fields, action budget |
| `approval.need.v1` | continue-to-policy, recommend-escalation, stop | Advisory only | Risk signals and evidence; real approval policy remains authoritative |

Every catalogue entry must define its own threshold policy. `unknown`, `insufficient-evidence`, `conflicting`, malformed response, and provider failure are first-class outcomes, never coerced into a positive action.
