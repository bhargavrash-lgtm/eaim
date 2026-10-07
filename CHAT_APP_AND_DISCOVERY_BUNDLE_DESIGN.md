# Chat app and discovery bundle: design record

**B-ID:** **B-305** (epic). **Status:** design record only, **parked (not scheduled)**; listed under "Parked (not scheduled)" in `AI_ITAM_EPIC_MASTER_SEQUENCE.md`, not in any phase.
**Recorded:** 2026-10-07 by Claude Code, from the founder's brief; the design below is saved as given.
**Roadmap:** Horizon 2, extending the Chat Engine item (`rheoARC_Roadmap_Enterprise_AI_Platform.md`).
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
Sign-in; models limited by policy; streaming; visible governance (redaction notices, denials, escalations waiting for approval shown in the app); an MCP client; tokens in the OS keychain; signed builds and auto-update.

## 5. IDENTITY
Chat uses delegated mode from AGENT_IDENTITY_DESIGN.md: every call carries the signed-in user, and audit records the user, the governed agent identity used and its owner. Recording the user depends on constraint C5 in AGENT_IDENTITY_DESIGN.md (audit_log has no user columns and a fixed hash). Until C5 is resolved, nobody may claim externally that audit records the user behind a chat call. OPEN, not decided: one chat-client governed agent per org (owner = the deploying admin, each call carries the end user) or one per user.

## 6. OPEN DECISIONS
Conversation storage (local, server or none: data residency, retention, and the B-279 secrets concern); the LLM-facing API (MCP or an OpenAI-compatible endpoint, B-152); desktop technology; whether the app optionally sends user-context discovery data (a bonus that partially covers B-193, never a dependency, since B-193 is fixed in the agent by enumerating user profiles regardless); version skew between the two components; behaviour on an unmanaged laptop with no elevation.

## 7. DEPENDENCIES
Agent identity: A1 shipped (B-301, B-302), A2 and Slice B needed for delegation; presets Slices 1-4; B-152 or the MCP path; the B-193 fix; B-138 for customers with an IdP.

## 8. PROPOSED SLICES (not scheduled)
0 prerequisites above. 1 minimal client: sign-in, one model through the gateway, streaming, redaction and denial notices. 2 the bundle: the installer installs the service if absent, with shared enrollment. 3 approvals in the app, plus MCP tools. 4 conversation storage once decided.

## 9. DEFERRED
The hive AI mind (chat across endpoints, collective intelligence), mobile, a hosted web chat mode, the local interceptor.
