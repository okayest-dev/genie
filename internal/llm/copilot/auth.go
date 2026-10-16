package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cli/browser"
	"github.com/cli/oauth"
	"github.com/cli/oauth/device"
)

// VSCodeClientID is the Visual Studio Code Copilot client ID.
// Using this ID is necessary for the Copilot backend to grant model entitlements.
// See docs/research/copilot-device-flow-oauth.md §2 for details.
const VSCodeClientID = "Iv1.b507a08c87ecfe98"

// deviceCodeDefaultExpiresIn is the fallback lifetime (seconds) of a device
// code when the caller cannot supply the server-provided expiry. GitHub's
// device codes expire after 900 seconds; this bounds the poll loop.
const deviceCodeDefaultExpiresIn = 900

// oauthHTTPTimeout bounds every device-flow HTTP call so a stalled endpoint
// cannot hang an interactive login.
const oauthHTTPTimeout = 30 * time.Second

// oauthHostBaseURL maps a Copilot host domain to the GitHub web base used for
// OAuth device-flow endpoints. It is a variable so tests can point the flow at
// a local server; production uses github.com for the default host and the
// tenant domain (e.g. octocorp.ghe.com) for GitHub Enterprise.
var oauthHostBaseURL = func(domain string) string {
	if domain == "" || domain == "github.com" {
		return "https://github.com"
	}
	return "https://" + domain
}

// restAPIBaseURL maps a Copilot host domain to its REST API root. It is a
// variable so tests can point requests at a local server.
var restAPIBaseURL = func(domain string) string {
	if domain == "" || domain == "github.com" {
		return "https://api.github.com"
	}
	return "https://api." + domain
}

// DeviceCodeResponse is the response from the device code endpoint.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// TokenResponse is the response from the token endpoint.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
}

// UserInfo is the GitHub user info response.
type UserInfo struct {
	Login string `json:"login"`
}

// StartDeviceFlow initiates the GitHub device flow and returns the device code
// response. The flow uses the official cli/oauth device strategy, which drives
// the same endpoints for github.com and GitHub Enterprise hosts.
func StartDeviceFlow(ctx context.Context, domain string) (*DeviceCodeResponse, error) {
	host, err := oauth.NewGitHubHost(oauthHostBaseURL(domain))
	if err != nil {
		return nil, fmt.Errorf("device flow host: %w", err)
	}

	// device.RequestCode takes no context, so run it alongside one and stop
	// waiting as soon as the caller's context is done. The request itself stays
	// bounded by oauthHTTPTimeout.
	type result struct {
		code *device.CodeResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		code, err := device.RequestCode(oauthClient(), host.DeviceCodeURL, VSCodeClientID, []string{"read:user"})
		ch <- result{code: code, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("device code request: %w", r.err)
		}
		return &DeviceCodeResponse{
			DeviceCode:      r.code.DeviceCode,
			UserCode:        r.code.UserCode,
			VerificationURI: r.code.VerificationURI,
			ExpiresIn:       r.code.ExpiresIn,
			Interval:        r.code.Interval,
		}, nil
	}
}

// PollForToken polls the token endpoint until the user authorizes or the code
// expires, honouring the server-supplied interval and the library's slow_down
// backoff. The interval is taken as-is from the device code response.
func PollForToken(ctx context.Context, domain string, deviceCode string, interval int) (*TokenResponse, error) {
	host, err := oauth.NewGitHubHost(oauthHostBaseURL(domain))
	if err != nil {
		return nil, fmt.Errorf("device flow host: %w", err)
	}

	tr, err := device.Wait(ctx, oauthClient(), host.TokenURL, device.WaitOptions{
		ClientID: VSCodeClientID,
		DeviceCode: &device.CodeResponse{
			DeviceCode: deviceCode,
			Interval:   interval,
			ExpiresIn:  deviceCodeDefaultExpiresIn,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	return &TokenResponse{
		AccessToken:  tr.Token,
		TokenType:    tr.Type,
		Scope:        tr.Scope,
		RefreshToken: tr.RefreshToken,
	}, nil
}

// FetchUserInfo fetches the GitHub user info using the OAuth token.
func FetchUserInfo(ctx context.Context, domain, token string) (*UserInfo, error) {
	endpoint := restAPIBaseURL(domain) + "/user"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := oauthClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("user request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("user request failed: %s: %s", resp.Status, string(body))
	}

	var ui UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&ui); err != nil {
		return nil, fmt.Errorf("parse user response: %w", err)
	}
	return &ui, nil
}

// oauthClient returns the HTTP client used for device-flow and user requests.
func oauthClient() *http.Client {
	return &http.Client{Timeout: oauthHTTPTimeout}
}

// OpenBrowser opens the verification URI in the user's default browser using
// the cli/browser helper, which knows the platform launchers (xdg-open, open,
// rundll32, ...) and does not block on the browser process.
func OpenBrowser(authURL string) error {
	if err := browser.OpenURL(authURL); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}
