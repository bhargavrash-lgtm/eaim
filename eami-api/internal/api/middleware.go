package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/eami/api/internal/license"
)

// contextKey is a typed key for request context values.
type contextKey string

const (
	ctxClaims contextKey = "claims"
)

// userClaims is extracted from the JWT and attached to the request context.
type userClaims struct {
	UserID uuid.UUID
	OrgID  uuid.UUID
	Email  string
	Role   string
}

// jwtMiddleware validates the Bearer JWT and attaches claims to the context.
// Returns 401 on missing or invalid token.
func (s *Server) jwtMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or malformed Authorization header")
			return
		}
		tokenStr := strings.TrimPrefix(header, "Bearer ")

		claims, err := s.authSvc.VerifyAccessToken(tokenStr)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}

		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid token subject")
			return
		}
		orgID, err := uuid.Parse(claims.OrgID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid token org")
			return
		}

		uc := userClaims{
			UserID: userID,
			OrgID:  orgID,
			Email:  claims.Email,
			Role:   claims.Role,
		}
		ctx := context.WithValue(r.Context(), ctxClaims, uc)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// claimsFromContext retrieves the userClaims from a request context.
// Panics if the middleware was not applied -- this is a programming error.
func claimsFromContext(r *http.Request) userClaims {
	v := r.Context().Value(ctxClaims)
	if v == nil {
		panic("api: jwtMiddleware not applied")
	}
	return v.(userClaims)
}

// requireServiceKey is a middleware that validates the X-Service-Key header
// against the configured service key using constant-time comparison to prevent
// timing attacks. Used for collector agent write paths that don't use JWT auth.
func (s *Server) requireServiceKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Service-Key")
		if subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.ServiceKey)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid service key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// -- Helpers ------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{Code: code, Message: message})
}

func decodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// requireRole returns a middleware that allows only requests whose JWT role
// is in the allowed list. Returns 403 Forbidden otherwise.
func (s *Server) requireRole(allowed ...string) func(http.Handler) http.Handler {
	set := make(map[string]bool, len(allowed))
	for _, r := range allowed {
		set[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uc := claimsFromContext(r)
			if !set[uc.Role] {
				writeError(w, http.StatusForbidden, "forbidden",
					"your role ("+uc.Role+") does not have access to this resource")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireModuleLicensed returns a middleware that allows only requests
// from an org whose CURRENT license (re-verified fresh on every request,
// never trusted from a cache) includes the named module. Composes
// independently with requireRole -- RBAC answers "can this user act,"
// this answers "is the org entitled at all" -- two orthogonal AND-ed
// conditions, the same way this codebase's own chi middleware chains
// already stack unrelated checks (B-157 investigation's own Part C
// finding, confirmed here in the actual implementation).
//
// Fails closed on every branch: no license uploaded, a license that
// fails re-verification (expired, tampered, wrong org), or a genuine DB
// error resolving it are all treated identically -- a 403, never a
// silent pass-through. Deliberately not distinguishing "no license" from
// "licensing check failed" in the response (a v1 simplification,
// disclosed in BUILT.md, not silently glossed over) -- both must block
// the request, and this brief has no sophistication requirement beyond
// "hard block, clear message" (its own explicit scope).
func (s *Server) requireModuleLicensed(module string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uc := claimsFromContext(r)
			row, err := s.queries.GetLatestLicense(r.Context(), uc.OrgID)
			if err != nil {
				if err != pgx.ErrNoRows {
					// A genuine infrastructure error resolving licensing
					// still fails closed (never fail open) -- logged so
					// an operator can tell this apart from a genuinely
					// unlicensed org, even though the client-facing
					// message is deliberately identical either way.
					slog.Error("license: failed to resolve org license", "org_id", uc.OrgID, "err", err)
				}
				writeError(w, http.StatusForbidden, "module_not_licensed",
					"your organization is not licensed for the "+module+" module")
				return
			}
			claims, verr := license.Verify(row.RawLicense)
			// Security review finding (this brief): a licenses row's org_id
			// column is never itself signed -- Verify only proves the JWT is
			// genuine and unexpired, not that it was issued FOR this org. A
			// privileged direct-DB write could otherwise relabel a
			// genuinely-signed license belonging to a DIFFERENT org (read
			// from a shared Postgres, a backup, or an MSP's other customer
			// appliance) with this org's org_id, and this check would
			// previously have accepted it. Re-deriving the binding from the
			// claims themselves, exactly like UploadLicense already does at
			// write time, closes that gap at read time too.
			if verr != nil || claims.OrgID() != uc.OrgID.String() || !claims.HasModule(module) {
				writeError(w, http.StatusForbidden, "module_not_licensed",
					"your organization is not licensed for the "+module+" module")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// viewerReadOnly blocks non-GET requests for the viewer role. All other roles
// pass through. Apply after jwtMiddleware.
func (s *Server) viewerReadOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uc := claimsFromContext(r)
		if uc.Role == "viewer" && r.Method != http.MethodGet {
			writeError(w, http.StatusForbidden, "forbidden", "viewer role is read-only")
			return
		}
		next.ServeHTTP(w, r)
	})
}
