# Genie — Agent Harness

The `genie` project: a minimal, std-lib-first Go terminal agent harness in the pi mould — a REPL that runs an agentic loop against an OpenAI-compatible provider.

## Language

**Harness**:
The `genie` CLI application itself — the shell that runs the agent loop and presents it in the terminal.
_Avoid_: agent (alone), tool

**Agent loop**:
The cycle in which the model produces text and/or tool calls, the harness executes the calls, and the results are fed back — repeating until the model stops calling tools.
_Avoid_: chat loop, run

**Turn**:
One full exchange in a session — from the user submitting a line at the prompt until the agent loop returns control (the model stops calling tools). A session is a sequence of turns.
_Avoid_: interaction, cycle

**Tool**:
A named capability the model can invoke — `read`, `write`, `edit`, `code` (and `bash` behind a feature flag) — defined by a JSON schema and executed by the harness.
_Avoid_: function, command

**Session**:
One conversation thread, persisted as JSONL, resumable.
_Avoid_: thread, chat

**REPL**:
The interactive loop that reads a user line at the `genie>` prompt, runs a turn (or a slash command), and repeats — the canonical-mode, std-lib front end of v1, distinct from the `-p` non-interactive mode.
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

**Counter**:
The token-counting seam behind an internal interface (`internal/tokens`) — omnitoken for models it has an adapter for, a len/4 heuristic approximation for the rest, so the harness can always count a conversation without failing.
_Avoid_: tokenizer, counter library

**Context window**:
The authoritative per-model token limit a conversation is budgeted against — resolved from a config override first, then from provider data (the plugin that introduces the model reports it, or the wire's model-info probe), never from a curated guess; unknown stays zero.
_Avoid_: max tokens, model size

**Budget**:
The portion of the context window a conversation may consume before the harness intervenes — an absolute `budget_tokens` or a percentage of the window (`budget_percent`, default 75%), keeping headroom so a request cannot silently blow the window.
_Avoid_: limit, quota

**Context layer**:
A bucket within a conversation that context management retains, evicts, or condenses independently — `instruction` (never evicted), `durable-intent` (user + assistant turns; windowed, compactable), or `tool-output` (condensable, net-drop opt-in). Layers are assigned by message role, not by token sensitivity.
_Avoid_: context bucket, history slice

**Context map**:
The derived, layered index over a session's JSONL — messages keyed by session-line index with a secondary tool-call-id lookup, carrying each message's cached token count. Rebuilt from the transcript on load; not the canonical store. Distinct from the session, which remains the append-only source of truth.
_Avoid_: context store, message index

**Spine**:
The fixed cheap prefix of every request — the agent instruction plus the current turn's messages — unconditionally present; prior turns are added by per-layer retention rather than shipped wholesale.
_Avoid_: header, front matter

**Condensation**:
The request-time shrinking of tool-output layer messages (e.g. abbreviated large results) before they reach the provider. A projection: the full result stays in the transcript; the condensed form is per-request, keyed by tool-call-id. Distinct from compaction.
_Avoid_: truncation, shortening

**Compaction**:
The synchronous eviction of the oldest durable-intent turns into a persisted summary entry when the budget is hit — a JSONL metadata marker, not a transcript rewrite. Distinct from condensation.
_Avoid_: summarisation (alone), compression

**Agent**:
A named configuration the harness can run a loop with — an `AgentDef` from an `agents/*.toml` file, resolved to a model, instruction, and tool set. The default agent or a named one (`orchestrator`, `feature-speccing`). Distinct from the "harness".
_Avoid_: (bare) tool

**Subagent**:
A delegated child agent — an agent instance spawned by another agent (its parent) to work a task. Runs its own loop and its own session, streams live output, and folds its result back into the parent.
_Avoid_: child agent (alone), worker

**Delegation**:
An agent handing a task to a subagent. The subagent works — possibly surfacing output live to the user — and its result folds back into the parent's loop. Start-of-effort mechanism is a tool call behind a delegation seam that can migrate to a real orchestrator.
_Avoid_: spawn (alone), fork

**Delegated task name**:
The purpose-derived label an agent assigns to a subagent instance (e.g. `spec-auth-flow`), used to identify and observe it in monitoring. Chosen by the delegating agent, not by position (`subagent-1`).
_Avoid_: subagent name

**Foreground**:
The agent the user is currently directly addressing in a session — the primary conversational party. Only one agent is in the foreground at a time.
_Avoid_: active agent, current agent

**Handoff**:
An agent yielding the foreground to another named agent, which becomes primary and addresses the user directly, then hands back. Driven by a tool; a stack remembers the return path. Distinct from delegation (delegation stays behind the scenes; handoff puts the target in the foreground).
_Avoid_: switch (alone), yield (alone)

## Code tool

**Code tool**:
A sandboxed code-execution tool the model invokes by emitting TypeScript/JavaScript snippets. Executes in a Deno subprocess with granular permissions (filesystem per-directory, network per-host, subprocess per-executable). Replaces `bash` as the default execution tool; `bash` is retained behind a feature flag.
_Avoid_: code execution tool, sandbox tool

**Base policy**:
The default permission envelope for the code tool, defined in the config file (`[permissions]` section). Specifies which filesystem paths, network hosts, subprocesses, and env vars the code model can access without escalation. Intersected with the model's per-call `permissions` array.
_Avoid_: default permissions, permission defaults

**Effective policy**:
The running union of base policy plus all granted escalations (permanent from config, session-scoped from in-memory, single-call from one-shot set). The code tool's permission intersection operates against this envelope, not the base policy alone.
_Avoid_: active policy, current permissions

**Permission escalation**:
The process by which the model requests permissions beyond the current effective policy. The model calls `request_permission`; the user decides the tier (single call, session, permanent). The harness updates the effective policy and, for permanent tier, persists the escalation to the config file.
_Avoid_: permission grant, scope expansion

**Escalation tier**:
The lifetime a user assigns to a granted escalation: single code call (discarded after one execution), remainder of session (in-memory, lost on session end), or permanent (persisted to config file, loaded at startup). The model does not choose the tier — the user decides interactively.
_Avoid_: permission scope, grant duration

## Plugin auth

**Credential store**:
The plugin-owned file a wire plugin with its own authentication persists durable credentials to — host-keyed JSON under the plugin's XDG data dir, owner-only permissions, atomic writes. Owned by the plugin; never the shared Copilot `apps.json`.
_Avoid_: auth store, apps.json, token cache

**Credential record**:
One host's entry in a credential store — the durable login (the GitHub OAuth token) plus its metadata: host, user, expiry and refresh token when issued, and the provider's API base. Never carries the derived token.
_Avoid_: credential (alone), stored token

**Derived token**:
The short-lived provider token minted from a credential record by a token exchange (the ~25 minute Copilot JWT), held in memory by the plugin only.
_Avoid_: session token, Copilot JWT, cached token

**Token exchange**:
The unattended call that turns a credential record into a fresh derived token for the provider's host. A plugin-owned operation, distinct from login.
_Avoid_: refresh (alone), mint

**Device-flow login**:
The attended, interactive login that creates or renews a credential record — displays a code and verification URL, polls until the user authorizes, and persists the record. Distinct from refresh.
_Avoid_: auth login, oauth login

**Refresh**:
Unattended renewal of a credential — re-exchanging a derived token, or rotating a refresh token back into the credential record. Distinct from login.
_Avoid_: re-login, re-auth

## Plugin command

**Plugin command**:
A user-typed slash command a wire plugin registers over the `commands` capability and the REPL invokes as `/<plugin> <command>` — e.g. `copilot`'s `auth` reached as `/copilot auth login`. The plugin advertises the command (`name`, `description`, `usage`) via `commands/list`; the REPL passes the raw remaining arguments to it through `commands/run`. Distinct from a Tool, which the model invokes, not the user.
_Avoid_: plugin slash command, plugin tool, registered command
