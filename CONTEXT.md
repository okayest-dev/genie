# og — Agent Harness

The `og` project: a minimal, std-lib-first Go terminal agent harness in the pi mould — a REPL that runs an agentic loop against an OpenAI-compatible provider.

## Language

**Harness**:
The `og` CLI application itself — the shell that runs the agent loop and presents it in the terminal.
_Avoid_: agent (alone), tool

**Agent loop**:
The cycle in which the model produces text and/or tool calls, the harness executes the calls, and the results are fed back — repeating until the model stops calling tools.
_Avoid_: chat loop, run

**Turn**:
One full exchange in a session — from the user submitting a line at the prompt until the agent loop returns control (the model stops calling tools). A session is a sequence of turns.
_Avoid_: interaction, cycle

**Tool**:
A named capability the model can invoke — `read`, `write`, `edit`, `bash` — defined by a JSON schema and executed by the harness.
_Avoid_: function, command

**Session**:
One conversation thread, persisted as JSONL, resumable.
_Avoid_: thread, chat

**REPL**:
The interactive loop that reads a user line at the `og>` prompt, runs a turn (or a slash command), and repeats — the canonical-mode, std-lib front end of v1, distinct from the `-p` non-interactive mode.
_Avoid_: shell, TUI

**Agent instruction**:
The fixed instruction block sent to the model on every turn of the agent loop — the harness identity and behaviour rules, distinct from user turns and tool results.
_Avoid_: system prompt, system message

**Instruction file**:
An on-disk source of agent instruction — the `AGENTS.md` in the working directory or the file named by the config. Auto-read `AGENTS.md` is cwd-only; an explicitly configured path may point anywhere (including outside the working directory).
_Avoid_: context file, context

**Change ledger**:
The per-session record of file changes, captured as batches of diffs — one batch per agent-loop cycle, each batch carrying the unified diffs of the files it touched; rendered by the `/changes` command.
_Avoid_: edit log, transaction log

**Change batch**:
One ledger entry — all the file changes a single agent-loop cycle made, collapsed into per-file diffs. The unit the `/changes` command lists; drilling into one (via its change id) shows its diffs.
_Avoid_: commit, changeset, diffset

**Changes view**:
A later-phase presentation of the change ledger that links each change batch to its actual file (open in editor, alt-screen list). The v1 `/changes <id>` drill-down already renders a batch's stored diffs inline; only the presentation seat is open.
_Avoid_: diff view, edit log viewer

**Provider**:
A configured model endpoint the harness talks to over a wire protocol.
_Avoid_: model provider, backend

**Wire protocol**:
The HTTP request/response format between harness and provider. v1: OpenAI chat/completions. Later: Anthropic messages, OpenAI responses, Google generateContent.
_Avoid_: API, transport format

**Wire registry**:
The mapping from a model ID (or config override) to the correct wire protocol implementation. Auto-detects from model ID prefix when no explicit `wire` config is set.
_Avoid_: provider selector, wire router

**Wire**:
A concrete implementation of `llm.Client` for a specific wire protocol — one package under `internal/llm/` (e.g. `openai/`, `anthropic/`, `responses/`, `google/`). Each wire handles request serialisation, SSE streaming, tool-call delta accumulation, and error mapping for its protocol.
_Avoid_: provider implementation, client
