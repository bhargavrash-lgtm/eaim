// workspace_policies.go -- eami-api/internal/api
//
// B-197 increment 4: real CRUD for policies scoped to a workspace,
// closing B-208's explicitly disclosed gap. Uses store.Queries.DB()
// throughout -- same reason as workspaces.go's own doc comment: sqlc's
// schema source (schema.sql) is frozen/historical (B-051) and predates
// policies.workspace_id (B-207, migration 000021) entirely, so
// store.CreatePolicyParams/UpdatePolicyParams/PolicyRow structurally
// have no WorkspaceID field to extend. These are entirely NEW handlers,
// not modifications to policies.go's existing CreatePolicy/UpdatePolicy/
// DeletePolicy -- those remain untouched and continue to be the
// org-admin path for org-wide (workspace_id NULL) policies exactly as
// before this brief.
//
// workspace_id is NEVER read from a request body. PolicyCreateRequest/
// PolicyUpdateRequest (types.go) have no workspace_id JSON field at all
// -- Go's encoding/json silently drops unknown fields on decode, so even
// a malicious body containing "workspace_id": "<other-workspace>" has
// nothing to bind to. This is a structural (type-level) guarantee, not
// merely handler discipline. Every handler below resolves workspace_id
// exclusively from the route's {workspaceId} param -- the same one
// requireWorkspaceRole(router.go) already validated server-side before
// this handler ever runs.
//
// UpdateWorkspacePolicy/DeleteWorkspacePolicy filter by
// id + org_id + workspace_id (not just id + org_id, unlike the existing
// org-wide UpdatePolicy/DeletePolicy) -- a policyId that is real but
// belongs to a DIFFERENT workspace, or is the org floor (workspace_id
// IS NULL, which can never equal a real route-param UUID), correctly
// 404s through this route rather than being silently modifiable by a
// workspace_admin who only controls one workspace.
package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ── Handlers ─────────────────────────────────────────────────────────────────

// CreateWorkspacePolicy handles POST /v1/workspaces/{workspaceId}/policies
// (requireWorkspaceRole(workspace_admin), router.go). workspace_id is
// server-resolved from the route param only -- see file-level doc comment.
func (s *Server) CreateWorkspacePolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	uc := claimsFromContext(r)
	workspaceID, err := parseUUIDParam(r, "workspaceId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workspaceId")
		return
	}
	var req PolicyCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Name == "" || req.Priority < 1 {
		writeError(w, http.StatusBadRequest, "bad_request", "name and priority >= 1 required")
		return
	}
	validPolicyActions := map[string]bool{"allow": true, "deny": true, "escalate": true}
	if !validPolicyActions[req.Action] {
		writeError(w, http.StatusBadRequest, "bad_request", "action must be one of: allow, deny, escalate")
		return
	}
	if req.Conditions == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "conditions is required")
		return
	}
	status := req.Status
	if status == "" {
		status = "draft"
	}

	var polID uuid.UUID
	err = s.queries.DB().QueryRow(r.Context(), `
		INSERT INTO policies (org_id, workspace_id, name, description, priority, action, alert, status, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`,
		pgtype.UUID{Bytes: uc.OrgID, Valid: true},
		pgtype.UUID{Bytes: workspaceID, Valid: true},
		req.Name, toPgtypeTextStr(req.Description), req.Priority, req.Action, req.Alert, status,
		pgtype.UUID{Bytes: uc.UserID, Valid: true},
	).Scan(&polID)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "conflict", "a policy with this priority already exists in this workspace")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "create policy: "+err.Error())
		return
	}

	// policy_conditions is unaffected by schema.sql's freeze (no
	// workspace_id column involved) -- safe to reuse the existing sqlc
	// method directly, same as policies.go's own CreatePolicy does.
	if _, err := s.queries.CreatePolicyCondition(r.Context(), conditionsReqToParams(polID, *req.Conditions)); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "conditions failed: "+err.Error())
		return
	}
	_ = s.queries.NotifyPolicyReload(r.Context())

	resp, err := s.getWorkspacePolicyRow(r.Context(), polID, uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// UpdateWorkspacePolicy handles PATCH
// /v1/workspaces/{workspaceId}/policies/{policyId}
// (requireWorkspaceRole(workspace_admin), router.go). The WHERE clause's
// explicit workspace_id = $3 (not just id + org_id) is what confirms the
// policy being modified actually belongs to THIS workspace -- see
// file-level doc comment.
func (s *Server) UpdateWorkspacePolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	uc := claimsFromContext(r)
	workspaceID, err := parseUUIDParam(r, "workspaceId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workspaceId")
		return
	}
	policyID, err := parseUUIDParam(r, "policyId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid policyId")
		return
	}
	var req PolicyUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	var priorityArg, alertArg interface{}
	if req.Priority != nil {
		priorityArg = int32(*req.Priority)
	}
	if req.Alert != nil {
		alertArg = *req.Alert
	}

	var idOut uuid.UUID
	err = s.queries.DB().QueryRow(r.Context(), `
		UPDATE policies SET
			name        = COALESCE($4, name),
			description = COALESCE($5, description),
			priority    = COALESCE($6, priority),
			action      = COALESCE($7, action),
			alert       = COALESCE($8, alert),
			status      = COALESCE($9, status)
		WHERE id = $1 AND org_id = $2 AND workspace_id = $3
		RETURNING id
	`,
		pgtype.UUID{Bytes: policyID, Valid: true},
		pgtype.UUID{Bytes: uc.OrgID, Valid: true},
		pgtype.UUID{Bytes: workspaceID, Valid: true},
		req.Name, req.Description, priorityArg, req.Action, alertArg, req.Status,
	).Scan(&idOut)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "policy not found in this workspace")
			return
		}
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "conflict", "a policy with this priority already exists in this workspace")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	if req.Conditions != nil {
		if err := s.queries.UpsertPolicyCondition(r.Context(), conditionsReqToParams(idOut, *req.Conditions)); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "conditions failed: "+err.Error())
			return
		}
	}
	_ = s.queries.NotifyPolicyReload(r.Context())

	resp, err := s.getWorkspacePolicyRow(r.Context(), idOut, uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// DeleteWorkspacePolicy handles DELETE
// /v1/workspaces/{workspaceId}/policies/{policyId}
// (requireWorkspaceRole(workspace_admin), router.go). Same
// id + org_id + workspace_id filter as UpdateWorkspacePolicy -- a
// policyId belonging to a different workspace or the org floor 404s
// instead of being deleted.
func (s *Server) DeleteWorkspacePolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	uc := claimsFromContext(r)
	workspaceID, err := parseUUIDParam(r, "workspaceId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workspaceId")
		return
	}
	policyID, err := parseUUIDParam(r, "policyId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid policyId")
		return
	}

	tag, err := s.queries.DB().Exec(r.Context(), `
		DELETE FROM policies WHERE id = $1 AND org_id = $2 AND workspace_id = $3
	`, pgtype.UUID{Bytes: policyID, Valid: true}, pgtype.UUID{Bytes: uc.OrgID, Valid: true}, pgtype.UUID{Bytes: workspaceID, Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found", "policy not found in this workspace")
		return
	}
	_ = s.queries.NotifyPolicyReload(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// ListWorkspacePolicies handles GET
// /v1/workspaces/{workspaceId}/policies (requireWorkspaceRole
// (workspace_member), router.go). Returns the union of this workspace's
// own policies AND the org-wide floor policies (workspace_id IS NULL) --
// the "Global floor" vs. "workspace-specific" visibility distinction
// from DESIGN_SYSTEM.md §7.3. Ordered with the same composite key B-207
// established for evaluation (floor rows first, then by priority) so the
// list's own display order matches the real precedence a workspace
// member would otherwise have to infer.
func (s *Server) ListWorkspacePolicies(w http.ResponseWriter, r *http.Request) {
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
		SELECT p.id, p.workspace_id, p.name, p.description, p.priority, p.action, p.alert, p.status,
		       p.created_by, p.created_at, p.updated_at,
		       pc.agent_name_pattern, pc.tool_names, pc.action_types, pc.environments,
		       pc.record_count_gt, pc.semantic_rule, COALESCE(pc.scope_drift, FALSE)
		FROM policies p
		LEFT JOIN policy_conditions pc ON pc.policy_id = p.id
		WHERE p.org_id = $1 AND (p.workspace_id = $2 OR p.workspace_id IS NULL)
		ORDER BY (p.workspace_id IS NULL) DESC, p.priority ASC
	`, pgtype.UUID{Bytes: uc.OrgID, Valid: true}, pgtype.UUID{Bytes: workspaceID, Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer rows.Close()

	data := []PolicyResp{}
	for rows.Next() {
		resp, err := scanWorkspacePolicyRow(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		data = append(data, resp)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, PolicyListResponse{Data: data})
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// rowScanner is satisfied by both pgx.Row (QueryRow) and pgx.Rows (Query),
// letting scanWorkspacePolicyRow back both getWorkspacePolicyRow (single
// row) and ListWorkspacePolicies (many rows) with one scan/convert path.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

// scanWorkspacePolicyRow scans one policies+policy_conditions joined row
// (column order fixed by both getWorkspacePolicyRow's and
// ListWorkspacePolicies' SELECT above) into a PolicyResp, including
// workspace_id -- nil/omitted on the JSON response means "org-wide floor
// policy," matching PolicyResp.WorkspaceID's own doc comment.
func scanWorkspacePolicyRow(row rowScanner) (PolicyResp, error) {
	var resp PolicyResp
	var idOut uuid.UUID
	var wsID pgtype.UUID
	var desc pgtype.Text
	var createdBy pgtype.UUID
	var agentPattern pgtype.Text
	var toolNames, actionTypes, envs []string
	var recordCountGT pgtype.Int4
	var semanticRule pgtype.Text
	var scopeDrift bool

	if err := row.Scan(
		&idOut, &wsID, &resp.Name, &desc, &resp.Priority, &resp.Action, &resp.Alert, &resp.Status,
		&createdBy, &resp.CreatedAt, &resp.UpdatedAt,
		&agentPattern, &toolNames, &actionTypes, &envs, &recordCountGT, &semanticRule, &scopeDrift,
	); err != nil {
		return PolicyResp{}, err
	}

	resp.ID = idOut.String()
	if wsID.Valid {
		id := uuid.UUID(wsID.Bytes).String()
		resp.WorkspaceID = &id
	}
	if desc.Valid {
		resp.Description = &desc.String
	}
	if createdBy.Valid {
		id := uuid.UUID(createdBy.Bytes).String()
		resp.CreatedBy = &id
	}
	resp.Conditions = PolicyConditionsResp{
		ToolNames: toolNames, ActionTypes: actionTypes, Environments: envs, ScopeDrift: scopeDrift,
	}
	if agentPattern.Valid {
		resp.Conditions.AgentNamePattern = &agentPattern.String
	}
	if recordCountGT.Valid {
		resp.Conditions.RecordCountGT = &recordCountGT.Int32
	}
	if semanticRule.Valid {
		resp.Conditions.SemanticRule = &semanticRule.String
	}
	return resp, nil
}

// getWorkspacePolicyRow is the shared org-scoped single-row fetch
// CreateWorkspacePolicy/UpdateWorkspacePolicy both use after a write, so
// their "read back what I just wrote" shape stays identical (same
// convention as workspaces.go's own getWorkspaceRow). Deliberately NOT
// also filtered by workspace_id: the caller already knows id is a real
// row it just wrote/confirmed belongs to the right workspace via the
// write query's own WHERE clause -- this is a read of a known-good id,
// not an access-control check.
func (s *Server) getWorkspacePolicyRow(ctx context.Context, id, orgID uuid.UUID) (PolicyResp, error) {
	row := s.queries.DB().QueryRow(ctx, `
		SELECT p.id, p.workspace_id, p.name, p.description, p.priority, p.action, p.alert, p.status,
		       p.created_by, p.created_at, p.updated_at,
		       pc.agent_name_pattern, pc.tool_names, pc.action_types, pc.environments,
		       pc.record_count_gt, pc.semantic_rule, COALESCE(pc.scope_drift, FALSE)
		FROM policies p
		LEFT JOIN policy_conditions pc ON pc.policy_id = p.id
		WHERE p.id = $1 AND p.org_id = $2
	`, pgtype.UUID{Bytes: id, Valid: true}, pgtype.UUID{Bytes: orgID, Valid: true})
	return scanWorkspacePolicyRow(row)
}
