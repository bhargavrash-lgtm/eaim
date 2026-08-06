package store

import (
	"context"

	"github.com/google/uuid"
)

// ListTools returns all tools for an org ordered by name.
func (q *Queries) ListTools(ctx context.Context, orgID uuid.UUID) ([]GatewayTool, error) {
	const sql = `
SELECT id, org_id, name, type, auth_type, mcp_command, base_url,
       status, last_used, last_tested, created_at, action_paths
FROM gateway_tools
WHERE org_id = $1
ORDER BY name ASC`

	rows, err := q.db.Query(ctx, sql, toPgtypeUUID(orgID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tools []GatewayTool
	for rows.Next() {
		var t GatewayTool
		if err := rows.Scan(
			&t.ID, &t.OrgID, &t.Name, &t.Type, &t.AuthType,
			&t.MCPCommand, &t.BaseURL, &t.Status,
			&t.LastUsed, &t.LastTested, &t.CreatedAt, &t.ActionPaths,
		); err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, rows.Err()
}

// CreateToolParams holds fields for inserting a new tool.
type CreateToolParams struct {
	OrgID      uuid.UUID
	Name       string
	Type       string
	AuthType   string
	MCPCommand *string
	MCPArgs    []string
	BaseURL    *string
	// CredentialsEncrypted is the AES-256-GCM-sealed credentials blob (see
	// internal/toolcreds), or nil if the tool has no secrets to store. Never
	// read back by any query in this file -- GatewayTool has no field for
	// it, so it cannot leak through ListTools/CreateTool's own RETURNING.
	CredentialsEncrypted []byte
	// ActionPaths is the raw JSONB bytes for gateway_tools.action_paths
	// (B-046), or nil for a tool with no per-action mappings.
	ActionPaths []byte
}

// CreateTool inserts a new gateway tool and returns it.
func (q *Queries) CreateTool(ctx context.Context, p CreateToolParams) (GatewayTool, error) {
	const sql = `
INSERT INTO gateway_tools (org_id, name, type, auth_type, mcp_command, mcp_args, base_url, credentials_encrypted, action_paths)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, org_id, name, type, auth_type, mcp_command, base_url,
          status, last_used, last_tested, created_at, action_paths`

	var t GatewayTool
	err := q.db.QueryRow(ctx, sql,
		toPgtypeUUID(p.OrgID), p.Name, p.Type, p.AuthType,
		toPgtypeText(p.MCPCommand), p.MCPArgs, toPgtypeText(p.BaseURL),
		p.CredentialsEncrypted, p.ActionPaths,
	).Scan(
		&t.ID, &t.OrgID, &t.Name, &t.Type, &t.AuthType,
		&t.MCPCommand, &t.BaseURL, &t.Status,
		&t.LastUsed, &t.LastTested, &t.CreatedAt, &t.ActionPaths,
	)
	return t, err
}

// UpdateToolParams holds fields for a partial tool update (B-045). Only
// non-nil/non-empty fields are applied -- a nil field leaves that column
// unchanged (COALESCE), matching UpdateAgent's established convention for
// this exact "optional partial update" shape. CredentialsEncrypted is nil
// when the caller didn't submit new credentials (existing value preserved)
// vs the newly encrypted blob when they did -- the encryption/decision of
// which case applies happens in tools.go's UpdateTool handler, not here.
type UpdateToolParams struct {
	ID                   uuid.UUID
	OrgID                uuid.UUID
	Name                 *string
	MCPCommand           *string
	MCPArgs              []string
	BaseURL              *string
	CredentialsEncrypted []byte
	// ActionPaths, like MCPArgs, is nil to leave action_paths unchanged
	// (COALESCE) and non-nil -- including []byte("{}") to explicitly clear
	// all mappings -- to overwrite it.
	ActionPaths []byte
}

// UpdateTool applies a partial update to an existing tool and returns the
// updated row. type/auth_type are deliberately not editable here -- an
// admin changing the fundamental integration type/auth mechanism is
// closer to "delete and recreate" than a partial edit, matching
// UpdateAgent's own precedent of only exposing operational fields.
func (q *Queries) UpdateTool(ctx context.Context, p UpdateToolParams) (GatewayTool, error) {
	const sql = `
UPDATE gateway_tools SET
    name                  = COALESCE($3, name),
    mcp_command           = COALESCE($4, mcp_command),
    mcp_args              = COALESCE($5, mcp_args),
    base_url              = COALESCE($6, base_url),
    credentials_encrypted = COALESCE($7, credentials_encrypted),
    action_paths          = COALESCE($8, action_paths)
WHERE id = $1 AND org_id = $2
RETURNING id, org_id, name, type, auth_type, mcp_command, base_url,
          status, last_used, last_tested, created_at, action_paths`

	var t GatewayTool
	err := q.db.QueryRow(ctx, sql,
		toPgtypeUUID(p.ID), toPgtypeUUID(p.OrgID),
		toPgtypeText(p.Name), toPgtypeText(p.MCPCommand), p.MCPArgs, toPgtypeText(p.BaseURL),
		p.CredentialsEncrypted, p.ActionPaths,
	).Scan(
		&t.ID, &t.OrgID, &t.Name, &t.Type, &t.AuthType,
		&t.MCPCommand, &t.BaseURL, &t.Status,
		&t.LastUsed, &t.LastTested, &t.CreatedAt, &t.ActionPaths,
	)
	return t, err
}

// DeleteTool removes a tool by ID scoped to an org. Returns false if not found.
func (q *Queries) DeleteTool(ctx context.Context, orgID, toolID uuid.UUID) (bool, error) {
	const sql = `DELETE FROM gateway_tools WHERE id = $1 AND org_id = $2`
	tag, err := q.db.Exec(ctx, sql, toPgtypeUUID(toolID), toPgtypeUUID(orgID))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// MarkToolTested updates last_tested and status for a tool.
func (q *Queries) MarkToolTested(ctx context.Context, orgID, toolID uuid.UUID, status string, latencyMs int) error {
	const sql = `
UPDATE gateway_tools
SET last_tested = NOW(), status = $3, test_latency_ms = $4
WHERE id = $1 AND org_id = $2`
	_, err := q.db.Exec(ctx, sql,
		toPgtypeUUID(toolID), toPgtypeUUID(orgID), status, latencyMs)
	return err
}
