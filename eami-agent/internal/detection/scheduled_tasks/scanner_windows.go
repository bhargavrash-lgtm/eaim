//go:build windows

package scheduled_tasks

import (
	"context"
	"encoding/csv"
	"os/exec"
	"strings"
	"time"
)

// aiTaskKeywords are substrings we look for in a scheduled task's name or command
// to decide whether it relates to AI tooling.
var aiTaskKeywords = []string{
	"ollama", "cursor", "claude", "chatgpt", "lmstudio", "lm studio",
	"jan", "comfyui", "koboldcpp", "llamafile", "localai", "gpt4all",
	"openai", "anthropic", "jupyter", "langchain", "huggingface",
	"stable-diffusion", "stablediffusion", "text-generation",
}

// Scan queries the Windows Task Scheduler for scheduled tasks that reference
// AI tools. It shells out to schtasks.exe (present on all Windows versions)
// with CSV output so we can locate the TaskName and "Task To Run" columns
// without parsing locale-specific list output.
//
// Permission errors and parsing failures are treated as non-fatal: the agent
// omits scheduled-task data rather than aborting the full report.
func Scan(ctx context.Context) ([]ScheduledTask, error) {
	cmd := exec.CommandContext(ctx, "schtasks", "/Query", "/FO", "CSV", "/V")
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil, nil // access denied or schtasks unavailable
	}

	r := csv.NewReader(strings.NewReader(string(out)))
	r.LazyQuotes = true    // schtasks CSV is not always well-formed
	r.FieldsPerRecord = -1 // variable column count across Windows versions
	records, err := r.ReadAll()
	if err != nil || len(records) < 2 {
		return nil, nil
	}

	// Locate the two columns we care about from the header row.
	taskNameIdx, taskToRunIdx := -1, -1
	for i, h := range records[0] {
		switch h {
		case "TaskName":
			taskNameIdx = i
		case "Task To Run":
			taskToRunIdx = i
		}
	}
	if taskNameIdx < 0 || taskToRunIdx < 0 {
		return nil, nil
	}

	now := time.Now().UTC()
	var tasks []ScheduledTask
	for _, row := range records[1:] {
		if ctx.Err() != nil {
			break
		}
		if taskNameIdx >= len(row) || taskToRunIdx >= len(row) {
			continue
		}
		name := row[taskNameIdx]
		command := row[taskToRunIdx]
		if !isAIRelated(name, command) {
			continue
		}
		tasks = append(tasks, ScheduledTask{
			Name:       name,
			Command:    command,
			DetectedAt: now,
		})
	}
	return tasks, nil
}

func isAIRelated(name, command string) bool {
	combined := strings.ToLower(name + " " + command)
	for _, kw := range aiTaskKeywords {
		if strings.Contains(combined, kw) {
			return true
		}
	}
	return false
}
