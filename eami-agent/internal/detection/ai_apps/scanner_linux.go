//go:build linux

package ai_apps

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
)

func Scan(_ context.Context) ([]AIApp, error) {
	var apps []AIApp
	apps = append(apps, scanDesktopFiles()...)
	apps = append(apps, scanBinaries()...)
	return apps, nil
}

func scanDesktopFiles() []AIApp {
	home, _ := os.UserHomeDir()
	dirs := []string{
		"/usr/share/applications",
		"/usr/local/share/applications",
		filepath.Join(home, ".local", "share", "applications"),
	}
	var apps []AIApp
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".desktop") {
				continue
			}
			name, exec := parseDesktop(filepath.Join(dir, e.Name()))
			if name == "" {
				continue
			}
			if !isKnown(name) && !isKnown(strings.TrimSuffix(e.Name(), ".desktop")) {
				continue
			}
			apps = append(apps, AIApp{Name: name, Path: exec, Source: "desktop_file"})
		}
	}
	return apps
}

func parseDesktop(path string) (name, exec string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "Name=") && name == "" {
			name = strings.TrimPrefix(line, "Name=")
		}
		if strings.HasPrefix(line, "Exec=") && exec == "" {
			parts := strings.Fields(strings.TrimPrefix(line, "Exec="))
			if len(parts) > 0 {
				exec = parts[0]
			}
		}
	}
	return
}

func scanBinaries() []AIApp {
	bins := []string{"ollama", "cursor", "lmstudio", "jan", "gpt4all", "localai"}
	dirs := []string{"/usr/bin", "/usr/local/bin", "/opt/bin"}
	var apps []AIApp
	for _, dir := range dirs {
		for _, bin := range bins {
			path := filepath.Join(dir, bin)
			if _, err := os.Stat(path); err == nil {
				apps = append(apps, AIApp{Name: bin, Path: path, Source: "bin"})
			}
		}
	}
	return apps
}

func isKnown(name string) bool {
	lower := strings.ToLower(name)
	for _, k := range knownAIApps {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}
