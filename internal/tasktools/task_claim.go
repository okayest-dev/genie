package tasktools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/okayest-dev/genie/internal/tools"
)

// ActiveTask holds the currently claimed task state for the session.
type ActiveTask struct {
	Task      *Task
	ClaimedAt time.Time
}

// TaskState manages the active task state for a session.
type TaskState struct {
	active *ActiveTask
}

// NewTaskState creates a new task state manager.
func NewTaskState() *TaskState {
	return &TaskState{}
}

// Get returns the currently active task, or nil if none.
func (s *TaskState) Get() *ActiveTask {
	return s.active
}

// Set sets the active task.
func (s *TaskState) Set(task *Task) {
	s.active = &ActiveTask{
		Task:      task,
		ClaimedAt: time.Now(),
	}
}

// Clear clears the active task.
func (s *TaskState) Clear() {
	s.active = nil
}

// HasActive returns true if a task is currently active.
func (s *TaskState) HasActive() bool {
	return s.active != nil
}

// ActiveTaskID returns the ID of the active task, or empty string.
func (s *TaskState) ActiveTaskID() string {
	if s.active == nil {
		return ""
	}
	return s.active.Task.ID
}

// MarshalJSON implements custom JSON serialization for the session transcript marker.
func (s *TaskState) MarshalJSON() ([]byte, error) {
	if s.active == nil {
		return json.Marshal(map[string]any{"active_task": nil})
	}
	return json.Marshal(map[string]any{
		"active_task": map[string]any{
			"id":          s.active.Task.ID,
			"title":       s.active.Task.Title,
			"type":        s.active.Task.Type,
			"labels":      s.active.Task.Labels,
			"claimed_at":  s.active.ClaimedAt.Format(time.RFC3339),
		},
	})
}

// TaskClaimArgs are the arguments for the task.claim tool.
type TaskClaimArgs struct {
	// TaskID is the optional task ID to claim. If empty, claims the frontier task.
	TaskID string `json:"task_id"`
}

// TaskClaimTool implements the task.claim tool.
type TaskClaimTool struct {
	tracker  Tracker
	state    *TaskState
	enabled  bool
	timeout  time.Duration
}

// NewTaskClaimTool creates a new task.claim tool.
func NewTaskClaimTool(tracker Tracker, state *TaskState, enabled bool, timeout time.Duration) *TaskClaimTool {
	return &TaskClaimTool{
		tracker:  tracker,
		state:    state,
		enabled:  enabled,
		timeout:  timeout,
	}
}

// Name returns the tool name.
func (t *TaskClaimTool) Name() string {
	return "task.claim"
}

// Description returns the tool description.
func (t *TaskClaimTool) Description() string {
	return "Claim the next ready task from the tracker, or a specific task by ID. " +
		"Only one task can be active per session. Use task.resolve to close the active task."
}

// Parameters returns the JSON Schema for the tool arguments.
func (t *TaskClaimTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "Optional task ID to claim. If omitted, claims the next ready task from the frontier.",
			},
		},
		"required": []any{},
	}
}

// Execute claims a task from the tracker.
func (t *TaskClaimTool) Execute(raw json.RawMessage) (string, error) {
	if !t.enabled {
		return "", fmt.Errorf("task.claim is disabled (task_tools not enabled in config)")
	}

	// Check if a task is already active
	if t.state.HasActive() {
		active := t.state.Get()
		return "", fmt.Errorf("task %s (%q) is already active; resolve it first with task.resolve", active.Task.ID, active.Task.Title)
	}

	var args TaskClaimArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	ctx := context.Background()

	var task *Task
	var err error

	if args.TaskID != "" {
		// Claim specific task
		task, err = t.tracker.Claim(ctx, args.TaskID)
	} else {
		// Claim frontier task
		task, err = t.tracker.Frontier(ctx)
		if err != nil {
			return "", fmt.Errorf("no ready tasks available: %w", err)
		}
		// Now claim it
		task, err = t.tracker.Claim(ctx, task.ID)
	}

	if err != nil {
		return "", fmt.Errorf("claim failed: %w", err)
	}

	// Set active task state
	t.state.Set(task)

	return fmt.Sprintf("Claimed task %s: %s\nType: %s\nLabels: %v\nClaimed at: %s",
		task.ID, task.Title, task.Type, task.Labels, time.Now().Format(time.RFC3339)), nil
}

// TaskClaimDef returns the tool definition for the registry.
func TaskClaimDef() tools.Tool {
	// This is a placeholder - the actual tool needs tracker and state injected
	return nil
}