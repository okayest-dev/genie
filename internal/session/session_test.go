package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/okayest-dev/og/internal/llm"
)

func TestNewCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.ID == "" {
		t.Error("session ID is empty")
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("session directory was not created")
	}
}

func TestAppendCreatesTranscript(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	msg := llm.Message{Role: llm.RoleUser, Content: "hello"}
	if err := s.Append(msg); err != nil {
		t.Fatalf("Append: %v", err)
	}

	data, err := os.ReadFile(s.TranscriptPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Errorf("transcript missing content: %s", data)
	}
	if !strings.Contains(string(data), `"role":"user"`) {
		t.Errorf("transcript missing role: %s", data)
	}
}

func TestAppendMultipleMessages(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system prompt"},
		{Role: llm.RoleUser, Content: "user message"},
		{Role: llm.RoleAssistant, Content: "assistant reply"},
	}

	for _, msg := range messages {
		if err := s.Append(msg); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loaded) != 3 {
		t.Fatalf("Load returned %d messages, want 3", len(loaded))
	}

	for i, want := range messages {
		if loaded[i].Role != want.Role {
			t.Errorf("message %d role = %q, want %q", i, loaded[i].Role, want.Role)
		}
		if loaded[i].Content != want.Content {
			t.Errorf("message %d content = %q, want %q", i, loaded[i].Content, want.Content)
		}
	}
}

func TestLoadReturnsNilForMissingFile(t *testing.T) {
	s := &Session{
		ID:             "test",
		TranscriptPath: filepath.Join(t.TempDir(), "nonexistent.jsonl"),
	}

	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded != nil {
		t.Errorf("Load returned %v, want nil for missing file", loaded)
	}
}

func TestLoadReturnsEmptyForEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Session{
		ID:             "empty",
		TranscriptPath: path,
	}

	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded != nil {
		t.Errorf("Load returned %v, want nil for empty file", loaded)
	}
}

func TestSessionIDsAreUnique(t *testing.T) {
	dir := t.TempDir()
	s1, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s2, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if s1.ID == s2.ID {
		t.Errorf("session IDs are not unique: %s == %s", s1.ID, s2.ID)
	}
}
