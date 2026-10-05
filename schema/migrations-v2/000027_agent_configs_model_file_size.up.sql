-- B-293 (master-sequence item 8a): the agent applies remote config correctly.
--
-- 1. model_file_size_mb becomes remotely settable (founder decision D-f). The
--    default, 100, matches the agent's built-in default (config.defaults()),
--    so adding the column changes no endpoint's behaviour. The range matches
--    the API's PUT validation and the agent's own bounds check.
-- 2. enabled_scanners: an empty array now means "no scanners" (decision D-a),
--    where the agent used to read it as "all scanners". No writer could
--    produce one before this change (the API rejected []), but any row that
--    holds one keeps its old meaning by becoming the full list, so no
--    endpoint silently stops scanning. Keep the list in sync with
--    store.AllScanners and the agent's config.AllScanners.

ALTER TABLE agent_configs
    ADD COLUMN IF NOT EXISTS model_file_size_mb INT NOT NULL DEFAULT 100
        CONSTRAINT agent_configs_model_file_size_mb_range CHECK (model_file_size_mb BETWEEN 1 AND 100000);

UPDATE agent_configs
SET enabled_scanners = ARRAY[
        'ai_apps','models','mcp_servers','cloud_clients','network_activity','browser',
        'ai_processes','gpu','python_envs','nodejs_ai'
    ],
    updated_at = NOW()
WHERE cardinality(enabled_scanners) = 0;
