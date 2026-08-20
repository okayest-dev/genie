// Package fake provides in-process, scripted fake providers for E2E testing.
// It supports OpenAI chat/completions, Anthropic Messages, OpenAI Responses-API,
// and Google generateContent wire formats. Tests drive the compiled og binary
// against these servers and assert only observable behavior.
package fake

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// Behavior scripts one POST /chat/completions response. Exactly one of the
// response shapes is used: if Error is non-nil the server replies with that
// status and body; otherwise it streams SSE built from Chunks.
type Behavior struct {
	// Error, when non-nil, replies with this status and body instead of
	// streaming. A nil body still honours the status.
	Error *Error

	// Chunks are streamed as "data: <chunk>\n\n" lines in order.
	Chunks []string

	// Delay pauses the response after writing each chunk, so tests can
	// observe deltas arriving before the stream (and process) ends.
	Delay time.Duration
}

// Error scripts a non-stream HTTP error response.
type Error struct {
	Status int
	Body   string
}

// Request records what the binary sent to the provider.
type Request struct {
	Method string
	Path   string
	Auth   string
	Body   string
}

// Provider is a scripted OpenAI-compatible server.
type Provider struct {
	URL string

	mu       sync.Mutex
	behavior Behavior
	requests []Request
	server   *httptest.Server
}

// New starts a provider with no configured behavior; tests set Behavior
// before invoking the binary.
func New() *Provider {
	p := &Provider{}
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	p.URL = p.server.URL
	return p
}

// Close shuts the server down.
func (p *Provider) Close() {
	p.server.Close()
}

// SetBehavior scripts the next chat/completions response.
func (p *Provider) SetBehavior(b Behavior) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.behavior = b
}

// Requests returns the requests received so far.
func (p *Provider) Requests() []Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Request, len(p.requests))
	copy(out, p.requests)
	return out
}

func (p *Provider) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	p.requests = append(p.requests, Request{
		Method: r.Method,
		Path:   r.URL.Path,
		Auth:   r.Header.Get("Authorization"),
		Body:   string(body),
	})
	behavior := p.behavior
	p.mu.Unlock()

	if r.Method == http.MethodPost && r.URL.Path == "/chat/completions" {
		p.serveChat(w, behavior)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/messages" {
		p.serveAnthropic(w, behavior)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/responses" {
		p.serveResponses(w, behavior)
		return
	}
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/models/") && strings.HasSuffix(r.URL.Path, ":generateContent") {
		p.serveGoogle(w, behavior)
		return
	}
	http.NotFound(w, r)
}

func (p *Provider) serveChat(w http.ResponseWriter, b Behavior) {
	if b.Error != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(b.Error.Status)
		io.WriteString(w, b.Error.Body)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, chunk := range b.Chunks {
		if b.Delay > 0 {
			time.Sleep(b.Delay)
		}
		io.WriteString(w, "data: "+chunk+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// serveAnthropic serves the Anthropic Messages wire format.
func (p *Provider) serveAnthropic(w http.ResponseWriter, b Behavior) {
	if b.Error != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(b.Error.Status)
		io.WriteString(w, b.Error.Body)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, chunk := range b.Chunks {
		if b.Delay > 0 {
			time.Sleep(b.Delay)
		}
		// Anthropic SSE uses event: + data: on separate lines, then blank line.
		for _, line := range strings.Split(chunk, "\n") {
			io.WriteString(w, line+"\n")
		}
		io.WriteString(w, "\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// serveResponses serves the OpenAI Responses-API wire format.
func (p *Provider) serveResponses(w http.ResponseWriter, b Behavior) {
	if b.Error != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(b.Error.Status)
		io.WriteString(w, b.Error.Body)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, chunk := range b.Chunks {
		if b.Delay > 0 {
			time.Sleep(b.Delay)
		}
		io.WriteString(w, chunk+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// serveGoogle serves the Google generateContent wire format.
func (p *Provider) serveGoogle(w http.ResponseWriter, b Behavior) {
	if b.Error != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(b.Error.Status)
		io.WriteString(w, b.Error.Body)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, chunk := range b.Chunks {
		if b.Delay > 0 {
			time.Sleep(b.Delay)
		}
		io.WriteString(w, "data: "+chunk+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// SSE helpers for building chunk payloads.

// --- OpenAI chat/completions helpers ---

// TextDelta builds a chat.completion.chunk carrying a content delta.
func TextDelta(content string) string {
	return fmt.Sprintf(`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`, content)
}

// Finish builds the terminal chunk carrying a finish reason.
func Finish(reason string) string {
	return fmt.Sprintf(`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":%q}]}`, reason)
}

// Usage builds the include_usage chunk (choices empty).
func Usage(prompt, completion, total int) string {
	return fmt.Sprintf(`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"m","choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`, prompt, completion, total)
}

// ReasoningDelta builds a chunk whose delta carries reasoning fields and no
// content — the harness must drop these.
func ReasoningDelta(text string) string {
	return fmt.Sprintf(`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"reasoning_content":%q,"reasoning":%q,"content":null},"finish_reason":null}]}`, text, text)
}

// ToolCallDelta builds a chunk carrying a tool call delta. On the first chunk
// for a tool call, include id and name; subsequent chunks can omit them and
// just carry arguments.
func ToolCallDelta(index int, id, name, arguments string) string {
	tc := map[string]any{"index": index}
	if id != "" {
		tc["id"] = id
	}
	if name != "" {
		tc["type"] = "function"
		tc["function"] = map[string]any{"name": name, "arguments": arguments}
	} else {
		tc["function"] = map[string]any{"arguments": arguments}
	}
	delta := map[string]any{"tool_calls": []map[string]any{tc}}
	choice := map[string]any{"index": 0, "delta": delta, "finish_reason": nil}
	b, _ := json.Marshal(map[string]any{
		"id":      "chatcmpl-1",
		"object":  "chat.completion.chunk",
		"model":   "m",
		"choices": []map[string]any{choice},
	})
	return string(b)
}

// --- Anthropic Messages helpers ---

// AnthropicTextDelta builds an Anthropic content_block_delta event.
func AnthropicTextDelta(text string) string {
	b, _ := json.Marshal(map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type": "text_delta",
			"text": text,
		},
	})
	return "event: content_block_delta\ndata: " + string(b)
}

// AnthropicMessageDelta builds an Anthropic message_delta event with stop reason.
func AnthropicMessageDelta(stopReason string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason": stopReason,
		},
		"usage": map[string]any{
			"output_tokens": 0,
		},
	})
	return "event: message_delta\ndata: " + string(b)
}

// AnthropicMessageStart builds an Anthropic message_start event.
func AnthropicMessageStart() string {
	b, _ := json.Marshal(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"usage": map[string]any{
				"input_tokens": 10,
			},
		},
	})
	return "event: message_start\ndata: " + string(b)
}

// --- OpenAI Responses-API helpers ---

// ResponsesTextDelta builds a response.output_text.delta event.
func ResponsesTextDelta(text string) string {
	b, _ := json.Marshal(map[string]any{
		"type":  "response.output_text.delta",
		"delta": text,
	})
	return fmt.Sprintf("event: response.output_text.delta\ndata: %s", string(b))
}

// ResponsesCompleted builds a response.completed event.
func ResponsesCompleted() string {
	b, _ := json.Marshal(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"status": "completed",
		},
	})
	return fmt.Sprintf("event: response.completed\ndata: %s", string(b))
}

// --- Google generateContent helpers ---

// GoogleTextDelta builds a Google chunk with text delta.
func GoogleTextDelta(text string) string {
	b, _ := json.Marshal(map[string]any{
		"candidates": []map[string]any{
			{
				"content": map[string]any{
					"parts": []map[string]any{
						{"text": text},
					},
				},
				"finishReason": nil,
			},
		},
	})
	return string(b)
}

// GoogleFinish builds a Google chunk with finish reason.
func GoogleFinish(reason string) string {
	b, _ := json.Marshal(map[string]any{
		"candidates": []map[string]any{
			{
				"content": map[string]any{},
				"finishReason": reason,
			},
		},
	})
	return string(b)
}

// Done is the SSE terminator payload.
const Done = "[DONE]"
