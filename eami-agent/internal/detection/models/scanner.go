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
	"strings"
	"time"
)

type Source string

const (
	SourceOllama      Source = "ollama"
	SourceLMStudio    Source = "lm_studio"
	SourceHuggingFace Source = "huggingface"
	SourceGPT4All     Source = "gpt4all"
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

// Scan detects locally-installed AI models.
func (s *Scanner) Scan(ctx context.Context, opts ScanOptions) ([]LocalModel, error) {
	if opts.MinSizeMB == 0 {
		opts.MinSizeMB = 100
	}
	minBytes := opts.MinSizeMB * 1024 * 1024
	var results []LocalModel

	if ollama, err := s.scanOllama(ctx); err == nil {
		results = append(results, ollama...)
	}
	if lms, err := scanDir(lmStudioPath(), ".gguf", SourceLMStudio, minBytes); err == nil {
		results = append(results, lms...)
	}
	if hf, err := scanHuggingFace(hfCachePath(), minBytes); err == nil {
		results = append(results, hf...)
	}
	if gpt, err := scanDir(gpt4AllPath(), "", SourceGPT4All, minBytes); err == nil {
		results = append(results, gpt...)
	}
	for _, p := range opts.ExtraScanPaths {
		if extra, err := scanDir(p, "", SourceLMStudio, minBytes); err == nil {
			results = append(results, extra...)
		}
	}
	return results, nil
}

// Scan is a package-level convenience wrapper.
func Scan(ctx context.Context, opts ScanOptions) ([]LocalModel, error) {
	return New().Scan(ctx, opts)
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

func scanDir(root, ext string, src Source, minBytes int64) ([]LocalModel, error) {
	if root == "" {
		return nil, nil
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	}
	var ms []LocalModel
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if ext != "" && !strings.EqualFold(filepath.Ext(path), ext) {
			return nil
		}
		if src == SourceGPT4All {
			e := strings.ToLower(filepath.Ext(path))
			if e != ".gguf" && e != ".bin" {
				return nil
			}
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
	return ms, err
}

func scanHuggingFace(root string, minBytes int64) ([]LocalModel, error) {
	if root == "" {
		return nil, nil
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	}
	var ms []LocalModel
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "config.json" {
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
	return ms, err
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
