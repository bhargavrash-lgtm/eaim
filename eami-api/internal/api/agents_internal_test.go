// agents_internal_test.go -- eami-api/internal/api
// Unit test for isUniqueViolation (B-074), mirroring
// workflows_internal_test.go's TestIsForeignKeyViolation_* pattern: a pure
// function tested directly against constructed *pgconn.PgError values, no
// live database needed.
//
// Run: go test -count=1 ./internal/api/... -run TestIsUniqueViolation
package api

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsUniqueViolation_RealUniqueCode(t *testing.T) {
	err := &pgconn.PgError{Code: "23505", Message: `duplicate key value violates unique constraint "gateway_agents_org_id_name_key"`}
	if !isUniqueViolation(err) {
		t.Errorf("SQLSTATE 23505 (unique_violation): want true, got false")
	}
}

func TestIsUniqueViolation_OtherPgError(t *testing.T) {
	err := &pgconn.PgError{Code: "23503", Message: "foreign key violation"}
	if isUniqueViolation(err) {
		t.Errorf("SQLSTATE 23503 (foreign_key_violation, not unique): want false, got true")
	}
}

func TestIsUniqueViolation_NonPgError(t *testing.T) {
	if isUniqueViolation(errors.New("some other error")) {
		t.Error("a non-PgError: want false, got true")
	}
}
