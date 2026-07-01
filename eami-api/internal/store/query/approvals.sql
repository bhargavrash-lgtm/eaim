-- name: ListApprovals :many
SELECT
    id, org_id, agent_id, agent_name, tool_name, action, parameters,
    justification, risk_level, estimated_records, reversible, environment,
    data_types, policy_id, status, approved_by, decision_reason,
    decided_at, expires_at, created_at, gateway_session_id, gateway_node_address
FROM approval_requests
WHERE org_id = $1
  AND ($2::text IS NULL OR status = $2)
  AND ($3::uuid IS NULL OR agent_id = $3)
  AND ($4::timestamptz IS NULL OR created_at >= $4)
  AND ($5::timestamptz IS NULL OR created_at <= $5)
ORDER BY created_at DESC
LIMIT $6 OFFSET $7;

-- name: CountApprovals :one
SELECT COUNT(*) FROM approval_requests
WHERE org_id = $1
  AND ($2::text IS NULL OR status = $2)
  AND ($3::uuid IS NULL OR agent_id = $3)
  AND ($4::timestamptz IS NULL OR created_at >= $4)
  AND ($5::timestamptz IS NULL OR created_at <= $5);

-- name: GetApproval :one
SELECT
    id, org_id, agent_id, agent_name, tool_name, action, parameters,
    justification, risk_level, estimated_records, reversible, environment,
    data_types, policy_id, status, approved_by, decision_reason,
    decided_at, expires_at, created_at, gateway_session_id, gateway_node_address
FROM approval_requests
WHERE id = $1 AND org_id = $2
LIMIT 1;

-- name: CreateApproval :one
INSERT INTO approval_requests (
    org_id, agent_id, agent_name, tool_name, action, parameters,
    justification, risk_level, estimated_records, reversible, environment,
    data_types, policy_id, expires_at, gateway_session_id, gateway_node_address
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11,
    $12, $13, $14, $15, $16
)
RETURNING
    id, org_id, agent_id, agent_name, tool_name, action, parameters,
    justification, risk_level, estimated_records, reversible, environment,
    data_types, policy_id, status, approved_by, decision_reason,
    decided_at, expires_at, created_at, gateway_session_id, gateway_node_address;

-- name: DecideApproval :one
UPDATE approval_requests SET
    status          = $3,
    approved_by     = $4,
    decision_reason = $5,
    decided_at      = NOW()
WHERE id = $1 AND org_id = $2 AND status = 'pending'
RETURNING
    id, org_id, agent_id, agent_name, tool_name, action, parameters,
    justification, risk_level, estimated_records, reversible, environment,
    data_types, policy_id, status, approved_by, decision_reason,
    decided_at, expires_at, created_at, gateway_session_id, gateway_node_address;
