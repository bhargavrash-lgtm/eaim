package models

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// sparse makes a file of size bytes without writing them.
func sparse(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

const mb = 1024 * 1024

// isolateDefaults points the default model locations at empty temp dirs,
// so tests don't depend on the host's LM Studio / HF / GPT4All trees.
func isolateDefaults(t *testing.T) {
	t.Helper()
	empty := t.TempDir()
	lm, hf, gpt := defaultLMStudioPath, defaultHFCachePath, defaultGPT4AllPath
	defaultLMStudioPath = func() string { return empty }
	defaultHFCachePath = func() string { return empty }
	defaultGPT4AllPath = func() string { return empty }
	t.Cleanup(func() { defaultLMStudioPath, defaultHFCachePath, defaultGPT4AllPath = lm, hf, gpt })
}

func scanPaths(t *testing.T, ctx context.Context, paths ...string) Result {
	t.Helper()
	isolateDefaults(t)
	// No Ollama on this port; the default-location walks find nothing in a
	// test home either way.
	r, _ := New(WithOllamaBaseURL("http://127.0.0.1:1")).ScanResult(ctx, ScanOptions{MinSizeMB: 1, ExtraScanPaths: paths})
	return r
}

func names(r Result, src Source) map[string]bool {
	out := map[string]bool{}
	for _, m := range r.Models {
		if m.Source == src {
			out[m.Name] = true
		}
	}
	return out
}

// B-194: under a configured path only model file types count, and hits are
// labelled scan_path (decision S2), not lm_studio.
func TestScanPath_OnlyModelFileTypesCount(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"weights.gguf", "model.safetensors", "ckpt.PT", "gpt4all.bin", "net.onnx"} {
		sparse(t, filepath.Join(root, n), 2*mb)
	}
	for _, n := range []string{"backup.iso", "movie.mp4", "disk.vmdk", "notes.txt", "noext"} {
		sparse(t, filepath.Join(root, n), 2*mb)
	}
	sparse(t, filepath.Join(root, "tiny.gguf"), mb/2) // under the size floor
	got := names(scanPaths(t, context.Background(), root), SourceScanPath)
	want := map[string]bool{"weights.gguf": true, "model.safetensors": true, "ckpt.PT": true, "gpt4all.bin": true, "net.onnx": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for n := range want {
		if !got[n] {
			t.Errorf("missing %s", n)
		}
	}
}

// S3: a model exactly MaxWalkDepth levels down is found; one level deeper
// is not, and the cut-off is recorded as depth_limited.
func TestScanPath_DepthLimit(t *testing.T) {
	root := t.TempDir()
	dir := root
	for i := 1; i <= MaxWalkDepth; i++ {
		dir = filepath.Join(dir, "d")
	}
	sparse(t, filepath.Join(dir, "at-limit.gguf"), 2*mb)
	sparse(t, filepath.Join(dir, "d", "too-deep.gguf"), 2*mb)

	r := scanPaths(t, context.Background(), root)
	got := names(r, SourceScanPath)
	if !got["at-limit.gguf"] || got["too-deep.gguf"] {
		t.Fatalf("depth handling: %v", got)
	}
	if len(r.Notes) != 1 || r.Notes[0] != NoteDepthLimited {
		t.Fatalf("notes %v, want [depth_limited]", r.Notes)
	}

	shallow := t.TempDir()
	sparse(t, filepath.Join(shallow, "a", "m.gguf"), 2*mb)
	if r := scanPaths(t, context.Background(), shallow); len(r.Notes) != 0 {
		t.Fatalf("depth_limited without a deep tree: %v", r.Notes)
	}
}

// The walk stops when the scan deadline passes, instead of running on
// behind it (B-281's still_running).
func TestScanPath_StopsAtDeadline(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 50; i++ {
		sparse(t, filepath.Join(root, "d", strings.Repeat("x", i%7+1), "m.gguf"), 2*mb)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // deadline already passed
	start := time.Now()
	r, err := New(WithOllamaBaseURL("http://127.0.0.1:1")).ScanResult(ctx, ScanOptions{MinSizeMB: 1, ExtraScanPaths: []string{root}})
	if err == nil {
		t.Fatal("a scan past its deadline reported success")
	}
	if len(names(r, SourceScanPath)) != 0 {
		t.Fatalf("walked after the deadline: %v", r.Models)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %v to stop", time.Since(start))
	}
}

// A configured filesystem root is skipped and noted, never walked (a
// hand-edited local YAML; remote config refuses it earlier with path_root).
func TestScanPath_RootIsSkippedAndNoted(t *testing.T) {
	root := "/"
	if runtime.GOOS == "windows" {
		root = `C:\`
	}
	r := scanPaths(t, context.Background(), root)
	if len(names(r, SourceScanPath)) != 0 {
		t.Fatal("a root was walked")
	}
	if len(r.Notes) != 1 || r.Notes[0] != NotePathRoot {
		t.Fatalf("notes %v, want [path_root]", r.Notes)
	}
}

// WalkDir doesn't follow symlinked directories: a link out of the
// configured tree isn't walked.
func TestScanPath_SymlinkedDirNotFollowed(t *testing.T) {
	outside := t.TempDir()
	sparse(t, filepath.Join(outside, "outside.gguf"), 2*mb)
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if got := names(scanPaths(t, context.Background(), root), SourceScanPath); got["outside.gguf"] {
		t.Fatal("followed a symlinked directory")
	}
}

// The default LM Studio and GPT4All locations keep their own, narrower
// type sets (unchanged by Slice 0).
func TestDefaultLocationsKeepTheirTypeSets(t *testing.T) {
	if !lmStudioExts[".gguf"] || len(lmStudioExts) != 1 {
		t.Fatalf("lm studio types %v", lmStudioExts)
	}
	if !gpt4AllExts[".gguf"] || !gpt4AllExts[".bin"] || len(gpt4AllExts) != 2 {
		t.Fatalf("gpt4all types %v", gpt4AllExts)
	}
}

// Security review M-2: a configured path that is a link to the root (with
// or without a trailing separator) is resolved first and refused, never
// walked.
func TestScanPath_LinkToRootIsRefused(t *testing.T) {
	root := "/"
	if runtime.GOOS == "windows" {
		root = `C:\`
	}
	link := filepath.Join(t.TempDir(), "to-root")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	for _, p := range []string{link, link + string(filepath.Separator)} {
		r := scanPaths(t, context.Background(), p)
		if len(r.Models) != 0 || len(r.Notes) != 1 || r.Notes[0] != NotePathRoot {
			t.Errorf("%s: models %d notes %v, want none and [path_root]", p, len(r.Models), r.Notes)
		}
	}
	if runtime.GOOS == "linux" {
		for _, p := range []string{"/proc/self/root/", "/proc/1/root/"} {
			if _, err := os.Stat(p); err != nil {
				continue
			}
			if r := scanPaths(t, context.Background(), p); len(r.Notes) != 1 || r.Notes[0] != NotePathRoot {
				t.Errorf("%s: notes %v, want [path_root]", p, r.Notes)
			}
		}
	}
}

// Forms Windows silently rewrites are refused by the scanner too (a
// hand-edited local YAML; remote config rejects them earlier).
func TestScanPath_NonNormalFormsAreRefused(t *testing.T) {
	dir := t.TempDir()
	sparse(t, filepath.Join(dir, "m.gguf"), 2*mb)
	for _, p := range []string{dir + string(filepath.Separator) + ".", dir + "." , dir + " ", dir + "::$INDEX_ALLOCATION", filepath.Join(dir, "x") + string(filepath.Separator) + ".."} {
		r := scanPaths(t, context.Background(), p)
		if len(names(r, SourceScanPath)) != 0 {
			t.Errorf("%q was walked", p)
		}
	}
}
