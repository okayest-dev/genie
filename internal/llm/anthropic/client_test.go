package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/okayest-dev/og/internal/llm"
)

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "claude-fable-5"},
				{"id": "claude-sonnet-4"},
			},
		})
	}))
	defer srv.Close()

	models, err := NewClient(srv.URL, "").ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0].ID != "claude-fable-5" || models[1].ID != "claude-sonnet-4" {
		t.Errorf("models = %+v, want claude-fable-5 and claude-sonnet-4", models)
	}
}

func TestErrorFromStatusAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"type":    "authentication_error",
				"message": "invalid x-api-key header",
			},
		})
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "bad-key").ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error for 401")
	}
	var pe *llm.ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error = %T, want *llm.ProviderError", err)
	}
	if pe.Kind != llm.KindAuth {
		t.Errorf("kind = %q, want %q", pe.Kind, llm.KindAuth)
	}
	if pe.Message != "invalid x-api-key header" {
		t.Errorf("message = %q, want %q", pe.Message, "invalid x-api-key header")
	}
}

func TestErrorFromStatusRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"type": "rate_limit_error", "message": "rate limited"},
		})
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "").ListModels(context.Background())
	var pe *llm.ProviderError
	if !errors.As(err, &pe) || pe.Kind != llm.KindRateLimit {
		t.Errorf("error = %v, want KindRateLimit", err)
	}
}

func TestErrorFromStatusInvalidRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"type": "invalid_request_error", "message": "bad request"},
		})
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "").ListModels(context.Background())
	var pe *llm.ProviderError
	if !errors.As(err, &pe) || pe.Kind != llm.KindInvalidRequest {
		t.Errorf("error = %v, want KindInvalidRequest", err)
	}
}

func TestErrorFromStatusOverloaded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(529)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"type": "overloaded_error", "message": "overloaded"},
		})
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "").ListModels(context.Background())
	var pe *llm.ProviderError
	if !errors.As(err, &pe) || pe.Kind != llm.KindRateLimit {
		t.Errorf("error = %v, want KindRateLimit for 529", err)
	}
}

func TestExtractSystem(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "Be helpful"},
		{Role: llm.RoleUser, Content: "Hi"},
	}
	system, rest := extractSystem(msgs)
	if system != "Be helpful" {
		t.Errorf("system = %q, want %q", system, "Be helpful")
	}
	if len(rest) != 1 || rest[0].Role != llm.RoleUser {
		t.Errorf("rest = %+v, want one user message", rest)
	}
}

func TestExtractSystemNoSystem(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "Hi"},
	}
	system, rest := extractSystem(msgs)
	if system != "" {
		t.Errorf("system = %q, want empty", system)
	}
	if len(rest) != 1 {
		t.Errorf("rest = %d, want 1", len(rest))
	}
}

func TestExtractSystemEmpty(t *testing.T) {
	system, rest := extractSystem(nil)
	if system != "" || len(rest) != 0 {
		t.Errorf("system=%q rest=%v, want empty", system, rest)
	}
}

func TestMessagesToWire(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "Hello"},
		{Role: llm.RoleAssistant, Content: "Hi there"},
	}
	wire := messagesToWire(msgs)
	if len(wire) != 2 {
		t.Fatalf("messages = %d, want 2", len(wire))
	}
	if wire[0]["role"] != "user" || wire[0]["content"] != "Hello" {
		t.Errorf("msg[0] = %+v, want user/Hello", wire[0])
	}
	if wire[1]["role"] != "assistant" || wire[1]["content"] != "Hi there" {
		t.Errorf("msg[1] = %+v, want assistant/Hi there", wire[1])
	}
}

func TestMessagesToWireToolCall(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, Content: "Let me check", ToolCalls: []llm.ToolCall{
			{ID: "toolu_123", Name: "read", Arguments: `{"path":"/x.go"}`},
		}},
	}
	wire := messagesToWire(msgs)
	if len(wire) != 1 {
		t.Fatalf("messages = %d, want 1", len(wire))
	}
	content := wire[0]["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content blocks = %d, want 2 (text + tool_use)", len(content))
	}
	text := content[0].(map[string]any)
	if text["type"] != "text" || text["text"] != "Let me check" {
		t.Errorf("text block = %+v", text)
	}
	tu := content[1].(map[string]any)
	if tu["type"] != "tool_use" || tu["id"] != "toolu_123" || tu["name"] != "read" {
		t.Errorf("tool_use block = %+v", tu)
	}
}

func TestMessagesToWireToolResult(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "file contents", ToolCallID: "toolu_123"},
	}
	wire := messagesToWire(msgs)
	if len(wire) != 1 {
		t.Fatalf("messages = %d, want 1", len(wire))
	}
	if wire[0]["role"] != "user" {
		t.Errorf("role = %v, want user", wire[0]["role"])
	}
	content := wire[0]["content"].([]any)
	tr := content[0].(map[string]any)
	if tr["type"] != "tool_result" || tr["tool_use_id"] != "toolu_123" {
		t.Errorf("tool_result = %+v", tr)
	}
}

func TestMessagesToWireToolResultsMerge(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "result1", ToolCallID: "toolu_1"},
		{Role: llm.RoleUser, Content: "result2", ToolCallID: "toolu_2"},
	}
	wire := messagesToWire(msgs)
	if len(wire) != 1 {
		t.Fatalf("messages = %d, want 1 (merged)", len(wire))
	}
	content := wire[0]["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content blocks = %d, want 2 merged tool_results", len(content))
	}
}

func TestToolsToWire(t *testing.T) {
	tools := []llm.ToolDef{
		{Name: "read", Description: "Read a file", Parameters: map[string]any{"type": "object"}},
	}
	result := toolsToWire(tools)
	if result == nil {
		t.Fatal("tools = nil, want array")
	}
	if len(result) != 1 {
		t.Fatalf("tools = %d, want 1", len(result))
	}
	if result[0]["name"] != "read" {
		t.Errorf("name = %v, want read", result[0]["name"])
	}
	if result[0]["input_schema"] == nil {
		t.Error("input_schema = nil, want object")
	}
}

func TestToolsToWireEmpty(t *testing.T) {
	if result := toolsToWire(nil); result != nil {
		t.Errorf("tools = %+v, want nil", result)
	}
	if result := toolsToWire([]llm.ToolDef{}); result != nil {
		t.Errorf("tools = %+v, want nil", result)
	}
}

func TestDoRequestSetsAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("x-api-key = %q, want test-key", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("anthropic-version = %q, want 2023-06-01", r.Header.Get("anthropic-version"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key")
	resp, err := c.doRequest(context.Background(), http.MethodGet, "/test", nil)
	if err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	resp.Body.Close()
}

func TestDoRequestOmitsAPIKeyWhenEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "" {
			t.Errorf("x-api-key = %q, want empty", r.Header.Get("x-api-key"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	resp, err := c.doRequest(context.Background(), http.MethodGet, "/test", nil)
	if err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	resp.Body.Close()
}
