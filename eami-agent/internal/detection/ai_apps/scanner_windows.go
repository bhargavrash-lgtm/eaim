//go:build windows

package ai_apps

import "context"

// Scan detects installed AI apps on Windows.
// TODO: implement via HKLM/HKCU\...\Uninstall registry scan.
func Scan(_ context.Context) ([]AIApp, error) { return nil, nil }
