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
	"net/http"
	"strings"

	"github.com/okayest-dev/og/internal/llm"
)

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
		"model":    req.Model,
		"messages": messagesToWire(req.Messages),
		"stream":   true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
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
				return
			}
			var ch chunk
			if err := json.Unmarshal([]byte(data), &ch); err != nil {
				ev := llm.Event{Kind: llm.EventError, Err: fmt.Errorf("malformed SSE chunk: %w", err)}
				if !yield(ev) {
					return
				}
				return
			}
			for _, ev := range parseChunk(ch) {
				if !yield(ev) {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &llm.ProviderError{Kind: llm.KindNetwork, Message: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, errorFromStatus(resp)
	}
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
	var eb errBody
	if json.Unmarshal(body, &eb) == nil && eb.Error != nil && eb.Error.Message != "" {
		message = eb.Error.Message
	}
	return &llm.ProviderError{Kind: kind, Message: message, StatusCode: resp.StatusCode}
}

// messagesToWire maps canonical messages to the wire's message objects.
func messagesToWire(messages []llm.Message) []map[string]string {
	out := make([]map[string]string, 0, len(messages))
	for _, m := range messages {
		out = append(out, map[string]string{"role": m.Role, "content": m.Content})
	}
	return out
}

// Wire types for chat.completion.chunk parsing.

type delta struct {
	// Reasoning and refusal fields are deliberately not decoded: unknown
	// fields are dropped at the seam.
	Content *string `json:"content"`
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
