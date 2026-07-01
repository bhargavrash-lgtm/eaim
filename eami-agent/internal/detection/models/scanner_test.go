package models

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanOllama_Success(t *testing.T) {
	resp := map[string]any{
		"models": []map[string]any{
			{"name": "llama3:8b", "size": int64(4_800_000_000), "modified_at": time.Now()},
			{"name": "mistral:7b", "size": int64(3_900_000_000), "modified_at": time.Now()},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	s := New(WithOllamaBaseURL(srv.URL))
	models, err := s.Scan(context.Background(), ScanOptions{MinSizeMB: 1})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(models) != 2 {
		t.Errorf("want 2 models, got %d", len(models))
	}
	if models[0].Source != SourceOllama {
		t.Errorf("source: want %q, got %q", SourceOllama, models[0].Source)
	}
}

func TestScanOllama_ServerDown(t *testing.T) {
	// Point at a non-listening address; should return empty, not error.
	s := New(WithOllamaBaseURL("http://127.0.0.1:1"))
	results, err := s.Scan(context.Background(), ScanOptions{MinSizeMB: 100})
	if err != nil {
		t.Fatalf("expected nil error for unreachable Ollama, got: %v", err)
	}
	for _, m := range results {
		if m.Source == SourceOllama {
			t.Error("should not have Ollama results when server is down")
		}
	}
}

func TestScanOllama_BadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	s := New(WithOllamaBaseURL(srv.URL))
	// Error from Ollama is non-fatal; Scan should not propagate it.
	_, err := s.Scan(context.Background(), ScanOptions{MinSizeMB: 100})
	if err != nil {
		t.Fatalf("Scan should not fail on Ollama 5xx: %v", err)
	}
}

func TestScanHuggingFace(t *testing.T) {
	// Build a fake HF hub cache tree:
	// hub/models--google--gemma-2b/snapshots/abc123/config.json
	dir := t.TempDir()
	snapshotDir := filepath.Join(dir, "models--google--gemma-2b", "snapshots", "abc123")
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"model_type":"gemma","architectures":["GemmaForCausalLM"]}`
	if err := os.WriteFile(filepath.Join(snapshotDir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write a fake weight file large enough to pass the size filter.
	weight := make([]byte, 200*1024*1024) // 200 MB
	if err := os.WriteFile(filepath.Join(snapshotDir, "model.safetensors"), weight, 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := scanHuggingFace(dir, 100*1024*1024)
	if err != nil {
		t.Fatalf("scanHuggingFace: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	m := results[0]
	if m.Source != SourceHuggingFace {
		t.Errorf("source: want %q, got %q", SourceHuggingFace, m.Source)
	}
	if m.ModelType != "gemma" {
		t.Errorf("model_type: want %q, got %q", "gemma", m.ModelType)
	}
	if m.Architecture != "GemmaForCausalLM" {
		t.Errorf("architecture: want %q, got %q", "GemmaForCausalLM", m.Architecture)
	}
}

func TestScanDir_SizeFilter(t *testing.T) {
	dir := t.TempDir()
	// Write one file below threshold (50 MB) and one above (150 MB).
	small := make([]byte, 50*1024*1024)
	large := make([]byte, 150*1024*1024)
	os.WriteFile(filepath.Join(dir, "small.gguf"), small, 0o644)
	os.WriteFile(filepath.Join(dir, "large.gguf"), large, 0o644)

	results, err := scanDir(dir, ".gguf", SourceLMStudio, 100*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("want 1 result (large only), got %d", len(results))
	}
	if results[0].Name != "large.gguf" {
		t.Errorf("expected large.gguf, got %q", results[0].Name)
	}
}

func TestWithOllamaBaseURL(t *testing.T) {
	s := New(WithOllamaBaseURL("http://custom:9999"))
	if s.ollamaBaseURL != "http://custom:9999" {
		t.Errorf("ollamaBaseURL: got %q", s.ollamaBaseURL)
	}
}
