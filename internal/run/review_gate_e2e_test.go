package run

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/gates"
	"github.com/okayest-dev/genie/internal/tasktools"
)

// reviewHarness wires the real tools a session uses for the review gate: the
// agent attests a review with task_review, then resolves with task_resolve.
type reviewHarness struct {
	tracker  *tasktools.FakeTracker
	state    *tasktools.TaskState
	ledger   gates.EvidenceLedger
	review   *tasktools.TaskReviewTool
	resolve  *tasktools.TaskResolveTool
	followup *tasktools.FollowupEngine
}

// newReviewHarness builds the harness around a tracker holding `existing` plus
// the active task `id`, which is set as the session's active task.
func newReviewHarness(t *testing.T, id string, existing []*tasktools.Task, dedup float64) *reviewHarness {
	t.Helper()

	active := &tasktools.Task{ID: id, Title: "Work " + id, Type: "task", Status: "in_progress", Assignee: "test"}
	tracker := tasktools.NewFakeTracker(append([]*tasktools.Task{active}, existing...))
	state := tasktools.NewTaskState()

	ledger := gates.NewInMemoryEvidenceLedger()
	registry := gates.NewDefaultGateRegistry()
	// Only the review gate is enabled so build/test/coverage execution evidence
	// isn't required for the test to be meaningful.
	cfg := gates.GateConfig{EnabledGates: []string{"review"}}

	followup := tasktools.NewFollowupEngine(tracker, tasktools.FollowupConfig{
		PrioritySecurity:             0,
		PriorityRecurrenceThreshold:  2,
		PriorityBlastRadiusThreshold: 10,
		DedupSimilarityThreshold:     dedup,
	}, &tasktools.FollowupMetrics{})
	ticketCreator := NewTrackerTicketCreatorWithFollowup(tracker, followup)

	observed := state.EvidenceTracker()
	runner := gates.NewGateRunner(registry, ledger, cfg, observed.Observed(), ticketCreator, gates.NewInMemoryDeferredFindingStore(), nil)

	h := &reviewHarness{
		tracker:  tracker,
		state:    state,
		ledger:   ledger,
		review:   tasktools.NewTaskReviewTool(state, true, 5*time.Second),
		resolve:  tasktools.NewTaskResolveTool(tracker, state, runner, true, 5*time.Second),
		followup: followup,
	}
	state.Set(active)
	return h
}

func (h *reviewHarness) recordReview(t *testing.T, args tasktools.TaskReviewArgs) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal review args: %v", err)
	}
	if _, err := h.review.Execute(raw); err != nil {
		t.Fatalf("task_review failed: %v", err)
	}
}

func (h *reviewHarness) resolveTask(t *testing.T) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(tasktools.TaskResolveArgs{Summary: "done"})
	return h.resolve.Execute(raw)
}

func finding(id, category, severity, disposition string) tasktools.TaskReviewFindingArgs {
	return tasktools.TaskReviewFindingArgs{
		FindingID:   id,
		Invariant:   "invariant " + id,
		Category:    category,
		Severity:    severity,
		Disposition: disposition,
	}
}

func TestTrackerTicketCreatorFallbackSetsProvenance(t *testing.T) {
	tr := tasktools.NewFakeTracker(nil)
	creator := NewTrackerTicketCreator(tr)

	id, err := creator.CreateTicket(context.Background(), gates.CreateTicketArgs{
		Title:       "Follow-up",
		FindingID:   "f1",
		Invariant:   "inv",
		Category:    "security",
		Severity:    "P1",
		BlastRadius: 2,
		Recurrence:  1,
		SourceGate:  "review",
		FindingOf:   "og-src",
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	ticket, err := tr.Read(context.Background(), id)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if ticket.FindingOf != "og-src" {
		t.Errorf("FindingOf = %q, want og-src", ticket.FindingOf)
	}
	if !strings.Contains(ticket.Description, "og-src") {
		t.Errorf("description should cite the source task: %q", ticket.Description)
	}
}

func TestTrackerTicketCreatorDisabled(t *testing.T) {
	creator := &TrackerTicketCreator{}
	if _, err := creator.CreateTicket(context.Background(), gates.CreateTicketArgs{}); err == nil {
		t.Fatal("disabled creator should error")
	}
}

func TestReviewGateDeferredFindingCreatesTicketWithProvenance(t *testing.T) {
	h := newReviewHarness(t, "og-src", nil, 0.8)

	f := finding("f1", "security", "P1", tasktools.DispositionDeferWithTicket)
	h.recordReview(t, tasktools.TaskReviewArgs{
		Subject:  "diff main..HEAD",
		Method:   "code-review skill",
		Findings: []tasktools.TaskReviewFindingArgs{f},
	})

	if _, err := h.resolveTask(t); err != nil {
		t.Fatalf("resolve should pass with a dispositioned finding: %v", err)
	}

	created := h.tracker.GetCreatedTickets()
	if len(created) != 1 {
		t.Fatalf("expected 1 follow-up ticket, got %v", created)
	}
	ticket, err := h.tracker.Read(context.Background(), created[0])
	if err != nil {
		t.Fatalf("read created ticket: %v", err)
	}
	// Provenance: the ticket cites the source task.
	if ticket.FindingOf != "og-src" {
		t.Errorf("ticket finding_of = %q, want og-src", ticket.FindingOf)
	}
	// Security findings shape toward P0.
	if ticket.Priority != 0 {
		t.Errorf("security ticket priority = %d, want 0", ticket.Priority)
	}

	// The ticket id is recorded as review-gate evidence against the source task.
	evs, err := h.ledger.Get(context.Background(), "og-src")
	if err != nil {
		t.Fatalf("ledger.Get: %v", err)
	}
	found := false
	for _, e := range evs {
		if e.GateName != "review" {
			continue
		}
		if e.TaskID != "og-src" {
			t.Errorf("review evidence TaskID = %q, want og-src", e.TaskID)
		}
		if ids, ok := e.Inputs["deferred_tickets"].([]string); ok && len(ids) == 1 && ids[0] == created[0] {
			found = true
		}
	}
	if !found {
		t.Errorf("review evidence did not carry the follow-up ticket id: %+v", evs)
	}
}

func TestReviewGateCleanReviewPasses(t *testing.T) {
	h := newReviewHarness(t, "og-clean", nil, 0.8)

	h.recordReview(t, tasktools.TaskReviewArgs{Subject: "diff main..HEAD"})
	if _, err := h.resolveTask(t); err != nil {
		t.Fatalf("clean review should resolve: %v", err)
	}
	if created := h.tracker.GetCreatedTickets(); len(created) != 0 {
		t.Errorf("clean review created tickets: %v", created)
	}
}

func TestReviewGateResolveInlineAndWontfixPass(t *testing.T) {
	h := newReviewHarness(t, "og-mixed", nil, 0.8)

	inline := finding("f1", "style", "P3", tasktools.DispositionResolveInline)
	wont := finding("f2", "style", "P3", tasktools.DispositionWontfix)
	wont.Reason = "out of scope for this task"

	h.recordReview(t, tasktools.TaskReviewArgs{
		Subject:  "diff main..HEAD",
		Findings: []tasktools.TaskReviewFindingArgs{inline, wont},
	})
	if _, err := h.resolveTask(t); err != nil {
		t.Fatalf("resolve-inline + wontfix should resolve: %v", err)
	}
	if created := h.tracker.GetCreatedTickets(); len(created) != 0 {
		t.Errorf("resolve-inline/wontfix should not create tickets, got %v", created)
	}
}

func TestReviewGateMissingAttestationRefusesResolve(t *testing.T) {
	h := newReviewHarness(t, "og-no-review", nil, 0.8)

	_, err := h.resolveTask(t)
	if err == nil {
		t.Fatal("resolve without a review should be refused")
	}
	if !strings.Contains(err.Error(), "review") {
		t.Errorf("error should cite the review gate, got %v", err)
	}
	// The task stays open.
	task, _ := h.tracker.Read(context.Background(), "og-no-review")
	if task.Status == "closed" {
		t.Errorf("task should not be closed when the review gate fails")
	}
}

func TestReviewGateUndispositionedFindingRefusedByTool(t *testing.T) {
	h := newReviewHarness(t, "og-undispositioned", nil, 0.8)

	f := finding("f1", "functional", "P2", "") // no disposition
	raw, _ := json.Marshal(tasktools.TaskReviewArgs{Subject: "diff", Findings: []tasktools.TaskReviewFindingArgs{f}})
	if _, err := h.review.Execute(raw); err == nil {
		t.Fatal("task_review should reject a finding without a disposition")
	}
}

func TestReviewGateSecurityWontfixNeedsHuman(t *testing.T) {
	h := newReviewHarness(t, "og-wontfix", nil, 0.8)

	f := finding("f1", "security", "P1", tasktools.DispositionWontfix)
	f.Reason = "accepted risk"
	raw, _ := json.Marshal(tasktools.TaskReviewArgs{Subject: "diff", Findings: []tasktools.TaskReviewFindingArgs{f}})
	if _, err := h.review.Execute(raw); err == nil {
		t.Fatal("security wontfix without human confirmation should be rejected")
	}

	// With human confirmation it is recorded and resolves.
	raw, _ = json.Marshal(tasktools.TaskReviewArgs{Subject: "diff", HumanConfirmed: true, Findings: []tasktools.TaskReviewFindingArgs{f}})
	if _, err := h.review.Execute(raw); err != nil {
		t.Fatalf("security wontfix with human confirmation should be accepted: %v", err)
	}
	if _, err := h.resolveTask(t); err != nil {
		t.Fatalf("resolve should pass: %v", err)
	}
}

func TestReviewGateDedupAbsorbsFindingAsComment(t *testing.T) {
	existing := &tasktools.Task{
		ID:          "og-existing",
		Title:       "[Security] No hardcoded secrets in code",
		Description: "Invariant no hardcoded secrets security api token in auth",
		Status:      "open",
		Priority:    0,
		Labels:      []string{"followup", "security"},
	}
	h := newReviewHarness(t, "og-dedup", []*tasktools.Task{existing}, 0.3)

	f := tasktools.TaskReviewFindingArgs{
		FindingID:   "f1",
		Invariant:   "no hardcoded secrets in code",
		Category:    "security",
		Severity:    "P1",
		Description: "hardcoded secrets security api token in auth",
		BlastRadius: 2,
		Recurrence:  1,
		Disposition: tasktools.DispositionDeferWithTicket,
	}
	h.recordReview(t, tasktools.TaskReviewArgs{Subject: "diff", Findings: []tasktools.TaskReviewFindingArgs{f}})

	if _, err := h.resolveTask(t); err != nil {
		t.Fatalf("resolve should pass when the finding is deduped: %v", err)
	}
	if created := h.tracker.GetCreatedTickets(); len(created) != 0 {
		t.Errorf("deduped finding should not create a ticket, got %v", created)
	}
	comments := h.tracker.GetComments("og-existing")
	absorbed := false
	for _, c := range comments {
		if strings.Contains(c, "Absorbed finding") {
			absorbed = true
		}
	}
	if !absorbed {
		t.Errorf("expected the finding absorbed as a comment, got %v", comments)
	}
	if m := h.followup.GetMetrics(); m.TicketsDeduped != 1 {
		t.Errorf("deduped metric = %d, want 1", m.TicketsDeduped)
	}
}

func TestReviewGatePriorityShaping(t *testing.T) {
	tests := []struct {
		name             string
		finding          tasktools.TaskReviewFindingArgs
		expectedPriority int
	}{
		{
			name:             "security -> P0",
			finding:          tasktools.TaskReviewFindingArgs{FindingID: "f1", Invariant: "sql injection", Category: "security", Severity: "P1", Disposition: tasktools.DispositionDeferWithTicket},
			expectedPriority: 0,
		},
		{
			name:             "high recurrence -> P1",
			finding:          tasktools.TaskReviewFindingArgs{FindingID: "f1", Invariant: "repeated pattern", Category: "functional", Severity: "P2", Recurrence: 3, Disposition: tasktools.DispositionDeferWithTicket},
			expectedPriority: 1,
		},
		{
			name:             "large blast radius -> P1",
			finding:          tasktools.TaskReviewFindingArgs{FindingID: "f1", Invariant: "wide impact", Category: "functional", Severity: "P2", BlastRadius: 15, Disposition: tasktools.DispositionDeferWithTicket},
			expectedPriority: 1,
		},
		{
			name:             "style P3 -> P3",
			finding:          tasktools.TaskReviewFindingArgs{FindingID: "f1", Invariant: "style nit", Category: "style", Severity: "P3", Disposition: tasktools.DispositionDeferWithTicket},
			expectedPriority: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newReviewHarness(t, "og-priority", nil, 0.8)
			h.recordReview(t, tasktools.TaskReviewArgs{Subject: "diff", Findings: []tasktools.TaskReviewFindingArgs{tc.finding}})
			if _, err := h.resolveTask(t); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			created := h.tracker.GetCreatedTickets()
			if len(created) != 1 {
				t.Fatalf("expected 1 ticket, got %v", created)
			}
			ticket, err := h.tracker.Read(context.Background(), created[0])
			if err != nil {
				t.Fatalf("read ticket: %v", err)
			}
			if ticket.Priority != tc.expectedPriority {
				t.Errorf("priority = %d, want %d", ticket.Priority, tc.expectedPriority)
			}
		})
	}
}
