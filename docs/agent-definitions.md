# Agent definitions

Agents let you package a model, an instruction, and a tool set under a name, and switch the harness between them — per-session via `/agent`, per-turn via `@name`, or per-run via `-a`. A Genie session doesn't have to use agents at all; they're an optional way to specialise what the harness runs.

An agent definition is a small TOML file. Every field is optional; a field you leave out **inherits from the harness config** (`model`, `instruction_file`, and the enabled tool set).

## Where agents live

Two directories are scanned; a local definition with the same name overrides the global one:

| Directory | Scope | Used for |
|-----------|-------|----------|
| `~/.config/genie/agents/*.toml` | all sessions, this machine | your standing agents |
| `.genie/agents/*.toml` | the working directory | project- or checkout-specific agents |

The agent's **name is the filename stem** — `feature-speccing.toml` defines the agent `feature-speccing`. Dotfiles and non-`.toml` files are skipped.

## The schema

```toml
# ~/.config/genie/agents/orchestrator.toml
model = "big-pickle"             # empty → inherit config model
instruction_file = "orchestrator.md"  # empty → inherit config instruction_file
tools = ["read", "write", "edit"]     # unset → inherit all enabled; set → exact set
inherit_agents_md = true              # default true
skills = ["tdd", "research"]          # unset → inherit all; set → exact set (empty = none)
```

| Key | Empty / unset | Set |
|-----|---------------|-----|
| `model` | inherit the config's `model` | run the loop with this model |
| `instruction_file` | inherit the config's `instruction_file` | use this instruction file instead |
| `tools` | inherit *all* enabled tools | the agent gets exactly this tool set (must be a subset of what's registered — validated at selection) |
| `inherit_agents_md` | `true` | `false` drops the cwd `AGENTS.md` from the agent's instruction |
| `skills` | inherit *all* discovered skills | the agent binds exactly these skills (`[]` = none) — names are validated against the discovered pool, so a typo errors at resolution |

An agent with a `tools` list names tools you may have disabled in config, or plugin tools — selection validates them against the registered registry, so a typo surfaces immediately rather than silently narrowing the agent.

## Selecting an agent

| Way | What happens |
|-----|--------------|
| config `default_agent = "orchestrator"` | loaded at startup |
| `genie -a orchestrator` | load for this run (beats `default_agent`) |
| `/agent` | list definitions (current agent marked `*`) |
| `/agent <name>` | switch for the rest of the session |
| `@<name> <prompt>` | one turn with that agent, then revert (see [commands](commands.md#one-shot-agents-name)) |

`/agent` shows each definition's name, resolved model, and (when constrained) its tool set. The startup/model for an agent works the same across REPL and `-p`: `-a orchestrator` in non-interactive mode run everything through that agent.

## What differs from the harness

Agents change four things per turn: the **model**, the **instruction**, the **tool set**, and the **skill bindings**. Everything else — sessions, the change ledger, context management, plugins — is harness-level and shared. Switching agents inside a session keeps the same session transcript, so the conversation history is continuous across a switch.