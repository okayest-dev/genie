# 03 — Debug logging (openai HTTP client)

**What to build:** `slog.Debug` calls in `internal/llm/openai` that emit low-level HTTP and SSE detail when debug mode is active. The client logs the full request URL, headers (with `Authorization` redacted to `Bearer <redacted>`), request body size in bytes, response status code, round-trip latency, and error response body on HTTP failures. The SSE stream logs each parsed chunk summary (event kind, content preview or finish reason, token counts) rather than raw text deltas, plus the `[DONE]` sentinel and final usage breakdown. The API key is never included in any slog call — hardcoded redacted string is used. E2e test through the compiled binary verifies debug output includes HTTP details with the API key redacted.

**Blocked by:** 01 — Slog infrastructure.

**Status:** ready-for-agent

- [ ] `openai.Client.doRequest` logs request URL, headers (auth redacted), body size at debug level
- [ ] `openai.Client.doRequest` logs response status code and round-trip latency at debug level
- [ ] `openai.Client.doRequest` logs error response body on HTTP failures at debug level
- [ ] `openai.Client.Stream` logs each parsed SSE chunk summary (event kind, content preview, token counts) at debug level
- [ ] `openai.Client.Stream` logs `[DONE]` sentinel and final usage breakdown at debug level
- [ ] API key never appears in any debug output — `Authorization` header logged as `Bearer <redacted>`
- [ ] E2e test: `OG_DEBUG=1` with the compiled binary; stderr contains HTTP details with API key redacted
- [ ] E2e test: full API key string never appears in stderr even with `OG_DEBUG=1`
