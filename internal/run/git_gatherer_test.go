package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGitGatherer_NotARepo(t *testing.T) {
	tmpDir := t.TempDir()
	g := newGitGatherer(tmpDir)

	info := g.Gather()
	if info.Valid {
		t.Errorf("expected invalid (not a git repo), got valid: %+v", info)
	}
}

func TestGitGatherer_CleanRepo(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize a git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Configure user
	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmpDir
	cmd.Run()

	// Create a file and commit
	cmd = exec.Command("sh", "-c", "echo hello > file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	cmd.Run()

	g := newGitGatherer(tmpDir)
	info := g.Gather()

	if !info.Valid {
		t.Errorf("expected valid, got invalid")
	}
	if info.Branch != "main" && info.Branch != "master" {
		t.Errorf("expected branch main or master, got %q", info.Branch)
	}
	if info.State != "clean" {
		t.Errorf("expected clean state, got %q", info.State)
	}
}

func TestGitGatherer_DirtyRepo(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize a git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("sh", "-c", "echo hello > file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	cmd.Run()

	// Modify file to make it dirty
	cmd = exec.Command("sh", "-c", "echo world >> file.txt")
	cmd.Dir = tmpDir
	cmd.Run()

	g := newGitGatherer(tmpDir)
	info := g.Gather()

	if !info.Valid {
		t.Errorf("expected valid, got invalid")
	}
	if info.State != "dirty" {
		t.Errorf("expected dirty state, got %q", info.State)
	}
}

func TestGitGatherer_UntrackedFileIsDirty(t *testing.T) {
	tmpDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("sh", "-c", "echo hello > file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	cmd.Run()

	// Add untracked file
	cmd = exec.Command("sh", "-c", "echo untracked > newfile.txt")
	cmd.Dir = tmpDir
	cmd.Run()

	g := newGitGatherer(tmpDir)
	info := g.Gather()

	if !info.Valid {
		t.Errorf("expected valid, got invalid")
	}
	if info.State != "dirty" {
		t.Errorf("expected dirty state (untracked file), got %q", info.State)
	}
}

func TestGitGatherer_DetachedHEAD(t *testing.T) {
	tmpDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("sh", "-c", "echo hello > file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	cmd.Run()

	// Get commit hash and checkout detached HEAD
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("git", "checkout", "--detach")
	cmd.Dir = tmpDir
	cmd.Run()

	g := newGitGatherer(tmpDir)
	info := g.Gather()

	if !info.Valid {
		t.Errorf("expected valid, got invalid")
	}
	if info.Branch != "HEAD" {
		t.Errorf("expected HEAD for detached, got %q", info.Branch)
	}
	if info.State != "clean" {
		t.Errorf("expected clean state, got %q", info.State)
	}
}

func TestGitGatherer_BranchNameStripsAheadBehind(t *testing.T) {
	tmpDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("sh", "-c", "echo hello > file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	cmd.Run()

	// Create a branch with upstream tracking
	cmd = exec.Command("git", "checkout", "-b", "feature")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "branch", "--set-upstream-to=main", "feature")
	cmd.Dir = tmpDir
	cmd.Run()

	g := newGitGatherer(tmpDir)
	info := g.Gather()

	if !info.Valid {
		t.Errorf("expected valid, got invalid")
	}
	// Branch name should not contain ahead/behind suffixes
	if info.Branch != "feature" {
		t.Errorf("expected branch 'feature', got %q", info.Branch)
	}
}

func TestGitGatherer_CheapPath(t *testing.T) {
	tmpDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("sh", "-c", "echo hello > file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	cmd.Run()

	g := newGitGatherer(tmpDir)
	branch, ok := g.gatherCheap()

	if !ok {
		t.Errorf("expected cheap path to succeed")
	}
	if branch != "main" && branch != "master" {
		t.Errorf("expected main or master, got %q", branch)
	}
}

func TestGitGatherer_DegradedRecovery(t *testing.T) {
	tmpDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("sh", "-c", "echo hello > file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	cmd.Run()

	g := newGitGatherer(tmpDir)

	// First, force degraded mode by setting a very short timeout
	// We can't easily test the timeout path without modifying the constant,
	// so we'll test the recovery logic directly by manipulating internal state
	g.mu.Lock()
	g.degraded = true
	g.lastTurnCount = 0
	g.mu.Unlock()

	// First gather after degradation - should use cheap path
	info := g.Gather()
	if !info.Valid {
		t.Errorf("expected valid in degraded mode")
	}
	if info.State != "unknown" {
		t.Errorf("expected unknown state in degraded mode, got %q", info.State)
	}

	// Notify turn completed
	g.NotifyTurnCompleted()

	// Next gather should exit degraded mode
	info = g.Gather()
	if !info.Valid {
		t.Errorf("expected valid after recovery")
	}
	if info.State != "clean" {
		t.Errorf("expected clean state after recovery, got %q", info.State)
	}
}

func TestGitGatherer_CheapPathFailsOnDetachedHEAD(t *testing.T) {
	tmpDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("sh", "-c", "echo hello > file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	cmd.Run()

	// Detach HEAD
	cmd = exec.Command("git", "checkout", "--detach")
	cmd.Dir = tmpDir
	cmd.Run()

	g := newGitGatherer(tmpDir)
	branch, ok := g.gatherCheap()

	if ok {
		t.Errorf("expected cheap path to fail on detached HEAD, got %q", branch)
	}
}

func TestParseGitStatus(t *testing.T) {
	g := newGitGatherer("/tmp")

	// Test clean repo output
	output := "## main\n"
	info := g.parseGitStatus(output)
	if !info.Valid {
		t.Errorf("expected valid")
	}
	if info.Branch != "main" {
		t.Errorf("expected main, got %q", info.Branch)
	}
	if info.State != "clean" {
		t.Errorf("expected clean, got %q", info.State)
	}

	// Test dirty repo output
	output = "## main\n M file.txt\n"
	info = g.parseGitStatus(output)
	if !info.Valid {
		t.Errorf("expected valid")
	}
	if info.State != "dirty" {
		t.Errorf("expected dirty, got %q", info.State)
	}

	// Test with ahead/behind
	output = "## feature...origin/feature [ahead 2] [behind 1]\n"
	info = g.parseGitStatus(output)
	if !info.Valid {
		t.Errorf("expected valid")
	}
	if info.Branch != "feature" {
		t.Errorf("expected feature, got %q", info.Branch)
	}

	// Test with remote only
	output = "## main...origin/main\n"
	info = g.parseGitStatus(output)
	if !info.Valid {
		t.Errorf("expected valid")
	}
	if info.Branch != "main" {
		t.Errorf("expected main, got %q", info.Branch)
	}

	// Test detached HEAD
	output = "## HEAD (no branch)\n"
	info = g.parseGitStatus(output)
	if !info.Valid {
		t.Errorf("expected valid")
	}
	if info.Branch != "HEAD" {
		t.Errorf("expected HEAD, got %q", info.Branch)
	}
}

func TestStripAheadBehind(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"main", "main"},
		{"feature [ahead 2]", "feature"},
		{"feature [behind 1]", "feature"},
		{"feature [ahead 2] [behind 1]", "feature"},
		{"main [ahead 5] [behind 3]", "main"},
		{"feature...origin/feature [ahead 2]", "feature...origin/feature"},
	}

	for _, tt := range tests {
		result := stripAheadBehind(tt.input)
		if result != tt.expected {
			t.Errorf("stripAheadBehind(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// Test that core.untrackedFiles=no doesn't affect dirty detection
func TestGitGatherer_UntrackedFilesConfig(t *testing.T) {
	tmpDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmpDir
	cmd.Run()

	// Set core.untrackedFiles=no
	cmd = exec.Command("git", "config", "core.untrackedFiles", "no")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("sh", "-c", "echo hello > file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	cmd.Run()

	// Add untracked file
	cmd = exec.Command("sh", "-c", "echo untracked > newfile.txt")
	cmd.Dir = tmpDir
	cmd.Run()

	g := newGitGatherer(tmpDir)
	info := g.Gather()

	if !info.Valid {
		t.Errorf("expected valid, got invalid")
	}
	// Even with core.untrackedFiles=no, --untracked-files=all should detect the file
	if info.State != "dirty" {
		t.Errorf("expected dirty state (untracked file with core.untrackedFiles=no), got %q", info.State)
	}
}

// Test that the gather timeout is respected (integration-style test)
func TestGitGatherer_TimeoutIsFixed(t *testing.T) {
	// The gatherTimeout constant should be ~150ms
	if gatherTimeout != 150*time.Millisecond {
		t.Errorf("gatherTimeout = %v, want 150ms", gatherTimeout)
	}
}

// Test that the timeout path is triggered when git status takes too long
func TestGitGatherer_TimeoutPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a slow git wrapper script
	wrapperPath := filepath.Join(tmpDir, "git")
	wrapperContent := `#!/bin/sh
if [ "$1" = "status" ]; then
    sleep 1
    exec /usr/bin/git "$@"
else
    exec /usr/bin/git "$@"
fi
`
	if err := os.WriteFile(wrapperPath, []byte(wrapperContent), 0755); err != nil {
		t.Fatalf("failed to write wrapper: %v", err)
	}

	// Initialize a git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("sh", "-c", "echo hello > file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	cmd.Run()

	// Use the slow wrapper by prepending its directory to PATH
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", tmpDir+":"+oldPath)
	defer os.Setenv("PATH", oldPath)

	g := newGitGatherer(tmpDir)
	info := g.Gather()

	// Should enter degraded mode and return unknown state via cheap path
	if !info.Valid {
		t.Errorf("expected valid (cheap path should succeed), got invalid")
	}
	if info.State != "unknown" {
		t.Errorf("expected unknown state (degraded mode), got %q", info.State)
	}
	if info.Branch != "main" && info.Branch != "master" {
		t.Errorf("expected main or master from cheap path, got %q", info.Branch)
	}
}

// Test that degraded mode cheap path failure collapses the segment
func TestGitGatherer_DegradedCheapPathFailure(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize a git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("sh", "-c", "echo hello > file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	cmd.Run()

	// Detach HEAD to make symbolic-ref fail
	cmd = exec.Command("git", "checkout", "--detach")
	cmd.Dir = tmpDir
	cmd.Run()

	g := newGitGatherer(tmpDir)

	// Force degraded mode
	g.MuLock()
	g.degraded = true
	g.MuUnlock()

	// Gather should fail because cheap path fails on detached HEAD
	info := g.Gather()
	if info.Valid {
		t.Errorf("expected invalid (cheap path fails on detached HEAD), got valid")
	}
}

// Test that timeout followed by cheap path failure collapses the segment
func TestGitGatherer_TimeoutThenCheapPathFailure(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a slow git wrapper script that also fails symbolic-ref
	wrapperPath := filepath.Join(tmpDir, "git")
	wrapperContent := `#!/bin/sh
if [ "$1" = "status" ]; then
    sleep 1
    exec /usr/bin/git "$@"
elif [ "$1" = "symbolic-ref" ]; then
    exit 128
else
    exec /usr/bin/git "$@"
fi
`
	if err := os.WriteFile(wrapperPath, []byte(wrapperContent), 0755); err != nil {
		t.Fatalf("failed to write wrapper: %v", err)
	}

	// Initialize a git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("sh", "-c", "echo hello > file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = tmpDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	cmd.Run()

	// Use the slow wrapper by prepending its directory to PATH
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", tmpDir+":"+oldPath)
	defer os.Setenv("PATH", oldPath)

	g := newGitGatherer(tmpDir)
	info := g.Gather()

	// Should enter degraded mode, then cheap path fails, segment collapses
	if info.Valid {
		t.Errorf("expected invalid (timeout then cheap path fails), got valid")
	}
}

// Test that we can create a git repo in a subdirectory
func TestGitGatherer_Subdirectory(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	cmd := exec.Command("git", "init")
	cmd.Dir = subDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = subDir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = subDir
	cmd.Run()

	cmd = exec.Command("sh", "-c", "echo hello > file.txt")
	cmd.Dir = subDir
	cmd.Run()
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = subDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = subDir
	cmd.Run()

	// Gather from parent directory (should not find repo)
	g := newGitGatherer(tmpDir)
	info := g.Gather()
	if info.Valid {
		t.Errorf("expected invalid from parent dir, got valid")
	}

	// Gather from subdirectory (should find repo)
	g = newGitGatherer(subDir)
	info = g.Gather()
	if !info.Valid {
		t.Errorf("expected valid from subdir, got invalid")
	}
	if info.Branch != "main" && info.Branch != "master" {
		t.Errorf("expected main or master, got %q", info.Branch)
	}
}