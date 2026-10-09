package gates

import (
	"context"
	"encoding/json"
	"time"
)

// GateType identifies a gate by name.
type GateType string

// EnforcementLevel defines how strictly a gate is enforced.
type EnforcementLevel string

const (
	EnforcementBlocking  EnforcementLevel = "blocking"
	EnforcementReporting EnforcementLevel = "reporting"
)

// TaskType identifies the type of task a gate applies to.
type TaskType string

const (
	TaskTypeCoding     TaskType = "task"
	TaskTypeResearch   TaskType = "research"
	TaskTypeGrilling   TaskType = "grilling"
	TaskTypePrototype  TaskType = "prototype"
	TaskTypeEpic       TaskType = "epic"
	TaskTypeFeature    TaskType = "feature"
	TaskTypeBug        TaskType = "bug"
)

// Gate defines a quality gate rule.
type Gate struct {
	Name              string           `json:"name"`
	Description       string           `json:"description"`
	AppliesTo         []TaskType       `json:"applies_to"`
	Enforcement       EnforcementLevel `json:"enforcement"`
	Precondition      string           `json:"precondition"`
	Verification      VerificationType `json:"verification"`
	PassCriteria      string           `json:"pass_criteria"`
	Config            json.RawMessage  `json:"config,omitempty"`
}

// VerificationType identifies how the harness verifies the gate.
type VerificationType string

const (
	VerificationBuild        VerificationType = "build"
	VerificationTest         VerificationType = "test"
	VerificationCoverage     VerificationType = "coverage"
	VerificationReview       VerificationType = "review"
	VerificationHygiene      VerificationType = "hygiene"
	VerificationTraceability VerificationType = "traceability"
	VerificationHumanConfirm VerificationType = "human_confirmation"
	VerificationCitation     VerificationType = "citation"
)

// GateSet is a collection of gates for a specific task type.
type GateSet struct {
	TaskType TaskType
	Gates    []Gate
}

// Evidence is a record of a gate check.
type Evidence struct {
	GateName     string                 `json:"gate_name"`
	Timestamp    time.Time              `json:"timestamp"`
	TaskID       string                 `json:"task_id"`
	TaskType     TaskType               `json:"task_type"`
	Verification VerificationType       `json:"verification"`
	Inputs       map[string]any         `json:"inputs"`
	Verdict      Verdict                `json:"verdict"`
	Details      string                 `json:"details,omitempty"`
}

// Verdict is the result of a gate check.
type Verdict string

const (
	VerdictPass  Verdict = "pass"
	VerdictFail  Verdict = "fail"
	VerdictSkip  Verdict = "skip"
	VerdictError Verdict = "error"
)

// EvidenceLedger is an append-only record of gate evidence.
type EvidenceLedger interface {
	Append(ctx context.Context, ev Evidence) error
	Get(ctx context.Context, taskID string) ([]Evidence, error)
	GetSince(ctx context.Context, taskID string, since time.Time) ([]Evidence, error)
}

// Task represents a task for gate evaluation.
type Task struct {
	ID          string
	Title       string
	Type        TaskType
	Labels      []string
	Status      string
	Priority    int
	Description string
	CreatedAt   string
	UpdatedAt   string
}

// GateRegistry manages gate definitions and per-task-type gate sets.
type GateRegistry interface {
	GetGate(name string) (Gate, bool)
	GetGateSet(taskType TaskType) GateSet
	RegisterGate(gate Gate) error
	SetGateSet(taskType TaskType, gates []Gate)
	ListGates() []Gate
}

// GateRunner is the interface for running quality gates on task resolution.
// The zero-value runner reports no gates configured.
type GateRunner interface {
	Run(ctx context.Context, task *Task, deferredFindings []DeferredFindingRecord) (GateResult, error)
}

// GateResult represents the outcome of running gates on a task.
type GateResult struct {
	Passed    []string
	Failed    []FailedGate
	Skipped   []string
	Commits   []string
	Worktree  string
	FollowUps []string
}

// FailedGate represents a gate that failed with its evidence gap.
type FailedGate struct {
	Name        string
	EvidenceGap string
}

// NoOpGateRunner is a gate runner that reports no gates configured.
type NoOpGateRunner struct{}

func (NoOpGateRunner) Run(ctx context.Context, task *Task, deferredFindings []DeferredFindingRecord) (GateResult, error) {
	return GateResult{}, nil
}

// DeferredFindingStore is an interface for storing and retrieving deferred findings.
type DeferredFindingStore interface {
	RecordDeferredFinding(ctx context.Context, finding DeferredFinding) error
	GetDeferredFindings(ctx context.Context, gateName string) ([]DeferredFinding, error)
	GetAllDeferredFindings(ctx context.Context) ([]DeferredFinding, error)
}

// TicketCreator is an interface for creating follow-up tickets from gate findings.
type TicketCreator interface {
	CreateTicket(ctx context.Context, args CreateTicketArgs) (string, error)
}

// CreateTicketArgs holds arguments for creating a follow-up ticket.
type CreateTicketArgs struct {
	Title         string
	Description   string
	FindingOf     string // Source task ID (provenance)
	SourceGate    string // Gate name that generated the finding
	FindingID     string // Finding ID
	Invariant     string // Invariant violated
	Severity      string // Finding severity
	Category      string // Finding category
	BlastRadius   int    // Blast radius files
	Recurrence    int    // Recurrence count
}

// GateConfig holds configuration for the gate system.
type GateConfig struct {
	CoverageThreshold float64
	RefixAttempts     int
	EnabledGates      []string
	ReportingGates    []string
	TestCommand       string
	BuildCommand      string
	CoverageCommand   string
}

// DeferredFindingRecord represents a deferred gate finding recorded during task work (from task state).
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

// DeferredFinding represents a finding that has been deferred with a ticket (stored in deferred finding store).
type DeferredFinding struct {
	GateName      string    `json:"gate_name"`
	FindingID     string    `json:"finding_id"`
	Invariant     string    `json:"invariant"`
	Severity      string    `json:"severity"`
	Category      string    `json:"category"`
	BlastRadius   int       `json:"blast_radius"`
	Recurrence    int       `json:"recurrence"`
	TicketID      string    `json:"ticket_id"`
	FindingOf     string    `json:"finding_of"`     // Source task ID
	Description   string    `json:"description"`
	CreatedAt     time.Time `json:"created_at"`
}