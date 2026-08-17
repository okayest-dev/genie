package readtool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{"path": "hello.txt"})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "line1") {
		t.Errorf("result missing line1: %s", result)
	}
	if !strings.Contains(result, "line3") {
		t.Errorf("result missing line3: %s", result)
	}
}

func TestReadFileAbsolute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abs.txt")
	os.WriteFile(path, []byte("absolute"), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{"path": path})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "absolute") {
		t.Errorf("result = %q, want it to contain 'absolute'", result)
	}
}

func TestReadFileNotFound(t *testing.T) {
	tool := New(t.TempDir())
	args, _ := json.Marshal(map[string]any{"path": "nonexistent.txt"})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute on missing file returned nil, want error")
	}
	if !strings.Contains(err.Error(), "no such file") && !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want file-not-found error", err)
	}
}

func TestReadOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lines.txt")
	os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{"path": "lines.txt", "offset": 3})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Should start from line 3 (1-indexed).
	if !strings.Contains(result, "c") {
		t.Errorf("result should contain line 3 'c': %s", result)
	}
	if strings.Contains(result, "\"a\"") || strings.Contains(result, "a\n") {
		// The raw content 'a' might appear in the continuation hint, so check carefully.
		lines := strings.Split(result, "\n")
		if len(lines) > 0 && lines[0] == "a" {
			t.Error("result should not start with line 1")
		}
	}
}

func TestReadLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lines.txt")
	os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{"path": "lines.txt", "limit": 2})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "a") {
		t.Errorf("result should contain 'a': %s", result)
	}
	if !strings.Contains(result, "b") {
		t.Errorf("result should contain 'b': %s", result)
	}
	if strings.Contains(result, "c\n") {
		// 'c' might appear in the continuation hint, but not as content.
		lines := strings.Split(result, "\n")
		for i, line := range lines {
			if line == "c" && i < len(lines)-1 {
				// Last line could be the hint.
				t.Error("result should not contain line 3 as content")
			}
		}
	}
}

func TestReadTruncationHint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lines.txt")
	var content strings.Builder
	for i := 1; i <= 100; i++ {
		content.WriteString("line\n")
	}
	os.WriteFile(path, []byte(content.String()), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{"path": "lines.txt", "limit": 5})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "[Showing lines 1-5 of 100.") {
		t.Errorf("result missing truncation hint: %s", result)
	}
	if !strings.Contains(result, "offset=") {
		t.Errorf("result missing continue pointer: %s", result)
	}
}

func TestReadDirectory(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hi"), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{"path": "."})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "subdir/") {
		t.Errorf("result should mark dir with trailing /: %s", result)
	}
	if !strings.Contains(result, "file.txt") {
		t.Errorf("result should contain file.txt: %s", result)
	}
}

func TestReadDirectoryLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		os.WriteFile(filepath.Join(dir, filepath.Base(t.TempDir())), []byte("x"), 0o644)
	}
	// Create uniquely named files instead.
	for i := 0; i < 10; i++ {
		name := filepath.Join(dir, strings.Repeat("a", i+1)+".txt")
		os.WriteFile(name, []byte("x"), 0o644)
	}

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{"path": ".", "limit": 3})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(result), "\n")
	// First 3 lines are entries, last line is the hint.
	if len(lines) != 4 {
		t.Errorf("result has %d lines, want 4 (3 entries + hint): %s", len(lines), result)
	}
	// The hint should mention offset=4.
	if !strings.Contains(result, "offset=4") {
		t.Errorf("hint should mention offset=4: %s", result)
	}
}

func TestReadBinaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.bin")
	os.WriteFile(path, []byte{0x00, 0x01, 0x02, 0xff}, 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{"path": "binary.bin"})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute on binary file returned nil, want error")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("error = %v, want it to mention binary", err)
	}
}

func TestReadBinarySuggestsTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.bin")
	os.WriteFile(path, []byte{0x00, 0x01}, 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{"path": "binary.bin"})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute on binary file returned nil, want error")
	}
	errStr := err.Error()
	for _, cmd := range []string{"xxd", "file", "head"} {
		if !strings.Contains(errStr, cmd) {
			t.Errorf("error should suggest %q: %s", cmd, errStr)
		}
	}
}

func TestReadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	os.WriteFile(path, []byte(""), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{"path": "empty.txt"})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Empty file should return empty content (possibly with a hint).
	if strings.TrimSpace(result) != "" && !strings.Contains(result, "[") {
		t.Errorf("empty file result = %q, want empty or hint-only", result)
	}
}

func TestReadMissingPath(t *testing.T) {
	tool := New(t.TempDir())
	args, _ := json.Marshal(map[string]any{})
	_, err := tool.Execute(args)
	if err == nil {
		t.Fatal("Execute with no path returned nil, want error")
	}
}

func TestReadName(t *testing.T) {
	tool := New(t.TempDir())
	if tool.Name() != "read" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "read")
	}
}

func TestReadParameters(t *testing.T) {
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
}

func TestReadDefaultLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lines.txt")
	var content strings.Builder
	for i := 0; i < 50; i++ {
		content.WriteString("line\n")
	}
	os.WriteFile(path, []byte(content.String()), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{"path": "lines.txt"})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Default limit should be 2000 lines. With 50 lines, no truncation.
	if strings.Contains(result, "Showing lines") {
		t.Errorf("50-line file should not be truncated: %s", result)
	}
}

func TestReadOffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lines.txt")
	var content strings.Builder
	for i := 1; i <= 20; i++ {
		content.WriteString("line\n")
	}
	os.WriteFile(path, []byte(content.String()), 0o644)

	tool := New(dir)
	args, _ := json.Marshal(map[string]any{"path": "lines.txt", "offset": 5, "limit": 3})
	result, err := tool.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "[Showing lines 5-7 of 20.") {
		t.Errorf("result missing hint for offset+limit: %s", result)
	}
}
