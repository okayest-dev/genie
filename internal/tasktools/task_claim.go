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

// DeferredFindingRecord represents a deferred gate finding recorded during task work.
type DeferredFindingRecord struct {
	GateName      string `json:"gate_name"`
	FindingID     string `json:"finding_id"`
	Invariant     string `json:"invariant"`
	Severity      string `json:"severity"`
	Category      string `json:"category"`
	BlastRadius   int    `json:"blast_radius"`
	Recurrence    int    `json:"recurrence"`
	Description   string `json:"description"`
	RecordedAt    string `json:"recorded_at"`
}

// ReviewFindingRecord is a single finding reported by a task_review call,
// carrying the review's triage decision for it.
type ReviewFindingRecord struct {
	FindingID      string `json:"finding_id"`
	Invariant      string `json:"invariant"`
	Category       string `json:"category"`
	Severity       string `json:"severity"`
	BlastRadius    int    `json:"blast_radius"`
	Recurrence     int    `json:"recurrence"`
	SourceFile     string `json:"source_file,omitempty"`
	SourceEvidence string `json:"source_evidence,omitempty"`
	Description    string `json:"description"`
	// Disposition is the triage decision: resolve-inline, defer-with-ticket, or
	// wontfix. Every recorded finding must carry one.
	Disposition string `json:"disposition"`
	// Reason records why a finding was wontfixed (required for wontfix).
	Reason string `json:"reason,omitempty"`
}

// ReviewRecord is an attestation that a review was performed against the active
// task. The review's method and subject are free-form: the harness consumes the
// findings, not the review procedure.
type ReviewRecord struct {
	Subject string `json:"subject"`
	Method  string `json:"method,omitempty"`
	// HumanConfirmed marks that a human made the review's decisions (needed to
	// wontfix a security/hygiene finding).
	HumanConfirmed bool                  `json:"human_confirmed,omitempty"`
	Findings       []ReviewFindingRecord `json:"findings"`
}

// TaskState manages the active task state for a session.
type TaskState struct {
	active               *ActiveTask
	onChange             func([]byte) error
	progressLog          []ProgressRecord
	deferredFindings     []DeferredFindingRecord
	reviews              []ReviewRecord
	evidenceTracker      *gates.ObservedEvidenceTracker
	taskStartLine        int    // transcript line index when task was claimed
	worktreePath         string // worktree allocated for the active task, if any
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
	s.deferredFindings = nil
	s.reviews = nil
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

// SetWorktree records the worktree allocated for the active task.
func (s *TaskState) SetWorktree(path string) {
	s.worktreePath = path
	if s.onChange != nil {
		data, _ := json.Marshal(s)
		_ = s.onChange(data)
	}
}

// WorktreePath returns the worktree path for the active task, or empty.
func (s *TaskState) WorktreePath() string {
	return s.worktreePath
}

// Clear clears the active task.
func (s *TaskState) Clear() {
	s.active = nil
	s.progressLog = nil
	s.deferredFindings = nil
	s.reviews = nil
	s.taskStartLine = 0
	s.worktreePath = ""
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

// RecordDeferredFinding records a deferred gate finding for the active task.
func (s *TaskState) RecordDeferredFinding(record DeferredFindingRecord) error {
	if s.active == nil {
		return fmt.Errorf("no active task")
	}
	record.RecordedAt = time.Now().Format(time.RFC3339)
	s.deferredFindings = append(s.deferredFindings, record)
	return nil
}

// GetDeferredFindings returns the deferred findings for the active task.
func (s *TaskState) GetDeferredFindings() []DeferredFindingRecord {
	return s.deferredFindings
}

// AddReview records a review attestation (with its findings) for the active task.
func (s *TaskState) AddReview(record ReviewRecord) error {
	if s.active == nil {
		return fmt.Errorf("no active task")
	}
	s.reviews = append(s.reviews, record)
	if s.onChange != nil {
		data, _ := json.Marshal(s)
		_ = s.onChange(data)
	}
	return nil
}

// GetReviews returns the review attestations recorded for the active task.
func (s *TaskState) GetReviews() []ReviewRecord {
	return s.reviews
}

// MarshalJSON implements custom JSON serialization for the session transcript marker.
func (s *TaskState) MarshalJSON() ([]byte, error) {
	if s.active == nil {
		return json.Marshal(map[string]any{"active_task": nil, "progress_log": s.progressLog, "deferred_findings": s.deferredFindings, "reviews": s.reviews})
	}
	return json.Marshal(map[string]any{
		"active_task": map[string]any{
			"id":           s.active.Task.ID,
			"title":        s.active.Task.Title,
			"type":         s.active.Task.Type,
			"labels":       s.active.Task.Labels,
			"claimed_at":   s.active.ClaimedAt.Format(time.RFC3339),
			"worktree_path": s.worktreePath,
		},
		"progress_log":      s.progressLog,
		"deferred_findings": s.deferredFindings,
		"reviews":           s.reviews,
	})
}

// TaskClaimArgs are the arguments for the task_claim tool.
type TaskClaimArgs struct {
	// TaskID is the optional task ID to claim. If empty, claims the frontier task.
	TaskID string `json:"task_id"`
}

// WorktreeAllocator allocates (or resumes) the isolated worktree for a claimed
// task. The worktree Manager (internal/worktree) satisfies it.
type WorktreeAllocator interface {
	Allocate(ctx context.Context, taskID, trackerID string) (*worktree.Allocation, error)
}

// TaskClaimTool implements the task_claim tool.
type TaskClaimTool struct {
	tracker        Tracker
	state          *TaskState
	enabled        bool
	timeout        time.Duration
	sess           *session.Session
	worktreePolicy string
	allocator      WorktreeAllocator
	rebind         func(root string) error
}

// NewTaskClaimTool creates a new task_claim tool.
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

// ConfigureWorktree enables claim-time worktree allocation (og-wm7.3). policy
// is the resolved worktree.policy; allocator performs the git work; rebind
// points the session's working root at the allocated worktree. A nil rebind
// leaves the session cwd untouched.
func (t *TaskClaimTool) ConfigureWorktree(policy string, allocator WorktreeAllocator, rebind func(root string) error) {
	t.worktreePolicy = policy
	t.allocator = allocator
	t.rebind = rebind
}

// WorktreePolicy returns the configured worktree policy (empty when unset).
func (t *TaskClaimTool) WorktreePolicy() string { return t.worktreePolicy }

// HasWorktreeAllocator reports whether an allocator is wired.
func (t *TaskClaimTool) HasWorktreeAllocator() bool { return t.allocator != nil }

// Name returns the tool name.
func (t *TaskClaimTool) Name() string {
	return "task_claim"
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
		return "", fmt.Errorf("task_claim is disabled (task_tools not enabled in config)")
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

	// Resolve the task first so worktree allocation and claim share one target.
	var task *Task
	var err error
	if args.TaskID != "" {
		task, err = t.tracker.Read(ctx, args.TaskID)
		if err != nil {
			return "", fmt.Errorf("read task %s: %w", args.TaskID, err)
		}
	} else {
		task, err = t.tracker.Frontier(ctx)
		if err != nil {
			return "", fmt.Errorf("no ready tasks available: %w", err)
		}
	}

	// Allocate the isolated worktree before claiming, so a failure leaves no
	// claimed task. policy=required is the only policy that allocates.
	var alloc *worktree.Allocation
	if t.worktreePolicy == worktree.PolicyRequired {
		alloc, err = t.allocateWorktree(ctx, task.ID)
		if err != nil {
			return "", err
		}
	}

	task, err = t.tracker.Claim(ctx, task.ID)
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

	// Rebind the session's working root to the worktree for the task lifetime.
	if alloc != nil {
		t.state.SetWorktree(alloc.Path)
		if t.rebind != nil {
			if err := t.rebind(alloc.Path); err != nil {
				return "", fmt.Errorf("claimed task %s but failed to rebind session to worktree %s: %w", task.ID, alloc.Path, err)
			}
		}
	}

	msg := fmt.Sprintf("Claimed task %s: %s\nType: %s\nLabels: %v\nClaimed at: %s",
		task.ID, task.Title, task.Type, task.Labels, time.Now().Format(time.RFC3339))
	if alloc != nil {
		state := "created"
		if alloc.Resumed {
			state = "resumed"
		}
		msg += fmt.Sprintf("\nWorktree: %s (branch %s, %s)", alloc.Path, alloc.Branch, state)
	}
	return msg, nil
}

// allocateWorktree allocates the task's isolation worktree, refusing when the
// active tracker is not worktree-safe or no allocator is configured.
func (t *TaskClaimTool) allocateWorktree(ctx context.Context, taskID string) (*worktree.Allocation, error) {
	if t.allocator == nil {
		return nil, fmt.Errorf("worktree.policy=required but no worktree allocator is configured")
	}
	if !t.tracker.Isolation().WorktreeSafe() {
		return nil, fmt.Errorf("worktree.policy=required but active tracker isolation is %q (not worktree-safe); parallel worktrees would diverge", t.tracker.Isolation().String())
	}
	alloc, err := t.allocator.Allocate(ctx, taskID, taskID)
	if err != nil {
		return nil, fmt.Errorf("worktree allocation failed for task %s: %w", taskID, err)
	}
	return alloc, nil
}

// TaskClaimDef returns the tool definition for the registry.
func TaskClaimDef() tools.Tool {
	// This is a placeholder - the actual tool needs tracker and state injected
	return nil
}