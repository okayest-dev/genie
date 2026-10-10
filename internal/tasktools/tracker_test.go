package tasktools

import (
	"testing"

	"github.com/okayest-dev/genie/internal/tracker"
)

func TestFakeTrackerIsolationDefaultsToUnknown(t *testing.T) {
	tr := NewFakeTracker(nil)
	if got := tr.Isolation(); got != tracker.IsolationUnknown {
		t.Errorf("default fake isolation = %v, want unknown", got)
	}
	if tr.Isolation().WorktreeSafe() {
		t.Error("unknown fake isolation must not be worktree-safe until set")
	}
}

func TestFakeTrackerSetIsolation(t *testing.T) {
	tr := NewFakeTracker(nil)
	tr.SetIsolation(tracker.IsolationShared)
	if got := tr.Isolation(); got != tracker.IsolationShared {
		t.Errorf("after SetIsolation(shared), isolation = %v, want shared", got)
	}
	if !tr.Isolation().WorktreeSafe() {
		t.Error("shared fake isolation must be worktree-safe")
	}
	tr.SetIsolation(tracker.IsolationPerCheckout)
	if got := tr.Isolation(); got != tracker.IsolationPerCheckout {
		t.Errorf("after SetIsolation(per_checkout), isolation = %v, want per_checkout", got)
	}
	if tr.Isolation().WorktreeSafe() {
		t.Error("per_checkout fake isolation must not be worktree-safe")
	}
}