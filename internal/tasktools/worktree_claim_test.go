package tasktools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/tracker"
	"github.com/okayest-dev/genie/internal/worktree"
)

type fakeAllocator struct {
	path    string
	resumed bool
	err     error
	called  string
}

func (f *fakeAllocator) Allocate(_ context.Context, taskID, trackerID string) (*worktree.Allocation, error) {
	f.called = taskID
	if f.err != nil {
		return nil, f.err
	}
	return &worktree.Allocation{
		TaskID:    taskID,
		TrackerID: trackerID,
		Path:      f.path,
		Branch:    "task/" + trackerID,
		Resumed:   f.resumed,
	}, nil
}

func newClaimFixture(t *testing.T, isolation tracker.Isolation) (*TaskClaimTool, *FakeTracker, *TaskState) {
	t.Helper()
	ft := NewFakeTracker([]*tracker.TrackerTask{{ID: "og-1", Title: "Task One", Type: "task", Status: "open"}})
	ft.SetIsolation(isolation)
	state := NewTaskState()
	tool := NewTaskClaimTool(ft, state, true, 0)
	return tool, ft, state
}

func TestClaimAllocatesWorktreeAndRebinds(t *testing.T) {
	tool, _, state := newClaimFixture(t, tracker.IsolationShared)
	alloc := &fakeAllocator{path: "/repo/.genie/worktrees/og-1"}
	var rebound string
	tool.ConfigureWorktree(worktree.PolicyRequired, alloc, func(root string) error {
		rebound = root
		return nil
	})

	out, err := tool.Execute(json.RawMessage(`{"task_id":"og-1"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if alloc.called != "og-1" {
		t.Errorf("allocator called with %q, want og-1", alloc.called)
	}
	if state.WorktreePath() != "/repo/.genie/worktrees/og-1" {
		t.Errorf("WorktreePath = %q", state.WorktreePath())
	}
	if rebound != "/repo/.genie/worktrees/og-1" {
		t.Errorf("rebind called with %q", rebound)
	}
	if !strings.Contains(out, "/repo/.genie/worktrees/og-1") || !strings.Contains(out, "created") {
		t.Errorf("output missing worktree info: %q", out)
	}
}

func TestClaimResumedWorktreeMessage(t *testing.T) {
	tool, _, _ := newClaimFixture(t, tracker.IsolationShared)
	alloc := &fakeAllocator{path: "/repo/wt", resumed: true}
	tool.ConfigureWorktree(worktree.PolicyRequired, alloc, nil)

	out, err := tool.Execute(json.RawMessage(`{"task_id":"og-1"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "resumed") {
		t.Errorf("expected resumed in output: %q", out)
	}
}

func TestClaimRefusesUnsafeTracker(t *testing.T) {
	tool, _, state := newClaimFixture(t, tracker.IsolationUnknown)
	alloc := &fakeAllocator{path: "/repo/wt"}
	rebound := false
	tool.ConfigureWorktree(worktree.PolicyRequired, alloc, func(string) error { rebound = true; return nil })

	_, err := tool.Execute(json.RawMessage(`{"task_id":"og-1"}`))
	if err == nil {
		t.Fatal("expected refusal for non-worktree-safe tracker")
	}
	if !strings.Contains(err.Error(), "not worktree-safe") {
		t.Errorf("error = %q", err)
	}
	if state.HasActive() {
		t.Error("task must not be claimed when allocation is refused")
	}
	if alloc.called != "" {
		t.Error("allocator must not run when tracker is not worktree-safe")
	}
	if rebound {
		t.Error("rebind must not run when allocation is refused")
	}
}

func TestClaimAllocationFailureLeavesNoActiveTask(t *testing.T) {
	tool, _, state := newClaimFixture(t, tracker.IsolationShared)
	alloc := &fakeAllocator{err: context.DeadlineExceeded}
	tool.ConfigureWorktree(worktree.PolicyRequired, alloc, nil)

	_, err := tool.Execute(json.RawMessage(`{"task_id":"og-1"}`))
	if err == nil {
		t.Fatal("expected allocation failure")
	}
	if state.HasActive() {
		t.Error("task must not be claimed when allocation fails")
	}
}

func TestClaimPolicyOffSkipsWorktree(t *testing.T) {
	tool, _, state := newClaimFixture(t, tracker.IsolationUnknown)
	alloc := &fakeAllocator{path: "/repo/wt"}
	tool.ConfigureWorktree(worktree.PolicyOff, alloc, nil)

	if _, err := tool.Execute(json.RawMessage(`{"task_id":"og-1"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if alloc.called != "" {
		t.Error("allocator must not run when policy=off")
	}
	if state.WorktreePath() != "" {
		t.Errorf("WorktreePath = %q, want empty", state.WorktreePath())
	}
}

func TestClaimRequiredWithoutAllocatorErrors(t *testing.T) {
	tool, _, _ := newClaimFixture(t, tracker.IsolationShared)
	tool.ConfigureWorktree(worktree.PolicyRequired, nil, nil)
	if _, err := tool.Execute(json.RawMessage(`{"task_id":"og-1"}`)); err == nil {
		t.Fatal("expected error with no allocator")
	}
}

func TestConfigureWorktreeAccessors(t *testing.T) {
	tool, _, _ := newClaimFixture(t, tracker.IsolationShared)
	if tool.WorktreePolicy() != "" {
		t.Errorf("WorktreePolicy before configure = %q, want empty", tool.WorktreePolicy())
	}
	if tool.HasWorktreeAllocator() {
		t.Error("HasWorktreeAllocator should be false before configure")
	}

	tool.ConfigureWorktree(worktree.PolicyRequired, &fakeAllocator{}, nil)
	if tool.WorktreePolicy() != worktree.PolicyRequired {
		t.Errorf("WorktreePolicy = %q, want %q", tool.WorktreePolicy(), worktree.PolicyRequired)
	}
	if !tool.HasWorktreeAllocator() {
		t.Error("HasWorktreeAllocator should be true after configure")
	}
}

func TestSetWorktreeNotifiesOnChange(t *testing.T) {
	state := NewTaskState()
	var callbackData []byte
	state.SetOnChange(func(data []byte) error {
		callbackData = data
		return nil
	})
	state.Set(&tracker.TrackerTask{ID: "og-1", Title: "T", Type: "task", Status: "open"})
	callbackData = nil

	state.SetWorktree("/repo/.genie/worktrees/og-1")
	if callbackData == nil {
		t.Fatal("onChange callback should fire on SetWorktree")
	}
	var result map[string]any
	if err := json.Unmarshal(callbackData, &result); err != nil {
		t.Fatalf("callback data not valid JSON: %v", err)
	}
	activeTask, ok := result["active_task"].(map[string]any)
	if !ok {
		t.Fatal("active_task not a map")
	}
	if activeTask["worktree_path"] != "/repo/.genie/worktrees/og-1" {
		t.Errorf("worktree_path = %v", activeTask["worktree_path"])
	}
}

func TestResolveRestoresCwdWhenWorktreeAllocated(t *testing.T) {
	state := NewTaskState()
	ft := NewFakeTracker([]*tracker.TrackerTask{{ID: "og-1", Title: "T", Type: "task", Status: "open"}})
	tool := NewTaskResolveTool(ft, state, NoOpGateRunner{}, true, time.Second)

	state.Set(&tracker.TrackerTask{ID: "og-1", Title: "T", Type: "task", Status: "open"})
	state.SetWorktree("/repo/.genie/worktrees/og-1")
	restored := false
	tool.SetRestoreCwd(func() error { restored = true; return nil })

	if _, err := tool.Execute(json.RawMessage(`{"summary":"done"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !restored {
		t.Error("resolve must restore the session cwd when a worktree was allocated")
	}
	if state.WorktreePath() != "" {
		t.Errorf("worktree path should be cleared, got %q", state.WorktreePath())
	}
}

func TestResolveDoesNotRestoreWithoutWorktree(t *testing.T) {
	state := NewTaskState()
	ft := NewFakeTracker([]*tracker.TrackerTask{{ID: "og-1", Title: "T", Type: "task", Status: "open"}})
	tool := NewTaskResolveTool(ft, state, NoOpGateRunner{}, true, time.Second)

	state.Set(&tracker.TrackerTask{ID: "og-1", Title: "T", Type: "task", Status: "open"})
	restored := false
	tool.SetRestoreCwd(func() error { restored = true; return nil })

	if _, err := tool.Execute(json.RawMessage(`{"summary":"done"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if restored {
		t.Error("resolve must not restore cwd when no worktree was allocated")
	}
}
