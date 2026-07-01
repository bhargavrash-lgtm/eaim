// Package nodejs_ai detects Node.js projects with AI packages installed.
package nodejs_ai

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// NodeProject describes a Node.js project with AI packages.
type NodeProject struct {
	Path       string    `json:"path"`
	AIPackages []string  `json:"ai_packages"`
	DetectedAt time.Time `json:"detected_at"`
}

var knownAINodePackages = []string{
	"openai", "@anthropic-ai/sdk", "langchain", "@langchain/core",
	"@langchain/openai", "@langchain/anthropic", "@google/generative-ai",
	"ai", "ollama", "@ollama/ollama", "llamaindex", "groq-sdk",
	"cohere-ai", "mistralai", "@mistralai/mistralai",
	"@huggingface/inference", "@aws-sdk/client-bedrock-runtime",
}

type packageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func scanPackageJSON(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	all := make(map[string]string, len(pkg.Dependencies)+len(pkg.DevDependencies))
	for k, v := range pkg.Dependencies {
		all[k] = v
	}
	for k, v := range pkg.DevDependencies {
		all[k] = v
	}
	var found []string
	for _, known := range knownAINodePackages {
		if _, ok := all[known]; ok {
			found = append(found, known)
		}
	}
	return found
}

func scanDir(ctx context.Context, root string, maxDepth int) []NodeProject {
	var projects []NodeProject
	walkLimited(ctx, root, 0, maxDepth, func(dir string) {
		pkgs := scanPackageJSON(filepath.Join(dir, "package.json"))
		if len(pkgs) > 0 {
			projects = append(projects, NodeProject{
				Path: dir, AIPackages: pkgs, DetectedAt: time.Now().UTC(),
			})
		}
	})
	return projects
}

func walkLimited(ctx context.Context, dir string, depth, maxDepth int, fn func(string)) {
	if depth > maxDepth || ctx.Err() != nil {
		return
	}
	fn(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" {
			continue
		}
		walkLimited(ctx, filepath.Join(dir, e.Name()), depth+1, maxDepth, fn)
	}
}
