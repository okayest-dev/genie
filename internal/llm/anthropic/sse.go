package anthropic

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/okayest-dev/og/internal/llm"
)

// sseEvent is one parsed Anthropic SSE frame: an event type and its JSON data.
type sseEvent struct {
	EventType string
	Data      json.RawMessage
}

// Wire types for Anthropic SSE event payloads.

type messageStart struct {
	Type    string `json:"type"`
	Message struct {
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type contentBlockStart struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
}

type contentBlockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
}

type contentBlockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type messageDelta struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason   *string `json:"stop_reason"`
		StopSequence *string `json:"stop_sequence"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type streamError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// parseEvent converts one Anthropic SSE event into normalized events. It is a
// pure function; accumulation state for tool calls is managed by the caller.
func parseEvent(evt sseEvent) []llm.Event {
	switch evt.EventType {
	case "ping":
		return nil
	case "error":
		var se streamError
		if json.Unmarshal(evt.Data, &se) == nil && se.Error.Message != "" {
			return []llm.Event{{Kind: llm.EventError, Err: &llm.ProviderError{
				Kind:    errorKindFromType(se.Error.Type),
				Message: se.Error.Message,
			}}}
		}
		return []llm.Event{{Kind: llm.EventError, Err: &llm.ProviderError{
			Kind:    llm.KindOther,
			Message: "stream error",
		}}}
	case "content_block_delta":
		var cbd contentBlockDelta
		if json.Unmarshal(evt.Data, &cbd) != nil {
			return nil
		}
		if cbd.Delta.Type == "text_delta" && cbd.Delta.Text != "" {
			return []llm.Event{{Kind: llm.EventText, Text: cbd.Delta.Text}}
		}
		return nil
	case "message_delta":
		var md messageDelta
		if json.Unmarshal(evt.Data, &md) != nil {
			return nil
		}
		var events []llm.Event
		if md.Usage.OutputTokens > 0 {
			events = append(events, llm.Event{Kind: llm.EventUsage, Usage: llm.Usage{
				CompletionTokens: md.Usage.OutputTokens,
			}})
		}
		if md.Delta.StopReason != nil {
			events = append(events, llm.Event{Kind: llm.EventFinish, End: canonicalFinishReason(*md.Delta.StopReason)})
		}
		return events
	default:
		return nil
	}
}

// canonicalFinishReason maps an Anthropic stop_reason to the canonical set.
func canonicalFinishReason(reason string) llm.FinishReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return llm.FinishStop
	case "tool_use":
		return llm.FinishToolCalls
	case "max_tokens":
		return llm.FinishLength
	default:
		return llm.FinishOther
	}
}

// errorKindFromType maps an Anthropic error type string to a canonical ErrorKind.
func errorKindFromType(errType string) llm.ErrorKind {
	switch errType {
	case "authentication_error":
		return llm.KindAuth
	case "invalid_request_error":
		return llm.KindInvalidRequest
	case "rate_limit_error", "overloaded_error":
		return llm.KindRateLimit
	case "api_error":
		return llm.KindOther
	default:
		return llm.KindOther
	}
}

// newSSEScanner scans an SSE body line by line, with headroom for long data lines.
func newSSEScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	return sc
}

// parseSSELines reads an SSE stream line by line and returns the next sseEvent.
// It returns io.EOF when the stream ends. Blank lines separate events; it
// accumulates event: and data: lines until a blank line, then yields one event.
func parseSSELines(scanner *bufio.Scanner) (sseEvent, error) {
	var eventType string
	var dataLines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			if eventType != "" || len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")
				return sseEvent{EventType: eventType, Data: json.RawMessage(data)}, nil
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if eventType != "" || len(dataLines) > 0 {
		data := strings.Join(dataLines, "\n")
		return sseEvent{EventType: eventType, Data: json.RawMessage(data)}, nil
	}
	if err := scanner.Err(); err != nil {
		return sseEvent{}, err
	}
	return sseEvent{}, io.EOF
}
