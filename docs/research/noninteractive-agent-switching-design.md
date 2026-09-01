# Design: Non-Interactive Agent Switching — -a Flag

**Ticket**: genie-e0d
**Status**: Resolution

---

## Problem

The `-a` flag lets users specify a named agent from the command line, either for a one-shot prompt (`genie -a coder -p "review this"`) or to pre-load an agent into interactive mode (`genie -a coder`). The agent definition provides model, instruction, and tool set — overriding config defaults for the duration of the run.

## Design

### Behaviour

| Command | Behaviour |
|---------|-----------|
| `genie -a coder -p "prompt"` | One-shot with agent "coder". Uses agent's model, instruction, tools. |
| `genie -a coder -p` (no value) | Reads prompt from stdin, runs with agent "coder". |
| `genie -a coder` | Interactive REPL with agent "coder" pre-loaded as default. |
| `genie -a nonexistent -p "prompt"` | Hard error, exit code 3. |
| `genie -a coder -p "prompt" @other "do stuff"` | Error: `-a` and `@name` are both agent selectors; `-a` wins for the session, `@name` is for REPL-only. Reject the combination. |

### Precedence

The `-a` flag is the **most specific** agent source. Precedence for agent selection:

1. `-a <name>` command-line flag (highest)
2. `default_agent` in config.toml / `GENIE_DEFAULT_AGENT` env
3. No agent (current behaviour)

When `-a` is used, it replaces any `default_agent` from config for this run.

### Flag parsing

The `-a` flag is a string flag, parsed alongside `-p`, `-v`, `-d`:

```go
agentFlag := fs.String("a", "", "agent definition to load for this run")
```

`-a` requires a value. `genie -a` with no agent name prints usage and exits with code 3 (same as `-p` with no value). `genie -a` at end of args → error.

### Wiring in main.go

The `-a` flag is resolved **after config.Load()** but **before instruction assembly and wire detection**. This is because the agent can override the model, which determines the wire protocol.

Revised startup order:

```
1. Parse flags (including -a)
2. config.Load()
3. Resolve agent from -a flag (or default_agent, or nil)
4. Apply agent overrides to config (model, instruction, tools)
5. Load instruction (using agent-resolved values)
6. Detect wire (from agent-resolved model)
7. Build client
8. Build registry (global), then Subset for agent tools
9. Load plugins
10. Branch: -p → single prompt; else → REPL
```

### Agent resolution

```go
// Resolve the agent for this run.
var runAgent *config.ResolvedAgent
agentName := *agentFlag // from -a flag
if agentName == "" && cfg.DefaultAgent != "" {
    agentName = cfg.DefaultAgent
}

if agentName != "" {
    agentReg := config.NewAgentReg(agentGlobalDir, agentLocalDir)
    runAgent, err = agentReg.GetResolved(agentName, cfg)
    if err != nil {
        fmt.Fprintf(stderr, "Error: agent %q: %v\n", agentName, err)
        return 3
    }
    // Validate tools against registry
    if err := registry.ValidateTools(runAgent.Tools); err != nil {
        fmt.Fprintf(stderr, "Error: agent %q: %v\n", agentName, err)
        return 3
    }
}
```

### Applying agent overrides

When an agent is resolved, its fields override config defaults:

```go
// Model: agent wins
model := cfg.Model
if runAgent != nil && runAgent.Model != "" {
    model = runAgent.Model
}

// Instruction: assembled with agent context
instruction, err := instruct.LoadWithAgent(cfg, runAgent, cwd)

// Registry: Subset for agent tools
runRegistry := registry
if runAgent != nil && runAgent.Tools != nil {
    runRegistry = registry.Subset(runAgent.Tools)
}
```

### Single-prompt path (-p)

```go
if *prompt != "" {
    sess, _ := session.New(cfg.SessionDir)
    ldg := ledger.New(cfg.SessionDir, sess.ID)

    var opts []agent.Option
    if runAgent != nil {
        opts = append(opts, agent.WithAgentName(runAgent.Name))
    }

    err = agent.RunTurn(ctx, client, model, instruction, *prompt,
        stdout, stderr, sess, runRegistry, ldg, cwd, opts...)
    // ... close ledger, handle error ...
}
```

The agent name is logged via `WithAgentName` — same mechanism as the REPL.

### Interactive REPL path

```go
replCfg := &repl.Config{
    Client:       client,
    Model:        model,            // agent-resolved model
    Instruction:  instruction,      // agent-resolved instruction
    SessionDir:   cfg.SessionDir,
    Registry:     runRegistry,      // agent-resolved registry
    Cwd:          cwd,
    AgentReg:     agentReg,
    DefaultAgent: runAgent,         // pre-loads agent into REPL
    BashTimeout:  cfg.BashTimeout,
    Stdin:        os.Stdin,
    Stdout:       stdout,
    Stderr:       stderr,
}
```

The `DefaultAgent` is passed to the REPL. The REPL's `replState.currentAgent` starts set to this agent. All turns use it until the user switches via `/agent` or `@name`.

### @name interaction with -a

When `-a` pre-loads an agent and the user types `@other prompt`:

- `@other` one-shot switches to `other`
- After the turn, reverts to the `-a` agent (not to no-agent)
- This is correct: `-a` defines the session's baseline agent

The `previousAgent` in `replState` is set to the `-a` agent, so revert goes back to it.

### Flag rejection: -a with @name on same line

`genie -a coder -p "@other do stuff"` — the `@other` is in the prompt text, not parsed as a REPL command. This is fine — it's literal text in a one-shot prompt. Only the REPL parses `@name` at line start.

No special rejection needed. The `-a` flag sets the agent for the run; `@other` in the prompt text is just text.

### Usage string update

```
usage: genie [-v] [-d] [-a agent] [-p prompt]

genie is a minimal terminal agent harness.

Flags:
  -a agent   load a named agent definition for this run
  -p prompt  run a single prompt, print the reply to stdout, and exit
  -v         verbose output: high-level flow to stderr
  -d         debug output: low-level detail to stderr (implies -v)

Environment:
  GENIE_DEBUG    enable debug mode (true/1/yes)

Without -p, genie starts an interactive REPL.
```

### Error cases

| Case | Behaviour |
|------|-----------|
| `genie -a nonexistent -p "prompt"` | `"Error: agent "nonexistent": not found"` → exit 3 |
| `genie -a coder -p "prompt"` (tool unavailable) | `"Error: agent "coder": tool(s) not available: write"` → exit 3 |
| `genie -a` (no value) | Prints usage → exit 3 |
| `genie -a coder` (agent dir doesn't exist) | Silent — no agents found, `"Error: agent "coder": not found"` → exit 3 |
| Config has `default_agent = "coder"` + `-a reviewer` | `-a reviewer` wins. Agent "reviewer" is loaded. |

### Files to modify

| File | Action | Purpose |
|------|--------|---------|
| `cmd/genie/main.go` | **Modify** | Add `-a` flag, agent resolution, apply overrides, update usage |
| `cmd/genie/main_test.go` | **Modify** | Test -a flag parsing, agent resolution, error cases |

### Interaction with /model

`-a` sets the model for the run. `/model` in the REPL changes `cfg.Model` (the fallback). When an agent is active, its model wins. When the user switches away from the agent (via `/agent` with no args to deactivate), the model reverts to `cfg.Model` — which may have been changed by `/model`.

Hierarchy: agent model > `/model`-set model > config model.
