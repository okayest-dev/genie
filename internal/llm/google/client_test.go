package google

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
			"models": []map[string]any{
				{"name": "models/gemini-2.0-flash", "displayName": "Gemini 2.0 Flash"},
				{"name": "models/gemini-2.5-pro", "displayName": "Gemini 2.5 Pro"},
			},
		})
	}))
	defer srv.Close()

	models, err := NewClient(srv.URL, "").ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0].ID != "gemini-2.0-flash" || models[1].ID != "gemini-2.5-pro" {
		t.Errorf("models = %+v, want gemini-2.0-flash and gemini-2.5-pro", models)
	}
}

func TestListModelsStripsModelsPrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "models/gemini-2.0-flash"},
			},
		})
	}))
	defer srv.Close()

	models, err := NewClient(srv.URL, "").ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gemini-2.0-flash" {
		t.Errorf("models[0].ID = %q, want gemini-2.0-flash (no models/ prefix)", models[0].ID)
	}
}

func TestErrorFromStatusAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "API key not valid",
				"status":  "UNAUTHENTICATED",
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
	if pe.Message != "API key not valid" {
		t.Errorf("message = %q, want %q", pe.Message, "API key not valid")
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

func TestMessagesToWire(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "You are helpful"},
		{Role: llm.RoleUser, Content: "Hello"},
		{Role: llm.RoleAssistant, Content: "Hi there"},
	}
	contents := contentsToWire(msgs)
	if len(contents) != 2 {
		t.Fatalf("contents = %d, want 2 (system skipped)", len(contents))
	}
	if contents[0]["role"] != "user" {
		t.Errorf("contents[0].role = %v, want user", contents[0]["role"])
	}
	if contents[1]["role"] != "model" {
		t.Errorf("contents[1].role = %v, want model", contents[1]["role"])
	}
}

func TestMessagesToWireToolCall(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{Name: "read", Arguments: `{"path":"/x.go"}`},
		}},
	}
	contents := contentsToWire(msgs)
	if len(contents) != 1 {
		t.Fatalf("contents = %d, want 1", len(contents))
	}
	parts := contents[0]["parts"].([]map[string]any)
	if len(parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(parts))
	}
	fc := parts[0]["functionCall"].(map[string]any)
	if fc["name"] != "read" {
		t.Errorf("functionCall.name = %v, want read", fc["name"])
	}
}

func TestMessagesToWireToolResult(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "file contents", ToolCallID: "call_123"},
	}
	contents := contentsToWire(msgs)
	if len(contents) != 1 {
		t.Fatalf("contents = %d, want 1", len(contents))
	}
	parts := contents[0]["parts"].([]map[string]any)
	fr := parts[0]["functionResponse"].(map[string]any)
	if fr["name"] != "call_123" {
		t.Errorf("functionResponse.name = %v, want call_123", fr["name"])
	}
}

func TestSystemInstructionToWire(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "Be helpful"},
		{Role: llm.RoleUser, Content: "Hi"},
	}
	si := systemInstructionToWire(msgs)
	if si == nil {
		t.Fatal("systemInstruction = nil, want object")
	}
	parts := si["parts"].([]map[string]any)
	if len(parts) != 1 || parts[0]["text"] != "Be helpful" {
		t.Errorf("parts = %+v, want [{text: Be helpful}]", parts)
	}
}

func TestSystemInstructionToWireNoSystem(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "Hi"},
	}
	si := systemInstructionToWire(msgs)
	if si != nil {
		t.Errorf("systemInstruction = %+v, want nil", si)
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
	funcs := arr[0]["functionDeclarations"].([]map[string]any)
	if len(funcs) != 1 || funcs[0]["name"] != "read" {
		t.Errorf("functions = %+v, want [{name: read}]", funcs)
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

func TestDoRequestSetsApiKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("x-goog-api-key = %q, want test-key", r.Header.Get("x-goog-api-key"))
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

func TestDoRequestOmitsApiKeyWhenEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "" {
			t.Errorf("x-goog-api-key = %q, want empty", r.Header.Get("x-goog-api-key"))
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
