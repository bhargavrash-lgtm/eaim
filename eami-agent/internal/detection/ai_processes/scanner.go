// Package ai_processes detects running processes whose name or path suggests AI tooling.
package ai_processes

import (
	"strings"
	"time"
)

// AIProcess describes a detected AI-related running process.
type AIProcess struct {
	PID         int       `json:"pid"`
	Name        string    `json:"name"`
	ExePath     string    `json:"exe_path,omitempty"`
	CommandLine string    `json:"command_line,omitempty"`
	DetectedAt  time.Time `json:"detected_at"`
}

var knownAIProcessNames = []string{
	"ollama", "lms", "lm studio", "gpt4all", "jan", "cursor",
	"comfyui", "koboldcpp", "stable-diffusion", "text-generation-webui",
	"jupyter", "localai", "llama", "mistral", "openwebui",
}

var knownAIPythonKeywords = []string{
	"ollama", "langchain", "transformers", "openai", "anthropic",
	"llama", "diffusers", "autogen", "crewai", "llamaindex",
	"huggingface", "fastchat", "vllm",
}

// IsAIProcess returns true if name/cmdline/exe indicates AI tooling.
func IsAIProcess(name, cmdline, exePath string) bool {
	lower := strings.ToLower(name)
	cmdLower := strings.ToLower(cmdline)
	exeLower := strings.ToLower(exePath)
	for _, ai := range knownAIProcessNames {
		if strings.Contains(lower, ai) || strings.Contains(exeLower, ai) {
			return true
		}
	}
	if lower == "python" || lower == "python3" || strings.HasPrefix(lower, "python3.") {
		for _, kw := range knownAIPythonKeywords {
			if strings.Contains(cmdLower, kw) {
				return true
			}
		}
	}
	return false
}

// Scan is provided by platform-specific files.
