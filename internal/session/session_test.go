package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/llm"
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

func TestAppendToolCallsPersisted(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	assistantMsg := llm.Message{
		Role:    llm.RoleAssistant,
		Content: "I'll read that file.",
		ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "read", Arguments: `{"path":"test.go"}`},
		},
	}
	if err := s.Append(assistantMsg); err != nil {
		t.Fatalf("Append assistant: %v", err)
	}

	toolMsg := llm.Message{
		Role:       llm.RoleTool,
		Content:    "file contents here",
		ToolCallID: "call_1",
	}
	if err := s.Append(toolMsg); err != nil {
		t.Fatalf("Append tool: %v", err)
	}

	// Verify JSONL contains tool call metadata.
	data, err := os.ReadFile(s.TranscriptPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `"tool_calls"`) {
		t.Errorf("transcript missing tool_calls: %s", data)
	}
	if !strings.Contains(string(data), `"tool_call_id":"call_1"`) {
		t.Errorf("transcript missing tool_call_id: %s", data)
	}

	// Verify Load round-trips the metadata.
	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("Load returned %d messages, want 2", len(loaded))
	}

	if len(loaded[0].ToolCalls) != 1 {
		t.Fatalf("assistant ToolCalls: got %d, want 1", len(loaded[0].ToolCalls))
	}
	if loaded[0].ToolCalls[0].ID != "call_1" {
		t.Errorf("ToolCall ID = %q, want %q", loaded[0].ToolCalls[0].ID, "call_1")
	}
	if loaded[0].ToolCalls[0].Name != "read" {
		t.Errorf("ToolCall Name = %q, want %q", loaded[0].ToolCalls[0].Name, "read")
	}
	if loaded[1].ToolCallID != "call_1" {
		t.Errorf("ToolCallID = %q, want %q", loaded[1].ToolCallID, "call_1")
	}
}

func TestHistoryTracksMessages(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi there"},
	}
	for _, msg := range msgs {
		if err := s.Append(msg); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	history := s.History()
	if len(history) != 3 {
		t.Fatalf("History returned %d messages, want 3", len(history))
	}
	for i, want := range msgs {
		if history[i].Role != want.Role {
			t.Errorf("history[%d].Role = %q, want %q", i, history[i].Role, want.Role)
		}
		if history[i].Content != want.Content {
			t.Errorf("history[%d].Content = %q, want %q", i, history[i].Content, want.Content)
		}
	}
}

func TestLoadIntoPopulatesHistory(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
		{Role: llm.RoleAssistant, Content: "second"},
	}
	for _, msg := range msgs {
		if err := s.Append(msg); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	// Create a fresh session object pointing to the same transcript.
	s2 := &Session{
		ID:             s.ID,
		TranscriptPath: s.TranscriptPath,
	}

	// History should be empty before LoadInto.
	if h := s2.History(); len(h) != 0 {
		t.Fatalf("History before LoadInto: got %d messages, want 0", len(h))
	}

	if err := s2.LoadInto(); err != nil {
		t.Fatalf("LoadInto: %v", err)
	}

	history := s2.History()
	if len(history) != 2 {
		t.Fatalf("History after LoadInto: got %d messages, want 2", len(history))
	}
	if history[0].Content != "first" {
		t.Errorf("history[0].Content = %q, want %q", history[0].Content, "first")
	}
}
