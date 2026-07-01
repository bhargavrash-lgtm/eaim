package api

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/eami/collector/internal/models"
)

var randRead = rand.Read

// IngestHandler handles POST /v1/ingest — validates, normalises, and buffers a report.
func IngestHandler(db *sql.DB, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := readBody(r)
		if err != nil {
			log.Warn("ingest: read body", "err", err)
			jsonError(w, "bad request body", http.StatusBadRequest)
			return
		}

		var report models.EndpointReport
		if err := json.Unmarshal(body, &report); err != nil {
			log.Warn("ingest: unmarshal", "err", err)
			jsonError(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		if err := validateReport(&report); err != nil {
			log.Warn("ingest: validation failed", "err", err, "agent", report.AgentID)
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		normalise(&report, r)

		compressed, err := gzipJSON(body)
		if err != nil {
			log.Error("ingest: gzip", "err", err)
			jsonError(w, "internal error", http.StatusInternalServerError)
			return
		}

		id := newUUID()
		now := time.Now().UTC().Format(time.RFC3339)
		_, err = db.ExecContext(r.Context(),
			`INSERT INTO report_buffer (id, received_at, agent_id, hostname, report_json)
			 VALUES (?, ?, ?, ?, ?)`,
			id, now, report.AgentID, report.Hostname, compressed)
		if err != nil {
			log.Error("ingest: buffer insert", "err", err)
			jsonError(w, "internal error", http.StatusInternalServerError)
			return
		}

		log.Info("report buffered",
			"id", id, "agent", report.AgentID, "hostname", report.Hostname)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
	}
}

// jsonError writes a JSON error body with the given status code.
func jsonError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// readBody reads the request body, decompressing gzip if Content-Encoding is set.
func readBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	reader := io.Reader(r.Body)
	if r.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		defer gr.Close()
		reader = gr
	}
	return io.ReadAll(io.LimitReader(reader, 10<<20)) // 10 MB cap
}

// validateReport checks required fields.
func validateReport(r *models.EndpointReport) error {
	if r.AgentID == "" {
		return fmt.Errorf("agent_id is required")
	}
	if r.Hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	if r.CollectedAt.IsZero() {
		return fmt.Errorf("collected_at is required")
	}
	return nil
}

// normalise canonicalises fields (hostname to lowercase, etc.).
func normalise(r *models.EndpointReport, req *http.Request) {
	if r.CollectedAt.IsZero() {
		r.CollectedAt = time.Now().UTC()
	}
}

func gzipJSON(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
