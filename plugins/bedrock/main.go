// Bedrock wire plugin for og.
// Reads AWS credentials from ~/.aws/credentials, implements SigV4 signing,
// streams via ConverseStream API.
//
// Configuration via config.toml next to the plugin binary:
//
//	region       = "us-east-1"      # AWS region (default: us-east-1)
//	profile      = "my-profile"     # AWS profile name (default: default)
//	max_tokens   = 4096             # Max output tokens (default: 4096)
//	endpoint_url = ""               # Custom endpoint URL for VPC (optional)
//
// Environment variable overrides (take precedence over config.toml):
//
//	OG_BEDROCK_REGION       - AWS region
//	OG_BEDROCK_PROFILE      - AWS profile name
//	OG_BEDROCK_MAX_TOKENS   - Max output tokens
//	OG_BEDROCK_ENDPOINT_URL - Custom endpoint URL
package main

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/okayest-dev/og/plugins/shared"
)

type bedrockConfig struct {
	Region      string `toml:"region"`
	Profile     string `toml:"profile"`
	MaxTokens   int    `toml:"max_tokens"`
	EndpointURL string `toml:"endpoint_url"`
}

type AWSCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
}

type BedrockClient struct {
	creds      AWSCredentials
	maxTokens  int
	endpoint   string
	httpClient *http.Client
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	cfg := loadConfig()
	creds, err := loadCredentials(cfg)
	if err != nil {
		slog.Error("failed to load AWS credentials", "error", err)
		os.Exit(1)
	}

	client := &BedrockClient{
		creds:      creds,
		maxTokens:  loadMaxTokens(cfg),
		endpoint:   loadEndpointURL(cfg),
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}

	h := shared.NewHandler(shared.Capabilities{
		Tools:     false,
		Wires:     true,
		Providers: false,
		Version:   1,
	})

	models := listModels()
	h.SetModels(models)

	h.OnInit(func() error {
		slog.Info("bedrock wire plugin initialized", "region", creds.Region, "max_tokens", client.maxTokens)
		if client.endpoint != "" {
			slog.Info("using custom endpoint", "url", client.endpoint)
		}
		return nil
	})

	h.OnStream(func(request json.RawMessage) (json.RawMessage, error) {
		return client.streamConverse(request)
	})

	if err := h.Run(); err != nil {
		slog.Error("handler failed", "error", err)
		os.Exit(1)
	}
}

func loadConfig() bedrockConfig {
	exe, err := os.Executable()
	if err != nil {
		return bedrockConfig{}
	}
	dir := filepath.Dir(exe)
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		return bedrockConfig{}
	}
	var cfg bedrockConfig
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		slog.Warn("failed to parse bedrock config, using defaults", "error", err)
		return bedrockConfig{}
	}
	if cfg.Region != "" {
		slog.Info("bedrock config loaded", "region", cfg.Region, "profile", cfg.Profile)
	}
	return cfg
}

func loadCredentials(cfg bedrockConfig) (AWSCredentials, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return AWSCredentials{}, fmt.Errorf("home dir: %w", err)
	}

	// Profile: env > config.toml > default
	profile := os.Getenv("OG_BEDROCK_PROFILE")
	if profile == "" {
		profile = os.Getenv("AWS_PROFILE")
	}
	if profile == "" {
		profile = cfg.Profile
	}
	if profile == "" {
		profile = "default"
	}

	// Region: env > config.toml > ~/.aws/config > default
	creds := AWSCredentials{
		Region: os.Getenv("OG_BEDROCK_REGION"),
	}
	if creds.Region == "" {
		creds.Region = os.Getenv("AWS_REGION")
	}
	if creds.Region == "" {
		creds.Region = cfg.Region
	}

	// Read ~/.aws/credentials
	credsPath := filepath.Join(home, ".aws", "credentials")
	if data, err := os.ReadFile(credsPath); err == nil {
		section := parseINISection(string(data), profile)
		creds.AccessKeyID = section["aws_access_key_id"]
		creds.SecretAccessKey = section["aws_secret_access_key"]
		creds.SessionToken = section["aws_session_token"]
	}

	// Read ~/.aws/config for region (if not set yet)
	configPath := filepath.Join(home, ".aws", "config")
	if data, err := os.ReadFile(configPath); err == nil {
		section := parseINISection(string(data), profile)
		if r := section["region"]; r != "" && creds.Region == "" {
			creds.Region = r
		}
	}

	// Env vars override (standard AWS vars)
	if v := os.Getenv("AWS_ACCESS_KEY_ID"); v != "" {
		creds.AccessKeyID = v
	}
	if v := os.Getenv("AWS_SECRET_ACCESS_KEY"); v != "" {
		creds.SecretAccessKey = v
	}
	if v := os.Getenv("AWS_SESSION_TOKEN"); v != "" {
		creds.SessionToken = v
	}
	if v := os.Getenv("AWS_DEFAULT_REGION"); v != "" {
		creds.Region = v
	}

	if creds.Region == "" {
		creds.Region = "us-east-1"
	}

	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return AWSCredentials{}, fmt.Errorf("no AWS credentials found — set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, or configure ~/.aws/credentials")
	}

	slog.Info("loaded AWS credentials", "region", creds.Region, "profile", profile)
	return creds, nil
}

// loadMaxTokens resolves the max_tokens from env > config > default.
func loadMaxTokens(cfg bedrockConfig) int {
	if v := os.Getenv("OG_BEDROCK_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
		slog.Warn("invalid OG_BEDROCK_MAX_TOKENS, using default", "value", v)
	}
	if cfg.MaxTokens > 0 {
		return cfg.MaxTokens
	}
	return 4096
}

// loadEndpointURL resolves the endpoint URL from env > config > empty.
func loadEndpointURL(cfg bedrockConfig) string {
	if v := os.Getenv("OG_BEDROCK_ENDPOINT_URL"); v != "" {
		return v
	}
	return cfg.EndpointURL
}

func parseINISection(data, section string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(data, "\n")
	inSection := false
	re := regexp.MustCompile(`^\[(.+)\]`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if m := re.FindStringSubmatch(line); m != nil {
			inSection = (m[1] == section || m[1] == "profile "+section)
			continue
		}

		if inSection {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				result[key] = val
			}
		}
	}

	return result
}

func listModels() []shared.ModelDef {
	return []shared.ModelDef{
		{ID: "anthropic.claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
		{ID: "anthropic.claude-opus-4-7", Name: "Claude Opus 4.7"},
		{ID: "anthropic.claude-opus-4-8", Name: "Claude Opus 4.8"},
		{ID: "anthropic.claude-haiku-4-5-20251001-v1:0", Name: "Claude Haiku 4.5"},
		{ID: "meta.llama4-scout-17b-1b-instruct-v1:0", Name: "Llama 4 Scout"},
		{ID: "meta.llama4-maverick-17b-128b-instruct-v1:0", Name: "Llama 4 Maverick"},
		{ID: "meta.llama3-70b-instruct-v1:0", Name: "Llama 3 70B"},
		{ID: "amazon.nova-pro-v1:0", Name: "Nova Pro"},
		{ID: "amazon.nova-lite-v1:0", Name: "Nova Lite"},
		{ID: "mistral.mistral-large-2407-v1:0", Name: "Mistral Large"},
		{ID: "cohere.command-r-v1:0", Name: "Command R"},
	}
}

func (c *BedrockClient) streamConverse(request json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Model   string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, fmt.Errorf("parse wire request: %w", err)
	}

	bedrockMessages := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		bedrockMessages = append(bedrockMessages, map[string]any{
			"role":    m.Role,
			"content": []map[string]any{{"text": m.Content}},
		})
	}

	payload, err := json.Marshal(map[string]any{
		"messages": bedrockMessages,
		"inferenceConfig": map[string]any{
			"maxTokens": c.maxTokens,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	endpoint := ""
	if c.endpoint != "" {
		endpoint = c.endpoint
	} else {
		endpoint = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", c.creds.Region)
	}
	url := fmt.Sprintf("%s/model/%s/converse-stream", endpoint, req.Model)

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	now := time.Now().UTC()
	signRequest(httpReq, payload, c.creds, now, "bedrock")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	return parseConverseStream(resp.Body)
}

func parseConverseStream(body io.Reader) (json.RawMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)

	var text strings.Builder
	var toolUses []map[string]any
	var usage map[string]any

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		if raw, ok := event["contentBlockDelta"]; ok {
			var delta struct {
				Delta struct {
					Text     string `json:"text"`
					ToolUse  *struct {
						ToolUseID string `json:"toolUseId"`
						Name      string `json:"name"`
						Input     string `json:"input"`
					} `json:"toolUse"`
				} `json:"delta"`
			}
			if err := json.Unmarshal(raw, &delta); err == nil {
				if delta.Delta.Text != "" {
					text.WriteString(delta.Delta.Text)
				}
			}
		}

		if raw, ok := event["contentBlockStart"]; ok {
			var start struct {
				Start struct {
					ToolUse *struct {
						ToolUseID string `json:"toolUseId"`
						Name      string `json:"name"`
					} `json:"toolUse"`
				} `json:"start"`
			}
			if err := json.Unmarshal(raw, &start); err == nil && start.Start.ToolUse != nil {
				toolUses = append(toolUses, map[string]any{
					"id":   start.Start.ToolUse.ToolUseID,
					"type": "function",
					"function": map[string]any{
						"name":      start.Start.ToolUse.Name,
						"arguments": "",
					},
				})
			}
		}

		if raw, ok := event["metadata"]; ok {
			var meta struct {
				Usage map[string]any `json:"usage"`
			}
			if err := json.Unmarshal(raw, &meta); err == nil {
				usage = meta.Usage
			}
		}
	}

	result := map[string]any{
		"text": text.String(),
	}
	if len(toolUses) > 0 {
		result["tool_calls"] = toolUses
	}
	if usage != nil {
		result["usage"] = usage
	}

	return json.Marshal(result)
}

func signRequest(req *http.Request, payload []byte, creds AWSCredentials, now time.Time, service string) {
	region := creds.Region
	date := now.Format("20060102")
	datetime := now.Format("20060102T150405Z")

	unsignedHeaders := map[string]string{
		"host":         req.Host,
		"content-type": "application/json",
		"x-amz-date":  datetime,
	}
	if creds.SessionToken != "" {
		unsignedHeaders["x-amz-security-token"] = creds.SessionToken
	}

	signedHeaderKeys := make([]string, 0, len(unsignedHeaders))
	for k := range unsignedHeaders {
		signedHeaderKeys = append(signedHeaderKeys, k)
	}
	sort.Strings(signedHeaderKeys)

	canonicalHeaders := ""
	signedHeaders := ""
	for _, k := range signedHeaderKeys {
		canonicalHeaders += k + ":" + strings.TrimSpace(unsignedHeaders[k]) + "\n"
		signedHeaders += k + ";"
	}
	signedHeaders = strings.TrimRight(signedHeaders, ";")

	payloadHash := sha256Hex(payload)

	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.Path,
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := strings.Join([]string{date, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		datetime,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := hmacSHA256(
		hmacSHA256(
			hmacSHA256(
				hmacSHA256(
					hmacSHA256(
						[]byte("AWS4"+creds.SecretAccessKey),
						[]byte(date),
					),
					[]byte(region),
				),
				[]byte(service),
			),
			[]byte("aws4_request"),
		),
		[]byte(stringToSign),
	)

	signature := hex.EncodeToString(signingKey)

	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		creds.AccessKeyID, credentialScope, signedHeaders, signature)

	req.Header.Set("Authorization", authHeader)
	req.Header.Set("x-amz-date", datetime)
	if creds.SessionToken != "" {
		req.Header.Set("x-amz-security-token", creds.SessionToken)
	}
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
