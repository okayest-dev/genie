Type: research
Status: resolved
Blocked by:

# 01-provider-wire-spec

## Question

Pin down, for the spec, the exact wire facts the agent loop depends on:

- **OpenCode Zen**: base URL, auth (Bearer, env `OPENCODE_API_KEY`), the model-catalog endpoint (`/v1/models`), the free model IDs and their availability, and any rate limits or usage reporting.
- **OpenAI chat/completions**: the request JSON (messages, `tools`/`tool_calls`, `stream`, `max_tokens`, temperature), the response JSON, and the streaming SSE event shapes (`chunk`/`delta`, tool-call accumulation, finish reason, usage). Exact field names and shapes matter — this is what the v1 `llm` package parses.

Follow every claim to a primary source (opencode docs, OpenAI API reference, or the Zen catalog itself). Deliverable: a Markdown findings doc citing each claim's source, saved to `.scratch/og-harness/research/01-provider-wire.md`.

Resolved by a `/research` subagent; capture findings on a `research/01-provider-wire` branch.

## Answer

Full findings: `.scratch/og-harness/research/01-provider-wire.md` (primary sources cited).

- **Zen wire**: OpenAI-compatible chat/completions at `POST https://opencode.ai/zen/v1/chat/completions`, Bearer auth, key from `OPENCODE_API_KEY`. Catalog: `GET https://opencode.ai/zen/v1/models` (unauthenticated, OpenAI `ListModels` shape, 61 models).
- **Free models**: all `*-free` IDs plus `big-pickle` ride the chat/completions wire. GPT/Grok (Responses API), Claude/Qwen (Anthropic messages), Gemini (Google) do **not** — those are the later multi-provider phase.
- **SSE**: `data: <chunk>` lines, `data: [DONE]` terminator. Text via concatenating `delta.content`; tool-call args via concatenating `delta.tool_calls[i].function.arguments`, keyed by `index`; `finish_reason` (`stop`/`tool_calls`) on the final chunk.
- **Usage**: absent unless `stream_options.include_usage: true`, and then only in an extra pre-`[DONE]` chunk with `choices: []` — treat as best-effort, don't hard-depend.
- **Tool flow**: echo the assistant turn verbatim (with `tool_calls`), then one `role: "tool"` message per call carrying `tool_call_id` and `content` (JSON-string arguments, validated before execution).
- **Open risks for the spec**: Zen streaming fidelity unverified live (no key), no documented rate limits, and free-model tool-call support unverified → include a **no-tools fallback path** and treat usage as optional.
