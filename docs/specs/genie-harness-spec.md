Status: ready-for-agent

# og — Agent Harness

## Problem Statement

Running a coding agent in a terminal means juggling a heavyweight toolchain: a full TUI, a plugin system, raw-mode screen management, permission popups, and a sprawling config surface — pi, opencode, and friends all ship this by default. Today there is no minimal, std-lib-first Go harness that just does the loop: read a prompt, run an agentic `read`/`write`/`edit`/`bash` loop against a cheap OpenAI-compatible provider, show a change ledger, persist resumable sessions, and get out of the way. Someone who wants a bare-bones agent on an OpenCode Zen free model has to either accept all the machinery or build it themselves.

## Solution

`og` is a minimal, std-lib-first Go terminal agent harness in the pi mould. It runs an agent loop against an OpenAI-compatible provider (OpenCode Zen free models first) with four tools — `read`, `write`, `edit`, `bash` — behind a REPL with a five-command slash surface (`/changes`, `/new`, `/model`, `/help`, `/quit`), a scriptable `-p` non-interactive mode, a per-session change ledger rendered by `/changes`, and resumable JSONL sessions. v1 is deliberately small: canonical-mode line input, no screen management, confirms on `write`/`bash` as the whole trust story, one config file with env overrides, and a provider seam that later native wires slot into without loop rework.

## User Stories

1. As a user, I want to run `og` and get a `og>` prompt in my terminal, so that I can start an agentic session in the same terminal I already work in.
2. As a user, I want to type a line at the `og>` prompt and get a complete agent turn back, so that I can delegate work to the agent without leaving the shell.
3. As a user, I want the agent's reply text to print live as it streams, so that I can see the turn progressing and interrupt early if it's going the wrong way.
4. As a user, I want to see nothing printed for the model's reasoning/inner-monologue tokens, so that the display stays clean and focused on output.
5. As a user, I want to press Ctrl+C while the agent is mid-turn to cancel the turn and return to the prompt, so that a runaway loop costs me nothing and leaves no partial garbage in the conversation.
6. As a user, I want to press Ctrl+C at the idle prompt to quit, so that leaving the REPL is a single instinctive gesture.
7. As a user, I want a line-editor-free canonical-mode REPL that uses the terminal's native line editing, so that I can paste and edit input without og imposing its own editing machinery.
8. As a user, I want the agent to be able to read files from the working directory, so that it can answer questions about code.
9. As a user, I want `read` to return a bounded head of long files (2000 lines / 50KB) with a pointer to continue, so that a huge file never floods the model context.
10. As a user, I want `read` on a directory to list its immediate children (dirs marked with a trailing `/`), so that I can discover a directory's shape without a separate `ls` tool.
11. As a user, I want `read` on a binary or non-UTF-8 file to return an error that points at shell inspection (`xxd`/`file`/`head -c`), so that binary content is never dumped into context.
12. As a user, I want the agent to be able to write whole files, so that it can scaffold new code from scratch.
13. As a user, I want `write` to a new file to proceed without confirmation, so that scaffolding work isn't cluttered by prompts.
14. As a user, I want `write` to an existing file to confirm before overwriting, so that my existing work is never silently clobbered.
15. As a user, I want the agent to be able to make surgical single-pair edit calls, so that it can change one thing at a time in a file.
16. As a user, I want `edit` to reject an `oldText` that isn't found, so that a stale edit never silently no-ops.
17. As a user, I want `edit` to reject an `oldText` that matches more than once, so that an ambiguous edit can't hit the wrong spot.
18. As a user, I want `edit` to be exact and whitespace-sensitive without prompting, so that edits are deterministic and scriptable.
19. As a user, I want the agent to be able to run shell commands, so that it can compile, test, and explore beyond the file tools.
20. As a user, I want every `bash` command to be confirmed before it runs, so that I retain a veto over anything executed on my machine.
21. As a user, I want `bash` to run in the session working directory with merged output, so that relative paths behave the same as in my own shell.
22. As a user, I want a hung command to be killed at a 120s default timeout (overridable per call), so that a runaway command can't block the session forever.
23. As a user, I want `bash` output truncated at the shared cap with the full copy spilled to a temp file whose path is returned, so that long output stays inspectable without flooding context.
24. As a user, I want a command that exits non-zero to return an error result carrying its output, so that the model learns a command failed and can adapt.
25. As a user, I want every tool result and every failure to come back in one uniform `Error: ...` shape, so that no exception aborts the loop and the model always gets something to work with.
26. As a user, I want tool execution to be strictly serial (one call per cycle), so that the turn stays predictable and easy to follow.
27. As a user, I want each tool run framed by a `── <tool> <args> ──` header and its result, so that I can see exactly what the agent did and what came back.
28. As a user, I want the conversation to persist as a resumable JSONL session, so that nothing I do in a turn is lost.
29. As a user, I want to start a fresh session with `/new`, so that I can change context without quitting.
30. As a user, I want `/changes` to list every agent cycle that changed files, newest first, with id, byte delta, timestamp, and touched files, so that I can audit exactly what the agent did.
31. As a user, I want `/changes <id>` to print the stored unified diffs of a cycle's batch, so that I can review the actual change before accepting it.
32. As a user, I want `/changes` on a session with no changes to print `no changes`, so that an empty audit is unambiguous.
33. As a user, I want `/changes <id>` with an unknown id to say `no such change 9`, so that typos are reported clearly.
34. As a user, I want `write`/`edit` changes to be captured as one ledger batch per agent-loop cycle, collapsing multiple edits to one file into a single diff, so that the ledger is a readable story of cycles, not a firehose of tool calls.
35. As a user, I want the ledger to store only diff text — never whole file contents — so that sessions stay small on disk.
36. As a user, I want failed tool calls (rejected write, failed edit) to leave no ledger entry, so that the ledger records only what actually changed.
37. As a user, I want `bash` mutations to be absent from the ledger, so that the ledger reflects only the agent's deliberate file tools (and bash already ran past my confirm).
38. As a user, I want `/model` with no argument to list the full model catalog, so that I can see what's available on the provider.
39. As a user, I want `/model <id>` to switch the session's model, so that I can change models mid-session.
40. As a user, I want `/model <unknown>` to fail with `og: no such model 'x'` and leave the current model untouched, so that a typo can't silently change my session.
41. As a user, I want `/help` to show the slash surface, so that I can recall commands without leaving the REPL.
42. As a user, I want `/quit` to exit, so that leaving is explicit when I prefer it over Ctrl+C.
43. As a user, I want an unknown command to print `og: unknown command 'x' — try /help`, so that I'm told the surface rather than left guessing.
44. As a user, I want blank input at the prompt to silently re-prompt, so that accidental Enter presses don't no-op the model.
45. As a user, I want confirm prompts in the `og: overwrite <path>? [y/N]` / `og: run: <command>? [y/N]` shape, so that I know exactly what a `y` agrees to.
46. As a user, I want only `y`/`yes` to accept a confirm and anything else to decline, so that the safe default is always to refuse.
47. As a user, I want a declined confirm to feed `Error: <tool> denied by user` back to the model, so that the agent keeps going and adapts instead of aborting.
48. As a user, I want to run a headless prompt with `-p "prompt"`, so that I can drive the agent from scripts, cron, and CI.
49. As a user, I want `-p ""` with piped stdin to read the prompt from stdin, so that long prompts can be piped in without command-line length limits.
50. As a user, I want `-p` with an empty prompt (and no piped stdin) to exit with a usage error, so that a useless invocation fails loudly.
51. As a user, I want `-p` output to go to stdout only, so that I can capture the answer into a file or variable cleanly.
52. As a user, I want `-p` tool framing to go to stderr, so that a headless run stays debuggable without polluting the piped answer.
53. As a user, I want `-p` to auto-deny confirms (no `--yes`), so that a headless run can't clobber my files, and the agent still gets the `denied by user` signal and adapts.
54. As a user, I want a `-p` run to persist as a one-turn session with its transcript and change ledger, so that I can audit headless changes with `/changes` in a later interactive session.
55. As a user, I want `-p` to print the session id to stderr on completion, so that I know where the headless run's record lives.
56. As a user, I want `-p` to exit `0` on success, `1` on a failed run, `2` when interrupted, and `3` on usage errors, so that my scripts can branch on the outcome.
57. As a user, I want `-p` to cancel on Ctrl+C the same way the REPL cancels a turn, so that an interrupt is never misreported as a run failure.
58. As a user, I want `-p` to take its model from config or `OG_MODEL` only, so that the headless surface stays minimal.
59. As a user, I want the agent instruction to always include a built-in default prompt, so that the harness has an identity even with no config at all.
60. As a user, I want an explicit `instruction_file` config path to load as instruction, so that I can set my own standing rules for the agent.
61. As a user, I want the `AGENTS.md` in the working directory to be read automatically as instruction, so that per-project agent rules work out of the box.
62. As a user, I want instruction sources to append (default + config file + `AGENTS.md`) rather than replace, so that no source silently drops another.
63. As a user, I want `AGENTS.md` discovery to be cwd-only, so that parent-directory layering stays out of v1.
64. As a user, I want config in `~/.config/og/config.toml` (XDG-aware), so that my settings live in one canonical place.
65. As a user, I want env vars (`OG_MODEL`, `OG_BASE_URL`, `OG_API_KEY_ENV`, `OG_INSTRUCTION_FILE`, `OG_SESSION_DIR`, `OG_BASH_TIMEOUT`) to beat the config file, so that I can override per-invocation without editing files.
66. As a user, I want the API key to live only in an env var (named by `api_key_env`, default `OPENCODE_API_KEY`), so that my key never ends up in a config file.
67. As a user, I want malformed TOML and unknown config keys to fail fast at startup, so that a typo like `bas_url` is caught rather than silently ignored.
68. As a user, I want to disable a tool via the `[tools]` table, so that I can narrow the agent's capabilities.
69. As a user, I want a stale call to a disabled tool to return `Error: tool 'bash' is disabled`, so that the loop keeps going with a clear message.
70. As a user, I want the default model to be `big-pickle`, so that the out-of-box experience rides a verified free tool-use model.
71. As a user, I want sessions to be stored under the session dir with the transcript and the `.changes.jsonl` ledger as siblings, so that both are easy to find and back up.
72. As a user, I want a provider outage or auth failure to print a clear `Error: ...` rather than a stack trace, so that I can fix the cause quickly.
73. As a user, I want the agent loop to retry without tools when the provider rejects the tools array (`invalid_request`), so that free models that don't support tool calling still work.
74. As a user, I want a mid-turn network failure to drop the partial turn and return to the prompt, so that the conversation never carries half a reply.
75. As a user, I want tool-call arguments to be JSON-validated before execution, so that a malformed call from the model can't crash the harness.
76. As a user, I want the assistant's tool-call turn echoed back verbatim to the provider, so that the wire conversation stays consistent and the model can continue correctly.
77. As a user, I want the `/model` catalog to be reachable unauthenticated, so that I can browse models without setting up a key first.
78. As a user, I want each tool's parameters described to the model via JSON schema, so that the model calls tools with the right shape.
79. As a user, I want usage (token counts) treated as best-effort and never required, so that a provider that omits it doesn't break the loop.
80. As a user, I want a session with no startup banner beyond at most one line (model id + cwd), so that the REPL stays quiet and purposeful.
81. As a user, I want a truncation-spill note to tell me where the full output of a long tool result was saved, so that nothing is lost when context caps apply.
82. As a user, I want sessions and ledgers to keep working even if the session directory doesn't exist yet, so that a fresh install runs without manual setup.
83. As a user, I want the whole harness to run on the Go standard library plus a single zero-dep TOML parser, so that building it is trivial and the binary stays small.

## Implementation Decisions

### Module shape

The harness is a Go 1.24+ module, std-lib-first, with one runtime dependency: `BurntSushi/toml` (itself zero-dep) for the config file. No code exists yet — the module structure is established by this spec.

### Seam: the `llm` package (`Client` interface)

The single architectural seam (ticket 06). The agent loop depends on one small interface; v1 ships exactly one wire implementation (OpenAI-compatible chat/completions); later native wires (OpenAI responses, Anthropic messages, Google generateContent) are more implementations of the same interface with no loop rework.

- Consumption is a **pull iterator**: `Stream(ctx, req) (iter.Seq[Event], error)`. Open failures (auth, 404, network-down) surface as the returned error; mid-stream failures surface as a terminal `Error` event. The loop `break`s to stop early (Ctrl+C).
- Event model normalizes and accumulates: `text` delta, `tool_call` (complete — id, name, args JSON string; emitted at finish, not streamed partially), `finish` (canonical reason `stop`/`tool_calls`/`length`/`other`), `usage` (best-effort). Partial args unsurfaced; reasoning dropped; no raw provider payload rides on events.
- Error surface: `ProviderError{ Kind, Message, StatusCode }`, `Kind` ∈ `auth` | `rate_limit` | `invalid_request` | `network` | `timeout` | `other`. `invalid_request` drives the no-tools fallback.
- Canonical conversation type: `Message{ Role, Content, ToolCalls, ToolCallID }`; the adapter maps per protocol. `Content` is a plain string (multi-part later). Verbatim assistant-echo happens at canonical level.
- `ListModels(ctx) ([]Model, error)` on the `Client`, normalized to `{ ID }`, drives `/model`.

### Wire protocol (ticket 01)

v1 targets OpenCode Zen over OpenAI chat/completions: `POST https://opencode.ai/zen/v1/chat/completions`, Bearer auth, key from `OPENCODE_API_KEY`. Model catalog at `GET https://opencode.ai/zen/v1/models` (unauthenticated, OpenAI `ListModels` shape). SSE: `data:` chunk lines + `data: [DONE]`; text via concatenating `delta.content`; tool-call args via index-keyed concatenation of `delta.tool_calls[i].function.arguments`; `finish_reason` on the final chunk; usage only when `stream_options.include_usage: true` is honored — treat as best-effort.

Two resilience requirements from the research's open risks: a **no-tools fallback** (free model rejects `tools` with `invalid_request` → retry the turn without tools) and tolerance of missing usage. Any OpenAI-compatible base URL is a valid provider (`OG_BASE_URL`).

### Agent instruction sources (ticket 02)

Three append-only sources, no full-prompt replacement, concatenated in order: (1) built-in default prompt (always present); (2) config `instruction_file` path (unrestricted — may point outside the working directory); (3) auto-read cwd `AGENTS.md` (cwd only, no parent walk). Config path beats `AGENTS.md` in load order; both present = both load. Missing instruction file → clear startup error.

### Tools (ticket 03)

All paths resolve relative to the session cwd; absolute and `~` allowed; no sandbox in v1 (hard sandbox deferred — see Out of Scope). The `tools` array is sent with `tool_choice: "auto"` and `parallel_tool_calls: false` (strictly serial). Tool result shape is uniform: `Error: <human-readable reason>` on failure; no exception aborts; failures flow back to the model.

- **read**: `{ path, offset?, limit? }` — `offset` 1-indexed line, `limit` max lines. Head-truncate at 2000 lines / 50KB (shared cap), append `[Showing lines X-Y of N. Use offset=Z to continue.]`. Binary/NUL/invalid UTF-8 → error pointing at `bash` (`xxd`/`file`/`head -c`). Directory → immediate children, sorted, one per line, dirs end `/`, `limit` honored. No fifth `ls` tool.
- **write**: `{ path, content }` — whole-file, auto-creates parent dirs, overwrites. Confirm only when the target already exists; new files proceed. 1MB content cap, rejected with error. Returns bytes written.
- **edit**: `{ path, oldText, newText }`, single pair per call. Exact whitespace-sensitive match; reject if not found; reject if ambiguous. Preserves the file's existing line endings; strips BOM before matching. No confirm (the write confirm is the guard). Every edit lands in the change ledger.
- **bash**: `{ command, timeout? }` — runs in the session cwd, stdout+stderr merged. Confirm every command. Default timeout 120s (configurable), per-call override (seconds); on timeout kills the process tree, returns `Command timed out after Ns` + partial output. Tail-truncate to 2000 lines / 50KB; full output spilled to a session temp file whose path is included when truncated. Non-zero exit → error result carrying output + `Command exited with code N`.

### Change ledger (ticket 04)

A batch-of-diffs record in the git `log`/`show` shape — no git/svn implemented. One batch per agent-loop cycle; on a cycle's first write/edit of a file, snapshot pre-mutation content; at cycle close, diff live vs snapshot and store only the diff text. Multiple edits to one file per cycle collapse into one diff; outside mutations are captured honestly in the diff. Only executed `write`/`edit` land diffs — failures and bash never do. A cycle that mutates nothing produces no batch.

Storage: append-only `<session-dir>/<session-id>.changes.jsonl`, one JSON batch per line — `seq` (change id, monotonic per session), `ts` (RFC3339 batch close), `session`, `tool_call_ids`, `files[]` with `path`, `ops`, `diff` (unified; `[binary]` marker for non-text; 2000-line/50KB truncate with spill to `<session-dir>/<seq>-<file>.diff` and path recorded), `delta` (+/- line counts). Never rewritten, replayed, or compacted in v1.

`/changes` lists newest-first (id, byte delta, ts, files; empty → `no changes`). `/changes <id>` prints the batch's stored diffs per file in first-touch order (binary marker, truncation note + spill path; unknown id → `no such change 9`).

### REPL (ticket 05)

Plain canonical-mode line reader over stdin — no raw mode, no multiline, no line editor (those belong to the full TUI). Text deltas print live (only `delta.content`); reasoning ignored. Ctrl+C: idle prompt → exit; in-flight turn → cancel (abort stream, print `(interrupted)`, drop the partial turn, return to prompt); during a confirm → decline. Slash surface exactly `/changes`, `/new`, `/model`, `/help`, `/quit`; unknown → `og: unknown command 'x' — try /help`. `/new` = fresh session, no confirm. `/model` mirrors `/changes` — no arg lists the catalog, arg validated against it, no-switch on failure. Confirms: `og: overwrite <path>? [y/N]` / `og: run: <command>? [y/N]`, only `y`/`yes` accepts, default no; declined → `Error: <tool> denied by user` fed back, loop continues. Tool runs framed by `── <tool> <args> ──` header + result truncated at the shared caps. Prompt glyph `og> `; no startup banner beyond at most one line (model id + cwd); blank → silent re-prompt; no per-turn usage line.

### Config (ticket 07)

TOML at `os.UserConfigDir()/og/config.toml`, no `--config` flag; missing file = pure defaults. Precedence **defaults < file < env** with six env overrides (`OG_MODEL`, `OG_BASE_URL`, `OG_API_KEY_ENV`, `OG_INSTRUCTION_FILE`, `OG_SESSION_DIR`, `OG_BASH_TIMEOUT`). API key never in the file — always read from the env var named by `api_key_env` (default `OPENCODE_API_KEY`). Session dir default `<UserConfigDir>/og/sessions` (hosts transcript + ledger as siblings). Six scalars — `model`=`big-pickle`, `base_url`=`https://opencode.ai/zen/v1`, `api_key_env`=`OPENCODE_API_KEY`, `instruction_file` (unset), `session_dir`, `bash_timeout`=`120` — plus `[tools]` four booleans (default true; disabled = omitted from `tools`, stale call → `Error: tool 'x' is disabled`). Malformed TOML + unknown keys → fail fast (`DisallowUnknownFields`). `/model` never writes the file.

### Non-interactive mode (ticket 08)

`-p <string>`; empty `-p` with piped stdin (stdin not a TTY) reads all of stdin; no positional prompt; empty prompt either way → usage error (exit 3). Full agent loop, tools enabled, confirms **auto-deny** (no `--yes` v1; denied → `Error: <tool> denied by user` fed back; read/edit/new-write work headless, bash/overwrites don't; deny ≠ failure). Answer text → stdout only; tool framing → stderr; no prompts, no confirm text, no usage line. Persists a one-turn session (transcript + ledger in the session dir), session id → stderr on completion. Exit codes `0` success · `1` run failed · `2` interrupted · `3` usage. Model from config only (no `-m` flag).

## Testing Decisions

### The seam

Two seams, by design:

1. **Primary — black-box binary seam**: tests drive the **compiled `og` binary as a subprocess** (`exec.Command`) against an **in-process fake OpenAI-compatible provider** (Go `httptest` server on a loopback `base_url`, emitting scripted SSE chunk sequences — text deltas, index-keyed tool-call fragments, finish reasons, the `include_usage` chunk). The fake provider is the one place the wire is fabricated; everything else is the real harness. Assert on observable behavior only: stdout, stderr, exit codes, files written, session transcripts, and the `.changes.jsonl` ledger. This single seam covers REPL and `-p` behavior (05, 08), confirm auto-deny and deny-flow (03, 08), ledger capture and `/changes` output (04), config/env precedence and fail-fast validation (07), instruction-source concatenation (02), the no-tools fallback and usage tolerance (01, 06), and the wire shape itself. It also gives the freebie that every test exercises the real config, instruction, and session machinery. Ctrl+C semantics are tested at this seam by sending SIGINT to the subprocess.

2. **Secondary — unit tests for pure helpers**: direct tests for internal functions that are hard to reach through the binary boundary and have no side effects worth faking: unified-diff text generation, the 2000-line/50KB truncation logic and spill behavior, `edit`'s exact-match/ambiguity rules, config parsing and env-override resolution, and the SSE accumulation state machine (index-keyed tool-call assembly, usage-chunk detection). These are tests of behavior, not implementation — each helper is tested through its public signature.

### What makes a good test

Only external behavior: a test asserts what the user observes (prompt text, printed frames, exit codes, files, ledger lines) or what a pure function returns — never which functions the harness called internally, and never provider payloads beyond what the fake server scripted. The fake provider and the subprocess stay small and scripted; each test is a scenario, not a session log.

### Prior art

There is no existing test code in the repo (planning-only so far). The patterns to follow are the language defaults for the reference implementation: Go `testing` + `httptest` + `os/exec` subprocess driving, and `testdata/` fixtures for instruction files, config TOML, and golden diff/ledger shapes. Session-dir and ledger tests follow the fixture-in-`testdata`-and-assert-jsonl pattern that JSONL persistence naturally supports.

## Out of Scope

- **The full TUI**: raw-mode line editor, multiline composition, alt-screen layout, and whether it absorbs the REPL. v1 is canonical-mode; the TUI is a later phase with its own presentation decisions.
- **Changes-view presentation**: where the stored-diff drill-down surfaces beyond the inline `/changes <id>` output (alt-screen list, `$EDITOR`). The diff source is settled in v1; only the presentation seat is open.
- **Multi-provider beyond the seam**: native wires for OpenAI responses, Anthropic messages, Google generateContent, model routing, cycling. Only the seam (06) is in v1.
- **`-m`/`--model` flag, `--config` flag, `--yes` flag**: v1 deliberately has none; model comes from config/`OG_MODEL`, config lives in one canonical location, and headless confirms are auto-deny.
- **Hard sandbox**: bash containment + file jailing via a CLI flag. Needs chroot/bwrap/sandbox-exec/job-objects (not std-lib, platform-specific); a partial jail leaks through bash. v1 trust is confirms only. Considered and deferred in ticket 03.
- **MCP support**: pi deliberately omits it; not requested.
- **Sub-agents / plan mode / to-do lists**: opencode-style features; pi omits them.
- **Plugin/extension system**: pi packages are out; "very bare bones to start" is the point.
- **Permission popups / granular trust dialogs**: v1 confirmations on `write`/`bash` are the whole trust story.
- **Parent-directory `AGENTS.md` walk and global instruction file**: pi's layering is a later phase; v1 is cwd-only plus the explicit config path.
- **Multi-part content** (images, files in messages): v1 `Content` is a plain string.
- **Config beyond the v1 keys**: per-tool prompts, themes, keybindings, model presets, XDG data-dir correction for sessions.

## Further Notes

- **Vocabulary**: this spec's terms are the glossary in `CONTEXT.md` — harness, agent loop, turn, tool, session, REPL, agent instruction, instruction file, change ledger, change batch, changes view, provider, wire protocol. Use those, not "system prompt", "function", "thread", "diffset".
- **Reference shape**: pi (earendil-works/pi) is the minimality reference; og deliberately diverges on raw-mode TUI (canonical mode in v1), confirms (write/bash), and instruction sources (no parent walk, no global file).
- **Provider risks carried into the spec**: Zen streaming fidelity unverified live (no key exercised), undocumented rate limits (429s, backoff headers unknown), and free-model tool-call support unverified — hence the no-tools fallback and best-effort usage. The fake-provider test seam doubles as the harness's resilience proof against these.
- **Free models are temporary**: OpenCode Zen's `*-free` models are "available for a limited time"; `big-pickle` is the current default and a free "stealth" model. The config default is a per-install scalar, not a promise.
- **Decision record**: all eight decision tickets live at `.scratch/og-harness/issues/01..08` with the research at `.scratch/og-harness/research/01-provider-wire.md`. The map at `.scratch/og-harness/map.md` indexes them. This spec is the hands-off handoff.
