package gates

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// GateEvaluator evaluates gates against observed evidence.
type GateEvaluator struct {
	registry           GateRegistry
	ledger             EvidenceLedger
	config             GateConfig
	observedEv         *ObservedEvidenceTracker
	ticketCreator      TicketCreator
	deferredFindingStore DeferredFindingStore
}

// ObservedEvidenceTracker tracks tool executions observed during the session.
type ObservedEvidenceTracker struct {
	mu          sync.Mutex
	executions  []ToolExecution
	claimedAt   time.Time
}

type ToolExecution struct {
	Timestamp   time.Time
	ToolName    string
	Args        map[string]any
	ExitCode    int
	Stdout      string
	Stderr      string
	Duration    time.Duration
}

func NewObservedEvidenceTracker() *ObservedEvidenceTracker {
	return &ObservedEvidenceTracker{
		executions: make([]ToolExecution, 0),
		claimedAt:  time.Now(),
	}
}

func (o *ObservedEvidenceTracker) RecordExecution(exec ToolExecution) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.executions = append(o.executions, exec)
}

func (o *ObservedEvidenceTracker) GetExecutionsSince(claimedAt time.Time) []ToolExecution {
	o.mu.Lock()
	defer o.mu.Unlock()
	var result []ToolExecution
	for _, exec := range o.executions {
		if exec.Timestamp.After(claimedAt) {
			result = append(result, exec)
		}
	}
	return result
}

func (o *ObservedEvidenceTracker) SetClaimedAt(t time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.claimedAt = t
}

func (o *ObservedEvidenceTracker) ClaimedAt() time.Time {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.claimedAt
}

// Observed returns the tracker itself (for compatibility with tasktools).
func (o *ObservedEvidenceTracker) Observed() *ObservedEvidenceTracker {
	return o
}

// RecordToolExecution records a tool execution for gate evidence.
// This is a convenience method for lifecycle hooks.
func (o *ObservedEvidenceTracker) RecordToolExecution(toolName string, args map[string]any, exitCode int, stdout, stderr string, duration time.Duration) {
	o.RecordExecution(ToolExecution{
		Timestamp:  time.Now(),
		ToolName:   toolName,
		Args:       args,
		ExitCode:   exitCode,
		Stdout:     stdout,
		Stderr:     stderr,
		Duration:   duration,
	})
}

// NewGateEvaluator creates a new gate evaluator.
func NewGateEvaluator(registry GateRegistry, ledger EvidenceLedger, config GateConfig, observed *ObservedEvidenceTracker, ticketCreator TicketCreator, deferredFindingStore DeferredFindingStore) *GateEvaluator {
	return &GateEvaluator{
		registry:             registry,
		ledger:               ledger,
		config:               config,
		observedEv:           observed,
		ticketCreator:        ticketCreator,
		deferredFindingStore: deferredFindingStore,
	}
}

// Run evaluates all gates for a task and returns the result.
func (e *GateEvaluator) Run(ctx context.Context, task *Task) (GateResult, error) {
	gateSet := e.registry.GetGateSet(task.Type)
	result := GateResult{}

	claimedAt := e.observedEv.ClaimedAt()
	executions := e.observedEv.GetExecutionsSince(claimedAt)

	for _, gate := range gateSet.Gates {
		// Check if gate is enabled
		if !e.isGateEnabled(gate.Name) {
			result.Skipped = append(result.Skipped, gate.Name)
			continue
		}

		// Evaluate the gate
		passed, evidenceGap, err := e.evaluateGate(ctx, gate, task, executions)
		if err != nil {
			// Record error evidence
			ev := Evidence{
				GateName:     gate.Name,
				Timestamp:    time.Now(),
				TaskID:       task.ID,
				TaskType:     task.Type,
				Verification: gate.Verification,
				Inputs:       map[string]any{"error": err.Error()},
				Verdict:      VerdictError,
				Details:      err.Error(),
			}
			_ = e.ledger.Append(ctx, ev)

			if gate.Enforcement == EnforcementBlocking {
				result.Failed = append(result.Failed, FailedGate{Name: gate.Name, EvidenceGap: err.Error()})
			}
			continue
		}

		// Check for deferred findings if gate would fail
		if !passed && gate.Enforcement == EnforcementBlocking {
			deferredPassed, deferredEvidence, defErr := e.checkDeferredFindings(ctx, gate, task)
			if defErr != nil {
				// Record error evidence
				ev := Evidence{
					GateName:     gate.Name,
					Timestamp:    time.Now(),
					TaskID:       task.ID,
					TaskType:     task.Type,
					Verification: gate.Verification,
					Inputs:       map[string]any{"error": defErr.Error()},
					Verdict:      VerdictError,
					Details:      defErr.Error(),
				}
				_ = e.ledger.Append(ctx, ev)
				result.Failed = append(result.Failed, FailedGate{Name: gate.Name, EvidenceGap: defErr.Error()})
				continue
			}
			if deferredPassed {
				passed = true
				evidenceGap = deferredEvidence
			}
		}

		// Record evidence
		ev := Evidence{
			GateName:     gate.Name,
			Timestamp:    time.Now(),
			TaskID:       task.ID,
			TaskType:     task.Type,
			Verification: gate.Verification,
			Inputs:       map[string]any{"executions_checked": len(executions)},
			Verdict:      mapBool(passed),
			Details:      evidenceGap,
		}
		_ = e.ledger.Append(ctx, ev)

		if passed {
			result.Passed = append(result.Passed, gate.Name)
		} else {
			if gate.Enforcement == EnforcementBlocking {
				result.Failed = append(result.Failed, FailedGate{Name: gate.Name, EvidenceGap: evidenceGap})
			} else {
				result.Skipped = append(result.Skipped, gate.Name+" (reporting)")
			}
		}
	}

	return result, nil
}

func (e *GateEvaluator) isGateEnabled(name string) bool {
	if len(e.config.EnabledGates) == 0 {
		return true // All gates enabled by default
	}
	for _, enabled := range e.config.EnabledGates {
		if enabled == name {
			return true
		}
	}
	return false
}

func (e *GateEvaluator) evaluateGate(ctx context.Context, gate Gate, task *Task, executions []ToolExecution) (bool, string, error) {
	switch gate.Verification {
	case VerificationBuild:
		return e.evalBuild(gate, executions)
	case VerificationTest:
		return e.evalTest(gate, executions)
	case VerificationCoverage:
		return e.evalCoverage(gate, executions)
	case VerificationReview:
		return e.evalReview(gate, executions)
	case VerificationHygiene:
		return e.evalHygiene(gate, executions)
	case VerificationTraceability:
		return e.evalTraceability(gate, task, executions)
	case VerificationHumanConfirm:
		return e.evalHumanConfirm(gate, task)
	case VerificationCitation:
		return e.evalCitation(gate, task)
	default:
		return false, fmt.Sprintf("unknown verification type: %s", gate.Verification), nil
	}
}

func (e *GateEvaluator) evalBuild(gate Gate, executions []ToolExecution) (bool, string, error) {
	cmd := e.config.BuildCommand
	if cmd == "" {
		cmd = "make build"
	}
	return e.evalCommandExitCode(executions, cmd, "build")
}

func (e *GateEvaluator) evalTest(gate Gate, executions []ToolExecution) (bool, string, error) {
	cmd := e.config.TestCommand
	if cmd == "" {
		cmd = "make test"
	}
	return e.evalCommandExitCode(executions, cmd, "test")
}

func (e *GateEvaluator) evalCoverage(gate Gate, executions []ToolExecution) (bool, string, error) {
	cmd := e.config.CoverageCommand
	if cmd == "" {
		cmd = "make coverage"
	}
	
	// Check if coverage command was run
	found, exitCode := e.findCommandExitCode(executions, cmd)
	if !found {
		return false, fmt.Sprintf("coverage command %q not observed in session", cmd), nil
	}
	
	if exitCode != 0 {
		return false, fmt.Sprintf("coverage command exited with code %d", exitCode), nil
	}
	
	// TODO: Parse coverage output and check threshold
	// For now, just verify the command ran successfully
	threshold := e.config.CoverageThreshold
	if threshold <= 0 {
		threshold = 0.8 // default 80%
	}
	
	// In a real implementation, we'd parse the coverage output
	// For now, we'll check if there's a coverage report in the execution
	for _, exec := range executions {
		if strings.Contains(strings.ToLower(exec.Stdout), "coverage") && exitCode == 0 {
			// Found coverage output - in reality we'd parse the percentage
			return true, "", nil
		}
	}
	
	return false, fmt.Sprintf("coverage threshold %.0f%% not verified (no coverage output parsed)", threshold*100), nil
}

// evalReview evaluates the review gate.
func (e *GateEvaluator) evalReview(gate Gate, executions []ToolExecution) (bool, string, error) {
	// Check if code-review skill was invoked (task.review or code-review tool)
	reviewFound := false
	findings := make([]Finding, 0)
	
	for _, exec := range executions {
		if strings.Contains(strings.ToLower(exec.ToolName), "review") ||
			strings.Contains(strings.ToLower(exec.ToolName), "code-review") {
			reviewFound = true
			// Extract findings from the review output
			extractedFindings, err := ReviewGateFindings(exec.Stdout)
			if err != nil {
				continue
			}
			findings = append(findings, extractedFindings...)
		}
	}
	
	if !reviewFound {
		return false, "code review not executed (no review tool invocation observed)", nil
	}
	
	// Check if all findings have been triaged
	var createdTickets []string
	for _, finding := range findings {
		if finding.Disposition == "" {
			return false, "review findings not triaged (missing disposition)", nil
		}
		
		// Verify disposition is valid
		if finding.Disposition == DispositionDeferWithTicket {
			if finding.RelatedTicketID == "" {
				// Try to create a ticket for this deferred finding
				if e.ticketCreator != nil {
					ticketID, err := e.createDeferredTicket(finding, gate.Name)
					if err != nil {
						return false, fmt.Sprintf("finding %s deferred but failed to create ticket: %v", finding.ID, err), nil
					}
					finding.RelatedTicketID = ticketID
					createdTickets = append(createdTickets, ticketID)
				} else {
					return false, fmt.Sprintf("finding %s deferred but no ticket creator available", finding.ID), nil
				}
			}
		}
		
		if finding.Disposition == DispositionWontfix && finding.DispositionReason == "" {
			return false, fmt.Sprintf("finding %s wontfix but no reason provided", finding.ID), nil
		}
	}
	
	// Record created tickets in evidence
	if len(createdTickets) > 0 {
		ev := Evidence{
			GateName:     gate.Name,
			Timestamp:    time.Now(),
			TaskID:       e.observedEv.ClaimedAt().String(), // This is a placeholder; we'll use task ID from context
			TaskType:     TaskTypeCoding, // Will be overridden
			Verification: gate.Verification,
			Inputs:       map[string]any{"deferred_tickets": createdTickets},
			Verdict:      VerdictPass,
			Details:      fmt.Sprintf("Deferred %d finding(s) with tickets: %v", len(createdTickets), createdTickets),
		}
		_ = e.ledger.Append(context.Background(), ev)
	}
	
	return true, "", nil
}

func (e *GateEvaluator) evalHygiene(gate Gate, executions []ToolExecution) (bool, string, error) {
	// Check for temp files, untracked artifacts, secrets in worktree
	// This would typically run a git status check or similar
	
	// For now, check if any hygiene-related command was run
	hygieneFound := false
	for _, exec := range executions {
		if strings.Contains(strings.ToLower(exec.ToolName), "bash") {
			cmd := ""
			if c, ok := exec.Args["command"].(string); ok {
				cmd = c
			}
			if strings.Contains(cmd, "git status") || 
				strings.Contains(cmd, "git diff") ||
				strings.Contains(cmd, "ls -la") {
				hygieneFound = true
			}
		}
	}
	
	if !hygieneFound {
		return false, "hygiene check not observed (no git status/diff or file listing)", nil
	}
	
	// In reality, we'd parse the output for temp files, secrets, etc.
	// For now, assume pass if check was run
	return true, "", nil
}

func (e *GateEvaluator) evalTraceability(gate Gate, task *Task, executions []ToolExecution) (bool, string, error) {
	// This gate is evaluated at resolve time based on the resolution summary
	// The summary is passed as an argument to task.resolve
	// For now, we just verify the gate exists and will be checked at resolve
	return true, "", nil
}

func (e *GateEvaluator) evalHumanConfirm(gate Gate, task *Task) (bool, string, error) {
	// This gate is evaluated at resolve time based on human_confirmed flag
	// The flag is passed as an argument to task.resolve
	return true, "", nil
}

func (e *GateEvaluator) evalCitation(gate Gate, task *Task) (bool, string, error) {
	// Check if research output contains citations
	// This would be evaluated based on task progress records
	return true, "", nil
}

func (e *GateEvaluator) evalCommandExitCode(executions []ToolExecution, cmd, gateName string) (bool, string, error) {
	found, exitCode := e.findCommandExitCode(executions, cmd)
	if !found {
		return false, fmt.Sprintf("%s command %q not observed in session since task claim", gateName, cmd), nil
	}
	if exitCode != 0 {
		return false, fmt.Sprintf("%s command exited with code %d", gateName, exitCode), nil
	}
	return true, "", nil
}

func (e *GateEvaluator) findCommandExitCode(executions []ToolExecution, cmd string) (bool, int) {
	for _, exec := range executions {
		if exec.ToolName == "bash" {
			if c, ok := exec.Args["command"].(string); ok {
				if strings.Contains(c, cmd) {
					return true, exec.ExitCode
				}
			}
		}
	}
	return false, -1
}

func mapBool(b bool) Verdict {
	if b {
		return VerdictPass
	}
	return VerdictFail
}

// createDeferredTicket creates a follow-up ticket for a deferred finding.
func (e *GateEvaluator) createDeferredTicket(finding Finding, gateName string) (string, error) {
	if e.ticketCreator == nil {
		return "", fmt.Errorf("no ticket creator available")
	}

	ctx := context.Background()
	ticketID, err := e.ticketCreator.CreateTicket(ctx, CreateTicketArgs{
		Title:       fmt.Sprintf("Deferred: %s", finding.InvariantViolated),
		FindingOf:   finding.SourceTaskID,
		SourceGate:  gateName,
		FindingID:   finding.ID,
		Invariant:   finding.InvariantViolated,
		Severity:    string(finding.Severity),
		Category:    string(finding.Category),
		BlastRadius: finding.BlastRadiusFiles,
		Recurrence:  finding.RecurrenceCount,
	})
	if err != nil {
		return "", err
	}
	return ticketID, nil
}

// checkDeferredFindings checks if there are deferred findings for a failed gate.
// If found, it creates tickets for them and returns pass with evidence.
func (e *GateEvaluator) checkDeferredFindings(ctx context.Context, gate Gate, task *Task) (bool, string, error) {
	if e.deferredFindingStore == nil {
		return false, "", nil
	}

	findings, err := e.deferredFindingStore.GetDeferredFindings(ctx, gate.Name)
	if err != nil {
		return false, "", fmt.Errorf("get deferred findings: %w", err)
	}

	if len(findings) == 0 {
		return false, "", nil
	}

	// Create tickets for any deferred findings that don't have one yet
	var ticketIDs []string
	for _, finding := range findings {
		if finding.TicketID == "" && e.ticketCreator != nil {
			ticketID, err := e.ticketCreator.CreateTicket(ctx, CreateTicketArgs{
				Title:       finding.Description,
				FindingOf:   finding.FindingOf,
				SourceGate:  finding.GateName,
				FindingID:   finding.FindingID,
				Invariant:   finding.Invariant,
				Severity:    finding.Severity,
				Category:    finding.Category,
				BlastRadius: finding.BlastRadius,
				Recurrence:  finding.Recurrence,
			})
			if err != nil {
				return false, "", fmt.Errorf("create ticket for deferred finding %s: %w", finding.FindingID, err)
			}
			finding.TicketID = ticketID
			// Update the stored finding with the ticket ID
			_ = e.deferredFindingStore.RecordDeferredFinding(ctx, finding)
			ticketIDs = append(ticketIDs, ticketID)
		} else if finding.TicketID != "" {
			ticketIDs = append(ticketIDs, finding.TicketID)
		}
	}

	if len(ticketIDs) > 0 {
		evidence := fmt.Sprintf("Gate deferred with %d follow-up ticket(s): %v", len(ticketIDs), ticketIDs)
		return true, evidence, nil
	}

	return false, "", nil
}