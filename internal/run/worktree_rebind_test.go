package run

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/worktree"
)

func readMarker(t *testing.T, h *Handle) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"path": "marker.txt"})
	out, err := h.Registry().Execute("read", raw)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	return out
}

func TestRebindCwdMovesToolsAndRestores(t *testing.T) {
	base := t.TempDir()
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "marker.txt"), []byte("main-checkout"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "marker.txt"), []byte("worktree"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newTestHandle(t, Options{Config: testCfg(t.TempDir()), Cwd: base, Stderr: io.Discard})

	if h.Cwd() != base {
		t.Fatalf("Cwd() = %q, want %q", h.Cwd(), base)
	}
	if got := readMarker(t, h); !strings.Contains(got, "main-checkout") {
		t.Fatalf("before rebind read = %q", got)
	}

	if err := h.RebindCwd(wt); err != nil {
		t.Fatalf("RebindCwd: %v", err)
	}
	if h.Cwd() != wt {
		t.Errorf("Cwd() after rebind = %q, want %q", h.Cwd(), wt)
	}
	if got := readMarker(t, h); !strings.Contains(got, "worktree") {
		t.Errorf("after rebind read = %q, want worktree content", got)
	}
	// AC6 (og-xm7): the main checkout stays readable while the session is
	// rebound into the worktree, but writes there remain confined to the
	// worktree.
	mainRead := filepath.Join(base, "marker.txt")
	if !h.store.Covered(permissions.AxisRead, mainRead) {
		t.Error("original checkout must stay readable while rebound into the worktree")
	}
	if h.store.Covered(permissions.AxisWrite, mainRead) {
		t.Error("writes to the original checkout must remain uncovered while rebound")
	}

	if err := h.RestoreCwd(); err != nil {
		t.Fatalf("RestoreCwd: %v", err)
	}
	if h.Cwd() != base {
		t.Errorf("Cwd() after restore = %q, want %q", h.Cwd(), base)
	}
	if got := readMarker(t, h); !strings.Contains(got, "main-checkout") {
		t.Errorf("after restore read = %q", got)
	}
	if h.store.Covered(permissions.AxisRead, filepath.Join(wt, "marker.txt")) {
		t.Error("worktree should not remain readable after restore (extra read root must be cleared)")
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func worktreeCfg() config.WorktreeConfig {
	return config.WorktreeConfig{
		Policy:       worktree.PolicyRequired,
		Path:         worktree.DefaultPath,
		BranchPrefix: worktree.DefaultBranchPrefix,
		Merge:        worktree.MergePark,
		DriftCheck:   "warn",
	}
}

func TestConfigureWorktreeWiresAllocatorInGitRepo(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)

	cfg := testCfg(t.TempDir())
	cfg.TaskTools.Enable = true
	cfg.Worktree = worktreeCfg()

	h := newTestHandle(t, Options{Config: cfg, Cwd: repo, Stderr: io.Discard})
	if h.claimTool == nil {
		t.Fatal("claim tool should be registered")
	}
	if h.claimTool.WorktreePolicy() != worktree.PolicyRequired {
		t.Errorf("worktreePolicy = %q, want required", h.claimTool.WorktreePolicy())
	}
	if !h.claimTool.HasWorktreeAllocator() {
		t.Error("allocator should be wired inside a git repo")
	}
}

func TestConfigureWorktreeExemptsNonGitDir(t *testing.T) {
	cfg := testCfg(t.TempDir())
	cfg.TaskTools.Enable = true
	cfg.Worktree = worktreeCfg()

	h := newTestHandle(t, Options{Config: cfg, Cwd: t.TempDir(), Stderr: io.Discard})
	if h.claimTool == nil {
		t.Fatal("claim tool should be registered")
	}
	if h.claimTool.HasWorktreeAllocator() {
		t.Error("non-git dir must be exempt: no allocator")
	}
	if h.claimTool.WorktreePolicy() != "" {
		t.Errorf("worktreePolicy = %q, want empty for exempt dir", h.claimTool.WorktreePolicy())
	}
}
