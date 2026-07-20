// Package scheduled_tasks detects Windows scheduled tasks that reference AI tools.
// The Scan implementation is platform-specific:
//   - Windows: queries schtasks.exe /Query /FO CSV /V
//   - Other platforms: returns empty (scheduled tasks are a Windows concept)
package scheduled_tasks

import "time"

// ScheduledTask describes a detected scheduled task that references AI tooling.
type ScheduledTask struct {
	Name       string    `json:"name"`
	Command    string    `json:"command"`
	DetectedAt time.Time `json:"detected_at"`
}
