package browser

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// makeExtDir creates a fake extension directory tree under base:
//
//	base/{id}/{version}_0/manifest.json
func makeExtDir(t *testing.T, base, id, version, name string) {
	t.Helper()
	verDir := filepath.Join(base, id, version+"_0")
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", verDir, err)
	}
	data, _ := json.Marshal(map[string]string{"name": name, "version": version})
	if err := os.WriteFile(filepath.Join(verDir, "manifest.json"), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func TestScan_FindsKnownExtension(t *testing.T) {
	tmp := t.TempDir()
	chromeExt := filepath.Join(tmp, "chrome", "Extensions")
	// Claude extension
	makeExtDir(t, chromeExt, "ppmdhllojcbgkbmcfkhdkklblickdbno", "1.2.3", "Claude")

	dirs := []ExtensionDir{
		{Browser: "chrome", Path: chromeExt, UserPath: "testuser"},
	}
	s := New(WithBaseDirs(dirs))
	results, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	got := results[0]
	if got.ExtensionID != "ppmdhllojcbgkbmcfkhdkklblickdbno" {
		t.Errorf("extension_id: got %q", got.ExtensionID)
	}
	if got.Browser != "chrome" {
		t.Errorf("browser: got %q", got.Browser)
	}
	if got.Name != "Claude" {
		t.Errorf("name: got %q", got.Name)
	}
	if got.Version != "1.2.3" {
		t.Errorf("version: got %q", got.Version)
	}
	if got.UserPath != "testuser" {
		t.Errorf("user_path: got %q", got.UserPath)
	}
}

func TestScan_MultipleExtensionsAndBrowsers(t *testing.T) {
	tmp := t.TempDir()
	chromeExt := filepath.Join(tmp, "chrome", "Extensions")
	edgeExt := filepath.Join(tmp, "edge", "Extensions")

	makeExtDir(t, chromeExt, "ppmdhllojcbgkbmcfkhdkklblickdbno", "1.0.0", "Claude")
	makeExtDir(t, edgeExt, "kbfnbcaeplbcioakkpcpgfkobkghlhen", "14.1095.0", "Grammarly")

	dirs := []ExtensionDir{
		{Browser: "chrome", Path: chromeExt, UserPath: "alice"},
		{Browser: "edge", Path: edgeExt, UserPath: "alice"},
	}
	s := New(WithBaseDirs(dirs))
	results, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	browsers := map[string]bool{}
	for _, r := range results {
		browsers[r.Browser] = true
	}
	if !browsers["chrome"] || !browsers["edge"] {
		t.Errorf("expected both chrome and edge results: %v", browsers)
	}
}

func TestScan_UnknownExtensionNotReported(t *testing.T) {
	tmp := t.TempDir()
	extDir := filepath.Join(tmp, "Extensions")
	// An extension ID that is not in knownExtensions.
	makeExtDir(t, extDir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "1.0.0", "Unknown")

	dirs := []ExtensionDir{
		{Browser: "chrome", Path: extDir, UserPath: "u"},
	}
	s := New(WithBaseDirs(dirs))
	results, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results for unknown extension, got %d", len(results))
	}
}

func TestScan_MissingInstallationNoError(t *testing.T) {
	// Point at a directory that does not exist.
	dirs := []ExtensionDir{
		{Browser: "chrome", Path: "/nonexistent/path/Extensions", UserPath: "u"},
	}
	s := New(WithBaseDirs(dirs))
	results, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: expected nil error for missing dir, got: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results, got %d", len(results))
	}
}

func TestScan_ManifestUnreadableFallsBackToKnownName(t *testing.T) {
	tmp := t.TempDir()
	extDir := filepath.Join(tmp, "Extensions")
	// Create extension dir with version subdir but no manifest.json.
	verDir := filepath.Join(extDir, "bbnkhnaphfggfomhfnnbhiplfflgolge", "2.0.0_0")
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		t.Fatal(err)
	}

	dirs := []ExtensionDir{
		{Browser: "chrome", Path: extDir, UserPath: "u"},
	}
	s := New(WithBaseDirs(dirs))
	results, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Name != "ChatGPT (OpenAI)" {
		t.Errorf("name fallback: got %q, want %q", results[0].Name, "ChatGPT (OpenAI)")
	}
	// Version falls back to the directory name.
	if results[0].Version != "2.0.0_0" {
		t.Errorf("version fallback: got %q, want %q", results[0].Version, "2.0.0_0")
	}
}

func TestScan_EmptyDirList_NoError(t *testing.T) {
	s := New(WithBaseDirs(nil))
	results, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results, got %d", len(results))
	}
}
