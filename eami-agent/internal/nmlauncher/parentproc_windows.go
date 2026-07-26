//go:build windows

package nmlauncher

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// parentProcessName resolves the image filename of the current process's
// immediate parent via a toolhelp snapshot -- the standard Win32
// mechanism for walking process ancestry (there is no direct
// GetParentProcess API). Mirrors the same pattern
// golang.org/x/sys/windows itself uses internally for its unexported
// Getppid() helper, but additionally returns the parent's executable
// name, not just its PID.
func parentProcessName() (string, error) {
	ppid := os.Getppid()
	if ppid <= 0 {
		return "", fmt.Errorf("no parent process id available")
	}

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", fmt.Errorf("create process snapshot: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return "", fmt.Errorf("enumerate processes: %w", err)
	}
	for {
		if entry.ProcessID == uint32(ppid) {
			return syscall.UTF16ToString(entry.ExeFile[:]), nil
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			return "", fmt.Errorf("parent process id %d not found in snapshot (exited before it could be checked?): %w", ppid, err)
		}
	}
}
