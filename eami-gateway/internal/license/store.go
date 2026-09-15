package license

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// errOrgMismatch is returned when a licenses row's org_id column doesn't
// match the org_id signed inside its own raw_license JWT -- see
// currentClaims's own doc comment for why this check exists.
var errOrgMismatch = errors.New("license: row org_id does not match the license's own signed org_id")

// Store resolves an org's current license directly against Postgres and
// independently re-verifies it via Verify -- eami-gateway never trusts
// eami-api's already-decoded licenses.modules/valid_until columns as the
// real answer; those exist for eami-api's own display convenience. This
// is the actual behavior the B-157 investigation and this brief's own
// design both call for: a privileged direct-DB write that hand-crafted a
// licenses row with fabricated columns (bypassing eami-api's upload
// handler entirely) is still caught here, since ModuleLicensed only ever
// trusts what Verify says about the row's real raw_license.
type Store struct {
	pool *pgxpool.Pool
	// usageSafetyMarginPct/inflightTTLSeconds (B-173) configure
	// ReserveIfNearLimit's near-limit concurrency guard -- see that
	// method's own doc comment. Threaded through from cmd/gateway's own
	// config (internal/config.LicensingConfig) rather than hardcoded: an
	// explicit user decision made when this fix's design was scoped --
	// the safety margin is a genuine operational tuning knob, not a fixed
	// constant this package should own silently.
	usageSafetyMarginPct int
	inflightTTLSeconds   int
}

// New creates a license Store backed by pool -- the same real Postgres
// pool eami-gateway already uses for gateway_tools/policies/audit_log,
// not a separate connection or a call to eami-api. usageSafetyMarginPct/
// inflightTTLSeconds configure ReserveIfNearLimit (B-173); see its own doc
// comment.
func New(pool *pgxpool.Pool, usageSafetyMarginPct, inflightTTLSeconds int) *Store {
	return &Store{pool: pool, usageSafetyMarginPct: usageSafetyMarginPct, inflightTTLSeconds: inflightTTLSeconds}
}

// ModuleLicensed reports whether orgID's current, independently-
// re-verified license includes module. Fails closed on every branch: no
// license row at all, a row whose raw_license fails Verify (expired,
// tampered, wrong org, wrong issuer/audience), or a genuine DB error are
// all treated identically -- false, never true by default.
func (s *Store) ModuleLicensed(ctx context.Context, orgID, module string) bool {
	claims, err := s.currentClaims(ctx, orgID)
	if err != nil {
		return false
	}
	return claims.HasModule(module)
}

// currentClaims fetches the most recently uploaded license row for orgID
// and re-verifies it fresh -- never cached, never trusted from the row's
// own denormalized columns. Returns an error for "no license at all,"
// "the DB query itself failed," "the stored raw_license fails
// verification," and "the row's org_id column doesn't match the license's
// own signed org_id" alike -- ModuleLicensed's own fail-closed contract
// doesn't need to distinguish these for its boolean answer, but a future
// caller that DOES need to (e.g. Dispatch's own rejection message) can
// still inspect the returned error.
//
// The org_id re-check (security review finding, this brief): a genuinely
// -signed license belonging to a DIFFERENT org (e.g. read from a shared
// Postgres, a backup, or an MSP's other customer appliance) could
// otherwise be direct-DB-inserted with org_id relabeled to the attacker's
// own org -- Verify() alone has no way to know which org_id the caller
// expects, so this package, not the JWT library, is responsible for
// enforcing that binding. ORDER BY created_at DESC, id DESC (tiebreaker
// added per code review): created_at has no DB-level uniqueness
// guarantee, so two rows inserted in the same instant would otherwise
// make "most recent" ambiguous on a security-relevant gate.
func (s *Store) currentClaims(ctx context.Context, orgID string) (*Claims, error) {
	var rawLicense string
	err := s.pool.QueryRow(ctx, `
		SELECT raw_license FROM licenses
		WHERE org_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, orgID).Scan(&rawLicense)
	if err != nil {
		// pgx.ErrNoRows ("no license uploaded at all") and any other real
		// query error are both real reasons to fail closed -- neither is
		// distinguished here, matching ModuleLicensed's own contract.
		return nil, err
	}
	claims, err := Verify(rawLicense)
	if err != nil {
		return nil, err
	}
	if claims.OrgID() != orgID {
		return nil, errOrgMismatch
	}
	return claims, nil
}

// WithinUsageLimit reports whether orgID's current, real token usage this
// calendar month is still within its currently-licensed usage_limits
// (B-157 epic, Brief 2). Re-verifies the license fresh via currentClaims,
// the same trust boundary ModuleLicensed already established -- this
// package never trusts eami-api's already-decoded licenses.usage_limits
// column for an enforcement decision, only the JWT's own signed claim.
//
// A license with no usage_limits claim at all, or one whose
// MaxTokensPerMonth is unset, is unlimited -- true. A license that fails
// to resolve at all (no row, verification failure, org mismatch, DB
// error) is NOT this method's concern: Dispatch already gates on
// ModuleLicensed first, and a caller that reaches this method despite
// ModuleLicensed having failed gets the same fail-closed answer (false)
// rather than an unlimited default, since "we can't tell" must never mean
// "assume no limit."
//
// currentUsage queries the SAME token_usage table/columns
// (org_id/tokens_in/tokens_out/recorded_at) B-097/108/111/112's own
// eami-api FinOps aggregation queries already established -- not a new,
// separate counting mechanism. A rolling calendar month (first-of-month
// 00:00 UTC through now), not a rolling 30-day window: simple, matches
// how a monthly volume entitlement is conventionally understood, and
// needs no extra state to track a "period start" anywhere.
//
// Known, disclosed limitation (code-review finding, this brief, logged as
// B-173 rather than fixed here): this is a check-THEN-act read against
// token_usage, and token_usage is populated by main.go's own pre-existing
// (B-099) `go safeWriteTokenUsage(...)` -- a fire-and-forget goroutine
// that HTTP-POSTs to eami-api asynchronously AFTER a dispatch completes,
// not a synchronous write inside Dispatch itself. A burst of rapid or
// concurrent dispatch calls can each query this SUM before any of the
// PRIOR calls' own usage has actually landed, letting an org overshoot
// MaxTokensPerMonth by an unbounded amount during that lag window. This
// is a real gap beyond the inherent, unavoidable "a call's own token
// count isn't known until the provider responds, so the call that
// crosses the limit is itself never preventable" limitation every
// token-based quota system shares (already accounted for in the caller's
// own design) -- fixing it for real would mean either making token_usage
// writes synchronous (a hot-dispatch-path latency/architecture change
// well beyond this brief's own scope) or a separate real-time reservation
// counter (new counting machinery, which this brief's own brief
// explicitly said not to build). Left as a known soft limit, not a hard
// one, until B-173 is picked up.
func (s *Store) WithinUsageLimit(ctx context.Context, orgID string) (bool, error) {
	claims, err := s.currentClaims(ctx, orgID)
	if err != nil {
		return false, err
	}
	if claims.UsageLimits == nil || claims.UsageLimits.MaxTokensPerMonth == nil {
		return true, nil
	}
	limit := *claims.UsageLimits.MaxTokensPerMonth

	var used int64
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(tokens_in + tokens_out), 0)
		FROM token_usage
		WHERE org_id = $1
		  AND recorded_at >= date_trunc('month', now())
	`, orgID).Scan(&used)
	if err != nil {
		return false, fmt.Errorf("license: usage limit query failed: %w", err)
	}
	return used < limit, nil
}

// ReserveIfNearLimit narrows the concurrency race WithinUsageLimit's own
// doc comment discloses as B-173: even a fresh, correct SUM read there is
// a check-THEN-act against token_usage, which main.go's own B-099 fire-
// and-forget goroutine populates asynchronously, well after a dispatch
// actually completes -- a burst of concurrent/rapid dispatches can each
// observe "under limit" before any of the PRIOR calls' own usage has
// landed, letting an org collectively overshoot MaxTokensPerMonth.
//
// A per-call token estimate can't close this honestly: the real cost of a
// not-yet-executed call isn't knowable in advance, and this gate applies
// uniformly to every dispatch -- rest_api tool calls included, most of
// which have no token concept at all -- so there is no caller-declared
// estimate (e.g. an LLM request's own max_tokens field) this package could
// generally rely on without inventing an arbitrary number for the common
// case. WithinUsageLimit's own doc comment already accepts "a call's own
// token count isn't known until the provider responds" as an inherent,
// unavoidable limitation of any token-based quota; a single very large
// call can still push an org over a small remaining cap no matter what
// this method does, and that is NOT what this method targets.
//
// What IS fully solvable is the narrower problem: a genuine concurrent
// RACE near the cap, several dispatches passing WithinUsageLimit's check
// in the same narrow window while none of their usage has landed yet. This
// is a deliberate design choice, not a compromise landed on by default --
// two alternatives (a flat per-call token estimate reserved on every
// dispatch; an ai_provider-specific estimate derived from a request's own
// max_tokens field) were both explicitly considered and rejected in favor
// of this one: see BACKLOG.md's B-173 entry for the full reasoning.
//
// Only engages once usage is within usageSafetyMarginPct percent of the
// license's own limit -- comfortably-under-cap dispatches (the overwhelming
// majority) pay no extra serialization or DB round trip beyond what this
// check itself costs. Once in that zone, at most one in-flight (reserved,
// not yet confirmed landed) dispatch is allowed per org at a time; a
// second concurrent dispatch racing the same near-exhausted limit is
// refused, not silently allowed through on a stale read.
//
// Must be called only AFTER WithinUsageLimit has already returned true --
// this method does not itself re-check "is used < limit", only "is used
// near the limit, and if so, is another dispatch already in flight."
// Callers MUST invoke the returned release func exactly once, once the
// dispatch this reservation covers has genuinely finished (success or
// failure) -- via `defer`, mirroring this codebase's usual resource-release
// convention. release is never nil (a caller never needs its own nil
// check): a no-op when no reservation was actually taken (an unlimited
// license, or usage comfortably under the margin).
//
// Reservations self-expire after inflightTTLSeconds regardless of whether
// release is ever called -- the safety net for a crashed/panicked dispatch
// goroutine that never reaches its own defer, so one lost reservation can
// never permanently wedge an org's near-cap dispatches.
func (s *Store) ReserveIfNearLimit(ctx context.Context, orgID string) (release func(), ok bool, err error) {
	noop := func() {}

	claims, err := s.currentClaims(ctx, orgID)
	if err != nil {
		return noop, false, err
	}
	if claims.UsageLimits == nil || claims.UsageLimits.MaxTokensPerMonth == nil {
		return noop, true, nil // unlimited -- mirrors WithinUsageLimit's own contract
	}
	limit := *claims.UsageLimits.MaxTokensPerMonth

	margin := limit * int64(s.usageSafetyMarginPct) / 100
	if margin < 0 {
		margin = 0
	}
	threshold := limit - margin
	if threshold < 0 {
		threshold = 0
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return noop, false, fmt.Errorf("license: begin usage reservation tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	// Org-scoped advisory lock, namespaced with a "usage_inflight:" prefix
	// so this never collides with eami-api/internal/api/license.go's own
	// unprefixed hashtext(orgID) upload-race lock -- a usage-limit check
	// for one org must never unnecessarily serialize against a license
	// upload for that same org.
	//
	// Security review finding (this brief, informational, not fixed): like
	// every other hashtext(orgID)-keyed lock already in this codebase
	// (eami-api/internal/api/bootstrap.go, license.go's own upload lock),
	// this is a 32-bit hash -- two different orgIDs could theoretically
	// collide, causing one org's check to spuriously serialize behind (and
	// occasionally lose a contention race to) an unrelated org's. This
	// cannot leak data (every query below still filters on the real
	// org_id) and isn't attacker-targetable without a preimage search; it
	// inherits an existing, already-accepted characteristic of this
	// codebase's advisory-lock convention rather than introducing a new
	// one, so it's not treated as its own fix here.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "usage_inflight:"+orgID); err != nil {
		return noop, false, fmt.Errorf("license: acquire usage reservation lock: %w", err)
	}

	var used int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(tokens_in + tokens_out), 0)
		FROM token_usage
		WHERE org_id = $1
		  AND recorded_at >= date_trunc('month', now())
	`, orgID).Scan(&used); err != nil {
		return noop, false, fmt.Errorf("license: usage reservation SUM query failed: %w", err)
	}

	if used < threshold {
		// Comfortably under the cap -- no serialization needed.
		if err := tx.Commit(ctx); err != nil {
			return noop, false, fmt.Errorf("license: commit usage reservation check: %w", err)
		}
		return noop, true, nil
	}

	var inflightCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM usage_dispatch_inflight
		WHERE org_id = $1 AND expires_at > now()
	`, orgID).Scan(&inflightCount); err != nil {
		return noop, false, fmt.Errorf("license: usage in-flight count query failed: %w", err)
	}
	if inflightCount > 0 {
		if err := tx.Commit(ctx); err != nil {
			return noop, false, fmt.Errorf("license: commit usage reservation contention check: %w", err)
		}
		// Another dispatch is already in flight near this org's cap --
		// fail closed on the SIDE OF CONTENTION, not an error: this is the
		// expected, correctly-enforced outcome of the race this method
		// exists to close, not a failure of the mechanism itself.
		return noop, false, nil
	}

	var reservationID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO usage_dispatch_inflight (org_id, expires_at)
		VALUES ($1, now() + make_interval(secs => $2))
		RETURNING id
	`, orgID, s.inflightTTLSeconds).Scan(&reservationID); err != nil {
		return noop, false, fmt.Errorf("license: insert usage reservation failed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return noop, false, fmt.Errorf("license: commit usage reservation: %w", err)
	}

	release = func() {
		// Best-effort: a failed release here only means this reservation
		// falls back to expiring via inflightTTLSeconds instead of being
		// cleared immediately -- never worth failing or blocking the real
		// dispatch outcome over. Own short-lived background context, not
		// the caller's ctx: release typically runs from a defer after the
		// caller's own ctx may already be winding down.
		delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.pool.Exec(delCtx, `DELETE FROM usage_dispatch_inflight WHERE id = $1`, reservationID); err != nil {
			slog.Warn("license: failed to release usage in-flight reservation", "org_id", orgID, "reservation_id", reservationID, "err", err)
		}
	}
	return release, true, nil
}
