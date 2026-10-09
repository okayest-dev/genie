package gates

import (
	"encoding/json"
	"fmt"
	"sync"
)

// DefaultGateRegistry is the default implementation of GateRegistry.
type DefaultGateRegistry struct {
	mu       sync.RWMutex
	gates    map[string]Gate
	gateSets map[TaskType][]Gate
}

// NewDefaultGateRegistry creates a new gate registry with built-in coding task gates.
func NewDefaultGateRegistry() *DefaultGateRegistry {
	r := &DefaultGateRegistry{
		gates:    make(map[string]Gate),
		gateSets: make(map[TaskType][]Gate),
	}
	r.registerBuiltinGates()
	return r
}

func (r *DefaultGateRegistry) registerBuiltinGates() {
	// Coding task gates
	codingGates := []Gate{
		{
			Name:        "build",
			Description: "Build passes (make build or configured build command)",
			AppliesTo:   []TaskType{TaskTypeCoding, TaskTypeFeature, TaskTypeBug},
			Enforcement: EnforcementBlocking,
			Precondition: "Code changes have been made since last build",
			Verification: VerificationBuild,
			PassCriteria: "Exit code 0",
		},
		{
			Name:        "tests",
			Description: "Full test suite passes (make test or configured test command)",
			AppliesTo:   []TaskType{TaskTypeCoding, TaskTypeFeature, TaskTypeBug},
			Enforcement: EnforcementBlocking,
			Precondition: "Code changes have been made since last test run",
			Verification: VerificationTest,
			PassCriteria: "Exit code 0",
		},
		{
			Name:        "coverage",
			Description: "Coverage threshold met on new code",
			AppliesTo:   []TaskType{TaskTypeCoding, TaskTypeFeature, TaskTypeBug},
			Enforcement: EnforcementBlocking,
			Precondition: "Test run executed with coverage",
			Verification: VerificationCoverage,
			PassCriteria: "Coverage >= threshold on new code",
			Config:      json.RawMessage(`{"threshold": 0.8}`),
		},
		{
			Name:        "review",
			Description: "Code review executed (standards + spec axes); findings resolved or deferred",
			AppliesTo:   []TaskType{TaskTypeCoding, TaskTypeFeature, TaskTypeBug},
			Enforcement: EnforcementBlocking,
			Precondition: "Code changes exist to review",
			Verification: VerificationReview,
			PassCriteria: "Review executed; all findings resolved inline or deferred with ticket",
		},
		{
			Name:        "hygiene",
			Description: "No temp files, untracked artifacts, or secrets in worktree diff",
			AppliesTo:   []TaskType{TaskTypeCoding, TaskTypeFeature, TaskTypeBug},
			Enforcement: EnforcementBlocking,
			Precondition: "Worktree has changes",
			Verification: VerificationHygiene,
			PassCriteria: "No temp docs, no secrets, no untracked artifacts from task",
		},
		{
			Name:        "traceability",
			Description: "Resolution summary contains ticket, commit range, gate evidence refs",
			AppliesTo:   []TaskType{TaskTypeCoding, TaskTypeFeature, TaskTypeBug, TaskTypeEpic, TaskTypeFeature},
			Enforcement: EnforcementBlocking,
			Precondition: "Task is being resolved",
			Verification: VerificationTraceability,
			PassCriteria: "All required fields present and consistent",
		},
	}

	// Research task gates
	researchGates := []Gate{
		{
			Name:        "citations",
			Description: "Output names primary sources with citations",
			AppliesTo:   []TaskType{TaskTypeResearch},
			Enforcement: EnforcementBlocking,
			Precondition: "Research output produced",
			Verification: VerificationCitation,
			PassCriteria: "All claims cite primary sources; findings posted to ticket",
		},
		{
			Name:        "traceability",
			Description: "Resolution summary contains sources and findings references",
			AppliesTo:   []TaskType{TaskTypeResearch},
			Enforcement: EnforcementBlocking,
			Precondition: "Task is being resolved",
			Verification: VerificationTraceability,
			PassCriteria: "All required fields present and consistent",
		},
	}

	// HITL task gates (grilling, prototype)
	hitlGates := []Gate{
		{
			Name:        "human_confirmation",
			Description: "Human confirmation marker present before close",
			AppliesTo:   []TaskType{TaskTypeGrilling, TaskTypePrototype},
			Enforcement: EnforcementBlocking,
			Precondition: "Task work completed",
			Verification: VerificationHumanConfirm,
			PassCriteria: "human_confirmed=true provided at resolve",
		},
	}

	r.SetGateSet(TaskTypeCoding, codingGates)
	r.SetGateSet(TaskTypeFeature, codingGates)
	r.SetGateSet(TaskTypeBug, codingGates)
	r.SetGateSet(TaskTypeResearch, researchGates)
	r.SetGateSet(TaskTypeGrilling, hitlGates)
	r.SetGateSet(TaskTypePrototype, hitlGates)
	r.SetGateSet(TaskTypeEpic, codingGates) // Epics use coding gates for traceability
}

func (r *DefaultGateRegistry) GetGate(name string) (Gate, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	gate, ok := r.gates[name]
	return gate, ok
}

func (r *DefaultGateRegistry) GetGateSet(taskType TaskType) GateSet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	gates := r.gateSets[taskType]
	if gates == nil {
		gates = r.gateSets[TaskTypeCoding]
	}
	return GateSet{TaskType: taskType, Gates: gates}
}

func (r *DefaultGateRegistry) RegisterGate(gate Gate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.gates[gate.Name]; exists {
		return fmt.Errorf("gate %q already registered", gate.Name)
	}
	r.gates[gate.Name] = gate
	return nil
}

func (r *DefaultGateRegistry) SetGateSet(taskType TaskType, gates []Gate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Register all gates
	for _, gate := range gates {
		r.gates[gate.Name] = gate
	}
	r.gateSets[taskType] = gates
}

func (r *DefaultGateRegistry) ListGates() []Gate {
	r.mu.RLock()
	defer r.mu.RUnlock()
	gates := make([]Gate, 0, len(r.gates))
	for _, gate := range r.gates {
		gates = append(gates, gate)
	}
	return gates
}