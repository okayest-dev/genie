// Package anthropic implements the llm seam over the Anthropic Messages
// wire protocol (used by Zen and other Anthropic-compatible providers).
package anthropic

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

const anthropicVersion = "2023-06-01"

func init() {
	llm.RegisterWire(llm.WireAnthropic, func(baseURL, apiKey string) llm.Client {
		return NewClient(baseURL, apiKey)
	})
}

// Client is an Anthropic Messages wire client.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient builds a client for a provider at baseURL. An empty apiKey
// sends requests unauthenticated.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{},
	}
}

// Stream sends one Messages request and returns the normalized event stream.
func (c *Client) Stream(ctx context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	system, messages := extractSystem(req.Messages)
	payload := map[string]any{
		"model":      req.Model,
		"messages":   messagesToWire(messages),
		"max_tokens": 4096,
		"stream":     true,
	}
	if system != "" {
		payload["system"] = system
	}
	if tools := toolsToWire(req.Tools); tools != nil {
		payload["tools"] = tools
		payload["tool_choice"] = map[string]any{"type": "auto"}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, "/messages", body)
	if err != nil {
		return nil, err
	}

	return func(yield func(llm.Event) bool) {
		defer resp.Body.Close()
		scanner := newSSEScanner(resp.Body)

		// Tool call accumulation state, keyed by content_block index.
		type toolAcc struct {
			ID   string
			Name string
			Args strings.Builder
		}
		toolCalls := make(map[int]*toolAcc)
		var inputTokens int

		for {
			if ctx.Err() != nil {
				return
			}
			evt, err := parseSSELines(scanner)
			if err != nil {
				if err != io.EOF && ctx.Err() == nil {
					slog.Debug("sse stream read error", "error", err)
					yield(llm.Event{Kind: llm.EventError, Err: fmt.Errorf("reading stream: %w", err)})
				}
				return
			}

			// Handle stateful events that parseEvent doesn't cover.
			switch evt.EventType {
			case "message_start":
				var ms messageStart
				if json.Unmarshal(evt.Data, &ms) == nil {
					inputTokens = ms.Message.Usage.InputTokens
				}
				continue
			case "content_block_start":
				var cbs contentBlockStart
				if json.Unmarshal(evt.Data, &cbs) == nil && cbs.ContentBlock.Type == "tool_use" {
					toolCalls[cbs.Index] = &toolAcc{
						ID:   cbs.ContentBlock.ID,
						Name: cbs.ContentBlock.Name,
					}
				}
				continue
			case "content_block_delta":
				var cbd contentBlockDelta
				if json.Unmarshal(evt.Data, &cbd) == nil && cbd.Delta.Type == "input_json_delta" {
					if tc, ok := toolCalls[cbd.Index]; ok {
						tc.Args.WriteString(cbd.Delta.PartialJSON)
					}
				}
				// Fall through to parseEvent for text_delta.
			case "content_block_stop":
				var cbs contentBlockStop
				if json.Unmarshal(evt.Data, &cbs) == nil {
					if tc, ok := toolCalls[cbs.Index]; ok {
						calls := []llm.ToolCall{{ID: tc.ID, Name: tc.Name, Arguments: tc.Args.String()}}
						slog.Debug("sse tool call accumulated", "id", tc.ID, "name", tc.Name)
						if !yield(llm.Event{Kind: llm.EventToolCall, ToolCalls: calls}) {
							return
						}
						delete(toolCalls, cbs.Index)
					}
				}
				continue
			case "message_stop":
				continue
			}

			for _, ev := range parseEvent(evt) {
				// Attach input_tokens from message_start to the first usage event.
				if ev.Kind == llm.EventUsage && inputTokens > 0 {
					ev.Usage.PromptTokens = inputTokens
					ev.Usage.TotalTokens = inputTokens + ev.Usage.CompletionTokens
					inputTokens = 0 // only once
				}
				if !yield(ev) {
					return
				}
			}
		}
	}, nil
}

// ListModels returns the provider's model catalog via the Zen models endpoint.
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
	req.Header.Set("anthropic-version", anthropicVersion)
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}

	slog.Debug("http request",
		"method", method,
		"url", c.baseURL+path,
		"auth", "x-api-key <redacted>",
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

// errorFromStatus maps a non-2xx response to a normalized ProviderError.
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
	case 529:
		kind = llm.KindRateLimit
	}

	message := fmt.Sprintf("provider returned %s", resp.Status)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	slog.Debug("http error body", "status", resp.StatusCode, "body", string(body))

	// Try Anthropic error format: {"error": {"type": "...", "message": "..."}}
	var eb struct {
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &eb) == nil && eb.Error != nil && eb.Error.Message != "" {
		message = eb.Error.Message
		if kind == llm.KindOther {
			kind = errorKindFromType(eb.Error.Type)
		}
	}
	return &llm.ProviderError{Kind: kind, Message: message, StatusCode: resp.StatusCode}
}

// extractSystem pulls the system instruction out of the first system message
// and returns it alongside the remaining messages.
func extractSystem(messages []llm.Message) (string, []llm.Message) {
	if len(messages) == 0 || messages[0].Role != llm.RoleSystem {
		return "", messages
	}
	return messages[0].Content, messages[1:]
}

// messagesToWire maps canonical messages to the Anthropic wire format.
// Tool results become user messages containing tool_result content blocks.
// Assistant messages with tool calls include tool_use content blocks.
// Consecutive same-role messages are merged.
func messagesToWire(messages []llm.Message) []map[string]any {
	var out []map[string]any
	for _, m := range messages {
		switch m.Role {
		case llm.RoleUser:
			if m.ToolCallID != "" {
				// Tool result -> user message with tool_result content block.
				block := map[string]any{
					"type":       "tool_result",
					"tool_use_id": m.ToolCallID,
					"content":    m.Content,
				}
				// Try to merge with the last user message.
				if len(out) > 0 && out[len(out)-1]["role"] == "user" {
					if existing, ok := out[len(out)-1]["content"].([]any); ok {
						out[len(out)-1]["content"] = append(existing, block)
						continue
					}
				}
				out = append(out, map[string]any{
					"role":    "user",
					"content": []any{block},
				})
			} else {
				out = append(out, map[string]any{"role": "user", "content": m.Content})
			}
		case llm.RoleAssistant:
			if len(m.ToolCalls) > 0 {
				var content []any
				if m.Content != "" {
					content = append(content, map[string]any{"type": "text", "text": m.Content})
				}
				for _, tc := range m.ToolCalls {
					var input any
					if tc.Arguments != "" {
						json.Unmarshal([]byte(tc.Arguments), &input)
					}
					content = append(content, map[string]any{
						"type":  "tool_use",
						"id":    tc.ID,
						"name":  tc.Name,
						"input": input,
					})
				}
				out = append(out, map[string]any{"role": "assistant", "content": content})
			} else {
				out = append(out, map[string]any{"role": "assistant", "content": m.Content})
			}
		}
	}
	return out
}

// toolsToWire maps tool definitions to the Anthropic tools array. Returns nil
// for empty tools (omitting the field from the JSON payload).
func toolsToWire(tools []llm.ToolDef) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"input_schema": t.Parameters,
		})
	}
	return out
}
