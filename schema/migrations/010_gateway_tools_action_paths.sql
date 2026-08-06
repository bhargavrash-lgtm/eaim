-- Migration: 010_gateway_tools_action_paths
-- Date: 2026-08-06
-- Author: Code-EAMI
-- Description: Add gateway_tools.action_paths (B-046) -- per-action
--              path/method mappings for rest_api-type tools, so
--              toolrouter.Forward can route each action to its real
--              specific REST endpoint instead of always POSTing to the
--              tool's flat base_url (B-044's original minimum-viable
--              behavior). Additive only: NULL/absent means "no mappings
--              defined", which is exactly B-044's existing flat-base_url
--              behavior, unchanged -- no existing tool is affected until
--              an admin explicitly defines mappings for it.
--
--              Shape: {"<action_name>": {"path": "/contacts", "method": "POST"}}
--              JSONB chosen over a new table -- matches this schema's own
--              established convention for small structured per-row config
--              (alert_rules.condition_config, notification_config.config,
--              episodes.steps all use the same approach) and avoids a
--              join on every dispatch for data that's always read as one
--              small whole per tool.

ALTER TABLE gateway_tools
    ADD COLUMN action_paths JSONB; -- NULL = no mappings, B-044's flat base_url behavior
