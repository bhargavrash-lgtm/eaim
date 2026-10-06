# B-301: live test and fix plan

**Date:** 2026-10-06 · **Author:** Claude Code · **Status:** live test done; fix **planned only, no code**.
**Item:** B-301 (an open MCP session keeps dispatching after the governed agent is suspended or revoked). Related: B-300 (Slice A1), B-302, B-253, B-230.

---

## 1. Result: **CONFIRMED, High**

**What a suspended, then revoked, governed agent can still do on an MCP session it opened while active:**
- **Every call is still processed:**
  - after **suspension**;
  - after **revoking its API key and its token**;
  - after setting its status to **revoked**.
- **Each call reached the dispatcher's allow path**, with a decision written to `audit_log`.
- **The gateway still attempted the downstream call.**
- **A second gateway node accepts a token** that node 1 has revoked.

**What does stop:**
- **Token expiry** ends the session.
- **New sessions** are refused after suspension or revocation on the node that handled the change.

## 2. Live test (dev stack, fixture data only)

**Fixtures:**
- governed agents `b301-fixture-a`, `-b` and `-c`, each with an agent-bound API key and a token;
- a fixture admin user (since soft-deleted);
- a deny policy `b301 fixture deny` (since deleted);
- a second gateway node, `b301-gw2`, started with `docker compose run` on port 8090 (since removed).

**Fixture error, stated plainly:**
- MCP tool names take the form `<tool>.<action>` (`splitToolAction`). The calls sent `b301_fixture_tool` with no separator, so the gateway saw an empty tool name. The deny policy's tool match never fired, and the calls fell to the **default allow**.
- The gateway then tried the default forward target, `localhost:9000` inside its own container. Nothing listens there, so the call failed locally, and the audit row was written as `denied` with no policy ID: the "proxy error" branch.
- **Nothing left the machine**, but that was thanks to the dead forward target, not the deny policy.
- **The finding is unaffected:** every audit row is the dispatcher processing a call it should have refused. The proof is actually stronger, because it went as far as attempting the downstream call.
- **For any rerun:** use `<tool>.<action>` names, and abort unless the baseline call's audit row carries the deny policy's ID.

| Step | Action | Result |
|---|---|---|
| a) baseline | Open an SSE session as A (active); send a call | Processed: audit row 13:06:13, `denied` (proxy error), latency 7 ms |
| b) suspend | `PATCH status=suspended` (200); call on the **same open session** | **Processed:** audit row 13:06:15, latency 4 ms. *Control:* a **new** session for A is refused (403). |
| c) revoke | Revoke A's API key (204), revoke its token by JTI (204), `PATCH status=revoked` (200); call on the **same open session** | **Processed:** audit row 13:06:16, latency 3 ms. *Control:* a **new** session with the token on node 1 is refused (401, "token … has been revoked"). |
| d) expiry | Agent B with `token_ttl_seconds` 60; open a session; call before expiry (processed); wait 75 s; call | **Not dispatched:** `POST /v1/mcp/messages` returns 404 "session not found or expired"; no audit row. The session manager ends the session at token expiry. |
| e) second node | Agent C **active**; node 2 running before the revocation; new session on node 2 before revoking (200); revoke the token on node 1 (204) | Node 1 refuses a new session (401). **Node 2 accepts a new session (200) with the revoked token.** |

(The first attempt at (e), with agent A, was refused on node 2 by the *status* check, so it was inconclusive. The clean rerun used an active agent and opened sessions only, with no tool calls.)

**Cleanup (before/after snapshot diff):**

| Table | Before | After | Note |
|---|---|---|---|
| `audit_log` | 1706 | 1710 | +4, the test's evidence; **stays** |
| `episodes` | 73 | 77 | +4, one per processed call |
| `agent_lifecycle_events` | 22 | 29 | +7 (created, suspended, deleted) |
| `ai_token_events` | 13 | 18 | +5 (3 issued, 2 revoked) |
| `api_keys` | 11 | 14 | +3, **all revoked** |
| `gateway_agents` | 13 | 15 | +2: `b301-fixture-a` (revoked), `b301-fixture-b` (suspended), inert. They can't be deleted, because the API refuses agents with episode history. `-c` was deleted. |
| `revoked_ai_tokens` | 0 | 1 | A's token. C's row cascaded away with C. |
| policies, policy conditions, live users, `gateway_nodes`, `approval_requests`, `token_usage`, `admin_audit_events` | — | unchanged | the policy was deleted, the admin soft-deleted, node 2 left nothing |

## 3. Part A §3 and §4, restated in full

**§3: suspended agents, open sessions, expiry.**
- **A suspended (or revoked) governed agent's open session keeps working.** `POST /v1/mcp/messages?sessionId=…` authenticates by **session ID only**. It doesn't re-validate the token or re-check the agent's status; it uses the agent record cached when the session opened. The dispatcher doesn't check status either. Suspending doesn't notify the gateway, revoke tokens or close sessions. Revoking a token by JTI is checked only when a session opens, in the process that handled the revocation; other nodes load the revoked set only at startup. **Confirmed live** (§2 b, c, e).
- **When its token expires, the session ends.** The session manager creates each session to expire at the token's `exp`; after that the message endpoint returns 404 and nothing is dispatched. **Confirmed live** (§2 d). So the exposure window is the rest of the token's lifetime: the agent's `token_ttl_seconds`, default 900 s, maximum 4 h.
- **New sessions** are refused for a suspended or revoked agent, by the status check at session open, and with a revoked token on the node that revoked it.

**§4: keys.**
- **Key expiry is enforced when set, but never set.** `ValidateAndResolveAgent` rejects a key whose `expires_at` has passed. But `expires_at` is optional and none of the 11 pre-existing keys has one.
- **Last-used is not written.** The `api_keys.last_used` column exists; no code in either service updates it. 0 of 11 keys have a value.
- **An agent can have any number of keys.** There's no uniqueness on `api_keys.agent_id`, and no rotation operation (create a new key, then revoke the old one).
- **Revoking a key doesn't affect live tokens.** Keys only gate *issuance* (B-098); tokens already issued with a revoked key stay valid until they expire (§2 c).

## 4. Fix plan (no build until approved)

### 4.1 Per-call validation at one choke point
- **Where:** `Dispatcher.Dispatch` (`cmd/gateway/dispatcher.go`), the path both production callers use (MCP messages and workflow runs). Add a first check, `rejectOnInactiveAgent`, before the license check.
- **What it reads:** one query by primary key, per call:
  - the governed agent's `status` (by `ac.AgentUUID` **and** `ac.OrgID`);
  - whether the session's token JTI is in `revoked_ai_tokens`.

  The database is the source of truth, so it's correct on every node even if a notification is missed.
- **On failure:**
  - refuse the call with a fixed JSON-RPC error and reason code (`agent_inactive` / `token_revoked`);
  - write an `audit_log` row (`denied`, parameters null), the same shape as `rejectOnMissingLicense`;
  - record the episode;
  - close the session.
- **Session-level checks in `ServeMessages`** (in-memory, about 9 ns):
  - the token's expiry: already ended by the session manager; kept as defence in depth;
  - the in-memory revoked set.
- **Workflow runs** in progress get the same check on every step, because each step goes through `Dispatch`. A run whose governed agent is suspended stops at its next step.

### 4.2 End live sessions on suspend and revoke
- **eami-api:** `UpdateAgent`, in the same statement path as the status change, sends `pg_notify('agent_status', <agent_id>)`. The payload is the UUID only, never a name.
- **Gateway:** a `LISTEN agent_status` listener, following the pattern of `approval_decision` and `policy_reload`. On notify, `SessionManager.CloseByAgent(uuid)` ends every open SSE stream for that governed agent on this node.
- **Token revocation** (the gateway's revoke route): closes local sessions carrying that JTI, and sends `pg_notify('token_revoked', <jti>)`. Every node adds the JTI to its in-memory set and closes matching sessions.
- **Missed notifications:** on every `LISTEN` (re)connect, a node reloads the revoked set from the database and re-checks the status of its open sessions' governed agents. The per-call database check (§4.1) covers the gap regardless.
- **Key revocation → token revocation (founder decision):** optionally, revoking an agent's API key also revokes every unexpired token issued with it. `ai_token_events` maps `api_key_id` → `jti`. Recommended, so that "revoke the key" means what an operator expects.

### 4.3 How revocation reaches other nodes
1. Postgres `NOTIFY`, already in use for approvals and policy reload. Prompt: each node closes sessions within one round trip.
2. Reload on reconnect.
3. The per-call `revoked_ai_tokens` check. Authoritative, so even a node that missed everything refuses the next call.

### 4.4 Measured latency cost on the hot path
- **Per-call status-plus-revocation query:** p50 **1.13 ms**, p95 **1.74 ms**, p99 **2.07 ms** (2000 runs after warm-up). Measured with pgx from the host through Docker Desktop's port forward, so it's an upper bound: container-to-container should be lower. The status-only query costs the same (1.13 ms p50).
- **In-memory revoked-set and expiry checks:** about 9 ns each.
- **For scale:**
  - the gateway's own processing on the denied path was **3–7 ms** in the live test, so the check adds roughly 15–35% to the gateway's own overhead;
  - it is negligible next to any real downstream tool or model call (hundreds of ms or more).
- **A cache is not recommended:** any TTL reopens the window this item closes. If the cost ever matters, a `NOTIFY`-invalidated in-memory status cache with the database check on cache miss is the next step. It would need the same tests.

### 4.5 Tests (real Postgres, throwaway databases, `eami-gateway/internal/testdb`; mutation-proven)

| # | Test | Deliberate breakage it must catch |
|---|---|---|
| T1 | Open session → suspend (database update) → next `tool_call` refused with the fixed code; `audit_log` row `denied`; a fake downstream server receives **0** requests | remove the `Dispatch` status check |
| T2 | Suspend sends `NOTIFY` → the open SSE stream closes within a bound | remove the listener, or `CloseByAgent` |
| T3 | Two gateway instances (two `Manager` + `SessionManager` on one database): revoke a JTI on A → B's open session is refused on the next call and closed; a new session on B is refused | remove the `NOTIFY` publish **and**, separately, the per-call `revoked_ai_tokens` check (each alone must be caught by a sub-case) |
| T4 | Multi-step workflow run; suspend after step 1 → step 2 not dispatched | move the check out of `Dispatch` into the MCP handler only |
| T5 | Token past expiry → message refused, nothing dispatched | remove the session-expiry handling |
| T6 | Reactivate after suspend → a new session works (no over-blocking) | make suspension sticky |
| T7 | Org A and org B have identically named governed agents; suspending A's never touches B's sessions (UUIDs, never names) | key `CloseByAgent` by name |
| T8 | A hostile or malformed `NOTIFY` payload is ignored (UUID parse only) | drop the parse check |
| T9 | Optional (§4.2 decision): revoking a key revokes its live tokens | remove the key-to-token cascade |

**Live re-verification after the build:** rerun §2 with correct `<tool>.<action>` names and a deny policy proven on the baseline. Expected: (b) and (c) refused with no new audit row (or one `denied` row carrying the reason code, per §4.1), and (e) refused on node 2.

**Fold in B-302** (fixed 401/403 messages) in the same change: it touches the same handlers.

**Reviews:** both mandatory; security review required (authentication, revocation, multi-node).
