# Agent identity (B-300): Part A investigation

**Date:** 2026-10-06 · **Author:** Claude Code · **Status:** read-only investigation, no code. Design record: `AGENT_IDENTITY_DESIGN.md` (parked).
**Roadmap:** Horizon 1, next to B-138 and B-231; not itself a numbered roadmap item.
**Method:** code trace of `eami-gateway` (`internal/identity`, `internal/mcp`, `internal/registry`, `cmd/gateway`), `eami-api` (`agents.go`, `types.go`, `router.go`) and `eami-collector`; the baseline and later migrations; read-only queries on the dev database. **Nothing was live-tested** for revocation (§3); that answer comes from the code.

Terms as in the design: **governed agent** (AI workload, `gateway_agents`), **discovery agent** (`eami-agent` on a machine), **human user** (`users`).

---

## 1. The owner field

- **Free text.** `gateway_agents.owner TEXT NOT NULL` (`000001_baseline.up.sql:163`). No reference to `users`. `AgentUpdateRequest` (`eami-api/internal/api/types.go:121`) can't change it: only scope, risk tier, status and token lifetime are editable (B-255).
- **It's copied into every token** as the free-text `owner` claim (`identity/tokens.go` `Claims.Owner`, set from the record at issuance, `issue_http.go`).
- **Live data (dev database):** 13 governed agents. **0 have an owner that matches a real user**: not by email (live or deleted), not by user ID. Values are things like `realowner@example.com`, `Test`, `b108-reverify`, `bhargav`. One value (`bhargavrash@gmail.com`) is a real user's email, but in a different org.
- **The only user link is `created_by`** (`UUID REFERENCES users(id)`): set on 10 of 13, all 10 pointing at live users. Creator isn't the same thing as accountable owner, so it's a possible backfill source, not an answer (§10, C1).

## 2. Tokens

| Aspect | Today |
|---|---|
| Format | JWT, **RS256** (`tokens.go`), via `golang-jwt`. Validation rejects non-RSA algorithms and checks audience and issuer. |
| Claims | `sub` = `agent:<name>`, `iss` (configured), `aud` = `eami-gateway`, `iat`, `exp`, `jti` (16 hex chars of SHA-256 over agent and time), plus custom `org_id` (server-set, B-141), `scope`, `task`, `model`, `owner` (free text), `risk_tier`. **No user claim.** |
| Lifetime | The agent's `token_ttl_seconds` (default 900), clamped to **60 s – 4 h** (`minTTL`/`maxTTL`). Gateway default `token.default_ttl_seconds` 900. |
| Signing key | **One RSA-2048 keypair**, generated on first boot if absent (`loadOrGenerateKey`), PKCS#1 PEM, **unencrypted**, mode 0600. In compose: `GATEWAY_JWT_KEY_PATH=/certs/gateway.key` on the `gateway_certs` Docker volume. **Not in the database backups** (`pg_dump` only), so a lost volume means a new key and every live token invalid. |
| Key ID / rotation | **No `kid` header, no rotation, one key.** `/.well-known/gateway-jwks.json` publishes that single key. Replacing it invalidates every live token at once. |
| Issuance | `POST /v1/gateway/tokens` with an agent-bound API key (`X-API-Key`), B-098 (§4). Issuance writes `ai_token_events` (`issued`). |

## 3. Revocation: do live tokens and open sessions stop immediately? **No** (code trace; not live-tested)

**What does stop:**
- **New tokens:** issuance refuses a suspended or revoked agent (`issue_http.go:308`).
- **New MCP sessions:** `GET /v1/mcp/sse` validates the token, then `registry.LookupByNameAndOrg`, which refuses `suspended`/`revoked` (`registry.go` `checkStatus`).
- **Episode reads and workflow runs:** both validate the agent's JWT and re-resolve the agent (with the status check) on every request (`episode/http.go:123`; `workflow/http.go` `HandleRun`). A workflow run already in progress was not traced.

**What doesn't stop:**
- **An MCP session opened before the suspension keeps working until its token expires** (up to the agent's TTL, at most 4 h):
  - `ServeMessages` (`POST /v1/mcp/messages?sessionId=…`) authenticates **by session ID only**. It doesn't re-check the token or the agent's status, and uses the agent record cached when the session opened (`sess.Agent`).
  - The dispatcher doesn't check status either.
  - So a suspended governed agent's open session can keep dispatching tool calls.
- **Suspending doesn't revoke tokens.** `UpdateAgent` (`eami-api/internal/api/agents.go`) changes `status` and writes a lifecycle event. It sends no notification to the gateway, writes nothing to `revoked_ai_tokens`, and closes no session. A token issued before the suspension stays cryptographically valid until it expires; it's only useless for *new* SSE sessions.
- **Revoking a token (by JTI) doesn't end its session.** `Revoke` adds the JTI to an in-memory set checked by `Validate`, which runs only when a session opens.
- **Multi-node:** the revoked set is loaded from the database **at startup** and updated **only in the process that handled the revoke**. Another gateway node keeps accepting that JTI until it restarts. (There's no LISTEN/NOTIFY for revocations; approvals use one, policies use `policy_reload`.)
- **`revoked` status isn't reachable from the product:** the UI only sends `suspended`/`active`, and `revoked → suspended → active` is possible through the API (B-230, open).

**Confirming this live** needs: open an SSE session, suspend the agent, send a `tool_call` on the open session, and see it dispatch. That's a short test against the real stack, not done here.

## 4. Keys

- **Gateway agent keys** (`api_keys`, Postgres): SHA-256 hash, prefix, `scopes[]`, `created_by`, `last_used`, `expires_at`, `revoked`; `agent_id` added by B-098 (`000010`).
- **B-098's gating:** token issuance needs a valid, unrevoked, unexpired key that is **bound to an agent** (`agent_id` not null). The requested agent is resolved inside the key's own org, and must be that same bound agent and not suspended or revoked. Otherwise 401/403 with a single message (no name-enumeration oracle).
- **Expiry:** supported by the schema and API (`expires_at`, settable since B-098), **optional, and unused**: 0 of 11 keys have one.
- **Rotation:** **none.** There's no rotate operation and no overlap window; the path is create a new key, then revoke the old one.
- **Last-used:** **column exists, never written.** Nothing in either service updates `api_keys.last_used`; 0 of 11 keys have a value.
- **Keys per agent:** **any number.** There's no uniqueness on `agent_id`. Live: 11 keys, all agent-bound, 9 revoked.

## 5. The collector's per-agent keys vs gateway agent keys

**Entirely separate**, for different identities:

| | Collector keys (B-073) | Gateway agent keys (B-098) |
|---|---|---|
| Identity | **Discovery agent** (a machine; `agent_id` = hostname) | **Governed agent** (`gateway_agents.id`) |
| Store | `eami-collector`'s own **SQLite** `api_keys` | eami-api's **Postgres** `api_keys` |
| Issued by | CLI `collector mint-key --agent-id <host>` | Admin UI or API (`POST /v1/auth/api-keys`) |
| Used for | `X-API-Key` on report upload; the report's `agent_id` must match the key | `X-API-Key` on token issuance |
| Also accepted | **the legacy shared `COLLECTOR_API_KEY`** (static, still accepted) | — |

The only link between the two kinds of agent is the endpoint-to-governed-agent link (`endpoints.gateway_agent_id`, B-164/B-200), not credentials. B-295 (hostname collision) and the D4 per-endpoint credential (B-269 Slice 3) are about the collector side.

## 6. B-243: the global service key

- One platform-wide `cfg.ServiceKey` checked by `requireServiceKey` on four eami-api routes:
  - `POST /v1/reports`: **no caller**; takes `org_id` from the body.
  - `POST /v1/ingest/batch`: the collector's forwarder; org resolved server-side (default org).
  - `POST /v1/internal/token-usage`: eami-gateway (FinOps), `org_id` in the body.
  - `GET /v1/agents/{agent_id}/config`: discovery-agent remote config, proxied by the collector (`config_proxy.go`).
- **Holders:** eami-collector (forwarder and config proxy) and eami-gateway (token usage).
- The gateway has two **separate** static service keys of its own: `EpisodeReadServiceKey` (episode reads) and `TokenRevokeServiceKey` (`POST /v1/gateway/tokens/{jti}/revoke`, with `org_id` supplied by the caller).
- **Risk as recorded on B-243:** any holder can write reports or token usage into any org, which is harmless only while all holders are platform-operated. It is a hard gate before a customer runs their own collector or gateway.

## 7. What identity a gateway call records

- **Only the governed agent.** `ActionContext` carries agent ID, UUID, name, org, workspace and scope, plus tool, action, parameters, environment, session and workflow run. **No user field.**
- `audit_log` stores `agent_id`/`agent_name` and, for escalations, `approved_by` (the approver: a human, but the person who approved the call, not the one it was made for).
- **No user context exists for any call path:**
  - MCP sessions are opened by the agent's token.
  - Workflow runs are triggered with the governed agent's JWT; `workflow_runs` has no user column.
  - Workspaces scope *users'* access to governed agents in eami-api, but no user identity flows into a gateway call.
  - The **Chat Engine** (where a user would drive an agent) is a design-system mode only; no Chat UI or route exists.

## 8. B-138, B-230 and B-255

- **B-138 (SSO/IdP):** a scoping placeholder for *human user* login through the customer's IdP. Unused `users.sso_provider`/`sso_subject` columns exist; nothing reads them. Open questions are OIDC vs SAML and a per-org IdP config table. **It covers users, not governed agents**; the design's slice C (and delegated calls in IdP orgs) depends on it.
- **B-230 (status transitions):** `revoked` isn't terminal (`revoked → suspended → active` in two clicks), and revocation isn't lifecycle-audited. The fix (409 out of `revoked`) is the design's "revoked cannot return". Live: 0 revoked agents.
- **B-255 (agent edit surface):** the UI can't edit scope, risk tier or token lifetime; the API can. It's folded into B-252 C7. It doesn't cover owner. The design's owner, backup owner, review date, expiry, mode and credential type would be new fields on that surface.

## 9. Federation: where an external token would be validated, and air-gapped needs

**Validation point:** `identity.Manager.Validate` is the single validator for governed-agent tokens. It's called by `mcp.Handler.parseBearer` (session open), `workflow/http.go` `HandleRun`, and episode reads. (`identity.Middleware` also wraps it but isn't wired to any route.) A federated mode would add a validator next to it that:
- selects by `iss`;
- checks signature, `aud`, `exp` and `nbf` with small clock-skew tolerance;
- maps claims to a governed agent through the design's "external binding" (issuer plus claim mapping, org-scoped);
- then runs the same registry status check.

Because messages on an open MCP session aren't re-validated (§3), federation would inherit the same "valid until expiry" gap unless that is fixed first (slice A).

**What an air-gapped deployment needs:**
- **No required JWKS fetch.** Per issuer, an admin uploads the IdP's signing keys (JWKS or PEM), stored org-scoped. Optionally an opt-in periodic fetch where outbound is allowed, keeping the **last good set** on failure.
- **`kid` selection with several keys per issuer**, so the IdP's rotation works: upload the new key, keep the old one until its tokens expire.
- **Pinned issuer and audience per binding**, plus an algorithm allowlist (RS256 or ES256; never `none` or HMAC with a public key).
- **The built-in issuer needs the same machinery** for its own rotation. Today it has no `kid` and one key (§2).
- **The user side (B-138)** is a different service: human users authenticate to eami-api (`auth.Service`, its own RS256 key). A delegated call would need the gateway to trust a user token, either the IdP's or eami-api's. Today there's **no cross-service user-token trust** at all.

## 10. Conflicts between the design and the code (flagged, not worked around)

| # | Design says | Code reality | Consequence |
|---|---|---|---|
| C1 | Owner is a **required link to a real user** | Free text `NOT NULL`; 0 of 13 match a user; copied into every token | Needs a migration plus a backfill decision: `created_by` (10/13), manual, or flag-and-require on next edit. Founder call. |
| C2 | Suspend and revoke **invalidate live tokens and sessions immediately** | Open MCP sessions keep dispatching until token expiry (≤ 4 h); messages are authenticated by session ID only; suspend doesn't revoke tokens; JTI revocation is per-process and only checked at session open | The design's core lifecycle guarantee doesn't hold today. Slice A must add per-call status/revocation checks and cross-node propagation (`NOTIFY`), or close sessions on suspend. |
| C3 | **Short-lived credentials; static keys are bootstrap secrets only** | Agent API keys have no expiry in practice (0/11), no last-used, no rotation; the collector still accepts the static `COLLECTOR_API_KEY`; one global service key (B-243) | All three are long-lived static secrets today. |
| C4 | **Key rotation with overlap** | One signing key, no `kid`; rotation invalidates every live token | The built-in issuer needs multi-key and `kid` before rotation is possible. |
| C5 | **Audit records the agent, the user and the owner** | `audit_log` has no user or owner columns, and its hash covers fixed gateway fields only | Adding them touches `audit_log`'s schema and possibly its hash (the "don't touch `audit_log` semantics" class). A separate delegation record, or new non-hashed columns, needs a decision. |
| C6 | **Secrets never in audit, logs or reports** | The MCP SSE and workflow-run handlers return `"unauthorized: "`/`"invalid bearer token: " + err.Error()` (parse errors, the revoked JTI) and `"agent not registered or suspended: " + err.Error()` (reveals the status) to the caller (`identity.Middleware`, unused, does the same) | A standing-check finding (raw error text across a trust boundary). Small; outside this brief; worth a B-ID. |
| C7 | **Revocation is a lifecycle state** that can't be undone | No product action sets `revoked`; the API allows leaving it (B-230) | B-230 is a prerequisite for slice A. |
| C8 | **Token issuance and failed authentication are not admin events; where they're recorded is open** | Issuance **is** recorded today (`ai_token_events`: `issued`, `revoked`); failed authentication is only `slog` | The open question is half answered: a table exists for issuance. Failed authentication has no durable record. |
| C9 | **Discovery reconciles IdP agent principals** (shadow AI) | Discovery is endpoint scanning (and planned network or credentialed probes); nothing reads an IdP directory | New capability; depends on an IdP connector (B-138-adjacent). |
| C10 | **Terminology:** never "agent" alone | Code and schema use "agent" for both kinds: `gateway_agents`, the collector's `agent_id` (= hostname), `eami-agent`, `/v1/agents/{agent_id}/config` | Docs can follow the rule; renaming code isn't proposed. Readers must disambiguate by table or route. |
| C11 | Signing key handling | Unencrypted PEM on a Docker volume, not in backups | Worth recording for slice A (back up or derive, and protect at rest). |

**Not conflicts, but worth knowing:**
- Governed agents already have `workspace_id` (Workspaces), and the token carries a server-set `org_id`. Both fit the design.
- The approval flow already records a human (`approved_by`).

---

## Summary for the founder

- **The design is sound against the code.** The biggest gap is **C2**: suspending a governed agent doesn't stop an already-open MCP session, which can keep dispatching tool calls for up to the token's lifetime (max 4 h). That's a security-relevant finding in its own right, independent of whether B-300 is scheduled. **Suggest minting it now** (Medium–High) and confirming it with a short live test.
- **C6** (raw error text in gateway 401/403 bodies) is a small standing-check finding, also worth its own B-ID.
- Slice A's real scope is larger than the record suggests: owner migration (C1), per-call status and revocation checks with cross-node propagation (C2), key expiry, last-used and rotation (C3), multi-key signing with `kid` (C4), and B-230 (C7).
- Delegation (slice B) collides with `audit_log`'s fixed schema and hash (C5) and needs a design decision before any build.
