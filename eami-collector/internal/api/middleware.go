// Package api implements the collector's HTTP API.
package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
)

// contextKey is an unexported type for context keys defined in this package,
// avoiding collisions with keys defined in other packages (standard Go idiom).
type contextKey string

const agentIDContextKey contextKey = "agent_id"

// AgentIDFromContext returns the agent_id bound to the credential that
// authenticated the current request, and whether one is present. Only
// DB-backed per-agent keys (B-073) carry a bound identity -- a request
// authenticated via the legacy static fleet-wide key has none, and callers
// must treat that as "no identity to check," not "identity is empty string"
// (see IngestHandler/ConfigProxyHandler for how this distinction is used).
func AgentIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(agentIDContextKey).(string)
	return v, ok
}

// APIKeyMiddleware validates the X-API-Key header against either the static
// fleet-wide key (if configured) or the per-agent api_keys table (B-073).
//
// Both paths are tried, in order, so a deployment can migrate from the
// legacy shared key to per-agent keys gradually rather than all-or-nothing:
//  1. If staticKey is set and the presented key matches it (constant-time),
//     the request is authenticated with NO bound identity -- preserves this
//     middleware's exact pre-B-073 behavior for anyone still using it.
//  2. Otherwise, the key is looked up in api_keys. A match resolves the
//     key's bound agent_id into the request context (AgentIDFromContext) --
//     handlers that accept a claimed agent_id in the request itself
//     (IngestHandler, ConfigProxyHandler) use this to reject a credential
//     being used to claim a different agent's identity.
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

			if staticKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(staticKey)) == 1 {
				next.ServeHTTP(w, r)
				return
			}

			agentID, ok := resolveDBKey(db, key)
			if !ok {
				log.Warn("rejected request: invalid API key",
					"remote", r.RemoteAddr, "path", r.URL.Path)
				http.Error(w, "invalid API key", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), agentIDContextKey, agentID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// hashKey returns the SHA-256 hex digest of a key (for storage).
func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// resolveDBKey looks up key in api_keys and returns the agent_id it's bound
// to, if the key is active (not revoked) and genuinely bound to an agent.
// A row with agent_id = '' (a legacy key minted before B-073, or a
// never-migrated pre-existing test row) is deliberately treated as
// not-found here -- it carries no real identity to enforce, so it can no
// longer authenticate once the static key is unset. This is a narrowing of
// what validateDBKey (pre-B-073) accepted, disclosed as a deliberate
// consequence of making per-agent identity real rather than optional.
func resolveDBKey(db *sql.DB, key string) (string, bool) {
	h := hashKey(key)
	var agentID string
	err := db.QueryRow(
		`SELECT agent_id FROM api_keys WHERE key_hash = ? AND revoked_at IS NULL AND agent_id != ''`, h,
	).Scan(&agentID)
	if err != nil {
		return "", false
	}
	return agentID, true
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

// RegisterKey inserts a new per-agent API key into the database (B-073).
// agentID must be non-empty -- it's the identity this key authenticates as,
// enforced unique among active (non-revoked) keys by
// idx_api_keys_agent_active (a constraint violation here means agentID
// already has an active key; the caller should revoke it first or use a
// different agentID, not silently overwrite).
func RegisterKey(db *sql.DB, rawKey, agentID, label string) error {
	if agentID == "" {
		return fmt.Errorf("api: RegisterKey: agentID must not be empty")
	}
	_, err := db.Exec(
		`INSERT INTO api_keys (id, key_hash, label, agent_id, created_at) VALUES (?, ?, ?, ?, datetime('now'))`,
		newUUID(), hashKey(rawKey), label, agentID,
	)
	return err
}

// RevokeKey marks agentID's active key (if any) as revoked, so it's
// rejected on its next authentication attempt. Returns the number of rows
// affected -- 0 means agentID had no active key to revoke (already revoked,
// or never existed), which the caller should treat as a no-op condition to
// report, not a hard error.
func RevokeKey(db *sql.DB, agentID string) (int64, error) {
	res, err := db.Exec(
		`UPDATE api_keys SET revoked_at = datetime('now') WHERE agent_id = ? AND revoked_at IS NULL`,
		agentID,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// KeyInfo is a non-secret summary of an api_keys row (never includes the
// key itself or its hash) for admin listing.
type KeyInfo struct {
	ID        string
	AgentID   string
	Label     string
	CreatedAt string
	RevokedAt string // empty if active
}

// ListKeys returns every api_keys row (active and revoked), newest first,
// for admin visibility (e.g. the collector's mint-key/list-keys CLI).
func ListKeys(db *sql.DB) ([]KeyInfo, error) {
	rows, err := db.Query(
		`SELECT id, agent_id, label, created_at, COALESCE(revoked_at, '')
		 FROM api_keys ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KeyInfo
	for rows.Next() {
		var k KeyInfo
		if err := rows.Scan(&k.ID, &k.AgentID, &k.Label, &k.CreatedAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
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
