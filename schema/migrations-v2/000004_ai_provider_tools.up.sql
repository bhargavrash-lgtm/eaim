-- AI Provider Connector (Thread A Model 1): a fourth gateway_tools type,
-- ai_provider, dispatching to an external AI provider (Claude first) as a
-- named tool via the same MCP tool_call path every other type already uses.
--
-- provider is deliberately NOT constrained by a CHECK enum here -- the set
-- of real providers is validated at the application layer (eami-api's
-- CreateTool/UpdateTool handlers) against a small allowlist that grows by
-- adding one entry + one gateway adapter, not by another migration every
-- time a provider is added. Nullable: meaningless for the other 3 types.
--
-- audit_mode governs only the audit_log write for this connector's calls
-- (never approval_requests, never episodes -- both intentionally keep
-- showing full parameters, unchanged). 'structural_metadata_only' is the
-- fail-safe default: a newly created connector never logs raw prompt
-- content until an admin explicitly opts into 'full'. A TEXT enum (not a
-- boolean) so a future 'redacted'/tokenized mode is a value addition, not a
-- column-type change -- deliberately not added to the CHECK yet, since
-- accepting it before that subsystem exists would let someone select a
-- mode nothing actually implements.
ALTER TABLE gateway_tools DROP CONSTRAINT IF EXISTS gateway_tools_type_check;
ALTER TABLE gateway_tools ADD CONSTRAINT gateway_tools_type_check
    CHECK (type IN ('mcp','rest_api','database','ai_provider'));

ALTER TABLE gateway_tools ADD COLUMN IF NOT EXISTS provider TEXT;

ALTER TABLE gateway_tools ADD COLUMN IF NOT EXISTS audit_mode TEXT
    NOT NULL DEFAULT 'structural_metadata_only'
    CHECK (audit_mode IN ('full','structural_metadata_only'));
