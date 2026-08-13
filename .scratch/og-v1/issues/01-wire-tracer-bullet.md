# 01 — Wire tracer bullet

**What to build:** the skeleton of the whole harness and the thinnest end-to-end path through it. This establishes the module structure, the `llm` seam (a `Client` interface with a pull-iterator `Stream` and `ListModels`), one OpenAI-compatible wire implementation (chat/completions over SSE), and the agent loop running a single no-tool turn. It is exposed through a minimal `-p` invocation: run a prompt, print the streamed reply to stdout. The black-box test seam — the compiled harness driven as a subprocess against a scripted in-process fake provider — is built here so every later ticket is tested through the real binary. This deliberately pre-empts part of ticket 12's surface; that is the point of a tracer bullet.

**Blocked by:** None — can start immediately.

**Status:** resolved

- [x] `-p "<prompt>"` sends a single chat/completions request to the provider and prints the streamed reply text to stdout, exiting 0.
- [x] `-p ""` with no piped stdin exits with a usage error (exit 3).
- [x] Text deltas stream live; provider reasoning fields are ignored; missing usage never breaks a turn.
- [x] Open failures (auth, network down, 404) print a clear `Error: ...` and exit non-zero — never a stack trace.
- [x] The test seam drives the compiled binary against the fake provider, asserting only observable behavior (stdout, stderr, exit codes).

## Resolution

Implemented on branch `01-wire-tracer-bullet`. Module `github.com/okayest-dev/og`, std-lib only, no deps.

- **Layout**: `cmd/og` (CLI, `-p` surface, exit 0/1/3), `internal/llm` (the seam: `Client` interface with pull-iterator `Stream` + `ListModels`, canonical `Message`, `Event` kinds text/finish/usage/error, `ProviderError{Kind,Message,StatusCode}`), `internal/llm/openai` (the one wire implementation: chat/completions over SSE — text deltas, finish_reason mapping, include_usage chunk, `data: [DONE]`, reasoning fields dropped by not decoding them, mid-stream provider errors → terminal `ProviderError` event), `internal/agent` (single no-tool turn, streams deltas to stdout then a trailing newline), `internal/e2e/fake` (the scripted in-process provider — the one place the wire is fabricated).
- **Wire config** via env only for now (ticket 02 adds the TOML layer): `OG_BASE_URL` (default `https://opencode.ai/zen/v1`), `OG_MODEL` (default `big-pickle`), `OPENCODE_API_KEY` (optional; Bearer when set).
- **Seams**: (1) black-box — e2e tests compile the binary in `TestMain` and drive it as a subprocess against the fake provider, asserting stdout/stderr/exit codes only, covering reply streaming, wire shape (single POST /chat/completions with model/messages/stream), reasoning-drop, usage tolerance (absent and present), auth/429/404, network-down, and liveness (deltas arrive before process exit). (2) unit — `parseChunk` accumulation rules and `ListModels` parsing tested directly.
- Open failures close the response body; 401/403→auth, 429→rate_limit, 400→invalid_request, else other; the provider's `error.message` is preferred when the body carries one.
- Deferred by design: stdin-piped prompts, persistence, stderr framing, exit code 2, and the REPL are later tickets (12/09); tool-call events land with ticket 05.

Note: this ticket uses the `-p` surface from spec ticket 08 in minimal form only (no stdin, no persistence, no stderr framing yet) — those land in ticket 12.
