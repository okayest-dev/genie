// Package openai implements the llm seam over the OpenAI-compatible
// chat/completions wire protocol (used by OpenCode Zen and other
// OpenAI-compatible providers). It is the v1 wire implementation.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/okayest-dev/og/internal/llm"
)

func init() {
	llm.RegisterWire(llm.WireOpenAI, func(baseURL, apiKey string) llm.Client {
		return NewClient(baseURL, apiKey)
	})
}

// Client is an OpenAI-compatible chat/completions client.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient builds a client for a provider at baseURL (the base of the
// OpenAI-compatible wire, e.g. https://opencode.ai/zen/v1). An empty apiKey
// sends requests unauthenticated.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{},
	}
}

// Stream sends one chat/completions request and returns the normalized event
// stream. Open failures (auth, 404, network down) surface as the returned
// error; mid-stream failures surface as a terminal error event.
func (c *Client) Stream(ctx context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	payload, err := json.Marshal(map[string]any{
		"model":              req.Model,
		"messages":           messagesToWire(req.Messages),
		"stream":             true,
		"parallel_tool_calls": false,
		"stream_options": map[string]any{
			"include_usage": true,
		},
		"tools":        toolsToWire(req.Tools),
		"tool_choice":  "auto",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, "/chat/completions", payload)
	if err != nil {
		return nil, err
	}

	return func(yield func(llm.Event) bool) {
		defer resp.Body.Close()
		scanner := newSSEScanner(resp.Body)
		toolCalls := make(map[int]*llm.ToolCall) // accumulated by index
		var hasToolCalls bool
		for scanner.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := strings.TrimSpace(scanner.Text())
			if line == "" || !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				slog.Debug("sse stream complete", "sentinel", "[DONE]")
				return
			}
			var ch chunk
			if err := json.Unmarshal([]byte(data), &ch); err != nil {
				slog.Debug("sse chunk parse error", "error", err)
				ev := llm.Event{Kind: llm.EventError, Err: fmt.Errorf("malformed SSE chunk: %w", err)}
				if !yield(ev) {
					return
				}
				return
			}
			// Accumulate tool call deltas from this chunk.
			for _, choice := range ch.Choices {
				for _, tc := range choice.Delta.ToolCalls {
					existing, ok := toolCalls[tc.Index]
					if !ok {
						existing = &llm.ToolCall{}
						toolCalls[tc.Index] = existing
					}
					if tc.ID != "" {
						existing.ID = tc.ID
					}
					if tc.Function.Name != "" {
						existing.Name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						existing.Arguments += tc.Function.Arguments
					}
					hasToolCalls = true
				}
			}
			for _, ev := range parseChunk(ch) {
				switch ev.Kind {
				case llm.EventText:
					preview := ev.Text
					if len(preview) > 80 {
						preview = preview[:80] + "..."
					}
					slog.Debug("sse chunk", "kind", "text", "preview", preview)
				case llm.EventFinish:
					slog.Debug("sse chunk", "kind", "finish", "reason", string(ev.End))
					// When finish reason is tool_calls, emit the accumulated calls.
					if ev.End == llm.FinishToolCalls && hasToolCalls {
						calls := make([]llm.ToolCall, 0, len(toolCalls))
						for i := 0; i < len(toolCalls); i++ {
							if tc, ok := toolCalls[i]; ok {
								calls = append(calls, *tc)
							}
						}
						slog.Debug("sse tool calls accumulated", "count", len(calls))
						if !yield(llm.Event{Kind: llm.EventToolCall, ToolCalls: calls}) {
							return
						}
					}
				case llm.EventUsage:
					slog.Debug("sse usage",
						"prompt_tokens", ev.Usage.PromptTokens,
						"completion_tokens", ev.Usage.CompletionTokens,
						"total_tokens", ev.Usage.TotalTokens,
					)
				case llm.EventError:
					slog.Debug("sse chunk", "kind", "error", "error", ev.Err)
				}
				if !yield(ev) {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			slog.Debug("sse stream read error", "error", err)
			ev := llm.Event{Kind: llm.EventError, Err: fmt.Errorf("reading stream: %w", err)}
			yield(ev)
		}
	}, nil
}

// ListModels returns the provider's model catalog, normalized to IDs.
func (c *Client) ListModels(ctx context.Context) ([]llm.Model, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	models := make([]llm.Model, 0, len(body.Data))
	for _, m := range body.Data {
		models = append(models, llm.Model{ID: m.ID})
	}
	return models, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, payload []byte) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	slog.Debug("http request",
		"method", method,
		"url", c.baseURL+path,
		"auth", "Bearer <redacted>",
		"body_bytes", len(payload),
	)

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	latency := time.Since(start)
	if err != nil {
		slog.Debug("http request failed", "error", err, "latency_ms", latency.Milliseconds())
		return nil, &llm.ProviderError{Kind: llm.KindNetwork, Message: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		slog.Debug("http error response", "status", resp.StatusCode, "latency_ms", latency.Milliseconds())
		return nil, errorFromStatus(resp)
	}
	slog.Debug("http response", "status", resp.StatusCode, "latency_ms", latency.Milliseconds())
	return resp, nil
}

// errorFromStatus maps a non-2xx response to a normalized ProviderError,
// preferring the provider's own error message when the body carries one.
func errorFromStatus(resp *http.Response) error {
	defer resp.Body.Close()
	kind := llm.KindOther
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		kind = llm.KindAuth
	case http.StatusTooManyRequests:
		kind = llm.KindRateLimit
	case http.StatusBadRequest:
		kind = llm.KindInvalidRequest
	}

	message := fmt.Sprintf("provider returned %s", resp.Status)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	slog.Debug("http error body", "status", resp.StatusCode, "body", string(body))
	var eb errBody
	if json.Unmarshal(body, &eb) == nil && eb.Error != nil && eb.Error.Message != "" {
		message = eb.Error.Message
	}
	return &llm.ProviderError{Kind: kind, Message: message, StatusCode: resp.StatusCode}
}

// messagesToWire maps canonical messages to the wire's message objects.
func messagesToWire(messages []llm.Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		msg := map[string]any{"role": m.Role, "content": m.Content}
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				calls = append(calls, map[string]any{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]any{
						"name":      tc.Name,
						"arguments": tc.Arguments,
					},
				})
			}
			msg["tool_calls"] = calls
		}
		if m.Role == llm.RoleTool && m.ToolCallID != "" {
			msg["tool_call_id"] = m.ToolCallID
		}
		out = append(out, msg)
	}
	return out
}

// toolsToWire maps tool definitions to the wire's tools array. A nil or
// empty slice produces nil, which omits the field from the JSON payload.
func toolsToWire(tools []llm.ToolDef) any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			},
		})
	}
	return out
}

// Wire types for chat.completion.chunk parsing.

type toolCallDelta struct {
	Index    int               `json:"index"`
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Function functionDelta    `json:"function"`
}

type functionDelta struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type delta struct {
	// Reasoning and refusal fields are deliberately not decoded: unknown
	// fields are dropped at the seam.
	Content   *string          `json:"content"`
	ToolCalls []toolCallDelta  `json:"tool_calls"`
}

type choice struct {
	Index        int     `json:"index"`
	Delta        delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type wireError struct {
	Message string `json:"message"`
}

type errBody struct {
	Error *wireError `json:"error"`
}

type chunk struct {
	Choices []choice   `json:"choices"`
	Usage   *usage     `json:"usage"`
	Error   *wireError `json:"error"`
}
