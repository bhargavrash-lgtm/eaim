package store

import (
	"context"

	"github.com/google/uuid"
)

// ListTools returns all tools for an org ordered by name.
func (q *Queries) ListTools(ctx context.Context, orgID uuid.UUID) ([]GatewayTool, error) {
	const sql = `
SELECT id, org_id, name, type, auth_type, mcp_command, base_url,
       status, last_used, last_tested, created_at
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
			&t.LastUsed, &t.LastTested, &t.CreatedAt,
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
}

// CreateTool inserts a new gateway tool and returns it.
func (q *Queries) CreateTool(ctx context.Context, p CreateToolParams) (GatewayTool, error) {
	const sql = `
INSERT INTO gateway_tools (org_id, name, type, auth_type, mcp_command, mcp_args, base_url)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, org_id, name, type, auth_type, mcp_command, base_url,
          status, last_used, last_tested, created_at`

	var t GatewayTool
	err := q.db.QueryRow(ctx, sql,
		toPgtypeUUID(p.OrgID), p.Name, p.Type, p.AuthType,
		toPgtypeText(p.MCPCommand), p.MCPArgs, toPgtypeText(p.BaseURL),
	).Scan(
		&t.ID, &t.OrgID, &t.Name, &t.Type, &t.AuthType,
		&t.MCPCommand, &t.BaseURL, &t.Status,
		&t.LastUsed, &t.LastTested, &t.CreatedAt,
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
