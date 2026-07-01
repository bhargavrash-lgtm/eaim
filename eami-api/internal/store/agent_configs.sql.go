// Hand-written store methods for agent_configs table.
// Requires migration 006_agent_configs.sql.
package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// AgentConfig mirrors the agent_configs table.
type AgentConfig struct {
	AgentID             uuid.UUID
	ScanIntervalSeconds int32
	ModelScanPaths      []string
	MaxReportSizeBytes  int32
	EnabledScanners     []string
	UpdatedAt           time.Time
}

// AgentConfigDefaults are the server-side defaults (match migration).
var AgentConfigDefaults = AgentConfig{
	ScanIntervalSeconds: 300,
	ModelScanPaths:      []string{"/home", "/Users", `C:\Users`},
	MaxReportSizeBytes:  5242880,
	EnabledScanners:     []string{"ai_apps", "models", "mcp_servers", "cloud_clients", "network_activity", "browser"},
}

const getAgentConfigSQL = `
SELECT agent_id, scan_interval_seconds, model_scan_paths,
       max_report_size_bytes, enabled_scanners, updated_at
FROM agent_configs
WHERE agent_id = $1`

// GetAgentConfig fetches the config for the given agent.
// Returns pgx.ErrNoRows when not found.
func (q *Queries) GetAgentConfig(ctx context.Context, agentID uuid.UUID) (*AgentConfig, error) {
	row := q.db.QueryRow(ctx, getAgentConfigSQL, toPgtypeUUID(agentID))
	var c AgentConfig
	var id [16]byte
	err := row.Scan(&id, &c.ScanIntervalSeconds, &c.ModelScanPaths,
		&c.MaxReportSizeBytes, &c.EnabledScanners, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.AgentID = uuid.UUID(id)
	return &c, nil
}

// UpsertAgentConfigParams holds the fields for insert-or-update.
type UpsertAgentConfigParams struct {
	AgentID             uuid.UUID
	ScanIntervalSeconds int32
	ModelScanPaths      []string
	MaxReportSizeBytes  int32
	EnabledScanners     []string
}

const upsertAgentConfigSQL = `
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
          max_report_size_bytes, enabled_scanners, updated_at`

// UpsertAgentConfig creates or fully replaces an agent's config row.
func (q *Queries) UpsertAgentConfig(ctx context.Context, p UpsertAgentConfigParams) (*AgentConfig, error) {
	row := q.db.QueryRow(ctx, upsertAgentConfigSQL,
		toPgtypeUUID(p.AgentID),
		p.ScanIntervalSeconds,
		p.ModelScanPaths,
		p.MaxReportSizeBytes,
		p.EnabledScanners,
	)
	var c AgentConfig
	var id [16]byte
	err := row.Scan(&id, &c.ScanIntervalSeconds, &c.ModelScanPaths,
		&c.MaxReportSizeBytes, &c.EnabledScanners, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.AgentID = uuid.UUID(id)
	return &c, nil
}

const seedAgentConfigSQL = `
INSERT INTO agent_configs (agent_id) VALUES ($1)
ON CONFLICT (agent_id) DO NOTHING`

// SeedAgentConfig inserts a default config row for a new agent.
// No-op if a row already exists. Called from CreateAgent handler.
func (q *Queries) SeedAgentConfig(ctx context.Context, agentID uuid.UUID) error {
	_, err := q.db.Exec(ctx, seedAgentConfigSQL, toPgtypeUUID(agentID))
	return err
}
