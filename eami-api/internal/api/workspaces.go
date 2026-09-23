// workspaces.go -- eami-api/internal/api
//
// B-197 increment 3: real CRUD for Workspaces + workspace membership, and
// the requireWorkspaceRole middleware (middleware.go) that enforces it.
// Realizes B-207's own schema (groups/workspaces/workspace_memberships,
// migration 000021) and B-197's investigation's RBAC design.
//
// Uses store.Queries.DB() throughout -- the established escape hatch for
// queries with no sqlc wrapper (db.go's own doc comment; tools.go/
// finops.go already use it the same way) -- deliberately NOT sqlc-
// generated: sqlc.yaml's schema source (schema.sql) is frozen/historical
// (superseded by the migrations-v2 runner, B-051), predates B-207's
// tables entirely, and un-freezing it is a real decision this brief was
// never scoped to make.
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ── Response / request types ──────────────────────────────────────────────────

type WorkspaceResp struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	GroupID   string    `json:"group_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Description (B-210) -- real data that already existed unexposed:
	// every workspace is 1:1 with a groups row (group_id), and groups
	// already has a real description column (migration 000021). Never
	// surfaced by any workspace endpoint until now. Nil, not "", when the
	// group has none -- distinguishable from a deliberately empty string.
	Description *string `json:"description,omitempty"`
}

type WorkspaceListResp struct {
	Data []WorkspaceResp `json:"data"`
}

type WorkspaceCreateRequest struct {
	Name string `json:"name"`
}

type WorkspaceUpdateRequest struct {
	Name *string `json:"name"`
}

type WorkspaceMemberResp struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type WorkspaceMemberListResp struct {
	Data []WorkspaceMemberResp `json:"data"`
}

type AddWorkspaceMemberRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type UpdateWorkspaceMemberRoleRequest struct {
	Role string `json:"role"`
}

type MyWorkspaceMembershipResp struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
	Role          string `json:"role"`
}

type MyWorkspaceMembershipsResp struct {
	Data []MyWorkspaceMembershipResp `json:"data"`
}

var validWorkspaceRoles = map[string]bool{"workspace_admin": true, "workspace_member": true}

// requireQueries writes a 500 and returns false if s.queries is nil (a
// Server built via NewHandler for another handler's tests, which never
// sets it -- mirrors the "if s.queries != nil" guard tools.go's own
// toolQueries() and policies.go's handlers already use, applied
// consistently across every handler in this file, not just CreateWorkspace's
// own original check. A code-review finding (B-197 increment 3): without
// this, most of these handlers would nil-pointer-panic instead of failing
// cleanly -- caught by chi's Recoverer as a generic 500 either way, not a
// security bypass, but a real, cheap robustness gap worth closing.
func (s *Server) requireQueries(w http.ResponseWriter) bool {
	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "workspaces require a real database connection")
		return false
	}
	return true
}

// ── Handlers: Workspace CRUD ────────────────────────────────────────────────────

// CreateWorkspace handles POST /v1/workspaces (admin-only, router.go).
// Two-statement transaction: a groups row, then a workspaces row
// referencing it -- realizing B-207's "every Workspace IS-A Group"
// relationship concretely, atomically (both or neither), via
// store.Queries.Begin (the same real-transaction mechanism bootstrap.go's
// own multi-statement setup wizard already establishes).
func (s *Server) CreateWorkspace(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	var req WorkspaceCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	if !s.requireQueries(w) {
		return
	}

	tx, err := s.queries.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck -- no-op after a successful Commit

	orgID := pgtype.UUID{Bytes: uc.OrgID, Valid: true}
	createdBy := pgtype.UUID{Bytes: uc.UserID, Valid: true}

	var groupID uuid.UUID
	if err := tx.QueryRow(r.Context(), `
		INSERT INTO groups (org_id, name, created_by) VALUES ($1, $2, $3) RETURNING id
	`, orgID, req.Name, createdBy).Scan(&groupID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "create group: "+err.Error())
		return
	}

	var resp WorkspaceResp
	var groupIDOut, idOut uuid.UUID
	if err := tx.QueryRow(r.Context(), `
		INSERT INTO workspaces (org_id, group_id, name, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, org_id, group_id, name, created_at, updated_at
	`, orgID, pgtype.UUID{Bytes: groupID, Valid: true}, req.Name, createdBy).Scan(
		&idOut, &orgID, &groupIDOut, &resp.Name, &resp.CreatedAt, &resp.UpdatedAt,
	); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "conflict", "a workspace with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "create workspace: "+err.Error())
		return
	}

	// Auto-grant the creator workspace_admin -- a real usability gap found
	// by this brief's own mandatory security review (not a security bug:
	// without this, a brand-new workspace starts with zero members, and
	// only an org-admin -- not the creator, if they're merely an operator
	// -- could grant anyone access to it, via a separate AddWorkspaceMember
	// call). Same transaction as the two inserts above -- a workspace is
	// never left ownerless even under a partial-failure/retry.
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO workspace_memberships (user_id, workspace_id, role) VALUES ($1, $2, 'workspace_admin')
	`, createdBy, pgtype.UUID{Bytes: idOut, Valid: true}); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "grant creator workspace_admin: "+err.Error())
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	resp.ID = idOut.String()
	resp.OrgID = uc.OrgID.String()
	resp.GroupID = groupIDOut.String()
	writeJSON(w, http.StatusCreated, resp)
}

// ListWorkspaces handles GET /v1/workspaces (existing read tier --
// admin/operator/viewer -- router.go). Workspace names/existence are
// organizational metadata, not the scoped data domains B-197 restricts to
// members only (CMDB/Memory/Training/policies) -- any real org member may
// see which workspaces exist, matching how ListAgents/ListPolicies are
// gated at this same tier.
func (s *Server) ListWorkspaces(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	uc := claimsFromContext(r)
	rows, err := s.queries.DB().Query(r.Context(), `
		SELECT w.id, w.org_id, w.group_id, w.name, w.created_at, w.updated_at, g.description
		FROM workspaces w
		JOIN groups g ON g.id = w.group_id
		WHERE w.org_id = $1 ORDER BY w.name ASC
	`, pgtype.UUID{Bytes: uc.OrgID, Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer rows.Close()

	data := []WorkspaceResp{}
	for rows.Next() {
		var id, orgID, groupID uuid.UUID
		var resp WorkspaceResp
		var description pgtype.Text
		if err := rows.Scan(&id, &orgID, &groupID, &resp.Name, &resp.CreatedAt, &resp.UpdatedAt, &description); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		resp.ID, resp.OrgID, resp.GroupID = id.String(), orgID.String(), groupID.String()
		if description.Valid {
			resp.Description = &description.String
		}
		data = append(data, resp)
	}
	writeJSON(w, http.StatusOK, WorkspaceListResp{Data: data})
}

// GetWorkspace handles GET /v1/workspaces/{workspaceId} (existing read tier).
func (s *Server) GetWorkspace(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "workspaceId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workspaceId")
		return
	}
	resp, err := s.getWorkspaceRow(r.Context(), id, uc.OrgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "workspace not found")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// UpdateWorkspace handles PATCH /v1/workspaces/{workspaceId}
// (requireWorkspaceRole(workspace_admin), router.go).
func (s *Server) UpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "workspaceId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workspaceId")
		return
	}
	var req WorkspaceUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Name == nil || *req.Name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}

	tag, err := s.queries.DB().Exec(r.Context(), `
		UPDATE workspaces SET name = $1 WHERE id = $2 AND org_id = $3
	`, *req.Name, pgtype.UUID{Bytes: id, Valid: true}, pgtype.UUID{Bytes: uc.OrgID, Valid: true})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "conflict", "a workspace with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found", "workspace not found")
		return
	}
	resp, err := s.getWorkspaceRow(r.Context(), id, uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// DeleteWorkspace handles DELETE /v1/workspaces/{workspaceId} (admin-only,
// router.go). Deletes via the underlying groups row -- the real cascade
// chain (groups -> workspaces -> workspace_memberships/policies CASCADE;
// gateway_agents/endpoints SET NULL, migration 000021) does the rest in
// one atomic statement, rather than a manual multi-step delete.
func (s *Server) DeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	uc := claimsFromContext(r)
	id, err := parseUUIDParam(r, "workspaceId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workspaceId")
		return
	}
	tag, err := s.queries.DB().Exec(r.Context(), `
		DELETE FROM groups WHERE id = (
			SELECT group_id FROM workspaces WHERE id = $1 AND org_id = $2
		)
	`, pgtype.UUID{Bytes: id, Valid: true}, pgtype.UUID{Bytes: uc.OrgID, Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found", "workspace not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getWorkspaceRow is the shared org-scoped single-row fetch GetWorkspace/
// UpdateWorkspace both use, so their "read back what I just wrote" shape
// stays identical.
func (s *Server) getWorkspaceRow(ctx context.Context, id, orgID uuid.UUID) (WorkspaceResp, error) {
	var resp WorkspaceResp
	var idOut, orgIDOut, groupIDOut uuid.UUID
	var description pgtype.Text
	err := s.queries.DB().QueryRow(ctx, `
		SELECT w.id, w.org_id, w.group_id, w.name, w.created_at, w.updated_at, g.description
		FROM workspaces w
		JOIN groups g ON g.id = w.group_id
		WHERE w.id = $1 AND w.org_id = $2
	`, pgtype.UUID{Bytes: id, Valid: true}, pgtype.UUID{Bytes: orgID, Valid: true}).Scan(
		&idOut, &orgIDOut, &groupIDOut, &resp.Name, &resp.CreatedAt, &resp.UpdatedAt, &description,
	)
	if err != nil {
		return WorkspaceResp{}, err
	}
	resp.ID, resp.OrgID, resp.GroupID = idOut.String(), orgIDOut.String(), groupIDOut.String()
	if description.Valid {
		resp.Description = &description.String
	}
	return resp, nil
}

// ── Handlers: workspace membership ──────────────────────────────────────────────

// MyWorkspaceMemberships handles GET /v1/workspaces/mine. Self-scoped by
// uc.UserID (the JWT's own server-set sub claim) -- never a client-
// supplied user id. Any authenticated role may call this; it can only
// ever return the caller's own real rows.
func (s *Server) MyWorkspaceMemberships(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	uc := claimsFromContext(r)
	data, err := s.queryMyWorkspaceMemberships(r.Context(), uc.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, MyWorkspaceMembershipsResp{Data: data})
}

// queryMyWorkspaceMemberships is shared by MyWorkspaceMemberships (GET
// /v1/workspaces/mine) and GetMe (GET /v1/users/me, users.go), which
// embeds the identical data under its own "workspaces" field -- one query,
// not two copies of the same JOIN.
func (s *Server) queryMyWorkspaceMemberships(ctx context.Context, userID uuid.UUID) ([]MyWorkspaceMembershipResp, error) {
	rows, err := s.queries.DB().Query(ctx, `
		SELECT wm.workspace_id, w.name, wm.role
		FROM workspace_memberships wm
		JOIN workspaces w ON w.id = wm.workspace_id
		WHERE wm.user_id = $1
		ORDER BY w.name ASC
	`, pgtype.UUID{Bytes: userID, Valid: true})
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	data := []MyWorkspaceMembershipResp{}
	for rows.Next() {
		var wsID uuid.UUID
		var m MyWorkspaceMembershipResp
		if err := rows.Scan(&wsID, &m.WorkspaceName, &m.Role); err != nil {
			return nil, err
		}
		m.WorkspaceID = wsID.String()
		data = append(data, m)
	}
	return data, rows.Err()
}

// ListWorkspaceMembers handles GET /v1/workspaces/{workspaceId}/members
// (requireWorkspaceRole(workspace_member) -- any real member, or an
// org-admin, may see the roster).
func (s *Server) ListWorkspaceMembers(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	uc := claimsFromContext(r)
	workspaceID, err := parseUUIDParam(r, "workspaceId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workspaceId")
		return
	}
	rows, err := s.queries.DB().Query(r.Context(), `
		SELECT wm.user_id, u.email, wm.role, wm.created_at
		FROM workspace_memberships wm
		JOIN users u ON u.id = wm.user_id
		JOIN workspaces w ON w.id = wm.workspace_id
		WHERE wm.workspace_id = $1 AND w.org_id = $2
		ORDER BY u.email ASC
	`, pgtype.UUID{Bytes: workspaceID, Valid: true}, pgtype.UUID{Bytes: uc.OrgID, Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer rows.Close()

	data := []WorkspaceMemberResp{}
	for rows.Next() {
		var userID uuid.UUID
		var m WorkspaceMemberResp
		if err := rows.Scan(&userID, &m.Email, &m.Role, &m.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		m.UserID = userID.String()
		data = append(data, m)
	}
	writeJSON(w, http.StatusOK, WorkspaceMemberListResp{Data: data})
}

// AddWorkspaceMember handles POST /v1/workspaces/{workspaceId}/members
// (requireWorkspaceRole(workspace_admin)).
//
// workspace_memberships has no org_id column of its own (migration
// 000021) -- unlike gateway_agents/endpoints/policies, there is no
// database-level trigger enforcing that the target user actually belongs
// to the same org as the workspace. This handler's own explicit check
// below is the ONLY thing enforcing that boundary -- a real,
// security-relevant application-layer invariant, not an incidental
// validation, and covered by its own explicit adversarial test
// (TestAddWorkspaceMember_RealDB_DifferentOrgUser_Rejected).
func (s *Server) AddWorkspaceMember(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	uc := claimsFromContext(r)
	workspaceID, err := parseUUIDParam(r, "workspaceId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workspaceId")
		return
	}
	var req AddWorkspaceMemberRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid user_id")
		return
	}
	if !validWorkspaceRoles[req.Role] {
		writeError(w, http.StatusBadRequest, "bad_request", "role must be workspace_admin or workspace_member")
		return
	}

	// Cross-org membership guard: the target user must belong to the SAME
	// org as the workspace. Checked explicitly, in the same query that
	// also confirms the workspace itself belongs to uc.OrgID (the
	// caller's own server-resolved org) -- a single round trip, not two
	// separately-racy checks.
	var userOrgMatches bool
	err = s.queries.DB().QueryRow(r.Context(), `
		SELECT EXISTS (
			SELECT 1 FROM users u
			JOIN workspaces w ON w.org_id = u.org_id
			WHERE u.id = $1 AND w.id = $2 AND w.org_id = $3
		)
	`, pgtype.UUID{Bytes: userID, Valid: true}, pgtype.UUID{Bytes: workspaceID, Valid: true}, pgtype.UUID{Bytes: uc.OrgID, Valid: true}).Scan(&userOrgMatches)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !userOrgMatches {
		writeError(w, http.StatusBadRequest, "bad_request", "user does not belong to this workspace's org")
		return
	}

	_, err = s.queries.DB().Exec(r.Context(), `
		INSERT INTO workspace_memberships (user_id, workspace_id, role) VALUES ($1, $2, $3)
	`, pgtype.UUID{Bytes: userID, Valid: true}, pgtype.UUID{Bytes: workspaceID, Valid: true}, req.Role)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "conflict", "user is already a member of this workspace")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// UpdateWorkspaceMemberRole handles PATCH
// /v1/workspaces/{workspaceId}/members/{userId}
// (requireWorkspaceRole(workspace_admin)).
func (s *Server) UpdateWorkspaceMemberRole(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	workspaceID, err := parseUUIDParam(r, "workspaceId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workspaceId")
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid userId")
		return
	}
	var req UpdateWorkspaceMemberRoleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if !validWorkspaceRoles[req.Role] {
		writeError(w, http.StatusBadRequest, "bad_request", "role must be workspace_admin or workspace_member")
		return
	}

	tag, err := s.queries.DB().Exec(r.Context(), `
		UPDATE workspace_memberships SET role = $1 WHERE user_id = $2 AND workspace_id = $3
	`, req.Role, pgtype.UUID{Bytes: userID, Valid: true}, pgtype.UUID{Bytes: workspaceID, Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found", "membership not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RemoveWorkspaceMember handles DELETE
// /v1/workspaces/{workspaceId}/members/{userId}
// (requireWorkspaceRole(workspace_admin)).
func (s *Server) RemoveWorkspaceMember(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	workspaceID, err := parseUUIDParam(r, "workspaceId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workspaceId")
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid userId")
		return
	}
	tag, err := s.queries.DB().Exec(r.Context(), `
		DELETE FROM workspace_memberships WHERE user_id = $1 AND workspace_id = $2
	`, pgtype.UUID{Bytes: userID, Valid: true}, pgtype.UUID{Bytes: workspaceID, Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found", "membership not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// isUniqueViolation (agents.go) is reused directly, not redefined here.
