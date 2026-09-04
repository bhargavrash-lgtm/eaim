DROP INDEX IF EXISTS idx_endpoints_gateway_agent;
ALTER TABLE endpoints DROP COLUMN IF EXISTS gateway_agent_id;
