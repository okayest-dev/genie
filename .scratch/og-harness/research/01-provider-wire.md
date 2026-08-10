# Research: provider wire — OpenCode Zen over OpenAI chat/completions

Research for ticket `01-provider-wire-spec`. Facts traced to primary sources: the OpenCode docs and live Zen catalog, the OpenAI API reference and function-calling guide, and the models.dev registry entry that OpenCode itself consumes. Fetch date: 2026-08-10.

## OpenCode Zen

### What it is

OpenCode Zen is an AI gateway serving a curated, benchmarked list of models ("a list of tested and verified models provided by the OpenCode team"). You sign in at opencode.ai/auth, add billing, copy an API key, and use the models from any agent. You are charged per request; credits can be added ([docs/zen Overview + How it works](https://opencode.ai/docs/zen/#overview), [How it works](https://opencode.ai/docs/zen/#how-it-works)).

### Base URL and wire protocol

- The Zen API base is **`https://opencode.ai/zen/v1`**. The models.dev registry entry for the `opencode` provider (named "OpenCode Zen") declares `api: "https://opencode.ai/zen/v1"` and `npm: "@ai-sdk/openai-compatible"` ([models.dev/api.json → provider `opencode`](https://models.dev/api.json), fetched 2026-08-10).
- For OpenAI-compatible models the chat-completions path is **`POST https://opencode.ai/zen/v1/chat/completions`**. The Zen docs' endpoints table lists that exact path for DeepSeek, MiniMax, GLM, Kimi, Big Pickle and the free models, with AI SDK package `@ai-sdk/openai-compatible` ([docs/zen Endpoints](https://opencode.ai/docs/zen/#endpoints)).
- The base URL is overridable via a provider `baseURL` option; the harness targets the chat/completions path specifically ([docs/providers Base URL](https://opencode.ai/docs/providers/#base-url)).

### Auth

- API keys are created in the Zen dashboard and pasted via `/connect`; OpenCode stores them in `~/.local/share/opencode/auth.json` ([docs/zen How it works](https://opencode.ai/docs/zen/#how-it-works), [docs/providers Credentials](https://opencode.ai/docs/providers/#credentials), [docs/providers OpenCode Zen](https://opencode.ai/docs/providers/#opencode-zen)).
- The environment variable is **`OPENCODE_API_KEY`**. It is declared in the models.dev registry entry for the `opencode` provider (`env: ["OPENCODE_API_KEY"]`, [models.dev/api.json](https://models.dev/api.json)), and OpenCode's provider loader reads keys from exactly those env entries (`provider.env.map((item) => envs[item])`, [opencode `provider.ts`](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/provider/provider.ts)). The ticket's assumption (Bearer + `OPENCODE_API_KEY`) is confirmed.
- On the wire this is the standard OpenAI-compatible **`Authorization: Bearer <key>`** header. The OpenAI API itself documents Bearer auth for chat completions ([OpenAI API reference — Authentication](https://platform.openai.com/docs/api-reference/authentication), [chat streaming example](https://platform.openai.com/docs/api-reference/chat/streaming)); the Zen docs do not restate the header verbatim — that part is inferred from the OpenAI-compatible wire plus the `@ai-sdk/openai-compatible` package, which sends `Bearer` ([AI SDK provider docs](https://ai-sdk.dev/docs/providers/openai-compatible)).

### Model catalog

- The catalog endpoint is **`GET https://opencode.ai/zen/v1/models`** — "You can fetch the full list of available models and their metadata from" this URL ([docs/zen Models](https://opencode.ai/docs/zen/#models)).
- Live fetch 2026-08-10 (`curl -s https://opencode.ai/zen/v1/models`) succeeded and returned the OpenAI `ListModels` shape, unauthenticated:
  ```json
  {"object":"list","data":[{"id":"claude-fable-5","object":"model","created":1786397007,"owned_by":"opencode"}, {"id":"gpt-5.6-sol","object":"model","created":1786397007,"owned_by":"opencode"}, ...]}
  ```
  Each entry is `{ id, object: "model", created: <unix seconds>, owned_by: "opencode" }`. The catalog returned 61 models total, including deprecated ones (e.g. `claude-sonnet-4`, `minimax-m2.5`, `glm-5`, `kimi-k2.5`). The docs list deprecated models separately ([docs/zen Deprecated models](https://opencode.ai/docs/zen/#deprecated-models)).
- Model IDs are referenced in OpenCode config as `opencode/<model-id>` ([docs/zen Endpoints](https://opencode.ai/docs/zen/#endpoints)).

### Free model IDs

Free (US$0) per the pricing table ([docs/zen Pricing](https://opencode.ai/docs/zen/#pricing)) — all served on the chat/completions endpoint:

| Model ID (catalog) | Docs pricing table name |
| --- | --- |
| `big-pickle` | Big Pickle (free, "stealth model") |
| `deepseek-v4-flash-free` | DeepSeek V4 Flash Free |
| `mimo-v2.5-free` | MiMo-V2.5 Free |
| `laguna-s-2.1-free` | Laguna S 2.1 Free |
| `ling-3.0-tiny-free` | Ling-3.0-tiny Free |
| `longcat-2.0-free` | LongCat-2.0 Free |
| `north-mini-code-free` | North Mini Code Free |
| `nemotron-3-ultra-free` | Nemotron 3 Ultra Free |

Discrepancy to note: the live catalog additionally returns **`ling-3.0-flash-free`** (a `-free` ID), which appears in neither the docs pricing table nor the free-model notes — treat the docs list as authoritative for what's marketed, the catalog as authoritative for what the endpoint accepts. The free models are temporary: "available on OpenCode for a limited time" while the team collects feedback ([docs/zen Pricing — free models note](https://opencode.ai/docs/zen/#pricing)).

### Endpoint path per model family

The same Zen key is routed per model family to a different wire/endpoint ([docs/zen Endpoints](https://opencode.ai/docs/zen/#endpoints)):

- OpenAI-compatible (DeepSeek, MiniMax, GLM, Kimi, **Big Pickle, all `*-free` models**): `https://opencode.ai/zen/v1/chat/completions`, `@ai-sdk/openai-compatible`
- Responses-API models (GPT 5.x, Grok): `https://opencode.ai/zen/v1/responses`, `@ai-sdk/openai`
- Anthropic Messages models (Claude, Qwen): `https://opencode.ai/zen/v1/messages`, `@ai-sdk/anthropic`
- Google models (Gemini): `https://opencode.ai/zen/v1/models/<model-id>` per model, `@ai-sdk/google`

So the chat/completions wire is valid for all free models + Big Pickle, but not for the GPT/Claude/Gemini rows.

### Rate limits, usage, credits

- **No per-request rate limits or RPM/TPM figures are documented** anywhere in the Zen docs as of this fetch. The documented limits are billing-level:
  - Monthly usage limits can be set per workspace and per member; Zen will not exceed them (with an auto-reload caveat) ([docs/zen Monthly limits](https://opencode.ai/docs/zen/#monthly-limits)).
  - Auto-reload: when balance drops below $5, Zen auto-loads $20 (amount configurable, feature can be disabled) ([docs/zen Auto-reload](https://opencode.ai/docs/zen/#auto-reload)).
  - Per-request billing against credits; prices per 1M tokens in the pricing table ([docs/zen Pricing](https://opencode.ai/docs/zen/#pricing), [How it works](https://opencode.ai/docs/zen/#how-it-works)).
- Requests to a disabled model "will return an error" (workspace model access control) ([docs/zen Model access](https://opencode.ai/docs/zen/#model-access)).
- No Zen-specific streaming notes are documented; streaming behavior is whatever the OpenAI-compatible chat/completions endpoint does (see below).

### sources

- [https://opencode.ai/docs/zen/](https://opencode.ai/docs/zen/) — the Zen reference: how it works, endpoints table, pricing, free models, privacy, teams.
- [https://opencode.ai/zen](https://opencode.ai/zen) — marketing overview.
- [https://opencode.ai/docs/providers/](https://opencode.ai/docs/providers/) — provider credentials, baseURL override, OpenCode Zen section.
- [https://opencode.ai/zen/v1/models](https://opencode.ai/zen/v1/models) — live catalog (fetched with `curl`).
- [https://models.dev/api.json](https://models.dev/api.json) — registry entry for provider `opencode`: `api`, `npm`, `env: ["OPENCODE_API_KEY"]` (this is the metadata OpenCode's loader consumes).
- [https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/provider/provider.ts](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/provider/provider.ts) — env-key loading, `baseURL`/`apiKey` wiring.

## OpenAI chat/completions request

Endpoint: **`POST /v1/chat/completions`** (on Zen: `/zen/v1/chat/completions`). All fields below from the [Create chat completion reference](https://platform.openai.com/docs/api-reference/chat/create).

- `model: string` — model ID, e.g. `big-pickle`.
- `messages: array` — the conversation. Roles: `developer`, `system`, `user`, `assistant`, `tool`, and (legacy) `function`. `content` is a string, or an array of content parts (text/image/audio/file) depending on role.
  - `system`/`developer`: developer-provided instructions. `user`: end-user messages. `assistant`: model messages, may carry `tool_calls` (required unless `content` specified) or `function_call` (deprecated). `tool`: result of a tool call — **must** carry `tool_call_id` plus `content`. `function` (deprecated): legacy function result with `name` + `content`.
- `tools: array` — `ChatCompletionFunctionTool` shape:
  ```json
  {
    "type": "function",
    "function": {
      "name": "bash",
      "description": "Run a shell command.",
      "parameters": {
        "type": "object",
        "properties": { "cmd": { "type": "string" } },
        "required": ["cmd"]
      }
    }
  }
  ```
  `function.name`: a-z, A-Z, 0-9, underscores, dashes; max 64 chars. `function.description` optional. `function.parameters` optional, a **JSON Schema** object (omitting it = empty parameter list). `strict` optional (structured-outputs constraint). Also `parallel_tool_calls: boolean` controls whether the model may emit multiple calls per turn.
- `tool_choice` — `"none"` (default when no tools), `"auto"` (default when tools present), `"required"`, or `{"type":"function","function":{"name":"my_function"}}` to force a specific tool.
- `stream: boolean` — if true, respond as a stream of SSE chunks.
- `stream_options: { include_usage?: boolean }` — only meaningful when streaming; `include_usage: true` adds a usage chunk before `data: [DONE]`.
- `max_tokens: number` — deprecated in favor of `max_completion_tokens` (the latter includes reasoning tokens; `max_tokens` is incompatible with o-series models). For the OpenAI-compatible Zen models, `max_tokens` is the widely-supported field.
- `temperature: number` (0–2), `top_p`, `presence_penalty`, `frequency_penalty`, `n`, `stop`, `seed`, `response_format`, `user` — all optional sampling/config params.

Example request (streaming, with one function tool):

```json
{
  "model": "big-pickle",
  "messages": [
    { "role": "system", "content": "You are a coding agent. Prefer the bash tool over guessing." },
    { "role": "user", "content": "List the files in the current directory." }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "bash",
        "description": "Run a shell command and return its output.",
        "parameters": {
          "type": "object",
          "properties": { "cmd": { "type": "string", "description": "Command line to run." } },
          "required": ["cmd"]
        }
      }
    }
  ],
  "tool_choice": "auto",
  "stream": true,
  "max_tokens": 4000,
  "temperature": 0.7
}
```

## OpenAI chat/completions response

Non-streaming response (`ChatCompletion`), from the [Create chat completion reference](https://platform.openai.com/docs/api-reference/chat/create#returns):

```json
{
  "id": "chatcmpl-123",
  "object": "chat.completion",
  "created": 1700000000,
  "model": "big-pickle",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "It's about 15°C in Paris.",
        "tool_calls": [
          {
            "id": "call_abc123",
            "type": "function",
            "function": { "name": "get_weather", "arguments": "{\"location\":\"Paris, France\"}" }
          }
        ]
      },
      "logprobs": null,
      "finish_reason": "tool_calls"
    }
  ],
  "usage": {
    "prompt_tokens": 9,
    "completion_tokens": 12,
    "total_tokens": 21
  }
}
```

- Top level: `id`, `object` (`"chat.completion"`), `created`, `model`, `choices`, `usage`, `system_fingerprint` (optional), `service_tier` (optional).
- `choices[]` — can be more than one if `n > 1`. Each has `index`, `message`, `logprobs`, `finish_reason`.
- `message` — `role: "assistant"`, `content: string | null`, optional `tool_calls` (array of `{ id, type: "function", function: { name, arguments } }` where `arguments` is the **JSON-encoded string**), optional `refusal`. Legacy `function_call` deprecated.
- `finish_reason` values: `"stop"`, `"length"`, `"tool_calls"`, `"content_filter"`, `"function_call"` (deprecated).
- `usage` — `prompt_tokens`, `completion_tokens`, `total_tokens`, plus optional `completion_tokens_details` (e.g. `reasoning_tokens`) and `prompt_tokens_details` (e.g. `cached_tokens`). Reasoning tokens count against completion/billing totals.

## Streaming SSE shape

From the [streaming section of the chat reference](https://platform.openai.com/docs/api-reference/chat/streaming) and the `ChatCompletionChunk` domain type ([domain types](https://platform.openai.com/docs/api-reference/chat/create)):

- With `stream: true` the response is **server-sent events** (SSE); each event is a `data:` line containing one `chat.completion.chunk` JSON object, ending with a final `data: [DONE]` terminator (the reference explicitly names the `data: [DONE]` message when describing `stream_options.include_usage`).
- Chunk shape: `object: "chat.completion.chunk"`; `id` is the same across all chunks; `created` and `model` repeat each chunk.
- `choices[0].delta` — the incremental payload: optional `role` (usually `"assistant"`, appears in the first chunk), optional `content` (text delta fragments), optional `refusal`, optional `tool_calls`. The final chunk carries an empty `delta` and the `finish_reason` (`"stop"` or `"tool_calls"`).
- `choices[0].finish_reason` — `null` on every chunk except the last, where it is the terminal reason (`"stop"`, `"length"`, `"tool_calls"`, ...).
- **Usage in streams**: `usage` is absent unless `stream_options: {"include_usage": true}`. When enabled, every chunk except the last has `usage: null`, and one extra chunk streamed just before `data: [DONE]` has `usage` populated and `choices: []`. If the stream is interrupted/cancelled you may not receive that final usage chunk — so a consumer should treat usage as best-effort and tolerate its absence.

Text delta accumulation — concatenate `choices[0].delta.content` across chunks:

```
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1694268190,"model":"big-pickle","choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1694268190,"model":"big-pickle","choices":[{"index":0,"delta":{"content":"Hello"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1694268190,"model":"big-pickle","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}]}
data: [DONE]
```

(Adapted from the [chat reference streaming example](https://platform.openai.com/docs/api-reference/chat/streaming#streaming), with the model ID swapped to a Zen free model.)

## Tool calling

Flow, from the [function-calling guide](https://platform.openai.com/docs/guides/function-calling) (note: its worked code samples target the Responses API; the Chat Completions shapes below come from the [chat reference](https://platform.openai.com/docs/api-reference/chat/create)):

1. Send `tools` + `tool_choice` in the request (definitions count as input tokens).
2. The model replies with `message.tool_calls` and `finish_reason: "tool_calls"` — zero, one, or several calls.
3. The harness executes each call (`arguments` is a JSON string; validate before executing — "the model does not always generate valid JSON").
4. Echo the assistant turn back **verbatim** (its `tool_calls` included) as an `assistant` message, then append one `role: "tool"` message per executed call carrying `tool_call_id` (the call's `id`) and `content` (the result, typically a string), and resubmit with the same `tools`.
5. Repeat until `finish_reason` is `"stop"`.

```json
{
  "model": "big-pickle",
  "messages": [
    { "role": "user", "content": "List the files." },
    {
      "role": "assistant",
      "content": null,
      "tool_calls": [
        { "id": "call_abc123", "type": "function", "function": { "name": "bash", "arguments": "{\"cmd\":\"ls\"}" } }
      ]
    },
    { "role": "tool", "tool_call_id": "call_abc123", "content": "README.md\nmain.go\n" }
  ],
  "tools": [ /* same tool defs as the first request */ ]
}
```

### Streaming a tool call

`delta.tool_calls` is an array of fragments identified by **`index`** (the position in the final `tool_calls` array). `id`, `type: "function"`, and `function.name` appear only on the **first** fragment for that index; `function.arguments` arrives as incremental string fragments that you **concatenate** per index. Accumulated arguments then replace the fragment string.

Streamed chunk sequence:

```
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1694268190,"model":"big-pickle","choices":[{"index":0,"delta":{"role":"assistant","content":null},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1694268190,"model":"big-pickle","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc123","type":"function","function":{"name":"bash","arguments":""}}]},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1694268190,"model":"big-pickle","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":"}}]},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1694268190,"model":"big-pickle","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\""}}]},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1694268190,"model":"big-pickle","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1694268190,"model":"big-pickle","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"tool_calls"}]}
data: [DONE]
```

Accumulated result: `tool_calls[0] = { id: "call_abc123", type: "function", function: { name: "bash", arguments: "{\"cmd\":\"ls\"}" } }` — structurally identical to the non-streaming `message.tool_calls[0]`, which is what you echo back as the `assistant` message in step 4. (Chunk field shapes from the `ChatCompletionChunk` domain type; the index-keyed accumulation rule is the standard chat-completions pattern also reflected in the reference's `delta.tool_calls` schema.)

## Open questions

- **Zen-specific streaming fidelity**: the SSE framing (`data:` lines, `data: [DONE]`, `stream_options.include_usage`) is documented for OpenAI's endpoint, not for Zen's. The docs assert only that the OpenAI-compatible models speak `/v1/chat/completions` with `@ai-sdk/openai-compatible`; we have not exercised a live streamed request (needs an API key). If Zen's gateway strips `stream_options` or the usage chunk, the harness must not hard-depend on them.
- **Free-model rate limiting**: no rate limits are documented for Zen (free or paid). Behavior under sustained free-model load (429s, backoff headers) is unknown.
- **Free-model tool-calling support**: the docs don't state per-model tool-call support for the free IDs; `big-pickle` is described as a reasoning model with "tool use" but the `*-free` models' tool-call quality/availability on the chat/completions wire is unverified. If one of the free models rejects `tools`, the harness needs a no-tools fallback path.
