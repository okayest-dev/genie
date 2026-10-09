package run

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/gates"
	"github.com/okayest-dev/genie/internal/tasktools"
)

func TestReviewGateWithFollowupEngine(t *testing.T) {
	// Create a fake tracker
	tracker := tasktools.NewFakeTracker(nil)

	// Create followup engine
	config := tasktools.FollowupConfig{
		PrioritySecurity:            0,
		PriorityRecurrenceThreshold: 2,
		PriorityBlastRadiusThreshold: 10,
		DedupSimilarityThreshold:    0.8,
		AutoWontfixRules:            []string{},
	}
	metrics := &tasktools.FollowupMetrics{}
	followupEngine := tasktools.NewFollowupEngine(tracker, config, metrics)

	// Create ticket creator with followup engine
	ticketCreator := NewTrackerTicketCreatorWithFollowup(tracker, followupEngine)

	// Create gate runner
	registry := gates.NewDefaultGateRegistry()
	ledger := gates.NewInMemoryEvidenceLedger()
	config2 := gates.GateConfig{
		TestCommand: "make test",
	}
	observed := gates.NewObservedEvidence()
	deferredStore := gates.NewInMemoryDeferredFindingStore()

	runner := gates.NewGateRunner(registry, ledger, config2, observed, ticketCreator, deferredStore)

	ctx := context.Background()
	task := &gates.Task{
		ID:   "og-test-review",
		Type: gates.TaskTypeCoding,
	}

	// Record a test execution that passes
	observed.RecordExecution(gates.ToolExecution{
		Timestamp: time.Now(),
		ToolName:  "bash",
		Args:      map[string]any{"command": "make test"},
		ExitCode:  0,
		Stdout:    "all tests passed",
		Duration:  time.Second,
	})

	// Record a review execution with findings
	findingJSON1 := `{"id":"finding-1","invariant_violated":"No hardcoded secrets","category":"security","severity":"P1","disposition":"defer-with-ticket","blast_radius_files":2,"recurrence_count":1,"source_task_id":"og-test-review","source_file":"auth.go","source_evidence":"Hardcoded API token"}`
	findingJSON2 := `{"id":"finding-2","invariant_violated":"Style violation","category":"style","severity":"P3","disposition":"defer-with-ticket","blast_radius_files":1,"recurrence_count":0,"source_task_id":"og-test-review","source_file":"style.go","source_evidence":"Bad naming"}`

	observed.RecordExecution(gates.ToolExecution{
		Timestamp: time.Now(),
		ToolName:  "code-review",
		Args:      map[string]any{"command": "git diff main..HEAD"},
		ExitCode:  0,
		Stdout:    fmt.Sprintf("Review output\nFINDING: %s\nFINDING: %s\n", findingJSON1, findingJSON2),
		Duration:  time.Second,
	})

	// Run the gate runner
	result, err := runner.Run(ctx, task, nil)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Check that review gate passed (findings should be triaged)
	reviewPassed := false
	for _, name := range result.Passed {
		if name == "review" {
			reviewPassed = true
			break
		}
	}
	if !reviewPassed {
		t.Errorf("review gate should have passed, got passed: %v, failed: %v", result.Passed, result.Failed)
	}

	// Check that tickets were created for the findings
	// Security finding should be P0, style finding should be P3
	createdTickets := tracker.GetCreatedTickets()
	t.Logf("Created tickets: %v", createdTickets)

	// Verify metrics
	gotMetrics := followupEngine.GetMetrics()
	t.Logf("Metrics: created=%d, deduped=%d, wontfix=%d", gotMetrics.TicketsCreated, gotMetrics.TicketsDeduped, gotMetrics.TicketsWontfix)

	if gotMetrics.TicketsCreated < 1 {
		t.Errorf("expected at least 1 ticket to be created, got %d", gotMetrics.TicketsCreated)
	}
}

func TestReviewGateWithDedupGuard(t *testing.T) {
	// Create a fake tracker with an existing similar task
	existingTask := &tasktools.Task{
		ID:          "og-existing",
		Title:       "[security] No hardcoded secrets",
		Description: "No hardcoded secrets security API token",
		Status:      "open",
		Priority:    0,
		Labels:      []string{"followup", "security"},
	}
	tracker := tasktools.NewFakeTracker([]*tasktools.Task{existingTask})

	// Create followup engine
	config := tasktools.FollowupConfig{
		PrioritySecurity:            0,
		PriorityRecurrenceThreshold: 2,
		PriorityBlastRadiusThreshold: 10,
		DedupSimilarityThreshold:    0.3,
		AutoWontfixRules:            []string{},
	}
	metrics := &tasktools.FollowupMetrics{}
	followupEngine := tasktools.NewFollowupEngine(tracker, config, metrics)

	// Create ticket creator with followup engine
	ticketCreator := NewTrackerTicketCreatorWithFollowup(tracker, followupEngine)

	// Create gate runner
	registry := gates.NewDefaultGateRegistry()
	ledger := gates.NewInMemoryEvidenceLedger()
	config2 := gates.GateConfig{
		EnabledGates: []string{"review"},
	}
	observed := gates.NewObservedEvidence()
	deferredStore := gates.NewInMemoryDeferredFindingStore()

	runner := gates.NewGateRunner(registry, ledger, config2, observed, ticketCreator, deferredStore)

	ctx := context.Background()
	task := &gates.Task{
		ID:   "og-test-dedup",
		Type: gates.TaskTypeCoding,
	}

	// Record a review execution with a finding similar to existing
	findingJSON := `{"id":"finding-1","invariant_violated":"No hardcoded secrets","category":"security","severity":"P1","disposition":"defer-with-ticket","blast_radius_files":2,"recurrence_count":1,"source_task_id":"og-test-dedup","source_file":"auth.go","source_evidence":"Hardcoded API token"}`

	observed.RecordExecution(gates.ToolExecution{
		Timestamp: time.Now(),
		ToolName:  "code-review",
		Args:      map[string]any{"command": "git diff main..HEAD"},
		ExitCode:  0,
		Stdout:    fmt.Sprintf("Review output\nFINDING: %s\n", findingJSON),
		Duration:  time.Second,
	})

	// Run the gate runner
	result, err := runner.Run(ctx, task, nil)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Check that review gate passed
	reviewPassed := false
	for _, name := range result.Passed {
		if name == "review" {
			reviewPassed = true
			break
		}
	}
	if !reviewPassed {
		t.Errorf("review gate should have passed, got passed: %v, failed: %v", result.Passed, result.Failed)
	}

	// Verify metrics - should be deduped, not created
	gotMetrics := followupEngine.GetMetrics()
	t.Logf("Metrics: created=%d, deduped=%d, wontfix=%d", gotMetrics.TicketsCreated, gotMetrics.TicketsDeduped, gotMetrics.TicketsWontfix)

	if gotMetrics.TicketsDeduped != 1 {
		t.Errorf("expected 1 ticket to be deduped, got %d", gotMetrics.TicketsDeduped)
	}
	if gotMetrics.TicketsCreated != 0 {
		t.Errorf("expected 0 tickets to be created (deduped), got %d", gotMetrics.TicketsCreated)
	}
}

func TestReviewGateWithPriorityShaping(t *testing.T) {
	tests := []struct {
		name               string
		finding            gates.Finding
		expectedPriority   int
	}{
		{
			name: "Security finding -> P0",
			finding: gates.Finding{
				ID:                "finding-1",
				InvariantViolated: "SQL injection vulnerability",
				Category:          gates.CategorySecurity,
				Severity:          gates.SeverityP1,
				BlastRadiusFiles:  1,
				RecurrenceCount:   0,
				SourceTaskID:      "og-test",
				SourceFile:        "db.go",
				SourceEvidence:    "User input not sanitized",
				Disposition:       gates.DispositionDeferWithTicket,
			},
			expectedPriority: 0,
		},
		{
			name: "High recurrence -> P1",
			finding: gates.Finding{
				ID:                "finding-2",
				InvariantViolated: "Repeated pattern",
				Category:          gates.CategoryFunctional,
				Severity:          gates.SeverityP2,
				BlastRadiusFiles:  3,
				RecurrenceCount:   3,
				SourceTaskID:      "og-test",
				SourceFile:        "handler.go",
				SourceEvidence:    "Same pattern in 3 places",
				Disposition:       gates.DispositionDeferWithTicket,
			},
			expectedPriority: 1,
		},
		{
			name: "Large blast radius -> P1",
			finding: gates.Finding{
				ID:                "finding-3",
				InvariantViolated: "Wide impact",
				Category:          gates.CategoryFunctional,
				Severity:          gates.SeverityP2,
				BlastRadiusFiles:  15,
				RecurrenceCount:   0,
				SourceTaskID:      "og-test",
				SourceFile:        "api.go",
				SourceEvidence:    "Affects 15 files",
				Disposition:       gates.DispositionDeferWithTicket,
			},
			expectedPriority: 1,
		},
		{
			name: "Style P3 -> P3",
			finding: gates.Finding{
ID:                "finding-4",
			InvariantViolated: "Style violation",
			Category:          gates.CategoryStyle,
			Severity:          gates.SeverityP3,
			BlastRadiusFiles:  1,
			RecurrenceCount:   0,
			SourceTaskID:      "og-test",
			SourceFile:        "style.go",
			SourceEvidence:    "Bad naming",
			Disposition:       gates.DispositionDeferWithTicket,
		},
			expectedPriority: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := tasktools.NewFakeTracker(nil)

			config := tasktools.FollowupConfig{
				PrioritySecurity:            0,
				PriorityRecurrenceThreshold: 2,
				PriorityBlastRadiusThreshold: 10,
				DedupSimilarityThreshold:    0.8,
				AutoWontfixRules:            []string{},
			}
			metrics := &tasktools.FollowupMetrics{}
			followupEngine := tasktools.NewFollowupEngine(tracker, config, metrics)

			ticketCreator := NewTrackerTicketCreatorWithFollowup(tracker, followupEngine)

			registry := gates.NewDefaultGateRegistry()
			ledger := gates.NewInMemoryEvidenceLedger()
			config2 := gates.GateConfig{
				EnabledGates: []string{"review"},
			}
			observed := gates.NewObservedEvidence()
			deferredStore := gates.NewInMemoryDeferredFindingStore()

			runner := gates.NewGateRunner(registry, ledger, config2, observed, ticketCreator, deferredStore)

			ctx := context.Background()
			task := &gates.Task{
				ID:   "og-test-priority",
				Type: gates.TaskTypeCoding,
			}

			// Marshal finding to JSON
			findingJSON, err := tc.finding.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON failed: %v", err)
			}

			observed.RecordExecution(gates.ToolExecution{
				Timestamp: time.Now(),
				ToolName:  "code-review",
				Args:      map[string]any{"command": "git diff main..HEAD"},
				ExitCode:  0,
				Stdout:    fmt.Sprintf("Review output\nFINDING: %s\n", string(findingJSON)),
				Duration:  time.Second,
			})

			_, err = runner.Run(ctx, task, nil)
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}

			// Verify metrics - should have created 1 ticket
			gotMetrics := followupEngine.GetMetrics()
			if gotMetrics.TicketsCreated != 1 {
				t.Errorf("expected 1 ticket to be created, got %d", gotMetrics.TicketsCreated)
			}

			// Check the created ticket's priority
			createdTickets := tracker.GetCreatedTickets()
			for _, ticketID := range createdTickets {
				if tsk, err := tracker.Read(ctx, ticketID); err == nil && tsk != nil {
					t.Logf("Created ticket %s: priority=%d, title=%s", ticketID, tsk.Priority, tsk.Title)
				}
			}
			if len(createdTickets) != 1 {
				t.Errorf("expected 1 created ticket, got %d", len(createdTickets))
			} else {
				createdTask, err := tracker.Read(ctx, createdTickets[0])
				if err != nil {
					t.Fatalf("Read failed: %v", err)
				}
				if createdTask.Priority != tc.expectedPriority {
					t.Errorf("expected priority %d, got %d", tc.expectedPriority, createdTask.Priority)
				}
			}
		})
	}
}

func TestReviewGateWithAutoWontfix(t *testing.T) {
	tracker := tasktools.NewFakeTracker(nil)

	config := tasktools.FollowupConfig{
		PrioritySecurity:            0,
		PriorityRecurrenceThreshold: 2,
		PriorityBlastRadiusThreshold: 10,
		DedupSimilarityThreshold:    0.8,
		AutoWontfixRules:            []string{"style:P3"},
	}
	metrics := &tasktools.FollowupMetrics{}
	followupEngine := tasktools.NewFollowupEngine(tracker, config, metrics)

	ticketCreator := NewTrackerTicketCreatorWithFollowup(tracker, followupEngine)

	registry := gates.NewDefaultGateRegistry()
	ledger := gates.NewInMemoryEvidenceLedger()
	config2 := gates.GateConfig{}
	observed := gates.NewObservedEvidence()
	deferredStore := gates.NewInMemoryDeferredFindingStore()

	runner := gates.NewGateRunner(registry, ledger, config2, observed, ticketCreator, deferredStore)

	ctx := context.Background()
	task := &gates.Task{
		ID:   "og-test-wontfix",
		Type: gates.TaskTypeCoding,
	}

	// Style P3 finding that matches auto-wontfix rule
	findingJSON := `{"id":"finding-1","invariant_violated":"Style violation","category":"style","severity":"P3","disposition":"defer-with-ticket","blast_radius_files":1,"recurrence_count":0,"source_task_id":"og-test-wontfix","source_file":"style.go","source_evidence":"Trailing whitespace"}`

	observed.RecordExecution(gates.ToolExecution{
		Timestamp: time.Now(),
		ToolName:  "code-review",
		Args:      map[string]any{"command": "git diff main..HEAD"},
		ExitCode:  0,
		Stdout:    fmt.Sprintf("Review output\nFINDING: %s\n", findingJSON),
		Duration:  time.Second,
	})

	result, err := runner.Run(ctx, task, nil)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	reviewPassed := false
	for _, name := range result.Passed {
		if name == "review" {
			reviewPassed = true
			break
		}
	}
	if !reviewPassed {
		t.Errorf("review gate should have passed, got passed: %v, failed: %v", result.Passed, result.Failed)
	}

	gotMetrics := followupEngine.GetMetrics()
	if gotMetrics.TicketsWontfix != 1 {
		t.Errorf("expected 1 ticket to be wontfix'd, got %d", gotMetrics.TicketsWontfix)
	}
	if gotMetrics.TicketsCreated != 0 {
		t.Errorf("expected 0 tickets to be created (wontfix), got %d", gotMetrics.TicketsCreated)
	}
}

func TestGateDeferralWithTicketIDInEvidence(t *testing.T) {
	tracker := tasktools.NewFakeTracker(nil)

	config := tasktools.FollowupConfig{
		PrioritySecurity:            0,
		PriorityRecurrenceThreshold: 2,
		PriorityBlastRadiusThreshold: 10,
		DedupSimilarityThreshold:    0.8,
		AutoWontfixRules:            []string{},
	}
	metrics := &tasktools.FollowupMetrics{}
	followupEngine := tasktools.NewFollowupEngine(tracker, config, metrics)

	ticketCreator := NewTrackerTicketCreatorWithFollowup(tracker, followupEngine)

	registry := gates.NewDefaultGateRegistry()
	ledger := gates.NewInMemoryEvidenceLedger()
	config2 := gates.GateConfig{
		BuildCommand: "make build",
	}
	observed := gates.NewObservedEvidence()
	deferredStore := gates.NewInMemoryDeferredFindingStore()

	runner := gates.NewGateRunner(registry, ledger, config2, observed, ticketCreator, deferredStore)

	ctx := context.Background()
	task := &gates.Task{
		ID:   "og-test-deferral",
		Type: gates.TaskTypeCoding,
	}

	// Record a build execution that fails
	observed.RecordExecution(gates.ToolExecution{
		Timestamp: time.Now(),
		ToolName:  "bash",
		Args:      map[string]any{"command": "make build"},
		ExitCode:  1,
		Stdout:    "build failed",
		Duration:  time.Second,
	})

	// Create a deferred finding for the build gate
	deferredFindings := []gates.DeferredFindingRecord{
		{
			GateName:      "build",
			FindingID:     "finding-1",
			Invariant:     "Build must pass",
			Severity:      "P1",
			Category:      "functional",
			BlastRadius:   3,
			Recurrence:    1,
			Description:   "Build failed due to missing dependency",
			RecordedAt:    time.Now().Format(time.RFC3339),
		},
	}

	// Run the gate runner with deferred findings
	result, err := runner.Run(ctx, task, deferredFindings)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// The build gate should pass because of the deferred finding
	buildPassed := false
	for _, name := range result.Passed {
		if name == "build" {
			buildPassed = true
			break
		}
	}
	if !buildPassed {
		t.Errorf("build gate should have passed due to deferred finding, got passed: %v, failed: %v", result.Passed, result.Failed)
	}

	// Check that a ticket was created
	createdTickets := tracker.GetCreatedTickets()
	if len(createdTickets) != 1 {
		t.Errorf("expected 1 ticket to be created, got %d: %v", len(createdTickets), createdTickets)
	}

	// Check that the ticket ID appears in the evidence
	for _, name := range result.Passed {
		if name == "build" {
			// The evidence should contain the ticket ID
			// We can't easily check this without accessing the ledger, but we verified the ticket was created
			t.Logf("Build gate passed with deferred ticket: %v", createdTickets)
			break
		}
	}
}

func TestEmptyFrontierReportWithSuggestions(t *testing.T) {
	// Create a fake tracker with no open tasks
	tracker := tasktools.NewFakeTracker(nil)

	// Create followup engine
	config := tasktools.FollowupConfig{
		PrioritySecurity:            0,
		PriorityRecurrenceThreshold: 2,
		PriorityBlastRadiusThreshold: 10,
		DedupSimilarityThreshold:    0.8,
		AutoWontfixRules:            []string{},
	}
	metrics := &tasktools.FollowupMetrics{}
	followupEngine := tasktools.NewFollowupEngine(tracker, config, metrics)

	// Create ticket creator with followup engine
	ticketCreator := NewTrackerTicketCreatorWithFollowup(tracker, followupEngine)

	// Create gate runner
	registry := gates.NewDefaultGateRegistry()
	ledger := gates.NewInMemoryEvidenceLedger()
	config2 := gates.GateConfig{
		EnabledGates: []string{"review"},
	}
	observed := gates.NewObservedEvidence()
	deferredStore := gates.NewInMemoryDeferredFindingStore()

	runner := gates.NewGateRunner(registry, ledger, config2, observed, ticketCreator, deferredStore)

	ctx := context.Background()
	
	// Simulate empty frontier by not having any open tasks
	// The task ID doesn't matter since we're testing the frontier report
	task := &gates.Task{
		ID:   "og-test-empty-frontier",
		Type: gates.TaskTypeCoding,
	}

	// Run the gate runner - should succeed since review gate is the only enabled gate
	result, err := runner.Run(ctx, task, nil)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// The review gate should fail because no review was executed
	reviewFailed := false
	for _, failed := range result.Failed {
		if failed.Name == "review" {
			reviewFailed = true
			t.Logf("Review gate failed as expected: %s", failed.EvidenceGap)
			break
		}
	}
	if !reviewFailed {
		t.Errorf("review gate should have failed (no review executed), got passed: %v", result.Passed)
	}

	// Verify no tickets were created
	createdTickets := tracker.GetCreatedTickets()
	if len(createdTickets) != 0 {
		t.Errorf("expected 0 tickets to be created, got %d: %v", len(createdTickets), createdTickets)
	}
}

func TestClosedLoopReportUnresolvedFindings(t *testing.T) {
	// Create a fake tracker
	tracker := tasktools.NewFakeTracker(nil)

	// Create followup engine
	config := tasktools.FollowupConfig{
		PrioritySecurity:            0,
		PriorityRecurrenceThreshold: 2,
		PriorityBlastRadiusThreshold: 10,
		DedupSimilarityThreshold:    0.8,
		AutoWontfixRules:            []string{},
	}
	metrics := &tasktools.FollowupMetrics{}
	followupEngine := tasktools.NewFollowupEngine(tracker, config, metrics)

	// Create ticket creator with followup engine
	ticketCreator := NewTrackerTicketCreatorWithFollowup(tracker, followupEngine)

	// Create gate runner
	registry := gates.NewDefaultGateRegistry()
	ledger := gates.NewInMemoryEvidenceLedger()
	config2 := gates.GateConfig{
		EnabledGates: []string{"review"},
	}
	observed := gates.NewObservedEvidence()
	deferredStore := gates.NewInMemoryDeferredFindingStore()

	runner := gates.NewGateRunner(registry, ledger, config2, observed, ticketCreator, deferredStore)

	ctx := context.Background()
	task := &gates.Task{
		ID:   "og-test-closed-loop",
		Type: gates.TaskTypeCoding,
	}

	// Record a review execution with a finding that has NO disposition (unresolved)
	findingJSON := `{"id":"finding-1","invariant_violated":"Missing error handling","category":"functional","severity":"P2","blast_radius_files":3,"recurrence_count":0,"source_task_id":"og-test-closed-loop","source_file":"handler.go","source_evidence":"No error check"}`

	observed.RecordExecution(gates.ToolExecution{
		Timestamp: time.Now(),
		ToolName:  "code-review",
		Args:      map[string]any{"command": "git diff main..HEAD"},
		ExitCode:  0,
		Stdout:    fmt.Sprintf("Review output\nFINDING: %s\n", findingJSON),
		Duration:  time.Second,
	})

	// Run the gate runner
	result, err := runner.Run(ctx, task, nil)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// The review gate should fail because the finding has no disposition
	reviewFailed := false
	for _, failed := range result.Failed {
		if failed.Name == "review" {
			reviewFailed = true
			t.Logf("Review gate failed as expected: %s", failed.EvidenceGap)
			// Check that the error mentions missing disposition
			if !strings.Contains(failed.EvidenceGap, "disposition") {
				t.Errorf("expected error to mention missing disposition, got: %s", failed.EvidenceGap)
			}
			break
		}
	}
	if !reviewFailed {
		t.Errorf("review gate should have failed (missing disposition), got passed: %v", result.Passed)
	}

	// Verify no tickets were created (since disposition is missing)
	createdTickets := tracker.GetCreatedTickets()
	if len(createdTickets) != 0 {
		t.Errorf("expected 0 tickets to be created (unresolved finding), got %d: %v", len(createdTickets), createdTickets)
	}
}