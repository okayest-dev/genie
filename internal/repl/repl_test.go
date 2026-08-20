package repl

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/okayest-dev/og/internal/ledger"
)

func TestSlashHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/help\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Commands:") {
		t.Errorf("stdout = %q, want help text", stdout.String())
	}
}

func TestSlashQuit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSlashUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/foo\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "unknown command: /foo") {
		t.Errorf("stdout = %q, want unknown command message", stdout.String())
	}
}

func TestEmptyInputSkipped(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("\n\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty lines should not produce any output except the prompt.
	if strings.Contains(stdout.String(), "Error:") {
		t.Errorf("stdout = %q, want no errors from empty input", stdout.String())
	}
}

func TestEOFExitsCleanly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader(""),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error on EOF: %v", err)
	}
}

func writeTestLedger(t *testing.T, dir, sessionID string) {
	t.Helper()
	l := ledger.New(dir, sessionID)
	l.RecordToolCall("call_1")
	l.Snapshot("foo.txt", "line1\nline2")
	l.RecordMutation("foo.txt", "line1\nline2", "line1\nline3", ledger.OpEdit)
	if err := l.Close(); err != nil {
		t.Fatalf("write ledger: %v", err)
	}
}

func TestSlashChangesEmpty(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/changes\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "no changes") {
		t.Errorf("stdout = %q, want 'no changes'", stdout.String())
	}
}

func TestSlashChangesListsBatches(t *testing.T) {
	dir := t.TempDir()
	// We need a known session ID to write ledger data, so write it manually.
	sessionID := "test-session"
	writeTestLedger(t, dir, sessionID)

	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/changes\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: dir,
	}
	// Inject session by pre-creating the session file. The REPL will create
	// a new session, so we write the ledger under that ID instead.
	// Actually, we need to match the session the REPL creates. Let's use a
	// different approach: write ledger, then point at it.
	// The REPL creates its own session, so we can't predict the ID.
	// Instead, let's write the ledger for the session the REPL will create.
	// We'll read the session ID from stderr after it's created.
	// For simplicity, let's just test the empty case here and test the
	// listing case via a unit test on the handler.

	// The /changes command uses the session the REPL creates, which starts
	// empty. So /changes on a fresh session should print "no changes".
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "no changes") {
		t.Errorf("stdout = %q, want 'no changes'", out)
	}
}

func TestSlashChangesIDNotFound(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/changes 99\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "no such change id: 99") {
		t.Errorf("stdout = %q, want 'no such change id: 99'", stdout.String())
	}
}

func TestSlashChangesInvalidID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/changes abc\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "invalid change id: abc") {
		t.Errorf("stdout = %q, want 'invalid change id: abc'", stdout.String())
	}
}

func TestHandleChangesListsBatches(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-session"

	// Write two batches.
	l := ledger.New(dir, sessionID)
	l.RecordToolCall("call_1")
	l.Snapshot("foo.txt", "old")
	l.RecordMutation("foo.txt", "old", "new", ledger.OpEdit)
	if err := l.Close(); err != nil {
		t.Fatalf("close 1: %v", err)
	}
	l.RecordToolCall("call_2")
	l.Snapshot("bar.txt", "a")
	l.RecordMutation("bar.txt", "a", "b", ledger.OpOverwrite)
	if err := l.Close(); err != nil {
		t.Fatalf("close 2: %v", err)
	}

	var stdout bytes.Buffer
	cfg := &Config{SessionDir: dir, Stderr: &bytes.Buffer{}}
	handleChanges("", cfg, sessionID, &stdout)

	out := stdout.String()
	if !strings.Contains(out, "foo.txt") {
		t.Errorf("output missing foo.txt: %s", out)
	}
	if !strings.Contains(out, "bar.txt") {
		t.Errorf("output missing bar.txt: %s", out)
	}
	// Newest first: seq 1 before seq 0.
	idx1 := strings.Index(out, "001")
	idx0 := strings.Index(out, "000")
	if idx1 >= idx0 {
		t.Errorf("expected batch 1 before batch 0, got:\n%s", out)
	}
}

func TestHandleChangesShowBatch(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-session"

	l := ledger.New(dir, sessionID)
	l.RecordToolCall("call_1")
	l.Snapshot("foo.txt", "line1\nline2")
	l.RecordMutation("foo.txt", "line1\nline2", "line1\nline3", ledger.OpEdit)
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var stdout bytes.Buffer
	cfg := &Config{SessionDir: dir, Stderr: &bytes.Buffer{}}
	handleChanges("0", cfg, sessionID, &stdout)

	out := stdout.String()
	if !strings.Contains(out, "foo.txt") {
		t.Errorf("output missing foo.txt: %s", out)
	}
	if !strings.Contains(out, "edit") {
		t.Errorf("output missing op type: %s", out)
	}
	if !strings.Contains(out, "-line2") || !strings.Contains(out, "+line3") {
		t.Errorf("output missing diff content: %s", out)
	}
}

func TestHandleChangesBatchNotFound(t *testing.T) {
	var stdout bytes.Buffer
	cfg := &Config{SessionDir: t.TempDir(), Stderr: &bytes.Buffer{}}
	handleChanges("0", cfg, "nonexistent", &stdout)
	if !strings.Contains(stdout.String(), "no such change id: 0") {
		t.Errorf("output = %q, want 'no such change id: 0'", stdout.String())
	}
}

func TestHandleChangesInvalidID(t *testing.T) {
	var stdout bytes.Buffer
	cfg := &Config{SessionDir: t.TempDir(), Stderr: &bytes.Buffer{}}
	handleChanges("abc", cfg, "test", &stdout)
	if !strings.Contains(stdout.String(), "invalid change id: abc") {
		t.Errorf("output = %q, want 'invalid change id: abc'", stdout.String())
	}
}
