//go:build windows

package ai_apps

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// squirrelDirs maps a LocalAppData subdirectory name to the display name to use.
// Squirrel-installed apps live in %LOCALAPPDATA%\<dir>\app-<version>\<exe>.
var squirrelDirs = map[string]string{
	"AnthropicClaude": "Claude",
	"cursor":          "Cursor",
}

// uninstallKeyPaths are the registry paths that list installed applications.
// We check both 64-bit and 32-bit (WOW6432Node) hives to catch all installers.
var uninstallKeyPaths = []string{
	`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`,
	`SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`,
}

// Scan detects installed AI apps on Windows by walking the Uninstall registry keys
// in both HKLM (system-wide) and HKCU (current user) hives, then falls back to
// scanning %LOCALAPPDATA% for Squirrel-installed apps (e.g. Claude Desktop, Cursor)
// which skip the Uninstall registry entirely.
func Scan(_ context.Context) ([]AIApp, error) {
	seen := make(map[string]bool)
	var apps []AIApp

	for _, hive := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		for _, keyPath := range uninstallKeyPaths {
			apps = append(apps, scanUninstallKey(hive, keyPath, seen)...)
		}
	}
	apps = append(apps, scanSquirrelApps(seen)...)
	return apps, nil
}

// scanSquirrelApps detects Squirrel-installed Electron apps that don't register
// in the Windows Uninstall key. Squirrel creates %LOCALAPPDATA%\<AppDir>\app-<version>\.
func scanSquirrelApps(seen map[string]bool) []AIApp {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return nil
	}
	var out []AIApp
	for dir, displayName := range squirrelDirs {
		dedup := strings.ToLower(displayName)
		if seen[dedup] {
			continue // already found via registry
		}
		base := filepath.Join(localAppData, dir)
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		// Find the latest app-<version> directory.
		version := ""
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "app-") {
				version = strings.TrimPrefix(e.Name(), "app-")
			}
		}
		if version == "" {
			continue
		}
		seen[dedup] = true
		out = append(out, AIApp{
			Name:    displayName,
			Version: version,
			Path:    base,
			Source:  "localappdata",
		})
	}
	return out
}

func scanUninstallKey(hive registry.Key, keyPath string, seen map[string]bool) []AIApp {
	k, err := registry.OpenKey(hive, keyPath, registry.READ)
	if err != nil {
		return nil
	}
	defer k.Close()

	subkeys, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}

	var apps []AIApp
	for _, sub := range subkeys {
		app := readUninstallEntry(hive, keyPath+`\`+sub)
		if app == nil {
			continue
		}
		dedup := strings.ToLower(app.Name)
		if seen[dedup] {
			continue
		}
		seen[dedup] = true
		apps = append(apps, *app)
	}
	return apps
}

func readUninstallEntry(hive registry.Key, keyPath string) *AIApp {
	k, err := registry.OpenKey(hive, keyPath, registry.READ)
	if err != nil {
		return nil
	}
	defer k.Close()

	displayName, _, err := k.GetStringValue("DisplayName")
	if err != nil || !isKnown(displayName) {
		return nil
	}

	version, _, _ := k.GetStringValue("DisplayVersion")
	installPath, _, _ := k.GetStringValue("InstallLocation")
	if installPath == "" {
		// Fallback: DisplayIcon is often "C:\path\to\app.exe,0" — strip the icon index.
		icon, _, _ := k.GetStringValue("DisplayIcon")
		if idx := strings.LastIndex(icon, ","); idx > 0 {
			installPath = icon[:idx]
		} else {
			installPath = icon
		}
	}

	return &AIApp{
		Name:    displayName,
		Version: version,
		Path:    installPath,
		Source:  "registry",
	}
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
