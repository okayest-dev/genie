// Package litellm implements the llm seam over the LiteLLM proxy.
// It supports both API key (Virtual Key) and OAuth2 authentication.
package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/llm/oauth2"
)

func init() {
	llm.RegisterWire(llm.WireLiteLLM, func(baseURL, apiKey string, opts map[string]any) llm.Client {
		return NewClient(baseURL, apiKey, opts)
	})
	llm.RegisterWire(llm.WireLiteLLMOAuth, func(baseURL, apiKey string, opts map[string]any) llm.Client {
		return NewOAuthClient(baseURL, opts)
	})
}

// Client is a LiteLLM proxy client using API key authentication.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient builds a client for a LiteLLM proxy at baseURL.
// An empty apiKey sends requests unauthenticated.
func NewClient(baseURL, apiKey string, _ map[string]any) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{},
	}
}

// Stream sends one chat/completions request and returns the normalized event stream.
func (c *Client) Stream(ctx context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	payloadMap := map[string]any{
		"model":               req.Model,
		"messages":            messagesToWire(req.Messages),
		"stream":              true,
		"parallel_tool_calls": false,
		"stream_options": map[string]any{
			"include_usage": true,
		},
		"tools":       toolsToWire(req.Tools),
		"tool_choice": "auto",
	}
	if req.MaxOutputTokens > 0 {
		payloadMap["max_tokens"] = req.MaxOutputTokens
	}
	payload, err := json.Marshal(payloadMap)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, "/chat/completions", payload)
	if err != nil {
		return nil, err
	}

	return func(yield func(llm.Event) bool) {
		defer resp.Body.Close()
		scanner := llm.NewScanner(resp.Body)
		toolCalls := make(map[int]*llm.ToolCall)
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

// ListModels returns the provider's model catalog.
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

// OAuthClient is a LiteLLM proxy client using OAuth2 authentication.
type OAuthClient struct {
	baseURL      string
	tokenManager *oauth2.TokenManager
	httpClient   *http.Client
}

// NewOAuthClient builds a client for a LiteLLM proxy using OAuth2.
// The opts map should contain oauth2 configuration.
func NewOAuthClient(baseURL string, opts map[string]any) *OAuthClient {
	cfg := &oauth2.Config{}

	if v, ok := opts["oauth2_token_url"].(string); ok {
		cfg.TokenURL = v
	}
	if v, ok := opts["oauth2_client_id"].(string); ok {
		cfg.ClientID = v
	}
	if v, ok := opts["oauth2_client_secret_env"].(string); ok {
		cfg.ClientSecret = os.Getenv(v)
	}
	if v, ok := opts["oauth2_scopes"].([]string); ok {
		cfg.Scopes = v
	} else if v, ok := opts["oauth2_scopes"].([]any); ok {
		for _, s := range v {
			if str, ok := s.(string); ok {
				cfg.Scopes = append(cfg.Scopes, str)
			}
		}
	}
	if v, ok := opts["oauth2_device_auth_url"].(string); ok {
		cfg.DeviceFlowAuthURL = v
	}
	if v, ok := opts["oauth2_device_token_url"].(string); ok {
		cfg.DeviceFlowTokenURL = v
	}
	if v, ok := opts["oauth2_device_client_id"].(string); ok {
		cfg.DeviceFlowClientID = v
	}
	if v, ok := opts["oauth2_audience"].(string); ok {
		cfg.Audience = v
	}

	return &OAuthClient{
		baseURL:      strings.TrimRight(baseURL, "/"),
		tokenManager: oauth2.NewTokenManager(cfg),
		httpClient:   &http.Client{},
	}
}

// Stream sends one chat/completions request with OAuth2 authentication.
func (c *OAuthClient) Stream(ctx context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	token, err := c.tokenManager.GetToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("oauth2: get token: %w", err)
	}

	payloadMap := map[string]any{
		"model":               req.Model,
		"messages":            messagesToWire(req.Messages),
		"stream":              true,
		"parallel_tool_calls": false,
		"stream_options": map[string]any{
			"include_usage": true,
		},
		"tools":       toolsToWire(req.Tools),
		"tool_choice": "auto",
	}
	if req.MaxOutputTokens > 0 {
		payloadMap["max_tokens"] = req.MaxOutputTokens
	}
	payload, err := json.Marshal(payloadMap)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, "/chat/completions", token, payload)
	if err != nil {
		// On 401, clear token and retry once
		if pe, ok := err.(*llm.ProviderError); ok && pe.Kind == llm.KindAuth {
			c.tokenManager.ClearToken()
			token, err = c.tokenManager.GetToken(ctx)
			if err != nil {
				return nil, fmt.Errorf("oauth2: get token after 401: %w", err)
			}
			resp, err = c.doRequest(ctx, http.MethodPost, "/chat/completions", token, payload)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	return func(yield func(llm.Event) bool) {
		defer resp.Body.Close()
		scanner := llm.NewScanner(resp.Body)
		toolCalls := make(map[int]*llm.ToolCall)
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

// ListModels returns the provider's model catalog with OAuth2 auth.
func (c *OAuthClient) ListModels(ctx context.Context) ([]llm.Model, error) {
	token, err := c.tokenManager.GetToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("oauth2: get token: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodGet, "/models", token, nil)
	if err != nil {
		if pe, ok := err.(*llm.ProviderError); ok && pe.Kind == llm.KindAuth {
			c.tokenManager.ClearToken()
			token, err = c.tokenManager.GetToken(ctx)
			if err != nil {
				return nil, fmt.Errorf("oauth2: get token after 401: %w", err)
			}
			resp, err = c.doRequest(ctx, http.MethodGet, "/models", token, nil)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
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

func (c *OAuthClient) doRequest(ctx context.Context, method, path, token string, payload []byte) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	slog.Debug("http request",
		"method", method,
		"url", c.baseURL+path,
		"auth", "Bearer <oauth2>",
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
	}

	message := fmt.Sprintf("provider returned %s", resp.Status)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	slog.Debug("http error body", "status", resp.StatusCode, "body", string(body))
	var eb errBody
	if json.Unmarshal(body, &eb) == nil && eb.Error != nil && eb.Error.Message != "" {
		message = eb.Error.Message
		if kind == llm.KindInvalidRequest && isContextOverflow(message) {
			kind = llm.KindContextOverflow
		}
	}
	return &llm.ProviderError{Kind: kind, Message: message, StatusCode: resp.StatusCode}
}

func isContextOverflow(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "context_length_exceeded") ||
		strings.Contains(lower, "maximum context length") ||
		strings.Contains(lower, "context window") ||
		strings.Contains(lower, "token limit") ||
		strings.Contains(lower, "max_tokens")
}

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

func toolsToWire(tools []llm.ToolDef) []map[string]any {
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

type toolCallDelta struct {
	Index    int           `json:"index"`
	ID       string        `json:"id"`
	Type     string        `json:"type"`
	Function functionDelta `json:"function"`
}

type functionDelta struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type delta struct {
	Content   *string         `json:"content"`
	ToolCalls []toolCallDelta `json:"tool_calls"`
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

// parseChunk converts one wire chunk into normalized events. It is a pure
// function so the accumulation rules are unit-testable at the SSE seam.
func parseChunk(ch chunk) []llm.Event {
	if ch.Error != nil {
		msg := ch.Error.Message
		if msg == "" {
			msg = "provider error"
		}
		return []llm.Event{{Kind: llm.EventError, Err: &llm.ProviderError{Kind: llm.KindOther, Message: msg}}}
	}
	if ch.Usage != nil && len(ch.Choices) == 0 {
		return []llm.Event{{
			Kind:  llm.EventUsage,
			Usage: llm.Usage{PromptTokens: ch.Usage.PromptTokens, CompletionTokens: ch.Usage.CompletionTokens, TotalTokens: ch.Usage.TotalTokens},
		}}
	}
	var events []llm.Event
	for _, choice := range ch.Choices {
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			events = append(events, llm.Event{Kind: llm.EventText, Text: *choice.Delta.Content})
		}
		if choice.FinishReason != nil {
			events = append(events, llm.Event{Kind: llm.EventFinish, End: canonicalFinishReason(*choice.FinishReason)})
		}
	}
	return events
}

// canonicalFinishReason maps a wire finish_reason to the canonical set,
// folding anything unknown into FinishOther.
func canonicalFinishReason(reason string) llm.FinishReason {
	switch reason {
	case string(llm.FinishStop):
		return llm.FinishStop
	case string(llm.FinishToolCalls):
		return llm.FinishToolCalls
	case string(llm.FinishLength):
		return llm.FinishLength
	default:
		return llm.FinishOther
	}
}