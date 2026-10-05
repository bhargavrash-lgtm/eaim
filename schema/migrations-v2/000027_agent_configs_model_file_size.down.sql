-- Rollback for B-293: drops the remotely settable model_file_size_mb. Agents
-- then fall back to their local value (default 100, the same as the column
-- default), so a rollback changes no endpoint's minimum model size unless an
-- admin had set a different one remotely.
--
-- The enabled_scanners backfill is deliberately not undone: it replaced '{}'
-- (which older agents read as "all scanners") with the full list, which
-- means the same thing to every agent version.
ALTER TABLE agent_configs DROP COLUMN IF EXISTS model_file_size_mb;
