package tasktools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/okayest-dev/genie/internal/gates"
	"github.com/okayest-dev/genie/internal/session"
	"github.com/okayest-dev/genie/internal/tools"
	"github.com/okayest-dev/genie/internal/worktree"
)

// GateRunner is the interface for running quality gates on task resolution.
// The zero-value runner reports no gates configured.
type GateRunner = gates.GateRunner

// GateResult represents the outcome of running gates on a task.
type GateResult = gates.GateResult

// FailedGate represents a gate that failed with its evidence gap.
type FailedGate = gates.FailedGate

// NoOpGateRunner is a gate runner that reports no gates configured.
type NoOpGateRunner = gates.NoOpGateRunner

// TaskResolveArgs are the arguments for the task_resolve tool.
type TaskResolveArgs struct {
	// Summary is the resolution summary carrying traceability.
	Summary string `json:"summary"`
	// HumanConfirmed is a marker that a human has confirmed the resolution.
	// Required for human-in-the-loop task types.
	HumanConfirmed bool `json:"human_confirmed"`
}

// ResolutionRecord is the return value of task_resolve.
type ResolutionRecord struct {
	TaskID      string                  `json:"task_id"`
	GatesPassed []string                `json:"gates_passed"`
	Commits     []string                `json:"commits"`
	Worktree    string                  `json:"worktree"`
	FollowUps   []string                `json:"follow_ups"`
	CheckIn     *worktree.CheckInResult `json:"check_in,omitempty"`
}

// TaskResolveTool implements the task_resolve tool.
type TaskResolveTool struct {
	tracker          Tracker
	state            *TaskState
	gateRunner       GateRunner
	enabled          bool
	timeout          time.Duration
	sess             *session.Session
	restoreCwd       func() error
	worktreeTeardown func(taskID string) error
}

// NewTaskResolveTool creates a new task_resolve tool.
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

// SetSession sets the session for boundary compaction.
func (t *TaskResolveTool) SetSession(sess *session.Session) {
	t.sess = sess
}

// SetRestoreCwd registers the callback that returns the session's working root
// to the main checkout once the task (and its worktree) is resolved (og-wm7.3).
func (t *TaskResolveTool) SetRestoreCwd(fn func() error) {
	t.restoreCwd = fn
}

// SetWorktreeTeardown registers the callback that tears down the task's worktree
// after the check-in flow completes (og-wm7.3.3). The callback receives the
// resolved task's id so teardown can run after the active task is cleared.
func (t *TaskResolveTool) SetWorktreeTeardown(fn func(taskID string) error) {
	t.worktreeTeardown = fn
}

// Name returns the tool name.
func (t *TaskResolveTool) Name() string {
	return "task_resolve"
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
		return "", fmt.Errorf("task_resolve is disabled (task_tools not enabled in config)")
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

	// Run gates - convert tasktools.Task to gates.Task
	gatesTask := &gates.Task{
		ID:          active.Task.ID,
		Title:       active.Task.Title,
		Type:        gates.TaskType(active.Task.Type),
		Labels:      active.Task.Labels,
		Status:      active.Task.Status,
		Priority:    active.Task.Priority,
		Description: active.Task.Description,
		CreatedAt:   active.Task.CreatedAt,
		UpdatedAt:   active.Task.UpdatedAt,
		Worktree:    t.state.WorktreePath(),
	}

	// Attach the review attestations recorded during task work. Findings carry
	// the source task id so a deferred finding's follow-up ticket inherits
	// provenance back to this task.
	for _, rv := range t.state.GetReviews() {
		review := gates.TaskReview{
			Subject:        rv.Subject,
			Method:         rv.Method,
			HumanConfirmed: rv.HumanConfirmed,
			Findings:       make([]gates.Finding, 0, len(rv.Findings)),
		}
		for _, f := range rv.Findings {
			review.Findings = append(review.Findings, gates.Finding{
				ID:                 f.FindingID,
				InvariantViolated:  f.Invariant,
				Category:           gates.FindingCategory(f.Category),
				Severity:           gates.FindingSeverity(f.Severity),
				BlastRadiusFiles:   f.BlastRadius,
				RecurrenceCount:    f.Recurrence,
				SourceTaskID:       active.Task.ID,
				SourceFile:         f.SourceFile,
				SourceEvidence:     f.SourceEvidence,
				Disposition:        gates.FindingDisposition(f.Disposition),
				DispositionReason:  f.Reason,
			})
		}
		gatesTask.Reviews = append(gatesTask.Reviews, review)
	}

	// Get deferred findings from task state
	deferredFindings := t.state.GetDeferredFindings()

	// Convert to gates.DeferredFindingRecord
	var gateDeferredFindings []gates.DeferredFindingRecord
	for _, df := range deferredFindings {
		gateDeferredFindings = append(gateDeferredFindings, gates.DeferredFindingRecord{
			GateName:    df.GateName,
			FindingID:   df.FindingID,
			Invariant:   df.Invariant,
			Severity:    df.Severity,
			Category:    df.Category,
			BlastRadius: df.BlastRadius,
			Recurrence:  df.Recurrence,
			Description: df.Description,
			RecordedAt:  df.RecordedAt,
		})
	}

	gateResult, err := t.gateRunner.Run(ctx, gatesTask, gateDeferredFindings)
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

	// Write boundary compaction marker before clearing task state
	if t.sess != nil {
		startLine := t.state.TaskStartLine()
		lines, err := t.sess.Lines()
		if err == nil {
			endLine := len(lines)
			if endLine > startLine {
				// Build structured boundary compaction summary
				boundarySummary := map[string]any{
					"task_id":            active.Task.ID,
					"title":              active.Task.Title,
					"resolution_summary": args.Summary,
					"gates_passed":       gateResult.Passed,
					"evidence_pointers":  t.buildEvidencePointers(),
					"follow_ups":         gateResult.FollowUps,
					"commit_ref":         joinWithComma(gateResult.Commits),
					"worktree_ref":       gateResult.Worktree,
				}
				if gateResult.CheckInInfo != nil {
					boundarySummary["check_in"] = gateResult.CheckInInfo
				}
				summaryJSON, _ := json.Marshal(boundarySummary)
				_ = t.sess.AppendCompaction(string(summaryJSON), startLine, endLine)
			}
		}
	}

	// Restore the session's working root to the main checkout once the task
	// state has been cleared, so the tools stop resolving inside the worktree.
	hadWorktree := t.state.WorktreePath() != ""
	t.state.Clear()
	if hadWorktree && t.restoreCwd != nil {
		if err := t.restoreCwd(); err != nil {
			return "", fmt.Errorf("task resolved but failed to restore session cwd: %w", err)
		}
	}

	// Teardown the worktree after check-in (if configured and worktree was used)
	if hadWorktree && t.worktreeTeardown != nil {
		if err := t.worktreeTeardown(active.Task.ID); err != nil {
			return "", fmt.Errorf("task resolved but failed to teardown worktree: %w", err)
		}
	}

	// Build resolution record
	record := ResolutionRecord{
		TaskID:      active.Task.ID,
		GatesPassed: gateResult.Passed,
		Commits:     gateResult.Commits,
		Worktree:    gateResult.Worktree,
		FollowUps:   gateResult.FollowUps,
		CheckIn:     gateResult.CheckInInfo,
	}

	recordJSON, _ := json.Marshal(record)
	return fmt.Sprintf("Task %s resolved.\n%s", active.Task.ID, string(recordJSON)), nil
}

// buildEvidencePointers returns a list of evidence pointers from the observed tool executions.
func (t *TaskResolveTool) buildEvidencePointers() []map[string]any {
	if t.state == nil || t.state.EvidenceTracker() == nil {
		return nil
	}
	observed := t.state.EvidenceTracker().Observed()
	if observed == nil {
		return nil
	}
	executions := observed.GetExecutionsSince(observed.ClaimedAt())
	pointers := make([]map[string]any, 0, len(executions))
	for _, exec := range executions {
		pointers = append(pointers, map[string]any{
			"tool":      exec.ToolName,
			"timestamp": exec.Timestamp.Format(time.RFC3339),
			"exit_code": exec.ExitCode,
			"duration":  exec.Duration.String(),
		})
	}
	return pointers
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
	if gateResult.CheckInInfo != nil {
		ci := gateResult.CheckInInfo
		comment += fmt.Sprintf("- **Check-in**: %s", ci.MergeMode)
		if ci.Merged {
			comment += fmt.Sprintf(" (merged to %s)", ci.BaseBranch)
		} else if ci.ParkedBranch != "" {
			comment += fmt.Sprintf(" (parked on %s)", ci.ParkedBranch)
		} else if ci.PushedBranch != "" {
			comment += fmt.Sprintf(" (pushed %s)", ci.PushedBranch)
		}
		if ci.DriftWarning != "" {
			comment += fmt.Sprintf(" — drift: %s", ci.DriftWarning)
		}
		comment += "\n"
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
