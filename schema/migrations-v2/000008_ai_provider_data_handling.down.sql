ALTER TABLE audit_log DROP COLUMN IF EXISTS data_handling_designation;

ALTER TABLE gateway_tools DROP COLUMN IF EXISTS data_handling_note;
ALTER TABLE gateway_tools DROP COLUMN IF EXISTS data_handling_designation;
