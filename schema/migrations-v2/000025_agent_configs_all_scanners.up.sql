-- B-271: linking an endpoint silently disabled 4 of 10 scanners.
--
-- The agent treats a non-empty enabled_scanners list as an allow-list, and
-- the column default listed only 6 of the 10 scanners it gates on. Every
-- governed agent's trigger-created row therefore disabled ai_processes, gpu,
-- python_envs and nodejs_ai on any endpoint linked to it (confirmed live:
-- gpus appeared in only 10 of 3,486 reports from the real linked endpoint).
--
-- 1. The default becomes all 10 (keep in sync with store.AllScanners and the
--    UI's VALID_SCANNERS).
-- 2. Backfill (founder decision D1): every existing row gains whichever of the
--    4 missing names it lacks. No UI ever offered them, so no admin could have
--    excluded them deliberately. Every other choice in a row is kept, including
--    the demo endpoint's B-194 exclusion of "models" (D2). An empty array
--    means "all scanners" to the agent, so it is left alone rather than
--    narrowed to a 4-name allow-list (no writer produces one today).

ALTER TABLE agent_configs
    ALTER COLUMN enabled_scanners SET DEFAULT ARRAY[
        'ai_apps','models','mcp_servers','cloud_clients','network_activity','browser',
        'ai_processes','gpu','python_envs','nodejs_ai'
    ];

UPDATE agent_configs
SET enabled_scanners = enabled_scanners || ARRAY(
        SELECT s
        FROM unnest(ARRAY['ai_processes','gpu','python_envs','nodejs_ai']) WITH ORDINALITY AS t(s, n)
        WHERE NOT (s = ANY (enabled_scanners))
        ORDER BY n
    ),
    updated_at = NOW()
WHERE cardinality(enabled_scanners) > 0
  AND NOT (enabled_scanners @> ARRAY['ai_processes','gpu','python_envs','nodejs_ai']);
