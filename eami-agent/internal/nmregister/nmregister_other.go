//go:build !windows

package nmregister

import "errors"

// ErrNotSupported is returned by Install/Uninstall on non-Windows
// platforms: there is no registry step here, native messaging
// registration is a plain manifest-file drop into a well-known per-browser
// directory, handled directly by the installer's postinstall script
// (installer/linux/postinstall.sh, installer/macos/postinstall) rather
// than through this CLI flag -- see those scripts for what's written and
// where.
var ErrNotSupported = errors.New("nmregister: native-messaging registration via --register-native-messaging is Windows-only on this build; see installer/linux or installer/macos postinstall scripts for this platform's registration")

// Install always fails on non-Windows builds; see ErrNotSupported.
func Install(exePath string) error { return ErrNotSupported }

// Uninstall always fails on non-Windows builds; see ErrNotSupported.
func Uninstall(exePath string) error { return ErrNotSupported }
