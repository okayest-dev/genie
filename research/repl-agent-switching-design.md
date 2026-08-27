# Design: REPL Agent Switching — /agent Command and @name Inline

**Ticket**: og-i1f
**Status**: Resolution

---

## Problem

The REPL (`internal/repl/repl.go`) runs turns with a fixed instruction and registry. When named agents are available, the REPL needs to:
- List and switch between agents via `/agent`
- Support one-shot agent switches via `@name` inline syntax
- Re-assemble instruction and re-filter registry per turn
- Record agent switches in the session transcript

The instruction-stacking (og-bvq) and tool-set-switching (og-4n6) designs already specified *what* changes per turn (instruction, model, registry). This design specifies *how the REPL drives it*.

## Design

### State management: `replState`

The REPL holds mutable agent state in a local struct inside `Run`, not on `Config`:

```go
type replState struct {
    currentAgent *config.ResolvedAgent // active agent (nil = no agent)
    previousAgent *config.ResolvedAgent // saved before @name one-shot, restored after
    instruction  string                // resolved instruction for current agent
}
```

Created at REPL start:
```go
state := &replState{
    currentAgent: cfg.DefaultAgent, // nil if no default
}
// Resolve initial instruction
state.instruction = resolveInstruction(cfg, state.currentAgent)
```

### Config changes

Following the instruction-stacking design, `repl.Config` gains:

```go
type Config struct {
    Client       llm.Client
    Model        string
    Instruction  string               // default instruction (no-agent fallback)
    SessionDir   string
    Registry     *tools.Registry      // global registry
    Cwd          string               // working directory for AGENTS.md
    AgentReg     *config.AgentReg     // agent registry (nil = no agents)
    DefaultAgent *config.ResolvedAgent // resolved default agent (nil = no default)
    BashTimeout  time.Duration        // for building per-agent registries
    Stdin        io.Reader
    Stdout       io.Writer
    Stderr       io.Writer
}
```

### Per-turn resolution

Before each `RunTurn`, the REPL resolves instruction and registry from the current agent:

```go
func resolveInstruction(cfg *Config, agent *config.ResolvedAgent) string {
    if agent == nil {
        return cfg.Instruction
    }
    s, err := instruct.LoadWithAgent(cfg, agent, cfg.Cwd)
    if err != nil {
        // Log error, fall back to default instruction
        slog.Error("failed to resolve instruction", "error", err)
        return cfg.Instruction
    }
    return s
}

func resolveRegistry(cfg *Config, agent *config.ResolvedAgent) *tools.Registry {
    if agent == nil {
        return cfg.Registry
    }
    if agent.Tools == nil {
        return cfg.Registry // inherit all
    }
    return cfg.Registry.Subset(agent.Tools)
}
```

### /agent command

Follows the established `/model` list-selection pattern: `/x` lists, `/x <name>` selects.

**`/agent` (no args) — list agents:**

```
Available agents:
  * coder       model: big-pickle   tools: read, write, edit, bash
    reviewer    model: big-pickle   tools: read, bash
    fast        model: gpt-4o-mini  tools: (all)

Current: coder
```

- `*` marks the current agent (or `(default: coder)` if from config default)
- `(all)` when tools field is nil/empty (inherits all)
- If `AgentReg` is nil or returns empty list: `"no agents configured"` (not an error — just informational)

**`/agent <name>` — switch:**

```
1. Look up agent via AgentReg.GetResolved(name, cfg)
   → Unknown name: hard error, current agent unchanged:
     "og: no such agent: <name>"
2. Validate tools: cfg.Registry.ValidateTools(agent.Tools)
   → Unavailable tool: hard error, current agent unchanged:
     "agent <name>: tool(s) not available: <missing>"
3. Set currentAgent = agent
4. Resolve instruction and registry
5. Announce:
     "switched to <name> (model: <model>)"
   If tools are restricted, append:
     "tools: <comma-separated list>"
6. Session log: metadata on next user turn (see Session Logging below)
```

No confirmation prompt — it's a lightweight switch like `/model`. The announce line gives immediate feedback.

### @name inline syntax

Parsed at the **start of the line**, before slash command dispatch. Only the first token matters.

**Parsing rule:**
- Line starts with `@` → extract agent name (characters until first space or end of input)
- Remaining text after the space is the prompt
- Agent name must match `[a-z0-9-]+` (same as filename stem derivation)

**Cases:**

| Input | Result |
|-------|--------|
| `@coder review this diff` | One-shot switch to `coder`, prompt = `review this diff` |
| `@coder-bar do something` | One-shot switch to `coder-bar`, prompt = `do something` |
| `@coder` (no prompt) | Error: `"og: @name requires a prompt"` |
| `@nonexistent fix this` | Error: `"og: no such agent: nonexistent"` |
| `@coder` tools unavailable | Error: `"agent coder: tool(s) not available: ..."` |
| `hello @coder world` | No `@` at start → literal text, no agent switch |

**Only the first `@name` is recognized.** `@a @b do something` switches to agent `a` with prompt `@b do something` (the `@b` is literal text in the prompt). No nesting.

**One-shot flow:**

```
1. User types: @coder review this diff
2. REPL saves: previousAgent = currentAgent
3. REPL sets:  currentAgent = AgentReg.GetResolved("coder")
4. Validates tools (error → no state change, turn doesn't execute)
5. Resolves: instruction via resolveInstruction, registry via resolveRegistry
6. Runs: agent.RunTurn(... with agent-scoped instruction + registry ...)
7. After turn completes: currentAgent = previousAgent (revert)
8. Instruction re-resolved for the reverted agent
```

State is always reverted, even if the turn errors. The revert happens in a `defer` or explicit post-turn step.

### Session logging: agent switch metadata

Agent switches are recorded as metadata on the **next user turn** in the JSONL transcript. This avoids adding a new role type and keeps the transcript linear.

**Implementation — `AppendWithMeta`:**

```go
// session.go

type TranscriptLine struct {
    Role       string            `json:"role"`
    Content    string            `json:"content"`
    ToolCalls  []transcriptToolCall `json:"tool_calls,omitempty"`
    ToolCallID string            `json:"tool_call_id,omitempty"`
    Metadata   map[string]string `json:"metadata,omitempty"` // NEW
}

// AppendWithMeta adds a message with metadata to the transcript.
// The metadata is persisted in the JSONL but not carried in llm.Message.
func (s *Session) AppendWithMeta(msg llm.Message, meta map[string]string) error {
    // Same as Append, but populates line.Metadata
    // ...
}
```

**How RunTurn receives the agent name — options pattern:**

```go
// agent.go

type Option func(*turnOptions)

type turnOptions struct {
    agentName string
}

func WithAgentName(name string) Option {
    return func(o *turnOptions) { o.agentName = name }
}

func RunTurn(ctx context.Context, c llm.Client, model, instruction, prompt string,
    out, errOut io.Writer, sess *session.Session, registry *tools.Registry,
    ldg *ledger.Ledger, cwd string, opts ...Option) error {
    
    var to turnOptions
    for _, o := range opts {
        o(&to)
    }
    // ...
    // When persisting the user message:
    if sess != nil {
        if to.agentName != "" {
            sess.AppendWithMeta(messages[1], map[string]string{"agent": to.agentName})
        } else {
            sess.Append(messages[1])
        }
    }
    // ...
}
```

The REPL passes the agent name for the user turn only:

```go
var agentOpt agent.Option
if state.currentAgent != nil {
    agentOpt = agent.WithAgentName(state.currentAgent.Name)
}
agent.RunTurn(turnCtx, cfg.Client, currentModel(), state.instruction, line,
    cfg.Stdout, cfg.Stderr, sess, resolveRegistry(cfg, state.currentAgent), nil, cfg.Cwd, agentOpt)
```

**Transcript example:**

```jsonl
{"role":"user","content":"hello"}
{"role":"assistant","content":"Hi! How can I help?"}
{"role":"user","content":"review this diff","metadata":{"agent":"coder"}}
{"role":"assistant","content":"Looking at the diff..."}
{"role":"user","content":"what do you think?"}
{"role":"assistant","content":"I think it looks good."}
```

The `"agent":"coder"` metadata on the second user turn tells the audit trail which agent handled that turn. No separate "switch" event needed — the metadata on the turn IS the record.

**Session log format — settled:** Option C from the ticket (metadata on next user turn). This is the leanest option: no new role, no system message injection, no struct bloat. Just an optional map on the transcript line.

### Model during switching

The model is resolved from the agent definition. The REPL does NOT mutate `cfg.Model` on agent switch — it reads the model from `currentAgent.Model` (or falls back to `cfg.Model` when no agent is active):

```go
func currentModel(cfg *Config, agent *config.ResolvedAgent) string {
    if agent != nil && agent.Model != "" {
        return agent.Model
    }
    return cfg.Model
}
```

`/model` switches the **default** model (modifies `cfg.Model`). An active agent's model overrides it. When the agent is deactivated (revert from @name, or `/agent` with no args), the model reverts to `cfg.Model`.

### Revised REPL main loop

```go
func Run(ctx context.Context, cfg *Config) error {
    sess, err := session.New(cfg.SessionDir)
    if err != nil { return err }

    state := &replState{
        currentAgent: cfg.DefaultAgent,
    }
    state.instruction = resolveInstruction(cfg, state.currentAgent)

    scanner := bufio.NewScanner(cfg.Stdin)
    for {
        // ... signal handling, prompt ...

        line := strings.TrimSpace(scanner.Text())
        if line == "" { continue }

        // 1. Parse @name one-shot (before slash commands)
        if agentName, prompt, ok := parseInlineAgent(line); ok {
            handleInlineAgent(ctx, agentName, prompt, cfg, state, sess)
            continue
        }

        // 2. Handle slash commands
        if strings.HasPrefix(line, "/") {
            if handleSlashCommand(ctx, line, cfg, state, &sess) {
                return nil
            }
            continue
        }

        // 3. Normal turn
        runTurn(ctx, cfg, state, line, sess)
    }
}
```

### Files to modify

| File | Action | Purpose |
|------|--------|---------|
| `internal/session/session.go` | **Modify** | Add `Metadata` to `TranscriptLine`, add `AppendWithMeta` |
| `internal/session/session_test.go` | **Modify** | Test `AppendWithMeta` round-trip |
| `internal/agent/agent.go` | **Modify** | Add `Option` type, `WithAgentName`, use in user-message persist |
| `internal/agent/agent_test.go` | **Modify** | Test agent-name option plumbing |
| `internal/repl/repl.go` | **Modify** | Add `replState`, Config fields, `/agent` command, `@name` parsing, per-turn resolution |
| `internal/repl/repl_test.go` | **Modify** | Test `/agent` list/switch, `@name` parsing, state revert |
| `cmd/og/main.go` | **Modify** | Pass new Config fields, resolve default agent |

### Edge cases

- **No agents configured** (`AgentReg` is nil): `/agent` prints `"no agents configured"`, `@name` errors with `"no such agent"`. No crash.
- **Default agent at startup**: `cfg.DefaultAgent` pre-populates `state.currentAgent`. All turns use it until switched.
- **@name when already on an agent**: saves current → sets new → runs → restores. Works correctly even if current is also an agent.
- **@name to same agent as current**: still a one-shot — saves, sets (no-op), runs, restores. Unnecessary but harmless.
- **/agent while in @name one-shot**: can't happen — @name is a single-turn operation, the next line is a fresh iteration.
- **Ctrl+C during @name turn**: turn cancels, state reverts. Same as normal turn cancellation.
- **Agent with unavailable tools**: error before turn executes, no state change, no session log entry.
- **Empty prompt after /agent**: `/agent coder` with no further input — switches agent, returns to prompt. The switch itself is the action.
- **Model from agent vs /model**: agent model wins when active. `/model` sets the fallback. Clear hierarchy.

### Interaction with /new

`/new` creates a fresh session. The agent state (`currentAgent`) is **not** reset — it persists across sessions within the same REPL run. This matches `/model` behaviour (model persists across `/new`).

### Interaction with /model

`/model` changes `cfg.Model` (the default). If an agent is active, the agent's model is used instead. After the agent is deactivated, `cfg.Model` takes effect. They're independent settings that compose: agent model > config model.
