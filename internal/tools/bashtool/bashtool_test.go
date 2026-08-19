package bashtool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubConfirmer returns the configured answer for every Confirm call.
type stubConfirmer struct{ accept bool }

func (s stubConfirmer) Confirm(string) bool { return s.accept }

func argsJSON(command string) json.RawMessage {
	b, _ := json.Marshal(bashArgs{Command: command})
	return b
}

func TestNameAndDescription(t *testing.T) {
	tool := New(t.TempDir(), stubConfirmer{accept: true}, 0)
	if tool.Name() != "bash" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "bash")
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
}

func TestParametersSchema(t *testing.T) {
	tool := New(t.TempDir(), stubConfirmer{accept: true}, 0)
	p := tool.Parameters()
	if p["type"] != "object" {
		t.Errorf("type = %v, want object", p["type"])
	}
	req, ok := p["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "command" {
		t.Errorf("required = %v, want [command]", p["required"])
	}
}

func TestSuccessfulCommand(t *testing.T) {
	tool := New(t.TempDir(), stubConfirmer{accept: true}, 0)
	out, err := tool.Execute(argsJSON("echo hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "hello" {
		t.Errorf("output = %q, want %q", strings.TrimSpace(out), "hello")
	}
}

func TestMergedStdoutStderr(t *testing.T) {
	tool := New(t.TempDir(), stubConfirmer{accept: true}, 0)
	out, err := tool.Execute(argsJSON("echo out; echo err >&2"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "out") || !strings.Contains(out, "err") {
		t.Errorf("output = %q, want both stdout and stderr", out)
	}
}

func TestConfirmGateDeniesCommand(t *testing.T) {
	tool := New(t.TempDir(), stubConfirmer{accept: false}, 0)
	_, err := tool.Execute(argsJSON("echo hi"))
	if err == nil {
		t.Fatal("expected error for denied command")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %q, want 'denied'", err)
	}
}

func TestEmptyCommand(t *testing.T) {
	tool := New(t.TempDir(), stubConfirmer{accept: true}, 0)
	_, err := tool.Execute(argsJSON(""))
	if err == nil {
		t.Fatal("expected error for empty command")
	}
	if !strings.Contains(err.Error(), "missing required argument: command") {
		t.Errorf("error = %q, want missing-argument message", err)
	}
}

func TestNonZeroExit(t *testing.T) {
	tool := New(t.TempDir(), stubConfirmer{accept: true}, 0)
	out, err := tool.Execute(argsJSON("exit 42"))
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "exit code 42") {
		t.Errorf("error = %q, want exit code 42", err)
	}
	if out != "" {
		t.Errorf("output = %q, want empty", out)
	}
}

func TestCommandRunsInCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("found"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := New(dir, stubConfirmer{accept: true}, 0)
	out, err := tool.Execute(argsJSON("cat marker.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "found" {
		t.Errorf("output = %q, want %q", out, "found")
	}
}

func TestTimeoutKillsProcess(t *testing.T) {
	tool := New(t.TempDir(), stubConfirmer{accept: true}, 50*time.Millisecond)
	start := time.Now()
	out, err := tool.Execute(argsJSON("echo before; sleep 5; echo after"))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error for timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want timeout message", err)
	}
	if elapsed > 1*time.Second {
		t.Errorf("command took %v, expected it to be killed quickly", elapsed)
	}
	// Partial output before the timeout should be captured.
	if !strings.Contains(out, "before") {
		t.Errorf("output = %q, want partial output before timeout", out)
	}
}

func TestOutputTruncationWithSpill(t *testing.T) {
	// Generate output larger than maxOutputBytes (1 MB).
	// python3 -c "print('x' * 1024)" produces 1025 bytes per line (1024 x's + newline).
	// 1024 lines = ~1 MB, so 1025 lines exceeds it.
	cmd := "python3 -c \"for _ in range(2000): print('x' * 1024)\""
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		cmd = "python3 -c \"print('x' * 1024 * 2)\" 2>/dev/null || seq 1 200000 | tr '\\n' x"
	}
	tool := New(t.TempDir(), stubConfirmer{accept: true}, 0)
	out, err := tool.Execute(argsJSON(cmd))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("output length = %d, want truncation marker in output", len(out))
	}
}

func TestInvalidJSON(t *testing.T) {
	tool := New(t.TempDir(), stubConfirmer{accept: true}, 0)
	_, err := tool.Execute(json.RawMessage("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("error = %q, want invalid-arguments message", err)
	}
}

func TestCommandWithOutput(t *testing.T) {
	tool := New(t.TempDir(), stubConfirmer{accept: true}, 0)
	out, err := tool.Execute(argsJSON("printf 'line1\\nline2\\nline3'"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "line1\nline2\nline3" {
		t.Errorf("output = %q, want line1/line2/line3", out)
	}
}
