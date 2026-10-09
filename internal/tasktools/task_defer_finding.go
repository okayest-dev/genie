package tasktools

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/okayest-dev/genie/internal/tools"
)

// TaskDeferFindingArgs are the arguments for the task_defer_finding tool.
type TaskDeferFindingArgs struct {
	// GateName is the name of the gate the finding relates to (e.g., "build", "tests", "coverage", "review", "hygiene").
	GateName string `json:"gate_name"`
	// FindingID is a unique identifier for this finding.
	FindingID string `json:"finding_id"`
	// Invariant is the invariant that was violated.
	Invariant string `json:"invariant"`
	// Severity is the severity of the finding (P0-P3).
	Severity string `json:"severity"`
	// Category is the category of the finding (security, hygiene, functional, style, architecture, other).
	Category string `json:"category"`
	// BlastRadius is the number of files affected.
	BlastRadius int `json:"blast_radius"`
	// Recurrence is the number of times this issue has recurred.
	Recurrence int `json:"recurrence"`
	// Description is a human-readable description of the finding.
	Description string `json:"description"`
}

// TaskDeferFindingTool implements the task_defer_finding tool.
type TaskDeferFindingTool struct {
	state   *TaskState
	enabled bool
	timeout time.Duration
}

// NewTaskDeferFindingTool creates a new task_defer_finding tool.
func NewTaskDeferFindingTool(state *TaskState, enabled bool, timeout time.Duration) *TaskDeferFindingTool {
	return &TaskDeferFindingTool{
		state:   state,
		enabled: enabled,
		timeout: timeout,
	}
}

// Name returns the tool name.
func (t *TaskDeferFindingTool) Name() string {
	return "task_defer_finding"
}

// Description returns the tool description.
func (t *TaskDeferFindingTool) Description() string {
	return "Record a deferred gate finding for the active task. This allows a blocking gate failure to be deferred with a follow-up ticket, so the task can still resolve. The finding will be checked during gate evaluation at task.resolve time. Requires an active task."
}

// Parameters returns the JSON Schema for the tool arguments.
func (t *TaskDeferFindingTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"gate_name": map[string]any{
				"type":        "string",
				"description": "Name of the gate (build, tests, coverage, review, hygiene, traceability).",
			},
			"finding_id": map[string]any{
				"type":        "string",
				"description": "Unique identifier for this finding.",
			},
			"invariant": map[string]any{
				"type":        "string",
				"description": "The invariant that was violated.",
			},
			"severity": map[string]any{
				"type":        "string",
				"description": "Severity: P0 (critical), P1 (high), P2 (medium), P3 (low).",
				"enum":        []string{"P0", "P1", "P2", "P3"},
			},
			"category": map[string]any{
				"type":        "string",
				"description": "Category: security, hygiene, functional, style, architecture, other.",
				"enum":        []string{"security", "hygiene", "functional", "style", "architecture", "other"},
			},
			"blast_radius": map[string]any{
				"type":        "integer",
				"description": "Number of files affected.",
				"minimum":     0,
			},
			"recurrence": map[string]any{
				"type":        "integer",
				"description": "Number of times this issue has recurred.",
				"minimum":     0,
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Human-readable description of the finding.",
			},
		},
		"required": []any{"gate_name", "finding_id", "invariant", "severity", "category", "description"},
	}
}

// Execute records a deferred finding for the active task.
func (t *TaskDeferFindingTool) Execute(raw json.RawMessage) (string, error) {
	if !t.enabled {
		return "", fmt.Errorf("task_defer_finding is disabled (task_tools not enabled in config)")
	}

	// Check if a task is active
	active := t.state.Get()
	if active == nil {
		return "", fmt.Errorf("no active task to record deferred finding for")
	}

	var args TaskDeferFindingArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Validate required fields
	if args.GateName == "" {
		return "", fmt.Errorf("gate_name is required")
	}
	if args.FindingID == "" {
		return "", fmt.Errorf("finding_id is required")
	}
	if args.Invariant == "" {
		return "", fmt.Errorf("invariant is required")
	}
	if args.Severity == "" {
		return "", fmt.Errorf("severity is required")
	}
	if args.Category == "" {
		return "", fmt.Errorf("category is required")
	}
	if args.Description == "" {
		return "", fmt.Errorf("description is required")
	}

	// Validate severity
	validSeverities := map[string]bool{"P0": true, "P1": true, "P2": true, "P3": true}
	if !validSeverities[args.Severity] {
		return "", fmt.Errorf("invalid severity: %s (must be P0, P1, P2, or P3)", args.Severity)
	}

	// Validate category
	validCategories := map[string]bool{"security": true, "hygiene": true, "functional": true, "style": true, "architecture": true, "other": true}
	if !validCategories[args.Category] {
		return "", fmt.Errorf("invalid category: %s (must be security, hygiene, functional, style, architecture, or other)", args.Category)
	}

	// Record the deferred finding in task state
	record := DeferredFindingRecord{
		GateName:      args.GateName,
		FindingID:     args.FindingID,
		Invariant:     args.Invariant,
		Severity:      args.Severity,
		Category:      args.Category,
		BlastRadius:   args.BlastRadius,
		Recurrence:    args.Recurrence,
		Description:   args.Description,
		RecordedAt:    time.Now().Format(time.RFC3339),
	}

	if err := t.state.RecordDeferredFinding(record); err != nil {
		return "", fmt.Errorf("failed to record deferred finding: %w", err)
	}

	// Build response
	response := fmt.Sprintf("Deferred finding recorded for task %s:\n", active.Task.ID)
	response += fmt.Sprintf("  Gate: %s\n", args.GateName)
	response += fmt.Sprintf("  Finding ID: %s\n", args.FindingID)
	response += fmt.Sprintf("  Invariant: %s\n", args.Invariant)
	response += fmt.Sprintf("  Severity: %s\n", args.Severity)
	response += fmt.Sprintf("  Category: %s\n", args.Category)
	response += fmt.Sprintf("  Blast Radius: %d files\n", args.BlastRadius)
	response += fmt.Sprintf("  Recurrence: %d\n", args.Recurrence)
	response += fmt.Sprintf("  Description: %s\n", args.Description)
	response += fmt.Sprintf("  Recorded at: %s", record.RecordedAt)

	return response, nil
}

// TaskDeferFindingDef returns the tool definition for the registry.
func TaskDeferFindingDef() tools.Tool {
	return nil
}