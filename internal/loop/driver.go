package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/okayest-dev/genie/internal/gates"
	"github.com/okayest-dev/genie/internal/run"
	"github.com/okayest-dev/genie/internal/session"
	"github.com/okayest-dev/genie/internal/tasktools"
	"github.com/okayest-dev/genie/internal/worktree"
)

// loopContext holds the runtime context for the loop driver.
type loopContext struct {
	Run             *run.Handle
	TaskState       *tasktools.TaskState
	GateRunner      gates.GateRunner
	Config          LoopConfig
	Reporter        Reporter
	State           LoopState
	CurrentTask     string
	RefixCount      int
	TaskCount       int
	StartTime       time.Time
	Session         *session.Session
	WorktreeManager worktree.ManagerInterface
}

// StandardLoopDriver implements the autonomous frontier loop driver.
type StandardLoopDriver struct {
	ctx    *loopContext
	cancel context.CancelFunc
}

// NewStandardLoopDriver creates a new loop driver.
func NewStandardLoopDriver(r *run.Handle, cfg LoopConfig, reporter Reporter) *StandardLoopDriver {
	if reporter == nil {
		reporter = &DefaultReporter{}
	}

	ctx := &loopContext{
		Run:             r,
		TaskState:       r.TaskState(),
		GateRunner:      r.GateRunner(),
		Config:          cfg,
		Reporter:        reporter,
		State:           LoopStateUnknown,
		StartTime:       time.Now(),
		Session:         r.Session(),
		WorktreeManager: r.WorktreeManager(),
	}

	return &StandardLoopDriver{ctx: ctx}
}

// Run executes the autonomous loop until the frontier is empty or a HITL gate trips.
func (d *StandardLoopDriver) Run(ctx context.Context) (*LoopReport, error) {
	ctx, d.cancel = context.WithCancel(ctx)

	report := &LoopReport{
		TasksCompleted: []TaskResult{},
		TasksEscalated: []TaskResult{},
		Suggestions:    []string{},
		StartTime:      d.ctx.StartTime,
	}

	// Initial orientation
	if err := d.orient(ctx); err != nil {
		report.EscalatedToHuman = true
		d.ctx.Reporter.OnError(fmt.Errorf("orient failed: %w", err))
		return report, err
	}

	// Main loop
	for {
		select {
		case <-ctx.Done():
			report.TotalDuration = time.Since(d.ctx.StartTime)
			d.ctx.Reporter.OnReport(*report)
			return report, ctx.Err()
		default:
		}

		// Check max tasks limit
		if d.ctx.Config.MaxTasksPerRun > 0 && d.ctx.TaskCount >= d.ctx.Config.MaxTasksPerRun {
			report.Suggestions = append(report.Suggestions, fmt.Sprintf("Max tasks per run (%d) reached", d.ctx.Config.MaxTasksPerRun))
			break
		}

		// Check for active task (crash recovery)
		if d.ctx.TaskState.HasActive() {
			slog.Info("resuming active task from crash", "task_id", d.ctx.TaskState.ActiveTaskID())
			d.ctx.CurrentTask = d.ctx.TaskState.ActiveTaskID()
			active := d.ctx.TaskState.Get()
			if err := d.work(ctx, active.Task); err != nil {
				d.ctx.Reporter.OnError(err)
			}
			continue
		}

		// CLAIM: Get next task from frontier
		task, err := d.claim(ctx)
		if err != nil {
			// No tasks available - empty frontier
			report.FrontierEmpty = true
			d.ctx.State = LoopStateNext
			d.ctx.Reporter.OnFrontierEmpty()
			break
		}

		d.ctx.CurrentTask = task.ID
		d.ctx.TaskCount++

		// WORK: Execute the task
		result := d.processTask(ctx, task)

		// Record result
		if result.Success {
			report.TasksCompleted = append(report.TasksCompleted, *result)
		} else if result.Error != "" && strings.Contains(result.Error, "escalated") {
			report.TasksEscalated = append(report.TasksEscalated, *result)
			report.EscalatedToHuman = true
		} else {
			report.TasksCompleted = append(report.TasksCompleted, *result)
		}

		// Check if escalated - stop the loop
		if result.Error != "" && strings.Contains(result.Error, "escalated") {
			break
		}
	}

	report.TotalDuration = time.Since(d.ctx.StartTime)
	d.ctx.Reporter.OnReport(*report)
	return report, nil
}

// RunSingleTask processes a single task through the loop and returns its result.
func (d *StandardLoopDriver) RunSingleTask(ctx context.Context) (*TaskResult, error) {
	if d.ctx.TaskState.HasActive() {
		return nil, fmt.Errorf("active task exists: %s", d.ctx.TaskState.ActiveTaskID())
	}

	if err := d.orient(ctx); err != nil {
		return nil, err
	}

	task, err := d.claim(ctx)
	if err != nil {
		return &TaskResult{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	d.ctx.CurrentTask = task.ID
	return d.processTask(ctx, task), nil
}

// CurrentState returns the current loop state.
func (d *StandardLoopDriver) CurrentState() LoopState {
	return d.ctx.State
}

// CurrentTask returns the currently active task ID, or empty if none.
func (d *StandardLoopDriver) CurrentTask() string {
	return d.ctx.CurrentTask
}

// Resume resumes the loop from a persisted session state.
// Restores active task state, evidence ledger, and continues from the correct boundary.
func (d *StandardLoopDriver) Resume(ctx context.Context) error {
	slog.Info("loop: resuming from persisted session")

	// Orient loads durable facts
	if err := d.orient(ctx); err != nil {
		return err
	}

	// Check if there's an active task to resume
	if d.ctx.TaskState.HasActive() {
		active := d.ctx.TaskState.Get()
		d.ctx.CurrentTask = active.Task.ID
		slog.Info("loop: resuming active task", "task_id", active.Task.ID, "worktree", d.ctx.TaskState.WorktreePath())

		// Determine which stage to resume from by checking session transcript
		// If the task was already resolved (has boundary compaction), we proceed to NEXT
		// If mid-task, we resume WORK state
		stage := d.determineResumeStage(active.Task)
		slog.Info("loop: determined resume stage", "stage", stage)

		switch stage {
		case LoopStateWork:
			// Resume mid-task: continue WORK
			d.setState(LoopStateWork)
			return d.work(ctx, active.Task)
		case LoopStateGateCheck:
			// Was at gate check: re-run gates
			d.setState(LoopStateGateCheck)
			// Run the full task processing which will re-run gates
			_ = d.processTask(ctx, active.Task)
			return nil
		case LoopStateResolve:
			// Was at resolve: proceed to resolve (will require human_confirmed for HITL)
			d.setState(LoopStateResolve)
			_ = d.processTask(ctx, active.Task)
			return nil
		case LoopStateNext:
			// Task was completed: clear active task and proceed to NEXT
			d.ctx.TaskState.Clear()
			d.ctx.CurrentTask = ""
			slog.Info("loop: task already completed, proceeding to next")
			return nil
		}
	}

	return nil
}

// determineResumeStage determines which stage to resume from based on session state.
func (d *StandardLoopDriver) determineResumeStage(task *tasktools.Task) LoopState {
	if d.ctx.Session == nil {
		return LoopStateWork
	}

	lines, err := d.ctx.Session.Lines()
	if err != nil {
		return LoopStateWork
	}

	// Look for task state markers to find the last known state
	var lastTaskState string
	for _, ln := range lines {
		if meta, ok := ln.Metadata["task_state"]; ok {
			lastTaskState = meta
		}
	}

	// Check for boundary compaction markers
	hasBoundaryCompaction := false
	for _, ln := range lines {
		if ln.Role == session.RoleCompaction {
			// Check if this compaction is for our task
			if strings.Contains(ln.Content, task.ID) {
				hasBoundaryCompaction = true
			}
		}
	}

	if hasBoundaryCompaction {
		return LoopStateNext
	}

	// Parse last task state to determine stage
	if strings.Contains(lastTaskState, "claimed") || strings.Contains(lastTaskState, "progress") {
		return LoopStateWork
	}
	if strings.Contains(lastTaskState, "gate") || strings.Contains(lastTaskState, "resolve") {
		return LoopStateGateCheck
	}

	return LoopStateWork
}

// orient performs the ORIENT stage: loads AGENTS.md, tracker config, domain glossary; reads frontier.
func (d *StandardLoopDriver) orient(ctx context.Context) error {
	d.setState(LoopStateOrient)
	slog.Info("loop: orienting")

	// The run handle already has the AGENTS.md loaded (via instruction resolution),
	// tracker config (via plugin manager), and domain glossary (via context plugins).
	// The frontier will be read at CLAIM stage via task_claim tool.

	// Verify we have a valid run handle with required components
	if d.ctx.Run == nil {
		return fmt.Errorf("no run handle")
	}
	if d.ctx.TaskState == nil {
		return fmt.Errorf("no task state")
	}

	slog.Info("loop: orient complete")
	return nil
}

// claim performs the CLAIM stage: calls task.claim, allocates worktree if needed.
func (d *StandardLoopDriver) claim(ctx context.Context) (*tasktools.Task, error) {
	d.setState(LoopStateClaim)
	slog.Info("loop: claiming next task")

	// Use the task_claim tool via the run's registry
	claimTool, ok := d.ctx.Run.Registry().Get("task_claim")
	if !ok {
		return nil, fmt.Errorf("task_claim tool not available")
	}

	// Build arguments - empty means claim frontier task
	args := map[string]any{}
	if d.ctx.Config.PreferReadyForAgent {
		// The tracker's Frontier method already prefers ready-for-agent
		// based on the tracker implementation
	}

	// Execute the tool
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal claim args: %w", err)
	}

	_, err = claimTool.Execute(raw)
	if err != nil {
		// Check if it's a "no ready tasks" error
		if strings.Contains(err.Error(), "no ready tasks") ||
			strings.Contains(err.Error(), "frontier empty") {
			return nil, fmt.Errorf("empty frontier: %w", err)
		}
		return nil, err
	}

	// Parse the result to get the claimed task
	active := d.ctx.TaskState.Get()
	if active == nil {
		return nil, fmt.Errorf("claim succeeded but no active task set")
	}

	d.ctx.Reporter.OnTaskClaimed(active.Task)
	slog.Info("loop: claimed task", "task_id", active.Task.ID, "title", active.Task.Title)

	return active.Task, nil
}

// processTask processes a single task through WORK → GATE_CHECK → RESOLVE → FLUSH.
// Includes bounded refix loop on gate failures.
func (d *StandardLoopDriver) processTask(ctx context.Context, task *tasktools.Task) *TaskResult {
	startTime := time.Now()
	result := &TaskResult{
		TaskID:    task.ID,
		Title:     task.Title,
		Success:   false,
		Duration:  0,
	}

	// WORK + GATE_CHECK + RESOLVE loop with bounded refix
	for {
		select {
		case <-ctx.Done():
			result.Error = ctx.Err().Error()
			result.Duration = time.Since(startTime)
			return result
		default:
		}

		// WORK stage
		d.setState(LoopStateWork)
		slog.Info("loop: working on task", "task_id", task.ID)

		workErr := d.work(ctx, task)
		if workErr != nil {
			result.Error = workErr.Error()
			result.Duration = time.Since(startTime)
			return result
		}

		// GATE_CHECK stage
		d.setState(LoopStateGateCheck)
		slog.Info("loop: running gate checks", "task_id", task.ID)

		gateResult, err := d.gateCheck(ctx, task)
		if err != nil {
			result.Error = fmt.Sprintf("gate runner failed: %v", err)
			result.Duration = time.Since(startTime)
			return result
		}

		result.GatesPassed = gateResult.Passed
		for _, fg := range gateResult.Failed {
			result.GatesFailed = append(result.GatesFailed, FailedGateInfo{
				Name:        fg.Name,
				EvidenceGap: fg.EvidenceGap,
			})
		}

		d.ctx.Reporter.OnGateResult(task.ID, gateResult)

		// Check for blocking gate failures
		if len(gateResult.Failed) > 0 {
			// Increment refix counter
			d.ctx.RefixCount++
			slog.Info("loop: gate failures, refix attempt", "task_id", task.ID, "attempt", d.ctx.RefixCount, "max", d.ctx.Config.RefixAttempts)

			if d.ctx.RefixCount > d.ctx.Config.RefixAttempts {
				// Escalate to human
				reason := fmt.Sprintf("exceeded %d refix attempts", d.ctx.Config.RefixAttempts)
				d.escalateTask(ctx, task, reason)
				result.Error = fmt.Sprintf("escalated: %s", reason)
				result.RefixAttempts = d.ctx.RefixCount
				result.Duration = time.Since(startTime)
				return result
			}

			// Build a refix prompt from the failed gates
			var promptParts []string
			promptParts = append(promptParts, fmt.Sprintf("Task %s (%s) has %d blocking gate(s) that failed:", task.ID, task.Title, len(gateResult.Failed)))
			for _, fg := range gateResult.Failed {
				promptParts = append(promptParts, fmt.Sprintf("  - %s: %s", fg.Name, fg.EvidenceGap))
			}
			promptParts = append(promptParts, fmt.Sprintf("\nThis is refix attempt %d of %d. Fix the issues and call task.progress to record your progress, then call task.resolve again.", d.ctx.RefixCount, d.ctx.Config.RefixAttempts))
			refixPrompt := strings.Join(promptParts, "\n")

			// Run an agent turn with the refix prompt
			if err := d.runAgentTurn(ctx, refixPrompt); err != nil {
				result.Error = fmt.Sprintf("refix turn failed: %v", err)
				result.Duration = time.Since(startTime)
				return result
			}

			// Loop back to re-run gate checks
			continue
		}

		// All gates passed - proceed to RESOLVE
		d.setState(LoopStateResolve)
		slog.Info("loop: resolving task", "task_id", task.ID)

		resolveErr := d.resolve(ctx, task, gateResult)
		if resolveErr != nil {
			result.Error = resolveErr.Error()
			result.Duration = time.Since(startTime)
			return result
		}

		// FLUSH stage
		d.setState(LoopStateFlush)
		slog.Info("loop: flushing context", "task_id", task.ID)

		if err := d.flush(ctx, task); err != nil {
			slog.Warn("loop: flush failed", "error", err)
			// Don't fail the task for flush errors
		}

		// NEXT stage
		d.setState(LoopStateNext)
		slog.Info("loop: task complete, moving to next", "task_id", task.ID)

		result.Success = true
		result.Commits = gateResult.Commits
		result.WorktreePath = gateResult.Worktree
		result.FollowUpTickets = gateResult.FollowUps
		result.CheckInInfo = gateResult.CheckInInfo
		result.RefixAttempts = d.ctx.RefixCount
		result.Duration = time.Since(startTime)

		d.ctx.Reporter.OnTaskResolved(*result)
		d.ctx.RefixCount = 0 // Reset refix counter on successful task

		return result
	}
}

// work performs the WORK stage: agent turns with full tool access.
// Returns nil when the task is ready for gate checking (agent called task.resolve or signaled completion).
func (d *StandardLoopDriver) work(ctx context.Context, task *tasktools.Task) error {
	// For the autonomous loop, we run a single agent turn with a prompt to work on the task.
	// The agent is expected to make progress and eventually call task.resolve when ready.
	// In a more sophisticated implementation, this would loop until the agent signals readiness.

	prompt := fmt.Sprintf("Work on task %s: %s\n\nType: %s\nLabels: %v\n\nMake progress on this task. When you are ready to submit for gate review, call task.resolve with a summary of your work.", task.ID, task.Title, task.Type, task.Labels)

	return d.runAgentTurn(ctx, prompt)
}

// runAgentTurn runs a single agent turn with the given prompt.
func (d *StandardLoopDriver) runAgentTurn(ctx context.Context, prompt string) error {
	// Use the run handle's Turn method to execute an agent turn
	// We discard output since the loop driver manages its own reporting
	var out, errOut strings.Builder
	return d.ctx.Run.Turn(ctx, prompt, &out, &errOut)
}

// gateCheck runs the gate runner for the task.
func (d *StandardLoopDriver) gateCheck(ctx context.Context, task *tasktools.Task) (gates.GateResult, error) {
	if d.ctx.GateRunner == nil {
		return gates.GateResult{Passed: []string{}, Failed: []gates.FailedGate{}}, nil
	}

	// Get deferred findings from task state
	deferredFindings := d.ctx.TaskState.GetDeferredFindings()

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

	// Convert tasktools.Task to gates.Task
	gatesTask := &gates.Task{
		ID:          task.ID,
		Title:       task.Title,
		Type:        gates.TaskType(task.Type),
		Labels:      task.Labels,
		Status:      task.Status,
		Priority:    task.Priority,
		Description: task.Description,
		CreatedAt:   task.CreatedAt,
		UpdatedAt:   task.UpdatedAt,
		Worktree:    d.ctx.TaskState.WorktreePath(),
	}

	// Attach review attestations
	for _, rv := range d.ctx.TaskState.GetReviews() {
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
				SourceTaskID:       task.ID,
				SourceFile:         f.SourceFile,
				SourceEvidence:     f.SourceEvidence,
				Disposition:        gates.FindingDisposition(f.Disposition),
				DispositionReason:  f.Reason,
			})
		}
		gatesTask.Reviews = append(gatesTask.Reviews, review)
	}

	return d.ctx.GateRunner.Run(ctx, gatesTask, gateDeferredFindings)
}

// resolve performs the RESOLVE stage: calls task.resolve with summary.
// For HITL task types, it will fail without human_confirmed=true, pausing the loop.
func (d *StandardLoopDriver) resolve(ctx context.Context, task *tasktools.Task, gateResult gates.GateResult) error {
	// Build resolution summary from gate results
	summaryParts := []string{
		fmt.Sprintf("Task completed with gates: %s", strings.Join(gateResult.Passed, ", ")),
	}
	if len(gateResult.Commits) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Commits: %s", strings.Join(gateResult.Commits, ", ")))
	}
	if gateResult.Worktree != "" {
		summaryParts = append(summaryParts, fmt.Sprintf("Worktree: %s", gateResult.Worktree))
	}
	if len(gateResult.FollowUps) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Follow-ups: %s", strings.Join(gateResult.FollowUps, ", ")))
	}
	summary := strings.Join(summaryParts, "\n")

	// Check if human confirmation is needed
	humanConfirmed := false
	if isHumanInTheLoop(task.Type) {
		// In interactive mode, the human would provide this via the REPL
		// In headless mode, this will fail and the task will be paused
		// The loop should pause here and wait for human to provide confirmation
		humanConfirmed = false
	}

	// Call task_resolve tool
	resolveTool, ok := d.ctx.Run.Registry().Get("task_resolve")
	if !ok {
		return fmt.Errorf("task_resolve tool not available")
	}

	args := map[string]any{
		"summary":           summary,
		"human_confirmed":   humanConfirmed,
	}

	raw, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("marshal resolve args: %w", err)
	}

	_, err = resolveTool.Execute(raw)
	return err
}

// flush performs the FLUSH stage: boundary compaction marker.
func (d *StandardLoopDriver) flush(ctx context.Context, task *tasktools.Task) error {
	// The boundary compaction is already written by task_resolve tool
	// when it calls sess.AppendCompaction.
	// We can optionally trigger a session reset here for a larger clean slate.

	// Optionally: if configured, call session.reset tool
	// For now, the boundary compaction at resolve is sufficient.
	return nil
}

// escalateTask marks a task as escalated and applies the escalation label.
func (d *StandardLoopDriver) escalateTask(ctx context.Context, task *tasktools.Task, reason string) {
	slog.Warn("loop: escalating task", "task_id", task.ID, "reason", reason)

	// Apply the escalation label via the tracker
	// The tracker's Close with a reason, or we could add a label
	// For now, we just record it
	d.ctx.Reporter.OnTaskEscalated(task.ID, reason)
}

// isHumanInTheLoop returns true if the task type requires human confirmation.
func isHumanInTheLoop(taskType string) bool {
	return taskType == "epic" || taskType == "feature" || taskType == "grilling" || taskType == "prototype"
}

// setState updates the loop state and notifies the reporter.
func (d *StandardLoopDriver) setState(state LoopState) {
	d.ctx.State = state
	d.ctx.Reporter.OnStateChange(state, d.ctx.CurrentTask)
}