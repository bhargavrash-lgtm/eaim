-- Migration: 009_tool_server_routing
-- Date: 2026-08-06
-- Author: Code-EAMI
-- Description: Add policy_conditions.tool_server_ids (B-044) -- lets a
--              policy rule pin to a specific, immutable resolved
--              gateway_tools.id, in addition to the existing by-name
--              tool_names match. Additive only: no existing column
--              changed, gateway_tools itself untouched.

ALTER TABLE policy_conditions
    ADD COLUMN tool_server_ids TEXT[]; -- empty/NULL = match any (including unresolved calls)
