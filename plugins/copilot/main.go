// Copilot wire plugin for og.
// Reads GitHub OAuth token from ~/.config/github-copilot/hosts.json,
// exchanges for short-lived Copilot JWT, streams via OpenAI-compatible API.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/okayest-dev/og/plugins/shared"
)

type HostsFile struct {
	GitHubCom *Account `json:"github.com"`
}

type Account struct {
	User       string `json:"user"`
	OAuthToken string `json:"oauth_token"`
}

type TokenExchange struct {
	Token     string            `json:"token"`
	Endpoints map[string]string `json:"endpoints"`
	ExpiresAt int64             `json:"expires_at"`
	RefreshIn int               `json:"refresh_in"`
}

type CopilotClient struct {
	githubToken  string
	copilotToken string
	apiBase      string
	expiresAt    time.Time
	httpClient   *http.Client
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	token, err := readCopilotToken()
	if err != nil {
		slog.Error("failed to read Copilot token", "error", err)
		os.Exit(1)
	}

	client := &CopilotClient{
		githubToken: token,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}

	if err := client.refreshToken(); err != nil {
		slog.Error("failed to exchange Copilot token", "error", err)
		os.Exit(1)
	}

	h := shared.NewHandler(shared.Capabilities{
		Tools:     false,
		Wires:     true,
		Providers: false,
		Version:   1,
	})

	h.SetModels([]shared.ModelDef{
		{ID: "gpt-4o", Name: "GPT-4o (via Copilot)"},
		{ID: "gpt-5.4", Name: "GPT-5.4 (via Copilot)"},
		{ID: "gpt-5.5", Name: "GPT-5.5 (via Copilot)"},
		{ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5 (via Copilot)"},
		{ID: "claude-opus-4-7", Name: "Claude Opus 4.7 (via Copilot)"},
		{ID: "gemini-3.5-pro", Name: "Gemini 3.5 Pro (via Copilot)"},
	})

	h.OnInit(func() error {
		slog.Info("copilot wire plugin initialized")
		return nil
	})

	h.OnStream(func(request json.RawMessage) (json.RawMessage, error) {
		if time.Now().After(client.expiresAt.Add(-5 * time.Minute)) {
			if err := client.refreshToken(); err != nil {
				return nil, fmt.Errorf("token refresh: %w", err)
			}
		}
		return client.streamCompletion(request)
	})

	if err := h.Run(); err != nil {
		slog.Error("handler failed", "error", err)
		os.Exit(1)
	}
}

func readCopilotToken() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}

	hostsPath := filepath.Join(home, ".config", "github-copilot", "hosts.json")
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		return "", fmt.Errorf("read hosts.json: %w", err)
	}

	var hosts HostsFile
	if err := json.Unmarshal(data, &hosts); err != nil {
		return "", fmt.Errorf("parse hosts.json: %w", err)
	}

	if hosts.GitHubCom == nil || hosts.GitHubCom.OAuthToken == "" {
		return "", fmt.Errorf("no github.com account in hosts.json")
	}

	slog.Info("read Copilot token", "user", hosts.GitHubCom.User)
	return hosts.GitHubCom.OAuthToken, nil
}

func (c *CopilotClient) refreshToken() error {
	req, err := http.NewRequest("GET", "https://api.github.com/copilot_internal/v2/token", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.githubToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("token exchange returned %d: %s", resp.StatusCode, string(body))
	}

	var exchange TokenExchange
	if err := json.NewDecoder(resp.Body).Decode(&exchange); err != nil {
		return fmt.Errorf("decode token exchange: %w", err)
	}

	c.copilotToken = exchange.Token
	c.apiBase = exchange.Endpoints["api"]
	if c.apiBase == "" {
		c.apiBase = "https://api.githubcopilot.com"
	}
	c.expiresAt = time.Unix(exchange.ExpiresAt, 0)

	slog.Info("Copilot token refreshed", "expires_at", c.expiresAt, "api_base", c.apiBase)
	return nil
}

func (c *CopilotClient) streamCompletion(request json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, fmt.Errorf("parse wire request: %w", err)
	}

	payload, err := json.Marshal(map[string]any{
		"model":  req.Model,
		"messages": req.Messages,
		"stream": true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	httpReq, err := http.NewRequest("POST", c.apiBase+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.copilotToken)
	httpReq.Header.Set("Editor-Version", "og/0.1.0")
	httpReq.Header.Set("Editor-Plugin-Version", "og-copilot/0.1.0")
	httpReq.Header.Set("Copilot-Integration-Id", "vscode-chat")
	httpReq.Header.Set("User-Agent", "GithubCopilot/og-0.1.0")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	return parseSSEStream(resp.Body)
}

func parseSSEStream(body io.Reader) (json.RawMessage, error) {
	scanner := newScanner(body)
	var text strings.Builder
	var toolCalls []map[string]any
	var usage map[string]any

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   *string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *map[string]any `json:"usage"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != nil {
				text.WriteString(*choice.Delta.Content)
			}
			for _, tc := range choice.Delta.ToolCalls {
				for len(toolCalls) <= tc.Index {
					toolCalls = append(toolCalls, map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Function.Name,
							"arguments": "",
						},
					})
				}
				if tc.Function.Arguments != "" {
					existing := toolCalls[tc.Index]["function"].(map[string]any)
					existing["arguments"] = existing["arguments"].(string) + tc.Function.Arguments
				}
				if tc.ID != "" {
					toolCalls[tc.Index]["id"] = tc.ID
				}
				if tc.Function.Name != "" {
					toolCalls[tc.Index]["function"].(map[string]any)["name"] = tc.Function.Name
				}
			}
		}

		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
	}

	result := map[string]any{
		"text": text.String(),
	}
	if len(toolCalls) > 0 {
		result["tool_calls"] = toolCalls
	}
	if usage != nil {
		result["usage"] = usage
	}

	return json.Marshal(result)
}

type lineScanner struct {
	reader  io.Reader
	buf     []byte
	overflow []byte
}

func newScanner(r io.Reader) *lineScanner {
	return &lineScanner{reader: r, buf: make([]byte, 0, 4096)}
}

func (s *lineScanner) Scan() bool {
	s.buf = s.buf[:0]
	if len(s.overflow) > 0 {
		s.buf = append(s.buf, s.overflow...)
		s.overflow = nil
	}

	for {
		if idx := bytes.IndexByte(s.buf, '\n'); idx >= 0 {
			line := s.buf[:idx]
			s.overflow = s.buf[idx+1:]
			s.buf = s.buf[:0]
			return len(line) > 0
		}

		n, err := s.reader.Read(s.buf[len(s.buf):cap(s.buf)])
		s.buf = s.buf[:len(s.buf)+n]
		if err != nil {
			return len(s.buf) > 0
		}
	}
}

func (s *lineScanner) Text() string {
	return string(s.buf)
}
