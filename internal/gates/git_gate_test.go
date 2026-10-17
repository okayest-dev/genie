package gates

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/worktree"
)

// fakeWorktreeManager is a scripted worktree.ManagerInterface for git-gate tests.
type fakeWorktreeManager struct {
	state      worktree.WorktreeState
	stateErr   error
	checkIn    worktree.CheckInResult
	checkInErr error

	checkedTask string
	checkedReg  string
	checkInID   string
}

func (f *fakeWorktreeManager) CheckWorktreeState(_ context.Context, taskID, registered string) (worktree.WorktreeState, error) {
	f.checkedTask = taskID
	f.checkedReg = registered
	return f.state, f.stateErr
}

func (f *fakeWorktreeManager) CheckIn(_ context.Context, taskID string) (worktree.CheckInResult, error) {
	f.checkInID = taskID
	return f.checkIn, f.checkInErr
}

func (f *fakeWorktreeManager) Teardown(context.Context, string) error { return nil }

func cleanState() worktree.WorktreeState {
	return worktree.WorktreeState{
		IsTaskWorktree:   true,
		TaskBranchExists: true,
		WorkCommitted:    true,
		NoStrayWorktrees: true,
		DiffConfined:     true,
		TaskBranch:       "task/og-1",
		WorktreePath:     "/repo/.genie/worktrees/og-1",
	}
}

func gitGateEvaluator(mgr worktree.ManagerInterface, enabled []string) *GateEvaluator {
	return NewGateEvaluator(
		NewDefaultGateRegistry(),
		NewInMemoryEvidenceLedger(),
		GateConfig{EnabledGates: enabled},
		NewObservedEvidenceTracker(),
		nil,
		NewInMemoryDeferredFindingStore(),
		mgr,
	)
}

func hasString(list []string, name string) bool {
	for _, s := range list {
		if s == name {
			return true
		}
	}
	return false
}

func failedContains(list []FailedGate, name string) bool {
	for _, f := range list {
		if f.Name == name {
			return true
		}
	}
	return false
}

func TestGitGateRegisteredForCodingTasks(t *testing.T) {
	r := NewDefaultGateRegistry()
	for _, tt := range []TaskType{TaskTypeCoding, TaskTypeFeature, TaskTypeBug} {
		set := r.GetGateSet(tt)
		found := false
		for _, g := range set.Gates {
			if g.Name == "git" {
				found = true
			}
		}
		if !found {
			t.Errorf("git gate missing from %s gate set", tt)
		}
	}
	if _, ok := r.GetGate("git"); !ok {
		t.Error("git gate not registered by name")
	}
}

func TestGitGateSkippedWithoutManager(t *testing.T) {
	e := gitGateEvaluator(nil, nil)
	res, err := e.Run(context.Background(), &Task{ID: "og-1", Type: TaskTypeCoding, Worktree: "/repo/.genie/worktrees/og-1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasString(res.Skipped, "git") {
		t.Errorf("Skipped = %v, want git", res.Skipped)
	}
	if hasString(res.Passed, "git") || failedContains(res.Failed, "git") {
		t.Errorf("git gate should be skipped, got passed=%v failed=%v", res.Passed, res.Failed)
	}
}

func TestGitGateSkippedWithoutAllocatedWorktree(t *testing.T) {
	mgr := &fakeWorktreeManager{state: cleanState()}
	e := gitGateEvaluator(mgr, nil)
	res, err := e.Run(context.Background(), &Task{ID: "og-1", Type: TaskTypeCoding})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasString(res.Skipped, "git") {
		t.Errorf("Skipped = %v, want git", res.Skipped)
	}
	if mgr.checkedTask != "" {
		t.Error("state must not be checked when no worktree is allocated")
	}
}

func TestGitGatePassesAndChecksIn(t *testing.T) {
	mgr := &fakeWorktreeManager{
		state:   cleanState(),
		checkIn: worktree.CheckInResult{TaskID: "og-1", MergeMode: worktree.MergePark, BaseBranch: "main", ParkedBranch: "task/og-1"},
	}
	e := gitGateEvaluator(mgr, nil)
	res, err := e.Run(context.Background(), &Task{ID: "og-1", Type: TaskTypeCoding, Worktree: "/repo/.genie/worktrees/og-1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasString(res.Passed, "git") {
		t.Fatalf("Passed = %v, want git; failed=%v", res.Passed, res.Failed)
	}
	if res.CheckInInfo == nil || res.CheckInInfo.ParkedBranch != "task/og-1" {
		t.Errorf("CheckInInfo = %+v, want parked task/og-1", res.CheckInInfo)
	}
	if mgr.checkedReg != "/repo/.genie/worktrees/og-1" {
		t.Errorf("registered worktree passed = %q", mgr.checkedReg)
	}
	if mgr.checkInID != "og-1" {
		t.Errorf("check-in task = %q, want og-1", mgr.checkInID)
	}
}

func TestGitGateFailsOnStateGaps(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*worktree.WorktreeState)
		want   string
	}{
		{"not task worktree", func(s *worktree.WorktreeState) { s.IsTaskWorktree = false }, "not the task's registered worktree"},
		{"branch missing", func(s *worktree.WorktreeState) { s.TaskBranchExists = false }, "does not exist"},
		{"uncommitted", func(s *worktree.WorktreeState) { s.WorkCommitted = false }, "not committed"},
		{"stray", func(s *worktree.WorktreeState) { s.NoStrayWorktrees = false }, "stray worktrees"},
		{"not confined", func(s *worktree.WorktreeState) { s.DiffConfined = false }, "diff not confined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := cleanState()
			tc.mutate(&state)
			mgr := &fakeWorktreeManager{state: state}
			e := gitGateEvaluator(mgr, nil)
			res, err := e.Run(context.Background(), &Task{ID: "og-1", Type: TaskTypeCoding, Worktree: "/repo/.genie/worktrees/og-1"})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !failedContains(res.Failed, "git") {
				t.Fatalf("Failed = %v, want git", res.Failed)
			}
			if res.CheckInInfo != nil {
				t.Error("check-in must not run on a failed state check")
			}
			if mgr.checkInID != "" {
				t.Error("CheckIn must not be called when state fails")
			}
			gap := ""
			for _, f := range res.Failed {
				if f.Name == "git" {
					gap = f.EvidenceGap
				}
			}
			if !strings.Contains(gap, tc.want) {
				t.Errorf("evidence gap = %q, want substring %q", gap, tc.want)
			}
		})
	}
}

func TestGitGateCheckInError(t *testing.T) {
	mgr := &fakeWorktreeManager{
		state:      cleanState(),
		checkInErr: errors.New("base branch has moved"),
	}
	e := gitGateEvaluator(mgr, nil)
	res, err := e.Run(context.Background(), &Task{ID: "og-1", Type: TaskTypeCoding, Worktree: "/repo/.genie/worktrees/og-1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !failedContains(res.Failed, "git") {
		t.Fatalf("Failed = %v, want git", res.Failed)
	}
	if res.CheckInInfo != nil {
		t.Error("CheckInInfo must be nil when check-in fails")
	}
}

func TestGitGateStateError(t *testing.T) {
	mgr := &fakeWorktreeManager{stateErr: errors.New("git unavailable")}
	e := gitGateEvaluator(mgr, nil)
	res, err := e.Run(context.Background(), &Task{ID: "og-1", Type: TaskTypeCoding, Worktree: "/repo/.genie/worktrees/og-1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !failedContains(res.Failed, "git") {
		t.Fatalf("Failed = %v, want git", res.Failed)
	}
}

func TestGitGateMergedEvidence(t *testing.T) {
	mgr := &fakeWorktreeManager{
		state:   cleanState(),
		checkIn: worktree.CheckInResult{TaskID: "og-1", MergeMode: worktree.MergeAuto, BaseBranch: "main", Merged: true, MergedBranch: "task/og-1", DriftWarning: "base moved"},
	}
	e := gitGateEvaluator(mgr, nil)
	res, err := e.Run(context.Background(), &Task{ID: "og-1", Type: TaskTypeCoding, Worktree: "/repo/.genie/worktrees/og-1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasString(res.Passed, "git") {
		t.Fatalf("Passed = %v, want git", res.Passed)
	}
	if res.CheckInInfo == nil || !res.CheckInInfo.Merged {
		t.Errorf("CheckInInfo = %+v, want merged", res.CheckInInfo)
	}
}
