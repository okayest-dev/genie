package tasktools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func activeReviewState(t *testing.T) (*TaskState, *Task) {
	t.Helper()
	state := NewTaskState()
	task := &Task{ID: "og-r1", Title: "Review me", Type: "task", Status: "in_progress"}
	state.Set(task)
	return state, task
}

func executeReview(t *testing.T, tool *TaskReviewTool, args TaskReviewArgs) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return tool.Execute(raw)
}

func TestTaskReviewToolNoActiveTask(t *testing.T) {
	state := NewTaskState()
	tool := NewTaskReviewTool(state, true, 30*time.Second)

	_, err := tool.Execute([]byte(`{"subject":"diff"}`))
	if err == nil || !strings.Contains(err.Error(), "no active task") {
		t.Fatalf("error = %v, want 'no active task'", err)
	}
}

func TestTaskReviewToolDisabled(t *testing.T) {
	state, _ := activeReviewState(t)
	tool := NewTaskReviewTool(state, false, 30*time.Second)

	_, err := tool.Execute([]byte(`{"subject":"diff"}`))
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("error = %v, want disabled", err)
	}
}

func TestTaskReviewToolRequiresSubject(t *testing.T) {
	state, _ := activeReviewState(t)
	tool := NewTaskReviewTool(state, true, 30*time.Second)

	_, err := executeReview(t, tool, TaskReviewArgs{})
	if err == nil || !strings.Contains(err.Error(), "subject is required") {
		t.Fatalf("error = %v, want subject required", err)
	}
}

func TestTaskReviewToolCleanReview(t *testing.T) {
	state, _ := activeReviewState(t)
	tool := NewTaskReviewTool(state, true, 30*time.Second)

	out, err := executeReview(t, tool, TaskReviewArgs{Subject: "diff main..HEAD", Method: "code-review skill"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "clean review") {
		t.Errorf("output = %q, want clean review note", out)
	}

	reviews := state.GetReviews()
	if len(reviews) != 1 {
		t.Fatalf("reviews = %d, want 1", len(reviews))
	}
	if reviews[0].Subject != "diff main..HEAD" || reviews[0].Method != "code-review skill" {
		t.Errorf("review = %+v", reviews[0])
	}
	if len(reviews[0].Findings) != 0 {
		t.Errorf("findings = %v, want none", reviews[0].Findings)
	}
}

func TestTaskReviewToolRecordsFindings(t *testing.T) {
	state, _ := activeReviewState(t)
	tool := NewTaskReviewTool(state, true, 30*time.Second)

	args := TaskReviewArgs{
		Subject: "diff main..HEAD",
		Findings: []TaskReviewFindingArgs{
			{FindingID: "f1", Invariant: "no secrets", Category: "security", Severity: "P1", Disposition: DispositionDeferWithTicket, BlastRadius: 2, Recurrence: 1},
			{FindingID: "f2", Invariant: "naming", Category: "style", Severity: "P3", Disposition: DispositionResolveInline},
		},
	}
	out, err := executeReview(t, tool, args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "Findings: 2") {
		t.Errorf("output = %q", out)
	}

	reviews := state.GetReviews()
	if len(reviews) != 1 || len(reviews[0].Findings) != 2 {
		t.Fatalf("reviews = %+v", reviews)
	}
	if reviews[0].Findings[0].Disposition != DispositionDeferWithTicket {
		t.Errorf("finding 0 disposition = %q", reviews[0].Findings[0].Disposition)
	}
}

func TestTaskReviewToolValidationErrors(t *testing.T) {
	base := func() TaskReviewArgs {
		return TaskReviewArgs{
			Subject:  "diff",
			Findings: []TaskReviewFindingArgs{{FindingID: "f1", Invariant: "i", Category: "functional", Severity: "P2", Disposition: DispositionResolveInline}},
		}
	}

	tests := []struct {
		name    string
		mutate  func(*TaskReviewArgs)
		wantErr string
	}{
		{"missing finding id", func(a *TaskReviewArgs) { a.Findings[0].FindingID = "" }, "finding_id is required"},
		{"missing invariant", func(a *TaskReviewArgs) { a.Findings[0].Invariant = "" }, "invariant is required"},
		{"bad severity", func(a *TaskReviewArgs) { a.Findings[0].Severity = "P9" }, "invalid severity"},
		{"bad category", func(a *TaskReviewArgs) { a.Findings[0].Category = "nonsense" }, "invalid category"},
		{"missing disposition", func(a *TaskReviewArgs) { a.Findings[0].Disposition = "" }, "disposition is required"},
		{"bad disposition", func(a *TaskReviewArgs) { a.Findings[0].Disposition = "maybe" }, "invalid disposition"},
		{"wontfix no reason", func(a *TaskReviewArgs) { a.Findings[0].Disposition = DispositionWontfix }, "requires a reason"},
		{"security wontfix no human", func(a *TaskReviewArgs) {
			a.Findings[0].Category = "security"
			a.Findings[0].Disposition = DispositionWontfix
			a.Findings[0].Reason = "risk accepted"
		}, "human confirmation"},
		{"duplicate id", func(a *TaskReviewArgs) {
			a.Findings = append(a.Findings, TaskReviewFindingArgs{FindingID: "f1", Invariant: "i2", Category: "style", Severity: "P3", Disposition: DispositionResolveInline})
		}, "duplicate finding_id"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state, _ := activeReviewState(t)
			tool := NewTaskReviewTool(state, true, 30*time.Second)
			args := base()
			tc.mutate(&args)
			_, err := executeReview(t, tool, args)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
			if len(state.GetReviews()) != 0 {
				t.Errorf("failed review should not be recorded")
			}
		})
	}
}

func TestTaskReviewToolMetadata(t *testing.T) {
	tool := NewTaskReviewTool(NewTaskState(), true, 30*time.Second)
	if tool.Name() != "task_review" {
		t.Errorf("Name() = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description() should not be empty")
	}
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("Parameters() type = %v", params["type"])
	}
	if _, ok := params["properties"]; !ok {
		t.Error("Parameters() should declare properties")
	}
}

func TestAddReviewNoActiveTask(t *testing.T) {
	state := NewTaskState()
	if err := state.AddReview(ReviewRecord{Subject: "diff"}); err == nil {
		t.Fatal("AddReview should fail with no active task")
	}
}

func TestAddReviewNotifiesOnChange(t *testing.T) {
	state, _ := activeReviewState(t)
	var notified int
	state.SetOnChange(func([]byte) error { notified++; return nil })
	if err := state.AddReview(ReviewRecord{Subject: "diff"}); err != nil {
		t.Fatalf("AddReview: %v", err)
	}
	if notified != 1 {
		t.Errorf("onChange calls = %d, want 1", notified)
	}
}

func TestTaskStateReviewsResetOnClaimAndClear(t *testing.T) {
	state, _ := activeReviewState(t)
	tool := NewTaskReviewTool(state, true, 30*time.Second)
	if _, err := executeReview(t, tool, TaskReviewArgs{Subject: "diff"}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(state.GetReviews()) != 1 {
		t.Fatalf("expected 1 review")
	}

	state.Clear()
	if len(state.GetReviews()) != 0 {
		t.Errorf("reviews should clear with the task")
	}

	state.Set(&Task{ID: "og-r2", Type: "task"})
	if len(state.GetReviews()) != 0 {
		t.Errorf("reviews should be empty for a newly claimed task")
	}
}
