// Package python_envs detects Python virtual environments with AI packages installed.
package python_envs

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PythonEnv describes a detected Python environment with AI packages.
type PythonEnv struct {
	Path       string    `json:"path"`
	Type       string    `json:"type"` // venv, conda, conda_base
	AIPackages []string  `json:"ai_packages"`
	DetectedAt time.Time `json:"detected_at"`
}

var aiPackages = []string{
	"openai", "anthropic", "langchain", "transformers", "torch",
	"tensorflow", "keras", "diffusers", "accelerate", "datasets",
	"huggingface_hub", "sentence_transformers", "llama_cpp",
	"llama_index", "llama_index_core", "autogen", "pyautogen",
	"crewai", "pydantic_ai", "google_generativeai", "cohere",
	"mistralai", "groq", "ollama", "chromadb", "faiss",
	"langchain_community", "langchain_core", "langchain_openai",
}

func scanEnvPath(envPath, envType string) *PythonEnv {
	sp := findSitePackages(envPath)
	if sp == "" {
		return nil
	}
	pkgs := detectAIPackages(sp)
	if len(pkgs) == 0 {
		return nil
	}
	return &PythonEnv{Path: envPath, Type: envType, AIPackages: pkgs, DetectedAt: time.Now().UTC()}
}

func findSitePackages(root string) string {
	for _, candidate := range []string{
		filepath.Join(root, "lib", "site-packages"),
		filepath.Join(root, "Lib", "site-packages"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	matches, _ := filepath.Glob(filepath.Join(root, "lib", "python3.*", "site-packages"))
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.IsDir() {
			return m
		}
	}
	return ""
}

func detectAIPackages(sitePackages string) []string {
	entries, err := os.ReadDir(sitePackages)
	if err != nil {
		return nil
	}
	found := map[string]bool{}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		name = stripSuffix(name, ".dist-info")
		name = stripSuffix(name, ".egg-info")
		name = strings.ReplaceAll(name, "-", "_")
		for _, pkg := range aiPackages {
			if name == pkg || strings.HasPrefix(name, pkg+"_") {
				found[pkg] = true
			}
		}
	}
	result := make([]string, 0, len(found))
	for p := range found {
		result = append(result, p)
	}
	return result
}

func stripSuffix(s, suffix string) string {
	if strings.HasSuffix(s, suffix) {
		return s[:len(s)-len(suffix)]
	}
	return s
}

func isVenvDir(path string) bool {
	for _, check := range []string{
		filepath.Join(path, "pyvenv.cfg"),
		filepath.Join(path, "bin", "python"),
		filepath.Join(path, "bin", "python3"),
	} {
		if _, err := os.Stat(check); err == nil {
			return true
		}
	}
	return false
}
