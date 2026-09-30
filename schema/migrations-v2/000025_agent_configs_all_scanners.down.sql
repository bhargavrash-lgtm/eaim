-- Reverts only the column default. The backfill is deliberately not undone:
-- stripping ai_processes, gpu, python_envs and nodejs_ai from existing rows
-- would reintroduce B-271's silent scanner disable, and an admin may since
-- have chosen them explicitly.
ALTER TABLE agent_configs
    ALTER COLUMN enabled_scanners SET DEFAULT ARRAY[
        'ai_apps','models','mcp_servers','cloud_clients','network_activity','browser'
    ];
