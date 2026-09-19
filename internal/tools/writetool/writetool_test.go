package writetool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/tools"
)

func TestWriteNewFile(t *testing.T) {
	dir := t.TempDir()
	tool := New(dir)
	args, _ := json.Marshal(map[string]any{
		"path":    "hello.txt",
		"content": "hello world",
	})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "created") {
		t.Errorf("result = %q, want 'created'", result)
	}
	data, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("file content = %q, want %q", string(data), "hello world")
	}
}

func TestWriteAutoCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	tool := New(dir)
	args, _ := json.Marshal(map[string]any{
		"path":    "a/b/c/file.txt",
		"content": "nested",
	})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "created") {
		t.Errorf("result = %q, want 'created'", result)
	}
	data, err := os.ReadFile(filepath.Join(dir, "a/b/c/file.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "nested" {
		t.Errorf("file content = %q, want %q", string(data), "nested")
	}
}

func TestWriteOverCapRejected(t *testing.T) {
	dir := t.TempDir()
	tool := New(dir)
	bigContent := strings.Repeat("x", maxContentBytes+1)
	args, _ := json.Marshal(map[string]any{
		"path":    "big.txt",
		"content": bigContent,
	})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute on oversized content returned nil, want error")
	}
	if !strings.Contains(err.Error(), "1 MB") {
		t.Errorf("error = %v, want it to mention 1 MB cap", err)
	}
}

func TestWriteOverwriteNoGate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	os.WriteFile(path, []byte("old"), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{
		"path":    "existing.txt",
		"content": "new",
	})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "overwrote") {
		t.Errorf("result = %q, want 'overwrote'", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Errorf("file content = %q, want %q", string(data), "new")
	}
}

func TestWriteMissingPath(t *testing.T) {
	tool := New(t.TempDir())
	args, _ := json.Marshal(map[string]any{"content": "x"})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute with no path returned nil, want error")
	}
	if !strings.Contains(err.Error(), "missing required argument: path") {
		t.Errorf("error = %v, want missing path error", err)
	}
}

func TestWriteMissingContent(t *testing.T) {
	tool := New(t.TempDir())
	args, _ := json.Marshal(map[string]any{"path": "x.txt"})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute with no content returned nil, want error")
	}
	if !strings.Contains(err.Error(), "missing required argument: content") {
		t.Errorf("error = %v, want missing content error", err)
	}
}

func TestWriteAbsolute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abs.txt")
	tool := New("/tmp")
	args, _ := json.Marshal(map[string]any{
		"path":    path,
		"content": "absolute",
	})
	_, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "absolute" {
		t.Errorf("file content = %q, want %q", string(data), "absolute")
	}
}

func TestWriteName(t *testing.T) {
	tool := New(t.TempDir())
	if tool.Name() != "write" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "write")
	}
}

func TestWriteParameters(t *testing.T) {
	tool := New(t.TempDir())
	params := tool.Parameters()
	if params == nil {
		t.Fatal("Parameters() returned nil")
	}
	if params["type"] != "object" {
		t.Errorf("Parameters type = %v, want object", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters properties is not a map")
	}
	if _, ok := props["path"]; !ok {
		t.Error("Parameters missing 'path' property")
	}
	if _, ok := props["content"]; !ok {
		t.Error("Parameters missing 'content' property")
	}
}

func TestWriteImplementation(t *testing.T) {
	tool := New(t.TempDir())
	if _, ok := any(tool).(interface {
		RequiredPermissions(json.RawMessage) ([]tools.Requirement, error)
	}); !ok {
		t.Fatal("write tool does not implement Permissioned")
	}
}

func TestRequiredPermissions(t *testing.T) {
	tool := New("/work")
	args, _ := json.Marshal(map[string]any{
		"path":    "sub/x.txt",
		"content": "x",
	})
	reqs, err := tool.RequiredPermissions(args)
	if err != nil {
		t.Fatalf("RequiredPermissions: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("got %d requirements, want 1", len(reqs))
	}
	got := reqs[0]
	if got.Axis != "write" {
		t.Errorf("Axis = %q, want \"write\"", got.Axis)
	}
	if got.Scope != "sub/x.txt" {
		t.Errorf("Scope = %q, want \"sub/x.txt\" (raw path)", got.Scope)
	}
}

func TestRequiredPermissionsBadArgs(t *testing.T) {
	tool := New("/work")
	_, err := tool.RequiredPermissions(json.RawMessage("{not json"))
	if err == nil {
		t.Fatal("RequiredPermissions with malformed args returned nil, want error")
	}
}
