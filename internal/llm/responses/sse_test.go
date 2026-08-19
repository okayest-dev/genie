package responses

import (
	"testing"

	"github.com/okayest-dev/og/internal/llm"
)

func TestParseEvent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		evt     sseEvent
		want    []llm.Event
		wantErr bool
	}{
		{
			name: "text delta",
			evt: sseEvent{
				event: "response.output_text.delta",
				data:  []byte(`{"type":"response.output_text.delta","delta":"Hello"}`),
			},
			want: []llm.Event{{Kind: llm.EventText, Text: "Hello"}},
		},
		{
			name: "empty text delta emits nothing",
			evt: sseEvent{
				event: "response.output_text.delta",
				data:  []byte(`{"type":"response.output_text.delta","delta":""}`),
			},
			want: nil,
		},
		{
			name: "function call arguments delta emits nothing",
			evt: sseEvent{
				event: "response.function_call_arguments.delta",
				data:  []byte(`{"type":"response.function_call_arguments.delta","delta":"{\"loc"}`),
			},
			want: nil,
		},
		{
			name: "completed with usage",
			evt: sseEvent{
				event: "response.completed",
				data: []byte(`{
					"type": "response.completed",
					"response": {
						"status": "completed",
						"usage": {
							"input_tokens": 37,
							"output_tokens": 11,
							"total_tokens": 48
						}
					}
				}`),
			},
			want: []llm.Event{
				{Kind: llm.EventUsage, Usage: llm.Usage{PromptTokens: 37, CompletionTokens: 11, TotalTokens: 48}},
				{Kind: llm.EventFinish, End: llm.FinishStop},
			},
		},
		{
			name: "completed without usage",
			evt: sseEvent{
				event: "response.completed",
				data:  []byte(`{"type":"response.completed","response":{"status":"completed"}}`),
			},
			want: []llm.Event{{Kind: llm.EventFinish, End: llm.FinishStop}},
		},
		{
			name: "failed with error message",
			evt: sseEvent{
				event: "response.failed",
				data:  []byte(`{"type":"response.failed","response":{"error":{"message":"server overloaded"}}}`),
			},
			want: []llm.Event{{Kind: llm.EventError, Err: &llm.ProviderError{Kind: llm.KindOther, Message: "server overloaded"}}},
		},
		{
			name: "failed without error message",
			evt: sseEvent{
				event: "response.failed",
				data:  []byte(`{"type":"response.failed","response":{}}`),
			},
			want: []llm.Event{{Kind: llm.EventError, Err: &llm.ProviderError{Kind: llm.KindOther, Message: "response failed"}}},
		},
		{
			name: "incomplete max_output_tokens",
			evt: sseEvent{
				event: "response.incomplete",
				data:  []byte(`{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`),
			},
			want: []llm.Event{{Kind: llm.EventFinish, End: llm.FinishLength}},
		},
		{
			name: "incomplete content_filter",
			evt: sseEvent{
				event: "response.incomplete",
				data:  []byte(`{"type":"response.incomplete","response":{"incomplete_details":{"reason":"content_filter"}}}`),
			},
			want: []llm.Event{{Kind: llm.EventFinish, End: llm.FinishLength}},
		},
		{
			name: "error event with message",
			evt: sseEvent{
				event: "error",
				data:  []byte(`{"type":"error","message":"Something went wrong"}`),
			},
			want: []llm.Event{{Kind: llm.EventError, Err: &llm.ProviderError{Kind: llm.KindOther, Message: "Something went wrong"}}},
		},
		{
			name: "error event without message",
			evt: sseEvent{
				event: "error",
				data:  []byte(`{"type":"error"}`),
			},
			want: []llm.Event{{Kind: llm.EventError, Err: &llm.ProviderError{Kind: llm.KindOther, Message: "provider error"}}},
		},
		{
			name: "unknown event type emits nothing",
			evt: sseEvent{
				event: "response.created",
				data:  []byte(`{"type":"response.created"}`),
			},
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseEvent(tc.evt)
			if len(got) != len(tc.want) {
				t.Fatalf("events = %d, want %d: %+v vs %+v", len(got), len(tc.want), got, tc.want)
			}
			for i := range got {
				if got[i].Kind != tc.want[i].Kind {
					t.Errorf("event[%d].Kind = %v, want %v", i, got[i].Kind, tc.want[i].Kind)
				}
				if tc.want[i].Text != "" && got[i].Text != tc.want[i].Text {
					t.Errorf("event[%d].Text = %q, want %q", i, got[i].Text, tc.want[i].Text)
				}
				if tc.want[i].Usage != (llm.Usage{}) && got[i].Usage != tc.want[i].Usage {
					t.Errorf("event[%d].Usage = %+v, want %+v", i, got[i].Usage, tc.want[i].Usage)
				}
				if tc.want[i].End != "" && got[i].End != tc.want[i].End {
					t.Errorf("event[%d].End = %q, want %q", i, got[i].End, tc.want[i].End)
				}
				if tc.want[i].Err != nil && got[i].Err.Error() != tc.want[i].Err.Error() {
					t.Errorf("event[%d].Err = %v, want %v", i, got[i].Err, tc.want[i].Err)
				}
			}
		})
	}
}

func TestCanonicalFinishReason(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   llm.FinishReason
	}{
		{"completed", llm.FinishStop},
		{"incomplete", llm.FinishLength},
		{"failed", llm.FinishOther},
		{"", llm.FinishOther},
		{"unknown", llm.FinishOther},
	} {
		got := canonicalFinishReason(tc.status)
		if got != tc.want {
			t.Errorf("canonicalFinishReason(%q) = %q, want %q", tc.status, got, tc.want)
		}
	}
}
