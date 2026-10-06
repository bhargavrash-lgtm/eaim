-- Rollback for B-269 Slice 0. Rows using the two new source values are
-- relabelled 'unknown' first (what the old constraint would have stored),
-- so the old CHECK can be restored. That relabelling is permanent: a later
-- re-apply can't recover which rows were gpt4all or scan_path. The column
-- default goes back to the old whole-profile paths.
-- Roll back the API binary FIRST: a newer API still running against this
-- schema would fail inserts of gpt4all / scan_path rows.
UPDATE endpoint_model_files SET source = 'unknown' WHERE source IN ('gpt4all', 'scan_path');
ALTER TABLE endpoint_model_files DROP CONSTRAINT IF EXISTS endpoint_model_files_source_check;
ALTER TABLE endpoint_model_files ADD CONSTRAINT endpoint_model_files_source_check
    CHECK (source IN ('ollama', 'lmstudio', 'huggingface', 'unknown'));

ALTER TABLE agent_configs ALTER COLUMN model_scan_paths SET DEFAULT ARRAY['/home', '/Users', 'C:\\Users'];
