# Chat app and discovery bundle: design record

**B-ID:** **B-130** (EPIC: Native Governed AI Desktop Client; B-305 was merged into it 2026-10-07). **Status:** design record only, **parked (not scheduled)**; listed under "Parked (not scheduled)" in `AI_ITAM_EPIC_MASTER_SEQUENCE.md`, not in any phase.
**Recorded:** 2026-10-07 by Claude Code, from the founder's brief; the design below is saved as given.
**Roadmap:** Horizon 2, its own line (B-130), distinct from the hosted Chat Engine (`rheoARC_Roadmap_Enterprise_AI_Platform.md`). Where this record and B-130's entry disagreed, the founder decided each on 2026-10-07 (§11); the first-surface question is open (§12).
**Related:** B-152 (unified API surface), B-193 (per-user detection blind spot), B-269 (presets and enrollment), B-295 (hostname collision), B-138 (SSO/IdP), B-300 (agent identity, `AGENT_IDENTITY_DESIGN.md`).
**Part A investigation:** `CHAT_APP_AND_DISCOVERY_BUNDLE_PART_A_INVESTIGATION.md`.

---

## 1. DECISIONS (founder, 2026-10-06)
- The chat app is an endpoint-installed desktop application (like Claude Desktop or ChatGPT Desktop): the governed interaction layer between a user and LLMs through the EAIM gateway. A hosted chat mode and a local traffic interceptor are separate ideas, not this record.
- If the chat app is installed, discovery runs on that machine as configured by its preset, with no separate discovery agent deployment.
- Form: one bundle, two components, never one process: the privileged discovery service (the same binary as the standalone agent, no fork) and a user-level chat app. The installer installs the service if it is not already present.
- The standalone discovery package stays: servers, machines that never run chat, shadow-AI users. Discovery never depends on chat adoption.

## 2. TERMINOLOGY
Governed agent (an AI workload), discovery agent (software on a machine), human user, and chat app (a client). Docs and briefs never say "agent" alone.

## 3. PRINCIPLES
- One endpoint identity per machine: one enrollment and one preset, whichever package lands first creates it and the other joins. Never two endpoint records for one machine.
- Privilege separation: the chat app never holds SYSTEM or root, so a UI bug cannot become a privileged bug.
- Chat without the service is not a v1 mode. Partial discovery that looks complete is the problem item 4 removed. If the service cannot be installed (no elevation), the app says so and discovery shows "not known", never a clean result.
- Disclosure: the app tells the user, in the app, what runs on the machine and who sees it, driven by policy, never a side effect.
- The app must be better than the shadow tools it replaces, or it will not be used.

## 4. WHAT THE APP NEEDS (minimum, indicative)
Sign-in; models limited by policy; streaming; visible governance (redaction notices, denials, escalations waiting for approval shown in the app); tokens in the OS keychain; signed builds and auto-update.

## 5. IDENTITY
Chat uses delegated mode from AGENT_IDENTITY_DESIGN.md: every call carries the signed-in user, and audit records the user, the governed agent identity used and its owner. Recording the user depends on constraint C5 in AGENT_IDENTITY_DESIGN.md (audit_log has no user columns and a fixed hash). Until C5 is resolved, nobody may claim externally that audit records the user behind a chat call. OPEN, not decided: one chat-client governed agent per org (owner = the deploying admin, each call carries the end user) or one per user.

## 6. OPEN DECISIONS
Conversation storage (local, server or none: data residency, retention, and the B-279 secrets concern); the LLM-facing API (MCP or an OpenAI-compatible endpoint, B-152); desktop technology (open until the first-surface decision, §12; its security review is required); whether the app optionally sends user-context discovery data (a bonus that partially covers B-193, never a dependency, since B-193 is fixed in the agent by enumerating user profiles regardless); version skew between the two components; behaviour on an unmanaged laptop with no elevation.

## 7. DEPENDENCIES
Agent identity: A1 shipped (B-301, B-302), A2 and Slice B needed for delegation; presets Slices 1-4; B-152 or the MCP path; the B-193 fix; B-138 for customers with an IdP.

## 8. PROPOSED SLICES (not scheduled)
0 prerequisites above. 1 minimal client: sign-in, one model through the gateway, streaming, redaction and denial notices. 2 the bundle: the installer installs the service if absent, with shared enrollment. 3 approvals in the app, plus MCP tools. 4 conversation storage once decided.

## 9. DEFERRED
The hive AI mind (chat across endpoints, collective intelligence), mobile, the local interceptor. Later phases of B-130 (decided 2026-10-07): the coding-agent interface; admin policy push to the client fleet; usage monitoring; skills distribution; RAG; content-aware model routing (depends on the B-150 classifier question). Hosted web chat is the roadmap's separate Chat Engine item.

## 10. CONSTRAINTS (founder, 2026-10-07)
- **The chat app never reads or reuses the discovery credential** (the per-endpoint credential of B-269 Slice 3, or the collector key). Unlike the browser native-messaging host, which sends as the discovery agent (B-275, B-297), the chat app authenticates as its user.
- **The discovery service's local interface authenticates its callers and exposes only status and disclosure text, never a credential** (B-297, B-275). It is new attack surface on a SYSTEM/root service and gets its own security review.
- **A no-elevation or per-user install is an open decision, not v1.** All existing installers are per-machine and elevated.
- **Conversation storage:** B-130's **gateway-owned history is the recorded default.** Open questions: is history stored redacted or raw; and how does deleting a conversation interact with audit rows, which are never deleted (B-224)?
- **The first chat slice is a gateway change before it is a client:** streaming (completions are buffered today), response-side redaction (only prompts are redacted today), and per-user cost (today cost is per governed agent).

## 11. DECISIONS ON THE B-130 DIFFERENCES (founder, 2026-10-07)
1. **History:** the gateway owns history. Open: store it redacted or raw; how deleting a conversation interacts with audit rows, which are never deleted (B-224).
2. **Packaging:** this record wins. The bundle keeps the discovery service; B-130's "third installed client" is a component count.
3. **Modes:** chat first. The coding-agent interface is a separate later phase, out of scope here.
4. **Tool use:** thin client. The gateway drives the agent loop; the app has no MCP client (removed from §4). Slice 3's "MCP tools" means governed tools the gateway calls in its loop. Escalation in the middle of a loop stays an open question.
5. **Models:** v1 is one model. Content-aware routing is later and depends on the B-150 classifier question.
6. **Identity:** delegated identity, which depends on agent identity Slice B and C5 (§5).
7. **Discovery:** this record wins. The chat app reduces shadow AI while detection still runs; B-130's "replacing the need to detect" is annotated there, not deleted.
8. **Further scope:** policy push, usage monitoring, skills distribution and RAG are later phases of B-130 (§9).
9. **Desktop technology:** open until the first-surface decision (§12); its security review is required.

## 12. FIRST SURFACE: OPEN
**Not decided.** The first slice is gateway chat work shared by any surface: a chat endpoint, streaming, response redaction, per-user cost, and gateway-owned history (§10). The recommendation is to prove it with a thin web client first, then wrap it in the desktop app.
