// Package ledger implements the change ledger: an append-only JSONL record
// of file mutations captured during agent-loop cycles. Each cycle that
// mutates files produces exactly one batch with per-file diffs.
package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxDiffLines = 2000
	maxDiffBytes = 50 << 10 // 50 KB
	spillDirName = ".og-changes"
)

// Op represents the type of file operation.
type Op string

const (
	OpCreate    Op = "create"
	OpOverwrite Op = "overwrite"
	OpEdit      Op = "edit"
	OpDelete    Op = "delete"
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
	Ops   Op     `json:"ops"`
	Diff  string `json:"diff"`  // unified diff text, or "[binary]"
	Delta Delta  `json:"delta"` // line counts
}

// Delta holds line count changes.
type Delta struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// Mutation holds pre- and post-mutation state for a file within a cycle.
type Mutation struct {
	Path       string
	OldContent string
	NewContent string
	Op         Op
}

// Ledger captures file mutations and writes batches to JSONL.
type Ledger struct {
	sessionDir string
	sessionID  string
	seq        int
	// Per-cycle state: mutations tracked during the cycle.
	mutations map[string]*Mutation // path -> mutation state
	toolIDs   []string             // tool call IDs in current cycle
}

// New creates a ledger for the given session.
func New(sessionDir, sessionID string) *Ledger {
	return &Ledger{
		sessionDir: sessionDir,
		sessionID:  sessionID,
		mutations:  make(map[string]*Mutation),
	}
}

// Snapshot records the pre-mutation content of a file. Call this before
// tool execution for write/edit tools. If the file doesn't exist, snapshot
// records an empty string (new file creation).
func (l *Ledger) Snapshot(path, content string) {
	m, ok := l.mutations[path]
	if !ok {
		m = &Mutation{Path: path}
		l.mutations[path] = m
	}
	m.OldContent = content
}

// GetSnapshot retrieves the pre-mutation content recorded by Snapshot.
func (l *Ledger) GetSnapshot(path string) string {
	if m, ok := l.mutations[path]; ok {
		return m.OldContent
	}
	return ""
}

// RecordToolCall adds a tool call ID to the current cycle.
func (l *Ledger) RecordToolCall(id string) {
	l.toolIDs = append(l.toolIDs, id)
}

// RecordMutation records that a file was successfully mutated. The oldContent
// parameter is the pre-mutation content (from Snapshot). The newContent is
// the post-mutation content. The op parameter specifies the operation type.
// Actual diff computation happens at Close.
func (l *Ledger) RecordMutation(path, oldContent, newContent string, op Op) {
	m, ok := l.mutations[path]
	if !ok {
		m = &Mutation{Path: path}
		l.mutations[path] = m
	}
	m.NewContent = newContent
	m.Op = op
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
	for _, m := range l.mutations {
		// Skip mutations that were never completed (Snapshot without RecordMutation).
		if m.Op == "" {
			continue
		}
		diff := computeDiff(m.Path, m.OldContent, m.NewContent)
		delta := computeDelta(diff)

		// Truncate huge diffs with spill.
		if len(diff) > maxDiffBytes || strings.Count(diff, "\n") > maxDiffLines {
			spillPath, err := l.spillDiff(m.Path, diff)
			if err != nil {
				return fmt.Errorf("spill diff: %w", err)
			}
			diff = fmt.Sprintf("[truncated — full diff spilled to %s]", spillPath)
		}

		batch.Files = append(batch.Files, File{
			Path:  m.Path,
			Ops:   m.Op,
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
	l.mutations = make(map[string]*Mutation)
	l.toolIDs = nil
	return nil
}

// ledgerPath returns the path to the JSONL ledger file for a session.
func ledgerPath(sessionDir, sessionID string) string {
	return filepath.Join(sessionDir, sessionID+".changes.jsonl")
}

// writeBatch appends one batch as a JSON line to the ledger file.
func (l *Ledger) writeBatch(batch Batch) error {
	path := ledgerPath(l.sessionDir, l.sessionID)
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
	// Use seq and hash of full path for unique spill file name.
	hash := sha256.Sum256([]byte(filePath))
	name := fmt.Sprintf("%d-%x.diff", l.seq, hash[:8])
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

	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")

	// Compute LCS to find longest common subsequence.
	lcs := computeLCS(oldLines, newLines)

	// Build diff from LCS.
	var diff strings.Builder
	fmt.Fprintf(&diff, "--- a/%s\n+++ b/%s\n", path, path)

	i, j, k := 0, 0, 0
	for k < len(lcs) {
		// Output removed lines (old lines not in LCS).
		for i < len(oldLines) && oldLines[i] != lcs[k] {
			fmt.Fprintf(&diff, "-%s\n", oldLines[i])
			i++
		}
		// Output added lines (new lines not in LCS).
		for j < len(newLines) && newLines[j] != lcs[k] {
			fmt.Fprintf(&diff, "+%s\n", newLines[j])
			j++
		}
		// Output kept line.
		fmt.Fprintf(&diff, " %s\n", oldLines[i])
		i++
		j++
		k++
	}

	// Output remaining removed lines.
	for i < len(oldLines) {
		fmt.Fprintf(&diff, "-%s\n", oldLines[i])
		i++
	}
	// Output remaining added lines.
	for j < len(newLines) {
		fmt.Fprintf(&diff, "+%s\n", newLines[j])
		j++
	}

	return diff.String()
}

// computeLCS computes the longest common subsequence of two string slices.
func computeLCS(a, b []string) []string {
	m, n := len(a), len(b)
	// Build DP table.
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] > dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	// Backtrack to find LCS.
	lcs := make([]string, 0, dp[m][n])
	i, j := m, n
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			lcs = append(lcs, a[i-1])
			i--
			j--
		} else if dp[i-1][j] > dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	// Reverse LCS (we built it backwards).
	for i, j := 0, len(lcs)-1; i < j; i, j = i+1, j-1 {
		lcs[i], lcs[j] = lcs[j], lcs[i]
	}
	return lcs
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

// LoadBatches reads all batches from a session's JSONL ledger file.
// Batches are returned in reverse chronological order (newest first).
func LoadBatches(sessionDir, sessionID string) ([]Batch, error) {
	path := ledgerPath(sessionDir, sessionID)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read ledger: %w", err)
	}

	var batches []Batch
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var b Batch
		if err := json.Unmarshal([]byte(line), &b); err != nil {
			return nil, fmt.Errorf("unmarshal batch: %w", err)
		}
		batches = append(batches, b)
	}

	// Reverse to newest-first.
	for i, j := 0, len(batches)-1; i < j; i, j = i+1, j-1 {
		batches[i], batches[j] = batches[j], batches[i]
	}

	return batches, nil
}

// LoadBatchByID returns the batch with the given sequence number, or nil if
// not found.
func LoadBatchByID(sessionDir, sessionID string, seq int) (*Batch, error) {
	batches, err := LoadBatches(sessionDir, sessionID)
	if err != nil {
		return nil, err
	}
	for i := range batches {
		if batches[i].Seq == seq {
			return &batches[i], nil
		}
	}
	return nil, nil
}
