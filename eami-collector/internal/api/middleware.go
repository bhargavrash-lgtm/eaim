// Package api implements the collector's HTTP API.
package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
)

// APIKeyMiddleware validates the X-API-Key header against the api_keys table.
// If apiKey is non-empty, it is used as a single static key (for simple deployments).
// If apiKey is empty, the key is validated against the database.
func APIKeyMiddleware(staticKey string, db *sql.DB, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-API-Key")
			if key == "" {
				key = r.Header.Get("Authorization")
				if len(key) > 7 && key[:7] == "Bearer " {
					key = key[7:]
				}
			}

			if key == "" {
				http.Error(w, "missing API key", http.StatusUnauthorized)
				return
			}

			var valid bool
			if staticKey != "" {
				valid = subtle.ConstantTimeCompare([]byte(key), []byte(staticKey)) == 1
			} else {
				valid = validateDBKey(db, key)
			}

			if !valid {
				log.Warn("rejected request: invalid API key",
					"remote", r.RemoteAddr, "path", r.URL.Path)
				http.Error(w, "invalid API key", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// hashKey returns the SHA-256 hex digest of a key (for storage).
func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func validateDBKey(db *sql.DB, key string) bool {
	h := hashKey(key)
	var exists bool
	err := db.QueryRow(
		`SELECT 1 FROM api_keys WHERE key_hash = ? AND revoked_at IS NULL`, h,
	).Scan(&exists)
	return err == nil && exists
}

// LoggingMiddleware logs each request.
func LoggingMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			log.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.status,
				"remote", r.RemoteAddr,
			)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

// RegisterKey inserts a new API key into the database.
func RegisterKey(db *sql.DB, rawKey, label string) error {
	_, err := db.Exec(
		`INSERT INTO api_keys (id, key_hash, label, created_at) VALUES (?, ?, ?, datetime('now'))`,
		newUUID(), hashKey(rawKey), label,
	)
	return err
}

// newUUID generates a simple UUID v4.
func newUUID() string {
	// crypto/rand-based UUID — good enough for key IDs
	b := make([]byte, 16)
	if _, err := randRead(b); err != nil {
		panic(fmt.Sprintf("uuid: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%12x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
