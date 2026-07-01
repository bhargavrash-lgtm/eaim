-- Migration 006: agent configuration table
-- Stores per-agent scanner and reporting configuration.
-- A default row is created automatically when a gateway_agents row is inserted.

CREATE TABLE IF NOT EXISTS agent_configs (
    agent_id              UUID PRIMARY KEY REFERENCES gateway_agents(id) ON DELETE CASCADE,
    scan_interval_seconds INT          NOT NULL DEFAULT 300,
    model_scan_paths      TEXT[]       NOT NULL DEFAULT ARRAY['/home', '/Users', 'C:\\Users'],
    max_report_size_bytes INT          NOT NULL DEFAULT 5242880,
    enabled_scanners      TEXT[]       NOT NULL DEFAULT ARRAY['ai_apps','models','mcp_servers','cloud_clients','network_activity','browser'],
    updated_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Automatically seed a default config row for every new agent.
CREATE OR REPLACE FUNCTION create_default_agent_config()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO agent_configs (agent_id) VALUES (NEW.id)
    ON CONFLICT (agent_id) DO NOTHING;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_agent_configs_default
AFTER INSERT ON gateway_agents
FOR EACH ROW EXECUTE FUNCTION create_default_agent_config();
