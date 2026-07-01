-- name: GetAgentConfig :one
SELECT agent_id, scan_interval_seconds, model_scan_paths,
       max_report_size_bytes, enabled_scanners, updated_at
FROM agent_configs
WHERE agent_id = $1;

-- name: UpsertAgentConfig :one
INSERT INTO agent_configs (agent_id, scan_interval_seconds, model_scan_paths,
                           max_report_size_bytes, enabled_scanners, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW())
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
