// Package google implements the llm seam over the Google generateContent
// wire protocol (used by OpenCode Zen for gemini-* models and by direct
// Google API endpoints).
package google

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
	llm.RegisterWire(llm.WireGoogle, func(baseURL, apiKey string) llm.Client {
		return NewClient(baseURL, apiKey)
	})
}

// Client is a Google generateContent client.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient builds a client for a provider at baseURL (e.g.
// https://generativelanguage.googleapis.com/v1beta or a Zen gateway). An
// empty apiKey sends requests unauthenticated.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{},
	}
}

// Stream sends one generateContent request with alt=sse and returns the
// normalized event stream. Open failures surface as the returned error;
// mid-stream failures surface as a terminal error event.
func (c *Client) Stream(ctx context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	payload, err := json.Marshal(map[string]any{
		"contents":          contentsToWire(req.Messages),
		"systemInstruction": systemInstructionToWire(req.Messages),
		"tools":             toolsToWire(req.Tools),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	path := "/models/" + req.Model + ":generateContent?alt=sse"
	resp, err := c.doRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return nil, err
	}

	return func(yield func(llm.Event) bool) {
		defer resp.Body.Close()
		scanner := newLineScanner(resp.Body)
		for scanner.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := strings.TrimSpace(scanner.Text())
			if line == "" || !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}
			var ch chunk
			if err := json.Unmarshal([]byte(data), &ch); err != nil {
				slog.Debug("google chunk parse error", "error", err)
				ev := llm.Event{Kind: llm.EventError, Err: fmt.Errorf("malformed google chunk: %w", err)}
				if !yield(ev) {
					return
				}
				return
			}
			for _, ev := range parseChunk(ch) {
				slog.Debug("google chunk", "kind", ev.Kind)
				if !yield(ev) {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			slog.Debug("google stream read error", "error", err)
			yield(llm.Event{Kind: llm.EventError, Err: fmt.Errorf("reading stream: %w", err)})
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
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	models := make([]llm.Model, 0, len(body.Models))
	for _, m := range body.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		models = append(models, llm.Model{ID: id})
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
		req.Header.Set("x-goog-api-key", c.apiKey)
	}

	slog.Debug("http request",
		"method", method,
		"url", c.baseURL+path,
		"auth", "x-goog-api-key <redacted>",
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

	// Try OpenAI-style nested {"error": {"message": "..."}} first.
	var eb errBody
	if json.Unmarshal(body, &eb) == nil && eb.Error != nil && eb.Error.Message != "" {
		message = eb.Error.Message
	} else {
		// Fall back to Google-style flat {"error": {"message": "...", "code": N}}.
		var ge googleError
		if json.Unmarshal(body, &ge) == nil && ge.Error.Message != "" {
			message = ge.Error.Message
		}
	}
	return &llm.ProviderError{Kind: kind, Message: message, StatusCode: resp.StatusCode}
}

// systemInstructionToWire extracts system messages and returns the
// systemInstruction object, or nil if there are no system messages.
func systemInstructionToWire(messages []llm.Message) map[string]any {
	var parts []map[string]any
	for _, m := range messages {
		if m.Role == llm.RoleSystem && m.Content != "" {
			parts = append(parts, map[string]any{"text": m.Content})
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return map[string]any{"parts": parts}
}

// contentsToWire maps canonical messages to the Google contents array.
func contentsToWire(messages []llm.Message) []map[string]any {
	var out []map[string]any
	for _, m := range messages {
		switch m.Role {
		case llm.RoleSystem:
			// Handled via systemInstruction; skip.
			continue
		case llm.RoleUser:
			if m.ToolCallID != "" {
				// Tool result: functionResponse in a user message.
				out = append(out, map[string]any{
					"role": "user",
					"parts": []map[string]any{{
						"functionResponse": map[string]any{
							"name":     m.ToolCallID,
							"response": map[string]any{"result": m.Content},
						},
					}},
				})
			} else {
				out = append(out, map[string]any{
					"role":  "user",
					"parts": []map[string]any{{"text": m.Content}},
				})
			}
		case llm.RoleAssistant:
			if len(m.ToolCalls) > 0 {
				parts := make([]map[string]any, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					var args any
					if tc.Arguments != "" {
						if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
							slog.Debug("unmarshal tool call arguments", "error", err, "name", tc.Name)
						}
					}
					parts = append(parts, map[string]any{
						"functionCall": map[string]any{
							"name": tc.Name,
							"args": args,
						},
					})
				}
				out = append(out, map[string]any{"role": "model", "parts": parts})
			} else if m.Content != "" {
				out = append(out, map[string]any{
					"role":  "model",
					"parts": []map[string]any{{"text": m.Content}},
				})
			}
		}
	}
	return out
}

// toolsToWire maps tool definitions to the Google tools array. Returns nil
// when there are no tools, which omits the field from the JSON payload.
func toolsToWire(tools []llm.ToolDef) any {
	if len(tools) == 0 {
		return nil
	}
	var decls []map[string]any
	for _, t := range tools {
		decl := map[string]any{
			"name":        t.Name,
			"description": t.Description,
		}
		if t.Parameters != nil {
			decl["parameters"] = t.Parameters
		}
		decls = append(decls, decl)
	}
	return []map[string]any{{"functionDeclarations": decls}}
}

// Wire types for Google generateContent response parsing.

type functionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type responsePart struct {
	Text         string       `json:"text"`
	FunctionCall functionCall `json:"functionCall"`
}

type responseContent struct {
	Parts []responsePart `json:"parts"`
	Role  string         `json:"role"`
}

type candidate struct {
	Content      responseContent `json:"content"`
	FinishReason string          `json:"finishReason"`
}

type usageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type chunk struct {
	Candidates   []candidate    `json:"candidates"`
	UsageMetadata *usageMetadata `json:"usageMetadata"`
	Error        *wireError     `json:"error"`
}

type wireError struct {
	Message string `json:"message"`
}

type errBody struct {
	Error *wireError `json:"error"`
}

type googleErrorBody struct {
	Message string `json:"message"`
}

type googleError struct {
	Error googleErrorBody `json:"error"`
}
