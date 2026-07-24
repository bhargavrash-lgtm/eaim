// auth_test.go — eami-api/internal/auth
// Unit tests for RSA key load-if-exists/generate-if-missing persistence
// (B-026): a JWT access token issued before a restart must remain valid
// after it, instead of every restart silently invalidating every
// outstanding token.
//
// Run: go test ./internal/auth/... -v
package auth_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eami/api/internal/auth"
	"github.com/google/uuid"
)

func TestNewService_GeneratesAndPersistsKey_WhenMissing(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "api.key")

	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("precondition: key file should not exist yet, got stat err=%v", err)
	}

	if _, err := auth.NewService(keyPath, time.Hour, 30*24*time.Hour); err != nil {
		t.Fatalf("NewService with missing key file: unexpected error: %v", err)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("NewService did not persist a key file at %s: %v", keyPath, err)
	}
	if info.Size() == 0 {
		t.Error("persisted key file is empty")
	}
}

func TestNewService_LoadsExistingKey_TokenValidAfterRestart(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "api.key")
	userID, orgID := uuid.New(), uuid.New()

	// Service A: fresh deployment, no key file yet.
	svcA, err := auth.NewService(keyPath, time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("NewService (A, generates key): %v", err)
	}
	token, _, err := svcA.IssueAccessToken(userID, orgID, "user@example.com", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	if _, err := svcA.VerifyAccessToken(token); err != nil {
		t.Fatalf("pre-restart VerifyAccessToken: %v", err)
	}

	// Service B: same key path, simulating a container restart.
	svcB, err := auth.NewService(keyPath, time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("NewService (B, loads existing key): %v", err)
	}

	// The token A issued must still verify against B -- proving B loaded
	// the same key A generated, rather than minting a new one.
	claims, err := svcB.VerifyAccessToken(token)
	if err != nil {
		t.Fatalf("token issued before restart rejected after restart: %v", err)
	}
	if claims.Subject != userID.String() {
		t.Errorf("claims.Subject = %q, want %q", claims.Subject, userID.String())
	}
}

func TestNewService_CorruptKeyFile_ReturnsError_NotOverwritten(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "api.key")
	garbage := []byte("this is not a PEM-encoded RSA key")
	if err := os.WriteFile(keyPath, garbage, 0600); err != nil {
		t.Fatalf("seed corrupt key file: %v", err)
	}

	if _, err := auth.NewService(keyPath, time.Hour, 30*24*time.Hour); err == nil {
		t.Fatal("NewService with a corrupt key file: expected error, got nil")
	}

	// Must not have silently regenerated over the corrupt file -- that would
	// mask disk corruption/tampering and itself invalidate every outstanding
	// token, the exact failure mode this fix exists to prevent.
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("re-read key file after failed NewService: %v", err)
	}
	if string(after) != string(garbage) {
		t.Error("corrupt key file was overwritten by NewService instead of returning an error")
	}
}

func TestNewService_EphemeralMode_EmptyPathStillWorks(t *testing.T) {
	svc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("NewService(\"\") (ephemeral dev mode): unexpected error: %v", err)
	}
	token, _, err := svc.IssueAccessToken(uuid.New(), uuid.New(), "user@example.com", "viewer")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	if _, err := svc.VerifyAccessToken(token); err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
}

func TestNewService_MissingParentDir_CreatesIt(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "nested", "dir", "api.key")

	if _, err := auth.NewService(keyPath, time.Hour, 30*24*time.Hour); err != nil {
		t.Fatalf("NewService with a not-yet-existing parent dir: unexpected error: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file not created under nested dir: %v", err)
	}
}
