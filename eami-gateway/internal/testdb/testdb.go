// testdb.go -- eami-gateway/internal/testdb
//
// Shared per-test throwaway-database provisioning for eami-gateway's real-
// Postgres tests (B-122/B-140). NewThrowawayPool creates a fresh, uniquely
// named database, applies every real schema/migrations-v2/*.up.sql file
// against it (the same files docker-compose's migrate service, B-051,
// applies in production), and returns a pool connected to it -- mirroring
// eami-api/internal/api/bootstrap_test.go's already-proven pattern exactly.
// Duplicated from that file rather than imported (eami-api and eami-gateway
// are separate Go modules under go.work), but built here as ONE shared
// package within eami-gateway itself instead of copied into 9 separate
// packages: a plain (non-_test.go) package so every package's own test
// files can import it, since Go doesn't allow importing another package's
// _test.go files, and nothing outside test code ever imports this package.
//
// Closes both B-122 (permanent orphaned audit_log rows accumulating in the
// shared dev database on every real-Postgres test run) and B-140 (a
// confirmed hash-chain-continuity test race caused by cross-package
// Postgres test parallelism sharing one global audit_log/orgs state) in one
// motion: full per-test database isolation means nothing is left behind to
// orphan, and no cross-test/cross-package interference is possible by
// construction.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./... -v
package testdb

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Conn holds real-Postgres admin-connection details resolved from the
// environment.
type Conn struct {
	host, user, pass string
}

// ResolveConn resolves connection details for the real Postgres instance
// these tests run against, skipping the calling test if neither env var is
// set -- byte-for-byte the same TEST_DATABASE_URL/POSTGRES_PASSWORD
// fallback convention every existing *TestDSN helper in this module already
// used independently (mainTestDSN, workflowTestDSN, approvalTestDSN, etc.),
// centralized here so all 9 packages share one implementation instead of 9
// near-identical copies that could drift apart.
func ResolveConn(t *testing.T) Conn {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatalf("parse TEST_DATABASE_URL: %v", err)
		}
		return Conn{host: fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), user: cfg.User, pass: cfg.Password}
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/127.0.0.1:5432 layout) to run this integration test against a real Postgres")
	}
	// 127.0.0.1, not localhost -- this machine resolves localhost to ::1
	// first and Docker Desktop's IPv6 port-forwarding silently drops
	// traffic (documented in CONTEXT.md).
	return Conn{host: "127.0.0.1:5432", user: "eami_app", pass: pw}
}

func (c Conn) adminURL() string {
	return fmt.Sprintf("postgres://%s:%s@%s/postgres?sslmode=disable", c.user, c.pass, c.host)
}

func (c Conn) dbURL(dbName string) string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", c.user, c.pass, c.host, dbName)
}

// newThrowawayDatabase creates a fresh, uniquely named database and
// registers its cleanup. Mirrors eami-api/internal/api/bootstrap_test.go's
// newThrowawayDB (itself mirroring schema/migrationtest's own precedent).
func newThrowawayDatabase(t *testing.T, c Conn) string {
	t.Helper()
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, c.adminURL())
	if err != nil {
		t.Fatalf("connect to admin DB: %v", err)
	}
	defer conn.Close(ctx)

	dbName := fmt.Sprintf("gatewaytest_%d_%d", os.Getpid(), rand.Int63())
	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, dbName)); err != nil {
		t.Fatalf("create throwaway database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		cc, err := pgx.Connect(context.Background(), c.adminURL())
		if err != nil {
			t.Errorf("cleanup: connect to admin DB (throwaway database %s may be leaked): %v", dbName, err)
			return
		}
		defer cc.Close(context.Background())
		_, _ = cc.Exec(context.Background(),
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName)
		if _, err := cc.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, dbName)); err != nil {
			t.Errorf("cleanup: drop throwaway database %s (leaked): %v", dbName, err)
		}
	})
	return dbName
}

// applyMigrations runs every schema/migrations-v2/*.up.sql file, in order,
// against dbName -- the exact files docker-compose's migrate service (B-051)
// applies in production, not a copy or a reduced subset. The path is
// resolved relative to the calling test binary's own working directory (go
// test always runs with CWD set to the package under test), which is why
// this only works correctly when called from a package exactly 3 levels
// under the repo root (eami-gateway/cmd/gateway or
// eami-gateway/internal/<pkg>) -- true of every current eami-gateway
// real-Postgres test package.
func applyMigrations(t *testing.T, c Conn, dbName string) {
	t.Helper()
	ctx := context.Background()

	dir, err := filepath.Abs("../../../schema/migrations-v2")
	if err != nil {
		t.Fatalf("resolve migrations-v2 path: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations-v2 dir: %v", err)
	}
	var upFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			upFiles = append(upFiles, e.Name())
		}
	}
	sort.Strings(upFiles)
	if len(upFiles) == 0 {
		t.Fatalf("no *.up.sql files found in %s", dir)
	}

	cfg, err := pgx.ParseConfig(c.dbURL(dbName))
	if err != nil {
		t.Fatalf("parse migration connect config: %v", err)
	}
	// Simple protocol -- these files contain multiple ;-separated
	// statements (and PL/pgSQL DO $$ ... $$ blocks), which the extended
	// protocol's one-statement-per-Parse restriction can't run in a single
	// Exec call.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect for migrations: %v", err)
	}
	defer conn.Close(ctx)

	for _, name := range upFiles {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := conn.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

// NewThrowawayPool resolves real-Postgres connection details from the
// environment, creates a fresh throwaway database, applies every real
// migration against it, and returns a pool connected to it.
//
// t.Cleanup(pool.Close) is registered here, before newThrowawayDatabase's
// own t.Cleanup-registered DROP DATABASE (registered earlier, inside this
// same call) -- so pool.Close runs FIRST (t.Cleanup unwinds LIFO), closing
// every connection cleanly before the drop's own pg_terminate_backend has
// to force any. Skips the calling test (not a hard failure) if the
// resolved Postgres is unreachable, matching every existing *TestEnv
// constructor's convention.
func NewThrowawayPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	c := ResolveConn(t)
	dbName := newThrowawayDatabase(t, c)
	applyMigrations(t, c, dbName)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, c.dbURL(dbName))
	if err != nil {
		t.Fatalf("connect pool to throwaway db: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach throwaway database: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}
