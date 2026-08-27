package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"log/slog"
	"strings"
	"testing"

	"github.com/okayest-dev/og/internal/llm"
	"github.com/okayest-dev/og/internal/tools"
)

type mockClient struct {
	events []llm.Event
}

func (m *mockClient) Stream(_ context.Context, _ llm.Request) (iter.Seq[llm.Event], error) {
	return func(yield func(llm.Event) bool) {
		for _, ev := range m.events {
			if !yield(ev) {
				return
			}
		}
	}, nil
}

func (m *mockClient) ListModels(_ context.Context) ([]llm.Model, error) {
	return nil, nil
}

func captureInfo(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() {
		slog.SetDefault(slog.New(slog.NewTextHandler(nil, &slog.HandlerOptions{Level: slog.LevelWarn})))
	})
	return &buf
}

func TestRunTurnLogsTurnStarted(t *testing.T) {
	buf := captureInfo(t)
	c := &mockClient{events: []llm.Event{
		{Kind: llm.EventText, Text: "hello"},
		{Kind: llm.EventFinish, End: llm.FinishStop},
	}}
	var out bytes.Buffer
	if err := RunTurn(context.Background(), c, "test-model", "sys", "hi", &out, nil, nil, nil, nil, ""); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if !strings.Contains(buf.String(), "turn started") {
		t.Errorf("log output missing 'turn started':\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "model=test-model") {
		t.Errorf("log output missing model:\n%s", buf.String())
	}
}

func TestRunTurnLogsTurnCompleted(t *testing.T) {
	buf := captureInfo(t)
	c := &mockClient{events: []llm.Event{
		{Kind: llm.EventText, Text: "ok"},
		{Kind: llm.EventFinish, End: llm.FinishStop},
		{Kind: llm.EventUsage, Usage: llm.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8}},
	}}
	var out bytes.Buffer
	if err := RunTurn(context.Background(), c, "m", "sys", "prompt", &out, nil, nil, nil, nil, ""); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "turn completed") {
		t.Errorf("log output missing 'turn completed':\n%s", logged)
	}
	if !strings.Contains(logged, "finish_reason=stop") {
		t.Errorf("log output missing finish_reason:\n%s", logged)
	}
	if !strings.Contains(logged, "total_tokens=8") {
		t.Errorf("log output missing total_tokens:\n%s", logged)
	}
}

func TestRunTurnToolCallsExecutedSerially(t *testing.T) {
	// Simulate: model returns a tool call, then after tool result, returns text.
	var calls int
	mock := &mockStreamClient{
		streamFunc: func(_ context.Context, _ llm.Request) (iter.Seq[llm.Event], error) {
			calls++
			if calls == 1 {
				return func(yield func(llm.Event) bool) {
					yield(llm.Event{Kind: llm.EventToolCall, ToolCalls: []llm.ToolCall{
						{ID: "call_1", Name: "echo", Arguments: `{"msg":"hello"}`},
					}})
					yield(llm.Event{Kind: llm.EventFinish, End: llm.FinishToolCalls})
				}, nil
			}
			return func(yield func(llm.Event) bool) {
				yield(llm.Event{Kind: llm.EventText, Text: "result"})
				yield(llm.Event{Kind: llm.EventFinish, End: llm.FinishStop})
			}, nil
		},
	}

	// Build a registry with an echo tool.
	reg := newTestRegistry(t)

	var out, errOut bytes.Buffer
	if err := RunTurn(context.Background(), mock, "m", "sys", "hi", &out, &errOut, nil, reg, nil, ""); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if out.String() != "result\n" {
		t.Errorf("stdout = %q, want %q", out.String(), "result\n")
	}
	// Check that the tool framing appeared on stderr.
	if !strings.Contains(errOut.String(), "── echo ") {
		t.Errorf("stderr missing tool framing: %q", errOut.String())
	}
}

func TestRunTurnDisabledToolReturnsError(t *testing.T) {
	var calls int
	mock := &mockStreamClient{
		streamFunc: func(_ context.Context, _ llm.Request) (iter.Seq[llm.Event], error) {
			calls++
			if calls == 1 {
				return func(yield func(llm.Event) bool) {
					yield(llm.Event{Kind: llm.EventToolCall, ToolCalls: []llm.ToolCall{
						{ID: "call_1", Name: "bash", Arguments: `{}`},
					}})
					yield(llm.Event{Kind: llm.EventFinish, End: llm.FinishToolCalls})
				}, nil
			}
			return func(yield func(llm.Event) bool) {
				yield(llm.Event{Kind: llm.EventText, Text: "saw the error"})
				yield(llm.Event{Kind: llm.EventFinish, End: llm.FinishStop})
			}, nil
		},
	}

	reg := newTestRegistry(t)

	var out bytes.Buffer
	if err := RunTurn(context.Background(), mock, "m", "sys", "hi", &out, nil, nil, reg, nil, ""); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	// The disabled tool error should flow back to the model, which then replies.
	if out.String() != "saw the error\n" {
		t.Errorf("stdout = %q, want %q", out.String(), "saw the error\n")
	}
}

func TestRunTurnMalformedArgsRejected(t *testing.T) {
	var calls int
	mock := &mockStreamClient{
		streamFunc: func(_ context.Context, _ llm.Request) (iter.Seq[llm.Event], error) {
			calls++
			if calls == 1 {
				return func(yield func(llm.Event) bool) {
					yield(llm.Event{Kind: llm.EventToolCall, ToolCalls: []llm.ToolCall{
						{ID: "call_1", Name: "read", Arguments: `not json`},
					}})
					yield(llm.Event{Kind: llm.EventFinish, End: llm.FinishToolCalls})
				}, nil
			}
			return func(yield func(llm.Event) bool) {
				yield(llm.Event{Kind: llm.EventText, Text: "handled"})
				yield(llm.Event{Kind: llm.EventFinish, End: llm.FinishStop})
			}, nil
		},
	}

	reg := newTestRegistry(t)

	var out bytes.Buffer
	if err := RunTurn(context.Background(), mock, "m", "sys", "hi", &out, nil, nil, reg, nil, ""); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if out.String() != "handled\n" {
		t.Errorf("stdout = %q, want %q", out.String(), "handled\n")
	}
}

// mockStreamClient allows specifying the stream function per call.
type mockStreamClient struct {
	streamFunc func(context.Context, llm.Request) (iter.Seq[llm.Event], error)
}

func (m *mockStreamClient) Stream(ctx context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	return m.streamFunc(ctx, req)
}

func (m *mockStreamClient) ListModels(_ context.Context) ([]llm.Model, error) {
	return nil, nil
}

// newTestRegistry creates a registry with read, echo, and a disabled bash tool.
func newTestRegistry(t *testing.T) *tools.Registry {
	t.Helper()
	reg := tools.NewRegistry()
	reg.Register(&readtoolStub{})
	reg.Register(&echoStub{})
	reg.Register(&bashStub{})
	reg.Disable("bash")
	return reg
}

type readtoolStub struct{}

func (r *readtoolStub) Name() string            { return "read" }
func (r *readtoolStub) Description() string     { return "Read a file" }
func (r *readtoolStub) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []any{"path"},
	}
}
func (r *readtoolStub) Execute(_ json.RawMessage) (string, error) {
	return "file contents", nil
}

type echoStub struct{}

func (e *echoStub) Name() string            { return "echo" }
func (e *echoStub) Description() string     { return "Echo input" }
func (e *echoStub) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (e *echoStub) Execute(_ json.RawMessage) (string, error) {
	return "echoed", nil
}

type bashStub struct{}

func (b *bashStub) Name() string            { return "bash" }
func (b *bashStub) Description() string     { return "Run bash" }
func (b *bashStub) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (b *bashStub) Execute(_ json.RawMessage) (string, error) {
	return "output", nil
}
