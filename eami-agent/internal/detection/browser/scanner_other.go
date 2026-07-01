//go:build !windows && !darwin && !linux

package browser

// platformDirs returns no directories on unsupported platforms.
func platformDirs() []ExtensionDir { return nil }
