// Real user provisioning: invite acceptance and password reset.
//
// invite_tokens / reset_tokens (schema/migrations-v2/000022_invite_reset_tokens)
// mirror setup_tokens (bootstrap.go) exactly: only a SHA-256 hash of the raw
// token is ever stored, consumed_at is set exactly once inside the same DB
// transaction that uses the token, and the real single-use/replay guard is a
// `SELECT ... FOR UPDATE` row lock at use time -- not an application-level
// check-then-act. See bootstrap.go's own package doc comment for the fuller
// reasoning behind that pattern; AcceptInvite/ResetPassword below follow it
// directly rather than inventing a second convention.
//
// No real email-sending capability exists anywhere in this codebase (grepped
// for SMTP/mail packages before writing this file -- none). InviteUser
// already returns its link directly to the inviting admin, who is expected
// to hand-deliver it -- an acceptable trust model since the admin is
// authenticated and already trusted with the invite in the first place.
// RequestPasswordReset is different: it's an unauthenticated, self-service
// endpoint, so it can NEVER return the reset link in its own HTTP response
// -- doing so would let anyone mint a working password-reset link for any
// email address they can guess, which is an account-takeover bug, not just
// an enumeration leak. Instead the generated link is written to the server
// log (log.Printf below) -- the same trust boundary bootstrap.go's own setup
// token already relies on (console/server access), honestly disclosed as a
// real limitation rather than implying email delivery that doesn't exist.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	authpkg "github.com/eami/api/internal/auth"
)

const (
	inviteTokenTTL = 48 * time.Hour
	resetTokenTTL  = 1 * time.Hour
	minPasswordLen = 8
)

// hashOpaqueToken is the same SHA-256-hex convention bootstrap.go's
// hashSetupToken already established for setup_tokens, reimplemented here
// (rather than calling bootstrap.go's private function directly) so this
// file's own token family stays self-contained -- same reasoning
// bootstrap_test.go itself already gives for not reaching into another
// file's unexported helper.
func hashOpaqueToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ── Accept invite ───────────────────────────────────────────────────────────

type AcceptInviteRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type AcceptInviteResp struct {
	Email string `json:"email"`
}

// AcceptInvite handles POST /v1/auth/accept-invite (unauthenticated -- the
// invitee has no session yet; the invite token itself is the credential).
// Sets password_hash for the invited user. No role change happens here --
// CreateInvitedUser (users.go) already writes the user's real target role
// at invite-creation time; the row never actually holds a literal
// 'invited' role value (only the now-removed invite JWT's claim used to
// say that), so there is nothing to "flip".
func (s *Server) AcceptInvite(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	if ok, retryAfter := s.provisioningLimiter.Allow(clientKey(r)); !ok {
		setRetryAfter(w, retryAfter)
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts -- try again later")
		return
	}
	var req AcceptInviteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "token is required")
		return
	}
	if len(req.Password) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "bad_request", "password must be at least 8 characters")
		return
	}

	ctx := r.Context()
	tx, err := s.queries.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not start transaction")
		return
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	// Lock the token row -- re-reads the committed consumed_at value for a
	// second request racing the same token, same as bootstrap.go's setup
	// token lock. Deliberately done BEFORE the (expensive, deliberately
	// slow) bcrypt hash below -- code review flagged the original ordering
	// as a CPU-exhaustion amplifier: hashing first meant every garbage
	// token guess paid a full bcrypt cost before the cheap lookup ever
	// rejected it.
	var tokenID, userID uuid.UUID
	var expiresAt, consumedAt pgtype.Timestamptz
	err = tx.QueryRow(ctx,
		`SELECT id, user_id, expires_at, consumed_at FROM invite_tokens WHERE token_hash = $1 FOR UPDATE`,
		hashOpaqueToken(req.Token),
	).Scan(&tokenID, &userID, &expiresAt, &consumedAt)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid invite token")
		return
	}
	if consumedAt.Valid {
		writeError(w, http.StatusUnauthorized, "unauthorized", "this invite has already been used")
		return
	}
	if time.Now().After(expiresAt.Time) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "this invite has expired")
		return
	}

	// Independent state check on the bound user, inside the same lock --
	// same belt-and-suspenders pattern as bootstrap.go's orgCount check on
	// top of its own token check. password_hash IS NULL is the real
	// "not yet accepted" signal (consumed_at above already guards this in
	// the normal path; this catches a row mutated some other way, e.g. a
	// user who somehow already has a password set for a still-unconsumed
	// token). deleted_at guards a user deactivated between invite and
	// acceptance.
	var existingHash pgtype.Text
	var deletedAt pgtype.Timestamptz
	var email string
	err = tx.QueryRow(ctx,
		`SELECT password_hash, deleted_at, email FROM users WHERE id = $1 FOR UPDATE`,
		userID,
	).Scan(&existingHash, &deletedAt, &email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid invite token")
		return
	}
	if deletedAt.Valid {
		writeError(w, http.StatusUnauthorized, "unauthorized", "this invite is no longer valid")
		return
	}
	if existingHash.Valid {
		writeError(w, http.StatusConflict, "already_accepted", "this invite has already been accepted")
		return
	}

	// Only now, once the token and its bound user are both confirmed
	// valid, is the expensive bcrypt hash paid.
	passHash, err := authpkg.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not hash password")
		return
	}

	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, passHash, userID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not set password")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE invite_tokens SET consumed_at = now() WHERE id = $1`, tokenID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not finalize invite")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not finalize invite")
		return
	}

	writeJSON(w, http.StatusOK, AcceptInviteResp{Email: email})
}

// ── Password reset ──────────────────────────────────────────────────────────

type RequestPasswordResetRequest struct {
	Email string `json:"email"`
}

type RequestPasswordResetResp struct {
	Status string `json:"status"`
}

// RequestPasswordReset handles POST /v1/auth/request-reset (unauthenticated).
// Always returns the identical 200 response regardless of whether the
// account exists -- the standard anti-enumeration convention -- and NEVER
// returns the generated token in the response body; see this file's own
// package doc comment for why. When a real, active (non-SSO,
// non-deactivated) account exists for the given email, a reset token is
// minted and logged server-side.
func (s *Server) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	// Applied uniformly before anything account-specific happens -- an
	// IP-keyed limit that doesn't depend on whether the email exists is
	// not an enumeration oracle, unlike a per-account limiter would be
	// here.
	if ok, retryAfter := s.provisioningLimiter.Allow(clientKey(r)); !ok {
		setRetryAfter(w, retryAfter)
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts -- try again later")
		return
	}
	var req RequestPasswordResetRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	email := strings.TrimSpace(req.Email)

	if s.queries != nil && email != "" {
		if u, err := s.queries.GetUserByEmail(r.Context(), email); err == nil && u.PasswordHash.Valid {
			s.issueResetToken(r, u.ID, u.Email)
		}
		// A lookup miss, an SSO-only account, or an empty email all fall
		// through silently -- same response either way, below.
	}

	writeJSON(w, http.StatusOK, RequestPasswordResetResp{Status: "ok"})
}

// issueResetToken mints and persists a reset token for userID, invalidating
// any previously-issued, still-unconsumed reset tokens for that user first
// (so only the most recently requested link ever works -- a stale link from
// an earlier request a user forgot about shouldn't remain a live credential
// indefinitely). The invalidate + insert run inside one transaction --
// code review caught that two separate, non-transactional Exec calls let
// two overlapping requests both pass the invalidate step before either
// insert committed, leaving two simultaneously-valid tokens and silently
// breaking this function's own "only the latest link works" guarantee.
// Logs the raw link server-side -- see package doc comment.
func (s *Server) issueResetToken(r *http.Request, userID uuid.UUID, email string) {
	raw, hash, err := authpkg.IssueRefreshToken() // generic random-token+hash helper, not refresh-token-specific
	if err != nil {
		log.Printf("password reset: could not generate token for user_id=%s: %v", userID, err)
		return
	}
	ctx := r.Context()
	tx, err := s.queries.Begin(ctx)
	if err != nil {
		log.Printf("password reset: could not start transaction for user_id=%s: %v", userID, err)
		return
	}
	defer tx.Rollback(ctx)

	// Per-user advisory lock, held for the transaction's lifetime -- same
	// technique as bootstrap.go's global pg_advisory_xact_lock, scoped to
	// this one user_id instead of globally. Without it, two genuinely
	// concurrent requests could each run the invalidate UPDATE against a
	// snapshot with zero prior unconsumed rows (neither sees the other's
	// not-yet-inserted row, so neither has anything to invalidate) and
	// both then INSERT, leaving two simultaneously-valid tokens -- a plain
	// transaction alone doesn't serialize two independent INSERTs the way
	// it does two UPDATEs racing the same existing row.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "reset_token:"+userID.String()); err != nil {
		log.Printf("password reset: could not acquire per-user lock for user_id=%s: %v", userID, err)
		return
	}

	if _, err := tx.Exec(ctx,
		`UPDATE reset_tokens SET consumed_at = now() WHERE user_id = $1 AND consumed_at IS NULL`, userID,
	); err != nil {
		log.Printf("password reset: could not invalidate prior tokens for user_id=%s: %v", userID, err)
		return
	}
	expiresAt := time.Now().Add(resetTokenTTL)
	if _, err := tx.Exec(ctx,
		`INSERT INTO reset_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, hash, expiresAt,
	); err != nil {
		log.Printf("password reset: could not persist token for user_id=%s: %v", userID, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("password reset: could not finalize token for user_id=%s: %v", userID, err)
		return
	}
	log.Printf("password reset requested for user_id=%s email=%s: reset_link=/reset-password?token=%s (expires %s) -- no email delivery configured, deliver this link out-of-band",
		userID, email, raw, expiresAt.Format(time.RFC3339))
}

type ResetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// ResetPassword handles POST /v1/auth/reset-password (unauthenticated --
// the reset token itself is the credential). Structurally identical to
// AcceptInvite: row-locked token lookup, consumed_at + expiry checks, the
// target user_id comes only from the token row (never from client input,
// so a token for one user can never be used to set another user's
// password), single UPDATE + consume inside one transaction.
func (s *Server) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireQueries(w) {
		return
	}
	if ok, retryAfter := s.provisioningLimiter.Allow(clientKey(r)); !ok {
		setRetryAfter(w, retryAfter)
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts -- try again later")
		return
	}
	var req ResetPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "token is required")
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "bad_request", "new_password must be at least 8 characters")
		return
	}

	ctx := r.Context()
	tx, err := s.queries.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not start transaction")
		return
	}
	defer tx.Rollback(ctx)

	// Token + user validated BEFORE the expensive bcrypt hash below -- same
	// fix, same reasoning as AcceptInvite above (code review finding: a
	// CPU-exhaustion amplifier if hashing runs before the cheap lookup can
	// reject a garbage token).
	var tokenID, userID uuid.UUID
	var expiresAt, consumedAt pgtype.Timestamptz
	err = tx.QueryRow(ctx,
		`SELECT id, user_id, expires_at, consumed_at FROM reset_tokens WHERE token_hash = $1 FOR UPDATE`,
		hashOpaqueToken(req.Token),
	).Scan(&tokenID, &userID, &expiresAt, &consumedAt)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid reset token")
		return
	}
	if consumedAt.Valid {
		writeError(w, http.StatusUnauthorized, "unauthorized", "this reset link has already been used")
		return
	}
	if time.Now().After(expiresAt.Time) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "this reset link has expired")
		return
	}

	var deletedAt pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `SELECT deleted_at FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&deletedAt); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid reset token")
		return
	}
	if deletedAt.Valid {
		writeError(w, http.StatusUnauthorized, "unauthorized", "this reset link is no longer valid")
		return
	}

	passHash, err := authpkg.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not hash password")
		return
	}

	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, passHash, userID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not set password")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE reset_tokens SET consumed_at = now() WHERE id = $1`, tokenID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not finalize reset")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not finalize reset")
		return
	}

	writeJSON(w, http.StatusOK, RequestPasswordResetResp{Status: "ok"})
}
