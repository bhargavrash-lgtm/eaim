# Agent identity: design record

**B-ID:** **B-300** (epic). **Status:** design record only, **parked (not scheduled)**. It is not in the active sequence (`AI_ITAM_EPIC_MASTER_SEQUENCE.md`, "Parked (not scheduled)").
**Recorded:** 2026-10-06 by Claude Code, from the founder's brief. The design below is saved as given.
**Roadmap:** Horizon 1 (`rheoARC_Roadmap_Enterprise_AI_Platform.md`), next to B-138 (SSO/IdP) and B-231 (step-up authentication). Agent identity is not itself a numbered roadmap item.
**Related:** B-138 (SSO/IdP), B-243 (global service key), B-231 (step-up), B-230 (status transitions), B-255 (agent edit surface), B-295 (discovery-agent identity), B-269 Slice 0b (admin audit trail).
**Part A investigation:** `AGENT_IDENTITY_PART_A_INVESTIGATION.md`.

---

## 1. DECISIONS (founder, 2026-10-06)
- Federate with the customer's IdP (Okta, Entra, any OIDC provider) and keep a built-in fallback issuer. We are not a general IdP.
- Customers with no IdP (startups) must be supported, so the fallback issuer exists at launch.
- Both modes are supported: autonomous and delegated. Every governed agent is tagged to an accountable human owner for audit, maintenance and troubleshooting.
- First-customer floor: federation plus owner, expiry and revocation. The proposed order puts accountability first (see section 8).
- Interpretation, founder to confirm: the owner tag is mandatory in both modes; delegation means a call carries a specific user's identity and the agent never exceeds that user's authority; an autonomous agent uses its own scope with its owner accountable.

## 2. TERMINOLOGY
Three kinds of identity: governed agent (an AI workload), discovery agent (software on a machine, B-295), human user. Docs and briefs use these words and never "agent" alone.

## 3. PRINCIPLES
- The registry, owner, risk tier, policy at dispatch and lineage stay ours. Identity is federated inward.
- Authentication proves who; policy at dispatch decides what. A valid credential can be misused (prompt injection), so enforcement does not depend on authentication alone.
- Credentials are short-lived. Static keys are bootstrap secrets only.
- Delegated authority is the intersection of the agent's scope and the user's rights, never the union.
- Secrets never appear in audit, logs or reports (standing checks).
- Federation must work for on-prem and air-gapped customers: no required outbound calls to fetch signing keys; support pinned or cached keys.

## 4. MODEL (indicative; Part A confirms)
- Owner is a link to a real user account, required. Backup owner. Review date and expiry. Mode: autonomous or delegated. Credential type: built-in or federated.
- External binding, nullable: issuer and the claim mapping that ties an IdP token to this governed agent.

## 5. LIFECYCLE
- Registration requires an owner. Owner leaves or is deactivated: the agent is flagged, then auto-suspended after a grace period. Periodic recertification: the owner confirms the agent is still needed. Expiry suspends. Fix B-230 so revoked cannot return.

## 6. CREDENTIALS AND AUTHENTICATION
- Built-in issuer: key expiry, rotation with overlap, last-used tracking, short token lifetime; suspend and revoke must invalidate live tokens and sessions immediately.
- Federation: accept an IdP-issued token mapped to a governed agent by claims. Discovery reconciles agent principals found in the customer's IdP against the registry, so an IdP agent with no governed record is a shadow-AI finding.

## 7. DELEGATION AND AUDIT
- A delegated call carries user context. Policy evaluates the intersection. Audit records the agent, the user and the owner.
- No-IdP customers: the user is a rheoARC user. IdP customers: the user token comes from the IdP, which depends on B-138 (SSO).
- Admin actions (key created, rotated or revoked, owner changed) go through the admin audit trail (Slice 0b). Token issuance and failed authentication are high-volume and are NOT admin actions; where they are recorded is an open question, not decided here.

## 8. PROPOSED SLICES (not scheduled)
- A: accountability and lifecycle (owner as a user link, expiry, revocation, key rotation, live-token invalidation). Serves every customer.
- B: delegation on the built-in issuer.
- C: federation, with the user side depending on B-138.

## 9. DEFERRED
Workload attestation, mTLS or DPoP binding, SPIFFE, SCIM for agents, agent-to-agent authentication, multi-org. Not a general IdP.
