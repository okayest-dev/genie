package tasktools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/okayest-dev/genie/internal/tools"
)

// GateRunner is the interface for running quality gates on task resolution.
// The zero-value runner reports no gates configured.
type GateRunner interface {
	Run(ctx context.Context, task *Task) (GateResult, error)
}

// GateResult represents the outcome of running gates on a task.
type GateResult struct {
	Passed  []string
	Failed  []FailedGate
	Commits []string
	Worktree string
	FollowUps []string
}

// FailedGate represents a gate that failed with its evidence gap.
type FailedGate struct {
	Name        string
	EvidenceGap string
}

// NoOpGateRunner is a gate runner that reports no gates configured.
type NoOpGateRunner struct{}

func (NoOpGateRunner) Run(ctx context.Context, task *Task) (GateResult, error) {
	return GateResult{}, nil
}

// TaskResolveArgs are the arguments for the task.resolve tool.
type TaskResolveArgs struct {
	// Summary is the resolution summary carrying traceability.
	Summary string `json:"summary"`
	// HumanConfirmed is a marker that a human has confirmed the resolution.
	// Required for human-in-the-loop task types.
	HumanConfirmed bool `json:"human_confirmed"`
}

// ResolutionRecord is the return value of task.resolve.
type ResolutionRecord struct {
	TaskID     string   `json:"task_id"`
	GatesPassed []string `json:"gates_passed"`
	Commits    []string `json:"commits"`
	Worktree   string   `json:"worktree"`
	FollowUps  []string `json:"follow_ups"`
}

// TaskResolveTool implements the task.resolve tool.
type TaskResolveTool struct {
	tracker    Tracker
	state      *TaskState
	gateRunner GateRunner
	enabled    bool
	timeout    time.Duration
}

// NewTaskResolveTool creates a new task.resolve tool.
func NewTaskResolveTool(tracker Tracker, state *TaskState, gateRunner GateRunner, enabled bool, timeout time.Duration) *TaskResolveTool {
	if gateRunner == nil {
		gateRunner = NoOpGateRunner{}
	}
	return &TaskResolveTool{
		tracker:    tracker,
		state:      state,
		gateRunner: gateRunner,
		enabled:    enabled,
		timeout:    timeout,
	}
}

// Name returns the tool name.
func (t *TaskResolveTool) Name() string {
	return "task.resolve"
}

// Description returns the tool description.
func (t *TaskResolveTool) Description() string {
	return "Resolve (close) the currently active task. Requires a resolution summary with traceability. " +
		"Runs quality gates; any blocking gate failure refuses the resolve. " +
		"Human-in-the-loop task types require human_confirmed=true."
}

// Parameters returns the JSON Schema for the tool arguments.
func (t *TaskResolveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary": map[string]any{
				"type":        "string",
				"description": "Resolution summary carrying traceability: the ticket, commit/branch range, and gate evidence.",
			},
			"human_confirmed": map[string]any{
				"type":        "boolean",
				"description": "Human confirmation marker. Required for human-in-the-loop task types.",
			},
		},
		"required": []any{"summary"},
	}
}

// Execute resolves the active task.
func (t *TaskResolveTool) Execute(raw json.RawMessage) (string, error) {
	if !t.enabled {
		return "", fmt.Errorf("task.resolve is disabled (task_tools not enabled in config)")
	}

	// Check if a task is active
	active := t.state.Get()
	if active == nil {
		return "", fmt.Errorf("no active task to resolve")
	}

	var args TaskResolveArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if args.Summary == "" {
		return "", fmt.Errorf("summary is required")
	}

	ctx := context.Background()

	// Run gates
	gateResult, err := t.gateRunner.Run(ctx, active.Task)
	if err != nil {
		return "", fmt.Errorf("gate runner failed: %w", err)
	}

	// Check for blocking gate failures
	if len(gateResult.Failed) > 0 {
		var failedDetails []string
		for _, fg := range gateResult.Failed {
			failedDetails = append(failedDetails, fmt.Sprintf("%s: %s", fg.Name, fg.EvidenceGap))
		}
		return "", fmt.Errorf("resolve refused: %d blocking gate(s) failed: %s", len(gateResult.Failed), joinWithComma(failedDetails))
	}

	// Check human-in-the-loop requirement
	if isHumanInTheLoop(active.Task.Type) && !args.HumanConfirmed {
		return "", fmt.Errorf("task type %q requires human confirmation (set human_confirmed=true)", active.Task.Type)
	}

	// Post resolution comment with traceability
	comment := buildResolutionComment(active.Task, args.Summary, gateResult)
	if err := t.tracker.Comment(ctx, active.Task.ID, comment); err != nil {
		return "", fmt.Errorf("failed to post resolution comment: %w", err)
	}

	// Close the task with the summary as reason
	if err := t.tracker.Close(ctx, active.Task.ID, args.Summary); err != nil {
		return "", fmt.Errorf("failed to close task: %w", err)
	}

	// Clear active task state (emits task boundary signal via onChange callback)
	t.state.Clear()

	// Build resolution record
	record := ResolutionRecord{
		TaskID:      active.Task.ID,
		GatesPassed: gateResult.Passed,
		Commits:     gateResult.Commits,
		Worktree:    gateResult.Worktree,
		FollowUps:   gateResult.FollowUps,
	}

	recordJSON, _ := json.Marshal(record)
	return fmt.Sprintf("Task %s resolved.\n%s", active.Task.ID, string(recordJSON)), nil
}

// isHumanInTheLoop returns true if the task type requires human confirmation.
func isHumanInTheLoop(taskType string) bool {
	// Epic and feature types require human confirmation
	return taskType == "epic" || taskType == "feature"
}

// buildResolutionComment builds the resolution comment with traceability.
func buildResolutionComment(task *Task, summary string, gateResult GateResult) string {
	var comment string
	comment += fmt.Sprintf("## Resolution: %s\n\n", task.ID)
	comment += fmt.Sprintf("%s\n\n", summary)
	comment += "### Traceability\n"
	comment += fmt.Sprintf("- **Task**: %s (%s)\n", task.ID, task.Title)
	comment += fmt.Sprintf("- **Type**: %s\n", task.Type)
	comment += fmt.Sprintf("- **Resolved at**: %s\n", time.Now().Format(time.RFC3339))
	if len(gateResult.Passed) > 0 {
		comment += fmt.Sprintf("- **Gates passed**: %s\n", joinWithComma(gateResult.Passed))
	}
	if len(gateResult.Commits) > 0 {
		comment += fmt.Sprintf("- **Commits**: %s\n", joinWithComma(gateResult.Commits))
	}
	if gateResult.Worktree != "" {
		comment += fmt.Sprintf("- **Worktree**: %s\n", gateResult.Worktree)
	}
	if len(gateResult.FollowUps) > 0 {
		comment += fmt.Sprintf("- **Follow-up tickets**: %s\n", joinWithComma(gateResult.FollowUps))
	}
	return comment
}

func joinWithComma(items []string) string {
	if len(items) == 0 {
		return ""
	}
	if len(items) == 1 {
		return items[0]
	}
	result := items[0]
	for _, item := range items[1:] {
		result += ", " + item
	}
	return result
}

// TaskResolveDef returns the tool definition for the registry.
func TaskResolveDef() tools.Tool {
	return nil
}