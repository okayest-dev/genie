package contextmgr

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/session"
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
		Model: "m",
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

// TestCrossTurnWindowKeepsRecentTurns is the acceptance for genie-8qu.5: with a
// window of N turns, only the most recent N prior turns are injected. After
// three prior turns with a window of 2, the request carries turns 3 and 2 but
// not turn 1.
func TestCrossTurnWindowKeepsRecentTurns(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	m := New(inner, s, WithTurns(2))

	// Turn 1.
	simulateTurn(t, s, "sys", "turn one")
	if err := s.Append(llm.Message{Role: llm.RoleAssistant, Content: "turn one answer"}); err != nil {
		t.Fatal(err)
	}
	// Turn 2.
	simulateTurn(t, s, "sys", "turn two")
	if err := s.Append(llm.Message{Role: llm.RoleAssistant, Content: "turn two answer"}); err != nil {
		t.Fatal(err)
	}
	// Turn 3.
	simulateTurn(t, s, "sys", "turn three")
	if err := s.Append(llm.Message{Role: llm.RoleAssistant, Content: "turn three answer"}); err != nil {
		t.Fatal(err)
	}
	// Turn 4 (current): the request must carry the last two prior turns
	// (turn 3 and turn 2) but elide turn 1.
	req := simulateTurn(t, s, "sys", "turn four")
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	want := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "turn two"},
		{Role: llm.RoleAssistant, Content: "turn two answer"},
		{Role: llm.RoleUser, Content: "turn three"},
		{Role: llm.RoleAssistant, Content: "turn three answer"},
		{Role: llm.RoleUser, Content: "turn four"},
	}
	if !reflect.DeepEqual(inner.gotReq.Messages, want) {
		t.Errorf("windowed request messages:\n got %+v\nwant %+v", inner.gotReq.Messages, want)
	}
}

// TestCrossTurnWindowZeroMeansUnlimited confirms the default (no option or
// WithTurns(0)) injects every prior turn, so the model can reference turn 1
// after three turns.
func TestCrossTurnWindowZeroMeansUnlimited(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	m := New(inner, s)

	simulateTurn(t, s, "sys", "turn one")
	if err := s.Append(llm.Message{Role: llm.RoleAssistant, Content: "turn one answer"}); err != nil {
		t.Fatal(err)
	}
	simulateTurn(t, s, "sys", "turn two")
	if err := s.Append(llm.Message{Role: llm.RoleAssistant, Content: "turn two answer"}); err != nil {
		t.Fatal(err)
	}
	req := simulateTurn(t, s, "sys", "turn three")
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	want := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "turn one"},
		{Role: llm.RoleAssistant, Content: "turn one answer"},
		{Role: llm.RoleUser, Content: "turn two"},
		{Role: llm.RoleAssistant, Content: "turn two answer"},
		{Role: llm.RoleUser, Content: "turn three"},
	}
	if !reflect.DeepEqual(inner.gotReq.Messages, want) {
		t.Errorf("unlimited request messages:\n got %+v\nwant %+v", inner.gotReq.Messages, want)
	}
}

// TestAssemblySkipsCompactionMarkers verifies a compaction marker line in the
// transcript is never shipped as a message: it is JSONL metadata, part of no
// layer. A resumed session (transcript read from disk via LoadInto) has the
// marker in its in-memory mirror, but request assembly must elide it.
func TestAssemblySkipsCompactionMarkers(t *testing.T) {
	s := buildSession(t,
		[]llm.Message{
			{Role: llm.RoleSystem, Content: "instr"},
			{Role: llm.RoleUser, Content: "q1"},
			{Role: llm.RoleAssistant, Content: "a1"},
		},
		`{"role":"`+markerRole+`","content":"[existing summary]"}`,
	)
	if err := s.LoadInto(); err != nil {
		t.Fatalf("LoadInto: %v", err)
	}

	inner := &mockClient{}
	m := New(inner, s)

	req := simulateTurn(t, s, "instr", "q2")
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	want := []llm.Message{
		{Role: llm.RoleSystem, Content: "instr"},
		{Role: llm.RoleUser, Content: "q1"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleUser, Content: "q2"},
	}
	if !reflect.DeepEqual(inner.gotReq.Messages, want) {
		t.Errorf("request messages:\n got %+v\nwant %+v", inner.gotReq.Messages, want)
	}
}

// TestPriorInstructionsNotShippedCurrentOnce pins the fixed-spine contract:
// only the current turn's instruction ships, and exactly once; prior turns'
// instruction (system) messages are never shipped.
func TestPriorInstructionsNotShippedCurrentOnce(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	m := New(inner, s)

	simulateTurn(t, s, "instr 1", "q1")
	if err := s.Append(llm.Message{Role: llm.RoleAssistant, Content: "a1"}); err != nil {
		t.Fatal(err)
	}
	simulateTurn(t, s, "instr 2", "q2")
	if err := s.Append(llm.Message{Role: llm.RoleAssistant, Content: "a2"}); err != nil {
		t.Fatal(err)
	}
	req := simulateTurn(t, s, "instr 3", "q3")
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	want := []llm.Message{
		{Role: llm.RoleSystem, Content: "instr 3"},
		{Role: llm.RoleUser, Content: "q1"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleUser, Content: "q2"},
		{Role: llm.RoleAssistant, Content: "a2"},
		{Role: llm.RoleUser, Content: "q3"},
	}
	if !reflect.DeepEqual(inner.gotReq.Messages, want) {
		t.Errorf("request messages:\n got %+v\nwant %+v", inner.gotReq.Messages, want)
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

// fakeCounter is a scripted tokens.Counter: every character counts as one
// token, so tests can predict counts exactly.
type fakeCounter struct {
	models []string
}

func (f *fakeCounter) Count(model, text string) int {
	f.models = append(f.models, model)
	return len(text)
}

func TestStreamTracksTokenCount(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	c := &fakeCounter{}
	m := New(inner, s, WithCounter(c))

	req := simulateTurn(t, s, "sys", "hello")
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// The count covers the outgoing messages: instruction + prompt.
	want := len("sys") + len("hello")
	if got := m.Tokens(); got != want {
		t.Errorf("Tokens = %d, want %d", got, want)
	}
	// Counting happens for the active model.
	for _, model := range c.models {
		if model != req.Model {
			t.Errorf("counted under model %q, want %q", model, req.Model)
		}
	}
}

func TestStreamTracksTokenCountAcrossToolLoop(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	m := New(inner, s, WithCounter(&fakeCounter{}))

	simulateTurn(t, s, "sys", "run a tool")
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
	req := llm.Request{
		Model: "m",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "sys"},
			{Role: llm.RoleUser, Content: "run a tool"},
			toolResult,
		},
	}
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// The count reflects the full outgoing request including the tool result
	// and the assistant tool-call arguments from history.
	want := len("sys") + len("run a tool") + len(`{"path":"f"}`) + len("file contents")
	if got := m.Tokens(); got != want {
		t.Errorf("Tokens = %d, want %d", got, want)
	}
}

func TestTokensZeroWithoutCounter(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	m := New(inner, s)

	req := simulateTurn(t, s, "sys", "hello")
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := m.Tokens(); got != 0 {
		t.Errorf("Tokens = %d, want 0 without a Counter attached", got)
	}
}

func TestTokensUntrackedBeforeFirstStream(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	m := New(inner, s, WithCounter(&fakeCounter{}))
	if got := m.Tokens(); got != 0 {
		t.Errorf("Tokens = %d, want 0 before any stream", got)
	}
}

// fakeHooks is a scriptable Hooks implementation for exercising the seam.
type fakeHooks struct {
	before        func(llm.Request) (llm.Request, error)
	after         func(llm.Request, llm.Usage) error
	compact       func(llm.Request) (llm.Request, error)
	condense      func(llm.Request) (llm.Request, error)
	afterGotUsage llm.Usage
}

func (f *fakeHooks) BeforeRequest(_ context.Context, req llm.Request) (llm.Request, error) {
	if f.before == nil {
		return req, nil
	}
	return f.before(req)
}

func (f *fakeHooks) AfterResponse(_ context.Context, req llm.Request, usage llm.Usage) error {
	f.afterGotUsage = usage
	if f.after == nil {
		return nil
	}
	return f.after(req, usage)
}

func (f *fakeHooks) Compact(_ context.Context, req llm.Request) (llm.Request, error) {
	if f.compact == nil {
		return req, nil
	}
	return f.compact(req)
}

func (f *fakeHooks) Condense(_ context.Context, req llm.Request) (llm.Request, error) {
	if f.condense == nil {
		return req, nil
	}
	return f.condense(req)
}

// TestBeforeRequestChainRewrites verifies a plugin before_request hook can
// rewrite the message list on top of injected history, and that its result is
// what reaches the inner client.
func TestBeforeRequestChainRewrites(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	hooks := &fakeHooks{
		before: func(req llm.Request) (llm.Request, error) {
			for i := range req.Messages {
				if req.Messages[i].Role == llm.RoleUser {
					req.Messages[i].Content += " [annotated]"
				}
			}
			return req, nil
		},
	}
	m := New(inner, s, WithHooks(hooks))

	req := simulateTurn(t, s, "sys", "turn one")
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	want := llm.Message{Role: llm.RoleUser, Content: "turn one [annotated]"}
	if !reflect.DeepEqual(inner.gotReq.Messages[1], want) {
		t.Errorf("before_request rewrite not forwarded:\n got %+v\nwant %+v", inner.gotReq.Messages[1], want)
	}
}

// TestAfterResponseObservesUsage verifies the after_response hook observes the
// completed stream's usage event.
func TestAfterResponseObservesUsage(t *testing.T) {
	s := newSession(t)
	evs := []llm.Event{
		{Kind: llm.EventText, Text: "hi"},
		{Kind: llm.EventUsage, Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
		{Kind: llm.EventFinish, End: llm.FinishStop},
	}
	inner := &mockClient{events: evs}
	hooks := &fakeHooks{}
	m := New(inner, s, WithHooks(hooks))

	req := simulateTurn(t, s, "sys", "hi")
	stream, err := m.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream {
	}
	want := llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	if !reflect.DeepEqual(hooks.afterGotUsage, want) {
		t.Errorf("after_response observed usage %+v, want %+v", hooks.afterGotUsage, want)
	}
}

// TestBeforeHookFailureDegradesGracefully verifies a failing before_request
// hook logs, skips its contribution (request proceeds unchanged), and surfaces
// a visible degradation message, without failing the stream.
func TestBeforeHookFailureDegradesGracefully(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	hooks := &fakeHooks{
		before: func(req llm.Request) (llm.Request, error) {
			return req, errors.New("boom")
		},
	}
	var degraded []string
	m := New(inner, s, WithHooks(hooks), WithOnDegrade(func(msg string) { degraded = append(degraded, msg) }))

	req := simulateTurn(t, s, "sys", "hi")
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream should not fail on hook error: %v", err)
	}
	if len(degraded) != 1 {
		t.Fatalf("expected one degradation message, got %d", len(degraded))
	}
	if !strings.Contains(degraded[0], "before_request") {
		t.Errorf("degradation message %q should mention the failing hook", degraded[0])
	}
	if !reflect.DeepEqual(inner.gotReq.Messages, req.Messages) {
		t.Errorf("request should proceed unchanged on hook failure:\n got %+v\nwant %+v", inner.gotReq.Messages, req.Messages)
	}
}

// TestCompactAndCondenseInvoked verifies the active single-active compact and
// condense hooks are invoked in the documented order (compact then condense)
// before the request is forwarded.
func TestCompactAndCondenseInvoked(t *testing.T) {
	s := newSession(t)
	inner := &mockClient{}
	var order []string
	hooks := &fakeHooks{
		compact: func(req llm.Request) (llm.Request, error) {
			order = append(order, "compact")
			return req, nil
		},
		condense: func(req llm.Request) (llm.Request, error) {
			order = append(order, "condense")
			return req, nil
		},
	}
	m := New(inner, s, WithHooks(hooks))

	req := simulateTurn(t, s, "sys", "hi")
	if _, err := m.Stream(context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := []string{"compact", "condense"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("hook order %v, want %v", order, want)
	}
}
