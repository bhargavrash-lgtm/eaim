package api

import (
	"net/http"
	"strings"
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

// MeResp is GET /v1/users/me's response -- deliberately a distinct type
// from UserResp2 (not just an alias) since it also carries workspace
// memberships, which no other /v1/users response includes.
type MeResp struct {
	ID         string             `json:"id"`
	Email      string             `json:"email"`
	Name       *string            `json:"name,omitempty"`
	Role       string             `json:"role"`
	OrgID      string             `json:"org_id"`
	Workspaces []MyWorkspaceMembershipResp `json:"workspaces"`
}

// UpdateMeRequest deliberately has only a Name field -- unlike
// UpdateRoleRequest, there is no way for a caller to smuggle a role/org_id
// change through this endpoint: encoding/json silently ignores unknown
// fields on decode, so a client sending {"name":"...","role":"admin"}
// only ever affects Name (proven by TestUpdateMe_CannotChangeRoleOrOrg).
// Name is a pointer, not a plain string -- code review caught that a
// plain string can't distinguish "field omitted" from "explicitly sent
// empty", so an omitted name (e.g. a future field being added to this
// same endpoint, or a partial client bug) would silently blank the
// user's real display name via the zero value.
type UpdateMeRequest struct {
	Name *string `json:"name"`
}

// ChangeMyPasswordRequest requires the caller's current password --
// PATCH /v1/users/me never touches password_hash, only this dedicated
// endpoint does, and only after re-verifying the caller actually knows
// their existing password (not just holds a still-valid access token).
type ChangeMyPasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
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
	// platform_admin (B-157 epic, Brief 2) is DELIBERATELY absent here --
	// this endpoint is itself gated admin-only (router.go), so including
	// it would let any ordinary org admin invite a teammate straight into
	// the platform-admin tier, defeating the entire point of B-113's fix
	// (a tier that must be genuinely harder to obtain than admin, not
	// self-service from it). platform_admin is provisioned only via
	// direct SQL against users.role -- see migration 000017's own comment.
	allowedRoles := map[string]bool{"admin": true, "operator": true, "approver": true, "viewer": true}
	if req.Role == "" {
		req.Role = "viewer"
	}
	if !allowedRoles[req.Role] {
		writeError(w, http.StatusBadRequest, "bad_request", "role must be admin|operator|approver|viewer")
		return
	}

	// Issue a real, single-use, DB-backed invite token (invite_tokens,
	// schema/migrations-v2/000022) -- NOT a JWT. The prior JWT-based design
	// had a real bug: its actual signed expiry was accessTTL (config
	// default 1 hour), not the 48 hours this comment and the response both
	// claimed, since IssueAccessToken never received the locally-computed
	// TTL at all. A DB row with a real expires_at column, checked directly,
	// can't drift from what it claims. See provisioning.go's package doc
	// comment for the full single-use-token pattern this mirrors
	// (bootstrap.go's setup_tokens). Generated before the transaction below
	// -- pure in-memory work, no reason to hold a DB transaction open for it.
	rawToken, tokenHash, err := authpkg.IssueRefreshToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not issue invite token")
		return
	}
	expiresAt := time.Now().Add(inviteTokenTTL)

	// The user row and its invite token are created in one transaction --
	// code review caught that two separate statements left a real gap: if
	// the token INSERT failed after CreateInvitedUser had already
	// committed, the user row would exist permanently with
	// password_hash IS NULL and no usable invite token, and since
	// users.email is globally UNIQUE, that email could never be
	// (re-)invited again without a direct DB fix.
	ctx := r.Context()
	tx, err := s.queries.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not start transaction")
		return
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	qtx := store.New(tx)
	u, err := qtx.CreateInvitedUser(ctx, store.CreateInvitedUserParams{
		OrgID:     uc.OrgID,
		Email:     req.Email,
		Role:      req.Role,
		InvitedBy: pgtype.UUID{Bytes: uc.UserID, Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO invite_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		u.ID, tokenHash, expiresAt,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not persist invite token")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not finalize invite")
		return
	}

	writeJSON(w, http.StatusCreated, InviteUserResp{
		User:       userRowToResp(*u),
		InviteLink: "/accept-invite?token=" + rawToken,
		ExpiresAt:  expiresAt,
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
	// platform_admin deliberately excluded -- see InviteUser's identical
	// comment above; this endpoint shares the same admin-only gate an
	// ordinary admin must never be able to use to self-promote or promote
	// a teammate into the platform-admin tier.
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

// GetMe handles GET /v1/users/me -- self-scoped entirely by the caller's
// own JWT sub claim (uc.UserID), same reasoning as MyWorkspaceMemberships
// for why any authenticated role may call it (it can only ever return the
// caller's own row).
func (s *Server) GetMe(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	uc := claimsFromContext(r)
	u, err := s.queries.GetUserByID(r.Context(), uc.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	workspaces, err := s.queryMyWorkspaceMemberships(r.Context(), uc.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	resp := MeResp{
		ID:         u.ID.String(),
		Email:      u.Email,
		Role:       u.Role,
		OrgID:      u.OrgID.String(),
		Workspaces: workspaces,
	}
	if u.Name.Valid {
		resp.Name = &u.Name.String
	}
	writeJSON(w, http.StatusOK, resp)
}

// UpdateMe handles PATCH /v1/users/me -- name only, by construction:
// UpdateMeRequest (above) has no Role/OrgID field, so those can never be
// set by this endpoint regardless of what a caller's JSON body contains
// (encoding/json silently drops unknown fields on decode). Any
// authenticated role may call it -- it only ever writes the caller's own
// row (WHERE id = uc.UserID), the same self-scoping as GetMe.
func (s *Server) UpdateMe(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	uc := claimsFromContext(r)
	var req UpdateMeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	u, err := s.queries.UpdateUserName(r.Context(), uc.UserID, uc.OrgID, *req.Name)
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

// ChangeMyPassword handles POST /v1/users/me/change-password -- requires
// the caller's current password (re-verified via authpkg.CheckPassword
// against the real stored hash, not just trusted from a valid access
// token) before setting a new one. An SSO-only account (no password_hash
// set) has nothing to verify against and is rejected outright, same
// "account uses SSO" convention Login already uses.
func (s *Server) ChangeMyPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	uc := claimsFromContext(r)
	var req ChangeMyPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "bad_request", "new_password must be at least 8 characters")
		return
	}

	u, err := s.queries.GetUserByID(r.Context(), uc.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !u.PasswordHash.Valid {
		writeError(w, http.StatusBadRequest, "bad_request", "account uses SSO")
		return
	}
	if err := authpkg.CheckPassword(req.CurrentPassword, u.PasswordHash.String); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "current password is incorrect")
		return
	}

	newHash, err := authpkg.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not hash password")
		return
	}
	if err := s.queries.UpdateUserPasswordHash(r.Context(), uc.UserID, newHash); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
