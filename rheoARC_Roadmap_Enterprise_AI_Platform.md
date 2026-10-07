# rheoARC — Product Roadmap
## The Governed, On-Prem, Budget Alternative to ServiceNow/Salesforce's Agentic Platforms

---

## The Real Positioning — Stated Honestly

**Not:** compete with ServiceNow or Salesforce at their own scale. Both have decades of head start, thousands of engineers, and existing enterprise trust no architecture decision can shortcut.

**Actually:** be the credible, radically simpler, radically cheaper, **on-premises** alternative for the large, real segment of the market these two structurally cannot serve well — and grow from "governance" into "comprehensive enterprise AI platform" the same way a real budget challenger grows into its category over time, not by trying to match incumbent breadth on day one.

**Two structural wedges, both already real, both decided before this roadmap was written:**
1. **On-premises, air-gapped by architecture, not by feature flag.** ServiceNow and Salesforce are fundamentally cloud-native. For government, defense, and any organization with genuine data-sovereignty requirements, that's disqualifying regardless of how good their governance story is. rheoARC's VM-appliance, Docker-Compose delivery model was chosen specifically for this reason.
2. **Vendor-neutral governance, not platform-native governance.** ServiceNow's Control Tower governs agents built on ServiceNow. Salesforce's Einstein Trust Layer governs agents inside Salesforce. rheoARC governs AI usage *wherever it actually happens* — Claude, ChatGPT, a local model, an unsanctioned shadow-IT tool. That's a different, harder, genuinely more valuable position.

**The real evidence this wedge matters, not just a hopeful claim:** Gartner's own 2026 research says 40% of enterprises will demote or decommission AI agents by 2027 over governance gaps discovered only *after* production incidents. Every hour of adversarial testing this session has done — the TOCTOU protections, the tamper-evident audit chain (with a real bug found in its own verifier), fail-closed RBAC, redaction at the actual dispatch chokepoint — is directly closing the exact gap that statistic describes.

---

## Horizon 0 — Foundation: Built and Adversarially Proven

This is not aspirational. Every item below shipped, was reviewed by mandatory code-review and security passes, and was live-verified against real data.

- **Discovery** — endpoint agent (10 real scanners), browser paste-detection, the endpoint-to-agent identity link
- **The Governance Gateway** — deterministic policy engine, the one-spine architecture (Identity → Action → Tool → Policy → Cost/Audit), MCP-based tool dispatch
- **Sensitive-data redaction** — intercepting at the real dispatch chokepoint, TOCTOU-protected
- **Tamper-evident audit trail** — hash-chained, with independently-verified chain integrity
- **Cost accounting (FinOps)** — real, query-time-computed, caching-aware
- **Modular licensing & entitlement** — offline-verifiable, inverted trust model
- **Workspaces** — real schema, real RBAC (org-level + per-workspace), real policy-floor-plus-restriction inheritance, real provisioning (invite/reset/self-profile), a real UI, correct post-login routing per user type
- **A complete, consistent design system** — real tokens, real elevation, a fully retrofitted, uniform UI across every existing page

**This is the substance behind "budget alternative," not just a lower price.** It means the wedge is real: genuinely rigorous governance, genuinely simpler to deploy, at a fraction of the cost structure a 20-year-old platform carries.

---

## Horizon 1 — Closing the Real, Known Gaps

Scoped, logged, real backlog items — not yet built, no invented urgency, sequenced by honest dependency:

- **CMDB completion** — admin-configurable CI classification, real multi-hop relationships beyond the agent-centric graph, plus the already-logged asset-perspective relationship graph extension (reusing RelationshipGraph.tsx's own mechanism, centered on a discovered endpoint instead of an agent)
- **B-138 (SSO/IdP)** — a genuine enterprise procurement requirement; local provisioning (just closed) was correctly built first so this has a working foundation to federate on top of
- **B-139 (Agentless discovery, passive)** — network-level detection with no endpoint install required; already has real technical framing from B-164's own investigation (mirror-port/proxy traffic inspection vs. DNS-query inspection), not starting from zero. **Scope confirmed 2026-09-30: it stays passive network inspection exactly as originally scoped.** Active, credentialed scanning is a separate item, **B-267**, below.
- **Discovery administration: the three-layer model** (added 2026-09-30 at founder direction; plan in `DISCOVERY_ADMIN_INVESTIGATION.md`). The build order is founder-approved:
  1. **B-269: Discovery presets and enrollment (Layer 1, and the Layer 2 re-key).**
     - Named, versioned scanner presets that **endpoints** are assigned to (today, remote scanner config is keyed by a *governed agent* and reaches only manually linked endpoints).
     - Per-preset enrollment keys, which also end the single-org config resolution.
     - The signed installers stay unchanged; a deployment bundle is the installer plus per-channel install parameters (Intune/SCCM, Jamf, Ansible).
     - Its own "Discovery setup" destination, not a Settings tab.
  2. **B-270: a read-only effective-config view on Endpoint Detail (Layer 3)**, built alongside B-252 C2.
     - It shows the assigned preset, its version, the values in force, and the last config the agent applied.
     - The only write is an admin-only preset-assignment change; no per-endpoint field editing.
     - The Agent Detail "Configure" action retires.
  3. **Shadow-agent surfacing and "Onboard as Governed Agent"**: deep-discovery case 2a, below.
  4. **LLM characterisation on the endpoint's own machine** (sequenced by the founder 2026-09-30).
     - It extends the agent's existing localhost Ollama call.
     - It shares the same discovery pass and trust boundary as Tier 1, so it needs **no new credential or scan-range work**.
     - Network-hosted model servers come later, through B-267.
  5. **B-267, the Discovery Probe**: active, credentialed agentless scanning, below.
- **B-267: the Discovery Probe** (`eami-probe`; deliberately **not** "Collector", which is a passive relay). This is **active, credentialed** agentless scanning, **separate from B-139**.
  - **v1 coverage:**
    - SSH for Linux and macOS, with a read-only command allowlist;
    - WinRM over HTTPS for Windows;
    - vCenter/vSphere and Proxmox APIs for hypervisor **inventory**;
    - a TCP probe of known AI-serving ports.
  - **Scope:** it scans only admin-entered, allow-listed ranges, and every range change is audited.
  - **Credentials, held to a stricter standard than tool credentials:**
    - **locked to the probe process**: the API stores them but **cannot decrypt** them;
    - write-only, AAD-bound, versioned and rotatable;
    - admin-only behind **step-up (B-231)**;
    - fully audited.
- **Deep-discovery tiers: triggered, not universal** (added 2026-09-30).
  - **LLM characterisation of customer-hosted models:**
    - metadata and health APIs only (Ollama, OpenAI-compatible servers, TGI, Triton/NIM), no inference by default;
    - the endpoint agent covers localhost **first (build order item 4)**; B-267 covers network-hosted servers later (item 5);
    - a discovered but unregistered model server gets **its own section on Endpoint Detail**, and the same record feeds Model Details (Tool Detail) once it is registered as an `ai_provider` tool;
  - **Case 2a, shadow agents:**
    - observational depth only, never implied to be dispatch-level;
    - shipped on the real signals first: `ai_processes` (collected, never surfaced today), executable path, command line, and PID-joined direct connections to known providers, which is the "bypasses the gateway" signal;
    - a real "Onboard as Governed Agent" flow, complete only on that agent's **first real dispatch**;
    - install-time and start-time capture is **B-268**, deferred and not blocking.
  - **Case 2b, governed agents:** no new mechanism. **Lineage** is the deep view.
- **B-135/B-136 (Observability & SIEM integration)** — real operational metrics plus export to Splunk/Sentinel-class tooling; a standard enterprise security-buyer requirement
- **B-137 (A secured, public third-party API)** — real ecosystem/integration requirement once customers want to build against rheoARC, not only use its UI
- **B-158/B-159 (Curated connector registry, hosted MCP wrapper)** — real Gateway completeness items, closing gaps found during the original TrueFoundry competitive research
- **B-154 (Onboarding templates)** — reduces real time-to-value for a new deployment
- **Step-up authentication for sensitive actions (B-231)** — server-enforced re-authentication before rotating a tool credential or changing a credentialed tool's URL, deleting/reactivating an agent, creating an agent API key, deleting a policy, promoting a user to admin, and generating an admin password-reset link. Suspending agents and revoking keys are excluded: emergency containment must stay frictionless. Must account for SSO-only users who have no password.
- **Agent lineage: real, per-agent activity from the dispatch path** (added 2026-09-29 at founder direction). A **Lineage** tab on Agent Detail shows what an agent has *actually done*, built only from data the gateway already records:
  - a summary: tools ever touched, escalations and denials in the last 30 days, first and last seen, and Risk and Owner;
  - an activity table over each real tool, policy and workflow relationship: calls in a 24h, 7d or 30d window, last call, and a decision breakdown;
  - cost per agent and per AI-provider tool, from `token_usage` ("—" for tools that don't produce token usage, never a fabricated $0).

  **Why it matters:** a gateway in the real dispatch path can show real behaviour; a system that only discovers an agent from outside cannot. **Honest limits:**
  - No data classification: redaction records only a count, not which pattern matched.
  - No behavioural drift, which needs a real historical baseline.
  - No SIEM or other external export (see B-135/B-136).
  - No org-wide or Workspace view yet.

  At scale, the per-agent reads want an `audit_log (org_id, agent_id, timestamp)` index (**B-265**, queued).

  **Status: SHIPPED 2026-09-29.** The Lineage tab and `GET /v1/gateway/agents/{id}/lineage` are live-verified against real gateway dispatches (`AGENT_LINEAGE_VERIFICATION.md`). B-265 remains queued.
- **B-280: the AI ITAM epic** — Horizon 1, in progress; record and standing build order: `AI_ITAM_EPIC_MASTER_SEQUENCE.md`.
- **Agent identity (B-300)** — **proposed** placement, founder to confirm: Slice A1 shipped (B-301, B-302); Slices A2 and A3 in Horizon 1; Slices B and C in Horizon 2; record `AGENT_IDENTITY_DESIGN.md`.
- **Dynamic Asset Grouping (part of B-280; schema resolution is master-sequence item 6)** — Horizon 1, design record, not a build brief: `DYNAMIC_ASSET_GROUPING_EPIC.md`.
- **AI/LLM Service Mapping (part of B-280; master-sequence item 16)** — Horizon 1, design record, begins once B-267 exists: `AI_LLM_SERVICE_MAPPING_DESIGN.md`.
- **CI-reconciliation identity keys and CMDB export (part of B-280; master-sequence item 15)** — Horizon 1, not started; no B-ID or design record yet (no CMDB export record exists).
- **The small, already-disclosed items** — B-213 (doc correction), B-218 (gateway_tools/nodes workspace scoping), the Status field allow-list nit, custom-roles investigation (deferred, correctly, pending real demonstrated need), Groups-for-users bulk-tagging (small, real, not blocked on custom roles per tonight's own analysis)

---

## Horizon 2 — The Agentic Layer: The Real Differentiator, and What Workspaces Was Actually For

**This is also where B-160 (Enterprise AI OS) stops being an abstract vision and becomes real.** B-160's own thesis — Compliance, Finance, HR, Engineering, each running real workflows on the same governed, trustworthy data — was never a separate feature to build later. Workspaces, fully shipped in Horizon 0, **is** the delegated-administration mechanism that makes it possible. What Horizon 2 adds is the actual capability those teams build *with*, inside their own workspace: real agents, real orchestration, real automation — not just scoped visibility into policies and assets.

Sequenced strictly by real dependency, per tonight's own research synthesis:

1. **B-151 (Model hosting/serving)** — the literal prerequisite. An agent reasoning with a model that isn't callable is nothing.
2. **RAG / real grounding — the "Memory" feature**, already logged as workspace-scoped. Real candidates identified: RAGFlow (Apache 2.0), Infinity over Elasticsearch (avoiding AGPL/SSPL). Equally foundational to model hosting — both are "what does the agent have access to."
3. **B-150 (Guardrails — content safety/PII/prompt-injection)** — belongs here explicitly, not as a separate, disconnected epic. A real, still-open architectural question from earlier tonight: does Guardrails' own content classification share a mechanism with the Build layer's content-aware model-routing classifier (item 4 below)? Both inspect a prompt's content and act on it. Investigate before building either in isolation.
4. **The Build/Orchestration layer** — a visual, branching workflow authoring experience. **B-155 (conditional workflow branching)**, logged earlier as its own small epic, is very likely subsumed by this rather than needing separate treatment; confirm during implementation rather than building both. The critical, non-negotiable discipline, confirmed against how ServiceNow and Salesforce both actually work: every node compiles down to the identity/action/governance layers already proven in Horizon 0. No parallel execution path.
5. **Real autonomy safeguards** — per-time-unit spend/action limits, a real admin kill-switch, automatic escalation triggers.
6. **B-152 (Unified multi-provider API surface)** — was originally blocked on having enough real adapters to unify; once model hosting (1) adds a genuinely new provider type alongside the existing Claude/external adapters, this gate is naturally satisfied.
7. **The Chat Engine** — the real, daily web surface for this. Worth stating precisely: this is distinct from **B-130's original scope, a native desktop client** — related, not identical. The Chat Engine (web) can ship first; a native desktop app remains its own, later, separate decision.
   - **Chat app and discovery bundle (B-305)** — Horizon 2, extends the Chat Engine item: parked, design record only: `CHAT_APP_AND_DISCOVERY_BUNDLE_DESIGN.md`.
8. **B-147 (Customer-controlled training orchestration)** — genuinely later in character, not a blocker for 1-7. Real candidates identified: Axolotl, Ludwig (both Apache 2.0). Requires model hosting to exist first, develops in parallel with 4-7 otherwise. Includes real model-evaluation/benchmarking-over-time, already folded into this scope.
9. **Multi-agent coordination — deliberately last, deliberately separate.** Confirmed via real, current research: the least-solved part of even ServiceNow's and Salesforce's own platforms. Single-agent v1 first.

---

## Horizon 3 — Enterprise Scale-Readiness

The operational maturity that turns a proven product into something a large enterprise can actually sign a contract for:

- **B-132 (Horizontal scale readiness)** — real multi-instance/multi-region deployment architecture; everything built tonight has run as a single dev stack
- **B-133 (TimescaleDB retention/scale strategy)** — real data-lifecycle planning for the hypertables (audit_log, token_usage, paste_events) as real deployments accumulate genuine volume
- **B-153 (Real performance benchmarking)** — never formally measured; belongs here once there's a real, stable platform worth benchmarking rather than one still actively changing shape
- **Compliance certification readiness (SOC 2, eventually relevant for regulated industries)** — the substance already exists (real audit logging, real access control, real encryption); what's missing is the formal documentation and audit process
- **Hosted visibility layer decision (on-prem versus SaaS) and the GxP/CSV validation gate** — Horizon 3, undecided; no B-ID; the nearest record is ADR-020 in `DECISIONS.md` (appliance first, hybrid SaaS later); no GxP/CSV record exists yet.
- **Groups-for-users (permission-bundle version) and custom configurable roles** — both correctly deferred pending real demonstrated need, not built speculatively

---

## The Honest One-Line Summary

**Horizon 0 is the proof the wedge is real. Horizon 1 closes the known gaps. Horizon 2 is what actually makes "one-stop-shop for enterprise AI" a true claim, not a slogan. Horizon 3 is what lets a real enterprise buy it.** Every horizon depends on the one before it being genuinely solid — which is exactly the discipline this entire session has run on.
