-- B-269 Slice 0 (B-277 path rules, B-194 file-type filter).
--
-- 1. New agents no longer default to walking every user profile (decision
--    S4). The old default ('/home', '/Users', 'C:\\Users') is the B-194
--    over-collection default; existing rows keep their paths (they stay
--    accepted and are flagged by the API's path_warnings), and cleaning them
--    is B-296. Matches store.AgentConfigDefaults.
-- 2. endpoint_model_files.source accepts the values agents actually send:
--    'gpt4all' (always sent, previously stored as 'unknown') and the new
--    'scan_path' (a hit under a configured model_scan_paths entry, decision
--    S2). The API also maps the agent's 'lm_studio' onto 'lmstudio'.

ALTER TABLE agent_configs ALTER COLUMN model_scan_paths SET DEFAULT '{}';

-- NOT VALID: the new list only widens the old one, so every existing row
-- already conforms; skipping the re-check avoids holding an exclusive lock
-- for a full table scan. New and updated rows are still checked.
ALTER TABLE endpoint_model_files DROP CONSTRAINT IF EXISTS endpoint_model_files_source_check;
ALTER TABLE endpoint_model_files ADD CONSTRAINT endpoint_model_files_source_check
    CHECK (source IN ('ollama', 'lmstudio', 'huggingface', 'unknown', 'gpt4all', 'scan_path')) NOT VALID;
