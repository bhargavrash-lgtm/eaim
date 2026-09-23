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
- **B-139 (Agentless discovery)** — network-level detection with no endpoint install required; already has real technical framing from B-164's own investigation (mirror-port/proxy traffic inspection vs. DNS-query inspection), not starting from zero
- **B-135/B-136 (Observability & SIEM integration)** — real operational metrics plus export to Splunk/Sentinel-class tooling; a standard enterprise security-buyer requirement
- **B-137 (A secured, public third-party API)** — real ecosystem/integration requirement once customers want to build against rheoARC, not only use its UI
- **B-158/B-159 (Curated connector registry, hosted MCP wrapper)** — real Gateway completeness items, closing gaps found during the original TrueFoundry competitive research
- **B-154 (Onboarding templates)** — reduces real time-to-value for a new deployment
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
8. **B-147 (Customer-controlled training orchestration)** — genuinely later in character, not a blocker for 1-7. Real candidates identified: Axolotl, Ludwig (both Apache 2.0). Requires model hosting to exist first, develops in parallel with 4-7 otherwise. Includes real model-evaluation/benchmarking-over-time, already folded into this scope.
9. **Multi-agent coordination — deliberately last, deliberately separate.** Confirmed via real, current research: the least-solved part of even ServiceNow's and Salesforce's own platforms. Single-agent v1 first.

---

## Horizon 3 — Enterprise Scale-Readiness

The operational maturity that turns a proven product into something a large enterprise can actually sign a contract for:

- **B-132 (Horizontal scale readiness)** — real multi-instance/multi-region deployment architecture; everything built tonight has run as a single dev stack
- **B-133 (TimescaleDB retention/scale strategy)** — real data-lifecycle planning for the hypertables (audit_log, token_usage, paste_events) as real deployments accumulate genuine volume
- **B-153 (Real performance benchmarking)** — never formally measured; belongs here once there's a real, stable platform worth benchmarking rather than one still actively changing shape
- **Compliance certification readiness (SOC 2, eventually relevant for regulated industries)** — the substance already exists (real audit logging, real access control, real encryption); what's missing is the formal documentation and audit process
- **Groups-for-users (permission-bundle version) and custom configurable roles** — both correctly deferred pending real demonstrated need, not built speculatively

---

## The Honest One-Line Summary

**Horizon 0 is the proof the wedge is real. Horizon 1 closes the known gaps. Horizon 2 is what actually makes "one-stop-shop for enterprise AI" a true claim, not a slogan. Horizon 3 is what lets a real enterprise buy it.** Every horizon depends on the one before it being genuinely solid — which is exactly the discipline this entire session has run on.
