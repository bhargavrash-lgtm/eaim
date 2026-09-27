-- name: GetAgentConfig :one
SELECT agent_id, scan_interval_seconds, model_scan_paths,
       max_report_size_bytes, enabled_scanners, updated_at
FROM agent_configs
WHERE agent_id = $1;

-- name: UpsertAgentConfig :one
-- B-232: org-scoped through gateway_agents (agent_configs has no org_id).
-- A foreign org's agent yields no SELECT row, so nothing is written and no
-- row is returned. Keep in sync with the hand-maintained agent_configs.sql.go.
INSERT INTO agent_configs (agent_id, scan_interval_seconds, model_scan_paths,
                           max_report_size_bytes, enabled_scanners, updated_at)
SELECT $1, $2, $3, $4, $5, NOW()
FROM gateway_agents
WHERE id = $1 AND org_id = $6
ON CONFLICT (agent_id) DO UPDATE SET
    scan_interval_seconds = EXCLUDED.scan_interval_seconds,
    model_scan_paths      = EXCLUDED.model_scan_paths,
    max_report_size_bytes = EXCLUDED.max_report_size_bytes,
    enabled_scanners      = EXCLUDED.enabled_scanners,
    updated_at            = NOW()
RETURNING agent_id, scan_interval_seconds, model_scan_paths,
          max_report_size_bytes, enabled_scanners, updated_at;

-- name: SeedAgentConfig :exec
INSERT INTO agent_configs (agent_id)
VALUES ($1)
ON CONFLICT (agent_id) DO NOTHING;
