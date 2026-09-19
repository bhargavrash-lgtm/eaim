//go:build windows

package nmlauncher

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processCreationTime returns the wall-clock creation time of the process
// identified by pid, via OpenProcess+GetProcessTimes. PROCESS_QUERY_LIMITED_INFORMATION
// is the minimal access right this needs -- it's grantable across a user
// session boundary (unlike full PROCESS_QUERY_INFORMATION), which matters
// here since a browser's own privilege level relative to this process is
// not something to assume.
func processCreationTime(pid uint32) (time.Time, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return time.Time{}, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, fmt.Errorf("get process times for %d: %w", pid, err)
	}
	return time.Unix(0, creation.Nanoseconds()), nil
}

// parentProcessName resolves the image filename of the current process's
// immediate parent via a toolhelp snapshot -- the standard Win32
// mechanism for walking process ancestry (there is no direct
// GetParentProcess API). Mirrors the same pattern golang.org/x/sys/windows
// itself uses internally for its unexported Getppid() helper, but
// additionally returns the parent's executable name, not just its PID.
//
// PID-reuse race, found and fixed 2026-09-19 (same underlying mechanism as
// the original B-037 incident, 2026-08-03/08-04 -- see the package doc
// comment's History section; B-037's own fix addressed the allowlist
// coverage, not this): os.Getppid() returns the PID our process was
// CreateProcess'd under, a value fixed at our own creation time. If the
// real parent has since exited, Windows is free to hand that now-vacant
// PID to a completely unrelated process before this function's toolhelp
// walk runs -- an inherent race in any Getppid()-then-look-up-the-name-
// later pattern, worse the longer that gap is (this process's own Go
// runtime init, plus whatever load is on the host at the time). The old
// code trusted whatever name it found for that PID number unconditionally.
// Fixed by pairing the PID with a creation-time check: a genuine parent
// must have been created strictly before this process (it had to still be
// running to CreateProcess us), so if the process currently holding ppid
// has a creation time at or after our own, that PID was necessarily
// reused by an unrelated later process, not our real parent -- and this
// function now refuses to trust its name, returning an error instead
// (the caller's existing fail-open-when-inconclusive policy applies,
// unchanged; see nmlauncher.go's VerifyLaunchedByBrowser and the package
// doc comment for why that asymmetry, confirmed correct as-is, is not
// touched by this fix).
func parentProcessName() (string, error) {
	ppid := os.Getppid()
	if ppid <= 0 {
		return "", fmt.Errorf("no parent process id available")
	}

	ownCreation, err := processCreationTime(uint32(os.Getpid()))
	if err != nil {
		return "", fmt.Errorf("query own process creation time: %w", err)
	}
	parentCreation, err := processCreationTime(uint32(ppid))
	if err != nil {
		// Parent's handle can't even be opened -- it has already exited
		// and nothing has (yet) reused the PID. Genuinely inconclusive,
		// not a detected reuse -- same "could not determine" bucket the
		// caller already fails open on.
		return "", fmt.Errorf("parent process id %d: %w (likely already exited)", ppid, err)
	}
	if err := verifyNotReusedPID(ppid, parentCreation, ownCreation); err != nil {
		return "", err
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
