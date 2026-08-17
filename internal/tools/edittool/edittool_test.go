package edittool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditSingleReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("hello world"), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{
		"path":    "file.txt",
		"oldText": "world",
		"newText": "go",
	})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "edited") {
		t.Errorf("result = %q, want 'edited'", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello go" {
		t.Errorf("file content = %q, want %q", string(data), "hello go")
	}
}

func TestEditNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("hello world"), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{
		"path":    "file.txt",
		"oldText": "xyz",
		"newText": "abc",
	})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute on missing text returned nil, want error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want 'not found' error", err)
	}
}

func TestEditAmbiguous(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("foo bar foo baz foo"), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{
		"path":    "file.txt",
		"oldText": "foo",
		"newText": "qux",
	})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute on ambiguous text returned nil, want error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error = %v, want 'ambiguous' error", err)
	}
}

func TestEditPreservesLineEndingsCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("line1\r\nline2\r\nline3\r\n"), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{
		"path":    "file.txt",
		"oldText": "line2",
		"newText": "LINE2",
	})
	_, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, _ := os.ReadFile(path)
	// Should preserve CRLF.
	if !strings.Contains(string(data), "\r\n") {
		t.Errorf("CRLF line endings were not preserved: %q", string(data))
	}
	if !strings.Contains(string(data), "LINE2") {
		t.Errorf("replacement not applied: %q", string(data))
	}
}

func TestEditPreservesLineEndingsLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{
		"path":    "file.txt",
		"oldText": "line2",
		"newText": "LINE2",
	})
	_, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, _ := os.ReadFile(path)
	// Should not have CRLF.
	if strings.Contains(string(data), "\r\n") {
		t.Errorf("LF line endings were changed to CRLF: %q", string(data))
	}
	if !strings.Contains(string(data), "LINE2") {
		t.Errorf("replacement not applied: %q", string(data))
	}
}

func TestEditStripsBOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("\uFEFFhello world"), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{
		"path":    "file.txt",
		"oldText": "hello",
		"newText": "hi",
	})
	_, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, _ := os.ReadFile(path)
	// BOM should be stripped and not in the result.
	if strings.HasPrefix(string(data), "\uFEFF") {
		t.Errorf("BOM was not stripped: %q", string(data))
	}
	if !strings.Contains(string(data), "hi world") {
		t.Errorf("file content = %q, want 'hi world'", string(data))
	}
}

func TestEditFileNotFound(t *testing.T) {
	tool := New(t.TempDir())
	args, _ := json.Marshal(map[string]any{
		"path":    "nonexistent.txt",
		"oldText": "x",
		"newText": "y",
	})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute on missing file returned nil, want error")
	}
	if !strings.Contains(err.Error(), "file not found") {
		t.Errorf("error = %v, want 'file not found' error", err)
	}
}

func TestEditMissingPath(t *testing.T) {
	tool := New(t.TempDir())
	args, _ := json.Marshal(map[string]any{
		"oldText": "x",
		"newText": "y",
	})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute with no path returned nil, want error")
	}
	if !strings.Contains(err.Error(), "missing required argument: path") {
		t.Errorf("error = %v, want missing path error", err)
	}
}

func TestEditMissingOldText(t *testing.T) {
	tool := New(t.TempDir())
	args, _ := json.Marshal(map[string]any{
		"path":    "file.txt",
		"newText": "y",
	})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute with no oldText returned nil, want error")
	}
	if !strings.Contains(err.Error(), "missing required argument: oldText") {
		t.Errorf("error = %v, want missing oldText error", err)
	}
}

func TestEditEmptyOldText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("hello"), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{
		"path":    "file.txt",
		"oldText": "",
		"newText": "y",
	})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute with empty oldText returned nil, want error")
	}
	if !strings.Contains(err.Error(), "missing required argument: oldText") {
		t.Errorf("error = %v, want missing oldText error", err)
	}
}

func TestEditAbsolute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abs.txt")
	os.WriteFile(path, []byte("foo bar"), 0o644)

	tool := New("/tmp")
	args, _ := json.Marshal(map[string]any{
		"path":    path,
		"oldText": "bar",
		"newText": "baz",
	})
	_, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "foo baz" {
		t.Errorf("file content = %q, want %q", string(data), "foo baz")
	}
}

func TestEditWhitespaceSensitive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("hello  world"), 0o644) // two spaces

	tool := New(dir)
	// Try matching with single space — should fail.
	args, _ := json.Marshal(map[string]any{
		"path":    "file.txt",
		"oldText": "hello world", // one space
		"newText": "hi",
	})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute with single-space match on double-space file returned nil, want error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want 'not found' error", err)
	}
}

func TestEditName(t *testing.T) {
	tool := New(t.TempDir())
	if tool.Name() != "edit" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "edit")
	}
}

func TestEditParameters(t *testing.T) {
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
	for _, name := range []string{"path", "oldText", "newText"} {
		if _, ok := props[name]; !ok {
			t.Errorf("Parameters missing %q property", name)
		}
	}
}
