//go:build !windows

package service

import "fmt"

// Install is not supported on non-Windows platforms.
func Install(_ string) error {
	return fmt.Errorf("service: install only supported on Windows")
}

// Start is not supported on non-Windows platforms.
func Start() error {
	return fmt.Errorf("service: start only supported on Windows")
}

// Stop is not supported on non-Windows platforms.
func Stop() error {
	return fmt.Errorf("service: stop only supported on Windows")
}

// Uninstall is not supported on non-Windows platforms.
func Uninstall() error {
	return fmt.Errorf("service: uninstall only supported on Windows")
}

// RunAsService is not supported on non-Windows platforms.
func RunAsService(_ RunFn) error {
	return fmt.Errorf("service: not supported on non-Windows platforms")
}

// IsService always returns false on non-Windows platforms.
func IsService() (bool, error) {
	return false, nil
}
