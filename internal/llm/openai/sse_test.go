package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/okayest-dev/og/internal/llm"
)

func TestParseChunkTextDelta(t *testing.T) {
	ev := parseChunk(chunk{
		Choices: []choice{{Delta: delta{Content: strPtr("Hello")}}},
	})
	if len(ev) != 1 {
		t.Fatalf("events = %d, want 1", len(ev))
	}
	if ev[0].Kind != llm.EventText || ev[0].Text != "Hello" {
		t.Errorf("event = %+v, want text delta %q", ev[0], "Hello")
	}
}

func TestParseChunkEmptyContentEmitsNothing(t *testing.T) {
	ev := parseChunk(chunk{Choices: []choice{{Delta: delta{Content: strPtr("")}}}})
	if len(ev) != 0 {
		t.Errorf("events = %d, want 0", len(ev))
	}
}

func TestParseChunkNullContentEmitsNothing(t *testing.T) {
	ev := parseChunk(chunk{Choices: []choice{{Delta: delta{}}}})
	if len(ev) != 0 {
		t.Errorf("events = %d, want 0", len(ev))
	}
}

func TestParseChunkFinishReason(t *testing.T) {
	for _, tc := range []struct {
		wire string
		want llm.FinishReason
	}{
		{wire: "stop", want: llm.FinishStop},
		{wire: "tool_calls", want: llm.FinishToolCalls},
		{wire: "length", want: llm.FinishLength},
		{wire: "content_filter", want: llm.FinishOther},
	} {
		reason := tc.wire
		ev := parseChunk(chunk{Choices: []choice{{FinishReason: &reason}}})
		if len(ev) != 1 || ev[0].Kind != llm.EventFinish || ev[0].End != tc.want {
			t.Errorf("finish %q: events = %+v, want finish %s", tc.wire, ev, tc.want)
		}
	}
}

func TestParseChunkUsage(t *testing.T) {
	ev := parseChunk(chunk{Usage: &usage{PromptTokens: 9, CompletionTokens: 3, TotalTokens: 12}})
	if len(ev) != 1 || ev[0].Kind != llm.EventUsage {
		t.Fatalf("events = %+v, want one usage event", ev)
	}
	if ev[0].Usage != (llm.Usage{PromptTokens: 9, CompletionTokens: 3, TotalTokens: 12}) {
		t.Errorf("usage = %+v", ev[0].Usage)
	}
}

func TestParseChunkUsageAlongsideChoicesIsIgnored(t *testing.T) {
	ev := parseChunk(chunk{
		Choices: []choice{{Delta: delta{Content: strPtr("hi")}}},
		Usage:   &usage{TotalTokens: 5},
	})
	for _, e := range ev {
		if e.Kind == llm.EventUsage {
			t.Errorf("usage event emitted alongside choices: %+v", ev)
		}
	}
}

func TestParseChunkError(t *testing.T) {
	ev := parseChunk(chunk{Error: &wireError{Message: "stream blew up"}})
	if len(ev) != 1 || ev[0].Kind != llm.EventError {
		t.Fatalf("events = %+v, want one error event", ev)
	}
	if ev[0].Err == nil || ev[0].Err.Error() != "stream blew up" {
		t.Errorf("error = %v, want %q", ev[0].Err, "stream blew up")
	}
}

func TestParseChunkErrorWithoutMessage(t *testing.T) {
	ev := parseChunk(chunk{Error: &wireError{}})
	if len(ev) != 1 || ev[0].Err == nil || ev[0].Err.Error() != "provider error" {
		t.Errorf("events = %+v, want provider error", ev)
	}
}

func strPtr(s string) *string { return &s }

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "big-pickle", "object": "model", "owned_by": "opencode"},
				{"id": "deepseek-v4-flash-free", "object": "model", "owned_by": "opencode"},
			},
		})
	}))
	defer srv.Close()

	models, err := NewClient(srv.URL, "").ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0].ID != "big-pickle" || models[1].ID != "deepseek-v4-flash-free" {
		t.Errorf("models = %+v, want the two catalog ids", models)
	}
}
