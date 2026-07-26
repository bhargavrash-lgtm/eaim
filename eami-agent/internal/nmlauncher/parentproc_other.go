//go:build !windows && !linux && !darwin

package nmlauncher

import "fmt"

// parentProcessName is not implemented on this platform. Treated as
// "could not determine" by VerifyLaunchedByBrowser, which fails open
// (with a loud warning) rather than breaking the feature entirely.
func parentProcessName() (string, error) {
	return "", fmt.Errorf("parent process detection is not implemented on this platform")
}
