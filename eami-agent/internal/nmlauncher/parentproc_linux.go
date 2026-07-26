//go:build linux

package nmlauncher

import (
	"fmt"
	"os"
)

// parentProcessName resolves the current process's immediate parent's
// executable path via /proc/<ppid>/exe, the standard Linux mechanism.
//
// NOTE: implemented and unit-testable (see the symlink-based test), but
// not exercised on a live Linux host in this development session (this
// machine is Windows) -- flagged here rather than claimed as verified,
// matching this codebase's established honesty convention for
// platforms that can't be tested directly in a given session.
func parentProcessName() (string, error) {
	ppid := os.Getppid()
	if ppid <= 0 {
		return "", fmt.Errorf("no parent process id available")
	}
	target, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", ppid))
	if err != nil {
		return "", fmt.Errorf("read /proc/%d/exe: %w", ppid, err)
	}
	return target, nil
}
