// Package ai_apps detects installed AI applications on the endpoint.
package ai_apps

// AIApp describes a detected installed AI application.
type AIApp struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Path    string `json:"path"`
	Source  string `json:"source"` // registry, applications_dir, desktop_file, bin
}

var knownAIApps = []string{
	"claude", "cursor", "chatgpt", "lm studio", "lmstudio",
	"gpt4all", "jan", "ollama", "msty", "openwebui", "comfyui",
	"pinokio", "librechat", "koboldai",
}
