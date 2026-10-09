package tasktools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/okayest-dev/genie/internal/tools"
)

// TaskProgressArgs are the arguments for the task_progress tool.
type TaskProgressArgs struct {
	// Summary is an optional one-line summary of the checkpoint.
	Summary string `json:"summary"`
	// Notes are optional detailed notes/evidence for the checkpoint.
	Notes string `json:"notes"`
	// TrackerNote is an optional comment to append to the tracker.
	TrackerNote string `json:"tracker_note"`
}

// TaskProgressTool implements the task_progress tool.
type TaskProgressTool struct {
	tracker  Tracker
	state    *TaskState
	enabled  bool
	timeout  time.Duration
}

// NewTaskProgressTool creates a new task_progress tool.
func NewTaskProgressTool(tracker Tracker, state *TaskState, enabled bool, timeout time.Duration) *TaskProgressTool {
	return &TaskProgressTool{
		tracker:  tracker,
		state:    state,
		enabled:  enabled,
		timeout:  timeout,
	}
}

// Name returns the tool name.
func (t *TaskProgressTool) Name() string {
	return "task_progress"
}

// Description returns the tool description.
func (t *TaskProgressTool) Description() string {
	return "Record a progress checkpoint for the active task without changing its lifecycle state. " +
		"Captures summary, notes, and optionally appends a comment to the tracker. " +
		"Requires an active task; does not claim or resolve."
}

// Parameters returns the JSON Schema for the tool arguments.
func (t *TaskProgressTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary": map[string]any{
				"type":        "string",
				"description": "Optional one-line summary of the checkpoint.",
			},
			"notes": map[string]any{
				"type":        "string",
				"description": "Optional detailed notes/evidence for the checkpoint.",
			},
			"tracker_note": map[string]any{
				"type":        "string",
				"description": "Optional comment to append to the tracker.",
			},
		},
		"required": []any{},
	}
}

// Execute records a progress checkpoint for the active task.
func (t *TaskProgressTool) Execute(raw json.RawMessage) (string, error) {
	if !t.enabled {
		return "", fmt.Errorf("task_progress is disabled (task_tools not enabled in config)")
	}

	// Check if a task is active
	active := t.state.Get()
	if active == nil {
		return "", fmt.Errorf("no active task to record progress for")
	}

	var args TaskProgressArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// At least one field should be provided
	if args.Summary == "" && args.Notes == "" && args.TrackerNote == "" {
		return "", fmt.Errorf("at least one of summary, notes, or tracker_note must be provided")
	}

	ctx := context.Background()

	// If tracker_note is provided, append it to the tracker
	if args.TrackerNote != "" {
		if err := t.tracker.Comment(ctx, active.Task.ID, args.TrackerNote); err != nil {
			return "", fmt.Errorf("failed to post tracker note: %w", err)
		}
	}

	// Record the progress checkpoint in the session's active-task evidence
	record := ProgressRecord{
		Timestamp:   time.Now().Format(time.RFC3339),
		Summary:     args.Summary,
		Notes:       args.Notes,
		TrackerNote: args.TrackerNote,
	}

	// Write the progress record to the session transcript as a metadata marker
	// This mirrors the task state change mechanism
	if err := t.state.AppendProgress(record); err != nil {
		return "", fmt.Errorf("failed to record progress: %w", err)
	}

	// Build response
	var response string
	if args.Summary != "" {
		response += fmt.Sprintf("Progress recorded for task %s: %s\n", active.Task.ID, args.Summary)
	} else {
		response += fmt.Sprintf("Progress recorded for task %s\n", active.Task.ID)
	}
	if args.Notes != "" {
		response += fmt.Sprintf("Notes: %s\n", args.Notes)
	}
	if args.TrackerNote != "" {
		response += "Tracker note appended.\n"
	}
	response += fmt.Sprintf("Recorded at: %s", record.Timestamp)

	return response, nil
}

// TaskProgressDef returns the tool definition for the registry.
func TaskProgressDef() tools.Tool {
	return nil
}