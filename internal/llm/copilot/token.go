package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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

// exchangeResult is the parsed response from the Copilot token exchange.
type exchangeResult struct {
	Token     string `json:"token"`
	Endpoints struct {
		API string `json:"api"`
	} `json:"endpoints"`
	ExpiresAt int64 `json:"expires_at"`
	RefreshIn int   `json:"refresh_in"`
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
		return nil, exchangeError(resp)
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

// exchangeError maps a non-2xx token exchange response to a normalized
// ProviderError. Auth-class failures (401/403) fail fast as KindAuth so the
// harness presents the user a login prompt rather than a generic error.
func exchangeError(resp *http.Response) error {
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
