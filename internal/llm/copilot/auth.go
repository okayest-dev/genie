package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"time"
)

// VSCodeClientID is the Visual Studio Code Copilot client ID.
// Using this ID is necessary for the Copilot backend to grant model entitlements.
// See docs/research/copilot-device-flow-oauth.md §2 for details.
const VSCodeClientID = "Iv1.b507a08c87ecfe98"

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

// StartDeviceFlow initiates the GitHub device flow and returns the device code response.
func StartDeviceFlow(ctx context.Context, domain string) (*DeviceCodeResponse, error) {
	baseURL := deviceFlowBaseURL(domain)
	endpoint := baseURL + "/login/device/code"

	data := url.Values{}
	data.Set("client_id", VSCodeClientID)
	data.Set("scope", "read:user")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build device code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Body = io.NopCloser(&requestBody{values: data})

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device code request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("device code failed: %s: %s", resp.Status, string(body))
	}

	var dcr DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dcr); err != nil {
		return nil, fmt.Errorf("parse device code response: %w", err)
	}
	return &dcr, nil
}

// PollForToken polls the token endpoint until the user authorizes or the code expires.
func PollForToken(ctx context.Context, domain string, deviceCode string, interval int) (*TokenResponse, error) {
	baseURL := deviceFlowBaseURL(domain)
	endpoint := baseURL + "/login/oauth/access_token"

	data := url.Values{}
	data.Set("client_id", VSCodeClientID)
	data.Set("device_code", deviceCode)
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

	client := &http.Client{Timeout: 30 * time.Second}

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
			if err != nil {
				return nil, fmt.Errorf("build token request: %w", err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Accept", "application/json")
			req.Body = io.NopCloser(&requestBody{values: data})

			resp, err := client.Do(req)
			if err != nil {
				return nil, fmt.Errorf("token request: %w", err)
			}

			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
			resp.Body.Close()

			if resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 403 {
				var errResp struct {
					Error            string `json:"error"`
					ErrorDescription string `json:"error_description"`
				}
				if json.Unmarshal(body, &errResp) == nil {
					switch errResp.Error {
					case "authorization_pending":
						continue
					case "slow_down":
						interval += 5
						ticker.Reset(time.Duration(interval) * time.Second)
						continue
					case "expired_token", "token_expired":
						return nil, fmt.Errorf("device code expired")
					case "access_denied":
						return nil, fmt.Errorf("user denied authorization")
					}
				}
				return nil, fmt.Errorf("token request failed: %s: %s", resp.Status, string(body))
			}

			if resp.StatusCode < 200 || resp.StatusCode > 299 {
				return nil, fmt.Errorf("token request failed: %s: %s", resp.Status, string(body))
			}

			var tr TokenResponse
			if err := json.Unmarshal(body, &tr); err != nil {
				return nil, fmt.Errorf("parse token response: %w", err)
			}
			if tr.AccessToken == "" {
				return nil, fmt.Errorf("empty access token in response")
			}
			return &tr, nil
		}
	}
}

// FetchUserInfo fetches the GitHub user info using the OAuth token.
func FetchUserInfo(ctx context.Context, domain, token string) (*UserInfo, error) {
	baseURL := apiBaseURL(domain)
	endpoint := baseURL + "/user"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
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

// deviceFlowBaseURL returns the base URL for device flow endpoints.
func deviceFlowBaseURL(domain string) string {
	if domain == "" || domain == "github.com" {
		return "https://github.com"
	}
	return "https://" + domain
}

// apiBaseURL returns the base URL for API endpoints.
func apiBaseURL(domain string) string {
	if domain == "" || domain == "github.com" {
		return "https://api.github.com"
	}
	return "https://api." + domain
}

// requestBody implements io.Reader for url.Values.
type requestBody struct {
	values url.Values
	encoded string
}

func (r *requestBody) Read(p []byte) (n int, err error) {
	if r.encoded == "" {
		r.encoded = r.values.Encode()
	}
	if len(r.encoded) == 0 {
		return 0, io.EOF
	}
	n = copy(p, r.encoded)
	r.encoded = r.encoded[n:]
	return n, nil
}

// OpenBrowser attempts to open the verification URI in the user's default browser.
func OpenBrowser(url string) error {
	var cmd string
	var args []string

	// Try common browser open commands
	for _, c := range []string{"xdg-open", "open", "start"} {
		if _, err := exec.LookPath(c); err == nil {
			cmd = c
			break
		}
	}
	if cmd == "" {
		return fmt.Errorf("no browser launcher found (tried xdg-open, open, start)")
	}

	args = []string{url}
	return exec.Command(cmd, args...).Start()
}