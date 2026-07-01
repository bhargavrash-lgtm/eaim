package api

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// HealthHandler handles GET /health — liveness probe.
func HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

// StatsHandler handles GET /stats — returns buffer and dead-letter row counts.
func StatsHandler(db *sql.DB, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var bufferCount, deadLetterCount int
		_ = db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM report_buffer").Scan(&bufferCount)
		_ = db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM dead_letter").Scan(&deadLetterCount)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{
			"buffer_rows":      bufferCount,
			"dead_letter_rows": deadLetterCount,
		})
	}
}

// Router builds the HTTP mux for the collector.
// saasURL and serviceKey are forwarded to the config proxy; both may be empty
// in standalone deployments (the proxy will return 503 in that case).
func Router(db *sql.DB, staticAPIKey, saasURL, serviceKey string, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	authMW := APIKeyMiddleware(staticAPIKey, db, log)
	logMW := LoggingMiddleware(log)

	proxyClient := &http.Client{Timeout: 15 * time.Second}

	mux.Handle("GET /health", logMW(HealthHandler()))
	mux.Handle("POST /v1/ingest", logMW(authMW(IngestHandler(db, log))))
	mux.Handle("GET /v1/agent-config/{agent_id}", logMW(authMW(ConfigProxyHandler(saasURL, serviceKey, proxyClient, log))))
	mux.Handle("GET /stats", logMW(authMW(StatsHandler(db, log))))

	return mux
}
