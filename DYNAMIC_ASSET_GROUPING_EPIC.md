# Dynamic Asset Grouping — Epic Design Record

**Status:** design discussion, not yet a build brief. Grounded in real, 
current practice across Lansweeper, JumpCloud, IBM MaaS360, Microsoft 
Intune/Entra ID, LibreNMS, and InvGate — independently convergent on the 
same core pattern, which is a strong signal this is the right 
architecture, not one vendor's opinion.

**Program:** part of the AI ITAM epic, **B-280**. Its schema resolution
is item 6 of `AI_ITAM_EPIC_MASTER_SEQUENCE.md`, the standing index.

**Why this is its own epic, not a line inside another one:** it's a 
universal primitive several other things depend on (presets, policies, 
future config surfaces), and it was caught, correctly, as a real 
prerequisite for B-269 before B-269 gets built — not something to bolt 
on after.

---

## 1. The real, converged pattern

Every mature platform researched uses the same three real membership 
modes, not one:

1. **Static** — manually assigned, exactly as `group_memberships` 
   (B-207) already works today.
2. **Dynamic/Smart** — membership computed automatically from a rule 
   against real attributes (IP range, OS, location, device type, 
   custom fields). Re-evaluated continuously, not on a manual refresh 
   (confirmed explicitly by IBM MaaS360's own "Smart Device Groups").
3. **Hybrid — "Exemptions"** (JumpCloud's own real, named pattern): a 
   dynamic rule computes membership, but specific assets can be 
   manually pinned in or out regardless of what the rule says.

**Confirmed, real precedent for the "universal parameter" idea 
specifically:** Huawei's IoT platform lets a dynamic group be selected 
as the live target of a firmware-upgrade task — membership changes 
automatically flow through to what the task applies to. This is 
directly the mechanism wanted here: a Group, once defined, should be 
selectable as a target anywhere a policy, preset, or future config 
surface currently references individual assets.

**Honest limitation, disclosed by JumpCloud about its own product:** 
"there is no validation in the rule builder... you could configure 
rules that contradict each other." Worth designing toward reasonable 
validation; not worth promising to catch every logical conflict.

---

## 2. The architectural resolution — extend, don't duplicate

Same discipline already used for Workspace-is-a-Group and 
Automation-runs-as-Agent: a Group (the existing B-207 primitive) gets an 
**optional rule definition**, not a second, parallel "smart group" 
table.

- No rule → today's exact static behavior, unchanged.
- A rule, no exemptions → pure dynamic group.
- A rule plus exemptions → the JumpCloud-confirmed hybrid.

**The rule's query mechanism should reuse CMDB's own already-built, 
already-tested filter vocabulary** (`kind`, `category_id`, `type_id`, 
`workspace_id`, `q`, and the real attributes it already exposes) rather 
than inventing a second query language for the same underlying data.

---

## 3. Proposed out-of-the-box default groupings

Adapted to what this product actually governs — not a generic ITAM 
default set:

- By OS/Platform (Windows / Linux / macOS)
- By discovery method (Agent-based / Agentless) — the real architectural 
  split already designed for Discovery
- By `risk_tier` (already real on governed agents)
- By location / IP range
- By "AI activity detected" — governance-specific; no general ITAM tool 
  would ship this, but it's exactly what this product's own admins need 
  most (e.g., "apply this stricter preset only to machines where AI 
  activity has actually been found")

This is a proposal for the epic to formalize during real design, not a 
final, locked list.

---

## 4. Explicitly future — not designed now

**AI-suggested groupings.** Real, and worth keeping on record, but its 
natural home is genuinely later than the near-term phase plan — it needs 
real historical usage data and a mature, well-used rule mechanism to 
already exist before an AI could meaningfully suggest anything. Likely 
sequenced alongside or after Horizon 2's agentic layer, not before. No 
design work done beyond naming it so it isn't lost.

---

## 5. Sequencing — the one hard dependency this creates

**This epic's core schema decision (§2) must resolve before B-269 
(Discovery presets) is built**, since B-269 was already agreed to assign 
presets via Groups. The full rule-builder UI and default-groupings list 
can follow as their own build step; only the schema shape needs to be 
settled first, so B-269 isn't built against a foundation that has to 
change under it.
