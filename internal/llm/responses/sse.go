package responses

import (
	"bufio"
	"encoding/json"
	"io"

	"github.com/okayest-dev/og/internal/llm"
)

// sseEvent is one parsed SSE frame: an event type and its JSON data payload.
type sseEvent struct {
	event string
	data  json.RawMessage
}

// newSSEScanner scans an SSE body line by line, with headroom for long
// data lines.
func newSSEScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	return sc
}

// parseEvent converts one Responses-API SSE event into normalized events.
// It is a pure function so the parsing rules are unit-testable at the SSE
// seam. Tool call accumulation is handled by the caller (Stream loop).
func parseEvent(evt sseEvent) []llm.Event {
	switch evt.event {
	case "response.output_text.delta":
		var d struct {
			Delta string `json:"delta"`
		}
		if json.Unmarshal(evt.data, &d) == nil && d.Delta != "" {
			return []llm.Event{{Kind: llm.EventText, Text: d.Delta}}
		}
	case "response.function_call_arguments.delta":
		var d struct {
			Delta string `json:"delta"`
		}
		if json.Unmarshal(evt.data, &d) == nil && d.Delta != "" {
			// Deltas are accumulated by the caller; emit nothing here.
			return nil
		}
	case "response.completed":
		var d struct {
			Response struct {
				Status string `json:"status"`
				Usage  *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
					TotalTokens  int `json:"total_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if json.Unmarshal(evt.data, &d) != nil {
			return nil
		}
		var events []llm.Event
		if d.Response.Usage != nil {
			events = append(events, llm.Event{
				Kind: llm.EventUsage,
				Usage: llm.Usage{
					PromptTokens:     d.Response.Usage.InputTokens,
					CompletionTokens: d.Response.Usage.OutputTokens,
					TotalTokens:      d.Response.Usage.TotalTokens,
				},
			})
		}
		events = append(events, llm.Event{Kind: llm.EventFinish, End: canonicalFinishReason(d.Response.Status)})
		return events
	case "response.failed":
		var d struct {
			Response struct {
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
		}
		if json.Unmarshal(evt.data, &d) == nil && d.Response.Error != nil {
			msg := d.Response.Error.Message
			if msg == "" {
				msg = "response failed"
			}
			return []llm.Event{{Kind: llm.EventError, Err: &llm.ProviderError{Kind: llm.KindOther, Message: msg}}}
		}
		return []llm.Event{{Kind: llm.EventError, Err: &llm.ProviderError{Kind: llm.KindOther, Message: "response failed"}}}
	case "response.incomplete":
		var d struct {
			Response struct {
				IncompleteDetails *struct {
					Reason string `json:"reason"`
				} `json:"incomplete_details"`
			} `json:"response"`
		}
		if json.Unmarshal(evt.data, &d) == nil && d.Response.IncompleteDetails != nil {
			reason := d.Response.IncompleteDetails.Reason
			if reason == "max_output_tokens" {
				return []llm.Event{{Kind: llm.EventFinish, End: llm.FinishLength}}
			}
		}
		return []llm.Event{{Kind: llm.EventFinish, End: llm.FinishLength}}
	case "error":
		var d struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(evt.data, &d) == nil && d.Message != "" {
			return []llm.Event{{Kind: llm.EventError, Err: &llm.ProviderError{Kind: llm.KindOther, Message: d.Message}}}
		}
		return []llm.Event{{Kind: llm.EventError, Err: &llm.ProviderError{Kind: llm.KindOther, Message: "provider error"}}}
	}
	return nil
}

// canonicalFinishReason maps a Responses-API status to the canonical
// finish reason set.
func canonicalFinishReason(status string) llm.FinishReason {
	switch status {
	case "completed":
		return llm.FinishStop
	case "incomplete":
		return llm.FinishLength
	case "failed":
		return llm.FinishOther
	default:
		return llm.FinishOther
	}
}
