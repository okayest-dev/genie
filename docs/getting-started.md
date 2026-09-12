# Getting started

A 10-minute tour that takes you from first run to a session customised to your taste. Everything here is expanded on in the rest of the docs — [configuration](configuration.md), [commands](commands.md), [plugins](plugins/README.md), [agents](agent-definitions.md), [context management](context-management.md).

## Prerequisites

- **Go 1.24+** (only to install or build; the binary is a single static executable).
- **An API key** for a model provider. Out of the box Genie talks to OpenCode Zen (the default `base_url`), whose key lives in `OPENCODE_API_KEY`. Point Genie at any OpenAI-compatible endpoint by changing `base_url` and the key env var — see [configuration](configuration.md).

## Install

```
go install github.com/okayest-dev/genie/cmd/genie@latest
```

or build from source:

```
git clone https://github.com/okayest-dev/genie && cd genie
make build
```

## First run

Set your key and start the REPL:

```
export OPENCODE_API_KEY="sk-..."
genie
```

You'll see a short banner and the `genie>` prompt:

```
session: 20260101-101530-a1b2c3d4
genie>
```

Now just type. Genie runs a full agent loop per line — the model can read, write, and edit files, and run shell commands, streaming its reply as it goes.

Try something that touches your machine, from a directory you don't mind being explored:

```
genie> list the files in this directory and summarize what this project is about
```

The `read` and `bash` tools run in your working directory. Watch the tool frames (`── read … ──`, `── bash … ──`) as the agent works.

## Things to try immediately

| Step | What you'll learn |
|------|-------------------|
| Press **Ctrl+C** mid-turn | A running turn cancels and returns to the prompt; nothing partial is left in the conversation |
| `genie -p "explain this repo in one paragraph"` | One-shot, scriptable mode — answer to stdout, exit code 0 |
| `genie -v` | Verbose flow to stderr: config resolution, instruction assembly, token usage |
| `/model` | The provider's model catalog; `/model <id>` switches mid-session |
| `/help` | The full command surface, including plugin commands when you have plugins |

## Project instructions: `AGENTS.md`

Genie reads `AGENTS.md` from the working directory, if present, and sends it to the model as part of the agent instruction on **every turn**. That's the canonical place for project rules — what to build, what not to touch, how to test.

```markdown
# My project
- This is a Go project; run `go test ./...` before you finish.
- Never edit files under vendor/.
- Use conventional commits.
```

Point Genie at a different (or an extra) instruction file with `instruction_file` in config, and see [agent definitions](agent-definitions.md) for per-agent instructions.

## Sessions and the change ledger

Everything you do lands in a **session** — a resumable JSONL transcript in `~/.config/genie/sessions/`. Two commands keep you oriented:

- `/new` — start a fresh session when you're changing topic.
- `/changes` — an audit of every file change the agent made, in batches. `/changes <id>` shows the stored unified diff for a batch.

Because sessions are just JSONL files, a session survives quitting: the story of what the agent did (and the diffs) is never lost.

## Context management, in one paragraph

Genie tracks the model's context window, budgets your conversation against it (default: 75% of the window), and keeps the request lean in two ways: it condenses oversized earlier tool output (opt-in) and, when you cross the budget, **compacts** the oldest turns into a summary persisted right in the transcript. You don't need to do anything — it works out of the box — but everything is configurable under `[context]`. [The full model](context-management.md) is worth a read once you're past the basics.

## Make it yours

The three big customization levers:

1. **[Configuration](configuration.md)** — one TOML file, every knob with an env var and a default. Change the model, disable a tool, point at a different provider, keep less history.
2. **[Plugins](plugins/using.md)** — drop an executable in `~/.config/genie/plugins/` to add tools, providers, REPL commands, or hooks. The bundled provider plugins (Bedrock, Copilot) install with a one-liner. Writing your own is a small, well-defined protocol — [the authoring guide](plugins/authoring.md) walks it end to end.
3. **[Agents](agent-definitions.md)** — package a model + instruction + tool set under a name (`orchestrator`, `feature-speccing`, …) and switch with `/agent`, `@name`, or `-a`.

A suggested starting config to see the surface:

```toml
# ~/.config/genie/config.toml
model = "big-pickle"
bash_timeout = 180                    # give long builds more time
instruction_file = "~/.config/genie/genie-rules.md"  # standing personal rules

[context]
turns = 30            # keep the last 30 turns, not the whole conversation
condense_size = 5000  # narrow tool results over 5k tokens
```

Then `genie -v` and watch your session's token usage against the window.

## Where to go next

- **[Command reference](commands.md)** — every flag, slash command, and key.
- **[Configuration reference](configuration.md)** — every field, env var, and default.
- **[Plugins](plugins/README.md)** — framework overview, then [using](plugins/using.md) or [writing](plugins/authoring.md).
- **[Context management](context-management.md)** — the layered model Genie uses to stay under budget.
- **[Agent definitions](agent-definitions.md)** — packaging model + instructions + tools.
- **[Protocol](plugins/protocol.md)** — the complete plugin wire reference.