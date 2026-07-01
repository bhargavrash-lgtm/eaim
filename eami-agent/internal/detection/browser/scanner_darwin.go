//go:build darwin

package browser

import (
	"os"
	"path/filepath"
)

// platformDirs returns Chrome and Edge extension directories for the current
// user on macOS.
func platformDirs() []ExtensionDir {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	base := filepath.Join(home, "Library", "Application Support")
	username := filepath.Base(home)

	return []ExtensionDir{
		{
			Browser:  "chrome",
			Path:     filepath.Join(base, "Google", "Chrome", "Default", "Extensions"),
			UserPath: username,
		},
		{
			Browser:  "edge",
			Path:     filepath.Join(base, "Microsoft Edge", "Default", "Extensions"),
			UserPath: username,
		},
	}
}
