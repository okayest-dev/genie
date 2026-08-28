# Research: Multi-Agent Delegation and Foreground Handoff Patterns

**Task**: Research-only. What other agent harnesses do for (a) subagent delegation and (b) foreground/primary-agent handoff, and what og should borrow or avoid.

**Scope**: opencode, Claude Code / Anthropic, Codex CLI / OpenAI Agents SDK, plus cross-cutting delegation observations. Sources cited per claim.

---

## 1. Subagent delegation patterns

### 1.1 Purpose-derived naming of subagent instances

Every harness that names a subagent *instance* derives the label from the task, never from a position counter.

**opencode** — the `task` tool requires a `description` parameter documented as *"A short (3-5 words) description of the task"*. That description becomes:
- the child session title: `title: params.description + " (@<next.name> subagent)"`, and
- the delegation's title in the parent transcript: `ctx.metadata({ title: params.description, ... })`.
A `task_id` is returned and can be passed back to *resume the same subagent session* instead of starting a fresh one.
Source: `packages/opencode/src/tool/task.ts` at https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/tool/task.ts — parameter schema and `sessions.create({... title: params.description ...})`.

**Claude Code** — splits the concern in two: a static *type name* (frontmatter `name`, e.g. `code-improver`) and a *description* (when to use it). Delegations render in the transcript as a tool-call row showing the type name plus a short task description, e.g. `code-improver(Suggest code improvements)`. Per-instance identity is a generated `agentId:` returned inside the Agent tool result text; it exists to resume, not for display.
Source: https://code.claude.com/docs/en/sub-agents (Quickstart step 3 transcript row; "Resume subagents" agentId) and https://code.claude.com/docs/en/agent-sdk/subagents (agentId trailer).

**Codex CLI** — the v2 multi-agent tools spawn a sub-agent *"with a `task_name` and `message`"*; `task_name` is a first-class parameter of `spawn_agent`. Custom agents additionally carry a `nickname_candidates` field — *"Pool of display names shown in the CLI's thread list."*
Sources: codex-rs tool handlers `multi_agents_v2/spawn.rs` via https://deepwiki.com/openai/codex/3.6-thread-management-and-multi-agent ; `nickname_candidates` per https://codex.danielvaughan.com/2026/03/26/codex-cli-subagents-toml-parallelism/.

**Anthropic research system** — subagents are given explicit task boundaries in the prompt (objective, output format, tools/sources, boundaries) to stop duplicated/overlapping work across parallel workers.
Source: https://www.anthropic.com/engineering/multi-agent-research-system ("Teach the orchestrator how to delegate").

> **Implication for og**: the `delegate` tool should take the purpose-derived task label as a required parameter (og's *delegated task name*, e.g. `spec-auth-flow`), and the harness should use that label for the child session title and the parent transcript record — exactly what opencode's `description` and Codex's `task_name` do. The *agent type* (which subagent definition runs) stays a separate parameter (`subagent_type` in both opencode and Claude Code).

### 1.2 Fresh vs inherited context

Consensus: subagents start with **fresh context**; they inherit a small, explicit whitelist, never the parent's conversation.

**Claude Code / Agent SDK** — a non-fork subagent's initial context is: its own system prompt, the delegation prompt string of the Agent tool call, the project `CLAUDE.md` hierarchy, and a git-status snapshot. It does *not* receive the parent's conversation history, tool results, system prompt, or output style. *"The only content you pass from parent to subagent is the Agent tool's prompt string, so include any file paths, error messages, or decisions the subagent needs directly in that prompt."* A "fork" is the explicit opt-in exception that inherits the whole conversation.
Sources: https://code.claude.com/docs/en/agent-sdk/subagents (What subagents inherit) and https://code.claude.com/docs/en/sub-agents (What loads at startup).

**opencode** — subagents run in a child session (`parentID: ctx.sessionID`) with its own context. The subagent's tool set is computed at spawn: a `todowrite`/`task` deny is injected if the subagent has no permission for those tools, so scope is *reduced* to what the definition allows, never *extended*. Model falls back to the parent's (`next.model ?? parent message model`) unless the subagent definition sets its own.
Source: https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/tool/task.ts.

**Codex CLI** — each subagent is its own thread with independent state; custom agent files act as config layers, inheriting session settings from the parent only where the file omits them (sandbox, MCP servers, skills).
Source: https://developers.openai.com/codex/subagents (Custom agents; Orchestration and thread controls).

### 1.3 Result fold-back

Delegation is a **bounded call that returns**: the parent blocks, receives a rendered result block, and continues. The result is always wrapped with a machine-readable marker and a resumption handle.

**opencode** — the task tool's output string is:
```
task_id: <child-session-id> (for resuming to continue this task if needed)

<task_result>
  ...last text part from the subagent...
</task_result>
```
Success wraps in `<task_result>`, failure in `<task_error>`; both carry the child session id. File edits etc. remain in the child's session and are not spliced into the parent.
Source: https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/tool/task.ts — `renderOutput()` and the `output:` that closes the tool call.

**Claude Code** — the subagent's final message is delivered as the Agent tool result; the parent reads it and usually summarizes. The result includes `agentId: <id>` for resuming via `SendMessage`. Subagent transcripts persist in separate files, independent of the main conversation. Since v2.1.210 the SDK scans the final message for instruction-shaped patterns (control-tag imitation, permission-config mentions, `Human:`/`Assistant:` turn-marker lines) to blunt prompt-injection via subagent output.
Sources: https://code.claude.com/docs/en/agent-sdk/subagents (Resume; output scanning note) and https://code.claude.com/docs/en/sub-agents (Resume subagents).

**Anthropic research system (pattern)** — avoid the "game of telephone": have subagents write large outputs (reports, code) to artifacts/filesystem and return only a lightweight reference to the coordinator. Cuts token overhead and fidelity loss of passing everything through one transcript.
Source: https://www.anthropic.com/engineering/multi-agent-research-system (Subagent output to a filesystem).

### 1.4 Fan-out / join

Parallelism is permissive but bounded by hard depth + concurrency (and sometimes spend) limits, because every subagent runs its own model and tool calls.

- **Claude Code**: subagents spawn via the Agent tool; default depth 3 layers (`CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH`), default 20 concurrent (`CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS`), optional spend cap (`max_budget_usd`). At the depth limit the Agent tool is withheld so the leaf *does its own work*. Beyond "a few delegated tasks per turn" the docs push orchestration into a `Workflow` tool (script-driven) rather than turn-by-turn nesting.
  Sources: https://code.claude.com/docs/en/sub-agents ("Let subagents spawn their own subagents", "Concurrent subagent limit") and https://code.claude.com/docs/en/agent-sdk/subagents (Cap subagent depth, concurrency, and spend).
- **opencode**: nesting is config-gated with `subagent_depth` (default 1 — subagents cannot nest by default); depth is computed by walking the session-parent chain. Subagent-to-subagent delegation was a long-requested, permission-gated feature (PR #7756 reworks), not the default.
  Sources: https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/tool/task.ts (depth check) and https://github.com/anomalyco/opencode/pull/7756.
- **Codex CLI**: `[agents] max_concurrent_threads_per_session` (legacy `max_threads`, default 6) and `max_depth` (default 1). A batch fan-out primitive exists: `spawn_agents_on_csv` runs one worker per CSV row and merges results into an output CSV, requiring each worker to call `report_agent_job_result` exactly once. Combined: *explicit wait semantics* — the parent waits until all requested results are available, then produces a consolidated response.
  Sources: https://codex.danielvaughan.com/2026/03/26/codex-cli-subagents-toml-parallelism/ and https://developers.openai.com/codex/subagents (Orchestration and thread controls) .
- **Anthropic research system**: parallel subagents with a lead that synthesizes; subagents run *asynchronously* (the lead doesn't block on each one), and the lead may re-spawn or refine on partial results.
  Source: https://www.anthropic.com/engineering/multi-agent-research-system.

> **Implication for og**: fan-out should let one turn delegate several named tasks; join = each `delegate` result folds back in the same loop. Bounds (max concurrent, max depth, default non-nested) matter because they are what every shipped harness converged on.

### 1.5 Live streaming of subagent output

Two distinct streaming behaviors: (a) stream the subagent's live output to the user, (b) don't poll — be *told* when a backgrounded subagent finishes.

- **opencode**: a foreground task runs the child prompt through the same streaming `prompt()` path (streams to the child session's live view) and blocks the parent turn until done. A background task (`background: true`) returns immediately with a `<task state="running">` block; when the job finishes, the harness *injects a synthetic message into the parent session* — `"Background task completed: <description>"` inside a `<task_result>` — so the model is informed on a later turn without polling. The prompt explicitly instructs the model: *"DO NOT sleep, poll for progress, ask the task for status..."*.
  Source: https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/tool/task.ts (`BACKGROUND_STARTED`, `injectBackgroundResult`).
- **Claude Code**: foreground subagents block and stream; permission prompts pass through. Background subagents run while the user keeps working; their permission prompts surface in the *main session* (with the subagent named), and their completion arrives as a notification a later turn — who asks about progress gets told it's still running. Background runs get a *narrower built-in tool set* than foreground runs (a curated list incl. Read/Grep/Glob/Bash/Edit/WebFetch/Monitor; everything else removed). Since v2.1.198 background is the default for model-spawned subagents; `run_in_background: false` is used when the result is needed before continuing.
  Sources: https://code.claude.com/docs/en/sub-agents ("Run subagents in foreground or background", "Available tools", "Background subagents... completion notification").
- **Codex CLI**: subagents stream on their own threads; in interactive mode, approval requests from *inactive* threads surface in the parent's terminal labeled with the source thread (`o` to open before approving).
  Source: https://developers.openai.com/codex/subagents (Approvals and sandbox controls).

---

## 2. Foreground / primary-agent handoff patterns

### 2.1 "Which agent is primary now"

Only opencode has an explicit primary/subagent role split with a stored "active agent"; the others treat "primary" as whoever the human talks to.

- **opencode**: agents have `mode: primary | subagent | all`. Primary agents are the ones *the user* interacts with directly, cycled with **Tab** / the `switch_agent` keybind; subagents cannot be selected as primary. The active primary agent is stored on the session; the selection never rewrites the agent already on an existing session, and an unselectable primary falls back to `build` then the first visible primary. **There is no model-issued "hand off the foreground" tool** — the foreground changes only by human action.
  Source: https://opencode.ai/docs/agents/ and https://opencode.ai/v2/docs/agents/
- **Claude Code / Codex / Anthropic**: no primary-handoff tool. Codex's `/agent` switches which *thread* you are viewing (`/subagents` switches among a session's subagents); Claude Code's foreground is the main conversation and subagents always stay behind it (an agent can be started as the main thread via `claude --agent`, but that's launch-time selection). In both, "which agent is primary" is purely human-driven.
  Sources: https://codex.danielvaughan.com/2026/03/28/agentic-pod-in-practice-multi-agent-roles/ (`/agent`) and https://code.claude.com/docs/en/agent-sdk/subagents (AgentDefinition `initialPrompt` runs as the main thread agent).

### 2.2 The handoff primitive (OpenAI Agents SDK)

The only widely-documented **first-class handoff object** is the OpenAI Agents SDK's `Handoff`:

- A handoff is a *tool exposed to the model*: `transfer_to_<agent_name>` (e.g. `transfer_to_refund_agent`), with default description `"Handoff to the <agent> agent to handle the request."` + the target's `handoff_description`. The model picks the handoff tool to route.
- The receiving agent takes over the **same conversation/run** and by default *sees the entire conversation history*; `input_filter` (or `nest_handoff_history`) can reshape what the receiver sees. Optional `input_type` lets the model attach structured metadata (reason, priority, summary) parsed and passed to `on_handoff`.
- **No automatic return-to-origin**: a handoff is an edge in a graph; control does not come back unless the receiving agent itself registers/returns a handoff. Return is modeled by explicit reverse edges (Semantic Kernel's handoff orchestration does exactly this: `StartWith(triage)` + caption reverse edges like *"Transfer to this agent if the issue is not status related"*).
- The docs prescribe a prompt prefix (`RECOMMENDED_PROMPT_PREFIX`) so the model knows handoffs exist and uses them; one handoff per destination; keep destinations narrow and descriptions short.
Sources: https://openai.github.io/openai-agents-python/handoffs/ and https://openai.github.io/openai-agents-python/ref/handoffs/ ; reverse-edge return pattern: https://learn.microsoft.com/en-us/semantic-kernel/frameworks/agent/agent-orchestration/handoff

### 2.3 Delegation vs handoff — the decision rule

OpenAI's orchestration guide is the sharpest statement:

| Pattern | Use when | What happens |
|---|---|---|
| Handoffs | A specialist should *take over the conversation* for that branch | Control moves to the specialist |
| Agents as tools | A manager should stay in control and call specialists as bounded capabilities | The manager keeps ownership of the reply |

- "Handoffs are better modeled as **routing** (the model decides where a conversation should live). Subagents are better modeled as **delegation** (the orchestrator breaks a task into pieces and merges results). Many real-world workflows need both."
- Both names coexist in one "Agent" abstraction in these SDKs: a tool-agent (`as_tool()`) returns its result to the caller; a handoff transfers ownership. og's terms already encode this: *delegation stays behind the scenes; handoff puts the target in the foreground*.
Sources: https://developers.openai.com/api/docs/guides/agents/orchestration and https://www.developersdigest.tech/blog/openai-agents-sdk-vs-claude-agent-sdk

### 2.4 Return-to-origin

- **OpenAI**: not automatic — you register reverse handoffs or let the receiving agent decide.
- **Semantic Kernel**: explicit reverse edges with per-edge captions the source model reads.
- **opencode** (session-hierarchy model): the "return" is *navigation*, not a tool: a child session has a parent link; keybinds `session_parent` (Up) return to the parent session, `session_child_first`/`session_child_cycle` descend or cycle siblings, and `s`/a dialog shows the whole session tree. This is the closest analogue to og's *"a stack remembers the return path"* — the return path is structural (parent link), not a message the model must re-send.
  Source: https://opencode.ai/docs/agents/ (Navigation between sessions) and https://github.com/anomalyco/opencode/pull/7756 (session tree dialog, breadcrumb header).

> **Implication for og**: og's tool-driven foreground handoff with a remembered return stack is *not matched* by any of these harnesses directly — they either make the human switch (opencode Tab, Codex `/agent`) or make the model re-register a reverse edge (OpenAI/Semantic Kernel). og's stack-based return is the clean version of the reverse-edge pattern. The handoff tool should be named in the `transfer_to_X` convention and take an optional short reason/summary payload (`input_type` analogue).

---

## 3. Monitoring running agents (terminal REPL affordances)

- **Claude Code**: 
  - a subagent panel below the prompt input lists running subagents; rows are removed immediately at clean completion, kept ~30s for failed/stopped ones, with a footer hint `/tasks to see subagents`.
  - `/tasks` lists running *and* recently finished background items; `Enter` on an item opens its transcript; `Ctrl+B` backgrounds a running task; `x` stops one; `SendMessage` steers/resumes a live subagent.
  - named background subagents appear in the `@`-mention typeahead *with their status* next to the name.
  Sources: https://code.claude.com/docs/en/sub-agents (Run subagents in foreground or background; Manage subagent context) and the `/tasks` command reference at https://code.claude.com/docs/en/commands.
- **opencode**: the session tree IS the monitor. Child sessions carry `parentID`; the TUI navigates the tree (down/right/left/up); v2.1-era PR #7756 added a depth-aware breadcrumb header (`Subagent session` with sessionPath/depth), a Session Tree dialog to jump anywhere, and a status panel. Task tool-call titles in the transcript are the task description. Background jobs surface in a status view.
  Sources: https://opencode.ai/docs/agents/ (Navigation) and https://github.com/anomalyco/opencode/pull/7756 (header rewrite, `DialogSessionTree`, `status_view`).
- **Codex CLI**: `/agent` opens the agent-thread view — one row per subagent thread with context/tool activity and its eventual result; approval requests from background threads are labeled with the source thread (`o` to open). Custom agents get a display-name pool (`nickname_candidates`). In the IDE the background-agent panel lists status and supports stopping all / opening a thread. Codex re-runs the same thread when you "resume"/steer a subagent — resumption preserves the subagent's own history.
  Sources: https://developers.openai.com/codex/subagents (Managing subagents; Approvals and sandbox controls) and https://deepwiki.com/openai/codex/3.6-thread-management-and-multi-agent.

---

## 4. Borrow vs avoid

### Borrow
1. **Purpose-derived task label as the instance name.** Task description → child session title + transcript record (opencode); `task_name` param (Codex v2). og's `delegate` tool should take `spec-auth-flow`-style labels directly and use them for session title and transcript metadata — never `subagent-1`.
2. **Wrapped result fold-back with a resumption handle.** `<task_result>/<task_error>` plus `task_id` (opencode), `agentId:` trailer (Claude Code). A small structured block the parent reads as normal tool output, carrying a handle to continue the same child session later.
3. **Fresh context + explicit whitelist.** Subagent gets its own instruction + the delegation prompt + project AGENTS.md (Claude Code) — never the parent transcript or parent system prompt. Fork/background variants are separate opt-ins.
4. **Foreground/background split on the delegation tool.** Default = block + stream (foreground); opt-in `background: true` completes asynchronously and the parent is *informed via an injected message on a later turn* — and the model is told not to poll (opencode/Claude Code).
5. **Hard bounds on the tree.** Depth defaults of 1 (Codex, opencode) to 3 (Claude Code); concurrency caps (6–20); optional spend cap. Default og should be depth-1 (no nesting) with a small concurrency cap, matching what shipped harnesses made the default.
6. **Permission-gate the delegation seam.** opencode `permission.task` glob patterns; a `deny` removes that subagent from the tool description so the model doesn't even try. Claude Code gates spawning via `Agent(agent_type)` allowlists on the parent's tool list. og's `delegate` should take a subagent *type* permissioned per-agent.
7. **Scan subagent results for prompt-injection shapes** (control-tag imitation, permission-config mentions, turn markers) before folding back (Claude Code v2.1.210).
8. **Handoff tool naming + routing hints.** `transfer_to_<agent>` (OpenAI) is the de facto naming; a short per-target description the choosing model reads. Semantic Kernel's captioned edges ("Transfer to this agent if…") is the same idea in graph form.
9. **Send structured metadata on the transfer** (`input_type` — reason/summary), validated by the harness and passed to an `on_handoff` hook, separate from the conversation payload.
10. **Return path as a stack / parent link.** opencode proves the *structural* parent-link return works well; og's stack-based return is that idea made explicit for foreground handoffs in a single transcript.

### Avoid
1. **Positional instance names.** No harness uses `subagent-1` for display — it's always task-derived. (Confirms the og requirement; nothing to copy from a counter.)
2. **Unbounded nesting as the default.** Every harness gates nesting behind config and warns (Claude Code depth-3 default, Codex depth-1, opencode `subagent_depth` default 1); Claude Code and Codex explicitly warn about token multiplication and approval-chain complexity for deep trees.
3. **Foreground handoff that silently keeps the old agent "primary" or resets the transcript.** opencode stores one agent per session and resets nothing; OpenAI handoffs flow the *whole conversation history* (with an optional filter). og's "same session/transcript, stack remembers the return" is coherent with both — just be explicit that a handoff does *not* create a child session.
4. **Automatic thread cleanup losing recoverable work.** Claude Code keeps failed/stopped rows visible for ~30s with `/tasks` access and protects partial output on API errors; Codex marks `report_agent_job_result`-missing workers as errors rather than dropping rows. Don't destroy a subagent's transcript on failure.
5. **Model polling for background results.** opencode/Claude Code explicitly forbid polling and inject notifications instead; that's the pattern to copy, not busy-polling.
6. **Full parent transcript spliced into the child.** Not done anywhere (fork is the explicit opt-in); keeps og's fold-back = summary, not transcript merge.
7. **A handoff primitive with no way back** as the *only* handoff shape (raw OpenAI Handoff). og's remembered-return stack is the improvement; don't regress to "register a reverse handoff by hand."
8. **Massive agent descriptions eating the model's context.** Claude Code warns at >15k tokens of combined subagent descriptions; keep `description`s short and push detail into the per-subagent instruction file (loaded only during delegation) — matches og's two-file agent model.

---

## Sources index (primary)

| Source | URL |
| --- | --- |
| opencode Agents docs | https://opencode.ai/docs/agents/ |
| opencode v2 Agents docs | https://opencode.ai/v2/docs/agents/ |
| opencode task tool source | https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/tool/task.ts |
| opencode PR #7756 (nested delegation, session tree) | https://github.com/anomalyco/opencode/pull/7756 |
| Claude Code subagents | https://code.claude.com/docs/en/sub-agents |
| Claude Agent SDK subagents | https://code.claude.com/docs/en/agent-sdk/subagents |
| OpenAI Agents SDK handoffs | https://openai.github.io/openai-agents-python/handoffs/ |
| OpenAI Agents SDK Handoff ref | https://openai.github.io/openai-agents-python/ref/handoffs/ |
| OpenAI orchestration guide (handoff vs agents-as-tools) | https://developers.openai.com/api/docs/guides/agents/orchestration |
| Semantic Kernel handoff orchestration | https://learn.microsoft.com/en-us/semantic-kernel/frameworks/agent/agent-orchestration/handoff |
| Codex subagents docs | https://developers.openai.com/codex/subagents |
| Codex thread management / multi-agent (code analysis) | https://deepwiki.com/openai/codex/3.6-thread-management-and-multi-agent |
| Codex TOML subagents & parallelism | https://codex.danielvaughan.com/2026/03/26/codex-cli-subagents-toml-parallelism/ |
| Anthropic multi-agent research system | https://www.anthropic.com/engineering/multi-agent-research-system |
| OpenAI vs Claude SDK orchestration comparison | https://www.developersdigest.tech/blog/openai-agents-sdk-vs-claude-agent-sdk |