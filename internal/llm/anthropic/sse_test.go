package anthropic

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/okayest-dev/og/internal/llm"
)

func TestParseEventTextDelta(t *testing.T) {
	data := json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`)
	ev := parseEvent(sseEvent{EventType: "content_block_delta", Data: data})
	if len(ev) != 1 || ev[0].Kind != llm.EventText || ev[0].Text != "Hello" {
		t.Errorf("events = %+v, want text delta %q", ev, "Hello")
	}
}

func TestParseEventEmptyTextEmitsNothing(t *testing.T) {
	data := json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`)
	ev := parseEvent(sseEvent{EventType: "content_block_delta", Data: data})
	if len(ev) != 0 {
		t.Errorf("events = %d, want 0", len(ev))
	}
}

func TestParseEventPingEmitsNothing(t *testing.T) {
	ev := parseEvent(sseEvent{EventType: "ping", Data: json.RawMessage(`{}`)})
	if len(ev) != 0 {
		t.Errorf("events = %d, want 0", len(ev))
	}
}

func TestParseEventFinishReason(t *testing.T) {
	for _, tc := range []struct {
		wire string
		want llm.FinishReason
	}{
		{wire: "end_turn", want: llm.FinishStop},
		{wire: "stop_sequence", want: llm.FinishStop},
		{wire: "tool_use", want: llm.FinishToolCalls},
		{wire: "max_tokens", want: llm.FinishLength},
		{wire: "unknown_reason", want: llm.FinishOther},
	} {
		stopReason := tc.wire
		md := messageDelta{}
		md.Delta.StopReason = &stopReason
		data, _ := json.Marshal(md)
		ev := parseEvent(sseEvent{EventType: "message_delta", Data: data})
		found := false
		for _, e := range ev {
			if e.Kind == llm.EventFinish {
				found = true
				if e.End != tc.want {
					t.Errorf("stop_reason %q: got %s, want %s", tc.wire, e.End, tc.want)
				}
			}
		}
		if !found {
			t.Errorf("stop_reason %q: no EventFinish in events = %+v", tc.wire, ev)
		}
	}
}

func TestParseEventUsage(t *testing.T) {
	data := json.RawMessage(`{"type":"message_delta","delta":{},"usage":{"output_tokens":42}}`)
	ev := parseEvent(sseEvent{EventType: "message_delta", Data: data})
	if len(ev) != 1 || ev[0].Kind != llm.EventUsage {
		t.Fatalf("events = %+v, want one usage event", ev)
	}
	if ev[0].Usage.CompletionTokens != 42 {
		t.Errorf("usage = %+v, want CompletionTokens=42", ev[0].Usage)
	}
}

func TestParseEventUsageAndFinish(t *testing.T) {
	data := json.RawMessage(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}`)
	ev := parseEvent(sseEvent{EventType: "message_delta", Data: data})
	if len(ev) != 2 {
		t.Fatalf("events = %d, want 2 (usage + finish)", len(ev))
	}
	if ev[0].Kind != llm.EventUsage || ev[1].Kind != llm.EventFinish {
		t.Errorf("kinds = [%v, %v], want [EventUsage, EventFinish]", ev[0].Kind, ev[1].Kind)
	}
}

func TestParseEventError(t *testing.T) {
	data := json.RawMessage(`{"type":"error","error":{"type":"authentication_error","message":"invalid key"}}`)
	ev := parseEvent(sseEvent{EventType: "error", Data: data})
	if len(ev) != 1 || ev[0].Kind != llm.EventError {
		t.Fatalf("events = %+v, want one error event", ev)
	}
	pe, ok := ev[0].Err.(*llm.ProviderError)
	if !ok {
		t.Fatalf("error type = %T, want *ProviderError", ev[0].Err)
	}
	if pe.Kind != llm.KindAuth || pe.Message != "invalid key" {
		t.Errorf("error = %+v, want kind=auth message='invalid key'", pe)
	}
}

func TestParseEventErrorWithoutMessage(t *testing.T) {
	data := json.RawMessage(`{"type":"error","error":{"type":"api_error","message":""}}`)
	ev := parseEvent(sseEvent{EventType: "error", Data: data})
	if len(ev) != 1 || ev[0].Err == nil {
		t.Fatalf("events = %+v, want one error event", ev)
	}
	if ev[0].Err.Error() != "stream error" {
		t.Errorf("error = %v, want 'stream error'", ev[0].Err)
	}
}

func TestParseEventContentBlockStartIgnored(t *testing.T) {
	data := json.RawMessage(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	ev := parseEvent(sseEvent{EventType: "content_block_start", Data: data})
	if len(ev) != 0 {
		t.Errorf("events = %d, want 0", len(ev))
	}
}

func TestParseEventContentBlockStopIgnored(t *testing.T) {
	data := json.RawMessage(`{"type":"content_block_stop","index":0}`)
	ev := parseEvent(sseEvent{EventType: "content_block_stop", Data: data})
	if len(ev) != 0 {
		t.Errorf("events = %d, want 0", len(ev))
	}
}

func TestParseEventMessageStartIgnored(t *testing.T) {
	data := json.RawMessage(`{"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[],"model":"claude-3","stop_reason":null,"usage":{"input_tokens":25,"output_tokens":1}}}`)
	ev := parseEvent(sseEvent{EventType: "message_start", Data: data})
	if len(ev) != 0 {
		t.Errorf("events = %d, want 0", len(ev))
	}
}

func TestParseEventMessageStopIgnored(t *testing.T) {
	data := json.RawMessage(`{"type":"message_stop"}`)
	ev := parseEvent(sseEvent{EventType: "message_stop", Data: data})
	if len(ev) != 0 {
		t.Errorf("events = %d, want 0", len(ev))
	}
}

func TestParseEventInputJSONDeltaIgnored(t *testing.T) {
	data := json.RawMessage(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"temp\":22}"}}`)
	ev := parseEvent(sseEvent{EventType: "content_block_delta", Data: data})
	if len(ev) != 0 {
		t.Errorf("events = %d, want 0 (input_json_delta accumulated by caller)", len(ev))
	}
}

func TestParseEventThinkingDeltaIgnored(t *testing.T) {
	data := json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`)
	ev := parseEvent(sseEvent{EventType: "content_block_delta", Data: data})
	if len(ev) != 0 {
		t.Errorf("events = %d, want 0 (thinking dropped)", len(ev))
	}
}

func TestParseSSELines(t *testing.T) {
	input := "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n"
	sc := newSSEScanner(strings.NewReader(input))

	evt1, err := parseSSELines(sc)
	if err != nil {
		t.Fatalf("event 1: %v", err)
	}
	if evt1.EventType != "message_start" {
		t.Errorf("event 1 type = %q, want message_start", evt1.EventType)
	}

	evt2, err := parseSSELines(sc)
	if err != nil {
		t.Fatalf("event 2: %v", err)
	}
	if evt2.EventType != "content_block_delta" {
		t.Errorf("event 2 type = %q, want content_block_delta", evt2.EventType)
	}

	_, err = parseSSELines(sc)
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestCanonicalFinishReason(t *testing.T) {
	tests := []struct {
		wire string
		want llm.FinishReason
	}{
		{"end_turn", llm.FinishStop},
		{"stop_sequence", llm.FinishStop},
		{"tool_use", llm.FinishToolCalls},
		{"max_tokens", llm.FinishLength},
		{"something_else", llm.FinishOther},
	}
	for _, tc := range tests {
		got := canonicalFinishReason(tc.wire)
		if got != tc.want {
			t.Errorf("canonicalFinishReason(%q) = %s, want %s", tc.wire, got, tc.want)
		}
	}
}

func TestErrorKindFromType(t *testing.T) {
	tests := []struct {
		wire string
		want llm.ErrorKind
	}{
		{"authentication_error", llm.KindAuth},
		{"invalid_request_error", llm.KindInvalidRequest},
		{"rate_limit_error", llm.KindRateLimit},
		{"overloaded_error", llm.KindRateLimit},
		{"api_error", llm.KindOther},
		{"unknown_error", llm.KindOther},
	}
	for _, tc := range tests {
		got := errorKindFromType(tc.wire)
		if got != tc.want {
			t.Errorf("errorKindFromType(%q) = %s, want %s", tc.wire, got, tc.want)
		}
	}
}
