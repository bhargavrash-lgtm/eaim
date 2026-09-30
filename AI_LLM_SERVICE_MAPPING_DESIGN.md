# AI/LLM Discovery & Service Mapping — Design Record

**Status:** design discussion, not yet a build brief. Grounded in a real 
scanner-capability audit and real, current Device42 documentation. 
Nothing here has been sent to Code as a build instruction.

---

## 0. Urgent findings from the scanner audit — must not get lost

These surfaced during Discovery admin design work and are real, current 
issues, separate from anything below:

1. **Live bug, real severity:** linking an endpoint to a governed agent 
   silently disables 4 of 10 scanners (`ai_processes`, `gpu`, 
   `python_envs`, `nodejs_ai`) via the default `agent_configs` row 
   allowing only 6 names. **B-271.**
2. **Confirmed live 2026-09-30 (Linux); macOS suspected, same 
   mechanism — the most urgent of the three:** packaged Linux agents 
   never read their installed config file (`/etc/eami/agent.yaml`) — 
   they run with no collector URL, printing reports to stdout only. 
   **B-273** (includes the live `.deb` evidence).
3. `network_activity` fully non-functional on Linux: **B-272** 
   (connection matching compares raw IPs to hostnames, so never 
   matches) together with the pre-existing **B-010** (stub DNS 
   fallback returns nothing) — to be fixed together. The 
   service-config-path question was not distinct from item 2; it is 
   B-273's root cause.
4. B-193 (SYSTEM-vs-interactive-user blind spot) and B-194 (models 
   over-collection) are both confirmed still open, and confirmed 
   *structurally linked*: the `C:\Users` default path that accidentally 
   works around B-193 for the `models` scanner is the exact path causing 
   B-194's over-collection. Fixing one without the other changes real 
   detection coverage — they need to be resolved together, not 
   separately.

---

## 1. Already-approved build sequence (recap, for context)

1. B-269 — Discovery presets
2. B-270 — Endpoint Detail read-only effective-config view (alongside C2)
3. Shadow-agent surfacing + "Onboard as Governed Agent" guided flow
4. Model characterization on the endpoint's own machine (Ollama extension)
5. B-267 — Discovery Probe (agentless, credentialed scanning)

Everything below is a **new, separate epic** that begins only once B-267 
exists — it does not change this sequence.

---

## 2. Agentless discovery settings — grounded in real Device42 practice

Applies to B-267's own configuration, once built:

- **IP scope ceiling:** /16 (65,534 addresses) per scan job — Device42's 
  own explicit, stated best practice, not an arbitrary limit.
- **Two-phase pattern:** a lightweight pre-scan (port + OS/service 
  fingerprint) runs first and determines which typed follow-up job runs 
  next (SSH for `*nix`, WinRM for Windows, vCenter/Proxmox for 
  hypervisors) — the admin does not pre-declare what's at each IP.
- **Credentials:** a dedicated, non-production account is required — 
  Device42's own explicit guidance, directly validating the 
  stricter-than-tool-credentials model already proposed for the probe 
  (locked to the probe process, API cannot decrypt, write-only, 
  rotatable, admin-only behind step-up auth once B-231 exists, fully 
  audited).
- **Global exclusion list** (IP/MAC), applied across all scan jobs.
- **Flexible scheduling** (hourly/daily/weekly/specific days) — not a 
  single global interval.

---

## 3. AI/LLM Service Mapping — the real new design

A four-tier model, adapted directly from Device42's real Application 
Dependency Mapping architecture.

**Tier 1 — Service Instance (already real).** One raw discovered fact on 
one machine: an AI process, an open model-server port, an MCP server 
entry, a governed agent's own real dispatch record.

**Tier 2 — Application Component (new, auto-grouped).** Multiple Tier-1 
facts on one machine that are clearly the same logical thing get 
auto-clustered without human input — e.g., a detected Ollama process + 
its open port + its model files become one "Local LLM Stack" component.

**Tier 3 — Service Connection (new, the core mechanism).** Not a single 
undirected edge. Two explicit roles per real connection:
- **Receiver** — the listening side (a Tier-2 component with an open port)
- **Initiator** — the connecting side (a governed agent's real dispatch 
  record if it's rheoARC-routed; a raw network observation otherwise)

A genuinely bidirectional relationship is simply two Connection records 
pointing opposite ways between the same two components — direction is 
structural, never inferred after the fact. Every Connection record 
carries port, protocol, first-observed timestamp, and last-observed 
timestamp — the real mechanism behind "understanding how things were set 
up," not a static yes/no edge.

**Tier 4 — Dependency Group (new, semi-automated).** Mirrors Device42's 
real "Application Group" workflow: an admin picks a real starting node, 
runs a rule that walks outward along real Tier-3 connections, the system 
**suggests** a candidate upstream/downstream chain, and the admin 
accepts, edits, or renames it. Never a blank-canvas manual draw.

**Renaming — a precise, resolved mechanism.** A customer-given display 
name is stored against a stable underlying match key (process name + 
install path + package identity), never against the raw discovered value 
alone. Re-discovery matches against the stable key first, so a rename 
persists across future scans instead of being silently overwritten — 
confirmed as Device42's own real, documented behavior.

**Honest, disclosed limitation, carried forward deliberately:** Device42 
itself admits that reducing large discovered data sets to clean 
source/destination dependency chains is genuinely hard, even for a 
mature product. This model is explicitly "suggest, then human curates" — 
never promised as fully automatic ground truth.

---

## 4. Agent vs. Agentless — the permanent capability boundary

- Agent-based `network_activity` can only ever show one endpoint's own 
  outbound view. It structurally cannot see the receiving side of a 
  connection unless that side is *also* independently discovered.
- Agentless (the probe) is the only path to genuine two-sided, 
  cross-machine topology, because it can query state on both ends.
- **Real, current blocker:** `network_activity` is confirmed broken 
  today — Windows matches only 7 hardcoded hostnames via unreliable 
  reverse-DNS; Linux is structurally non-functional. This needs real 
  fixing before agent-side data feeds Tier 3 meaningfully — extending a 
  broken mechanism as-is would just propagate the breakage into the new 
  model.

---

## 5. Explicitly future — not designed now

**"Hive AI mind"** — a chat interface embedded in the agent software 
itself, contributing to a collective, queryable intelligence across 
endpoints. Logged as a placeholder idea only. Presumes model hosting and 
the orchestration layer (Horizon 2) already exist. No design work done 
beyond naming it so it isn't lost.

---

## 6. Proposed build sequence for this new epic specifically

1. Fix `network_activity`'s confirmed brokenness — a real prerequisite, 
   likely its own small brief, not part of this epic itself.
2. Build B-267 (the probe), including Tier-1 discovery and the §2 
   settings model.
3. Tier 2 (auto-grouping) — smallest real step once Tier 1 data exists.
4. Tier 3 (Initiator/Receiver connections + timelines) — the core new 
   mechanism.
5. Tier 4 (suggest-and-curate dependency groups + persistent renaming).

This is a separate epic from the already-approved sequence in §1 — it 
begins only once B-267 exists, and does not reorder anything already 
committed to.
