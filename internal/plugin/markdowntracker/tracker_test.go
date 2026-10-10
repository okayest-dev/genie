package markdowntracker

import (
	"path/filepath"
	"testing"

	"github.com/okayest-dev/genie/internal/tracker"
)

func TestMarkdownTrackerIsolation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	tr := NewMarkdownTracker(dir)
	if got := tr.Isolation(); got != tracker.IsolationShared {
		t.Errorf("markdown tracker isolation = %v, want shared", got)
	}
	if !tr.Isolation().WorktreeSafe() {
		t.Error("shared markdown tracker must be worktree-safe")
	}
}

func TestMarkdownTrackerDefaultsToUserDir(t *testing.T) {
	tr := NewMarkdownTracker("")
	if tr.tasksDir == "" {
		t.Error("default markdown tracker should resolve a tasks dir")
	}
}