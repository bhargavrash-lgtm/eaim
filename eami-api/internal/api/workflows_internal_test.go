// workflows_internal_test.go -- eami-api/internal/api
// Unit test for isForeignKeyViolation (B-058 code-review fix), mirroring
// tool_connectivity_test.go's TestClassifyPgConnectError_* pattern: a pure
// function tested directly against constructed *pgconn.PgError values, no
// live database needed.
//
// Run: go test -count=1 ./internal/api/... -run TestIsForeignKeyViolation
package api

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsForeignKeyViolation_RealFKCode(t *testing.T) {
	err := &pgconn.PgError{Code: "23503", Message: `insert or update on table "workflow_steps" violates foreign key constraint`}
	if !isForeignKeyViolation(err) {
		t.Errorf("SQLSTATE 23503 (foreign_key_violation): want true, got false")
	}
}

func TestIsForeignKeyViolation_OtherPgError(t *testing.T) {
	err := &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
	if isForeignKeyViolation(err) {
		t.Errorf("SQLSTATE 23505 (unique_violation, not FK): want false, got true")
	}
}

func TestIsForeignKeyViolation_NonPgError(t *testing.T) {
	if isForeignKeyViolation(errors.New("some other error")) {
		t.Error("a non-PgError: want false, got true")
	}
}
