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

	row, err := s.queries.CreateLicense(r.Context(), store.CreateLicenseParams{
		OrgID:      uc.OrgID,
		RawLicense: body.RawLicense,
		Modules:    claims.Modules,
		ValidFrom:  validFrom,
		ValidUntil: claims.ExpiresAt.Time,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
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
	writeJSON(w, http.StatusOK, licenseRowToResp(row))
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
