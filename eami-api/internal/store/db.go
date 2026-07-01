// Package store is the repository layer for eami-api.
// Query files (*.sql.go) mirror what sqlc would generate from internal/store/query/*.sql.
// To regenerate: sqlc generate (from repo root with sqlc.yaml present).
package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is satisfied by both *pgxpool.Pool and pgx.Tx, allowing queries to
// run inside or outside a transaction.
type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Queries holds a reference to a database connection (pool or tx).
type Queries struct {
	db DBTX
}

// New creates a Queries instance backed by the given pool or tx.
func New(db DBTX) *Queries {
	return &Queries{db: db}
}

// WithTx returns a new Queries scoped to the given transaction.
func (q *Queries) WithTx(tx pgx.Tx) *Queries {
	return &Queries{db: tx}
}

// NotifyPolicyReload sends a NOTIFY on the policy_reload channel so that
// all gateway nodes reload their in-memory policy set.
func (q *Queries) NotifyPolicyReload(ctx context.Context) error {
	_, err := q.db.Exec(ctx, "SELECT pg_notify('policy_reload', '')")
	return err
}

// NewPool creates a connection pool from the given DSN and validates the
// connection by pinging the database.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// DB returns the underlying DBTX connection. Used by the alerting engine
// to run ad-hoc metric queries that don't have sqlc wrappers.
func (q *Queries) DB() DBTX {
	return q.db
}
