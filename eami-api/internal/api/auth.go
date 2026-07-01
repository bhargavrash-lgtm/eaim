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

func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "email and password are required")
		return
	}
	// Resolve user — production uses queries; test path uses storeIface.
	var (
		userID    uuid.UUID
		userOrgID uuid.UUID
		userEmail string
		userRole  string
		userName  string
		passHash  string
	)
	if s.queries != nil {
		u, err := s.queries.GetUserByEmail(r.Context(), req.Email)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
			return
		}
		if !u.PasswordHash.Valid {
			writeError(w, http.StatusUnauthorized, "unauthorized", "account uses SSO")
			return
		}
		userID = u.ID
		userOrgID = u.OrgID
		userEmail = u.Email
		userRole = u.Role
		if u.Name.Valid {
			userName = u.Name.String
		}
		passHash = u.PasswordHash.String
	} else {
		su, err := s.storeIface.GetUserByEmail(r.Context(), req.Email)
		if err != nil {
			if err == ErrNotFound {
				writeError(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
			} else {
				writeError(w, http.StatusInternalServerError, "internal_error", "authentication service unavailable")
			}
			return
		}
		if su.PasswordHash == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "account uses SSO")
			return
		}
		userID = su.ID
		userOrgID = su.OrgID
		userEmail = su.Email
		userRole = su.Role
		userName = su.Name
		passHash = su.PasswordHash
	}
	if err := authpkg.CheckPassword(req.Password, passHash); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
		return
	}
	accessToken, exp, err := s.authSvc.IssueAccessToken(userID, userOrgID, userEmail, userRole)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not issue access token")
		return
	}
	rawRefresh, hashRefresh, err := authpkg.IssueRefreshToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not issue refresh token")
		return
	}
	// Persist refresh token only when DB is available (test path skips this).
	if s.queries != nil {
		refreshExpiry := time.Now().Add(30 * 24 * time.Hour)
		if _, err := s.queries.CreateRefreshToken(r.Context(), userID, hashRefresh, refreshExpiry); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "could not persist refresh token")
			return
		}
	}
	_ = hashRefresh // prevent unused-var error in test path
	writeJSON(w, http.StatusOK, LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    int(time.Until(exp).Seconds()),
		User: &UserResp{
			ID:    userID.String(),
			Email: userEmail,
			Name:  userName,
			Role:  userRole,
			OrgID: userOrgID.String(),
		},
	})
}

func (s *Server) Refresh(w http.ResponseWriter, r *http.Request) {
	var req RefreshRequest
	if err := decodeJSON(r, &req); err != nil || req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "refresh_token required")
		return
	}
	rt, err := s.queries.GetRefreshToken(r.Context(), req.RefreshToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired refresh token")
		return
	}
	dbUser2, err := s.storeIface.GetUserByID(r.Context(), rt.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	user := &store.User{
		ID:    dbUser2.ID,
		OrgID: dbUser2.OrgID,
		Email: dbUser2.Email,
		Role:  dbUser2.Role,
	}
	if dbUser2.Name != "" {
		user.Name = pgtype.Text{String: dbUser2.Name, Valid: true}
	}
	_ = s.queries.RevokeRefreshToken(r.Context(), rt.ID)
	accessToken, exp, err := s.authSvc.IssueAccessToken(user.ID, user.OrgID, user.Email, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not issue access token")
		return
	}
	rawRefresh, hashRefresh, err := authpkg.IssueRefreshToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not issue refresh token")
		return
	}
	refreshExpiry := time.Now().Add(30 * 24 * time.Hour)
	if _, err := s.queries.CreateRefreshToken(r.Context(), user.ID, hashRefresh, refreshExpiry); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not persist refresh token")
		return
	}
	name := ""
	if user.Name.Valid {
		name = user.Name.String
	}
	writeJSON(w, http.StatusOK, LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    int(time.Until(exp).Seconds()),
		User: &UserResp{
			ID:    user.ID.String(),
			Email: user.Email,
			Name:  name,
			Role:  user.Role,
			OrgID: user.OrgID.String(),
		},
	})
}

func (s *Server) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	keys, err := s.queries.ListAPIKeys(r.Context(), uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	resp := make([]APIKeyResp, 0, len(keys))
	for _, k := range keys {
		resp = append(resp, apiKeyToResp(k))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"data": resp})
}

func (s *Server) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	var req CreateAPIKeyRequest
	if err := decodeJSON(r, &req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	rawKey, prefix, keyHash, err := authpkg.APIKeyFromRaw()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not generate key")
		return
	}
	scopes := req.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	k, err := s.queries.CreateAPIKey(r.Context(), store.CreateAPIKeyParams{
		OrgID:     uc.OrgID,
		Name:      req.Name,
		KeyHash:   keyHash,
		Prefix:    prefix,
		Scopes:    scopes,
		CreatedBy: pgtype.UUID{Bytes: uc.UserID, Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, CreateAPIKeyResponse{Key: rawKey, Meta: apiKeyToResp(*k)})
}

func (s *Server) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	id, err := uuid.Parse(chi.URLParam(r, "keyId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid keyId")
		return
	}
	if err := s.queries.RevokeAPIKey(r.Context(), id, uc.OrgID); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "API key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func apiKeyToResp(k store.APIKey) APIKeyResp {
	resp := APIKeyResp{
		ID:        k.ID.String(),
		Name:      k.Name,
		Prefix:    k.Prefix,
		Scopes:    k.Scopes,
		CreatedAt: k.CreatedAt,
	}
	if k.LastUsed.Valid {
		resp.LastUsed = &k.LastUsed.Time
	}
	return resp
}
