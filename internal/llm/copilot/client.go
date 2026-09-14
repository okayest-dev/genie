package copilot

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
	"sync"
	"time"

	"github.com/okayest-dev/genie/internal/llm"
)

func init() {
	llm.RegisterWire(llm.WireCopilot, func(baseURL, apiKey string, opts map[string]any) llm.Client {
		domain := "github.com"
		if d, ok := opts["domain"].(string); ok && d != "" {
			domain = d
		}
		return NewClient(domain, defaultXDGDataHome())
	})
}

// Client is a bundled in-process Copilot wire. It authenticates through the
// Copilot credential store (ADR-0002) — never an api_key_env — exchanging the
// durable GitHub OAuth token for a short-lived Copilot JWT on demand.
type Client struct {
	domain      string // GitHub host: github.com or a *.ghe.com tenant
	dataHome    string // XDG data home; the credential store lives under it
	exchangeURL string // test override; empty means the domain's default

	httpClient *http.Client

	mu           sync.Mutex
	copilotToken string    // memory-only derived token
	apiBase      string    // account-tier API base from the exchange
	refreshAfter time.Time // re-exchange once the token is near expiry
}

// NewClient builds a Copilot wire client for a GitHub host (default
// github.com), reading credentials from the store under xdgDataHome.
func NewClient(domain, xdgDataHome string) *Client {
	if domain == "" {
		domain = "github.com"
	}
	return &Client{
		domain:     domain,
		dataHome:   xdgDataHome,
		httpClient: &http.Client{},
	}
}

// access returns a valid Copilot JWT and its API base, exchanging the OAuth
// token from the credential store when the cached derived token is absent or
// within refresh_in of expiry. The derived token lives in memory only
// (ADR-0002); the store is read fresh on every exchange so a concurrent login
// is picked up on the next request.
func (c *Client) access(ctx context.Context) (token, apiBase string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.copilotToken != "" && time.Now().Before(c.refreshAfter) {
		return c.copilotToken, c.apiBase, nil
	}
	oauth, err := ReadOAuthToken(c.dataHome, c.domain)
	if err != nil {
		return "", "", err
	}
	tr, err := c.exchange(ctx, oauth)
	if err != nil {
		return "", "", err
	}
	c.copilotToken = tr.Token
	c.apiBase = tr.Endpoints.API
	if c.apiBase == "" {
		c.apiBase = defaultAPIBase
	}
	ttl := time.Duration(tr.RefreshIn)*time.Second - 60*time.Second
	if ttl < time.Second {
		ttl = time.Second
	}
	c.refreshAfter = time.Now().Add(ttl)
	return c.copilotToken, c.apiBase, nil
}

// Stream sends one chat/completions request and returns the normalized event
// stream. Open failures (auth, 404, network down) surface as the returned
// error; mid-stream failures surface as a terminal error event.
func (c *Client) Stream(ctx context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	token, apiBase, err := c.access(ctx)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"model":               req.Model,
		"messages":            messagesToWire(req.Messages),
		"stream":              true,
		"parallel_tool_calls": false,
		"stream_options": map[string]any{
			"include_usage": true,
		},
		"tools":       toolsToWire(req.Tools),
		"tool_choice": "auto",
	})
	if err != nil {
		return nil, fmt.Errorf("copilot: marshal request: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, apiBase, "/v1/chat/completions", token, payload)
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
				slog.Debug("copilot sse stream complete", "sentinel", "[DONE]")
				return
			}
			var ch chunk
			if err := json.Unmarshal([]byte(data), &ch); err != nil {
				slog.Debug("copilot sse chunk parse error", "error", err)
				ev := llm.Event{Kind: llm.EventError, Err: fmt.Errorf("copilot: malformed SSE chunk: %w", err)}
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
					slog.Debug("copilot sse chunk", "kind", "text", "preview", preview(ev.Text))
				case llm.EventFinish:
					if ev.End == llm.FinishToolCalls && hasToolCalls {
						calls := make([]llm.ToolCall, 0, len(toolCalls))
						for i := 0; i < len(toolCalls); i++ {
							if tc, ok := toolCalls[i]; ok {
								calls = append(calls, *tc)
							}
						}
						if !yield(llm.Event{Kind: llm.EventToolCall, ToolCalls: calls}) {
							return
						}
					}
				case llm.EventUsage:
					slog.Debug("copilot sse usage",
						"prompt_tokens", ev.Usage.PromptTokens,
						"completion_tokens", ev.Usage.CompletionTokens,
						"total_tokens", ev.Usage.TotalTokens,
					)
				case llm.EventError:
					slog.Debug("copilot sse chunk", "kind", "error", "error", ev.Err)
				}
				if !yield(ev) {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			slog.Debug("copilot sse stream read error", "error", err)
			yield(llm.Event{Kind: llm.EventError, Err: fmt.Errorf("copilot: reading stream: %w", err)})
		}
	}, nil
}

// ListModels returns the account's Copilot model catalog, normalized to IDs.
func (c *Client) ListModels(ctx context.Context) ([]llm.Model, error) {
	token, apiBase, err := c.access(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.doRequest(ctx, http.MethodGet, apiBase, "/v1/models", token, nil)
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
		return nil, fmt.Errorf("copilot: decode models: %w", err)
	}
	models := make([]llm.Model, 0, len(body.Data))
	for _, m := range body.Data {
		models = append(models, llm.Model{ID: m.ID})
	}
	return models, nil
}

// ModelInfo implements the optional llm.ModelInfoProvider seam. The Copilot
// catalog exposes no context window, so it returns a zero ModelInfo (unknown)
// rather than an invented window; callers fall back to a config override.
func (c *Client) ModelInfo(_ context.Context, _ string) (*llm.ModelInfo, error) {
	return &llm.ModelInfo{}, nil
}

// doRequest sends a request to the Copilot API with the mandatory identity
// headers, mapping non-2xx responses to normalized ProviderErrors.
func (c *Client) doRequest(ctx context.Context, method, apiBase, path, token string, payload []byte) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, body)
	if err != nil {
		return nil, fmt.Errorf("copilot: build request: %w", err)
	}
	req.Header = copilotHeaders(token)

	if payload != nil {
		slog.Debug("copilot http request", "method", method, "url", apiBase+path, "body_bytes", len(payload))
	} else {
		slog.Debug("copilot http request", "method", method, "url", apiBase+path)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Debug("copilot http request failed", "error", err)
		return nil, &llm.ProviderError{Kind: llm.KindNetwork, Message: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		slog.Debug("copilot http error response", "status", resp.StatusCode)
		return nil, apiError(resp)
	}
	return resp, nil
}

// copilotHeaders builds the identity headers the Copilot API requires; without
// a consistent editor identity the API rejects business/enterprise requests.
func copilotHeaders(token string) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer "+token)
	h.Set("Editor-Version", "genie/0.1.0")
	h.Set("Editor-Plugin-Version", "genie-copilot/0.1.0")
	h.Set("Copilot-Integration-Id", "vscode-chat")
	h.Set("User-Agent", "GithubCopilot/genie-0.1.0")
	return h
}

// apiError maps a non-2xx Copilot API response to a normalized ProviderError.
// 401/403 (an expired or rejected derived token) fail fast as KindAuth.
func apiError(resp *http.Response) error {
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
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	msg := fmt.Sprintf("copilot: %s", resp.Status)
	if text := jsonErrorText(body); text != "" {
		msg = fmt.Sprintf("copilot: %s", text)
	}
	return &llm.ProviderError{Kind: kind, Message: msg, StatusCode: resp.StatusCode}
}

func preview(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
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

// Wire types for chat.completion.chunk parsing.

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

type chunk struct {
	Choices []choice   `json:"choices"`
	Usage   *usage     `json:"usage"`
	Error   *wireError `json:"error"`
}
