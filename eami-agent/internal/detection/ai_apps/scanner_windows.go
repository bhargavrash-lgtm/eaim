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
// scanning %LOCALAPPDATA% for Squirrel-installed apps (e.g. Cursor) which skip
// the Uninstall registry entirely, then MSIX/AppX packages (e.g. Claude Desktop,
// which the Uninstall/Squirrel mechanisms never cover at all), then per-user
// ~/.local/bin CLI installs (e.g. Claude Code).
//
// This process runs as the EAMIAgent Windows service under LocalSystem (see
// installer/Product.wxs), not the interactive user. LocalSystem has its own
// (effectively always-empty) profile, so registry.CURRENT_USER and
// os.Getenv("LOCALAPPDATA") resolve to LocalSystem's own hive/profile, never
// the real logged-in user's -- meaning scanSquirrelApps and the HKCU half of
// the Uninstall walk above only ever see LocalSystem's own data in practice
// when running as this service (they still work correctly when the binary is
// run interactively by a user, e.g. local dev/testing). scanMSIXApps and
// scanUserLocalBinApps below are written to work under the service by
// explicitly walking real logged-on users' hives/profiles (via HKEY_USERS and
// C:\Users) rather than relying on this process's own per-user context --
// confirmed necessary and correct against this machine's real state (Claude
// Desktop ships as MSIX under a per-user HKEY_USERS\<SID>_Classes hive; Claude
// Code installs to a specific user's C:\Users\<user>\.local\bin). The same
// LocalSystem-vs-interactive-user gap affects scanSquirrelApps and the HKCU
// Uninstall walk for any other per-user-only-installed app (e.g. Cursor) --
// disclosed, not fixed here (out of this fix's scope, see BACKLOG.md).
func Scan(_ context.Context) ([]AIApp, error) {
	seen := make(map[string]bool)
	var apps []AIApp

	for _, hive := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		for _, keyPath := range uninstallKeyPaths {
			apps = append(apps, scanUninstallKey(hive, keyPath, seen)...)
		}
	}
	apps = append(apps, scanSquirrelApps(seen)...)
	apps = append(apps, scanMSIXApps(seen)...)
	apps = append(apps, scanUserLocalBinApps(seen)...)
	return apps, nil
}

// msixDisplayNameOverrides renames an MSIX package's own DisplayName to a
// clearer, more specific label where the raw package name is ambiguous.
// "Claude" is Anthropic's actual MSIX package DisplayName for Claude Desktop
// (confirmed via a real installed package on this machine: PackageFullName
// "Claude_2.2553.1.0_x64__pzs8sxrjxfjjc", DisplayName "Claude") -- renamed
// here so it can't collide with scanUserLocalBinApps' "Claude Code" entry.
var msixDisplayNameOverrides = map[string]string{
	"claude": "Claude Desktop",
}

// localBinDisplayNameOverrides mirrors msixDisplayNameOverrides for CLI tools
// found under a user's ~/.local/bin (e.g. Claude Code's real install shape on
// this machine: C:\Users\<user>\.local\bin\claude.exe).
var localBinDisplayNameOverrides = map[string]string{
	"claude": "Claude Code",
}

// scanMSIXApps detects MSIX/AppX-packaged apps (e.g. Claude Desktop), which
// register in neither the Uninstall registry key nor a Squirrel LocalAppData
// directory. Real per-user MSIX package metadata lives under
// HKEY_USERS\<SID>_Classes\Local Settings\Software\Microsoft\Windows\
// CurrentVersion\AppModel\Repository\Packages\<PackageFullName> -- verified
// directly against this machine's real registry (HKCU's own equivalent path
// is where Get-AppxPackage reads from; SYSTEM's own HKEY_USERS entry has no
// such data, so this must walk real users' _Classes hives explicitly).
func scanMSIXApps(seen map[string]bool) []AIApp {
	var apps []AIApp
	for _, sid := range loggedOnUserClassesSIDs() {
		keyPath := sid + `\Local Settings\Software\Microsoft\Windows\CurrentVersion\AppModel\Repository\Packages`
		k, err := registry.OpenKey(registry.USERS, keyPath, registry.READ)
		if err != nil {
			continue
		}
		names, err := k.ReadSubKeyNames(-1)
		k.Close()
		if err != nil {
			continue
		}
		for _, fullName := range names {
			apps = append(apps, readMSIXPackage(sid, keyPath, fullName, seen)...)
		}
	}
	return apps
}

func readMSIXPackage(sid, parentKeyPath, fullName string, seen map[string]bool) []AIApp {
	k, err := registry.OpenKey(registry.USERS, parentKeyPath+`\`+fullName, registry.READ)
	if err != nil {
		return nil
	}
	defer k.Close()

	displayName, _, err := k.GetStringValue("DisplayName")
	if err != nil || displayName == "" || strings.HasPrefix(displayName, "ms-resource:") {
		// No usable plain-string DisplayName (some packages only ship a
		// resource-indirect string here) -- fall back to the package family
		// name, which is always a plain literal (the segment of
		// PackageFullName before its first "_", per Windows' own naming
		// convention: <Name>_<Version>_<Arch>__<PublisherId>).
		if idx := strings.Index(fullName, "_"); idx > 0 {
			displayName = fullName[:idx]
		} else {
			displayName = fullName
		}
	}
	if !isKnown(displayName) {
		return nil
	}

	name := displayName
	if override, ok := msixDisplayNameOverrides[strings.ToLower(displayName)]; ok {
		name = override
	}
	dedup := strings.ToLower(name)
	if seen[dedup] {
		return nil
	}
	seen[dedup] = true

	version := ""
	if parts := strings.Split(fullName, "_"); len(parts) > 1 {
		version = parts[1]
	}
	path, _, _ := k.GetStringValue("PackageRootFolder")

	return []AIApp{{
		Name:    name,
		Version: version,
		Path:    path,
		Source:  "msix",
	}}
}

// scanUserLocalBinApps detects CLI tools installed to a real user's
// ~/.local/bin (the Windows equivalent, %USERPROFILE%\.local\bin) -- the
// actual, verified real install shape of Claude Code on this machine
// (C:\Users\<user>\.local\bin\claude.exe), which the Uninstall registry,
// Squirrel LocalAppData convention, and MSIX packaging all miss entirely
// (it's a plain copied binary, not installed through any of those
// mechanisms). Must walk real user profile directories explicitly for the
// same LocalSystem-vs-interactive-user reason documented on Scan() above.
func scanUserLocalBinApps(seen map[string]bool) []AIApp {
	var apps []AIApp
	for _, profileDir := range realUserProfileDirs() {
		binDir := filepath.Join(profileDir, ".local", "bin")
		entries, err := os.ReadDir(binDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".exe") {
				continue
			}
			base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			if !isKnown(base) {
				continue
			}
			name := base
			if override, ok := localBinDisplayNameOverrides[strings.ToLower(base)]; ok {
				name = override
			}
			dedup := strings.ToLower(name)
			if seen[dedup] {
				continue
			}
			seen[dedup] = true
			apps = append(apps, AIApp{
				Name:   name,
				Path:   filepath.Join(binDir, e.Name()),
				Source: "user_local_bin",
			})
		}
	}
	return apps
}

// loggedOnUserClassesSIDs returns the "<SID>_Classes" HKEY_USERS subkey names
// for every currently loaded real user hive. Only an interactively logged-on
// user has this hive loaded (it backs their per-user UsrClass.dat), which is
// exactly the set of users this scanner can meaningfully inspect from a
// LocalSystem service -- there is no way to read a logged-off user's registry
// data without loading their hive file directly, deliberately not attempted
// here (that requires the user's own NTUSER.DAT/UsrClass.dat file path and
// carries real risk of corrupting it if done incorrectly).
func loggedOnUserClassesSIDs() []string {
	k, err := registry.OpenKey(registry.USERS, "", registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer k.Close()

	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	var sids []string
	for _, n := range names {
		if strings.HasPrefix(n, "S-1-5-21-") && strings.HasSuffix(n, "_Classes") {
			sids = append(sids, n)
		}
	}
	return sids
}

// realUserProfileDirs returns real interactive users' profile directories
// under C:\Users, excluding known non-user pseudo-profiles.
func realUserProfileDirs() []string {
	systemDrive := os.Getenv("SystemDrive")
	if systemDrive == "" {
		systemDrive = "C:"
	}
	usersDir := systemDrive + `\Users`
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		return nil
	}
	excluded := map[string]bool{
		"public":       true,
		"default":      true,
		"default user": true,
		"all users":    true,
		"defaultuser0": true,
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() || excluded[strings.ToLower(e.Name())] {
			continue
		}
		dirs = append(dirs, filepath.Join(usersDir, e.Name()))
	}
	return dirs
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
