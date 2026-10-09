package tasktools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/okayest-dev/genie/internal/tools"
	"github.com/okayest-dev/genie/internal/tracker"
)

// TaskCreateArgs are the arguments for the task_create tool.
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
	// Finding metadata for follow-up ticket creation with dedup/priority shaping
	InvariantViolated string `json:"invariant_violated,omitempty"`
	Category          string `json:"category,omitempty"`
	Severity          string `json:"severity,omitempty"`
	BlastRadiusFiles  int    `json:"blast_radius_files,omitempty"`
	RecurrenceCount   int    `json:"recurrence_count,omitempty"`
	SourceFile        string `json:"source_file,omitempty"`
	SourceEvidence    string `json:"source_evidence,omitempty"`
}

// TaskCreateTool implements the task_create tool.
type TaskCreateTool struct {
	tracker      Tracker
	followup     *FollowupEngine
	enabled      bool
	timeout      time.Duration
}

// NewTaskCreateTool creates a new task_create tool.
func NewTaskCreateTool(tracker Tracker, enabled bool, timeout time.Duration) *TaskCreateTool {
	return &TaskCreateTool{
		tracker: tracker,
		enabled: enabled,
		timeout: timeout,
	}
}

// NewTaskCreateToolWithFollowup creates a new task_create tool with follow-up engine.
func NewTaskCreateToolWithFollowup(tracker Tracker, followup *FollowupEngine, enabled bool, timeout time.Duration) *TaskCreateTool {
	return &TaskCreateTool{
		tracker:  tracker,
		followup: followup,
		enabled:  enabled,
		timeout:  timeout,
	}
}

// Name returns the tool name.
func (t *TaskCreateTool) Name() string {
	return "task_create"
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
			"invariant_violated": map[string]any{
				"type":        "string",
				"description": "The invariant that was violated (for follow-up ticket creation).",
			},
			"category": map[string]any{
				"type":        "string",
				"description": "Category: security, hygiene, functional, style, architecture, other (for follow-up ticket creation).",
				"enum":        []string{"security", "hygiene", "functional", "style", "architecture", "other"},
			},
			"severity": map[string]any{
				"type":        "string",
				"description": "Severity: P0 (critical), P1 (high), P2 (medium), P3 (low) (for follow-up ticket creation).",
				"enum":        []string{"P0", "P1", "P2", "P3"},
			},
			"blast_radius_files": map[string]any{
				"type":        "integer",
				"description": "Number of files affected (for priority shaping).",
				"minimum":     0,
			},
			"recurrence_count": map[string]any{
				"type":        "integer",
				"description": "Number of times this issue has recurred (for priority shaping).",
				"minimum":     0,
			},
			"source_file": map[string]any{
				"type":        "string",
				"description": "Source file where the finding was detected.",
			},
			"source_evidence": map[string]any{
				"type":        "string",
				"description": "Evidence supporting the finding.",
			},
		},
		"required": []any{"title"},
	}
}

// Execute creates a new task in the tracker.
func (t *TaskCreateTool) Execute(raw json.RawMessage) (string, error) {
	if !t.enabled {
		return "", fmt.Errorf("task_create is disabled (task_tools not enabled in config)")
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

	// Default priority to 2 (medium) if not explicitly provided.
	// Note: priority 0 is valid (P0/critical), so we only default when priority is 0
	// and no finding metadata is provided (which would trigger followup engine priority calculation).
	hasFindingMetadata := args.InvariantViolated != "" || args.Category != "" || args.Severity != "" ||
		args.BlastRadiusFiles > 0 || args.RecurrenceCount > 0 || args.SourceFile != "" || args.SourceEvidence != ""
	
	if args.Priority < 0 || args.Priority > 4 {
		args.Priority = 2
	} else if args.Priority == 0 && !hasFindingMetadata {
		args.Priority = 2
	}

	ctx := context.Background()

	// If finding metadata is provided and followup engine is available, use it
	if hasFindingMetadata && t.followup != nil {
		// Use followup engine for dedup guard and priority shaping
		result, err := t.followup.ProcessFinding(ctx, FindingInput{
			InvariantViolated: args.InvariantViolated,
			Category:          args.Category,
			Severity:          args.Severity,
			BlastRadiusFiles:  args.BlastRadiusFiles,
			RecurrenceCount:   args.RecurrenceCount,
			SourceTaskID:      args.FindingOf,
			SourceFile:        args.SourceFile,
			SourceEvidence:    args.SourceEvidence,
			Description:       args.Description,
		})
		if err != nil {
			return "", fmt.Errorf("followup processing failed: %w", err)
		}

		// If deduped or wontfix, return the result without creating a new ticket
		if result.Action == "deduped" {
			return fmt.Sprintf("Finding absorbed into existing ticket %s: %s\nReason: %s\nPriority: %d\nAction: %s",
				result.TicketID, result.TicketTitle, result.Reason, result.Priority, result.Action), nil
		}
		if result.Action == "wontfix" {
			return fmt.Sprintf("Finding wontfix'd\nReason: %s\nAction: %s", result.Reason, result.Action), nil
		}

		// Created - return the created ticket info
		return fmt.Sprintf("Created follow-up task %s: %s\nType: %s\nPriority: %d\nLabels: %v\nAction: %s\nCreated at: %s",
			result.TicketID, result.TicketTitle, "task", result.Priority, []string{"followup", args.Category}, result.Action, time.Now().Format(time.RFC3339)), nil
	}

	// Standard task creation (no followup engine or no finding metadata)
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