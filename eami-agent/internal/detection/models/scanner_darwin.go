//go:build darwin

package models

import "path/filepath"

func lmStudioPath() string {
	return filepath.Join(homeDir(), "Documents", "LM Studio", "models")
}

func gpt4AllPath() string {
	return filepath.Join(homeDir(), "Library", "Application Support", "nomic.ai", "GPT4All")
}
