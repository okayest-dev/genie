# Command reference

Everything you can tell Genie to do: the command-line flags that start a session, the slash commands and `@agent` forms inside the REPL, and the keys that interrupt it.

## Command-line flags

```
genie [-v] [-d] [-a agent] [-p prompt]
```

| Flag | Meaning |
|------|---------|
| `-p <prompt>` / `--prompt <prompt>` | Run one prompt to completion, print the reply to stdout, and exit. With `-p` and *no* value (or piped stdin), the prompt is read from stdin instead. |
| `-a <agent>` | Load a named agent definition for this run (see [agent definitions](agent-definitions.md)). |
| `-v` | Verbose: high-level flow to stderr. |
| `-d` | Debug: low-level detail to stderr (implies `-v`). |

`GENIE_DEBUG=1` (or `true`/`yes`) with no flag is the same as `-d`.

No `-p` means an interactive REPL. There is deliberately no `--config`, `-m/--model`, or `--yes` flag: config lives in one place, model comes from config, and headless confirms are always auto-denied.

### `-p` non-interactive mode

- Runs a full agent loop (tools included), prints only the answer to **stdout**; tool framing and session id go to **stderr**.
- Confirmation prompts (overwriting a file, running a `bash` command) are **auto-denied** — no `--yes`.
- Persists a one-turn session (transcript + change ledger) in the session directory.
- Reads stdin when `-p` has no value; an empty prompt either way is a usage error.

Exit codes:

| Code | Meaning |
|------|---------|
| `0` | success |
| `1` | the run failed (config, load, or turn error) |
| `2` | interrupted (Ctrl+C / SIGINT) |
| `3` | usage error (bad flags, unknown agent) |

## REPL slash commands

At the `genie>` prompt, lines starting with `/` are slash commands.

| Command | What it does |
|---------|--------------|
| `/help` | Show the command surface, including the flat *Plugin commands* section |
| `/quit`, `/exit` | Leave the REPL |
| `/new` | Start a fresh session (with its own transcript and ledger) |
| `/changes` | List the current session's change batches (id, time, line delta, touched files) |
| `/changes <id>` | Show a batch's stored unified diffs |
| `/model` | List the provider's model catalog (`*` marks the current model) |
| `/model <id>` | Switch the session's model; unknown ids leave the model untouched |
| `/agent` | List available agent definitions with their model and tool set |
| `/agent <name>` | Switch to a named agent |
| `@<name> <prompt>` | Run one turn with a named agent, then revert (one-shot switch) |

### One-shot agents: `@name`

```
genie> @orchestrator draft the release notes
```

Runs that prompt through the `orchestrator` agent definition for a single turn, then returns you to the previous agent. The agent's model, instruction, and tool set all apply for that turn.

### Plugin commands

Plugins can register commands, addressed as `/<plugin> <command>` with the plugin's name as the namespace:

```
genie> /copilot auth login
Open https://github.com/login/device and enter code ABCD-1234
```

Everything after the command token is passed to the plugin verbatim as a raw argument string — the plugin owns sub-command parsing (`/copilot auth login --host tenant.ghe.com`). A bare `/<plugin>` shows the plugin's curated help (or its flat command list). `/help` lists all plugin commands. See [installing and using plugins](plugins/using.md#using-plugin-commands-in-the-repl).

### Keyboard

| Key | At the idle prompt | Mid-turn |
|-----|--------------------|----------|
| Ctrl+C | exits the REPL | cancels the running turn, drops the partial turn, returns to the prompt |

During a confirmation prompt, Ctrl+C declines the confirm.