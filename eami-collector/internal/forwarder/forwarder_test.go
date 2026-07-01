package forwarder

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, ddl := range []string{
		`CREATE TABLE report_buffer (id TEXT PRIMARY KEY, received_at TEXT NOT NULL, agent_id TEXT NOT NULL, hostname TEXT NOT NULL, report_json BLOB NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, last_attempt TEXT)`,
		`CREATE TABLE dead_letter (id TEXT PRIMARY KEY, received_at TEXT NOT NULL, agent_id TEXT NOT NULL, hostname TEXT NOT NULL, report_json BLOB NOT NULL, attempts INTEGER NOT NULL, failed_at TEXT NOT NULL, reason TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	return db
}

func gzipJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, _ := json.Marshal(v)
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(raw)
	_ = w.Close()
	return buf.Bytes()
}

func insertRow(t *testing.T, db *sql.DB, id string, attempts int) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO report_buffer (id, received_at, agent_id, hostname, report_json, attempts) VALUES (?, ?, ?, ?, ?, ?)`,
		id, time.Now().UTC().Format(time.RFC3339), "agent-1", "host-a",
		gzipJSON(t, map[string]string{"key": "value"}), attempts)
	if err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func count(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&n)
	return n
}

func TestTick_202_DeletesFromBuffer(t *testing.T) {
	db := openTestDB(t)
	insertRow(t, db, "r1", 0)
	insertRow(t, db, "r2", 0)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	f := New(Config{SAASURL: srv.URL, BatchSize: 10}, db, nil)
	if err := f.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if n := count(t, db, "report_buffer"); n != 0 {
		t.Errorf("buffer: want 0, got %d", n)
	}
}

func TestTick_4xx_MovesToDeadLetter(t *testing.T) {
	db := openTestDB(t)
	insertRow(t, db, "r1", 0)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	f := New(Config{SAASURL: srv.URL, BatchSize: 10}, db, nil)
	if err := f.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if n := count(t, db, "report_buffer"); n != 0 {
		t.Errorf("buffer: want 0, got %d", n)
	}
	if n := count(t, db, "dead_letter"); n != 1 {
		t.Errorf("dead_letter: want 1, got %d", n)
	}
	var reason string
	db.QueryRow("SELECT reason FROM dead_letter WHERE id='r1'").Scan(&reason)
	if reason != "http_4xx:400" {
		t.Errorf("reason: got %q", reason)
	}
}

func TestTick_5xx_IncrementsAttempts(t *testing.T) {
	db := openTestDB(t)
	insertRow(t, db, "r1", 0)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f := New(Config{SAASURL: srv.URL, BatchSize: 10}, db, nil)
	_ = f.tick(context.Background())

	var attempts int
	db.QueryRow("SELECT attempts FROM report_buffer WHERE id='r1'").Scan(&attempts)
	if attempts != 1 {
		t.Errorf("attempts: want 1, got %d", attempts)
	}
	if n := count(t, db, "dead_letter"); n != 0 {
		t.Errorf("dead_letter: want 0, got %d", n)
	}
}

func TestTick_MaxAttempts_MovesToDeadLetter(t *testing.T) {
	db := openTestDB(t)
	insertRow(t, db, "r1", maxAttempts)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f := New(Config{SAASURL: srv.URL, BatchSize: 10}, db, nil)
	_ = f.tick(context.Background())

	if n := count(t, db, "report_buffer"); n != 0 {
		t.Errorf("buffer: want 0, got %d", n)
	}
	if n := count(t, db, "dead_letter"); n != 1 {
		t.Errorf("dead_letter: want 1, got %d", n)
	}
}

func TestTick_NetworkError_IncrementsAttempts(t *testing.T) {
	db := openTestDB(t)
	insertRow(t, db, "r1", 0)
	f := New(Config{SAASURL: "http://127.0.0.1:1", BatchSize: 10, TimeoutSeconds: 1}, db, nil)
	_ = f.tick(context.Background())
	var attempts int
	db.QueryRow("SELECT attempts FROM report_buffer WHERE id='r1'").Scan(&attempts)
	if attempts != 1 {
		t.Errorf("attempts: want 1, got %d", attempts)
	}
}

func TestTick_EmptyBuffer_NoRequest(t *testing.T) {
	db := openTestDB(t)
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	f := New(Config{SAASURL: srv.URL, BatchSize: 10}, db, nil)
	_ = f.tick(context.Background())
	if called {
		t.Error("HTTP call made for empty buffer")
	}
}

func TestSendBatch_SetsAPIKeyHeader(t *testing.T) {
	db := openTestDB(t)
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get(apiKeyHeader)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	f := New(Config{SAASURL: srv.URL, APIKey: "test-key-123"}, db, nil)
	rows := []BufferRow{{
		ID: "r1", AgentID: "a", Hostname: "h",
		ReportJSON: gzipJSON(t, map[string]string{"x": "y"}),
	}}
	status, err := f.sendBatch(context.Background(), rows)
	if err != nil {
		t.Fatalf("sendBatch: %v", err)
	}
	if status != http.StatusAccepted {
		t.Errorf("status: want 202, got %d", status)
	}
	if gotKey != "test-key-123" {
		t.Errorf("X-API-Key: got %q", gotKey)
	}
}
