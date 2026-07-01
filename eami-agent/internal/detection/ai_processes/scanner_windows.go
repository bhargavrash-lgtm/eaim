//go:build windows

package ai_processes

import "context"

// Scan detects running AI-related processes on Windows.
// TODO: implement via WMI Win32_Process using github.com/go-ole/go-ole.
func Scan(_ context.Context) ([]AIProcess, error) { return nil, nil }
