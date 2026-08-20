# GitHub Copilot API — Wire Protocol Research

Research compiled for Go wire plugin implementation. All facts sourced from
reverse-engineering efforts, community proxies, and primary documentation.

**WARNING:** The Copilot chat/completions API is **undocumented and unofficial**.
Endpoint URLs, response shapes, and auth mechanisms can change without notice.
This is distinct from the official GitHub REST API for seat management.

---

## 1. HTTP Endpoints

### Primary (Individual)

```
POST https://api.githubcopilot.com/v1/chat/completions
GET  https://api.githubcopilot.com/v1/models
```

### Business/Enterprise

The token exchange response from `/copilot_internal/v2/token` includes an
`endpoints.api` field that provides the correct base URL per subscription tier:

| Tier              | Base URL                              |
|-------------------|---------------------------------------|
| Individual        | `https://api.githubcopilot.com`       |
| Business          | `https://api.business.githubcopilot.com` |
| Enterprise (GHE)  | `https://copilot-api.<domain>`        |

**Source:** https://github.com/anomalyco/opencode/issues/20759

Do **not** hardcode `api.githubcopilot.com` — read the endpoint from the token
exchange response.

### Additional wire format endpoints

The Copilot backend also supports newer wire formats depending on the model:

| Wire format       | Path               | Used when                     |
|-------------------|--------------------|-------------------------------|
| Chat Completions  | `/v1/chat/completions` | Fallback / legacy models  |
| Responses         | `/v1/responses`    | GPT-Codex models (5.2+)       |
| Anthropic Messages| `/v1/messages`     | Claude models (via proxy)     |

For a Go wire plugin targeting the broadest compatibility, **Chat Completions** is
the safest starting point.

**Source:** https://copilot-cli.genisisiq.com/02-context-model-loop/model-api-routing/

---

## 2. Wire Protocol

**Chat Completions (OpenAI-compatible)** — standard OpenAI wire format:

```
POST /v1/chat/completions
Content-Type: application/json

{
  "model": "gpt-4o",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello"}
  ],
  "stream": true,
  "max_tokens": 4096,
  "temperature": 0.7,
  "tools": [...]
}
```

Response is standard OpenAI `ChatCompletion` / streaming SSE chunks.

**Source:** https://deepwiki.com/ericc-ch/copilot-api/4.1-chat-completions-api

---

## 3. Authentication — Two-Phase Flow

### Phase 1: GitHub OAuth Device Flow → GitHub Token

1. Request device code:
   ```
   POST https://github.com/login/device/code
   Content-Type: application/json
   Accept: application/json

   {
     "client_id": "Iv1.b507a08c87ecfe98",
     "scope": "read:user"
   }
   ```

2. Poll for access token after user approves:
   ```
   POST https://github.com/login/oauth/access_token
   Content-Type: application/json
   Accept: application/json

   {
     "client_id": "Iv1.b507a08c87ecfe98",
     "device_code": "<device_code>",
     "grant_type": "urn:ietf:params:oauth:grant-type:device_code"
   }
   ```

3. Response: `{ "access_token": "gho_...", "token_type": "bearer" }`

**Critical:** Use VS Code's client ID `Iv1.b507a08c87ecfe98`. Other client IDs
have different model allowlists on the server side. The `gho_` prefix tokens
work; newer `ghu_` tokens from the GitHub App flow also work but require
additional handling.

**Source:** https://github.com/dvcrn/copilot-oauth-proxy/blob/main/authentication_flow.md
**Source:** https://hexdocs.pm/sycophant/0.4.2/github-copilot.md

### Phase 2: Token Exchange → Copilot Bearer Token

```
GET https://api.github.com/copilot_internal/v2/token
Authorization: token gho_<github_oauth_token>
Accept: application/json
```

Response:
```json
{
  "token": "tid=...;exp=...;...",
  "endpoints": {
    "api": "https://api.githubcopilot.com"
  },
  "expires_at": 1672531200,
  "refresh_in": 1500
}
```

- The `token` is a short-lived HMAC-signed JWT (~25-30 minutes).
- The `endpoints.api` field tells you the correct base URL.
- The `refresh_in` field says when to refresh (in seconds).
- You must re-exchange before expiry. There is **no refresh token** mechanism.

**For Enterprise:** the token exchange URL becomes:
```
GET https://api.<enterprise-domain>/copilot_internal/v2/token
```

**Source:** https://github.com/dvcrn/copilot-oauth-proxy/blob/main/authentication_flow.md
**Source:** https://github.com/anomalyco/opencode/issues/20759

---

## 4. Required Headers

### Token exchange request
```
Authorization: token gho_xxxxx        # Note: "token" not "Bearer"
Accept: application/json
```

### Chat completions request (all required for Business/Enterprise)
```
Authorization: Bearer <copilot_token>         # The JWT from /copilot_internal/v2/token
Editor-Version: vscode/1.85.1                 # Fake editor identity
Editor-Plugin-Version: copilot/1.155.0       # Fake plugin version
Copilot-Integration-Id: vscode-chat          # Integration identifier
User-Agent: GithubCopilot/1.155.0            # Must match editor identity
Content-Type: application/json
```

### Optional headers
```
Copilot-Vision-Request: true                 # When sending image content
x-initiator: user | agent                    # Distinguishes user vs agent requests
```

**Critical for Business/Enterprise:** The API validates that `Editor-Version`,
`Editor-Plugin-Version`, `Copilot-Integration-Id`, and `User-Agent` are present
and consistent. Without these, requests are rejected with 400 "model not
supported".

**Source:** https://docs.litellm.ai/docs/providers/github_copilot
**Source:** https://github.com/anomalyco/opencode/issues/20759

---

## 5. Supported Models

Query `GET /v1/models` with a valid token to get the current list. As of
August 2026, the available models include:

### OpenAI
| Model ID              | Notes                    |
|-----------------------|--------------------------|
| gpt-5.6 Luna          | GA                       |
| gpt-5.6 Sol           | GA                       |
| gpt-5.6 Terra         | GA                       |
| gpt-5.5               | GA                       |
| gpt-5.4               | GA                       |
| gpt-5.4 mini          | GA                       |
| gpt-5.4 nano          | GA                       |
| gpt-5.3-codex         | GA — requires Responses API |
| gpt-5.2-codex         | GA — requires Responses API |
| gpt-5-mini            | GA                       |
| gpt-4o                | GA                       |

### Anthropic
| Model ID              | Notes                    |
|-----------------------|--------------------------|
| claude-opus-5         | GA                       |
| claude-opus-4.8       | GA                       |
| claude-opus-4.7       | GA                       |
| claude-sonnet-4.6     | GA (retiring for some tiers) |
| claude-sonnet-4.5     | GA                       |
| claude-haiku-4.5      | GA                       |
| claude-fable-5        | GA (new, 2026)           |

### Google
| Model ID              | Notes                    |
|-----------------------|--------------------------|
| gemini-3.7-flash      | GA                       |
| gemini-3.6-pro        | GA                       |
| gemini-3.5-pro        | GA                       |
| gemini-3.5-flash      | GA                       |

### xAI
| Model ID              | Notes                    |
|-----------------------|--------------------------|
| grok-code-fast-1      | Retired 2026-05-15       |

**Note:** Codex models (gpt-5.2-codex, gpt-5.3-codex) may require the
`/v1/responses` wire format rather than chat completions. For broadest
compatibility, use non-Codex models with the chat completions endpoint.

**Source:** https://docs.github.com/en/copilot/reference/ai-models/supported-models
**Source:** https://github.com/openclaw/openclaw/issues/72805

---

## 6. Streaming (SSE)

Yes. Set `"stream": true` in the request body. The response uses standard OpenAI
SSE format:

```
data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

For streaming with usage stats:
```json
{
  "stream": true,
  "stream_options": {"include_usage": true}
}
```

**Source:** https://deepwiki.com/ericc-ch/copilot-api/4.1-chat-completions-api
**Source:** https://copilot-cli.genisisiq.com/02-context-model-loop/model-api-routing/

---

## 7. Tool / Function Calling

Yes. Standard OpenAI function calling is supported:

```json
{
  "model": "gpt-4o",
  "messages": [...],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get weather for a location",
        "parameters": {
          "type": "object",
          "properties": {
            "location": {"type": "string"}
          },
          "required": ["location"]
        }
      }
    }
  ],
  "tool_choice": "auto"
}
```

Streaming tool calls work incrementally — `arguments` are accumulated across
chunks.

**Source:** https://deepwiki.com/ericc-ch/copilot-api/4.1-chat-completions-api

---

## 8. Rate Limiting & Error Codes

### HTTP Status Codes
| Code  | Meaning                                      |
|-------|----------------------------------------------|
| 200   | Success                                      |
| 400   | Bad request / model not supported            |
| 401   | Unauthorized — invalid or expired token      |
| 403   | Forbidden — missing identity headers or subscription issue |
| 404   | Token exchange failed (`gho_` with /v2/token)|
| 429   | Rate limited — too many requests             |
| 500   | Internal — copilot token not set             |

### Rate Limit Behavior
- Free tier: ~180 requests/hour, ~3 requests/minute burst
- Pro tier: higher daily token quotas (prompt tokens/day)
- Enterprise: even higher, but still capped per tier
- Different models have different capacity limits ("limited capacity" models
  are rate-limited more aggressively)

Rate limit responses include:
```
x-ratelimit-remaining: 0
x-ratelimit-reset: <epoch_seconds>
retry-after: <seconds>
```

**Source:** https://docs.github.com/en/copilot/how-tos/troubleshoot-copilot/troubleshoot-common-issues
**Source:** https://srisatyalokesh.is-a.dev/learn-ai/github-copilot-usage-limits/

---

## 9. Go Implementation Sketch

### Auth flow

```go
// Phase 1: Device flow (one-time, store token)
func StartDeviceFlow(clientID string) (userCode, verificationURI string, poll func() (string, error)) {
    // POST github.com/login/device/code
    // Display user_code to user
    // Return poll function that POSTs github.com/login/oauth/access_token
}

// Phase 2: Token exchange (every ~25 minutes)
func GetCopilotToken(githubToken string) (copilotToken string, apiBaseURL string, expiresIn int, err error) {
    req, _ := http.NewRequest("GET", "https://api.github.com/copilot_internal/v2/token", nil)
    req.Header.Set("Authorization", "token "+githubToken)
    // Parse response for .token and .endpoints.api
}
```

### Request headers

```go
func CopilotHeaders(copilotToken string) http.Header {
    h := http.Header{}
    h.Set("Authorization", "Bearer "+copilotToken)
    h.Set("Content-Type", "application/json")
    h.Set("Editor-Version", "vscode/1.103.0")
    h.Set("Editor-Plugin-Version", "copilot/1.341.0")
    h.Set("Copilot-Integration-Id", "vscode-chat")
    h.Set("User-Agent", "GithubCopilot/1.341.0")
    return h
}
```

### Chat completions call

```go
func ChatCompletion(apiBase, model string, messages []Message, stream bool) (*http.Response, error) {
    body := map[string]any{
        "model":    model,
        "messages": messages,
        "stream":   stream,
    }
    jsonBody, _ := json.Marshal(body)
    req, _ := http.NewRequest("POST", apiBase+"/v1/chat/completions", bytes.NewReader(jsonBody))
    req.Header = CopilotHeaders(copilotToken)
    return http.DefaultClient.Do(req)
}
```

### Token refresh loop

```go
func StartTokenRefreshLoop(githubToken string, apiBase *string) {
    for {
        token, base, expiresIn, err := GetCopilotToken(githubToken)
        if err != nil {
            log.Printf("token refresh failed: %v", err)
            time.Sleep(30 * time.Second)
            continue
        }
        *apiBase = base
        // use token for requests...
        time.Sleep(time.Duration(expiresIn-300) * time.Second) // refresh 5 min early
    }
}
```

---

## 10. Key References

| Resource | URL |
|----------|-----|
| Copilot REST API (seat mgmt) | https://docs.github.com/en/rest/copilot |
| Supported AI models | https://docs.github.com/en/copilot/reference/ai-models/supported-models |
| LiteLLM Copilot provider | https://docs.litellm.ai/docs/providers/github_copilot |
| copilot-api proxy (auth details) | https://github.com/ericc-ch/copilot-api |
| copilot-oauth-proxy (auth flow) | https://github.com/dvcrn/copilot-oauth-proxy |
| Model routing (CLI analysis) | https://copilot-cli.genisisiq.com/02-context-model-loop/model-api-routing/ |
| Token exchange details | https://hexdocs.pm/sycophant/0.4.2/github-copilot.md |
| Auth/header issues | https://github.com/anomalyco/opencode/issues/20759 |
