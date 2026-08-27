package contextmgr

import (
	"context"
	"iter"
	"reflect"
	"testing"

	"github.com/okayest-dev/og/internal/llm"
	"github.com/okayest-dev/og/internal/session"
)

// mockClient records the request it receives and yields scripted events,
// mirroring the mockClient pattern in internal/llm/routing_test.go.
type mockClient struct {
	gotReq llm.Request
	models []llm.Model
	events []llm.Event
}

func (m *mockClient) Stream(_ context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	m.gotReq = req
	return func(yield func(llm.Event) bool) {
		for _, ev := range m.events {
			if !yield(ev) {
				return
			}
		}
	}, nil
}

func (m *mockClient) ListModels(_ context.Context) ([]llm.Model, error) {
	return m.models, nil
}

// newSession creates a session in a temp dir.
func newSession(t *testing.T) *session.Session {
	t.Helper()
	s, err := session.New(t.TempDir())
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	return s
}

// simulateTurn appends a turn's messages to the session and returns them as
// the request the manager would receive, mirroring agent.RunTurn's flow of
// persisting before streaming.
func simulateTurn(t *testing.T, s *session.Session, instruction, prompt string) llm.Request {
	t.Helper()
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: instruction},
		{Role: llm.RoleUser, Content: prompt},
	}
	for _, m := range messages {
		if err := s.Append(m); err != nil {
			t.Fatalf("session.Append: %v", err)
		}
	}
	return llm.Request{Model: "m", Messages: messages}
}

func TestFirstTurnInjectsNoHistory(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	m := New(inner, s)

	req := simulateTurn(t, s, "sys", "hello")
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if inner.gotReq.Model != req.Model {
		t.Errorf("model = %q, want %q", inner.gotReq.Model, req.Model)
	}
	if !reflect.DeepEqual(inner.gotReq.Messages, req.Messages) {
		t.Errorf("first turn messages changed:\n got %+v\nwant %+v", inner.gotReq.Messages, req.Messages)
	}
}

func TestLaterTurnInjectsPriorHistory(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	m := New(inner, s)

	// Turn 1: user asks, assistant replies, tool runs, tool result feeds back.
	simulateTurn(t, s, "sys", "turn one question")
	if err := s.Append(llm.Message{Role: llm.RoleAssistant, Content: "turn one answer"}); err != nil {
		t.Fatal(err)
	}

	// Turn 2: current turn.
	req := simulateTurn(t, s, "sys", "turn two question")
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	want := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "turn one question"},
		{Role: llm.RoleAssistant, Content: "turn one answer"},
		{Role: llm.RoleUser, Content: "turn two question"},
	}
	if !reflect.DeepEqual(inner.gotReq.Messages, want) {
		t.Errorf("later turn request messages:\n got %+v\nwant %+v", inner.gotReq.Messages, want)
	}
}

func TestLaterTurnInjectsToolMessages(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	m := New(inner, s)

	simulateTurn(t, s, "sys", "run a tool")
	if err := s.Append(llm.Message{
		Role:      llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read", Arguments: `{"path":"f"}`}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(llm.Message{
		Role:       llm.RoleTool,
		Content:    "file contents",
		ToolCallID: "call_1",
	}); err != nil {
		t.Fatal(err)
	}

	req := simulateTurn(t, s, "sys", "next turn")
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	want := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "run a tool"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read", Arguments: `{"path":"f"}`}}},
		{Role: llm.RoleTool, Content: "file contents", ToolCallID: "call_1"},
		{Role: llm.RoleUser, Content: "next turn"},
	}
	if !reflect.DeepEqual(inner.gotReq.Messages, want) {
		t.Errorf("tool turn request messages:\n got %+v\nwant %+v", inner.gotReq.Messages, want)
	}
}

// TestToolLoopDoesNotDuplicateCurrentTurn reproduces the intra-turn tool loop:
// the agent streams, the model asks for a tool, the assistant message and tool
// result are appended to the session, and the loop streams again. The second
// stream must not duplicate the current user prompt or the tool result even
// though the assistant tool-call message lives only in the session (never on
// req.Messages).
func TestToolLoopDoesNotDuplicateCurrentTurn(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	m := New(inner, s)

	// Turn start: system + user appended, then streamed.
	simulateTurn(t, s, "sys", "run the tool")

	// The model replies with a tool call, then the tool runs; both land in the
	// session (the assistant message with tool calls is not placed on the
	// request, only the tool result is).
	if err := s.Append(llm.Message{
		Role:      llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read", Arguments: `{"path":"f"}`}},
	}); err != nil {
		t.Fatal(err)
	}
	toolResult := llm.Message{Role: llm.RoleTool, Content: "file contents", ToolCallID: "call_1"}
	if err := s.Append(toolResult); err != nil {
		t.Fatal(err)
	}

	// The agent's request for the second iteration carries only the current
	// user prompt plus the fresh tool result, mirroring its loop bookkeeping.
	req := llm.Request{
		Model:    "m",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "sys"},
			{Role: llm.RoleUser, Content: "run the tool"},
			toolResult,
		},
	}
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	want := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "run the tool"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read", Arguments: `{"path":"f"}`}}},
		toolResult,
	}
	if !reflect.DeepEqual(inner.gotReq.Messages, want) {
		t.Errorf("tool-loop request messages:\n got %+v\nwant %+v", inner.gotReq.Messages, want)
	}
}

func TestStreamSurfacesEventsUnchanged(t *testing.T) {
	s := newSession(t)
	evs := []llm.Event{
		{Kind: llm.EventText, Text: "hello"},
		{Kind: llm.EventUsage, Usage: llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}},
		{Kind: llm.EventFinish, End: llm.FinishStop},
	}
	inner := &mockClient{events: evs}
	m := New(inner, s)

	req := simulateTurn(t, s, "sys", "hi")
	stream, err := m.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var got []llm.Event
	for ev := range stream {
		got = append(got, ev)
	}
	if !reflect.DeepEqual(got, evs) {
		t.Errorf("surfaced events differ:\n got %+v\nwant %+v", got, evs)
	}
}

func TestListModelsPassesThrough(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{models: []llm.Model{{ID: "big-pickle"}, {ID: "gpt-4o"}}}
	m := New(inner, s)

	models, err := m.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if !reflect.DeepEqual(models, inner.models) {
		t.Errorf("models = %+v, want %+v", models, inner.models)
	}
}
