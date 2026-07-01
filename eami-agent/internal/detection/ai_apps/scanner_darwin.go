//go:build darwin

package ai_apps

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

func Scan(_ context.Context) ([]AIApp, error) {
	home, _ := os.UserHomeDir()
	var apps []AIApp
	for _, dir := range []string{"/Applications", filepath.Join(home, "Applications")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".app") {
				continue
			}
			display := strings.TrimSuffix(e.Name(), ".app")
			if !isKnown(display) {
				continue
			}
			appPath := filepath.Join(dir, e.Name())
			apps = append(apps, AIApp{
				Name:    display,
				Version: bundleVersion(appPath),
				Path:    appPath,
				Source:  "applications_dir",
			})
		}
	}
	return apps, nil
}

func bundleVersion(appPath string) string {
	data, err := os.ReadFile(filepath.Join(appPath, "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	s := string(data)
	key := "CFBundleShortVersionString"
	idx := strings.Index(s, key)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(key):]
	start := strings.Index(rest, "<string>")
	end := strings.Index(rest, "</string>")
	if start < 0 || end <= start {
		return ""
	}
	return rest[start+len("<string>") : end]
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
