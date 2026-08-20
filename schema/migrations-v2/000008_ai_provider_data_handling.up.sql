-- Data-handling visibility for AI Provider connectors (B-078): a
-- customer's actual data-retention agreement with a third-party provider
-- (Claude, Gemini, OpenAI, ...) is governed by their own commercial
-- contract, not by anything EAMI's code can enforce -- dispatched data
-- leaves EAMI's control the moment it reaches the provider. This is a
-- VISIBILITY feature only, mirroring audit_mode's exact precedent
-- (migration 000004): a structured designation for filtering/alerting,
-- plus free text for the actual agreement citation an admin can't fit
-- into an enum.
--
-- data_handling_designation defaults to 'unknown', not a specific
-- retention posture -- a newly created connector must never silently
-- imply a data-handling agreement nobody has actually confirmed. This is
-- a stricter fail-safe than audit_mode's own default ('structural_
-- metadata_only' is itself already the safe choice for that field);
-- here, the safe choice is "flag as unconfirmed," since the *contents*
-- of "unknown" vs. any real designation could not be more different for
-- a compliance reviewer.
ALTER TABLE gateway_tools ADD COLUMN IF NOT EXISTS data_handling_designation TEXT
    NOT NULL DEFAULT 'unknown'
    CHECK (data_handling_designation IN ('zero_retention','standard_retention','unknown'));

ALTER TABLE gateway_tools ADD COLUMN IF NOT EXISTS data_handling_note TEXT;

-- audit_log's own copy is a per-call SNAPSHOT of the connector's
-- designation at dispatch time, not a live-validated field -- so it's
-- deliberately nullable/unconstrained here (no NOT NULL, no CHECK): a
-- non-ai_provider dispatch (rest_api, mcp, database) never has one to
-- record, and every audit_log row written before this migration has no
-- value to backfill (backfilling a real "unknown" would misrepresent
-- history no admin ever configured). NOT part of the hash-chain formula
-- (audit.Writer.Write, eami-gateway) -- confirmed directly against that
-- code before this migration was written: the hash covers prevHash, id,
-- org_id, agent_name, tool_name, action, decision, and timestamp only,
-- the same set of columns policy_id/approval_id/parameters already sit
-- outside of. Adding this column changes nothing about tamper-evidence.
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS data_handling_designation TEXT;
