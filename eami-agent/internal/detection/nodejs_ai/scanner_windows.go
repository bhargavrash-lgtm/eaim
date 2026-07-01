//go:build windows

package nodejs_ai

import "context"

// Scan detects Node.js AI projects on Windows. TODO: implement.
func Scan(_ context.Context) ([]NodeProject, error) { return nil, nil }
