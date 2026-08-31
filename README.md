# Genie

A minimal, std-lib-first Go terminal agent harness. A REPL that runs an agentic loop against an OpenAI-compatible provider.

## Install

Requires Go 1.24+.

```
go install github.com/okayest-dev/genie/cmd/genie@latest
```

Or build from source:

```
git clone https://github.com/okayest-dev/genie && cd genie
make build
```

## Quick start

1. Set your API key:

```
export OPENCODE_API_KEY="sk-..."
```

2. Run:

```
genie
```

You get an interactive `genie>` prompt. Type naturally — the model can read, write, edit files, and run shell commands.

## Usage

### Interactive REPL

```
genie
```

Starts an interactive session at the `genie>` prompt. Each input runs a full agent loop — the model produces text and/or tool calls, the harness executes them, and results are fed back until the model stops calling tools.

### Non-interactive mode

```
genie -p "explain this project"
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

Config lives at `~/.config/genie/config.toml` (XDG-aware). The file is optional — everything has sensible defaults.

Precedence: **defaults < config file < environment variables**.

### Config file

```toml
model = "big-pickle"
base_url = "https://opencode.ai/zen/v1"
api_key_env = "OPENCODE_API_KEY"
# wire = "openai"            # auto-detect from model prefix
# provider = "copilot"       # route through a wire plugin (e.g. copilot, bedrock)
# instruction_file = ""      # path to agent instruction file
# session_dir = ""           # defaults to ~/.config/genie/sessions
bash_timeout = 120

[tools]
read = true
write = true
edit = true
bash = true

[plugins]
# dir = "~/.config/genie/plugins"
# enable = ["my-plugin"]
# disable = ["broken-plugin"]

[context]
# turns = 0   # prior turns of history carried into each new turn; 0 = all
```

### Environment variables

| Variable | Description |
|----------|-------------|
| `GENIE_MODEL` | Model ID |
| `GENIE_BASE_URL` | Provider base URL |
| `GENIE_API_KEY_ENV` | Name of env var holding the API key |
| `GENIE_WIRE` | Wire protocol override |
| `GENIE_PROVIDER` | Route through a wire plugin by name |
| `GENIE_GATEWAY` | Gateway URL override |
| `GENIE_INSTRUCTION_FILE` | Path to agent instruction file |
| `GENIE_SESSION_DIR` | Session storage directory |
| `GENIE_BASH_TIMEOUT` | Bash command timeout (seconds) |
| `GENIE_PLUGIN_DIR` | Plugin discovery directory |
| `GENIE_CONTEXT_TURNS` | Prior turns of history carried into each new turn (`0` = all) |
| `GENIE_DEBUG` | Enable debug mode (`true`/`1`/`yes`) |

### Debug and verbose modes

```
genie -v          # verbose: high-level flow to stderr
genie -d          # debug: low-level detail (implies -v)
GENIE_DEBUG=1 genie  # same as -d, via env var
```

Verbose shows config resolution, instruction assembly, turn lifecycle, and token usage. Debug adds HTTP requests, SSE chunks, and full config values.

## Wire protocols

Genie auto-detects the wire protocol from the model ID prefix:

| Prefix | Wire |
|--------|------|
| `claude-*` | Anthropic messages |
| `gpt-*` | OpenAI Responses API |
| `gemini-*` | Google generateContent |
| everything else | OpenAI chat/completions |

Override with `wire = "openai"` (or `anthropic`, `responses`, `google`) in config or `GENIE_WIRE` env var.

If a model doesn't support tool calling, the harness retries without tools — letting free/non-tool models still work.

## Plugins

Genie supports plugins via NDJSON-RPC 2.0 over stdio. Drop an executable into `~/.config/genie/plugins/` and it's loaded automatically.

Wire plugins speak the genie wire plugin protocol (version 1; the schema in `protocol/schema.yaml` is the single source of truth for the generated `wireplugin` package and the `plugins/shared` helpers). A wire plugin reports the models it exposes via `wire/list_models`; each `ModelDef` may carry an optional `context_window` (tokens) so the harness can budget the conversation without guessing.

### Plugin types

- **Tool plugins** — add new tools to the harness
- **Wire plugins** — add new provider backends (e.g. AWS Bedrock, GitHub Copilot)

### Plugin layout

Plugins can be laid out in two ways:

**Directory layout (recommended):**
```
~/.config/genie/plugins/
  copilot/
    manifest.toml
    config.toml     # optional, plugin-specific
    copilot         # binary
```

**Flat layout (backward compatible):**
```
~/.config/genie/plugins/
  copilot           # binary
  copilot.toml      # manifest
```

### Plugin manifest (optional)

A TOML file describing the plugin. In directory layout, place it inside the plugin directory as `manifest.toml`. In flat layout, place it next to the executable as `<name>.toml`:

```toml
name = "my-plugin"
version = "1.0.0"
capabilities = ["tools", "wires"]
```

### Included plugins

- **bedrock** — AWS Bedrock wire (SigV4 signing, ConverseStream API)
- **copilot** — GitHub Copilot wire (OAuth token exchange, OpenAI-compatible API)

### Copilot config (GHE support)

For GitHub Enterprise, create `~/.config/genie/plugins/copilot/config.toml`:

```toml
domain = "github.example.com"
```

The plugin will use `https://api.github.example.com/copilot_internal/v2/token` for token exchange and read the matching host key from `~/.config/github-copilot/hosts.json`. Without this file, the plugin defaults to `github.com`.

### Plugin enable/disable

```toml
[plugins]
dir = "~/.config/genie/plugins"
enable = ["bedrock"]    # explicit allowlist (empty = all)
disable = ["broken"]    # denylist (takes precedence)
```

Max 16 plugins loaded concurrently. Plugins that crash or hang are automatically marked inactive.

## Agent instructions

Genie reads `AGENTS.md` from the working directory (if present) and sends it as the agent instruction on every turn. Set `instruction_file` in config or `GENIE_INSTRUCTION_FILE` env var to use a different file.

## Session persistence

Sessions are saved as JSONL in `~/.config/genie/sessions/`. Each session carries a change ledger — a record of every file change made during that session, grouped into change batches with unified diffs. Use `/new` to start a fresh session.

## License

GPL v3 — see [LICENSE](LICENSE).
