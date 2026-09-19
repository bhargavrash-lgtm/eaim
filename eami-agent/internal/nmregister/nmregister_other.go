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

// EnsureRegistered is a no-op on non-Windows builds. The self-healing
// concern it addresses on Windows (Product.wxs's install/upgrade
// CustomActions being the only mechanism keeping registration correct)
// doesn't apply the same way here -- Linux/macOS registration is a plain
// manifest-file drop written once by the installer's postinstall script,
// not something this process's own service lifecycle can help keep in
// sync. Out of scope for this fix; see ErrNotSupported's doc comment for
// where that registration actually happens on these platforms.
func EnsureRegistered(exePath string) error { return nil }
