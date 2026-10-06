package api

import (
	"net/http"

	"github.com/eami/api/internal/store"
	"github.com/google/uuid"
)

// Test-only exports for admin_audit_test.go (package api_test). Slice 0b
// ships these helpers for Slice 1's handlers; nothing calls them yet.

// WriteAdminAuditFailureForTest exposes writeAdminAuditFailure.
func WriteAdminAuditFailureForTest(w http.ResponseWriter, r *http.Request, err error) bool {
	return writeAdminAuditFailure(w, r, err)
}

// AdminAuditActorForTest exposes adminAuditActor.
func AdminAuditActorForTest(userID, orgID uuid.UUID, email, role string) store.AdminAuditActor {
	return adminAuditActor(userClaims{UserID: userID, OrgID: orgID, Email: email, Role: role})
}

// AdminAuditRequestIDForTest exposes adminAuditRequestID.
func AdminAuditRequestIDForTest(r *http.Request) string { return adminAuditRequestID(r) }
