-- name: ListAudit :many
SELECT
    id, org_id, agent_id, agent_name, tool_name, action, parameters, decision,
    policy_id, approval_id, approved_by, latency_ms, token_in, token_out,
    timestamp, prev_hash, hash
FROM audit_log
WHERE org_id = $1
  AND ($2::text IS NULL OR agent_name ILIKE '%' || $2 || '%')
  AND ($3::text IS NULL OR tool_name  ILIKE '%' || $3 || '%')
  AND ($4::text IS NULL OR decision = $4)
  AND ($5::timestamptz IS NULL OR timestamp >= $5)
  AND ($6::timestamptz IS NULL OR timestamp <= $6)
ORDER BY timestamp DESC
LIMIT $7 OFFSET $8;

-- name: CountAudit :one
SELECT COUNT(*) FROM audit_log
WHERE org_id = $1
  AND ($2::text IS NULL OR agent_name ILIKE '%' || $2 || '%')
  AND ($3::text IS NULL OR tool_name  ILIKE '%' || $3 || '%')
  AND ($4::text IS NULL OR decision = $4)
  AND ($5::timestamptz IS NULL OR timestamp >= $5)
  AND ($6::timestamptz IS NULL OR timestamp <= $6);
