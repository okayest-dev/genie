package loop

import (
	"context"
	"time"

	"github.com/okayest-dev/genie/internal/gates"
	"github.com/okayest-dev/genie/internal/tasktools"
	"github.com/okayest-dev/genie/internal/worktree"
)

// LoopState represents the current state of the autonomous frontier loop.
type LoopState int

const (
	LoopStateUnknown LoopState = iota
	LoopStateOrient
	LoopStateClaim
	LoopStateWork
	LoopStateGateCheck
	LoopStateResolve
	LoopStateFlush
	LoopStateNext
	LoopStateStop
	LoopStateEscalated
)

// String returns the string representation of the loop state.
func (s LoopState) String() string {
	switch s {
	case LoopStateOrient:
		return "ORIENT"
	case LoopStateClaim:
		return "CLAIM"
	case LoopStateWork:
		return "WORK"
	case LoopStateGateCheck:
		return "GATE_CHECK"
	case LoopStateResolve:
		return "RESOLVE"
	case LoopStateFlush:
		return "FLUSH"
	case LoopStateNext:
		return "NEXT"
	case LoopStateStop:
		return "STOP"
	case LoopStateEscalated:
		return "ESCALATED"
	default:
		return "UNKNOWN"
	}
}

// TaskResult captures the outcome of processing a single task through the loop.
type TaskResult struct {
	TaskID          string
	Title           string
	Success         bool
	GatesPassed     []string
	GatesFailed     []FailedGateInfo
	RefixAttempts   int
	Commits         []string
	WorktreePath    string
	FollowUpTickets []string
	CheckInInfo     *worktree.CheckInResult
	ResolutionSummary string
	Error           string
	Duration        time.Duration
}

// FailedGateInfo represents a gate that failed with its evidence gap.
type FailedGateInfo struct {
	Name        string
	EvidenceGap string
}

// LoopReport is the final report when the loop stops.
type LoopReport struct {
	TasksCompleted    []TaskResult
	TasksEscalated    []TaskResult
	FrontierEmpty     bool
	TotalDuration     time.Duration
	StartTime         time.Time
	Suggestions       []string
	EscalatedToHuman  bool
}

// LoopDriver is the interface for the autonomous frontier loop driver.
type LoopDriver interface {
	// Run executes the autonomous loop until the frontier is empty or a HITL gate trips.
	Run(ctx context.Context) (*LoopReport, error)
	// RunSingleTask processes a single task through the loop and returns its result.
	RunSingleTask(ctx context.Context) (*TaskResult, error)
	// CurrentState returns the current loop state.
	CurrentState() LoopState
	// CurrentTask returns the currently active task ID, or empty if none.
	CurrentTask() string
	// Resume resumes the loop from a persisted session state.
	Resume(ctx context.Context) error
}

// Reporter is the interface for reporting loop progress and results.
type Reporter interface {
	OnStateChange(state LoopState, taskID string)
	OnTaskClaimed(task *tasktools.Task)
	OnTaskProgress(taskID string, record tasktools.ProgressRecord)
	OnGateResult(taskID string, result gates.GateResult)
	OnTaskResolved(result TaskResult)
	OnTaskEscalated(taskID string, reason string)
	OnFrontierEmpty()
	OnReport(report LoopReport)
	OnError(err error)
}

// LoopConfig holds configuration for the autonomous loop driver.
type LoopConfig struct {
	// RefixAttempts is the maximum number of consecutive refix attempts before escalation.
	RefixAttempts int
	// EmptyFrontierAction is what to do when the frontier is empty: "stop" or "suggest".
	EmptyFrontierAction string
	// EscalationLabel is the label applied to a task when refix attempts are exhausted.
	EscalationLabel string
	// MaxTasksPerRun is the maximum number of tasks to process in one run (0 = unlimited).
	MaxTasksPerRun int
	// PreferReadyForAgent controls whether to prefer ready-for-agent labelled tickets.
	PreferReadyForAgent bool
}

// DefaultLoopConfig returns the default loop configuration.
func DefaultLoopConfig() LoopConfig {
	return LoopConfig{
		RefixAttempts:       3,
		EmptyFrontierAction: "stop",
		EscalationLabel:     "ready-for-human",
		MaxTasksPerRun:      0,
		PreferReadyForAgent: true,
	}
}

// DefaultReporter is a no-op reporter.
type DefaultReporter struct{}

func (r *DefaultReporter) OnStateChange(state LoopState, taskID string)            {}
func (r *DefaultReporter) OnTaskClaimed(task *tasktools.Task)                       {}
func (r *DefaultReporter) OnTaskProgress(taskID string, record tasktools.ProgressRecord) {}
func (r *DefaultReporter) OnGateResult(taskID string, result gates.GateResult)      {}
func (r *DefaultReporter) OnTaskResolved(result TaskResult)                         {}
func (r *DefaultReporter) OnTaskEscalated(taskID string, reason string)             {}
func (r *DefaultReporter) OnFrontierEmpty()                                         {}
func (r *DefaultReporter) OnReport(report LoopReport)                               {}
func (r *DefaultReporter) OnError(err error)                                        {}