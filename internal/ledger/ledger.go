// Package ledger implements the change ledger: an append-only JSONL record
// of file mutations captured during agent-loop cycles. Each cycle that
// mutates files produces exactly one batch with per-file diffs.
package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxDiffLines  = 2000
	maxDiffBytes  = 50 << 10 // 50 KB
	spillDirName  = ".og-changes"
)

// Batch is one ledger entry — all file changes from a single agent-loop cycle.
type Batch struct {
	Seq         int      `json:"seq"`
	Time        string   `json:"ts"`
	Session     string   `json:"session"`
	ToolCallIDs []string `json:"tool_call_ids"`
	Files       []File   `json:"files"`
}

// File represents one file's changes within a batch.
type File struct {
	Path  string `json:"path"`
	Ops   string `json:"ops"`   // "create", "overwrite", "edit"
	Diff  string `json:"diff"`  // unified diff text, or "[binary]"
	Delta Delta  `json:"delta"` // line counts
}

// Delta holds line count changes.
type Delta struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// Ledger captures file mutations and writes batches to JSONL.
type Ledger struct {
	sessionDir string
	sessionID  string
	seq        int
	// Per-cycle state: snapshots taken before mutations.
	snapshots map[string]string // path -> pre-mutation content
	toolIDs   []string          // tool call IDs in current cycle
}

// New creates a ledger for the given session.
func New(sessionDir, sessionID string) *Ledger {
	return &Ledger{
		sessionDir: sessionDir,
		sessionID:  sessionID,
		snapshots:  make(map[string]string),
	}
}

// Snapshot records the pre-mutation content of a file. Call this before
// tool execution for write/edit tools. If the file doesn't exist, snapshot
// records an empty string (new file creation).
func (l *Ledger) Snapshot(path, content string) {
	l.snapshots[path] = content
}

// GetSnapshot retrieves the pre-mutation content recorded by Snapshot.
func (l *Ledger) GetSnapshot(path string) string {
	return l.snapshots[path]
}

// RecordToolCall adds a tool call ID to the current cycle.
func (l *Ledger) RecordToolCall(id string) {
	l.toolIDs = append(l.toolIDs, id)
}

// RecordMutation records that a file was successfully mutated. The oldContent
// parameter is the pre-mutation content (from Snapshot). The newContent is
// the post-mutation content.
func (l *Ledger) RecordMutation(path, oldContent, newContent, ops string) {
	// This is a placeholder — the actual diff computation happens at Close.
	// For now, store the mutation info.
	l.snapshots[path+"_new"] = newContent
	l.snapshots[path+"_ops"] = ops
}

// Close computes diffs and writes the batch to the JSONL ledger file.
// It returns nil if no mutations were recorded.
func (l *Ledger) Close() error {
	if len(l.toolIDs) == 0 {
		return nil
	}

	batch := Batch{
		Seq:         l.seq,
		Time:        time.Now().UTC().Format(time.RFC3339),
		Session:     l.sessionID,
		ToolCallIDs: l.toolIDs,
	}

	// Compute diffs for each mutated file.
	for path := range l.snapshots {
		if strings.HasSuffix(path, "_new") || strings.HasSuffix(path, "_ops") {
			continue
		}
		oldContent := l.snapshots[path]
		newContent := l.snapshots[path+"_new"]
		ops := l.snapshots[path+"_ops"]

		diff := computeDiff(path, oldContent, newContent)
		delta := computeDelta(diff)

		// Truncate huge diffs with spill.
		if len(diff) > maxDiffBytes || strings.Count(diff, "\n") > maxDiffLines {
			spillPath, err := l.spillDiff(path, diff)
			if err != nil {
				return fmt.Errorf("spill diff: %w", err)
			}
			diff = fmt.Sprintf("[truncated — full diff spilled to %s]", spillPath)
		}

		batch.Files = append(batch.Files, File{
			Path:  path,
			Ops:   ops,
			Diff:  diff,
			Delta: delta,
		})
	}

	if len(batch.Files) == 0 {
		return nil
	}

	// Write the batch to the JSONL file.
	if err := l.writeBatch(batch); err != nil {
		return err
	}

	l.seq++
	l.snapshots = make(map[string]string)
	l.toolIDs = nil
	return nil
}

// writeBatch appends one batch as a JSON line to the ledger file.
func (l *Ledger) writeBatch(batch Batch) error {
	path := filepath.Join(l.sessionDir, l.sessionID+".changes.jsonl")
	data, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal batch: %w", err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open ledger: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write ledger: %w", err)
	}
	return nil
}

// spillDiff writes a large diff to a spill file and returns its path.
func (l *Ledger) spillDiff(filePath, diff string) (string, error) {
	dir := filepath.Join(l.sessionDir, spillDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Use seq and basename for the spill file name.
	base := filepath.Base(filePath)
	name := fmt.Sprintf("%d-%s.diff", l.seq, base)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(diff), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// computeDiff produces a unified diff between old and new content.
func computeDiff(path, old, new string) string {
	if old == new {
		return ""
	}
	if old == "" {
		// New file creation — all lines are additions.
		lines := strings.Split(new, "\n")
		var diff strings.Builder
		fmt.Fprintf(&diff, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", path, len(lines))
		for _, line := range lines {
			fmt.Fprintf(&diff, "+%s\n", line)
		}
		return diff.String()
	}
	if new == "" {
		// File deletion — all lines are removals.
		lines := strings.Split(old, "\n")
		var diff strings.Builder
		fmt.Fprintf(&diff, "--- a/%s\n+++ /dev/null\n@@ -1,%d +0,0 @@\n", path, len(lines))
		for _, line := range lines {
			fmt.Fprintf(&diff, "-%s\n", line)
		}
		return diff.String()
	}
	// Simple line-based diff.
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")

	var diff strings.Builder
	fmt.Fprintf(&diff, "--- a/%s\n+++ b/%s\n", path, path)

	// Simple approach: show removed and added lines.
	removed := 0
	added := 0
	for _, line := range oldLines {
		if !contains(newLines, line) {
			removed++
		}
	}
	for _, line := range newLines {
		if !contains(oldLines, line) {
			added++
		}
	}

	fmt.Fprintf(&diff, "@@ -1,%d +1,%d @@\n", len(oldLines), len(newLines))
	for _, line := range oldLines {
		if contains(newLines, line) {
			fmt.Fprintf(&diff, " %s\n", line)
		} else {
			fmt.Fprintf(&diff, "-%s\n", line)
		}
	}
	for _, line := range newLines {
		if !contains(oldLines, line) {
			fmt.Fprintf(&diff, "+%s\n", line)
		}
	}

	return diff.String()
}

// computeDelta extracts added/removed line counts from a diff.
func computeDelta(diff string) Delta {
	var d Delta
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			d.Removed++
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			d.Added++
		}
	}
	return d
}

// contains checks if a string slice contains a value.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
