# og

A minimal, std-lib-first Go terminal agent harness. A REPL that runs an agentic loop against an OpenAI-compatible provider.

## Install

Requires Go 1.24+.

```
go install github.com/okayest-dev/og/cmd/og@latest
```

Or build from source:

```
git clone https://github.com/okayest-dev/og && cd og
make build
```

## Quick start

1. Set your API key:

```
export OPENCODE_API_KEY="sk-..."
```

2. Run:

```
og
```

You get an interactive `og>` prompt. Type naturally — the model can read, write, edit files, and run shell commands.

## Usage

### Interactive REPL

```
og
```

Starts an interactive session at the `og>` prompt. Each input runs a full agent loop — the model produces text and/or tool calls, the harness executes them, and results are fed back until the model stops calling tools.

### Non-interactive mode

```
og -p "explain this project"
```

Runs a single prompt, prints the reply to stdout, and exits. Tool calls requiring confirmation are auto-denied in this mode.

### Slash commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/quit`, `/exit` | Exit the REPL |
| `/new` | Start a new session |
| `/model` | List available models |
| `/model <id>` | Switch to a different model |

Ctrl+C cancels a running turn. Ctrl+C at the prompt exits.

## Tools

The model has access to four tools:

| Tool | Description |
|------|-------------|
| **read** | Read file contents or list directories. Supports offset/limit pagination. Rejects binary files. |
| **write** | Create or overwrite files. Overwrites require confirmation. Auto-creates parent directories. |
| **edit** | Surgical find-and-replace. Exact, whitespace-sensitive matching. One pair at a time. |
| **bash** | Run shell commands via `sh -c`. Requires confirmation. 120s timeout (configurable). |

Tools can be individually disabled in config.

## Configuration

Config lives at `~/.config/og/config.toml` (XDG-aware). The file is optional — everything has sensible defaults.

Precedence: **defaults < config file < environment variables**.

### Config file

```toml
model = "big-pickle"
base_url = "https://opencode.ai/zen/v1"
api_key_env = "OPENCODE_API_KEY"
# wire = "openai"            # auto-detect from model prefix
# instruction_file = ""      # path to agent instruction file
# session_dir = ""           # defaults to ~/.config/og/sessions
bash_timeout = 120

[tools]
read = true
write = true
edit = true
bash = true

[plugins]
# dir = "~/.config/og/plugins"
# enable = ["my-plugin"]
# disable = ["broken-plugin"]
```

### Environment variables

| Variable | Description |
|----------|-------------|
| `OG_MODEL` | Model ID |
| `OG_BASE_URL` | Provider base URL |
| `OG_API_KEY_ENV` | Name of env var holding the API key |
| `OG_WIRE` | Wire protocol override |
| `OG_GATEWAY` | Gateway URL override |
| `OG_INSTRUCTION_FILE` | Path to agent instruction file |
| `OG_SESSION_DIR` | Session storage directory |
| `OG_BASH_TIMEOUT` | Bash command timeout (seconds) |
| `OG_PLUGIN_DIR` | Plugin discovery directory |
| `OG_DEBUG` | Enable debug mode (`true`/`1`/`yes`) |

### Debug and verbose modes

```
og -v          # verbose: high-level flow to stderr
og -d          # debug: low-level detail (implies -v)
OG_DEBUG=1 og  # same as -d, via env var
```

Verbose shows config resolution, instruction assembly, turn lifecycle, and token usage. Debug adds HTTP requests, SSE chunks, and full config values.

## Wire protocols

Og auto-detects the wire protocol from the model ID prefix:

| Prefix | Wire |
|--------|------|
| `claude-*` | Anthropic messages |
| `gpt-*` | OpenAI Responses API |
| `gemini-*` | Google generateContent |
| everything else | OpenAI chat/completions |

Override with `wire = "openai"` (or `anthropic`, `responses`, `google`) in config or `OG_WIRE` env var.

If a model doesn't support tool calling, the harness retries without tools — letting free/non-tool models still work.

## Plugins

Og supports plugins via NDJSON-RPC 2.0 over stdio. Drop an executable into `~/.config/og/plugins/` and it's loaded automatically.

### Plugin types

- **Tool plugins** — add new tools to the harness
- **Wire plugins** — add new provider backends (e.g. AWS Bedrock, GitHub Copilot)

### Plugin manifest (optional)

A TOML file next to the executable:

```toml
name = "my-plugin"
version = "1.0.0"
capabilities = ["tools", "wires"]
```

### Included plugins

- **bedrock** — AWS Bedrock wire (SigV4 signing, ConverseStream API)
- **copilot** — GitHub Copilot wire (OAuth token exchange, OpenAI-compatible API)

### Plugin config

```toml
[plugins]
dir = "~/.config/og/plugins"
enable = ["bedrock"]    # explicit allowlist (empty = all)
disable = ["broken"]    # denylist (takes precedence)
```

Max 16 plugins loaded concurrently. Plugins that crash or hang are automatically marked inactive.

## Agent instructions

Og reads `AGENTS.md` from the working directory (if present) and sends it as the agent instruction on every turn. Set `instruction_file` in config or `OG_INSTRUCTION_FILE` env var to use a different file.

## Session persistence

Sessions are saved as JSONL in `~/.config/og/sessions/`. Each session carries a change ledger — a record of every file change made during that session, grouped into change batches with unified diffs. Use `/new` to start a fresh session.

## License

GPL v3 — see [LICENSE](LICENSE).
