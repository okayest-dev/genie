package google

import (
	"testing"

	"github.com/okayest-dev/og/internal/llm"
)

func TestParseChunkText(t *testing.T) {
	for _, tc := range []struct {
		name string
		ch   chunk
		want []llm.Event
	}{
		{
			name: "single text delta",
			ch: chunk{Candidates: []candidate{{
				Content: responseContent{Parts: []responsePart{{Text: "Hello"}}},
			}}},
			want: []llm.Event{{Kind: llm.EventText, Text: "Hello"}},
		},
		{
			name: "empty text emits nothing",
			ch:   chunk{Candidates: []candidate{{Content: responseContent{Parts: []responsePart{{Text: ""}}}}}},
			want: nil,
		},
		{
			name: "multiple text parts",
			ch: chunk{Candidates: []candidate{{
				Content: responseContent{Parts: []responsePart{{Text: "A"}, {Text: "B"}}},
			}}},
			want: []llm.Event{
				{Kind: llm.EventText, Text: "A"},
				{Kind: llm.EventText, Text: "B"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseChunk(tc.ch)
			assertEvents(t, got, tc.want)
		})
	}
}

func TestParseChunkFunctionCall(t *testing.T) {
	ch := chunk{Candidates: []candidate{{
		Content: responseContent{Parts: []responsePart{{
			FunctionCall: functionCall{Name: "read_file", Args: map[string]any{"path": "/foo.go"}},
		}}},
	}}}
	got := parseChunk(ch)
	if len(got) != 1 || got[0].Kind != llm.EventToolCall {
		t.Fatalf("events = %+v, want one tool call event", got)
	}
	if got[0].ToolCalls[0].Name != "read_file" {
		t.Errorf("name = %q, want read_file", got[0].ToolCalls[0].Name)
	}
}

func TestParseChunkFinishReason(t *testing.T) {
	for _, tc := range []struct {
		wire string
		want llm.FinishReason
	}{
		{"STOP", llm.FinishStop},
		{"MAX_TOKENS", llm.FinishLength},
		{"SAFETY", llm.FinishOther},
		{"FINISH_REASON_UNSPECIFIED", llm.FinishOther},
		{"OTHER_REASON", llm.FinishOther},
	} {
		reason := tc.wire
		got := parseChunk(chunk{Candidates: []candidate{{FinishReason: reason}}})
		if len(got) != 1 || got[0].Kind != llm.EventFinish || got[0].End != tc.want {
			t.Errorf("finish %q: events = %+v, want finish %s", tc.wire, got, tc.want)
		}
	}
}

func TestParseChunkUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		ch   chunk
		want llm.Usage
	}{
		{
			name: "usage alone",
			ch:   chunk{UsageMetadata: &usageMetadata{PromptTokenCount: 9, CandidatesTokenCount: 3, TotalTokenCount: 12}},
			want: llm.Usage{PromptTokens: 9, CompletionTokens: 3, TotalTokens: 12},
		},
		{
			name: "usage alongside candidate",
			ch: chunk{
				Candidates:   []candidate{{Content: responseContent{Parts: []responsePart{{Text: "hi"}}}}},
				UsageMetadata: &usageMetadata{PromptTokenCount: 5, CandidatesTokenCount: 2, TotalTokenCount: 7},
			},
			want: llm.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseChunk(tc.ch)
			var found bool
			for _, e := range got {
				if e.Kind == llm.EventUsage {
					if e.Usage != tc.want {
						t.Errorf("usage = %+v, want %+v", e.Usage, tc.want)
					}
					found = true
				}
			}
			if !found {
				t.Errorf("no usage event in %+v", got)
			}
		})
	}
}

func TestParseChunkError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ch      chunk
		wantMsg string
	}{
		{
			name:    "error with message",
			ch:      chunk{Error: &wireError{Message: "stream blew up"}},
			wantMsg: "stream blew up",
		},
		{
			name:    "error without message",
			ch:      chunk{Error: &wireError{}},
			wantMsg: "provider error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseChunk(tc.ch)
			if len(got) != 1 || got[0].Kind != llm.EventError {
				t.Fatalf("events = %+v, want one error event", got)
			}
			if got[0].Err == nil || got[0].Err.Error() != tc.wantMsg {
				t.Errorf("error = %v, want %q", got[0].Err, tc.wantMsg)
			}
		})
	}
}

func TestCanonicalFinishReason(t *testing.T) {
	for _, tc := range []struct {
		wire string
		want llm.FinishReason
	}{
		{"STOP", llm.FinishStop},
		{"MAX_TOKENS", llm.FinishLength},
		{"SAFETY", llm.FinishOther},
		{"", llm.FinishOther},
	} {
		got := canonicalFinishReason(tc.wire)
		if got != tc.want {
			t.Errorf("canonicalFinishReason(%q) = %q, want %q", tc.wire, got, tc.want)
		}
	}
}

// assertEvents checks that got matches want in kind and key fields.
func assertEvents(t *testing.T, got, want []llm.Event) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("events = %d, want %d: %+v vs %+v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i].Kind != want[i].Kind {
			t.Errorf("event[%d].Kind = %v, want %v", i, got[i].Kind, want[i].Kind)
		}
		if want[i].Text != "" && got[i].Text != want[i].Text {
			t.Errorf("event[%d].Text = %q, want %q", i, got[i].Text, want[i].Text)
		}
	}
}
