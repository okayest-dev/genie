package agent

import (
	"bytes"
	"context"
	"iter"
	"log/slog"
	"strings"
	"testing"

	"github.com/okayest-dev/og/internal/llm"
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
	if err := RunTurn(context.Background(), c, "test-model", "sys", "hi", &out, nil); err != nil {
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
	if err := RunTurn(context.Background(), c, "m", "sys", "prompt", &out, nil); err != nil {
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
