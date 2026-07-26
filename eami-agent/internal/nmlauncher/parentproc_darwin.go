//go:build darwin

package nmlauncher

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// parentProcessName resolves the current process's immediate parent's
// command name via `ps -o comm= -p <ppid>`. macOS has no /proc filesystem
// (unlike Linux); shelling out to the system `ps` utility (always present)
// avoids adding a cgo/third-party dependency just for this.
//
// NOTE: not exercised on a live macOS host in this development session
// (this machine is Windows) -- flagged rather than claimed as verified.
func parentProcessName() (string, error) {
	ppid := os.Getppid()
	if ppid <= 0 {
		return "", fmt.Errorf("no parent process id available")
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", fmt.Sprintf("%d", ppid)).Output()
	if err != nil {
		return "", fmt.Errorf("ps -p %d: %w", ppid, err)
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", fmt.Errorf("ps returned no output for pid %d", ppid)
	}
	return name, nil
}
