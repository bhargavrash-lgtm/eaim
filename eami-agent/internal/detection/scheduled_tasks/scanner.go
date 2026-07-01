// Package scheduled_tasks detects Windows scheduled tasks referencing AI tools.
// TODO: implement via Task Scheduler COM API or schtasks.exe parsing.
package scheduled_tasks

import (
	"context"
	"time"
)

// ScheduledTask describes a detected scheduled task referencing AI tooling.
type ScheduledTask struct {
	Name       string    `json:"name"`
	Command    string    `json:"command"`
	DetectedAt time.Time `json:"detected_at"`
}

// Scan detects AI-related scheduled tasks.
func Scan(_ context.Context) ([]ScheduledTask, error) { return nil, nil }
