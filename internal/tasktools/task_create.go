package tasktools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/okayest-dev/genie/internal/tools"
	"github.com/okayest-dev/genie/internal/tracker"
)

// TaskCreateArgs are the arguments for the task.create tool.
type TaskCreateArgs struct {
	// Title is the task title (required).
	Title string `json:"title"`
	// Type is the task type (task, epic, feature, bug). Defaults to "task".
	Type string `json:"type"`
	// Labels are the task labels.
	Labels []string `json:"labels"`
	// Priority is the task priority (0-4, where 0 is critical). Defaults to 2.
	Priority int `json:"priority"`
	// Description is the task description.
	Description string `json:"description"`
	// Parent is the optional parent task ID.
	Parent string `json:"parent"`
	// DependsOn is an optional list of task IDs this task depends on.
	DependsOn []string `json:"depends_on"`
	// FindingOf is the provenance link - the task ID this finding came from.
	FindingOf string `json:"finding_of"`
}

// TaskCreateTool implements the task.create tool.
type TaskCreateTool struct {
	tracker Tracker
	enabled bool
	timeout time.Duration
}

// NewTaskCreateTool creates a new task.create tool.
func NewTaskCreateTool(tracker Tracker, enabled bool, timeout time.Duration) *TaskCreateTool {
	return &TaskCreateTool{
		tracker: tracker,
		enabled: enabled,
		timeout: timeout,
	}
}

// Name returns the tool name.
func (t *TaskCreateTool) Name() string {
	return "task.create"
}

// Description returns the tool description.
func (t *TaskCreateTool) Description() string {
	return "Create a new task in the tracker. Requires title; optional type, labels, priority, description, parent, depends_on, and finding_of (provenance). Returns the created task with its ID following project prefix conventions."
}

// Parameters returns the JSON Schema for the tool arguments.
func (t *TaskCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "Task title (required).",
			},
			"type": map[string]any{
				"type":        "string",
				"description": "Task type: task, epic, feature, or bug. Defaults to 'task'.",
			},
			"labels": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Task labels.",
			},
			"priority": map[string]any{
				"type":        "integer",
				"description": "Task priority 0-4 (0=critical, 1=high, 2=medium, 3=low, 4=backlog). Defaults to 2.",
				"minimum":     0,
				"maximum":     4,
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Task description.",
			},
			"parent": map[string]any{
				"type":        "string",
				"description": "Optional parent task ID.",
			},
			"depends_on": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional list of task IDs this task depends on.",
			},
			"finding_of": map[string]any{
				"type":        "string",
				"description": "Provenance: the task ID this finding came from.",
			},
		},
		"required": []any{"title"},
	}
}

// Execute creates a new task in the tracker.
func (t *TaskCreateTool) Execute(raw json.RawMessage) (string, error) {
	if !t.enabled {
		return "", fmt.Errorf("task.create is disabled (task_tools not enabled in config)")
	}

	var args TaskCreateArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if args.Title == "" {
		return "", fmt.Errorf("title is required")
	}

	if args.Type == "" {
		args.Type = "task"
	}

	if args.Priority < 0 || args.Priority > 4 {
		args.Priority = 2
	}

	ctx := context.Background()

	task, err := t.tracker.Create(ctx, tracker.CreateArgs{
		Title:       args.Title,
		Type:        args.Type,
		Labels:      args.Labels,
		Priority:    args.Priority,
		Description: args.Description,
		Parent:      args.Parent,
		DependsOn:   args.DependsOn,
		FindingOf:   args.FindingOf,
	})
	if err != nil {
		return "", fmt.Errorf("create failed: %w", err)
	}

	return fmt.Sprintf("Created task %s: %s\nType: %s\nPriority: %d\nLabels: %v\nCreated at: %s",
		task.ID, task.Title, task.Type, task.Priority, task.Labels, time.Now().Format(time.RFC3339)), nil
}

// TaskCreateDef returns the tool definition for the registry.
func TaskCreateDef() tools.Tool {
	return nil
}