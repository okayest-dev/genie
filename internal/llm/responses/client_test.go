package responses

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
			"object": "list",
			"data": []map[string]any{
				{"id": "gpt-4o", "object": "model"},
				{"id": "gpt-4o-mini", "object": "model"},
			},
		})
	}))
	defer srv.Close()

	models, err := NewClient(srv.URL, "").ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0].ID != "gpt-4o" || models[1].ID != "gpt-4o-mini" {
		t.Errorf("models = %+v, want gpt-4o and gpt-4o-mini", models)
	}
}

func TestErrorFromStatusAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "Invalid API key"},
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
	if pe.Message != "Invalid API key" {
		t.Errorf("message = %q, want %q", pe.Message, "Invalid API key")
	}
}

func TestErrorFromStatusRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "rate limited"},
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
			"error": map[string]any{"message": "bad request"},
		})
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "").ListModels(context.Background())
	var pe *llm.ProviderError
	if !errors.As(err, &pe) || pe.Kind != llm.KindInvalidRequest {
		t.Errorf("error = %v, want KindInvalidRequest", err)
	}
}

func TestDoRequestSetsBearerAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", r.Header.Get("Authorization"), "Bearer test-key")
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

func TestDoRequestOmitsAuthWhenEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Authorization = %q, want empty", r.Header.Get("Authorization"))
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

func TestInputToWireMessages(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "You are helpful"},
		{Role: llm.RoleUser, Content: "Hello"},
		{Role: llm.RoleAssistant, Content: "Hi there"},
	}
	input := inputToWire(msgs)
	if len(input) != 2 {
		t.Fatalf("input = %d, want 2 (system skipped)", len(input))
	}
	if input[0]["role"] != "user" {
		t.Errorf("input[0].role = %v, want user", input[0]["role"])
	}
	if input[1]["role"] != "assistant" {
		t.Errorf("input[1].role = %v, want assistant", input[1]["role"])
	}
}

func TestInputToWireToolCall(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "call_123", Name: "read", Arguments: `{"path":"/x.go"}`},
		}},
	}
	input := inputToWire(msgs)
	if len(input) != 1 {
		t.Fatalf("input = %d, want 1", len(input))
	}
	if input[0]["type"] != "function_call" {
		t.Errorf("type = %v, want function_call", input[0]["type"])
	}
	if input[0]["call_id"] != "call_123" {
		t.Errorf("call_id = %v, want call_123", input[0]["call_id"])
	}
}

func TestInputToWireToolResult(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "file contents", ToolCallID: "call_456"},
	}
	input := inputToWire(msgs)
	if len(input) != 1 {
		t.Fatalf("input = %d, want 1", len(input))
	}
	if input[0]["type"] != "function_call_output" {
		t.Errorf("type = %v, want function_call_output", input[0]["type"])
	}
	if input[0]["call_id"] != "call_456" {
		t.Errorf("call_id = %v, want call_456", input[0]["call_id"])
	}
}

func TestInstructionsToWire(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "Be helpful"},
		{Role: llm.RoleUser, Content: "Hi"},
	}
	ins := instructionsToWire(msgs)
	if ins != "Be helpful" {
		t.Errorf("instructions = %q, want %q", ins, "Be helpful")
	}
}

func TestInstructionsToWireNoSystem(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "Hi"},
	}
	ins := instructionsToWire(msgs)
	if ins != "" {
		t.Errorf("instructions = %q, want empty", ins)
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
	arr := result.([]map[string]any)
	if len(arr) != 1 {
		t.Fatalf("tools len = %d, want 1", len(arr))
	}
	if arr[0]["name"] != "read" {
		t.Errorf("name = %v, want read", arr[0]["name"])
	}
	if arr[0]["type"] != "function" {
		t.Errorf("type = %v, want function", arr[0]["type"])
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

func TestStreamTextResponse(t *testing.T) {
	sseBody := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\" world\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2,\"total_tokens\":7}}}\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sseBody))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	iter, err := c.Stream(context.Background(), llm.Request{
		Model:    "gpt-4o",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var texts []string
	var gotFinish bool
	var gotUsage bool
	for ev := range iter {
		switch ev.Kind {
		case llm.EventText:
			texts = append(texts, ev.Text)
		case llm.EventFinish:
			gotFinish = true
		case llm.EventUsage:
			gotUsage = true
			if ev.Usage.PromptTokens != 5 || ev.Usage.CompletionTokens != 2 {
				t.Errorf("usage = %+v, want prompt=5 completion=2", ev.Usage)
			}
		}
	}
	if len(texts) != 2 || texts[0] != "Hello" || texts[1] != " world" {
		t.Errorf("texts = %+v, want [Hello, world]", texts)
	}
	if !gotFinish {
		t.Error("no finish event")
	}
	if !gotUsage {
		t.Error("no usage event")
	}
}

func TestStreamToolCallResponse(t *testing.T) {
	sseBody := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"get_weather\",\"arguments\":\"\"}}\n\n" +
		"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"{\\\"loc\"}\n\n" +
		"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"ation\":\"Paris\"}\"}\n\n" +
		"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"output_index\":0,\"name\":\"get_weather\",\"arguments\":\"{\\\"location\\\":\\\"Paris\\\"}\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sseBody))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	iter, err := c.Stream(context.Background(), llm.Request{
		Model:    "gpt-4o",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "What is the weather?"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var gotToolCall bool
	for ev := range iter {
		if ev.Kind == llm.EventToolCall {
			gotToolCall = true
			if len(ev.ToolCalls) != 1 {
				t.Fatalf("tool calls = %d, want 1", len(ev.ToolCalls))
			}
			tc := ev.ToolCalls[0]
			if tc.Name != "get_weather" {
				t.Errorf("name = %q, want get_weather", tc.Name)
			}
			if tc.ID != "call_1" {
				t.Errorf("id = %q, want call_1", tc.ID)
			}
			if tc.Arguments != `{"location":"Paris"}` {
				t.Errorf("arguments = %q, want %q", tc.Arguments, `{"location":"Paris"}`)
			}
		}
	}
	if !gotToolCall {
		t.Error("no tool call event")
	}
}

func TestStreamErrorResponse(t *testing.T) {
	sseBody := "event: error\ndata: {\"type\":\"error\",\"message\":\"Something went wrong\"}\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sseBody))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	iter, err := c.Stream(context.Background(), llm.Request{
		Model:    "gpt-4o",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var gotError bool
	for ev := range iter {
		if ev.Kind == llm.EventError {
			gotError = true
			if ev.Err.Error() != "Something went wrong" {
				t.Errorf("error = %v, want %q", ev.Err, "Something went wrong")
			}
		}
	}
	if !gotError {
		t.Error("no error event")
	}
}
