// First-boot setup wizard (B-053 follow-up). These three routes are the
// only way an org/admin can ever be created outside a direct DB write --
// there is no other self-service signup path anywhere in eami-api.
//
// Trust model: a customer's hypervisor console (the same trust boundary
// appliance/README.md already documents as the emergency-access channel,
// since SSH is permanently disabled) is where the one-time setup token is
// displayed -- appliance/scripts/eami-stack.sh generates and prints it, this
// file only ever validates a hash of it. Pure network reachability of these
// routes is deliberately not enough to create an org: without a valid token,
// Bootstrap creates nothing regardless of how well-formed the rest of the
// request is.
//
// Concurrency: Bootstrap runs entirely inside one DB transaction that takes
// a global Postgres advisory lock (pg_advisory_xact_lock) for its lifetime,
// so every concurrent Bootstrap call -- regardless of which token each one
// holds -- serializes on that lock, not just two calls sharing the same
// token. It also takes a row lock (SELECT ... FOR UPDATE) on the specific
// setup_tokens row, so a second call for the same token sees the token
// already consumed once it acquires the lock, not a stale in-memory read.
// See schema/migrations-v2/000003_add_setup_tokens.up.sql.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	authpkg "github.com/eami/api/internal/auth"
)

// ── Request / response types ────────────────────────────────────────────────

type SetupStatusResp struct {
	Configured bool `json:"configured"`
}

type ValidateSetupTokenRequest struct {
	SetupToken string `json:"setup_token"`
}

type BootstrapRequest struct {
	SetupToken    string `json:"setup_token"`
	OrgName       string `json:"org_name"`
	AdminEmail    string `json:"admin_email"`
	AdminPassword string `json:"admin_password"`
}

type BootstrapResponse struct {
	OrgID      string `json:"org_id"`
	OrgName    string `json:"org_name"`
	AdminEmail string `json:"admin_email"`
}

// ── Rate limiting ────────────────────────────────────────────────────────────
//
// setupLimiter (s.setupLimiter, wired in router.go) is a *rateLimiter --
// see ratelimit.go (B-070) for the shared implementation and clientKey.
// Originally hand-rolled here by B-055 as a bootstrap-only type; extracted
// and generalized for B-070 so login could reuse the identical design
// rather than a second bespoke limiter. Defense-in-depth only for these
// setup routes: the setup token itself is a 256-bit value
// (appliance/scripts/eami-stack.sh's `openssl rand -hex 32`), so brute
// force is computationally infeasible regardless of this limiter.

// ── Handlers ──────────────────────────────────────────────────────────────────

// SetupStatus handles GET /v1/setup/status -- unauthenticated, no setup
// token required. Reveals only a boolean, which is what lets the frontend
// redirect an already-configured appliance's /setup route straight to
// /login (closing the "wizard is unreachable after setup" requirement)
// without needing a token just to find that out.
func (s *Server) SetupStatus(w http.ResponseWriter, r *http.Request) {
	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "setup status store is not configured")
		return
	}
	var count int
	if err := s.queries.DB().QueryRow(r.Context(), `SELECT count(*) FROM orgs`).Scan(&count); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, SetupStatusResp{Configured: count > 0})
}

// ValidateSetupToken handles POST /v1/setup/token/validate -- a read-only
// check (no row lock, no mutation) that lets the wizard reveal the org/admin
// form only after a plausible token has been entered. The real, atomic,
// single-use enforcement happens in Bootstrap below -- this endpoint cannot
// itself be used to burn a token or race the org-creation slot.
func (s *Server) ValidateSetupToken(w http.ResponseWriter, r *http.Request) {
	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "setup store is not configured")
		return
	}
	if ok, retryAfter := s.setupLimiter.Allow(clientKey(r)); !ok {
		setRetryAfter(w, retryAfter)
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many setup attempts -- try again later")
		return
	}
	var req ValidateSetupTokenRequest
	if err := decodeJSON(r, &req); err != nil || req.SetupToken == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "setup_token is required")
		return
	}

	ctx := r.Context()

	var orgCount int
	if err := s.queries.DB().QueryRow(ctx, `SELECT count(*) FROM orgs`).Scan(&orgCount); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if orgCount > 0 {
		writeError(w, http.StatusConflict, "already_configured", "this appliance is already configured")
		return
	}

	var expiresAt, consumedAt pgtype.Timestamptz
	err := s.queries.DB().QueryRow(ctx,
		`SELECT expires_at, consumed_at FROM setup_tokens WHERE token_hash = $1`,
		hashSetupToken(req.SetupToken),
	).Scan(&expiresAt, &consumedAt)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid setup token")
		return
	}
	if consumedAt.Valid {
		writeError(w, http.StatusUnauthorized, "unauthorized", "setup token has already been used")
		return
	}
	if time.Now().After(expiresAt.Time) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "setup token has expired")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Bootstrap handles POST /v1/setup/bootstrap -- creates the first org and
// admin user. See this file's package-level doc comment for the
// concurrency/trust-boundary design. Every failure path returns before
// tx.Commit, so the deferred tx.Rollback discards any partial work
// (including token consumption) -- an interrupted attempt never leaves the
// system in an ambiguous state, and the same token remains valid for a
// fresh attempt.
func (s *Server) Bootstrap(w http.ResponseWriter, r *http.Request) {
	if s.queries == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "setup store is not configured")
		return
	}
	if ok, retryAfter := s.setupLimiter.Allow(clientKey(r)); !ok {
		setRetryAfter(w, retryAfter)
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many setup attempts -- try again later")
		return
	}

	var req BootstrapRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.OrgName = strings.TrimSpace(req.OrgName)
	req.AdminEmail = strings.TrimSpace(req.AdminEmail)

	if req.SetupToken == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "setup_token is required")
		return
	}
	if req.OrgName == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "org_name is required")
		return
	}
	if !isValidSetupEmail(req.AdminEmail) {
		writeError(w, http.StatusBadRequest, "bad_request", "admin_email is not a valid email address")
		return
	}
	if len(req.AdminPassword) < 8 {
		writeError(w, http.StatusBadRequest, "bad_request", "admin_password must be at least 8 characters")
		return
	}

	passHash, err := authpkg.HashPassword(req.AdminPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not hash admin password")
		return
	}

	ctx := r.Context()
	tx, err := s.queries.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not start setup transaction")
		return
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	// Global advisory lock, held for the transaction's lifetime -- the real
	// concurrency guard for the general case, not just "two requests share
	// the same token". Locking only the individual token row (below) would
	// still let two concurrent requests holding two *different* valid
	// tokens both observe orgCount == 0 and both insert an org, since
	// they'd hold no lock in common. In normal operation only one
	// unconsumed token ever exists (appliance/scripts/eami-stack.sh deletes
	// any prior one before minting a new one), but that's an operational
	// invariant enforced by a shell script, not a database guarantee -- the
	// brief requires a real DB-level lock regardless of how many tokens
	// happen to exist. A fixed advisory-lock key serializes every Bootstrap
	// transaction globally; it's automatically released on commit or
	// rollback, no manual unlock needed.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('eami_setup_bootstrap'))`); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not acquire setup lock")
		return
	}

	// Lock the token row too -- re-reads the committed consumed_at value
	// (not a stale snapshot) for a second request racing the same token.
	var tokenID uuid.UUID
	var expiresAt, consumedAt pgtype.Timestamptz
	err = tx.QueryRow(ctx,
		`SELECT id, expires_at, consumed_at FROM setup_tokens WHERE token_hash = $1 FOR UPDATE`,
		hashSetupToken(req.SetupToken),
	).Scan(&tokenID, &expiresAt, &consumedAt)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid setup token")
		return
	}
	if consumedAt.Valid {
		writeError(w, http.StatusUnauthorized, "unauthorized", "setup token has already been used")
		return
	}
	if time.Now().After(expiresAt.Time) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "setup token has expired")
		return
	}

	// Independent system-state check -- a valid token alone is not enough;
	// the appliance must also still be unconfigured. Safe from a
	// two-different-valid-tokens race specifically because of the advisory
	// lock acquired above, which every concurrent Bootstrap call must hold
	// before reaching this point.
	var orgCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM orgs`).Scan(&orgCount); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if orgCount > 0 {
		writeError(w, http.StatusConflict, "already_configured", "this appliance is already configured")
		return
	}

	orgID := uuid.New()
	slug := slugifyOrgName(req.OrgName, orgID)
	if _, err := tx.Exec(ctx,
		`INSERT INTO orgs (id, name, slug, plan) VALUES ($1, $2, $3, 'trial')`,
		orgID, req.OrgName, slug,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not create organization")
		return
	}

	adminID := uuid.New()
	if _, err := tx.Exec(ctx,
		`INSERT INTO users (id, org_id, email, name, role, password_hash) VALUES ($1, $2, $3, $4, 'admin', $5)`,
		adminID, orgID, req.AdminEmail, adminDisplayName(req.AdminEmail), passHash,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not create admin user")
		return
	}

	if _, err := tx.Exec(ctx, `UPDATE setup_tokens SET consumed_at = now() WHERE id = $1`, tokenID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not finalize setup token")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not finalize setup")
		return
	}

	writeJSON(w, http.StatusCreated, BootstrapResponse{
		OrgID:      orgID.String(),
		OrgName:    req.OrgName,
		AdminEmail: req.AdminEmail,
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func hashSetupToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// setupEmailPattern deliberately does not require a dot in the domain part
// (e.g. "admin@localhost" is accepted) -- matching zod's more permissive
// .email() check on the frontend (SetupWizardPage.tsx). A stricter server
// check that rejects what the client already accepted would bounce an
// admin back to re-entering the setup token over a cosmetic mismatch, not
// a real problem with the address.
var setupEmailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)

func isValidSetupEmail(email string) bool {
	return setupEmailPattern.MatchString(email)
}

// adminDisplayName mirrors scripts/setup.sh's own convention
// (${EAMI_ADMIN_EMAIL%%@*}) -- the local-part of the email as a display name.
func adminDisplayName(email string) string {
	if i := strings.Index(email, "@"); i > 0 {
		return email[:i]
	}
	return email
}

var slugInvalidChars = regexp.MustCompile(`[^a-z0-9]+`)

// slugifyOrgName mirrors scripts/setup.sh's slugify() (lowercase, collapse
// non-alphanumerics to hyphens, trim leading/trailing hyphens). Unlike
// setup.sh, collisions can't happen here -- Bootstrap only ever runs while
// orgs is verified empty in the same transaction -- but a name that slugifies
// to nothing (e.g. "!!!") still needs a fallback so the INSERT's NOT NULL
// slug column never gets an empty string.
func slugifyOrgName(name string, fallbackID uuid.UUID) string {
	s := strings.ToLower(name)
	s = slugInvalidChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "org-" + fallbackID.String()[:8]
	}
	return s
}
