// Package responses implements the llm seam over the OpenAI Responses-API
// wire protocol (used by OpenCode Zen for gpt-* models).
package responses

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
	llm.RegisterWire(llm.WireOpenAIResponses, func(baseURL, apiKey string) llm.Client {
		return NewClient(baseURL, apiKey)
	})
}

// Client is an OpenAI Responses-API client.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient builds a client for a provider at baseURL (e.g.
// https://opencode.ai/zen/v1). An empty apiKey sends requests unauthenticated.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{},
	}
}

// Stream sends one Responses-API request and returns the normalized event
// stream. Open failures (auth, 404, network down) surface as the returned
// error; mid-stream failures surface as a terminal error event.
func (c *Client) Stream(ctx context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	payload, err := json.Marshal(map[string]any{
		"model":    req.Model,
		"input":    inputToWire(req.Messages),
		"instructions": instructionsToWire(req.Messages),
		"tools":    toolsToWire(req.Tools),
		"stream":   true,
		"tool_choice":              "auto",
		"parallel_tool_calls":      false,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, "/responses", payload)
	if err != nil {
		return nil, err
	}

	return func(yield func(llm.Event) bool) {
		defer resp.Body.Close()
		scanner := newSSEScanner(resp.Body)
		toolCalls := make(map[int]*llm.ToolCall) // accumulated by output_index
		var currentEvent sseEvent
		for scanner.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				// Blank line: process accumulated SSE event.
				if currentEvent.event != "" {
					for _, ev := range parseEvent(currentEvent) {
						slog.Debug("responses sse", "kind", ev.Kind)
						if !yield(ev) {
							return
						}
					}
					// Handle tool call accumulation for function_call events.
					if currentEvent.event == "response.output_item.added" {
						var d struct {
							Item struct {
								Type   string `json:"type"`
								ID     string `json:"id"`
								CallID string `json:"call_id"`
								Name   string `json:"name"`
							} `json:"item"`
							OutputIndex int `json:"output_index"`
						}
						if json.Unmarshal(currentEvent.data, &d) == nil && d.Item.Type == "function_call" {
							toolCalls[d.OutputIndex] = &llm.ToolCall{
								ID:   d.Item.CallID,
								Name: d.Item.Name,
							}
						}
					}
					if currentEvent.event == "response.function_call_arguments.delta" {
						var d struct {
							OutputIndex int    `json:"output_index"`
							Delta       string `json:"delta"`
						}
						if json.Unmarshal(currentEvent.data, &d) == nil {
							if tc, ok := toolCalls[d.OutputIndex]; ok {
								tc.Arguments += d.Delta
							}
						}
					}
					if currentEvent.event == "response.function_call_arguments.done" {
						var d struct {
							OutputIndex int    `json:"output_index"`
							Arguments   string `json:"arguments"`
						}
						if json.Unmarshal(currentEvent.data, &d) == nil {
							if tc, ok := toolCalls[d.OutputIndex]; ok {
								tc.Arguments = d.Arguments
							}
						}
					}
					currentEvent = sseEvent{}
				}
				continue
			}
			if strings.HasPrefix(line, "event:") {
				currentEvent.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			} else if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				currentEvent.data = json.RawMessage(data)
			}
		}
		// Process any remaining event (in case stream ends without blank line).
		if currentEvent.event != "" {
			for _, ev := range parseEvent(currentEvent) {
				if !yield(ev) {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			slog.Debug("responses stream read error", "error", err)
			yield(llm.Event{Kind: llm.EventError, Err: fmt.Errorf("reading stream: %w", err)})
		}
		// Emit accumulated tool calls if any.
		if len(toolCalls) > 0 {
			calls := make([]llm.ToolCall, 0, len(toolCalls))
			for i := 0; i < len(toolCalls); i++ {
				if tc, ok := toolCalls[i]; ok {
					calls = append(calls, *tc)
				}
			}
			if len(calls) > 0 {
				slog.Debug("responses tool calls accumulated", "count", len(calls))
				yield(llm.Event{Kind: llm.EventToolCall, ToolCalls: calls})
			}
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

// instructionsToWire extracts system messages and returns the instructions
// string, or empty if there are no system messages.
func instructionsToWire(messages []llm.Message) string {
	for _, m := range messages {
		if m.Role == llm.RoleSystem && m.Content != "" {
			return m.Content
		}
	}
	return ""
}

// inputToWire maps canonical messages to the Responses-API input array.
func inputToWire(messages []llm.Message) []map[string]any {
	var out []map[string]any
	for _, m := range messages {
		switch m.Role {
		case llm.RoleSystem:
			// Handled via instructions; skip.
			continue
		case llm.RoleUser:
			if m.ToolCallID != "" {
				out = append(out, map[string]any{
					"type":    "function_call_output",
					"call_id": m.ToolCallID,
					"output":  m.Content,
				})
			} else {
				out = append(out, map[string]any{
					"role":    "user",
					"content": m.Content,
				})
			}
		case llm.RoleAssistant:
			if len(m.ToolCalls) > 0 {
				for _, tc := range m.ToolCalls {
					out = append(out, map[string]any{
						"type":      "function_call",
						"id":        tc.ID,
						"call_id":   tc.ID,
						"name":      tc.Name,
						"arguments": tc.Arguments,
					})
				}
			} else if m.Content != "" {
				out = append(out, map[string]any{
					"role":    "assistant",
					"content": m.Content,
				})
			}
		}
	}
	return out
}

// toolsToWire maps tool definitions to the Responses-API tools array. Returns
// nil when there are no tools, which omits the field from the JSON payload.
func toolsToWire(tools []llm.ToolDef) any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"name": t.Name,
			"description": t.Description,
			"parameters":  t.Parameters,
		})
	}
	return out
}

// Wire types for error response parsing.

type wireError struct {
	Message string `json:"message"`
}

type errBody struct {
	Error *wireError `json:"error"`
}
