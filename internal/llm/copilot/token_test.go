package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/okayest-dev/genie/internal/llm"
)

func TestTokenExchangeURL(t *testing.T) {
	tests := []struct {
		domain string
		want   string
	}{
		{"", "https://api.github.com/copilot_internal/v2/token"},
		{"github.com", "https://api.github.com/copilot_internal/v2/token"},
		{"kudelski.ghe.com", "https://api.kudelski.ghe.com/copilot_internal/v2/token"},
	}
	for _, tc := range tests {
		if got := tokenExchangeURL(tc.domain); got != tc.want {
			t.Errorf("tokenExchangeURL(%q) = %q, want %q", tc.domain, got, tc.want)
		}
	}
}

// exchangeServer is a scripted token-exchange server.
type exchangeServer struct {
	mu       sync.Mutex
	token    string
	apiBase  string
	refresh  int
	status   int
	requests []string // Authorization header values received
}

func newExchangeServer() *exchangeServer {
	return &exchangeServer{token: "tid=1;exp=2", apiBase: "https://api.githubcopilot.com", refresh: 1500}
}

func (s *exchangeServer) serve(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Header.Get("Authorization"))
		status, token, apiBase, refresh := s.status, s.token, s.apiBase, s.refresh
		s.mu.Unlock()
		if status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			fmt.Fprintf(w, `{"message":"bad token"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":%q,"endpoints":{"api":%q},"expires_at":%d,"refresh_in":%d}`,
			token, apiBase, 1700000000, refresh)
	}))
}

func TestExchangeSuccess(t *testing.T) {
	ts := newExchangeServer()
	srv := ts.serve(t)
	defer srv.Close()

	c := NewClient("github.com", t.TempDir())
	c.exchangeURL = srv.URL
	tr, err := c.exchange(context.Background(), "gho_abc")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if tr.Token != "tid=1;exp=2" {
		t.Errorf("token = %q, want tid=1;exp=2", tr.Token)
	}
	if tr.Endpoints.API != "https://api.githubcopilot.com" {
		t.Errorf("api base = %q, want default", tr.Endpoints.API)
	}
	if tr.RefreshIn != 1500 {
		t.Errorf("refresh_in = %d, want 1500", tr.RefreshIn)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.requests) != 1 || ts.requests[0] != "token gho_abc" {
		t.Errorf("exchange auth header = %v, want [token gho_abc]", ts.requests)
	}
}

func TestExchangeSendsTokenScheme(t *testing.T) {
	ts := newExchangeServer()
	srv := ts.serve(t)
	defer srv.Close()

	c := NewClient("github.com", t.TempDir())
	c.exchangeURL = srv.URL
	if _, err := c.exchange(context.Background(), "ghu_xyz"); err != nil {
		t.Fatalf("exchange: %v", err)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.requests) != 1 || ts.requests[0] != "token ghu_xyz" {
		t.Errorf("exchange auth header = %v, want [token ghu_xyz] (token scheme, not Bearer)", ts.requests)
	}
}

func TestExchangeAuthErrorMapsToKindAuth(t *testing.T) {
	ts := &exchangeServer{status: 401}
	srv := ts.serve(t)
	defer srv.Close()

	c := NewClient("github.com", t.TempDir())
	c.exchangeURL = srv.URL
	_, err := c.exchange(context.Background(), "gho_stale")
	if err == nil {
		t.Fatal("exchange should error on 401")
	}
	pe, ok := err.(*llm.ProviderError)
	if !ok {
		t.Fatalf("error type = %T, want *llm.ProviderError", err)
	}
	if pe.Kind != llm.KindAuth {
		t.Errorf("Kind = %s, want auth", pe.Kind)
	}
}

func TestExchangeMalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{not json}`)
	}))
	defer srv.Close()

	c := NewClient("github.com", t.TempDir())
	c.exchangeURL = srv.URL
	if _, err := c.exchange(context.Background(), "gho_abc"); err == nil {
		t.Fatal("exchange should error on malformed response")
	}
}

func TestExchangeEmptyToken(t *testing.T) {
	ts := &exchangeServer{token: ""}
	srv := ts.serve(t)
	defer srv.Close()

	c := NewClient("github.com", t.TempDir())
	c.exchangeURL = srv.URL
	if _, err := c.exchange(context.Background(), "gho_abc"); err == nil {
		t.Fatal("exchange should error on empty token")
	}
}

func TestJsonErrorText(t *testing.T) {
	body, _ := json.Marshal(map[string]string{"message": "boom"})
	if got := jsonErrorText(body); got != "boom" {
		t.Errorf("flat message = %q, want boom", got)
	}
	body, _ = json.Marshal(map[string]any{"error": map[string]any{"message": "nested boom"}})
	if got := jsonErrorText(body); got != "nested boom" {
		t.Errorf("nested message = %q, want nested boom", got)
	}
	if got := jsonErrorText([]byte(`{"unrelated": 1}`)); got != "" {
		t.Errorf("unrecognized body = %q, want empty", got)
	}
	if got := jsonErrorText([]byte(`{not json`)); got != "" {
		t.Errorf("malformed body = %q, want empty", got)
	}
}
