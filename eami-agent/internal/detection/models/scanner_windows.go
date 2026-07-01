//go:build windows

package models

import (
	"os"
	"path/filepath"
)

func lmStudioPath() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return ""
	}
	return filepath.Join(base, "LM-Studio", "models")
}

func gpt4AllPath() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return ""
	}
	return filepath.Join(base, "nomic.ai", "GPT4All")
}
