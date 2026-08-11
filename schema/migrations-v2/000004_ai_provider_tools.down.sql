ALTER TABLE gateway_tools DROP COLUMN IF EXISTS audit_mode;
ALTER TABLE gateway_tools DROP COLUMN IF EXISTS provider;

ALTER TABLE gateway_tools DROP CONSTRAINT IF EXISTS gateway_tools_type_check;
ALTER TABLE gateway_tools ADD CONSTRAINT gateway_tools_type_check
    CHECK (type IN ('mcp','rest_api','database'));
