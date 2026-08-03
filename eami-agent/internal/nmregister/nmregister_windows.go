//go:build windows

// On Windows, registration means writing a manifest JSON file next to a
// hard-linked launcher copy of the installed binary, and pointing
// Chrome's and Edge's NativeMessagingHosts registry keys at that
// manifest -- the standard, documented mechanism both browsers use to
// discover native messaging hosts.
package nmregister

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// AllowedExtensionID is B1's real, stable extension ID -- eami-browser-extension's
// manifest.json pins this via its "key" field (an embedded RSA public
// key), which makes Chrome/Edge derive this exact ID deterministically
// (SHA-256 of the DER-encoded public key, first 16 bytes, hex nibbles
// mapped to a-p) regardless of install path or Web Store publication
// status. See eami-browser-extension/README.md for the full derivation
// and how to regenerate it with a different key if ever needed.
const AllowedExtensionID = "ngmdfnecljeoleiancdedbmhjdihaoaa"

// manifestFileName is written next to the agent binary.
const manifestFileName = "com.eami.agent-native-messaging.json"

var registryKeys = []string{
	`Software\Google\Chrome\NativeMessagingHosts\` + HostName,
	`Software\Microsoft\Edge\NativeMessagingHosts\` + HostName,
}

// manifest mirrors the Chrome/Edge native-messaging-host manifest schema.
// See https://developer.chrome.com/docs/apps/nativeMessaging for the
// authoritative field list -- only allowed_origins is used (not
// allowed_extensions, which is Firefox's key name for the same concept).
type manifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

// Install creates a hard-linked launcher copy of exePath (named per
// LauncherBaseName -- see its doc comment for why this indirection is
// required), writes the native messaging manifest pointing at that
// launcher, and registers it with both Chrome and Edge via HKLM registry
// keys. exePath should be the absolute path to the installed
// eami-agent.exe.
func Install(exePath string) error {
	absExe, err := filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("nmregister: resolve exe path: %w", err)
	}
	dir := filepath.Dir(absExe)
	launcherPath := filepath.Join(dir, LauncherBaseName+".exe")
	manifestPath := filepath.Join(dir, manifestFileName)

	// Remove any pre-existing launcher first (os.Link fails if the
	// destination already exists -- e.g. a reinstall/repair) then hard
	// link it to the real binary. A hard link means launcherPath is
	// always byte-identical to absExe (same underlying file), so an
	// agent upgrade that replaces absExe's content in place is
	// automatically reflected with no separate re-link step needed --
	// though an upgrade that replaces the *file* (new inode) rather than
	// overwriting in place would need Install to run again, which the
	// WiX CustomAction already does on every install/upgrade.
	if err := os.Remove(launcherPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("nmregister: remove stale launcher %s: %w", launcherPath, err)
	}
	if err := os.Link(absExe, launcherPath); err != nil {
		return fmt.Errorf("nmregister: create launcher hard link %s: %w", launcherPath, err)
	}

	m := manifest{
		Name:        HostName,
		Description: "EAMI Agent native messaging host (real-time paste-event relay)",
		Path:        launcherPath,
		Type:        "stdio",
		AllowedOrigins: []string{
			"chrome-extension://" + AllowedExtensionID + "/",
		},
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("nmregister: marshal manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, body, 0644); err != nil {
		return fmt.Errorf("nmregister: write manifest %s: %w", manifestPath, err)
	}

	for _, key := range registryKeys {
		if err := writeDefaultValue(key, manifestPath); err != nil {
			return fmt.Errorf("nmregister: register %s: %w", key, err)
		}
	}
	return nil
}

// Uninstall removes both browsers' registry registrations, the manifest
// file, and the launcher hard link created by Install. None of these
// three are declared as WiX File/Component entries in Product.wxs (they
// are generated dynamically by Install, not laid down by the MSI itself),
// so nothing else will clean them up on uninstall unless this function
// does -- exePath is needed to find them, same directory Install used.
func Uninstall(exePath string) error {
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	for _, key := range registryKeys {
		err := registry.DeleteKey(registry.LOCAL_MACHINE, key)
		if err != nil && err != registry.ErrNotExist {
			note(fmt.Errorf("nmregister: delete %s: %w", key, err))
		}
	}

	absExe, err := filepath.Abs(exePath)
	if err != nil {
		note(fmt.Errorf("nmregister: resolve exe path: %w", err))
		return firstErr
	}
	dir := filepath.Dir(absExe)
	launcherPath := filepath.Join(dir, LauncherBaseName+".exe")
	manifestPath := filepath.Join(dir, manifestFileName)

	if err := os.Remove(launcherPath); err != nil && !os.IsNotExist(err) {
		note(fmt.Errorf("nmregister: remove launcher %s: %w", launcherPath, err))
	}
	if err := os.Remove(manifestPath); err != nil && !os.IsNotExist(err) {
		note(fmt.Errorf("nmregister: remove manifest %s: %w", manifestPath, err))
	}
	return firstErr
}

// writeDefaultValue sets the (Default) value of HKLM\<key> to manifestPath
// -- the exact convention Chrome/Edge document and read from.
func writeDefaultValue(key, manifestPath string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue("", manifestPath)
}
