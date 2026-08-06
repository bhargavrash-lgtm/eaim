// tools_update_pg_test.go -- eami-api/internal/api
// Real-Postgres integration test for UpdateTool's credential-preservation
// mechanism (B-045), following paste_events_test.go's established
// TEST_DATABASE_URL/POSTGRES_PASSWORD convention.
//
// This exists specifically because tools_update_test.go's handler-level
// tests all run against fakeToolStore -- they prove the HTTP handler
// passes a nil CredentialsEncrypted to the store when credentials are
// omitted, but not that pgx/Postgres's COALESCE($7, credentials_encrypted)
// actually preserves the real encrypted bytea value end-to-end. Flagged by
// this task's own security review as a real coverage gap, not a
// hypothetical one -- closed here.
//
// Run against the project's docker-compose Postgres:
//   docker compose up -d postgres
//   POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestUpdateTool_RealDB -v
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/store"
	"github.com/eami/api/internal/toolcreds"
)

func toolsUpdateTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/localhost:5432 layout) to run UpdateTool integration tests against a real Postgres")
	}
	return fmt.Sprintf("postgresql://eami_app:%s@localhost:5432/eami", pw)
}

const toolsUpdateTestKeyHex = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// TestUpdateTool_RealDB_CredentialsPreservedWhenOmitted proves AC2 against
// real Postgres: a name-only update (CredentialsEncrypted: nil) leaves the
// stored, real, decryptable credential exactly as it was.
func TestUpdateTool_RealDB_CredentialsPreservedWhenOmitted(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	defer pool.Close()

	q := store.New(pool)
	cipher, err := toolcreds.NewCipher(toolsUpdateTestKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "tools-update-test-"+orgID.String()[:8], "tools-update-test-"+orgID.String()); err != nil {
		t.Fatalf("seed test org: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID)

	origPlaintext, _ := json.Marshal(map[string]string{"api_key": "sk-original-real-secret"})
	origEncrypted, err := cipher.Encrypt(origPlaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	created, err := q.CreateTool(ctx, store.CreateToolParams{
		OrgID: orgID, Name: "before-edit", Type: "rest_api", AuthType: "api_key",
		BaseURL: strPtrTest("https://example.com"), CredentialsEncrypted: origEncrypted,
	})
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}

	// Real UpdateTool call, name-only -- the exact code path
	// tools.go's UpdateTool handler exercises for an omitted `credentials`
	// field.
	newName := "after-edit"
	updated, err := q.UpdateTool(ctx, store.UpdateToolParams{
		ID: created.ID, OrgID: orgID,
		Name:                 &newName,
		CredentialsEncrypted: nil, // omitted, per credentialsProvided()'s contract
	})
	if err != nil {
		t.Fatalf("UpdateTool: %v", err)
	}
	if updated.Name != "after-edit" {
		t.Errorf("Name = %q, want after-edit", updated.Name)
	}

	// Read the real stored bytes back directly (GatewayTool has no field
	// for them, by design -- same reason tools.go reads via a raw query).
	var storedEncrypted []byte
	if err := pool.QueryRow(ctx, `SELECT credentials_encrypted FROM gateway_tools WHERE id = $1`, created.ID).Scan(&storedEncrypted); err != nil {
		t.Fatalf("read back credentials_encrypted: %v", err)
	}

	decrypted, err := cipher.Decrypt(storedEncrypted)
	if err != nil {
		t.Fatalf("Decrypt: the stored credential is no longer decryptable after a name-only edit: %v", err)
	}
	if string(decrypted) != string(origPlaintext) {
		t.Errorf("decrypted credential after edit = %q, want the original %q (unchanged)", decrypted, origPlaintext)
	}
}

// TestUpdateTool_RealDB_CredentialsRotatedWhenProvided proves AC3 against
// real Postgres: submitting new credentials replaces the old encrypted
// value with a new one that decrypts to the NEW plaintext, not the old.
func TestUpdateTool_RealDB_CredentialsRotatedWhenProvided(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	defer pool.Close()

	q := store.New(pool)
	cipher, err := toolcreds.NewCipher(toolsUpdateTestKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "tools-update-test2-"+orgID.String()[:8], "tools-update-test2-"+orgID.String()); err != nil {
		t.Fatalf("seed test org: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID)

	origPlaintext, _ := json.Marshal(map[string]string{"api_key": "sk-original"})
	origEncrypted, _ := cipher.Encrypt(origPlaintext)

	created, err := q.CreateTool(ctx, store.CreateToolParams{
		OrgID: orgID, Name: "rotate-me", Type: "rest_api", AuthType: "api_key",
		CredentialsEncrypted: origEncrypted,
	})
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}

	newPlaintext, _ := json.Marshal(map[string]string{"api_key": "sk-rotated-new-value"})
	newEncrypted, err := cipher.Encrypt(newPlaintext)
	if err != nil {
		t.Fatalf("Encrypt (rotated): %v", err)
	}

	if _, err := q.UpdateTool(ctx, store.UpdateToolParams{
		ID: created.ID, OrgID: orgID,
		CredentialsEncrypted: newEncrypted,
	}); err != nil {
		t.Fatalf("UpdateTool: %v", err)
	}

	var storedEncrypted []byte
	if err := pool.QueryRow(ctx, `SELECT credentials_encrypted FROM gateway_tools WHERE id = $1`, created.ID).Scan(&storedEncrypted); err != nil {
		t.Fatalf("read back: %v", err)
	}
	decrypted, err := cipher.Decrypt(storedEncrypted)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(decrypted) != string(newPlaintext) {
		t.Errorf("decrypted credential after rotation = %q, want the NEW value %q", decrypted, newPlaintext)
	}
	if string(decrypted) == string(origPlaintext) {
		t.Error("credential was not actually rotated -- still decrypts to the original value")
	}
}

// TestUpdateTool_RealDB_CrossOrg_NoRowsAffected proves org isolation
// against real Postgres: an UpdateTool call with the wrong org_id affects
// no row (surfaces as pgx.ErrNoRows via RETURNING, matching what the
// handler treats as 404), and the real tool row -- including its real
// credential -- is completely untouched.
func TestUpdateTool_RealDB_CrossOrg_NoRowsAffected(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	defer pool.Close()

	q := store.New(pool)

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "tools-update-test3-"+orgID.String()[:8], "tools-update-test3-"+orgID.String()); err != nil {
		t.Fatalf("seed test org: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID)

	otherOrgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		otherOrgID, "tools-update-test3-other-"+otherOrgID.String()[:8], "tools-update-test3-other-"+otherOrgID.String()); err != nil {
		t.Fatalf("seed other org: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, otherOrgID)

	created, err := q.CreateTool(ctx, store.CreateToolParams{
		OrgID: orgID, Name: "org-a-tool", Type: "rest_api", AuthType: "api_key",
	})
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}

	newName := "hijacked-name"
	_, err = q.UpdateTool(ctx, store.UpdateToolParams{
		ID: created.ID, OrgID: otherOrgID, // wrong org
		Name: &newName,
	})
	if err == nil {
		t.Fatal("expected an error (no matching row) when updating with the wrong org_id, got nil")
	}
	if err != pgx.ErrNoRows {
		t.Errorf("err = %v, want pgx.ErrNoRows", err)
	}

	// Confirm the real row genuinely wasn't touched.
	var name string
	if err := pool.QueryRow(ctx, `SELECT name FROM gateway_tools WHERE id = $1`, created.ID).Scan(&name); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if name != "org-a-tool" {
		t.Errorf("name = %q after a cross-org update attempt, want it unchanged (\"org-a-tool\")", name)
	}
}

// TestUpdateTool_RealDB_MCPArgsPreservedWhenOmitted proves against real
// Postgres that omitting mcp_args from a PATCH preserves the existing
// stored array. Flagged by this task's own code review: unlike Name/
// MCPCommand/BaseURL (wrapped in pgtype.Text{Valid: bool} via toPgtypeText,
// an explicit "was this field set" signal), MCPArgs is passed as a raw
// []string and relies on pgx v5's implicit nil-slice-encodes-as-SQL-NULL
// array behavior for COALESCE to treat "omitted" correctly. This test
// verifies that behavior directly against real Postgres rather than
// assuming it.
func TestUpdateTool_RealDB_MCPArgsPreservedWhenOmitted(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	defer pool.Close()

	q := store.New(pool)

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "tools-update-test4-"+orgID.String()[:8], "tools-update-test4-"+orgID.String()); err != nil {
		t.Fatalf("seed test org: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID)

	created, err := q.CreateTool(ctx, store.CreateToolParams{
		OrgID: orgID, Name: "mcp-args-tool", Type: "mcp", AuthType: "api_key",
		MCPArgs: []string{"--port", "3001", "--verbose"},
	})
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}

	// Update name only -- MCPArgs left as the Go zero value (nil slice),
	// exactly what UpdateTool's handler passes when the request body
	// omits "mcp_args" entirely.
	newName := "mcp-args-tool-renamed"
	if _, err := q.UpdateTool(ctx, store.UpdateToolParams{
		ID: created.ID, OrgID: orgID,
		Name: &newName,
		// MCPArgs deliberately not set (nil)
	}); err != nil {
		t.Fatalf("UpdateTool: %v", err)
	}

	var storedArgs []string
	if err := pool.QueryRow(ctx, `SELECT mcp_args FROM gateway_tools WHERE id = $1`, created.ID).Scan(&storedArgs); err != nil {
		t.Fatalf("read back mcp_args: %v", err)
	}
	want := []string{"--port", "3001", "--verbose"}
	if len(storedArgs) != len(want) {
		t.Fatalf("mcp_args after a name-only edit = %v, want unchanged %v", storedArgs, want)
	}
	for i := range want {
		if storedArgs[i] != want[i] {
			t.Errorf("mcp_args[%d] = %q, want %q", i, storedArgs[i], want[i])
		}
	}
}

func strPtrTest(s string) *string { return &s }
