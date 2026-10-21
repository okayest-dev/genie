package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/okayest-dev/genie/internal/llm"
)

// defaultAPIBase is where Copilot chat requests land when the token exchange
// response carries no endpoints.api (rare; the exchange normally names it).
const defaultAPIBase = "https://api.githubcopilot.com"

// tokenExchangeURL builds the Copilot token exchange endpoint for a GitHub
// host: github.com talks to the public API, a GHE tenant to its own api
// subdomain.
func tokenExchangeURL(domain string) string {
	if domain == "" || domain == "github.com" {
		return "https://api.github.com/copilot_internal/v2/token"
	}
	return "https://api." + domain + "/copilot_internal/v2/token"
}

// userAuthURL builds the user auth validation endpoint for a GitHub host.
// For GHE data-residency (*.ghe.com), this is the working auth check endpoint
// (GET /copilot_internal/user) instead of the token exchange.
func userAuthURL(domain string) string {
	if domain == "" || domain == "github.com" {
		return "https://api.github.com/copilot_internal/user"
	}
	return "https://api." + domain + "/copilot_internal/user"
}

// copilotAPIBase builds the inference API base for a GitHub host.
// For GHE data-residency (*.ghe.com), inference routes to copilot-api.<tenant>
// instead of api.<tenant> or the exchange response's endpoints.api.
func copilotAPIBase(domain string) string {
	if domain == "" || domain == "github.com" {
		return defaultAPIBase
	}
	// For *.ghe.com data-residency, inference goes to copilot-api.<tenant>
	if strings.HasSuffix(domain, ".ghe.com") {
		return "https://copilot-api." + domain
	}
	// Other GHE: fall back to api.<domain> (will be overridden by exchange response)
	return "https://api." + domain
}

// exchangeResult is the parsed response from the Copilot token exchange.
type exchangeResult struct {
	Token     string `json:"token"`
	Endpoints struct {
		API string `json:"api"`
	} `json:"endpoints"`
	ExpiresAt int64 `json:"expires_at"`
	RefreshIn int   `json:"refresh_in"`
}

// userAuthResult is the parsed response from the user auth validation (GET /copilot_internal/user).
type userAuthResult struct {
	User string `json:"user"`
	// The data-residency flow doesn't return a token or endpoints;
	// we construct the api base from the domain.
}

// exchange exchanges a GitHub OAuth token for a short-lived Copilot JWT and
// the API base that serves the account tier (ADR-0002: the derived token is
// memory-only; only the durable oauth record lives on disk).
func (c *Client) exchange(ctx context.Context, oauthToken string) (*exchangeResult, error) {
	url := tokenExchangeURL(c.domain)
	if c.exchangeURL != "" {
		url = c.exchangeURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("copilot: build token exchange request: %w", err)
	}
	req.Header.Set("Authorization", "token "+oauthToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &llm.ProviderError{Kind: llm.KindNetwork, Message: fmt.Sprintf("copilot token exchange: %v", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, exchangeError(resp, c.domain)
	}
	var tr exchangeResult
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, fmt.Errorf("copilot: parse token exchange: %w", err)
	}
	if tr.Token == "" {
		return nil, fmt.Errorf("copilot: token exchange returned no token")
	}
	return &tr, nil
}

// validateUserAuth validates the OAuth token via GET /copilot_internal/user.
// This is the working auth path for GHE data-residency (*.ghe.com) tenants
// where the token exchange endpoint returns 403.
func (c *Client) validateUserAuth(ctx context.Context, oauthToken string) error {
	url := userAuthURL(c.domain)
	if c.userAuthURL != "" {
		url = c.userAuthURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("copilot: build user auth request: %w", err)
	}
	req.Header.Set("Authorization", "token "+oauthToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &llm.ProviderError{Kind: llm.KindNetwork, Message: fmt.Sprintf("copilot user auth: %v", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return &llm.ProviderError{
			Kind:       llm.KindAuth,
			Message:    fmt.Sprintf("copilot user auth failed: %s: %s", resp.Status, string(body)),
			StatusCode: resp.StatusCode,
		}
	}
	return nil
}

// access returns a valid Copilot JWT and its API base, exchanging the OAuth
// token from the credential store when the cached derived token is absent or
// within refresh_in of expiry. The derived token lives in memory only
// (ADR-0002); the store is read fresh on every exchange so a concurrent login
// is picked up on the next request.
func (c *Client) access(ctx context.Context) (token, apiBase string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.copilotToken != "" && time.Now().Before(c.refreshAfter) {
		return c.copilotToken, c.apiBase, nil
	}
	oauth, err := ReadOAuthToken(c.dataHome, c.domain)
	if err != nil {
		return "", "", err
	}

	// For GHE data-residency (*.ghe.com), try the standard token exchange first.
	// If it fails with the specific 403 "Resource not accessible by integration",
	// fall back to the data-residency auth flow: validate via GET /copilot_internal/user
	// and use copilot-api.<tenant>.ghe.com as the inference base.
	tr, err := c.exchange(ctx, oauth)
	if err != nil {
		// Check if this is the data-residency specific error
		if pe, ok := err.(*llm.ProviderError); ok && isGHEDataResidency(c.domain) && pe.StatusCode == http.StatusForbidden {
			if strings.Contains(strings.ToLower(pe.Message), "resource not accessible by integration") {
				// Fall back to data-residency auth flow
				if err := c.validateUserAuth(ctx, oauth); err != nil {
					return "", "", err
				}
				// For data-residency, we don't get a Copilot JWT from the exchange.
				// Use the OAuth token directly as the Bearer token for inference.
				// The API base is copilot-api.<tenant>.ghe.com.
				c.copilotToken = oauth
				c.apiBase = c.copilotAPIBase
				if c.apiBase == "" {
					c.apiBase = copilotAPIBase(c.domain)
				}
				// Use a conservative refresh window since we don't have refresh_in
				c.refreshAfter = time.Now().Add(20 * time.Minute)
				return c.copilotToken, c.apiBase, nil
			}
		}
		return "", "", err
	}

	c.copilotToken = tr.Token
	c.apiBase = tr.Endpoints.API
	if c.apiBase == "" {
		c.apiBase = defaultAPIBase
	}
	ttl := time.Duration(tr.RefreshIn)*time.Second - 60*time.Second
	if ttl < time.Second {
		ttl = time.Second
	}
	c.refreshAfter = time.Now().Add(ttl)
	return c.copilotToken, c.apiBase, nil
}

// exchangeError maps a non-2xx token exchange response to a normalized
// ProviderError. Auth-class failures (401/403) fail fast as KindAuth so the
// harness presents the user a login prompt rather than a generic error.
// For GHE data-residency hosts (*.ghe.com), the error is surfaced to allow
// the caller to fall back to the data-residency auth flow.
func exchangeError(resp *http.Response, domain string) error {
	kind := llm.KindOther
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		kind = llm.KindAuth
	case http.StatusTooManyRequests:
		kind = llm.KindRateLimit
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	msg := fmt.Sprintf("copilot token exchange: %s", resp.Status)
	if text := jsonErrorText(body); text != "" {
		msg = fmt.Sprintf("copilot token exchange: %s", text)
	}
	return &llm.ProviderError{Kind: kind, Message: msg, StatusCode: resp.StatusCode}
}

// isGHEDataResidency reports whether the domain is a GitHub Enterprise data-residency host.
func isGHEDataResidency(domain string) bool {
	return strings.HasSuffix(domain, ".ghe.com")
}

// jsonErrorText extracts a message from the common {"message": ...} /
// {"error": {..., "message": ...}} error shapes, returning "" when absent.
func jsonErrorText(body []byte) string {
	var flat struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &flat) == nil && flat.Message != "" {
		return flat.Message
	}
	var nested struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &nested) == nil && nested.Error.Message != "" {
		return nested.Error.Message
	}
	return ""
}
