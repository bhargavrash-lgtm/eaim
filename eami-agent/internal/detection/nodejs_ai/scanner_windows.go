//go:build windows

package nodejs_ai

import (
	"context"
	"os"
	"path/filepath"
)

// Scan detects Node.js projects with AI packages on Windows.
// It walks the user home directory (depth-3) for package.json files and also
// checks the default global npm modules directory (%APPDATA%\npm\node_modules).
func Scan(ctx context.Context) ([]NodeProject, error) {
	home, _ := os.UserHomeDir()
	appData := os.Getenv("APPDATA") // e.g. C:\Users\alice\AppData\Roaming

	var projects []NodeProject

	// Walk home dir up to depth-3 to cover typical project layouts:
	//   C:\Users\alice\projects\my-ai-app\package.json
	projects = append(projects, scanDir(ctx, home, 3)...)

	// Global npm packages — checked at depth-1 since each top-level entry is
	// already a package root (e.g. %APPDATA%\npm\node_modules\openai\package.json).
	if appData != "" {
		globalNpm := filepath.Join(appData, "npm", "node_modules")
		projects = append(projects, scanDir(ctx, globalNpm, 1)...)
	}

	return projects, nil
}
