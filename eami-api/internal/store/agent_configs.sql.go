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
	// ModelFileSizeMB is the models scanner's minimum file size (B-293,
	// migration 000027). Default 100, the agent's own built-in default.
	ModelFileSizeMB int32
}

// AllScanners is every scanner name eami-agent's payload.Build gates on
// (det.IsEnabled), in the column default's order. It is the only valid set
// for enabled_scanners, and the default: B-271 found the old 6-name default
// silently disabling ai_processes, gpu, python_envs and nodejs_ai on every
// linked endpoint, because the agent treats a non-empty list as an
// allow-list. Keep in sync with migration 000025's column default and the
// UI's VALID_SCANNERS (AgentConfigPanel.tsx).
var AllScanners = []string{
	"ai_apps", "models", "mcp_servers", "cloud_clients", "network_activity", "browser",
	"ai_processes", "gpu", "python_envs", "nodejs_ai",
}

// IsKnownScanner reports whether name is one of AllScanners (exact,
// case-sensitive: the agent matches names exactly).
func IsKnownScanner(name string) bool {
	for _, s := range AllScanners {
		if s == name {
			return true
		}
	}
	return false
}

// AgentConfigDefaults are the server-side defaults (match migration).
var AgentConfigDefaults = AgentConfig{
	ScanIntervalSeconds: 300,
	ModelScanPaths:      []string{"/home", "/Users", `C:\Users`},
	MaxReportSizeBytes:  5242880,
	EnabledScanners:     append([]string(nil), AllScanners...),
	ModelFileSizeMB:     100,
}

const getAgentConfigSQL = `
SELECT agent_id, scan_interval_seconds, model_scan_paths,
       max_report_size_bytes, enabled_scanners, updated_at, model_file_size_mb
FROM agent_configs
WHERE agent_id = $1`

// GetAgentConfig fetches the config for the given agent.
// Returns pgx.ErrNoRows when not found.
func (q *Queries) GetAgentConfig(ctx context.Context, agentID uuid.UUID) (*AgentConfig, error) {
	row := q.db.QueryRow(ctx, getAgentConfigSQL, toPgtypeUUID(agentID))
	var c AgentConfig
	var id [16]byte
	err := row.Scan(&id, &c.ScanIntervalSeconds, &c.ModelScanPaths,
		&c.MaxReportSizeBytes, &c.EnabledScanners, &c.UpdatedAt, &c.ModelFileSizeMB)
	if err != nil {
		return nil, err
	}
	c.AgentID = uuid.UUID(id)
	return &c, nil
}

// UpsertAgentConfigParams holds the fields for insert-or-update. OrgID scopes
// the write: agent_configs has no org_id column of its own, so ownership is
// enforced through gateway_agents inside the same statement (B-232).
type UpsertAgentConfigParams struct {
	OrgID               uuid.UUID
	AgentID             uuid.UUID
	ScanIntervalSeconds int32
	ModelScanPaths      []string
	MaxReportSizeBytes  int32
	EnabledScanners     []string
	ModelFileSizeMB     int32
}

const upsertAgentConfigSQL = `
INSERT INTO agent_configs (agent_id, scan_interval_seconds, model_scan_paths,
                           max_report_size_bytes, enabled_scanners, model_file_size_mb, updated_at)
SELECT $1, $2, $3, $4, $5, $7, NOW()
FROM gateway_agents
WHERE id = $1 AND org_id = $6
ON CONFLICT (agent_id) DO UPDATE SET
    scan_interval_seconds = EXCLUDED.scan_interval_seconds,
    model_scan_paths      = EXCLUDED.model_scan_paths,
    max_report_size_bytes = EXCLUDED.max_report_size_bytes,
    enabled_scanners      = EXCLUDED.enabled_scanners,
    model_file_size_mb    = EXCLUDED.model_file_size_mb,
    updated_at            = NOW()
RETURNING agent_id, scan_interval_seconds, model_scan_paths,
          max_report_size_bytes, enabled_scanners, updated_at, model_file_size_mb`

// UpsertAgentConfig creates or fully replaces an agent's config row, but only
// when the agent belongs to p.OrgID. For any other org's agent the INSERT's
// SELECT yields no row, nothing is written, and pgx.ErrNoRows is returned.
func (q *Queries) UpsertAgentConfig(ctx context.Context, p UpsertAgentConfigParams) (*AgentConfig, error) {
	row := q.db.QueryRow(ctx, upsertAgentConfigSQL,
		toPgtypeUUID(p.AgentID),
		p.ScanIntervalSeconds,
		p.ModelScanPaths,
		p.MaxReportSizeBytes,
		p.EnabledScanners,
		toPgtypeUUID(p.OrgID),
		p.ModelFileSizeMB,
	)
	var c AgentConfig
	var id [16]byte
	err := row.Scan(&id, &c.ScanIntervalSeconds, &c.ModelScanPaths,
		&c.MaxReportSizeBytes, &c.EnabledScanners, &c.UpdatedAt, &c.ModelFileSizeMB)
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
