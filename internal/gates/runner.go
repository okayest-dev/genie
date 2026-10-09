package gates

import (
	"context"
	"time"
)

// GateRunnerImpl implements the GateRunner interface for tasktools.
type GateRunnerImpl struct {
	evaluator *GateEvaluator
	registry  GateRegistry
	ledger    EvidenceLedger
	config    GateConfig
}

// NewGateRunner creates a new gate runner.
func NewGateRunner(registry GateRegistry, ledger EvidenceLedger, config GateConfig, observed *ObservedEvidenceTracker, ticketCreator TicketCreator, deferredFindingStore DeferredFindingStore) *GateRunnerImpl {
	evaluator := NewGateEvaluator(registry, ledger, config, observed, ticketCreator, deferredFindingStore)
	return &GateRunnerImpl{
		evaluator: evaluator,
		registry:  registry,
		ledger:    ledger,
		config:    config,
	}
}

// Run runs the gate set for a task and returns the result.
// deferredFindings are the deferred findings recorded during task work.
func (r *GateRunnerImpl) Run(ctx context.Context, task *Task, deferredFindings []DeferredFindingRecord) (GateResult, error) {
	// Sync deferred findings to the store
	if r.evaluator.deferredFindingStore != nil && len(deferredFindings) > 0 {
		for _, df := range deferredFindings {
			finding := DeferredFinding{
				GateName:      df.GateName,
				FindingID:     df.FindingID,
				Invariant:     df.Invariant,
				Severity:      df.Severity,
				Category:      df.Category,
				BlastRadius:   df.BlastRadius,
				Recurrence:    df.Recurrence,
				FindingOf:     task.ID, // The source task is the current task
				Description:   df.Description,
				CreatedAt:     time.Now(), // Will be parsed from df.RecordedAt if needed
			}
			// Parse the recorded time
			if t, err := time.Parse(time.RFC3339, df.RecordedAt); err == nil {
				finding.CreatedAt = t
			}
			_ = r.evaluator.deferredFindingStore.RecordDeferredFinding(ctx, finding)
		}
	}
	result, err := r.evaluator.Run(ctx, task)
	if err != nil {
		return GateResult{}, err
	}
	return result, nil
}

// DefaultGateConfig returns the default gate configuration.
func DefaultGateConfig() GateConfig {
	return GateConfig{
		CoverageThreshold: 0.8,
		RefixAttempts:     3,
		EnabledGates:      []string{},
		ReportingGates:    []string{},
		TestCommand:       "",
		BuildCommand:      "",
		CoverageCommand:   "",
	}
}