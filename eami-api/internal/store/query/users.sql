-- name: ListUsers :many
SELECT id, org_id, email, name, role, created_at, last_login, deleted_at
FROM users
WHERE org_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountUsers :one
SELECT COUNT(*) FROM users WHERE org_id = $1 AND deleted_at IS NULL;

-- name: CreateInvitedUser :one
INSERT INTO users (org_id, email, role, invited_at, invited_by)
VALUES ($1, $2, $3, NOW(), $4)
RETURNING id, org_id, email, name, role, created_at, last_login, deleted_at;

-- name: UpdateUserRole :one
UPDATE users SET role = $3
WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
RETURNING id, org_id, email, name, role, created_at, last_login, deleted_at;

-- name: SoftDeleteUser :exec
UPDATE users SET deleted_at = NOW()
WHERE id = $1 AND org_id = $2;
