// Hand-written store methods for workspace_memberships writes (B-233).
package store

import (
	"context"

	"github.com/google/uuid"
)

// workspace_memberships has no org_id column of its own, so both writes are
// scoped to the caller's org through workspaces inside the same statement:
// a workspace belonging to any other org matches no row, nothing changes,
// and 0 rows affected is reported exactly as for a nonexistent membership.

const updateWorkspaceMemberRoleSQL = `
UPDATE workspace_memberships SET role = $1
WHERE user_id = $2 AND workspace_id = $3
  AND workspace_id IN (SELECT id FROM workspaces WHERE org_id = $4)`

// UpdateWorkspaceMemberRole sets a member's role in orgID's workspace and
// returns the number of rows changed (0: no such membership in this org).
func (q *Queries) UpdateWorkspaceMemberRole(ctx context.Context, orgID, workspaceID, userID uuid.UUID, role string) (int64, error) {
	tag, err := q.db.Exec(ctx, updateWorkspaceMemberRoleSQL, role, toPgtypeUUID(userID), toPgtypeUUID(workspaceID), toPgtypeUUID(orgID))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

const removeWorkspaceMemberSQL = `
DELETE FROM workspace_memberships
WHERE user_id = $1 AND workspace_id = $2
  AND workspace_id IN (SELECT id FROM workspaces WHERE org_id = $3)`

// RemoveWorkspaceMember deletes a membership from orgID's workspace and
// returns the number of rows removed (0: no such membership in this org).
func (q *Queries) RemoveWorkspaceMember(ctx context.Context, orgID, workspaceID, userID uuid.UUID) (int64, error) {
	tag, err := q.db.Exec(ctx, removeWorkspaceMemberSQL, toPgtypeUUID(userID), toPgtypeUUID(workspaceID), toPgtypeUUID(orgID))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
