// Package file_changes monitors specific paths for model file changes.
// This is a future package — no implementation yet.
package file_changes

import (
	"context"
	"time"
)

// FileChange describes a detected model file change event.
type FileChange struct {
	Path       string    `json:"path"`
	Event      string    `json:"event"` // created, modified, deleted
	SizeBytes  int64     `json:"size_bytes,omitempty"`
	DetectedAt time.Time `json:"detected_at"`
}

// Scan returns file change events since the last scan window.
func Scan(_ context.Context) ([]FileChange, error) { return nil, nil }
