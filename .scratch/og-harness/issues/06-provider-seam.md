Type: grilling
Status: resolved
Blocked by: 01

# 06-provider-seam

## Question

Design the v1 LLM client interface (the seam between the agent loop and the provider) so native Anthropic / OpenAI-responses / Google wires slot in later without rework. What does the agent loop depend on — a stream of text deltas, tool-call deltas, finish reason, usage? What may leak (provider-specific fields, wire quirks)? This is the later-phase multi-provider ticket's blocker — the seam decides how much of that is pre-paid now.

## Answer

Decided by grilling over one round. The seam is a small `llm` package: one `Client` interface the agent loop depends on, one OpenAI-compatible implementation in v1; native wires slot in later as further implementations of the same interface.

- **Consumption shape**: pull iterator — `Stream(ctx, req) (iter.Seq[Event], error)`. Open failures (auth, 404, network-down) surface as the returned error; mid-stream failures surface as a terminal `Error` event. The loop `break`s to stop early (Ctrl+C, 05). No channels, no callback.
- **Event model**: the seam normalizes and accumulates — events: `text` delta, `tool_call` (complete — id, name, args JSON string; emitted at finish, not streamed partially), `finish` (canonical reason: `stop`/`tool_calls`/`length`/`other`), `usage` (best-effort, only when the provider sends it, 01). One event struct with a kind tag. Partial tool-call args are not surfaced in v1 (05's header prints after completion; live arg-streaming is TUI fog). Reasoning content is dropped at the seam (05). No raw provider payload rides on events — the seam is the normalization boundary.
- **Error surface**: `ProviderError{ Kind, Message, StatusCode }`, `Kind` ∈ `auth` | `rate_limit` | `invalid_request` | `network` | `timeout` | `other`. `invalid_request` drives 01's no-tools fallback (a free model rejecting the `tools` array → retry without tools); `auth`/`rate_limit` drive user-facing messages. `Error()` returns `Message`.
- **Canonical messages**: the loop's conversation is canonical `Message{ Role, Content, ToolCalls, ToolCallID }`; the adapter serializes/parses per protocol (OpenAI `role: "tool"` + `tool_call_id`; Anthropic `tool_result` blocks + role mapping; Google content parts — later). 01's verbatim assistant-echo happens at the canonical level. v1 `Content` is a plain string; multi-part content (images) is deliberately unmodeled (later phase).
- **Model listing**: `ListModels(ctx) ([]Model, error)` on the `Client` — normalized to `{ ID }`; drives `/model` (05). Every native protocol (OpenAI, Anthropic, Google) has a models-list endpoint, so this is cheap to pre-pay.
- **Package**: `llm`, interface `Client`. v1 has exactly one wire implementation (OpenAI-compatible chat/completions per 01); native wires are more implementations, no loop rework.
