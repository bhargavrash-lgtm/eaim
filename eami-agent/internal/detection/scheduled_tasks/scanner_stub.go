//go:build !windows

package scheduled_tasks

import "context"

// Scan is a no-op on non-Windows platforms — scheduled tasks are Windows-only.
func Scan(_ context.Context) ([]ScheduledTask, error) { return nil, nil }
