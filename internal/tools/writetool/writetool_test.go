package writetool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/okayest-dev/og/internal/tools"
)

// alwaysAllow is a Confirmer that always accepts.
type alwaysAllow struct{}

func (alwaysAllow) Confirm(string) bool { return true }

// neverAllow is a Confirmer that always declines.
type neverAllow struct{}

func (neverAllow) Confirm(string) bool { return false }

func TestWriteNewFile(t *testing.T) {
	dir := t.TempDir()
	tool := New(dir, alwaysAllow{})
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
	tool := New(dir, alwaysAllow{})
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
	tool := New(dir, alwaysAllow{})
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

func TestWriteOverwriteConfirmed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	os.WriteFile(path, []byte("old"), 0o644)

	tool := New(dir, alwaysAllow{})
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

func TestWriteOverwriteDenied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	os.WriteFile(path, []byte("old"), 0o644)

	tool := New(dir, neverAllow{})
	args, _ := json.Marshal(map[string]any{
		"path":    "existing.txt",
		"content": "new",
	})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute on denied overwrite returned nil, want error")
	}
	if !strings.Contains(err.Error(), "denied by user") {
		t.Errorf("error = %v, want 'denied by user'", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "old" {
		t.Errorf("file was modified despite denial: content = %q", string(data))
	}
}

func TestWriteMissingPath(t *testing.T) {
	tool := New(t.TempDir(), alwaysAllow{})
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
	tool := New(t.TempDir(), alwaysAllow{})
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
	tool := New("/tmp", alwaysAllow{})
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
	tool := New(t.TempDir(), tools.AutoDeny{})
	if tool.Name() != "write" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "write")
	}
}

func TestWriteParameters(t *testing.T) {
	tool := New(t.TempDir(), tools.AutoDeny{})
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
