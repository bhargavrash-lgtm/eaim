package store

import (
	"context"

	"github.com/google/uuid"
)

// ListNodes returns all gateway nodes for an org with their latest metrics.
func (q *Queries) ListNodes(ctx context.Context, orgID uuid.UUID) ([]GatewayNode, error) {
	const sql = `
SELECT n.id, n.org_id, n.name, n.role, n.status, n.address,
       n.hostname, n.version, n.last_heartbeat,
       m.cpu_pct, m.requests_per_min
FROM gateway_nodes n
LEFT JOIN LATERAL (
    SELECT cpu_pct, requests_per_min
    FROM gateway_node_metrics
    WHERE node_id = n.id
    ORDER BY recorded_at DESC
    LIMIT 1
) m ON true
WHERE n.org_id = $1
ORDER BY n.name ASC`

	rows, err := q.db.Query(ctx, sql, toPgtypeUUID(orgID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []GatewayNode
	for rows.Next() {
		var n GatewayNode
		if err := rows.Scan(
			&n.ID, &n.OrgID, &n.Name, &n.Role, &n.Status, &n.Address,
			&n.Hostname, &n.Version, &n.LastHeartbeat,
			&n.CPUPct, &n.RequestsPerMin,
		); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// DeleteNode removes a node by ID scoped to an org. Returns false if not found.
func (q *Queries) DeleteNode(ctx context.Context, orgID, nodeID uuid.UUID) (bool, error) {
	const sql = `DELETE FROM gateway_nodes WHERE id = $1 AND org_id = $2`
	tag, err := q.db.Exec(ctx, sql, toPgtypeUUID(nodeID), toPgtypeUUID(orgID))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
