//go:build windows

package ai_processes

import (
	"context"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Scan enumerates running processes on Windows using CreateToolhelp32Snapshot
// and returns those whose name or executable path indicates AI tooling.
//
// Command-line capture is skipped here because it requires NtQueryInformationProcess
// (undocumented) or a WMI round-trip. Detection therefore relies on process name
// and exe path; Python processes referencing AI libraries via cmdline will be missed
// unless the exe path itself contains an AI keyword.
func Scan(ctx context.Context) ([]AIProcess, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))

	if err := windows.Process32First(snap, &pe); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	var procs []AIProcess
	for {
		if ctx.Err() != nil {
			break
		}
		name := windows.UTF16ToString(pe.ExeFile[:])
		exePath := queryExePath(pe.ProcessID)

		if IsAIProcess(name, "", exePath) {
			procs = append(procs, AIProcess{
				PID:        int(pe.ProcessID),
				Name:       name,
				ExePath:    exePath,
				DetectedAt: now,
			})
		}

		if err := windows.Process32Next(snap, &pe); err != nil {
			break // ERROR_NO_MORE_FILES at end of list
		}
	}
	return procs, nil
}

// queryExePath resolves the full executable path for the given PID.
// Returns empty string if the process has already exited or access is denied.
func queryExePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
