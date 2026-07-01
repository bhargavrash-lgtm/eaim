package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	authpkg "github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/store"
)

// ── Response / request types ──────────────────────────────────────────────────

type UserResp2 struct {
	ID        string     `json:"id"`
	Email     string     `json:"email"`
	Name      *string    `json:"name,omitempty"`
	Role      string     `json:"role"`
	OrgID     string     `json:"org_id"`
	CreatedAt time.Time  `json:"created_at"`
	LastLogin *time.Time `json:"last_login,omitempty"`
}

type UserListResp struct {
	Data []UserResp2    `json:"data"`
	Meta PaginationMeta `json:"meta"`
}

type InviteUserRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type InviteUserResp struct {
	User       UserResp2 `json:"user"`
	InviteLink string    `json:"invite_link"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type UpdateRoleRequest struct {
	Role string `json:"role"`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// ListUsers handles GET /v1/users
func (s *Server) ListUsers(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	q := r.URL.Query()
	page, perPage := pagination(q.Get("page"), q.Get("per_page"), 25, 100)

	rows, err := s.queries.ListUsers(r.Context(), uc.OrgID, int32(perPage), int32((page-1)*perPage))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	total, err := s.queries.CountUsers(r.Context(), uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	data := make([]UserResp2, 0, len(rows))
	for _, u := range rows {
		data = append(data, userRowToResp(u))
	}
	writeJSON(w, http.StatusOK, UserListResp{
		Data: data,
		Meta: PaginationMeta{Total: total, Page: page, PerPage: perPage},
	})
}

// InviteUser handles POST /v1/users/invite
func (s *Server) InviteUser(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	var req InviteUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Email == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "email is required")
		return
	}
	allowedRoles := map[string]bool{"admin": true, "operator": true, "approver": true, "viewer": true}
	if req.Role == "" {
		req.Role = "viewer"
	}
	if !allowedRoles[req.Role] {
		writeError(w, http.StatusBadRequest, "bad_request", "role must be admin|operator|approver|viewer")
		return
	}

	// Create the user record (password_hash will be set when they accept the invite).
	u, err := s.queries.CreateInvitedUser(r.Context(), store.CreateInvitedUserParams{
		OrgID:     uc.OrgID,
		Email:     req.Email,
		Role:      req.Role,
		InvitedBy: pgtype.UUID{Bytes: uc.UserID, Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	// Issue a 48-hour invite JWT.
	inviteTTL := 48 * time.Hour
	inviteToken, exp, err := s.authSvc.IssueAccessToken(u.ID, uc.OrgID, req.Email, "invited")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not issue invite token")
		return
	}
	_ = exp

	writeJSON(w, http.StatusCreated, InviteUserResp{
		User:       userRowToResp(*u),
		InviteLink: "/accept-invite?token=" + inviteToken,
		ExpiresAt:  time.Now().Add(inviteTTL),
	})
}

// UpdateUserRole handles PUT /v1/users/{userId}/role
func (s *Server) UpdateUserRole(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid userId")
		return
	}
	var req UpdateRoleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	allowedRoles := map[string]bool{"admin": true, "operator": true, "approver": true, "viewer": true}
	if !allowedRoles[req.Role] {
		writeError(w, http.StatusBadRequest, "bad_request", "role must be admin|operator|approver|viewer")
		return
	}
	u, err := s.queries.UpdateUserRole(r.Context(), id, uc.OrgID, req.Role)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, userRowToResp(*u))
}

// DeleteUser handles DELETE /v1/users/{userId} (soft delete via deleted_at).
func (s *Server) DeleteUser(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid userId")
		return
	}
	if id == uc.UserID {
		writeError(w, http.StatusBadRequest, "bad_request", "cannot delete your own account")
		return
	}
	if err := s.queries.SoftDeleteUser(r.Context(), id, uc.OrgID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Converter ─────────────────────────────────────────────────────────────────

func userRowToResp(u store.UserRow) UserResp2 {
	resp := UserResp2{
		ID:        u.ID.String(),
		Email:     u.Email,
		Role:      u.Role,
		OrgID:     u.OrgID.String(),
		CreatedAt: u.CreatedAt,
	}
	if u.Name.Valid {
		resp.Name = &u.Name.String
	}
	if u.LastLogin.Valid {
		resp.LastLogin = &u.LastLogin.Time
	}
	return resp
}

// ensure authpkg is imported (used in InviteUser for IssueAccessToken)
var _ = authpkg.CheckPassword
