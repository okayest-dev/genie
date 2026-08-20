# OpenAI Responses-API Wire Format

Research compiled for Go agent harness implementation. All facts sourced from
OpenAI API reference and guides.

## 1. Request Shape

**Endpoint:** `POST /v1/responses` (or your proxy at `POST /zen/v1/responses`)

**Source:** https://platform.openai.com/docs/api-reference/responses/create

### Full JSON request body

```json
{
  "model": "gpt-4o",
  "input": [
    {
      "role": "system",
      "content": "You are a helpful assistant."
    },
    {
      "role": "user",
      "content": "What is the weather in Paris?"
    }
  ],
  "instructions": "Be concise.",
  "tools": [
    {
      "type": "function",
      "name": "get_weather",
      "description": "Get current weather for a location.",
      "parameters": {
        "type": "object",
        "properties": {
          "location": {
            "type": "string",
            "description": "City and country"
          }
        },
        "required": ["location"],
        "additionalProperties": false
      },
      "strict": true
    }
  ],
  "stream": true,
  "temperature": 1.0,
  "max_output_tokens": 4096,
  "top_p": 1.0,
  "tool_choice": "auto",
  "parallel_tool_calls": true,
  "previous_response_id": null,
  "store": true,
  "truncation": "disabled",
  "text": {
    "format": { "type": "text" }
  }
}
```

### Key fields

| Field | Type | Notes |
|---|---|---|
| `model` | string | Required |
| `input` | string \| array | String = single user message. Array of input items for multi-turn. |
| `instructions` | string | System/developer prompt. Not carried across `previous_response_id`. |
| `tools` | array | Function tools, built-in tools (web_search, file_search, code_interpreter, mcp, computer). |
| `stream` | boolean | Set `true` for SSE streaming. |
| `temperature` | number | Sampling temperature. |
| `max_output_tokens` | integer | Max tokens in response. |
| `top_p` | number | Nucleus sampling. |
| `tool_choice` | string \| object | `"auto"`, `"none"`, `"required"`, or `{"type":"function","name":"..."}`. |
| `parallel_tool_calls` | boolean | Allow multiple tool calls in one turn. |
| `previous_response_id` | string | Chain to a previous response for state. |
| `store` | boolean | Persist response (30-day TTL). |
| `truncation` | `"auto"` \| `"disabled"` | Auto-truncate if input exceeds context window. |
| `text` | object | Response format config: `{"format": {"type": "text"}}` or JSON schema. |
| `metadata` | object | Key-value pairs (max 16 keys, 512-char values). |
| `background` | boolean | Run as background task. |

### Input item types (in the `input` array)

```json
// Simple message
{"role": "user", "content": "Hello"}

// Message with typed content
{"role": "user", "content": [{"type": "input_text", "text": "Hello"}]}

// Image input
{"role": "user", "content": [{"type": "input_image", "image_url": "https://..."}]}

// Previous assistant output (replay)
{
  "type": "message",
  "id": "msg_abc123",
  "role": "assistant",
  "status": "completed",
  "content": [{"type": "output_text", "text": "Sure!"}]
}

// Function call output (tool result)
{
  "type": "function_call_output",
  "call_id": "call_abc123",
  "output": "{\"temperature\": 25}"
}
```

**Source:** https://platform.openai.com/docs/api-reference/responses/create#responses_create-input

---

## 2. SSE Streaming Format

**Source:** https://platform.openai.com/docs/guides/streaming-responses
**Source:** https://platform.openai.com/docs/api-reference/responses/streaming

SSE format: each event is `event: <type>\ndata: <json>\n\n`

### Event types (in order of a typical text response)

```json
// 1. Response created
event: response.created
data: {
  "type": "response.created",
  "sequence_number": 0,
  "response": { /* full Response object, status: "in_progress", output: [] */ }
}

// 2. Response in progress
event: response.in_progress
data: {
  "type": "response.in_progress",
  "sequence_number": 1,
  "response": { /* same Response object */ }
}

// 3. Output item added (e.g. a message or function_call)
event: response.output_item.added
data: {
  "type": "response.output_item.added",
  "sequence_number": 2,
  "output_index": 0,
  "item": {
    "id": "msg_abc123",
    "type": "message",
    "status": "in_progress",
    "role": "assistant",
    "content": []
  }
}

// 4. Content part added (inside a message)
event: response.content_part.added
data: {
  "type": "response.content_part.added",
  "sequence_number": 3,
  "item_id": "msg_abc123",
  "output_index": 0,
  "content_index": 0,
  "part": {"type": "output_text", "text": "", "annotations": []}
}

// 5. Text delta (streaming text chunks)
event: response.output_text.delta
data: {
  "type": "response.output_text.delta",
  "sequence_number": 4,
  "item_id": "msg_abc123",
  "output_index": 0,
  "content_index": 0,
  "delta": "Hello"
}

// 6. Text done
event: response.output_text.done
data: {
  "type": "response.output_text.done",
  "sequence_number": 5,
  "item_id": "msg_abc123",
  "output_index": 0,
  "content_index": 0,
  "text": "Hello, how can I help you?"
}

// 7. Content part done
event: response.content_part.done
data: {
  "type": "response.content_part.done",
  "sequence_number": 6,
  "item_id": "msg_abc123",
  "output_index": 0,
  "content_index": 0,
  "part": {"type": "output_text", "text": "Hello, how can I help you?", "annotations": []}
}

// 8. Output item done
event: response.output_item.done
data: {
  "type": "response.output_item.done",
  "sequence_number": 7,
  "output_index": 0,
  "item": {
    "id": "msg_abc123",
    "type": "message",
    "status": "completed",
    "role": "assistant",
    "content": [{"type": "output_text", "text": "Hello, how can I help you?", "annotations": []}]
  }
}

// 9. Response completed (includes usage)
event: response.completed
data: {
  "type": "response.completed",
  "sequence_number": 8,
  "response": { /* full Response object with status: "completed" and usage */ }
}
```

### Error event (can happen at any time)

```json
event: error
data: {
  "type": "error",
  "code": "server_error",
  "message": "Something went wrong",
  "param": null
}
```

### Terminal events

| Event | Meaning |
|---|---|
| `response.completed` | Response finished successfully. Contains full response + usage. |
| `response.failed` | Response failed. Contains full response with `status: "failed"` and `error` object. |
| `response.incomplete` | Response stopped early (e.g. `max_output_tokens` or `content_filter`). Contains `incomplete_details.reason`. |

---

## 3. Tool Call Format

**Source:** https://platform.openai.com/docs/guides/function-calling#streaming

### Non-streaming response output item

```json
{
  "id": "fc_12345xyz",
  "call_id": "call_12345xyz",
  "type": "function_call",
  "name": "get_weather",
  "arguments": "{\"location\":\"Paris, France\"}"
}
```

### Streaming function call events

```json
// Output item added (empty arguments)
event: response.output_item.added
data: {
  "type": "response.output_item.added",
  "sequence_number": 10,
  "output_index": 0,
  "item": {
    "type": "function_call",
    "id": "fc_abc123",
    "call_id": "call_xyz789",
    "name": "get_weather",
    "arguments": ""
  }
}

// Arguments delta (partial JSON string)
event: response.function_call_arguments.delta
data: {
  "type": "response.function_call_arguments.delta",
  "sequence_number": 11,
  "item_id": "fc_abc123",
  "output_index": 0,
  "delta": "{\"loc"
}

event: response.function_call_arguments.delta
data: {
  "type": "response.function_call_arguments.delta",
  "sequence_number": 12,
  "item_id": "fc_abc123",
  "output_index": 0,
  "delta": "ation\":\"Paris\"}"
}

// Arguments done (full string)
event: response.function_call_arguments.done
data: {
  "type": "response.function_call_arguments.done",
  "sequence_number": 13,
  "item_id": "fc_abc123",
  "output_index": 0,
  "name": "get_weather",
  "arguments": "{\"location\":\"Paris\"}"
}

// Output item done
event: response.output_item.done
data: {
  "type": "response.output_item.done",
  "sequence_number": 14,
  "output_index": 0,
  "item": {
    "type": "function_call",
    "id": "fc_abc123",
    "call_id": "call_xyz789",
    "name": "get_weather",
    "arguments": "{\"location\":\"Paris\"}",
    "status": "completed"
  }
}
```

**Aggregation pattern:** Concatenate `delta` strings from `response.function_call_arguments.delta` events, keyed by `output_index`. The `response.function_call_arguments.done` event confirms the full string.

---

## 4. Tool Result Format

**Source:** https://platform.openai.com/docs/guides/function-calling#handling-function-calls

Tool results are sent as input items of type `function_call_output`:

```json
{
  "type": "function_call_output",
  "call_id": "call_xyz789",
  "output": "{\"temperature\": 25, \"unit\": \"celsius\"}"
}
```

The `output` field is a string (typically JSON-encoded). For richer results, it can be an array of content objects:

```json
{
  "type": "function_call_output",
  "call_id": "call_xyz789",
  "output": [
    {"type": "input_text", "text": "Temperature is 25C"},
    {"type": "input_image", "image_url": "data:image/png;base64,..."}
  ]
}
```

**Multi-turn flow:**
1. Send request with tools → receive `function_call` output items
2. Execute functions locally
3. Append the original output items + `function_call_output` items to `input`
4. Send next request with updated `input`

Or use `previous_response_id` to chain responses without replaying the full history.

---

## 5. Error Shapes

**Source:** https://platform.openai.com/docs/guides/error-codes

### HTTP error response body

```json
{
  "error": {
    "message": "Invalid API key",
    "type": "invalid_request_error",
    "code": "invalid_api_key",
    "param": null
  }
}
```

### HTTP status codes

| Code | Meaning |
|---|---|
| 400 | Bad request / invalid parameters |
| 401 | Invalid authentication |
| 403 | Forbidden (unsupported region, etc.) |
| 404 | Not found |
| 429 | Rate limit / spend limit / credit exhaustion |
| 500 | Server error |
| 503 | Overloaded / slow down |

### Error codes in 429 responses

| `error.code` | Meaning |
|---|---|
| `credit_balance_exhausted` | No prepaid credits |
| `organization_spend_limit_exceeded` | Org spend limit hit |
| `project_spend_limit_exceeded` | Project spend limit hit |
| `organization_usage_limit_exceeded` | Usage tier limit hit |

### Response-level error (in streaming or non-streaming)

```json
{
  "error": {
    "code": "server_error" | "rate_limit_exceeded" | "invalid_prompt" | ... ,
    "message": "Human-readable description"
  }
}
```

The `error` field on the Response object itself uses `ResponseError`:
```json
{
  "error": {
    "code": "server_error",
    "message": "Something went wrong"
  }
}
```

### Streaming error event

```json
event: error
data: {
  "type": "error",
  "code": "server_error",
  "message": "Something went wrong",
  "param": null
}
```

---

## 6. Finish Reasons / Status

**Source:** https://platform.openai.com/docs/api-reference/responses/object

The Response object's `status` field takes these values:

| Value | Meaning |
|---|---|
| `completed` | Response finished normally |
| `failed` | Response failed (check `error` field) |
| `incomplete` | Response stopped early (check `incomplete_details.reason`) |
| `in_progress` | Response is still being generated |
| `cancelled` | Background response was cancelled |
| `queued` | Background response is queued |

The `incomplete_details.reason` field takes:
| Value | Meaning |
|---|---|
| `max_output_tokens` | Hit the token limit |
| `content_filter` | Stopped by content filtering |

---

## 7. Usage

**Source:** https://platform.openai.com/docs/api-reference/responses/object

Usage is reported in the `response.completed` event (streaming) or in the response object (non-streaming).

### Usage object

```json
{
  "usage": {
    "input_tokens": 37,
    "input_tokens_details": {
      "cache_write_tokens": 0,
      "cached_tokens": 0
    },
    "output_tokens": 11,
    "output_tokens_details": {
      "reasoning_tokens": 0
    },
    "total_tokens": 48
  }
}
```

| Field | Type | Description |
|---|---|---|
| `input_tokens` | integer | Total input tokens |
| `input_tokens_details.cache_write_tokens` | integer | Tokens written to prompt cache |
| `input_tokens_details.cached_tokens` | integer | Tokens read from cache |
| `output_tokens` | integer | Total output tokens |
| `output_tokens_details.reasoning_tokens` | integer | Tokens used for reasoning |
| `total_tokens` | integer | Sum of input + output |

**Note:** In streaming, `usage` is `null` until the `response.completed` event, where it contains the final token counts. Non-streaming responses include `usage` directly on the response object.

---

## Summary: Key Types for Go Implementation

### Input items (what goes in `input` array)
- `EasyInputMessage` — `{role, content}` with string or content array
- `FunctionCallOutput` — `{type: "function_call_output", call_id, output}`
- `ResponseOutputMessage` — previous assistant output to replay

### Output items (what comes back in `output` array)
- `message` — `{type: "message", role: "assistant", content: [{type: "output_text", text: "..."}]}`
- `function_call` — `{type: "function_call", call_id, name, arguments}`
- `reasoning` — `{type: "reasoning", ...}` (for reasoning models)

### Streaming event types (for SSE parser)
- `response.created`
- `response.in_progress`
- `response.output_item.added`
- `response.content_part.added`
- `response.output_text.delta`
- `response.output_text.done`
- `response.content_part.done`
- `response.output_item.done`
- `response.function_call_arguments.delta`
- `response.function_call_arguments.done`
- `response.completed`
- `response.failed`
- `response.incomplete`
- `error`
