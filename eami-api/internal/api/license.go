// license.go -- eami-api/internal/api
//
// Modular licensing & entitlement system, Brief 1 of 3 (B-157 epic, built
// as B-169). Renewal reuses the LESSON from the first-boot setup wizard
// (bootstrap.go) -- an admin action through the running web application,
// persisted to Postgres, never a file the appliance itself has to trust --
// not its literal code: bootstrap.go's one-time console-token mechanism
// solves a different problem (creating the very first org, before any
// user or JWT session exists) that doesn't apply here, since uploading or
// renewing a license is an ordinary authenticated admin action against an
// org that already exists.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/eami/api/internal/license"
	"github.com/eami/api/internal/store"
)

// validModules is the fixed set of real module identifiers this product
// currently defines (B-157's own "Module 1/2/3" naming). Module 3 (AI
// Infrastructure) is included here -- a license CAN legitimately list it
// as purchased -- even though no enforcement surface exists for it yet
// (per this brief's own explicit OUT OF SCOPE: "Any Module 3 enforcement
// (doesn't exist yet)"); rejecting it at upload time would incorrectly
// prevent a real, valid future-dated license from ever being accepted.
var validModules = map[string]bool{"discovery": true, "gateway": true, "ai_infrastructure": true}

type UploadLicenseRequest struct {
	RawLicense string `json:"raw_license"`
}

type LicenseResp struct {
	Modules    []string  `json:"modules"`
	ValidFrom  time.Time `json:"valid_from"`
	ValidUntil time.Time `json:"valid_until"`
	// Status is "active" (currently within its validity window) or
	// "expired" -- both are shown, never silently equated: an admin
	// looking at Settings must be able to tell "we have a license but it
	// lapsed" from "we never had one" (the latter is a 404, not a 200
	// with an empty/absent body -- see GetLicense below).
	Status string `json:"status"`
}

// UploadLicense handles POST /v1/settings/license (admin-only). Verifies
// the submitted raw license entirely offline (license.Verify, no network
// call anywhere in this handler) before ever touching Postgres -- a
// license that fails verification is never persisted, partially or
// otherwise. Always a new row (CreateLicense is INSERT-only, matching
// the licenses table's own append-only design): a renewal or a module
// change is a new, real, historical event, never an overwrite.
func (s *Server) UploadLicense(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)

	var body UploadLicenseRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if body.RawLicense == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "raw_license is required")
		return
	}

	claims, err := license.Verify(body.RawLicense)
	if err != nil {
		// Security review finding (this brief): the raw jwt/v5 error text
		// distinguishes bad-signature from expired from wrong-issuer/
		// audience from malformed input -- useful for debugging but an
		// oracle an attacker iterating on forgery attempts shouldn't get
		// for free. Logged in full server-side; the client only ever sees
		// one generic message, matching requireModuleLicensed's own
		// "don't distinguish reasons in the response" discipline.
		slog.Warn("license: upload verification failed", "org_id", uc.OrgID, "err", err)
		writeError(w, http.StatusBadRequest, "invalid_license", "license could not be verified")
		return
	}
	// The license's own signed org_id (Subject) must match the uploading
	// admin's actual org -- never trust a client-supplied org_id for
	// this (there isn't one in the request body at all, deliberately),
	// and never silently accept org A's admin installing a license
	// signed for org B, which would misattribute entitlements across
	// tenants even though the signature itself is genuinely valid.
	if claims.OrgID() != uc.OrgID.String() {
		writeError(w, http.StatusBadRequest, "invalid_license", "this license was not issued for your organization")
		return
	}
	for _, m := range claims.Modules {
		if !validModules[m] {
			writeError(w, http.StatusBadRequest, "invalid_license", "license names an unrecognized module: "+m)
			return
		}
	}

	// claims.ExpiresAt is guaranteed non-nil here -- license.Verify itself
	// now enforces jwt.WithExpirationRequired() (security review finding,
	// this brief: the presence check used to live only here, meaning a
	// caller that used Verify directly, like Store.ModuleLicensed and
	// requireModuleLicensed both do, never got it).
	validFrom := time.Now().UTC()
	if claims.NotBefore != nil {
		validFrom = claims.NotBefore.Time
	}

	// usage_limits (B-157 epic, Brief 2) -- the license's own signed claim,
	// re-marshaled for the licenses.usage_limits JSONB column. Nil when
	// the license sets no volume cap, matching the column's own
	// nullability -- never a fabricated {} standing in for "unlimited".
	var usageLimitsJSON []byte
	if claims.UsageLimits != nil {
		usageLimitsJSON, err = json.Marshal(claims.UsageLimits)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to encode usage limits")
			return
		}
	}

	// license_events (B-157 epic, Brief 2): "created" if this org has never
	// uploaded a license before, "renewed" otherwise -- determined by
	// checking for a prior row before this brief's own new INSERT.
	//
	// Code-review finding (this brief): GetLatestLicense-then-CreateLicense
	// is a check-then-act race if run as two independent statements --two
	// near-simultaneous uploads for the SAME org (a double-submit, a
	// retried request, two admins acting at once) could both observe
	// pgx.ErrNoRows and both record "created", instead of one "created" +
	// one "renewed". Fixed by wrapping the check, the insert, and the
	// event record in one real transaction, serialized by a real
	// org-scoped Postgres advisory lock -- mirrors bootstrap.go's own
	// identical "check-then-act needs a real DB-level lock, not just
	// sequential Go statements" fix for the setup-token consume race.
	// hashtext(orgID-as-text), not a fixed global key: uploads for
	// DIFFERENT orgs must not serialize against each other.
	tx, err := s.queries.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not start license upload transaction")
		return
	}
	defer tx.Rollback(r.Context()) // no-op once Commit has succeeded

	if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtext($1))`, uc.OrgID.String()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not acquire license upload lock")
		return
	}
	qtx := s.queries.WithTx(tx)

	eventType := "renewed"
	if _, err := qtx.GetLatestLicense(r.Context(), uc.OrgID); err != nil {
		if err == pgx.ErrNoRows {
			eventType = "created"
		} else {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}

	row, err := qtx.CreateLicense(r.Context(), store.CreateLicenseParams{
		OrgID:       uc.OrgID,
		RawLicense:  body.RawLicense,
		Modules:     claims.Modules,
		ValidFrom:   validFrom,
		ValidUntil:  claims.ExpiresAt.Time,
		UsageLimits: usageLimitsJSON,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	// The license_events write happens INSIDE the same transaction (unlike
	// agents.go's InsertAgentLifecycleEvent, which is a genuinely separate
	// best-effort statement after its own primary action commits) --
	// deliberately: this row's own eventType was computed under the lock
	// this transaction holds, so it must commit atomically with the
	// license row it describes, or not at all. A failure here still rolls
	// back the whole upload rather than leaving a license row with no
	// corresponding event -- a stricter contract than agents.go's, chosen
	// because this event IS the audit trail this brief exists to guarantee,
	// not a secondary side record.
	performedBy := uc.UserID
	if err := qtx.InsertLicenseEvent(r.Context(), store.InsertLicenseEventParams{
		OrgID: uc.OrgID, LicenseID: row.ID, EventType: eventType, PerformedBy: &performedBy,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "license uploaded but failed to record its audit event: "+err.Error())
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not commit license upload")
		return
	}
	writeJSON(w, http.StatusCreated, licenseRowToResp(row))
}

// GetLicense handles GET /v1/settings/license. Re-verifies the stored
// raw_license on every read (not just trusting the cached valid_until
// column) so Status always reflects the license's real, current
// validity -- the same "raw_license is the source of truth" principle
// the migration's own doc comment establishes.
func (s *Server) GetLicense(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	row, err := s.queries.GetLatestLicense(r.Context(), uc.OrgID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "no license has been uploaded for this organization")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	resp := licenseRowToResp(row)
	// license_events (B-157 epic, Brief 2): "expired" has no human action
	// to hang off of -- detected here, the first time a status check
	// observes it, rather than by any background job (none exists on this
	// on-prem/air-gapped appliance, per ADR-020). InsertLicenseEvent's own
	// ON CONFLICT DO NOTHING (license_id, event_type) makes this
	// idempotent: repeated GETs against the same already-expired license
	// row record exactly one event, not one per request. Best-effort,
	// same non-fatal convention as UploadLicense's own record above --
	// a status check must not fail because this record did.
	if resp.Status == "expired" {
		if err := s.queries.InsertLicenseEvent(r.Context(), store.InsertLicenseEventParams{
			OrgID: uc.OrgID, LicenseID: row.ID, EventType: "expired",
		}); err != nil {
			slog.Error("license: failed to record license_events expired row", "org_id", uc.OrgID, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func licenseRowToResp(row store.License) LicenseResp {
	status := "expired"
	if _, err := license.Verify(row.RawLicense); err == nil {
		status = "active"
	}
	return LicenseResp{
		Modules:    row.Modules,
		ValidFrom:  row.ValidFrom,
		ValidUntil: row.ValidUntil,
		Status:     status,
	}
}
