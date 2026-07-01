-- name: ListAgents :many
SELECT id, org_id, name, model, owner, scope, risk_tier, status,
       token_ttl_seconds, created_by, created_at, updated_at, last_seen
FROM gateway_agents
WHERE org_id = $1
  AND ($2::text IS NULL OR status = $2)
  AND ($3::text IS NULL OR risk_tier = $3)
ORDER BY name ASC;

-- name: GetAgent :one
SELECT id, org_id, name, model, owner, scope, risk_tier, status,
       token_ttl_seconds, created_by, created_at, updated_at, last_seen
FROM gateway_agents
WHERE id = $1 AND org_id = $2
LIMIT 1;

-- name: CreateAgent :one
INSERT INTO gateway_agents (org_id, name, model, owner, scope, risk_tier, token_ttl_seconds, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, org_id, name, model, owner, scope, risk_tier, status,
          token_ttl_seconds, created_by, created_at, updated_at, last_seen;

-- name: UpdateAgent :one
UPDATE gateway_agents SET
    scope             = COALESCE(@scope, scope),
    risk_tier         = COALESCE(@risk_tier, risk_tier),
    status            = COALESCE(@status, status),
    token_ttl_seconds = COALESCE(@token_ttl_seconds, token_ttl_seconds)
WHERE id = @id AND org_id = @org_id
RETURNING id, org_id, name, model, owner, scope, risk_tier, status,
          token_ttl_seconds, created_by, created_at, updated_at, last_seen;

-- name: DeleteAgent :exec
DELETE FROM gateway_agents WHERE id = $1 AND org_id = $2;
