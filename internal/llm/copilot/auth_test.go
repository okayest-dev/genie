package copilot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// overrideOAuthHostBase points device-flow endpoints at a test server.
func overrideOAuthHostBase(t *testing.T, base string) {
	t.Helper()
	prev := oauthHostBaseURL
	oauthHostBaseURL = func(string) string { return base }
	t.Cleanup(func() { oauthHostBaseURL = prev })
}

// overrideRESTAPIBase points the REST API root at a test server.
func overrideRESTAPIBase(t *testing.T, base string) {
	t.Helper()
	prev := restAPIBaseURL
	restAPIBaseURL = func(string) string { return base }
	t.Cleanup(func() { restAPIBaseURL = prev })
}

func TestOAuthHostBaseURL(t *testing.T) {
	cases := []struct {
		domain string
		want   string
	}{
		{"", "https://github.com"},
		{"github.com", "https://github.com"},
		{"octocorp.ghe.com", "https://octocorp.ghe.com"},
	}
	for _, tc := range cases {
		if got := oauthHostBaseURL(tc.domain); got != tc.want {
			t.Errorf("oauthHostBaseURL(%q) = %q, want %q", tc.domain, got, tc.want)
		}
	}
}

func TestRESTAPIBaseURL(t *testing.T) {
	cases := []struct {
		domain string
		want   string
	}{
		{"", "https://api.github.com"},
		{"github.com", "https://api.github.com"},
		{"octocorp.ghe.com", "https://api.octocorp.ghe.com"},
	}
	for _, tc := range cases {
		if got := restAPIBaseURL(tc.domain); got != tc.want {
			t.Errorf("restAPIBaseURL(%q) = %q, want %q", tc.domain, got, tc.want)
		}
	}
}

func TestStartDeviceFlow(t *testing.T) {
	var gotClientID, gotScope string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/device/code" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		gotClientID = r.PostForm.Get("client_id")
		gotScope = r.PostForm.Get("scope")
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		_, _ = w.Write([]byte("device_code=DEV123&user_code=WDJB-MJHT&verification_uri=" +
			"https%3A%2F%2Fgithub.com%2Flogin%2Fdevice&expires_in=900&interval=5"))
	}))
	defer srv.Close()
	overrideOAuthHostBase(t, srv.URL)

	dcr, err := StartDeviceFlow(context.Background(), "github.com")
	if err != nil {
		t.Fatalf("StartDeviceFlow: %v", err)
	}
	if dcr.DeviceCode != "DEV123" {
		t.Errorf("DeviceCode = %q, want DEV123", dcr.DeviceCode)
	}
	if dcr.UserCode != "WDJB-MJHT" {
		t.Errorf("UserCode = %q, want WDJB-MJHT", dcr.UserCode)
	}
	if dcr.VerificationURI != "https://github.com/login/device" {
		t.Errorf("VerificationURI = %q, want the device URL", dcr.VerificationURI)
	}
	if dcr.Interval != 5 || dcr.ExpiresIn != 900 {
		t.Errorf("Interval/ExpiresIn = %d/%d, want 5/900", dcr.Interval, dcr.ExpiresIn)
	}
	if gotClientID != VSCodeClientID {
		t.Errorf("client_id = %q, want %q", gotClientID, VSCodeClientID)
	}
	if gotScope != "read:user" {
		t.Errorf("scope = %q, want read:user", gotScope)
	}
}

func TestStartDeviceFlowServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("error=server_error"))
	}))
	defer srv.Close()
	overrideOAuthHostBase(t, srv.URL)

	if _, err := StartDeviceFlow(context.Background(), "github.com"); err == nil {
		t.Fatal("StartDeviceFlow should error on a non-2xx device code response")
	}
}

func TestStartDeviceFlowContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		_, _ = w.Write([]byte("device_code=DEV&user_code=U&verification_uri=https%3A%2F%2Fg%2Fd&expires_in=900&interval=5"))
	}))
	defer srv.Close()
	overrideOAuthHostBase(t, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := StartDeviceFlow(ctx, "github.com")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StartDeviceFlow error = %v, want context.Canceled", err)
	}
}

func TestPollForTokenPendingThenSuccess(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" {
			http.NotFound(w, r)
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		if calls == 1 {
			_, _ = w.Write([]byte("error=authorization_pending"))
			return
		}
		_, _ = w.Write([]byte("access_token=gho_tok&token_type=bearer&scope=read%3Auser"))
	}))
	defer srv.Close()
	overrideOAuthHostBase(t, srv.URL)

	tr, err := PollForToken(context.Background(), "github.com", "DEV123", 0)
	if err != nil {
		t.Fatalf("PollForToken: %v", err)
	}
	if tr.AccessToken != "gho_tok" {
		t.Errorf("AccessToken = %q, want gho_tok", tr.AccessToken)
	}
	if tr.TokenType != "bearer" {
		t.Errorf("TokenType = %q, want bearer", tr.TokenType)
	}
	if tr.Scope != "read:user" {
		t.Errorf("Scope = %q, want read:user", tr.Scope)
	}
	if calls < 2 {
		t.Errorf("polls = %d, want at least 2 (pending then success)", calls)
	}
}

func TestPollForTokenDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		_, _ = w.Write([]byte("error=access_denied&error_description=user%20said%20no"))
	}))
	defer srv.Close()
	overrideOAuthHostBase(t, srv.URL)

	_, err := PollForToken(context.Background(), "github.com", "DEV123", 0)
	if err == nil {
		t.Fatal("PollForToken should error when the user denies access")
	}
}

func TestPollForTokenGHEHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		_, _ = w.Write([]byte("access_token=gho_ghe&token_type=bearer&scope=read%3Auser"))
	}))
	defer srv.Close()
	overrideOAuthHostBase(t, srv.URL)

	tr, err := PollForToken(context.Background(), "octocorp.ghe.com", "DEV123", 0)
	if err != nil {
		t.Fatalf("PollForToken (GHE): %v", err)
	}
	if tr.AccessToken != "gho_ghe" {
		t.Errorf("AccessToken = %q, want gho_ghe", tr.AccessToken)
	}
}

func TestFetchUserInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer gho_tok" {
			t.Errorf("Authorization = %q, want Bearer gho_tok", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"octocat"}`))
	}))
	defer srv.Close()
	overrideRESTAPIBase(t, srv.URL)

	ui, err := FetchUserInfo(context.Background(), "github.com", "gho_tok")
	if err != nil {
		t.Fatalf("FetchUserInfo: %v", err)
	}
	if ui.Login != "octocat" {
		t.Errorf("Login = %q, want octocat", ui.Login)
	}
}

func TestFetchUserInfoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"bad credentials"}`))
	}))
	defer srv.Close()
	overrideRESTAPIBase(t, srv.URL)

	if _, err := FetchUserInfo(context.Background(), "github.com", "stale"); err == nil {
		t.Fatal("FetchUserInfo should error on a non-2xx response")
	}
}

func TestFetchUserInfoMalformed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer srv.Close()
	overrideRESTAPIBase(t, srv.URL)

	if _, err := FetchUserInfo(context.Background(), "github.com", "gho_tok"); err == nil {
		t.Fatal("FetchUserInfo should error on a malformed body")
	}
}

func TestOpenBrowserNoLauncher(t *testing.T) {
	// An empty PATH guarantees no xdg-open/wslview launcher is found, so the
	// underlying cli/browser helper returns an error rather than launching.
	t.Setenv("PATH", t.TempDir())
	if err := OpenBrowser("https://github.com/login/device"); err == nil {
		t.Fatal("OpenBrowser should error when no launcher is on PATH")
	}
}
