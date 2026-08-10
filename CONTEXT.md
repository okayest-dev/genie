# og — Agent Harness

The `og` project: a minimal, std-lib-first Go terminal agent harness in the pi mould — a REPL that runs an agentic loop against an OpenAI-compatible provider.

## Language

**Harness**:
The `og` CLI application itself — the shell that runs the agent loop and presents it in the terminal.
_Avoid_: agent (alone), tool

**Agent loop**:
The cycle in which the model produces text and/or tool calls, the harness executes the calls, and the results are fed back — repeating until the model stops calling tools.
_Avoid_: chat loop, run

**Tool**:
A named capability the model can invoke — `read`, `write`, `edit`, `bash` — defined by a JSON schema and executed by the harness.
_Avoid_: function, command

**Session**:
One conversation thread, persisted as JSONL, resumable.
_Avoid_: thread, chat

**Agent instruction**:
The fixed instruction block sent to the model on every turn of the agent loop — the harness identity and behaviour rules, distinct from user turns and tool results.
_Avoid_: system prompt, system message

**Instruction file**:
An on-disk source of agent instruction — the `AGENTS.md` in the working directory or the file named by the config. Auto-read `AGENTS.md` is cwd-only; an explicitly configured path may point anywhere (including outside the working directory).
_Avoid_: context file, context

**Change ledger**:
The per-session record of file-mutating tool actions (write, edit), each entry linking a change to the file it touched; rendered by the `/changes` command.
_Avoid_: edit log, transaction log

**Changes view**:
A later-phase presentation of the change ledger that links each change to its actual file (open in editor, diff, alt-screen list). Its concrete form is still undecided.

**Provider**:
A configured model endpoint the harness talks to over a wire protocol.
_Avoid_: model provider, backend

**Wire protocol**:
The HTTP request/response format between harness and provider. v1: OpenAI chat/completions. Later: Anthropic messages, OpenAI responses, Google generateContent.
_Avoid_: API, transport format
