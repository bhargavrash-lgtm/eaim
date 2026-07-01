-- name: GetUserByID :one
SELECT id, org_id, email, name, password_hash, role
FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
SELECT id, org_id, email, name, password_hash, role
FROM users
WHERE email = $1
LIMIT 1;

-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING id, user_id, token_hash, expires_at, created_at, revoked;

-- name: GetRefreshToken :one
SELECT id, user_id, token_hash, expires_at, revoked
FROM refresh_tokens
WHERE token_hash = $1 AND revoked = FALSE AND expires_at > NOW()
LIMIT 1;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens SET revoked = TRUE WHERE id = $1;

-- name: ListAPIKeys :many
SELECT id, org_id, name, prefix, scopes, created_by, created_at, last_used, revoked
FROM api_keys
WHERE org_id = $1 AND revoked = FALSE
ORDER BY created_at DESC;

-- name: CreateAPIKey :one
INSERT INTO api_keys (org_id, name, key_hash, prefix, scopes, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, org_id, name, prefix, scopes, created_by, created_at, last_used, revoked;

-- name: RevokeAPIKey :exec
UPDATE api_keys SET revoked = TRUE WHERE id = $1 AND org_id = $2;

-- name: GetAPIKeyByHash :one
SELECT id, org_id, name, prefix, scopes, created_by, created_at, last_used, revoked
FROM api_keys
WHERE key_hash = $1 AND revoked = FALSE
LIMIT 1;
