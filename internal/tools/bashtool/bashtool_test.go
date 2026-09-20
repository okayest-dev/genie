package bashtool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/tools"
)

func argsJSON(command string) json.RawMessage {
	b, _ := json.Marshal(bashArgs{Command: command})
	return b
}

func TestNameAndDescription(t *testing.T) {
	tool := New(t.TempDir(), 0)
	if tool.Name() != "bash" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "bash")
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
}

func TestParametersSchema(t *testing.T) {
	tool := New(t.TempDir(), 0)
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
	tool := New(t.TempDir(), 0)
	out, err := tool.Execute(argsJSON("echo hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "hello" {
		t.Errorf("output = %q, want %q", strings.TrimSpace(out), "hello")
	}
}

func TestMergedStdoutStderr(t *testing.T) {
	tool := New(t.TempDir(), 0)
	out, err := tool.Execute(argsJSON("echo out; echo err >&2"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "out") || !strings.Contains(out, "err") {
		t.Errorf("output = %q, want both stdout and stderr", out)
	}
}

func TestEmptyCommand(t *testing.T) {
	tool := New(t.TempDir(), 0)
	_, err := tool.Execute(argsJSON(""))
	if err == nil {
		t.Fatal("expected error for empty command")
	}
	if !strings.Contains(err.Error(), "missing required argument: command") {
		t.Errorf("error = %q, want missing-argument message", err)
	}
}

func TestNonZeroExit(t *testing.T) {
	tool := New(t.TempDir(), 0)
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
	tool := New(dir, 0)
	out, err := tool.Execute(argsJSON("cat marker.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "found" {
		t.Errorf("output = %q, want %q", out, "found")
	}
}

func TestTimeoutKillsProcess(t *testing.T) {
	tool := New(t.TempDir(), 50*time.Millisecond)
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
	tool := New(t.TempDir(), 0)
	out, err := tool.Execute(argsJSON(cmd))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("output length = %d, want truncation marker in output", len(out))
	}
}

func TestInvalidJSON(t *testing.T) {
	tool := New(t.TempDir(), 0)
	_, err := tool.Execute(json.RawMessage("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("error = %q, want invalid-arguments message", err)
	}
}

// TestBashImplementsPermissioned pins the deny-point seam: the bash tool
// declares its run/net requirements so commands escalate via the gate.
func TestBashImplementsPermissioned(t *testing.T) {
	var _ interface {
		RequiredPermissions(json.RawMessage) ([]tools.Requirement, error)
	} = New(t.TempDir(), 0)
}

func TestRequiredPermissionsExtractsRunExecutable(t *testing.T) {
	tool := New(t.TempDir(), 0)
	reqs, err := tool.RequiredPermissions(argsJSON("git status"))
	if err != nil {
		t.Fatalf("RequiredPermissions: %v", err)
	}
	// Should have run axis with executable "git"
	if len(reqs) != 1 {
		t.Fatalf("got %d requirements, want 1 (run)", len(reqs))
	}
	if reqs[0].Axis != "run" {
		t.Errorf("Axis = %q, want \"run\"", reqs[0].Axis)
	}
	if reqs[0].Scope != "git" {
		t.Errorf("Scope = %q, want \"git\"", reqs[0].Scope)
	}
}

func TestRequiredPermissionsExtractsNetFromURL(t *testing.T) {
	tool := New(t.TempDir(), 0)
	reqs, err := tool.RequiredPermissions(argsJSON("curl https://api.github.com/users"))
	if err != nil {
		t.Fatalf("RequiredPermissions: %v", err)
	}
	// Should have net axis with host
	var hasNet bool
	for _, r := range reqs {
		if r.Axis == "net" {
			hasNet = true
			if r.Scope != "api.github.com" && r.Scope != "api.github.com:443" {
				t.Errorf("Scope = %q, want host from URL", r.Scope)
			}
		}
	}
	if !hasNet {
		t.Errorf("got %d requirements, want net axis for URL", len(reqs))
	}
}

func TestRequiredPermissionsExtractsBothNetAndRun(t *testing.T) {
	tool := New(t.TempDir(), 0)
	reqs, err := tool.RequiredPermissions(argsJSON("curl -X POST https://api.example.com:8080/data"))
	if err != nil {
		t.Fatalf("RequiredPermissions: %v", err)
	}
	// Should have both net (with port) and run axes
	axes := make(map[string]string)
	for _, r := range reqs {
		axes[r.Axis] = r.Scope
	}
	if _, ok := axes["net"]; !ok {
		t.Errorf("missing net axis; got %v", axes)
	}
	if _, ok := axes["run"]; !ok {
		t.Errorf("missing run axis; got %v", axes)
	}
	if axes["run"] != "curl" {
		t.Errorf("run scope = %q, want \"curl\"", axes["run"])
	}
	if axes["net"] != "api.example.com:8080" {
		t.Errorf("net scope = %q, want host:port \"api.example.com:8080\"", axes["net"])
	}
}

func TestRequiredPermissionsExtractsFromPipedPipeline(t *testing.T) {
	tool := New(t.TempDir(), 0)
	reqs, err := tool.RequiredPermissions(argsJSON("curl https://api.example.com/data | jq ."))
	if err != nil {
		t.Fatalf("RequiredPermissions: %v", err)
	}
	// A piped pipeline still extracts the leading executable and the URL host.
	axes := make(map[string]string)
	for _, r := range reqs {
		axes[r.Axis] = r.Scope
	}
	if axes["run"] != "curl" {
		t.Errorf("run scope = %q, want \"curl\" (leading executable of the pipeline)", axes["run"])
	}
	if axes["net"] != "api.example.com" {
		t.Errorf("net scope = %q, want \"api.example.com\"", axes["net"])
	}
}

func TestRequiredPermissionsFallbackToAxisOnly(t *testing.T) {
	tool := New(t.TempDir(), 0)
	// Command starting with an operator: no executable can be extracted, so the
	// run requirement falls back to a blanket (axis-only) scope.
	reqs, err := tool.RequiredPermissions(argsJSON("> /dev/null"))
	if err != nil {
		t.Fatalf("RequiredPermissions: %v", err)
	}
	if len(reqs) != 1 || reqs[0].Axis != "run" || reqs[0].Scope != "" {
		t.Errorf("reqs = %v, want a single blanket run axis requirement", reqs)
	}
}

func TestRequiredPermissionsWhitespaceCommandFallsBackToBlanket(t *testing.T) {
	tool := New(t.TempDir(), 0)
	reqs, err := tool.RequiredPermissions(argsJSON("   "))
	if err != nil {
		t.Fatalf("RequiredPermissions: %v", err)
	}
	if len(reqs) != 1 || reqs[0].Axis != "run" || reqs[0].Scope != "" {
		t.Errorf("reqs = %v, want a single blanket run axis requirement for whitespace command", reqs)
	}
}

func TestRequiredPermissionsBasenameOfPathExecutable(t *testing.T) {
	tool := New(t.TempDir(), 0)
	reqs, err := tool.RequiredPermissions(argsJSON("/usr/bin/git status"))
	if err != nil {
		t.Fatalf("RequiredPermissions: %v", err)
	}
	if len(reqs) != 1 || reqs[0].Scope != "git" {
		t.Errorf("reqs = %v, want run:git (basename of the path)", reqs)
	}
}

func TestRequiredPermissionsBadArgs(t *testing.T) {
	tool := New(t.TempDir(), 0)
	_, err := tool.RequiredPermissions(json.RawMessage("{not json"))
	if err == nil {
		t.Fatal("RequiredPermissions with malformed args returned nil, want error")
	}
}

func TestCommandWithOutput(t *testing.T) {
	tool := New(t.TempDir(), 0)
	out, err := tool.Execute(argsJSON("printf 'line1\\nline2\\nline3'"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "line1\nline2\nline3" {
		t.Errorf("output = %q, want line1/line2/line3", out)
	}
}
