# Configuration reference

Genie is configured by one optional TOML file plus environment variables. Everything has a sensible default — a fresh install with no config file works out of the box.

## Where config lives

The config file is `config.toml` in the genie config directory, default `~/.config/genie/config.toml` (XDG-aware). The base directory itself can be overridden with `GENIE_CONFIG_DIR`:

| How | Path |
|-----|------|
| default | `~/.config/genie/config.toml` |
| `GENIE_CONFIG_DIR=/tmp/x` | `/tmp/x/genie/config.toml` |

`GENIE_CONFIG_DIR` also moves the derived default paths for the session directory, the plugin directory, and the agent directory (see below).

A missing config file is **pure defaults**; malformed TOML and unknown keys fail fast at startup (a typo like `bas_url` is caught, not ignored).

## Precedence

```
defaults < config file < environment variables
```

An env var that is set but empty leaves the file value in place. The API key never lives in the config file — it is always read from the environment variable named by `api_key_env` (default `OPENCODE_API_KEY`).

## Top-level keys

| Key | Type | Default | Env var | Meaning |
|-----|------|---------|---------|---------|
| `model` | string | `big-pickle` | `GENIE_MODEL` | model ID for the session |
| `base_url` | string | `https://opencode.ai/zen/v1` | `GENIE_BASE_URL` | provider wire base URL |
| `api_key_env` | string | `OPENCODE_API_KEY` | `GENIE_API_KEY_ENV` | name of the env var holding the API key |
| `wire` | string | `""` (auto-detect) | `GENIE_WIRE` | wire protocol override: `openai`, `anthropic`, `responses`, `google` |
| `provider` | string | `""` | `GENIE_PROVIDER` | route all requests through a loaded wire plugin by name |
| `gateway` | string | `""` | `GENIE_GATEWAY` | URL override for the provider gateway (replaces `base_url`) |
| `instruction_file` | string | `""` (none) | `GENIE_INSTRUCTION_FILE` | extra agent-instruction file, loaded after the built-in default |
| `session_dir` | string | `~/.config/genie/sessions` | `GENIE_SESSION_DIR` | where sessions and their change ledgers are stored |
| `bash_timeout` | integer (seconds) | `120` | `GENIE_BASH_TIMEOUT` | default kill timeout for `bash` tool commands |
| `default_agent` | string | `""` (none) | `GENIE_DEFAULT_AGENT` | name of the agent definition loaded at startup |

Notes:

- `wire` empty auto-detects from the model ID prefix: `claude-*` → anthropic, `gpt-*` → responses, `gemini-*` → google, anything else → openai. An explicit `wire` beats detection, and an invalid value is a startup error.
- `provider` is meaningful only when a plugin with that name is loaded and supports wires; it routes the whole session through it.
- `gateway` is applied by overriding `base_url`; it exists for provider gateways that front multiple endpoints.
- `instruction_file` errors at startup if the file is missing.

```toml
model = "big-pickle"
base_url = "https://opencode.ai/zen/v1"
api_key_env = "OPENCODE_API_KEY"
wire = "openai"                  # openai | anthropic | responses | google
# provider = "copilot"
# gateway = "https://gateway.example.com"
# instruction_file = "~/.config/genie/instructions.md"
# session_dir = "~/.config/genie/sessions"
bash_timeout = 120
# default_agent = "orchestrator"
```

## `[tools]` — per-tool toggles

| Key | Type | Default | Env var | Meaning |
|-----|------|---------|---------|---------|
| `tools.read` | boolean | `true` | — | enable the `read` tool |
| `tools.write` | boolean | `true` | — | enable the `write` tool |
| `tools.edit` | boolean | `true` | — | enable the `edit` tool |
| `tools.bash` | boolean | `true` | — | enable the `bash` tool |

A disabled tool is omitted from the tools array sent to the provider; a stale call to it returns `Error: tool 'bash' is disabled`. There is **no env var** for these — the config file is the only switch.

```toml
[tools]
read = true
write = true
edit = true
bash = true
```

## `[plugins]` — plugin discovery

| Key | Type | Default | Env var | Meaning |
|-----|------|---------|---------|---------|
| `plugins.dir` | string | `~/.config/genie/plugins` | `GENIE_PLUGIN_DIR` | directory where plugin executables are discovered |
| `plugins.enable` | array of strings | `[]` (all) | — | allowlist of plugin names to load |
| `plugins.disable` | array of strings | `[]` | — | denylist of plugin names to skip (wins over `enable`) |
| `plugins.wire_stream_timeout` | integer (seconds) | `600` | `GENIE_PLUGIN_WIRE_STREAM_TIMEOUT` | per-call timeout for a wire plugin's `wire/stream` completion RPC |

See [installing and using plugins](plugins/using.md) for the plugin layouts and discovery rules.

`wire_stream_timeout` exists because LLM completions routinely exceed the 5s request timeout used for quick RPCs (tool calls, pings, context hooks). When a stream call does time out, genie waits a short grace period for the late completion to arrive so the codec resyncs — the plugin is only marked inactive if it never answers.

```toml
[plugins]
dir = "~/.config/genie/plugins"
enable = ["bedrock"]        # explicit allowlist (empty = all)
disable = ["broken-plugin"] # denylist (takes precedence)
# wire_stream_timeout = 600 # seconds; per-call timeout for wire/stream RPCs
```

## `[context]` — context management

| Key | Type | Default | Env var | Meaning |
|-----|------|---------|---------|---------|
| `context.turns` | integer | `0` (all) | `GENIE_CONTEXT_TURNS` | prior turns of history injected into each new turn; `0` = unlimited |
| `context.budget_tokens` | integer (tokens) | `0` (unset) | `GENIE_CONTEXT_BUDGET_TOKENS` | absolute context budget; overrides the percentage below |
| `context.budget_percent` | float | `75` | `GENIE_CONTEXT_BUDGET_PERCENT` | budget as a percentage of the model's context window |
| `context.windows` | map[string]int | `{}` | — | per-model context window overrides, in tokens |
| `context.condense_size` | integer (tokens) | `0` (disabled) | `GENIE_CONTEXT_CONDENSE_SIZE` | threshold above which a prior-turn tool result is condensed |
| `context.net_drop` | boolean | `false` | `GENIE_CONTEXT_NET_DROP` | drop oversized prior-turn tool results entirely (calls stay visible) |
| `context.plugins.order` | array of strings | discovery order | — | chain order for context hooks |
| `context.plugins.active_compact` | string | `builtin` | — | single-active compact implementation (plugin name) |
| `context.plugins.active_condense` | string | `builtin` | — | single-active condense implementation (plugin name) |

See [context management](context-management.md) for how these work. Validation:

- `budget_percent` must land in `(0, 100]`.
- `turns`, `budget_tokens`, `condense_size`, and every `windows` entry must be non-negative; `budget_tokens` and windows must be positive when set.
- Single-active conflict: two plugins claiming the same `active_compact`/`active_condense` gap without an explicit choice is a startup error.

```toml
[context]
turns = 0            # 0 = keep the whole conversation
# budget_tokens = 50000
budget_percent = 75
# windows = { "big-pickle" = 200000 }
condense_size = 0    # 0 = condensation off
net_drop = false

[context.plugins]
# order = ["my-ctx-hook"]
# active_compact = "my-compactor"     # default "builtin"
# active_condense = "my-condenser"    # default "builtin"
```

## `[lifecycle.plugins]` — lifecycle hooks order

| Key | Type | Default | Env var | Meaning |
|-----|------|---------|---------|---------|
| `lifecycle.plugins.order` | array of strings | discovery order | — | ordered chain for lifecycle events |

Request-side events fire in this order; response-side events fire reversed (onion), so paired plugins pack and unpack. Anything not listed is appended in discovery order. See [writing plugins](plugins/authoring.md#lifecycle-plugins).

```toml
[lifecycle.plugins]
order = ["guardrails", "logging"]
```

## Agent definitions

Agents are not keys in `config.toml` — they are separate TOML files discovered from the agent directories. See [agent definitions](agent-definitions.md) for the schema. `default_agent` above selects which one loads at startup.

## Environment variables

The complete set of knobs that can be set from the environment:

| Variable | Setter for | Values / notes |
|----------|-----------|----------------|
| `OPENCODE_API_KEY` | the API key itself (default key holder named by `api_key_env`) | `sk-...` |
| `GENIE_MODEL` | `model` | model ID |
| `GENIE_BASE_URL` | `base_url` | provider wire base URL |
| `GENIE_API_KEY_ENV` | `api_key_env` | name of another env var |
| `GENIE_WIRE` | `wire` | `openai` \| `anthropic` \| `responses` \| `google` |
| `GENIE_PROVIDER` | `provider` | a loaded wire plugin's name |
| `GENIE_GATEWAY` | `gateway` | gateway URL |
| `GENIE_INSTRUCTION_FILE` | `instruction_file` | path to an instruction file |
| `GENIE_SESSION_DIR` | `session_dir` | session storage directory |
| `GENIE_BASH_TIMEOUT` | `bash_timeout` | seconds, positive integer |
| `GENIE_PLUGIN_DIR` | `plugins.dir` | plugin discovery directory |
| `GENIE_PLUGIN_WIRE_STREAM_TIMEOUT` | `plugins.wire_stream_timeout` | seconds, positive integer |
| `GENIE_DEFAULT_AGENT` | `default_agent` | agent name |
| `GENIE_CONTEXT_TURNS` | `context.turns` | non-negative integer |
| `GENIE_CONTEXT_BUDGET_TOKENS` | `context.budget_tokens` | positive integer |
| `GENIE_CONTEXT_BUDGET_PERCENT` | `context.budget_percent` | number in `(0, 100]` |
| `GENIE_CONTEXT_CONDENSE_SIZE` | `context.condense_size` | non-negative integer |
| `GENIE_CONTEXT_NET_DROP` | `context.net_drop` | boolean |
| `GENIE_DEBUG` | debug mode, same as `-d` | `true` / `1` / `yes` |
| `GENIE_CONFIG_DIR` | base dir for `config.toml` | directory path |

There is **no env var** for `[tools]`, `plugins.enable`/`plugins.disable`, `context.windows`, `context.plugins.*`, or `lifecycle.plugins.*` — those exist only in the config file.

See [commands](commands.md) for `-v`/`-d`/`-p`/`-a` and exit codes.

## Debug and verbose output

```
genie -v          # verbose: high-level flow to stderr
genie -d          # debug: low-level detail to stderr (implies -v)
GENIE_DEBUG=1     # same as -d
```

Verbose shows config resolution, instruction assembly, turn lifecycle, and token usage. Debug adds HTTP requests, SSE chunks, context usage (`tokens`, `window`, `budget`), and full config values.