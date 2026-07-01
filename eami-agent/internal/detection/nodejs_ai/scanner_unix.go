//go:build darwin || linux

package nodejs_ai

import (
	"context"
	"os"
	"path/filepath"
)

func Scan(ctx context.Context) ([]NodeProject, error) {
	home, _ := os.UserHomeDir()
	var projects []NodeProject
	projects = append(projects, scanDir(ctx, home, 3)...)
	for _, global := range []string{"/usr/local/lib/node_modules", "/usr/lib/node_modules"} {
		projects = append(projects, scanDir(ctx, global, 1)...)
	}
	nvmDir := filepath.Join(home, ".nvm", "versions", "node")
	if entries, err := os.ReadDir(nvmDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				projects = append(projects, scanDir(ctx,
					filepath.Join(nvmDir, e.Name(), "lib", "node_modules"), 1)...)
			}
		}
	}
	return projects, nil
}
