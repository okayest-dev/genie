# 01 — Wire tracer bullet

**What to build:** the skeleton of the whole harness and the thinnest end-to-end path through it. This establishes the module structure, the `llm` seam (a `Client` interface with a pull-iterator `Stream` and `ListModels`), one OpenAI-compatible wire implementation (chat/completions over SSE), and the agent loop running a single no-tool turn. It is exposed through a minimal `-p` invocation: run a prompt, print the streamed reply to stdout. The black-box test seam — the compiled harness driven as a subprocess against a scripted in-process fake provider — is built here so every later ticket is tested through the real binary. This deliberately pre-empts part of ticket 12's surface; that is the point of a tracer bullet.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] `-p "<prompt>"` sends a single chat/completions request to the provider and prints the streamed reply text to stdout, exiting 0.
- [ ] `-p ""` with no piped stdin exits with a usage error (exit 3).
- [ ] Text deltas stream live; provider reasoning fields are ignored; missing usage never breaks a turn.
- [ ] Open failures (auth, network down, 404) print a clear `Error: ...` and exit non-zero — never a stack trace.
- [ ] The test seam drives the compiled binary against the fake provider, asserting only observable behavior (stdout, stderr, exit codes).

Note: this ticket uses the `-p` surface from spec ticket 08 in minimal form only (no stdin, no persistence, no stderr framing yet) — those land in ticket 12.
