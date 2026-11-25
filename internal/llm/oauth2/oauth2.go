package oauth2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenManager handles OAuth2 token acquisition and caching.
type TokenManager struct {
	mu           sync.Mutex
	token        *TokenResponse
	httpClient   *http.Client
	config       *Config
}

// Config holds OAuth2 client configuration.
type Config struct {
	TokenURL              string
	ClientID              string
	ClientSecret          string
	Scopes                []string
	DeviceFlowAuthURL     string
	DeviceFlowTokenURL    string
	DeviceFlowClientID    string
	Audience              string
}

// TokenResponse is the OAuth2 token endpoint response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`

	expiresAt time.Time
}

// Valid reports whether the token is still valid (with a 60s buffer).
func (t *TokenResponse) Valid() bool {
	if t == nil || t.AccessToken == "" {
		return false
	}
	return time.Now().Add(60 * time.Second).Before(t.expiresAt)
}

// NewTokenManager creates a new OAuth2 token manager.
func NewTokenManager(cfg *Config) *TokenManager {
	return &TokenManager{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		config:     cfg,
	}
}

// GetToken returns a valid access token, acquiring a new one if needed.
func (m *TokenManager) GetToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.token != nil && m.token.Valid() {
		return m.token.AccessToken, nil
	}

	if m.config.ClientSecret != "" {
		return m.fetchClientCredentialsToken(ctx)
	}

	if m.config.DeviceFlowAuthURL != "" && m.config.DeviceFlowTokenURL != "" {
		return m.fetchDeviceFlowToken(ctx)
	}

	return "", fmt.Errorf("oauth2: no valid token and no configured flow (need client_secret or device flow)")
}

// fetchClientCredentialsToken fetches a token using client credentials flow.
func (m *TokenManager) fetchClientCredentialsToken(ctx context.Context) (string, error) {
	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", m.config.ClientID)
	data.Set("client_secret", m.config.ClientSecret)
	if len(m.config.Scopes) > 0 {
		data.Set("scope", strings.Join(m.config.Scopes, " "))
	}
	if m.config.Audience != "" {
		data.Set("audience", m.config.Audience)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.config.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("oauth2: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth2: token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return "", fmt.Errorf("oauth2: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("oauth2: token request failed: %s: %s", resp.Status, string(body))
	}

	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("oauth2: parse token response: %w", err)
	}

	tr.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	m.token = &tr
	return tr.AccessToken, nil
}

// DeviceCodeResponse is the response from the device authorization endpoint.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval,omitempty"`
}

// fetchDeviceFlowToken initiates device flow and polls for token.
func (m *TokenManager) fetchDeviceFlowToken(ctx context.Context) (string, error) {
	clientID := m.config.ClientID
	if m.config.DeviceFlowClientID != "" {
		clientID = m.config.DeviceFlowClientID
	}

	// Step 1: Request device code
	data := url.Values{}
	data.Set("client_id", clientID)
	if len(m.config.Scopes) > 0 {
		data.Set("scope", strings.Join(m.config.Scopes, " "))
	}
	if m.config.Audience != "" {
		data.Set("audience", m.config.Audience)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.config.DeviceFlowAuthURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("oauth2: build device code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth2: device code request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return "", fmt.Errorf("oauth2: read device code response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("oauth2: device code request failed: %s: %s", resp.Status, string(body))
	}

	var dc DeviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return "", fmt.Errorf("oauth2: parse device code response: %w", err)
	}

	// Print user instructions (in a real implementation, this would go through the UI)
	fmt.Printf("To authenticate, visit: %s\nAnd enter code: %s\n", dc.VerificationURI, dc.UserCode)

	// Step 2: Poll for token
	interval := dc.Interval
	if interval == 0 {
		interval = 5
	}
	expiresAt := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)

	tokenData := url.Values{}
	tokenData.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	tokenData.Set("device_code", dc.DeviceCode)
	tokenData.Set("client_id", clientID)

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(interval) * time.Second):
		}

		if time.Now().After(expiresAt) {
			return "", fmt.Errorf("oauth2: device code expired")
		}

		tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.config.DeviceFlowTokenURL, strings.NewReader(tokenData.Encode()))
		if err != nil {
			return "", fmt.Errorf("oauth2: build token request: %w", err)
		}
		tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		tokenReq.Header.Set("Accept", "application/json")

		tokenResp, err := m.httpClient.Do(tokenReq)
		if err != nil {
			return "", fmt.Errorf("oauth2: token poll request: %w", err)
		}

		tokenBody, err := io.ReadAll(io.LimitReader(tokenResp.Body, 16<<10))
		tokenResp.Body.Close()

		if err != nil {
			return "", fmt.Errorf("oauth2: read token response: %w", err)
		}

		if tokenResp.StatusCode == 200 {
			var tr TokenResponse
			if err := json.Unmarshal(tokenBody, &tr); err != nil {
				return "", fmt.Errorf("oauth2: parse token response: %w", err)
			}
			tr.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
			m.token = &tr
			return tr.AccessToken, nil
		}

		// Handle specific error codes per RFC 8628
		var errResp struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if json.Unmarshal(tokenBody, &errResp) == nil {
			switch errResp.Error {
			case "authorization_pending":
				continue // Keep polling
			case "slow_down":
				interval += 5 // Increase interval
				continue
			case "expired_token":
				return "", fmt.Errorf("oauth2: device code expired")
			case "access_denied":
				return "", fmt.Errorf("oauth2: access denied by user")
			}
		}

		return "", fmt.Errorf("oauth2: token request failed: %s: %s", tokenResp.Status, string(tokenBody))
	}
}

// ClearToken clears the cached token (e.g., on 401 response).
func (m *TokenManager) ClearToken() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = nil
}

// TokenSource is an interface for getting OAuth2 tokens.
type TokenSource interface {
	GetToken(ctx context.Context) (string, error)
	ClearToken()
}