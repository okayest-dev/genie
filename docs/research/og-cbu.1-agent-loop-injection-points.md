# Research: Agent Loop Injection Points

**Ticket:** og-cbu.1
**Branch:** research/og-cbu.1-injection-points
**Date:** 2026-09-03

## Summary

This maps every candidate lifecycle-hook injection point in the two harness turn
drivers (`agent.RunTurn` and `repl.runTurn`), the data available at each, and the
dependency direction problem that governs how hooks can arrive.

---

## 1. `agent.RunTurn` — Candidate Injection Points

File: `internal/agent/agent.go:45`
Signature:
```go
func RunTurn(ctx context.Context, c llm.Client, model, instruction, prompt string,
    out, errOut io.Writer, sess *session.Session, registry *tools.Registry,
    ldg *ledger.Ledger, cwd string, opts ...Option) error
```

### 1.1 Turn Start

**Location:** Lines 46–71 (after options applied, messages built, before request construction)

| Data in scope | Source |
|---|---|
| `model` | Parameter |
| `instruction` | Parameter (assembled agent instruction) |
| `prompt` | Parameter (user's current-turn text) |
| `to.agentName` | From `WithAgentName` option (may be `""`) |
| `messages` | Local `[]llm.Message` — exactly `[system/instruction, user/prompt]` |
| `sess` | Parameter (may be nil) |
| `registry` | Parameter (may be nil) |
| `ldg` | Parameter (may be nil) |
| `cwd` | Parameter |

**Inside/Outside request boundary:** Outside. `req` has not been built yet (line 74).
**Derivable vs threaded:** All fields derivable from parameters; no request object to thread.

### 1.2 Request Built / Pre-Stream

**Location:** Lines 74–81 (after `req` assembled, before `c.Stream`)

| Data in scope | Source |
|---|---|
| Everything from 1.1 | — |
| `req.Model` | From `model` |
| `req.Messages` | The `[instruction, prompt]` spine |
| `req.Tools` | From `registry.ToolDefs()` (nil when registry is nil) |

**Inside/Outside request boundary:** Outside. The request is assembled but not yet
sent to the provider. This is the last point before the Stream boundary.

### 1.3 Stream Open Failure / Tool-Array Retry

**Location:** Lines 88–108 (inside the `for` loop, after `c.Stream` returns error)

| Data in scope | Source |
|---|---|
| `err` | Stream open error (`*llm.ProviderError` when applicable) |
| `retriedNoTools` | `bool` — whether we already retried without tools |
| `req` | Current request (may have `.Tools` nulled on retry, line 94) |
| `reply` | Partial reply buffer (may be non-empty if a prior iteration succeeded) |

**Inside/Outside request boundary:** Inside (request was sent to provider).
**Hook semantics:** `before_tool_error` / `on_stream_error` — fires when the
provider rejects the request. Useful for error-recovery hooks but rare.

### 1.4 Per-Stream Event Iteration

**Location:** Lines 114–130 (`for ev := range stream`)

Each iteration yields one `llm.Event`. Candidate sub-points:

#### 1.4a Text Delta

**Location:** Line 116–119 (`case llm.EventText`)

| Data in scope | Source |
|---|---|
| `ev.Text` | Streaming text delta |
| `reply` | Accumulated reply so far (before this delta appended) |

#### 1.4b Finish Reason

**Location:** Line 121–122 (`case llm.EventFinish`)

| Data in scope | Source |
|---|---|
| `ev.End` | `llm.FinishReason` — `stop`, `tool_calls`, `length`, `other` |

#### 1.4c Usage

**Location:** Line 123–124 (`case llm.EventUsage`)

| Data in scope | Source |
|---|---|
| `ev.Usage` | `llm.Usage` — `PromptTokens`, `CompletionTokens`, `TotalTokens` |

#### 1.4d Tool Calls

**Location:** Line 125–126 (`case llm.EventToolCall`)

| Data in scope | Source |
|---|---|
| `ev.ToolCalls` | `[]llm.ToolCall` — each has `.ID`, `.Name`, `.Arguments` |

#### 1.4e Stream Error

**Location:** Line 127–128 (`case llm.EventError`)

| Data in scope | Source |
|---|---|
| `ev.Err` | Terminal mid-stream error |

**All of 1.4a–1.4e are inside the Stream boundary** (the provider connection is
open; the iterator is draining). Per-event sub-points are fine-grained but may
be too chatty for most lifecycle hooks; a single "stream iteration" point that
receives the aggregated `usage`/`finishReason`/`toolCalls` after the loop drains
is more practical (see 1.5).

### 1.5 Post-Stream / Pre-Tool-Execution

**Location:** Lines 132–170 (after `for ev := range stream` completes, before
tool-call execution)

| Data in scope | Source |
|---|---|
| `reply` | Complete accumulated text for this stream iteration |
| `toolCalls` | `[]llm.ToolCall` from the stream (may be empty) |
| `finishReason` | `llm.FinishReason` |
| `usage` | `llm.Usage` (may be zero-valued if provider omitted it) |
| `req` | The request that was sent |
| `sess` | Session (may be nil) |

**Inside/Outside request boundary:** Inside (provider returned a full response).

This is the most data-rich single point. It fires once per stream iteration
(there can be multiple stream iterations per turn when tool calls are present).

Two distinct outcomes diverge here:

- **No tool calls (lines 162–170):** Turn complete. `finishReason` and `usage`
  are final. This is the "turn end" point.
- **Tool calls present (line 173+):** Tool execution follows.

### 1.6 Tool Call — Pre-Execute

**Location:** Lines 174–198 (inside `for _, tc := range toolCalls`, after framing,
before `tool.Execute`)

| Data in scope | Source |
|---|---|
| `tc.Name` | Tool name (e.g. `"read"`, `"write"`, `"edit"`) |
| `tc.ID` | Unique tool-call ID |
| `tc.Arguments` | Raw JSON arguments string |
| `tool` | Resolved `tools.Tool` (nil if unknown/disabled) |
| `registry` | Tool registry (may be nil) |
| `ldg` | Ledger (may be nil) |
| `cwd` | Working directory |

**Inside/Outside request boundary:** Inside (this is within the turn, after the
provider responded with tool calls).

**Sub-point — Pre-mutation snapshot (lines 200–213):**
Fires only for `write`/`edit` tools. `args.Path` is unmarshalled from arguments.
This is a ledger concern, not a lifecycle hook.

### 1.7 Tool Call — Post-Execute

**Location:** Lines 214–254 (after `tool.Execute`, before tool message appended to request)

| Data in scope | Source |
|---|---|
| `tc.Name` | Tool name |
| `tc.ID` | Tool-call ID |
| `tc.Arguments` | Original arguments |
| `result` | Tool output string (empty on error) |
| `execErr` | Error from execution (nil on success) |
| `toolContent` | Formatted result/error string that will be sent to the model |
| `ldg` | Ledger (may be nil) |

**Inside/Outside request boundary:** Inside.

### 1.8 Tool Result Persisted / Added to Request

**Location:** Lines 257–269 (after tool message appended to `req.Messages` and session)

| Data in scope | Source |
|---|---|
| `toolMsg` | The `llm.Message{Role: "tool", Content: toolContent, ToolCallID: tc.ID}` |
| `req.Messages` | Growing message list (now includes this tool result) |
| `sess` | Session (may be nil) |

**Inside/Outside request boundary:** Inside. The loop will then go back to line 86
to stream the next response with updated `req.Messages`.

### 1.9 Turn End (No Tool Calls — Final)

**Location:** Lines 162–170

| Data in scope | Source |
|---|---|
| `finishReason` | Final finish reason |
| `usage` | Final token usage |
| `reply` | Complete assistant text |
| `model` | Original model parameter |

**Inside/Outside request boundary:** Inside (provider returned, stream drained).

This is the canonical "turn completed" hook point. The function returns `nil` here.

### 1.10 Turn Error (Provider or Tool Failure)

**Location:** Line 108 (`return err` from Stream open), line 128 (`return ev.Err`
from mid-stream error), and the implicit error return from any `sess.Append`
failure (lines 60, 66, 97, 148, 156, 266).

| Data in scope | Source |
|---|---|
| `err` or `ev.Err` | The error |
| `model` | Original model parameter |
| `reply` | Partial accumulated text (may be empty) |
| `finishReason` | May be set if the error came after a finish event |
| `usage` | May be partially populated |

**Inside/Outside:** Depends on the error path. Stream open failures are at the
boundary; mid-stream errors are inside.

---

## 2. `repl.runTurn` — Candidate Injection Points

File: `internal/repl/repl.go:219`
Signature:
```go
func runTurn(ctx context.Context, cfg *Config, state *replState,
    prompt string, sess *session.Session, sigCh <-chan os.Signal)
```

Note: `runTurn` is unexported and returns nothing. Errors are printed to stderr
and swallowed. Cancellation is handled via `select` on `sigCh`.

### 2.1 Turn Begin

**Location:** Lines 221–228 (after resolving instruction/registry/model, before goroutine launch)

| Data in scope | Source |
|---|---|
| `instruction` | Resolved instruction for current agent |
| `registry` | Resolved tool registry for current agent |
| `model` | Model ID for current agent |
| `prompt` | User's input text |
| `state.currentAgent` | Current agent config (may be nil for default) |
| `state.client` | The context-wrapped `llm.Client` (owns history injection) |
| `sess` | Session |
| `cfg.Cwd` | Working directory |

**Derivable vs threaded:** All derivable. `state.client` is the contextmgr-wrapped
client; the agent loop never sees the raw client.

### 2.2 Turn Launched (Goroutine Start)

**Location:** Lines 231–235 (`go func() { errCh <- agent.RunTurn(...) }()`)

A goroutine is spawned; `agent.RunTurn` executes asynchronously. The REPL holds
a `turnCtx` with `cancel()`.

### 2.3 Ctrl+C Cancellation

**Location:** Lines 238–240 (`case <-sigCh`)

| Data in scope | Source |
|---|---|
| `turnCtx` | The cancellable context (about to be cancelled) |
| `cancel` | Cancel function |
| `prompt` | Original user input |
| `state.currentAgent` | Current agent |

`cancel()` is called, which propagates into the agent loop's `c.Stream(ctx, req)`
call, aborting the in-flight provider request.

**No data about the in-flight turn is available here** — the goroutine is
abandoned (not joined). The session may be left in a partially-persisted state.

### 2.4 Turn Complete (Error Channel)

**Location:** Lines 241–246 (`case err := <-errCh`)

| Data in scope | Source |
|---|---|
| `err` | Error returned by `agent.RunTurn` (nil on success) |
| `prompt` | Original user input |
| `state.currentAgent` | Current agent |

`cancel()` is always called. On error, it is printed to stderr. On success,
nothing happens — the REPL loops back to read the next input.

**No access to `finishReason`, `usage`, or `toolCalls`** — those are consumed
inside `agent.RunTurn` and not returned.

### 2.5 Inline Agent One-Shot

**Location:** Lines 186–216 (`handleInlineAgent`)

| Data in scope | Source |
|---|---|
| `agentName` | Parsed from `@name` prefix |
| `resolved` | Resolved agent config |
| `prompt` | Remaining text after `@name` |
| `state.previousAgent` | Saved previous agent (restored after turn) |

This is a wrapper around `runTurn` that saves/restores agent state. The same
injection points as 2.1–2.4 apply.

---

## 3. Dependency Direction Problem

### Current Import Graph

```
cmd/genie/main.go
  └─→ internal/agent        (direct import)
  └─→ internal/repl         (direct import)
  └─→ internal/plugin       (direct import — plugin loading)

internal/repl
  └─→ internal/agent        (direct import — calls agent.RunTurn)
  └─→ internal/contextmgr   (direct import — constructs ContextManager)
  └─→ internal/config
  └─→ internal/instruct
  └─→ internal/ledger
  └─→ internal/llm
  └─→ internal/session
  └─→ internal/tools

internal/agent
  └─→ internal/ledger       (direct import)
  └─→ internal/llm          (direct import)
  └─→ internal/session      (direct import)
  └─→ internal/tools        (direct import)
  └─→ ✗ internal/contextmgr (NOT imported)
  └─→ ✗ internal/plugin     (NOT imported)

internal/contextmgr
  └─→ internal/llm
  └─→ internal/modelinfo
  └─→ internal/session
  └─→ internal/tokens
  └─→ ✗ internal/agent      (NOT imported — no cycle)

internal/plugin
  └─→ internal/contextmgr   (direct import — implements contextmgr.Hooks)
  └─→ internal/llm
  └─→ ✗ internal/agent      (NOT imported)
```

### Key Finding

**`internal/agent` does NOT import `internal/contextmgr` or `internal/plugin`.**
The agent package is a pure loop driver: it receives an `llm.Client` (which
happens to be a `contextmgr.ContextManager` at runtime) and knows nothing about
the context-management layer or the plugin system.

**`internal/repl` is the assembly point** — it imports both `agent` and
`contextmgr`, constructs the context-wrapped client, and passes it to
`agent.RunTurn`. The REPL also has access to `plugin` (via `cmd/genie`).

### Implication for Lifecycle Hooks

Hooks **cannot** arrive at `agent.RunTurn` via direct import of `contextmgr` or
`plugin` without creating a dependency from `agent → contextmgr` (which is clean
today) or `agent → plugin` (which would pull the full plugin subsystem into the
agent loop).

Three architectural paths:

1. **Interface parameter on `RunTurn`** — define a `LifecycleHooks` interface in
   `internal/agent` (or a shared package like `internal/llm`), pass it as an
   `Option`. The REPL (or `main`) constructs the implementation by wiring it to
   the plugin system. No new imports in `agent`. This is the cleanest seam.

2. **Extend `llm.Client`** — add lifecycle callbacks to the `llm.Client`
   interface or wrap it in a decorator. The agent loop already holds a `Client`;
   a decorating client could fire hooks transparently. Problem: the `Client`
   interface is already a clean provider seam; bloating it with lifecycle concerns
   conflates two responsibilities.

3. **Fire hooks in `repl.runTurn`** — the REPL already has full access. It could
   fire `before_turn`/`after_turn` around the `agent.RunTurn` call, and
   `on_cancel` in the `sigCh` branch. The agent loop would remain hook-free.
   Problem: this misses intra-turn events (tool before/after, per-stream
   iteration) that only the agent loop sees.

**Recommendation:** Path 1 (interface parameter) for intra-turn hooks (tool
before/after, stream events), Path 3 (REPL-level) for turn-scoped hooks
(turn begin/end, cancellation). These compose cleanly.

---

## 4. Data Availability: Derivable vs. Threaded

### Data derivable at every injection point (no request threading needed)

These come from `RunTurn` parameters or local variables built before the request:

| Data | Where available |
|---|---|
| `model` | Parameter — all points |
| `instruction` | Parameter — all points |
| `prompt` | Parameter — all points |
| `to.agentName` | Option — all points |
| `cwd` | Parameter — all points |
| `sess` | Parameter — all points (nil check needed) |
| `registry` | Parameter — all points (nil check needed) |
| `ldg` | Parameter — all points (nil check needed) |

### Data available only after the Stream boundary

These require the provider to have been called and the stream to have drained
(at least partially):

| Data | Where available |
|---|---|
| `finishReason` | Post-stream (line 122), turn-end (line 162) |
| `usage` | Post-stream (line 124), turn-end (line 162) |
| `toolCalls` | Post-stream (line 126), pre-tool-execution (line 174) |
| `reply` (accumulated text) | During stream (line 120), post-stream (line 133) |

### Data requiring the `llm.Request` to be threaded

The `req` variable (built at line 74, mutated by tool results at line 262) is
**not** exposed as a parameter or return value. If a hook needs the full
assembled request (model + messages + tools), it must either:

- Be passed `req` explicitly via the hook interface, or
- Reconstruct it from the parameters (possible for the initial request; not
  possible for the mid-turn request with accumulated tool results).

The existing `contextmgr.Hooks` interface receives the full `llm.Request` at
`BeforeRequest` and `AfterResponse` — this works because the ContextManager
owns the request assembly. Agent-loop hooks that need the full request would
need it threaded through the hook call.

### Data available only in `repl.runTurn`

| Data | Where available |
|---|---|
| `state.currentAgent` | Lines 221–223, 238–246 |
| `state.client` | Line 233 (the contextmgr-wrapped client) |
| `turnCtx` / `cancel` | Lines 230–231, 239 |
| Cancellation event | Line 238 (`sigCh`) |

---

## 5. Complete Injection Point Index

| # | Point | File:Line | Boundary | Data highlights |
|---|---|---|---|---|
| A1 | Turn start | agent.go:46–71 | Pre-request | model, instruction, prompt, agentName |
| A2 | Request built | agent.go:74–81 | Pre-request | req.Model, req.Messages, req.Tools |
| A3 | Stream open error | agent.go:88–108 | Inside (failed) | err, retriedNoTools, req |
| A4 | Post-stream | agent.go:132–170 | Inside | reply, toolCalls, finishReason, usage |
| A5 | Tool pre-execute | agent.go:174–198 | Inside | tc.Name, tc.ID, tc.Arguments, tool |
| A6 | Tool post-execute | agent.go:214–254 | Inside | result, execErr, tc.Name, toolContent |
| A7 | Tool result persisted | agent.go:257–269 | Inside | toolMsg, req.Messages |
| A8 | Turn end (no tools) | agent.go:162–170 | Inside | finishReason, usage, reply |
| A9 | Turn error | agent.go:108/128 | Boundary/inside | err, reply (partial) |
| R1 | REPL turn begin | repl.go:221–228 | Pre-goroutine | instruction, model, prompt, agent |
| R2 | Ctrl+C cancel | repl.go:238–240 | Mid-turn | turnCtx, cancel (no turn data) |
| R3 | REPL turn done | repl.go:241–246 | Post-goroutine | err (no finish/usage/toolCalls) |

---

## 6. Existing Hook Surface for Comparison

The existing `contextmgr.Hooks` interface (defined at `internal/contextmgr/contextmgr.go:33`)
fires four hooks around `ContextManager.Stream`:

| Hook | When | Data |
|---|---|---|
| `BeforeRequest` | After history injection, before `inner.Stream` | Full `llm.Request` (rewritable) |
| `AfterResponse` | After stream drains | `llm.Request` + final `llm.Usage` |
| `Compact` | During request assembly (single-active) | `llm.Request` (rewritable) |
| `Condense` | During request assembly (single-active) | `llm.Request` (rewritable) |

These fire **inside the ContextManager**, which is the `llm.Client` passed to
`agent.RunTurn`. The agent loop is unaware of them. The proposed agent-loop
hooks would fire at a **different layer** — inside the loop itself — and see
data the ContextManager never sees (tool names, tool results, per-iteration
events, cancellation).

The two layers are complementary, not overlapping:
- ContextManager hooks: request-level (history rewriting, token accounting)
- Agent-loop hooks: turn-level and tool-level (observability, guardrails, UX)
