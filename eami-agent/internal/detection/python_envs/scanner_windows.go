//go:build windows

package python_envs

import "context"

// Scan detects Python venvs on Windows. TODO: implement.
func Scan(_ context.Context) ([]PythonEnv, error) { return nil, nil }
