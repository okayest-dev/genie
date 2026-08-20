# AWS Bedrock API Research — Wire Plugin Implementation Guide

> Sources: AWS API Reference docs (2026-08-20), AWS SDK docs, community examples.

---

## 1. HTTP Endpoints

The runtime API lives on a region-specific host:

```
https://bedrock-runtime.{region}.amazonaws.com
```

Examples:
- `https://bedrock-runtime.us-east-1.amazonaws.com`
- `https://bedrock-runtime.eu-west-1.amazonaws.com`
- `https://bedrock-runtime.us-west-2.amazonaws.com`

There is also a newer **Mantle** endpoint for Claude-only Messages API:
```
https://bedrock-mantle.{region}.api.aws
```

The control plane (model listing) uses a different service:
```
https://bedrock.{region}.amazonaws.com
```

For VPC endpoints, you can override with `AWS_ENDPOINT_URL_BEDROCK_RUNTIME`.

**Source:** [Converse API Reference](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html), [InvokeModel API Reference](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModel.html)

---

## 2. Wire Protocols (Four API Styles)

### 2a. Converse (recommended, model-agnostic)

```
POST /model/{modelId}/converse
Content-Type: application/json
```

Request body (all fields optional except you need at least `messages`):
```json
{
  "modelId": "anthropic.claude-sonnet-4-6",
  "messages": [
    {
      "role": "user",
      "content": [{ "text": "Hello world" }]
    }
  ],
  "system": [{ "text": "You are helpful." }],
  "inferenceConfig": {
    "maxTokens": 1000,
    "temperature": 0.5,
    "topP": 0.9,
    "stopSequences": ["STOP"]
  },
  "toolConfig": {
    "tools": [{ "toolSpec": { "name": "get_weather", ... } }],
    "toolChoice": { "auto": {} }
  }
}
```

Response (HTTP 200, `Content-Type: application/json`):
```json
{
  "output": {
    "message": {
      "role": "assistant",
      "content": [{ "text": "Hello! How can I help?" }]
    }
  },
  "stopReason": "end_turn",
  "usage": { "inputTokens": 30, "outputTokens": 12, "totalTokens": 42 },
  "metrics": { "latencyMs": 850 }
}
```

**IAM permission needed:** `bedrock:InvokeModel`

### 2b. ConverseStream (streaming, model-agnostic)

```
POST /model/{modelId}/converse-stream
Content-Type: application/json
```

Same request body as `Converse`. Response is a **stream of JSON events** separated by newlines (`\n`):

```
{"messageStart": {"role": "assistant"}}
{"contentBlockStart": {"contentBlockIndex": 0, "start": {}}}
{"contentBlockDelta": {"contentBlockIndex": 0, "delta": {"text": "Hello"}}}
{"contentBlockDelta": {"contentBlockIndex": 0, "delta": {"text": "!"}}}
{"contentBlockStop": {"contentBlockIndex": 0}}
{"messageStop": {"stopReason": "end_turn"}}
{"metadata": {"usage": {"inputTokens": 30, "outputTokens": 5, "totalTokens": 35}, "metrics": {"latencyMs": 420}}}
```

**IAM permission needed:** `bedrock:InvokeModelWithResponseStream`

### 2c. InvokeModel (legacy, model-specific)

```
POST /model/{modelId}/invoke
Content-Type: application/json
Accept: application/json
```

Request body is **model-specific** (e.g., Claude Messages format):
```json
{
  "anthropic_version": "bedrock-2023-05-31",
  "max_tokens": 1024,
  "messages": [
    { "role": "user", "content": [{ "type": "text", "text": "Hello" }] }
  ]
}
```

Response is model-specific JSON (e.g., Claude `{"completion": "...", "stop_reason": "end_turn"}`).

### 2d. InvokeModelWithResponseStream

```
POST /model/{modelId}/invoke-with-response-stream
Content-Type: application/json
```

Same model-specific body; streaming response.

**Recommendation for a wire plugin:** Target **Converse/ConverseStream** for maximum model compatibility with a single request shape. Use InvokeModel only when you need model-specific features.

---

## 3. Authentication

### Primary: AWS Signature Version 4 (SigV4)

All Bedrock requests must be signed with **AWS SigV4**. The signing details:

- **Service name:** `bedrock` (for Converse/InvokeModel)
- **Region:** the same region as the endpoint (e.g., `us-east-1`)
- **Algorithm:** `AWS4-HMAC-SHA256`

Required headers for every signed request:
```
Authorization: AWS4-HMAC-SHA256 Credential={access_key}/{date}/{region}/bedrock/aws4_request, SignedHeaders={headers}, Signature={sig}
Host: bedrock-runtime.us-east-1.amazonaws.com
x-amz-date: 20260820T120000Z
x-amz-security-token: {session_token}  # only if using temporary credentials
```

**SigV4 signing process (for Go implementation):**
1. Create canonical request: `HTTPMethod\nCanonicalURI\nCanonicalQueryString\nCanonicalHeaders\nSignedHeaders\nHashedPayload`
2. Create string to sign: `AWS4-HMAC-SHA256\n{timestamp}\n{credential-scope}\n{hash-of-canonical-request}`
3. Calculate signature: `HMAC(HMAC(HMAC(HMAC("AWS4"+secret, date), region), service), "aws4_request", string_to_sign)`

**Source:** [AWS SigV4 signing examples](https://github.com/aws-samples/sigv4-signing-examples), [SigV4 recipe](https://github.com/firzen/signature-recipes/tree/main/platforms/aws-bedrock-sigv4)

### Alternative: API Keys (Bearer token)

AWS Bedrock now supports API key authentication via bearer tokens:
```
Authorization: Bearer {token}
```

Short-term tokens (up to 12h) generated via `@aws/bedrock-token-generator`. Useful for environments without IAM.

**Source:** [AWS Bedrock API keys docs](https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys.html)

---

## 4. Supported Models (2026)

| Provider | Model IDs (on-demand) | Tool Use | Streaming |
|----------|----------------------|----------|-----------|
| **Anthropic Claude** | `anthropic.claude-sonnet-4-6`, `anthropic.claude-opus-4-7`, `anthropic.claude-opus-4-8`, `anthropic.claude-haiku-4-5-20251001-v1:0`, `anthropic.claude-3-5-sonnet-20240620-v1:0` | ✅ | ✅ |
| **Meta Llama** | `meta.llama4-scout-17b-1b-instruct-v1:0`, `meta.llama4-maverick-17b-128b-instruct-v1:0`, `meta.llama3-70b-instruct-v1:0`, `meta.llama3-8b-instruct-v1:0` | ✅ | ✅ |
| **Amazon Nova** | `amazon.nova-pro-v1:0`, `amazon.nova-lite-v1:0`, `amazon.nova-micro-v1:0` | ✅ | ✅ |
| **Amazon Titan** | `amazon.titan-text-express-v1`, `amazon.titan-text-lite-v1` | ❌ | ✅ |
| **Mistral** | `mistral.mistral-large-2407-v1:0`, `mistral.mistral-7b-instruct-v0:2`, `mistral.devstral-2-123b` | ✅ | ✅ |
| **Cohere** | `cohere.command-r-v1:0`, `cohere.command-r-plus-v1:0` | ✅ | ✅ |
| **AI21** | `ai21.jamba-1-5-large-v1:0`, `ai21.jamba-1-5-mini-v1:0` | ✅ | ✅ |
| **Stability AI** | `stability.stable-diffusion-xl-v1` | ❌ | ❌ |
| **OpenAI** (new) | `openai.gpt-5.5`, etc. | ✅ | ✅ |
| **DeepSeek** | `deepseek.v3.5` | ✅ | ✅ |

**Cross-region inference profiles** use prefixed IDs like `us.anthropic.claude-sonnet-4-6` or `global.anthropic.claude-opus-4-8`.

**Model ID format:** `{provider}.{model-name}[:{version}]` or ARN.

**Source:** [Bedrock model IDs](https://docs.aws.amazon.com/bedrock/latest/userguide/model-ids.html), [Supported models and features](https://docs.aws.amazon.com/bedrock/latest/userguide/conversation-inference.html#conversation-inference-supported-models-features)

---

## 5. Streaming

**Yes**, Bedrock supports streaming via two mechanisms:

### ConverseStream (recommended)
- Endpoint: `POST /model/{modelId}/converse-stream`
- Returns newline-delimited JSON events
- Event types: `messageStart`, `contentBlockStart`, `contentBlockDelta`, `contentBlockStop`, `messageStop`, `metadata`
- Delta types: `{"text": "..."}` or `{"toolUse": {"toolUseId": "...", "name": "...", "input": "..."}}`

### InvokeModelWithResponseStream (legacy)
- Endpoint: `POST /model/{modelId}/invoke-with-response-stream`
- Model-specific streaming format

**Streaming event flow for tool use:**
```
messageStart        → {"role": "assistant"}
contentBlockStart   → {"contentBlockIndex": 0, "start": {"toolUse": {"toolUseId": "toolu_xxx", "name": "get_weather"}}}
contentBlockDelta   → {"contentBlockIndex": 0, "delta": {"toolUse": {"input": "{\"city\":"}}}
contentBlockDelta   → {"contentBlockIndex": 0, "delta": {"toolUse": {"input": "\"Boston\"}"}}}
contentBlockStop    → {"contentBlockIndex": 0}
messageStop         → {"stopReason": "tool_use"}
metadata            → {"usage": {...}, "metrics": {...}}
```

**Source:** [ConverseStream API Reference](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ConverseStream.html)

---

## 6. Tool/Function Calling

**Yes**, supported via `toolConfig` in Converse/ConverseStream requests.

### Defining tools
```json
{
  "toolConfig": {
    "tools": [
      {
        "toolSpec": {
          "name": "get_weather",
          "description": "Get current weather for a city",
          "inputSchema": {
            "json": {
              "type": "object",
              "properties": {
                "city": { "type": "string", "description": "City name" }
              },
              "required": ["city"]
            }
          }
        }
      }
    ],
    "toolChoice": {
      "auto": {}
    }
  }
}
```

`toolChoice` options:
- `{"auto": {}}` — model decides whether to call a tool
- `{"any": {}}` — model must call at least one tool
- `{"tool": {"name": "get_weather"}}` — model must call a specific tool
- `{"none": {}}` — model must not call any tool

### Tool use response
When the model calls a tool, `stopReason` is `"tool_use"` and the response contains:
```json
{
  "output": {
    "message": {
      "role": "assistant",
      "content": [
        {
          "toolUse": {
            "toolUseId": "toolu_bdrk_0123456789abcdef",
            "name": "get_weather",
            "input": { "city": "Boston" }
          }
        }
      ]
    }
  },
  "stopReason": "tool_use"
}
```

### Sending tool results back
```json
{
  "messages": [
    { "role": "user", "content": [{ "text": "What's the weather?" }] },
    { "role": "assistant", "content": [{ "toolUse": { "toolUseId": "toolu_xxx", "name": "get_weather", "input": {"city": "Boston"} } }] },
    { "role": "user", "content": [{ "toolResult": { "toolUseId": "toolu_xxx", "content": [{ "text": "72°F, sunny" }] } }] }
  ]
}
```

**Models supporting tool use:** Claude 3+, Llama 3+, Mistral Large, Cohere Command R+, Nova, AI21 Jamba, and others.

**Source:** [Tool use docs](https://docs.aws.amazon.com/bedrock/latest/userguide/tool-use.html), [Converse API Reference](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html)

---

## 7. Required Headers

### For Converse/ConverseStream (raw HTTP):
```
POST /model/{modelId}/converse HTTP/1.1
Host: bedrock-runtime.{region}.amazonaws.com
Content-Type: application/json
Authorization: AWS4-HMAC-SHA256 ...
x-amz-date: 20260820T120000Z
x-amz-security-token: {optional, for temp credentials}
```

### For InvokeModel (raw HTTP):
```
POST /model/{modelId}/invoke HTTP/1.1
Host: bedrock-runtime.{region}.amazonaws.com
Content-Type: application/json
Accept: application/json
Authorization: AWS4-HMAC-SHA256 ...
x-amz-date: 20260820T120000Z
```

### Optional InvokeModel headers:
```
X-Amzn-Bedrock-Trace: {trace}
X-Amzn-Bedrock-GuardrailIdentifier: {guardrailId}
X-Amzn-Bedrock-GuardrailVersion: {guardrailVersion}
X-Amzn-Bedrock-PerformanceConfig-Latency: "optimized"
X-Amzn-Bedrock-Service-Tier: "default" | "provisioned"
X-Amzn-Bedrock-Request-Metadata: {json}
```

---

## 8. Rate Limiting & Error Codes

### Rate Limits (enforced per-model, per-region, per-account)

Two independent limits:
- **RPM** (Requests Per Minute) — number of API calls
- **TPM** (Tokens Per Minute) — total input + output tokens

When exceeded, Bedrock returns `429 ThrottlingException`.

### Error Codes

| HTTP Status | Error Code | Meaning |
|-------------|------------|---------|
| 400 | `ValidationException` | Bad request body / missing params |
| 400 | `IncompleteSignature` | Malformed SigV4 signature |
| 400 | `RequestExpired` | Timestamp too old (>15 min skew) |
| 403 | `AccessDeniedException` | IAM policy denies the action |
| 404 | `ResourceNotFoundException` | Model ID or ARN not found |
| 408 | `ModelTimeoutException` | Model took too long to respond |
| 424 | `ModelErrorException` | Error in model processing (contains `originalStatusCode`) |
| 424 | `ModelStreamErrorException` | Error during streaming |
| 429 | `ThrottlingException` | Rate limit / quota exceeded |
| 429 | `ModelNotReadyException` | Model not ready (cold start); SDK retries 5x |
| 500 | `InternalServerException` | Bedrock internal error |
| 503 | `ServiceUnavailableException` | Service temporarily unavailable |

### Retry Strategy (AWS recommended)
- Exponential backoff with jitter
- Base delay: 1 second, doubling each retry
- Max retries: typically 3-5
- Retry on: 429, 500, 503, 424 (model errors)

### ThrottlingException messages:
- `"Too many requests, please wait before trying again."`
- `"Your request rate is too high. Reduce the frequency of requests."`
- `"Too many tokens, please wait before trying again."`

**Source:** [Troubleshooting API Error Codes](https://docs.aws.amazon.com/bedrock/latest/userguide/troubleshooting-api-error-codes.html), [Converse API Reference errors section](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html)

---

## 9. Go Implementation Notes

### Key decisions for a wire plugin:

1. **Target Converse/ConverseStream** as the primary wire — one request shape covers all models.
2. **SigV4 signing** is mandatory. Use `github.com/aws/aws-sdk-go-v2` for credential resolution, or implement SigV4 from scratch using `crypto/hmac` + `crypto/sha256`.
3. **Streaming** uses HTTP chunked transfer with newline-delimited JSON. Parse each line as a JSON event.
4. **Tool use** is a first-class feature of the Converse API — no model-specific formatting needed.
5. **Model IDs** vary by provider and version. Users supply the model ID string.
6. **Content-Type** is always `application/json`.
7. **Stop reasons:** `end_turn`, `tool_use`, `max_tokens`, `stop_sequence`, `guardrail_intervened`, `content_filtered`, `malformed_model_output`, `malformed_tool_use`, `model_context_window_exceeded`.

### Minimal Go SigV4 signing (without SDK):
```
Required: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN (optional), AWS_REGION
Canonical request → String to sign → HMAC chain → Authorization header
```

### AWS SDK for Go v2 (recommended if available):
```go
import (
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

cfg, _ := config.LoadDefaultConfig(context.TODO(), config.WithRegion("us-east-1"))
client := bedrockruntime.NewFromConfig(cfg)
resp, _ := client.Converse(ctx, &bedrockruntime.ConverseInput{
    ModelId: aws.String("anthropic.claude-sonnet-4-6"),
    Messages: []types.Message{
        { Role: types.ConversationRoleUser, Content: []types.ContentBlock{
            { Text: aws.String("Hello") },
        }},
    },
})
```
