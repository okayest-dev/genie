Status: ready-for-agent

# Debug & Verbose Flag

## Problem Statement

Og is completely silent unless it encounters an error or produces a successful reply. There is no way to observe what the harness is doing internally — which config file was loaded, what model was resolved, what instruction sources were assembled, what HTTP request was sent to the provider, what SSE chunks arrived, or how many tokens were consumed. When something goes wrong (wrong model, missing API key, unexpected provider behaviour), the user has no diagnostic surface beyond the final `Error: ...` message. This makes debugging configuration issues, provider problems, and harness misbehaviour unnecessarily difficult.

## Solution

Add two CLI flags — `-v` (verbose) and `-d` (debug) — plus one env var (`OG_DEBUG`) that control structured diagnostic output via `log/slog` to stderr. Verbose mode (`-v`) shows high-level flow: config resolution, instruction assembly, agent turn lifecycle, and token usage. Debug mode (`-d` or `OG_DEBUG=true`) adds low-level detail: full config values, instruction content, HTTP request/response details, and SSE chunk parsing. Debug implies verbose — enabling debug automatically enables verbose output. Neither flag affects stdout, exit codes, or the model's reply.

## User Stories

1. As a user, I want to pass `-v` to see high-level flow information (config loaded, model chosen, turn started, tokens used), so that I can confirm the harness is doing what I expect without wading into low-level detail.
2. As a user, I want to pass `-d` to see full diagnostic detail (config values, HTTP payloads, SSE chunks), so that I can diagnose provider issues, configuration mistakes, or harness bugs.
3. As a user, I want `-d` to automatically include verbose output, so that I don't have to remember to pass both flags when debugging.
4. As a user, I want `OG_DEBUG=true` (or `1`, or `yes`) to enable debug mode without a CLI flag, so that I can set persistent debug output in my shell profile or CI environment.
5. As a user, I want `-d` to take precedence over `OG_DEBUG` — if either is active, debug is on, so that I can always override a stale env var with the flag.
6. As a user, I want debug/verbose output to go to stderr, so that stdout stays clean for piping and capturing the model's reply.
7. As a user, I want debug output to appear on stderr regardless of whether stdout is a TTY, so that `og -d -p "hello" | jq` still shows debug on my terminal.
8. As a user, I want my API key to never appear in debug output, so that I can share debug logs or paste them into issues without leaking credentials.
9. As a user, I want verbose output to show the final resolved config (model, base URL, instruction file path, AGENTS.md found/not), so that I can confirm my config is being applied correctly.
10. As a user, I want verbose output to show which env vars were applied (names only, not values, except API key which is never shown), so that I can tell whether an override is active.
11. As a user, I want verbose output to show the total assembled instruction length, so that I can gauge whether my instruction sources are contributing meaningfully.
12. As a user, I want verbose output to show the model name, provider base URL, and prompt length at turn start, so that I know exactly what is being sent.
13. As a user, I want verbose output to show finish reason and token usage (prompt + completion + total) when a turn completes, so that I can track cost and detect truncation.
14. As a user, I want debug output to show every config value as it is resolved (with API key redacted), so that I can trace exactly where a setting came from.
15. As a user, I want debug output to show the full instruction text content, so that I can verify what the model actually received.
16. As a user, I want debug output to show each instruction source appended with its byte length, so that I can see the contribution of each source.
17. As a user, I want debug output to show the full HTTP request URL, headers (with auth redacted), and request body size, so that I can verify the wire request.
18. As a user, I want debug output to show the HTTP response status code and round-trip latency, so that I can diagnose slow or failed requests.
19. As a user, I want debug output to show the error response body on HTTP failures, so that I can see the provider's error message.
20. As a user, I want debug output to show each parsed SSE chunk summary (event kind, content preview, token counts) rather than raw text deltas, so that I can trace the stream without flooding stderr.
21. As a user, I want debug output to show the `[DONE]` sentinel and final usage breakdown, so that I can confirm the stream completed cleanly.
22. As a user, I want neither `-v` nor `-d` to produce any output by default, so that the harness stays silent unless I explicitly ask for diagnostics.
23. As a user, I want a one-line banner when verbose or debug mode is enabled (e.g., `level=INFO msg="verbose mode enabled"`), so that I can confirm the flag took effect.
24. As a user, I want debug output to use `log/slog` with a text handler and no timestamps, so that the output is clean, grep-friendly, and consistent with Go stdlib conventions.
25. As a user, I want `-v` without `-d` to show only high-level flow (no HTTP payloads, no SSE chunks), so that I can get a quick overview without noise.

## Implementation Decisions

### Slog configuration

Slog is configured once in `main.go`, early — before config loading, instruction assembly, or any other work. The handler is `slog.NewTextHandler` writing to `os.Stderr`, with `ReplaceAttr` stripping the `time` key to remove timestamps. The log level is set based on flag/env precedence:

- No flags, no env: `slog.LevelWarn` (silent — neither info nor debug messages appear)
- `-v` only: `slog.LevelInfo` (verbose messages appear, debug suppressed)
- `-d` (with or without `-v`): `slog.LevelDebug` (both info and debug messages appear)
- `OG_DEBUG=true` (with or without `-v`): same as `-d`

The env var `OG_DEBUG` is parsed in `main.go` before `config.Load()`, using truthy string comparison (`"true"`, `"1"`, `"yes"`). The `-d` flag overrides a false `OG_DEBUG` — either one being active enables debug.

### Two output layers

**Verbose (`slog.Info`)** — high-level flow:

- Config loading: file path and whether it was found; final resolved model; base URL; which env var names were applied (not values)
- Instruction assembly: instruction file path (if set); whether AGENTS.md was found; total assembled instruction length
- Agent turn: model name and provider base URL; prompt length; turn start
- Stream completion: finish reason; token usage (prompt, completion, total)

**Debug (`slog.Debug`)** — low-level detail:

- Config loading: every resolved config value (API key redacted); env var values (API key redacted); TOML file parse result
- Instruction assembly: full assembled instruction text; each source appended with byte length
- HTTP client: full request URL; request headers (Authorization redacted to `Bearer <redacted>`); request body size in bytes; response status code; round-trip latency; error response body on failure
- SSE stream: each parsed chunk summary (event kind, content preview or finish reason, token counts); `[DONE]` sentinel; final usage breakdown

Text deltas are not logged individually — they would flood stderr. Instead, the debug layer logs chunk summaries and the final usage line.

### Threading mechanism

The debug/verbose flag is **not** added to the `Config` struct. Debug is a CLI concern, not a config concern. Since slog is configured globally in `main.go` before anything else runs, all downstream packages (`config`, `instruct`, `agent`, `llm/openai`) simply call `slog.Info()` or `slog.Debug()` directly — no parameter threading required. The slog level controls what appears.

The only package that needs special handling is `internal/llm/openai`, which must redact the API key in debug output. This is done by hardcoding the redacted string in slog calls — the client does not carry debug state.

### Flag definitions

Added to the existing `flag.NewFlagSet` in `main.go`:

- `-v` (bool): enable verbose output
- `-d` (bool): enable debug output (implies `-v`)

Usage string updated to document both flags and the `OG_DEBUG` env var.

### Security

The API key is always redacted in all debug and verbose output. No slog call ever includes the actual key value. Headers are logged as `Authorization: Bearer <redacted>`. Env var values are logged except for the API key env var.

### Stderr behaviour

All debug/verbose output goes to stderr via `slog`. This is standard unix behaviour — stderr appears on the terminal regardless of whether stdout is piped. No TTY detection. No auto-suppression.

### Startup banner

When verbose or debug mode is active, a one-line banner is emitted as the first slog call:

- Verbose: `level=INFO msg="verbose mode enabled"`
- Debug: `level=DEBUG msg="debug mode enabled"` (which also implies verbose)

This confirms the flag took effect before any other output appears.

## Testing Decisions

### What makes a good test

Tests assert on observable behaviour only: stderr contains expected substrings when `-v` or `-d` is passed, stderr is empty when neither is passed, stdout is unaffected by debug flags, and the API key never appears in debug output. No assertions on internal slog calls or handler configuration — those are implementation details.

### Modules tested

- **`cmd/og` (e2e)**: binary tests that pass `-v`, `-d`, `OG_DEBUG=true`, and combinations; assert on stderr content and stdout emptiness. Verify the banner appears, verbose messages appear with `-v`, debug messages appear with `-d`, and nothing appears without flags.
- **`internal/llm/openai` (e2e)**: verify that debug output includes HTTP details with the API key redacted. Verify that the full API key never appears in stderr even when `OG_DEBUG=1` is set.
- **`internal/config` (unit)**: verify that config loading produces expected verbose/debug slog output when the global slog level is set to `LevelInfo` or `LevelDebug`.

### Prior art

The existing e2e test suite (`internal/e2e/e2e_test.go`) compiles the binary in `TestMain` and drives it as a subprocess, capturing stdout, stderr, and exit codes. The same pattern applies — pass `-v`/`-d` flags and assert on stderr content. The fake provider (`internal/e2e/fake/fake.go`) is reused unchanged.

## Out of Scope

- **Log levels beyond info/debug**: no warn-level or error-level debug output; errors go through the existing `fmt.Fprintf(stderr, "Error: ...")` path.
- **Timestamps in debug output**: stripped by default; can be added later if latency analysis becomes important.
- **Debug output to a file**: stderr only in v1; a `OG_DEBUG_LOG` file destination is a future option.
- **Per-package debug control**: all packages share the same slog level; no way to debug config loading without also debugging HTTP.
- **`OG_VERBOSE` env var**: verbose is CLI-only (`-v` flag); if users want persistent verbose output they can alias `og` to `og -v`.
- **Structured JSON debug output**: text handler only; JSON handler is a future option.
- **Interactive mode debug integration**: the REPL (ticket 09) may need special handling for debug output interleaving with prompts; that lands when the REPL is built.

## Further Notes

- This feature is not in the original harness spec (`og-harness/spec.md`). It was identified during implementation as a diagnostic need.
- The design follows the unix convention of `curl -v` / `curl --debug` — stderr for diagnostics, stdout for data.
- `log/slog` is stdlib-only (Go 1.21+), consistent with Og's std-lib-first philosophy. No new dependencies.
- The two-layer design (verbose = high-level, debug = low-level) is extensible — future layers (trace, per-tool) can be added as new slog levels or custom levels without breaking the existing interface.
