//go:build linux

package browser

import (
	"os"
	"path/filepath"
)

// platformDirs returns Chrome and Edge extension directories for the current
// user on Linux.
func platformDirs() []ExtensionDir {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	username := filepath.Base(home)

	return []ExtensionDir{
		{
			Browser:  "chrome",
			Path:     filepath.Join(home, ".config", "google-chrome", "Default", "Extensions"),
			UserPath: username,
		},
		{
			Browser:  "edge",
			Path:     filepath.Join(home, ".config", "microsoft-edge", "Default", "Extensions"),
			UserPath: username,
		},
	}
}
