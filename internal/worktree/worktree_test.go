package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.Policy != PolicyOff {
		t.Errorf("Policy = %q, want %q", c.Policy, PolicyOff)
	}
	if c.Path != DefaultPath {
		t.Errorf("Path = %q, want %q", c.Path, DefaultPath)
	}
	if c.BranchPrefix != DefaultBranchPrefix {
		t.Errorf("BranchPrefix = %q, want %q", c.BranchPrefix, DefaultBranchPrefix)
	}
	if c.Merge != MergePark {
		t.Errorf("Merge = %q, want %q", c.Merge, MergePark)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("default config must validate: %v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"off-ok", Config{Policy: PolicyOff}, false},
		{"empty-policy-ok", Config{}, false},
		{"optional-ok", Config{Policy: PolicyOptional, Path: "x", BranchPrefix: "task/"}, false},
		{"required-ok", Config{Policy: PolicyRequired, Path: "x", BranchPrefix: "task/"}, false},
		{"bad-policy", Config{Policy: "sometimes"}, true},
		{"bad-merge", Config{Policy: PolicyOff, Merge: "rebase"}, true},
		{"bad-drift", Config{Policy: PolicyOff, DriftCheck: "explode"}, true},
		{"required-empty-path", Config{Policy: PolicyRequired, Path: " ", BranchPrefix: "task/"}, true},
		{"required-empty-prefix", Config{Policy: PolicyRequired, Path: "x", BranchPrefix: ""}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestManagerPathAndBranch(t *testing.T) {
	m := newManager("/repo", Config{Path: ".genie/worktrees", BranchPrefix: "task/"}, &fakeRunner{root: "/repo"})
	if got := m.Path("og-1"); got != filepath.Join("/repo", ".genie/worktrees", "og-1") {
		t.Errorf("Path() = %q", got)
	}
	if got := m.Branch("og-1"); got != "task/og-1" {
		t.Errorf("Branch() = %q", got)
	}

	// A prefix without a trailing slash is normalized.
	m2 := newManager("/repo", Config{Path: "wt", BranchPrefix: "task"}, &fakeRunner{root: "/repo"})
	if got := m2.Branch("og-2"); got != "task/og-2" {
		t.Errorf("Branch() with bare prefix = %q", got)
	}

	// An absolute path root is honoured.
	m3 := newManager("/repo", Config{Path: "/tmp/wts", BranchPrefix: "task/"}, &fakeRunner{root: "/repo"})
	if got := m3.Path("og-3"); got != filepath.Join("/tmp/wts", "og-3") {
		t.Errorf("Path() absolute = %q", got)
	}
}

func TestAllocateFresh(t *testing.T) {
	root := t.TempDir()
	r := &fakeRunner{root: root}
	m := newManager(root, Config{Policy: PolicyRequired, Path: ".genie/worktrees", BranchPrefix: "task/"}, r)

	alloc, err := m.Allocate(context.Background(), "og-1", "og-1")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if alloc.Resumed {
		t.Error("fresh allocation must not be Resumed")
	}
	if alloc.Branch != "task/og-1" {
		t.Errorf("Branch = %q", alloc.Branch)
	}
	if alloc.Path != filepath.Join(root, ".genie/worktrees", "og-1") {
		t.Errorf("Path = %q", alloc.Path)
	}
	if !r.sawCall("worktree", "add", alloc.Path, "-b", "task/og-1") {
		t.Errorf("expected fresh worktree add, calls: %v", r.calls)
	}
	// The worktree root must have been excluded.
	data, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("read exclude: %v", err)
	}
	if !strings.Contains(string(data), "/.genie/worktrees/") {
		t.Errorf("exclude missing worktree root: %q", data)
	}
}

func TestAllocateResumesRegisteredWorktree(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, ".genie", "worktrees", "og-1")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{root: root, worktrees: []string{wt}}
	m := newManager(root, Config{Policy: PolicyRequired, Path: ".genie/worktrees", BranchPrefix: "task/"}, r)

	alloc, err := m.Allocate(context.Background(), "og-1", "og-1")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if !alloc.Resumed {
		t.Error("registered worktree must resume")
	}
	if r.sawCall("worktree", "add", alloc.Path, "-b", "task/og-1") {
		t.Error("resume must not run git worktree add")
	}
}

func TestAllocateResumesExistingBranch(t *testing.T) {
	root := t.TempDir()
	r := &fakeRunner{root: root, branches: map[string]bool{"task/og-1": true}}
	m := newManager(root, Config{Policy: PolicyRequired, Path: ".genie/worktrees", BranchPrefix: "task/"}, r)

	alloc, err := m.Allocate(context.Background(), "og-1", "og-1")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if !alloc.Resumed {
		t.Error("existing branch must resume")
	}
	if r.sawCall("worktree", "add", alloc.Path, "-b", "task/og-1") {
		t.Error("existing branch must be attached, not recreated with -b")
	}
	if !r.sawCall("worktree", "add", alloc.Path, "task/og-1") {
		t.Errorf("expected attach of existing branch, calls: %v", r.calls)
	}
}

func TestAllocatePathConflict(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, ".genie", "worktrees", "og-1")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{root: root}
	m := newManager(root, Config{Policy: PolicyRequired, Path: ".genie/worktrees", BranchPrefix: "task/"}, r)

	if _, err := m.Allocate(context.Background(), "og-1", "og-1"); err == nil {
		t.Fatal("expected error for path occupied by a non-worktree")
	}
}

func TestAllocateFailureBubbles(t *testing.T) {
	root := t.TempDir()
	r := &fakeRunner{root: root, addErr: true}
	m := newManager(root, Config{Policy: PolicyRequired, Path: ".genie/worktrees", BranchPrefix: "task/"}, r)
	if _, err := m.Allocate(context.Background(), "og-1", "og-1"); err == nil {
		t.Fatal("expected git failure to bubble")
	}
}

func TestRemove(t *testing.T) {
	root := t.TempDir()
	r := &fakeRunner{root: root}
	m := newManager(root, Config{Path: ".genie/worktrees", BranchPrefix: "task/"}, r)
	wt := m.Path("og-1")
	if err := m.Remove(context.Background(), "og-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !r.sawCall("worktree", "remove", "--force", wt) {
		t.Errorf("expected worktree remove, calls: %v", r.calls)
	}
}

// TestAllocateIntegration exercises the real git binary end to end.
func TestAllocateIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")

	m := New(dir, Config{Policy: PolicyRequired, Path: DefaultPath, BranchPrefix: DefaultBranchPrefix})
	ctx := context.Background()

	alloc, err := m.Allocate(ctx, "og-1", "og-1")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if alloc.Resumed {
		t.Error("first allocation should not resume")
	}
	if _, err := os.Stat(filepath.Join(alloc.Path, "README.md")); err != nil {
		t.Errorf("worktree does not contain checkout: %v", err)
	}
	// The branch exists.
	if out := runGit(t, dir, "branch", "--list", "task/og-1"); !strings.Contains(out, "task/og-1") {
		t.Errorf("branch not created: %q", out)
	}
	// Re-allocation resumes.
	again, err := m.Allocate(ctx, "og-1", "og-1")
	if err != nil {
		t.Fatalf("re-Allocate: %v", err)
	}
	if !again.Resumed {
		t.Error("second allocation should resume the existing worktree")
	}
	// Remove leaves the branch and clears the worktree.
	if err := m.Remove(ctx, "og-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if out := runGit(t, dir, "branch", "--list", "task/og-1"); !strings.Contains(out, "task/og-1") {
		t.Errorf("branch should survive teardown: %q", out)
	}
}

func TestManagerAccessors(t *testing.T) {
	m := newManager("/repo", Config{Path: "wt", BranchPrefix: "task/"}, &fakeRunner{root: "/repo"})
	if m.RepoRoot() != "/repo" {
		t.Errorf("RepoRoot() = %q", m.RepoRoot())
	}
	if m.Config().BranchPrefix != "task/" {
		t.Errorf("Config().BranchPrefix = %q", m.Config().BranchPrefix)
	}
	// Empty path/prefix fall back to defaults at construction.
	m2 := newManager("/repo", Config{}, &fakeRunner{root: "/repo"})
	if m2.Config().Path != DefaultPath || m2.Config().BranchPrefix != DefaultBranchPrefix {
		t.Errorf("defaults not applied: %+v", m2.Config())
	}
}

func TestEnsureExcludedIdempotent(t *testing.T) {
	root := t.TempDir()
	excludeDir := filepath.Join(root, ".git", "info")
	if err := os.MkdirAll(excludeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(excludeDir, "exclude")
	if err := os.WriteFile(exclude, []byte("/.genie/worktrees/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newManager(root, Config{Path: ".genie/worktrees", BranchPrefix: "task/"}, &fakeRunner{root: root})
	if _, err := m.Allocate(context.Background(), "og-1", "og-1"); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	data, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "/.genie/worktrees/"); n != 1 {
		t.Errorf("exclude entry count = %d, want 1: %q", n, data)
	}
}

func TestDetectRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	root, ok, err := DetectRoot(context.Background(), dir)
	if err != nil {
		t.Fatalf("DetectRoot: %v", err)
	}
	if !ok {
		t.Fatal("expected dir to be a git repo")
	}
	if filepath.Clean(root) != filepath.Clean(dir) {
		t.Errorf("root = %q, want %q", root, dir)
	}

	nonRepo := t.TempDir()
	if _, ok, err := DetectRoot(context.Background(), nonRepo); err != nil || ok {
		t.Errorf("non-repo DetectRoot = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
}

func TestExecRunner(t *testing.T) {
	dir := t.TempDir()
	out, err := (execRunner{}).Run(context.Background(), dir, "version")
	if err != nil {
		t.Fatalf("git version: %v", err)
	}
	if !strings.Contains(out, "git version") {
		t.Errorf("unexpected output: %q", out)
	}
	if _, err := (execRunner{}).Run(context.Background(), dir, "definitely-not-a-cmd"); err == nil {
		t.Error("expected error for invalid git subcommand")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

type fakeRunner struct {
	root      string
	worktrees []string
	branches  map[string]bool
	addErr    bool
	calls     [][]string
}

func (f *fakeRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	switch {
	case equalArgs(args, "rev-parse", "--show-toplevel"):
		return f.root + "\n", nil
	case equalArgs(args, "rev-parse", "--git-common-dir"):
		return ".git\n", nil
	case equalArgs(args, "worktree", "list", "--porcelain"):
		var b strings.Builder
		for _, w := range f.worktrees {
			b.WriteString("worktree " + w + "\n")
		}
		return b.String(), nil
	case len(args) == 4 && equalArgs(args[:3], "rev-parse", "--verify", "--quiet"):
		branch := strings.TrimPrefix(args[3], "refs/heads/")
		if f.branches[branch] {
			return "deadbeef\n", nil
		}
		return "", errNotFound
	case len(args) >= 2 && args[0] == "worktree" && args[1] == "add":
		if f.addErr {
			return "", errGitFail
		}
		return "", nil
	case len(args) >= 2 && args[0] == "worktree" && args[1] == "remove":
		return "", nil
	}
	return "", errUnexpected
}

func (f *fakeRunner) sawCall(want ...string) bool {
	for _, c := range f.calls {
		if equalArgs(c, want...) {
			return true
		}
	}
	return false
}

func equalArgs(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

var (
	errNotFound   = &fakeErr{"not found"}
	errGitFail    = &fakeErr{"git failed"}
	errUnexpected = &fakeErr{"unexpected git invocation"}
)

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }
