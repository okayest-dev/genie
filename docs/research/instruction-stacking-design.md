# Design: Instruction Stacking with Agent Definitions

**Ticket**: genie-bvq
**Status**: Resolution

---

## Problem

Today, `instruct.Load(cfg, cwd)` assembles the instruction once at startup and returns a string. The REPL stores it and passes the same string to every `agent.RunTurn()` call. There is no mechanism to change the instruction mid-session.

When a named agent is active, its `instruction_file` replaces the config-level instruction file, and `inherit_agents_md` controls whether AGENTS.md still appends. The instruction must be re-assemblable per-turn to support `/agent` switches and `@name` one-shot overrides.

## Design

### Assembly rule

The agent's `instruction_file` **fully replaces** the config's `instruction_file`. They do not stack. The agent IS the instruction source when active.

**Assembly order** (unchanged structure, different sources):

```
1. DefaultPrompt                           (always present)
2. instruction_file                        (agent's if set; else config's if set; else skip)
3. AGENTS.md from cwd                      (if inherit_agents_md is true; default: true)
```

When no agent is active: identical to today's behaviour (default + config instruction_file + AGENTS.md). No regression.

When an agent is active with `instruction_file = "..."`: default + agent's file + [AGENTS.md if inherit_agents_md].

When an agent is active with no `instruction_file`: default + config's instruction_file + [AGENTS.md if inherit_agents_md]. The agent inherits the config's instruction source.

### New function: `LoadWithAgent`

A new function alongside the existing `Load`. `Load` is unchanged (backward compatible — existing callers don't break). `LoadWithAgent` handles agent-aware assembly.

```go
// LoadWithAgent assembles the instruction with an optional agent override.
// When agent is nil, behaves identically to Load.
// When agent is set:
//   - agent.InstructionFile replaces cfg.InstructionFile (if agent's is non-empty)
//   - agent.InheritAgentsMD controls AGENTS.md inclusion (default true)
func LoadWithAgent(cfg *config.Config, agent *config.ResolvedAgent, cwd string) (string, error) {
    instruction := DefaultPrompt

    // Determine which instruction file to use.
    instructionFile := cfg.InstructionFile  // config default
    if agent != nil && agent.InstructionFile != "" {
        instructionFile = agent.InstructionFile  // agent overrides
    }

    if instructionFile != "" {
        b, err := os.ReadFile(instructionFile)
        if err != nil {
            return "", fmt.Errorf("instruction file %s: %w", instructionFile, err)
        }
        instruction += "\n" + string(b)
        slog.Info("instruction file loaded", "path", instructionFile, "bytes", len(b))
    }

    // AGENTS.md: included unless agent explicitly excludes it.
    inheritAgentsMD := true
    if agent != nil && agent.InheritAgentsMD != nil {
        inheritAgentsMD = *agent.InheritAgentsMD
    }

    if inheritAgentsMD {
        agentsPath := filepath.Join(cwd, "AGENTS.md")
        b, err := os.ReadFile(agentsPath)
        if err == nil {
            instruction += "\n" + string(b)
            slog.Info("AGENTS.md loaded", "path", agentsPath, "bytes", len(b))
        } else if !os.IsNotExist(err) {
            return "", fmt.Errorf("reading AGENTS.md: %w", err)
        }
    }

    slog.Info("instruction assembled", "total_bytes", len(instruction))
    return instruction, nil
}
```

### `Load` delegates to `LoadWithAgent`

The existing `Load` becomes a thin wrapper:

```go
func Load(cfg *config.Config, cwd string) (string, error) {
    return LoadWithAgent(cfg, nil, cwd)
}
```

Zero behaviour change for existing callers.

### REPL changes

The REPL currently receives a pre-assembled `Instruction` string in its `Config`. For agent switching, it needs to re-assemble per-turn.

**Config changes:**

```go
type Config struct {
    Client      llm.Client
    Model       string
    Instruction string          // default instruction (no agent); kept as fallback
    SessionDir  string
    Registry    *tools.Registry
    Cwd         string          // NEW: working directory for AGENTS.md lookup
    AgentReg    *config.AgentReg // NEW: agent registry (nil if no agents configured)
    DefaultAgent *config.ResolvedAgent // NEW: resolved default agent (nil = no default)
    Stdin       io.Reader
    Stdout      io.Writer
    Stderr      io.Writer
}
```

**Per-turn instruction resolution:**

The REPL tracks two pieces of state:
- `currentAgent *config.ResolvedAgent` — the active agent (nil = no agent / default behaviour)
- `previousAgent *config.ResolvedAgent` — saved before @name one-shot, restored after

Before each `RunTurn`, the REPL resolves the instruction:

```go
func (r *replState) resolveInstruction() (string, error) {
    return instruct.LoadWithAgent(r.cfg, r.currentAgent, r.cfg.Cwd)
}
```

For a normal turn: uses `currentAgent`.
For `@name` turn: sets `currentAgent` to the named agent, resolves instruction, runs turn, restores `previousAgent`.

### @name one-shot flow

```
1. User types: @coder review this diff
2. REPL saves currentAgent → previousAgent
3. REPL sets currentAgent = AgentReg.GetResolved("coder")
4. REPL calls: instruction = resolveInstruction()
5. REPL calls: agent.RunTurn(... instruction, "review this diff" ...)
6. After turn completes: currentAgent = previousAgent (revert)
```

If the agent isn't found: hard error, turn doesn't execute, no state change.

### /agent switch flow

```
1. User types: /agent coder
2. REPL sets currentAgent = AgentReg.GetResolved("coder")
3. REPL announces: "Switched to coder (model: big-pickle)"
4. Session log entry recorded (see genie-i1f for format)
5. All subsequent turns use the new agent until switched again
```

### main.go wiring

```go
// After config.Load() and instruct.Load():
agentReg := config.NewAgentReg(agentGlobalDir, agentLocalDir)
var defaultAgent *config.ResolvedAgent
if cfg.DefaultAgent != "" {
    da, err := agentReg.GetResolved(cfg.DefaultAgent, cfg)
    if err != nil {
        fmt.Fprintf(stderr, "Error: default agent %q: %v\n", cfg.DefaultAgent, err)
        return 1
    }
    defaultAgent = da
}

// If there's a default agent, resolve instruction with it:
instruction, err := instruct.LoadWithAgent(cfg, defaultAgent, cwd)

// Pass to REPL:
replCfg := &repl.Config{
    // ...
    Cwd:          cwd,
    AgentReg:     agentReg,
    DefaultAgent: defaultAgent,
}
```

For non-interactive mode (`-a` flag, later ticket): same pattern — resolve agent, call `LoadWithAgent`, pass to `RunTurn`.

## Files to modify

| File | Action | Purpose |
|------|--------|---------|
| `internal/instruct/instruct.go` | **Modify** | Add `LoadWithAgent`, refactor `Load` to delegate |
| `internal/instruct/instruct_test.go` | **Modify** | Add tests for `LoadWithAgent` (agent nil, agent with file, inherit_agents_md false) |
| `internal/repl/repl.go` | **Modify** | Add AgentReg/Cwd/DefaultAgent to Config, per-turn instruction resolution |
| `cmd/genie/main.go` | **Modify** | Wire AgentReg, resolve default agent, pass to REPL |

## Edge cases

- **Agent with empty instruction_file**: inherits config's instruction_file. Assembly is identical to no-agent behavior but AGENTS.md is still controlled by the agent's inherit_agents_md.
- **Agent with inherit_agents_md = false**: AGENTS.md is skipped entirely. The agent is isolated from project context.
- **@name to unknown agent**: hard error, no state change, turn doesn't execute.
- **/agent to unknown agent**: hard error, current agent unchanged.
- **Switching from agent to no-agent**: set currentAgent = nil. Instruction reverts to default (config instruction_file + AGENTS.md).
- **AGENTS.md missing**: silent skip (unchanged from today). Not an error.
