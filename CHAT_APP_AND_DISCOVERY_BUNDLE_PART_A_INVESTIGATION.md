# Chat app and discovery bundle (B-305): Part A investigation

**Date:** 2026-10-07 · **Author:** Claude Code · **Status:** read-only investigation, no code. Design record: `CHAT_APP_AND_DISCOVERY_BUNDLE_DESIGN.md` (parked).
**Method:** code trace of `eami-gateway` (`internal/aiprovider`, `internal/mcp`, `cmd/gateway`), `eami-agent` (`cmd/agent`, `internal/nativemsg`, `installer/`), `eami-ui`, and the backlog and roadmap entries cited. Nothing was built or run.

Terms as in the design: **governed agent**, **discovery agent**, **human user**, **chat app**.

---

## 1. How a completion flows through the gateway today

- **Path:** there is no chat or completion endpoint. A completion is an MCP `tool_call` (or a workflow step) whose tool name is `<connector>.messages`, where the connector is an `ai_provider` tool. It goes `mcp.Handler.ServeMessages` → `Dispatcher.Dispatch` (liveness, licence, usage, policy) → `aiprovider.Router.Dispatch` → `ClaudeAdapter`. Claude is the only adapter, and `messages` is its only action (`aiprovider/claude.go:65`). The reply comes back on the agent's SSE stream as a JSON-RPC result.
- **Streaming: no.** `aiprovider.Response` is "already fully buffered -- streaming is explicitly out of scope" (`aiprovider/types.go:46`). The adapter reads the whole provider response, then the gateway sends one SSE `message` event. The MCP SSE transport streams *events*, but a completion arrives as one block. The design's "streaming" requirement (§4) needs new work in the adapter, the dispatcher and the transport.
- **Redaction: prompts only.** Pattern redaction (`redaction.Redact`) runs on the request parameters, immediately before the adapter call (`aiprovider/router.go:147`), per connector (`gateway_tools.redaction_rules`). **The model's response is never redacted.** The audit row and episode record a `redacted_count`, so a "redaction notice" for the prompt is possible from data the gateway already has. There is no response-side redaction to notify about.
- **Token cost:** after a successful dispatch, `recordTokenUsageHook` → `extractTokenUsage` reads Claude's `usage` block (input, output and cache tokens) from the response, then `safeWriteTokenUsage` posts it **asynchronously, fire-and-forget**, to eami-api `POST /v1/internal/token-usage` with the global service key (B-243). A failed write is only logged. Cost is computed at query time in FinOps from `token_usage` and `model_pricing`, attributed to the governed agent, never to a human user.

## 2. What identity a gateway call records

Only the **governed agent**. See `AGENT_IDENTITY_PART_A_INVESTIGATION.md` §7: `ActionContext` and `audit_log` carry the agent (and, since B-301, the token's JTI and issuing key), never a human user; `approved_by` records an approver, not the person a call was made for. B-301 added token binding (`agent_uuid`, `api_key_id`) but no user context. The design's delegated mode (§5) needs agent-identity Slice B and the C5 decision on `audit_log`.

## 3. What B-152, B-130 and the Horizon 2 Chat Engine entries say

- **B-152 (unified multi-provider API surface):** one endpoint where the caller picks the model by parameter. Explicitly **gated**, "not startable yet": it needs 2–3 more provider adapters beyond Claude before "unified" means anything. It would sit on the existing `ai_provider` adapter registry. No OpenAI-compatible endpoint exists today.
- **Roadmap Horizon 2, item 7, "The Chat Engine":** "the real, daily web surface … **distinct from B-130's original scope, a native desktop client** — related, not identical. The Chat Engine (web) can ship first; a native desktop app remains its own, later, separate decision." `DESIGN_SYSTEM.md` defines the Chat Engine mode (warm palette, canvas Layer 5), and §7.8 lists "Chat" only as an inert placeholder sidebar entry.
- **B-130 (EPIC: Native Governed AI Desktop Client, "the Claude Desktop replacement")** already exists, logged and not investigated. It is substantially the same product as this record's chat app:
  - a first-party desktop app on end-user machines, provider-agnostic, as **a thin client to the gateway** that makes no decisions itself;
  - admin policy push, usage monitoring, skills distribution;
  - packaging, signing, auto-update and enterprise deployment called out as real new infrastructure ("a THIRD installed client");
  - a gateway-orchestrated agentic loop in scope;
  - **the gateway, not the client, owns conversation history**;
  - a designed "gateway unreachable" behaviour.

  See §7, conflicts C1 and C2.

## 4. Installers, elevation, and any local interface

- **Windows (`Product.wxs`):** WiX, `Scope="perMachine"`, installs to `Program Files\EAMI\Agent`, one feature with components for the binary and config, and a **`ServiceInstall` as `LocalSystem`**. A second component is structurally easy: another `Component` or `Feature` in the same package (for example the app under `Program Files` and a Start-menu shortcut). **Elevation is required**, and **there is no per-user install path.**
- **Linux (`nfpm.yaml`, deb and rpm):** system packages with root scripts (`postinstall.sh` etc.); a systemd unit with `User=root`. A desktop app could ship as more files (binary plus a `.desktop` entry) in the same or a sibling package. Installation is always root; there's no per-user path.
- **macOS (`installer/macos`):** a `.pkg` with a root `postinstall`; the service is a **LaunchDaemon** (`/Library/LaunchDaemons/io.eami.agent.plist`). An app bundle in `/Applications` could be added to the same pkg. Admin rights are required, and macOS remains "unverified" until B-274.
- **Local interface: none.** The discovery agent opens no socket, named pipe or HTTP listener (a grep for `net.Listen`/pipes/`ListenAndServe` finds none). The only "second process" precedent is the **browser native-messaging host** (`eami-agent --native-messaging-host`, `internal/nativemsg`). That is the same binary, launched **per user by the browser**, talking to the extension over stdin/stdout. It is not a channel to the running service: it loads the agent config itself and sends paste events **directly to the collector under the agent's identity**. A user-level chat app therefore has no way today to ask the service for status, for the endpoint identity, or for the disclosure content the design wants (§3 "Disclosure").

## 5. Chat code in the repo

**None beyond design material.** No chat page or component exists in `eami-ui` (the only "chat" string match is unrelated, in the paste-events page). No chat route exists in eami-api or eami-gateway; the Go matches are AI-app *detection* (ChatGPT and Claude Desktop as discovered apps). There are only:
- `DESIGN_SYSTEM.md`'s Chat Engine mode and palette, from the canvas Layer 5 mock;
- the inert "Chat" sidebar placeholder (§7.8, `IA_CONSOLIDATION_MIGRATION_PLAN.md`).

## 6. Sharing enrollment and endpoint identity between the two components

- **Today:** a machine's endpoint identity is the discovery agent's `agent_id` (hostname-derived, B-295) plus the collector key. Linking an endpoint to a governed agent is a manual admin action (B-164/B-200).
- **Planned (B-269 Slice 3, decision D4):** an enrollment key is exchanged once at first registration for a **server-issued per-endpoint credential**, stored in its own protected file in the B-293 state directory (readable by SYSTEM/root only), with reports attributed by that credential, not by hostname. This is the "one endpoint identity per machine" the design needs, and the new §12 line (keys bind to a preset, never a package type) keeps it package-agnostic.
- **How the chat app should join, given privilege separation:**
  - The chat app must **not** read the endpoint credential: it is SYSTEM/root-only by design, and the design says the app never holds privileged material.
  - It needs the service to **tell it** the endpoint identity and status. That requires a new local interface: a per-user-ACL'd named pipe or Unix socket, read-only, for status and disclosure. None exists (§4).
  - **Alternative:** the server joins them. The user signs in to the chat app, and the server links that sign-in to the endpoint. But the only machine key the app could present is the hostname, which is exactly the B-295 collision problem.
- **Install-order cases:**
  - **Chat bundle first:** the installer runs the standalone service install with the enrollment key, so the service creates the identity.
  - **Service first:** the bundle detects the existing service and installs only the app, so there is never a second enrollment.

  Both need the service to be detectable and queryable by the installer. That works on Windows and macOS (service and daemon registries) but is undefined for version skew (design §6).

## 7. Conflicts with code and repo reality (flagged, not worked around)

| # | Design says | Reality | Consequence |
|---|---|---|---|
| C1 | B-305 is a new epic | **B-130 already exists** for the same product (native governed desktop client, thin client to the gateway), logged 2026-08 and never investigated | Two epics for one product. **Founder decision:** fold B-305 into B-130 (B-305 as the discovery-bundle part, or superseded), or retire B-130 in favour of B-305. Nothing changed. |
| C2 | Roadmap: chat app "extends the existing Chat Engine item" (as added in this brief's Part 0) | The roadmap's item 7 says the Chat Engine is the **web** surface and **distinct from** the native desktop client (B-130), "a later, separate decision" | The line I added per the brief may mislead. **Founder decision:** keep it under item 7, or re-point it to B-130 as the desktop client. Not changed. |
| C3 | Conversation storage is open (local, server or none) | B-130 states the **gateway** should own and re-inject conversation history, so every turn's full context passes policy and audit | A prior design position exists; reconcile when deciding storage. |
| C4 | Streaming (§4) | Completions are fully buffered (`aiprovider/types.go:46`) | New work in the adapter, dispatcher and MCP transport (B-130's "Model 2" territory). |
| C5 | Redaction notices; governance visible in the app | Redaction is **request-side only**; responses are never redacted | Notices can cover prompts only, unless response redaction is designed. |
| C6 | Delegated mode: every call carries the user; audit records the user | No user context anywhere in a gateway call; `audit_log` has no user column (agent-identity C5) | The design already says don't claim it; it's blocked on agent-identity Slice B and C5. |
| C7 | Disclosure: the app tells the user what runs, from policy | The service exposes **no local interface** | A new, minimal, read-only local interface (pipe or socket with per-user ACL) is needed. It's a new attack surface on a SYSTEM/root service and needs its own security review. |
| C8 | Privilege separation: the chat app never holds SYSTEM/root or privileged material | The existing user-level precedent, the **native-messaging host, loads the agent config and sends to the collector as the agent**, so a user-level process already handles the machine's agent credential (cf. B-297, the possibly-readable HKLM key) | Don't copy the native-messaging pattern for the chat app. B-297 and the D4 protected credential file are prerequisites. |
| C9 | The installer installs the service if absent; "no elevation" means the app says "not known" | All three installers are **per-machine and need elevation**; there's no per-user install mode, and a per-user app-only install would be a new installer variant | The "unmanaged laptop, no elevation" behaviour (design §6) needs a new per-user app-only package, and the service can't be added without elevation. |
| C10 | The LLM-facing API is MCP or OpenAI-compatible (B-152) | No OpenAI-compatible endpoint; B-152 is gated on 2–3 more adapters; MCP `tool_call` to `<connector>.messages` works today but isn't a chat API | Slice 1 can use the MCP path; B-152 stays gated. |
| C11 | Token cost per user | Token usage is attributed to the governed agent, and the write is fire-and-forget | Per-user cost needs the user identity (C6) and possibly a durable write. |

**Not conflicts, but noted:**
- The same discovery binary in both packages is already the pattern: the native-messaging host is the same binary.
- B-193 (per-user detection from a SYSTEM service) remains independent of the app, as the design says.
- macOS is unverified (B-274).

---

## Summary for the founder

- **The biggest finding is C1/C2:** B-130 already is this product (a native governed desktop client), and the roadmap explicitly separates it from the web Chat Engine. Decide how B-305 relates to B-130, and where its roadmap line points.
- **The technical gaps** before even Slice 1:
  - **streaming** (completions are buffered);
  - **response-side redaction** (absent);
  - **user identity in calls** (agent-identity Slice B and C5).
- **The bundle (Slice 2) needs:** a new **read-only local interface** on the service (security-reviewed), **B-269 Slice 3's protected per-endpoint credential**, and **B-297** resolved. The native-messaging pattern must not be reused.
- **All installers are per-machine and elevated.** "No elevation" means a new per-user app-only variant, with discovery shown as "not known".
