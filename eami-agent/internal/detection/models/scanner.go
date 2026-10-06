// Package models detects locally-installed AI model files and running model servers.
package models

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/eami/agent/internal/scanpath"
)

type Source string

const (
	SourceOllama      Source = "ollama"
	SourceLMStudio    Source = "lm_studio"
	SourceHuggingFace Source = "huggingface"
	SourceGPT4All     Source = "gpt4all"
	// SourceScanPath marks a hit under a configured model_scan_paths entry
	// (B-269 Slice 0, decision S2; shown as "Configured path"). These used
	// to be mislabelled lm_studio.
	SourceScanPath Source = "scan_path"
)

const defaultOllamaBaseURL = "http://localhost:11434"

// LocalModel describes a single detected model.
type LocalModel struct {
	Name         string    `json:"name"`
	Source       Source    `json:"source"`
	FilePath     string    `json:"file_path,omitempty"`
	SizeBytes    int64     `json:"size_bytes"`
	ModifiedAt   time.Time `json:"modified_at,omitempty"`
	ModelType    string    `json:"model_type,omitempty"`
	Architecture string    `json:"architecture,omitempty"`
}

// ScanOptions controls scanner behaviour.
type ScanOptions struct {
	MinSizeMB      int64
	ExtraScanPaths []string
}

// Option is a functional option for Scanner.
type Option func(*Scanner)

// WithOllamaBaseURL overrides the Ollama API base URL for testing.
func WithOllamaBaseURL(url string) Option {
	return func(s *Scanner) { s.ollamaBaseURL = url }
}

// Scanner holds configuration for model detection.
type Scanner struct{ ollamaBaseURL string }

// New creates a Scanner with the given options.
func New(opts ...Option) *Scanner {
	s := &Scanner{ollamaBaseURL: defaultOllamaBaseURL}
	for _, o := range opts {
		o(s)
	}
	return s
}

// MaxWalkDepth is how many directory levels below a walk root are entered
// (B-277 / B-269 Slice 0, decision S3). LM Studio's own layout is
// models/<publisher>/<repo>/<file>, so 8 leaves room without allowing a
// whole-disk crawl. Hitting it is recorded (NoteDepthLimited), never silent.
const MaxWalkDepth = 8

// ModelFileExtensions is the B-194 filter for configured scan paths: only
// these count as model files there. ".bin" is deliberately kept (GPT4All,
// older PyTorch) even though it also matches non-model binaries; the size
// floor still applies (decision S1). Directory-shaped formats (.mlpackage,
// TF SavedModel) are out of scope: the walk reports files.
var ModelFileExtensions = []string{
	".gguf", ".ggml", ".safetensors", ".bin", ".pt", ".pth", ".ckpt", ".onnx",
	".tflite", ".h5", ".keras", ".pb", ".mlmodel", ".llamafile",
}

// Notes recorded in Result.Notes (and the report's scanner_notes).
const (
	NoteDepthLimited = "depth_limited" // a walk reached MaxWalkDepth; deeper files weren't scanned
	NotePathRoot     = "path_root"     // a configured path was (or resolved to) a filesystem root and was skipped
	NotePathNetwork  = "path_network"  // a configured path resolved to a network share and was skipped
)

// Default model locations. Package variables so tests can point them at
// empty directories instead of the host's real LM Studio / HF / GPT4All.
var (
	defaultLMStudioPath = lmStudioPath
	defaultHFCachePath  = hfCachePath
	defaultGPT4AllPath  = gpt4AllPath
)

// resolveScanRoot resolves a configured path's links before it is walked
// (B-269 Slice 0 security review M-2). Shape checks alone can't see that
// /proc/1/root/, /proc/self/cwd/ or a symlink to \\host\share lead to the
// whole disk or a network share: WalkDir follows a link given as its root
// (with a trailing separator), so the resolved target is checked against the
// same rules and against the real root, and only the resolved path is
// walked. A path that doesn't resolve (missing, unreadable) has nothing to
// walk.
func resolveScanRoot(p string) (string, string) {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", ""
	}
	if scanpath.IsNetwork(r) {
		return "", NotePathNetwork
	}
	if scanpath.IsRoot(r) || isSameAsRoot(r) {
		return "", NotePathRoot
	}
	return r, ""
}

// isSameAsRoot reports whether r is the filesystem (or volume) root itself,
// whatever it's called.
func isSameAsRoot(r string) bool {
	root := string(filepath.Separator)
	if vol := filepath.VolumeName(r); vol != "" {
		root = vol + string(filepath.Separator)
	}
	fi, err := os.Stat(r)
	if err != nil {
		return false
	}
	ri, err := os.Stat(root)
	return err == nil && os.SameFile(fi, ri)
}

func extSet(exts ...string) map[string]bool {
	m := make(map[string]bool, len(exts))
	for _, e := range exts {
		m[strings.ToLower(e)] = true
	}
	return m
}

var (
	lmStudioExts = extSet(".gguf")
	gpt4AllExts  = extSet(".gguf", ".bin")
	scanPathExts = extSet(ModelFileExtensions...)
)

// Result is a scan's models plus the notes that explain what it skipped.
type Result struct {
	Models []LocalModel
	Notes  []string // sorted, de-duplicated NoteDepthLimited / NotePathRoot
}

// ScanResult detects locally-installed AI models. A walk stops when ctx is
// done (the scan deadline) and ScanResult returns ctx.Err(), instead of
// running on behind the deadline (B-281's still_running); payload.collect
// records that as a timeout. Partial hits from an interrupted walk are
// dropped, not reported as a complete result.
func (s *Scanner) ScanResult(ctx context.Context, opts ScanOptions) (Result, error) {
	if opts.MinSizeMB == 0 {
		opts.MinSizeMB = 100
	}
	minBytes := opts.MinSizeMB * 1024 * 1024
	var res Result
	notes := map[string]bool{}
	add := func(ms []LocalModel, limited bool) {
		res.Models = append(res.Models, ms...)
		if limited {
			notes[NoteDepthLimited] = true
		}
	}

	if ollama, err := s.scanOllama(ctx); err == nil {
		res.Models = append(res.Models, ollama...)
	}
	if ms, limited, err := scanDir(ctx, defaultLMStudioPath(), lmStudioExts, SourceLMStudio, minBytes); err == nil {
		add(ms, limited)
	}
	if ms, limited, err := scanHuggingFace(ctx, defaultHFCachePath(), minBytes); err == nil {
		add(ms, limited)
	}
	if ms, limited, err := scanDir(ctx, defaultGPT4AllPath(), gpt4AllExts, SourceGPT4All, minBytes); err == nil {
		add(ms, limited)
	}
	for _, p := range opts.ExtraScanPaths {
		// Remote config never delivers a root or a non-normal path
		// (remoteconfig rejects them); these catch a hand-edited local YAML
		// and links that lead to the root or a share.
		if scanpath.IsRoot(p) || !scanpath.IsNormalized(p) || scanpath.HasStrayColon(p) {
			notes[NotePathRoot] = true
			continue
		}
		if scanpath.IsNetwork(p) {
			notes[NotePathNetwork] = true
			continue
		}
		resolved, note := resolveScanRoot(p)
		if note != "" {
			notes[note] = true
			continue
		}
		if resolved == "" {
			continue
		}
		if ms, limited, err := scanDir(ctx, resolved, scanPathExts, SourceScanPath, minBytes); err == nil {
			add(ms, limited)
		}
	}
	for n := range notes {
		res.Notes = append(res.Notes, n)
	}
	sort.Strings(res.Notes)
	return res, ctx.Err()
}

// Scan detects locally-installed AI models (models only; see ScanResult).
func (s *Scanner) Scan(ctx context.Context, opts ScanOptions) ([]LocalModel, error) {
	r, err := s.ScanResult(ctx, opts)
	return r.Models, err
}

// Scan is a package-level convenience wrapper.
func Scan(ctx context.Context, opts ScanOptions) ([]LocalModel, error) {
	return New().Scan(ctx, opts)
}

// ScanWithNotes is the package-level ScanResult.
func ScanWithNotes(ctx context.Context, opts ScanOptions) (Result, error) {
	return New().ScanResult(ctx, opts)
}

// depthOf is how many directory levels p is below root.
func depthOf(root, p string) int {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

// --- Ollama ---

type ollamaTagsResponse struct {
	Models []struct {
		Name       string    `json:"name"`
		Size       int64     `json:"size"`
		ModifiedAt time.Time `json:"modified_at"`
	} `json:"models"`
}

func (s *Scanner) scanOllama(ctx context.Context) ([]LocalModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ollamaBaseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var tags ollamaTagsResponse
	if err := json.Unmarshal(body, &tags); err != nil {
		return nil, err
	}
	out := make([]LocalModel, 0, len(tags.Models))
	for _, m := range tags.Models {
		out = append(out, LocalModel{
			Name: m.Name, Source: SourceOllama,
			SizeBytes: m.Size, ModifiedAt: m.ModifiedAt,
		})
	}
	return out, nil
}

// --- Filesystem ---

// scanDir walks root for files with one of exts, at or over minBytes. It
// enters at most MaxWalkDepth directory levels (reporting depthLimited when
// it had to stop) and stops when ctx is done. WalkDir never follows
// symlinked directories.
func scanDir(ctx context.Context, root string, exts map[string]bool, src Source, minBytes int64) (ms []LocalModel, depthLimited bool, err error) {
	if root == "" {
		return nil, false, nil
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, false, nil
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if depthOf(root, path) > MaxWalkDepth {
				depthLimited = true
				return filepath.SkipDir
			}
			return nil
		}
		if !exts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() < minBytes {
			return nil
		}
		ms = append(ms, LocalModel{
			Name: d.Name(), Source: src, FilePath: path,
			SizeBytes: info.Size(), ModifiedAt: info.ModTime(),
		})
		return nil
	})
	return ms, depthLimited, err
}

// scanHuggingFace walks the hub cache (same depth limit and ctx stop; no
// type filter: a snapshot's size is the sum of its files by design).
func scanHuggingFace(ctx context.Context, root string, minBytes int64) (ms []LocalModel, depthLimited bool, err error) {
	if root == "" {
		return nil, false, nil
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, false, nil
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if depthOf(root, path) > MaxWalkDepth {
				depthLimited = true
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "config.json" {
			return nil
		}
		modelDir := filepath.Base(filepath.Dir(filepath.Dir(path)))
		modelName := strings.ReplaceAll(strings.TrimPrefix(modelDir, "models--"), "--", "/")
		modelType, arch := parseHFConfig(path)
		snapshotDir := filepath.Dir(path)
		var total int64
		_ = filepath.WalkDir(snapshotDir, func(_ string, di os.DirEntry, e error) error {
			if e == nil && !di.IsDir() {
				if inf, err := di.Info(); err == nil {
					total += inf.Size()
				}
			}
			return nil
		})
		if total < minBytes {
			return nil
		}
		info, _ := d.Info()
		var mod time.Time
		if info != nil {
			mod = info.ModTime()
		}
		ms = append(ms, LocalModel{
			Name: modelName, Source: SourceHuggingFace, FilePath: snapshotDir,
			SizeBytes: total, ModifiedAt: mod, ModelType: modelType, Architecture: arch,
		})
		return nil
	})
	return ms, depthLimited, err
}

type hfConfig struct {
	ModelType     string   `json:"model_type"`
	Architectures []string `json:"architectures"`
}

func parseHFConfig(path string) (modelType, arch string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	var cfg hfConfig
	if err := json.NewDecoder(io.LimitReader(f, 64*1024)).Decode(&cfg); err != nil {
		return
	}
	modelType = cfg.ModelType
	if len(cfg.Architectures) > 0 {
		arch = cfg.Architectures[0]
	}
	return
}

// homeDir returns the user's home directory.
func homeDir() string {
	if v := os.Getenv("USERPROFILE"); v != "" {
		return v
	}
	h, _ := os.UserHomeDir()
	return h
}

// hfCachePath is the same on all platforms.
func hfCachePath() string {
	if v := os.Getenv("HF_HOME"); v != "" {
		return filepath.Join(v, "hub")
	}
	return filepath.Join(homeDir(), ".cache", "huggingface", "hub")
}

// lmStudioPath and gpt4AllPath are platform-specific (scanner_paths_*.go).
