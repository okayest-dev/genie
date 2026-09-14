package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/okayest-dev/genie/internal/llm"
)

func TestParseChunk(t *testing.T) {
	content := "hello"
	ch := chunk{Choices: []choice{{Delta: delta{Content: &content}}}}
	events := parseChunk(ch)
	if len(events) != 1 || events[0].Kind != llm.EventText || events[0].Text != "hello" {
		t.Errorf("text chunk events = %+v, want one text event", events)
	}

	reason := "stop"
	ch = chunk{Choices: []choice{{Delta: delta{}, FinishReason: &reason}}}
	events = parseChunk(ch)
	if len(events) != 1 || events[0].Kind != llm.EventFinish || events[0].End != llm.FinishStop {
		t.Errorf("finish chunk events = %+v, want finish stop", events)
	}
}

func TestParseChunkUsageOnly(t *testing.T) {
	ch := chunk{Usage: &usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}}
	events := parseChunk(ch)
	if len(events) != 1 || events[0].Kind != llm.EventUsage {
		t.Fatalf("usage chunk events = %+v, want one usage event", events)
	}
	if events[0].Usage.PromptTokens != 1 || events[0].Usage.CompletionTokens != 2 || events[0].Usage.TotalTokens != 3 {
		t.Errorf("usage = %+v, want (1,2,3)", events[0].Usage)
	}
}

func TestParseChunkError(t *testing.T) {
	ch := chunk{Error: &wireError{Message: "provider exploded"}}
	events := parseChunk(ch)
	if len(events) != 1 || events[0].Kind != llm.EventError || events[0].Err == nil {
		t.Fatalf("error chunk events = %+v, want one error event", events)
	}
	if got := events[0].Err.Error(); got != "provider exploded" {
		t.Errorf("error message = %q, want provider exploded", got)
	}
}

func TestErrorChunkDefaultsMessage(t *testing.T) {
	ch := chunk{Error: &wireError{}}
	events := parseChunk(ch)
	if len(events) != 1 || events[0].Kind != llm.EventError {
		t.Fatalf("error chunk events = %+v, want one error event", events)
	}
	if got := events[0].Err.Error(); got != "copilot provider error" {
		t.Errorf("error message = %q, want copilot provider error", got)
	}
}

func TestCanonicalFinishReason(t *testing.T) {
	cases := map[string]llm.FinishReason{
		"stop":         llm.FinishStop,
		"tool_calls":   llm.FinishToolCalls,
		"length":       llm.FinishLength,
		"max_tokens":   llm.FinishOther,
		"content_filt": llm.FinishOther,
		"":             llm.FinishOther,
		"bogus":        llm.FinishOther,
	}
	for in, want := range cases {
		if got := canonicalFinishReason(in); got != want {
			t.Errorf("canonicalFinishReason(%q) = %q, want %q", in, got, want)
		}
	}
}

// chatServer is a scripted copilot chat API: token exchange + chat/models.
type chatServer struct {
	mu           sync.Mutex
	modelStream  []string // SSE chunks for /v1/chat/completions
	models       []string // model IDs for /v1/models
	chatBody     string   // last chat payload received
	listModels   int      // count of /v1/models requests
	exchangeHits int      // count of token exchanges
	auth         string   // Authorization header on the chat request
}

func (s *chatServer) serve(t *testing.T, withCreds bool) (srv *httptest.Server, dataHome string) {
	t.Helper()
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/copilot_internal/v2/token":
			s.mu.Lock()
			s.exchangeHits++
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"token":"tid=1;exp=2","endpoints":{"api":%q},"expires_at":%d,"refresh_in":1500}`,
				srv.URL, 1700000000)
		case "/v1/chat/completions":
			s.mu.Lock()
			body, _ := io.ReadAll(r.Body)
			s.chatBody = string(body)
			s.auth = r.Header.Get("Authorization")
			chunks := s.modelStream
			s.mu.Unlock()
			assertCopilotHeaders(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			for _, c := range chunks {
				io.WriteString(w, "data: "+c+"\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		case "/v1/models":
			s.mu.Lock()
			s.listModels++
			models := s.models
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":[%s]}`, modelJSON(models))
		default:
			http.NotFound(w, r)
		}
	}))
	dataHome = t.TempDir()
	if withCreds {
		writeCreds(t, dataHome, "github.com")
	}
	return srv, dataHome
}

func assertCopilotHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	want := map[string]string{
		"Editor-Version":         "genie/0.1.0",
		"Editor-Plugin-Version":  "genie-copilot/0.1.0",
		"Copilot-Integration-Id": "vscode-chat",
		"User-Agent":             "GithubCopilot/genie-0.1.0",
	}
	for k, v := range want {
		if got := r.Header.Get(k); got != v {
			t.Errorf("%s header = %q, want %q", k, got, v)
		}
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		t.Errorf("Authorization = %q, want Bearer scheme", r.Header.Get("Authorization"))
	}
}

// writeCreds writes a credential store for github.com.
func writeCreds(t *testing.T, dataHome, host string) {
	t.Helper()
	store := credentialsFile{Version: 1, Hosts: map[string]hostEntry{
		host: {OAuthToken: "gho_creds"},
	}}
	data, err := json.Marshal(store)
	if err != nil {
		t.Fatalf("marshal store: %v", err)
	}
	path := StorePath(dataHome)
	if err := writeFilePrivate(path, data); err != nil {
		t.Fatalf("write store: %v", err)
	}
}

func writeFilePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func modelJSON(models []string) string {
	parts := make([]string, len(models))
	for i, m := range models {
		b, _ := json.Marshal(map[string]string{"id": m, "object": "model"})
		parts[i] = string(b)
	}
	return strings.Join(parts, ",")
}

func newTestClient(srv *httptest.Server, dataHome string) *Client {
	c := NewClient("github.com", dataHome)
	c.exchangeURL = srv.URL + "/copilot_internal/v2/token"
	return c
}

func TestStreamEmitsTextAndFinish(t *testing.T) {
	cs := &chatServer{modelStream: []string{textChunk("hi"), finishChunk("stop")}}
	srv, dataHome := cs.serve(t, true)
	defer srv.Close()

	c := newTestClient(srv, dataHome)
	i, err := c.Stream(context.Background(), llm.Request{Model: "gpt-4o", Messages: []llm.Message{{Role: llm.RoleUser, Content: "yo"}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var texts []string
	var finished bool
	for ev := range i {
		switch ev.Kind {
		case llm.EventText:
			texts = append(texts, ev.Text)
		case llm.EventFinish:
			finished = true
		}
	}
	if strings.Join(texts, "") != "hi" {
		t.Errorf("texts = %v, want [hi]", texts)
	}
	if !finished {
		t.Error("no finish event emitted")
	}
}

func TestStreamToolCalls(t *testing.T) {
	cs := &chatServer{
		modelStream: []string{
			`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"t1","type":"function","function":{"name":"read","arguments":""}}]},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"/etc/hosts"}}]},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		},
	}
	srv, dataHome := cs.serve(t, true)
	defer srv.Close()

	c := newTestClient(srv, dataHome)
	i, err := c.Stream(context.Background(), llm.Request{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var calls []llm.ToolCall
	for ev := range i {
		if ev.Kind == llm.EventToolCall {
			calls = append(calls, ev.ToolCalls...)
		}
	}
	if len(calls) != 1 || calls[0].ID != "t1" || calls[0].Name != "read" || calls[0].Arguments != "/etc/hosts" {
		t.Errorf("tool calls = %+v, want the accumulated read(/etc/hosts)", calls)
	}
}

func TestStreamUsage(t *testing.T) {
	cs := &chatServer{modelStream: []string{usageChunk(5, 7, 12)}}
	srv, dataHome := cs.serve(t, true)
	defer srv.Close()

	c := newTestClient(srv, dataHome)
	i, err := c.Stream(context.Background(), llm.Request{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var got llm.Usage
	for ev := range i {
		if ev.Kind == llm.EventUsage {
			got = ev.Usage
		}
	}
	if got.PromptTokens != 5 || got.CompletionTokens != 7 || got.TotalTokens != 12 {
		t.Errorf("usage = %+v, want (5,7,12)", got)
	}
}

func TestStreamNoCredentials(t *testing.T) {
	cs := &chatServer{}
	srv, dataHome := cs.serve(t, false)
	defer srv.Close()

	c := newTestClient(srv, dataHome)
	_, err := c.Stream(context.Background(), llm.Request{Model: "gpt-4o"})
	if err == nil {
		t.Fatal("Stream without credentials should error")
	}
	if !strings.Contains(err.Error(), "credentials") {
		t.Errorf("error = %q, want a credentials message", err)
	}
}

func TestStreamExchangeOnceThenCached(t *testing.T) {
	cs := &chatServer{modelStream: []string{textChunk("a"), finishChunk("stop")}}
	srv, dataHome := cs.serve(t, true)
	defer srv.Close()

	c := newTestClient(srv, dataHome)
	for i := 0; i < 3; i++ {
		st, err := c.Stream(context.Background(), llm.Request{Model: "gpt-4o"})
		if err != nil {
			t.Fatalf("Stream #%d: %v", i, err)
		}
		for range st {
		}
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.exchangeHits != 1 {
		t.Errorf("token exchanges = %d, want 1 (cached after first)", cs.exchangeHits)
	}
	if cs.listModels != 0 {
		t.Errorf("/v1/models calls = %d, want 0", cs.listModels)
	}
}

func TestListModels(t *testing.T) {
	cs := &chatServer{models: []string{"gpt-4o", "claude-sonnet-4-5"}}
	srv, dataHome := cs.serve(t, true)
	defer srv.Close()

	c := newTestClient(srv, dataHome)
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	var ids []string
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	if len(ids) != 2 || ids[0] != "gpt-4o" || ids[1] != "claude-sonnet-4-5" {
		t.Errorf("models = %v, want [gpt-4o claude-sonnet-4-5]", ids)
	}
}

func TestListModelsNoCredentials(t *testing.T) {
	cs := &chatServer{}
	srv, dataHome := cs.serve(t, false)
	defer srv.Close()

	c := newTestClient(srv, dataHome)
	if _, err := c.ListModels(context.Background()); err == nil {
		t.Fatal("ListModels without credentials should error")
	}
}

func TestClientGHEIdentityHeaders(t *testing.T) {
	cs := &chatServer{modelStream: []string{finishChunk("stop")}}
	srv, dataHome := cs.serve(t, true)
	defer srv.Close()

	c := NewClient("kudelski.ghe.com", dataHome)
	c.exchangeURL = srv.URL + "/copilot_internal/v2/token"

	// Write credentials for the GHE host too, so the store lookup succeeds.
	writeCreds(t, dataHome, "kudelski.ghe.com")

	st, err := c.Stream(context.Background(), llm.Request{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range st {
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if !strings.HasPrefix(cs.auth, "Bearer ") {
		t.Errorf("Authorization = %q, want Bearer scheme", cs.auth)
	}
}

func TestModelInfoUnknown(t *testing.T) {
	c := NewClient("github.com", t.TempDir())
	info, err := c.ModelInfo(context.Background(), "gpt-4o")
	if err != nil {
		t.Fatalf("ModelInfo: %v", err)
	}
	if info.ContextLength != 0 {
		t.Errorf("ContextLength = %d, want 0 (unknown)", info.ContextLength)
	}
}

func TestStreamFullRequestShape(t *testing.T) {
	cs := &chatServer{modelStream: []string{textChunk(longText), finishChunk("stop")}}
	srv, dataHome := cs.serve(t, true)
	defer srv.Close()

	c := newTestClient(srv, dataHome)
	req := llm.Request{
		Model: "gpt-4o",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "sum files"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "t1", Name: "bash", Arguments: "ls"}}},
			{Role: llm.RoleTool, ToolCallID: "t1", Content: "ok"},
		},
		Tools: []llm.ToolDef{{Name: "bash", Description: "run", Parameters: map[string]any{"type": "object"}}},
	}
	i, err := c.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var longest string
	for ev := range i {
		if ev.Kind == llm.EventText && len(ev.Text) > len(longest) {
			longest = ev.Text
		}
	}
	if longest != longText {
		t.Errorf("longest text delta shorter than the full chunk content")
	}
	cs.mu.Lock()
	body := cs.chatBody
	cs.mu.Unlock()
	for _, want := range []string{`"role":"assistant"`, `"tool_calls"`, `"tool_call_id":"t1"`, `"parameters":{"type":"object"}`, `"tools"`} {
		if !strings.Contains(body, want) {
			t.Errorf("chat body missing %s:\n%s", want, body)
		}
	}
}

func TestDefaultXDGDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	if got := defaultXDGDataHome(); got != "/tmp/xdg" {
		t.Errorf("defaultXDGDataHome() with XDG_DATA_HOME = %q, want /tmp/xdg", got)
	}
	t.Setenv("XDG_DATA_HOME", "")
	if got := defaultXDGDataHome(); got == "" || got == "." {
		t.Errorf("defaultXDGDataHome() without XDG_DATA_HOME = %q, want the ~/.local/share fallback", got)
	}
}

func TestProviderErrorFromAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/copilot_internal/v2/token":
			fmt.Fprintf(w, `{"token":"t","endpoints":{"api":%q},"refresh_in":1500}`, "http://"+r.Host)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"message":"model not supported"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dataHome := t.TempDir()
	writeCreds(t, dataHome, "github.com")
	c := newTestClient(srv, dataHome)
	_, err := c.Stream(context.Background(), llm.Request{Model: "gpt-4o"})
	if err == nil {
		t.Fatal("Stream should error on non-2xx chat response")
	}
	pe, ok := err.(*llm.ProviderError)
	if !ok {
		t.Fatalf("error type = %T, want *llm.ProviderError", err)
	}
	if !strings.Contains(pe.Message, "model not supported") {
		t.Errorf("error message = %q, want the provider body message", pe.Message)
	}
}

func TestApiErrorKindMapping(t *testing.T) {
	cases := []struct {
		status int
		kind   llm.ErrorKind
	}{
		{401, llm.KindAuth},
		{403, llm.KindAuth},
		{400, llm.KindInvalidRequest},
		{429, llm.KindRateLimit},
		{500, llm.KindOther},
	}
	for _, tc := range cases {
		body := ""
		if tc.status != 500 {
			body = `{"error":{"message":"nope"}}`
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/copilot_internal/v2/token":
				fmt.Fprintf(w, `{"token":"t","endpoints":{"api":%q},"refresh_in":1500}`, "http://"+r.Host)
			case "/v1/chat/completions":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, body)
			default:
				http.NotFound(w, r)
			}
		}))
		dataHome := t.TempDir()
		writeCreds(t, dataHome, "github.com")
		c := newTestClient(srv, dataHome)
		_, err := c.Stream(context.Background(), llm.Request{Model: "gpt-4o"})
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: Stream should error", tc.status)
		}
		pe, ok := err.(*llm.ProviderError)
		if !ok {
			t.Fatalf("status %d: error type = %T, want *llm.ProviderError", tc.status, err)
		}
		if pe.Kind != tc.kind {
			t.Errorf("status %d: Kind = %s, want %s", tc.status, pe.Kind, tc.kind)
		}
	}
}

// textChunk builds a chat completion chunk with a content delta.
func textChunk(content string) string {
	return fmt.Sprintf(`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`, content)
}

// longText is a text delta long enough to exercise the preview truncation.
const longText = "a very long text delta that exceeds the eighty character preview limit of the debug logging helper for sure"

// finishChunk builds the terminal chunk carrying a finish reason.
func finishChunk(reason string) string {
	return fmt.Sprintf(`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":%q}]}`, reason)
}

// usageChunk builds an include_usage chunk.
func usageChunk(p, c, t int) string {
	return fmt.Sprintf(`{"id":"c1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`, p, c, t)
}
