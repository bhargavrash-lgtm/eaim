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
	return nil
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
		created_at TEXT NOT NULL,
		revoked_at TEXT
	)`,
}
