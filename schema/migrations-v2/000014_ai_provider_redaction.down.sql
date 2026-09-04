ALTER TABLE audit_log DROP COLUMN IF EXISTS redacted_count;
ALTER TABLE gateway_tools DROP COLUMN IF EXISTS redaction_rules;
