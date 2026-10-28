package markdowntracker

import (
	"context"
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

func TestMarkdownTrackerFindingOfRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	tr := NewMarkdownTracker(dir)
	ctx := context.Background()

	created, err := tr.Create(ctx, tracker.CreateArgs{Title: "Follow-up", FindingOf: "og-source"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.FindingOf != "og-source" {
		t.Errorf("created FindingOf = %q, want og-source", created.FindingOf)
	}

	read, err := tr.Read(ctx, created.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.FindingOf != "og-source" {
		t.Errorf("read FindingOf = %q, want og-source (provenance not persisted)", read.FindingOf)
	}
}