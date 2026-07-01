-- name: ListPolicies :many
SELECT
    p.id, p.org_id, p.name, p.description, p.priority, p.action, p.alert, p.status,
    p.created_by, p.created_at, p.updated_at,
    pc.id AS cond_id,
    pc.agent_name_pattern, pc.tool_names, pc.action_types, pc.environments,
    pc.record_count_gt, pc.semantic_rule, COALESCE(pc.scope_drift, FALSE) AS scope_drift
FROM policies p
LEFT JOIN policy_conditions pc ON pc.policy_id = p.id
WHERE p.org_id = $1
  AND ($2::text IS NULL OR p.status = $2)
ORDER BY p.priority ASC;

-- name: GetPolicy :one
SELECT
    p.id, p.org_id, p.name, p.description, p.priority, p.action, p.alert, p.status,
    p.created_by, p.created_at, p.updated_at,
    pc.id AS cond_id,
    pc.agent_name_pattern, pc.tool_names, pc.action_types, pc.environments,
    pc.record_count_gt, pc.semantic_rule, COALESCE(pc.scope_drift, FALSE) AS scope_drift
FROM policies p
LEFT JOIN policy_conditions pc ON pc.policy_id = p.id
WHERE p.id = $1 AND p.org_id = $2
LIMIT 1;

-- name: CreatePolicy :one
INSERT INTO policies (org_id, name, description, priority, action, alert, status, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, org_id, name, description, priority, action, alert, status,
          created_by, created_at, updated_at;

-- name: CreatePolicyCondition :one
INSERT INTO policy_conditions
    (policy_id, agent_name_pattern, tool_names, action_types, environments,
     record_count_gt, semantic_rule, scope_drift)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, policy_id, agent_name_pattern, tool_names, action_types, environments,
          record_count_gt, semantic_rule, scope_drift;

-- name: UpdatePolicy :one
UPDATE policies SET
    name        = COALESCE(@name, name),
    description = COALESCE(@description, description),
    priority    = COALESCE(@priority, priority),
    action      = COALESCE(@action, action),
    alert       = COALESCE(@alert, alert),
    status      = COALESCE(@status, status)
WHERE id = @id AND org_id = @org_id
RETURNING id, org_id, name, description, priority, action, alert, status,
          created_by, created_at, updated_at;

-- name: UpsertPolicyCondition :exec
INSERT INTO policy_conditions
    (policy_id, agent_name_pattern, tool_names, action_types, environments,
     record_count_gt, semantic_rule, scope_drift)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (policy_id) DO UPDATE SET
    agent_name_pattern = EXCLUDED.agent_name_pattern,
    tool_names         = EXCLUDED.tool_names,
    action_types       = EXCLUDED.action_types,
    environments       = EXCLUDED.environments,
    record_count_gt    = EXCLUDED.record_count_gt,
    semantic_rule      = EXCLUDED.semantic_rule,
    scope_drift        = EXCLUDED.scope_drift;

-- name: DeletePolicy :exec
DELETE FROM policies WHERE id = $1 AND org_id = $2;

-- name: ReorderPolicies :exec
UPDATE policies SET priority = $2 WHERE id = $1 AND org_id = $3;
