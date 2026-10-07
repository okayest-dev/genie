package tasktools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/okayest-dev/genie/internal/gates"
	"github.com/okayest-dev/genie/internal/session"
	"github.com/okayest-dev/genie/internal/tools"
)

// ActiveTask holds the currently claimed task state for the session.
type ActiveTask struct {
	Task      *Task
	ClaimedAt time.Time
}

// ProgressRecord represents a recorded progress checkpoint.
type ProgressRecord struct {
	Timestamp   string `json:"timestamp"`
	Summary     string `json:"summary,omitempty"`
	Notes       string `json:"notes,omitempty"`
	TrackerNote string `json:"tracker_note,omitempty"`
}

// TaskState manages the active task state for a session.
type TaskState struct {
	active           *ActiveTask
	onChange         func([]byte) error
	progressLog      []ProgressRecord
	evidenceTracker  *gates.ObservedEvidenceTracker
	taskStartLine    int // transcript line index when task was claimed
}

// NewTaskState creates a new task state manager.
func NewTaskState() *TaskState {
	return &TaskState{
		evidenceTracker: gates.NewObservedEvidenceTracker(),
	}
}

// EvidenceTracker returns the observed evidence tracker for gate verification.
func (s *TaskState) EvidenceTracker() *gates.ObservedEvidenceTracker {
	return s.evidenceTracker
}

// SetOnChange sets a callback that is called when the task state changes.
// The callback receives the JSON representation of the new state.
func (s *TaskState) SetOnChange(fn func([]byte) error) {
	s.onChange = fn
}

// Get returns the currently active task, or nil if none.
func (s *TaskState) Get() *ActiveTask {
	return s.active
}

// Set sets the active task.
func (s *TaskState) Set(task *Task) {
	claimedAt := time.Now()
	s.active = &ActiveTask{
		Task:      task,
		ClaimedAt: claimedAt,
	}
	s.progressLog = []ProgressRecord{}
	s.taskStartLine = 0 // will be set by caller after session is available
	// Notify evidence tracker of claim time
	s.evidenceTracker.SetClaimedAt(claimedAt)
	if s.onChange != nil {
		data, _ := json.Marshal(s)
		_ = s.onChange(data)
	}
}

// SetTaskStartLine records the transcript line index when the task was claimed.
func (s *TaskState) SetTaskStartLine(line int) {
	s.taskStartLine = line
}

// TaskStartLine returns the transcript line index when the task was claimed.
func (s *TaskState) TaskStartLine() int {
	return s.taskStartLine
}

// Clear clears the active task.
func (s *TaskState) Clear() {
	s.active = nil
	s.progressLog = nil
	s.taskStartLine = 0
	if s.onChange != nil {
		data, _ := json.Marshal(s)
		_ = s.onChange(data)
	}
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

// AppendProgress appends a progress record to the active task's evidence log.
// Returns an error if no task is active.
func (s *TaskState) AppendProgress(record ProgressRecord) error {
	if s.active == nil {
		return fmt.Errorf("no active task")
	}
	s.progressLog = append(s.progressLog, record)
	return nil
}

// GetProgressLog returns the progress log for the active task.
func (s *TaskState) GetProgressLog() []ProgressRecord {
	return s.progressLog
}

// MarshalJSON implements custom JSON serialization for the session transcript marker.
func (s *TaskState) MarshalJSON() ([]byte, error) {
	if s.active == nil {
		return json.Marshal(map[string]any{"active_task": nil, "progress_log": s.progressLog})
	}
	return json.Marshal(map[string]any{
		"active_task": map[string]any{
			"id":          s.active.Task.ID,
			"title":       s.active.Task.Title,
			"type":        s.active.Task.Type,
			"labels":      s.active.Task.Labels,
			"claimed_at":  s.active.ClaimedAt.Format(time.RFC3339),
		},
		"progress_log": s.progressLog,
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
	sess     *session.Session
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

// SetSession sets the session for boundary compaction tracking.
func (t *TaskClaimTool) SetSession(sess *session.Session) {
	t.sess = sess
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

	// Record transcript start line for boundary compaction
	if t.sess != nil {
		lines, err := t.sess.Lines()
		if err == nil {
			t.state.SetTaskStartLine(len(lines))
		}
	}

	return fmt.Sprintf("Claimed task %s: %s\nType: %s\nLabels: %v\nClaimed at: %s",
		task.ID, task.Title, task.Type, task.Labels, time.Now().Format(time.RFC3339)), nil
}

// TaskClaimDef returns the tool definition for the registry.
func TaskClaimDef() tools.Tool {
	// This is a placeholder - the actual tool needs tracker and state injected
	return nil
}