-- B-093: let an audit_log row indicate which multi-hop workflow run/step
-- (if any) it was part of. workflow_run_steps (000007) has no FK to/from
-- audit_log in either direction -- today there is no way, from an
-- audit_log row, to tell it was part of a workflow run at all.
--
-- Nullable, no FK -- matches audit_log's own existing convention exactly
-- (policy_id/approval_id/data_handling_designation are all plain,
-- FK-less columns on this table already; see 000008's identical
-- ADD COLUMN IF NOT EXISTS ... TEXT pattern for data_handling_designation).
-- Deliberately excluded from the hash-chain formula (internal/audit/
-- writer.go's Write) -- same B-078 DataHandling precedent, confirmed
-- directly against that code before this migration was written, not
-- assumed.
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS workflow_run_id UUID;
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS step_index INTEGER;
