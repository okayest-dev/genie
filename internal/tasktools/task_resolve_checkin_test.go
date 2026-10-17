package tasktools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/gates"
	"github.com/okayest-dev/genie/internal/plugin"
	"github.com/okayest-dev/genie/internal/worktree"
)

type checkInGateRunner struct {
	res gates.GateResult
}

func (c *checkInGateRunner) Run(context.Context, *gates.Task, []gates.DeferredFindingRecord) (gates.GateResult, error) {
	return c.res, nil
}

func TestTaskResolveTeardownAfterClear(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	runner := &checkInGateRunner{res: gates.GateResult{
		Passed: []string{"git"},
		CheckInInfo: &worktree.CheckInResult{
			TaskID:       "og-1",
			MergeMode:    worktree.MergePark,
			BaseBranch:   "main",
			ParkedBranch: "task/og-1",
		},
	}}
	tool := NewTaskResolveTool(tracker, state, runner, true, 30*time.Second)
	state.Set(tasks[0])
	state.SetWorktree("/repo/.genie/worktrees/og-1")

	restored := false
	tool.SetRestoreCwd(func() error { restored = true; return nil })

	var teardownID string
	tool.SetWorktreeTeardown(func(taskID string) error {
		if state.HasActive() {
			t.Error("teardown must run after the active task is cleared")
		}
		teardownID = taskID
		return nil
	})

	result, err := tool.Execute([]byte(`{"summary": "done"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !restored {
		t.Error("restoreCwd was not called")
	}
	if teardownID != "og-1" {
		t.Errorf("teardown task = %q, want og-1", teardownID)
	}
	if !strings.Contains(result, "check_in") {
		t.Errorf("result missing check-in record: %s", result)
	}
}

func TestTaskResolveTeardownFailureBubbles(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tool := NewTaskResolveTool(NewFakeTracker(tasks), state, NoOpGateRunner{}, true, 30*time.Second)
	state.Set(tasks[0])
	state.SetWorktree("/repo/.genie/worktrees/og-1")
	tool.SetWorktreeTeardown(func(string) error { return errTeardown })

	_, err := tool.Execute([]byte(`{"summary": "done"}`))
	if err == nil || !strings.Contains(err.Error(), "failed to teardown worktree") {
		t.Fatalf("Execute err = %v, want teardown failure", err)
	}
}

func TestTaskResolveTeardownSkippedWithoutWorktree(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tool := NewTaskResolveTool(NewFakeTracker(tasks), state, NoOpGateRunner{}, true, 30*time.Second)
	state.Set(tasks[0])

	called := false
	tool.SetWorktreeTeardown(func(string) error { called = true; return nil })

	if _, err := tool.Execute([]byte(`{"summary": "done"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if called {
		t.Error("teardown must not run when no worktree was allocated")
	}
}

func TestBuildResolutionCommentCheckIn(t *testing.T) {
	task := &Task{ID: "og-1", Title: "Task 1", Type: "task"}
	cases := []struct {
		name    string
		ci      *worktree.CheckInResult
		wantSub []string
	}{
		{
			name:    "parked",
			ci:      &worktree.CheckInResult{MergeMode: worktree.MergePark, ParkedBranch: "task/og-1", DriftWarning: "base moved"},
			wantSub: []string{"**Check-in**: park", "parked on task/og-1", "drift: base moved"},
		},
		{
			name:    "merged",
			ci:      &worktree.CheckInResult{MergeMode: worktree.MergeAuto, Merged: true, BaseBranch: "main"},
			wantSub: []string{"**Check-in**: auto", "merged to main"},
		},
		{
			name:    "pushed",
			ci:      &worktree.CheckInResult{MergeMode: worktree.MergePR, PushedBranch: "task/og-1"},
			wantSub: []string{"**Check-in**: pr", "pushed task/og-1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comment := buildResolutionComment(task, "done", GateResult{CheckInInfo: tc.ci})
			for _, want := range tc.wantSub {
				if !strings.Contains(comment, want) {
					t.Errorf("comment missing %q:\n%s", want, comment)
				}
			}
		})
	}
}

var errTeardown = teardownErr{}

type teardownErr struct{}

func (teardownErr) Error() string { return "teardown boom" }
