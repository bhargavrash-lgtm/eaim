// Package db manages the SQLite WAL buffer database for eami-collector.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Open opens (or creates) the SQLite buffer database at path with WAL mode
// and creates both the report_buffer and dead_letter tables.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("db: mkdir: %w", err)
	}

	dsn := path + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite WAL: single writer

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("db: migrate: %w", err)
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	for _, ddl := range schema {
		if _, err := db.Exec(ddl); err != nil {
			return err
		}
	}
	// api_keys.agent_id (B-073) is baked into the CREATE TABLE above for a
	// fresh database, but a pre-existing DB file created before this change
	// won't have it -- IF NOT EXISTS never alters an existing table. This
	// idempotently backfills the column so upgrading an already-deployed
	// collector doesn't require a manual migration step.
	if err := ensureColumn(db, "api_keys", "agent_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("db: ensure api_keys.agent_id: %w", err)
	}
	// Partial unique index: at most one ACTIVE (revoked_at IS NULL) key per
	// agent_id. Revoked rows are excluded so revoke-then-reissue for the
	// same agent works (key rotation), and rows with agent_id = '' (legacy
	// keys minted before this column existed, or the disposable dev-test
	// rows created by earlier sessions) are excluded since they carry no
	// real identity to enforce uniqueness on.
	if _, err := db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_agent_active
		 ON api_keys(agent_id) WHERE revoked_at IS NULL AND agent_id != ''`,
	); err != nil {
		return fmt.Errorf("db: create idx_api_keys_agent_active: %w", err)
	}
	return nil
}

// ensureColumn adds column to table with the given type/default if it
// doesn't already exist, via PRAGMA table_info -- SQLite's ALTER TABLE ADD
// COLUMN has no IF NOT EXISTS form, so existence must be checked manually.
func ensureColumn(db *sql.DB, table, column, decl string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + decl)
	return err
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS report_buffer (
		id           TEXT PRIMARY KEY,
		received_at  TEXT NOT NULL,
		agent_id     TEXT NOT NULL,
		hostname     TEXT NOT NULL,
		report_json  BLOB NOT NULL,
		attempts     INTEGER NOT NULL DEFAULT 0,
		last_attempt TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_buffer_received ON report_buffer(received_at ASC)`,
	`CREATE INDEX IF NOT EXISTS idx_buffer_attempts ON report_buffer(attempts ASC)`,
	`CREATE TABLE IF NOT EXISTS dead_letter (
		id           TEXT PRIMARY KEY,
		received_at  TEXT NOT NULL,
		agent_id     TEXT NOT NULL,
		hostname     TEXT NOT NULL,
		report_json  BLOB NOT NULL,
		attempts     INTEGER NOT NULL,
		failed_at    TEXT NOT NULL,
		reason       TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_dead_letter_failed ON dead_letter(failed_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_dead_letter_agent  ON dead_letter(agent_id)`,
	`CREATE TABLE IF NOT EXISTS api_keys (
		id         TEXT PRIMARY KEY,
		key_hash   TEXT NOT NULL UNIQUE,
		label      TEXT NOT NULL,
		agent_id   TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		revoked_at TEXT
	)`,
}
