//go:build linux

package models

import "path/filepath"

func lmStudioPath() string {
	return filepath.Join(homeDir(), "lm-studio", "models")
}

func gpt4AllPath() string {
	return filepath.Join(homeDir(), ".local", "share", "nomic.ai", "GPT4All")
}
