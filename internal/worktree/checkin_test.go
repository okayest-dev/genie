package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func resolveCfg(mode string) Config {
	c := DefaultConfig()
	c.Policy = PolicyRequired
	c.Path = DefaultPath
	c.BranchPrefix = DefaultBranchPrefix
	c.Merge = mode
	return c
}

func TestCheckWorktreeStateClean(t *testing.T) {
	r := &fakeRunner{
		root:      "/repo",
		worktrees: []string{"/repo/.genie/worktrees/og-1"},
		branches:  map[string]bool{"task/og-1": true},
		head:      "task/og-1",
		commit:    "abc123",
	}
	m := newManager("/repo", resolveCfg(MergePark), r)

	state, err := m.CheckWorktreeState(context.Background(), "og-1", "/repo/.genie/worktrees/og-1")
	if err != nil {
		t.Fatalf("CheckWorktreeState: %v", err)
	}
	if !state.IsTaskWorktree {
		t.Error("IsTaskWorktree = false, want true")
	}
	if !state.TaskBranchExists {
		t.Error("TaskBranchExists = false, want true")
	}
	if !state.WorkCommitted {
		t.Error("WorkCommitted = false, want true")
	}
	if !state.NoStrayWorktrees {
		t.Error("NoStrayWorktrees = false, want true")
	}
	if !state.DiffConfined {
		t.Error("DiffConfined = false, want true")
	}
	if state.CurrentCommit != "abc123" {
		t.Errorf("CurrentCommit = %q, want abc123", state.CurrentCommit)
	}
}

func TestCheckWorktreeStateAllowsSiblingWorktrees(t *testing.T) {
	r := &fakeRunner{
		root:      "/repo",
		worktrees: []string{"/repo/.genie/worktrees/og-1", "/repo/.genie/worktrees/og-2"},
		branches:  map[string]bool{"task/og-1": true},
		head:      "task/og-1",
	}
	m := newManager("/repo", resolveCfg(MergePark), r)
	state, err := m.CheckWorktreeState(context.Background(), "og-1", "/repo/.genie/worktrees/og-1")
	if err != nil {
		t.Fatalf("CheckWorktreeState: %v", err)
	}
	if !state.NoStrayWorktrees {
		t.Error("sibling worktrees under the worktree root must not count as stray")
	}
}

func TestCheckWorktreeStateFailures(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*fakeRunner)
		reg     string
		wantErr string
	}{
		{
			name:    "session not in worktree",
			reg:     "",
			wantErr: "IsTaskWorktree",
		},
		{
			name:    "registered path mismatch",
			reg:     "/somewhere/else",
			mutate:  func(*fakeRunner) {},
			wantErr: "IsTaskWorktree",
		},
		{
			name: "path not registered",
			reg:  "/repo/.genie/worktrees/og-1",
			mutate: func(r *fakeRunner) {
				r.worktrees = nil
			},
			wantErr: "IsTaskWorktree",
		},
		{
			name: "branch missing",
			reg:  "/repo/.genie/worktrees/og-1",
			mutate: func(r *fakeRunner) {
				r.branches = nil
			},
			wantErr: "TaskBranchExists",
		},
		{
			name: "work uncommitted",
			reg:  "/repo/.genie/worktrees/og-1",
			mutate: func(r *fakeRunner) {
				r.status = " M file.go\n"
			},
			wantErr: "WorkCommitted",
		},
		{
			name: "head not on task branch",
			reg:  "/repo/.genie/worktrees/og-1",
			mutate: func(r *fakeRunner) {
				r.head = "main"
			},
			wantErr: "DiffConfined",
		},
		{
			name: "stray worktree",
			reg:  "/repo/.genie/worktrees/og-1",
			mutate: func(r *fakeRunner) {
				r.worktrees = append(r.worktrees, "/tmp/loose-worktree")
			},
			wantErr: "NoStrayWorktrees",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{
				root:      "/repo",
				worktrees: []string{"/repo/.genie/worktrees/og-1"},
				branches:  map[string]bool{"task/og-1": true},
				head:      "task/og-1",
			}
			if tc.mutate != nil {
				tc.mutate(r)
			}
			m := newManager("/repo", resolveCfg(MergePark), r)
			state, err := m.CheckWorktreeState(context.Background(), "og-1", tc.reg)
			if err != nil {
				t.Fatalf("CheckWorktreeState: %v", err)
			}
			assertStateFieldFalse(t, tc.wantErr, state)
		})
	}
}

func assertStateFieldFalse(t *testing.T, field string, s WorktreeState) {
	t.Helper()
	failed := false
	switch field {
	case "IsTaskWorktree":
		failed = !s.IsTaskWorktree
	case "TaskBranchExists":
		failed = !s.TaskBranchExists
	case "WorkCommitted":
		failed = !s.WorkCommitted
	case "DiffConfined":
		failed = !s.DiffConfined
	case "NoStrayWorktrees":
		failed = !s.NoStrayWorktrees
	default:
		t.Fatalf("unknown field %q", field)
	}
	if !failed {
		t.Errorf("%s = true, want false", field)
	}
}

func TestCheckInPark(t *testing.T) {
	r := &fakeRunner{
		root:     "/repo",
		branches: map[string]bool{"task/og-1": true, "main": true},
	}
	m := newManager("/repo", resolveCfg(MergePark), r)

	res, err := m.CheckIn(context.Background(), "og-1")
	if err != nil {
		t.Fatalf("CheckIn: %v", err)
	}
	if res.Merged {
		t.Error("park must not merge")
	}
	if res.ParkedBranch != "task/og-1" {
		t.Errorf("ParkedBranch = %q, want task/og-1", res.ParkedBranch)
	}
	if res.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want main", res.BaseBranch)
	}
}

func TestCheckInAutoMerges(t *testing.T) {
	r := &fakeRunner{
		root:     "/repo",
		branches: map[string]bool{"task/og-1": true, "main": true},
	}
	cfg := resolveCfg(MergeAuto)
	cfg.BaseBranch = "main"
	m := newManager("/repo", cfg, r)

	res, err := m.CheckIn(context.Background(), "og-1")
	if err != nil {
		t.Fatalf("CheckIn: %v", err)
	}
	if !res.Merged || res.MergedBranch != "task/og-1" {
		t.Errorf("auto merge result = %+v, want merged task/og-1", res)
	}
	if !r.sawCall("checkout", "main") {
		t.Errorf("expected checkout main, calls: %v", r.calls)
	}
}

func TestCheckInAutoRefusesDirtyCheckout(t *testing.T) {
	r := &fakeRunner{
		root:     "/repo",
		branches: map[string]bool{"task/og-1": true, "main": true},
		status:   " M dirty.go\n",
	}
	cfg := resolveCfg(MergeAuto)
	cfg.BaseBranch = "main"
	m := newManager("/repo", cfg, r)

	_, err := m.CheckIn(context.Background(), "og-1")
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("CheckIn err = %v, want uncommitted-changes refusal", err)
	}
}

func TestCheckInAutoMergeFailure(t *testing.T) {
	r := &fakeRunner{
		root:     "/repo",
		branches: map[string]bool{"task/og-1": true, "main": true},
		mergeErr: true,
	}
	cfg := resolveCfg(MergeAuto)
	cfg.BaseBranch = "main"
	m := newManager("/repo", cfg, r)

	_, err := m.CheckIn(context.Background(), "og-1")
	if err == nil || !strings.Contains(err.Error(), "merge") {
		t.Fatalf("CheckIn err = %v, want merge failure", err)
	}
}

func TestCheckInPR(t *testing.T) {
	r := &fakeRunner{
		root:     "/repo",
		branches: map[string]bool{"task/og-1": true, "main": true},
	}
	m := newManager("/repo", resolveCfg(MergePR), r)

	res, err := m.CheckIn(context.Background(), "og-1")
	if err != nil {
		t.Fatalf("CheckIn: %v", err)
	}
	if res.PushedBranch != "task/og-1" {
		t.Errorf("PushedBranch = %q, want task/og-1", res.PushedBranch)
	}
	if !r.sawCall("push", "origin", "task/og-1") {
		t.Errorf("expected push, calls: %v", r.calls)
	}
}

func TestCheckInPRPushFailure(t *testing.T) {
	r := &fakeRunner{
		root:     "/repo",
		branches: map[string]bool{"task/og-1": true, "main": true},
		pushErr:  true,
	}
	m := newManager("/repo", resolveCfg(MergePR), r)
	if _, err := m.CheckIn(context.Background(), "og-1"); err == nil {
		t.Fatal("expected push failure to bubble")
	}
}

func TestBaseBranchResolution(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		originHead string
		branches   map[string]bool
		want       string
	}{
		{"configured wins", "release", "refs/remotes/origin/trunk", map[string]bool{"main": true}, "release"},
		{"origin head", "", "refs/remotes/origin/trunk", map[string]bool{"main": true}, "trunk"},
		{"main fallback", "", "", map[string]bool{"main": true}, "main"},
		{"master fallback", "", "", map[string]bool{"master": true}, "master"},
		{"none", "", "", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{root: "/repo", originHead: tc.originHead, branches: tc.branches}
			cfg := resolveCfg(MergePark)
			cfg.BaseBranch = tc.configured
			m := newManager("/repo", cfg, r)
			got, err := m.baseBranch(context.Background(), cfg.BaseBranch)
			if err != nil {
				t.Fatalf("baseBranch: %v", err)
			}
			if got != tc.want {
				t.Errorf("baseBranch = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckInDriftWarn(t *testing.T) {
	r := &fakeRunner{
		root:     "/repo",
		branches: map[string]bool{"task/og-1": true, "main": true},
		drift:    "3\t1\n",
	}
	cfg := resolveCfg(MergePark)
	cfg.DriftCheck = "warn"
	m := newManager("/repo", cfg, r)

	res, err := m.CheckIn(context.Background(), "og-1")
	if err != nil {
		t.Fatalf("CheckIn: %v", err)
	}
	if res.DriftWarning == "" {
		t.Error("expected drift warning")
	}
}

func TestCheckInDriftBlock(t *testing.T) {
	r := &fakeRunner{
		root:     "/repo",
		branches: map[string]bool{"task/og-1": true, "main": true},
		drift:    "3\t1\n",
	}
	cfg := resolveCfg(MergePark)
	cfg.DriftCheck = "block"
	m := newManager("/repo", cfg, r)

	_, err := m.CheckIn(context.Background(), "og-1")
	if err == nil || !strings.Contains(err.Error(), "has moved") {
		t.Fatalf("CheckIn err = %v, want drift block", err)
	}
}

func TestTeardownRemovesWorktree(t *testing.T) {
	r := &fakeRunner{root: "/repo"}
	m := newManager("/repo", resolveCfg(MergePark), r)
	if err := m.Teardown(context.Background(), "og-1"); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if !r.sawCall("worktree", "remove", "--force", m.Path("og-1")) {
		t.Errorf("expected worktree remove, calls: %v", r.calls)
	}
}

// TestCheckInIntegration exercises allocation, state verification, check-in and
// teardown against the real git binary.
func TestCheckInIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := initRepo(t)

	ctx := context.Background()
	park := newManager(root, Config{Policy: PolicyRequired, Path: DefaultPath, BranchPrefix: DefaultBranchPrefix, Merge: MergePark, DriftCheck: "warn"}, execRunner{})
	alloc, err := park.Allocate(ctx, "og-1", "og-1")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	// Commit the task's work inside the worktree.
	if err := os.WriteFile(filepath.Join(alloc.Path, "work.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, alloc.Path, "add", ".")
	runGit(t, alloc.Path, "commit", "-q", "-m", "task work")

	state, err := park.CheckWorktreeState(ctx, "og-1", alloc.Path)
	if err != nil {
		t.Fatalf("CheckWorktreeState: %v", err)
	}
	if !state.IsTaskWorktree || !state.WorkCommitted || !state.DiffConfined || !state.NoStrayWorktrees {
		t.Fatalf("state not clean: %+v", state)
	}

	res, err := park.CheckIn(ctx, "og-1")
	if err != nil {
		t.Fatalf("CheckIn park: %v", err)
	}
	if res.ParkedBranch != "task/og-1" {
		t.Errorf("ParkedBranch = %q", res.ParkedBranch)
	}

	if err := park.Teardown(ctx, "og-1"); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if out := runGit(t, root, "branch", "--list", "task/og-1"); !strings.Contains(out, "task/og-1") {
		t.Errorf("branch should survive teardown: %q", out)
	}
}

func TestCheckInAutoIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := initRepo(t)

	ctx := context.Background()
	auto := newManager(root, Config{Policy: PolicyRequired, Path: DefaultPath, BranchPrefix: DefaultBranchPrefix, Merge: MergeAuto, BaseBranch: "main"}, execRunner{})
	alloc, err := auto.Allocate(ctx, "og-2", "og-2")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(alloc.Path, "auto.txt"), []byte("merged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, alloc.Path, "add", ".")
	runGit(t, alloc.Path, "commit", "-q", "-m", "auto work")

	res, err := auto.CheckIn(ctx, "og-2")
	if err != nil {
		t.Fatalf("CheckIn auto: %v", err)
	}
	if !res.Merged {
		t.Fatalf("expected auto merge, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "auto.txt")); err != nil {
		t.Errorf("merged file missing from main checkout: %v", err)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}
