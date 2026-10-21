package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
		{"octocorp.ghe.com", "https://api.octocorp.ghe.com/copilot_internal/v2/token"},
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

func TestIsGHEDataResidency(t *testing.T) {
	tests := []struct {
		domain string
		want   bool
	}{
		{"github.com", false},
		{"octocorp.ghe.com", true},
		{"my-tenant.ghe.com", true},
		{"ghe.com", false},
		{"example.ghe.com.test", false},
	}
	for _, tc := range tests {
		if got := isGHEDataResidency(tc.domain); got != tc.want {
			t.Errorf("isGHEDataResidency(%q) = %v, want %v", tc.domain, got, tc.want)
		}
	}
}

func TestExchangeErrorGHEDataResidency(t *testing.T) {
	// Test that GHE data-residency hosts with 403 "Resource not accessible by integration"
	// on the exchange endpoint fall back to user auth validation and succeed
	dataHome := t.TempDir()
	// Write credentials for the ghe.com host
	store := CredentialsFile{Version: 1, Hosts: map[string]HostEntry{
		"octocorp.ghe.com": {OAuthToken: "gho_abc"},
	}}
	data, err := json.Marshal(store)
	if err != nil {
		t.Fatalf("marshal store: %v", err)
	}
	path := StorePath(dataHome)
	if err := writeFilePrivate(path, data); err != nil {
		t.Fatalf("write store: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Token exchange returns 403 (data-residency)
		if r.URL.Path == "/copilot_internal/v2/token" {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"Resource not accessible by integration. For more on scraping GitHub and how it may affect your rights, please review our Terms of Service (https://docs.github.com/en/site-policy/github-terms/github-terms-of-service)"}`)
			return
		}
		// User auth validation returns 200 (works on data-residency)
		if r.URL.Path == "/copilot_internal/user" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"user":"testuser"}`)
			return
		}
		// Models endpoint on copilot-api
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"data":[{"id":"gpt-4o"}]}`)
			return
		}
		// Chat completions
		if r.URL.Path == "/v1/chat/completions" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"hello"}},{"finish_reason":"stop"}]}\n\n`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient("octocorp.ghe.com", dataHome)
	c.exchangeURL = srv.URL + "/copilot_internal/v2/token"
	c.userAuthURL = srv.URL + "/copilot_internal/user"
	c.copilotAPIBase = srv.URL
	
	// The access method should fall back and succeed
	token, apiBase, err := c.access(context.Background())
	if err != nil {
		t.Fatalf("access should succeed with fallback: %v", err)
	}
	if token != "gho_abc" {
		t.Errorf("token = %q, want oauth token", token)
	}
	if apiBase != srv.URL {
		t.Errorf("apiBase = %q, want %q", apiBase, srv.URL)
	}
	
	// ListModels should work with the fallback apiBase
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels should work with fallback: %v", err)
	}
	if len(models) == 0 || models[0].ID != "gpt-4o" {
		t.Errorf("models = %v, want [gpt-4o]", models)
	}
}

func TestExchangeErrorGHEDataResidencyNonMatchingError(t *testing.T) {
	// Test that GHE data-residency hosts with a different 403 error
	// on the exchange endpoint do NOT fall back, and return the standard error
	dataHome := t.TempDir()
	// Write credentials for the ghe.com host
	store := CredentialsFile{Version: 1, Hosts: map[string]HostEntry{
		"octocorp.ghe.com": {OAuthToken: "gho_abc"},
	}}
	data, err := json.Marshal(store)
	if err != nil {
		t.Fatalf("marshal store: %v", err)
	}
	path := StorePath(dataHome)
	if err := writeFilePrivate(path, data); err != nil {
		t.Fatalf("write store: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Token exchange returns 403 with different message
		if r.URL.Path == "/copilot_internal/v2/token" {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"some other forbidden error"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient("octocorp.ghe.com", dataHome)
	c.exchangeURL = srv.URL + "/copilot_internal/v2/token"
	c.userAuthURL = srv.URL + "/copilot_internal/user"
	c.copilotAPIBase = srv.URL
	_, err = c.exchange(context.Background(), "gho_abc")
	if err == nil {
		t.Fatal("exchange should error on 403")
	}
	pe, ok := err.(*llm.ProviderError)
	if !ok {
		t.Fatalf("error type = %T, want *llm.ProviderError", err)
	}
	if pe.Kind != llm.KindAuth {
		t.Errorf("Kind = %s, want auth", pe.Kind)
	}
	// Should fall back to standard error message (NOT the data-residency fallback)
	if !strings.Contains(pe.Message, "some other forbidden error") {
		t.Errorf("message = %q, should contain original error", pe.Message)
	}
}

func TestAccessGHEDataResidencyFallbackFailsUserAuth(t *testing.T) {
	// Test that GHE data-residency fallback fails if user auth validation also fails
	dataHome := t.TempDir()
	// Write credentials for the ghe.com host
	store := CredentialsFile{Version: 1, Hosts: map[string]HostEntry{
		"octocorp.ghe.com": {OAuthToken: "gho_abc"},
	}}
	data, err := json.Marshal(store)
	if err != nil {
		t.Fatalf("marshal store: %v", err)
	}
	path := StorePath(dataHome)
	if err := writeFilePrivate(path, data); err != nil {
		t.Fatalf("write store: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Token exchange returns 403 (data-residency)
		if r.URL.Path == "/copilot_internal/v2/token" {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"Resource not accessible by integration. For more on scraping GitHub and how it may affect your rights, please review our Terms of Service (https://docs.github.com/en/site-policy/github-terms/github-terms-of-service)"}`)
			return
		}
		// User auth validation returns 401
		if r.URL.Path == "/copilot_internal/user" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"Bad credentials"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient("octocorp.ghe.com", dataHome)
	c.exchangeURL = srv.URL + "/copilot_internal/v2/token"
	c.userAuthURL = srv.URL + "/copilot_internal/user"
	c.copilotAPIBase = srv.URL
	
	_, _, err = c.access(context.Background())
	if err == nil {
		t.Fatal("access should fail when user auth also fails")
	}
	pe, ok := err.(*llm.ProviderError)
	if !ok {
		t.Fatalf("error type = %T, want *llm.ProviderError", err)
	}
	if pe.Kind != llm.KindAuth {
		t.Errorf("Kind = %s, want auth", pe.Kind)
	}
	if !strings.Contains(pe.Message, "user auth failed") {
		t.Errorf("message = %q, should mention user auth failed", pe.Message)
	}
}

func TestExchangeErrorNonGHEHost(t *testing.T) {
	// Test that non-GHE hosts with 403 "Resource not accessible by integration"
	// fall back to standard error handling (not the GHE-specific message)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Resource not accessible by integration. For more on scraping GitHub and how it may affect your rights, please review our Terms of Service (https://docs.github.com/en/site-policy/github-terms/github-terms-of-service)"}`)
	}))
	defer srv.Close()

	c := NewClient("github.com", t.TempDir())
	c.exchangeURL = srv.URL
	_, err := c.exchange(context.Background(), "gho_abc")
	if err == nil {
		t.Fatal("exchange should error on 403")
	}
	pe, ok := err.(*llm.ProviderError)
	if !ok {
		t.Fatalf("error type = %T, want *llm.ProviderError", err)
	}
	if pe.Kind != llm.KindAuth {
		t.Errorf("Kind = %s, want auth", pe.Kind)
	}
	// Should NOT have the GHE-specific message
	if strings.Contains(pe.Message, "data-residency tenants") {
		t.Errorf("message = %q, should NOT mention data-residency for github.com", pe.Message)
	}
	// Should fall back to standard error message
	if !strings.Contains(pe.Message, "Resource not accessible by integration") {
		t.Errorf("message = %q, should contain original error", pe.Message)
	}
}
