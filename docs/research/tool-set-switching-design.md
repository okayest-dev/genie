# Design: Tool-Set Switching — Per-Agent Tool Configuration

**Ticket**: genie-4n6
**Status**: Resolution

---

## Problem

Today, `main.go` builds a single `*tools.Registry` with all four built-in tools (plus any plugin tools). This registry is passed to the REPL and to every `agent.RunTurn()` call. There is no mechanism to restrict which tools a particular agent can use.

When a named agent is active, its `tools` field (a `[]string` of tool names) defines the exact set of tools available to the model for that turn. The agent's tool set replaces the global set; an absent or empty `tools` field inherits all available tools.

## Design

### Core principle: subset, not mutation

The global registry is the source of truth — it holds all registered tools (built-in + plugin). Tool-set switching never mutates the global registry. Instead, it creates a **filtered copy** containing only the tools the active agent may use. This is safe for concurrent sessions and avoids state leakage between agent switches.

### New method: `Registry.Subset`

A new method on `*tools.Registry` that returns a new `*Registry` containing only the named tools:

```go
// Subset returns a new Registry containing only the tools whose names
// appear in the allowed list, in the same order as the original.
// Names not found in the registry are silently skipped.
// If allowed is nil or empty, returns a copy of the full registry.
func (r *Registry) Subset(allowed []string) *Registry {
    if len(allowed) == 0 {
        return r.Copy()
    }
    sub := NewRegistry()
    for _, name := range allowed {
        if t, ok := r.Get(name); ok {
            sub.Register(t)
        }
    }
    return sub
}
```

### New method: `Registry.Copy`

A shallow copy of the registry — same tools, independent disabled map:

```go
// Copy returns a new Registry with the same tools and disabled state.
func (r *Registry) Copy() *Registry {
    c := NewRegistry()
    for _, t := range r.tools {
        c.Register(t)
    }
    for name, disabled := range r.disabled {
        if disabled {
            c.Disable(name)
        }
    }
    return c
}
```

### Tool names: built-in and plugin

The agent's `tools` list may reference **any registered tool name** — built-in (`read`, `write`, `edit`, `bash`) or plugin-registered. The names are validated at activation time against the live registry, not at parse time (plugins may not be loaded when the TOML is parsed — this was decided in genie-7l6).

### Missing or disabled tools: error at activation

When resolving an agent's tool set against the registry:

- If a tool name in the agent's list does not exist in the global registry → **error at activation** (the agent cannot be used in this environment)
- If a tool name exists but is disabled in config → also an error (disabled tools are not in `Get()`'s result set, so `Subset` silently skips them — but the agent asked for a tool it can't have)

The error is surfaced when the agent is selected (`/agent`, `@name`, `-a`), not at parse time. This gives a clear, actionable message:

```
agent "reviewer": tool "search" not available (not registered)
```

or:

```
agent "coder": tool "write" is disabled in config
```

**Implementation**: add a `ValidateTools` method on `Registry`:

```go
// ValidateTools checks that every name in the list is registered and not
// disabled. Returns an error listing all unavailable tools.
func (r *Registry) ValidateTools(names []string) error {
    var missing []string
    for _, name := range names {
        if _, ok := r.Get(name); !ok {
            missing = append(missing, name)
        }
    }
    if len(missing) > 0 {
        return fmt.Errorf("tool(s) not available: %s", strings.Join(missing, ", "))
    }
    return nil
}
```

### Agent's tool list is authoritative

When an agent specifies `tools = ["read", "bash"]`, only `read` and `bash` are available — regardless of what the config's `[tools]` section says. The config toggles are the **global default** (applied when no agent is active); the agent's list overrides them completely.

When no agent is active (or the agent has `tools = []` / no `tools` field): the config's `[tools]` toggles apply as they do today. No regression.

### How the REPL passes the registry

The REPL gains agent state (from the instruction-stacking design). The per-turn flow becomes:

```
Before each RunTurn:
  1. Resolve instruction (per instruction-stacking design)
  2. Resolve tool registry:
     - If currentAgent == nil → use cfg.Registry (global, config-toggled)
     - If currentAgent != nil && currentAgent.Tools != nil →
         cfg.Registry.Subset(currentAgent.Tools)
     - If currentAgent != nil && currentAgent.Tools == nil →
         use cfg.Registry (inherit all)
  3. Pass the resolved registry to agent.RunTurn()
```

The REPL stores the **global** registry in its Config. It never holds a per-agent registry — it creates one fresh each turn from the global + current agent. This avoids stale state.

### @name one-shot: same pattern as instructions

```
1. User types: @coder search for "TODO"
2. REPL saves currentAgent → previousAgent
3. REPL sets currentAgent = AgentReg.GetResolved("coder")
4. REPL resolves: instruction via LoadWithAgent, registry via Subset
5. REPL calls: agent.RunTurn(... with agent-scoped registry ...)
6. After turn completes: currentAgent = previousAgent (revert)
```

The tool set reverts along with the instruction. No separate state to manage.

### /agent switch: same pattern

```
1. User types: /agent reviewer
2. REPL sets currentAgent = AgentReg.GetResolved("reviewer")
3. REPL validates: cfg.Registry.ValidateTools(currentAgent.Tools)
   → error if any tool unavailable
4. REPL announces: "Switched to reviewer (model: big-pickle, tools: read, bash)"
5. All subsequent turns use the new agent's tool set
```

### Non-interactive mode (`-a` flag)

Same pattern — resolve agent, validate tools, create subset, pass to `RunTurn`:

```go
agent, err := agentReg.GetResolved(agentName, cfg)
if err != nil { /* error */ }
if err := registry.ValidateTools(agent.Tools); err != nil {
    fmt.Fprintf(stderr, "agent %q: %v\n", agentName, err)
    return 1
}
agentRegistry := registry.Subset(agent.Tools)
instruction, err := instruct.LoadWithAgent(cfg, agent, cwd)
err = agent.RunTurn(ctx, client, agent.Model, instruction, *prompt, stdout, stderr, sess, agentRegistry, ldg, cwd)
```

### Interaction with config `[tools]` section

The config `[tools]` section disables tools **globally** at startup, before any agent is resolved:

```
config.Load()
  → Tools{Read: true, Write: false, Edit: true, Bash: true}

buildRegistry()
  → Registry{read, write(DISABLED), edit, bash}

Agent "coder" has tools = ["read", "write", "edit"]
  → ValidateTools: "write" is disabled → error

Agent "reviewer" has tools = ["read", "bash"]
  → ValidateTools: all available → Subset{read, bash}
```

Config toggles gate what exists in the global registry. Agent lists gate what the agent can see. The two compose: an agent can only use tools that are both registered AND not config-disabled.

### Interaction with plugin tools

Plugin tools are registered on the global registry during `pluginMgr.LoadPlugins()`. They're available for agent tool lists just like built-in tools:

```toml
# ~/.config/genie/agents/researcher.toml
tools = ["read", "bash", "websearch"]
```

If the `websearch` plugin isn't loaded, `Subset("websearch")` finds nothing → `ValidateTools` catches it → error at activation.

If the plugin IS loaded, `Subset` wraps the `pluginTool` in a new registry. The plugin tool's `Execute()` still delegates to the plugin subprocess — no change to the plugin system.

---

## Files to create/modify

| File | Action | Purpose |
|------|--------|---------|
| `internal/tools/tools.go` | **Modify** | Add `Subset`, `Copy`, `ValidateTools` methods |
| `internal/tools/tools_test.go` | **Modify** | Tests for Subset, Copy, ValidateTools |
| `internal/repl/repl.go` | **Modify** | Per-turn registry resolution (depends on genie-bvq, genie-i1f) |
| `cmd/genie/main.go` | **Modify** | Validate + subset for `-a` flag (depends on genie-e0d) |

## Edge cases

- **Agent with empty tools field**: `Subset(nil)` → `Copy()` → full registry. Identical to no-agent behavior.
- **Agent with tools that include a config-disabled tool**: `ValidateTools` returns error listing the disabled tool. Agent cannot be activated.
- **Agent with tools that include a plugin tool name**: works if plugin is loaded; error if not. Validated at activation, not parse time.
- **@name to agent with unavailable tool**: error before turn executes; no state change (previousAgent not consumed).
- **Switching from agent to no-agent**: currentAgent = nil → registry reverts to global. All config toggles apply.
- **Concurrent sessions**: each turn creates its own Subset from the global. No shared mutable state between sessions.
