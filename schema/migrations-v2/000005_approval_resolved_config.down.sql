ALTER TABLE approval_requests DROP COLUMN IF EXISTS resume_outcome;
ALTER TABLE approval_requests DROP COLUMN IF EXISTS resolved_config_hash;
ALTER TABLE approval_requests DROP COLUMN IF EXISTS resolved_tool_id;
