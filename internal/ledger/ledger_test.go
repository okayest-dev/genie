package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewCreatesLedger(t *testing.T) {
	l := New(t.TempDir(), "test-session")
	if l == nil {
		t.Fatal("New returned nil")
	}
}

func TestCloseWithNoMutationsReturnsNil(t *testing.T) {
	l := New(t.TempDir(), "test-session")
	if err := l.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCloseWithNoToolCallsReturnsNil(t *testing.T) {
	l := New(t.TempDir(), "test-session")
	l.Snapshot("test.txt", "old content")
	if err := l.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSingleFileMutation(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, "test-session")

	l.RecordToolCall("call_1")
	l.Snapshot("test.txt", "old content")
	l.RecordMutation("test.txt", "old content", "new content", OpOverwrite)

	if err := l.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the JSONL file was created.
	path := filepath.Join(dir, "test-session.changes.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}

	var batch Batch
	if err := json.Unmarshal(data, &batch); err != nil {
		t.Fatalf("unmarshal batch: %v", err)
	}

	if batch.Seq != 0 {
		t.Errorf("seq = %d, want 0", batch.Seq)
	}
	if batch.Session != "test-session" {
		t.Errorf("session = %q, want %q", batch.Session, "test-session")
	}
	if len(batch.ToolCallIDs) != 1 || batch.ToolCallIDs[0] != "call_1" {
		t.Errorf("tool_call_ids = %v, want [call_1]", batch.ToolCallIDs)
	}
	if len(batch.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(batch.Files))
	}
	if batch.Files[0].Path != "test.txt" {
		t.Errorf("file path = %q, want %q", batch.Files[0].Path, "test.txt")
	}
	if batch.Files[0].Ops != OpOverwrite {
		t.Errorf("file ops = %q, want %q", batch.Files[0].Ops, OpOverwrite)
	}
	if !strings.Contains(batch.Files[0].Diff, "-old content") {
		t.Errorf("diff missing removed line: %q", batch.Files[0].Diff)
	}
	if !strings.Contains(batch.Files[0].Diff, "+new content") {
		t.Errorf("diff missing added line: %q", batch.Files[0].Diff)
	}
}

func TestMultipleEditsToOneFileCollapse(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, "test-session")

	l.RecordToolCall("call_1")
	l.RecordToolCall("call_2")
	l.Snapshot("test.txt", "original")
	l.RecordMutation("test.txt", "original", "modified once", OpEdit)
	l.RecordMutation("test.txt", "modified once", "modified twice", OpEdit)

	if err := l.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	path := filepath.Join(dir, "test-session.changes.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}

	var batch Batch
	if err := json.Unmarshal(data, &batch); err != nil {
		t.Fatalf("unmarshal batch: %v", err)
	}

	if len(batch.Files) != 1 {
		t.Fatalf("files = %d, want 1 (collapsed)", len(batch.Files))
	}
	if len(batch.ToolCallIDs) != 2 {
		t.Errorf("tool_call_ids = %d, want 2", len(batch.ToolCallIDs))
	}
}

func TestNewFileCreate(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, "test-session")

	l.RecordToolCall("call_1")
	l.Snapshot("new.txt", "") // Empty = new file
	l.RecordMutation("new.txt", "", "file content", OpCreate)

	if err := l.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	path := filepath.Join(dir, "test-session.changes.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}

	var batch Batch
	if err := json.Unmarshal(data, &batch); err != nil {
		t.Fatalf("unmarshal batch: %v", err)
	}

	if batch.Files[0].Ops != OpCreate {
		t.Errorf("ops = %q, want %q", batch.Files[0].Ops, OpCreate)
	}
	if !strings.Contains(batch.Files[0].Diff, "+++ b/new.txt") {
		t.Errorf("diff missing new file header: %q", batch.Files[0].Diff)
	}
}

func TestFileDelete(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, "test-session")

	l.RecordToolCall("call_1")
	l.Snapshot("deleted.txt", "content to delete")
	l.RecordMutation("deleted.txt", "content to delete", "", OpDelete)

	if err := l.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	path := filepath.Join(dir, "test-session.changes.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}

	var batch Batch
	if err := json.Unmarshal(data, &batch); err != nil {
		t.Fatalf("unmarshal batch: %v", err)
	}

	if !strings.Contains(batch.Files[0].Diff, "--- a/deleted.txt") {
		t.Errorf("diff missing delete header: %q", batch.Files[0].Diff)
	}
}

func TestMultipleBatchesIncrementSeq(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, "test-session")

	// First batch.
	l.RecordToolCall("call_1")
	l.Snapshot("a.txt", "old")
	l.RecordMutation("a.txt", "old", "new", OpEdit)
	if err := l.Close(); err != nil {
		t.Fatalf("close 1: %v", err)
	}

	// Second batch.
	l.RecordToolCall("call_2")
	l.Snapshot("b.txt", "old")
	l.RecordMutation("b.txt", "old", "new", OpEdit)
	if err := l.Close(); err != nil {
		t.Fatalf("close 2: %v", err)
	}

	path := filepath.Join(dir, "test-session.changes.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}

	var b1, b2 Batch
	if err := json.Unmarshal([]byte(lines[0]), &b1); err != nil {
		t.Fatalf("unmarshal batch 1: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &b2); err != nil {
		t.Fatalf("unmarshal batch 2: %v", err)
	}

	if b1.Seq != 0 || b2.Seq != 1 {
		t.Errorf("seqs = %d, %d, want 0, 1", b1.Seq, b2.Seq)
	}
}

func TestComputeDiffNoChange(t *testing.T) {
	diff := computeDiff("test.txt", "same", "same")
	if diff != "" {
		t.Errorf("diff = %q, want empty", diff)
	}
}

func TestComputeDiffAddedLines(t *testing.T) {
	diff := computeDiff("test.txt", "", "line1\nline2")
	if !strings.Contains(diff, "+line1") || !strings.Contains(diff, "+line2") {
		t.Errorf("diff missing added lines: %q", diff)
	}
}

func TestComputeDiffRemovedLines(t *testing.T) {
	diff := computeDiff("test.txt", "line1\nline2", "")
	if !strings.Contains(diff, "-line1") || !strings.Contains(diff, "-line2") {
		t.Errorf("diff missing removed lines: %q", diff)
	}
}

func TestComputeDelta(t *testing.T) {
	diff := "--- a/test.txt\n+++ b/test.txt\n@@ -1,2 +1,3 @@\n line1\n-line2\n+line2\n+line3"
	delta := computeDelta(diff)
	if delta.Removed != 1 {
		t.Errorf("removed = %d, want 1", delta.Removed)
	}
	if delta.Added != 2 {
		t.Errorf("added = %d, want 2", delta.Added)
	}
}

func TestComputeDiffReorderedLines(t *testing.T) {
	old := "a\nb\nc"
	new := "c\nb\na"
	diff := computeDiff("test.txt", old, new)
	if diff == "" {
		t.Fatal("diff should not be empty for reordered lines")
	}
	removed := strings.Count(diff, "\n-")
	added := strings.Count(diff, "\n+")
	if removed == 0 || added == 0 {
		t.Errorf("diff should show both removals and additions for reorder: %q", diff)
	}
}

func TestSpillDiff(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, "test-session")

	// Create a large diff.
	var diff strings.Builder
	for i := 0; i < maxDiffLines+100; i++ {
		diff.WriteString("+added line\n")
	}

	spillPath, err := l.spillDiff("test.txt", diff.String())
	if err != nil {
		t.Fatalf("spillDiff: %v", err)
	}

	if _, err := os.Stat(spillPath); os.IsNotExist(err) {
		t.Errorf("spill file not created: %s", spillPath)
	}
}

func TestSpillDiffUniquePaths(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, "test-session")

	diff := "+line\n"
	path1, err := l.spillDiff("a/foo.txt", diff)
	if err != nil {
		t.Fatalf("spillDiff 1: %v", err)
	}
	path2, err := l.spillDiff("b/foo.txt", diff)
	if err != nil {
		t.Fatalf("spillDiff 2: %v", err)
	}

	if path1 == path2 {
		t.Errorf("spill paths collide: %s == %s", path1, path2)
	}
}

func TestNoEntryForSnapshotWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, "test-session")

	// Snapshot without RecordMutation should produce no entry.
	l.RecordToolCall("call_1")
	l.Snapshot("test.txt", "old content")
	// No RecordMutation call.

	if err := l.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	path := filepath.Join(dir, "test-session.changes.jsonl")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("ledger file should not exist when no mutations recorded")
	}
}

func TestNoEntryForToolCallWithoutSnapshot(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, "test-session")

	// RecordToolCall without Snapshot/RecordMutation should produce no entry.
	l.RecordToolCall("call_1")
	// No Snapshot or RecordMutation calls.

	if err := l.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	path := filepath.Join(dir, "test-session.changes.jsonl")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("ledger file should not exist when no mutations recorded")
	}
}

func TestSpillTruncationMessageRecorded(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, "test-session")

	// Create a huge diff that will be truncated.
	var diff strings.Builder
	for i := 0; i < maxDiffLines+100; i++ {
		diff.WriteString("+added line\n")
	}

	l.RecordToolCall("call_1")
	l.Snapshot("test.txt", "old")
	l.RecordMutation("test.txt", "old", diff.String(), OpOverwrite)

	if err := l.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	path := filepath.Join(dir, "test-session.changes.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}

	var batch Batch
	if err := json.Unmarshal(data, &batch); err != nil {
		t.Fatalf("unmarshal batch: %v", err)
	}

	if len(batch.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(batch.Files))
	}
	if !strings.Contains(batch.Files[0].Diff, "[truncated") {
		t.Errorf("diff should contain truncation message: %q", batch.Files[0].Diff)
	}
}

func TestMultipleEditsCollapseDiffContent(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, "test-session")

	l.RecordToolCall("call_1")
	l.RecordToolCall("call_2")
	l.Snapshot("test.txt", "original")
	l.RecordMutation("test.txt", "original", "modified once", OpEdit)
	l.RecordMutation("test.txt", "modified once", "modified twice", OpEdit)

	if err := l.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	path := filepath.Join(dir, "test-session.changes.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}

	var batch Batch
	if err := json.Unmarshal(data, &batch); err != nil {
		t.Fatalf("unmarshal batch: %v", err)
	}

	if len(batch.Files) != 1 {
		t.Fatalf("files = %d, want 1 (collapsed)", len(batch.Files))
	}
	// The diff should show original -> modified twice (collapsed).
	if !strings.Contains(batch.Files[0].Diff, "-original") {
		t.Errorf("diff should show original content removed: %q", batch.Files[0].Diff)
	}
	if !strings.Contains(batch.Files[0].Diff, "+modified twice") {
		t.Errorf("diff should show final content added: %q", batch.Files[0].Diff)
	}
}
