-- Gate AI-agent token issuance with scoped API keys (B-098).
--
-- api_keys was org-scoped only -- no link to a specific gateway_agents row,
-- so it could not gate POST /v1/gateway/tokens (which mints a credential
-- for one specific agent). agent_id is nullable: an org-scoped-only key
-- (today's only real usage, e.g. collector ingest) stays valid without
-- being agent-scoped; only a key with agent_id set can authorize gateway
-- token issuance, enforced in application code
-- (eami-gateway/internal/identity/apikey.go), not by a NOT NULL here.
-- ON DELETE SET NULL, not CASCADE: deleting the agent shouldn't silently
-- delete/orphan the key row itself, just its scoping.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS agent_id UUID REFERENCES gateway_agents(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_api_keys_agent ON api_keys(agent_id) WHERE agent_id IS NOT NULL;

-- AI-token issuance/revocation audit trail (B-098) -- a new, small,
-- purpose-built table, deliberately NOT agent_lifecycle_events (B-087):
-- that table's own doc comment scopes it to "who did what to which
-- gateway_agents row" admin CRUD history with a performed_by that assumes
-- a human session; a machine-presented API key issuing/revoking a JWT is a
-- different concern with no human actor. Same reasoning that already kept
-- agent_lifecycle_events out of audit_log (a hash-chained dispatch-decision
-- ledger) applies again here, one level down.
--
-- agent_id/agent_name/api_key_id are snapshot columns with NO foreign key,
-- mirroring agent_lifecycle_events' documented reasoning exactly: the
-- point is that an issuance/revocation record survives the agent being
-- deleted or the key being revoked, not that it stays live-joinable.
CREATE TABLE IF NOT EXISTS ai_token_events (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    agent_id     UUID,
    agent_name   TEXT NOT NULL,
    api_key_id   UUID,
    jti          TEXT NOT NULL,
    event_type   TEXT NOT NULL CHECK (event_type IN ('issued','revoked')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_ai_token_events_org_agent ON ai_token_events(org_id, agent_id, created_at DESC);
